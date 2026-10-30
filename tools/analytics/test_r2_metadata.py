from contextlib import redirect_stderr, redirect_stdout
from datetime import datetime, timezone
import hashlib
import io
import json
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import Mock, patch

import duckdb

from r2_metadata import decode_record, main, refresh, select_chain, UnsupportedContractError


def record(sequence=1, parent='', agent='pi'):
    body = {'schema': 3, 'machine_id': 'machine', 'kind': 'manifest',
            'agent': agent, 'key': 'session', 'sequence': sequence,
            'manifest': {'schema': 3, 'machine_id': 'machine', 'agent': agent,
                         'session_key': 'session'}}
    if parent:
        body['parent'] = parent
    data = json.dumps(body).encode()
    digest = hashlib.sha256(data).hexdigest()
    stream = hashlib.sha256((agent + '\0session').encode()).hexdigest()
    key = f'v3/machines/machine/records/manifest/{stream}/commits/{sequence:020d}-{digest}.json'
    return decode_record(key, data), data


class MetadataTests(unittest.TestCase):
    def test_complete_chain_in_any_input_order(self):
        first, _ = record()
        second, _ = record(2, first['hash'])
        self.assertEqual(select_chain([second, first]), second)

    def test_refuses_conflicts_gaps_wrong_parent_and_identity(self):
        first, _ = record()
        second, _ = record(2, first['hash'])
        conflict, _ = record(2, 'f' * 64)
        foreign, _ = record(2, first['hash'], 'claude')
        for records in ([second], [first, second, conflict], [first, conflict],
                        [first, foreign], []):
            with self.subTest(records=records), self.assertRaises(ValueError):
                select_chain(records)

    def test_checksum_and_path_identity(self):
        first, data = record()
        with self.assertRaises(ValueError):
            decode_record(first['key'], data + b' ')
        with self.assertRaises(ValueError):
            decode_record(first['key'].replace('/machine/', '/other/'), data)

    def mocked_refresh(self, output, pages, objects=None):
        client = Mock()
        client.get_paginator.return_value.paginate.return_value = pages
        objects = objects or {}
        client.get_object.side_effect = lambda **kw: {'Body': io.BytesIO(objects[kw['Key']])}
        cfg = dict(R2_ENDPOINT='offline', R2_BUCKET='offline',
                   R2_ACCESS_KEY_ID='offline', R2_SECRET_ACCESS_KEY='offline')
        with patch('r2_metadata.settings', return_value=cfg), \
                patch('r2_metadata.boto3.client', return_value=client), \
                redirect_stdout(io.StringIO()):
            refresh(SimpleNamespace(config=Path('unused'), output=output))
        return client

    @staticmethod
    def page(*keys):
        return {'Contents': [dict(Key=key, Size=1, LastModified=datetime.now(timezone.utc))
                             for key in keys]}

    def test_session_inventories_rejected_before_fetch_or_any_local_write(self):
        legacy, _ = record()
        session = legacy['key'].replace('/records/manifest/', '/records/session/')
        for pages in ([self.page(session)], [self.page(legacy['key']), self.page(session)]):
            with self.subTest(pages=len(pages)), tempfile.TemporaryDirectory() as tmp:
                output = Path(tmp) / 'not-created'
                with self.assertRaisesRegex(UnsupportedContractError, 'session commits'):
                    # No object bodies supplied: a GET would fail the test.
                    self.mocked_refresh(output, pages)
                self.assertFalse(output.exists())

    def test_session_guard_preserves_database_timestamp_and_artifacts(self):
        legacy, _ = record()
        session = legacy['key'].replace('/records/manifest/', '/records/session/')
        for mixed in (False, True):
            with self.subTest(mixed=mixed), tempfile.TemporaryDirectory() as tmp:
                output = Path(tmp)
                self.mocked_refresh(output, [{}])
                database = output / 'sessions.duckdb'
                with duckdb.connect(str(database)) as db:
                    db.execute("INSERT INTO current_manifests VALUES ('historical', '{}')")
                    observed = db.sql('SELECT observed_at::VARCHAR FROM refresh_info').fetchall()
                artifact = output / 'report' / 'reviewed.json'
                artifact.parent.mkdir()
                artifact.write_text('user-owned research')
                before = {p.relative_to(output): p.read_bytes()
                          for p in output.rglob('*') if p.is_file()}
                pages = [self.page(legacy['key'])] if mixed else []
                pages.append(self.page(session))
                with self.assertRaises(UnsupportedContractError):
                    self.mocked_refresh(output, pages)
                after = {p.relative_to(output): p.read_bytes()
                         for p in output.rglob('*') if p.is_file()}
                self.assertEqual(before, after)
                with duckdb.connect(str(database), read_only=True) as db:
                    self.assertEqual(db.sql('SELECT observed_at::VARCHAR FROM refresh_info').fetchall(), observed)
                    self.assertEqual(db.sql('SELECT object_key FROM current_manifests').fetchall(),
                                     [('historical',)])

    def test_empty_store_and_legacy_import_remain_supported(self):
        legacy, data = record()
        with tempfile.TemporaryDirectory() as tmp:
            output = Path(tmp)
            self.mocked_refresh(output, [self.page(legacy['key'])], {legacy['key']: data})
            with duckdb.connect(str(output / 'sessions.duckdb'), read_only=True) as db:
                self.assertEqual(db.sql('SELECT count(*) FROM current_manifests').fetchone(), (1,))
            client = self.mocked_refresh(output, [{}])
            client.get_object.assert_not_called()
            with duckdb.connect(str(output / 'sessions.duckdb'), read_only=True) as db:
                self.assertEqual(db.sql('SELECT count(*) FROM current_manifests').fetchone(), (0,))
                self.assertEqual(db.sql('SELECT count(*) FROM refresh_info').fetchone(), (1,))

    def test_unsupported_schemas_explicit(self):
        legacy, _ = record()
        for envelope, payload in ((4, 3), (3, 4), (3, 5)):
            body = json.loads(json.dumps(legacy['body']))
            body['schema'], body['manifest']['schema'] = envelope, payload
            data = json.dumps(body).encode()
            key = legacy['key'].replace(legacy['hash'], hashlib.sha256(data).hexdigest())
            with self.subTest(envelope=envelope, payload=payload):
                with self.assertRaisesRegex(UnsupportedContractError, 'Unsupported'):
                    decode_record(key, data)
                with tempfile.TemporaryDirectory() as tmp:
                    output = Path(tmp)
                    self.mocked_refresh(output, [{}])
                    before = (output / 'sessions.duckdb').read_bytes()
                    with self.assertRaises(UnsupportedContractError):
                        self.mocked_refresh(output, [self.page(key)], {key: data})
                    self.assertEqual((output / 'sessions.duckdb').read_bytes(), before)

    def test_cli_reports_safe_unsupported_contract_and_nonzero_status(self):
        message = 'Unsupported archive contract; see docs/session-capture-contract.md.'
        with patch('sys.argv', ['r2_metadata.py']), \
                patch('r2_metadata.refresh', side_effect=UnsupportedContractError(message)), \
                patch('r2_metadata.os.umask'), redirect_stderr(io.StringIO()) as stderr:
            self.assertEqual(main(), 1)
        self.assertIn(message, stderr.getvalue())

    def test_sql_keeps_child_models_and_sizes_separate(self):
        with duckdb.connect() as db:
            db.execute('CREATE TABLE current_manifests(object_key VARCHAR, body JSON)')
            first, _ = record()
            body = first['body']
            body['manifest'].update({
                'first_seen': 1, 'synced_at': 2, 'launches': [],
                'transcripts': [
                    {'key': 'main', 'role': 'main', 'size': 100, 'line_count': 3,
                     'head': {'model': 'main-model', 'started_at': '2026-01-01T00:00:00Z'}},
                    {'key': 'child', 'role': 'subagent', 'size': 50, 'line_count': 2,
                     'head': {'model': 'child-model', 'started_at': 1700000000000}},
                ],
            })
            db.execute('INSERT INTO current_manifests VALUES (?, ?)', ['commit', json.dumps(body)])
            db.execute(Path(__file__).with_name('views.sql').read_text())
            self.assertEqual(db.sql('SELECT count(*) FROM sessions').fetchone(), (1,))
            self.assertEqual(db.sql('SELECT sum(decoded_bytes) FROM transcripts').fetchone(), (150,))
            self.assertEqual(db.sql("SELECT model FROM transcripts WHERE role='main'").fetchone(), ('main-model',))
            self.assertEqual(db.sql("SELECT started_at FROM transcripts WHERE role='subagent'").fetchone(), (None,))


if __name__ == '__main__':
    unittest.main()
