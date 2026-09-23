"""Live template check with a real Node streaming fixture, not private ClientCo application code."""
import json, os, shutil, subprocess, tempfile, urllib.request
from pathlib import Path
from concurrent.futures import ThreadPoolExecutor
skill=Path(__file__).resolve().parents[1]
env={key:value for key,value in os.environ.items() if key != 'NPM_TOKEN'}
def run(cmd):
 p=subprocess.run(cmd,capture_output=True,text=True,env=env)
 if p.returncode: raise RuntimeError(p.stdout+p.stderr)
 return p.stdout
before=set(run(['docker','ps','-aq']).split())
with tempfile.TemporaryDirectory(prefix='idv-template-smoke-') as folder:
 base=Path(folder); stacks=[]; images=[]
 registry=base/'fixture.npmrc'
 registry.write_text('//npm.pkg.github.com/:_authToken=synthetic-template-check-only\n')
 try:
  for slot in ['a','b']:
   root=base/slot/'app'; root.mkdir(parents=True)
   run(['git','init','-q',str(root)])
   dest=root/'.agent-dev'; dest.mkdir()
   for name in ['Dockerfile','entrypoint.sh','nginx.conf','registry.npmrc','nuxt.compose.yaml']:
    shutil.copyfile(skill/'assets'/name,dest/name)
   (root/'package.json').write_text(json.dumps({'name':'idv-template-fixture','scripts':{'dev':'node server.mjs'}}))
   (root/'server.mjs').write_text("""import { createServer } from 'node:http';
import { Readable } from 'node:stream';
createServer((req, res) => {
  if (req.url === '/download') {
    res.writeHead(200, {'Content-Type': 'application/octet-stream', 'Content-Disposition': 'attachment; filename=fixture.bin'});
    Readable.from([Buffer.from([0, 1, 2]), Buffer.from([253, 254, 255])]).pipe(res);
    return;
  }
  res.setHeader('Content-Type', 'application/json');
  res.end(JSON.stringify({marker: process.env.RR_MARKER, db: process.env.DATABASE_URL,
    host: req.headers.host, hostname: process.env.DEV_HOSTNAME, port: process.env.NUXT_PORT, node: process.versions.node, bun: !!process.versions.bun}));
}).listen(3000, '0.0.0.0');
""")
   (root/'.env').write_text('DATABASE_URL=do-not-load-original-env\n')
   (root/'.npmrc').write_text('# original checkout registry file must remain untouched\n')
   run(['docker','run','--rm','--network','none','-v',str(root)+':/app','-w','/app','oven/bun:1.3.9','bun','install','--lockfile-only','--ignore-scripts'])
   runtime=dest/'runtime.env'
   runtime.write_text(f'DEV_CHECKOUT={root}\nDEV_RUNTIME_ENV={runtime}\nDEV_DB_NAME=fixture\nDEV_DB_IMAGE=postgres:17\nDEV_APP_REPLICAS=2\nNUXT_SESSION_PASSWORD=local-test-session-password-at-least-32-characters\nRR_MARKER={slot}\nDATABASE_URL=postgres://postgres:example@db:5432/fixture\n')
   cli=['python3',str(skill/'scripts/compose.py'),'--root',str(root),'--state-dir',str(base/'state')]
   run(cli+(['--npmrc',str(registry)] if slot == 'a' else [])+['prepare'])
   where=json.loads(run(cli+['where'])); images.append(where['project']+'-app:dev')
   for name in ['compose.json','manifest.json']:
    assert 'synthetic-template-check-only' not in (Path(where['state'])/name).read_text()
   stacks.append((cli,root,slot))
  with ThreadPoolExecutor(max_workers=2) as pool:
   list(pool.map(lambda item:run(item[0]+['up','--build','--service','nginx','--wait-timeout','120']),stacks))
  ports=[]
  for cli,root,slot in stacks:
   rows=[json.loads(x) for x in run(cli+['status']).splitlines() if x]
   apps=[r for r in rows if r['Service']=='app']; assert len(apps)==2 and all(r['Health']=='healthy' for r in apps)
   proxy=next(r for r in rows if r['Service']=='nginx'); port=proxy['Publishers'][0]['PublishedPort']; ports.append(port)
   hostname=json.loads(run(cli+['where']))['hostname']
   assert f'http://{hostname}:{port}' in run(cli+['urls'])
   data=json.loads(run(['curl','--noproxy','*','--fail','--silent','--resolve',f'{hostname}:{port}:127.0.0.1',f'http://{hostname}:{port}']))
   assert data['host']==f'{hostname}:{port}' and data['hostname']==hostname, data
   assert data['marker']==slot and data['db']=='postgres://postgres:example@db:5432/fixture' and data['port']=='3000',data
   assert data['node'].startswith('22.') and not data['bun'], 'The node command must execute real Node 22, not Bun compatibility mode'
   with urllib.request.urlopen(f'http://127.0.0.1:{port}/download',timeout=5) as response:
    assert response.read()==bytes([0,1,2,253,254,255])
    assert len(response.headers.get_all('Transfer-Encoding',[]))<=1
    assert response.headers['Content-Disposition']=='attachment; filename=fixture.bin'
   assert (root/'.env').read_text()=='DATABASE_URL=do-not-load-original-env\n'
   assert (root/'.npmrc').read_text()=='# original checkout registry file must remain untouched\n'
  assert len(set(ports))==2
  run(stacks[0][0]+['down','--volumes'])
  assert json.load(urllib.request.urlopen(f'http://127.0.0.1:{ports[1]}',timeout=5))['marker']=='b'
  print('PASS: real Node 22, streamed downloads through nginx, remembered registry source without persisted tokens, credential-free generic startup, default prepare, named URLs and proxy Host preservation, two stacks/four replicas, independent teardown')
 finally:
  for cli,_,_ in stacks:
   run(cli+['down','--volumes'])
  for image in images:
   subprocess.run(['docker','image','rm',image],capture_output=True,env=env)
assert before==set(run(['docker','ps','-aq']).split())
print('PASS: all template smoke containers and generated image tags cleaned; existing container inventory unchanged')
