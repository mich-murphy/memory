"""Mandatory gate with CAPTURE_V1_BINARY; fixtures come from the actual Go writer.

Generic historical analytics discovery may skip when no candidate binary was built.
`make capture-acceptance` always supplies the binary and cannot skip this journey.
"""
import contextlib
import hashlib
import json
import os
from pathlib import Path
import re
import select
import subprocess
import sys
import tempfile
import unittest
import urllib.error
import urllib.parse
import urllib.request

import duckdb
import publications


@unittest.skipUnless(os.environ.get('CAPTURE_V1_BINARY'), 'run make capture-acceptance for writer/consumer acceptance')
class CaptureJourney(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.binary = os.environ['CAPTURE_V1_BINARY']
        self.fixture = self.root / 'fixture'
        self.run_writer('fixture', '--output', str(self.fixture))
        self.export = self.fixture / 'export'
        self.metadata = json.loads((self.export / 'metadata.json').read_text())
        self.expected = json.loads((self.fixture / 'expectations.json').read_text())
        self.db = self.root / 'analytics.duckdb'

    def run_writer(self, *args, succeeds=True):
        process = subprocess.run([self.binary, 'capture-v1', *args],
                                 stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                 timeout=60, check=False)
        self.assertEqual(process.returncode == 0, succeeds,
                         'writer command failed (diagnostics withheld from transcript)')
        return process

    def test_actual_capture_publication_reader_event_index_report(self):
        manifest = self.metadata['manifest']
        self.assertEqual(self.metadata['contract'], 'session-reader/v2')
        self.assertEqual(manifest['identity'], self.expected['identity'])
        self.assertEqual(manifest['sequence'], 4)
        self.assertEqual(len(manifest['sources']), 3)
        self.assertEqual(len(self.metadata['bodies']), 3)
        expected_hashes = sorted(hashlib.sha256(x.encode()).hexdigest()
                                 for x in self.expected['retained_bodies'])
        self.assertEqual(sorted(x['sha256'] for x in self.metadata['bodies']), expected_hashes)
        for body in self.metadata['bodies']:
            content = (self.export / body['path']).read_bytes()
            self.assertEqual(hashlib.sha256(content).hexdigest(), body['sha256'])
            self.assertEqual(len(content), body['size'])
        for source in manifest['sources']:
            self.assertEqual(source['role'], self.expected['roles'][source['source_id']])
        main_epochs = [x['epoch_id'] for x in manifest['sources'] if x['source_id'] == 'main']
        self.assertEqual(len(set(main_epochs)), 2, 'rewrite must retain earlier captured epoch')
        publications.import_export(self.export, self.db)
        with duckdb.connect(str(self.db), read_only=True) as conn:
            before = conn.execute('SELECT event_id FROM events ORDER BY event_id').fetchall()
            roles = dict(conn.execute('SELECT role,count(*) FROM events GROUP BY role').fetchall())
            self.assertEqual(roles, {'user': 2, 'assistant': 3, 'tool': 1})
            self.assertEqual(conn.execute('SELECT count(*) FROM issues').fetchone()[0], 3)
            self.assertEqual(conn.execute('SELECT count(DISTINCT epoch_id) FROM events WHERE source_id=?', ['main']).fetchone()[0], 2)
        publications.import_export(self.export, self.db)
        with duckdb.connect(str(self.db), read_only=True) as conn:
            after = conn.execute('SELECT event_id FROM events ORDER BY event_id').fetchall()
        self.assertEqual(before, after, 'same publication import must be idempotent')
        result = publications.report(self.db, now_ns=1_700_000_001_000_000_000)
        self.assertEqual(result['contract'], 'session-analytics-report/v2')
        self.assertEqual(result['archive_id'], manifest['identity']['archive_id'])
        self.assertEqual(result['failures'], [])
        self.assertEqual(len(result['sessions']), 1)
        summary = result['sessions'][0]
        self.assertEqual(summary['publication_id'], manifest['publication_id'])
        self.assertEqual(summary['coverage'], manifest['coverage'])
        self.assertEqual(summary['normalization'], 'partial')
        self.assertEqual(summary['captured_at_ns'], manifest['captured_at_ns'])
        self.assertEqual(summary['published_at_ns'], manifest['published_at_ns'])
        self.assertEqual(summary['published_at_ns'], 0, 'unknown commit time must not be fabricated from capture time')
        self.assertGreater(summary['imported_at_ns'], 0)
        self.assertEqual(result['source_roles'], [{'source_role': 'main', 'sources': 2},
                                                 {'source_role': 'subagent', 'sources': 1}])
        self.assertEqual(result['issue_counts'], [{'kind': 'incomplete_tail', 'issues': 1},
                                                 {'kind': 'malformed_json', 'issues': 2}])
        # No source/spool access is needed by restoration or analytics.
        import shutil
        shutil.rmtree(self.fixture / 'sources')
        shutil.rmtree(self.fixture / 'spool')
        identity = manifest['identity']
        restored = self.root / 'restored'
        self.run_writer('export', '--store', str(self.fixture / 'store'),
                        '--archive', identity['archive_id'], '--machine', identity['machine_id'],
                        '--agent', identity['agent'], '--session', identity['session_id'],
                        '--output', str(restored))
        restored_meta = json.loads((restored / 'metadata.json').read_text())
        self.assertEqual(restored_meta['manifest']['publication_id'], manifest['publication_id'])
        for body in restored_meta['bodies']:
            self.assertEqual(hashlib.sha256((restored / body['path']).read_bytes()).hexdigest(), body['sha256'])
        publications.import_export(restored, self.db)
        with duckdb.connect(str(self.db), read_only=True) as conn:
            self.assertEqual(conn.execute('SELECT event_id FROM events ORDER BY event_id').fetchall(), before)

    def test_sdk_factory_writer_reader_import_without_sources_or_spool(self):
        fixture = self.root / 'sdk-fixture'
        self.run_writer('fixture', '--backend', 'sdk-fixture', '--output', str(fixture))
        self.assertFalse((fixture / 'sources').exists())
        self.assertFalse((fixture / 'spool').exists())
        export = fixture / 'export'
        metadata = json.loads((export / 'metadata.json').read_text())
        expected = json.loads((fixture / 'expectations.json').read_text())
        self.assertEqual(metadata['contract'], 'session-reader/v2')
        self.assertEqual(metadata['manifest']['identity'], expected['identity'])
        self.assertEqual(metadata['manifest']['sequence'], 4)
        self.assertEqual(sorted(body['sha256'] for body in metadata['bodies']),
                         sorted(hashlib.sha256(body.encode()).hexdigest()
                                for body in expected['retained_bodies']))
        for body in metadata['bodies']:
            raw = (export / body['path']).read_bytes()
            self.assertEqual(hashlib.sha256(raw).hexdigest(), body['sha256'])
            self.assertEqual(len(raw), body['size'])
        publications.import_export(export, self.db)
        first = publications.report(self.db)
        with duckdb.connect(str(self.db), read_only=True) as conn:
            events = conn.execute('SELECT event_id FROM events ORDER BY event_id').fetchall()
            self.assertEqual(dict(conn.execute('SELECT role,count(*) FROM events GROUP BY role').fetchall()),
                             {'user': 2, 'assistant': 3, 'tool': 1})
            self.assertEqual(conn.execute('SELECT count(DISTINCT epoch_id) FROM events WHERE source_id=?',
                                          ['main']).fetchone()[0], 2)
        publications.import_export(export, self.db)
        second = publications.report(self.db)
        self.assertEqual(second['sessions'][0]['imported_at_ns'], first['sessions'][0]['imported_at_ns'])
        self.assertEqual(second['sessions'][0]['publication_id'], metadata['manifest']['publication_id'])
        self.assertEqual(second['sessions'][0]['published_at_ns'], 0)
        self.assertEqual(second['sessions'][0]['normalization'], 'partial')
        self.assertEqual(second['failures'], [])
        with duckdb.connect(str(self.db), read_only=True) as conn:
            self.assertEqual(conn.execute('SELECT event_id FROM events ORDER BY event_id').fetchall(), events)

    def test_existing_dashboard_entry_point_serves_actual_reader_projection(self):
        publications.import_export(self.export, self.db)
        import shutil
        shutil.rmtree(self.fixture)  # UI has only owned projection evidence, no reader exports or store.
        historical = self.root / 'historical-report.json'
        historical.write_text('{"frozen_research":"synthetic"}')
        frozen_hash = hashlib.sha256(historical.read_bytes()).hexdigest()
        command = [sys.executable, str(Path(__file__).with_name('serve.py')),
                   '--source', 'publications', '--database', str(self.db), '--port', '0']
        process = subprocess.Popen(command, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        def stop():
            if process.poll() is None:
                process.terminate()
            process.communicate(timeout=10)
        self.addCleanup(stop)
        self.assertTrue(select.select([process.stdout], [], [], 10)[0], 'dashboard did not bind promptly')
        match = re.search(r'http://127\.0\.0\.1:\d+', process.stdout.readline())
        self.assertIsNotNone(match, 'dashboard entry point did not announce a loopback URL')
        base = match.group()
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        def get(path, **headers):
            request = urllib.request.Request(base + path, headers=headers)
            with opener.open(request, timeout=10) as response:
                self.assertEqual(response.headers['X-Content-Type-Options'], 'nosniff')
                return json.load(response)
        report = get('/api/report')
        self.assertEqual(report['contract'], 'session-analytics-report/v2')
        self.assertTrue(report['dashboard_wired'])
        self.assertEqual(report['source'], 'publications')
        self.assertEqual(report['archive_id'], self.expected['identity']['archive_id'])
        self.assertEqual(report['sessions'][0]['publication_id'], self.metadata['manifest']['publication_id'])
        self.assertEqual(report['sessions'][0]['published_at_ns'], '0')
        self.assertIn('unavailable', report['classification_status'])
        listing = get('/api/sessions?limit=200')
        self.assertEqual(listing['total'], 3)
        self.assertEqual(sorted(item['source_role'] for item in listing['items']), ['main', 'main', 'subagent'])
        self.assertEqual(len({item['epoch_id'] for item in listing['items'] if item['source_id'] == 'main'}), 2)
        event_ids, unicode_seen = [], False
        for item in listing['items']:
            self.assertIsNone(item['project'])
            self.assertNotIn('original_path', item)
            self.assertNotIn('path', item)
            trace = get('/api/trace?' + urllib.parse.urlencode({'id': item['trace_id']}))
            event_ids.extend(event['event_id'] for event in trace['items'])
            if item['source_role'] == 'main':
                detail = get('/api/event?' + urllib.parse.urlencode({'trace': item['trace_id'], 'line': 1}))
                unicode_seen |= 'π' in detail['raw_text']
        self.assertTrue(unicode_seen, 'exact Unicode raw offsets did not survive actual dashboard caller')
        with duckdb.connect(str(self.db), read_only=True) as conn:
            self.assertEqual(sorted(event_ids), sorted(row[0] for row in conn.execute('SELECT event_id FROM events').fetchall()))
        issues = get('/api/issues?limit=200')
        malformed = next(issue for issue in issues['items'] if issue['kind'] == 'malformed_json')
        detail = get('/api/event?' + urllib.parse.urlencode({'trace': malformed['trace_id'], 'line': malformed['line']}))
        self.assertFalse(detail['valid_json'])
        with self.assertRaises(urllib.error.HTTPError) as denied:
            get('/api/report', Origin='https://synthetic-untrusted.invalid')
        self.assertEqual(denied.exception.code, 403)
        self.assertEqual(hashlib.sha256(historical.read_bytes()).hexdigest(), frozen_hash)

    def test_optional_failure_crash_restart_repair_and_privacy_revoke(self):
        import shutil
        root = self.root / 'isolation'
        self.run_writer('isolation-fixture', '--output', str(root), '--stage', 'crash')
        expected = json.loads((root / 'expectations.json').read_text())
        withheld = root / 'export-withheld'
        first = json.loads((withheld / 'metadata.json').read_text())
        source = next(item for item in first['manifest']['sources'] if item.get('optional', False))
        self.assertEqual(source['withheld']['reason'], 'unavailable')
        self.assertNotIn('object', source)
        self.assertEqual(source['path'], '')
        self.assertEqual([(withheld / body['path']).read_text() for body in first['bodies']],
                         [expected['main_body']])
        publications.import_export(withheld, self.db)
        before = publications.report(self.db)
        self.assertIn({'kind': 'source_withheld_unavailable', 'issues': 1}, before['issue_counts'])
        with duckdb.connect(str(self.db), read_only=True) as conn:
            initial_events = conn.execute('SELECT event_id FROM events ORDER BY event_id').fetchall()
        shutil.rmtree(root / 'sources')
        # New process, no hook and no original sources: reclaim interrupted durable
        # repair and select it onto the main view published before the prior crash.
        self.run_writer('isolation-fixture', '--output', str(root), '--stage', 'repair')
        repaired = root / 'export-repaired'
        second = json.loads((repaired / 'metadata.json').read_text())
        artifact = next(item for item in second['manifest']['sources'] if item.get('optional', False))
        self.assertNotIn('withheld', artifact)
        self.assertEqual(artifact['object'], expected['artifact']['object'])
        self.assertEqual(artifact['captured_at_ns'], expected['artifact']['captured_at_ns'])
        self.assertEqual(sorted((repaired / body['path']).read_text() for body in second['bodies']),
                         sorted((expected['main_body'], expected['artifact_body'])))
        self.assertGreater(second['manifest']['sequence'], first['manifest']['sequence'])
        publications.import_export(repaired, self.db)
        with duckdb.connect(str(self.db), read_only=True) as conn:
            self.assertEqual(conn.execute('SELECT event_id FROM events ORDER BY event_id').fetchall(), initial_events)
        cache = self.db.with_name(self.db.name + '.bodies') / expected['artifact']['object']['sha256']
        self.assertTrue(cache.is_file())
        self.run_writer('isolation-fixture', '--output', str(root), '--stage', 'revoke')
        revoked = root / 'export-revoked'
        third = json.loads((revoked / 'metadata.json').read_text())
        artifact = next(item for item in third['manifest']['sources'] if item.get('optional', False))
        self.assertEqual(artifact['withheld']['reason'], 'policy')
        self.assertNotIn('object', artifact)
        self.assertEqual(artifact['path'], '')
        self.assertEqual(artifact['captured_at_ns'], expected['artifact']['captured_at_ns'])
        self.assertEqual([(revoked / body['path']).read_text() for body in third['bodies']],
                         [expected['revoked_body']])
        publications.import_export(revoked, self.db)
        report = publications.report(self.db)
        self.assertEqual(report['failures'], [])
        self.assertEqual(report['sessions'][0]['normalization'], 'partial')
        self.assertIn({'kind': 'source_withheld_policy', 'issues': 1}, report['issue_counts'])
        self.assertTrue(cache.is_file(), 'revocation deleted retained evidence instead of withholding')
        imported_at = report['sessions'][0]['imported_at_ns']
        publications.import_export(revoked, self.db)
        self.assertEqual(publications.report(self.db)['sessions'][0]['imported_at_ns'], imported_at)
        with duckdb.connect(str(self.db), read_only=True) as conn:
            self.assertEqual(dict(conn.execute('SELECT role,count(*) FROM events GROUP BY role').fetchall()),
                             {'user': 2, 'assistant': 1})
            self.assertEqual(conn.execute("SELECT count(*) FROM events e JOIN sources s USING(trace_id) WHERE s.source_role='artifact'").fetchone()[0], 0)

    def test_broken_a_does_not_block_restart_delivery_or_import_of_b(self):
        publications.import_export(self.export, self.db)
        first = publications.report(self.db)['sessions'][0]
        # A loses a selected remote dependency; reader must fail visibly. Its
        # previously imported success remains, with an explicit importer gap.
        source = self.metadata['manifest']['sources'][0]
        (self.fixture / 'store' / 'data' / source['object']['key']).unlink()
        identity = self.metadata['manifest']['identity']
        self.run_writer('export', '--store', str(self.fixture / 'store'),
                        '--archive', identity['archive_id'], '--machine', identity['machine_id'],
                        '--agent', identity['agent'], '--session', identity['session_id'],
                        '--output', str(self.root / 'broken-a'), succeeds=False)
        (self.export / self.metadata['bodies'][0]['path']).unlink()
        with self.assertRaises(publications.ImportFailure):
            publications.import_export(self.export, self.db)
        b_source = self.fixture / 'sources' / 'healthy-b.jsonl'
        b_source.write_text('{"type":"user","uuid":"b1","message":{"role":"user","content":"Synthetic B"}}\n')
        common = ['--archive', identity['archive_id'], '--machine', identity['machine_id'],
                  '--agent', 'claude', '--session', 'root/healthy-b',
                  '--spool', str(self.fixture / 'spool'), '--policy-dir', str(self.fixture / 'policy')]
        self.run_writer('capture', *common, '--source', 'main', '--path', str(b_source))
        b_source.unlink()
        # A new process delivers durable B without its original or another hook.
        self.run_writer('deliver', *common, '--store', str(self.fixture / 'store'))
        b_export = self.root / 'healthy-b'
        self.run_writer('export', '--store', str(self.fixture / 'store'),
                        '--archive', identity['archive_id'], '--machine', identity['machine_id'],
                        '--agent', 'claude', '--session', 'root/healthy-b', '--output', str(b_export))
        publications.import_export(b_export, self.db)
        result = publications.report(self.db)
        sessions = {session['session_id']: session for session in result['sessions']}
        self.assertEqual(len(sessions), 2)
        self.assertEqual(sessions[identity['session_id']]['imported_at_ns'], first['imported_at_ns'])
        self.assertTrue(sessions[identity['session_id']]['stale'])
        self.assertFalse(sessions['root/healthy-b']['stale'])
        self.assertEqual(len(result['failures']), 1)
        self.assertEqual(result['failures'][0]['session_id'], identity['session_id'])
        with duckdb.connect(str(self.db), read_only=True) as conn:
            self.assertEqual(conn.execute('SELECT role,count(*) FROM events WHERE session_id=? GROUP BY role',
                                          ['root/healthy-b']).fetchall(), [('user', 1)])

    def test_unsupported_archive_is_error_not_successful_empty(self):
        # Change only the archive descriptor; the writer's current root remains.
        descriptor = self.fixture / 'store' / 'data' / 'capture-v1' / 'archive.json'
        archive = json.loads(descriptor.read_text())
        archive['contract'] = 'unknown-contract/v99'
        descriptor.write_text(json.dumps(archive))
        identity = self.metadata['manifest']['identity']
        destination = self.root / 'unsupported'
        self.run_writer('export', '--store', str(self.fixture / 'store'),
                        '--archive', identity['archive_id'], '--machine', identity['machine_id'],
                        '--agent', identity['agent'], '--session', identity['session_id'],
                        '--output', str(destination), succeeds=False)
        self.assertFalse((destination / 'metadata.json').exists())


@unittest.skipUnless(os.environ.get('CAPTURE_V1_BINARY'), 'run make capture-acceptance for coordinator acceptance')
class CoordinatorJourney(unittest.TestCase):
    """Actual processes, durable hints/restart, shared reader and actual importer."""
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name).resolve()
        self.binary = str(Path(os.environ['CAPTURE_V1_BINARY']).resolve())
        self.home = self.root / 'home'
        self.home.mkdir(mode=0o700)
        self.env = dict(os.environ, HOME=str(self.home), XDG_CONFIG_HOME=str(self.home / '.config'))
        self.env.pop('AGENT_SESSIONS_DISABLE', None)
        self.policy = self.root / 'policy'
        (self.policy / 'exclusions').mkdir(parents=True, mode=0o700)
        self.config = self.root / 'runtime.json'
        config = dict(version='capture-runtime/v1', archive_id='coordinator', machine_id='synthetic-machine',
            store=str(self.root / 'store'), spool=str(self.root / 'spool'), policy_dir=str(self.policy),
            home=str(self.home), default_roots=True, roots=[], capture_workers=2, delivery_workers=2,
            poll_seconds=1, operation_seconds=30, max_attempts=2, max_spool_bytes=1 << 24,
            min_free_bytes=0, entries_per_root=256)
        self.config.write_text(json.dumps(config))
        self.config.chmod(0o600)
        self.process = None
        self.addCleanup(self.stop)
        self.run_writer('init', '--store', str(self.root / 'store'), '--archive', 'coordinator')

    def run_writer(self, *args, succeeds=True):
        process = subprocess.run([self.binary, 'capture-v1', *args], env=self.env,
            stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=60, check=False)
        self.assertEqual(process.returncode == 0, succeeds, 'coordinator command failed; private diagnostics withheld')
        return process

    def start(self):
        self.process = subprocess.Popen([self.binary, 'capture-v1', 'run', '--config', str(self.config)],
            env=self.env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)

    def stop(self):
        if self.process is not None:
            process, self.process = self.process, None
            if process.poll() is None:
                process.terminate()
            try:
                process.wait(timeout=30)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=10)
                self.fail('coordinator did not join workers on shutdown')
            self.assertEqual(process.returncode, 0, 'coordinator exited abnormally')

    def until(self, check):
        import time
        deadline = time.monotonic() + 45
        while time.monotonic() < deadline:
            if self.process is not None:
                self.assertIsNone(self.process.poll(), 'coordinator exited before acceptance')
            value = check()
            if value:
                return value
            time.sleep(0.1)
        self.fail('coordinator acceptance deadline exceeded')

    def view(self):
        process = subprocess.run([self.binary, 'capture-v1', 'work-status', '--config', str(self.config)],
            env=self.env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=10, check=False)
        return json.loads(process.stdout) if process.returncode == 0 else None

    def restore(self, identity, name, sequence):
        destination = self.root / name
        if destination.exists():
            import shutil
            shutil.rmtree(destination)  # Only an owned failed synthetic export attempt.
        process = subprocess.run([self.binary, 'capture-v1', 'export', '--store', str(self.root / 'store'),
            '--archive', 'coordinator', '--machine', 'synthetic-machine', '--agent', identity['agent'],
            '--session', identity['session_id'], '--output', str(destination)], env=self.env,
            stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=10, check=False)
        if process.returncode != 0:
            return None
        metadata = json.loads((destination / 'metadata.json').read_text())
        return destination if metadata['manifest']['sequence'] == sequence else None

    def test_declared_alias_move_preserves_source_epoch_and_actual_import(self):
        import shutil
        logical_root = self.home / '.claude'
        first_root, next_root = self.root / 'alias-first', self.root / 'alias-next'
        relative = Path('projects/declared/12345678-1234-1234-1234-123456789abc.jsonl')
        source = first_root / relative
        source.parent.mkdir(parents=True, mode=0o700)
        first = b'{"type":"user","uuid":"alias-u","message":{"role":"user","content":"Synthetic alias initial"}}\n'
        appended = first + b'{"type":"assistant","uuid":"alias-a","message":{"role":"assistant","content":"Synthetic alias append"}}\n'
        rewritten = b'{"type":"user","uuid":"alias-r","message":{"role":"user","content":"Synthetic alias rewrite"}}\n'
        source.write_bytes(first)
        config = json.loads(self.config.read_text())
        config['root_aliases'] = [dict(agent='claude', root=str(logical_root), path=str(path))
                                  for path in (first_root, next_root)]
        self.config.write_text(json.dumps(config))
        self.start()
        row = self.until(lambda: next((row for row in (self.view() or {}).get('Sources', [])
                         if row['Request']['Path'] == str(source) and row['CapturedVersion'] > 0), None))
        identity, source_id = row['Request']['Identity'], row['Request']['SourceID']
        self.until(lambda: self.restore(identity, 'alias-initial', 1))
        self.stop()
        first_root.rename(next_root)  # Only synthetic fixture input; logical namespace never moves.
        source = next_root / relative
        source.write_bytes(appended)
        # The reported logical file does not exist. The exact declared route supplies
        # available bytes, while the queue retains both hint and open coordinates.
        self.assertFalse((logical_root / relative).exists())
        self.run_writer('hook', '--config', str(self.config), '--agent', 'claude', '--path', str(logical_root / relative))
        self.start()
        second = self.until(lambda: self.restore(identity, 'alias-appended', 2))
        metadata = json.loads((second / 'metadata.json').read_text())
        self.assertEqual(len(metadata['manifest']['sources']), 1)
        self.assertEqual(metadata['manifest']['sources'][0]['source_id'], source_id)
        self.assertEqual((second / metadata['bodies'][0]['path']).read_bytes(), appended)
        source.write_bytes(rewritten)
        final = self.until(lambda: self.restore(identity, 'alias-rewritten', 3))
        self.stop()
        metadata = json.loads((final / 'metadata.json').read_text())
        self.assertEqual({item['source_id'] for item in metadata['manifest']['sources']}, {source_id})
        self.assertEqual(len({item['epoch_id'] for item in metadata['manifest']['sources']}), 2)
        self.assertEqual(sorted((final / body['path']).read_bytes() for body in metadata['bodies']),
                         sorted((appended, rewritten)))
        shutil.rmtree(next_root)
        shutil.rmtree(self.root / 'spool')
        restored = self.restore(identity, 'alias-independent', 3)
        self.assertIsNotNone(restored)
        db = self.root / 'alias-publications.duckdb'
        publications.import_export(restored, db)
        before = publications.report(db)
        publications.import_export(restored, db)
        after = publications.report(db)
        self.assertEqual(before['sessions'][0]['imported_at_ns'], after['sessions'][0]['imported_at_ns'])
        self.assertEqual(after['sessions'][0]['publication_id'], metadata['manifest']['publication_id'])
        with duckdb.connect(str(db), read_only=True) as conn:
            self.assertEqual(conn.execute('SELECT count(DISTINCT epoch_id) FROM events').fetchone()[0], 2)
            self.assertEqual(dict(conn.execute('SELECT role,count(*) FROM events GROUP BY role').fetchall()),
                             {'user': 2, 'assistant': 1})

    def test_native_launch_and_closed_snapshot_survive_without_originals_or_spool(self):
        import io
        import shutil
        import tarfile
        config = json.loads(self.config.read_text())
        config['enrichment'] = True
        self.config.write_text(json.dumps(config))
        project = self.home / 'workspace'
        project.mkdir(mode=0o700)
        rules = b'Synthetic project instruction closure\n'
        (project / 'AGENTS.md').write_bytes(rules)
        directory = self.home / '.claude' / 'projects' / 'native-enriched'
        directory.mkdir(parents=True, mode=0o700)
        transcript = directory / '12345678-1234-1234-1234-123456789abc.jsonl'
        raw = b'{"type":"user","uuid":"enriched-main","message":{"role":"user","content":"Synthetic enriched main"}}\n'
        transcript.write_bytes(raw)
        (self.home / '.claude' / 'settings.json').write_text(
            '{"theme":"dark","env":{"API_TOKEN":"synthetic-never-export-token"}}')
        self.run_writer('hooks', '--config', str(self.config), '--executable', self.binary,
                        '--action', 'install', '--apply')
        settings = json.loads((self.home / '.claude' / 'settings.json').read_text())
        command = next(hook['command'] for group in settings['hooks']['SessionStart']
                       for hook in group['hooks'] if 'agent-sessions-capture-v1' in hook.get('command', ''))
        self.start()
        payload = dict(hook_event_name='SessionStart', source='startup', cwd=str(project),
            session_id='12345678-1234-1234-1234-123456789abc', transcript_path=str(transcript),
            prompt='synthetic-prompt-not-launch-metadata')
        native = subprocess.run(command, shell=True, input=json.dumps(payload).encode(), env=self.env,
            stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=10, check=False)
        self.assertEqual(native.returncode, 0, 'owned native evidence command failed; diagnostics withheld')
        row = self.until(lambda: next((r for r in (self.view() or {}).get('Sources', [])
            if r['Request']['Path'] == str(transcript) and r['CapturedVersion'] > 0), None))
        identity = row['Request']['Identity']

        def enriched_export():
            destination = self.root / 'enriched-observation'
            if destination.exists():
                shutil.rmtree(destination)  # Owned synthetic reader attempts only.
            exported = subprocess.run([self.binary, 'capture-v1', 'export', '--store', str(self.root / 'store'),
                '--archive', 'coordinator', '--machine', 'synthetic-machine', '--agent', identity['agent'],
                '--session', identity['session_id'], '--output', str(destination)], env=self.env,
                stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=10, check=False)
            if exported.returncode:
                return None
            metadata = json.loads((destination / 'metadata.json').read_text())
            bodies = {(b['source_id'], b['epoch_id']): b for b in metadata['bodies']}
            launch_seen, project_seen = False, False
            for source in metadata['manifest']['sources']:
                body = bodies.get((source['source_id'], source['epoch_id']))
                if body is None:
                    continue
                data = (destination / body['path']).read_bytes()
                self.assertNotIn(b'synthetic-never-export-token', data)
                if 'enrichment:launch' in (source.get('associations') or []):
                    evidence = json.loads(data)
                    self.assertEqual(evidence['Context']['CWD'], str(project))
                    self.assertNotIn(b'synthetic-prompt-not-launch-metadata', data)
                    launch_seen = True
                if 'enrichment:snapshot' in (source.get('associations') or []):
                    with tarfile.open(fileobj=io.BytesIO(data), mode='r:') as archive:
                        members = archive.getmembers()
                        self.assertTrue(all(m.isfile() for m in members))
                        project_seen |= any(archive.extractfile(m).read() == rules
                            for m in members if m.name.startswith('assets/'))
            return destination if launch_seen and project_seen else None

        first = self.until(enriched_export)
        self.stop()
        shutil.rmtree(self.home)
        shutil.rmtree(self.root / 'spool')
        restored = self.root / 'independent-enriched'
        publications.export_session(self.binary, self.root / 'store', 'coordinator', 'synthetic-machine',
            identity['agent'], identity['session_id'], restored)
        metadata = json.loads((restored / 'metadata.json').read_text())
        main = next(s for s in metadata['manifest']['sources'] if s['role'] == 'main')
        body = next(b for b in metadata['bodies'] if (b['source_id'], b['epoch_id']) == (main['source_id'], main['epoch_id']))
        self.assertEqual((restored / body['path']).read_bytes(), raw)
        self.assertEqual(json.loads((first / 'metadata.json').read_text())['manifest'], metadata['manifest'])
        db = self.root / 'enriched.duckdb'
        publications.import_export(restored, db)
        self.assertEqual(publications.import_export(restored, db)['status'], 'unchanged')
        report = publications.report(db)
        self.assertTrue(report['event_counts'])
        self.assertTrue(any(r['kind'] == 'artifact_not_normalized' for r in report['issue_counts']))
        self.assertEqual(report['sessions'][0]['normalization'], 'partial')

    def test_periodic_capture_quarantine_and_restart_without_another_hook(self):
        directory = self.home / '.claude' / 'projects' / 'synthetic'
        directory.mkdir(parents=True, mode=0o700)
        a, b = directory / 'a.jsonl', directory / 'b.jsonl'
        a.write_text('{"type":"user","uuid":"a","message":{"role":"user","content":"Synthetic A"}}\n')
        b_bytes = b'{"type":"user","uuid":"b1","message":{"role":"user","content":"Synthetic B"}}\n'
        b.write_bytes(b_bytes)
        # Native hook owns only metadata. It must not require opening queue.sqlite.
        self.run_writer('hook', '--config', str(self.config), '--agent', 'claude', '--path', str(a))
        hint = next((self.root / 'spool' / 'hints').glob('*.json'))
        a_request = json.loads(hint.read_text())
        a.unlink()
        a.mkdir()  # The observed path becomes poison before any bytes are accepted.
        self.start()
        # B has no hook at all: periodic discovery must capture it independently.
        def b_source():
            view = self.view()
            return next((row for row in (view or {}).get('Sources', [])
                         if row['Request']['Path'] == str(b) and row['CapturedVersion'] > 0), None)
        b_row = self.until(b_source)
        b_request = b_row['Request']
        first = self.until(lambda: self.restore(b_request['Identity'], 'b-first', 1))
        metadata = json.loads((first / 'metadata.json').read_text())
        self.assertEqual((first / metadata['bodies'][0]['path']).read_bytes(), b_bytes)
        db = self.root / 'coordinator.duckdb'
        publications.import_export(first, db)
        def quarantined():
            view = self.view()
            return next((row for row in (view or {}).get('Quarantine', [])
                         if row['Kind'] == 'capture' and row['Active']), None)
        entry = self.until(quarantined)
        self.assertGreaterEqual(entry['Attempts'], 2)
        self.stop()
        # Persist a second actual writer capture, then remove its original. Restart
        # must deliver accepted work without another event, source or manual deliver.
        b_bytes += b'{"type":"user","uuid":"b2","message":{"role":"user","content":"Synthetic B append"}}\n'
        b.write_bytes(b_bytes)
        self.run_writer('capture', '--archive', 'coordinator', '--machine', 'synthetic-machine',
            '--agent', 'claude', '--session', b_request['Identity']['session_id'], '--spool', str(self.root / 'spool'),
            '--policy-dir', str(self.policy), '--source', b_request['SourceID'], '--path', str(b))
        b.unlink()
        a.rmdir()
        a.write_text('{"type":"user","uuid":"a","message":{"role":"user","content":"Synthetic A replay"}}\n')
        self.run_writer('replay', '--config', str(self.config), '--id', str(entry['ID']))
        self.start()
        second = self.until(lambda: self.restore(b_request['Identity'], 'b-second', 2))
        restored = json.loads((second / 'metadata.json').read_text())
        self.assertEqual((second / restored['bodies'][0]['path']).read_bytes(), b_bytes)
        a_export = self.until(lambda: self.restore(a_request['Identity'], 'a-replayed', 1))
        publications.import_export(second, db)
        publications.import_export(a_export, db)
        publications.import_export(second, db)
        report = publications.report(db)
        self.assertEqual(len(report['sessions']), 2)
        self.assertEqual(report['failures'], [])
        with duckdb.connect(str(db), read_only=True) as conn:
            self.assertEqual(conn.execute('SELECT count(*) FROM events').fetchone()[0], 3)
        self.stop()

    def test_derived_sources_restore_and_normalize_from_actual_publications(self):
        import shutil
        import sqlite3
        directory = self.home / '.claude' / 'projects' / 'synthetic'
        directory.mkdir(parents=True, mode=0o700)
        main = directory / 'main.jsonl'
        main_bytes = b'{"type":"user","uuid":"main-u","message":{"role":"user","content":"Synthetic main"}}\n'
        main.write_bytes(main_bytes)
        sub = directory / 'main' / 'subagents' / 'agent-child.jsonl.zst'
        sub.parent.mkdir(parents=True, mode=0o700)
        plain = self.root / 'expected-subagent.jsonl'
        # The pinned DuckDB supplies an offline zstd fixture encoder; capture and
        # reader still run through the actual Go implementation, not Python mocks.
        query = "SELECT 'user' AS type, 'sub-u' AS uuid, {'role':'user','content':'Synthetic subagent'} AS message"
        with duckdb.connect() as conn:
            for path, compression in [(plain, 'NONE'), (sub, 'ZSTD')]:
                escaped = str(path).replace("'", "''")
                conn.execute(f"COPY ({query}) TO '{escaped}' (FORMAT JSON, COMPRESSION {compression})")
        sub_bytes = plain.read_bytes()
        settings = self.home / '.claude' / 'settings.json'
        primary = self.home / '.claude.json'
        secret = 'synthetic-credential-must-not-survive'
        settings.write_text(json.dumps({'env': {'TOKEN': secret}}))
        primary.write_text(json.dumps({'primaryApiKey': secret, 'numStartups': 1}))
        database = self.home / '.claude' / 'state.sqlite'
        original = sqlite3.connect(database)
        self.addCleanup(original.close)
        original.execute('PRAGMA journal_mode=WAL')
        original.execute('CREATE TABLE evidence(body TEXT)')
        original.execute("INSERT INTO evidence VALUES ('Synthetic WAL evidence')")
        original.commit()
        self.start()
        paths = {str(path) for path in [main, sub, settings, primary, database]}
        def accepted():
            view = self.view()
            rows = [row for row in (view or {}).get('Sources', [])
                    if row['Request']['Path'] in paths and row['CapturedVersion'] > 0]
            return rows if len(rows) == len(paths) else None
        rows = self.until(accepted)
        requests = {row['Request']['Path']: row['Request'] for row in rows}
        self.assertEqual(requests[str(main)]['Identity'], requests[str(sub)]['Identity'])
        exports = {}
        for n, path in enumerate([main, settings, primary, database]):
            sequence = 2 if path == main else 1
            exports[path] = self.until(lambda p=path, n=n, sequence=sequence:
                self.restore(requests[str(p)]['Identity'], f'derived-{n}', sequence))
        metadata = json.loads((exports[main] / 'metadata.json').read_text())
        by_pair = {(body['source_id'], body['epoch_id']): body for body in metadata['bodies']}
        bodies = {}
        for source in metadata['manifest']['sources']:
            body = by_pair[(source['source_id'], source['epoch_id'])]
            bodies[source['role']] = (exports[main] / body['path']).read_bytes()
        self.assertEqual(bodies, {'main': main_bytes, 'subagent': sub_bytes})
        self.stop()
        original.close()
        shutil.rmtree(self.home)
        shutil.rmtree(self.root / 'spool')
        # Every source type remains independently restorable without source, WAL,
        # spool, hooks, materialization cache, or the live coordinator.
        restored = {}
        for n, path in enumerate([main, settings, primary, database]):
            sequence = 2 if path == main else 1
            restored[path] = self.restore(requests[str(path)]['Identity'], f'restored-derived-{n}', sequence)
            self.assertIsNotNone(restored[path])
        for path in [settings, primary]:
            metadata = json.loads((restored[path] / 'metadata.json').read_text())
            body = (restored[path] / metadata['bodies'][0]['path']).read_bytes()
            self.assertFalse(secret.encode() in body, 'protected credential survived actual publication')
            self.assertIn(b'[redacted]', body)
        metadata = json.loads((restored[database] / 'metadata.json').read_text())
        backup = restored[database] / metadata['bodies'][0]['path']
        with contextlib.closing(sqlite3.connect(f'file:{backup}?mode=ro', uri=True)) as conn:
            self.assertEqual(conn.execute('SELECT body FROM evidence').fetchall(), [('Synthetic WAL evidence',)])
        db = self.root / 'derived.duckdb'
        for export in restored.values():
            publications.import_export(export, db)
            publications.import_export(export, db)
        report = publications.report(db)
        self.assertEqual(len(report['sessions']), 4)
        self.assertEqual(report['failures'], [])
        self.assertEqual(report['source_roles'], [{'source_role': 'artifact', 'sources': 3},
            {'source_role': 'main', 'sources': 1}, {'source_role': 'subagent', 'sources': 1}])
        self.assertEqual(report['issue_counts'], [{'kind': 'artifact_not_normalized', 'issues': 3}])
        with duckdb.connect(str(db), read_only=True) as conn:
            self.assertEqual(conn.execute('SELECT is_human,count(*) FROM events GROUP BY is_human ORDER BY is_human').fetchall(),
                             [(False, 1), (True, 1)])

    def test_owned_native_commands_wake_discovery_and_capture_indexed_artifacts(self):
        import time
        config = json.loads(self.config.read_text())
        config['poll_seconds'] = 30
        self.config.write_text(json.dumps(config))
        settings = self.home / '.claude' / 'settings.json'
        settings.parent.mkdir(parents=True, mode=0o700)
        foreign = 'printf synthetic-foreign-hook'
        settings.write_text(json.dumps({'hooks': {'Stop': [{'hooks': [{'type': 'command', 'command': foreign}]}]}}))
        saved_config = self.config.read_bytes()
        self.run_writer('hooks', '--config', str(self.config), '--executable', self.binary,
                        '--action', 'install', '--apply')
        def owned_command(path):
            data = json.loads(path.read_text())
            return next(handler['command'] for group in data['hooks']['Stop']
                        for handler in group['hooks'] if 'agent-sessions-capture-v1' in handler.get('command', ''))
        claude_hook = owned_command(settings)
        codex_hook = owned_command(self.home / '.codex' / 'hooks.json')
        self.start()
        self.until(lambda: (self.view() or {}).get('Runtime', {}).get('CompletedSweep', 0) > 0)
        uuid = '12345678-1234-1234-1234-123456789abc'
        main = self.home / '.claude' / 'projects' / 'native' / f'{uuid}.jsonl'
        main.parent.mkdir(parents=True, mode=0o700)
        main_bytes = b'{"type":"user","uuid":"native-a","message":{"role":"user","content":"Synthetic native Claude"}}\n'
        main.write_bytes(main_bytes)
        def invoke(command, payload):
            process = subprocess.run(command, shell=True, input=json.dumps(payload).encode(), env=self.env,
                stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=5, check=False)
            self.assertEqual(process.returncode, 0, 'owned native command failed; diagnostics withheld')
            self.assertEqual(process.stdout, b'', 'a hook must not write transcript data')
        # Execute the exact installed shell command with native-shaped stdin.
        invoke(claude_hook, {'transcript_path': str(main), 'session_id': uuid})
        def source(path):
            view = self.view()
            return next((row for row in (view or {}).get('Sources', [])
                         if row['Request']['Path'] == str(path) and row['CapturedVersion'] > 0), None)
        main_row = self.until(lambda: source(main))
        self.until(lambda: self.restore(main_row['Request']['Identity'], 'native-main-first', 1))
        artifact = self.home / '.claude' / 'file-history' / uuid / 'revision.txt'
        artifact.parent.mkdir(parents=True, mode=0o700)
        artifact_bytes = b'Synthetic associated file-history evidence\n'
        artifact.write_bytes(artifact_bytes)
        codex = self.home / '.codex' / 'sessions' / '2026' / '10' / '23' / 'rollout-2026-10-23T00-00-00-22345678-1234-1234-1234-123456789abc.jsonl'
        codex.parent.mkdir(parents=True, mode=0o700)
        codex_bytes = b'{"timestamp":"2026-10-23T00:00:00Z","type":"event_msg","payload":{"type":"user_message","message":"Synthetic native Codex"}}\n'
        codex.write_bytes(codex_bytes)
        began = time.monotonic()
        # No path is available in this valid Codex event. It must durably wake the
        # coordinator, not fail or wait for another 30-second discovery timer.
        invoke(codex_hook, {'type': 'agent-turn-complete', 'thread-id': 'synthetic-thread'})
        artifact_row = self.until(lambda: source(artifact))
        codex_row = self.until(lambda: source(codex))
        self.assertLess(time.monotonic() - began, 10, 'pathless native wake waited for the periodic timer')
        self.assertEqual(artifact_row['Request']['Identity'], main_row['Request']['Identity'])
        a_export = self.until(lambda: self.restore(main_row['Request']['Identity'], 'native-claude', 2))
        c_export = self.until(lambda: self.restore(codex_row['Request']['Identity'], 'native-codex', 1))
        metadata = json.loads((a_export / 'metadata.json').read_text())
        pair = {(body['source_id'], body['epoch_id']): body for body in metadata['bodies']}
        selected = {src['role']: (a_export / pair[(src['source_id'], src['epoch_id'])]['path']).read_bytes()
                    for src in metadata['manifest']['sources']}
        self.assertEqual(selected, {'main': main_bytes, 'artifact': artifact_bytes})
        db = self.root / 'native.duckdb'
        for export in [a_export, c_export, a_export, c_export]:
            publications.import_export(export, db)
        report = publications.report(db)
        self.assertEqual(len(report['sessions']), 2)
        self.assertEqual(report['failures'], [])
        self.assertEqual(report['issue_counts'], [{'kind': 'artifact_not_normalized', 'issues': 1}])
        with duckdb.connect(str(db), read_only=True) as conn:
            self.assertEqual(conn.execute('SELECT agent,role,count(*) FROM events GROUP BY agent,role ORDER BY agent').fetchall(),
                             [('claude', 'user', 1), ('codex', 'user', 1)])
        self.stop()
        self.run_writer('hooks', '--config', str(self.config), '--executable', self.binary,
                        '--action', 'uninstall', '--apply')
        data = json.loads(settings.read_text())
        commands = [handler['command'] for group in data['hooks']['Stop'] for handler in group['hooks']]
        self.assertEqual(commands, [foreign])
        self.assertEqual(self.config.read_bytes(), saved_config)
        self.assertFalse((self.home / '.pi' / 'agent' / 'extensions' / 'agent-sessions-capture-v1.ts').exists())


if __name__ == '__main__':
    unittest.main()
