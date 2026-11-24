"""Synthetic fixtures only: Pi run records through import_shared (brief 56)."""
import hashlib
import json
from pathlib import Path
import tempfile
import unittest

import duckdb
import publications
import pi_runs
import setup_facts
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



def custom(kind, **kw):
    return dict(type='custom_message', customType=kind, **kw)


def tool_result(details):
    return {'type': 'message', 'timestamp': 'ts', 'message': {'role': 'toolResult', 'details': details,
                                                              'content': [{'text': 'run-free-text'}]}}


REFS = [
    tool_result({'runId': 'r-tool', 'mission': {'workflowChildren': [{'runId': 'r-wf1'}, {'runId': 'r-wf1'}]},
                 'workflowChildren': {'children': [{'runId': 'r-wf2'}, 5]},
                 'pending': [{'runId': 'r-pend'}, 'x']}),
    tool_result({'runId': 'r-tool'}),
    custom('subagent_supervisor_request', details={'runId': 'r-req'}),
    custom('subagent_supervisor_reply', data={'runId': 'r-rep'}),
    custom('subagent_control_notice', details={'event': {'runId': 'r-note'}}),
    custom('subagent_control_notice', details={'runId': 'r-wrong-place'}),
    {'type': 'message', 'message': {'role': 'assistant', 'content': 'runId r-free'}},
    tool_result({'runId': 'bad/id', 'text': 'r-text'}),
]
REF_SQL = 'SELECT run_id, via, line FROM pi_run_ref ORDER BY line, run_id'


class RefTest(SessionBase):
    def test_each_via_first_sighting_only_structured(self):
        publications.import_export(self.export('pi', REFS), self.db)
        self.assertEqual(self.sql(REF_SQL), [
            ('r-pend', 'pending', 1), ('r-tool', 'tool_result', 1), ('r-wf1', 'workflow_child', 1),
            ('r-wf2', 'workflow_child', 1), ('r-req', 'supervisor_request', 3),
            ('r-rep', 'supervisor_reply', 4), ('r-note', 'control_notice', 5)])
        self.assertEqual(set(pi_runs.VIAS), {r[1] for r in self.sql(REF_SQL)})

    def test_only_pi_sessions(self):
        publications.import_export(self.export('claude', REFS), self.db)
        self.assertEqual(self.sql('SELECT count(*) FROM pi_run_ref'), [(0,)])

    def test_version_two_database_rederives_once(self):
        d = self.export('pi', REFS)
        publications.import_export(d, self.db)
        want = self.sql('SELECT * FROM pi_run_ref ORDER BY line, run_id')
        self.assertEqual(setup_facts.SETUP_VERSION, 3)
        with duckdb.connect(str(self.db)) as c:
            c.execute('DROP TABLE pi_run_ref')
            c.execute('UPDATE session_setup_version SET version=2')
        self.assertEqual(publications.import_export(d, self.db)['status'], 'unchanged')
        self.assertEqual(self.sql('SELECT * FROM pi_run_ref ORDER BY line, run_id'), want)
        self.assertEqual(self.sql('SELECT version FROM session_setup_version'), [(3,)])
        publications.import_export(d, self.db)
        self.assertEqual(self.sql('SELECT * FROM pi_run_ref ORDER BY line, run_id'), want)

    def test_reset_clears_refs(self):
        publications.import_export(self.export('pi', REFS), self.db)
        publications.import_export(self.export('pi', [REFS[2]]), self.db, reset=True)
        self.assertEqual(self.sql('SELECT run_id FROM pi_run_ref'), [('r-req',)])



class DimReportTest(SessionBase):
    def shared(self, records):
        helper = Shared()
        helper.setUp()
        self.addCleanup(helper.tmp.cleanup)
        helper.db = self.db
        publications.import_shared(helper.build(records), self.db)

    def test_view_links_earliest_session_and_leaves_session_totals(self):
        usage = {'type': 'message', 'message': {'role': 'assistant', 'usage': {'input': 7}, 'content': []}}
        self.shared([run_record('r-req', usage=dict(input=1000)), run_record('r-none')])
        publications.import_export(self.export('pi', [usage, REFS[2]]), self.db)
        rows = self.sql('SELECT run_id, session_key IS NOT NULL, referencing_sessions FROM pi_run_dim '
                        'ORDER BY run_id')
        self.assertEqual(rows, [('r-none', False, 0), ('r-req', True, 1)])
        self.assertEqual(self.sql('SELECT input_tokens FROM session_dim'), [(7,)])
        self.assertEqual(self.sql('SELECT input_tokens FROM usage_by_model')[0], (7,))

    def test_report_runs_section(self):
        self.shared([run_record('a', model='p/m', requestedModel='p/n', exitCode=1,
                                acceptance=dict(status='passed'),
                                launchResolvedExtensions=dict(effective=[D, 'sha256:' + 'cd' * 32])),
                     run_record('b', model='p/m', requestedModel='p/m', error='x',
                                launchResolvedExtensions=dict(effective=[D]))])
        publications.import_export(self.export('pi', [REFS[2].__class__(REFS[2], details={'runId': 'a'})]), self.db)
        runs = publications.report(self.db, summary=True)['runs']
        self.assertEqual(runs['totals'], dict(runs=2, linked=1, with_error=1, non_zero_exit=1))
        self.assertEqual(runs['by_model'], [dict(model='p/m', runs=2)])
        self.assertEqual(runs['requested_differs'], 1)
        self.assertEqual(runs['extensions_effective'], dict(min=1, median=1.5, max=2))
        self.assertEqual(runs['distinct_effective_digests'], 2)
        self.assertEqual(runs['acceptance'], [dict(agent_type='worker', acceptance_status='passed', runs=1)])
        self.assertEqual(runs['by_agent_type'], [dict(agent_type='worker', runs=2, non_zero_exits=1)])

    def test_read_only_old_database(self):
        publications.import_export(self.export('pi', PI), self.db)
        self.assertNotEqual(publications.report(self.db, summary=True)['runs'], None)
        with duckdb.connect(str(self.db)) as c:
            c.execute('DROP VIEW pi_run_dim')
            for table in ('pi_run_ref', *pi_runs.TABLES[:2]):
                c.execute(f'DROP TABLE {table}')
        tables = self.sql('SHOW TABLES')
        self.assertEqual(publications.report(self.db, summary=True)['runs'], {})
        self.assertEqual(publications.report(self.db)['sessions'][0]['agent'], 'pi')
        self.assertEqual(self.sql('SHOW TABLES'), tables)


if __name__ == '__main__':
    unittest.main()
