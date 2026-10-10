#!/usr/bin/env python3
"""Owned isolated services only; never signals a process by port discovery."""
import json, os, pathlib, signal, subprocess, sys, time, urllib.request
ROOT = pathlib.Path(os.environ.get('CORE_ENDLY_ROOT', ''))
REPO = pathlib.Path(__file__).resolve().parents[2]
if not os.environ.get('CORE_ENDLY_ROOT') or not ROOT.is_absolute() or ROOT.is_symlink():
    raise SystemExit('explicit absolute owned fixture root required')
(ROOT/'pids').mkdir(parents=True, exist_ok=True)
(ROOT/'logs').mkdir(parents=True, exist_ok=True)
PORTS = {29501,29502,29511,29512,29513,29514,29515,29516}
def fail(message):
    raise SystemExit(message)
def identity(pid):
    result = subprocess.run(['ps','-p',str(pid),'-o','lstart=','-o','command='],capture_output=True,text=True)
    return result.stdout.strip() if result.returncode == 0 else ''
def listeners(port):
    result = subprocess.run(['lsof','-tiTCP:'+str(port),'-sTCP:LISTEN'],capture_output=True,text=True)
    return {int(p) for p in result.stdout.split()}
def ready(port, pid):
    if listeners(port) != {pid}: return False
    if port >= 29511: return True
    try:
        with urllib.request.urlopen('http://127.0.0.1:'+str(port)+'/healthz',timeout=1) as response:
            return response.status == 200 and json.load(response).get('status') == 'ok'
    except Exception: return False
os.umask(0o077)
action, raw_port, *command = sys.argv[1:]
port = int(raw_port)
if port not in PORTS or action not in {'start','stop'}: fail('unapproved isolated action/port')
record = ROOT/'pids'/(str(port)+'.json')
lock = ROOT/'pids'/(str(port)+'.lock')
try: lock.mkdir()
except FileExistsError: fail('service lifecycle already locked; manual review required')
try:
    owned = json.loads(record.read_text()) if record.exists() else None
    if owned and identity(owned['pid']) != owned['identity']:
        if identity(owned['pid']): fail('recorded process identity changed; no process signaled')
        record.unlink(); owned = None
    if action == 'stop':
        if not owned:
            if listeners(port): fail('unowned listener; no process signaled')
            sys.exit(0)
        if listeners(port) - {owned['pid']}: fail('foreign listener; no process signaled')
        if identity(owned['pid']) != owned['identity']: fail('ownership changed')
        os.kill(owned['pid'], signal.SIGTERM)
        for _ in range(40):
            if not identity(owned['pid']):
                record.unlink(); sys.exit(0)
            time.sleep(.25)
        fail('owned process did not terminate; no force kill attempted')
    binary = pathlib.Path(command[0]) if command else pathlib.Path('/')
    allowed_binary = binary.parent == REPO/'e2e/bin' and binary.name in {'agently-app','agui-probe','mcp-resources','mcp-elicitation-form','mcp-elicitation-oob','mcp-approval-tool','mcp-large-result'}
    if not allowed_binary or not binary.is_file() or binary.is_symlink(): fail('isolated binary required')
    if owned:
        if owned['command'] != command: fail('owned service configuration differs; explicit stop required')
        if ready(port, owned['pid']): sys.exit(0)
        fail('owned service is live but unhealthy; inspect restricted log')
    if listeners(port): fail('isolated port busy; no listener killed')
    dsn = os.environ.get('CORE_ENDLY_MYSQL_DSN','')
    if not __import__('re').fullmatch(r'[^\r\n]*@tcp\(127\.0\.0\.1:23309\)/core_endly_[a-z0-9_]+\?parseTime=true', dsn): fail('reviewed isolated datasource required')
    environment = dict(os.environ)
    for name in ['ANTHROPIC_API_KEY','GEMINI_API_KEY','GOOGLE_API_KEY','GROK_API_KEY']: environment.pop(name,None)
    if port in {29501,29502} and not environment.get('OPENAI_API_KEY'): fail('configured OpenAI test key required')
    if port >= 29511: environment.pop('OPENAI_API_KEY',None)
    environment.update(GOWORK='off',TEST='1',AGENTLY_DB_DRIVER='mysql',AGENTLY_DB_DSN=dsn,AGENTLY_WORKSPACE=str(ROOT/'workspace/.agently'))
    with (ROOT/'logs'/(str(port)+'.log')).open('ab') as output:
        child = subprocess.Popen(command,env=environment,stdin=subprocess.DEVNULL,stdout=output,stderr=output,start_new_session=True)
    process_identity = identity(child.pid)
    if not process_identity: fail('owned service exited before registration')
    record.write_text(json.dumps({'pid':child.pid,'identity':process_identity,'command':command}))
    for _ in range(40):
        if ready(port,child.pid): sys.exit(0)
        if identity(child.pid) != process_identity: fail('owned service exited; inspect restricted log')
        time.sleep(.5)
    fail('owned process not ready; retained ownership for explicit cleanup')
finally:
    lock.rmdir()
