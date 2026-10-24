"""Refresh scheduling/status/security tests; actual reader data lives in journey tests."""
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import publication_refresh as refresh


class RefreshTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.database = self.root / 'analytics.duckdb'
        self.status = refresh.open_status(self.root / 'status.sqlite')
        self.addCleanup(self.status.close)
        self.endpoints = [dict(name=name, binary='/bin/reader', config='/private/' + name)
                          for name in ('Mac', 'BC01')]
        self.heads = {name: [self.head(name, session) for session in ('a', 'b')]
                      for name in ('Mac', 'BC01')}
        self.down = set()
        self.failed = set()
        self.imports = []
        self.advance = False
        self.wrong_identity = False

    def head(self, machine, session, seq=1):
        return {'identity': {'archive_id': 'archive', 'machine_id': machine,
                            'agent': 'claude', 'session_id': session},
                'sequence': seq, 'publication_id': 'pub-' + str(seq)}

    def transport(self, command, root, **kwargs):
        endpoint = next(e for e in self.endpoints if e['config'] in command)
        if endpoint['name'] in self.down:
            raise refresh.RefreshFailure('process_failed')
        cursor = command[command.index('--after-session') + 1]
        # Exercise multiple bounded pages, not merely one mock full response.
        heads = self.heads[endpoint['name']]
        selected = [h for h in heads if h['identity']['session_id'] > cursor][:1]
        complete = not selected or selected == heads[-1:]
        return json.dumps({'contract': 'session-publication-heads/v1', 'archive_id': 'archive',
                           'heads': selected, 'next_cursor': selected[-1]['identity']['session_id'] if selected else cursor,
                           'complete': complete}).encode()

    def exporter(self, endpoint, identity, output, *args):
        if (identity['machine_id'], identity['session_id']) in self.failed:
            raise refresh.RefreshFailure('reader_export_failed')
        output.mkdir()
        head = next(h for h in self.heads[endpoint['name']] if h['identity'] == identity)
        manifest = dict(head)
        manifest['identity'] = dict(identity)
        if self.advance:
            manifest['sequence'] += 1
            manifest['publication_id'] = 'advanced'
        if self.wrong_identity:
            manifest['identity']['session_id'] = 'other'
        (output / 'unit-coordinate.json').write_text(json.dumps(manifest))

    def loader(self, output):
        return json.loads((output / 'unit-coordinate.json').read_text()), {}, {}

    def importer(self, output, database):
        self.imports.append(self.loader(output)[0])

    def sweep(self):
        with patch.object(refresh, 'run', side_effect=self.transport), \
             patch.object(refresh, 'export', side_effect=self.exporter), \
             patch.object(refresh.publications, '_load', side_effect=self.loader), \
             patch.object(refresh.publications, 'import_export', side_effect=self.importer), \
             patch.object(refresh, '_imported_time', return_value=123):
            return refresh.sweep(self.endpoints, self.database, self.status)

    def test_two_endpoints_sessions_restart_full_sweeps_and_rebuilt_destination(self):
        result = self.sweep()
        self.assertEqual((result['expected_targets'], result['unimported']), (4, 0))
        self.assertEqual(len(self.imports), 4)
        self.status.close()
        self.status = refresh.open_status(self.root / 'status.sqlite')
        self.addCleanup(self.status.close)
        self.sweep()
        self.assertEqual(len(self.imports), 8, 'revalidate actual destination after restart/rebuild')
        self.assertEqual(self.status.execute('SELECT count(*) FROM targets').fetchone(), (4,))
        # Lexically earlier new sessions must be found by the next sweep.
        self.heads['Mac'].insert(0, self.head('Mac', '0'))
        self.assertEqual(self.sweep()['expected_targets'], 5)

    def test_healthy_actual_projection_skips_reader_without_advancing_freshness(self):
        self.sweep()
        self.imports.clear()
        before = self.status.execute('SELECT imported_ns FROM targets ORDER BY endpoint,identity').fetchall()
        def healthy(database, head, prior):
            timestamp = self.status.execute('SELECT imported_ns FROM targets WHERE endpoint=? AND identity=?',
                (head['identity']['machine_id'], json.dumps(head['identity'], sort_keys=True))).fetchone()[0]
            return prior[0], prior[1], timestamp
        with patch.object(refresh, '_projection_coordinate', side_effect=healthy), \
             patch.object(refresh, 'export', side_effect=AssertionError('unchanged export')):
            self.sweep()
        self.assertEqual(self.imports, [])
        self.assertEqual(before, self.status.execute('SELECT imported_ns FROM targets ORDER BY endpoint,identity').fetchall())

    def test_outage_is_not_successful_empty_and_other_endpoint_progresses(self):
        self.down.add('Mac')
        result = self.sweep()
        self.assertEqual(result['endpoint_errors'], 1)
        self.assertEqual(len(self.imports), 2)
        self.assertIsNone(self.status.execute('SELECT completed_ns FROM endpoints WHERE name=?', ('Mac',)).fetchone()[0])
        self.down.clear()
        self.assertEqual(self.sweep()['endpoint_errors'], 0)
        self.heads['Mac'] = []
        result = self.sweep()
        self.assertEqual(result['endpoint_errors'], 0)
        self.assertEqual(result['expected_targets'], 4, 'past expectations survive empty enumerations')

    def test_failure_isolation_prior_success_and_retry(self):
        self.sweep()
        self.heads['Mac'][0] = self.head('Mac', 'a', 2)
        self.failed.add(('Mac', 'a'))
        result = self.sweep()
        self.assertEqual((result['unimported'], result['target_errors']), (1, 1))
        self.assertEqual(self.status.execute('SELECT imported_sequence FROM targets WHERE endpoint=? ORDER BY identity', ('Mac',)).fetchone(), (1,))
        self.failed.clear()
        self.assertEqual(self.sweep()['unimported'], 0)

    def test_advance_race_imports_actual_coordinate_identity_conflict_and_regression(self):
        self.advance = True
        self.sweep()
        self.assertEqual(set(r[0] for r in self.status.execute('SELECT imported_sequence FROM targets')), {2})
        self.assertEqual({m['publication_id'] for m in self.imports}, {'advanced'})
        self.advance = False
        self.wrong_identity = True
        self.assertEqual(self.sweep()['target_errors'], 4)
        self.wrong_identity = False
        self.heads['Mac'][0]['publication_id'] = 'conflict'
        self.assertEqual(self.sweep()['target_errors'], 1)
        self.heads['Mac'][0] = self.head('Mac', 'a', 2)
        self.sweep()
        self.heads['Mac'][0] = self.head('Mac', 'a', 1)
        self.assertEqual(self.sweep()['target_errors'], 1)

    def test_unknown_contract_cursor_loop_and_destination_errors_visible(self):
        with patch.object(refresh, 'run', return_value=b'{"contract":"unknown"}'):
            self.assertEqual(refresh.sweep(self.endpoints, self.database, self.status)['endpoint_errors'], 2)
        with patch.object(refresh, 'run', side_effect=self.transport), \
             patch.object(refresh, 'export', side_effect=self.exporter), \
             patch.object(refresh.publications, '_load', side_effect=self.loader), \
             patch.object(refresh.publications, 'import_export', side_effect=OSError('private data')):
            result = refresh.sweep(self.endpoints, self.database, self.status)
        self.assertEqual((result['target_errors'], result['unimported']), (4, 4))
        self.assertEqual(set(row[0] for row in self.status.execute('SELECT error FROM targets')), {'import_failed'})
        with patch.object(refresh, 'run', side_effect=self.transport), \
             patch.object(refresh, 'export', side_effect=refresh.RefreshFailure('reader_export_failed')):
            self.assertEqual(refresh.sweep(self.endpoints, self.database, self.status, max_pages=1)['endpoint_errors'], 2)

    def test_endpoint_config_is_private_explicit_and_remote_args_quoted(self):
        path = self.root / 'endpoints.json'
        path.write_text(json.dumps({'contract': 'session-refresh-endpoints/v1', 'endpoints': self.endpoints}))
        path.chmod(0o600)
        self.assertEqual(refresh.load_endpoints(path), self.endpoints)
        alias = self.root / 'endpoint-link.json'
        alias.symlink_to(path)
        with self.assertRaises(OSError):
            refresh.load_endpoints(alias)
        path.chmod(0o644)
        with self.assertRaises(refresh.RefreshFailure):
            refresh.load_endpoints(path)
        endpoint = dict(self.endpoints[0], ssh_host='BC-01', binary='/opt/a b/reader')
        command = refresh.command(endpoint, ['export', '--session', 'a; touch /tmp/no'])
        self.assertEqual(command[-1], "'/opt/a b/reader' capture-v1 export --session 'a; touch /tmp/no'")
        endpoint['ssh_host'] = '-oProxyCommand=bad'
        path.chmod(0o600)
        path.write_text(json.dumps({'contract': 'session-refresh-endpoints/v1', 'endpoints': [endpoint]}))
        with self.assertRaises(refresh.RefreshFailure):
            refresh.load_endpoints(path)

    def test_discovery_command_selection_preserves_local_export_adapter(self):
        for mode, subcommand in ((None, 'publication-status'), ('queue', 'publication-status'),
                                 ('remote-current', 'current-heads')):
            with self.subTest(mode=mode):
                endpoint = dict(self.endpoints[0])
                if mode is not None:
                    endpoint['discovery'] = mode
                page = json.dumps(dict(contract='session-publication-heads/v1', archive_id='archive',
                    heads=[], next_cursor='', complete=True)).encode()
                with patch.object(refresh, 'run', return_value=page) as run:
                    result = refresh.sweep([endpoint], self.database, self.status)
                self.assertEqual(result['incomplete_endpoints'], 0)
                run.assert_called_once()
                self.assertEqual(run.call_args.args[0], ['/bin/reader', 'capture-v1', subcommand,
                    '--config', endpoint['config'], '--after-session', '', '--limit', '256'])
                identity = self.head('Mac', 'a')['identity']
                output = self.root / 'export'
                with patch.object(refresh.publications, 'export_session') as exporter:
                    refresh._local_export(endpoint, identity, output)
                exporter.assert_called_once_with(endpoint['binary'], None,
                    'archive', 'Mac', 'claude', 'a', output, config=endpoint['config'])

    def test_discovery_validation(self):
        path = self.root / 'endpoints.json'
        for mode in ('queue', 'remote-current', 'unknown', '', None, 1, [], {}):
            with self.subTest(mode=mode):
                endpoint = dict(self.endpoints[0], discovery=mode)
                path.write_text(json.dumps(dict(contract='session-refresh-endpoints/v1', endpoints=[endpoint])))
                path.chmod(0o600)
                if mode in ('queue', 'remote-current'):
                    self.assertEqual(refresh.load_endpoints(path), [endpoint])
                else:
                    with self.assertRaisesRegex(refresh.RefreshFailure, 'endpoint_discovery'):
                        refresh.load_endpoints(path)

    def test_discovery_change_invalidates_durable_completion_before_enumeration(self):
        self.assertEqual(self.sweep()['incomplete_endpoints'], 0)
        before = self.status.execute('SELECT configuration_id FROM binding').fetchone()
        targets = self.status.execute('SELECT * FROM targets ORDER BY endpoint,identity').fetchall()
        self.endpoints[0]['discovery'] = 'remote-current'
        class Crash(BaseException):
            pass
        with patch.object(refresh, 'run', side_effect=Crash):
            with self.assertRaises(Crash):
                refresh.sweep(self.endpoints, self.database, self.status)
        self.status.close()
        self.status = refresh.open_status(self.root / 'status.sqlite')
        self.addCleanup(self.status.close)
        self.assertNotEqual(before, self.status.execute('SELECT configuration_id FROM binding').fetchone())
        self.assertEqual(refresh.report(self.status)['incomplete_endpoints'], 2)
        self.assertEqual(targets, self.status.execute('SELECT * FROM targets ORDER BY endpoint,identity').fetchall())
        self.assertEqual(self.sweep()['incomplete_endpoints'], 0)

    def test_crash_after_binding_preserves_unattempted_expected_scope(self):
        class Crash(BaseException):
            pass
        with patch.object(refresh, 'run', side_effect=Crash):
            with self.assertRaises(Crash):
                refresh.sweep(self.endpoints, self.database, self.status)
        self.status.close()
        self.status = refresh.open_status(self.root / 'status.sqlite')
        self.addCleanup(self.status.close)
        self.assertEqual(self.status.execute('SELECT name FROM expected_endpoints WHERE active=1 ORDER BY name').fetchall(),
                         [('BC01',), ('Mac',)])
        self.assertEqual(self.status.execute('SELECT completed_ns FROM endpoints').fetchall(), [(None,), (None,)])
        self.assertEqual(self.status.execute('SELECT attempted_ns FROM endpoints WHERE name=?', ('BC01',)).fetchone(), (None,))
        self.assertEqual(refresh.report(self.status)['incomplete_endpoints'], 2)
        self.assertEqual(self.sweep()['incomplete_endpoints'], 0)

    def test_old_status_version_is_refused_without_reset(self):
        import sqlite3
        import contextlib
        for version in (1, 2):
            with self.subTest(version=version):
                path = self.root / ('old-status-' + str(version) + '.sqlite')
                with contextlib.closing(sqlite3.connect(path)) as old:
                    old.execute('CREATE TABLE binding(version INTEGER)')
                    old.execute('INSERT INTO binding VALUES (?)', (version,))
                    old.commit()
                with self.assertRaisesRegex(refresh.RefreshFailure, 'status_contract'):
                    refresh.open_status(path)
                with contextlib.closing(sqlite3.connect(path)) as old:
                    self.assertEqual(old.execute('SELECT version FROM binding').fetchall(), [(version,)])
                    self.assertEqual(old.execute("SELECT name FROM sqlite_master WHERE type='table'").fetchall(), [('binding',)])

    def test_process_output_bytes_and_timeout_bounded(self):
        import sys
        with self.assertRaisesRegex(refresh.RefreshFailure, 'process_output_limit'):
            refresh.run([sys.executable, '-c', 'print("x"*4096)'], self.root, output_limit=16)
        with self.assertRaisesRegex(refresh.RefreshFailure, 'process_timeout'):
            refresh.run([sys.executable, '-c', 'import time; time.sleep(10)'], self.root, timeout=.05)
        (self.root / 'too-large').write_bytes(b'x' * 4096)
        with self.assertRaisesRegex(refresh.RefreshFailure, 'export_byte_limit'):
            refresh.disk_bytes(self.root, 1024)


class DiscoveryFailureConsumerTests(unittest.TestCase):
    """Mock only transport/export: import, body ownership, status and health are real."""
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.database = self.root / 'projection.duckdb'
        self.status_path = self.root / 'status.sqlite'
        self.status = refresh.open_status(self.status_path)
        self.addCleanup(lambda: self.status.close())
        self.endpoints = [dict(name=name, binary='/reader-wrapper', config='/private/' + name,
                               discovery='remote-current') for name in ('Mac', 'BC01')]
        self.a = self.head('Mac', 'claude', 'a')
        self.b = self.head('Mac', 'claude', 'b')
        self.c = self.head('Mac', 'pi', 'c')
        self.d = self.head('BC01', 'codex', 'd')

    def head(self, machine, agent, session):
        return dict(identity=dict(archive_id='archive', machine_id=machine,
                    agent=agent, session_id=session), sequence=1, publication_id='pub-' + session)

    def fixture_export(self, endpoint, identity, output, *args):
        import hashlib
        output.mkdir(mode=0o700)
        raw = b'{"type":"user","uuid":"synthetic","message":{"role":"user","content":"hello"}}\n'
        digest = hashlib.sha256(raw).hexdigest()
        body = output / 'body.jsonl'
        body.write_bytes(raw)
        body.chmod(0o600)
        manifest = dict(contract='session-publication/v2', identity=identity, sequence=1,
            publication_id='pub-' + identity['session_id'], observed_at_ns=1, captured_at_ns=2,
            published_at_ns=3, coverage=dict(discovery='complete', enrichment='partial', missing=['launch']),
            sources=[dict(source_id='main', epoch_id='epoch', role='main', path='/NEVER-READ',
                state='present', revision=1, observed_at_ns=1, captured_at_ns=2, associations=[],
                object=dict(key='immutable/object', encoding='zstd', sha256=digest,
                    encoded_sha256=digest, size=len(raw), encoded_size=len(raw)))])
        (output / 'metadata.json').write_text(json.dumps(dict(contract='session-reader/v2',
            manifest=manifest, bodies=[dict(source_id='main', epoch_id='epoch', path='body.jsonl',
                                          sha256=digest, size=len(raw))])))

    def page(self, heads=(), failures=(), *, complete=True, cursor=''):
        return dict(contract='session-publication-heads/v1', archive_id='archive', heads=list(heads),
                    failures=list(failures), complete=complete, next_cursor=cursor)

    def sweep(self, pages):
        with patch.object(refresh, 'run', side_effect=[json.dumps(p).encode() for p in pages]), \
             patch.object(refresh, 'export', side_effect=self.fixture_export):
            return refresh.sweep(self.endpoints, self.database, self.status)

    def test_failed_a_durable_healthy_siblings_import_and_automatic_recovery(self):
        import publications
        from publication_dashboard import PublicationDashboard
        failure = dict(identity=self.a['identity'], reason='current-unreadable')
        failed_pages = [self.page([self.b], [failure], complete=False, cursor='next'),
                        self.page([self.c]), self.page([self.d])]
        result = self.sweep(failed_pages)
        self.assertEqual((result['expected_targets'], result['discovery_errors'], result['unimported']), (4, 1, 0))
        self.assertEqual(result['endpoint_errors'], 0)
        self.assertEqual(self.status.execute('SELECT count(*) FROM targets').fetchone(), (3,))
        first = publications.report(self.database)
        self.assertEqual(len(first['sessions']), 3, 'B and later page/endpoint really imported')
        with refresh.projection_access.connect(self.database) as projection:
            self.assertEqual(projection.execute("SELECT count(*) FROM events WHERE role='user'").fetchone()[0], 3)
        adapter = PublicationDashboard(self.database, self.status_path)
        self.assertFalse(adapter.refresh_health()['healthy'])
        self.assertEqual(adapter.refresh_health()['discovery_errors'], 1)
        original = self.status.execute('SELECT attempts,first_observed_ns,last_observed_ns FROM head_failures').fetchone()
        self.status.close()
        self.status = refresh.open_status(self.status_path)
        self.sweep(failed_pages)
        repeated = self.status.execute('SELECT attempts,first_observed_ns,last_observed_ns FROM head_failures').fetchone()
        self.assertEqual(repeated[:2], (2, original[1]))
        self.assertGreaterEqual(repeated[2], original[2])
        self.assertEqual([s['imported_at_ns'] for s in first['sessions']],
                         [s['imported_at_ns'] for s in publications.report(self.database)['sessions']])
        # Even a fresh successful empty sweep cannot erase unreadable A.
        self.assertEqual(self.sweep([self.page(), self.page()])['discovery_errors'], 1)
        self.assertFalse(adapter.refresh_health()['healthy'])
        recovered = [self.page([self.a, self.b, self.c]), self.page([self.d])]
        self.assertEqual(self.sweep(recovered)['discovery_errors'], 0)
        self.assertTrue(adapter.refresh_health()['healthy'])
        imported = publications.report(self.database)
        self.assertEqual(len(imported['sessions']), 4)
        self.sweep(recovered)
        self.assertEqual([s['imported_at_ns'] for s in imported['sessions']],
                         [s['imported_at_ns'] for s in publications.report(self.database)['sessions']])
        # An unreadable update retains A's previous valid projection/target, not a fake seq 0.
        result = self.sweep(failed_pages)
        self.assertEqual((result['expected_targets'], result['discovery_errors']), (4, 1))
        self.assertEqual(self.status.execute('SELECT count(*) FROM targets WHERE sequence=1 AND imported_sequence=1').fetchone(), (4,))
        self.assertEqual(len(publications.report(self.database)['sessions']), 4)
        self.assertFalse(adapter.refresh_health()['healthy'])
        self.sweep(recovered)
        self.assertTrue(adapter.refresh_health()['healthy'])
        # Omitted endpoints do not supply coverage or active discovery failures.
        self.sweep(failed_pages)
        refresh.bind_endpoints(self.status, self.endpoints[1:])
        self.assertEqual(refresh.report(self.status)['discovery_errors'], 0)
        self.assertEqual(refresh.report(self.status)['omitted_endpoints'], 1)

    def test_projection_repairs_do_not_hide_discovery_failure(self):
        import shutil
        import publications
        pages = [self.page([self.b], [dict(identity=self.a['identity'], reason='current-unreadable')]),
                 self.page([self.d])]
        self.assertEqual(self.sweep(pages)['discovery_errors'], 1)
        with refresh.projection_access.connect(self.database) as projection:
            body = Path(projection.execute('SELECT path FROM sources LIMIT 1').fetchone()[0])
        original = body.read_bytes()
        body.write_bytes(b'x' * len(original))
        self.assertEqual(self.sweep(pages)['discovery_errors'], 1)
        self.assertEqual(body.read_bytes(), original)
        body.unlink()
        self.assertEqual(self.sweep(pages)['discovery_errors'], 1)
        self.assertEqual(body.read_bytes(), original)
        self.database.unlink()
        shutil.rmtree(self.database.with_name(self.database.name + '.bodies'))
        result = self.sweep(pages)
        self.assertEqual((result['expected_targets'], result['unimported'], result['discovery_errors']), (3, 0, 1))
        self.assertEqual(len(publications.report(self.database)['sessions']), 2)

    def test_failure_validation_is_bounded_typed_and_duplicate_safe(self):
        import copy
        failure = dict(identity=self.a['identity'], reason='current-unreadable')
        invalid = []
        for field, value in (('archive_id', 'other'), ('machine_id', ''), ('agent', 'unknown'),
                             ('session_id', 'bad\x00id')):
            item = copy.deepcopy(failure)
            item['identity'][field] = value
            invalid.append(self.page(failures=[item]))
        for value in ('private exception text', None, ['current-unreadable']):
            invalid.append(self.page(failures=[dict(failure, reason=value)]))
        invalid += [dict(self.page(), failures=None),
                    self.page(failures=[dict(failure, extra='untrusted')]),
                    self.page([self.a], [failure]), self.page(failures=[failure, failure]),
                    self.page([self.b] * 256, [failure])]
        for page in invalid:
            with self.subTest(page=page):
                with patch.object(refresh, 'run', return_value=json.dumps(page).encode()):
                    result = refresh.sweep(self.endpoints, self.database, self.status)
                self.assertEqual(result['endpoint_errors'], 2)
                self.assertEqual(self.status.execute('SELECT count(*) FROM head_failures').fetchone(), (0,))
                self.assertEqual(self.status.execute('SELECT count(*) FROM targets').fetchone(), (0,))
        # Cross-page duplicate failure/head identities are also rejected.
        pages = [self.page(failures=[failure], complete=False, cursor='next'),
                 self.page([self.a]), self.page()]
        result = self.sweep(pages)
        self.assertEqual(result['endpoint_errors'], 1)
        self.assertEqual(result['discovery_errors'], 1)
        self.assertEqual(self.status.execute('SELECT reason FROM head_failures').fetchall(), [('current-unreadable',)])


if __name__ == '__main__':
    unittest.main()
