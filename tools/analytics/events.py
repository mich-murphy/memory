#!/usr/bin/env python3
"""Legacy body-index normalization; writes a private, atomically replaced DB.

Not a candidate publication importer; source/epoch identities are absent.
See docs/session-capture-contract.md. Existing historical reports remain usable.

Body index paths are interpreted relative to the index folder. Raw JSON is not copied: offsets reference verified body files.
Consumers should filter NOT duplicate and is_human for human-text analyses.
Timestamps retain source spelling/units; NULL errors mean unknown, not success.
"""
import argparse
from collections import Counter
import hashlib
import json
import os
from pathlib import Path
import re
import sys
import tempfile

import duckdb

EVENT_COLUMNS = {
    'event_id': 'VARCHAR', 'trace_id': 'VARCHAR', 'line': 'BIGINT',
    'offset': 'BIGINT', 'length': 'BIGINT', 'ordinal': 'INTEGER',
    'native_id': 'VARCHAR', 'parent_id': 'VARCHAR', 'raw_type': 'VARCHAR',
    'event_type': 'VARCHAR', 'timestamp': 'VARCHAR', 'role': 'VARCHAR', 'origin': 'VARCHAR',
    'text': 'VARCHAR', 'tool_name': 'VARCHAR', 'tool_call_id': 'VARCHAR',
    'tool_args': 'JSON', 'is_error': 'BOOLEAN', 'model': 'VARCHAR',
    'is_human': 'BOOLEAN', 'duplicate': 'BOOLEAN', 'duplicate_of': 'VARCHAR',
    'is_mirror': 'BOOLEAN', 'byte_offset': 'BIGINT', 'byte_length': 'BIGINT',
}
FILE_COLUMNS = {k: 'VARCHAR' for k in (
    'trace_id', 'machine_id', 'agent', 'session_key', 'file_key', 'source_role',
    'sha256', 'path', 'file_commit', 'cwd', 'project', 'started_at', 'model')}
FILE_COLUMNS.update({k: 'BIGINT' for k in ('line_count', 'event_count', 'issue_count', 'duplicate_count')})
ISSUE_COLUMNS = {'trace_id': 'VARCHAR', 'line': 'BIGINT', 'offset': 'BIGINT',
                 'length': 'BIGINT', 'kind': 'VARCHAR', 'record_type': 'VARCHAR'}
SYNTHETIC = re.compile(
    r'<(?:task-notification|system-reminder|environment_context|environment_details|'
    r'environment|local-command-caveat|local-command-stdout|command-name|'
    r'permissions instructions|subagent_notification)\b|'
    r'^\s*(?:# AGENTS\.md instructions|<INSTRUCTIONS>|\[Request interrupted|'
    r'Another Claude session sent a message:|The coordinator sent a message while you were working:|'
    r'Base directory for this skill:|\[Subagent hand-back\]|\[\d+\] tool (?:exec|js) call:|'
    r'Reviewed Codex session id:|>>> TRANSCRIPT DELTA|<recommended_plugins>|\[Image:|<bash-(?:input|stdout|stderr)>)|<agent-message\b', re.I)


def trace_id(machine_id, agent, file_key):
    """File identity, not parent session identity (children stay distinct)."""
    return hashlib.sha256('\0'.join((machine_id, agent, file_key)).encode()).hexdigest()[:32]


def _string(value):
    if value is None or isinstance(value, str):
        return value
    return json.dumps(value, ensure_ascii=False, separators=(',', ':'))


def _text(content):
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        return '\n'.join(filter(None, (_text(c) for c in content)))
    if isinstance(content, dict):
        return content.get('text') or content.get('thinking') or content.get('summary') or ''
    return ''


def normalize_record(record, agent):
    """Return normalized components (ordinals assigned by iter_events).

    Unknown records are represented by one unknown event plus an issue, never
    silently discarded. Control records remain traceable system events.
    """
    typ = record.get('type', 'unknown')
    message = record.get('message') if isinstance(record.get('message'), dict) else {}
    payload = record.get('payload') if isinstance(record.get('payload'), dict) else {}
    native = record.get('uuid') or record.get('id') or message.get('id') or payload.get('id') or (payload.get('call_id') if agent == 'codex' else None)
    base = dict(native_id=_string(native), parent_id=_string(record.get('parentUuid', record.get('parentId', payload.get('parent_id')))),
                raw_type=str(typ), timestamp=_string(record.get('timestamp', message.get('timestamp'))),
                model=_string(message.get('model', record.get('model', record.get('modelId', payload.get('model'))))))
    role = message.get('role', record.get('role', 'system'))
    content = message.get('content', record.get('content', ''))
    kind = None
    if agent == 'claude':
        if typ == 'unknown' and 'agentType' in record and 'toolUseId' in record:
            kind, content = 'system', record.get('description', '')
        elif typ in ('user', 'assistant'):
            role = message.get('role', typ)
        elif typ in ('system', 'summary', 'progress', 'queue-operation', 'attachment',
                     'file-history-snapshot', 'last-prompt', 'custom-title', 'agent-name',
                     'pr-link', 'saved_hook_context', 'mode', 'ai-title', 'file-history-delta',
                     'permission-mode', 'cost-state', 'atis-latch'):
            kind = 'system'
            content = record.get('content', record.get('summary', ''))
        else:
            kind = 'unknown'
    elif agent == 'pi':
        if typ != 'message':
            kind = 'system' if typ in ('session', 'model_change', 'thinking_level_change',
                'session_info', 'custom', 'custom_message', 'compaction', 'branch_summary', 'label',
                'context_edit', 'token_usage_record', 'inter_agent_communication_metadata', 'world_state') else 'unknown'
            content = record.get('summary', record.get('content', ''))
        if role == 'toolResult':
            kind, role = 'tool_result', 'tool'
    elif agent == 'codex':
        base['raw_type'] = f'{typ}.{payload.get("type", "unknown")}'
        if typ == 'response_item':
            ptype = payload.get('type')
            role, content = payload.get('role', 'assistant'), payload.get('content', '')
            if ptype in ('function_call', 'custom_tool_call'):
                content = [dict(type='tool_use', name=payload.get('name'), id=payload.get('call_id'),
                                input=payload.get('arguments', payload.get('input')))]
            elif ptype in ('function_call_output', 'custom_tool_call_output'):
                kind, role, content = 'tool_result', 'tool', payload.get('output', '')
            elif ptype == 'agent_message':
                # Codex internal agent-to-agent messages are not human turns.
                kind, role = 'message', 'assistant'
            elif ptype == 'reasoning':
                kind, content = 'thinking', payload.get('summary', payload.get('content', ''))
            elif ptype != 'message':
                kind = 'unknown'
        elif typ == 'event_msg' and payload.get('type') in ('user_message', 'agent_message'):
            role = 'user' if payload['type'] == 'user_message' else 'assistant'
            content = payload.get('message', '')
        elif typ in ('session_meta', 'turn_context', 'event_msg', 'compacted'):
            kind, content = 'system', payload.get('message', '')
        else:
            kind = 'unknown'
    else:
        kind = 'unknown'
    blocks = content if isinstance(content, list) else [content]
    if not blocks:
        blocks = ['']
    rows = []
    for block in blocks:
        b = block if isinstance(block, dict) else {}
        bt = b.get('type')
        event_kind, event_role = kind or 'message', role
        name = call = args = None
        error = None
        text = _text(block)
        if bt in ('tool_use', 'toolCall'):
            event_kind, event_role = 'tool_call', 'assistant'
            name, call = b.get('name'), b.get('id')
            args = b.get('input', b.get('arguments'))
            # Codex encodes JSON arguments as a string; retain invalid JSON as a JSON string.
            if isinstance(args, str):
                try:
                    args = json.loads(args)
                except ValueError:
                    pass
            text = ''
        elif bt == 'tool_result':
            event_kind, event_role = 'tool_result', 'tool'
            text, call = _text(b.get('content')), b.get('tool_use_id')
            error = b.get('is_error') if isinstance(b.get('is_error'), bool) else None
        elif bt in ('thinking', 'reasoning', 'redacted_thinking'):
            event_kind = 'thinking'
        elif bt == 'encrypted_content':
            # Preserve the original opaque block through byte offsets, never decode it.
            event_kind, event_role, text = 'system', 'system', ''
        elif bt not in (None, 'text', 'input_text', 'output_text', 'summary_text', 'image', 'input_image') and not kind:
            event_kind = 'unknown'
        if event_kind == 'tool_result':
            call = call or message.get('toolCallId') or payload.get('call_id')
            name = message.get('toolName')
            if isinstance(message.get('isError'), bool):
                error = message['isError']
            elif isinstance(payload.get('is_error'), bool):
                error = payload['is_error']
        origin = {'user': 'human', 'assistant': 'agent', 'tool': 'tool', 'system': 'system',
                  'developer': 'system'}.get(event_role, 'unknown')
        human = origin == 'human' and event_kind == 'message' and not SYNTHETIC.search(text)
        if origin == 'human' and not human:
            origin = 'system'
        rows.append(dict(base, event_type=event_kind, role=event_role, origin=origin,
                         text=text, tool_name=name, tool_call_id=call,
                         tool_args=json.dumps(args, ensure_ascii=False) if args is not None else None,
                         is_error=error, is_human=bool(human), duplicate=False,
                         duplicate_of=None, is_mirror=False))
    return rows


def iter_events(path, agent, trace):
    """Yield (event, issue) pairs, streaming bytes with exact Unicode-safe offsets."""
    seen = {}
    offset = 0
    model = None
    with Path(path).open('rb') as source:
        for number, raw in enumerate(source, 1):
            loc = dict(trace_id=trace, line=number, offset=offset, length=len(raw))
            offset += len(raw)
            try:
                record = json.loads(raw)
                if not isinstance(record, dict):
                    raise ValueError('not an object')
            except (ValueError, UnicodeDecodeError):
                yield None, dict(loc, kind='malformed_json', record_type=None)
                continue
            try:
                components = normalize_record(record, agent)
            except (TypeError, AttributeError, ValueError):
                yield None, dict(loc, kind='malformed_record', record_type=str(record.get('type', 'unknown')))
                continue
            for ordinal, row in enumerate(components):
                model = row['model'] or model
                row['model'] = model
                row.update(loc, ordinal=ordinal, byte_offset=loc['offset'], byte_length=loc['length'], event_id=f'{trace}:{number}:{ordinal}')
                native = row['native_id']
                # Claude fallback message.id can span distinct chunks: never collapse
                # different payloads merely because they share a message id.
                suffix = ''
                if agent == 'claude' and not record.get('uuid'):
                    # Use component content, not line provenance, in this fallback.
                    suffix = hashlib.sha256(json.dumps([row[k] for k in ('text', 'tool_args', 'tool_call_id')]).encode()).hexdigest()
                key = (native, ordinal, row['event_type'], suffix)
                if native and key in seen:
                    row.update(duplicate=True, duplicate_of=seen[key], is_human=False)
                elif native:
                    seen[key] = row['event_id']
                issue = dict(loc, kind='unknown_record', record_type=str(record.get('type', 'unknown'))) if row['event_type'] == 'unknown' else None
                yield row, issue


def _metadata(path):
    if not path or not Path(path).exists():
        return {}
    result = {}
    with duckdb.connect(str(path), read_only=True) as db:
        for machine, agent, session, info, git in db.execute('SELECT machine_id, agent, session_key, info, git FROM sessions').fetchall():
            info = json.loads(info) if isinstance(info, str) else (info or {})
            git = json.loads(git) if isinstance(git, str) else (git or {})
            result[(machine, agent, session)] = dict(cwd=info.get('cwd'), project=git.get('root') or info.get('cwd'),
                                                   started_at=_string(info.get('started_at')), model=info.get('model'))
    return result


class Batch:
    """Bounded JSONL batches avoid per-row DuckDB transactions and pandas."""
    def __init__(self, db, table, columns, folder):
        self.db, self.table, self.columns = db, table, columns
        self.path = Path(folder) / (table + '.jsonl')
        self.rows, self.size = [], 0

    def add(self, row):
        values = {k: row.get(k) for k in self.columns}
        for key, typ in self.columns.items():
            if typ == 'JSON' and values[key] is not None:
                values[key] = json.loads(values[key])
        encoded = json.dumps(values, ensure_ascii=False)
        self.rows.append(encoded)
        self.size += len(encoded)
        if len(self.rows) >= 4000 or self.size >= 8 << 20:
            self.flush()

    def flush(self):
        if not self.rows:
            return
        self.path.write_text('\n'.join(self.rows) + '\n')
        os.chmod(self.path, 0o600)
        spec = '{' + ','.join(f"'{k}':'{v}'" for k, v in self.columns.items()) + '}'
        self.db.execute(f"INSERT INTO {self.table} SELECT * FROM read_json(?, columns={spec}, format='newline_delimited', maximum_object_size=268435456)", [str(self.path)])
        self.rows, self.size = [], 0


def build(index=Path('.analytics/bodies/index.json'), database=Path('.analytics/events.duckdb'), metadata=None):
    """Build a complete replacement; failures leave the last database untouched.

    Returns counts only, never transcript contents. Index is a list or {entries:list} (legacy {files:list} accepted).
    """
    index, database = Path(index), Path(database)
    entries = json.loads(index.read_text())
    index_info = entries if isinstance(entries, dict) else {}
    if isinstance(entries, dict):
        entries = entries.get('entries', entries.get('files'))
        if not isinstance(entries, list):
            raise ValueError('index requires entries list')
    meta = _metadata(metadata)
    database.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    counts = Counter()
    with tempfile.TemporaryDirectory(prefix='.events-', dir=database.parent) as folder:
        os.chmod(folder, 0o700)
        temporary = Path(folder) / 'events.duckdb'
        with duckdb.connect(str(temporary)) as db:
            batches = {}
            for table, columns in [('events', EVENT_COLUMNS), ('files', FILE_COLUMNS), ('issues', ISSUE_COLUMNS)]:
                db.execute(f'CREATE TABLE {table} (' + ','.join(f'"{k}" {v}' for k, v in columns.items()) + ')')
                batches[table] = Batch(db, table, columns, folder)
            identities = set()
            for entry in entries:
                trace = trace_id(entry['machine_id'], entry['agent'], entry['file_key'])
                if trace in identities:
                    raise ValueError('duplicate file identity in body index')
                identities.add(trace)
                path = Path(entry['path'])
                if not path.is_absolute():
                    path = index.parent / path
                # Verify whole body before publishing offsets; importer normally did this too.
                with path.open('rb') as source:
                    digest = hashlib.file_digest(source, 'sha256').hexdigest()
                if digest != entry['sha256']:
                    raise ValueError('body checksum mismatch')
                file = dict(meta.get((entry['machine_id'], entry['agent'], entry['session_key']), {}))
                file.update({k: entry[k] for k in FILE_COLUMNS if k in entry})
                file['project'] = file.get('project') or file.get('cwd')
                file.update(trace_id=trace, path=str(path.resolve()), source_role=entry.get('role'),
                            line_count=0, event_count=0, issue_count=0, duplicate_count=0)
                for event, issue in iter_events(path, entry['agent'], trace):
                    loc = event or issue
                    file['line_count'] = max(file['line_count'], loc['line'])
                    if event:
                        # Delegated task prompts are agent assignments, not human corrections.
                        if entry.get('role') == 'subagent' and event['role'] == 'user':
                            event.update(is_human=False, origin='agent')
                        batches['events'].add(event)
                        file['event_count'] += 1
                        file['duplicate_count'] += int(event['duplicate'])
                    if issue:
                        batches['issues'].add(issue)
                        file['issue_count'] += 1
                batches['files'].add(file)
                counts.update(files=1, events=file['event_count'], issues=file['issue_count'])
            for batch in batches.values():
                batch.flush()
            # Pair mirrors by occurrence, not just text: repeated real prompts count.
            # Match the whole line's normalized text, including multi-block messages.
            db.execute("""CREATE TEMP TABLE mirrors AS
                WITH lines AS (
                  SELECT trace_id,line,raw_type,
                         string_agg(text, '\n' ORDER BY ordinal) AS text,
                         min(event_id) AS event_id
                  FROM events WHERE role='user' AND NOT duplicate
                  AND raw_type IN ('response_item.message','event_msg.user_message')
                  GROUP BY trace_id,line,raw_type
                ), ranked AS (
                  SELECT *, row_number() OVER(PARTITION BY trace_id,raw_type,text ORDER BY line) n
                  FROM lines
                )
                SELECT a.trace_id,a.line,b.event_id FROM ranked a JOIN ranked b
                ON a.trace_id=b.trace_id AND a.text=b.text AND a.n=b.n
                WHERE a.raw_type='event_msg.user_message' AND b.raw_type='response_item.message'
            """)
            db.execute('''UPDATE events SET is_mirror=true,duplicate=true,is_human=false,duplicate_of=m.event_id
                FROM mirrors m WHERE events.trace_id=m.trace_id AND events.line=m.line''')
            db.execute('''UPDATE files SET duplicate_count=(SELECT count(*) FROM events e
                WHERE e.trace_id=files.trace_id AND e.duplicate)''')
            counts['duplicates'] = db.execute('SELECT count(*) FROM events WHERE duplicate').fetchone()[0]
            db.execute('CREATE TABLE build_info AS SELECT current_timestamp AS built_at, ?::BIGINT AS files, ?::BIGINT AS events, ?::BIGINT AS issues, ?::BOOLEAN AS index_complete, ?::BOOLEAN AS index_limited, ?::BIGINT AS index_failures',
                       [counts['files'], counts['events'], counts['issues'], index_info.get('complete'), index_info.get('limited'), len(index_info.get('failures', []))])
        os.chmod(temporary, 0o600)
        os.replace(temporary, database)
    return dict(counts)


def main():
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--index', type=Path, default=Path('.analytics/bodies/index.json'))
    parser.add_argument('--database', type=Path, default=Path('.analytics/events.duckdb'))
    parser.add_argument('--metadata', type=Path, default=Path('.analytics/sessions.duckdb'))
    args = parser.parse_args()
    try:
        counts = build(args.index, args.database, args.metadata)
    except Exception as exc:
        print(f'Normalization failed ({type(exc).__name__}); previous database unchanged.', file=sys.stderr)
        return 1
    print('Normalized: ' + ', '.join(f'{k}={v}' for k, v in counts.items()))
    return 0


if __name__ == '__main__':
    sys.exit(main())
