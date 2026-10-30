#!/usr/bin/env python3
"""Consume ONLY generated authoritative exports; never open original source paths."""
import json
from pathlib import Path
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'analytics'))
import duckdb
import publications


def main():
    if sys.argv[1:] == ['--preflight']:
        publications._dependencies()
        return
    old, final, healthy, database = map(Path, sys.argv[1:])
    metadata = [json.loads((p / 'metadata.json').read_text()) for p in (old, final, healthy)]
    a0, a1, b = [m['manifest'] for m in metadata]
    assert a0['identity'] == a1['identity']
    assert a0['identity']['session_id'] == 'A'
    assert b['identity']['session_id'] == 'B'
    assert a1['sequence'] > a0['sequence']
    assert a1['sources'][0]['epoch_id'] == a0['sources'][0]['epoch_id']
    assert a1['sources'][0]['revision'] > a0['sources'][0]['revision']
    for export in (old, healthy, final):
        publications.import_export(export, database)
    with duckdb.connect(str(database), read_only=True) as conn:
        before = conn.execute('SELECT event_id FROM events ORDER BY event_id').fetchall()
        assert len(before) == 3
        assert dict(conn.execute('SELECT role,count(*) FROM events GROUP BY role').fetchall()) == {'user': 2, 'assistant': 1}
        assert conn.execute('SELECT count(*) FROM issues').fetchone()[0] == 0
        expected = []
        for m in (a1, b):
            i, s = m['identity'], m['sources'][0]
            expected.append((i['archive_id'], i['machine_id'], i['agent'], i['session_id'],
                             m['publication_id'], 'main', s['epoch_id']))
        assert sorted(conn.execute('SELECT DISTINCT archive_id,machine_id,agent,session_id,publication_id,source_id,epoch_id FROM events').fetchall()) == sorted(expected)
    for export in (final, healthy):
        publications.import_export(export, database)
    with duckdb.connect(str(database), read_only=True) as conn:
        assert before == conn.execute('SELECT event_id FROM events ORDER BY event_id').fetchall()
    report = publications.report(database)
    assert report['contract'] == 'session-analytics-report/v2'
    assert report['archive_id'] == a1['identity']['archive_id']
    assert report['failures'] == []
    assert len(report['sessions']) == 2
    for manifest in (a1, b):
        session = next(s for s in report['sessions'] if s['session_id'] == manifest['identity']['session_id'])
        assert session['publication_id'] == manifest['publication_id']
        assert session['coverage'] == manifest['coverage']
        assert session['normalization'] == 'complete'
        assert session['captured_at_ns'] == manifest['captured_at_ns']
        assert session['imported_at_ns'] > 0


if __name__ == '__main__':
    main()
