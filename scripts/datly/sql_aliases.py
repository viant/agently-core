"""Lex SQL/DQL and reject unquoted MySQL reserved table/view aliases.

The lexer discards strings/comments and template expressions. Parenthesized
sources are balanced tokens, so nested views are checked independently.
"""
import json
from pathlib import Path

RESERVED = set(json.loads(Path(__file__).with_name('mysql_reserved_words.json').read_text())['words'])
CLAUSES = set('WHERE ON USING JOIN LEFT RIGHT INNER OUTER FULL CROSS NATURAL GROUP ORDER HAVING LIMIT OFFSET UNION EXCEPT INTERSECT SET VALUES RETURNING FOR WINDOW INTO'.split())

def tokens(source):
    result=[]
    i=0
    while i<len(source):
        c=source[i]
        if c.isspace(): i+=1; continue
        if source.startswith('--',i) or source.startswith('//',i):
            end=source.find('\n',i); i=len(source) if end<0 else end+1; continue
        if source.startswith('/*',i):
            end=source.find('*/',i+2)
            if end<0: raise ValueError('unterminated SQL comment')
            i=end+2; continue
        if c in "'\"`":
            start=i; quote=c; i+=1
            while i<len(source):
                if source[i]=='\\': i+=2; continue
                if source[i]==quote:
                    if i+1<len(source) and source[i+1]==quote: i+=2; continue
                    i+=1; break
                i+=1
            else: raise ValueError('unterminated quoted SQL token')
            if quote=='`': result.append((source[start+1:i-1],True,start))
            continue
        if c == '#':
            end=i+1
            while end<len(source) and (source[end].isalnum() or source[end]=='_'): end+=1
            directive=source[i+1:end].lower()
            if directive not in {'package','import','setting','define','if','elseif','else','end','foreach','set','evaluate','macro','break','stop'}:
                end=source.find('\n',i); i=len(source) if end<0 else end+1; continue
        if c in '#$':
            # Directive/macro arguments can contain SQL-looking strings; they
            # are metadata/template code, not alias declarations.
            i+=1
            if i<len(source) and source[i]=='{': close='}'; depth=1; i+=1
            else:
                while i<len(source) and (source[i].isalnum() or source[i] in '_.'): i+=1
                if i>=len(source) or source[i]!='(': continue
                close=')'; depth=1; i+=1
            quote=None
            while i<len(source) and depth:
                x=source[i]
                if quote:
                    if x=='\\': i+=2; continue
                    if x==quote: quote=None
                elif x in "'\"`": quote=x
                elif x==('(' if close==')' else '{'): depth+=1
                elif x==close: depth-=1
                i+=1
            if depth: raise ValueError('unterminated DQL expression')
            continue
        start=i
        if c.isalpha() or c=='_':
            i+=1
            while i<len(source) and (source[i].isalnum() or source[i]=='_'): i+=1
        else: i+=1
        result.append((source[start:i],False,start))
    return result

def alias_errors(source):
    items=tokens(source); errors=[]
    def check(index):
        if index>=len(items): return
        word,quoted,pos=items[index]
        if not quoted and word.upper() in RESERVED:
            errors.append((source.count('\n',0,pos)+1,word))
    depth=0
    from_scope={}
    for index,(word,quoted,_) in enumerate(items):
        if word == ')':
            from_scope.pop(depth,None)
            depth-=1
        if not quoted and word.upper() in {'SELECT','WHERE','GROUP','ORDER','HAVING','LIMIT','UNION','EXCEPT','INTERSECT','RETURNING'}:
            from_scope[depth]=False
        source_start = not quoted and (word.upper() in {'FROM','JOIN'} or word == ',' and from_scope.get(depth,False))
        if not quoted and word.upper() == 'FROM': from_scope[depth]=True
        if word == '(': depth+=1
        # A CTE declares its identifier before AS (...), unlike CAST's type.
        if word.upper() == 'AS' and index > 0 and index+1 < len(items) and items[index+1][0] == '(':
            check(index-1)
        if not source_start: continue
        j=index+1
        if j>=len(items): continue
        if items[j][0]=='(':
            source_depth=1; j+=1
            while j<len(items) and source_depth:
                if items[j][0]=='(': source_depth+=1
                elif items[j][0]==')': source_depth-=1
                j+=1
        else:
            j+=1
            while j+1<len(items) and items[j][0]=='.': j+=2
        if j<len(items) and items[j][0].upper()=='AS':
            check(j+1)
        elif j<len(items) and items[j][0].upper() not in CLAUSES:
            check(j)
    return sorted(set(errors))
