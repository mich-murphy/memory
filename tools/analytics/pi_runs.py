"""Pi subagent run records from a shared-reader/v1 export (brief 56).

Tables `pi_run` (one row per run record), `pi_run_extension` (one row per extension digest of a
run) and `pi_run_owner` (one row per shared owner: its publication and RUNS_VERSION) are created
on writable opens only. Never stored: a task, error text, transcript path, child report content,
criteria text or evidence, command, argument, path, pid or file name.
"""
import json
import re
from pathlib import Path

import setup_facts
import usage_facts

# Bump when the extraction changes; an owner imported under another version is re-imported.
# Version 2 (parent, brief 56 real-data run): extension digests of 16 to 64 hex characters.
RUNS_VERSION = 2

COORDINATES = ('owner_key', 'archive_id', 'machine_id', 'source_id')
RUN_COLUMNS = {
    'run_id': 'VARCHAR', 'timestamp_ms': 'BIGINT', 'agent_type': 'VARCHAR', 'model': 'VARCHAR',
    'requested_model': 'VARCHAR', 'exit_code': 'BIGINT', 'has_error': 'BOOLEAN',
    'acceptance_status': 'VARCHAR', 'evidence_status': 'VARCHAR', 'acceptance_level': 'VARCHAR',
    'acceptance_explicit': 'BOOLEAN', 'review_status': 'VARCHAR', 'criteria': 'BIGINT',
    'runtime_checks': 'BIGINT', 'input_tokens': 'BIGINT', 'output_tokens': 'BIGINT',
    'cache_read_tokens': 'BIGINT', 'cache_write_tokens': 'BIGINT', 'cost_usd': 'DOUBLE',
    'turns': 'BIGINT', 'extensions_configured': 'BIGINT', 'extensions_effective': 'BIGINT',
    'extensions_runtime': 'BIGINT', 'extensions_required': 'BIGINT',
    'extensions_omitted': 'BIGINT', 'ambient_extensions_disabled': 'BOOLEAN',
    'runner_type': 'VARCHAR', 'external_duration_ms': 'BIGINT', 'external_exit_code': 'BIGINT'}
EXTENSION_COLUMNS = {'owner_key': 'VARCHAR', 'run_id': 'VARCHAR', 'list': 'VARCHAR',
                     'digest': 'VARCHAR'}
OWNER_COLUMNS = {'owner_key': 'VARCHAR', 'archive_id': 'VARCHAR', 'machine_id': 'VARCHAR',
                 'agent': 'VARCHAR', 'publication_id': 'VARCHAR', 'sequence': 'BIGINT',
                 'manifest_sha256': 'VARCHAR', 'runs_version': 'INTEGER',
                 'captured_at_ns': 'BIGINT', 'published_at_ns': 'BIGINT',
                 'imported_at_ns': 'BIGINT'}
TABLES = ('pi_run', 'pi_run_extension', 'pi_run_owner')
REF_COLUMNS = {'trace_id': 'VARCHAR', 'line': 'BIGINT', 'timestamp': 'VARCHAR',
               'run_id': 'VARCHAR', 'via': 'VARCHAR'}
VIAS = ('tool_result', 'workflow_child', 'pending', 'supervisor_request', 'supervisor_reply',
        'control_notice')


def ensure(db):
    """Writable-open only: create the run tables; never called read-only."""
    spec = dict({k: 'VARCHAR' for k in setup_facts.COORDINATES}, **REF_COLUMNS)
    db.execute('CREATE TABLE IF NOT EXISTS pi_run_ref (' +
               ','.join(f'"{k}" {v}' for k, v in spec.items()) + ')')
    run = dict({k: 'VARCHAR' for k in COORDINATES}, **RUN_COLUMNS)
    for table, columns in (('pi_run', run), ('pi_run_extension', EXTENSION_COLUMNS),
                           ('pi_run_owner', OWNER_COLUMNS)):
        db.execute(f'CREATE TABLE IF NOT EXISTS {table} (' +
                   ','.join(f'"{k}" {v}' for k, v in columns.items()) + ')')


STATUS = re.compile(r'[a-z][a-z0-9_-]{0,39}')
MODEL = re.compile(r'(?!/|\.|~)[A-Za-z0-9._:@/-]{1,100}')
# Real run records carry a 16-hex-character digest prefix (`sha256:` + 16 lowercase hex, 136 of
# 136 on dev-01, parent 2026-11-24); a full 64-character digest is accepted too.
DIGEST = re.compile(r'sha256:[0-9a-f]{16,64}')
LISTS = ('configured', 'effective', 'runtime', 'required')
MAX_RECORD = 8 << 20


def create_view(db):
    """pi_run with its earliest referencing session; run usage is never added to sessions."""
    db.execute('''CREATE OR REPLACE VIEW pi_run_dim AS
        SELECT r.*, f.session_key, coalesce(f.referencing_sessions, 0) AS referencing_sessions
        FROM pi_run r LEFT JOIN (
            SELECT archive_id, machine_id, run_id,
                list(session_key ORDER BY timestamp NULLS LAST, session_key)[1] AS session_key,
                count(DISTINCT session_key) AS referencing_sessions
            FROM pi_run_ref GROUP BY archive_id, machine_id, run_id) f
        ON f.archive_id = r.archive_id AND f.machine_id = r.machine_id AND f.run_id = r.run_id''')


def _d(value):
    return value if isinstance(value, dict) else {}


class _Fields(usage_facts._Count):
    def match(self, value, pattern, extra=None):
        if value is None:
            return None
        if isinstance(value, str) and pattern.fullmatch(value) and (extra is None or extra(value)):
            return value
        self.skip()
        return None

    def status(self, value):
        return self.match(value, STATUS)

    def name(self, value):
        return self.match(value, setup_facts.USED_NAME)

    def model(self, value):
        return self.match(value, MODEL, lambda v: '//' not in v)

    def count(self, value):
        if value is None:
            return None
        if isinstance(value, list):
            return len(value)
        self.skip()
        return None


def _row(c, record, extensions):
    acceptance = _d(record.get('acceptance'))
    usage = _d(record.get('usage'))
    launch = _d(record.get('launchResolvedExtensions'))
    omitted = _d(launch.get('omitted'))
    runner = _d(record.get('runner'))
    external = _d(record.get('externalProcess'))
    error = record.get('error')
    if error is None:
        has_error = False
    elif isinstance(error, str):
        has_error = bool(error)
    else:
        c.skip()
        has_error = None
    lengths = {}
    for name in LISTS:
        items = launch.get(name)
        lengths[name] = c.count(items)
        for digest in items if isinstance(items, list) else []:
            if isinstance(digest, str) and DIGEST.fullmatch(digest):
                extensions.append((name, digest))
            else:
                c.skip()
    omitted_values = [c.integer(omitted.get(n)) for n in LISTS]
    known = [v for v in omitted_values if v is not None]
    return dict(
        timestamp_ms=c.integer(record.get('timestamp')), agent_type=c.name(record.get('agent')),
        model=c.model(record.get('model')), requested_model=c.model(record.get('requestedModel')),
        exit_code=c.integer(record.get('exitCode'), True), has_error=has_error,
        acceptance_status=c.status(acceptance.get('status')),
        evidence_status=c.status(acceptance.get('evidenceStatus')),
        acceptance_level=c.status(_d(acceptance.get('effectiveAcceptance')).get('level')),
        acceptance_explicit=c.flag(acceptance.get('explicit')),
        review_status=c.status(_d(acceptance.get('reviewResult')).get('status')),
        criteria=c.count(acceptance.get('criteria')),
        runtime_checks=c.count(record.get('runtimeChecks', acceptance.get('runtimeChecks'))),
        input_tokens=c.integer(usage.get('input')), output_tokens=c.integer(usage.get('output')),
        cache_read_tokens=c.integer(usage.get('cacheRead')),
        cache_write_tokens=c.integer(usage.get('cacheWrite')), cost_usd=c.number(usage.get('cost')),
        turns=c.integer(usage.get('turns')),
        extensions_configured=lengths['configured'], extensions_effective=lengths['effective'],
        extensions_runtime=lengths['runtime'], extensions_required=lengths['required'],
        extensions_omitted=sum(known) if known else None,
        ambient_extensions_disabled=c.flag(launch.get('disableAmbientExtensions')),
        runner_type=c.status(runner.get('type')),
        external_duration_ms=c.integer(external.get('durationMs')),
        external_exit_code=c.integer(external.get('exitCode'), True))


def write_runs(db, coords, sources, staged):
    """Insert one owner's rows (caller holds the transaction); returns the counters."""
    skipped, runs = {}, 0
    c = _Fields(skipped)
    for pair, source in sources.items():
        try:
            with staged[pair].open('rb') as stream:
                record = json.loads(stream.read(MAX_RECORD + 1)[:MAX_RECORD])
        except (ValueError, RecursionError):
            c.skip()
            continue
        if not isinstance(record, dict):
            c.skip()
            continue
        run_id = record.get('runId')
        if not (isinstance(run_id, str) and setup_facts.USED_NAME.fullmatch(run_id)):
            c.skip()
            continue
        extensions = []
        row = _row(c, record, extensions)
        row = dict(coords, source_id=source['source_id'], run_id=run_id, **row)
        keys = ','.join('"' + k + '"' for k in row)
        db.execute(f'INSERT INTO pi_run ({keys}) VALUES (' + ','.join('?' for _ in row) + ')',
                   list(row.values()))
        for name, digest in sorted(set(extensions)):
            db.execute('INSERT INTO pi_run_extension VALUES (?, ?, ?, ?)',
                       [coords['owner_key'], run_id, name, digest])
        runs += 1
    return dict(runs=runs, runs_skipped=skipped.get('skipped', 0))


def _ids(items):
    return [i.get('runId') for i in items if isinstance(i, dict)] if isinstance(items, list) else []


def _refs(r):
    """(via, raw run id) pairs from exactly the structured fields; never free text."""
    out = []
    custom = r.get('customType')
    if custom == 'subagent_supervisor_request':
        out.append(('supervisor_request', _d(r.get('details')).get('runId')))
    elif custom == 'subagent_supervisor_reply':
        out.append(('supervisor_reply', _d(r.get('data')).get('runId')))
    elif custom == 'subagent_control_notice':
        out.append(('control_notice', _d(_d(r.get('details')).get('event')).get('runId')))
    m = _d(r.get('message'))
    if r.get('type') == 'message' and m.get('role') == 'toolResult':
        d = _d(m.get('details'))
        out.append(('tool_result', d.get('runId')))
        out += [('workflow_child', i) for i in _ids(_d(d.get('mission')).get('workflowChildren'))]
        out += [('workflow_child', i) for i in _ids(_d(d.get('workflowChildren')).get('children'))]
        out += [('pending', i) for i in _ids(d.get('pending'))]
    return out


def iter_refs(path, trace, skipped=None):
    """Yield one row per first sighting of (run_id, via) in a Pi session source."""
    c = _Fields(skipped)
    seen = set()
    with Path(path).open('rb') as source:
        for number, raw in enumerate(source, 1):
            try:
                r = json.loads(raw)
                if not isinstance(r, dict):
                    continue
                found = _refs(r)
            except (ValueError, RecursionError):
                continue
            ts = r.get('timestamp')
            ts = ts if isinstance(ts, str) and len(ts) <= 200 else None
            try:
                ts and ts.encode('utf-8')
            except UnicodeError:
                ts = None
            for via, value in found:
                run_id = c.name(value)
                if run_id is None or (run_id, via) in seen:
                    continue
                seen.add((run_id, via))
                yield dict(trace_id=trace, line=number, timestamp=ts, run_id=run_id, via=via)
