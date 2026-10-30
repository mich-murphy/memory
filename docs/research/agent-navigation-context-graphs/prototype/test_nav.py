"""Synthetic-only regression tests; never read experiment tasks/judgments."""
import json
from contextlib import closing
import os
from pathlib import Path
import sqlite3
import subprocess
import tempfile
import unittest

import nav


class NavigationTest(unittest.TestCase):
    def setUp(self):
        nav.CACHE.mkdir(exist_ok=True)
        self.tmp = tempfile.TemporaryDirectory(prefix='test-', dir=nav.CACHE)
        self.base = Path(self.tmp.name)
        self.root = self.base / 'repo'
        self.root.mkdir()
        self.db = self.base / 'index.sqlite3'
        self.routes = self.base / 'routing.json'
        self.routes.write_text(json.dumps({'routes': [
            {'id': 'ui', 'terms': 'screen interface widgets', 'prefixes': ['src'],
             'docs': ['docs/start.md'], 'source': 'docs/repository/README.md:13'}]}))
        self.put('src/main.ts', "import { beta } from './beta';\nexport const alpha = beta;\n")
        self.put('src/beta.ts', 'export const beta = 42;\n')
        self.put('src/ambiguous.ts', "import x from './choice';\n")
        self.put('src/choice.ts', 'export default 1;\n')
        self.put('src/choice.tsx', 'export default 2;\n')
        self.put('docs/start.md', '# Alpha start\n[main](../src/main.ts)\n[dead](missing.md)\n[escape](../../outside.md)\n[remote](https://example.com/x)\n')
        self.put('packages/a/package.json', '{"name":"@demo/a", "dependencies":{"@demo/b":"workspace:*"}}')
        self.put('packages/b/package.json', '{"name":"@demo/b"}')
        self.put('.github/CODEOWNERS', '/src/ @general\n/src/beta.ts @specific\n/src/choice.ts\n')
        self.put('AGENTS.md', 'forbiddenagentsunique')
        self.put('disabled/SKILL.md.disabled', 'forbiddenskillunique')
        self.put('benchmark/tasks.json', '{"heldout":"neverread"}')
        self.put('foo/tasks.json', '{"heldout":"neverread"}')
        self.put('node_modules/secret.ts', 'dependencyunique')
        self.put('generated/large.ts', 'generatedunique')
        self.put('data.dump', 'dumpunique')
        self.git('init', '-q')
        self.git('add', '.')
        self.git('-c', 'user.name=Test', '-c', 'user.email=test@example.invalid', 'commit', '-qm', 'fixture')
        nav.build(self.root, self.db, self.routes)

    def tearDown(self):
        self.tmp.cleanup()

    def git(self, *args):
        return subprocess.run(['git', '-C', str(self.root), *args], check=True, capture_output=True)

    def put(self, name, content):
        p = self.root / name
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(content)

    def query(self, text='alpha', mode='hybrid', limit=10, depth=1, scope=None):
        return nav.query_db(self.root, self.db, self.routes, text, mode, limit, depth, 12, scope)

    def sql(self, sql, args=()):
        with closing(sqlite3.connect(self.db)) as db:
            return db.execute(sql, args).fetchall()

    def test_fts5_and_no_results(self):
        self.assertTrue(self.query('alpha', 'lexical')['results'])
        self.assertEqual(self.query('unfindablezzzz', 'lexical')['results'], [])
        self.assertEqual(self.query('!!!', 'hybrid')['results'], [])

    def test_stale_content(self):
        self.put('src/main.ts', 'changed')
        with self.assertRaisesRegex(nav.NavError, 'STALE_INDEX'):
            self.query()

    def test_stale_added_tracked_file(self):
        self.put('src/new.ts', 'new')
        self.git('add', 'src/new.ts')
        with self.assertRaisesRegex(nav.NavError, 'STALE_INDEX'):
            self.query()

    def test_untracked_files_not_corpus(self):
        self.put('src/new.ts', 'new')
        self.assertEqual(self.query('unfindablezzzz', 'lexical')['results'], [])

    def test_stale_deleted_file(self):
        (self.root / 'src/beta.ts').unlink()
        with self.assertRaisesRegex(nav.NavError, 'STALE_INDEX'):
            self.query()

    def test_stale_head(self):
        self.git('-c', 'user.name=Test', '-c', 'user.email=test@example.invalid', 'commit', '--allow-empty', '-qm', 'next')
        with self.assertRaisesRegex(nav.NavError, 'head'):
            self.query()

    def test_stale_other_worktree(self):
        other = self.base / 'other'
        self.git('worktree', 'add', '--detach', str(other), 'HEAD')
        with self.assertRaisesRegex(nav.NavError, 'root'):
            nav.query_db(other, self.db, self.routes, 'alpha', 'lexical', 10, 1, 12, None)

    def test_dead_links_and_escape(self):
        rows = self.sql("SELECT dst FROM edges WHERE kind='markdown_link'")
        self.assertEqual(rows, [('src/main.ts',)])
        self.assertEqual(self.sql('SELECT target FROM issues'), [('docs/missing.md',)])
        self.assertIsNone(nav.relative_target('docs/start.md', '../../outside.md'))
        self.assertIsNone(nav.relative_target('docs/start.md', '%2fetc/passwd'))
        self.assertIsNone(nav.relative_target('docs/start.md', '..\\secret.md'))

    def test_symlink_and_exclusions(self):
        outside = self.base / 'outside.md'
        outside.write_text('outsideunique')
        os.symlink(outside, self.root / 'src/link.md')
        os.symlink(self.base, self.root / 'linked')
        self.git('add', 'src/link.md', 'linked')
        files = nav.inventory(self.root)
        for path in ['src/link.md', 'linked', 'AGENTS.md', 'disabled/SKILL.md.disabled',
                     'benchmark/tasks.json', 'foo/tasks.json', 'node_modules/secret.ts',
                     'generated/large.ts', 'data.dump']:
            self.assertNotIn(path, files)
        # Disallowed contents not opened at all, not merely removed after ingest.
        from unittest.mock import patch
        original = Path.read_bytes
        def guarded(p):
            if p.name.lower() in ('agents.md', 'skill.md.disabled', 'tasks.json'):
                self.fail('read forbidden content: ' + str(p))
            return original(p)
        with patch.object(Path, 'read_bytes', guarded):
            nav.inventory(self.root)

    def test_import_confidence_package_dependency(self):
        rows = self.sql("SELECT src,dst FROM edges WHERE kind='ts_import'")
        self.assertIn(('src/main.ts', 'src/beta.ts'), rows)
        self.assertFalse(any(src == 'src/ambiguous.ts' for src, _ in rows))
        self.assertEqual(self.sql("SELECT src,dst FROM edges WHERE kind='package_dependency'"),
                         [('packages/a/package.json', 'packages/b/package.json')])

    def test_owner_last_match_and_no_traversal(self):
        rows = dict(self.sql('SELECT path,owners FROM owners'))
        self.assertEqual(json.loads(rows['src/beta.ts']), ['@specific'])
        self.assertEqual(json.loads(rows['src/main.ts']), ['@general'])
        self.assertEqual(json.loads(rows['src/choice.ts']), [])
        self.assertNotIn('docs/start.md', rows)
        self.assertTrue(nav.owner_match('infra/a/b.tf', '/infra/'))
        self.assertFalse(nav.owner_match('x/infra/a.tf', '/infra/'))
        self.assertFalse(nav.owner_match('a/b/c.ts', '/a/*.ts'))
        self.assertTrue(nav.owner_match('a/b/c.ts', '/a/**/*.ts'))
        for r in self.query('alpha', 'graph', depth=2)['results']:
            self.assertFalse(r['path'].startswith('owner:'))
            self.assertTrue(all(x['type'] != 'owned_by' for x in r['reasons']))

    def test_modes_seed_stability_and_scope(self):
        lexical = self.query('alpha', 'lexical')['results']
        graph = self.query('alpha', 'graph')['results']
        self.assertEqual(lexical[0]['path'], graph[0]['path'])
        self.assertTrue(any(x['type'] == 'markdown_link' for r in graph for x in r['reasons']))
        self.assertTrue(self.query('widgets', 'map-only')['results'])
        self.assertTrue(self.query('widgets', 'map')['results'])
        self.assertEqual(self.query('widgets', 'graph')['results'], [])
        scoped = self.query('alpha', 'hybrid', scope='src')['results']
        self.assertTrue(all(r['path'] == 'src' or r['path'].startswith('src/') for r in scoped))

    def test_context_limit_and_result_limit(self):
        result = self.query('alpha', limit=2)
        self.assertLessEqual(len(result['results']), 2)
        encoded = nav.bounded_output(result, 1200)
        self.assertLessEqual(len(encoded.encode()) + 1, 1200)
        parsed = json.loads(encoded)
        self.assertEqual(parsed['output_bytes'], len(encoded.encode()) + 1)
        with self.assertRaisesRegex(nav.NavError, 'CONTEXT_LIMIT'):
            nav.bounded_output(self.query('alpha'), 10)

    def test_source_refs_mentions_and_stopwords(self):
        text = ('---\nsourceRefs:\n  - path: src/main.ts\n  - path: "src/beta.ts"\n'
                '  - path: ../../escape.ts\n  - path: |\n  - path: src/missing.ts\n'
                '---\nSee `src/choice.ts` and src/missing.ts, not proof.\n'
                '```\nsrc/ambiguous.ts\n```\n')
        nodes = {'src/main.ts', 'src/beta.ts', 'src/choice.ts', 'src/ambiguous.ts'}
        relations, issues = nav.source_relations(text, nodes)
        self.assertIn(('src/main.ts', 3, 'source_reference'), relations)
        self.assertIn(('src/beta.ts', 4, 'source_reference'), relations)
        self.assertIn(('src/choice.ts', 9, 'path_mention'), relations)
        self.assertFalse(any(dst == 'src/ambiguous.ts' for dst, _, _ in relations))
        self.assertEqual(len(issues), 3)
        inline, diag = nav.source_relations('---\nsourceRefs: [unsupported]\n---\n', nodes)
        self.assertEqual(inline, [])
        self.assertEqual(diag[0][2], 'unsupported-source-ref-scalar')
        self.assertEqual(nav.tokens('Where can I find the alpha and the beta?'), ['alpha', 'beta'])
        self.assertEqual(len(nav.tokens(' '.join('term' + str(i) for i in range(40)))), 32)
        self.assertTrue(nav.eligible('classic/src/dbl/example.dbx'))
        for path in ['src/lock.rs', 'src/clock.ts', 'src/agents.ts', 'docs/skills.md']:
            self.assertTrue(nav.eligible(path), path)
        for path in ['AGENTS.md', 'CLAUDE.md', 'SKILL.md', 'SKILL.md.disabled',
                     'SKILL.disabled.md', 'bun.lock', 'package-lock.json', 'lock.yaml', 'Cargo.lock']:
            self.assertFalse(nav.eligible(path), path)

    def test_stale_routes_and_route_escape(self):
        self.routes.write_text('{"routes":[]}')
        with self.assertRaisesRegex(nav.NavError, 'routing'):
            self.query()
        self.routes.write_text(json.dumps({'routes': [{'prefixes': ['../../outside'], 'docs': []}]}))
        with self.assertRaisesRegex(nav.NavError, 'unsafe route'):
            nav.load_routes(self.routes)

    def test_db_escape_and_cli(self):
        with self.assertRaisesRegex(nav.NavError, '--db must be inside'):
            nav.db_path(self.root, '/tmp/outside.sqlite3')
        proc = subprocess.run(['python3', str(nav.HERE / 'nav.py'), '--root', str(self.root),
                               '--db', str(self.db), '--routes', str(self.routes),
                               'query', 'alpha', '--scope', '../escape'], capture_output=True, text=True)
        self.assertEqual(proc.returncode, 2)
        self.assertIn('unsafe scope', json.loads(proc.stdout)['error'])

if __name__ == '__main__':
    unittest.main()
