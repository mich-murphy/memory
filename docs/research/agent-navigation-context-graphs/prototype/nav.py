#!/usr/bin/env python3
"""Frozen v1 navigation candidates; stdlib only. See README for scoring and limits."""
import argparse
from contextlib import closing
import fnmatch
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import posixpath
import re
import sqlite3
import subprocess
import sys
import time
from urllib.parse import unquote

HERE = Path(__file__).resolve().parent
CACHE = HERE / '.cache'
SCHEMA = 'navigation-v1'
MAX_FILE = 512 * 1024
# Positive source/doc allowlist, plus aggressive private/generated exclusions.
EXTENSIONS = set('md mdx txt rst yaml yml json toml ini cfg py ts tsx js jsx mjs cjs css scss html sql sh ps1 bash dbl dbx dbc def inc cs fs vb c h cpp hpp rs go java kt xml xaml tf tfvars bicep proto graphql gql vue svelte rb'.split())
EXCLUDED = {'.git', '.local', '.agents', '.claude', '.codex', '.pi', '.worktrees',
            'node_modules', 'vendor', 'target', 'dist', 'build', 'out', 'coverage',
            '.generated', 'generated', '.next', '.turbo', '.cache', '__pycache__',
            'benchmark', 'benchmarks', 'judgments', 'judgements', 'evals', 'datasets',
            'fixtures', 'dumps', 'artifacts', 'logs'}
IMPORT = re.compile(r'''(?:\b(?:import|export)\s+(?:[^;'"\n]*?\s+from\s*)?|\b(?:import|require)\s*\(\s*)["']([^"']+)["']''')
LINK = re.compile(r'\[[^\]\n]*\]\(\s*<?([^\s)>]+)>?(?:\s+["\'][^\n]*?["\'])?\s*\)')
REF = re.compile(r'^\s*\[[^\]]+\]:\s*<?([^\s>]+)>?', re.M)
STOPWORDS = set('a an and are as at be been but by can could did do does for from had has have how i if in into is it its me my of on or our please should so than that the their them then there these they this those to us was we were what when where which who why will with would you your find locate looking show'.split())
PATH_MENTION = re.compile(r'(?<![\w./:-])([A-Za-z0-9_.@+-]+(?:/[A-Za-z0-9_.@+-]+)+/?)')

class NavError(Exception):
    pass


def digest(data):
    return hashlib.sha256(data).hexdigest()


def git(root, *args):
    p = subprocess.run(['git', '-C', str(root), *args], capture_output=True)
    if p.returncode:
        raise NavError('git failed: ' + p.stderr.decode('utf8', 'replace')[:200])
    return p.stdout


def root_path(root):
    root = Path(root).resolve()
    if Path(git(root, 'rev-parse', '--show-toplevel').decode().strip()).resolve() != root:
        raise NavError('--root must be the worktree root')
    return root


def eligible(path):
    p = PurePosixPath(path)
    parts = [s.lower() for s in p.parts]
    name = p.name.lower()
    if p.is_absolute() or '..' in p.parts or any(s in EXCLUDED for s in parts):
        return False
    instruction = re.match(r'^(?:agents|claude|skill)(?:[._-](?:disabled|off|bak|backup))?\.md(?:[._-].*)?$', name)
    lockfile = (name.endswith(('.lock', '.lockb')) or name in {
        'package-lock.json', 'npm-shrinkwrap.json', 'pnpm-lock.yaml', 'pnpm-lock.yml',
        'lock.yaml', 'lock.yml', 'lock.json', 'packages.lock.json', 'deno.lock'})
    if (instruction or 'judgment' in name or 'judgement' in name
            or name == 'tasks.json' or name.startswith('.env') or lockfile
            or any(s in name for s in ('.dump', '.min.', '.map', '.generated.'))):
        return False
    return name == 'codeowners' or (p.suffix.lower().lstrip('.') in EXTENSIONS)


def safe_file(root, name):
    p = root / name
    # Never follow symlinks, even links back inside the checkout.
    if any(q.is_symlink() for q in [p, *list(p.parents)[:len(PurePosixPath(name).parts)-1]]):
        return None
    try:
        if not p.resolve().is_relative_to(root) or not p.is_file() or p.stat().st_size > MAX_FILE:
            return None
        data = p.read_bytes()
        if b'\x00' in data:
            return None
        data.decode('utf8')
        return data
    except (OSError, UnicodeError):
        return None


def inventory(root):
    files = {}
    for raw in sorted(git(root, 'ls-files', '-z').split(b'\x00')):
        if not raw:
            continue
        name = raw.decode('utf8', 'surrogateescape')
        if not eligible(name):
            continue
        data = safe_file(root, name)
        if data is not None:
            files[name] = digest(data)
    return files


def load_routes(path):
    try:
        obj = json.loads(Path(path).read_text())
        for route in obj['routes']:
            for name in route['prefixes'] + route['docs']:
                if not eligible(name + '/x.md') or name.startswith('/') or '..' in PurePosixPath(name).parts:
                    raise NavError('unsafe route path')
        return obj
    except (ValueError, KeyError, OSError) as e:
        raise NavError('invalid routing file: ' + str(e))


def stamp(root, routes, files):
    return {'schema': SCHEMA, 'root': str(root),
            'head': git(root, 'rev-parse', 'HEAD').decode().strip(),
            'inputs': digest(json.dumps(files, sort_keys=True).encode()),
            'routing': digest(Path(routes).read_bytes()),
            'algorithm': digest(Path(__file__).read_bytes())}


def db_path(root, arg):
    p = Path(arg).expanduser().resolve() if arg else CACHE / (digest(str(root).encode())[:16] + '.sqlite3')
    # All writes for this experiment must remain in its ignored cache.
    if not p.is_relative_to(CACHE.resolve()):
        raise NavError('--db must be inside ' + str(CACHE))
    if p.is_relative_to(root):
        raise NavError('database must not be inside indexed worktree')
    return p


def edge(db, src, dst, kind, provenance, line=1, origin='extracted'):
    db.execute('INSERT OR IGNORE INTO edges VALUES(?,?,?,?,?,?)',
               (src, dst, kind, provenance, line, origin))


def relative_target(src, raw):
    raw = unquote(raw.split('#', 1)[0].split('?', 1)[0])
    if not raw or re.match(r'^[a-zA-Z][\w+.-]*:', raw) or raw.startswith('/') or '\\' in raw:
        return None
    path = posixpath.normpath(posixpath.join(posixpath.dirname(src), raw))
    return None if path == '..' or path.startswith('../') else path


def source_relations(text, nodes):
    """Conservative frontmatter subset and validated literal body path mentions.

    Never interpret YAML tags, aliases, folded values, globs or templated paths.
    A sourceRef is an asserted relation, not a judgment of behavioural proof.
    """
    lines = text.splitlines()
    front_end = 0
    relations, issues = [], []
    in_refs = False
    if lines and lines[0].strip() == '---':
        front_end = next((i for i in range(1, len(lines)) if lines[i].strip() == '---'), 0)
        for i, line in enumerate(lines[1:front_end], 2):
            if re.match(r'^sourceRefs\s*:', line):
                value = line.split(':', 1)[1].strip()
                in_refs = not value
                if value and value != '[]':
                    issues.append((i, value[:200], 'unsupported-source-ref-scalar'))
                continue
            if not in_refs:
                continue
            if line and not line[0].isspace() and not line.startswith('#'):
                in_refs = False
                continue
            match = re.match(r'^\s+-\s+(?:path:\s*)?(.+?)\s*$', line)
            if not match:
                if re.match(r'^\s+path:', line):
                    issues.append((i, line.strip()[:200], 'unsupported-source-ref-scalar'))
                continue
            value = match.group(1).split(' #', 1)[0].strip()
            if len(value) > 1 and value[0] == value[-1] and value[0] in ('"', "'"):
                value = value[1:-1]
            if (not re.fullmatch(r'[A-Za-z0-9_.@+-]+(?:/[A-Za-z0-9_.@+-]+)+/?', value)
                    or '..' in PurePosixPath(value).parts):
                issues.append((i, value[:200], 'unsupported-source-ref-scalar'))
            elif value.rstrip('/') in nodes:
                relations.append((value.rstrip('/'), i, 'source_reference'))
            else:
                issues.append((i, value[:200], 'missing-or-excluded-source-ref'))
    seen = {dst for dst, _, _ in relations}
    fenced = False
    for i, line in enumerate(lines[front_end+1 if front_end else 0:], front_end+2 if front_end else 1):
        if re.match(r'^\s*(```|~~~)', line):
            fenced = not fenced
            continue
        if fenced:
            continue
        for match in PATH_MENTION.finditer(line):
            value = match.group(1).rstrip('/')
            # An exact tracked node is required. Relative links and speculative
            # patterns are not promoted to source references.
            if value in nodes and value not in seen and len(seen) < 256:
                relations.append((value, i, 'path_mention'))
                seen.add(value)
    return relations, issues


def package_rules(text):
    rules = []
    for n, line in enumerate(text.splitlines(), 1):
        bits = line.split('#', 1)[0].split()
        if not bits:
            continue
        pattern = bits[0]
        # CODEOWNERS does not support negation, [] ranges or escaped spaces.
        if pattern.startswith('!') or '[' in pattern or '\\' in pattern:
            continue
        rules.append((pattern, bits[1:], n))
    return rules


def owner_match(path, pattern):
    anchored = pattern.startswith('/')
    pattern = pattern.lstrip('/')
    # Segment-based glob: * never crosses '/', ** does. Directory entries
    # match descendants. Bare names match at any level.
    directory = pattern.endswith('/')
    pattern = pattern.rstrip('/')
    regex = ''
    i = 0
    while i < len(pattern):
        ch = pattern[i]
        if pattern[i:i+3] == '**/':
            regex += '(?:.*/)?'; i += 3
        elif pattern[i:i+2] == '**':
            regex += '.*'; i += 2
        elif ch == '*':
            regex += '[^/]*'; i += 1
        elif ch == '?':
            regex += '[^/]'; i += 1
        else:
            regex += re.escape(ch); i += 1
    prefix = '^' if anchored or '/' in pattern else r'^(?:.*/)?'
    suffix = '/.*$' if directory else '(?:/.*)?$'
    return re.match(prefix + regex + suffix, path) is not None


def build(root, dbfile, routes):
    start = time.perf_counter()
    files = inventory(root)
    initial = stamp(root, routes, files)
    routing = load_routes(routes)
    dbfile.parent.mkdir(parents=True, exist_ok=True)
    tmp = dbfile.with_suffix('.tmp-' + str(os.getpid()))
    db = sqlite3.connect(tmp)
    try:
        db.executescript('''
        CREATE TABLE meta(key TEXT PRIMARY KEY, value TEXT);
        CREATE TABLE nodes(path TEXT PRIMARY KEY, kind TEXT, hash TEXT);
        CREATE VIRTUAL TABLE lex USING fts5(path, headings, body, tokenize='porter unicode61');
        CREATE TABLE edges(src TEXT, dst TEXT, kind TEXT, provenance TEXT, line INTEGER,
                           origin TEXT, UNIQUE(src,dst,kind,provenance,line,origin));
        CREATE INDEX outgoing ON edges(src, origin);
        CREATE INDEX incoming ON edges(dst, origin);
        CREATE TABLE issues(path TEXT, line INTEGER, target TEXT, reason TEXT);
        CREATE TABLE owners(path TEXT PRIMARY KEY, owners TEXT, line INTEGER);
        ''')
        dirs = set()
        for path, sha in files.items():
            db.execute('INSERT INTO nodes VALUES(?,?,?)', (path, 'file', sha))
            dirs.update(str(p) for p in PurePosixPath(path).parents if str(p) != '.')
        for path in sorted(dirs):
            db.execute('INSERT INTO nodes VALUES(?,?,?)', (path, 'directory', None))
        nodes = set(files) | dirs
        packages = {}
        package_data = {}
        for path in files:
            if PurePosixPath(path).name == 'package.json':
                try:
                    obj = json.loads(safe_file(root, path))
                    package_data[path] = obj
                    if obj.get('name'):
                        packages.setdefault(obj['name'], []).append(path)
                except (ValueError, TypeError):
                    pass
        rules = package_rules((safe_file(root, '.github/CODEOWNERS') or b'').decode()) if '.github/CODEOWNERS' in files else []
        for path in sorted(nodes):
            parent = posixpath.dirname(path)
            if parent in dirs:
                edge(db, path, parent, 'directory_member', path, origin='extracted')
        for path in files:
            raw = safe_file(root, path)
            if raw is None or digest(raw) != files[path]:
                raise NavError('inputs changed during build; retry')
            text = raw.decode()
            headings = '\n'.join(line.lstrip('# ') for line in text.splitlines() if line.startswith('#'))
            db.execute('INSERT INTO lex VALUES(?,?,?)', (path, headings, text))
            domain = path.split('/')[0]
            if domain in dirs and domain != posixpath.dirname(path):
                edge(db, path, domain, 'domain_member', path)
            parent = PurePosixPath(path).parent
            for p in [parent, *parent.parents]:
                manifest = str(p / 'package.json')
                if manifest in package_data and manifest != path:
                    edge(db, path, manifest, 'package_member', manifest)
                    break
            winner = None
            for pattern, owners, n in rules:
                if owner_match(path, pattern):
                    winner = (owners, n)
            if winner:
                owners, n = winner
                db.execute('INSERT INTO owners VALUES(?,?,?)', (path, json.dumps(owners), n))
                for owner in owners:
                    edge(db, path, 'owner:' + owner, 'owned_by', '.github/CODEOWNERS', n)
            if path.endswith(('.md', '.mdx')):
                relations, issues = source_relations(text, nodes)
                for dst, n, kind in relations:
                    edge(db, path, dst, kind, path, n)
                for n, target, reason in issues:
                    db.execute('INSERT INTO issues VALUES(?,?,?,?)', (path, n, target, reason))
                # Ignore fenced examples to reduce false links; line numbers retained.
                fenced = False
                clean = []
                for line in text.splitlines(keepends=True):
                    if re.match(r'^\s*(```|~~~)', line):
                        fenced = not fenced
                        clean.append('\n')
                    else:
                        clean.append('\n' if fenced else line)
                clean = ''.join(clean)
                for match in list(LINK.finditer(clean)) + list(REF.finditer(clean)):
                    target = relative_target(path, match.group(1))
                    if target is None:
                        continue
                    n = clean.count('\n', 0, match.start()) + 1
                    if target in nodes:
                        edge(db, path, target, 'markdown_link', path, n)
                    else:
                        db.execute('INSERT INTO issues VALUES(?,?,?,?)', (path, n, target, 'missing-or-excluded'))
            if path.endswith(('.ts', '.tsx', '.js', '.jsx', '.mjs', '.cjs')):
                for match in IMPORT.finditer(text):
                    spec = match.group(1)
                    n = text.count('\n', 0, match.start()) + 1
                    if spec.startswith('.'):
                        base = relative_target(path, spec)
                        if not base:
                            continue
                        candidates = {base} if base in files else set()
                        if not PurePosixPath(base).suffix:
                            candidates |= {base + ext for ext in ('.ts', '.tsx', '.js', '.jsx', '/index.ts', '/index.tsx', '/index.js') if base + ext in files}
                        elif base.endswith('.js') and base not in files:
                            candidates |= {base[:-3] + ext for ext in ('.ts', '.tsx') if base[:-3] + ext in files}
                        if len(candidates) == 1:
                            edge(db, path, next(iter(candidates)), 'ts_import', path, n)
                    else:
                        name = '/'.join(spec.split('/')[:2]) if spec.startswith('@') else spec.split('/')[0]
                        # Exact package-root import only; no speculative aliases/subpaths.
                        if spec == name and len(packages.get(name, [])) == 1:
                            edge(db, path, packages[name][0], 'ts_package_import', path, n)
        for path, obj in package_data.items():
            text = safe_file(root, path).decode()
            for field in ('dependencies', 'devDependencies', 'peerDependencies', 'optionalDependencies'):
                for name in obj.get(field, {}):
                    if len(packages.get(name, [])) == 1:
                        n = next((i for i, line in enumerate(text.splitlines(), 1) if '"' + name + '"' in line), 1)
                        edge(db, path, packages[name][0], 'package_dependency', path + ':' + field, n)
        for route in routing['routes']:
            src = 'route:' + route['id']
            for dst in route['prefixes'] + route['docs']:
                if dst in nodes:
                    edge(db, src, dst, 'authored_route', route['source'], origin='authored')
        # Detect additions/deletions/content edits during build, including tracked staging.
        if stamp(root, routes, inventory(root)) != initial:
            raise NavError('inputs changed during build; retry')
        for key, value in initial.items():
            db.execute('INSERT INTO meta VALUES(?,?)', (key, value))
        db.commit()
        stats = {'files': len(files), 'directories': len(dirs),
                 'edges': db.execute('SELECT count(*) FROM edges').fetchone()[0],
                 'dead_links': db.execute("SELECT count(*) FROM issues WHERE reason='missing-or-excluded'").fetchone()[0],
                 'diagnostics': db.execute('SELECT count(*) FROM issues').fetchone()[0]}
        db.close()
        os.replace(tmp, dbfile)
        return {'status': 'built', 'db': str(dbfile), **initial, **stats,
                'seconds': round(time.perf_counter() - start, 4)}
    finally:
        db.close()
        if tmp.exists():
            tmp.unlink()


def tokens(text):
    return list(dict.fromkeys(t for t in re.findall(r'[^\W_]+', text.lower(), re.UNICODE)
                              if t not in STOPWORDS))[:32]


def lexical(db, query, limit, scope=None):
    ts = tokens(query)
    if not ts:
        return []
    expr = ' OR '.join('"' + t + '"' for t in ts)
    sql = 'SELECT path, bm25(lex,8,4,1) AS score FROM lex WHERE lex MATCH ?'
    args = [expr]
    if scope:
        sql += ' AND (path=? OR substr(path,1,?)=?)'
        args.extend([scope, len(scope) + 1, scope + '/'])
    sql += ' ORDER BY score,path LIMIT ?'
    args.append(limit)
    return [(path, -score) for path, score in db.execute(sql, args)]


def line_for(root, path, query):
    raw = safe_file(root, path)
    if raw is None:
        return None
    ts = tokens(query)
    for i, line in enumerate(raw.decode().splitlines(), 1):
        if any(t in line.lower() for t in ts):
            return i
    return 1


def route_hits(routing, query):
    ts = set(tokens(query))
    hits = []
    for route in routing['routes']:
        overlap = len(ts & set(tokens(route['terms'])))
        if overlap:
            hits.append((overlap / max(1, len(ts)), route))
    return sorted(hits, key=lambda x: (-x[0], x[1]['id']))[:3]


def query_db(root, dbfile, routes, query, mode, limit, depth, fanout, scope):
    start = time.perf_counter()
    if not dbfile.is_file():
        raise NavError('INDEX_MISSING: run build first')
    db = sqlite3.connect('file:' + str(dbfile) + '?mode=ro', uri=True)
    try:
        meta = dict(db.execute('SELECT key,value FROM meta'))
        now = stamp(root, routes, inventory(root))
        if meta != now:
            changed = sorted(k for k in now if meta.get(k) != now[k])
            raise NavError('STALE_INDEX: rebuild required (' + ','.join(changed) + ')')
        freshness_seconds = time.perf_counter() - start
        retrieval_start = time.perf_counter()
        routing = load_routes(routes)
        seeds = lexical(db, query, 40, scope) if mode != 'map-only' else []
        results = {}
        def add(path, score, reason, line=None):
            if scope and path != scope and not path.startswith(scope + '/'):
                return
            if not db.execute('SELECT 1 FROM nodes WHERE path=?', (path,)).fetchone():
                return
            old = results.get(path)
            if old is None:
                results[path] = {'path': path, 'line': line, 'score': round(score, 6), 'reasons': [reason]}
            else:
                old['score'] = max(old['score'], round(score, 6))
                if reason not in old['reasons'] and len(old['reasons']) < 3:
                    old['reasons'].append(reason)
        # Rank-based normalization is identical in every mode that uses lexical.
        # Top five lexical seeds always outrank any adjacency or authored addition.
        for i, (path, bm25) in enumerate(seeds):
            add(path, 1 / (1 + .08 * i), {'type': 'lexical', 'rank': i+1, 'bm25': round(bm25, 6)})
        hits = route_hits(routing, query) if mode in ('map', 'map-only', 'hybrid') else []
        for relevance, route in hits:
            reason = {'type': 'authored_route', 'route': route['id'], 'provenance': route['source'], 'origin': 'authored'}
            for dst in route['docs'] + route['prefixes']:
                add(dst, .48 + .1 * relevance, reason, 1)
            if mode != 'map-only':
                for prefix in route['prefixes']:
                    for i, (path, _) in enumerate(lexical(db, query, 10, prefix)):
                        add(path, (.62 + .06 * relevance) / (1 + .08 * i), reason)
        if mode in ('graph', 'hybrid'):
            frontier = [(path, 1 / (1 + .08 * i), 0) for i, (path, _) in enumerate(seeds[:8])]
            if mode == 'hybrid':
                frontier += [(p, .5, 0) for _, r in hits for p in r['docs'] + r['prefixes'] if p in results]
            visited = set()
            for hop in range(depth):
                next_frontier = []
                for src, score, _ in frontier:
                    if src in visited:
                        continue
                    visited.add(src)
                    # Ownership is metadata only: never traverse owner hubs. Fanout
                    # is per source, deterministic, both directions; typed links first.
                    rows = db.execute('''SELECT * FROM (
                        SELECT dst AS target,kind,provenance,line,'out' AS direction FROM edges
                        WHERE src=? AND origin='extracted' AND kind!='owned_by'
                        UNION ALL SELECT src AS target,kind,provenance,line,'in' AS direction FROM edges
                        WHERE dst=? AND origin='extracted' AND kind!='owned_by')
                        ORDER BY CASE WHEN kind IN ('directory_member','domain_member','package_member')
                        THEN 2 WHEN kind='path_mention' THEN 1 ELSE 0 END,
                        kind,target,provenance,line,direction LIMIT ?''', (src, src, fanout)).fetchall()
                    rows.sort(key=lambda r: (r[1] in ('directory_member', 'domain_member', 'package_member'), r[1], r[0]))
                    for dst, kind, provenance, n, direction in rows:
                        weight = (.30 if kind == 'path_mention' else .36
                                  if kind in ('directory_member', 'domain_member', 'package_member') else .65)
                        value = score * weight
                        add(dst, value, {'type': kind, 'from': src, 'direction': direction,
                                        'provenance': provenance, 'line': n, 'origin': 'extracted', 'hop': hop+1,
                                        'semantics': 'mention-not-proof' if kind == 'path_mention' else 'explicit-navigation-relation'})
                        next_frontier.append((dst, value, hop+1))
                frontier = sorted(next_frontier, key=lambda x: (-x[1], x[0]))[:16]
        ranked = sorted(results.values(), key=lambda r: (-r['score'], r['path']))[:limit]
        for i, result in enumerate(ranked, 1):
            result['rank'] = i
            result['kind'] = db.execute('SELECT kind FROM nodes WHERE path=?', (result['path'],)).fetchone()[0]
            if result['line'] is None:
                result['line'] = line_for(root, result['path'], query) if result['kind'] == 'file' else None
            row = db.execute('SELECT owners,line FROM owners WHERE path=?', (result['path'],)).fetchone()
            result['owners'] = {'names': json.loads(row[0]), 'provenance': '.github/CODEOWNERS', 'line': row[1]} if row else None
        return {'status': 'ok', 'mode': mode, 'query': query, 'root': str(root), 'head': meta['head'],
                'input_fingerprint': meta['inputs'], 'algorithm': meta['algorithm'],
                'depth': depth if mode in ('graph', 'hybrid') else 0, 'fanout': fanout,
                'freshness_seconds': round(freshness_seconds, 6),
                'retrieval_seconds': round(time.perf_counter() - retrieval_start, 6),
                'results': ranked, 'truncated': len(results) > limit}
    finally:
        db.close()


def compact(obj):
    return json.dumps(obj, ensure_ascii=True, separators=(',', ':'))


def bounded_output(obj, max_bytes):
    # No file bodies. Entire JSON, including metadata, must fit the byte budget.
    obj['output_bytes'] = 0
    obj['estimated_tokens'] = 0
    while True:
        for _ in range(8):
            n = len(compact(obj).encode()) + 1
            if obj['output_bytes'] == n:
                break
            obj['output_bytes'] = n
            obj['estimated_tokens'] = (n + 3) // 4
        if obj['output_bytes'] <= max_bytes:
            return compact(obj)
        if obj.get('results'):
            obj['results'].pop()
            obj['truncated'] = True
        else:
            raise NavError('CONTEXT_LIMIT: metadata exceeds --max-bytes')


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', required=True)
    parser.add_argument('--db', help='optional path inside prototype/.cache')
    parser.add_argument('--routes', default=str(HERE / 'routing.json'))
    sub = parser.add_subparsers(dest='command', required=True)
    sub.add_parser('build')
    q = sub.add_parser('query')
    q.add_argument('text')
    q.add_argument('--mode', choices=['lexical', 'map', 'map-only', 'map_lexical', 'map_only', 'graph', 'hybrid'], default='hybrid')
    q.add_argument('--limit', type=int, default=10)
    q.add_argument('--depth', type=int, choices=[1, 2], default=1)
    q.add_argument('--fanout', type=int, default=12)
    q.add_argument('--max-bytes', type=int, default=12000)
    q.add_argument('--scope', help='repo-relative directory or file prefix, no glob')
    args = parser.parse_args(argv)
    try:
        root = root_path(args.root)
        dbfile = db_path(root, args.db)
        # Explicit startup capability check on this Python interpreter.
        with closing(sqlite3.connect(':memory:')) as check:
            check.execute('CREATE VIRTUAL TABLE probe USING fts5(text)')
        if args.command == 'build':
            print(compact(build(root, dbfile, args.routes)))
        else:
            if not 1 <= args.limit <= 50 or not 1 <= args.fanout <= 24 or not 1024 <= args.max_bytes <= 65536:
                raise NavError('limit=1..50, fanout=1..24, max-bytes=1024..65536 required')
            if len(args.text.encode()) > 1000:
                raise NavError('query exceeds 1000 UTF-8 bytes')
            if args.scope:
                p = PurePosixPath(args.scope)
                if p.is_absolute() or '..' in p.parts or '\\' in args.scope:
                    raise NavError('unsafe scope')
                args.scope = str(p).rstrip('/')
            args.mode = {'map_lexical': 'map', 'map_only': 'map-only'}.get(args.mode, args.mode)
            obj = query_db(root, dbfile, args.routes, args.text, args.mode,
                           args.limit, args.depth, args.fanout, args.scope)
            print(bounded_output(obj, args.max_bytes))
        return 0
    except (NavError, sqlite3.Error, OSError, ValueError) as e:
        print(compact({'status': 'error', 'error': str(e)}))
        return 2

if __name__ == '__main__':
    sys.exit(main())
