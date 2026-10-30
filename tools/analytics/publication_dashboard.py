"""Read-only dashboard adapter for the separate version-2 publication projection.

No reader invocation, import, refresh, storage access, or classification happens here.
"""
import contextlib
import hashlib
import json
import os
from pathlib import Path
import stat
import sqlite3
import time

import duckdb
import publications
import projection_access
import publication_refresh


MAX_RAW = 1024 * 1024
MAX_BODY = 1024 * 1024 * 1024


def rows(db, sql, params=()):
    result = db.execute(sql, params)
    return [dict(zip([c[0] for c in result.description], row)) for row in result.fetchall()]


def safe_numbers(value):
    """Keep int64 nanoseconds/revisions exact in JavaScript, not rounded doubles."""
    if isinstance(value, dict):
        return {k: (str(v) if isinstance(v, int) and not isinstance(v, bool) and
                    (k.endswith('_ns') or k in ('revision', 'sequence', 'size')) else safe_numbers(v))
                for k, v in value.items()}
    if isinstance(value, list):
        return [safe_numbers(v) for v in value]
    return value


def number(query, key, default, maximum):
    value = int(query.get(key, [default])[0])
    if not 0 <= value <= maximum:
        raise ValueError('invalid pagination')
    return value


# Explicit projection: original_path and locally owned filesystem paths are not UI provenance.
SOURCE = '''SELECT s.* EXCLUDE(original_path,path), NULL::VARCHAR AS project,
    s.source_key_hash AS file_key, NULL::VARCHAR AS started_at,
    j.coverage,j.normalization,j.sequence,j.published_at_ns,j.imported_at_ns,j.normalized_at_ns
    FROM sources s JOIN sessions j ON s.session_key=j.session_key
    AND s.publication_id=j.publication_id AND s.archive_id=j.archive_id'''


class PublicationDashboard:
    def __init__(self, database, refresh_status=None, refresh_stale_seconds=300):
        self.database = Path(database)
        self.refresh_status = Path(refresh_status) if refresh_status else None
        self.refresh_stale_seconds = refresh_stale_seconds
        self.validate()

    @contextlib.contextmanager
    def connect(self):
        with projection_access.connect(self.database) as db:
            db.execute("SET TimeZone = 'UTC'")
            db.execute("SET threads = 2")
            db.execute("SET memory_limit = '256MB'")
            yield db

    def refresh_health(self):
        result = dict(contract='session-publication-refresh-health/v1', healthy=False,
                      mode='partial' if self.refresh_status is None else 'unavailable',
                      archive_coverage_claim=False)
        if self.refresh_status is None:
            return result
        try:
            descriptor = os.open(self.refresh_status, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
            with os.fdopen(descriptor, 'rb') as stream:
                info = os.fstat(stream.fileno())
                if not (stat.S_ISREG(info.st_mode) and info.st_uid == os.getuid() and info.st_mode & 0o077 == 0):
                    return result
                uri = self.refresh_status.resolve().as_uri() + '?mode=ro'
                with contextlib.closing(sqlite3.connect(uri, uri=True, timeout=1)) as status:
                    deadline = time.monotonic() + 1
                    status.set_progress_handler(lambda: int(time.monotonic() > deadline), 1000)
                    status.execute('PRAGMA query_only=ON')
                    status.execute('BEGIN')
                    if status.execute('SELECT version FROM binding').fetchall() != [(4,)]:
                        return result
                    binding = status.execute('SELECT configuration_id FROM binding').fetchall()
                    expected = status.execute('SELECT name,fingerprint FROM expected_endpoints WHERE active=1 ORDER BY name LIMIT 17').fetchall()
                    configuration = hashlib.sha256(json.dumps(dict(expected), sort_keys=True).encode()).hexdigest()
                    if not expected or len(expected)>16 or binding != [(configuration,)]:
                        return result
                    endpoints = status.execute('''SELECT x.name,e.attempted_ns,e.completed_ns,e.error
                        FROM expected_endpoints x LEFT JOIN endpoints e USING(name)
                        WHERE x.active=1 ORDER BY x.name''').fetchall()
                    omitted = status.execute('SELECT count(*) FROM expected_endpoints WHERE active=0').fetchone()[0]
                    unknown = status.execute('''SELECT count(*) FROM endpoints e WHERE NOT EXISTS
                        (SELECT 1 FROM expected_endpoints x WHERE x.name=e.name)''').fetchone()[0]
                    counts = publication_refresh.summary(status)
                    after = self.refresh_status.stat(follow_symlinks=False)
                    if (after.st_dev, after.st_ino) != (info.st_dev, info.st_ino):
                        return result
            now = time.time_ns()
            stale = not endpoints or any(row[2] is None or now-row[2] > self.refresh_stale_seconds*10**9 for row in endpoints)
            errors = sum(row[3] is not None for row in endpoints)
            completed = min((row[2] for row in endpoints if row[2] is not None), default=None)
            result.update(mode='configured', endpoint_errors=errors, expected_targets=counts['expected_targets'],
                          discovery_errors=counts['discovery_errors'],
                          unimported=counts['unimported'], target_errors=counts['target_errors'],
                          last_completed_sweep_ns=completed, stale=stale,
                          expected_endpoints=len(expected), omitted_endpoints=omitted,
                          incomplete_endpoints=sum(row[2] is None for row in endpoints),
                          endpoint_scope='configured endpoints only; omitted endpoints are not covered',
                          healthy=bool(endpoints) and len(endpoints)<=16 and not (unknown or stale or errors or counts['unimported'] or counts['target_errors'] or counts['discovery_errors']))
        except (OSError, sqlite3.Error, ValueError):
            pass
        return result

    def validate(self):
        with self.connect() as db:
            # The actual importer validates its current schema without creating tables.
            publications._require('publication_binding' in {r[0] for r in db.execute('SHOW TABLES').fetchall()},
                                  'not_publication_database')
            publications._schema(db, None)
            publications._require(len(db.execute('SELECT * FROM publication_binding').fetchall()) == 1,
                                  'unsupported_database')
            db.execute(SOURCE + ' LIMIT 0')
            _, events = publications._dependencies()
            for table, columns in (('events', events.EVENT_COLUMNS), ('issues', events.ISSUE_COLUMNS)):
                db.execute('SELECT ' + ','.join('"' + key + '"' for key in columns) +
                           ',session_key,archive_id,machine_id,agent,session_id,source_id,epoch_id,publication_id FROM ' + table + ' LIMIT 0')
            db.execute('SELECT session_key,sequence,failed_at_ns,reason,attempts FROM failures LIMIT 0')
            db.execute('SELECT observed_at_ns,captured_at_ns,normalized_at_ns FROM sessions LIMIT 0')

    def report(self):
        with projection_access.access(self.database):
            result = publications.report(self.database, summary=True)
        result['refresh_health'] = self.refresh_health()
        result.update(dashboard_wired=True, source='publications',
                      review_status='unavailable: legacy research is archive-separated',
                      classification_status='unavailable: not run for this projection')
        return safe_numbers(result)

    def sessions(self, query):
        clauses, params = [], []
        for key in ('agent', 'project'):
            value = query.get(key, [''])[0]
            if value:
                clauses.append('f.' + key + '=?')
                params.append(value)
        value = query.get('q', [''])[0]
        if value:
            clauses.append('(contains(lower(f.source_id),lower(?)) OR contains(lower(f.session_id),lower(?)))')
            params.extend([value, value])
        where = ' WHERE ' + ' AND '.join(clauses) if clauses else ''
        limit = max(1, number(query, 'limit', 50, 200))
        offset = number(query, 'offset', 0, 10000000)
        with self.connect() as db:
            total = db.execute('SELECT count(*) FROM (' + SOURCE + ') f' + where, params).fetchone()[0]
            items = rows(db, '''SELECT f.*,coalesce(e.events,0) events,coalesce(e.humans,0) humans,
                coalesce(e.tools,0) tools,coalesce(e.errors,0) errors FROM (''' + SOURCE + ''') f
                LEFT JOIN (SELECT trace_id,count(*) events,
                count(DISTINCT line) FILTER(WHERE is_human AND NOT duplicate) humans,
                count(*) FILTER(WHERE event_type='tool_call' AND NOT duplicate) tools,
                count(*) FILTER(WHERE is_error IS TRUE AND NOT duplicate) errors
                FROM events GROUP BY trace_id) e USING(trace_id)''' + where +
                ' ORDER BY f.captured_at_ns DESC,f.trace_id LIMIT ? OFFSET ?', params + [limit, offset])
        return safe_numbers(dict(items=items, total=total, offset=offset))

    def _file(self, db, query):
        trace = query.get('id', query.get('trace', ['']))[0]
        found = rows(db, 'SELECT * FROM (' + SOURCE + ') f WHERE trace_id=?', [trace])
        if len(found) != 1:
            raise KeyError('unknown trace')
        return found[0]

    def trace(self, query):
        with self.connect() as db:
            file = self._file(db, query)
            clauses, params = ['trace_id=?'], [file['trace_id']]
            value = query.get('q', [''])[0]
            if value:
                clauses.append('contains(lower(text),lower(?))')
                params.append(value)
            role = query.get('role', [''])[0]
            if role == 'human':
                clauses.append('is_human AND NOT duplicate')
            elif role == 'tool':
                clauses.append("event_type IN ('tool_call','tool_result')")
            elif role == 'assistant':
                clauses.append("role='assistant' AND event_type IN ('message','thinking')")
            elif role:
                clauses.append('role=?')
                params.append(role)
            where = ' WHERE ' + ' AND '.join(clauses)
            limit = max(1, number(query, 'limit', 60, 200))
            offset = number(query, 'offset', 0, 10000000)
            focus = number(query, 'line', 0, 100000000)
            if focus:
                before = db.execute('SELECT count(*) FROM events' + where + ' AND line<?', params + [focus]).fetchone()[0]
                offset = max(0, before - min(5, limit // 2))
            total = db.execute('SELECT count(*) FROM events' + where, params).fetchone()[0]
            items = rows(db, 'SELECT * FROM events' + where + ' ORDER BY line,ordinal LIMIT ? OFFSET ?', params + [limit, offset])
        return safe_numbers(dict(file=file, total=total, offset=offset, items=items, focus_line=focus))

    def issues(self, query):
        limit = max(1, number(query, 'limit', 100, 500))
        offset = number(query, 'offset', 0, 10000000)
        with self.connect() as db:
            where, params = '', []
            if query.get('trace', [''])[0]:
                where, params = ' WHERE i.trace_id=?', [query['trace'][0]]
            base = ' FROM issues i JOIN (' + SOURCE + ') f USING(trace_id)' + where
            total = db.execute('SELECT count(*)' + base, params).fetchone()[0]
            items = rows(db, 'SELECT i.*,f.source_key_hash,f.source_role,f.revision,f.withheld_reason' + base +
                         ' ORDER BY i.kind,i.trace_id,i.line LIMIT ? OFFSET ?', params + [limit, offset])
        return safe_numbers(dict(items=items, total=total, offset=offset))

    def event(self, query):
        with self.connect() as db:
            file = self._file(db, query)
            # Never use retained old bodies for a now-withheld coordinate or parse opaque artifacts.
            if file['withheld_reason'] or file['source_role'] == 'artifact':
                raise ValueError('raw unavailable')
            line = number(query, 'line', 1, 100000000)
            positions = rows(db, '''SELECT byte_offset AS start,byte_length AS size FROM events
                WHERE trace_id=? AND line=? LIMIT 1''', [file['trace_id'], line])
            if not positions:
                positions = rows(db, '''SELECT "offset" AS start,length AS size FROM issues
                    WHERE trace_id=? AND line=? LIMIT 1''', [file['trace_id'], line])
            if not positions:
                raise KeyError('unknown line')
            position = positions[0]
            owned = db.execute('SELECT path FROM sources WHERE trace_id=? AND withheld_reason IS NULL', [file['trace_id']]).fetchone()[0]
        storage = self.database.with_name(self.database.name + '.bodies')
        if not publications._digest(file['sha256']) or storage.is_symlink():
            raise OSError('unowned body')
        expected = storage.resolve() / file['sha256']
        if not owned or Path(owned) != expected or expected.is_symlink():
            raise OSError('unowned body')
        start, size = position['start'], position['size']
        if not (0 <= start <= file['size'] <= MAX_BODY and 0 < size <= MAX_RAW and start + size <= file['size']):
            raise ValueError('raw resource limit')
        # Verify the selected owned file on every raw request, on the same descriptor used for slicing.
        flags = os.O_RDONLY | getattr(os, 'O_NOFOLLOW', 0)
        with os.fdopen(os.open(expected, flags), 'rb') as stream:
            info = os.fstat(stream.fileno())
            if not stat.S_ISREG(info.st_mode) or info.st_size != file['size'] or info.st_uid != os.getuid():
                raise OSError('body integrity')
            digest, count = hashlib.sha256(), 0
            while chunk := stream.read(min(1024 * 1024, file['size'] - count + 1)):
                count += len(chunk)
                if count > file['size']:
                    raise OSError('body integrity')
                digest.update(chunk)
            if count != file['size'] or digest.hexdigest() != file['sha256']:
                raise OSError('body integrity')
            stream.seek(start)
            data = stream.read(size)
            after = os.fstat(stream.fileno())
            if len(data) != size or (after.st_size, after.st_mtime_ns, after.st_ctime_ns) != (info.st_size, info.st_mtime_ns, info.st_ctime_ns):
                raise OSError('body changed')
        try:
            raw, valid = json.loads(data), True
        except (ValueError, UnicodeError):
            raw, valid = data.decode('utf-8', errors='replace'), False
        return dict(line=line, raw=raw, raw_text=data.decode('utf-8', errors='replace'), valid_json=valid)
