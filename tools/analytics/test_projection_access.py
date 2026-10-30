"""Real cross-process DuckDB coordination; no network or external locks."""
import os
from pathlib import Path
import select
import subprocess
import sys
import tempfile
import time
import unittest

import duckdb
import projection_access


class ProjectionAccessTests(unittest.TestCase):
    def test_writer_reader_and_reader_writer_wait_until_connection_closed(self):
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp) / 'projection.duckdb'
            with duckdb.connect(str(path)) as db:
                db.execute('CREATE TABLE example(value INTEGER)')
                db.execute('INSERT INTO example VALUES (1)')
            for writer in (True, False):
                code = '''import sys,time,duckdb
from projection_access import access
with access(sys.argv[1],write=sys.argv[2]=='True'):
 with duckdb.connect(sys.argv[1],read_only=sys.argv[2]!='True') as db:
  print('ready',flush=True)
  time.sleep(.4)
'''
                env = dict(os.environ, PYTHONPATH=str(Path(__file__).parent))
                child = subprocess.Popen([sys.executable, '-c', code, str(path), str(writer)],
                                         env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
                try:
                    self.assertTrue(select.select([child.stdout], [], [], 10)[0])
                    self.assertEqual(child.stdout.readline().strip(), 'ready')
                    started = time.monotonic()
                    with projection_access.access(path, write=not writer):
                        with duckdb.connect(str(path), read_only=writer) as db:
                            self.assertEqual(db.execute('SELECT value FROM example').fetchone(), (1,))
                    self.assertGreater(time.monotonic()-started, .2)
                    child.communicate(timeout=10)
                    self.assertEqual(child.returncode, 0)
                finally:
                    if child.poll() is None:
                        child.kill()
                    child.communicate()

    def test_private_no_follow_and_bounded_wait(self):
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp) / 'projection'
            lock = path.with_name(path.name + '.projection.lock')
            lock.symlink_to(Path(temp) / 'target')
            with self.assertRaisesRegex(projection_access.ProjectionBusy, 'projection_lock_unavailable'):
                with projection_access.access(path):
                    pass
            lock.unlink()
            with projection_access.access(path, write=True):
                with self.assertRaises(projection_access.ProjectionBusy):
                    with projection_access.access(path, timeout=.05):
                        pass


if __name__ == '__main__':
    unittest.main()
