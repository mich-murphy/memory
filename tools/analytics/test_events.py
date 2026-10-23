"""Synthetic fixtures only: never print local transcript contents."""
import hashlib
import json
from pathlib import Path
import tempfile
import unittest

import duckdb

import events


class EventsTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)

    def body(self, agent, records, key=None, role='main'):
        path = self.root / f'{agent}-{len(list(self.root.glob("*.jsonl")))}.jsonl'
        data = b''.join((json.dumps(r, ensure_ascii=False) + '\n').encode() if isinstance(r, dict) else r for r in records)
        path.write_bytes(data)
        return dict(machine_id='machine', agent=agent, session_key='session',
                    file_key=key or path.name, role=role, sha256=hashlib.sha256(data).hexdigest(), path=str(path))

    def build(self, entries):
        index = self.root / 'index.json'
        index.write_text(json.dumps(entries))
        database = self.root / 'events.duckdb'
        counts = events.build(index, database)
        return duckdb.connect(str(database), read_only=True), counts

    def test_all_agents_tools_errors_humans_and_offsets(self):
        claude = self.body('claude', [
            {'type': 'user', 'uuid': 'u1', 'message': {'role': 'user', 'content': 'héllo 🌏'}},
            {'type': 'user', 'uuid': 'u2', 'message': {'role': 'user', 'content': '<task-notification>done</task-notification>'}},
            {'type': 'user', 'uuid': 'u3', 'message': {'role': 'user', 'content': '<system-reminder>injected</system-reminder>'}},
            {'type': 'assistant', 'uuid': 'a', 'message': {'id': 'shared', 'model': 'test-model', 'content': [
                {'type': 'thinking', 'thinking': 'consider'},
                {'type': 'tool_use', 'id': 'call', 'name': 'shell', 'input': {'command': 'true'}}]}},
            {'type': 'user', 'uuid': 'r', 'message': {'role': 'user', 'content': [
                {'type': 'tool_result', 'tool_use_id': 'call', 'is_error': True, 'content': 'failure'},
                {'type': 'tool_result', 'tool_use_id': 'unknown', 'content': 'no explicit status'}]}},
            b'broken\n', {'type': 'future_type', 'data': 'not duplicated as raw JSON'},
        ])
        pi = self.body('pi', [
            {'type': 'session', 'id': 's', 'cwd': '/work'},
            {'type': 'message', 'id': 'p1', 'parentId': 's', 'message': {'role': 'user', 'content': 'fix this'}},
            {'type': 'message', 'id': 'p2', 'parentId': 'p1', 'message': {'role': 'assistant', 'content': [
                {'type': 'thinking', 'thinking': 'hmm'},
                {'type': 'toolCall', 'id': 't1', 'name': 'read', 'arguments': {'path': 'x'}},
                {'type': 'toolCall', 'id': 't2', 'name': 'write', 'arguments': {'path': 'y'}}]}},
            {'type': 'message', 'id': 'p3', 'message': {'role': 'toolResult', 'toolCallId': 't1', 'toolName': 'read', 'isError': False, 'content': [{'type': 'text', 'text': 'ok'}]}},
        ], key='child.jsonl', role='subagent')
        db, counts = self.build([claude, pi])
        with db:
            self.assertEqual(counts['issues'], 2)
            self.assertEqual(db.execute('SELECT count(*) FROM events WHERE is_human').fetchone()[0], 1)
            self.assertEqual(db.execute("SELECT origin,is_human,text FROM events WHERE native_id='p1'").fetchone(), ('agent', False, 'fix this'))
            self.assertEqual(db.execute("SELECT count(*) FROM events WHERE event_type='tool_call'").fetchone()[0], 3)
            self.assertEqual(db.execute('SELECT count(*) FROM events WHERE is_error').fetchone()[0], 1)
            self.assertEqual(db.execute("SELECT is_error FROM events WHERE tool_call_id='unknown'").fetchone()[0], None)
            self.assertEqual(db.execute("SELECT is_error FROM events WHERE native_id='p3'").fetchone()[0], False)
            rows = db.execute("SELECT event_id,line,ordinal,byte_offset,byte_length,text FROM events WHERE trace_id=? ORDER BY line,ordinal", [events.trace_id('machine', 'claude', claude['file_key'])]).fetchall()
            raw = Path(claude['path']).read_bytes()
            for eid, line, ordinal, offset, length, text in rows:
                self.assertEqual(eid, f'{events.trace_id("machine", "claude", claude["file_key"])}:{line}:{ordinal}')
                self.assertEqual(json.loads(raw[offset:offset + length])['type'], 'user' if line in (1, 2, 3, 5) else 'assistant' if line == 4 else 'future_type')
            self.assertEqual(rows[1][3], len(raw.splitlines(keepends=True)[0]))
            self.assertEqual(db.execute('SELECT count(DISTINCT trace_id) FROM files').fetchone()[0], 2)
        self.assertEqual((self.root / 'events.duckdb').stat().st_mode & 0o777, 0o600)

    def test_dedup_uuid_preferred_chunks_and_codex_mirrors(self):
        first = {'type': 'assistant', 'uuid': 'a', 'message': {'id': 'shared', 'content': [{'type': 'text', 'text': 'one'}]}}
        second = {'type': 'assistant', 'uuid': 'b', 'message': {'id': 'shared', 'content': [{'type': 'text', 'text': 'two'}]}}
        fallback1 = {'type': 'assistant', 'message': {'id': 'fallback', 'content': 'chunk 1'}}
        fallback2 = {'type': 'assistant', 'message': {'id': 'fallback', 'content': 'chunk 2'}}
        claude = self.body('claude', [first, second, first, fallback1, fallback2, fallback1])
        codex = self.body('codex', [
            {'type': 'event_msg', 'payload': {'type': 'user_message', 'message': 'again'}},
            {'type': 'response_item', 'payload': {'type': 'message', 'id': 'u1', 'role': 'user', 'content': [{'type': 'input_text', 'text': 'again'}]}},
            {'type': 'response_item', 'payload': {'type': 'message', 'id': 'u2', 'role': 'user', 'content': [{'type': 'input_text', 'text': 'again'}]}},
            {'type': 'event_msg', 'payload': {'type': 'user_message', 'message': 'again'}},
            {'type': 'event_msg', 'payload': {'type': 'user_message', 'message': 'unpaired'}},
            {'type': 'response_item', 'payload': {'type': 'function_call', 'call_id': 'c', 'name': 'exec', 'arguments': '{"cmd":"false"}'}},
            {'type': 'response_item', 'payload': {'type': 'function_call_output', 'call_id': 'c', 'output': 'exit code 1'}},
        ])
        db, counts = self.build([claude, codex])
        with db:
            self.assertEqual(counts['duplicates'], 4)
            self.assertEqual(db.execute('SELECT count(*) FROM events WHERE is_human').fetchone()[0], 3)
            self.assertEqual(db.execute('SELECT count(*) FROM events WHERE is_mirror').fetchone()[0], 2)
            self.assertEqual(db.execute("SELECT is_error FROM events WHERE event_type='tool_result'").fetchone()[0], None)
            args = db.execute("SELECT tool_args FROM events WHERE event_type='tool_call'").fetchone()[0]
            self.assertEqual(json.loads(args), {'cmd': 'false'})

    def test_relative_index_coverage_flags_and_structural_issues(self):
        body = self.body('pi', [
            {'type': 'model_change', 'id': 'm', 'modelId': 'model-x'},
            {'type': 'message', 'id': 'u', 'message': {'role': 'user', 'content': '<environment_context>cwd</environment_context>'}},
            {'type': 'message', 'id': 'bad', 'message': {'role': ['invalid'], 'content': 'bad role'}},
            b'[]\n',
            {'type': 'message', 'id': 'ok', 'message': {'role': 'assistant', 'content': 'fine'}},
        ])
        body['path'] = Path(body['path']).name
        index = self.root / 'index.json'
        index.write_text(json.dumps({'entries': [body], 'complete': False, 'limited': True,
                                     'failures': [{'code': 'unavailable'}]}))
        database = self.root / 'events.duckdb'
        counts = events.build(index, database)
        self.assertEqual(counts['issues'], 2)
        with duckdb.connect(str(database), read_only=True) as db:
            self.assertEqual(db.execute('SELECT index_complete,index_limited,index_failures FROM build_info').fetchone(), (False, True, 1))
            self.assertEqual(db.execute('SELECT count(*) FROM events WHERE is_human').fetchone()[0], 0)
            self.assertEqual(db.execute("SELECT model FROM events WHERE native_id='ok'").fetchone()[0], 'model-x')
            self.assertEqual(db.execute('SELECT count(*) FROM events WHERE byte_offset != "offset" OR byte_length != length').fetchone()[0], 0)

    def test_embedded_handbacks_and_injected_transcript_are_not_humans(self):
        injections = ['Another Claude session sent a message:\n<agent-message from="worker">report</agent-message>',
            'The coordinator sent a message while you were working:\nfix this',
            'Base directory for this skill: /skills/example\n# Instructions',
            ' [3] tool js call: {"code":"x"}', '>>> TRANSCRIPT DELTA START',
            '<recommended_plugins>list</recommended_plugins>', '[Image: original 100x100]']
        for text in injections:
            with self.subTest(text=text[:35]):
                row = events.normalize_record({'type':'user','message':{'role':'user','content':text}}, 'claude')[0]
                self.assertFalse(row['is_human'])
        row = events.normalize_record({'type':'user','message':{'role':'user','content':
            '# Annotations on assistant\n> This still breaks the real requirement.'}}, 'claude')[0]
        self.assertTrue(row['is_human'])
        agent = events.normalize_record({'type':'response_item','payload':{'type':'agent_message',
            'author':'/root/worker','recipient':'/root','content':[{'type':'text','text':'report'},
            {'type':'encrypted_content','encrypted_content':'opaque'}]}}, 'codex')
        self.assertEqual(agent[0]['event_type'],'message')
        self.assertEqual(agent[0]['origin'],'agent')
        self.assertFalse(agent[0]['is_human'])
        self.assertEqual(agent[1]['event_type'],'system')
        self.assertEqual(agent[1]['text'],'')
        metadata = events.normalize_record({'agentType':'Explore','toolUseId':'call','description':'Find files'},'claude')[0]
        self.assertEqual(metadata['event_type'],'system')

    def test_atomic_failure_and_metadata(self):
        body = self.body('pi', [{'type': 'message', 'id': 'p', 'message': {'role': 'user', 'content': 'hi'}}])
        db, _ = self.build([body])
        db.close()
        database = self.root / 'events.duckdb'
        before = database.read_bytes()
        Path(body['path']).write_text('{}\n')
        with self.assertRaises(ValueError):
            events.build(self.root / 'index.json', database)
        self.assertEqual(database.read_bytes(), before)
        metadata = self.root / 'sessions.duckdb'
        with duckdb.connect(str(metadata)) as conn:
            conn.execute('CREATE TABLE sessions(machine_id VARCHAR,agent VARCHAR,session_key VARCHAR,info JSON,git JSON)')
            conn.execute('INSERT INTO sessions VALUES (?,?,?,?,?)', ['machine', 'pi', 'session', '{"cwd":"/work/sub","model":"m"}', '{"root":"/work"}'])
        self.assertEqual(events._metadata(metadata)[('machine', 'pi', 'session')]['project'], '/work')


if __name__ == '__main__':
    unittest.main()
