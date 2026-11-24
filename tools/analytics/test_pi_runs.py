"""Synthetic fixtures only: Pi run records through import_shared (brief 56)."""
import hashlib
import json
from pathlib import Path
import tempfile
import unittest

import duckdb
import publications
import pi_runs
from test_setup_facts import Base as SessionBase, PI

CANARY = 'CANARY-5c21d8'


def run_record(run_id='run-1', **kw):
    return dict(dict(runId=run_id, agent='worker', exitCode=0, task=CANARY, timestamp=1700000000000), **kw)


class Shared(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.db = self.root / 'p.duckdb'
        self.dir = self.root / 'shared'

    def build(self, records=(None,), publication='p1', sequence=1, mutate=None, archive='a'):
        """Write a shared-reader/v1 export; a record may be raw bytes or a JSON value."""
        self.dir.mkdir(mode=0o700, exist_ok=True)
        sources, bodies = [], []
        for n, record in enumerate(records if records != (None,) else [run_record()]):
            raw = record if isinstance(record, bytes) else json.dumps(record).encode()
            h = hashlib.sha256(raw).hexdigest()
            (self.dir / f'{n}.bin').write_bytes(raw)
            sources.append(dict(source_id=f's{n}', epoch_id='0', kind='pi_run_record',
                                path=f'sessions/x/subagent-artifacts/{CANARY}{n}.json',
                                state='active', revision=1, captured_at_ns=4,
                                object=dict(key='k', encoding='zstd', sha256=h, encoded_sha256=h,
                                            size=len(raw), encoded_size=len(raw))))
            bodies.append(dict(source_id=f's{n}', epoch_id='0', path=f'{n}.bin', sha256=h,
                               size=len(raw)))
        manifest = dict(contract='shared-publication/v1',
                        identity=dict(archive_id=archive, machine_id='m', agent='pi'),
                        publication_id=publication, sequence=sequence, captured_at_ns=2,
                        published_at_ns=3, sources=sources)
        data = dict(contract='shared-reader/v1', manifest_sha256='ab' * 32, manifest=manifest,
                    bodies=bodies)
        if mutate:
            mutate(data)
        (self.dir / 'metadata.json').write_text(json.dumps(data))
        return self.dir

    def sql(self, sql, args=()):
        with duckdb.connect(str(self.db), read_only=True) as c:
            return c.execute(sql, list(args)).fetchall()

    def refused(self, code, **kw):
        with self.assertRaises(publications.ImportFailure) as cm:
            publications.import_shared(self.build(**kw), self.db)
        self.assertEqual(str(cm.exception), code)


class ImportSharedTest(Shared):
    def test_imports_and_unchanged(self):
        d = self.build()
        self.assertEqual(publications.import_shared(d, self.db)['status'], 'imported')
        self.assertEqual(publications.import_shared(d, self.db)['status'], 'unchanged')
        self.assertEqual(self.sql('SELECT count(*) FROM pi_run_owner'), [(1,)])

    def test_new_publication_replaces_and_old_sequence_refused(self):
        publications.import_shared(self.build(), self.db)
        d = self.build([run_record('r2')], publication='p2', sequence=2)
        self.assertEqual(publications.import_shared(d, self.db)['status'], 'imported')
        self.assertEqual(self.sql('SELECT count(*) FROM pi_run_owner'), [(1,)])
        self.refused('stale_publication', publication='p1', sequence=1)
        self.refused('publication_conflict', publication='other', sequence=2)

    def test_runs_version_change_reimports(self):
        d = self.build()
        publications.import_shared(d, self.db)
        with duckdb.connect(str(self.db)) as c:
            c.execute('UPDATE pi_run_owner SET runs_version=0')
        self.assertEqual(publications.import_shared(d, self.db)['status'], 'imported')
        self.assertEqual(self.sql('SELECT runs_version FROM pi_run_owner'), [(pi_runs.RUNS_VERSION,)])

    def test_archive_mismatch_and_reset(self):
        publications.import_shared(self.build(), self.db)
        self.refused('archive_mismatch', archive='other')
        d = self.build(archive='other')
        self.assertEqual(publications.import_shared(d, self.db, reset=True)['status'], 'imported')
        self.assertEqual(self.sql('SELECT archive_id FROM publication_binding'), [('other',)])
        publications.import_shared(self.build(), self.db, reset=True)
        publications.import_shared(self.build(), self.db, reset=True)
        self.assertEqual(self.sql('SELECT count(*) FROM pi_run_owner'), [(1,)])

    def test_closed_keys_and_bounds_refused(self):
        def top(d): d['extra'] = 1
        def manifest(d): d['manifest']['extra'] = 1
        def missing(d): del d['manifest']['sequence']
        def identity(d): d['manifest']['identity']['session_id'] = 'x'
        def source(d): d['manifest']['sources'][0]['role'] = 'main'
        def obj(d): d['manifest']['sources'][0]['object']['extra'] = 1
        def body(d): d['bodies'][0]['extra'] = 1
        def kind(d): d['manifest']['sources'][0]['kind'] = 'pi_config'
        def seq(d): d['manifest']['sequence'] = 0
        def time(d): d['manifest']['captured_at_ns'] = -1
        def digest(d): d['manifest_sha256'] = 'zz'
        def nul(d): d['manifest']['publication_id'] = 'a\x00b'
        def big(d): d['manifest']['sources'] *= 1
        def srcdigest(d): d['manifest']['sources'][0]['object']['sha256'] = 'A' * 64
        def empty(d): d['manifest']['identity']['machine_id'] = ''
        def nobody(d): d['bodies'] = []
        def dup(d): d['manifest']['sources'].append(dict(d['manifest']['sources'][0]))
        def path(d): d['bodies'][0]['path'] = '../x'
        def absolute(d): d['bodies'][0]['path'] = '/x'
        def mismatch(d): d['bodies'][0]['size'] += 1
        for fn in (top, manifest, missing, identity, source, obj, body, kind, seq, time, digest,
                   nul, srcdigest, empty):
            with self.subTest(fn.__name__):
                self.refused('invalid_metadata', mutate=fn)
        self.refused('body_coverage', mutate=nobody)
        self.refused('duplicate_source', mutate=dup)
        self.refused('unsafe_body_path', mutate=path)
        self.refused('unsafe_body_path', mutate=absolute)
        self.refused('body_metadata_mismatch', mutate=mismatch)
        self.assertFalse(self.db.exists() and self.sql('SELECT count(*) FROM pi_run')[0][0])

    def test_source_bound(self):
        def many(d):
            d['manifest']['sources'] = [d['manifest']['sources'][0]] * (publications.MAX_SHARED_SOURCES + 1)
        self.refused('invalid_metadata', mutate=many)

    def test_metadata_bound(self):
        self.build()
        (self.dir / 'metadata.json').write_bytes(b' ' * (publications.MAX_METADATA + 1))
        with self.assertRaises(publications.ImportFailure) as cm:
            publications.import_shared(self.dir, self.db)
        self.assertEqual(str(cm.exception), 'invalid_metadata')

    def test_body_integrity_checked_while_reading(self):
        self.build()
        (self.dir / '0.bin').write_bytes(b'{"runId":"tampered"}')
        with self.assertRaises(publications.ImportFailure) as cm:
            publications.import_shared(self.dir, self.db)
        self.assertEqual(str(cm.exception), 'body_integrity')
        self.assertEqual(self.sql('SELECT count(*) FROM pi_run_owner'), [(0,)])

    def test_contract_cross_refusal(self):
        shared = self.build()
        with self.assertRaises(publications.ImportFailure) as cm:
            publications.import_export(shared, self.db)
        self.assertEqual(str(cm.exception), 'unsupported_contract')
        helper = SessionBase()
        helper.setUp()
        self.addCleanup(helper.tmp.cleanup)
        session = helper.export('pi', PI)
        with self.assertRaises(publications.ImportFailure) as cm:
            publications.import_shared(session, self.db)
        self.assertEqual(str(cm.exception), 'unsupported_contract')

    def test_session_import_unaffected_and_reset_clears_session_import(self):
        publications.import_shared(self.build(), self.db)
        helper = SessionBase()
        helper.setUp()
        self.addCleanup(helper.tmp.cleanup)
        publications.import_export(helper.export('pi', PI), self.db)
        self.assertEqual(self.sql('SELECT count(*) FROM pi_run_owner'), [(1,)])
        self.assertEqual(self.sql('SELECT count(*) FROM sessions'), [(1,)])


D = 'sha256:' + 'ab' * 32
FULL = dict(
    runId='run-1', agent='worker', exitCode=1, timestamp=1700000000123, model='prov/m:high',
    requestedModel='prov/m2', error=CANARY, transcriptPath=f'/h/{CANARY}/t.jsonl',
    usage=dict(input=1, output=2, cacheRead=3, cacheWrite=4, turns=5, cost=0.25),
    launchResolvedExtensions=dict(
        version=1, source=f'/p/{CANARY}', configured=[D, 'sha256:' + 'cd' * 32], effective=[D],
        runtime=[], required=['bad', D],
        omitted=dict(configured=1, effective=2, required=3, runtime=4),
        disableAmbientExtensions=True),
    acceptance=dict(status='passed', evidenceStatus='verified', explicit=True,
                    criteria=[{'text': CANARY, 'evidence': CANARY}],
                    effectiveAcceptance=dict(level='strict'), reviewResult=dict(status='approved'),
                    runtimeChecks=[{'command': CANARY}, {}],
                    childReport=dict(commands=[CANARY], changedFiles=[CANARY], notes=CANARY)),
    runner=dict(type='external-cli', command=CANARY, args=[CANARY]),
    externalProcess=dict(durationMs=77, exitCode=-2, pid=4242, cwd=f'/w/{CANARY}'))
COLUMNS = ', '.join(pi_runs.RUN_COLUMNS)


class RunRowsTest(Shared):
    def row(self, record):
        publications.import_shared(self.build([record]), self.db)
        return dict(zip(pi_runs.RUN_COLUMNS, self.sql(f'SELECT {COLUMNS} FROM pi_run')[0]))

    def test_every_column(self):
        result = publications.import_shared(self.build([FULL]), self.db)
        self.assertEqual((result['runs'], result['runs_skipped']), (1, 1))  # one bad digest
        self.assertEqual(dict(zip(pi_runs.RUN_COLUMNS, self.sql(f'SELECT {COLUMNS} FROM pi_run')[0])), dict(
            run_id='run-1', timestamp_ms=1700000000123, agent_type='worker', model='prov/m:high',
            requested_model='prov/m2', exit_code=1, has_error=True, acceptance_status='passed',
            evidence_status='verified', acceptance_level='strict', acceptance_explicit=True,
            review_status='approved', criteria=1, runtime_checks=2, input_tokens=1, output_tokens=2,
            cache_read_tokens=3, cache_write_tokens=4, cost_usd=0.25, turns=5,
            extensions_configured=2, extensions_effective=1, extensions_runtime=0,
            extensions_required=2, extensions_omitted=10, ambient_extensions_disabled=True,
            runner_type='external-cli', external_duration_ms=77, external_exit_code=-2))
        self.assertEqual(self.sql('SELECT "list", digest FROM pi_run_extension ORDER BY 1, 2'),
                         [('configured', D), ('configured', 'sha256:' + 'cd' * 32),
                          ('effective', D), ('required', D)])

    def test_canaries_never_stored(self):
        publications.import_shared(self.build([FULL]), self.db)
        with duckdb.connect(str(self.db), read_only=True) as c:
            for (table,) in c.execute('SHOW TABLES').fetchall():
                for col in [r[0] for r in c.execute(f'DESCRIBE "{table}"').fetchall()]:
                    hits = c.execute(f'SELECT count(*) FROM "{table}" WHERE '
                                     f'CAST("{col}" AS VARCHAR) ILIKE ?', ['%canary%']).fetchone()[0]
                    self.assertEqual(hits, 0, (table, col))
        self.assertNotIn(CANARY, json.dumps(publications.report(self.db, summary=True)))

    def test_invalid_values_null_and_counted(self):
        bad = dict(runId='r', agent='bad/agent', exitCode='1', timestamp=-1, model='/abs',
                   requestedModel='a//b', error=5, usage=dict(input=-1, cost=float('inf'), turns=True),
                   acceptance=dict(status='Passed', evidenceStatus='x' * 41, explicit='yes',
                                   criteria='x', reviewResult=dict(status=3)),
                   launchResolvedExtensions=dict(effective='x', disableAmbientExtensions=1),
                   runner=dict(type='A'), externalProcess=dict(durationMs=2 ** 53, exitCode=1.5))
        result = publications.import_shared(self.build([bad]), self.db)
        row = dict(zip(pi_runs.RUN_COLUMNS, self.sql(f'SELECT {COLUMNS} FROM pi_run')[0]))
        self.assertEqual({k for k, v in row.items() if v is not None}, {'run_id'})
        self.assertEqual(result['runs_skipped'], 19)  # one per invalid value above

    def test_absent_fields_are_null_not_skipped(self):
        result = publications.import_shared(self.build([dict(runId='r')]), self.db)
        self.assertEqual(result['runs_skipped'], 0)
        row = dict(zip(pi_runs.RUN_COLUMNS, self.sql(f'SELECT {COLUMNS} FROM pi_run')[0]))
        self.assertEqual({k for k, v in row.items() if v is not None}, {'run_id', 'has_error'})

    def test_not_a_row(self):
        records = [b'not json', b'[1]', json.dumps(dict(agent='w')).encode(),
                   json.dumps(dict(runId='a/b')).encode(), json.dumps(dict(runId=5)).encode(),
                   run_record('ok')]
        result = publications.import_shared(self.build(records), self.db)
        self.assertEqual((result['runs'], result['runs_skipped']), (1, 5))
        self.assertEqual(self.sql('SELECT run_id FROM pi_run'), [('ok',)])

    def test_empty_error_is_false_and_coordinates(self):
        self.assertFalse(self.row(dict(runId='r', error=''))['has_error'])
        self.assertEqual(self.sql('SELECT archive_id, machine_id, source_id FROM pi_run'),
                         [('a', 'm', 's0')])


if __name__ == '__main__':
    unittest.main()
