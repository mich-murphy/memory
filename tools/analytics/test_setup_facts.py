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
    {'type': 'attachment', 'timestamp': 't1', 'version': '9.9', 'entrypoint': 'cli', 'cwd': f'/w/{CANARY}',
     'gitBranch': CANARY, 'attachment': {'type': 'skill_listing',
                                         'names': ['s-one', 's-two', f'.x/{CANARY}/plug-in:p-skill']}},
    {'type': 'attachment', 'attachment': {'type': 'model', 'identity': {'modelId': 'claude-att'}}},
    {'type': 'attachment', 'attachment': {'type': 'agent_listing_delta', 'addedTypes': ['reviewer']}},
    {'type': 'attachment', 'attachment': {'type': 'deferred_tools_delta', 'addedNames': ['WebFetch'],
        'failedMcpServers': [{'name': 'bad-srv'}]}},
    {'type': 'attachment', 'attachment': {'type': 'mcp_instructions_delta', 'addedNames': ['srv']}},
    {'type': 'attachment', 'attachment': {'type': 'invoked_skills', 'skills': [{'name': 's-one'}]}},
    {'type': 'attachment', 'attachment': {'type': 'command_permissions',
                                          'allowedTools': ['Bash(ls)', f'Read(~/{CANARY}/**)']}},
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
# The shape real rollouts use (parent, brief 53 real-data run): world_state, payload.state.agents_md.
CODEX_WORLD = [
    {'type': 'session_meta', 'payload': {'id': 'y', 'cwd': f'/w/{CANARY}', 'cli_version': '1.3'}},
    {'type': 'world_state', 'payload': {'state': {'agents_md': {'directory': f'/w/{CANARY}', 'text': CANARY}}}},
    {'type': 'world_state', 'payload': {'state': {'agents_md': {'text': CANARY}}}},
    {'type': 'world_state', 'payload': {'state': {'agents_md': {}}}},
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
                     ('skill_invoked', 's-one', ''), ('tool_allowed', 'Bash', ''), ('tool_allowed', 'Read', ''),
                     ('skill_available', 'plug-in:p-skill', '')]:
            self.assertIn(want, rows)
        # Parent (brief 53 real-data run): a permission pattern keeps only its tool name, and a
        # plugin skill only its last segment.
        self.assertFalse([r for r in rows if r[0] in ('tool_allowed', 'skill_available') and '(' in r[1]])

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

    def test_codex_world_state_agents_md(self):
        rows = [r for r in self.run_import('codex', CODEX_WORLD, name='cw') if r[0] == 'instruction_file']
        self.assertEqual(sorted(rows), [('instruction_file', 'AGENTS.md', 'other'),
                                        ('instruction_file', 'AGENTS.md', 'project')])

    def test_path_characters_are_skipped_outside_model(self):
        path = self.root / 'g.jsonl'
        path.write_bytes(encode([{'type': 'x'}]))
        from unittest import mock
        fake = lambda r, ctx: [('provider', '', 'a/b'), ('entrypoint', '', '~x'), ('skill_available', 'a\\b', ''),
                               ('model', 'org/model', '')]
        skipped = {}
        with mock.patch.dict(setup_facts._AGENTS, {'claude': fake}):
            rows = [(r['fact'], r['name'], r['value']) for r in setup_facts.iter_setup(path, 'claude', 't', skipped)]
        self.assertEqual(rows, [('harness', 'claude', ''), ('model', 'org/model', '')])
        self.assertEqual(skipped, {'skipped': 3})

    def test_codex_absent_approval_policy_records_nothing(self):
        # Parent (brief 53 review): only a structured policy is `granular`; an absent one is no fact.
        rows = self.run_import('codex', [
            {'type': 'session_meta', 'payload': {'id': 'x', 'cli_version': '1.2'}},
            {'type': 'turn_context', 'payload': {'model': 'gpt-x'}},
            {'type': 'turn_context', 'payload': {'model': 'gpt-x', 'approval_policy': {'granular': {}}}},
        ])
        self.assertEqual([r for r in rows if r[0] == 'approval_policy'], [('approval_policy', '', 'granular')])

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

    def test_unknown_fact_is_skipped_and_counted(self):
        # Parent (brief 53 teeth): the closed list is enforced at the validator, whatever an
        # extractor yields.
        from unittest import mock
        path = self.root / 'u.jsonl'
        path.write_bytes(encode([{'type': 'x'}]))
        # No path character in the value, so only the closed list can reject it (the path
        # guard would also catch a cwd-shaped value).
        fake = lambda r, ctx: [('cwd', '', 'plain'), ('model', 'm', '')]
        skipped = {}
        with mock.patch.dict(setup_facts._AGENTS, {'claude': fake}):
            rows = list(setup_facts.iter_setup(path, 'claude', 't', skipped))
        self.assertEqual({r['fact'] for r in rows}, {'harness', 'model'})
        self.assertEqual(skipped, {'skipped': 1})

    def test_closed_fact_list(self):
        for agent, records in (('claude', CLAUDE), ('codex', CODEX), ('pi', PI)):
            self.run_import(agent, records)
        facts = {r[0] for r in self.sql('SELECT DISTINCT fact FROM session_setup')}
        self.assertTrue(facts <= setup_facts.FACTS, facts - setup_facts.FACTS)
        # Brief 55: four used facts join the closed list (21 -> 25).
        self.assertEqual(len(setup_facts.FACTS), 25)
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

    def test_used_facts_brief_55(self):
        rows = self.run_import('claude', [
            {'type': 'assistant', 'attributionSkill': 'sk', 'attributionPlugin': 'pl@m',
             'attributionMcpServer': 'srv', 'message': {'role': 'assistant', 'content': 'a'}},
            {'type': 'assistant', 'attributionSkill': 'bad name', 'message': {'role': 'assistant'}}])
        for want in (('skill_used', 'sk', ''), ('plugin_used', 'pl@m', ''), ('mcp_server_used', 'srv', '')):
            self.assertIn(want, rows)
        self.assertFalse([r for r in rows if r[1] == 'bad name'])
        rows = self.run_import('codex', [
            {'type': 'event_msg', 'payload': {'type': 'item_completed', 'item': {
                'type': 'McpToolCall', 'server': 's1', 'pluginId': 'p1',
                'metadata': {'mcp_attribution': {'sources': [{'server_name': 's2'}]}}}}}])
        for want in (('plugin_used', 'p1', ''), ('mcp_server_used', 's1', ''), ('mcp_server_used', 's2', '')):
            self.assertIn(want, rows)
        rows = self.run_import('pi', [
            {'type': 'custom', 'customType': 'subagents:record'},
            {'type': 'message', 'message': {'role': 'assistant', 'content': [
                {'type': 'toolCall', 'name': 'read', 'arguments': {'path': f'/h/{CANARY}/skills/my-skill/SKILL.md'}}]}}])
        self.assertIn(('extension_used', 'subagents', ''), rows)
        self.assertIn(('skill_used', 'my-skill', ''), rows)
        self.assertFalse([r for r in rows if CANARY in r[1] or CANARY in r[2]])

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
        # Parent (brief 53 real-data run): the fixture gained a plugin skill, so 3 skills.
        self.assertEqual(row[4:], (3, 1, 1, 4, 'plan'))

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
