# Evidence 52: dead code deleted, stale index records pruned (brief 57)

All dates are UTC. Status: increments RUST and LEGACY accepted locally at `4bc0a86` on
2026-11-25. That is 18 commits over the brief `c695151`, on `main` `81e1f78`. The capture binary
changed; install and the real-data measurements follow merge (section Install, added then).
Increment 3 (shim removal) follows the install on both hosts.

[Brief 57](../../57-dead-code-and-pruning.md) records the audits behind the decisions.

## Before (2026-11-24, counts only)

| | This Mac | dev-01 |
|---|---|---|
| `index.json` | 8,195 records, 8.6 MB | 6,172 records, 6.5 MB |
| Records with `withheld` set | 0 | 1 (a retired Pi run log, `snapshot` also set, so the brief 48 rule never dropped it) |
| `memory check` (installed `07bfc7d`) | exit 0, 6,080 sources, 6,080 match, `retired=1791`, `remote_only=0`; 962 s; peak RSS 193 MiB (`/usr/bin/time -l` on `memory` itself) | exit 0, 4,531 sources, `retired=1492` (brief 56 install) |

## Commits

| Commit | By | Change |
|---|---|---|
| `92b77eb` | RUST worker | `locator::stored_retired(kind, path)`: retired unless in `CAPTURED_KINDS` or a `PiAux` run record; used by `Probe::Retired`, `all_gone`, `check` |
| `1c8eaf6` | RUST worker | `prune_locals`: after a complete, finished full round with every owner base `Known`/`Absent`, keep only walked ids and live non-retired base entries; replaces the brief 48 two-walk rule |
| `e0dbeb3` | RUST worker | SQLite store machinery deleted (`rusqlite`, backup, `Encoding::Sqlite`, `begin/end_snapshot`, `Backup`, `Deferred`, `BackupFailed`, the 6 h interval, `SnapshotFingerprint::Sqlite`, `WalFingerprint`, `judge_store`); `sqlite_store.rs` and `codex_store.rs` tests deleted |
| `34f7f99` | RUST worker | `BRIEF57-SHIM` (`index_shim.rs`): old records load (`snapshot_at_ns` ignored, a `Sqlite` fingerprint loads as no snapshot) and are rewritten without them |
| `5941d2a` | RUST worker | `Profile::Config`, `Profile::TextAsset`, `text.rs`, config scrubbing in `admit`/`transform_owned` deleted; `rules_digest` no longer covers the config table |
| `0eb206e` | RUST worker | Comments citing Go paths reworded; module docs |
| `1b1726c` | parent | `judge_source` (a pass-through after `judge_store` left) inlined; a 100-column fix |
| `b6056c8`, `7d10c30`, `3aefdac`, `577630d`, `80d6470` | sweep worker | Unused `pub` items: `SourceSet`, `request_budget` (s3 has its own `TimeoutPolicy`), key-length helpers, `create_machine_id`, `write_durable`, `tmp_dir`, `sync_policy`, `publish_new`, `StateFile::{Roots,MachineId}`, `resolve_facts`, `STARTUP_*`, `EXIT_ATTENTION`, `withhold_reason`, `Inventory::verified`; daemon test seams gated on `sim` |
| `ca1070e` | parent | `privacy/document.rs` and `MalformedProtected` deleted (see Launch scrub) |
| `7059e0f` | LEGACY worker | `rust.yml` runs the analytics unit suite; new `lint.yml` (zizmor pedantic, actionlint) |
| `bead4ce` | LEGACY worker | Go tree, tooling, `ci.yml`, `release.yml`, setup-go, `.claude/`, `.mcp.json` deleted; flake `default` shell is the Rust shell plus `cargo-mutants` |
| `6839ac4` | LEGACY worker | Go-binary Python tools and the `verify-agent-sessions` skill deleted |
| `a94402a` | LEGACY worker | `tools/analytics` trimmed to the production workflow; `boto3` dropped |
| `4bc0a86` | LEGACY worker | Docs: Go instructions removed, historical banners, analytics README rewritten |

Size: `rust/` 47 files, +656 / -4,629; outside `rust/` 750 files, +232 / -113,343.

## Launch scrub

After the scrubbing removal only `launch.rs` called `document.rs` (`args_scrub`, then the
allowlist). Every value the allowlist keeps must match a shape that excludes `{`, `[` and `=`,
no allowlisted flag is a secret name, and every other argument becomes `[redacted]`. A temporary
differential property test (20,000 cases of generated argv weighted to secret flags, JSON
arguments, headers and `name=value` forms) compared `sanitize_launch` with and without the scrub:
equal in every slot but the program slot, which holds only an allowlisted name or `[redacted]`
either way. The test and the hook were reverted; `document.rs` was deleted. Neither host's
`index.json` holds a `malformed_protected` reason (0 and 0).

## Acceptance

- Diffs read in full by the parent; the worker reports' unverified items checked (step 1
  disagreement: kept kinds never locate to `Ignored`; the new test only widens retirement for
  stored dropped kinds whose path now locates to `UnknownKind`).
- Teeth (`b57_teeth.py`), 12 applied, 11 caught: kept kind retired, any `PiAux` kept, prune
  without the complete-walk gate, prune with an `Unknown` base, prune dropping live base
  entries, prune keeping deleted and retired entries, `all_gone` counting retired entries, shim
  removed, shim keeping `Sqlite` pieces, `Probe::Retired` as `Skip`, `check` counting retired as
  `remote_only`. Equivalent: prune in a stopped round (`finished` dropped). An owner not visited
  in a stopped round has no recovered base on a first round, so `ready` refuses; on later rounds
  its base is still the one this daemon wrote and its walked ids are already in the keep set, so
  the pruned set is the same.
- Gates on `4bc0a86`, `PROPERTY_CASES=500`: 14 of 14, seed 64374776, line coverage 95.61 %
  (floor 80; 93.67 % before `document.rs`, of which it was 40 % covered).
- Linux container on a writable copy of `4bc0a86`, `HEGEL_TEST_CASES` and `PROPTEST_CASES` 500:
  980 passed, 0 failed, 5 ignored.
- Python suite: 121 tests OK, 8 skipped (all `RUST_EXPORT_JOURNEY`, which the consumer gate runs);
  was 216 with 29 skipped. No skip needs a Go binary.
- Deleted tests covered only deleted behaviour; the worker reports list them. The subagent-role
  assertion dropped from `test_events.py` is covered by `test_publications.py`.
