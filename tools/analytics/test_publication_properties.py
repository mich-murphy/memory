"""Seeded properties (A1-A8) for the DuckDB publication consumer (GAP-07, GAP-08).

Model is computed from raw body bytes with stdlib json only (never importer output).
Replay one seed: PUB_SEED=<n>.  Seed count: PUB_SEEDS (default 40).
Time-valued columns excluded from dumps: sessions.imported_at_ns / normalized_at_ns
(time.time_ns() at import) and failures.failed_at_ns.  sources.path is reduced to its
basename (it embeds the per-database owned-body directory).
"""
import contextlib
import hashlib
import json
import os
import random
import subprocess
import tempfile
import unittest
from pathlib import Path

import duckdb
import publications

SEEDS = [int(os.environ['PUB_SEED'])] if os.environ.get('PUB_SEED') else list(range(int(os.environ.get('PUB_SEEDS', '40'))))
TIME_COLUMNS = {'sessions': {'imported_at_ns', 'normalized_at_ns'}, 'failures': {'failed_at_ns'}}
TABLES = ('publication_binding', 'sessions', 'sources', 'events', 'issues', 'failures')
WORDS = ['alpha', 'béta', 'γάμμα', '日本語', 'delta 🌏', 'eps']
EV = 'line,"offset",length,native_id,role,text,duplicate'


def sha(b):
    return hashlib.sha256(b).hexdigest()


def dump(db_path, skip=(), tables=TABLES):
    out = {}
    with duckdb.connect(str(db_path), read_only=True) as db:
        for t in tables:
            cols = [r[1] for r in db.execute(f"PRAGMA table_info('{t}')").fetchall()]
            cols = [c for c in cols if c not in TIME_COLUMNS.get(t, ()) and c not in skip]
            rows = db.execute('SELECT ' + ','.join(f'"{c}"' for c in cols) + f' FROM {t} ORDER BY ALL').fetchall()
            if t == 'sources':
                i = cols.index('path')
                rows = [r[:i] + (Path(r[i]).name if r[i] else None,) + r[i + 1:] for r in rows]
            out[t] = (cols, [tuple(map(str, r)) for r in rows])
    return out


def gen_line(rng, uuids):
    if rng.random() < .12:
        return rng.choice([b'not-json\n', b'{"type":\n', b'\n', b'[1,2]\n', b'"str"\n'])
    role = rng.choice(['user', 'assistant'])
    rec = {'type': role, 'message': {'role': role, 'content': ' '.join(rng.choices(WORDS, k=rng.randint(1, 4)))}}
    if rng.random() < .85:
        rec['uuid'] = rng.choice(uuids)
    return (json.dumps(rec, ensure_ascii=rng.random() < .5) + '\n').encode()


def gen_body(rng, uuids, lo=1, hi=10):
    return b''.join(gen_line(rng, uuids) for _ in range(rng.randint(lo, hi)))


def gen_entries(rng, uuids):
    entries, used = [], set()
    for _ in range(rng.randint(1, 4)):
        src, ep = rng.choice(['main', 'child', 'side/x']), rng.choice(['e1', 'e2', 'e3'])
        if (src, ep) in used:
            continue
        used.add((src, ep))
        raw = entries[-1]['raw'] if entries and rng.random() < .2 else gen_body(rng, uuids)
        entries.append(dict(source=src, epoch=ep, role='main' if src == 'main' else 'subagent', raw=raw))
    return entries


def model(raw):
    """Independent expectation from raw bytes: (events, issues, tail, dup_lines)."""
    parts = raw.split(b'\n')
    lines = [p + b'\n' for p in parts[:-1]] + ([parts[-1]] if parts[-1] else [])
    events, issues, seen, dups, offset = [], [], set(), set(), 0
    for n, line in enumerate(lines, 1):
        try:
            rec = json.loads(line)
            ok = isinstance(rec, dict)
        except ValueError:
            ok = False
        if not ok:
            issues.append((n, offset, len(line), 'malformed_json'))
        else:
            uuid = rec.get('uuid')
            events.append((n, offset, len(line), uuid, rec['message']['role'], rec['message']['content']))
            if uuid in seen:
                dups.add(n)
            elif uuid:
                seen.add(uuid)
        tail = n == len(lines) and not line.endswith(b'\n')
        if tail:
            issues.append((n, offset, len(line), 'incomplete_tail'))
        offset += len(line)
    return events, sorted(issues), bool(lines) and not lines[-1].endswith(b'\n'), dups


def identity(sid, agent='claude'):
    return dict(archive_id='arch', machine_id='m', agent=agent, session_id=sid)


def write_export(directory, ident, seq, pub, entries, coverage=None):
    directory = Path(directory)
    directory.mkdir(parents=True)
    sources, bodies = [], []
    for n, e in enumerate(entries):
        raw, d = e['raw'], sha(e['raw'])
        name = f'body-{n}.bin'
        (directory / name).write_bytes(raw)
        sources.append(dict(source_id=e['source'], epoch_id=e['epoch'], role=e['role'], path='/original/' + e['source'],
            state='present', revision=e.get('revision', 1), observed_at_ns=3, captured_at_ns=4, associations=[],
            object=dict(key='immutable/' + d, encoding='zstd', sha256=d, encoded_sha256=d, size=len(raw), encoded_size=len(raw))))
        bodies.append(dict(source_id=e['source'], epoch_id=e['epoch'], path=name, sha256=d, size=len(raw)))
    manifest = dict(contract='session-publication/v2', identity=dict(ident), publication_id=pub, sequence=seq,
        observed_at_ns=1, captured_at_ns=2, published_at_ns=3,
        coverage=coverage or dict(discovery='complete', enrichment='complete', missing=[]), sources=sources)
    meta = dict(contract='session-reader/v2', manifest_sha256=sha(json.dumps(manifest).encode()), manifest=manifest, bodies=bodies)
    save(directory, meta)
    return meta


def save(directory, meta):
    (Path(directory) / 'metadata.json').write_text(json.dumps(meta))


class Base(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.n = 0

    def path(self, name):
        self.n += 1
        return self.root / f'{name}-{self.n}'

    def same(self, a, b, what):
        for t in a:
            if a[t][0] != b[t][0] or sorted(a[t][1]) != sorted(b[t][1]):  # multiset compare; physical order is not a projection property
                ra, rb = set(a[t][1]), set(b[t][1])
                self.fail(f'{what}: table {t} differs: only-left={sorted(ra - rb)[:2]} only-right={sorted(rb - ra)[:2]} '
                          f'counts={len(a[t][1])}/{len(b[t][1])}')

    def check_model(self, db, entries, ident_sid=None):
        total_lines = total_events = 0
        with duckdb.connect(str(db), read_only=True) as c:
            for e in entries:
                ev, iss, tail, dups = model(e['raw'])
                got = c.execute(f'SELECT {EV} FROM events WHERE source_id=? AND epoch_id=? ORDER BY line', [e['source'], e['epoch']]).fetchall()
                want = [(n, o, l, u, r, t, n in dups) for n, o, l, u, r, t in ev]
                self.assertEqual(got, want, f"events {e['source']}/{e['epoch']}")
                gi = c.execute('SELECT line,"offset",length,kind FROM issues WHERE source_id=? AND epoch_id=?', [e['source'], e['epoch']]).fetchall()
                self.assertEqual(sorted(gi), iss, f"issues {e['source']}/{e['epoch']}")
                pt = c.execute('SELECT partial_tail FROM sources WHERE source_id=? AND epoch_id=?', [e['source'], e['epoch']]).fetchone()
                self.assertEqual(pt, (tail,))
                total_events += len(ev)
            self.assertEqual(c.execute('SELECT count(*) FROM events').fetchone()[0], total_events, 'unexpected extra events')
            self.assertEqual(c.execute('SELECT count(*) FROM sources').fetchone()[0], len(entries))
            self.assertEqual(c.execute('SELECT count(*)-count(DISTINCT event_id) FROM events').fetchone()[0], 0)
            self.assertEqual(c.execute('SELECT count(*)-count(DISTINCT (source_id,epoch_id,line)) FROM events').fetchone()[0], 0)
        return total_events


class Properties(Base):
    def sessions(self, rng, k):
        uuids = [f'u{i}' for i in range(rng.randint(2, 5))]  # shared pool: duplicate UUIDs across epochs/sessions
        return [(identity(f's{i}'), gen_entries(rng, uuids)) for i in range(k)]

    def test_control_harness_reaches_importer(self):
        rng = random.Random(0)
        (ident, entries), = self.sessions(rng, 1)
        db = self.path('db')
        r = publications.import_export(write_export(self.path('x'), ident, 1, 'p1', entries) and self.root / f'x-{self.n}', db)
        self.assertEqual(r['status'], 'imported')
        self.assertEqual(r['events'], self.check_model(db, entries))
        self.assertGreater(r['events'], 0)

    def test_A1_idempotence_and_reinsert_equivalence(self):
        for seed in SEEDS:
            with self.subTest(seed=seed):
                rng = random.Random(seed)
                db = self.path('db')
                ss = self.sessions(rng, rng.randint(1, 3))
                dirs = []
                for ident, entries in ss:
                    d = self.path('x')
                    write_export(d, ident, 1, 'p1', entries)
                    dirs.append(d)
                    publications.import_export(d, db)
                first = dump(db)
                for d in dirs:
                    self.assertEqual(publications.import_export(d, db)['status'], 'unchanged')
                self.same(first, dump(db), 'second import changed projection')
                # A new publication id with identical content must rebuild the same rows (only publication coordinates differ).
                for (ident, entries) in ss:
                    d = self.path('x')
                    write_export(d, ident, 2, 'p2', entries)
                    self.assertEqual(publications.import_export(d, db)['status'], 'imported')
                skip = ('publication_id', 'sequence', 'manifest_sha256')
                self.same(dump(db, skip, ('events', 'issues')), dump_with_pub(first, db, skip), 'reimport new pub')

    def test_A2_order_invariance(self):
        for seed in SEEDS:
            with self.subTest(seed=seed):
                rng = random.Random(seed)
                ss = self.sessions(rng, rng.randint(2, 4))
                dirs = []
                for ident, entries in ss:
                    d = self.path('x')
                    write_export(d, ident, 1, 'p-' + ident['session_id'], entries)
                    dirs.append(d)
                a, b = self.path('db'), self.path('db')
                for d in dirs: publications.import_export(d, a)
                order = dirs[::-1] if seed % 2 else rng.sample(dirs, len(dirs))
                if order == dirs: order = dirs[::-1]
                for d in order: publications.import_export(d, b)
                self.same(dump(a), dump(b), 'order matters')
                self.assertGreater(len(dump(a)['events'][1]), 0)

    def test_A3_epoch_non_collision(self):
        for seed in SEEDS:
            with self.subTest(seed=seed):
                rng = random.Random(seed)
                (ident, entries), = self.sessions(rng, 1)
                db = self.path('db')
                publications.import_export(write_export(self.path('x'), ident, 1, 'p1', entries) and self.root / f'x-{self.n}', db)
                self.check_model(db, entries)

    def test_A4_partial_tail(self):
        parsed_tails = 0
        for seed in SEEDS:
            with self.subTest(seed=seed):
                rng = random.Random(seed)
                raw = gen_body(rng, ['u1', 'u2', 'u3'], 2, 8)
                cut = rng.randint(1, len(raw) - 1)
                raw = raw[:cut]
                entries = [dict(source='main', epoch='e1', role='main', raw=raw)]
                db = self.path('db')
                d = self.path('x')
                write_export(d, identity('t'), 1, 'p1', entries)
                publications.import_export(d, db)
                self.check_model(db, entries)
                ev, iss, tail, _ = model(raw)
                with duckdb.connect(str(db), read_only=True) as c:
                    kinds = [r[0] for r in c.execute("SELECT kind FROM issues WHERE kind='incomplete_tail'").fetchall()]
                    self.assertEqual(len(kinds), int(not raw.endswith(b'\n')))
                    if not raw.endswith(b'\n'):
                        self.assertEqual(c.execute('SELECT normalization FROM sessions').fetchone(), ('partial',))
                        last = len(raw.split(b'\n'))
                        parsed_tails += c.execute('SELECT count(*) FROM events WHERE line=?', [last]).fetchone()[0]
        print(f'A4 CHARACTERIZATION unterminated-but-parseable tails emitted as events: {parsed_tails}')

    def test_A4b_parseable_unterminated_tail_is_event_and_flagged(self):
        raw = b'{"type":"user","uuid":"u","message":{"role":"user","content":"hi"}}\n{"type":"user","uuid":"v","message":{"role":"user","content":"tail"}}'
        db = self.path('db')
        d = self.path('x')
        write_export(d, identity('t'), 1, 'p1', [dict(source='main', epoch='e', role='main', raw=raw)])
        publications.import_export(d, db)
        with duckdb.connect(str(db), read_only=True) as c:
            print('A4b events', c.execute('SELECT line,text FROM events ORDER BY line').fetchall(),
                  'issues', c.execute('SELECT line,kind FROM issues').fetchall(),
                  'partial_tail', c.execute('SELECT partial_tail FROM sources').fetchall())
            self.assertEqual(c.execute("SELECT line FROM issues WHERE kind='incomplete_tail'").fetchall(), [(2,)])
            self.assertEqual(c.execute('SELECT partial_tail FROM sources').fetchone(), (True,))
            self.assertEqual(c.execute('SELECT count(*) FROM events').fetchone()[0], 2)  # tail event emitted AND flagged

    def test_A5_append_monotonicity(self):
        for seed in SEEDS:
            with self.subTest(seed=seed):
                rng = random.Random(seed)
                uuids = ['u1', 'u2', 'u3']
                ident = identity('a')
                n1 = gen_entries(rng, uuids)
                n2 = []
                for e in n1:
                    e2 = dict(e, revision=2)
                    if rng.random() < .6:
                        e2['raw'] = e['raw'] + gen_body(rng, uuids)
                    n2.append(e2)
                if rng.random() < .3:
                    n2.append(dict(source='new', epoch='e9', role='subagent', raw=gen_body(rng, uuids), revision=1))
                db = self.path('db')
                d1 = self.path('x')
                write_export(d1, ident, 1, 'p1', n1)
                publications.import_export(d1, db)
                self.check_model(db, n1)
                with duckdb.connect(str(db), read_only=True) as c:
                    before = c.execute('SELECT * EXCLUDE(publication_id) FROM events ORDER BY ALL').fetchall()
                    before_i = c.execute('SELECT * EXCLUDE(publication_id) FROM issues ORDER BY ALL').fetchall()
                d2 = self.path('x')
                write_export(d2, ident, 2, 'p2', n2)
                self.assertEqual(publications.import_export(d2, db)['status'], 'imported')
                self.check_model(db, n2)
                with duckdb.connect(str(db), read_only=True) as c:
                    after = set(c.execute('SELECT * EXCLUDE(publication_id) FROM events').fetchall())
                    after_i = set(c.execute('SELECT * EXCLUDE(publication_id) FROM issues').fetchall())
                self.assertFalse(set(before) - after, 'prior event rewritten/lost by append')
                self.assertFalse(set(before_i) - after_i, 'prior issue rewritten/lost by append')

    VARIANTS = {  # name: (expected reason, failure row recorded in DB?)
        'flip_byte': ('body_integrity', True), 'truncate_1': ('body_integrity', True), 'append_1': ('body_integrity', True),
        'both_sha_bad': ('body_integrity', True), 'missing_body': ('missing_body', True),
        'body_size_plus': ('body_metadata_mismatch', False), 'body_sha_flip': ('body_metadata_mismatch', False),
        'source_sha_flip': ('body_metadata_mismatch', False), 'source_size_plus': ('body_metadata_mismatch', False),
        'metadata_garbage': ('invalid_metadata', False), 'contract_bad': ('unsupported_contract', False),
        'stale_sequence': ('stale_publication', True), 'conflict_same_seq': ('publication_conflict', True),
        'same_pub_flip': ('body_integrity', True),
    }

    def mutate(self, kind, d, meta, rng):
        i = rng.randrange(len(meta['bodies']))
        body, src = meta['bodies'][i], meta['manifest']['sources'][i]
        p = d / body['path']
        flip = lambda h: ('0' if h[0] != '0' else '1') + h[1:]
        if kind in ('flip_byte', 'same_pub_flip'):
            b = bytearray(p.read_bytes())
            b[rng.randrange(len(b))] ^= 1 << rng.randrange(8)
            p.write_bytes(bytes(b))
        elif kind == 'truncate_1': p.write_bytes(p.read_bytes()[:-1])
        elif kind == 'append_1': p.write_bytes(p.read_bytes() + b'x')
        elif kind == 'missing_body': p.unlink()
        elif kind == 'body_size_plus': body['size'] += 1
        elif kind == 'body_sha_flip': body['sha256'] = flip(body['sha256'])
        elif kind == 'source_sha_flip': src['object']['sha256'] = flip(src['object']['sha256'])
        elif kind == 'source_size_plus': src['object']['size'] += 1
        elif kind == 'both_sha_bad': body['sha256'] = src['object']['sha256'] = flip(body['sha256'])
        elif kind == 'contract_bad': meta['contract'] = 'session-reader/v3'
        elif kind == 'metadata_garbage': save(d, meta); (d / 'metadata.json').write_bytes(b'{"contract":'); return
        save(d, meta)

    def test_A6_corruption_atomicity(self):
        for seed in SEEDS[:max(1, len(SEEDS) // 2)]:
            rng = random.Random(seed)
            ident = identity('c')
            entries = gen_entries(rng, ['u1', 'u2'])
            db = self.path('db')
            base = self.path('x')
            write_export(base, ident, 2, 'p2', entries)
            publications.import_export(base, db)
            before = dump(db, tables=('publication_binding', 'sessions', 'sources', 'events', 'issues'))
            for kind, (reason, row) in self.VARIANTS.items():
                with self.subTest(seed=seed, kind=kind):
                    with duckdb.connect(str(db)) as c:
                        c.execute('DELETE FROM failures')
                    seq, pub = {'stale_sequence': (1, 'p1'), 'conflict_same_seq': (2, 'pX'), 'same_pub_flip': (2, 'p2')}.get(kind, (3, 'p3'))
                    d = self.path('x')
                    meta = write_export(d, ident, seq, pub, entries)
                    self.mutate(kind, d, meta, rng)
                    with self.assertRaises(publications.ImportFailure) as cm:
                        publications.import_export(d, db)
                    self.assertEqual(str(cm.exception), reason)
                    self.same(before, dump(db, tables=('publication_binding', 'sessions', 'sources', 'events', 'issues')), 'projection changed by failed import')
                    with duckdb.connect(str(db), read_only=True) as c:
                        rows = c.execute('SELECT sequence,reason FROM failures').fetchall()
                    self.assertEqual(rows, [(seq, reason)] if row else [], 'failure visibility')
            # control: a valid next publication does change the projection
            d = self.path('x')
            write_export(d, ident, 5, 'p5', entries)
            self.assertEqual(publications.import_export(d, db)['status'], 'imported')
            with duckdb.connect(str(db), read_only=True) as c:
                self.assertEqual(c.execute('SELECT sequence FROM sessions').fetchone(), (5,))


def dump_with_pub(first, db, skip):
    """Project the earlier full dump (events/issues) with the same column exclusions."""
    out = {}
    for t in ('events', 'issues'):
        cols, rows = first[t]
        keep = [i for i, c in enumerate(cols) if c not in skip]
        out[t] = ([cols[i] for i in keep], sorted(tuple(r[i] for i in keep) for r in rows))
    return out


class A8Strictness(Base):
    def probe(self, mutate, name):
        d = self.path('x')
        meta = write_export(d, identity('p'), 1, 'p1', [dict(source='main', epoch='e', role='main', raw=gen_body(random.Random(1), ['u']))])
        mutate(meta)
        save(d, meta)
        db = self.path('db')
        try:
            publications.import_export(d, db)
            res = 'accepted'
        except publications.ImportFailure as e:
            res = 'rejected:' + str(e)
        except Exception as e:  # not a safe ImportFailure code: characterize
            res = 'RAW_EXCEPTION:' + type(e).__name__
        print(f'A8 PROBE {name}: {res}', flush=True)
        return res, db

    def test_control_valid_accepted(self):
        self.assertEqual(self.probe(lambda m: None, 'control_valid')[0], 'accepted')

    def test_A8_unknown_fields_rejected_like_go(self):
        """Go decodes every wire struct with DisallowUnknownFields; so must the consumer."""
        for name, f in {
            'unknown_top': lambda m: m.update(zz=1),
            'unknown_manifest': lambda m: m['manifest'].update(zz=1),
            'unknown_identity': lambda m: m['manifest']['identity'].update(zz=1),
            'unknown_coverage': lambda m: m['manifest']['coverage'].update(zz=1),
            'unknown_source': lambda m: m['manifest']['sources'][0].update(zz=1),
            'unknown_object': lambda m: m['manifest']['sources'][0]['object'].update(zz=1),
            'unknown_body_entry': lambda m: m['bodies'][0].update(zz=1),
        }.items():
            with self.subTest(name):
                res, db = self.probe(f, name)
                self.assertEqual(res, 'rejected:invalid_metadata')
                self.assertFalse(db.exists())

    def test_A8_sequence_probes(self):
        for name, v, expect_reject in (('sequence_0', 0, True), ('sequence_neg', -1, True), ('sequence_2p63', 2**63, True),
                                       ('sequence_true', True, True), ('sequence_1_control', 1, False)):
            with self.subTest(name):
                res, _ = self.probe(lambda m: m['manifest'].update(sequence=v), name)
                self.assertEqual(res.startswith('rejected'), expect_reject, f'{name} -> {res} (Go validOrder requires 1..MaxInt64)')

    def test_A8_metadata_exact_go_bound(self):
        """Exactly MaxMetadata (8 MiB) is accepted; one byte more is rejected."""
        for extra, want in ((0, 'accepted'), (1, 'rejected:invalid_metadata')):
            with self.subTest(extra=extra):
                d = self.path('x')
                meta = write_export(d, identity('p'), 1, 'p1', [dict(source='main', epoch='e', role='main', raw=gen_body(random.Random(1), ['u']))])
                meta['manifest']['coverage']['missing'] = ['']
                size = len(json.dumps(meta))
                meta['manifest']['coverage']['missing'] = ['x' * (8 * 1024 * 1024 - size + extra)]
                save(d, meta)
                self.assertEqual(len(json.dumps(meta)), 8 * 1024 * 1024 + extra)
                try:
                    publications.import_export(d, self.path('db'))
                    got = 'accepted'
                except publications.ImportFailure as e:
                    got = 'rejected:' + str(e)
                self.assertEqual(got, want)

    def test_A8_missing_digest_is_distinct_typed_code(self):
        res, db = self.probe(lambda m: m.pop('manifest_sha256'), 'digest_missing')
        self.assertEqual(res, 'rejected:reader_digest_missing')
        self.assertFalse(db.exists())

    def test_A8_source_count_bound(self):
        for n, want in ((1024, 'accepted'), (1025, 'rejected:invalid_metadata')):
            with self.subTest(sources=n):
                entries = [dict(source=f's{i}', epoch='e', role='subagent', raw=b'') for i in range(n)]
                d = self.path('x')
                write_export(d, identity('p'), 1, 'p1', entries)
                try:
                    publications.import_export(d, self.path('db'))
                    got = 'accepted'
                except publications.ImportFailure as e:
                    got = 'rejected:' + str(e)
                self.assertEqual(got, want)

    def test_A8_digest_size_and_encoding_bounds(self):
        cases = {
            'digest_uppercase': lambda m: m.update(manifest_sha256='A' * 64),
            'digest_short': lambda m: m.update(manifest_sha256='a' * 63),
            'size_maxint64': lambda m: m['manifest']['sources'][0]['object'].update(size=2**63 - 1),
            'encoded_size_maxint64': lambda m: m['manifest']['sources'][0]['object'].update(encoded_size=2**63 - 1),
            'encoding_gzip': lambda m: m['manifest']['sources'][0]['object'].update(encoding='gzip'),
        }
        for name, f in cases.items():
            with self.subTest(name):
                res, db = self.probe(f, name)
                self.assertEqual(res, 'rejected:invalid_metadata')
                self.assertFalse(db.exists())

    def test_A8_metadata_size_probes(self):
        for name, mib, expect_reject in (('metadata_7MiB_control', 7, False), ('metadata_9MiB_over_go_8MiB', 9, True), ('metadata_17MiB_over_py_16MiB', 17, True)):
            with self.subTest(name):
                n = mib * 1024 * 1024 // 1003
                res, _ = self.probe(lambda m: m['manifest']['coverage'].update(missing=['x' * 1000] * n), name)
                self.assertEqual(res.startswith('rejected'), expect_reject, f'{name} -> {res} (Go MaxMetadata = 8MiB)')


@unittest.skipUnless(os.environ.get('CAPTURE_V1_BINARY'), 'needs actual writer binary')
class WriterBacked(Base):
    CAP = 48 * 1024

    def setUp(self):
        super().setUp()
        self.binary = str(Path(os.environ['CAPTURE_V1_BINARY']).resolve())
        r = self.root
        for p in ('home', 'policy/exclusions', 'src'):
            (r / p).mkdir(parents=True, mode=0o700)
        self.cfg = r / 'runtime.json'
        self.cfg.write_text(json.dumps(dict(version='capture-runtime/v1', archive_id='a7', machine_id='M', store=str(r / 'store'),
            spool=str(r / 'spool'), policy_dir=str(r / 'policy'), home=str(r / 'home'), default_roots=False, roots=[],
            min_free_bytes=0, capture_workers=2, delivery_workers=2, poll_seconds=1, operation_seconds=30, max_attempts=2,
            max_spool_bytes=1 << 24, entries_per_root=256,
            quiet_seconds=0, max_delay_seconds=0)))
        self.cfg.chmod(0o600)
        self.writer('init', '--store', str(r / 'store'), '--archive', 'a7')
        common = ['--archive', 'a7', '--machine', 'M', '--agent', 'claude', '--session', 'root/big', '--spool', str(r / 'spool'), '--policy-dir', str(r / 'policy')]
        src = r / 'src' / 'main.jsonl'
        for prefix, n in (('e1', 100), ('e2', 100), ('e3', 1)):  # three disjoint rewrites: two ~30KB sealed epochs, small latest
            src.write_text(''.join(json.dumps(dict(type='user', uuid=f'{prefix}-{i}', message=dict(role='user',
                content=f'{prefix} lorem ipsum dolor sit amet ' * 8))) + '\n' for i in range(n)))
            self.writer('capture', *common, '--source', 'main', '--path', str(src))
        self.writer('deliver', *common, '--store', str(r / 'store'))

    def writer(self, *args, ok=True):
        p = subprocess.run([self.binary, 'capture-v1', *args], stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=60)
        self.assertEqual(p.returncode == 0, ok, f'writer {args[0]} rc={p.returncode}')
        return p

    def export(self, name, cap=None, ok=True):
        out = self.root / name
        args = ['export', '--config', str(self.cfg), '--archive', 'a7', '--machine', 'M', '--agent', 'claude', '--session', 'root/big', '--output', str(out)]
        if cap is not None:
            args += ['--max-bytes', str(cap)]
        self.writer(*args, ok=ok)
        return out

    def sizes(self):
        meta = json.loads((self.export('full') / 'metadata.json').read_text())
        return [b['size'] for b in meta['bodies']], meta

    def test_A7_go_cap_math(self):
        sizes, meta = self.sizes()
        total = sum(sizes)
        print(f'A7 epochs={len(sizes)} sizes={sizes} cumulative={total} latest={sizes[-1]} refresh_cap={self.CAP}', flush=True)
        self.assertGreaterEqual(len(sizes), 3, 'rewrites must retain sealed epochs')
        self.assertLess(sizes[-1], self.CAP)
        self.assertGreater(total, self.CAP)
        self.export('exact', total)
        self.export('minus1', total - 1, ok=False)
        self.export('refresh_cap', self.CAP, ok=False)

    def sweep(self, cap):
        import publication_refresh as refresh
        db, status = self.path('db'), self.path('status')
        endpoints = [dict(name='M', binary=self.binary, config=str(self.cfg))]
        with contextlib.closing(refresh.open_status(status)) as st:
            rep = refresh.sweep(endpoints, db, st, timeout=30, max_bytes=cap)
            targets = st.execute('SELECT sequence,imported_sequence,error FROM targets').fetchall()
        n = 0
        if db.exists():
            with duckdb.connect(str(db), read_only=True) as c:
                n = c.execute('SELECT count(*) FROM sessions').fetchone()[0]
        print(f'A7 SWEEP cap={cap} report={ {k: rep[k] for k in ("expected_targets", "unimported", "target_errors", "discovery_errors")} } targets={targets} sessions={n}', flush=True)
        return rep, targets, n

    def test_A7_control_large_cap_imports(self):
        rep, targets, n = self.sweep(1 << 30)
        self.assertEqual((n, rep['target_errors'], rep['unimported']), (1, 0, 0))

    def test_A7_weak_visible_failure_or_import(self):
        rep, targets, n = self.sweep(self.CAP)
        self.assertTrue(n == 1 or rep['target_errors'] > 0 or rep['unimported'] > 0 or rep['discovery_errors'] > 0,
                        'green refresh silently lacks the session')

    def test_A7_over_cap_is_typed_visible_coverage_gap(self):
        """Cumulative history over the cap never yields a green refresh lacking the session."""
        rep, targets, n = self.sweep(self.CAP)
        self.assertEqual(n, 0)
        self.assertEqual((rep['expected_targets'], rep['unimported'], rep['target_errors']), (1, 1, 1))
        self.assertEqual(rep['target_error_codes'], {'export_too_large': 1})
        self.assertEqual([(t[1], t[2]) for t in targets], [(None, 'export_too_large')])

    def test_A7_cap_at_cumulative_size_imports(self):
        sizes, _ = self.sizes()
        rep, targets, n = self.sweep(sum(sizes))
        self.assertEqual((n, rep['target_errors'], rep['unimported'], rep['target_error_codes']), (1, 0, 0, {}))

    def test_A7_exact_cap_boundary_matches_go(self):
        sizes, _ = self.sizes()
        total = sum(sizes)
        rep, _, n = self.sweep(total)
        self.assertEqual((n, rep['target_error_codes']), (1, {}))
        rep, targets, n = self.sweep(total - 1)
        self.assertEqual((n, rep['target_error_codes']), (0, {'export_too_large': 1}))

    def test_A7_default_cap_clears_measured_real_session(self):
        import publication_refresh as refresh
        self.assertEqual(refresh.DEFAULT_MAX_BYTES, 1 << 30)
        self.assertGreater(refresh.DEFAULT_MAX_BYTES, 615_165_952)  # largest measured session

    def test_A8_manifest_digest_is_remote_current_digest(self):
        _, meta = self.sizes()
        current = next((self.root / 'store').rglob('current.json'))
        self.assertEqual(json.loads(current.read_text())['manifest_sha256'], meta['manifest_sha256'])
        db = self.path('db')
        publications.import_export(self.root / 'full', db)
        with duckdb.connect(str(db), read_only=True) as c:
            self.assertEqual(c.execute('SELECT manifest_sha256 FROM sessions').fetchone(), (meta['manifest_sha256'],))


if __name__ == '__main__':
    unittest.main()
