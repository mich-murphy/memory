"""Synthetic importer -> existing HTTP/query entrypoint; no host sources or services."""
import hashlib
import http.client
import json
from pathlib import Path
import select
import shutil
import subprocess
import sys
import tempfile
import threading
import unittest
from urllib.parse import urlencode
from unittest.mock import patch

import duckdb
import publications
from publication_dashboard import PublicationDashboard
from serve import Dashboard


class PublicationDashboardTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.database = self.root / 'publications.duckdb'
        export = self.root / 'export'
        export.mkdir()
        self.raw = ('{"type":"user","message":{"role":"user","content":"hello 🦆"}}\n'
                    '{"type":"assistant","message":{"role":"assistant","content":"<script>x</script>"}}\n'
                    'broken 🦆 JSON\n').encode()
        sources, bodies = [], []
        for n, (source, epoch, role, raw) in enumerate([
                ('source', 'epoch', 'main', self.raw),
                ('source', 'rewrite', 'main', self.raw),
                ('opaque', 'backup', 'artifact', b'SQLite format 3\x00\xff')]):
            digest = hashlib.sha256(raw).hexdigest()
            name = str(n) + '.bin'
            (export / name).write_bytes(raw)
            sources.append(dict(source_id=source, epoch_id=epoch, role=role,
                path='/NEVER-OPEN-OR-GUESS-PROJECT', state='present', revision=1,
                observed_at_ns=0, captured_at_ns=0, associations=[],
                object=dict(key='object', encoding='zstd', sha256=digest,
                            encoded_sha256=digest, size=len(raw), encoded_size=len(raw))))
            bodies.append(dict(source_id=source, epoch_id=epoch, path=name, sha256=digest, size=len(raw)))
        sources.append(dict(source_id='', epoch_id='private', role='artifact', path='',
            state='present', revision=2, observed_at_ns=0, captured_at_ns=0, associations=[],
            optional=True, withheld=dict(source_id_hash='a' * 64, snapshot_commitment='b' * 64, reason='policy')))
        manifest = dict(contract='session-publication/v2', identity=dict(archive_id='archive',
            machine_id='machine', agent='claude', session_id='session'), publication_id='p1', sequence=1,
            observed_at_ns=1700000000000000001, captured_at_ns=1700000000000000002,
            published_at_ns=0, coverage=dict(discovery='unknown', enrichment='partial', missing=['source-withheld']),
            sources=sources)
        (export / 'metadata.json').write_text(json.dumps(dict(contract='session-reader/v2', manifest=manifest, bodies=bodies)))
        publications.import_export(export, self.database)
        shutil.rmtree(export)  # Serving cannot fall back to transient exports or originals.
        self.adapter = PublicationDashboard(self.database)
        self.server = Dashboard(('127.0.0.1', 0), self.database, source='publications')
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.addCleanup(self.stop_server)

    def stop_server(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join()

    def request(self, path, headers=None, port=None):
        conn = http.client.HTTPConnection('127.0.0.1', port or self.server.server_port, timeout=5)
        try:
            conn.request('GET', path, headers=headers or {})
            res = conn.getresponse()
            return res.status, dict(res.getheaders()), res.read()
        finally:
            conn.close()

    def api(self, endpoint, **params):
        status, _, raw = self.request('/api/' + endpoint + '?' + urlencode(params))
        self.assertEqual(status, 200, raw)
        return json.loads(raw)

    def test_actual_report_and_source_epoch_navigation(self):
        with patch.object(publications, 'import_export', side_effect=AssertionError('GET must not import')):
            report = self.api('report')
        self.assertTrue(report['dashboard_wired'])
        self.assertFalse(publications.report(self.database)['dashboard_wired'])
        self.assertEqual(report['contract'], 'session-analytics-report/v2')
        self.assertEqual(report['freshness']['published_at_ns'], '0')
        self.assertEqual(report['freshness']['captured_at_ns'], '1700000000000000002')
        self.assertIn('unavailable', report['classification_status'])
        data = self.api('sessions', limit=2, offset=1)
        self.assertEqual((data['total'], data['offset'], len(data['items'])), (4, 1, 2))
        sources = self.api('sessions')['items']
        mains = [s for s in sources if s['source_role'] == 'main']
        self.assertEqual(len({s['trace_id'] for s in mains}), 2)
        for source in sources:
            self.assertIsNone(source['project'])
            self.assertNotIn('path', source)
            self.assertNotIn('original_path', source)
            self.assertEqual(source['archive_id'], 'archive')
            self.assertEqual(source['machine_id'], 'machine')
        trace = self.api('trace', id=mains[0]['trace_id'], line=2, limit=1)
        self.assertEqual((trace['focus_line'], trace['items'][0]['line']), (2, 2))
        self.assertEqual(self.api('trace', id=mains[0]['trace_id'], role='human')['total'], 1)
        self.assertEqual(self.api('sessions', q="' OR TRUE--")['total'], 0)
        self.assertEqual(self.api('sessions', project='NEVER')['total'], 0)

    def test_unicode_malformed_raw_and_owned_integrity(self):
        source = next(s for s in self.api('sessions')['items'] if s['source_role'] == 'main')
        trace = source['trace_id']
        raw = self.api('event', trace=trace, line=2)
        self.assertIn('<script>x</script>', raw['raw_text'])
        bad = self.api('event', trace=trace, line=3)
        self.assertFalse(bad['valid_json'])
        self.assertEqual(bad['raw_text'], 'broken 🦆 JSON\n')
        issues = self.api('issues', trace=trace, limit=1)
        self.assertEqual(len(issues['items']), 1)
        body = self.database.with_name(self.database.name + '.bodies') / source['sha256']
        body.write_bytes(b'x' * body.stat().st_size)
        self.assertEqual(self.request('/api/event?' + urlencode(dict(trace=trace, line=1)))[0], 503)

    def test_raw_rejects_unowned_paths_symlinks_and_large_lines(self):
        source = next(s for s in self.api('sessions')['items'] if s['source_role'] == 'main')
        trace = source['trace_id']
        url = '/api/event?' + urlencode(dict(trace=trace, line=1))
        # No original path fallback, even if an operator-corrupted DB points at real bytes.
        outside = self.root / 'outside'
        outside.write_bytes(self.raw)
        with duckdb.connect(str(self.database)) as db:
            db.execute('UPDATE sources SET path=? WHERE trace_id=?', [str(outside), trace])
        self.assertEqual(self.request(url)[0], 503)
        body = self.database.with_name(self.database.name + '.bodies') / source['sha256']
        with duckdb.connect(str(self.database)) as db:
            db.execute('UPDATE sources SET path=? WHERE trace_id=?', [str(body.resolve()), trace])
        body.unlink()
        body.symlink_to(outside)
        self.assertEqual(self.request(url)[0], 503)
        body.unlink()
        body.write_bytes(self.raw)
        with duckdb.connect(str(self.database)) as db:
            db.execute('UPDATE events SET byte_length=1048577 WHERE trace_id=? AND line=1', [trace])
        self.assertEqual(self.request(url)[0], 400)

    def test_withheld_and_binary_never_expose_raw_even_with_retained_cache(self):
        # A retained hash-named file is not selection permission.
        (self.database.with_name(self.database.name + '.bodies') / ('a' * 64)).write_bytes(b'PRIVATE')
        for source in self.api('sessions')['items']:
            if source['source_role'] == 'artifact':
                self.assertEqual(self.request('/api/event?' + urlencode(dict(trace=source['trace_id'], line=0)))[0], 400)
                self.assertEqual(self.api('trace', id=source['trace_id'])['total'], 0)
        self.assertNotIn(b'PRIVATE', self.request('/api/report')[2])

    def test_security_and_resource_bounds(self):
        for path in ('/../secret', '/api/sql?sql=SELECT%201', '/tools/analytics/serve.py'):
            self.assertIn(self.request(path)[0], (400, 404))
        for headers in ({'Host': 'evil.example'}, {'Sec-Fetch-Site': 'cross-site'}):
            self.assertEqual(self.request('/api/report', headers)[0], 403)
        for params in ('limit=-1', 'limit=201', 'offset=10000001', 'limit=garbage'):
            self.assertEqual(self.request('/api/sessions?' + params)[0], 400)
        status, headers, script = self.request('/app.js')
        self.assertEqual(status, 200)
        self.assertIn("frame-ancestors 'none'", headers['Content-Security-Policy'])
        self.assertEqual(headers['Cache-Control'], 'no-store')
        self.assertEqual(headers['X-Content-Type-Options'], 'nosniff')
        self.assertNotIn(b'innerHTML', script)
        self.assertIn(b'session-observatory-reviews-v1', script)
        self.assertIn(b'BigInt(value) / 1000000n', script)

    def run_cli(self, name, *args, cwd=None):
        return subprocess.run([sys.executable, str(Path(__file__).with_name(name)), *args], cwd=cwd,
                              capture_output=True, timeout=15)

    def test_existing_query_entrypoint_publication_mode_and_readonly(self):
        result = self.run_cli('query.py', '--source', 'publications', '--database', str(self.database),
                              '--sql', 'SELECT archive_id FROM sessions')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn(b'archive', result.stdout)
        for sql in (None, 'DELETE FROM sessions'):
            args = ['--source', 'publications', '--database', str(self.database)]
            if sql:
                args += ['--sql', sql]
            self.assertNotEqual(self.run_cli('query.py', *args).returncode, 0)
        self.assertEqual(len(publications.report(self.database)['sessions']), 1)

    def test_actual_server_main_and_separate_default(self):
        local = self.root / '.analytics'
        local.mkdir()
        shutil.copy2(self.database, local / 'publications.duckdb')
        # No legacy report or database exists here.
        proc = subprocess.Popen([sys.executable, str(Path(__file__).with_name('serve.py')),
            '--source', 'publications', '--port', '0'], cwd=self.root,
            stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        try:
            ready, _, _ = select.select([proc.stdout], [], [], 10)
            self.assertTrue(ready, 'synthetic server failed to start')
            line = proc.stdout.readline().decode()
            self.assertIn('http://127.0.0.1:', line)
            port = int(line.split('http://127.0.0.1:')[1].split(' ')[0])
            status, _, body = self.request('/api/report', port=port)
            self.assertEqual(status, 200)
            self.assertTrue(json.loads(body)['dashboard_wired'])
        finally:
            proc.terminate()
            proc.communicate(timeout=10)
        self.assertNotEqual(self.run_cli('serve.py', '--source', 'publications', '--database', str(self.database),
                                        '--report', str(self.root / 'legacy.json')).returncode, 0)

    def test_invalid_and_old_projection_rejected_before_bind_nonmutating(self):
        for variant in ('old', 'legacy', 'malformed'):
            database = self.root / (variant + '.duckdb')
            with duckdb.connect(str(database)) as db:
                if variant != 'legacy':
                    db.execute('CREATE TABLE publication_binding(version INTEGER,archive_id VARCHAR)')
                    db.execute("INSERT INTO publication_binding VALUES (?, 'old')", [1 if variant == 'old' else 2])
                else:
                    db.execute('CREATE TABLE files(trace_id VARCHAR)')
            before = database.read_bytes()
            with patch('serve.ThreadingHTTPServer.__init__', side_effect=AssertionError('must not bind')):
                with self.assertRaises((ValueError, duckdb.Error)):
                    Dashboard(('127.0.0.1', 0), database, source='publications')
            self.assertEqual(database.read_bytes(), before)
            result = self.run_cli('serve.py', '--source', 'publications', '--database', str(database), '--port', '0')
            self.assertNotEqual(result.returncode, 0)
            self.assertNotIn(b'Traceback', result.stderr)
            self.assertEqual(database.read_bytes(), before)


if __name__ == '__main__':
    unittest.main()
