"""Synthetic fixtures only: session-setup facts through the import path (brief 53)."""
import hashlib
import json
from pathlib import Path
import tempfile
import unittest

import duckdb
import publications
import setup_facts

CANARY = 'CANARY-7f3a91'

CLAUDE = [
    {'type': 'attachment', 'timestamp': 't1', 'version': '9.9', 'entrypoint': 'cli',
     'attachment': {'type': 'skill_listing', 'names': ['s-one', 's-two']}},
    {'type': 'attachment', 'attachment': {'type': 'model', 'identity': {'modelId': 'claude-att'}}},
    {'type': 'attachment', 'attachment': {'type': 'agent_listing_delta', 'addedTypes': ['reviewer']}},
    {'type': 'attachment', 'attachment': {'type': 'deferred_tools_delta', 'addedNames': ['WebFetch'],
        'failedMcpServers': [{'name': 'bad-srv'}]}},
    {'type': 'attachment', 'attachment': {'type': 'mcp_instructions_delta', 'addedNames': ['srv']}},
    {'type': 'attachment', 'attachment': {'type': 'invoked_skills', 'skills': [{'name': 's-one'}]}},
    {'type': 'attachment', 'attachment': {'type': 'command_permissions', 'allowedTools': ['Bash(ls)']}},
    {'type': 'attachment', 'attachment': {'type': 'instructions', 'files': [
        {'path': f'/home/{CANARY}/proj/CLAUDE.md', 'type': 'Project', 'content': CANARY + '-text'},
        {'path': f'/home/{CANARY}/proj/.claude/rules/{CANARY}.md', 'type': 'Project', 'content': CANARY},
        {'path': f'C:\\Users\\{CANARY}\\CLAUDE.local.md', 'type': 'Local'},
        {'path': '/x/AGENTS.md', 'type': 'Weird'}]}},
    {'type': 'permission-mode', 'permissionMode': 'plan'},
    {'type': 'assistant', 'version': '9.9', 'effort': 'high', 'message': {'role': 'assistant', 'model': 'claude-x', 'content': 'a'}},
    {'type': 'assistant', 'effort': 'high', 'message': {'role': 'assistant', 'model': 'claude-x', 'content': 'again'}},
]
CODEX = [
    {'type': 'session_meta', 'payload': {'id': 'x', 'cwd': f'/w/{CANARY}', 'cli_version': '1.2',
        'model_provider': 'openai', 'originator': 'codex-tui', 'source': 'cli'}},
    {'type': 'turn_context', 'payload': {'model': 'gpt-x', 'effort': 'low', 'approval_policy': 'never',
        'sandbox_policy': {'type': 'read-only'}, 'collaboration_mode': {'mode': 'default'},
        'disabled_plugin_ids': ['pl'], 'agents_md': {'directory': f'/w/{CANARY}', 'text': CANARY}}},
    {'type': 'turn_context', 'payload': {'model': 'gpt-x', 'agents_md': {'directory': f'/elsewhere/{CANARY}', 'text': CANARY}}},
]
PI = [
    {'type': 'session', 'version': 3, 'id': 'a', 'cwd': f'/w/{CANARY}'},
    {'type': 'model_change', 'modelId': 'm1', 'provider': 'prov'},
    {'type': 'thinking_level_change', 'thinkingLevel': 'xhigh'},
    {'type': 'message', 'message': {'role': 'assistant', 'model': 'm2', 'provider': 'prov', 'thinkingLevel': 'xhigh', 'content': []}},
]


def encode(records):
    return b''.join(r if isinstance(r, bytes) else (json.dumps(r) + '\n').encode() for r in records)


class Base(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.db = self.root / 'p.duckdb'

    def export(self, agent, records, name=None, publication='p', sequence=1):
        d = self.root / (name or agent)
        d.mkdir(mode=0o700, exist_ok=True)
        raw = encode(records)
        h = hashlib.sha256(raw).hexdigest()
        (d / 'b.jsonl').write_bytes(raw)
        src = dict(source_id='s', epoch_id='0', role='main', path='/o', state='active', revision=sequence,
                   observed_at_ns=3, captured_at_ns=4, associations=[],
                   object=dict(key='k', encoding='zstd', sha256=h, encoded_sha256=h, size=len(raw), encoded_size=len(raw)))
        man = dict(contract='session-publication/v2', identity=dict(archive_id='a', machine_id='m', agent=agent, session_id=agent),
                   publication_id=publication, sequence=sequence, observed_at_ns=1, captured_at_ns=2, published_at_ns=3,
                   coverage=dict(discovery='complete', enrichment='partial', missing=[]), sources=[src])
        (d / 'metadata.json').write_text(json.dumps(dict(contract='session-reader/v2', manifest_sha256='ab' * 32,
            manifest=man, bodies=[dict(source_id='s', epoch_id='0', path='b.jsonl', sha256=h, size=len(raw))])))
        return d

    def run_import(self, agent, records, **kw):
        publications.import_export(self.export(agent, records, **kw), self.db)
        return self.rows(agent)

    def sql(self, sql, args=()):
        with duckdb.connect(str(self.db), read_only=True) as c:
            return c.execute(sql, list(args)).fetchall()

    def rows(self, agent):
        return self.sql('SELECT fact,name,value FROM session_setup WHERE agent=? ORDER BY line,fact,name', [agent])


class SetupFactsTest(Base):
    def test_claude_every_source(self):
        rows = self.run_import('claude', CLAUDE)
        for want in [('harness', 'claude', ''), ('harness_version', '', '9.9'), ('entrypoint', '', 'cli'),
                     ('model', 'claude-x', ''), ('model', 'claude-att', ''), ('reasoning_effort', '', 'high'),
                     ('permission_mode', '', 'plan'), ('skill_available', 's-two', ''),
                     ('subagent_type_available', 'reviewer', ''), ('tool_available', 'WebFetch', ''),
                     ('mcp_server_failed', 'bad-srv', ''), ('mcp_server_available', 'srv', ''),
                     ('skill_invoked', 's-one', ''), ('tool_allowed', 'Bash(ls)', '')]:
            self.assertIn(want, rows)

    def test_codex_every_source(self):
        rows = self.run_import('codex', CODEX)
        for want in [('harness', 'codex', ''), ('harness_version', '', '1.2'), ('provider', '', 'openai'),
                     ('originator', '', 'codex-tui'), ('entrypoint', '', 'cli'), ('model', 'gpt-x', ''),
                     ('reasoning_effort', '', 'low'), ('approval_policy', '', 'never'),
                     ('sandbox_policy', '', 'read-only'), ('collaboration_mode', '', 'default'),
                     ('plugin_disabled', 'pl', '')]:
            self.assertIn(want, rows)

    def test_codex_agents_md_scope(self):
        rows = self.run_import('codex', CODEX)
        self.assertIn(('instruction_file', 'AGENTS.md', 'project'), rows)
        self.assertIn(('instruction_file', 'AGENTS.md', 'other'), rows)

    def test_pi_every_source_and_session_format(self):
        rows = self.run_import('pi', PI)
        for want in [('harness', 'pi', ''), ('session_format', '', '3'), ('model', 'm1', ''), ('model', 'm2', ''),
                     ('provider', '', 'prov'), ('reasoning_effort', '', 'xhigh')]:
            self.assertIn(want, rows)
        self.assertFalse([r for r in rows if r[0] == 'harness_version'])
        self.assertEqual(self.sql("SELECT harness_version, session_format FROM session_dim")[0], (None, '3'))

    def test_change_only(self):
        rows = self.run_import('claude', CLAUDE)
        self.assertEqual(rows.count(('model', 'claude-x', '')), 1)
        recs = [{'type': 'model_change', 'modelId': m} for m in ('a', 'b', 'a')]
        rows = [r for r in self.run_import('pi', recs) if r[0] == 'model']
        # first sighting and each new value: a, b; the final repeat of a is skipped
        self.assertEqual(sorted(rows), [('model', 'a', ''), ('model', 'b', '')])

    def test_privacy_canary_and_scopes(self):
        for agent, records in (('claude', CLAUDE), ('codex', CODEX), ('pi', PI)):
            self.run_import(agent, records)
        rows = self.rows('claude')
        self.assertIn(('instruction_file', 'CLAUDE.md', 'project'), rows)
        self.assertIn(('instruction_file', 'CLAUDE.local.md', 'local'), rows)
        self.assertIn(('instruction_file', 'other', 'project'), rows)  # a rule file
        self.assertIn(('instruction_file', 'AGENTS.md', 'other'), rows)  # unknown type
        with duckdb.connect(str(self.db), read_only=True) as c:
            for table in ('session_setup', 'session_dim'):
                cols = [r[0] for r in c.execute(f'DESCRIBE {table}').fetchall()]
                for col in cols:
                    hits = c.execute(f'SELECT count(*) FROM {table} WHERE CAST("{col}" AS VARCHAR) ILIKE ?',
                                     ['%canary%']).fetchone()[0]
                    self.assertEqual(hits, 0, (table, col))
                    hits = c.execute(f'SELECT count(*) FROM {table} WHERE CAST("{col}" AS VARCHAR) ~ ?',
                                     [r'[/\\]']).fetchone()[0]
                    self.assertEqual(hits, 0, (table, col))

    def test_bounds_skip_and_import_succeeds(self):
        recs = [
            b'{not json\n',
            {'type': 'model_change', 'modelId': 'x' * 201},
            {'type': 'model_change', 'modelId': 12},
            b'{"type":"model_change","modelId":"bad\\ud800"}\n',
            {'type': 'model_change', 'modelId': 'x' * 200},
        ]
        d = self.export('pi', recs)
        result = publications.import_export(d, self.db)
        self.assertEqual(result['status'], 'imported')
        self.assertEqual(result['setup_skipped'], 3)
        models = [r for r in self.rows('pi') if r[0] == 'model']
        self.assertEqual(models, [('model', 'x' * 200, '')])

    def test_closed_fact_list(self):
        for agent, records in (('claude', CLAUDE), ('codex', CODEX), ('pi', PI)):
            self.run_import(agent, records)
        facts = {r[0] for r in self.sql('SELECT DISTINCT fact FROM session_setup')}
        self.assertTrue(facts <= setup_facts.FACTS, facts - setup_facts.FACTS)
        self.assertEqual(len(setup_facts.FACTS), 21)
        self.assertFalse(setup_facts.FACTS & {'thread_source', 'personality'})

    def test_rederivation_of_unchanged_publication(self):
        d = self.export('claude', CLAUDE)
        publications.import_export(d, self.db)
        before = self.sql('SELECT * FROM session_setup ORDER BY line,fact,name')
        self.assertTrue(before)
        # Simulate an old database: no setup tables at all.
        with duckdb.connect(str(self.db)) as c:
            c.execute('DROP VIEW session_dim')
            c.execute('DROP TABLE session_setup')
            c.execute('DROP TABLE session_setup_version')
        self.assertEqual(publications.import_export(d, self.db)['status'], 'unchanged')
        self.assertEqual(self.sql('SELECT * FROM session_setup ORDER BY line,fact,name'), before)
        self.assertEqual(self.sql('SELECT version FROM session_setup_version'), [(setup_facts.SETUP_VERSION,)])
        # A stale recorded version re-derives too; a current one changes nothing.
        with duckdb.connect(str(self.db)) as c:
            c.execute('UPDATE session_setup_version SET version=0')
            c.execute('DELETE FROM session_setup')
        publications.import_export(d, self.db)
        self.assertEqual(self.sql('SELECT * FROM session_setup ORDER BY line,fact,name'), before)
        publications.import_export(d, self.db)
        self.assertEqual(self.sql('SELECT * FROM session_setup ORDER BY line,fact,name'), before)
        self.assertEqual(self.sql('SELECT count(*) FROM session_setup_version'), [(1,)])

    def test_new_publication_replaces_rows_and_records_version(self):
        self.run_import('pi', PI)
        self.run_import('pi', [{'type': 'model_change', 'modelId': 'only'}], publication='p2', sequence=2)
        self.assertEqual([r for r in self.rows('pi') if r[0] == 'model'], [('model', 'only', '')])
        self.assertEqual(self.sql('SELECT count(*) FROM session_setup_version'), [(1,)])

    def test_view_shape(self):
        self.run_import('claude', CLAUDE)
        row = self.sql('SELECT agent, harness_version, entrypoint, models, skills_available, mcp_servers_available, '
                       'tools_available, instruction_files_available, permission_mode FROM session_dim')[0]
        self.assertEqual(row[:3], ('claude', '9.9', 'cli'))
        self.assertEqual(row[3], ['claude-att', 'claude-x'])
        self.assertEqual(row[4:], (2, 1, 1, 4, 'plan'))

    def test_read_only_old_database_creates_nothing(self):
        self.run_import('claude', CLAUDE)
        with duckdb.connect(str(self.db)) as c:
            c.execute('DROP VIEW session_dim')
            c.execute('DROP TABLE session_setup')
            c.execute('DROP TABLE session_setup_version')
        tables = self.sql('SHOW TABLES')
        report = publications.report(self.db, now_ns=10)
        self.assertEqual(len(report['sessions']), 1)
        import publication_dashboard
        publication_dashboard.PublicationDashboard(self.db)  # validate() on the old schema
        publication_dashboard.PublicationDashboard(self.db).report()
        self.assertEqual(self.sql('SHOW TABLES'), tables)


if __name__ == '__main__':
    unittest.main()
