#!/usr/bin/env python3
"""Serve the private evidence dashboard on loopback; no credentials or R2 access."""
import argparse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import sys
from urllib.parse import parse_qs, urlsplit

import duckdb

SITE = Path(__file__).with_name('site')


def rows(db, sql, params=()):
    cursor = db.execute(sql, params)
    names = [column[0] for column in cursor.description]
    return [dict(zip(names, row)) for row in cursor.fetchall()]


def number(query, key, default, maximum):
    value = int(query.get(key, [default])[0])
    if value < 0 or value > maximum:
        raise ValueError('invalid pagination')
    return value


def filters(query, trace=False):
    clauses, params = [], []
    for key in ('agent', 'project'):
        value = query.get(key, [''])[0]
        if value:
            clauses.append('f.' + key + ' = ?')
            params.append(value)
    value = query.get('q', [''])[0]
    if value:
        clauses.append(('contains(lower(e.text), lower(?))' if trace else
                        '(contains(lower(f.file_key), lower(?)) OR contains(lower(f.session_key), lower(?)))'))
        params.extend([value] if trace else [value, value])
    if trace:
        role = query.get('role', [''])[0]
        if role == 'human':
            clauses.append('e.is_human AND NOT e.duplicate')
        elif role == 'tool':
            clauses.append("e.event_type IN ('tool_call','tool_result')")
        elif role == 'assistant':
            clauses.append("e.role='assistant' AND e.event_type IN ('message','thinking')")
        elif role:
            clauses.append('e.role = ?')
            params.append(role)
    return clauses, params


class Dashboard(ThreadingHTTPServer):
    daemon_threads = True

    def __init__(self, address, database, report=None, source='legacy'):
        self.database = str(database)
        self.report = Path(report) if report is not None else None
        self.publications = None
        if source == 'publications':
            if report is not None:
                raise ValueError('publications does not accept a legacy report')
            from publication_dashboard import PublicationDashboard
            # Fail closed on unsupported projections before binding even a loopback port.
            self.publications = PublicationDashboard(database)
        elif source != 'legacy':
            raise ValueError('unknown source')
        super().__init__(address, Handler)


class Handler(BaseHTTPRequestHandler):
    def log_message(self, format, *args):
        # Requests may contain private search text; do not persist access logs.
        pass

    def send_data(self, data, mime='application/json; charset=utf-8', status=200):
        payload = data if isinstance(data, bytes) else json.dumps(data, default=str, ensure_ascii=False).encode()
        self.send_response(status)
        self.send_header('Content-Type', mime)
        self.send_header('Content-Length', str(len(payload)))
        self.send_header('Cache-Control', 'no-store')
        self.send_header('X-Content-Type-Options', 'nosniff')
        self.send_header('Referrer-Policy', 'no-referrer')
        self.send_header('Content-Security-Policy', "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
        self.end_headers()
        self.wfile.write(payload)

    def do_GET(self):
        # Reject DNS rebinding and cross-site requests to private transcript APIs.
        host = self.headers.get('Host', '')
        allowed = {'127.0.0.1:' + str(self.server.server_port), 'localhost:' + str(self.server.server_port)}
        origin = self.headers.get('Origin')
        if (host not in allowed or self.headers.get('Sec-Fetch-Site') == 'cross-site' or
                (origin is not None and origin not in {'http://' + item for item in allowed})):
            self.send_data({'error': 'loopback requests only'}, status=403)
            return
        parsed = urlsplit(self.path)
        query = parse_qs(parsed.query)
        try:
            if self.server.publications is not None and parsed.path.startswith('/api/'):
                adapter = self.server.publications
                methods = {'/api/report': lambda: adapter.report(),
                           '/api/sessions': lambda: adapter.sessions(query),
                           '/api/trace': lambda: adapter.trace(query),
                           '/api/event': lambda: adapter.event(query),
                           '/api/issues': lambda: adapter.issues(query)}
                if parsed.path not in methods:
                    raise ValueError('unknown API')
                self.send_data(methods[parsed.path]())
                return
            if parsed.path == '/api/report':
                self.send_data(self.server.report.read_bytes())
                return
            if parsed.path.startswith('/api/'):
                with duckdb.connect(self.server.database, read_only=True) as db:
                    db.execute("SET TimeZone = 'UTC'")
                    self.send_data(self.api(db, parsed.path, query))
                return
            name = {'/': 'index.html', '/index.html': 'index.html', '/app.js': 'app.js', '/style.css': 'style.css'}.get(parsed.path)
            if not name:
                self.send_data({'error': 'not found'}, status=404)
                return
            mime = {'html': 'text/html', 'js': 'text/javascript', 'css': 'text/css'}[name.rsplit('.', 1)[1]]
            self.send_data((SITE / name).read_bytes(), mime + '; charset=utf-8')
        except (ValueError, KeyError):
            self.send_data({'error': 'invalid request or missing trace'}, status=400)
        except (OSError, duckdb.Error):
            # Do not expose filesystem paths, SQL, or private text in error messages.
            self.send_data({'error': 'data unavailable; rebuild the report and check the database'}, status=503)

    def api(self, db, path, query):
        if path == '/api/issues':
            return {'items': rows(db, 'SELECT i.*,f.agent,f.file_key FROM issues i JOIN files f USING(trace_id) ORDER BY i.kind,i.trace_id,i.line LIMIT 500')}
        if path == '/api/sessions':
            clauses, params = filters(query)
            where = ' WHERE ' + ' AND '.join(clauses) if clauses else ''
            limit = max(1, number(query, 'limit', 50, 200))
            offset = number(query, 'offset', 0, 10000000)
            total = db.execute('SELECT count(*) FROM files f' + where, params).fetchone()[0]
            items = rows(db, '''SELECT f.*, coalesce(s.events,0) events, coalesce(s.humans,0) humans,
                coalesce(s.tools,0) tools, coalesce(s.errors,0) errors FROM files f
                LEFT JOIN (SELECT trace_id, count(*) events, count(DISTINCT line) FILTER(WHERE is_human AND NOT duplicate) humans,
                count(*) FILTER(WHERE event_type='tool_call' AND NOT duplicate) tools,
                count(*) FILTER(WHERE is_error IS TRUE AND NOT duplicate) errors FROM events GROUP BY trace_id) s USING(trace_id)'''
                         + where + ' ORDER BY f.started_at DESC NULLS LAST, f.trace_id LIMIT ? OFFSET ?', params + [limit, offset])
            return {'total': total, 'items': items, 'offset': offset}
        trace = query.get('id', query.get('trace', ['']))[0]
        files = rows(db, 'SELECT * FROM files WHERE trace_id = ?', [trace])
        if not files:
            raise KeyError('unknown trace')
        file = files[0]
        if path == '/api/event':
            line = number(query, 'line', 1, 100000000)
            positions = rows(db, 'SELECT byte_offset, byte_length FROM events WHERE trace_id=? AND line=? LIMIT 1', [trace, line])
            if not positions:
                positions = rows(db, 'SELECT "offset" AS byte_offset,length AS byte_length FROM issues WHERE trace_id=? AND line=? LIMIT 1', [trace,line])
            if not positions:
                raise KeyError('unknown event')
            position = positions[0]
            with Path(file['path']).open('rb') as stream:
                stream.seek(position['byte_offset'])
                data = stream.read(position['byte_length'])
            try:
                raw = json.loads(data)
                valid = True
            except (ValueError, UnicodeDecodeError):
                raw, valid = data.decode('utf-8', errors='replace'), False
            return {'line': line, 'raw': raw, 'raw_text': data.decode('utf-8', errors='replace'), 'valid_json': valid}
        if path != '/api/trace':
            raise ValueError('unknown API')
        clauses, params = filters(query, trace=True)
        clauses.insert(0, 'e.trace_id = ?')
        params.insert(0, trace)
        where = ' WHERE ' + ' AND '.join(clauses)
        limit = max(1, number(query, 'limit', 60, 200))
        offset = number(query, 'offset', 0, 10000000)
        focus = number(query, 'line', 0, 100000000)
        if focus:
            # Centre around the cited source line, not its position in a filtered page.
            before = db.execute('SELECT count(*) FROM events e JOIN files f USING(trace_id)' + where + ' AND e.line < ?', params + [focus]).fetchone()[0]
            offset = max(0, before - min(5, limit // 2))
        total = db.execute('SELECT count(*) FROM events e JOIN files f USING(trace_id)' + where, params).fetchone()[0]
        items = rows(db, 'SELECT e.* FROM events e JOIN files f USING(trace_id)' + where + ' ORDER BY e.line,e.ordinal LIMIT ? OFFSET ?', params + [limit, offset])
        return {'file': file, 'total': total, 'offset': offset, 'items': items, 'focus_line': focus}


def main():
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source', choices=('legacy', 'publications'), default='legacy')
    parser.add_argument('--database', type=Path)
    parser.add_argument('--report', type=Path)
    parser.add_argument('--port', type=int, default=8765)
    args = parser.parse_args()
    if args.source == 'publications' and args.report is not None:
        parser.error('--report is legacy-only; publications serves the read-only projection')
    args.database = args.database or Path('.analytics/publications.duckdb' if args.source == 'publications' else '.analytics/events.duckdb')
    if args.source == 'legacy':
        args.report = args.report or Path('.analytics/report/report.json')
    if not args.database.is_file() or (args.report is not None and not args.report.is_file()):
        parser.error('build the private report first (see tools/analytics/README.md)')
    if args.source == 'publications':
        try:
            from publication_dashboard import PublicationDashboard
            PublicationDashboard(args.database)
        except (ValueError, OSError, duckdb.Error):
            parser.error('unsupported or unavailable publication projection')
    try:
        with Dashboard(('127.0.0.1', args.port), args.database, args.report, source=args.source) as server:
            print(f'Private session observatory: http://127.0.0.1:{server.server_port} (Ctrl-C to stop)', flush=True)
            server.serve_forever()
    except KeyboardInterrupt:
        return 0
    except OSError:
        print('Cannot bind the loopback port; choose another --port.', file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())
