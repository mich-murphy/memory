# Analytics projection (`publications.py`)

`tools/analytics/publications.py` imports the exports described in the
[export contract](session-publication-v1.md) into a private DuckDB database and reports on it.
The file name is kept for existing links. The importer reads only the export directory
(`metadata.json` plus the relative body files); it never lists storage, selects publications,
decompresses objects or reconstructs history. Tables and views are described in the
[analytics README](../tools/analytics/README.md); the end-to-end command sequence is in
[install](install.md).

## Commands and API

```sh
python tools/analytics/publications.py import EXPORT --database DB [--reset]
python tools/analytics/publications.py import-shared EXPORT --database DB [--reset]
python tools/analytics/publications.py report --database DB [--summary]
```

- `import_export(export_dir, db_path, *, reset=False)` returns a count/status dict. Success is
  `status` `imported` or `unchanged`. A failure raises `ImportFailure` with a content-free
  reason code (the command prints it to stderr and exits 1).
- `import_shared(...)` does the same for a `shared-reader/v1` export (Pi run records).
- `report(db_path, *, now_ns=None, stale_after_ns=86400 * 10**9, summary=False)` returns a
  JSON-safe report, contract `session-analytics-report/v2`: sessions with coverage, capture age
  and a stale flag (older than one day, or with an outstanding failure), failures, source and
  role counts, event and issue counts by kind, withheld counts, and freshness (maximum
  captured, published and imported nanosecond times, with counts of unknown times; zero is
  unknown). `summary=True` returns exact SQL aggregates, at most 50 session and failure
  details, no source list, and the `setup` and `runs` sections.

## Database

- Use a path that does not hold another database. The schema is `publication_binding.version
  = 2`; an existing database with another version, or a non-projection database, is refused
  (`unsupported_database`, `not_publication_database`) before any write, even with `--reset`.
  There is no migration.
- The first import binds one `archive_id`; another archive fails `archive_mismatch` unless
  `--reset`, which clears the projection tables (not the body files) and rebinds.
- Verified bodies are copied to `<database>.bodies/<sha256>`, the database file and bodies
  0600 and directories 0700. Body files are never garbage collected.
- The database and export directories must stay private and trusted.

## Import semantics

- One import replaces one session inside a transaction. Bodies are copied through a staging
  directory and verified against the pinned sha256 and size first. A repeat of the same
  publication re-verifies and repairs the owned bodies and returns `unchanged`; it never
  advances freshness.
- Order: a lower `sequence` than the stored one fails `stale_publication`. The same sequence
  with a different publication id or manifest digest fails `publication_conflict`, as does the
  same publication id with a different digest.
- Failures: a failed import of a known session records a content-free reason and an attempt
  count in `failures` and leaves the previous success in place. The failure is a sequence
  watermark: an older failed retry only increments `attempts`; a successful import of that
  publication, or of a strictly newer one, clears it. Metadata rejected before the identity is
  known creates no row; the caller sees the exit code.
- Identities: `session_key` hashes archive, machine, agent and session. A trace id hashes the
  session key, the effective source key (SHA-256 of the `source_id`, or the withheld
  `source_id_hash`) and the epoch id. Advancing a revision replaces that epoch's rows;
  overlapping epochs stay distinct and are not deduplicated.
- Normalization: transcripts are parsed line by line into `events` and `issues` (malformed
  lines keep their byte offsets; `record_type` is never stored). A nonempty last line without a
  newline is an `incomplete_tail` issue. A session with any issue is `partial`, otherwise
  `complete`. Role `subagent` user records are nonhuman. Codex mirror records are paired.
- Role `artifact` sources (databases, settings, snapshots) are kept as verified raw bytes and
  produce one `artifact_not_normalized` issue, no events and no tail check.
- A withheld source produces a `sources` row with no path, hash or size, a
  `source_withheld_<reason>` issue and a partial session; it opens no body. Reports expose
  `source_key_hash`, `optional` and `withheld_reason`.
- Derived tables (setup facts, usage, tool outcomes, Pi run references) are re-derived from the
  owned bodies when the extractor version changes; see the analytics README.

## Tests

`python -m unittest discover -s tools/analytics` runs the importer suite with synthetic
exports. The export journey (`test_rust_export_journey.py`) runs real `memory export` output
through the importer, event index and report as the `10-consumer` Rust gate; see
[quality gates](quality-gates.md).
