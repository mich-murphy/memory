#!/usr/bin/env python3
"""Private session-reader/v2 consumer; no storage discovery or reconstruction.

This independent projection is available to dashboard/query callers through explicit
--source publications mode; legacy defaults remain unchanged. Raw coordinates refer
to verified, locally owned bodies, not ephemeral reader exports. No classifier
or external service is used. NS columns are integer Unix nanoseconds throughout.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import subprocess
import sys
import tempfile
import time


class ImportFailure(ValueError):
    """A safe, content-free diagnostic code."""


def _dependencies():
    try:
        import duckdb
        import events
    except ImportError:
        raise ImportFailure('missing_dependency') from None
    return duckdb, events


def _require(condition, code='invalid_metadata'):
    if not condition:
        raise ImportFailure(code)


def _integer(value):
    return type(value) is int and 0 <= value <= 2**63 - 1


def _digest(value):
    return isinstance(value, str) and len(value) == 64 and all(c in '0123456789abcdef' for c in value)


def _source_hash(source_id):
    _require(isinstance(source_id, str) and bool(source_id))
    return hashlib.sha256(source_id.encode('utf-8')).hexdigest()


def _load(root):
    try:
        with (root / 'metadata.json').open('rb') as stream:
            raw = stream.read(16 * 1024 * 1024 + 1)
        _require(len(raw) <= 16 * 1024 * 1024)
        data = json.loads(raw)
        _require(isinstance(data, dict))
        _require(data.get('contract') == 'session-reader/v2', 'unsupported_contract')
        manifest = data['manifest']
        _require(manifest.get('contract') == 'session-publication/v2', 'unsupported_contract')
        identity = manifest['identity']
        for key in ('archive_id', 'machine_id', 'agent', 'session_id'):
            _require(isinstance(identity[key], str) and bool(identity[key]))
        _require(isinstance(manifest['publication_id'], str) and bool(manifest['publication_id']))
        for key in ('sequence', 'observed_at_ns', 'captured_at_ns', 'published_at_ns'):
            _require(_integer(manifest[key]))
        coverage = manifest['coverage']
        # Go's v2 wire model permits nil slices as JSON null. Normalize only
        # declared array fields, not missing fields or unsupported contracts.
        if coverage['missing'] is None:
            coverage['missing'] = []
        if manifest['sources'] is None:
            manifest['sources'] = []
        if data['bodies'] is None:
            data['bodies'] = []
        _require(isinstance(coverage['discovery'], str) and isinstance(coverage['enrichment'], str)
                 and isinstance(coverage['missing'], list))
        _require(isinstance(manifest['sources'], list) and isinstance(data['bodies'], list))
        pairs = {}
        selected = {}
        for source in manifest['sources']:
            for key in ('epoch_id', 'role', 'state'):
                _require(isinstance(source[key], str) and bool(source[key]))
            for key in ('revision', 'observed_at_ns', 'captured_at_ns'):
                _require(_integer(source[key]))
            if source['associations'] is None:
                source['associations'] = []
            _require(isinstance(source['associations'], list) and
                     all(isinstance(a, str) for a in source['associations']))
            optional = source.get('optional', False)
            _require(type(optional) is bool and (not optional or source['role'] == 'artifact'))
            withheld = source.get('withheld')
            if withheld is not None:
                _require(set(source) <= {'source_id', 'epoch_id', 'role', 'path', 'state',
                         'revision', 'observed_at_ns', 'captured_at_ns', 'associations',
                         'optional', 'withheld'}, 'withheld_metadata_redaction')
                _require(isinstance(withheld, dict) and
                         set(withheld) == {'source_id_hash', 'snapshot_commitment', 'reason'})
                _require(source['revision'] > 0)
                _require(optional and source['role'] == 'artifact' and
                         source['source_id'] == '' and source['path'] == '' and
                         not source['associations'] and 'object' not in source,
                         'withheld_metadata_redaction')
                _require(_digest(withheld['source_id_hash']) and
                         _digest(withheld['snapshot_commitment']) and
                         withheld['reason'] in ('policy', 'integrity', 'unavailable'))
                _require(coverage['enrichment'] == 'partial' and
                         'source-withheld' in coverage['missing'], 'withheld_coverage')
                source_hash = withheld['source_id_hash']
            else:
                for key in ('source_id', 'path'):
                    _require(isinstance(source[key], str) and bool(source[key]))
                obj = source['object']
                for key in ('sha256', 'encoded_sha256'):
                    _require(_digest(obj[key]))
                for key in ('size', 'encoded_size'):
                    _require(_integer(obj[key]))
                _require(isinstance(obj['key'], str) and isinstance(obj['encoding'], str))
                source_hash = _source_hash(source['source_id'])
            pair = (source_hash, source['epoch_id'])
            for key in ('source_id', 'epoch_id', 'role', 'path', 'state'):
                _require(isinstance(source[key], str) and '\x00' not in source[key])
                source[key].encode('utf-8')
            _require(pair not in pairs, 'duplicate_source')
            pairs[pair] = source
            if withheld is None:
                selected[pair] = source
        bodies = {}
        for body in data['bodies']:
            pair = (_source_hash(body['source_id']), body['epoch_id'])
            _require(pair not in bodies and pair in selected, 'body_coverage')
            _require(body['sha256'] == pairs[pair]['object']['sha256'] and
                     body['size'] == pairs[pair]['object']['size'], 'body_metadata_mismatch')
            path = PurePosixPath(body['path'])
            _require(not path.is_absolute() and bool(path.parts) and '..' not in path.parts
                     and '\\' not in str(path), 'unsafe_body_path')
            bodies[pair] = body
        _require(selected.keys() == bodies.keys(), 'body_coverage')
        return manifest, pairs, bodies
    except ImportFailure:
        raise
    except (OSError, ValueError, KeyError, TypeError, AttributeError, UnicodeError):
        raise ImportFailure('invalid_metadata') from None


def _schema(db, events):
    tables = {row[0] for row in db.execute('SHOW TABLES').fetchall()}
    if tables:
        _require('publication_binding' in tables, 'not_publication_database')
        _require(db.execute('SELECT version FROM publication_binding').fetchone() == (2,), 'unsupported_database')
        _require('sequence' in {row[1] for row in db.execute("PRAGMA table_info('failures')").fetchall()},
                 'unsupported_database')
        _require({'source_key_hash', 'optional', 'withheld_reason'} <=
                 {row[1] for row in db.execute("PRAGMA table_info('sources')").fetchall()},
                 'unsupported_database')
        return
    db.execute('CREATE TABLE publication_binding(version INTEGER, archive_id VARCHAR)')
    db.execute('INSERT INTO publication_binding VALUES (2, NULL)')
    db.execute('''CREATE TABLE sessions(session_key VARCHAR PRIMARY KEY, archive_id VARCHAR,
        machine_id VARCHAR, agent VARCHAR, session_id VARCHAR, publication_id VARCHAR,
        sequence BIGINT, manifest_sha256 VARCHAR, observed_at_ns BIGINT, captured_at_ns BIGINT,
        published_at_ns BIGINT, imported_at_ns BIGINT, normalized_at_ns BIGINT,
        coverage JSON, normalization VARCHAR)''')
    coords = {k: 'VARCHAR' for k in ('session_key', 'archive_id', 'machine_id', 'agent',
              'session_id', 'source_id', 'epoch_id', 'publication_id')}
    for table, columns in (('events', events.EVENT_COLUMNS), ('issues', events.ISSUE_COLUMNS)):
        spec = dict(columns, **coords)
        db.execute(f'CREATE TABLE {table} (' + ','.join(f'"{k}" {v}' for k, v in spec.items()) + ')')
    db.execute('''CREATE TABLE sources(session_key VARCHAR, trace_id VARCHAR, archive_id VARCHAR,
        machine_id VARCHAR, agent VARCHAR, session_id VARCHAR, publication_id VARCHAR,
        source_id VARCHAR, epoch_id VARCHAR, source_role VARCHAR, revision BIGINT,
        state VARCHAR, original_path VARCHAR, path VARCHAR, sha256 VARCHAR, size BIGINT,
        observed_at_ns BIGINT, captured_at_ns BIGINT, associations JSON, partial_tail BOOLEAN, source_key_hash VARCHAR,
        optional BOOLEAN, withheld_reason VARCHAR)''')
    db.execute('''CREATE TABLE failures(session_key VARCHAR PRIMARY KEY, archive_id VARCHAR,
        machine_id VARCHAR, agent VARCHAR, session_id VARCHAR, publication_id VARCHAR,
        sequence BIGINT, failed_at_ns BIGINT, reason VARCHAR, attempts BIGINT)''')


def _clear_failure(db, session, manifest):
    # Equal sequence only resolves the corresponding publication, not a conflict.
    db.execute('''DELETE FROM failures WHERE session_key=? AND
        (sequence < ? OR (sequence = ? AND publication_id = ?))''',
        [session, manifest['sequence'], manifest['sequence'], manifest['publication_id']])


def _insert(db, table, row):
    keys = ','.join('"' + k + '"' for k in row)
    db.execute(f'INSERT INTO {table} ({keys}) VALUES (' + ','.join('?' for _ in row) + ')', list(row.values()))


class _ProjectionBatch:
    """Typed, bounded event/issue staging inside the session's DuckDB transaction."""
    def __init__(self, db, table, columns, folder):
        self.db, self.table, self.columns = db, table, columns
        self.path = Path(folder) / (table + '.jsonl')
        self.rows = []
        self.size = 0

    def add(self, row):
        values = {key: row.get(key) for key in self.columns}
        # JSON columns already contain normalized JSON text. Stage as VARCHAR,
        # then cast to JSON in SQL: parsing again here changes duplicate keys
        # and numeric lexemes that parameterized inserts previously preserved.
        encoded = json.dumps(values, ensure_ascii=False, separators=(',', ':'))
        self.rows.append(encoded)
        self.size += len(encoded.encode('utf-8')) + 1
        if len(self.rows) >= 1000 or self.size >= 1024 * 1024:
            self.flush()

    def flush(self):
        if not self.rows:
            return
        # The parent is an owned private 0700 staging directory. No second index
        # or persistent data authority: SQL rows commit or roll back as one session.
        with self.path.open('w', encoding='utf-8') as stream:
            os.chmod(self.path, 0o600)
            stream.write('\n'.join(self.rows) + '\n')
        spec = '{' + ','.join(f"'{key}':'{'VARCHAR' if typ == 'JSON' else typ}'"
                              for key, typ in self.columns.items()) + '}'
        projection = ','.join(f'CAST("{key}" AS JSON) AS "{key}"' if typ == 'JSON' else f'"{key}"'
                              for key, typ in self.columns.items())
        self.db.execute(f'INSERT INTO {self.table} BY NAME SELECT {projection} FROM read_json('
                        "?, columns=" + spec + ", format='newline_delimited', maximum_object_size=" +
                        str(max(self.size, 1024 * 1024)) + ')', [str(self.path)])
        self.rows.clear()
        self.size = 0


def _key(values):
    return hashlib.sha256(json.dumps(list(values), separators=(',', ':')).encode()).hexdigest()


def _copy_verified(root, body, destination):
    # Private reader directory is a trusted handoff, but reject symlink escapes.
    relative = PurePosixPath(body['path'])
    path = root
    for part in relative.parts:
        path = path / part
        _require(not path.is_symlink(), 'unsafe_body_path')
    _require(path.is_file(), 'missing_body')
    digest, size = hashlib.sha256(), 0
    try:
        with path.open('rb') as source, destination.open('xb') as target:
            os.chmod(destination, 0o600)
            while chunk := source.read(1024 * 1024):
                size += len(chunk)
                _require(size <= body['size'], 'body_integrity')
                digest.update(chunk)
                target.write(chunk)
            target.flush()
            os.fsync(target.fileno())
        _require(size == body['size'] and digest.hexdigest() == body['sha256'], 'body_integrity')
    except OSError:
        raise ImportFailure('body_unavailable') from None


def import_export(export_dir, db_path, *, reset=False):
    """Atomically replace one session's projection; failures retain its old success.

    Explicit reset clears only this dedicated projection (not legacy databases).
    Unsupported metadata versions are rejected before any local write.
    """
    duckdb, events = _dependencies()
    root, path = Path(export_dir).resolve(), Path(db_path)
    manifest, sources, bodies = _load(root)
    identity = manifest['identity']
    archive = identity['archive_id']
    session = _key(identity[k] for k in ('archive_id', 'machine_id', 'agent', 'session_id'))
    coords = dict(session_key=session, **identity, publication_id=manifest['publication_id'])
    fingerprint = hashlib.sha256(json.dumps(manifest, sort_keys=True, separators=(',', ':')).encode()).hexdigest()
    if path.exists():
        # Refuse old projections before a writable open (including --reset).
        with duckdb.connect(str(path), read_only=True) as existing:
            _require(bool(existing.execute('SHOW TABLES').fetchall()), 'unsupported_database')
            _schema(existing, events)
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    # Reserve file privately before DuckDB opens it (DuckDB rejects an empty file).
    if not path.exists():
        with tempfile.NamedTemporaryFile(dir=path.parent) as reservation:
            private = reservation.name + '.duckdb'
            with duckdb.connect(private) as initial:
                _schema(initial, events)
            os.chmod(private, 0o600)
            try:
                os.link(private, path)
            finally:
                os.unlink(private)
    with duckdb.connect(str(path)) as db:
        _schema(db, events)
        bound = db.execute('SELECT archive_id FROM publication_binding').fetchone()[0]
        _require(bound in (None, archive) or reset, 'archive_mismatch')
        if reset:
            db.execute('BEGIN')
            for table in ('sessions', 'sources', 'events', 'issues', 'failures'):
                db.execute(f'DELETE FROM {table}')
            db.execute('UPDATE publication_binding SET archive_id=?', [archive])
            db.execute('COMMIT')
        old = db.execute('SELECT publication_id, sequence, manifest_sha256 FROM sessions WHERE session_key=?', [session]).fetchone()
        storage = path.with_name(path.name + '.bodies')
        storage.mkdir(mode=0o700, exist_ok=True)
        try:
            if old:
                _require(manifest['sequence'] >= old[1], 'stale_publication')
                _require(manifest['sequence'] != old[1] or
                         (manifest['publication_id'], fingerprint) == (old[0], old[2]), 'publication_conflict')
                _require(manifest['publication_id'] != old[0] or fingerprint == old[2], 'publication_conflict')
            with tempfile.TemporaryDirectory(prefix='.import-', dir=storage) as temp:
                staged = {}
                for n, (pair, body) in enumerate(bodies.items()):
                    target = Path(temp) / str(n)
                    _copy_verified(root, body, target)
                    staged[pair] = target
                if old and old[0] == manifest['publication_id']:
                    # Revalidate/repair owned raw coordinates too; a successful
                    # idempotent retry must not leave a corrupt local cache green.
                    for pair, target in staged.items():
                        os.replace(target, storage / bodies[pair]['sha256'])
                    _clear_failure(db, session, manifest)
                    return {'status': 'unchanged', 'publication_id': old[0]}
                db.execute('BEGIN')
                try:
                    for table in ('events', 'issues', 'sources', 'sessions'):
                        db.execute(f'DELETE FROM {table} WHERE session_key=?', [session])
                    counts = dict(sources=0, events=0, issues=0)
                    codex_traces = []
                    batch_columns = dict(**{k: 'VARCHAR' for k in coords}, source_id='VARCHAR', epoch_id='VARCHAR')
                    batches = {table: _ProjectionBatch(db, table, dict(columns, **batch_columns), temp)
                               for table, columns in (('events', events.EVENT_COLUMNS), ('issues', events.ISSUE_COLUMNS))}
                    for pair, source in sources.items():
                        trace = _key([session, *pair])
                        loc = dict(coords, source_id=source['source_id'], epoch_id=pair[1])
                        withheld = source.get('withheld')
                        if withheld is not None:
                            batches['issues'].add(dict(loc, trace_id=trace, line=0, offset=0,
                                length=0, kind='source_withheld_' + withheld['reason'], record_type=None))
                            _insert(db, 'sources', dict(loc, trace_id=trace, source_role=source['role'],
                                revision=source['revision'], state=source['state'],
                                original_path=None, path=None, sha256=None, size=None,
                                observed_at_ns=source['observed_at_ns'], captured_at_ns=source['captured_at_ns'],
                                associations='[]', partial_tail=False, source_key_hash=pair[0],
                                optional=True, withheld_reason=withheld['reason']))
                            counts['sources'] += 1
                            counts['issues'] += 1
                            continue
                        body = bodies[pair]
                        owned = storage / body['sha256']
                        # Always replace from verified staging, never trust an old cache.
                        os.replace(staged[pair], owned)
                        partial = False
                        with owned.open('rb') as stream:
                            if body['size'] and source['role'] != 'artifact':
                                stream.seek(-1, 2)
                                partial = stream.read(1) != b'\n'
                        # Opaque artifacts (SQLite backups, settings and snapshots)
                        # are verified/owned raw evidence, not JSONL conversations.
                        # Never allocate a potentially huge binary "line" or invent
                        # events/tails by running the transcript parser over them.
                        if source['role'] == 'artifact':
                            parsed = [(None, dict(trace_id=trace, line=0, offset=0,
                                length=body['size'], kind='artifact_not_normalized', record_type=None))]
                        else:
                            parsed = events.iter_events(owned, identity['agent'], trace)
                            if identity['agent'] == 'codex':
                                codex_traces.append(trace)
                        for event, issue in parsed:
                            if event:
                                if source['role'] == 'subagent' and event['role'] == 'user':
                                    event.update(is_human=False, origin='agent')
                                batches['events'].add(dict(event, **loc))
                                counts['events'] += 1
                            if issue:
                                # record_type can itself contain private malformed data.
                                issue['record_type'] = None
                                batches['issues'].add(dict(issue, **loc))
                                counts['issues'] += 1
                        if partial:
                            with owned.open('rb') as stream:
                                offset, number, length = 0, 0, 0
                                for number, raw in enumerate(stream, 1):
                                    offset += length
                                    length = len(raw)
                            batches['issues'].add(dict(loc, trace_id=trace, line=number,
                                    offset=offset, length=length, kind='incomplete_tail', record_type=None))
                            counts['issues'] += 1
                        _insert(db, 'sources', dict(loc, trace_id=trace, source_role=source['role'],
                            revision=source['revision'], state=source['state'], original_path=source['path'],
                            path=str(owned.resolve()), sha256=body['sha256'], size=body['size'],
                            observed_at_ns=source['observed_at_ns'], captured_at_ns=source['captured_at_ns'],
                            associations=json.dumps(source['associations']), partial_tail=partial,
                            source_key_hash=pair[0], optional=source.get('optional', False), withheld_reason=None))
                        counts['sources'] += 1
                    for batch in batches.values():
                        batch.flush()
                    events.pair_codex_mirrors(db, codex_traces)
                    now = time.time_ns()
                    _insert(db, 'sessions', dict(coords, sequence=manifest['sequence'], manifest_sha256=fingerprint,
                        observed_at_ns=manifest['observed_at_ns'], captured_at_ns=manifest['captured_at_ns'],
                        published_at_ns=manifest['published_at_ns'], imported_at_ns=now, normalized_at_ns=now,
                        coverage=json.dumps(manifest['coverage']), normalization='partial' if counts['issues'] else 'complete'))
                    db.execute('UPDATE publication_binding SET archive_id=?', [archive])
                    _clear_failure(db, session, manifest)
                    db.execute('COMMIT')
                    return dict(counts, status='imported', publication_id=manifest['publication_id'])
                except Exception:
                    db.execute('ROLLBACK')
                    raise
        except Exception as exc:
            reason = str(exc) if isinstance(exc, ImportFailure) else 'import_failed'
            # Replacing a failure must not expose a committed gap in its watermark.
            # Keep the previous failure if any replacement write is interrupted.
            db.execute('BEGIN')
            try:
                prior = db.execute('SELECT sequence, attempts FROM failures WHERE session_key=?', [session]).fetchone()
                if prior and prior[0] > manifest['sequence']:
                    # An older retry is not evidence that the newer target disappeared.
                    db.execute('UPDATE failures SET attempts=attempts+1 WHERE session_key=?', [session])
                else:
                    db.execute('DELETE FROM failures WHERE session_key=?', [session])
                    _insert(db, 'failures', dict(coords, sequence=manifest['sequence'],
                        failed_at_ns=time.time_ns(), reason=reason, attempts=(prior[1] if prior else 0) + 1))
                db.execute('UPDATE publication_binding SET archive_id=? WHERE archive_id IS NULL', [archive])
                db.execute('COMMIT')
            except BaseException:
                db.execute('ROLLBACK')
                raise
            raise ImportFailure(reason) from None


def report(db_path, *, now_ns=None, stale_after_ns=86400 * 10**9, summary=False):
    """Actual event-index report; summary bounds detail for interactive overview.

    The default remains exhaustive for offline writer/reader acceptance and exports.
    Summary totals are exact SQL aggregates; details are capped and ordered.
    """
    duckdb, _ = _dependencies()
    now = time.time_ns() if now_ns is None else now_ns
    with duckdb.connect(str(db_path), read_only=True) as db:
        def rows(sql):
            result = db.execute(sql)
            return [dict(zip([c[0] for c in result.description], row)) for row in result.fetchall()]
        _require('publication_binding' in {r[0] for r in db.execute('SHOW TABLES').fetchall()}, 'not_publication_database')
        _schema(db, None)
        detail_limit = ' LIMIT 50' if summary else ''
        sessions = rows('SELECT * EXCLUDE(manifest_sha256) FROM sessions ORDER BY session_key' + detail_limit)
        failures = rows('SELECT * FROM failures ORDER BY session_key' + detail_limit)
        if summary and sessions:
            keys = [session['session_key'] for session in sessions]
            failed = {row[0] for row in db.execute(
                'SELECT session_key FROM failures WHERE session_key IN (' +
                ','.join('?' for _ in keys) + ')', keys).fetchall()}
        elif summary:
            failed = set()
        else:
            failed = {failure['session_key'] for failure in failures}
        for session in sessions:
            session['coverage'] = json.loads(session['coverage'])
            session['capture_age_ns'] = max(0, now - session['captured_at_ns'])
            session['stale'] = session['capture_age_ns'] > stale_after_ns or session['session_key'] in failed
        return dict(contract='session-analytics-report/v2', served_at_ns=now,
            archive_id=db.execute('SELECT archive_id FROM publication_binding').fetchone()[0],
            sessions=sessions, failures=failures,
            **(dict(detail_limit=50,
                    totals=rows('SELECT (SELECT count(*) FROM sessions) AS sessions, '
                                '(SELECT count(*) FROM failures) AS failures, '
                                '(SELECT count(*) FROM sources) AS sources')[0],
                    agents=rows('SELECT agent,count(*) AS sessions FROM sessions GROUP BY agent ORDER BY agent'))
               if summary else {}),
            freshness=rows('''SELECT max(captured_at_ns) AS captured_at_ns,
                max(published_at_ns) AS published_at_ns, max(imported_at_ns) AS imported_at_ns,
                count(*) FILTER (WHERE captured_at_ns=0) AS unknown_capture_sessions,
                count(*) FILTER (WHERE published_at_ns=0) AS unknown_publication_sessions
                FROM sessions''')[0],
            source_counts=rows('''SELECT count(*) AS sources,
                count(*) FILTER (WHERE withheld_reason IS NULL) AS selected,
                count(*) FILTER (WHERE withheld_reason IS NOT NULL) AS withheld FROM sources''')[0],
            sources=rows('''SELECT session_key,trace_id,source_id,epoch_id,publication_id,
                source_role,revision,state,source_key_hash,optional,withheld_reason,sha256,size,observed_at_ns,captured_at_ns,
                associations,partial_tail FROM sources ORDER BY session_key,source_id,epoch_id''') if not summary else [],
            withheld_counts=rows('SELECT withheld_reason AS reason, count(*) AS sources FROM sources WHERE withheld_reason IS NOT NULL GROUP BY withheld_reason ORDER BY withheld_reason'),
            source_roles=rows('SELECT source_role, count(*) AS sources FROM sources GROUP BY source_role ORDER BY source_role'),
            event_counts=rows('SELECT event_type, count(*) AS events FROM events GROUP BY event_type ORDER BY event_type'),
            issue_counts=rows('SELECT kind, count(*) AS issues FROM issues GROUP BY kind ORDER BY kind'),
            dashboard_wired=False)


def export_session(binary, store, archive, machine, agent, session, output, *, config=None):
    """Invoke the sole reader; configuration carries references, never secret argv."""
    _require((store is None) != (config is None), 'reader_destination_required')
    destination = ['--store', str(store)] if config is None else ['--config', str(config)]
    command = [str(binary), 'capture-v1', 'export', *destination, '--archive', archive,
               '--machine', machine, '--agent', agent, '--session', session, '--output', str(output)]
    try:
        subprocess.run(command, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=300)
    except subprocess.TimeoutExpired:
        raise ImportFailure('reader_export_timeout') from None
    except (OSError, subprocess.CalledProcessError):
        raise ImportFailure('reader_export_failed') from None


def main():
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest='command', required=True)
    imp = sub.add_parser('import')
    imp.add_argument('export_dir', type=Path)
    imp.add_argument('--database', type=Path, required=True)
    imp.add_argument('--reset', action='store_true')
    rep = sub.add_parser('report')
    rep.add_argument('--database', type=Path, required=True)
    args = parser.parse_args()
    try:
        result = (import_export(args.export_dir, args.database, reset=args.reset)
                  if args.command == 'import' else report(args.database))
    except Exception as exc:
        print(str(exc) if isinstance(exc, ImportFailure) else 'consumer_failed', file=sys.stderr)
        return 1
    print(json.dumps(result, sort_keys=True))
    return 0


if __name__ == '__main__':
    sys.exit(main())
