#!/usr/bin/env python3
"""Build the selected stock CLI with compiled project predicate/codec types.

Disposable stock command sources live inside ignored root bin/ so Go permits
imports from Core internal/. A private modfile keeps CLI-only dependencies out
of the application's module manifest. No framework source is retained.
"""
import os
import shutil
import subprocess
import tempfile
from pathlib import Path
from contracts import PROJECT_ROOT

project = PROJECT_ROOT
selected_modfile = Path(os.environ.get('DATLY_MODFILE', project / 'go.mod')).resolve()
env = dict(os.environ, GOWORK='off')
module_args = [] if selected_modfile == project / 'go.mod' else ['-modfile=' + str(selected_modfile)]
source_root = Path(subprocess.check_output(['go', 'list', '-mod=readonly', *module_args, '-m', '-f', '{{.Dir}}', 'github.com/viant/datly'], cwd=project, env=env, text=True).strip())
command_root = source_root / 'cmd/datly'
(project / 'bin').mkdir(exist_ok=True)
with tempfile.TemporaryDirectory(prefix='datly-transcriber-', dir=project / 'bin') as tmp:
    stage = Path(tmp)
    modfile = stage / 'cli.mod'
    shutil.copyfile(selected_modfile, modfile)
    selected_sum = selected_modfile.with_suffix('.sum')
    if selected_sum.exists():
        shutil.copyfile(selected_sum, modfile.with_suffix('.sum'))
    sources = []
    for source in sorted(command_root.glob('*.go')):
        if source.name.endswith('_test.go'):
            continue
        text = source.read_text()
        if '//go:embed' in text:
            raise SystemExit('selected CLI embeds resources; disposable build must preserve them')
        if source.name == 'main.go':
            anchor = 'import (\n'
            if anchor not in text:
                raise SystemExit('selected stock CLI main has no Go import block')
            imports = '\t_ "github.com/viant/agently-core/internal/datly/predicate"\n\t_ "github.com/viant/agently-core/internal/datly/codec"\n'
            text = text.replace(anchor, anchor + imports, 1)
        target = stage / source.name
        target.write_text(text)
        sources.append(str(target))
    if not sources:
        raise SystemExit('selected Datly module has no stock CLI sources')
    subprocess.run(['go', 'build', '-mod=mod', '-modfile=' + str(modfile), '-o', str(project / 'bin/datly'), *sources], cwd=project, env=env, check=True)
