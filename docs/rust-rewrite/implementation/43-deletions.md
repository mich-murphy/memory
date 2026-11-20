# 43. Record deleted sources (2026-11-20)

Coordinator brief. The parent session owns this file and acceptance. It follows brief 42
([evidence 34](evidence/34-operations/)). Evidence goes to `evidence/35-deletions/`. Text that
states a plan stays as written; later changes are marked.

The production daemon (`launchd` label `memory`, `1c5618b` build) keeps running while this brief is
worked on. Nothing here touches `~/.local/state/memory*`, `~/Library/Logs/memory`, the installed
binary, the agents' settings or R2. Every test uses a temporary directory and the local S3 fake.

## Why

Operator, 2026-11-20: "let's fix this" (deleted files are never recorded as deleted, an open item
since brief 31). DESIGN 6 and 9 define the rule: a source missing on two consecutive reconciles is
`Deleted`, recorded in the manifest as `{"op": "deleted", "deleted_at": …}`. Sessions are never
deleted from the store (DESIGN 19): a deletion record says the local file is gone, not that its
bytes are dropped.

## Facts checked on `4d3bcba`

| Fact | Where |
|---|---|
| Capture already implements the rule: `step` with `stat == None` goes to `absence_plan`, which counts `Update::Missing(id, n)`; at `n >= 2` (`Observation::missing`) and a live remote entry it pushes `Change::Deleted { id, kind, deleted_at }` and `Update::Deleted`. The remote entry is authoritative for "already recorded" | `memory/src/capture.rs:430-500`; `memory-core/src/planner.rs:60-74` |
| **The gap:** `Daemon::reconcile_once` builds `groups` from `self.sources.enumerate()` only. `WalkSources::enumerate` yields files that exist. A deleted file is never handed to `capture_into`, so `missing` never counts and no deletion is ever committed | `memory/src/daemon.rs:1101-1120`; `memory/src/resolve.rs:404-495` |
| `step` calls `admit` before `stat`; for a missing path `resolve()` gives `path_resolved = false` and `admit` returns `Unknown(PathUnresolvable)`, so even a missing source handed to `step` would be `Blocked`, not counted | `memory/src/capture.rs:455-485`; `memory/src/resolve.rs:135-190`; `memory-core/src/privacy/mod.rs:284-292` |
| An owner whose every file is gone yields no walk row, so it is never visited; `IndexBody` has `sources` (by `SourceId`, no owner) and `hints` only | `memory/src/daemon.rs:104-126` |
| The stored owner state (`Base.state: OwnerState`) holds every source `{ kind, path (root-relative, latest), class, epochs, deleted_at }`; `run_owner` recovers it (`recover`, cached as `RemoteBase::Known`) before `capture_sources` | `memory/src/commit.rs:37-44`; `memory-core/src/manifest/state.rs:10-40`; `memory/src/daemon.rs:829-886, 951-1003` |
| Every `SourceId` starts with its root's namespace and `:` (also hashed ids, `<ns>:h:<hex>`); `Roots` holds the namespace but has no public accessor | `memory-core/src/locator.rs:105-120, 195-220, 438-458` |
| Fold: `Change::Deleted` on a live entry sets `deleted_at` (errors on a missing, kind-changed or already deleted entry); a later `Epoch` on the same id clears `deleted_at` | `memory-core/src/manifest/state.rs:122-166` |
| Restore and export skip deleted entries (`skipped_deleted`); export marks the last epoch `active`, older ones `sealed`; the importer accepts any non-empty `state` string | `memory/src/restore.rs:125-148`; `memory-reader/src/export.rs:140-225`; `tools/analytics/publications.py:120-175` |
| `RealFs::stat` returns `Ok(None)` for `NotFound` | `memory/src/realfs.rs:274-300` |
| Walk counts: `truncated` (entry bound hit), `busy`, `unreadable` | `memory/src/resolve.rs:280-302`; `memory/src/realfs.rs:364-380, 451-480` |
| Production today: `memory check` `remote_only=0`, so no deletion is pending on this Mac | evidence 31, checks log 2026-11-19 |

## Decisions

Each is reversible before merge. A worker that finds one wrong reports it and does not decide.

| Topic | Decision |
|---|---|
| Where absence is observed | Only in a full round (`reconcile_once`): "two consecutive reconciles" (DESIGN 6). `capture_owner` (event-driven, `of(owner)`) is unchanged and never counts absence |
| Candidates | For each owner the round visits, after its base is recovered: every stored entry with `deleted_at == None`, class `Append`, whose id is not among the owner's walked sources and not a parked source. Snapshot-class entries are never candidates (slice 3) |
| Where a candidate is | Root: the configured root whose namespace is the id's prefix before the first `:` (add `Roots::namespace()` in `memory-core`). Path: `root.join(entry.path)`. An id whose namespace matches no configured root, or a path that does not parse as a `RootRelativePath`, is not a candidate (counted, see Log) |
| Guards (no false deletion) | No candidate at all in a round whose walk was `truncated` or `busy`. No candidate of a root whose directory does not exist or is not a directory at the start of the round (an unmounted or moved root never deletes). A stat error other than `NotFound` is not an absence (no count change) |
| Absence probe | New `capture::absent_into` (or a flag on the call), separate from `step`: per candidate, `stat` only (no read, no `admit`: nothing leaves the host but the stored id, kind and a timestamp, all already in the manifest). `None`: `absence_plan` as today (count; at the second consecutive miss with a live entry, `Change::Deleted`). `Some`: no change this round (the walk will yield it, or it is excluded for another reason; the miss count is reset with `Update::Present`). The changes join the owner's commit with any capture changes |
| Owners with no walked file | `IndexBody` gains `owners: BTreeSet<Owner>` with `#[serde(default)]` (an old `index.json` still loads; schema stays `memory.index/1`). A full round adds every walked owner; owners in the set but not walked are visited with an empty walked list (their base is recovered, one GET, then cached). An owner whose stored state has no live append entry left is removed from the set. Owners deleted before this build, or while `index.json` was lost, are not found (the store has no LIST): accepted, same limit as `memory check` |
| Recreated file | A file that comes back at a deleted id is captured as a new epoch (`deleted_at` cleared by the fold). Proven by a test |
| Restore | Unchanged: skips deleted sources (it reproduces the local disk) |
| Export | Changed: a deleted source's epochs are exported, every one `sealed` (none `active`), because sessions are never deleted and analysis must still see them. `Envelope.skipped_deleted` becomes `deleted` (count of deleted sources exported); the stderr line becomes `memory: exported sources=<n> deleted=<n> bytes=<n>`. No new envelope key. The consumer gate must import such an envelope; if the importer rejects a source with no `active` epoch, stop and report |
| Log | The `round` line gains `deleted=<n>` (deletions in commits that landed this round) and `absence_skipped=<n>` (candidates not probed: unknown namespace, bad path, missing root). `log.rs` table, `log_lines.rs` `ROUND_KEYS` learn them. No path or id is logged |
| `memory status` | Unchanged |
| Out of scope | Snapshot deletions, purging bytes (never), the Linux install |

## Increment

| # | Increment | Branch | Owner |
|---|---|---|---|
| 1 | DELETE | `rust/deletions` | worker (Sonnet, medium) |

Owns `memory/src/{daemon.rs,capture.rs,resolve.rs,log.rs,export.rs}`, a `namespace()` accessor in
`memory-core/src/locator.rs`, `memory-reader/src/export.rs`, `tests/common/log_lines.rs`, tests
under `memory/tests/` and `memory-reader/tests/`, and the consumer gate test if it needs a
deleted-source case. Read-only everywhere else.

Ordered steps, one commit each; `cargo fmt`, clippy `-Dwarnings` and the focused tests green before
each commit:

1. `Roots::namespace()`, the candidate selection and the absence probe in capture, wired into
   `reconcile_once` with the guards. Tests (daemon-level with a closure `Sources` or the S3 fake,
   whichever is closer to existing tests): a captured file deleted is recorded deleted after the
   second full round and not after the first; a file deleted then recreated before the second
   round is not deleted; a missing root directory deletes nothing over three rounds; a truncated
   walk deletes nothing; a stat error other than `NotFound` deletes nothing; a parked source is
   never a candidate; a recreated file after a recorded deletion gets a new epoch and is live;
   `capture_owner` never counts absence.
2. `owners` in `index.json`: an owner whose every file is deleted is recorded deleted after two
   rounds and then leaves the set; an old `index.json` without `owners` loads (`state_load` not
   damaged); a restart keeps the set.
3. Log keys `deleted` and `absence_skipped`; a real-binary test (`s3_store`): daemon or
   `memory reconcile` twice after deleting a captured file, stderr lines parse, `deleted=1` on the
   second round, the stored entry has `deleted_at`, `memory check` then reports `remote_only=0`,
   and `memory restore` skips it.
4. Export of deleted sources (`sealed`), `deleted=` in the stderr line, the reader unit tests and
   the consumer gate (`MEMORY_REQUIRE_CONSUMER=1`) importing an envelope with a deleted source.
5. Docs in code (module docs of `capture.rs`, `daemon.rs`, `export.rs`).

## Rules for the worker

- Cargo only via `nix develop /Users/mm/dev/memory#rust --command sh -c 'cd <worktree>/rust && …'`,
  one cargo job at a time. Combine nextest `-E` conditions into one expression.
- No `unsafe`. No new crate. Never touch `~/.local/state/memory*`, `~/Library/Logs`,
  `~/Library/LaunchAgents`, agent settings, the installed binary, `.env` or R2. A production daemon
  runs on this Mac: do not inspect, signal or run any command against its state root. Every
  real-binary test clears the environment and sets `MEMORY_STATE_DIR` to a temporary directory.
  No `git stash`. Never commit `.proptest-regressions`.
- No gate is loosened: no timeout raised, no test deleted or weakened. The capture property and
  its scripted tests must stay green.
- Report: commits, deviations with reasons, exact commands with test counts, `git status --short`.

## Acceptance (parent)

Read the whole diff. Teeth: one miss deletes; the truncated-walk guard removed; the missing-root
guard removed; a stat error treated as absence; parked sources probed; candidates include walked
ids (a live file deleted); the owner set never filled (an all-deleted owner never recorded);
export drops deleted sources again; export marks a deleted source's last epoch `active`;
`deleted` not counted in the round line. Then the full gates once, evidence 35, DESIGN 6.4, 9.1,
9.2 and D52, the handoff, push, hosted gates, merge, install in place, then `memory status` and the
next `memory check` against production read-only.

## Ledger

- 2026-11-20 08:15Z: brief written on `4d3bcba`.
