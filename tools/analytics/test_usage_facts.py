"""Synthetic fixtures only: usage, tool outcomes and used facts through import_export (brief 55)."""
import json
import unittest

import duckdb
import publications
import setup_facts
import usage_facts
from test_setup_facts import Base, CANARY

TOK = ('input_tokens', 'cache_read_tokens', 'cache_write_tokens', 'output_tokens', 'reasoning_tokens')
USAGE = 'SELECT line, model, input_tokens, cache_read_tokens, cache_write_tokens, output_tokens, ' \
        'reasoning_tokens, cost_usd FROM session_usage WHERE agent=? ORDER BY line'
OUT = 'SELECT line, tool_call_id, tool_name, is_error, exit_code, duration_ms FROM tool_outcome ' \
      'WHERE agent=? ORDER BY line'


def claude_assistant(mid, usage, content=None, model='claude-x'):
    return {'type': 'assistant', 'message': {'id': mid, 'role': 'assistant', 'model': model,
                                             'usage': usage, 'content': content or []}}


def count(total, last):
    return {'type': 'event_msg', 'payload': {'type': 'token_count', 'info': {
        'total_token_usage': {'total_tokens': total}, 'last_token_usage': last}}}


def item(kind, **kw):
    return {'type': 'event_msg', 'payload': {'type': 'item_completed', 'item': dict(type=kind, **kw)}}


def pi_msg(usage):
    return {'type': 'message', 'message': {'role': 'assistant', 'model': 'pm', 'usage': usage, 'content': []}}


class UsageTest(Base):
    def test_claude_usage_dedup_and_null(self):
        u = {'input_tokens': 3, 'cache_read_input_tokens': 5, 'cache_creation_input_tokens': 7,
             'output_tokens': 11, 'output_tokens_details': {'thinking_tokens': 2}}
        self.run_import('claude', [claude_assistant('m1', u), claude_assistant('m1', u),
                                   claude_assistant('m2', {'output_tokens': 4})])
        self.assertEqual(self.sql(USAGE, ['claude']), [
            (1, 'claude-x', 3, 5, 7, 11, 2, None), (3, 'claude-x', None, None, None, 4, None, None)])

    def test_claude_tool_outcome(self):
        use = [{'type': 'tool_use', 'id': 'u1', 'name': 'Bash', 'input': {'command': CANARY}}]
        res = {'type': 'user', 'toolUseResult': {'durationSeconds': 1.5, 'stdout': CANARY},
               'message': {'role': 'user', 'content': [{'type': 'tool_result', 'tool_use_id': 'u1',
                                                        'is_error': True, 'content': CANARY}]}}
        res2 = {'type': 'user', 'toolUseResult': {'durationMs': 9},
                'message': {'role': 'user', 'content': [{'type': 'tool_result', 'tool_use_id': 'zz'}]}}
        self.run_import('claude', [claude_assistant('m', {'output_tokens': 1}, use), res, res2])
        self.assertEqual(self.sql(OUT, ['claude']), [(2, 'u1', 'Bash', True, None, 1500),
                                                     (3, 'zz', None, None, None, 9)])

    def test_codex_usage_dedup_and_total(self):
        self.run_import('codex', [
            {'type': 'turn_context', 'payload': {'model': 'gx'}},
            count(10, {'input_tokens': 8, 'cached_input_tokens': 3, 'cache_write_input_tokens': 1,
                       'output_tokens': 2, 'reasoning_output_tokens': 1}),
            count(10, {'input_tokens': 8, 'cached_input_tokens': 3, 'output_tokens': 2}),
            count(25, {'input_tokens': 20, 'cached_input_tokens': 5, 'output_tokens': 5})])
        self.assertEqual(self.sql(USAGE, ['codex']), [(2, 'gx', 5, 3, 1, 2, 1, None),
                                                      (4, 'gx', 15, 5, None, 5, None, None)])
        self.assertEqual(self.sql('SELECT output_tokens FROM session_dim WHERE agent=?', ['codex']), [(7,)])

    def test_codex_negative_input_skipped_and_counted(self):
        d = self.export('codex', [count(5, {'input_tokens': 1, 'cached_input_tokens': 2, 'output_tokens': 3})])
        result = publications.import_export(d, self.db)
        self.assertEqual(result['usage_skipped'], 1)
        self.assertEqual(self.sql(USAGE, ['codex']), [(1, None, None, 2, None, 3, None, None)])

    def test_codex_outcomes(self):
        self.run_import('codex', [
            item('CommandExecution', id='c', exit_code=2, command=CANARY, cwd=f'/w/{CANARY}',
                 aggregated_output=CANARY, duration={'secs': 1, 'nanos': 500000000}),
            item('McpToolCall', id='call_1', server='srv', tool='t', duration={'secs': 0, 'nanos': 2000000})])
        self.assertEqual(self.sql(OUT, ['codex']), [(1, None, 'CommandExecution', None, 2, 1500),
                                                    (2, 'call_1', 'McpToolCall', None, None, 2)])

    def test_pi_usage_and_outcomes(self):
        self.run_import('pi', [
            pi_msg({'input': 1, 'output': 2, 'cacheRead': 3, 'cacheWrite': 4, 'reasoning': 5,
                    'cost': {'total': 0.25}}),
            pi_msg({'output': 2, 'cost': {'total': 3}}),
            {'type': 'compaction', 'usage': {'input': 9, 'output': 1}},
            {'type': 'message', 'message': {'role': 'toolResult', 'toolCallId': 'a', 'toolName': 'read',
                                            'isError': False, 'durationMs': 7, 'content': CANARY}},
            {'type': 'message', 'message': {'role': 'bashExecution', 'exitCode': 1, 'command': CANARY,
                                            'output': CANARY}}])
        self.assertEqual(self.sql(USAGE, ['pi']), [
            (1, 'pm', 1, 3, 4, 2, 5, 0.25), (2, 'pm', None, None, None, 2, None, 3.0),
            (3, 'pm', 9, None, None, 1, None, None)])
        self.assertEqual(self.sql(OUT, ['pi']), [(4, 'a', 'read', False, None, 7),
                                                 (5, None, 'bash', None, 1, None)])

    def test_bad_values_skipped_and_counted(self):
        d = self.export('pi', [b'{"type":"message","message":{"role":"assistant","usage":{"input":-1,'
                               b'"output":1.5,"cacheRead":true,"cacheWrite":9007199254740992,"reasoning":4,'
                               b'"cost":{"total":-1}}}}\n'])
        result = publications.import_export(d, self.db)
        self.assertEqual(result['usage_skipped'], 5)
        self.assertEqual(self.sql(USAGE, ['pi']), [(1, None, None, None, None, None, 4, None)])

    def test_canary_never_stored(self):
        use = [{'type': 'tool_use', 'id': 'u1', 'name': 'Bash', 'input': {'command': CANARY}}]
        self.run_import('claude', [claude_assistant('m', {'output_tokens': 1}, use), {
            'type': 'user', 'cwd': f'/w/{CANARY}', 'toolUseResult': {'durationMs': 1, 'stdout': CANARY},
            'message': {'content': [{'type': 'tool_result', 'tool_use_id': 'u1', 'content': CANARY}]}}])
        self.run_import('codex', [item('CommandExecution', exit_code=0, command=CANARY, cwd=f'/w/{CANARY}',
                                       duration={'secs': 0, 'nanos': 0})])
        self.run_import('pi', [
            {'type': 'message', 'message': {'role': 'toolResult', 'toolCallId': 'a', 'toolName': 'read',
                                            'isError': False, 'content': CANARY}},
            {'type': 'message', 'message': {'role': 'assistant', 'content': [{
                'type': 'toolCall', 'name': 'read',
                'arguments': {'path': f'/h/{CANARY}/skills/my-skill/SKILL.md'}}]}}])
        report = json.dumps(publications.report(self.db, summary=True)['setup'])
        self.assertNotIn(CANARY, report)
        with duckdb.connect(str(self.db), read_only=True) as c:
            for table in ('session_usage', 'tool_outcome', 'session_setup', 'session_dim', 'usage_by_model'):
                for col in [r[0] for r in c.execute(f'DESCRIBE {table}').fetchall()]:
                    hits = c.execute(f'SELECT count(*) FROM {table} WHERE CAST("{col}" AS VARCHAR) ILIKE ?',
                                     ['%canary%']).fetchone()[0]
                    self.assertEqual(hits, 0, (table, col))
        self.assertIn(('skill_used', 'my-skill', ''), self.rows('pi'))

    def test_views(self):
        self.run_import('pi', [pi_msg({'input': 1, 'output': 2, 'cost': {'total': 0.5}}),
                               pi_msg({'input': 2}),
                               {'type': 'message', 'message': {'role': 'toolResult', 'isError': True,
                                                               'durationMs': 5}},
                               {'type': 'custom', 'customType': 'ext:x'}])
        row = self.sql('SELECT input_tokens, cache_read_tokens, output_tokens, cost_usd, tool_results, '
                       'tool_errors, tool_duration_ms, extensions_used, skills_used FROM session_dim')[0]
        self.assertEqual(row, (3, None, 2, 0.5, 1, 1, 5, 1, 0))
        self.assertEqual(self.sql('SELECT agent, model, sessions, input_tokens FROM usage_by_model'),
                         [('pi', 'pm', 1, 3)])

    def test_report_setup_groups(self):
        self.run_import('pi', [pi_msg({'input': 1}), {'type': 'custom', 'customType': 'ext:x'},
                               {'type': 'message', 'message': {'role': 'toolResult', 'isError': False}}])
        setup = publications.report(self.db, summary=True)['setup']
        self.assertEqual(set(setup), {'harness_versions', 'models', 'reasoning_efforts', 'permissions',
                                      'usage_by_model', 'skills_used', 'plugins_used', 'mcp_servers_used',
                                      'extensions_used', 'tool_results'})
        self.assertEqual(setup['extensions_used'], [{'agent': 'pi', 'extension': 'ext', 'sessions': 1}])
        self.assertEqual(setup['tool_results'][0]['results'], 1)

    def test_version_one_database_rederives_once(self):
        d = self.export('pi', [pi_msg({'input': 1}), {'type': 'custom', 'customType': 'ext:x'}])
        publications.import_export(d, self.db)
        want = [self.sql(f'SELECT * FROM {t} ORDER BY line') for t in ('session_usage', 'tool_outcome')]
        with duckdb.connect(str(self.db)) as c:  # simulate version 1: no new tables, no views
            for v in ('session_dim', 'usage_by_model'):
                c.execute(f'DROP VIEW {v}')
            c.execute('DROP TABLE session_usage')
            c.execute('DROP TABLE tool_outcome')
            c.execute('DELETE FROM session_setup WHERE fact=\'extension_used\'')
            c.execute('UPDATE session_setup_version SET version=1')
        tables = self.sql('SHOW TABLES')
        self.assertEqual(publications.report(self.db, summary=True)['setup'], {})
        publications.report(self.db)
        self.assertEqual(self.sql('SHOW TABLES'), tables)  # read-only creates nothing
        self.assertEqual(publications.import_export(d, self.db)['status'], 'unchanged')
        self.assertEqual([self.sql(f'SELECT * FROM {t} ORDER BY line') for t in ('session_usage', 'tool_outcome')], want)
        self.assertIn(('extension_used', 'ext', ''), self.rows('pi'))
        self.assertEqual(self.sql('SELECT version FROM session_setup_version'), [(setup_facts.SETUP_VERSION,)])
        snap = [self.sql(f'SELECT * FROM {t} ORDER BY 1,2,3') for t in ('session_usage', 'session_setup')]
        publications.import_export(d, self.db)
        self.assertEqual([self.sql(f'SELECT * FROM {t} ORDER BY 1,2,3') for t in ('session_usage', 'session_setup')], snap)

    def test_reset_clears_new_tables(self):
        d = self.export('pi', [pi_msg({'input': 1})])
        publications.import_export(d, self.db)
        publications.import_export(d, self.db, reset=True)
        self.assertEqual(self.sql('SELECT count(*) FROM session_usage'), [(1,)])
        self.assertEqual(usage_facts.TABLES, ('session_usage', 'tool_outcome'))


if __name__ == '__main__':
    unittest.main()
