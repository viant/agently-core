#!/usr/bin/env python3
"""Prove native regeneration preserves every generated artifact and authored hook."""
import hashlib, os, subprocess, sys
from pathlib import Path
from contracts import PROJECT_ROOT, generated_files
root=PROJECT_ROOT
def snapshot():
    return {p.relative_to(root).as_posix():hashlib.sha256(p.read_bytes()).hexdigest()
            for p in generated_files(root)}
before=snapshot()
subprocess.run([sys.executable,str(root/'scripts/datly/transcribe.py')],cwd=root,env=dict(os.environ,GOWORK='off'),check=True)
after=snapshot()
changed=[name for name in sorted(set(before)|set(after)) if before.get(name)!=after.get(name)]
if changed:raise SystemExit('regeneration changed artifacts/hooks:\n'+'\n'.join(changed))
print(f'Regeneration preserved {len(before)} artifacts, including authored lifecycle hooks')
