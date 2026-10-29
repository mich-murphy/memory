"""Actual launcher -> candidate -> durable queue; synthetic files only.

Run with CAPTURE_V1_BINARY pointing at the built production binary. This test
never contacts R2: missing credentials must pause delivery before transport.
"""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time
import unittest


@unittest.skipUnless(os.environ.get('CAPTURE_V1_BINARY'), 'requires explicitly built candidate binary')
class LaunchJourneyTests(unittest.TestCase):
    def test_missing_and_malformed_credentials_capture_durably(self):
        for malformed in (False, True):
            with self.subTest(malformed=malformed), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp).resolve()
                binary = Path(os.environ['CAPTURE_V1_BINARY']).resolve()
                home = root / 'home'
                home.mkdir(mode=0o700)
                policy = root / 'policy'
                (policy / 'exclusions').mkdir(parents=True, mode=0o700)
                credentials = root / 'credentials.env'
                if malformed:
                    credentials.write_text('R2_ACCESS_KEY_ID=$(synthetic-secret)\n')
                    credentials.chmod(0o600)
                config = root / 'runtime.json'
                config.write_text(json.dumps(dict(version='capture-runtime/v1', archive_id='launcher',
                    machine_id='synthetic', backend=dict(kind='r2',
                        endpoint='https://' + '0' * 32 + '.r2.cloudflarestorage.com', bucket='synthetic-fixture',
                        access_key_env='R2_ACCESS_KEY_ID', secret_key_env='R2_SECRET_ACCESS_KEY'),
                    spool=str(root / 'spool'), policy_dir=str(policy), home=str(home),
                    default_roots=True, roots=[], capture_workers=1, delivery_workers=1,
                    poll_seconds=1, operation_seconds=2, max_attempts=3,
                    max_spool_bytes=1 << 24, min_free_bytes=0, entries_per_root=64)))
                config.chmod(0o600)
                source = home / '.claude/projects/fixture/12345678-1234-1234-1234-123456789abc.jsonl'
                source.parent.mkdir(parents=True, mode=0o700)
                source.write_bytes(b'{"type":"user","uuid":"u","message":{"role":"user","content":"Synthetic launcher capture"}}\n')
                env = dict(os.environ, HOME=str(home), XDG_CONFIG_HOME=str(home / '.config'),
                           R2_ACCESS_KEY_ID='ambient-must-not-be-used', R2_SECRET_ACCESS_KEY='ambient-must-not-be-used')
                env.pop('AGENT_SESSIONS_DISABLE', None)
                wrapper = [sys.executable, str(Path(__file__).with_name('launch.py')),
                           '--binary', str(binary), '--credentials-env', str(credentials), '--', 'capture-v1']
                process = subprocess.Popen([*wrapper, 'run', '--config', str(config)], env=env,
                                           stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
                try:
                    deadline = time.monotonic() + 30
                    captured = None
                    while time.monotonic() < deadline:
                        self.assertIsNone(process.poll(), 'actual launcher/coordinator exited before capture')
                        status = subprocess.run([*wrapper, 'work-status', '--config', str(config)], env=env,
                                                capture_output=True, timeout=10, check=False)
                        if status.returncode == 0:
                            view = json.loads(status.stdout)
                            rows = [row for row in view.get('Sources', []) if row['CapturedVersion'] > 0]
                            if rows and view['Runtime']['Paused']:
                                captured = rows[0]
                                break
                        time.sleep(0.05)
                    self.assertIsNotNone(captured, 'durable local capture/destination pause not observed')
                finally:
                    process.terminate()
                    _, diagnostic = process.communicate(timeout=15)
                self.assertEqual(process.returncode, 0)
                self.assertIn(b'local capture only', diagnostic)
                self.assertNotIn(b'synthetic-secret', diagnostic)
                self.assertNotIn(b'ambient-must-not-be-used', diagnostic)
                # Reopen after orderly exit: accepted bytes outlive the source and
                # do not require another hook to remain pending for delivery.
                source.unlink()
                status = subprocess.run([*wrapper, 'work-status', '--config', str(config)], env=env,
                                        capture_output=True, timeout=10, check=False)
                self.assertEqual(status.returncode, 0)
                view = json.loads(status.stdout)
                self.assertTrue(view['Runtime']['Paused'])
                self.assertGreater(view['Runtime']['PendingPublications'], 0)
                self.assertTrue(any(row['CapturedVersion'] > 0 for row in view['Sources']))
                if os.environ.get('CAPTURE_V1_REPAIR_BINARY'):
                    # Separately scoped test-only build: the existing SDK fixture
                    # peer replaces transport, never credentials or queue logic.
                    # The unmodified binary above proves production startup.
                    repaired = Path(os.environ['CAPTURE_V1_REPAIR_BINARY']).resolve()
                    wrapper[wrapper.index('--binary') + 1] = str(repaired)
                    credentials.write_text('R2_ACCESS_KEY_ID=synthetic-repaired\nR2_SECRET_ACCESS_KEY=synthetic-repaired-secret\n')
                    credentials.chmod(0o600)
                    resumed = subprocess.run([*wrapper, 'resume', '--config', str(config)], env=env,
                                             capture_output=True, timeout=10, check=False)
                    self.assertEqual(resumed.returncode, 0)
                    process = subprocess.Popen([*wrapper, 'run', '--config', str(config)], env=env,
                                               stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
                    try:
                        deadline = time.monotonic() + 30
                        delivered = False
                        while time.monotonic() < deadline:
                            self.assertIsNone(process.poll(), 'repaired coordinator exited')
                            status = subprocess.run([*wrapper, 'work-status', '--config', str(config)],
                                                    env=env, capture_output=True, timeout=10, check=False)
                            if status.returncode == 0:
                                view = json.loads(status.stdout)
                                delivered = (not view['Runtime']['Paused'] and
                                    view['Runtime']['PendingPublications'] == 0 and
                                    any(row['PublicationID'] for row in view['Sources']))
                                if delivered:
                                    break
                            time.sleep(0.05)
                        self.assertTrue(delivered, 'repaired restart did not deliver durable work without source/hook')
                    finally:
                        process.terminate()
                        _, diagnostic = process.communicate(timeout=15)
                    self.assertEqual(process.returncode, 0)
                    self.assertNotIn(b'synthetic-repaired-secret', diagnostic)


if __name__ == '__main__':
    unittest.main()
