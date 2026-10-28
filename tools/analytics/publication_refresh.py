#!/usr/bin/env python3
"""Explicit endpoint sweeps -> authoritative reader -> owned analytics projection.

No archive resolution, credentials, original-source reads or persistent lexical
watermark live here. Status is independent of the replaceable DuckDB projection.
"""
import argparse
import contextlib
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import shlex
import signal
import stat
import sqlite3
import subprocess
import tempfile
import time

import publications
import projection_access


class RefreshFailure(ValueError):
    pass


def require(ok, code):
    if not ok:
        raise RefreshFailure(code)


def load_endpoints(path):
    descriptor = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(descriptor, 'rb') as stream:
        info = os.fstat(stream.fileno())
        require(stat.S_ISREG(info.st_mode) and info.st_uid == os.getuid() and
                info.st_mode & 0o077 == 0, 'endpoint_config_not_private')
        raw = stream.read(65537)
        require(len(raw) <= 65536, 'endpoint_config_too_large')
        data = json.loads(raw)
    require(data.get('contract') == 'session-refresh-endpoints/v1', 'endpoint_contract')
    endpoints = data.get('endpoints')
    require(isinstance(endpoints, list) and 1 <= len(endpoints) <= 16, 'endpoint_count')
    names = set()
    for endpoint in endpoints:
        require(set(endpoint) <= {'name', 'binary', 'config', 'ssh_host', 'discovery'}, 'endpoint_fields')
        require(endpoint.get('discovery', 'queue') in ('queue', 'remote-current'),
                'endpoint_discovery')
        for field in ('name', 'binary', 'config'):
            value = endpoint.get(field)
            require(isinstance(value, str) and 0 < len(value) <= 4096 and '\x00' not in value,
                    'endpoint_field')
        require(re.fullmatch(r'[A-Za-z0-9_-]{1,64}', endpoint['name']) and
                endpoint['name'] not in names, 'endpoint_name')
        names.add(endpoint['name'])
        require(endpoint['binary'].startswith('/') and endpoint['config'].startswith('/'),
                'endpoint_absolute_path')
        if 'ssh_host' in endpoint:
            require(isinstance(endpoint['ssh_host'], str) and
                    re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9_.@-]{0,252}', endpoint['ssh_host']),
                    'endpoint_host')
    return endpoints


def disk_bytes(root, cap):
    total = 0
    entries = 0
    for directory, dirs, files in os.walk(root, followlinks=False):
        entries += len(dirs) + len(files)
        require(entries <= 65536, 'export_entry_limit')
        for name in dirs + files:
            path = Path(directory) / name
            try:
                info = path.lstat()
            except FileNotFoundError:
                # The active reader atomically renames its temporary metadata.
                # An enumerated name may therefore disappear before this sample.
                # Completed exports are sampled again before import validation.
                continue
            require(not stat.S_ISLNK(info.st_mode), 'export_symlink')
            total += info.st_size
            require(total <= cap, 'export_byte_limit')
    return total


def run(command, root, *, timeout=300, max_bytes=256 * 1024 * 1024, output_limit=2 * 1024 * 1024):
    """One bounded process group; stderr is discarded, never exposed as diagnostics."""
    with tempfile.TemporaryFile() as output:
        try:
            process = subprocess.Popen(command, stdout=output, stderr=subprocess.DEVNULL,
                                       start_new_session=True)
        except OSError:
            raise RefreshFailure('process_start_failed') from None
        deadline = time.monotonic() + timeout
        try:
            while True:
                require(time.monotonic() < deadline, 'process_timeout')
                require(os.fstat(output.fileno()).st_size <= output_limit, 'process_output_limit')
                disk_bytes(root, max_bytes)
                if process.poll() is not None:
                    break
                time.sleep(.025)
            require(process.returncode == 0, 'process_failed')
            output.seek(0)
            return output.read(output_limit + 1)
        finally:
            # Also fence descendants if a parent exited early.
            with contextlib.suppress(ProcessLookupError, PermissionError):
                os.killpg(process.pid, signal.SIGKILL)
            if process.poll() is None:
                process.kill()
            process.wait()


def command(endpoint, args):
    args = [endpoint['binary'], 'capture-v1', *args]
    if 'ssh_host' not in endpoint:
        return args
    return ['ssh', '-oBatchMode=yes', '-oConnectTimeout=10', '--', endpoint['ssh_host'],
            shlex.join(args)]


def export(endpoint, identity, output, root, timeout, max_bytes):
    """Run the authoritative reader's export, locally or over ssh, under one bound."""
    def reader_args(destination):
        args = ['export', '--config', endpoint['config']]
        for flag, key in (('archive', 'archive_id'), ('machine', 'machine_id'),
                          ('agent', 'agent'), ('session', 'session_id')):
            args += ['--' + flag, identity[key]]
        return args + ['--output', str(destination), '--max-bytes', str(max_bytes)]

    if 'ssh_host' not in endpoint:
        run(command(endpoint, reader_args(output)), root, timeout=timeout, max_bytes=max_bytes)
        return
    host = endpoint['ssh_host']
    ssh = ['ssh', '-oBatchMode=yes', '-oConnectTimeout=10', '--', host]
    remote = run([*ssh, 'umask 077; mktemp -d /tmp/session-refresh.XXXXXXXXXXXX'], root,
                 timeout=timeout).decode().strip()
    require(re.fullmatch(r'/tmp/session-refresh\.[A-Za-z0-9]{12}', remote), 'remote_temp_invalid')
    try:
        run(command(endpoint, reader_args(remote + '/export')), root, timeout=timeout, max_bytes=max_bytes)
        run(['scp', '-q', '-r', '-oBatchMode=yes', '-oConnectTimeout=10', '--',
             host + ':' + remote + '/export', str(output)], root,
            timeout=timeout, max_bytes=max_bytes)
    finally:
        # Only the exact mktemp-owned directory, never config/source paths.
        run([*ssh, shlex.join(['rm', '-rf', '--', remote])], root, timeout=timeout)


def open_status(path):
    path = Path(path)
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    db = sqlite3.connect(path)
    os.chmod(path, 0o600)
    db.execute('PRAGMA journal_mode=WAL')
    db.execute('PRAGMA synchronous=FULL')
    db.execute('CREATE TABLE IF NOT EXISTS binding(version INTEGER, configuration_id TEXT)')
    if db.execute('SELECT version FROM binding').fetchone() is None:
        db.execute('INSERT INTO binding VALUES (4,NULL)')
    if db.execute('SELECT version FROM binding').fetchall() not in ([(3,)], [(4,)]):
        db.close()
        raise RefreshFailure('status_contract')
    db.execute('''CREATE TABLE IF NOT EXISTS expected_endpoints(name TEXT PRIMARY KEY,
        fingerprint TEXT NOT NULL, active INTEGER NOT NULL CHECK(active IN (0,1)))''')
    db.execute('''CREATE TABLE IF NOT EXISTS endpoints(name TEXT PRIMARY KEY,
        attempted_ns INTEGER, completed_ns INTEGER, error TEXT)''')
    db.execute('''CREATE TABLE IF NOT EXISTS targets(endpoint TEXT, identity TEXT,
        sequence INTEGER, publication_id TEXT, imported_sequence INTEGER,
        imported_publication TEXT, imported_ns INTEGER, error TEXT,
        fingerprint TEXT NOT NULL DEFAULT '', imported_fingerprint TEXT NOT NULL DEFAULT '',
        PRIMARY KEY(endpoint,identity))''')
    db.execute('''CREATE TABLE IF NOT EXISTS head_failures(endpoint TEXT, identity TEXT,
        reason TEXT NOT NULL, attempts INTEGER NOT NULL, first_observed_ns INTEGER NOT NULL,
        last_observed_ns INTEGER NOT NULL, fingerprint TEXT NOT NULL DEFAULT '', PRIMARY KEY(endpoint,identity))''')
    # v3 lacked target ownership. Bind retained expectations to the endpoint
    # fingerprint recorded by v3; imported successes need rediscovery to qualify.
    if db.execute('SELECT version FROM binding').fetchone() == (3,):
        # sqlite3's context manager does not start a transaction for DDL.
        # Begin explicitly so interruption cannot leave v3 with half-added columns.
        db.execute('BEGIN IMMEDIATE')
        try:
            db.execute("ALTER TABLE targets ADD COLUMN fingerprint TEXT NOT NULL DEFAULT ''")
            db.execute("ALTER TABLE targets ADD COLUMN imported_fingerprint TEXT NOT NULL DEFAULT ''")
            db.execute("ALTER TABLE head_failures ADD COLUMN fingerprint TEXT NOT NULL DEFAULT ''")
            for table in ('targets', 'head_failures'):
                require(not db.execute('''SELECT 1 FROM ''' + table + ''' t WHERE NOT EXISTS
                    (SELECT 1 FROM expected_endpoints x WHERE x.name=t.endpoint
                        AND length(x.fingerprint)=64) LIMIT 1''').fetchone(), 'status_unbound_target')
                db.execute('''UPDATE ''' + table + ''' SET fingerprint=(SELECT x.fingerprint
                    FROM expected_endpoints x WHERE x.name=''' + table + '''.endpoint)''')
            db.execute('UPDATE endpoints SET completed_ns=NULL')
            db.execute('UPDATE binding SET version=4')
            db.commit()
        except BaseException:
            db.rollback()
            db.close()
            raise
    db.commit()
    return db


def bind_endpoints(status, endpoints):
    '''Commit the entire configured scope before any endpoint work can start.

    This is a configuration binding, not a global all-session snapshot. Existing
    completion is reusable only for the exact same private endpoint definition.
    Inactive history remains explicit; it cannot stand in for configured coverage.
    '''
    expected = {endpoint['name']: hashlib.sha256(json.dumps(endpoint, sort_keys=True,
        separators=(',', ':')).encode()).hexdigest() for endpoint in endpoints}
    require(1 <= len(expected) == len(endpoints) <= 16, 'endpoint_count')
    configuration = hashlib.sha256(json.dumps(expected, sort_keys=True).encode()).hexdigest()
    with status:
        changed = status.execute('SELECT configuration_id FROM binding').fetchone()[0] != configuration
        old = dict(status.execute('SELECT name,fingerprint FROM expected_endpoints WHERE active=1'))
        status.execute('UPDATE expected_endpoints SET active=0')
        for name, fingerprint in expected.items():
            status.execute('''INSERT INTO expected_endpoints VALUES (?,?,1)
                ON CONFLICT(name) DO UPDATE SET fingerprint=excluded.fingerprint,active=1''', (name, fingerprint))
            status.execute('INSERT OR IGNORE INTO endpoints VALUES (?,NULL,NULL,NULL)', (name,))
            if changed or old.get(name) != fingerprint:
                status.execute('UPDATE endpoints SET attempted_ns=NULL,completed_ns=NULL,error=NULL WHERE name=?', (name,))
        status.execute('UPDATE binding SET configuration_id=?', (configuration,))


def summary(db):
    """Active-binding target scope shared by CLI and dashboard."""
    targets = db.execute('''SELECT count(*),
        sum(t.imported_fingerprint!=t.fingerprint OR t.imported_sequence IS NULL
            OR t.imported_sequence<t.sequence),sum(t.error IS NOT NULL)
        FROM targets t JOIN expected_endpoints x ON x.name=t.endpoint
            AND x.active=1 AND x.fingerprint=t.fingerprint''').fetchone()
    discovery_errors = db.execute('''SELECT count(*) FROM head_failures f
        JOIN expected_endpoints x ON x.name=f.endpoint AND x.fingerprint=f.fingerprint WHERE x.active=1''').fetchone()[0]
    expected_targets = db.execute('''SELECT count(*) FROM (
        SELECT t.endpoint,t.identity FROM targets t JOIN expected_endpoints x
            ON x.name=t.endpoint AND x.active=1 AND x.fingerprint=t.fingerprint
        UNION SELECT f.endpoint,f.identity FROM head_failures f
            JOIN expected_endpoints x ON x.name=f.endpoint AND x.fingerprint=f.fingerprint WHERE x.active=1)''').fetchone()[0]
    return dict(expected_targets=expected_targets, unimported=targets[1] or 0,
                target_errors=targets[2] or 0, discovery_errors=discovery_errors)


def report(db):
    endpoints = db.execute('''SELECT count(*),sum(error IS NOT NULL),sum(completed_ns IS NULL)
        FROM expected_endpoints x LEFT JOIN endpoints e USING(name) WHERE active=1''').fetchone()
    return {'contract': 'session-publication-refresh-status/v1',
            'endpoints': endpoints[0], 'endpoint_errors': endpoints[1] or 0,
            'incomplete_endpoints': endpoints[2] or 0,
            'omitted_endpoints': db.execute('SELECT count(*) FROM expected_endpoints WHERE active=0').fetchone()[0],
            **summary(db),
            'endpoint_health': [dict(zip(('name', 'attempted_ns', 'completed_ns', 'error'), row))
                for row in db.execute('''SELECT name,attempted_ns,completed_ns,error FROM expected_endpoints x
                    LEFT JOIN endpoints e USING(name) WHERE active=1 ORDER BY name''')]}


def sweep(endpoints, database, status, *, timeout=300, max_bytes=256 * 1024 * 1024,
          max_pages=1024):
    bind_endpoints(status, endpoints)
    for endpoint in endpoints:
        name = endpoint['name']
        status.execute('''INSERT INTO endpoints VALUES (?,?,NULL,NULL)
            ON CONFLICT(name) DO UPDATE SET attempted_ns=excluded.attempted_ns''', (name, time.time_ns()))
        status.commit()
        try:
            cursor = ''  # Full bounded sweeps, including after restart/outage.
            archive = None
            seen = set()
            cursors = {''}
            with tempfile.TemporaryDirectory(prefix='session-refresh-') as temp:
                root = Path(temp)
                for _ in range(max_pages):
                    discovery_command = ('current-heads' if endpoint.get('discovery', 'queue') == 'remote-current'
                                         else 'publication-status')
                    raw = run(command(endpoint, [discovery_command, '--config', endpoint['config'],
                        '--after-session', cursor, '--limit', '256']), root, timeout=timeout)
                    try:
                        page = json.loads(raw)
                        require(page['contract'] == 'session-publication-heads/v1', 'heads_contract')
                        require(isinstance(page['archive_id'], str) and bool(page['archive_id']), 'heads_archive')
                        require(archive in (None, page['archive_id']), 'heads_archive')
                        archive = page['archive_id']
                        require(type(page['complete']) is bool and isinstance(page['next_cursor'], str), 'heads_page')
                        heads, failures = _validate_heads(page, archive, seen)
                        # Persist failed coordinates before importing healthy siblings.
                        # A new sweep/empty page cannot erase a known unreadable current.
                        with status:
                            fingerprint = status.execute('SELECT fingerprint FROM expected_endpoints WHERE name=?', (name,)).fetchone()[0]
                            for failure, key in failures:
                                now = time.time_ns()
                                status.execute("""INSERT INTO head_failures VALUES (?,?,?,1,?,?,?)
                                    ON CONFLICT(endpoint,identity) DO UPDATE SET reason=excluded.reason,
                                    attempts=head_failures.attempts+1,last_observed_ns=excluded.last_observed_ns,
                                    fingerprint=excluded.fingerprint""",
                                    (name, key, failure['reason'], now, now, fingerprint))
                        for head, key in heads:
                            _target(status, endpoint, head, key, database, root, timeout, max_bytes)
                    except (KeyError, TypeError, ValueError) as exc:
                        if isinstance(exc, RefreshFailure):
                            raise
                        raise RefreshFailure('invalid_heads') from None
                    if page['complete']:
                        break
                    require(page['next_cursor'] not in cursors, 'heads_cursor')
                    cursor = page['next_cursor']
                    cursors.add(cursor)
                else:
                    raise RefreshFailure('sweep_page_limit')
            if endpoint.get('discovery', 'queue') == 'remote-current':
                # Only completed current-root enumeration proves absence. Unreadable
                # heads are in seen; partial/queue sweeps never infer disappearance.
                fingerprint = status.execute('SELECT fingerprint FROM expected_endpoints WHERE name=?', (name,)).fetchone()[0]
                for (key,) in status.execute('SELECT identity FROM targets WHERE endpoint=? AND fingerprint=?',
                                             (name, fingerprint)):
                    if key not in seen:
                        status.execute('UPDATE targets SET error=? WHERE endpoint=? AND identity=?',
                                       ('current_missing', name, key))
            status.execute('UPDATE endpoints SET completed_ns=?,error=NULL WHERE name=?', (time.time_ns(), name))
        except Exception as exc:
            code = str(exc) if isinstance(exc, RefreshFailure) else 'enumeration_failed'
            status.execute('UPDATE endpoints SET error=? WHERE name=?', (code, name))
        status.commit()
    return report(status)


def _validate_heads(page, archive, seen):
    """Validate the whole bounded page before committing any of its coordinates."""
    heads, failures = page['heads'], page.get('failures', [])
    require(isinstance(heads, list) and isinstance(failures, list) and
            len(heads) + len(failures) <= 256, 'heads_count')
    validated = ([], [])
    for index, items in enumerate((heads, failures)):
        for item in items:
            identity = item['identity']
            require(isinstance(identity, dict) and
                    set(identity) == {'archive_id', 'machine_id', 'agent', 'session_id'} and
                    all(isinstance(v, str) and 0 < len(v) <= 4096 and '\x00' not in v
                        for v in identity.values()) and identity['archive_id'] == archive and
                    identity['agent'] in ('claude', 'codex', 'pi'), 'head_identity')
            if index == 0:
                require(type(item['sequence']) is int and 0 < item['sequence'] < 2**63 and
                        isinstance(item['publication_id'], str) and bool(item['publication_id']), 'head_coordinate')
            else:
                require(set(item) == {'identity', 'reason'} and
                        item['reason'] == 'current-unreadable', 'head_failure')
            key = json.dumps(identity, sort_keys=True)
            require(key not in seen, 'duplicate_head')
            seen.add(key)
            validated[index].append((item, key))
    return validated


def _imported_time(database, identity):
    duckdb, _ = publications._dependencies()
    with projection_access.connect(database) as projection:
        return projection.execute('SELECT imported_at_ns FROM sessions WHERE session_key=?',
            [publications._key(identity[k] for k in
             ('archive_id', 'machine_id', 'agent', 'session_id'))]).fetchone()[0]


def _projection_coordinate(database, head, prior):
    if prior is None or prior[0] is None or not Path(database).is_file():
        return None
    try:
        with projection_access.access(database):
            return _projection_coordinate_locked(database, head, prior)
    except projection_access.ProjectionBusy:
        return None


def _projection_coordinate_locked(database, head, prior):
    """Validate only the owned projection, never storage/current/source paths.

    A status success alone is not proof that a replaceable projection still exists.
    Stream hashes without copying selected bodies or advancing any freshness clock.
    """
    if prior is None or prior[0] is None or not Path(database).is_file():
        return None
    try:
        duckdb, _ = publications._dependencies()
        identity = head['identity']
        key = publications._key(identity[k] for k in ('archive_id', 'machine_id', 'agent', 'session_id'))
        with projection_access.connect(database) as db:
            publications._schema(db, None)
            row = db.execute('SELECT sequence,publication_id,imported_at_ns FROM sessions WHERE session_key=?', [key]).fetchone()
            if row is None or row[0] < head['sequence'] or row[0] < prior[0]:
                return None
            if row[0] == head['sequence'] and row[1] != head['publication_id']:
                return None
            if row[0] == prior[0] and row[1] != prior[1]:
                return None
            if db.execute('SELECT count(*) FROM failures WHERE session_key=?', [key]).fetchone()[0]:
                return None
            bodies = db.execute('SELECT path,size,sha256 FROM sources WHERE session_key=? AND withheld_reason IS NULL', [key]).fetchall()
        owned = Path(database).absolute().with_name(Path(database).name + '.bodies')
        for path, size, digest in bodies:
            path = Path(path)
            relative = path.relative_to(owned)
            descriptor = os.open(owned, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
            try:
                for component in relative.parts[:-1]:
                    child = os.open(component, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=descriptor)
                    os.close(descriptor)
                    descriptor = child
                body = os.open(relative.name, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=descriptor)
            finally:
                os.close(descriptor)
            with os.fdopen(body, 'rb') as stream:
                info = os.fstat(stream.fileno())
                if not (stat.S_ISREG(info.st_mode) and info.st_uid == os.getuid() and
                        info.st_mode & 0o077 == 0 and info.st_size == size):
                    return None
                actual = hashlib.sha256()
                remaining = size
                while remaining:
                    chunk = stream.read(min(1024 * 1024, remaining))
                    if not chunk:
                        return None
                    remaining -= len(chunk)
                    actual.update(chunk)
                if stream.read(1) or actual.hexdigest() != digest:
                    return None
        return row
    except Exception:
        return None


def _target(status, endpoint, head, key, database, root, timeout, max_bytes):
    name = endpoint['name']
    fingerprint = status.execute('SELECT fingerprint FROM expected_endpoints WHERE name=?', (name,)).fetchone()[0]
    old = status.execute('SELECT sequence,publication_id,fingerprint FROM targets WHERE endpoint=? AND identity=?',
                         (name, key)).fetchone()
    if old and old[2] == fingerprint and (head['sequence'] < old[0] or
                (head['sequence'] == old[0] and head['publication_id'] != old[1])):
        status.execute('UPDATE targets SET error=? WHERE endpoint=? AND identity=?', ('head_regression', name, key))
        status.commit()
        return
    status.execute('''INSERT INTO targets(endpoint,identity,sequence,publication_id,fingerprint)
        VALUES (?,?,?,?,?) ON CONFLICT(endpoint,identity) DO UPDATE SET
        sequence=excluded.sequence,publication_id=excluded.publication_id,
        fingerprint=excluded.fingerprint,error=NULL''',
                   (name, key, head['sequence'], head['publication_id'], fingerprint))
    status.execute('DELETE FROM head_failures WHERE endpoint=? AND identity=?', (name, key))
    status.commit()
    # Target ownership changes before export; only an import validated under this
    # binding can be reused, including after a failed attempt and process restart.
    prior = status.execute('''SELECT imported_sequence,imported_publication FROM targets
        WHERE endpoint=? AND identity=? AND imported_fingerprint=?''',
                           (name, key, fingerprint)).fetchone()
    healthy = _projection_coordinate(database, head, prior)
    if healthy is not None:
        status.execute('UPDATE targets SET imported_sequence=?,imported_publication=?,imported_ns=?,imported_fingerprint=?,error=NULL WHERE endpoint=? AND identity=?',
                       (*healthy, fingerprint, name, key))
        status.commit()
        return
    try:
        with tempfile.TemporaryDirectory(dir=root) as temp:
            output = Path(temp) / 'export'
            export(endpoint, head['identity'], output, root, timeout, max_bytes)
            manifest, _, _ = publications._load(output)
            require(manifest['identity'] == head['identity'], 'reader_identity')
            require(manifest['sequence'] >= head['sequence'], 'reader_regression')
            require(manifest['sequence'] != head['sequence'] or
                    manifest['publication_id'] == head['publication_id'], 'reader_conflict')
            with projection_access.access(database, write=True):
                publications.import_export(output, database)
            # Importer idempotence preserves projection freshness; reflect that
            # actual timestamp rather than treating validation as new capture.
            imported_ns = _imported_time(database, head['identity'])
            status.execute('''UPDATE targets SET imported_sequence=?,imported_publication=?,imported_ns=?,imported_fingerprint=?,error=NULL
                WHERE endpoint=? AND identity=?''', (manifest['sequence'], manifest['publication_id'], imported_ns, fingerprint, name, key))
    except Exception as exc:
        code = str(exc) if isinstance(exc, (RefreshFailure, projection_access.ProjectionBusy)) else 'import_failed'
        status.execute('UPDATE targets SET error=? WHERE endpoint=? AND identity=?', (code, name, key))
    status.commit()


def _stop(signum, frame):
    raise KeyboardInterrupt


def main():
    os.umask(0o077)
    signal.signal(signal.SIGTERM, _stop)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--endpoints', type=Path, required=True)
    parser.add_argument('--database', type=Path, required=True)
    parser.add_argument('--status', type=Path, required=True)
    parser.add_argument('--once', action='store_true')
    parser.add_argument('--interval', type=float, default=60)
    parser.add_argument('--timeout', type=float, default=300)
    parser.add_argument('--max-bytes', type=int, default=256 * 1024 * 1024)
    args = parser.parse_args()
    try:
        require(1 <= args.interval <= 86400 and 1 <= args.timeout <= 3600 and
                1024 <= args.max_bytes <= 1024**3, 'invalid_bounds')
        endpoints = load_endpoints(args.endpoints)
        args.status.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        args.database.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        with args.database.with_name(args.database.name + '.refresh.lock').open('a') as lock:
            os.chmod(lock.name, 0o600)
            try:
                fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except BlockingIOError:
                raise RefreshFailure('writer_owned') from None
            with contextlib.closing(open_status(args.status)) as status:
                while True:
                    result = sweep(endpoints, args.database, status, timeout=args.timeout, max_bytes=args.max_bytes)
                    print(json.dumps(result, sort_keys=True), flush=True)
                    if args.once:
                        return int(bool(result['endpoint_errors'] or result['incomplete_endpoints'] or result['target_errors'] or result['unimported'] or result['discovery_errors']))
                    time.sleep(args.interval)
    except KeyboardInterrupt:
        return 0
    except Exception:
        print(json.dumps({'contract': 'session-publication-refresh-status/v1', 'error': 'refresh_failed'}), flush=True)
        return 1


if __name__ == '__main__':
    raise SystemExit(main())
