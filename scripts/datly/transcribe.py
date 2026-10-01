#!/usr/bin/env python3
"""Discover one DQL contract per source package and invoke the v1 CLI."""
import json, os, re, subprocess, sys, tempfile
from pathlib import Path
from contracts import PROJECT_ROOT, contract_inventory

project = PROJECT_ROOT
module, inventory = contract_inventory(project)
expected_packages = {item["new_dql"]: item["new_package"] for item in inventory}
cli = Path(os.environ.get('DATLY_BIN', str(project/'bin/datly'))).resolve()
contracts = []
for source in sorted((project/'dql').rglob('*.dql')):
    text = source.read_text()
    route = re.search(r"\$route\(['\"]([^'\"]+)['\"]\s*,\s*['\"](GET|POST|PUT|PATCH|DELETE)['\"]\)", text)
    package = re.search(r"#package\(['\"]([^'\"]+)['\"]\)",text)
    if not route or not package:
        raise SystemExit('missing explicit operation/package in '+str(source))
    if package[1] != expected_packages[source.relative_to(project).as_posix()]:
        raise SystemExit('DQL package differs from canonical destination: '+str(source))
    folder = source.parent
    if len(list(folder.glob('*.dql')))!=1:
        raise SystemExit('one contract per source package required: '+str(folder))
    contracts.append((source,route[2].lower(),module+'/'+folder.relative_to(project).as_posix()))

env = dict(os.environ, GOWORK='off')
with tempfile.TemporaryDirectory(prefix='agently-v1-schema-') as scratch:
    db = str(Path(scratch)/'schema.db')
    modfile = os.environ.get('DATLY_MODFILE')
    go_options = ['-mod=readonly'] + (['-modfile='+str(Path(modfile).resolve())] if modfile else [])
    subprocess.run(['go','run',*go_options,'./tools/schema',db],cwd=project,env=env,check=True)
    failures=[];report=[]
    for source,operation,package in contracts:
        print(operation+' '+str(source.relative_to(project)),flush=True)
        process=subprocess.run([str(cli),'transcribe',operation,'-dir',str(project),'-schema','-connector','agently','-driver','sqlite3','-dsn',db,package],cwd=project,env=env,text=True,capture_output=True)
        item=dict(source=str(source.relative_to(project)),operation=operation,success=process.returncode==0)
        if process.returncode:
            diagnostic=(process.stderr+process.stdout).replace(db,'<disposable-schema>')
            item['diagnostic']=diagnostic
            print(diagnostic[-1500:],file=sys.stderr)
            failures.append(item)
        report.append(item)
    (project/'generation-report.json').write_text(json.dumps(report,indent=2)+'\n')
    print(f'Transcribed {len(report)-len(failures)}/{len(report)} contracts')
    sys.exit(bool(failures))
