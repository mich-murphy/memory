#!/usr/bin/env python3
"""Legacy schema-3 reconstruction of main/subagent transcripts (read-only R2).

Unsupported for legacy session commits or candidate publications; no stream resolver.
See docs/session-capture-contract.md. Historical local reports need no refresh.

Requires the metadata import's current_manifests table and the zstd executable.
Only GET is used remotely. index.json contains a files list with absolute paths;
complete is false for debug limits or any failure. Never log exception messages or bodies.
"""
import argparse
from collections import Counter
from concurrent.futures import ThreadPoolExecutor
from contextlib import contextmanager
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys
import tempfile

import boto3
from botocore.config import Config
import duckdb

from r2_metadata import MAX_RECORD, settings

CHUNK = 1 << 20


class IntegrityError(ValueError):
    """A fixed, safe error code, never source content or SDK detail."""


def require(condition, code):
    if not condition:
        raise IntegrityError(code)


def digest(value):
    require(isinstance(value, str) and re.fullmatch(r'[0-9a-f]{64}', value), 'invalid_digest')
    return value


def private_dir(path):
    # Reject existing symlinks rather than letting a cache redirect writes.
    path = Path(path)
    if not path.exists() and not path.is_symlink():
        if not path.parent.exists():
            private_dir(path.parent)
        path.mkdir(mode=0o700, exist_ok=True)
    require(not path.is_symlink() and path.is_dir(), 'unsafe_cache_directory')
    os.chmod(path, 0o700)


@contextmanager
def staged(path):
    fd, name = tempfile.mkstemp(prefix='.partial-', dir=path.parent)
    temporary = Path(name)
    try:
        with os.fdopen(fd, 'w+b') as stream:
            yield stream, temporary
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
    finally:
        temporary.unlink(missing_ok=True)


def open_cached(path):
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    try:
        require(stat.S_ISREG(os.fstat(fd).st_mode), 'unsafe_cache_file')
        os.fchmod(fd, 0o600)
        return os.fdopen(fd, 'rb')
    except BaseException:
        os.close(fd)
        raise


def copy_checked(source, target, size, sha):
    require(type(size) is int and size >= 0, 'invalid_size')
    total, checksum = 0, hashlib.sha256()
    while True:
        chunk = source.read(min(CHUNK, size - total + 1))
        if not chunk:
            break
        total += len(chunk)
        require(total <= size, 'length_mismatch')
        checksum.update(chunk)
        if target is not None:
            target.write(chunk)
    require(total == size, 'length_mismatch')
    require(checksum.hexdigest() == sha, 'checksum_mismatch')


def record_key(key, kind, machine, agent, logical_key):
    stream = hashlib.sha256((agent + '\0' + logical_key).encode()).hexdigest()
    prefix = f'v3/machines/{machine}/records/{kind}/{stream}/commits/'
    require(key.startswith(prefix), 'descriptor_key_identity')
    match = re.fullmatch(r'([0-9]{20})-([0-9a-f]{64})\.json', key[len(prefix):])
    require(match is not None and int(match[1]) > 0, 'descriptor_key_format')
    return int(match[1]), match[2]


class Importer:
    def __init__(self, client, bucket, output):
        self.client, self.bucket = client, bucket
        self.root = Path(output) / 'bodies'
        for path in (Path(output), self.root, self.root / 'commits', self.root / 'files'):
            private_dir(path)

    def record(self, key, kind, machine, agent, logical_key):
        sequence, sha = record_key(key, kind, machine, agent, logical_key)
        path = self.root / 'commits' / (sha + '.json')

        def decode(data):
            require(len(data) <= MAX_RECORD and hashlib.sha256(data).hexdigest() == sha,
                    'descriptor_checksum')
            body = json.loads(data)
            require(body['schema'] == 3 and body['kind'] == kind
                    and body['machine_id'] == machine and body['agent'] == agent
                    and body['key'] == logical_key and body['sequence'] == sequence,
                    'descriptor_identity')
            return body

        if path.exists() or path.is_symlink():
            with open_cached(path) as source:
                data = source.read(MAX_RECORD + 1)
            try:
                return decode(data)
            except (ValueError, KeyError, TypeError):
                # A damaged ordinary cached record is repairable by a verified GET.
                pass
        response = self.client.get_object(Bucket=self.bucket, Key=key)
        with response['Body'] as source:
            data = source.read(MAX_RECORD + 1)
        body = decode(data)
        with staged(path) as (target, _):
            target.write(data)
        return body

    def manifest(self, key, expected):
        body = self.record(key, 'manifest', expected['machine_id'], expected['agent'], expected['key'])
        require(body == expected, 'database_manifest_mismatch')
        # Also validate the typed payload's session identity.
        payload = body['manifest']
        require(payload['schema'] == 3 and payload['machine_id'] == body['machine_id']
                and payload['agent'] == body['agent'] and payload['session_key'] == body['key'],
                'manifest_identity')
        return body

    def generation(self, manifest, entry):
        body = self.record(entry['commit'], 'file', manifest['machine_id'], manifest['agent'], entry['key'])
        generation = body['file']
        identity = generation['identity']
        require(generation['schema'] == 3 and identity['machine_id'] == manifest['machine_id']
                and identity['agent'] == manifest['agent'] and identity['key'] == entry['key']
                and identity.get('session_key') == manifest['key']
                and generation['sequence'] == body['sequence']
                and generation.get('parent', '') == body.get('parent', '')
                and generation['kind'] in ('capture', 'append', 'fold', 'delete'), 'file_identity')
        for field in ('role', 'size', 'sha256', 'line_count', 'prefix_sha256'):
            require(generation.get(field) == entry.get(field), 'manifest_file_mismatch')
        require(generation['pieces'] == entry['pieces'], 'manifest_pieces_mismatch')
        digest(generation['sha256'])
        size = generation['size']
        require(type(size) is int and size >= 0, 'invalid_size')
        end = 0
        require(bool(generation['pieces']), 'missing_pieces')
        dependencies = []
        for piece in generation['pieces']:
            digest(piece['sha256'])
            sha = digest(piece['encoded_sha256'])
            require(type(piece['start']) is int and type(piece['end']) is int
                    and piece['start'] == end and piece['end'] >= end
                    and (piece['end'] > end or size == 0)
                    and piece['end'] <= size, 'piece_coverage')
            require(type(piece['encoded_size']) is int and piece['encoded_size'] > 0
                    and piece['object'] == f'v3/blobs/zstd/{sha}.zst', 'piece_identity')
            dependencies.append(dict(object=piece['object'], sha256=sha, size=piece['encoded_size'],
                                     encoding='zstd', raw_sha256=piece['sha256'],
                                     raw_size=piece['end'] - end))
            end = piece['end']
        require(end == size, 'piece_coverage')
        if 'dependencies' in body:
            require(body['dependencies'] == dependencies, 'file_dependencies')
        return generation

    def piece(self, piece, target):
        # Encoded pieces are temporary disk files, not held in RAM. Repeated pieces
        # are deliberately read in order (their offsets differ); no set/dedup ordering.
        with tempfile.TemporaryFile(dir=self.root) as encoded:
            response = self.client.get_object(Bucket=self.bucket, Key=piece['object'])
            with response['Body'] as source:
                copy_checked(source, encoded, piece['encoded_size'], piece['encoded_sha256'])
            encoded.seek(0)
            process = subprocess.Popen(['zstd', '-d', '-q', '-c', '--memory=128MB'],
                                       stdin=encoded, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
            try:
                copy_checked(process.stdout, target, piece['end'] - piece['start'], piece['sha256'])
                require(process.wait() == 0, 'zstd_failed')
            finally:
                process.stdout.close()
                if process.poll() is None:
                    process.kill()
                process.wait()

    def body(self, manifest_key, manifest, entry):
        generation = self.generation(manifest, entry)
        path = self.root / 'files' / digest(generation['sha256'])
        reusable = False
        if path.exists() or path.is_symlink():
            with open_cached(path) as source:
                try:
                    copy_checked(source, None, generation['size'], generation['sha256'])
                    reusable = True
                except IntegrityError:
                    pass
        if not reusable:
            with staged(path) as (target, _):
                for piece in generation['pieces']:
                    self.piece(piece, target)
                target.seek(0)
                copy_checked(target, None, generation['size'], generation['sha256'])
        return dict(machine_id=manifest['machine_id'], agent=manifest['agent'],
                    session_key=manifest['key'], file_key=entry['key'], role=entry['role'],
                    sha256=generation['sha256'], size=generation['size'],
                    path=str(path.resolve()), head=entry.get('head', {}),
                    source_object_key=entry['commit'],
                    file_commit=entry['commit'], manifest_commit=manifest_key,
                    sequence=generation['sequence'], parent=generation.get('parent', ''),
                    pieces=generation['pieces'], reused=reusable)


def safe_code(exc):
    # IntegrityError codes are constants owned here. Never stringify other exceptions.
    return str(exc) if isinstance(exc, IntegrityError) else type(exc).__name__


def run_import(client, bucket, database, output, workers=8, limit=None):
    require(workers > 0 and (limit is None or limit > 0), 'invalid_options')
    importer = Importer(client, bucket, output)
    with duckdb.connect(str(database), read_only=True) as db:
        rows = db.execute('SELECT object_key, body FROM current_manifests ORDER BY object_key').fetchall()
    failures, tasks = [], []
    for key, raw in rows:
        try:
            manifest = importer.manifest(key, json.loads(raw))
            for entry in manifest['manifest'].get('transcripts') or []:
                if entry['role'] in ('main', 'subagent'):
                    tasks.append((key, manifest, entry))
        except Exception as exc:
            failures.append(dict(stage='manifest', source_object_key=key, code=safe_code(exc)))
    selected = tasks if limit is None else tasks[:limit]

    def fetch(task):
        key, manifest, entry = task
        try:
            return importer.body(key, manifest, entry), None
        except Exception as exc:
            return None, dict(stage='body', machine_id=manifest['machine_id'], agent=manifest['agent'],
                              session_key=manifest['key'], file_key=entry['key'],
                              source_object_key=entry.get('commit'), code=safe_code(exc))

    entries = []
    with ThreadPoolExecutor(max_workers=workers) as pool:
        for entry, failure in pool.map(fetch, selected):
            if failure:
                failures.append(failure)
            else:
                entries.append(entry)
    limited = len(selected) < len(tasks)
    index = dict(schema=1, observed_at=datetime.now(timezone.utc).isoformat(),
                 database=str(Path(database).resolve()), complete=not failures and not limited,
                 limited=limited, counts=dict(manifests=len(rows), available=len(tasks),
                 attempted=len(selected), imported=len(entries), errors=len(failures),
                 reused=sum(e['reused'] for e in entries)),
                 error_counts=dict(Counter(f['code'] for f in failures)), files=entries, failures=failures)
    with staged(importer.root / 'index.json') as (target, _):
        target.write((json.dumps(index, indent=2) + '\n').encode())
    return index


def main():
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--config', type=Path, default=Path.home() / '.config/agent-sessions/config.env')
    parser.add_argument('--output', type=Path, default=Path('.analytics'))
    parser.add_argument('--database', type=Path)
    parser.add_argument('--workers', type=int, default=8)
    parser.add_argument('--limit', type=int)
    args = parser.parse_args()
    try:
        require(args.workers > 0 and (args.limit is None or args.limit > 0), 'invalid_options')
        cfg = settings(args.config)
        client = boto3.client('s3', endpoint_url=cfg['R2_ENDPOINT'], region_name='auto',
                              aws_access_key_id=cfg['R2_ACCESS_KEY_ID'],
                              aws_secret_access_key=cfg['R2_SECRET_ACCESS_KEY'],
                              config=Config(max_pool_connections=args.workers,
                                            retries={'mode': 'standard', 'max_attempts': 4}))
        index = run_import(client, cfg['R2_BUCKET'], args.database or args.output / 'sessions.duckdb',
                           args.output, args.workers, args.limit)
        counts = index['counts']
        print(f"Bodies: {counts['imported']}/{counts['attempted']} verified; "
              f"{counts['errors']} errors; complete={index['complete']}", flush=True)
        return 1 if counts['errors'] else 0
    except Exception as exc:
        print(f'Body import failed ({safe_code(exc)}); no full import claimed.', file=sys.stderr)
        return 1


if __name__ == '__main__':
    sys.exit(main())
