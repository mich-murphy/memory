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

## Install (2026-11-24)

Merged as `07bfc7d` (stack #281, PRs #276 to #280) and installed on both hosts.

| | This Mac | dev-01 |
|---|---|---|
| Time | 08:14Z | 08:47Z |
| sha256 prefix | `d5321ae74a624772` | `1f4387e8762485b0` (built there in 10 min; `ldd` store paths equal the gcroots) |
| Process | pid 11758, `runs = 18` | pid 713973, `NRestarts=0` |
| Backup | `memory-df23437` | `memory-df23437` |
| Start round | `landed=1 failed=0 deleted=0`, no WARN/ERROR, `tmp/` empty | `landed=2 failed=0 deleted=0`, no WARN/ERROR, `tmp/` empty |
| `memory check` | exit 0: 1,079 owners, 6,077 sources, 6,077 match, `remote_only=0`, `retired=1791`, withheld 0 | exit 0: 509 owners, 4,531 sources, 4,528 match, 3 behind (live), `remote_only=0`, `retired=1492`, withheld 0 |
| Memory | daemon RSS peak 128,992 KiB (126 MiB) during the check; the check itself 183,744 KiB | daemon `VmHWM` at most 103,932 KiB (101 MiB) at the start round and during the check |

On dev-01 the check's own memory was not measured: the sampler read the pid of its `timeout`
wrapper, not of `memory`.

## Real data (2026-11-24, counts only)

`memory owners --shared` listed 6 shared owners (Claude, Codex and Pi on each host). The installed
binary exported all 6 (exit 0). Only dev-01's Pi owner holds run records: `sources=59
bytes=250982 skipped=229`; the other five export no source (`skipped` 79, 59, 134, 12 and 11:
retired entries). The brief 55 scratch database was copied; its 860 Pi session exports were
imported again (re-derived for `SETUP_VERSION = 3`, status `unchanged`) together with 9 Pi
sessions new on dev-01 since those exports (exported with the installed binary); nothing skipped.
`import_shared` imported all 6 shared exports, and a second round gave 6 `unchanged`.

The first run stored no extension row: `runs_skipped=136`, every extension string. A counts-only
probe on dev-01 showed all 136 are `sha256:` followed by 16 lowercase hex characters, not 64 (the
earlier shape probe collapsed digit runs, so it could not show the length). `1ead95b` accepts 16
to 64 lowercase hex characters and moves `RUNS_VERSION` to 2, so stored owners are re-imported
once (teeth: a 64-only rule, any length, no upper bound, upper case, the version not bumped; 5 of
5 caught). After it, on the same database: 6 owners re-imported then `unchanged`,
`runs_skipped=0`, 136 extension rows (effective 68 with 2 distinct digests, runtime 57 with 1,
configured 11 with 1), effective extensions per run 1 to 2.

| `pi_run` | |
|---|---|
| Rows | 59 (59 distinct run ids), all from dev-01 |
| With model and requested model | 57; requested differs from resolved in 20; 7 distinct models |
| Agent types | 8 distinct |
| Exit codes | 0: 50, 1: 9; `has_error` 9 |
| Acceptance status | attested 25, review-required 14, not-required 11, rejected 7, none 2 |
| Evidence status | attested 25, checked 14, not-required 11, rejected 7, none 2 |
| Review status | 14 (review-required) |
| Runner type | `external-cli` 2 |
| Usage sums | input 5,492,183, output 578,204, cache read 175,585,920, cost 25.99 USD, 1,602 turns |

`pi_run_ref`: tool result 236, workflow child 140, supervisor request 51, control notice 48,
supervisor reply 23, pending 19 rows, naming 265 distinct run ids (most runs referenced in
sessions have no captured record: this Mac keeps none, and older dev-01 records were gone before
capture). 48 of 59 runs link to a session (at most 1 referencing session each). The other 11 are
named only inside text (message text, session names), never in a structured field; the brief's
rule reads structured fields only, so they stay unlinked. The brief's statement that all 59 are
named by structured fields came from a probe that also counted substrings, and is amended there.

Leak check over `pi_run`, `pi_run_extension`, `pi_run_ref` and `pi_run_owner` (6,895 stored
strings): none holds the home path or a space; only `model` and `requested_model` hold a slash
(the provider prefix). The longest string (236 characters) is a `source_id` coordinate in
`pi_run_ref`, as stored in every session table.
