"""Checkout-scoped Compose snapshots. Python stdlib + Docker Compose with --no-env-resolution."""
import argparse
import copy
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile


def project_name(root, branch):
    slug = re.sub(r'[^a-z0-9-]', '-', root.name.lower())[:24].strip('-') or 'app'
    digest = hashlib.sha256(f'{root.resolve()}\0{branch}'.encode()).hexdigest()[:12]
    return f'idv-{slug}-{digest}'


def docker_env():
    # Never inherit a different project's Compose selection or implicit .env.
    env = {k: v for k, v in os.environ.items() if not k.startswith('COMPOSE_')}
    env['COMPOSE_DISABLE_ENV_FILE'] = '1'
    return env


def render(root, files, env_file):
    cmd = ['docker', 'compose', '-p', 'idv-render', '--project-directory', str(root),
           '--env-file', str(env_file or os.devnull), '--profile', '*']
    for path in files:
        cmd += ['-f', str(path)]
    result = subprocess.run(cmd + ['config', '--no-env-resolution', '--format', 'json'],
                            env=docker_env(), capture_output=True, text=True)
    if result.returncode:
        # Compose errors can include interpolated secret values.
        raise ValueError('Compose config failed; check file syntax, required variables and paths locally. Output suppressed to avoid exposing secrets.')
    return json.loads(result.stdout)


def isolate(source, root, project, env_file):
    model = copy.deepcopy(source)
    model['name'] = project
    for kind in ('volumes', 'networks', 'secrets', 'configs'):
        for name, definition in model.get(kind, {}).items():
            if definition is None:
                definition = model[kind][name] = {}
            if definition.get('external'):
                raise ValueError(f'{kind}.{name}: external resource requires a project-specific replacement')
            if definition.get('driver_opts') or definition.get('driver') not in (None, 'local', 'bridge'):
                raise ValueError(f'{kind}.{name}: custom driver/storage needs review')
            if kind == 'networks' and definition.get('ipam', {}).get('config'):
                raise ValueError(f'networks.{name}: fixed subnets must be removed for concurrent stacks')
            if kind in ('volumes', 'networks'):
                definition['name'] = f'{project}_{name}'
    for name, service in model.get('services', {}).items():
        service.setdefault('labels', {})['io.isolated-dev.root'] = str(root.resolve()).replace('$', '$$')
        for key in ('privileged', 'devices', 'volumes_from', 'external_links', 'provider', 'develop', 'post_start', 'pre_start', 'pre_stop'):
            if service.get(key):
                raise ValueError(f'{name}.{key}: cannot isolate automatically')
        for key in ('network_mode', 'pid', 'ipc'):
            if service.get(key) not in (None, 'none', 'private'):
                raise ValueError(f'{name}.{key}: shared namespace requires a project-specific replacement')
        service.pop('container_name', None)
        if service.get('build'):
            service['image'] = f'{project}-{name}:dev'
            service['build'].pop('tags', None)
        for port in service.get('ports', []):
            if not isinstance(port, dict) or not isinstance(port.get('target'), int):
                raise ValueError(f'{name}: unresolved port mapping')
            port.pop('published', None)
            port['host_ip'] = '127.0.0.1'
            port['mode'] = 'ingress'
        for entry in service.get('env_file', []):
            path = Path(entry['path'] if isinstance(entry, dict) else entry).resolve()
            if env_file is None or path != env_file.resolve():
                raise ValueError(f'{name}: env_file must reference only the explicitly supplied --env-file; override the original env_file list')
        for mount in service.get('volumes', []):
            if mount.get('type') != 'bind':
                continue
            path = Path(mount['source']).resolve()
            if path.name.endswith('.sock') or str(path) in ('/proc', '/sys', '/dev'):
                raise ValueError(f'{name}: host control mount is not isolated')
            if not path.exists():
                raise ValueError(f'{name}: bind source does not exist: {path}')
            if not mount.get('read_only') and not path.is_relative_to(root.resolve()):
                raise ValueError(f'{name}: writable bind outside checkout: {path}')
            mount.setdefault('bind', {})['create_host_path'] = False
    return model


def write_private(path, data):
    fd, temporary = tempfile.mkstemp(dir=path.parent)
    try:
        with os.fdopen(fd, 'w') as stream:
            json.dump(data, stream, indent=2)
            stream.write('\n')
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def local_docker():
    host = os.environ.get('DOCKER_HOST', '')
    if not host or os.environ.get('DOCKER_CONTEXT'):
        result = subprocess.run(['docker', 'context', 'inspect'], capture_output=True, text=True, check=True)
        host = json.loads(result.stdout)[0]['Endpoints']['docker']['Host']
    if not host.startswith(('unix://', 'npipe://')):
        raise ValueError('This skill operates on a local Docker daemon; select a local context first')


def public_status(output):
    rows = json.loads(output) if output.lstrip().startswith('[') else [json.loads(line) for line in output.splitlines() if line.strip()]
    result = []
    for row in rows:
        item = {key: row.get(key, '') for key in ('ID', 'Name', 'Service', 'State', 'Health')}
        item['Publishers'] = [p for p in (row.get('Publishers') or []) if p.get('PublishedPort')]
        result.append(item)
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', type=Path, default=Path.cwd())
    parser.add_argument('--state-dir', type=Path, default=Path.home() / '.local/state/isolated-dev')
    subs = parser.add_subparsers(dest='action', required=True)
    prepare = subs.add_parser('prepare', help='Render an explicitly selected Compose stack into an isolated snapshot')
    prepare.add_argument('-f', '--file', type=Path, action='append', required=True)
    prepare.add_argument('--env-file', type=Path)
    subs.add_parser('where', help='Print identity and state location without reading configuration')
    up = subs.add_parser('up')
    up.add_argument('--build', action='store_true')
    up.add_argument('--profile', action='append', default=[])
    up.add_argument('--service', action='append', default=[])
    up.add_argument('--wait-timeout', type=int, default=180)
    subs.add_parser('status')
    down = subs.add_parser('down')
    down.add_argument('--volumes', action='store_true', help='Delete ONLY this instance\'s volumes; destructive')
    down.add_argument('--instance', help='Stop a prepared instance from another branch of this same checkout')
    for action in ('exec', 'run'):
        child = subs.add_parser(action)
        child.add_argument('service')
        child.add_argument('command', nargs=argparse.REMAINDER)
    args = parser.parse_args()
    git = subprocess.run(['git', '-C', str(args.root), 'rev-parse', '--show-toplevel'],
                         capture_output=True, text=True)
    if git.returncode:
        raise ValueError('--root must be inside a Git checkout')
    root = Path(git.stdout.strip()).resolve()
    branch = subprocess.run(['git', '-C', str(root), 'symbolic-ref', '--short', '-q', 'HEAD'],
                            capture_output=True, text=True).stdout.strip()
    if not branch:
        branch = 'detached-' + subprocess.check_output(['git', '-C', str(root), 'rev-parse', 'HEAD'], text=True).strip()
    project = project_name(root, branch)
    state_base = args.state_dir.expanduser().resolve()
    if args.action == 'down' and args.instance:
        if not re.fullmatch(r'idv-[a-z0-9-]+', args.instance):
            raise ValueError('Invalid instance name')
        project = args.instance
    state = state_base / project
    if args.action == 'where':
        instances = []
        for path in state_base.glob('idv-*/manifest.json'):
            info = json.loads(path.read_text())
            if info.get('root') == str(root):
                instances.append({'project': info['project'], 'branch': info['branch']})
        print(json.dumps({'project': project, 'root': str(root), 'branch': branch,
                          'state': str(state), 'prepared_instances': instances}, indent=2))
        return
    state.mkdir(parents=True, exist_ok=True, mode=0o700)
    with (state / '.lock').open('a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        config = state / 'compose.json'
        manifest = state / 'manifest.json'
        if args.action == 'prepare':
            files = [p.resolve() for p in args.file]
            env_file = args.env_file.resolve() if args.env_file else None
            model = isolate(render(root, files, env_file), root, project, env_file)
            # Compose's canonical output already escapes dollars for round-tripping.
            inputs = files + ([env_file] if env_file else [])
            info = {'project': project, 'root': str(root), 'branch': branch,
                    'env_file': str(env_file) if env_file else None,
                    'inputs': {str(p): hashlib.sha256(p.read_bytes()).hexdigest() for p in inputs}}
            write_private(config, model)
            info['config_sha256'] = hashlib.sha256(config.read_bytes()).hexdigest()
            write_private(manifest, info)
            print(json.dumps({'project': project, 'state': str(state),
                              'services': list(model['services']), 'result': 'prepared; not started'}, indent=2))
            return
        if not config.is_file() or not manifest.is_file():
            raise ValueError('No prepared stack for this checkout/branch. Run prepare first.')
        info = json.loads(manifest.read_text())
        if info['root'] != str(root) or info['project'] != project or project_name(root, info['branch']) != project:
            raise ValueError('Snapshot belongs to a different checkout or branch')
        if args.action != 'down' and info['branch'] != branch:
            raise ValueError('Snapshot belongs to a different branch')
        if hashlib.sha256(config.read_bytes()).hexdigest() != info['config_sha256']:
            raise ValueError('Snapshot changed; edit the source Compose files and run prepare again')
        if args.action in ('up', 'run', 'exec'):
            isolate(json.loads(config.read_text()), root, project,
                    Path(info['env_file']) if info['env_file'] else None)
            for path, digest in info['inputs'].items():
                if not Path(path).is_file() or hashlib.sha256(Path(path).read_bytes()).hexdigest() != digest:
                    raise ValueError(f'Input changed: {path}; run prepare again')
        local_docker()
        if args.action in ('up', 'run'):
            active = subprocess.check_output([
                'docker', 'ps', '--filter', f'label=io.isolated-dev.root={root}',
                '--format', '{{.Label "com.docker.compose.project"}}'], text=True).split()
            if any(name != project for name in active):
                raise ValueError('Another branch stack still runs from this checkout. Use where, then down --instance NAME before starting this branch.')
        command = ['docker', 'compose', '-p', project, '--project-directory', str(root),
                   '--env-file', os.devnull, '-f', str(config)]
        if args.action == 'up':
            for profile in args.profile:
                command += ['--profile', profile]
            command += ['up', '-d', '--wait', '--wait-timeout', str(args.wait_timeout)]
            if args.build:
                command.append('--build')
            command += args.service
        elif args.action == 'down':
            command += ['--profile', '*', 'down', '--remove-orphans']
            if args.volumes:
                command.append('--volumes')
        elif args.action == 'status':
            command += ['--profile', '*', 'ps', '--all', '--format', 'json']
            result = subprocess.run(command, env=docker_env(), capture_output=True, text=True, check=True)
            for row in public_status(result.stdout):
                print(json.dumps(row))
            return
        else:
            model = json.loads(config.read_text())
            if args.service not in model['services'] or not args.command:
                raise ValueError('Specify a service in this snapshot and a command')
            command += (['exec', '-T'] if args.action == 'exec' else ['run', '--rm', '--no-deps', '-T'])
            command += [args.service, *args.command]
        result = subprocess.run(command, env=docker_env())
        raise SystemExit(result.returncode)


if __name__ == '__main__':
    try:
        main()
    except (ValueError, OSError, subprocess.CalledProcessError) as error:
        print(f'isolated-dev: {error}', file=sys.stderr)
        raise SystemExit(1)
