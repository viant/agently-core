#!/usr/bin/env python3
"""One-time migration: reconstruct DQL from the legacy typed contracts/resources.

This authors DQL only. Datly's operation-based CLI owns all Go generation.
Inventory input comes from inventory.go; no application database is consulted.
"""
import json, re, sys
from pathlib import Path

root = Path(sys.argv[1]).resolve()
files = json.loads(Path(sys.argv[2]).read_text())
project = root
if (project/'components.json').exists():
    raise SystemExit('DQL recovery is one-time; existing authored contracts must be edited directly.')
module = 'github.com/viant/agently-core'
legacy_module = 'github.com/viant/agently-core'
shapes = {}
for file in files:
    for shape in file.get('Shapes') or []:
        shapes[(str(Path(file['Path']).parent), shape['Name'])] = (shape, file)

def typename(expr):
    return re.sub(r'^[\[\]*]+', '', expr)

def resolve(expr, file):
    name = typename(expr)
    folder = str(Path(file['Path']).parent)
    if '.' in name:
        alias, name = name.split('.', 1)
        imp = file['Imports'][alias]
        if not imp.startswith(legacy_module + '/'):
            raise ValueError('foreign shape ' + expr)
        folder = imp[len(legacy_module)+1:]
    if (folder, name) not in shapes:
        # Legacy writer compatibility aliases name one canonical physical shape.
        text = '\n'.join(p.read_text() for p in (root/folder).glob('*.go'))
        alias = re.search(r'type\s+' + re.escape(name) + r'\s*=\s*(\w+)', text)
        if alias:
            name = alias[1]
    return shapes[(folder, name)]

def source_sql(file, resource):
    if not resource.startswith('uri='):
        return resource.strip()
    name = resource[4:]
    path = root / Path(file['Path']).parent / name
    if not path.is_file():
        # A few legacy holders borrow another package's embedded resources.
        borrowed = re.findall(r'&(\w+)\.\w+FS', (root/file['Path']).read_text())
        for alias in borrowed:
            imp=file['Imports'].get(alias,'')
            if imp.startswith(legacy_module+'/'):
                candidate=root/imp[len(legacy_module)+1:]/name
                if candidate.is_file():
                    path=candidate
                    break
        if path.is_file():
            query=path.read_text().strip()
            return normalize_sql(query[1:-1].strip() if query.startswith('(') and query.endswith(')') else query)
        candidates = list((root/'pkg').rglob(name))
        if len(candidates) != 1:
            raise ValueError('missing/ambiguous SQL resource ' + str(path))
        path = candidates[0]
    query = path.read_text().strip()
    if query.startswith('(') and query.endswith(')'):
        query = query[1:-1].strip()
    return normalize_sql(query)

def normalize_sql(query):
    # Fixed conditions precede optional predicate expansion.
    pattern=r'(\$\{predicate\.Builder\(\).*?\.Build\("WHERE"\)\})\s+AND\s+(.+?)(?=\n\s*(?:ORDER BY|GROUP BY|LIMIT|\$\{)|$)'
    query=re.sub(pattern,lambda m:'WHERE '+m[2].strip()+'\n'+m[1].replace('Build("WHERE")','Build("AND")'),query,flags=re.S)
    query=re.sub(r'\$\{predicate\.Builder\(\)\.CombineOr\(\$predicate\.FilterGroup\(0, \"AND\"\)\)\.Build\(\"WHERE\"\)\}\s*\$\{predicate\.Builder\(\)\.CombineOr\(\$predicate\.FilterGroup\(1, \"AND\"\)\)\.Build\(\"AND\"\)\}', '${predicate.Builder().CombineAnd($predicate.FilterGroup(0, \"AND\"), $predicate.FilterGroup(1, \"AND\")).Build(\"WHERE\")}', query)
    query=re.sub(r'\b(LIMIT|OFFSET)\s+\$(\w+)',r'\1 :\2',query)
    return query

def quote(value):
    # Directive strings use double quotes when SQL contains apostrophes.
    if "'" not in value:
        return "'" + value + "'"
    if '"' not in value:
        return '"' + value + '"'
    raise ValueError('directive string needs explicit quoting review: '+value)

def kind_options(value):
    result = {}
    for item in value.split(',')[1:]:
        key, _, val = item.partition('=')
        result[key] = val or 'true'
    return result

def binding(field):
    tags = field['Tags']; options = kind_options(tags.get('parameter',''))
    if options.get('kind') not in ('path','query','header','cookie','body'):
        return None
    text = f"#define($_ = ${field['Name']}<{field['Type']}>({options['kind']}/{options.get('in','')}))"
    tail = '.Required()' if options.get('required')=='true' or options['kind']=='path' else '.Optional()'
    if 'value' in tags:
        tail += f".Value({quote(tags['value'])})"
    if 'querySelector' in tags:
        tail += f".QuerySelector({quote(tags['querySelector'])})"
    if 'predicate' in tags:
        predicate = tags['predicate']
        name, rest = predicate.split(',',1)
        group, rest = rest.split(',',1)
        group = group.split('=',1)[1]
        args = [rest] if name=='expr' else rest.split(',')
        tail += f".WithPredicate({group},{quote(name)}," + ','.join(quote(arg) for arg in args) + ')'
    # Preserve authored source codecs; unsupported dependencies remain blockers.
    if 'codec' in tags:
        parts = tags['codec'].split(',')
        tail += '.WithCodec(' + ','.join(quote(p) for p in parts) + ')'
    return text[:-1] + tail + ')'

def column_meta(alias, field, writer=False):
    tags = field['Tags']; mapping = tags.get('sqlx','').split(',')[0]
    if not mapping or mapping=='-':
        return []
    col = mapping.split('.')[-1]
    expr = field['Type']
    if expr in ('map[string]interface{}','map[string]any','Elicitation'):
        expr = 'model.JSON'
    if not re.fullmatch(r'\*?(?:\[\])?\*?(?:[A-Za-z_]\w*)(?:\.\w+)?',expr):
        raise ValueError('unsupported rich type: ' + expr)
    result = [f'CAST({alias}.{col} AS {expr})']
    keep = {key:value for key,value in tags.items() if key in ('validate','internal','json')}
    # Existing SQLX key/reference authority is retained for writers.
    if writer:
        keep['sqlx'] = tags['sqlx']
    if keep:
        result.append(f'tag({alias}.{col},' + quote(' '.join(f'{k}:"{v}"' for k,v in keep.items())) + ')')
    return result

def header(dest, route, operation, input_name, output_name):
    return [f"#package('{module}/generated/{dest}')", "#import('time','time')", f"#import('model','{module}/model')", "#setting($_ = $connector('agently'))", "#setting($_ = $internal(true))", f"#setting($_ = $route('{route}','{operation.upper()}'))", f"#setting($_ = $input_type('{input_name}'))", f"#setting($_ = $output_type('{output_name}'))", "#setting($_ = $case_format('lc'))"]

components = []; blocked = []
for file in files:
    if not file['Path'].startswith('pkg/'):
        continue
    for output in file.get('Shapes') or []:
        fields = [f for f in output.get('Fields') or [] if f['Tags'].get('parameter','').endswith('in=view')]
        if not fields:
            continue
        try:
            data = fields[0]
            input_name = output['Name'].replace('Output','Input')
            if input_name=='Input' and not any(s['Name']=='Input' for s in file['Shapes']):
                raise ValueError('missing reader input')
            inp = next(s for s in file['Shapes'] if s['Name']==input_name)
            constants = file['Constants']
            routes = [v for k,v in constants.items() if 'URI' in k or k=='URI']
            if len(routes)!=1:
                raise ValueError('ambiguous route')
            dest = '/'.join([str(Path(file['Path']).parent)[4:].lower(), Path(file['Path']).stem.lower(), 'reader'])
            definitions = header(dest,routes[0],'GET',input_name,output['Name'])
            definitions += [b for field in inp['Fields'] if (b:=binding(field))]
            root_shape, root_file = resolve(data['Type'],file)
            definitions += [f"#define($_ = $Data<{data['Type'].replace(typename(data['Type']),root_shape['Name'])}>(output/view))"]
            # Reader output metadata stays a generated contract.
            projections=[]; views=[]; joins=[]; seen=set()
            def visit(shape,owner,alias,parent=None,on=None):
                if alias in seen: raise ValueError('duplicate graph alias '+alias)
                seen.add(alias)
                physical=[f for f in shape['Fields'] if f['Tags'].get('sqlx','').split(',')[0] not in ('','-')]
                projections.extend(alias+'.'+f['Tags']['sqlx'].split(',')[0].split('.')[-1] for f in physical)
                view_type=shape['Name'] if parent is None else alias[0].upper()+alias[1:]+'View'
                projections.append(f"type({alias},'{view_type}')")
                for field in physical: projections.extend(column_meta(alias,field))
                if parent is None:
                    views.append(f'FROM (\n{source_sql(file,data["Tags"]["sql"])}\n) {alias}')
                else:
                    query=source_sql(owner,on['Tags']['sql'])
                    conditions=[]
                    for part in on['Tags']['on'].split(','):
                        left,right=part.split('=')
                        l=left.split(':')[-1].split('.')[-1]
                        r=right.split(':')[-1].split('.')[-1]
                        # Explicit Go keys are the typed authority; their SQLX
                        # output aliases can differ from inner source names.
                        if ':' in right:
                            key=right.split(':')[0]
                            mapped=next((f['Tags'].get('sqlx','').split(',')[0] for f in shape['Fields'] if f['Name']==key),None)
                            if mapped:r=mapped.split('.')[-1]
                        l=re.sub(r'\(.*\)$','',l)
                        conditions.append(f'{alias}.{r} = {parent}.{l}')
                    if not on['Type'].startswith('[]'): conditions.append('1=1')
                    views.append('LEFT JOIN (\n'+query+'\n) '+alias+' ON '+' AND '.join(conditions))
                for field in shape['Fields']:
                    if 'on' in field['Tags'] and 'sql' in field['Tags']:
                        child,child_file=resolve(field['Type'],owner)
                        child_alias=field['Name'][0].lower()+field['Name'][1:]
                        visit(child,owner,child_alias,alias,field)
            alias=data['Tags'].get('view','').split(',')[0] or 'rows'
            visit(root_shape,root_file,alias)
            text='\n'.join(definitions)+'\n\nSELECT '+',\n       '.join(projections)+'\n'+'\n'.join(views)+'\n'
            src='dql/'+dest
            path=project/src/'reader.dql';path.parent.mkdir(parents=True,exist_ok=True)
            if path.exists(): raise ValueError('refusing to overwrite authored DQL '+str(path))
            path.write_text(text)
            components.append(dict(operation='get',source=src,destination='generated/'+dest,legacy=file['Path'],route=routes[0],status='authored',hooks='reader hooks require port/parity' if any('.On' in f for f in file.get('Functions') or []) else ''))
        except (ValueError,KeyError,StopIteration) as e:
            blocked.append(dict(legacy=file['Path'],operation='get',reason=str(e)))

for file in files:
    if not file['Path'].startswith('pkg/') or Path(file['Path']).name!='input.go' or '/write/' not in file['Path']:continue
    try:
        inp=next(s for s in file['Shapes'] if s['Name']=='Input')
        body=next(f for f in inp['Fields'] if 'kind=body' in f['Tags'].get('parameter',''))
        if '/write/' not in file['Path']:continue
        folder=root/Path(file['Path']).parent
        behavior='\n'.join(p.read_text() for p in folder.glob('*.go') if not p.name.endswith('_test.go'))
        if '/linkstate/' in file['Path']:
            raise ValueError('CreateOrGetPending requires atomic affected-row/CAS support; graph-only replacement unproven')
        table_names=re.findall(r'(?:Insert|Update)\("([^"\n]+)"',behavior)
        if not table_names and '/modelcall/' in file['Path']:
            table_names=['model_call']
        if not table_names: raise ValueError('no authoritative mutation table')
        table=table_names[0]
        shape,owner=resolve(body['Type'],file)
        physical=[f for f in shape['Fields'] if f['Tags'].get('sqlx','').split(',')[0] not in ('','-')]
        if not physical:raise ValueError('no physical writer shape')
        dest=str(Path(file['Path']).parent)[4:].lower()
        route_constants={k:v for f in files if Path(f['Path']).parent==Path(file['Path']).parent for k,v in f['Constants'].items()}
        route=route_constants.get('PathURI')
        if route is None:raise ValueError('missing route constant')
        # Original generic upsert graph is PATCH; special conditional paths are
        # recorded separately, never disguised as generic updates.
        declarations=header(dest,route,'PATCH','Input','Output')
        declarations += [f"#define($_ = ${body['Name']}<{body['Type'].replace(typename(body['Type']),shape['Name'])}>(body/data))", f"#define($_ = $Data<{body['Type'].replace(typename(body['Type']),shape['Name'])}>(output/body))"]
        projections=['rows.'+f['Tags']['sqlx'].split(',')[0] for f in physical]
        projections += [f"type(rows,'{shape['Name']}')",f"lifecycle_type(rows,'Lifecycle')"]
        for f in physical:projections.extend(column_meta('rows',f,True))
        cols=', '.join('`'+f['Tags']['sqlx'].split(',')[0]+'`' for f in physical)
        text='\n'.join(declarations)+'\n\nSELECT '+',\n       '.join(projections)+f'\nFROM (SELECT {cols} FROM {table}) rows\n'
        src='dql/'+dest
        p=project/src/'writer.dql';p.parent.mkdir(parents=True,exist_ok=True)
        if p.exists(): raise ValueError('refusing to overwrite authored DQL '+str(p))
        p.write_text(text)
        hooks='pending business hook port' if (folder/'input_init.go').exists() else ''
        components.append(dict(operation='patch',source=src,destination='generated/'+dest,legacy=file['Path'],route=route,status='authored',hooks=hooks,table=table))
        if table=='run':
            blocked.append(dict(legacy=str(Path(file['Path']).parent/'condition.go'),operation='put',reason='atomic expected status/attempt/lease-owner criteria and affected-row outcome are not standard generated concurrency-token semantics'))
    except (ValueError,KeyError,StopIteration) as e:
        blocked.append(dict(legacy=file['Path'],operation='patch',reason=str(e)))

(project/'model').mkdir(exist_ok=True)
(project/'model/json.go').write_text('package model\n\n// JSON is a logical elicitation payload populated by reader hooks.\ntype JSON map[string]interface{}\n\n// Bytes preserves nullable binary fields without a pointer-to-slice CAST.\ntype Bytes []byte\n')
# Structured inventory contains no DSNs or credentials.
(project/'components.json').write_text(json.dumps(dict(module=module,datly_commit='5aad1bdd54943d289f7e3be0eb3f66659838164e',components=components,blocked=blocked),indent=2)+'\n')
print(f'Authored {len(components)} DQL contracts; {len(blocked)} cases need explicit review')
