#!/usr/bin/env python3
"""Actual native export -> publication importer -> event index -> report.

Reads only the authoritative export and its verified owned body cache, never
original source paths. All writes stay below the supplied scratch directory.
"""
import hashlib
import importlib.util
import json
from pathlib import Path
import sys

import duckdb

# Load only these explicit public modules: the sandbox intentionally forbids
# listing or reading the rest of the repository (including private research).
for name in ('events', 'publications'):
    path = Path(__file__).resolve().parents[1] / 'analytics' / (name + '.py')
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    sys.modules[name] = module
    spec.loader.exec_module(module)
publications = sys.modules['publications']


def main():
    export, database = map(Path, sys.argv[1:])
    metadata = json.loads((export / 'metadata.json').read_text())
    manifest = metadata['manifest']
    identity = manifest['identity']
    sources = {(s['source_id'], s['epoch_id']): s for s in manifest['sources']}
    bodies = {(b['source_id'], b['epoch_id']): b for b in metadata['bodies']}
    assert len(sources) == len(manifest['sources'])
    assert len(bodies) == len(metadata['bodies'])
    assert set(bodies) == {pair for pair, s in sources.items() if not s.get('withheld')}
    for pair, body in bodies.items():
        data = (export / body['path']).read_bytes()
        assert len(data) == body['size'] == sources[pair]['object']['size']
        assert hashlib.sha256(data).hexdigest() == body['sha256'] == sources[pair]['object']['sha256']
    publications.import_export(export, database)
    with duckdb.connect(str(database), read_only=True) as conn:
        before = conn.execute('SELECT event_id FROM events ORDER BY event_id').fetchall()
        assert before
        assert conn.execute("SELECT count(*) FROM events WHERE role='assistant' AND text='ACCEPTANCE_OK'").fetchone()[0] > 0
        assert conn.execute("SELECT count(*) FROM events WHERE role='user' AND contains(text, 'Reply with exactly ACCEPTANCE_OK. Do not use tools.')").fetchone()[0] > 0
        expected = sorted((identity['archive_id'], identity['machine_id'], identity['agent'],
                           identity['session_id'], manifest['publication_id'], *pair) for pair in sources)
        actual = sorted(conn.execute('SELECT archive_id,machine_id,agent,session_id,publication_id,source_id,epoch_id FROM sources').fetchall())
        assert actual == expected
        for source_id, epoch_id, path, sha256, size in conn.execute('SELECT source_id,epoch_id,path,sha256,size FROM sources WHERE withheld_reason IS NULL').fetchall():
            data = Path(path).read_bytes()
            assert Path(path).is_relative_to(database.parent)
            assert hashlib.sha256(data).hexdigest() == sha256 == bodies[source_id, epoch_id]['sha256']
            assert len(data) == size == bodies[source_id, epoch_id]['size']
    publications.import_export(export, database)
    with duckdb.connect(str(database), read_only=True) as conn:
        assert before == conn.execute('SELECT event_id FROM events ORDER BY event_id').fetchall()
        assert conn.execute('SELECT count(*) FROM sources').fetchone()[0] == len(sources)
    report = publications.report(database)
    assert report['contract'] == 'session-analytics-report/v2'
    assert report['archive_id'] == identity['archive_id']
    assert report['failures'] == []
    assert len(report['sessions']) == 1
    session = report['sessions'][0]
    for key in ('archive_id', 'machine_id', 'agent', 'session_id'):
        assert session[key] == identity[key]
    assert session['publication_id'] == manifest['publication_id']
    assert session['coverage'] == manifest['coverage']
    assert session['captured_at_ns'] == manifest['captured_at_ns']
    assert session['imported_at_ns'] > 0 and session['normalized_at_ns'] > 0
    assert report['source_counts']['sources'] == len(sources)
    assert {(s['source_id'], s['epoch_id']) for s in report['sources']} == set(sources)
    # Opaque native enrichment is owned evidence, not a normalized conversation.
    assert session['normalization'] in ('complete', 'partial')
    (database.parent / 'report.json').write_text(json.dumps(report, sort_keys=True))
    print('native export -> importer -> event index -> report: PASS; repeat import idempotent')


if __name__ == '__main__':
    main()
