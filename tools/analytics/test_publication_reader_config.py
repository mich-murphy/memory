import unittest
from unittest import mock

import publications


class ConfiguredReaderTests(unittest.TestCase):
    def test_explicit_configuration_uses_sole_reader_without_secret_arguments(self):
        with mock.patch.object(publications.subprocess, 'run') as run:
            publications.export_session('/binary', None, 'archive', 'machine', 'claude', 'session',
                                        '/output', config='/private/runtime.json')
        argv = run.call_args.args[0]
        self.assertEqual(argv[:3], ['/binary', 'capture-v1', 'export'])
        self.assertIn('--config', argv)
        self.assertNotIn('--store', argv)
        self.assertNotIn('--secret-key', argv)
        self.assertNotIn('shell', run.call_args.kwargs)
        self.assertEqual(run.call_args.kwargs['timeout'], 300)

    def test_conflicting_or_absent_destination_never_invokes_reader(self):
        for store, config in ((None, None), ('/store', '/config')):
            with mock.patch.object(publications.subprocess, 'run') as run:
                with self.assertRaisesRegex(publications.ImportFailure, '^reader_destination_required$'):
                    publications.export_session('/binary', store, 'a', 'm', 'pi', 's', '/output', config=config)
                run.assert_not_called()
