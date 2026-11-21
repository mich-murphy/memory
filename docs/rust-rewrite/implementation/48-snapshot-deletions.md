# 48. Slice 3, part 4: deletions of snapshot sources (2026-11-21)

Coordinator brief. The parent session owns this file and acceptance. It follows D52 (brief 43),
briefs 45 and 47 and handoff item 23. Evidence goes to `evidence/42-snapshot-deletions/`. Text that
states a plan stays as written; later changes are marked.

Production daemons run on this Mac (launchd) and on dev-01 (systemd), both on the `36adb8c` build,
while this brief is worked on. Nothing here touches `~/.local/state/memory*`, `~/.codex`,
`~/.claude`, `~/.pi`, `~/Library`, the installed binaries, agent settings, `.env` or R2.

## Why

Since briefs 45 and 47 the archive holds live snapshot entries: the Codex stores (shared owner) and
Claude file-history (805 files on this Mac, 419 on dev-01). D52 records a deletion only for
append sources. Claude's cleanup removes file-history after `cleanupPeriodDays` (default 30), and
a Codex store can be removed or renamed by Codex. Today such an entry stays live in the manifest
forever: `memory check` counts it as `remote_only`, `memory restore` writes a file the host no
longer has, and `memory export` calls it `active`. This brief applies the D52 rules unchanged to
snapshot entries.

## Facts checked on `9353b4c`

| Fact | Where |
|---|---|
| `candidates()` skips every stored entry whose class is not `Append` | `memory/src/daemon.rs` (`candidates`, `entry.class != SourceClass::Append`) |
| `all_gone` keeps an owner in the owner set only while it has a live append entry, so an owner whose last live entries are snapshots leaves the set and is never visited again | `memory/src/daemon.rs` (`all_gone`, the `self.known.remove` in `reconcile_once`) |
| `absent_into` is `stat`-only: no read, no admission; at the second consecutive miss of a complete full round it records `Change::Deleted`; a stat error is not an absence | `memory/src/capture.rs` (`absent_into`, `absence_plan`) |
| `locate_absent` resolves the root by namespace, refuses a missing root or a bad path, and registers the path `Plain` (or `Zstd` for a packed rollout); a `stat` of a `Plain` handle on a SQLite store path is a plain `stat` of the database file | `memory/src/resolve.rs` (`locate_absent`), `memory/src/realfs.rs` (`stat`) |
| `Update::Deleted` clears the local snapshot fingerprint, pieces, time and withheld record | `memory/src/capture.rs` (`Update::apply`) |
| The fold accepts `Change::Deleted` for any class and clears `deleted_at` on a later `Epoch` or `Replace` with the next epoch number | `memory-core/src/manifest/state.rs` (`apply_one`) |
| A withheld file-history version is never in the manifest, so it can never be a candidate | brief 47, D58 |
| Snapshot kinds still deferred or ignored were never captured by the Rust writer, so no stored entry of theirs exists | briefs 46 and 47, evidence 40 and 41 |
| `restore` skips deleted entries; `export` exports a deleted source's epochs `sealed` | `memory/src/restore.rs` (`plan`), `memory-reader/src/export.rs` (D52) |

## Decisions

Each is reversible before merge. A worker that finds one wrong reports it and does not decide.

| Topic | Decision |
|---|---|
| Candidates | Every live stored entry of either class (`Append` or `Snapshot`) that the walk did not yield and that is not parked. All other D52 guards stay as they are: complete walk only, root present, namespace known, path parses, stat error is not an absence, two consecutive full rounds, `capture_owner` never counts absence |
| Owner set | `all_gone` counts live entries of either class. An owner with a live snapshot entry and no walked file is still visited by a full round |
| Probe target | `stat` of `root.join(entry.path)` as today. A Codex store is probed by its database file only; a missing `-wal` or `-shm` is never an absence |
| Recreated file | A snapshot file that comes back at a deleted id is captured as a new epoch (`Replace` with the next epoch number) with a full upload (local pieces were cleared). Proven by a test, for file-history and for a store |
| Withheld record | A scanned file whose index entry holds only a withheld record and that disappears: its local entry is dropped after the same two consecutive misses (no manifest change, it was never published), so `index.json` does not keep records of gone files. A test proves no `Change::Deleted` is built for it |
| Log, status, check | Unchanged: `deleted=` already counts every deletion; `memory check` stops counting a deleted entry as `remote_only` because it is no longer live |
| Out of scope | Purging bytes (never), the remaining deferred snapshot kinds, recording withheld versions in R2 |

## Increment

| # | Increment | Branch | Owner |
|---|---|---|---|
| 1 | SNAPDEL | `rust/snapshot-deletions` | worker (Sonnet, medium) |

Owns `memory/src/{daemon,capture,resolve}.rs`, `memory/tests/**`. Read-only everywhere else; if
`memory-core` must change, stop and report.

Ordered steps, one commit each; `cargo fmt`, clippy `-Dwarnings` and the focused tests green before
each commit:

1. Candidates and the owner set cover snapshot entries. Tests in `tests/daemon.rs` (the `Gate`
   rig): a stored snapshot entry missing for one full round is not deleted, for two is; a
   snapshot file back before the second round is not deleted; an incomplete walk or a missing root
   deletes nothing; an owner whose only live entries are snapshots stays in the owner set until
   they are recorded deleted; `capture_owner` never counts absence for a snapshot.
2. Recreated snapshot file: new live epoch, `deleted_at` cleared. The withheld-only local record is
   dropped after two misses and builds no change. Tests for both.
3. Real binary against the fake S3 store (`tests/s3_store/`): one session with a transcript and a
   clean file-history file, plus a synthetic Codex store in a Codex root. Two reconciles after the
   file-history file and the store are removed: the second round line shows `deleted=2`; `memory
   check` exits 0 with `remote_only=0`; `memory restore` of the session writes the transcript only
   and reports `skipped_deleted=1`; `memory export` of the session still lists the file-history
   source as `sealed`. Then recreate the store: the next reconcile lands it as a new epoch and
   `restore` of the shared owner writes it again with `integrity_check` `ok`.
4. Module docs.

## Rules for the worker

- Cargo only via `nix develop /Users/mm/dev/memory#rust --command sh -c 'cd <worktree>/rust && …'`,
  one cargo job at a time. Combine nextest `-E` conditions into one expression. The consumer gate
  needs `MEMORY_REQUIRE_CONSUMER=1 MEMORY_ANALYTICS_PYTHON=/Users/mm/dev/memory/.analytics-venv/bin/python`.
- No `unsafe`, no new crate. No `git stash`. Never commit `.proptest-regressions`.
- Never touch `~/.local/state/memory*`, `~/.codex`, `~/.claude`, `~/.pi`, `~/Library`, the
  installed binary, `.env` or R2; do not list or read real agent directories. Use temporary
  directories. A production daemon runs on this Mac: do not inspect or signal it.
- No gate is loosened: no timeout raised, no test deleted or weakened, no coverage floor lowered.
  An existing test whose expectation changes because of a decision above is updated with a comment
  naming D52 or this brief; list each one in the report.
- If a decision looks wrong or cannot be implemented cleanly, stop on that point, report it with the
  reason, and do not decide it yourself. Do not edit the brief, DESIGN.md or the handoff.
- Do not run `scripts/gates.sh`. Do not push.
- Report: commits, deviations with reasons, changed existing test expectations, exact commands with
  test counts, `git status --short`.

## Acceptance (parent)

Read the whole diff. Teeth: snapshot entries still skipped as candidates; one miss deletes a
snapshot; the truncated-walk guard dropped for snapshots; the missing-root guard dropped; `all_gone`
back to append-only; a missing `-wal` counted as absence; a recreated store not landed; the
withheld-only record kept forever; `capture_owner` counting absence. Then the full gates once, the
Linux container run of the daemon tests, the hosted gates, evidence 42, DESIGN (6, 7, D59), the
handoff; merge; install on this Mac and dev-01; record two full rounds on each host (`deleted=` is
expected 0, since every captured snapshot file still exists) and `memory check`.

## Ledger

- 2026-11-21 08:00Z: brief written on `9353b4c`.
