import importlib.util
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('candidate_launch', Path(__file__).with_name('launch.py'))
launch = importlib.util.module_from_spec(spec)
spec.loader.exec_module(launch)


class LaunchTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.config = self.root / 'config.env'
        self.config.write_text('R2_ENDPOINT=https://unused.invalid\nR2_ACCESS_KEY_ID=synthetic-key\nR2_SECRET_ACCESS_KEY="synthetic-secret"\nOTHER=ignored\n')
        self.config.chmod(0o600)
        self.binary = self.root / 'candidate'
        self.binary.write_text('#!/bin/sh\nexit 0\n')
        self.binary.chmod(0o700)
        self.argv = ['--binary', str(self.binary), '--credentials-env', str(self.config), '--', 'capture-v1', 'work-status']

    def test_named_values_only_no_shell(self):
        self.assertEqual(launch.credentials(self.config), {'R2_ACCESS_KEY_ID': 'synthetic-key', 'R2_SECRET_ACCESS_KEY': 'synthetic-secret'})
        self.config.write_text('R2_ACCESS_KEY_ID=$(touch /never-run)\nR2_SECRET_ACCESS_KEY=synthetic\n')
        with self.assertRaises(ValueError):
            launch.credentials(self.config)

    def test_duplicate_missing_oversize_and_permissions(self):
        original = self.config.read_text()
        for body in ['R2_ACCESS_KEY_ID=one\n', original + 'R2_ACCESS_KEY_ID=duplicate\n', 'x' * 65537]:
            self.config.write_text(body)
            with self.assertRaises(ValueError):
                launch.credentials(self.config)
        self.config.write_text(original)
        self.config.chmod(0o644)
        with self.assertRaises(ValueError):
            launch.credentials(self.config)

    def test_refuses_symlink_and_nonregular(self):
        link = self.root / 'linked'
        link.symlink_to(self.config)
        with self.assertRaises(OSError):
            launch.credentials(link)
        fifo = self.root / 'fifo'
        os.mkfifo(fifo, 0o600)
        with self.assertRaises(ValueError):
            launch.credentials(fifo)

    def test_explicit_candidate_only_and_safe_binary(self):
        with patch.dict(os.environ, {'AGENT_SESSIONS_LIVE': '1', 'AGENT_SESSIONS_CAPTURE_NATIVE': '1'}):
            binary, command, env = launch.arguments(self.argv)
        self.assertEqual(binary, self.binary)
        self.assertEqual(command, ['capture-v1', 'work-status'])
        self.assertNotIn('AGENT_SESSIONS_LIVE', env)
        self.assertNotIn('AGENT_SESSIONS_CAPTURE_NATIVE', env)
        self.assertNotIn('R2_ACCESS_KEY_ID', env)
        with self.assertRaises(ValueError):
            launch.arguments([*self.argv[:-2], 'sync'])
        self.binary.chmod(0o777)
        with self.assertRaises(ValueError):
            launch.arguments(self.argv)

    def test_only_remote_commands_receive_named_credentials(self):
        for command in ('init', 'run', 'export', 'current-heads'):
            _, _, env = launch.arguments([*self.argv[:-1], command])
            self.assertEqual(env['R2_ACCESS_KEY_ID'], 'synthetic-key')
            self.assertEqual(env['R2_SECRET_ACCESS_KEY'], 'synthetic-secret')
        self.config.unlink()
        with patch.dict(os.environ, {'R2_ACCESS_KEY_ID': 'ambient-private', 'R2_SECRET_ACCESS_KEY': 'ambient-private'}):
            for command in ('hook', 'publication-status', 'work-status'):
                _, _, env = launch.arguments([*self.argv[:-1], command])
                self.assertNotIn('R2_ACCESS_KEY_ID', env)
                self.assertNotIn('R2_SECRET_ACCESS_KEY', env)

    def test_run_without_valid_credentials_never_inherits_ambient(self):
        for body in (None, 'R2_ACCESS_KEY_ID=$(private-value)\n'):
            if body is None:
                self.config.unlink()
            else:
                self.config.write_text(body)
                self.config.chmod(0o600)
            with patch.dict(os.environ, {'R2_ACCESS_KEY_ID': 'ambient-private', 'R2_SECRET_ACCESS_KEY': 'ambient-private'}):
                _, _, env = launch.arguments([*self.argv[:-1], 'run'])
            self.assertNotIn('R2_ACCESS_KEY_ID', env)
            self.assertNotIn('R2_SECRET_ACCESS_KEY', env)
            for command in ('init', 'export', 'current-heads'):
                with self.assertRaises((OSError, ValueError)):
                    launch.arguments([*self.argv[:-1], command])

    def test_configured_credential_references_use_only_explicit_file(self):
        import json
        runtime = self.root / 'runtime.json'
        runtime.write_text(json.dumps({'backend': {'kind': 'r2', 'access_key_env': 'ALT_ACCESS', 'secret_key_env': 'ALT_SECRET'}}))
        runtime.chmod(0o600)
        command = [*self.argv[:-1], 'run', '--config', str(runtime)]
        with patch.dict(os.environ, {'ALT_ACCESS': 'ambient-private', 'ALT_SECRET': 'ambient-private'}):
            _, _, env = launch.arguments(command)
            self.assertEqual(env.get('ALT_ACCESS'), 'synthetic-key')
            self.assertEqual(env.get('ALT_SECRET'), 'synthetic-secret')
            self.config.unlink()
            _, _, env = launch.arguments(command)
            self.assertNotIn('ALT_ACCESS', env)
            self.assertNotIn('ALT_SECRET', env)
            self.config.write_text('R2_ACCESS_KEY_ID=$(private-value)\n')
            self.config.chmod(0o600)
            _, _, env = launch.arguments(command)
            self.assertNotIn('ALT_ACCESS', env)
            self.assertNotIn('ALT_SECRET', env)

    def test_runtime_reference_projection_is_bounded_and_unambiguous(self):
        import json
        runtime = self.root / 'runtime.json'
        original = json.dumps({'backend': {'kind': 'r2', 'access_key_env': 'ALT_ACCESS', 'secret_key_env': 'ALT_SECRET'}})
        runtime.write_text(original)
        runtime.chmod(0o600)
        for flag in ('--config', '-config'):
            for flags in ([flag, str(runtime)], [flag + '=' + str(runtime)]):
                _, _, env = launch.arguments([*self.argv[:-1], 'run', *flags])
                self.assertEqual(env['ALT_ACCESS'], 'synthetic-key')
                self.assertEqual(env['ALT_SECRET'], 'synthetic-secret')
        for body in ('{', '[]', '{"backend":{},"backend":{}}',
                     '{"backend":{"kind":"r2","access_key_env":"ALT_ACCESS","secret_key_env":"ALT_ACCESS"}}',
                     '{"backend":{"kind":"r2","access_key_env":"BAD-NAME","secret_key_env":"ALT_SECRET"}}',
                     'x' * 65537):
            runtime.write_text(body)
            with self.assertRaises((OSError, ValueError)):
                launch.arguments([*self.argv[:-1], 'run', '--config', str(runtime)])
        runtime.write_text(original)
        with self.assertRaises(ValueError):
            launch.arguments([*self.argv[:-1], 'run', '--config', str(runtime), '-config=' + str(runtime)])
        runtime.chmod(0o644)
        with self.assertRaises(ValueError):
            launch.arguments([*self.argv[:-1], 'run', '--config', str(runtime)])

    def test_case_insensitive_runtime_reference_names(self):
        runtime = self.root / 'runtime.json'
        runtime.write_text('{"Backend":{"KIND":"r2","ACCESS_KEY_ENV":"ALT_ACCESS","SECRET_KEY_ENV":"ALT_SECRET"}}')
        runtime.chmod(0o600)
        with patch.dict(os.environ, {'ALT_ACCESS': 'ambient-private', 'ALT_SECRET': 'ambient-private'}):
            _, _, env = launch.arguments([*self.argv[:-1], 'run', '--config', str(runtime)])
            self.assertEqual(env.get('ALT_ACCESS'), 'synthetic-key')
            self.assertEqual(env.get('ALT_SECRET'), 'synthetic-secret')
        runtime.write_text('{"backend":{},"Backend":{}}')
        with self.assertRaises(ValueError):
            launch.arguments([*self.argv[:-1], 'run', '--config', str(runtime)])

    def test_failure_diagnostic_contains_no_values(self):
        with patch.object(launch.os, 'execve', side_effect=OSError('synthetic-secret')), patch('sys.stderr') as err:
            self.assertEqual(launch.main(self.argv), 1)
        text = ''.join(str(call) for call in err.write.call_args_list)
        self.assertNotIn('synthetic-secret', text)
        self.assertIn('private details withheld', text)


if __name__ == '__main__':
    unittest.main()
