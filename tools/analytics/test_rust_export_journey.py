"""Consumer gate, Python half: Rust `memory export` output -> importer -> event index -> report.

Skips unless RUST_EXPORT_JOURNEY names the expectations file written by the Rust gate test
(rust/crates/memory/tests/s3_store/consumer_gate.rs). Synthetic fixtures only.
"""
import hashlib
import json
import os
from pathlib import Path
import shutil
import tempfile
import unittest

import sys

sys.path.insert(0, str(Path(__file__).resolve().parent))

import duckdb  # noqa: E402
import publications  # noqa: E402

EXPECT = os.environ.get('RUST_EXPORT_JOURNEY')


def session_key(e):
    raw = json.dumps([e['archive'], e['machine'], e['agent'], e['session']], separators=(',', ':'))
    return hashlib.sha256(raw.encode()).hexdigest()


@unittest.skipUnless(EXPECT, 'run through the Rust consumer gate (RUST_EXPORT_JOURNEY)')
class RustExportJourney(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.tmpdir = Path(self.tmp.name)
        self.db = self.tmpdir / 'a.duckdb'
        self.want = json.loads(Path(EXPECT).read_text())
        self.p1 = {e['name']: e for e in self.want['phase1']}
        self.p2 = {e['name']: e for e in self.want['phase2']}

    def query(self, sql, args=()):
        with duckdb.connect(str(self.db), read_only=True) as conn:
            return conn.execute(sql, list(args)).fetchall()

    def import_all(self, entries):
        out = {}
        for e in entries:
            out[e['name']] = publications.import_export(e['dir'], self.db)
        return out

    def test_counts_roles_issues_and_idempotence(self):
        results = self.import_all(self.p1.values())
        for name, e in self.p1.items():
            r = results[name]
            self.assertEqual(r['status'], 'imported', name)
            self.assertEqual((r['sources'], r['events']), (e['sources'], e['events']), name)
            key = session_key(e)
            roles = dict(self.query('SELECT source_role,count(*) FROM sources WHERE session_key=? GROUP BY 1', [key]))
            self.assertEqual(roles, e['roles'], name)
            issues = dict(self.query('SELECT kind,count(*) FROM issues WHERE session_key=? GROUP BY 1', [key]))
            self.assertEqual(issues, e['issues'], name)
        before = self.query('SELECT event_id FROM events ORDER BY 1')
        for name, e in self.p1.items():
            self.assertEqual(publications.import_export(e['dir'], self.db)['status'], 'unchanged', name)
        self.assertEqual(self.query('SELECT event_id FROM events ORDER BY 1'), before)
        report = publications.report(self.db, now_ns=1_700_000_001_000_000_000)
        self.assertEqual(report['contract'], 'session-analytics-report/v2')
        self.assertEqual(report['failures'], [])
        self.assertEqual(len(report['sessions']), len(self.p1))
        self.assertEqual(report['source_counts']['sources'], sum(e['sources'] for e in self.p1.values()))
        self.assertEqual(sum(x['events'] for x in report['event_counts']), sum(e['events'] for e in self.p1.values()))
        self.assertEqual(sum(x['issues'] for x in report['issue_counts']), sum(sum(e['issues'].values()) for e in self.p1.values()))
        self.assertEqual({s['session_key'] for s in report['sessions']}, {session_key(e) for e in self.p1.values()})

    def test_setup_facts_end_to_end(self):
        self.import_all(self.p1.values())

        def facts(name):
            return {tuple(r) for r in self.query(
                'SELECT fact,name,value FROM session_setup WHERE session_key=?', [session_key(self.p1[name])])}
        claude = facts('s1')
        self.assertIn(('model', 'claude-x', ''), claude)
        self.assertIn(('harness_version', '', '9.9.9'), claude)
        self.assertIn(('instruction_file', 'CLAUDE.md', 'project'), claude)
        codex = facts('codex')
        self.assertIn(('model', 'gpt-x', ''), codex)
        self.assertIn(('approval_policy', '', 'never'), codex)
        self.assertIn(('model', 'pi-m', ''), facts('pi'))

    def test_usage_and_tool_outcomes_end_to_end(self):
        # Brief 55: usage rows for every agent, tool outcomes for Claude and Pi.
        self.import_all(self.p1.values())

        def rows(sql, name):
            return self.query(sql, [session_key(self.p1[name])])
        usage = 'SELECT input_tokens, cache_read_tokens, output_tokens, cost_usd FROM session_usage WHERE session_key=?'
        out = 'SELECT tool_name, is_error, duration_ms FROM tool_outcome WHERE session_key=?'
        self.assertEqual(rows(usage, 's1'), [(3, None, 4, None)])
        self.assertEqual(rows(usage, 'codex'), [(4, 2, 3, None)])
        self.assertEqual(rows(usage, 'pi'), [(4, None, 2, 0.5)])
        self.assertEqual(rows(out, 's1'), [('Read', None, 5)])
        self.assertEqual(rows(out, 'pi'), [('bash', False, 7)])

    def test_shared_pi_run_end_to_end(self):
        # Brief 56: the built binary's shared Pi owner export, imported after the sessions.
        self.import_all(self.p1.values())
        shared = self.want['shared']
        first = publications.import_shared(shared, self.db)
        self.assertEqual((first['status'], first['runs'], first['runs_skipped']), ('imported', 1, 0))
        self.assertEqual(publications.import_shared(shared, self.db)['status'], 'unchanged')
        cols = ('run_id', 'agent_type', 'model', 'requested_model', 'exit_code', 'has_error',
                'acceptance_status', 'evidence_status', 'acceptance_level', 'acceptance_explicit',
                'review_status', 'criteria', 'runtime_checks', 'input_tokens', 'output_tokens',
                'cache_read_tokens', 'cache_write_tokens', 'cost_usd', 'turns',
                'extensions_configured', 'extensions_effective', 'extensions_runtime',
                'extensions_required', 'extensions_omitted', 'ambient_extensions_disabled',
                'runner_type', 'external_duration_ms', 'external_exit_code', 'timestamp_ms')
        rows = self.query('SELECT ' + ','.join(cols) + ' FROM pi_run')
        self.assertEqual(len(rows), 1)
        self.assertEqual(dict(zip(cols, rows[0])), dict(
            run_id='run-gate-1', agent_type='worker', model='pi-m', requested_model='pi-m2',
            exit_code=0, has_error=True, acceptance_status='passed', evidence_status='verified',
            acceptance_level='strict', acceptance_explicit=True, review_status='approved',
            criteria=1, runtime_checks=1, input_tokens=5, output_tokens=6, cache_read_tokens=1,
            cache_write_tokens=2, cost_usd=0.25, turns=3, extensions_configured=2,
            extensions_effective=1, extensions_runtime=0, extensions_required=0,
            extensions_omitted=1, ambient_extensions_disabled=True, runner_type='external-cli',
            external_duration_ms=1200, external_exit_code=0, timestamp_ms=1_700_000_000_000))
        ext = sorted(self.query('SELECT list, digest FROM pi_run_extension'))
        self.assertEqual(ext, sorted([('configured', 'sha256:' + 'a' * 64),
                                      ('configured', 'sha256:' + 'b' * 64),
                                      ('effective', 'sha256:' + 'a' * 64)]))
        dim = self.query('SELECT session_key, referencing_sessions FROM pi_run_dim')
        self.assertEqual(dim, [(session_key(self.p1['pi']), 1)])
        report = publications.report(self.db, now_ns=1_700_000_001_000_000_000, summary=True)
        self.assertEqual(report['failures'], [])
        self.assertTrue(report['runs'])
        self.assertEqual(report['runs']['totals']['runs'], 1)
        self.assertEqual(report['runs']['totals']['linked'], 1)
        # Canaries: dump every table and view as text, and the report.
        with duckdb.connect(str(self.db), read_only=True) as conn:
            names = [r[0] for r in conn.execute('SHOW TABLES').fetchall()]
            names += [r[0] for r in conn.execute(
                'SELECT view_name FROM duckdb_views() WHERE NOT internal').fetchall()]
            dump = [json.dumps(report, default=str)]
            for n in names:
                dump.append(repr(conn.execute(f'SELECT * FROM "{n}"').fetchall()))
        text = '\n'.join(dump)
        self.assertIn('run-gate-1', text)
        self.assertGreater(len(names), 10)
        for needle in self.want['run_canaries']:
            self.assertNotIn(needle, text, needle)

    def test_session_key_and_non_ascii(self):
        self.import_all(self.p1.values())
        e = self.p1['nonascii']
        key = session_key(e)
        paths = [r[0] for r in self.query('SELECT original_path FROM sources WHERE session_key=?', [key])]
        self.assertTrue(any(ord(c) > 127 for p in paths for c in p), paths)
        self.assertEqual(len(self.query('SELECT 1 FROM sessions WHERE session_key=?', [key])), 1)
        self.assertEqual(self.query('SELECT session_id FROM sessions WHERE session_key=?', [key])[0][0], e['session'])

    def test_subagent_artifact_and_codex_mirror(self):
        self.import_all(self.p1.values())
        for name in ('s1', 'pi'):
            key = session_key(self.p1[name])
            rows = self.query("""SELECT e.role,e.origin,e.is_human FROM events e JOIN sources s
                ON e.session_key=s.session_key AND e.source_id=s.source_id AND e.epoch_id=s.epoch_id
                WHERE s.session_key=? AND s.source_role='subagent'""", [key])
            self.assertTrue(any(r[0] == 'user' for r in rows), name)
            for role, origin, human in rows:
                self.assertFalse(human, name)
                if role == 'user':
                    self.assertEqual(origin, 'agent', name)
        key = session_key(self.p1['s1'])
        self.assertEqual(self.query("""SELECT count(*) FROM events e JOIN sources s
            ON e.source_id=s.source_id AND e.epoch_id=s.epoch_id AND e.session_key=s.session_key
            WHERE s.session_key=? AND s.source_role='artifact'""", [key])[0][0], 0)
        self.assertEqual(self.query("SELECT count(*) FROM issues WHERE session_key=? AND kind='artifact_not_normalized'", [key])[0][0], 1)
        ckey = session_key(self.p1['codex'])
        self.assertEqual(self.query('SELECT count(*) FROM events WHERE session_key=? AND is_mirror', [ckey])[0][0], 1)
        self.assertEqual(self.query("SELECT count(*) FROM events WHERE session_key=? AND is_human", [ckey])[0][0], 1)

    def test_append_and_rewrite_publish_new_sequences(self):
        first = self.import_all(self.p1.values())
        for name in ('s1', 'rewrite'):
            old, new = self.p1[name], self.p2[name]
            m_old = json.loads((Path(old['dir']) / 'metadata.json').read_text())['manifest']
            m_new = json.loads((Path(new['dir']) / 'metadata.json').read_text())['manifest']
            self.assertGreater(m_new['sequence'], m_old['sequence'], name)
            self.assertNotEqual(m_new['publication_id'], m_old['publication_id'], name)
            self.assertEqual(first[name]['publication_id'], m_old['publication_id'])
            r = publications.import_export(new['dir'], self.db)
            self.assertEqual(r['status'], 'imported', name)
            self.assertEqual((r['sources'], r['events']), (new['sources'], new['events']), name)
            self.assertEqual(r['publication_id'], m_new['publication_id'])
            self.assertEqual(publications.import_export(new['dir'], self.db)['status'], 'unchanged')
        key = session_key(self.p2['rewrite'])
        states = sorted(r[0] for r in self.query('SELECT state FROM sources WHERE session_key=?', [key]))
        self.assertEqual(states, ['active', 'sealed'])
        self.assertEqual(self.query('SELECT count(DISTINCT epoch_id) FROM events WHERE session_key=?', [key])[0][0], 2)
        report = publications.report(self.db, now_ns=1_700_000_001_000_000_000)
        self.assertEqual(report['failures'], [])

    def test_flipped_body_byte_fails_and_keeps_projection(self):
        self.import_all(self.p1.values())
        key = session_key(self.p1['s1'])
        old = self.query('SELECT publication_id FROM sessions WHERE session_key=?', [key])
        events = self.query('SELECT event_id FROM events WHERE session_key=? ORDER BY 1', [key])
        bad = self.tmpdir / 'bad'
        shutil.copytree(self.p2['s1']['dir'], bad)
        body = sorted((bad / 'bodies').iterdir())[0]
        data = bytearray(body.read_bytes())
        data[0] ^= 0x01
        body.write_bytes(bytes(data))
        with self.assertRaises(publications.ImportFailure) as ctx:
            publications.import_export(bad, self.db)
        self.assertEqual(str(ctx.exception), 'body_integrity')
        self.assertEqual(self.query('SELECT publication_id FROM sessions WHERE session_key=?', [key]), old)
        self.assertEqual(self.query('SELECT event_id FROM events WHERE session_key=? ORDER BY 1', [key]), events)


if __name__ == '__main__':
    unittest.main()
