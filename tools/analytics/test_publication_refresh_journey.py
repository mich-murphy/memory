"""Actual two independent machine queues -> status -> reader -> refresh -> report.

No handwritten reader exports. Mandatory capture-acceptance supplies the binary.
"""
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import time
import unittest

import publications


@unittest.skipUnless(os.environ.get('CAPTURE_V1_BINARY'), 'run make capture-acceptance')
class PublicationRefreshJourney(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        self.binary = str(Path(os.environ['CAPTURE_V1_BINARY']).resolve())
        self.db = self.root / 'analytics.duckdb'
        self.status = self.root / 'refresh.sqlite'
        self.endpoints_path = self.root / 'endpoints.json'
        self.endpoints = []
        for machine in ('Mac', 'BC01'):
            root = self.root / machine
            root.mkdir(mode=0o700)
            (root / 'home').mkdir(mode=0o700)
            (root / 'policy' / 'exclusions').mkdir(parents=True, mode=0o700)
            config = dict(version='capture-runtime/v1', archive_id='refresh-journey', machine_id=machine,
                          store=str(root / 'store'), spool=str(root / 'spool'), policy_dir=str(root / 'policy'),
                          home=str(root / 'home'), default_roots=False, roots=[], min_free_bytes=0,
                          capture_workers=2, delivery_workers=2, poll_seconds=1, operation_seconds=30,
                          max_attempts=2, max_spool_bytes=1 << 24, entries_per_root=256)
            path = root / 'runtime.json'
            path.write_text(json.dumps(config))
            path.chmod(0o600)
            self.endpoints.append(dict(name=machine, binary=self.binary, config=str(path)))
            self.writer('init', '--store', str(root / 'store'), '--archive', 'refresh-journey')
        self.save_endpoints()

    def save_endpoints(self):
        self.endpoints_path.write_text(json.dumps({'contract': 'session-refresh-endpoints/v1', 'endpoints': self.endpoints}))
        self.endpoints_path.chmod(0o600)

    def writer(self, *args):
        result = subprocess.run([self.binary, 'capture-v1', *args], stdout=subprocess.PIPE,
                                stderr=subprocess.DEVNULL, timeout=60, check=False)
        self.assertEqual(result.returncode, 0, 'writer failed; private diagnostics withheld')
        return result

    def capture(self, machine, session, *, append=False):
        root = self.root / machine
        source = root / (session + '.jsonl')
        first = '{"type":"user","uuid":"' + machine + session + '","message":{"role":"user","content":"Synthetic refresh"}}\n'
        content = first + ('{"type":"assistant","uuid":"next","message":{"role":"assistant","content":"Synthetic append"}}\n' if append else '')
        source.write_text(content)
        common = ['--archive', 'refresh-journey', '--machine', machine, '--agent', 'claude',
                  '--session', 'root/' + session, '--spool', str(root / 'spool'), '--policy-dir', str(root / 'policy')]
        self.writer('capture', *common, '--source', 'main', '--path', str(source))
        self.writer('deliver', *common, '--store', str(root / 'store'))
        return content

    def refresh(self, *, succeeds=True):
        command = [sys.executable, str(Path(__file__).with_name('publication_refresh.py')),
                   '--endpoints', str(self.endpoints_path), '--database', str(self.db),
                   '--status', str(self.status), '--once', '--timeout', '10']
        result = subprocess.run(command, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=60)
        self.assertEqual(result.returncode == 0, succeeds, 'refresh exit mismatch; private diagnostics withheld')
        self.assertEqual(result.stderr, b'', 'refresh printed private diagnostics')
        report = json.loads(result.stdout)
        self.assertEqual(report['contract'], 'session-publication-refresh-status/v1')
        return report

    def test_two_machine_full_sweeps_restart_outage_rebuild_and_autonomous_poll(self):
        for machine in ('Mac', 'BC01'):
            for session in ('a', 'b'):
                self.capture(machine, session)
        # Originals are not available to the reader/consumer.
        for source in self.root.glob('*/*.jsonl'):
            source.unlink()
        report = self.refresh()
        self.assertEqual((report['expected_targets'], report['unimported']), (4, 0))
        first = publications.report(self.db)
        self.assertEqual(len(first['sessions']), 4)
        self.assertEqual({s['machine_id'] for s in first['sessions']}, {'Mac', 'BC01'})
        self.refresh()
        self.assertEqual([s['imported_at_ns'] for s in publications.report(self.db)['sessions']],
                         [s['imported_at_ns'] for s in first['sessions']])
        # A healthy unchanged projection must not download a reader export.
        # Same-size corruption and missing owned bodies must force repair.
        import publication_refresh as refresh
        from unittest.mock import patch
        import contextlib
        with contextlib.closing(refresh.open_status(self.status)) as status:
            with patch.object(refresh, 'export', wraps=refresh.export) as exporter:
                self.assertEqual(refresh.sweep(self.endpoints, self.db, status)['target_errors'], 0)
                self.assertEqual(exporter.call_count, 0)
                import duckdb
                with duckdb.connect(str(self.db), read_only=True) as projection:
                    body = Path(projection.execute('SELECT path FROM sources ORDER BY path LIMIT 1').fetchone()[0])
                original = body.read_bytes()
                body.write_bytes(b'x' * len(original))
                self.assertEqual(refresh.sweep(self.endpoints, self.db, status)['target_errors'], 0)
                self.assertEqual(exporter.call_count, 1)
                self.assertEqual(body.read_bytes(), original)
                body.unlink()
                self.assertEqual(refresh.sweep(self.endpoints, self.db, status)['target_errors'], 0)
                self.assertEqual(exporter.call_count, 2)
                self.assertEqual(body.read_bytes(), original)
        # Enumeration failure on A is not a successful empty result, while B
        # imports another actual publication without manual one-session targeting.
        original_binary = self.endpoints[0]['binary']
        self.endpoints[0]['binary'] = '/nonexistent/synthetic-reader'
        self.save_endpoints()
        self.capture('BC01', '0-new')
        report = self.refresh(succeeds=False)
        self.assertEqual(report['endpoint_errors'], 1)
        self.assertEqual(len(publications.report(self.db)['sessions']), 5)
        self.endpoints[0]['binary'] = original_binary
        self.save_endpoints()
        # Rebuilding an empty projection cannot rely on durable imported markers.
        self.db.unlink()
        shutil.rmtree(self.db.with_name(self.db.name + '.bodies'))
        self.assertEqual(self.refresh()['expected_targets'], 5)
        self.assertEqual(len(publications.report(self.db)['sessions']), 5)
        # Autonomous mode discovers an advancing publication with no refresh hook.
        command = [sys.executable, str(Path(__file__).with_name('publication_refresh.py')),
                   '--endpoints', str(self.endpoints_path), '--database', str(self.db),
                   '--status', str(self.status), '--interval', '1', '--timeout', '10']
        import select
        import re
        import urllib.request
        server = subprocess.Popen([sys.executable, str(Path(__file__).with_name('serve.py')),
            '--source', 'publications', '--database', str(self.db), '--refresh-status', str(self.status),
            '--port', '0'], stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True)
        def stop_server():
            if server.poll() is None:
                server.terminate()
            server.communicate(timeout=15)
        self.addCleanup(stop_server)
        self.assertTrue(select.select([server.stdout], [], [], 10)[0], 'dashboard startup timeout')
        match = re.search(r'http://127\.0\.0\.1:\d+', server.stdout.readline())
        self.assertIsNotNone(match)
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        process = subprocess.Popen(command, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        try:
            self.capture('BC01', 'b', append=True)
            # Poll independent durable consumer status, avoiding DuckDB writer contention.
            import sqlite3
            deadline = time.monotonic() + 30
            while time.monotonic() < deadline:
                self.assertIsNone(process.poll(), 'autonomous refresh exited')
                with opener.open(match.group() + '/api/report', timeout=15) as response:
                    health = json.load(response)['refresh_health']
                    self.assertEqual(health['contract'], 'session-publication-refresh-health/v1')
                with contextlib.closing(sqlite3.connect(self.status)) as status:
                    sequences = status.execute('SELECT imported_sequence FROM targets WHERE endpoint=?', ('BC01',)).fetchall()
                if (2,) in sequences:
                    break
                time.sleep(.1)
            else:
                self.fail('autonomous refresh did not import advancing publication')
        finally:
            process.terminate()
            process.wait(timeout=15)
        current = publications.report(self.db)
        advanced = next(s for s in current['sessions'] if s['machine_id'] == 'BC01' and s['session_id'] == 'root/b')
        self.assertEqual(advanced['sequence'], 2)
        import duckdb
        with duckdb.connect(str(self.db), read_only=True) as db:
            self.assertEqual(dict(db.execute('SELECT role,count(*) FROM events GROUP BY role').fetchall()),
                             {'assistant': 1, 'user': 5})


if __name__ == '__main__':
    unittest.main()
