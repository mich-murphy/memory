#!/usr/bin/env python3
"""Synthetic, opt-in baseline/current importer comparison; no host sources.

Run from repository root with pinned Nix and the private analytics Python:
  nix develop --command env PYTHONDONTWRITEBYTECODE=1 \
    /Users/mm/dev/memory/.analytics-venv/bin/python tools/analytics/bench_publication_import.py
"""
import duckdb
import hashlib
import importlib.util
import json
from pathlib import Path
import subprocess
import tempfile
import time

import publications
from test_publications import PublicationsTests


def projection_digest(db_path):
    with duckdb.connect(str(db_path), read_only=True) as db:
        event_rows = db.execute('''SELECT line,"offset",length,event_type,role,text,
            is_human,duplicate,tool_args FROM events ORDER BY line''').fetchall()
        issue_rows = db.execute('''SELECT line,"offset",length,kind,record_type
            FROM issues ORDER BY line,kind''').fetchall()
    return hashlib.sha256(json.dumps([event_rows, issue_rows], ensure_ascii=False).encode()).hexdigest(), len(event_rows), len(issue_rows)


def main():
    with tempfile.TemporaryDirectory() as temp:
        prior_path = Path(temp) / 'prior_publications.py'
        prior_path.write_bytes(subprocess.check_output([
            'git', 'show', '965cf69:tools/analytics/publications.py']))
        spec = importlib.util.spec_from_file_location('prior_publications', prior_path)
        previous = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(previous)
        for count in (1000, 10000):
            expected = None
            for label, importer in (('before', previous), ('after', publications)):
                fixture = PublicationsTests()
                fixture.setUp()
                try:
                    raw = (b'{"type":"user","uuid":"u","message":{"role":"user","content":"hello"}}\n'
                           * (count - 1) + b'bad JSON\n')
                    fixture.fixture([('s', 'e', 'main', raw)])
                    start = time.monotonic()
                    importer.import_export(fixture.export, fixture.db)
                    elapsed = time.monotonic() - start
                    digest, events, issues = projection_digest(fixture.db)
                    if expected is None:
                        expected = (digest, events, issues)
                    elif expected != (digest, events, issues):
                        raise AssertionError('indexed event/issue mismatch')
                    print(label, count, 'events', events, 'issues', issues,
                          'seconds', round(elapsed, 3), 'indexed_digest', digest, flush=True)
                finally:
                    fixture.temp.cleanup()


if __name__ == '__main__':
    main()
