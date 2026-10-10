#!/usr/bin/env python3
import os,pathlib,re,subprocess
root=os.environ.get('CORE_ENDLY_ROOT','')
if not root or not pathlib.Path(root).is_absolute() or pathlib.Path(root).is_symlink(): raise SystemExit('explicit absolute owned fixture root required')
if not re.fullmatch(r'[^\r\n]*@tcp\(127\.0\.0\.1:23309\)/core_endly_[a-z0-9_]+\?parseTime=true',os.environ.get('CORE_ENDLY_MYSQL_DSN','')): raise SystemExit('reviewed isolated database required')
if not os.environ.get('OPENAI_API_KEY'): raise SystemExit('configured OpenAI test credential required')
for port in [29501,29502,29511,29512,29513,29514,29515,29516]:
 if subprocess.run(['lsof','-tiTCP:'+str(port),'-sTCP:LISTEN'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL).returncode==0: raise SystemExit('owned port busy; no process signaled')
pathlib.Path(root).mkdir(parents=True,exist_ok=True)
print('owned fixture configuration present; no credentials displayed')
