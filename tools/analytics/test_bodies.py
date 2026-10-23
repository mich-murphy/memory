import copy
import hashlib
import io
import json
from pathlib import Path
import subprocess
import tempfile
import unittest

import duckdb

from bodies import Importer, IntegrityError, run_import


def sha(data):
    return hashlib.sha256(data).hexdigest()


class Store:
    """Only get_object exists: accidental remote mutations cannot pass tests."""
    def __init__(self):
        self.objects = {}
        self.calls = []

    def get_object(self, *, Bucket, Key):
        self.calls.append(Key)
        return {'Body': io.BytesIO(self.objects[Key])}

    def record(self, body):
        data = json.dumps(body, separators=(',', ':'), sort_keys=True).encode()
        stream = sha((body['agent'] + '\0' + body['key']).encode())
        key = (f"v3/machines/{body['machine_id']}/records/{body['kind']}/{stream}"
               f"/commits/{body['sequence']:020d}-{sha(data)}.json")
        self.objects[key] = data
        return key


class BodyTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary.name)
        self.store = Store()
        self.importer = Importer(self.store, 'bucket', self.root)
        self.content = b'first line\nsecond line\n'
        self.pieces = []
        offset = 0
        # Duplicate compressed objects at different decoded offsets are legitimate.
        for data in (self.content, self.content):
            encoded = subprocess.run(['zstd', '-q', '-c'], input=data, capture_output=True,
                                     check=True).stdout
            key = f'v3/blobs/zstd/{sha(encoded)}.zst'
            self.store.objects[key] = encoded
            self.pieces.append(dict(start=offset, end=offset + len(data), sha256=sha(data),
                                    encoded_sha256=sha(encoded), encoded_size=len(encoded), object=key))
            offset += len(data)
        self.generation = dict(schema=3, identity=dict(machine_id='machine', agent='pi',
                               key='../../never-a-local-path', session_key='session'), sequence=1,
                               kind='capture', role='main', size=offset, sha256=sha(self.content * 2),
                               line_count=4, pieces=self.pieces)
        self.manifest = dict(schema=3, machine_id='machine', agent='pi', kind='manifest',
                             key='session', sequence=1)
        self.publish()

    def tearDown(self):
        self.temporary.cleanup()

    def publish(self):
        self.publication = dict(schema=3, machine_id='machine', agent='pi', kind='file',
                                key=self.generation['identity']['key'], sequence=1,
                                file=self.generation)
        self.commit = self.store.record(self.publication)
        self.entry = {key: copy.deepcopy(self.generation[key]) for key in
                      ('role', 'size', 'sha256', 'line_count', 'pieces')}
        self.entry.update(key=self.publication['key'], commit=self.commit, head={'model': 'model'})
        self.manifest['manifest'] = dict(schema=3, machine_id='machine', agent='pi',
                                         session_key='session', transcripts=[self.entry])
        self.manifest_key = self.store.record(self.manifest)

    def restore(self):
        return self.importer.body(self.manifest_key, self.manifest, self.entry)

    def test_repeated_pieces_stream_and_reuse_private_digest_cache(self):
        result = self.restore()
        path = Path(result['path'])
        self.assertEqual(path.read_bytes(), self.content * 2)
        self.assertEqual(path.name, sha(self.content * 2))
        self.assertEqual(path.parent, (self.root / 'bodies' / 'files').resolve())
        self.assertEqual(path.stat().st_mode & 0o777, 0o600)
        self.assertEqual(path.parent.stat().st_mode & 0o777, 0o700)
        calls = len(self.store.calls)
        self.assertTrue(self.restore()['reused'])
        self.assertEqual(len(self.store.calls), calls)

    def test_corrupt_descriptor_hash_and_wrong_identity(self):
        self.store.objects[self.commit] += b' '
        with self.assertRaisesRegex(IntegrityError, 'descriptor_checksum'):
            self.restore()
        self.publish()
        self.generation['identity']['session_key'] = 'foreign'
        self.publish()
        with self.assertRaisesRegex(IntegrityError, 'file_identity'):
            self.restore()

    def test_encoded_hash_and_length_corruption(self):
        key = self.pieces[0]['object']
        encoded = self.store.objects[key]
        for data in (encoded[:-1], bytes([encoded[0] ^ 1]) + encoded[1:], encoded + b'x'):
            with self.subTest(length=len(data)):
                self.store.objects[key] = data
                with self.assertRaises(IntegrityError):
                    self.restore()
                self.assertFalse(any((self.root / 'bodies' / 'files').iterdir()))

    def test_decoded_hash_and_length_corruption(self):
        original = copy.deepcopy(self.generation)
        for field, value in (('sha256', 'f' * 64), ('end', len(self.content) - 1)):
            with self.subTest(field=field):
                generation = copy.deepcopy(original)
                generation['pieces'][0][field] = value
                if field == 'end':
                    generation['pieces'][1]['start'] = value
                self.generation = generation
                self.publish()
                with self.assertRaises(IntegrityError):
                    self.restore()

    def test_gap_reorder_and_whole_hash_mismatch(self):
        original = copy.deepcopy(self.generation)
        for mode in ('gap', 'reorder', 'whole'):
            with self.subTest(mode=mode):
                self.generation = copy.deepcopy(original)
                if mode == 'gap':
                    self.generation['pieces'][1]['start'] += 1
                elif mode == 'reorder':
                    self.generation['pieces'].reverse()
                else:
                    self.generation['sha256'] = 'f' * 64
                self.publish()
                with self.assertRaises(IntegrityError):
                    self.restore()
                self.assertFalse(any((self.root / 'bodies' / 'files').iterdir()))

    def test_manifest_mismatch_and_explicit_bad_dependencies(self):
        self.entry['size'] += 1
        with self.assertRaisesRegex(IntegrityError, 'manifest_file_mismatch'):
            self.restore()
        self.publish()
        self.publication['dependencies'] = []
        self.entry['commit'] = self.store.record(self.publication)
        with self.assertRaisesRegex(IntegrityError, 'file_dependencies'):
            self.restore()

    def test_unsafe_object_key_cannot_escape_cache(self):
        self.generation['pieces'][0]['object'] = '../../outside'
        self.publish()
        with self.assertRaisesRegex(IntegrityError, 'piece_identity'):
            self.restore()
        self.assertEqual(self.store.calls, [self.commit])

    def test_corrupt_body_cache_repairs_but_symlink_is_rejected(self):
        result = self.restore()
        path = Path(result['path'])
        path.write_bytes(b'corrupt')
        self.assertFalse(self.restore()['reused'])
        self.assertEqual(path.read_bytes(), self.content * 2)
        path.unlink()
        outside = self.root / 'outside'
        outside.write_bytes(self.content * 2)
        path.symlink_to(outside)
        with self.assertRaises(OSError):
            self.restore()
        self.assertEqual(outside.read_bytes(), self.content * 2)

    def test_corrupt_commit_cache_repairs_and_private_index_reports_failures(self):
        self.restore()
        cached = next((self.root / 'bodies' / 'commits').iterdir())
        cached.write_bytes(b'corrupt')
        self.restore()
        self.assertGreater(len(self.store.calls), 3)
        database = self.root / 'sessions.duckdb'
        with duckdb.connect(str(database)) as db:
            db.execute('CREATE TABLE current_manifests(object_key VARCHAR, body JSON)')
            db.execute('INSERT INTO current_manifests VALUES (?, ?)',
                       [self.manifest_key, json.dumps(self.manifest)])
        index = run_import(self.store, 'bucket', database, self.root, workers=2)
        self.assertTrue(index['complete'])
        self.assertEqual(index['files'][0]['head'], {'model': 'model'})
        self.assertEqual(index['counts']['imported'], 1)
        self.assertEqual((self.root / 'bodies' / 'index.json').stat().st_mode & 0o777, 0o600)
        # Raw SDK exception text is never copied into the index.
        self.store.objects.pop(self.manifest_key)
        for path in (self.root / 'bodies' / 'commits').iterdir():
            path.unlink()
        index = run_import(self.store, 'bucket', database, self.root)
        self.assertFalse(index['complete'])
        self.assertEqual(index['counts']['errors'], 1)
        self.assertEqual(index['error_counts'], {'KeyError': 1})
        self.assertEqual(index['files'], [])
        self.assertNotIn('Traceback', json.dumps(index))


if __name__ == '__main__':
    unittest.main()
