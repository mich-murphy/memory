# Evidence 51: Pi subagent run records in analytics (brief 56)

All dates are UTC. Status: increments SHARED, RUNS and GATE accepted locally at `2bf71d9` on
2026-11-24. That is 14 commits (11 by workers, 3 by the parent) over the brief `743aee6`, on
`main` `2ff1640`. `memory export`, `memory owners` and the reader library changed (capture is
unchanged), and the analytics importer. The workers read no real agent data and no production
state. Install and the real-data run follow merge (section Install, added then).

[Brief 56](../../56-pi-run-records.md) records the measured run-record shape and links behind the
decisions.

## Commits

| Commit | By | Change |
|---|---|---|
| `b04739e` | worker 1 | `memory_core::locator::shared_analytics(kind, path)`: `PiAux` with the run-record shape only, reusing `pi_run_record` |
| `0a4a897` | worker 1 | `build_shared_envelope`: `shared-reader/v1` / `shared-publication/v1`, analytics sources only, others counted `skipped`, deleted sources `sealed`, sources without epochs left out, bounds |
| `354eafa` | worker 1 | `memory export` writes a shared owner; the shared success line carries `skipped=` |
| `d86b1ff` | worker 1 | `memory owners --shared` lists `<prefix>shared/` |
| `94b2c65` | worker 1 | Module docs |
| `a705b61` | parent | A module doc line wrapped at 100 columns |
| `73b0993` | parent | Test for a surviving tooth: a deleted run record without epochs is left out and not counted |
| `e2e4aac` | worker 2 | `publications.import_shared`: strict validation of the shared contract, archive binding, one owner replaced per transaction, `unchanged` on the same publication and `RUNS_VERSION` |
| `8b15d7e` | worker 2 | `pi_runs.py`: `pi_run` and `pi_run_extension` rows, value rules, never-stored fields |
| `ac98d6a` | worker 2 | `pi_run_ref` from Pi session bodies (structured fields only), `SETUP_VERSION = 3` |
| `411b62a` | worker 2 | View `pi_run_dim` and the report's `runs` section |
| `cbe64ba` | worker 2 | README |
| `33c5832` | parent | Test for a surviving tooth: `pi_run_dim` picks the earlier of two referencing sessions |
| `2bf71d9` | worker 3 | Consumer gate: a run record and a parent reference in the Pi fixture; the built binary exports the shared owner; `import_shared` imports it; columns, link, report and 12 canaries checked end to end |

Deviations, accepted:

- `ExportError::Shared` is kept (`build_envelope` still refuses a shared head; unreachable from
  `export_owner`, which picks the builder by owner kind) and `ExportError::Session` is new
  (`build_shared_envelope` given a session head). `memory::export::Failure::Shared` is removed.
  `Envelope` gained `skipped` (0 for sessions); `Report` gained `skipped` and `shared`, so only
  the shared success line differs. `owners::collect_shared_owners` is public beside
  `collect_owners`.
- The 16,384-source bound is not the binding limit: one source with its body entry is about 600
  bytes of metadata, so the 8 MiB metadata bound refuses first at roughly 13,900 sources. The test
  pins both (16,384 gives `export_metadata_too_large`, 16,385 `export_too_many_sources`, 10,000
  passes). At the measured rate (about 60 run records a week on dev-01) that is years away; both
  refusals are closed codes.
- The S3-rig export test has one run record and `skipped=0`. A retired entry under a shared owner
  cannot be produced by the current writer (dropped kinds are never captured), so the retired
  case (`skipped`) is covered at the library level (`shared_export_keeps_run_records_only`,
  `shared_export_of_only_retired_entries_is_empty`).
- `pi_run_owner` (one row per shared owner: publication, sequence, manifest digest,
  `runs_version`, times) is an extra table; `unchanged` needs it and `reset` clears it.
- The database-preparation block of `import_export` moved into `_reserve`, shared by both
  importers; behaviour unchanged.
- `pi_run_ref` lives in `pi_runs.py` (`iter_refs`) and is called from `_write_setup` for Pi
  sources; its skips count in `usage_skipped`. A bad extension digest counts in `runs_skipped`,
  and the list length still counts every entry.
- `runtime_checks` reads `acceptance.runtimeChecks` (where dev-01's records hold it), with a
  top-level `runtimeChecks` fallback; `review_status` reads `acceptance.reviewResult.status`.
- GATE landed as one commit: its Rust half writes the expectations the Python half reads.
- Changed existing expectations: `s3_store/export.rs`
  `the_binary_exports_a_session_and_its_exit_table` (a shared owner without `current.json` is now
  66 `owner_absent`, was 64 `owner_shared`) and
  `an_existing_destination_is_refused_before_any_fetch` (`Failure::Absent` for an absent shared
  owner); the consumer gate's `report.landed` 6 → 7 (the
  fixture gained a shared owner). All commented with brief 56.

## Parent acceptance

- Whole diffs read.
- Teeth, RUNS (`b56_runs_teeth.py`, `test_pi_runs`, `test_usage_facts` and `test_setup_facts`,
  baseline 52 tests): 26 of 26 caught after `33c5832`:
  - value rules: status, model `//`, digest and run-id rules removed, skips not reported;
  - leaks: `task` stored as the agent type, `runner.command` stored as the runner type;
  - columns: review status, cost, omitted count dropped;
  - references: each `via` path dropped (tool result, mission children, workflow children,
    pending, supervisor reply, control notice), first-sighting removed;
  - the view taking the latest reference (survived at first; caught by `33c5832`);
  - the version not bumped, `pi_run_ref` not re-derived, `RUNS_VERSION` ignored, reset keeping
    run rows, the source kind unchecked, source keys open, the source bound removed, the report's
    old-database guard removed.
- Teeth, SHARED (`b56_shared_teeth.py`, focused filter over `core_locator`, the reader's
  `export` tests and the `export::`/`owners::` rig tests, baseline 54 then 55): 11 of 13 caught
  after `73b0993`, 2 equivalent:
  - caught: the predicate widened to any `PiAux` or to Pi config, a retired entry exported,
    `skipped` not counted, a source without epochs kept (survived at first; caught by `73b0993`),
    the shared bound removed, a deleted source exported `active`, the wrong contract, the session
    builder used for shared owners, the success line without `skipped`, `--shared` listing
    `sessions/`;
  - equivalent: `owners --shared` accepting a session-shaped key and plain `owners` accepting a
    shared-shaped key. The family prefix is checked first, so a key under `shared/` never parses
    as a session owner and a key under `sessions/` never parses as a shared one.
- Python suite on `2bf71d9`: 213 tests, OK (29 environment-dependent skips).
- Full Rust gates on `2bf71d9`: 14 of 14, seed 1904380078, line coverage 95.25 %.
- Linux container (`memory-linux-test`) on `2bf71d9`: reader `export` 19 passed, `core_locator`
  24 passed, `s3_store` `export::` and `owners::` 20 passed.
