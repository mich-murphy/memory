"""Offline consumer tests, not a substitute for the actual Go writer journey."""
import builtins
import hashlib
import json
from pathlib import Path
import tempfile
import subprocess
import unittest
from unittest.mock import patch

import duckdb
import publications


class PublicationsTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.export = self.root / 'export'
        self.export.mkdir(mode=0o700)
        self.db = self.root / 'projection.duckdb'
        self.text = b'{"type":"user","uuid":"u","message":{"role":"user","content":"hello"}}\n'
        self.manifest = dict(contract='session-publication/v2',
            identity=dict(archive_id='archive', machine_id='machine', agent='claude', session_id='session'),
            publication_id='p1', sequence=1, observed_at_ns=1700000000000000001,
            captured_at_ns=1700000000000000002, published_at_ns=1700000000000000003,
            coverage=dict(discovery='complete', enrichment='partial', missing=['launch']), sources=[])

    def fixture(self, entries=None):
        self.manifest['sources'] = []
        bodies = []
        for n, (source, epoch, role, raw) in enumerate(entries or [('s', 'e', 'main', self.text)]):
            digest = hashlib.sha256(raw).hexdigest()
            name = f'body-{n}.jsonl'
            (self.export / name).write_bytes(raw)
            self.manifest['sources'].append(dict(source_id=source, epoch_id=epoch, role=role,
                path='/original/transcript', state='present', revision=self.manifest['sequence'],
                observed_at_ns=3, captured_at_ns=4, associations=[], object=dict(key='immutable/object',
                encoding='zstd', sha256=digest, encoded_sha256=digest, size=len(raw), encoded_size=len(raw))))
            bodies.append(dict(source_id=source, epoch_id=epoch, path=name, sha256=digest, size=len(raw)))
        data = dict(contract='session-reader/v2', manifest=self.manifest, bodies=bodies)
        self.save(data)
        return data

    def save(self, data):
        (self.export / 'metadata.json').write_text(json.dumps(data))

    def query(self, sql):
        with duckdb.connect(str(self.db), read_only=True) as db:
            return db.execute(sql).fetchall()

    def test_summary_exact_totals_bounded_detail_and_offline_exhaustive(self):
        self.fixture()
        publications.import_export(self.export, self.db)
        # SQL expansion is intentionally synthetic: no repeated capture/import setup.
        with duckdb.connect(str(self.db)) as db:
            db.execute('''INSERT INTO sessions SELECT sessions.* REPLACE
                ('bulk-'||i::VARCHAR AS session_key, 'bulk-'||i::VARCHAR AS session_id)
                FROM sessions, range(240) t(i) WHERE session_id='session' ''')
            db.execute('''INSERT INTO sources SELECT sources.* REPLACE
                ('bulk-'||i::VARCHAR AS session_key, trace_id||'-'||i::VARCHAR AS trace_id)
                FROM sources, range(240) t(i)''')
            db.execute('''INSERT INTO failures(session_key,archive_id,machine_id,agent,
                session_id,publication_id,sequence,failed_at_ns,reason,attempts)
                SELECT 'bulk-'||i::VARCHAR,'archive','machine','claude',
                    'bulk-'||i::VARCHAR,'p2',2,10,'import_failed',1 FROM range(90) t(i)''')
        full = publications.report(self.db, now_ns=1000)
        summary = publications.report(self.db, now_ns=1000, summary=True)
        self.assertEqual((len(full['sessions']), len(full['sources']), len(full['failures'])), (241, 241, 90))
        self.assertEqual(summary['totals'], dict(sessions=241, sources=241, failures=90))
        self.assertEqual(summary['detail_limit'], 50)
        self.assertEqual((len(summary['sessions']), len(summary['sources']), len(summary['failures'])), (50, 0, 50))
        self.assertEqual(summary['agents'], [dict(agent='claude', sessions=241)])
        self.assertEqual(summary['freshness'], full['freshness'])
        self.assertEqual(summary['source_counts'], full['source_counts'])
        self.assertEqual(summary['event_counts'], full['event_counts'])
        self.assertEqual(summary['issue_counts'], full['issue_counts'])
        self.assertEqual(summary['source_roles'], full['source_roles'])
        self.assertEqual([s['session_key'] for s in summary['sessions']],
                         [s['session_key'] for s in full['sessions'][:50]])
        self.assertEqual([f['session_key'] for f in summary['failures']],
                         [f['session_key'] for f in full['failures'][:50]])
        self.assertEqual(summary['sessions'], full['sessions'][:50], 'failed row staleness remains exact')

    def test_wire_nil_arrays_are_legal_not_unsupported_empty(self):
        data = self.fixture()
        data['manifest']['coverage']['missing'] = None
        data['manifest']['sources'][0]['associations'] = None
        self.save(data)
        self.assertEqual(publications.import_export(self.export, self.db)['events'], 1)
        self.assertEqual(self.query('SELECT associations FROM sources'), [('[]',)])
        data['manifest'].update(sequence=2, publication_id='empty', sources=None)
        data['manifest']['identity']['session_id'] = 'explicitly-empty'
        data['bodies'] = None
        self.save(data)
        result = publications.import_export(self.export, self.db)
        self.assertEqual((result['sources'], result['events']), (0, 0))
        self.assertEqual(len(publications.report(self.db)['sessions']), 2)
        # Unknown contracts never become an empty successful refresh.
        data['contract'] = 'session-reader/v99'
        self.save(data)
        with self.assertRaisesRegex(publications.ImportFailure, '^unsupported_contract$'):
            publications.import_export(self.export, self.db)

    def test_binary_artifacts_remain_owned_without_transcript_parsing(self):
        opaque = b'SQLite format 3\x00' + b'\xff\x00' * 16384
        self.fixture([('main', 'epoch', 'main', self.text),
                      ('database', 'backup', 'artifact', opaque)])
        _, events = publications._dependencies()
        original = events.iter_events
        with patch.object(events, 'iter_events', wraps=original) as parser:
            result = publications.import_export(self.export, self.db)
        self.assertEqual(parser.call_count, 1)
        self.assertEqual((result['sources'], result['events'], result['issues']), (2, 1, 1))
        self.assertEqual(self.query('SELECT kind, line, "offset", length FROM issues'),
                         [('artifact_not_normalized', 0, 0, len(opaque))])
        path, partial = self.query("SELECT path, partial_tail FROM sources WHERE source_role='artifact'")[0]
        self.assertEqual(Path(path).read_bytes(), opaque)
        self.assertFalse(partial)
        self.assertEqual(self.query('SELECT normalization FROM sessions'), [('partial',)])
        self.assertEqual(publications.import_export(self.export, self.db)['status'], 'unchanged')
        self.assertEqual(self.query('SELECT count(*) FROM events'), [(1,)])

    def test_unsupported_version_no_writes(self):
        data = self.fixture()
        data['contract'] = 'session-reader/v999'
        self.save(data)
        with self.assertRaisesRegex(publications.ImportFailure, '^unsupported_contract$'):
            publications.import_export(self.export, self.db)
        self.assertFalse(self.db.exists())
        data['contract'] = 'session-reader/v2'
        data['manifest']['contract'] = 'session-publication/v999'
        self.save(data)
        with self.assertRaises(publications.ImportFailure):
            publications.import_export(self.export, self.db)
        self.assertFalse(self.db.exists())
        # Unsupported updates cannot mutate an existing success or its failures.
        self.manifest['contract'] = 'session-publication/v2'
        self.fixture()
        publications.import_export(self.export, self.db)
        before = self.db.read_bytes()
        data = self.fixture()
        data['contract'] = 'session-reader/v999'
        self.save(data)
        with self.assertRaises(publications.ImportFailure):
            publications.import_export(self.export, self.db)
        self.assertEqual(self.db.read_bytes(), before)

    def test_missing_python_dependency_safe(self):
        real_import = builtins.__import__
        def importing(name, *args, **kwargs):
            if name == 'duckdb':
                raise ImportError('PRIVATE dependency path')
            return real_import(name, *args, **kwargs)
        with patch('builtins.__import__', side_effect=importing):
            with self.assertRaisesRegex(publications.ImportFailure, '^missing_dependency$'):
                publications.import_export(self.export, self.db)
        self.assertFalse(self.db.exists())

    def test_idempotence_owned_bytes_and_freshness(self):
        self.fixture()
        result = publications.import_export(self.export, self.db)
        self.assertEqual(result['events'], 1)
        first = self.query('SELECT imported_at_ns FROM sessions')
        self.assertEqual(publications.import_export(self.export, self.db)['status'], 'unchanged')
        self.assertEqual(self.query('SELECT imported_at_ns FROM sessions'), first)
        self.assertEqual(self.query('SELECT count(*) FROM events'), [(1,)])
        owned = Path(self.query('SELECT path FROM sources')[0][0])
        (self.export / 'body-0.jsonl').unlink()
        self.assertEqual(owned.read_bytes(), self.text)
        result = publications.report(self.db, now_ns=1700000000000000010)
        session = result['sessions'][0]
        self.assertEqual(session['captured_at_ns'], 1700000000000000002)
        self.assertEqual(session['capture_age_ns'], 8)
        self.assertFalse(session['stale'])
        self.assertEqual(session['coverage']['enrichment'], 'partial')
        self.assertEqual(result['event_counts'], [dict(event_type='message', events=1)])
        self.assertFalse(result['dashboard_wired'])

    def test_missing_and_corrupt_body_preserve_success(self):
        self.fixture()
        publications.import_export(self.export, self.db)
        original = self.query('SELECT * FROM sessions')
        self.manifest.update(sequence=2, publication_id='p2')
        self.fixture()
        (self.export / 'body-0.jsonl').unlink()
        with self.assertRaisesRegex(publications.ImportFailure, '^missing_body$'):
            publications.import_export(self.export, self.db)
        self.assertEqual(self.query('SELECT * FROM sessions'), original)
        self.fixture()
        (self.export / 'body-0.jsonl').write_bytes(b'PRIVATE corrupt transcript')
        with self.assertRaisesRegex(publications.ImportFailure, '^body_integrity$'):
            publications.import_export(self.export, self.db)
        self.assertEqual(self.query('SELECT * FROM sessions'), original)
        report = publications.report(self.db)
        self.assertTrue(report['sessions'][0]['stale'])
        self.assertEqual(report['failures'][0]['attempts'], 2)
        self.assertNotIn('PRIVATE', json.dumps(report))
        # Healthy B succeeds independently without erasing A's error.
        self.manifest['identity']['session_id'] = 'healthy-b'
        self.fixture()
        publications.import_export(self.export, self.db)
        report = publications.report(self.db)
        self.assertEqual(len(report['sessions']), 2)
        self.assertEqual(len(report['failures']), 1)

    def test_failure_watermark_survives_older_retries(self):
        for broken in ('missing', 'corrupt'):
            with self.subTest(broken=broken):
                self.manifest.update(sequence=1, publication_id='p1')
                self.fixture()
                publications.import_export(self.export, self.db, reset=True)
                success = self.query('SELECT * FROM sessions')
                self.manifest.update(sequence=2, publication_id='p2')
                self.fixture()
                body = self.export / 'body-0.jsonl'
                if broken == 'missing':
                    body.unlink()
                else:
                    body.write_bytes(b'bad')
                with self.assertRaises(publications.ImportFailure):
                    publications.import_export(self.export, self.db)
                failure = self.query('SELECT * EXCLUDE(attempts) FROM failures')
                self.manifest.update(sequence=1, publication_id='p1')
                self.fixture()
                self.assertEqual(publications.import_export(self.export, self.db)['status'], 'unchanged')
                self.assertEqual(self.query('SELECT * FROM sessions'), success)
                self.assertEqual(self.query('SELECT * EXCLUDE(attempts) FROM failures'), failure)
                self.assertEqual(self.query('SELECT attempts FROM failures'), [(1,)])
                self.assertTrue(publications.report(self.db, now_ns=1700000000000000010)['sessions'][0]['stale'])
                # A stale failed P0 attempt counts, but cannot replace P2's target/reason/time.
                self.manifest.update(sequence=0, publication_id='p0')
                self.fixture()
                with self.assertRaisesRegex(publications.ImportFailure, '^stale_publication$'):
                    publications.import_export(self.export, self.db)
                self.assertEqual(self.query('SELECT * EXCLUDE(attempts) FROM failures'), failure)
                self.assertEqual(self.query('SELECT publication_id,sequence,attempts FROM failures'), [('p2', 2, 2)])
                self.manifest.update(sequence=2, publication_id='p2')
                self.fixture()
                publications.import_export(self.export, self.db)
                self.assertEqual(self.query('SELECT count(*) FROM failures'), [(0,)])
                self.assertFalse(publications.report(self.db, now_ns=1700000000000000010)['sessions'][0]['stale'])

    def test_newer_success_clears_failure_watermark(self):
        self.fixture()
        (self.export / 'body-0.jsonl').unlink()
        with self.assertRaises(publications.ImportFailure):
            publications.import_export(self.export, self.db)
        self.manifest.update(sequence=2, publication_id='p2')
        self.fixture()
        publications.import_export(self.export, self.db)
        self.assertEqual(self.query('SELECT count(*) FROM failures'), [(0,)])

    def test_database_schema_version_explicit(self):
        self.fixture()
        publications.import_export(self.export, self.db)
        with duckdb.connect(str(self.db)) as db:
            db.execute('ALTER TABLE failures DROP COLUMN sequence')
        with self.assertRaisesRegex(publications.ImportFailure, '^unsupported_database$'):
            publications.import_export(self.export, self.db)
        with duckdb.connect(str(self.db)) as db:
            db.execute('UPDATE publication_binding SET version=999')
        with self.assertRaisesRegex(publications.ImportFailure, '^unsupported_database$'):
            publications.import_export(self.export, self.db)

    def test_hash_and_length_are_independently_verified(self):
        self.fixture()
        # Identical byte length is not evidence of matching bytes.
        (self.export / 'body-0.jsonl').write_bytes(self.text.replace(b'hello', b'wrong'))
        with self.assertRaisesRegex(publications.ImportFailure, '^body_integrity$'):
            publications.import_export(self.export, self.db)
        self.assertEqual(self.query('SELECT count(*) FROM sessions'), [(0,)])
        self.fixture()
        (self.export / 'body-0.jsonl').write_bytes(self.text + b'\n')
        with self.assertRaisesRegex(publications.ImportFailure, '^body_integrity$'):
            publications.import_export(self.export, self.db)
        self.assertEqual(self.query('SELECT count(*) FROM events'), [(0,)])

    def test_epochs_roles_coordinates_and_revision_replacement(self):
        self.fixture([('s', 'old', 'main', self.text), ('s', 'new', 'subagent', self.text),
                      ('artifact', 'one', 'launch', b'{"type":"system"}\n')])
        publications.import_export(self.export, self.db)
        rows = self.query('SELECT trace_id,event_id,source_id,epoch_id,publication_id FROM events ORDER BY epoch_id')
        self.assertEqual(len({row[0] for row in rows}), 3)
        self.assertEqual(len({row[1] for row in rows}), 3)
        self.assertEqual(self.query('SELECT source_role FROM sources ORDER BY source_role'), [('launch',), ('main',), ('subagent',)])
        self.assertEqual(self.query("SELECT is_human,origin FROM events WHERE epoch_id='new'"), [(False, 'agent')])
        old_ids = {r[3]: r[1] for r in rows}
        self.manifest.update(sequence=2, publication_id='p2')
        self.fixture([('s', 'old', 'main', self.text + self.text), ('s', 'new', 'subagent', self.text)])
        publications.import_export(self.export, self.db)
        self.assertEqual(self.query('SELECT count(*) FROM events'), [(3,)])
        self.assertEqual(self.query("SELECT event_id FROM events WHERE epoch_id='old' ORDER BY line")[0][0], old_ids['old'])
        self.assertEqual(self.query('SELECT DISTINCT publication_id FROM events'), [('p2',)])
        self.assertEqual(self.query('SELECT count(*) FROM sources'), [(2,)])

    def test_malformed_unicode_offsets_and_incomplete_tail(self):
        good = self.text.replace(b'hello', 'héllo'.encode())
        malformed = b'{PRIVATE broken}\n'
        tail = b'{"unfinished":'
        self.fixture([('s', 'e', 'main', good + malformed + tail)])
        publications.import_export(self.export, self.db)
        self.assertEqual(self.query('SELECT line,"offset",length FROM events'), [(1, 0, len(good))])
        issues = self.query('SELECT line,"offset",length,kind,record_type FROM issues ORDER BY line,kind')
        self.assertEqual(issues, [(2, len(good), len(malformed), 'malformed_json', None),
            (3, len(good)+len(malformed), len(tail), 'incomplete_tail', None),
            (3, len(good)+len(malformed), len(tail), 'malformed_json', None)])
        self.assertEqual(self.query('SELECT partial_tail FROM sources'), [(True,)])
        self.assertEqual(publications.report(self.db)['sessions'][0]['normalization'], 'partial')

    def test_exact_pair_coverage_and_metadata_pin(self):
        data = self.fixture()
        data['bodies'][0]['epoch_id'] = 'wrong'
        self.save(data)
        with self.assertRaisesRegex(publications.ImportFailure, 'body_coverage'):
            publications.import_export(self.export, self.db)
        self.assertFalse(self.db.exists())
        data = self.fixture()
        data['bodies'][0]['sha256'] = '0' * 64
        self.save(data)
        with self.assertRaisesRegex(publications.ImportFailure, 'body_metadata_mismatch'):
            publications.import_export(self.export, self.db)

    def test_archive_binding_reset_and_research_protection(self):
        self.fixture()
        publications.import_export(self.export, self.db)
        self.manifest['identity']['archive_id'] = 'new-archive'
        self.fixture()
        with self.assertRaisesRegex(publications.ImportFailure, 'archive_mismatch'):
            publications.import_export(self.export, self.db)
        self.assertEqual(publications.report(self.db)['archive_id'], 'archive')
        publications.import_export(self.export, self.db, reset=True)
        self.assertEqual(publications.report(self.db)['archive_id'], 'new-archive')
        research = self.root / 'research.duckdb'
        with duckdb.connect(str(research)) as db:
            db.execute('CREATE TABLE evidence(note VARCHAR)')
        with self.assertRaisesRegex(publications.ImportFailure, 'not_publication_database'):
            publications.import_export(self.export, research, reset=True)
        with duckdb.connect(str(research)) as db:
            self.assertEqual(db.execute('SHOW TABLES').fetchall(), [('evidence',)])

    def test_safe_paths_and_conflicts(self):
        data = self.fixture()
        data['bodies'][0]['path'] = '../escape'
        self.save(data)
        with self.assertRaisesRegex(publications.ImportFailure, 'unsafe_body_path'):
            publications.import_export(self.export, self.db)
        self.fixture()
        publications.import_export(self.export, self.db)
        self.manifest['publication_id'] = 'conflict'
        self.fixture()
        with self.assertRaisesRegex(publications.ImportFailure, 'publication_conflict'):
            publications.import_export(self.export, self.db)

    def withhold(self, data, index=0, reason='policy'):
        source = data['manifest']['sources'][index]
        source_hash = publications._source_hash(source['source_id'])
        obj = source.pop('object')
        commitment = hashlib.sha256('\0'.join(str(obj[k]) for k in
            ('key', 'sha256', 'encoded_sha256', 'encoding', 'size', 'encoded_size')).encode()).hexdigest()
        source.update(optional=True, source_id='', path='', associations=[],
            withheld=dict(source_id_hash=source_hash, snapshot_commitment=commitment, reason=reason))
        data['bodies'] = [b for b in data['bodies'] if publications._source_hash(b['source_id']) != source_hash]
        data['manifest']['coverage'].update(enrichment='partial', missing=['source-withheld'])
        self.save(data)
        return source

    def test_withheld_only_never_accesses_body_or_parser(self):
        data = self.fixture([('private-artifact', 'e', 'artifact', b'private')])
        source = self.withhold(data)
        (self.export / 'body-0.jsonl').unlink()
        _, events = publications._dependencies()
        with patch.object(publications, '_copy_verified', side_effect=AssertionError('body accessed')), \
             patch.object(events, 'iter_events', side_effect=AssertionError('parser accessed')):
            result = publications.import_export(self.export, self.db)
            self.assertEqual(publications.import_export(self.export, self.db)['status'], 'unchanged')
        self.assertEqual((result['sources'], result['events'], result['issues']), (1, 0, 1))
        self.assertEqual(self.query('SELECT original_path,path,sha256,size,partial_tail FROM sources'),
                         [(None, None, None, None, False)])
        self.assertEqual(self.query('SELECT kind FROM issues'), [('source_withheld_policy',)])
        report = publications.report(self.db)
        self.assertEqual(report['contract'], 'session-analytics-report/v2')
        self.assertEqual(report['sources'][0]['source_key_hash'], source['withheld']['source_id_hash'])
        self.assertTrue(report['sources'][0]['optional'])
        self.assertEqual(report['sessions'][0]['normalization'], 'partial')
        self.assertEqual(report['source_counts'], dict(sources=1, selected=0, withheld=1))
        self.assertEqual(report['withheld_counts'], [dict(reason='policy', sources=1)])
        self.assertNotIn('private-artifact', json.dumps(report))

    def test_mixed_selected_exact_body_guard(self):
        data = self.fixture([('main', 'e', 'main', self.text), ('artifact', 'a', 'artifact', b'private')])
        self.withhold(data, 1, 'unavailable')
        (self.export / 'body-1.jsonl').unlink()
        with patch.object(publications, '_copy_verified', wraps=publications._copy_verified) as copy:
            publications.import_export(self.export, self.db)
        self.assertEqual(copy.call_count, 1)
        self.assertEqual(self.query('SELECT count(*) FROM events'), [(1,)])
        self.assertEqual(self.query('SELECT kind FROM issues'), [('source_withheld_unavailable',)])
        self.db.unlink()
        data['bodies'] = []
        self.save(data)
        with self.assertRaisesRegex(publications.ImportFailure, 'body_coverage'):
            publications.import_export(self.export, self.db)
        self.assertFalse(self.db.exists())

    def test_withheld_invalid_metadata_and_injected_bodies_rejected(self):
        mutations = [
            lambda s, d: s.update(optional=False),
            lambda s, d: s.update(role='main'),
            lambda s, d: s.update(source_id='private'),
            lambda s, d: s.update(path='/private/path'),
            lambda s, d: s.update(associations=['private']),
            lambda s, d: s.update(object={}),
            lambda s, d: s.update(body='private'),
            lambda s, d: s['withheld'].update(source_id_hash='bad'),
            lambda s, d: s['withheld'].update(snapshot_commitment='bad'),
            lambda s, d: s['withheld'].update(reason='private reason'),
            lambda s, d: s.update(epoch_id=''),
            lambda s, d: s.update(revision=-1),
            lambda s, d: s.update(revision=0),
            lambda s, d: s.update(captured_at_ns=-1),
            lambda s, d: d['manifest']['coverage'].update(enrichment='complete'),
            lambda s, d: d['manifest']['coverage'].update(missing=[]),
            lambda s, d: d['bodies'].append(dict(source_id='artifact', epoch_id='e', path='../private',
                sha256='0'*64, size=0)),
        ]
        for mutate in mutations:
            with self.subTest(mutation=mutations.index(mutate)):
                data = self.fixture([('artifact', 'e', 'artifact', b'private')])
                source = self.withhold(data)
                mutate(source, data)
                self.save(data)
                with self.assertRaises(publications.ImportFailure):
                    publications.import_export(self.export, self.db)
                self.assertFalse(self.db.exists())
        # Selected and withheld use the same effective hash/epoch coordinate.
        data = self.fixture([('artifact', 'e', 'artifact', b'private'),
                             ('artifact', 'e', 'artifact', b'private')])
        self.withhold(data, 1)
        with self.assertRaisesRegex(publications.ImportFailure, 'duplicate_source'):
            publications.import_export(self.export, self.db)

    def test_selected_withheld_reselected_keeps_private_evidence_and_failures(self):
        entries = [('main', 'm', 'main', self.text), ('artifact', 'a', 'artifact', b'private')]
        data = self.fixture(entries)
        data['manifest']['sources'][1]['optional'] = True
        self.save(data)
        publications.import_export(self.export, self.db)
        owned = Path(self.query("SELECT path FROM sources WHERE source_role='artifact'")[0][0])
        trace = self.query("SELECT trace_id FROM sources WHERE source_role='artifact'")[0][0]
        self.manifest.update(sequence=2, publication_id='p2')
        data = self.fixture(entries)
        self.withhold(data, 1, 'integrity')
        publications.import_export(self.export, self.db)
        self.assertEqual(owned.read_bytes(), b'private')
        self.assertEqual(self.query("SELECT trace_id FROM sources WHERE source_role='artifact'"), [(trace,)])
        self.assertEqual(self.query('SELECT count(*) FROM events'), [(1,)])
        old_import = self.query('SELECT imported_at_ns FROM sessions')
        self.assertEqual(publications.import_export(self.export, self.db)['status'], 'unchanged')
        self.assertEqual(self.query('SELECT imported_at_ns FROM sessions'), old_import)
        self.manifest.update(sequence=3, publication_id='p3')
        data = self.fixture(entries)
        data['manifest']['sources'][1]['optional'] = True
        self.save(data)
        (self.export / 'body-0.jsonl').unlink()
        with self.assertRaises(publications.ImportFailure):
            publications.import_export(self.export, self.db)
        self.manifest.update(sequence=2, publication_id='p2')
        data = self.fixture(entries)
        self.withhold(data, 1, 'integrity')
        publications.import_export(self.export, self.db)
        self.assertEqual(self.query('SELECT sequence FROM failures'), [(3,)])
        self.manifest.update(sequence=3, publication_id='p3')
        data = self.fixture(entries)
        data['manifest']['sources'][1]['optional'] = True
        self.save(data)
        publications.import_export(self.export, self.db)
        self.assertEqual(self.query('SELECT count(*) FROM failures'), [(0,)])
        self.assertEqual(self.query('SELECT count(*) FROM events'), [(1,)])
        self.assertEqual(self.query('SELECT kind FROM issues'), [('artifact_not_normalized',)])
        self.assertEqual(owned.read_bytes(), b'private')

    def test_v1_contracts_and_database_refused_without_mutation(self):
        for contract in ('reader', 'publication'):
            data = self.fixture()
            if contract == 'reader':
                data['contract'] = 'session-reader/v1'
            else:
                data['manifest']['contract'] = 'session-publication/v1'
            self.save(data)
            with self.assertRaisesRegex(publications.ImportFailure, 'unsupported_contract'):
                publications.import_export(self.export, self.db)
            self.assertFalse(self.db.exists())
        self.manifest['contract'] = 'session-publication/v2'
        self.fixture()
        publications.import_export(self.export, self.db)
        with duckdb.connect(str(self.db)) as db:
            db.execute('UPDATE publication_binding SET version=1')
        before = self.db.read_bytes()
        for reset in (False, True):
            with self.assertRaisesRegex(publications.ImportFailure, 'unsupported_database'):
                publications.import_export(self.export, self.db, reset=reset)
            self.assertEqual(self.db.read_bytes(), before)
        with self.assertRaisesRegex(publications.ImportFailure, 'unsupported_database'):
            publications.report(self.db)
        self.assertEqual(self.db.read_bytes(), before)

    def test_export_helper_without_shell(self):
        with patch('publications.subprocess.run') as run:
            publications.export_session('/binary', '/store', 'a', 'm', 'pi', 's;not-shell', '/output')
        args, kwargs = run.call_args
        self.assertIsInstance(args[0], list)
        self.assertIn('s;not-shell', args[0])
        self.assertNotIn('shell', kwargs)
        self.assertEqual(kwargs['timeout'], 300)
        with patch('publications.subprocess.run', side_effect=subprocess.TimeoutExpired('PRIVATE', 300)):
            with self.assertRaisesRegex(publications.ImportFailure, '^reader_export_timeout$'):
                publications.export_session('/binary', '/store', 'a', 'm', 'pi', 's', '/output')


if __name__ == '__main__':
    unittest.main()
