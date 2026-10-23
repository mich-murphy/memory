#!/usr/bin/env python3
"""Run local read-only DuckDB SQL; no R2 access or credentials needed."""
import argparse
import os
from pathlib import Path

import duckdb


def main():
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source', choices=('legacy', 'publications'), default='legacy')
    parser.add_argument('--database')
    parser.add_argument('--sql', help='explicit local read-only SQL (required for publications)')
    args = parser.parse_args()
    database = args.database or ('.analytics/publications.duckdb' if args.source == 'publications' else '.analytics/sessions.duckdb')
    if args.source == 'publications':
        if not args.sql:
            parser.error('--source publications requires explicit --sql; no legacy report is used')
        try:
            from publication_dashboard import PublicationDashboard
            PublicationDashboard(database)
        except (ValueError, OSError, duckdb.Error):
            parser.error('unsupported or unavailable publication projection')
    with duckdb.connect(database, read_only=True) as db:
        db.execute("SET TimeZone = 'UTC'")
        sql = args.sql or Path(__file__).with_name('report.sql').read_text()
        for statement in db.extract_statements(sql):
            if args.source == 'publications' and statement.type != duckdb.StatementType.SELECT:
                parser.error('publications SQL must be a read-only SELECT')
            db.sql(statement.query).show(max_rows=100)
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
