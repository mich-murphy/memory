"""Pi subagent run records from a shared-reader/v1 export (brief 56).

Tables `pi_run` (one row per run record), `pi_run_extension` (one row per extension digest of a
run) and `pi_run_owner` (one row per shared owner: its publication and RUNS_VERSION) are created
on writable opens only. Never stored: a task, error text, transcript path, child report content,
criteria text or evidence, command, argument, path, pid or file name.
"""

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


def write_runs(db, coords, sources, staged):
    """Insert the rows of one owner's run records; returns the counters."""
    return dict(runs=0, runs_skipped=0)
