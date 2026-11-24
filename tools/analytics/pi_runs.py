"""Pi subagent run records from a shared-reader/v1 export (brief 56).

Tables `pi_run` (one row per run record), `pi_run_extension` (one row per extension digest of a
run) and `pi_run_owner` (one row per shared owner: its publication and RUNS_VERSION) are created
on writable opens only. Never stored: a task, error text, transcript path, child report content,
criteria text or evidence, command, argument, path, pid or file name.
"""
import json
import re

import setup_facts
import usage_facts

# Bump when the extraction changes; an owner imported under another version is re-imported.
RUNS_VERSION = 1

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


def ensure(db):
    """Writable-open only: create the run tables; never called read-only."""
    run = dict({k: 'VARCHAR' for k in COORDINATES}, **RUN_COLUMNS)
    for table, columns in (('pi_run', run), ('pi_run_extension', EXTENSION_COLUMNS),
                           ('pi_run_owner', OWNER_COLUMNS)):
        db.execute(f'CREATE TABLE IF NOT EXISTS {table} (' +
                   ','.join(f'"{k}" {v}' for k, v in columns.items()) + ')')


STATUS = re.compile(r'[a-z][a-z0-9_-]{0,39}')
MODEL = re.compile(r'(?!/|\.|~)[A-Za-z0-9._:@/-]{1,100}')
DIGEST = re.compile(r'sha256:[0-9a-f]{64}')
LISTS = ('configured', 'effective', 'runtime', 'required')
MAX_RECORD = 8 << 20


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
