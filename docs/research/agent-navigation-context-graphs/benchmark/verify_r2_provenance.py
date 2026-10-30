#!/usr/bin/env python3
"""Read-only verification of task provenance against archived R2 file commits.

Requires optional analytics environment (boto3, duckdb), not navigation runtime.
GET only: no LIST, capture, upload, remote writes, or raw transcript output.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import sys


def main():
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--memory-root', type=Path, default=Path(__file__).resolve().parents[4])
    parser.add_argument('--config', type=Path, default=Path.home()/'.config/agent-sessions/config.env')
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    root = args.memory_root
    sys.path.insert(0, str(root/'tools/analytics'))
    from r2_metadata import settings
    import boto3
    from botocore.config import Config
    import duckdb
    tasks = json.loads((Path(__file__).parent/'tasks.json').read_text())['tasks']
    config = settings(args.config)
    client = boto3.client('s3', endpoint_url=config['R2_ENDPOINT'], region_name='auto',
                          aws_access_key_id=config['R2_ACCESS_KEY_ID'],
                          aws_secret_access_key=config['R2_SECRET_ACCESS_KEY'],
                          config=Config(retries={'mode':'standard','max_attempts':3}))
    db = duckdb.connect(str(root/'.analytics/events.duckdb'), read_only=True)
    records = []
    fetched = {}
    for task in tasks:
        source = task['source']
        row = db.execute('select file_commit,sha256 from files where trace_id=?',
                         [source['trace_id']]).fetchone()
        if row is None:
            raise ValueError('Unknown trace')
        key, source_hash = row
        if key not in fetched:
            response = client.get_object(Bucket=config['R2_BUCKET'],Key=key)
            with response['Body'] as body:
                raw = body.read((16<<20)+1)
            digest = hashlib.sha256(raw).hexdigest()
            if len(raw) > 16<<20 or digest != key.rsplit('-',1)[-1].removesuffix('.json'):
                raise ValueError('R2 descriptor checksum mismatch')
            descriptor = json.loads(raw)
            file = descriptor['file']
            fetched[key] = (digest,file['sha256'])
        digest, archived_hash = fetched[key]
        if archived_hash != source_hash or archived_hash != source['source_file_sha256']:
            raise ValueError('Task source does not match committed archive bytes')
        # Record identifiers/checksums only; descriptor payload and transcript remain private.
        records.append({'task_id':task['id'],'trace_id':source['trace_id'],
                        'source_line':source['line'],'r2_file_commit':key,
                        'descriptor_sha256':digest,'transcript_sha256':archived_hash,
                        'verified':True})
    import datetime
    output = {'verified_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),
              'operations':'R2 GET immutable file commits only',
              'unique_gets':len(fetched),'tasks':records,
              'limitations':['Checks selected archived generations, not latest bucket state or complete chain audit.',
                             'No raw transcript bodies or private configuration copied into proof.']}
    args.output.parent.mkdir(parents=True,exist_ok=True)
    args.output.write_text(json.dumps(output,indent=2)+'\n')
    print(f'Verified {len(records)} task references against {len(fetched)} immutable R2 commits')

if __name__ == '__main__':
    try:
        main()
    except Exception as exc:
        # SDK exception text can include request information; do not expose it.
        print(f'Provenance verification failed ({type(exc).__name__})',file=sys.stderr)
        raise SystemExit(1)
