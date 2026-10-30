#!/usr/bin/env python3
"""Legacy schema-3 manifest import; unsupported for legacy session commits or candidate publications.

Never fetches blobs or writes to R2. See docs/session-capture-contract.md.
Historical local reports remain usable without a remote refresh.
"""
import argparse
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timezone
import hashlib
import json
import os
import re
from pathlib import Path
import sys

import boto3
from botocore.config import Config
import duckdb

MAX_RECORD = 16 << 20


class UnsupportedContractError(ValueError):
    """A fixed diagnostic safe to print without keys, payloads or SDK details."""


def guard_inventory(inventory):
    """Reject known session publications, not resolve or interpret their bodies.

    Envelope/namespace v3 does not imply payload schema 3. Even a mixed archive
    cannot be safely refreshed by selecting only its legacy manifest subset.
    This is a guard for known incompatible paths, not future contract discovery.
    """
    if any('/records/session/' in r['key'] and '/commits/' in r['key']
           for r in inventory):
        raise UnsupportedContractError(
            'Unsupported archive contract: session commits are present. '
            'This legacy importer only supports schema-3 manifest payloads; '
            'legacy collector session schemas 4/5 and candidate publications are unsupported. '
            'Use the authoritative reader/importer described in docs/session-capture-contract.md. '
            'Existing local research reports can still be opened offline.')


def settings(path):
    values = {}
    for line in (path.read_text().splitlines() if path.exists() else []):
        line = line.strip().removeprefix('export ')
        if line and not line.startswith('#') and '=' in line:
            key, value = line.split('=', 1)
            values[key.strip()] = value.strip().strip('\"\'')
    for key in ('R2_ENDPOINT', 'R2_BUCKET', 'R2_ACCESS_KEY_ID', 'R2_SECRET_ACCESS_KEY'):
        values[key] = os.environ.get(key) or values.get(key)
        if not values[key]:
            raise ValueError(f'Missing setting: {key}')
    return values


def select_chain(records):
    """Require one complete sequence/parent chain, never elect by timestamp."""
    ordered = sorted(records, key=lambda r: r['body']['sequence'])
    parent = ''
    identity = None
    for sequence, record in enumerate(ordered, 1):
        body = record['body']
        current_identity = (body['machine_id'], body['agent'], body['key'])
        if identity is None:
            identity = current_identity
        if (body['sequence'] != sequence or body.get('parent', '') != parent
                or current_identity != identity):
            raise ValueError('Conflicting or incomplete manifest chain; refusing latest selection')
        parent = record['hash']
    if not ordered:
        raise ValueError('Empty manifest chain')
    return ordered[-1]


def decode_record(key, data):
    parts = key.split('/')
    sequence, digest = parts[-1].removesuffix('.json').split('-', 1)
    if len(data) > MAX_RECORD or hashlib.sha256(data).hexdigest() != digest:
        raise ValueError('Manifest size/checksum mismatch')
    body = json.loads(data)
    stream = hashlib.sha256((body['agent'] + '\0' + body['key']).encode()).hexdigest()
    if body.get('schema') != 3 or body.get('kind') != 'manifest':
        raise UnsupportedContractError(
            'Unsupported record schema/kind: legacy schema-3 manifests only; '
            'see docs/session-capture-contract.md.')
    if (body['machine_id'] != parts[2] or stream != parts[5]
            or body['sequence'] != int(sequence)):
        raise ValueError('Manifest identity mismatch')
    manifest = body['manifest']
    if manifest.get('schema') != 3:
        raise UnsupportedContractError(
            'Unsupported manifest payload schema: legacy schema 3 only; '
            'see docs/session-capture-contract.md.')
    if (manifest['machine_id'] != body['machine_id']
            or manifest['agent'] != body['agent'] or manifest['session_key'] != body['key']):
        raise ValueError('Manifest payload identity mismatch')
    return {'key': key, 'hash': digest, 'body': body}


def refresh(args):
    cfg = settings(args.config)
    client = boto3.client(
        's3', endpoint_url=cfg['R2_ENDPOINT'], region_name='auto',
        aws_access_key_id=cfg['R2_ACCESS_KEY_ID'],
        aws_secret_access_key=cfg['R2_SECRET_ACCESS_KEY'],
        config=Config(max_pool_connections=8, retries={'mode': 'standard', 'max_attempts': 4}),
    )
    inventory = []
    for page in client.get_paginator('list_objects_v2').paginate(Bucket=cfg['R2_BUCKET']):
        for obj in page.get('Contents', []):
            inventory.append({'key': obj['Key'], 'size': obj['Size'],
                              'last_modified': obj['LastModified'].isoformat()})
    # Finish LIST and reject incompatible inventories before GETs or local writes.
    guard_inventory(inventory)
    args.output.mkdir(mode=0o700, parents=True, exist_ok=True)
    cache = args.output / 'manifests'
    cache.mkdir(mode=0o700, exist_ok=True)
    keys = [r['key'] for r in inventory
            if '/records/manifest/' in r['key'] and '/commits/' in r['key']
            and r['key'].startswith('v3/machines/')]
    print(f'Listed {len(inventory)} objects; reading {len(keys)} manifest commits', flush=True)

    def fetch(key):
        # Content-addressed cache: all cached bodies are reverified on every run.
        digest = key.rsplit('-', 1)[-1].removesuffix('.json')
        if not re.fullmatch('[0-9a-f]{64}', digest):
            raise ValueError('Invalid descriptor hash in object key')
        path = cache / (digest + '.json')
        if path.exists():
            data = path.read_bytes()
        else:
            response = client.get_object(Bucket=cfg['R2_BUCKET'], Key=key)
            with response['Body'] as body:
                data = body.read(MAX_RECORD + 1)
            decode_record(key, data)
            temporary = path.with_suffix('.tmp')
            temporary.write_bytes(data)
            temporary.replace(path)
        return decode_record(key, data)

    streams = {}
    commits = []
    with ThreadPoolExecutor(max_workers=8) as pool:
        for record in pool.map(fetch, keys):
            streams.setdefault(record['key'].split('/commits/')[0], []).append(record)
            commits.append((record['key'], json.dumps(record['body'])))
    latest = [select_chain(records) for records in streams.values()]
    # Import transactionally: a failed refresh leaves the previous database intact.
    with duckdb.connect(str(args.output / 'sessions.duckdb')) as db:
        db.execute('BEGIN')
        db.execute('CREATE OR REPLACE TABLE inventory(key VARCHAR, size BIGINT, last_modified TIMESTAMPTZ)')
        if inventory:
            db.executemany('INSERT INTO inventory VALUES (?, ?, ?)',
                           [(r['key'], r['size'], r['last_modified']) for r in inventory])
        db.execute('CREATE OR REPLACE TABLE manifest_commits(object_key VARCHAR, body JSON)')
        if commits:
            db.executemany('INSERT INTO manifest_commits VALUES (?, ?)', commits)
        db.execute('CREATE OR REPLACE TABLE current_manifests(object_key VARCHAR, body JSON)')
        if latest:
            db.executemany('INSERT INTO current_manifests VALUES (?, ?)',
                           [(r['key'], json.dumps(r['body'])) for r in latest])
        db.execute('CREATE OR REPLACE TABLE refresh_info AS SELECT ?::TIMESTAMPTZ AS observed_at',
                   [datetime.now(timezone.utc).isoformat()])
        db.execute(Path(__file__).with_name('views.sql').read_text())
        db.execute('COMMIT')
    print(f'Imported {len(latest)} current session manifests into {args.output}/sessions.duckdb')


def main():
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--config', type=Path, default=Path.home() / '.config/agent-sessions/config.env')
    parser.add_argument('--output', type=Path, default=Path('.analytics'))
    args = parser.parse_args()
    try:
        refresh(args)
    except UnsupportedContractError as exc:
        print(f'Import failed: {exc}', file=sys.stderr)
        return 1
    except Exception as exc:
        # SDK exception text can contain request/config data. Keep credentials out of logs.
        print(f'Import failed ({type(exc).__name__}); check credentials, network and metadata integrity.', file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())
