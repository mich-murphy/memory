"""Launcher tests use fake credentials and a mocked child; never contact R2."""
import contextlib
import io
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

import run


class LauncherTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.config = Path(self.temp.name) / 'config.env'
        self.config.write_text('# synthetic only\nexport R2_ENDPOINT="https://example.invalid"\n'
                               ' R2_BUCKET = synthetic-bucket\n'
                               "R2_ACCESS_KEY_ID='synthetic-access'\n"
                               'R2_SECRET_ACCESS_KEY=synthetic-secret\n'
                               'UNRELATED_PRIVATE_SETTING=ignored\n')
        self.config.chmod(0o600)

    def test_parse_data_without_shell_expansion(self):
        self.assertEqual(run.private_values(self.config), {
            'R2_ENDPOINT': 'https://example.invalid', 'R2_BUCKET': 'synthetic-bucket',
            'R2_ACCESS_KEY_ID': 'synthetic-access', 'R2_SECRET_ACCESS_KEY': 'synthetic-secret'})
        self.config.write_text(self.config.read_text().replace('synthetic-secret', '$(touch unsafe)'))
        with self.assertRaises(ValueError):
            run.private_values(self.config)
        self.assertFalse((Path(self.temp.name) / 'unsafe').exists())

    def test_private_regular_file_required(self):
        self.config.chmod(0o644)
        with self.assertRaises(ValueError):
            run.private_values(self.config)
        self.config.chmod(0o600)
        link = Path(self.temp.name) / 'link'
        link.symlink_to(self.config)
        with self.assertRaises(OSError):
            run.private_values(link)

    def test_missing_duplicate_and_oversize_fail(self):
        for text in ('R2_BUCKET=only-one\n', self.config.read_text() + 'R2_BUCKET=duplicate\n',
                     'x' * 65537):
            self.config.write_text(text)
            with self.assertRaises(ValueError):
                run.private_values(self.config)

    def test_live_launcher_is_specific_and_no_legacy_opt_in(self):
        args = ['run.py', '--config-env', str(self.config), '--analytics-python', sys.executable,
                '--live-new-synthetic-namespace-only']
        with patch.object(sys, 'argv', args), patch.dict(os.environ, {'AGENT_SESSIONS_TEST_R2': '1', 'AGENT_SESSIONS_LIVE': '1'}), patch.object(run.subprocess, 'run', return_value=subprocess.CompletedProcess([], 0)) as child:
            self.assertEqual(run.main(), 0)
        call = child.call_args
        self.assertIn('^TestCandidateR2LiveAcceptance$', call.args[0])
        self.assertIn('-count=1', call.args[0])
        self.assertEqual(call.kwargs['env']['CANDIDATE_R2_LIVE_ACCEPTANCE'], 'new-synthetic-namespace-only')
        self.assertNotIn('AGENT_SESSIONS_TEST_R2', call.kwargs['env'])
        self.assertNotIn('AGENT_SESSIONS_LIVE', call.kwargs['env'])
        self.assertNotIn('UNRELATED_PRIVATE_SETTING', call.kwargs['env'])
        self.assertNotIn('synthetic-secret', ' '.join(call.args[0]))

    def test_preserves_virtualenv_interpreter_symlink(self):
        interpreter = Path(self.temp.name) / 'venv' / 'bin' / 'python'
        interpreter.parent.mkdir(parents=True)
        interpreter.symlink_to(sys.executable)
        args = ['run.py', '--config-env', str(self.config), '--analytics-python', str(interpreter),
                '--live-new-synthetic-namespace-only']
        with patch.object(sys, 'argv', args), patch.object(run.subprocess, 'run', return_value=subprocess.CompletedProcess([], 0)) as child:
            self.assertEqual(run.main(), 0)
        self.assertEqual(child.call_args.kwargs['env']['CANDIDATE_R2_ANALYTICS_PYTHON'], str(interpreter))
        self.assertNotEqual(interpreter, interpreter.resolve())

    def test_missing_deliberate_opt_in_never_loads_private_config(self):
        with patch.object(sys, 'argv', ['run.py', '--config-env', str(self.config), '--analytics-python', sys.executable]), patch.object(run, 'private_values') as load, contextlib.redirect_stderr(io.StringIO()):
            with self.assertRaises(SystemExit):
                run.main()
            load.assert_not_called()


if __name__ == '__main__':
    unittest.main()
