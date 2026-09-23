"""Live template smoke test with a synthetic Bun app, not private ClientCo application code."""
import json, os, shutil, subprocess, tempfile, urllib.request
from pathlib import Path
from concurrent.futures import ThreadPoolExecutor
skill=Path(__file__).resolve().parents[1]
env=dict(os.environ, NPM_TOKEN='synthetic-template-check-only')
def run(cmd):
 p=subprocess.run(cmd,capture_output=True,text=True,env=env)
 if p.returncode: raise RuntimeError(p.stdout+p.stderr)
 return p.stdout
before=set(run(['docker','ps','-aq']).split())
with tempfile.TemporaryDirectory(prefix='idv-template-smoke-') as folder:
 base=Path(folder); stacks=[]; images=[]
 try:
  for slot in ['a','b']:
   root=base/slot/'app'; root.mkdir(parents=True)
   run(['git','init','-q',str(root)])
   dest=root/'.agent-dev'; dest.mkdir()
   for name in ['Dockerfile','entrypoint.sh','nginx.conf','registry.npmrc','nuxt.compose.yaml']:
    shutil.copyfile(skill/'assets'/name,dest/name)
   (root/'package.json').write_text(json.dumps({'name':'idv-template-fixture','scripts':{'dev':'bun server.ts'}}))
   (root/'server.ts').write_text('Bun.serve({ hostname: "0.0.0.0", port: 3000, fetch() { return Response.json({marker: process.env.RR_MARKER, db: process.env.DATABASE_URL, port:process.env.NUXT_PORT}); } });\n')
   (root/'.env').write_text('DATABASE_URL=do-not-load-original-env\n')
   (root/'.npmrc').write_text('# original checkout registry file must remain untouched\n')
   run(['docker','run','--rm','--network','none','-v',str(root)+':/app','-w','/app','oven/bun:1.3.9','bun','install','--lockfile-only','--ignore-scripts'])
   runtime=dest/'runtime.env'
   runtime.write_text(f'RR_CHECKOUT={root}\nRR_RUNTIME_ENV={runtime}\nRR_DB_NAME=fixture\nRR_DB_IMAGE=postgres:17\nNUXT_SESSION_PASSWORD=local-test-session-password-at-least-32-characters\nRR_MARKER={slot}\nDATABASE_URL=postgres://postgres:example@db:5432/fixture\n')
   cli=['python3',str(skill/'scripts/compose.py'),'--root',str(root),'--state-dir',str(base/'state')]
   run(cli+['prepare','-f',str(dest/'nuxt.compose.yaml'),'--env-file',str(runtime)])
   where=json.loads(run(cli+['where'])); images.append(where['project']+'-app:dev')
   stacks.append((cli,root,slot))
  with ThreadPoolExecutor(max_workers=2) as pool:
   list(pool.map(lambda item:run(item[0]+['up','--build','--service','nginx','--wait-timeout','120']),stacks))
  ports=[]
  for cli,root,slot in stacks:
   rows=[json.loads(x) for x in run(cli+['status']).splitlines() if x]
   apps=[r for r in rows if r['Service']=='app']; assert len(apps)==2 and all(r['Health']=='healthy' for r in apps)
   proxy=next(r for r in rows if r['Service']=='nginx'); port=proxy['Publishers'][0]['PublishedPort']; ports.append(port)
   data=json.load(urllib.request.urlopen(f'http://127.0.0.1:{port}',timeout=5))
   assert data=={'marker':slot,'db':'postgres://postgres:example@db:5432/fixture','port':'3000'},data
   assert (root/'.env').read_text()=='DATABASE_URL=do-not-load-original-env\n'
   assert (root/'.npmrc').read_text()=='# original checkout registry file must remain untouched\n'
  assert len(set(ports))==2
  run(stacks[0][0]+['down','--volumes'])
  assert json.load(urllib.request.urlopen(f'http://127.0.0.1:{ports[1]}',timeout=5))['marker']=='b'
  print('PASS: two generated app stacks, four healthy app replicas, isolated proxy ports, correct DB env, original env/registry untouched, other proxy survives teardown')
 finally:
  for cli,_,_ in stacks:
   run(cli+['down','--volumes'])
  for image in images:
   subprocess.run(['docker','image','rm',image],capture_output=True,env=env)
assert before==set(run(['docker','ps','-aq']).split())
print('PASS: all template smoke containers and generated image tags cleaned; existing container inventory unchanged')
