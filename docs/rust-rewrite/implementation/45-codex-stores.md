# 45. Slice 3, part 1: consistent snapshots of the Codex SQLite stores (2026-11-20)

Coordinator brief. The parent session owns this file and acceptance. It follows D55
([evidence 38](evidence/38-snapshot-scope/)) and handoff items 18 to 20. Evidence goes to
`evidence/39-codex-stores/`. Text that states a plan stays as written; later changes are marked.

Production daemons run on this Mac (launchd, `ecc0489` build) and on dev-01 (systemd, `769e689`)
while this brief is worked on. Nothing here touches `~/.local/state/memory*`, `~/.codex`,
`~/Library`, the installed binaries, agent settings, `.env` or R2. Every test uses temporary
directories and synthetic databases.

## Why

Handoff item 18: Codex's `state_5.sqlite` lists 902 threads on this Mac, but only 29 rollout files
exist. 873 threads survive only in `~/.codex/thread_history_1.sqlite` (and in the retired Go
export), which the Rust writer never captures: the walk counts every snapshot-class file as
`deferred_snapshot` and skips it. D55 also made dropping Codex `generated_images` depend on this
database being captured. The retired Go directories (47 GB here, 5.8 GB on dev-01) and the two
preserved copies cannot be deleted until it is.

## Facts checked on `648c25a`

| Fact | Where |
|---|---|
| The locator maps any top-level Codex `*.sqlite`, `*.sqlite3`, `*.db` to `CodexStore`, class `Snapshot`, owner `Shared`; that includes `logs_2.sqlite` | `memory-core/src/locator.rs:711-733` |
| The walk counts every `Snapshot`-class source as `deferred_snapshot` and does not yield it | `memory/src/resolve.rs:455-480` |
| `CodexStore` has profile `OtherKnown`: identity transform, `prefix_stable` true, so capture streams one piece at a time | `memory/src/resolve.rs:87-119`, `memory-core/src/privacy/mod.rs:155-165` |
| Capture already has a snapshot path: `SnapshotFingerprint::Plain(id, size, mtime)`, planner `Strategy::Snapshot` (unchanged fingerprint and rules means `Unchanged`), `Mode::Replace` through the streamed write | `memory/src/capture.rs:603-655`, `memory-core/src/planner.rs:172-194` |
| `SnapshotFingerprint::Sqlite { db, wal: WalFingerprint }` exists and is unused | `memory-core/src/domain.rs:518-538` |
| The index keeps `snapshot: Option<(SnapshotFingerprint, Option<RulesDigest>)>` per source and nothing about pieces | `memory/src/daemon.rs:113-124` |
| A piece is always PUT with `If-None-Match: *`; a 412 reads the stored object back to record its encoding. So a re-snapshot of unchanged bytes moves every piece twice | `memory/src/commit.rs:321-410`, DESIGN 7.3 step 6 |
| `.jsonl.zst` rollouts are decoded inside the real filesystem seam (`Encoding::Zstd`, `read_decoded`), so capture never sees the file format | `memory/src/realfs.rs:304-325` (D48) |
| `state/tmp/` exists, is vetted and is emptied at daemon start | `memory/src/state.rs:680-722` |
| `restore` accepts a `shared/<archive>/<machine>/<agent>` owner; `export` refuses shared owners (`owner_shared`) | `memory-core/src/limits.rs:90-120`, `memory-reader/src/export.rs:120-135` |
| DESIGN 7.2: a SQLite fingerprint is db plus `-wal`; 7.3: open read-only, never `immutable=1`, backup API into a 0600 file under `state/tmp/`, `SourceChanged` (retry) and `BackupFailed` (`Local`) kept apart; 13: `rusqlite` bundled, slice 3 | `DESIGN.md` |
| No `rusqlite` in the workspace yet; `cc` is already in the lockfile | `rust/Cargo.lock`, `rust/crates/memory/Cargo.toml` |

## Measurements (parent, 2026-11-20 and 21, counts only)

- This Mac, `~/.codex`: `thread_history_1.sqlite` 615,325,696 B (zstd-3 215,941,387 B),
  `state_5.sqlite` 71,450,624 B (zstd-3 5,762,020 B), `goals_1`, `memories_1`, `queue_1` under
  50 KB each, `logs_2.sqlite` 67 MB plus a 4 MB `-wal` that changes every few minutes. All six are
  in WAL mode.
- dev-01: `thread_history_1.sqlite` 232 MB with a 4.2 MB `-wal` never checkpointed since 11-10;
  `state_5.sqlite` 0.6 MB with a 4.2 MB `-wal`. A fingerprint without the WAL would call a changed
  store unchanged.
- A read-only backup (Python `sqlite3`, `mode=ro`, backup API) changed only the `-shm` mtime of the
  source; the db and the `-wal` kept identity, size and mtime (2 databases, 3 files each). The
  fingerprint must therefore never include `-shm`.
- 8 MiB pieces of `thread_history_1.sqlite` at the same offset, Go export of 2026-10-20 against the
  backup of 2026-11-20: 65 of 74 equal (rows 66,731 to 66,764). `state_5.sqlite`: 3 of 9 equal.
  Same-day backups compared: 74 of 74 (this Mac) and 28 of 28 (dev-01) equal. So most of a
  re-snapshot is bytes already stored.

## Decisions

Each is reversible before merge. A worker that finds one wrong reports it and does not decide.

| Topic | Decision |
|---|---|
| Which stores | Top-level Codex `*.sqlite` whose name does not start with `logs_`. `logs_*.sqlite` becomes `UnknownKind` (brief 46 turns it into `Ignored`). `.sqlite3`, `.db` and `-wal`/`-shm`/`-journal` files are unchanged by this brief. Every other snapshot kind (config, Claude aux, file-history, Pi) stays `deferred_snapshot` |
| Fingerprint | `SnapshotFingerprint::Sqlite { db, wal }`: identity, size and mtime of the db, and of its `-wal` or its absence. Never `-shm`. Unchanged fingerprint and rules: `Unchanged` without opening the store |
| Copy | `rusqlite` (current release, feature `bundled` only, in `memory` only; `memory-core` keeps its I/O ban). Open the source read-only by its admitted path with `SQLITE_OPEN_READ_ONLY` and `SQLITE_OPEN_NO_MUTEX`, never `immutable=1`, a busy timeout of 5 s, and run the backup API with all pages in one step into a new 0600 file under `state/tmp/`. The pieces are read from that file; it is removed after the source's capture ends, landed or not |
| Where | Inside the real filesystem seam, mirroring the `.jsonl.zst` decode (D48): the capture code reads a stable byte stream and never sees SQLite. The simulated filesystem in the tests gets an equivalent so capture properties cover a store whose bytes change between stat and read. If the seam cannot carry it cleanly, stop and report |
| Fingerprint timing | The fingerprint recorded is the one stat'ed before the backup. The copy is consistent at the backup's read transaction; if the store changed during the backup, the next round sees a newer fingerprint and snapshots again. A store whose identity changed or that vanished between stat and backup is `SourceChanged` (a retry, counted like the existing raced outcome); a backup error is `BackupFailed` (class `Local`); both closed codes, no path or text |
| Piece reuse | The index keeps, per snapshot source, the piece descriptors of its last landed epoch (`#[serde(default)]`). A new piece whose transformed SHA-256 equals one of them reuses that descriptor (object key, encoding, encoded length and digest) with no PUT and no GET. Safe because objects are never deleted (DESIGN 19, default 4); `memory check` and `restore` verify every digest. A lost index means one full upload, as today |
| Interval | A store whose fingerprint changed is snapshotted at most once per 6 h after its last landed snapshot (default, unmeasured; `MEMORY_SIM_SNAPSHOT_INTERVAL` under `sim` only). The first snapshot is immediate. Reason: a 615 MB backup on every 15-minute round of an active Codex day writes about 2.4 GB/h locally, while live sessions are already captured from their rollouts; the store matters for threads whose rollouts are gone, which do not change. Inside the interval the source counts as `deferred` |
| `memory check` | For a SQLite store: fetch and verify the stored epoch (every piece digest and the whole digest, as restore does); `match` when the local fingerprint equals the one indexed for that epoch, `behind` when it changed since. Never compare bytes with the live file (a backup is not a byte copy of it). Keep the existing exit-code meaning of `behind` |
| `memory status` | Unchanged except that `deferred_snapshot` no longer counts Codex stores |
| Restore | `memory restore shared/<archive>/<machine>/codex --out DIR` writes each store as a standalone database. A real-binary test opens the restored file, runs `PRAGMA integrity_check` (`ok`) and compares row counts with the source |
| Export, analytics | Unchanged: export refuses shared owners. Reading the 873 threads into analytics is out of scope; restore is the recovery path |
| Memory and disk | Streamed: one 8 MiB piece in memory at a time. The temp copy needs free space equal to the store; `BackupFailed` when it does not fit |

## Increment

| # | Increment | Branch | Owner |
|---|---|---|---|
| 1 | STORES | `rust/codex-stores` | worker (Sonnet, medium) |

Owns `memory-core/src/locator.rs`, `memory-core/src/domain.rs` (only if a type must change),
`memory/src/{resolve,realfs,environment,capture,daemon,check,status}.rs`, `rust/Cargo.toml`,
`rust/Cargo.lock`, `rust/deny.toml` (only if a licence must be allowed; report it),
`memory/Cargo.toml`, `memory/tests/**`, `docs/install.md` (only if an operator-visible line
changes). Read-only everywhere else.

Ordered steps, one commit each; `cargo fmt`, clippy `-Dwarnings` and the focused tests green before
each commit:

1. Locator: `logs_*.sqlite` is `UnknownKind`; the walk yields `CodexStore` sources and still defers
   every other snapshot kind. Tests for both.
2. `rusqlite`, the backup in the real filesystem seam, the SQLite fingerprint, the two closed
   failure codes, temp-file removal; the simulated-filesystem equivalent. Tests: a WAL-mode store
   with frames only in the `-wal` is captured with those rows; a `-shm` change alone is
   `Unchanged`; a `-wal` change alone is a new snapshot; the temp directory is empty after success
   and after each failure.
3. Piece reuse and the 6 h interval (with the `sim` override). Tests: a re-snapshot after one row
   changes PUTs only the changed pieces (count PUTs on the fake store); a lost index uploads
   everything once and lands the same manifest content; inside the interval the source is
   deferred, after it the snapshot lands.
4. `memory check` and restore for a store; real-binary tests against the fake S3 store:
   `memory reconcile`, `memory check` (`match`), a change, `check` (`behind`), `restore` of the
   shared owner, `integrity_check` and row counts.
5. Module docs, and `docs/install.md` if a user-visible line changed.

## Rules for the worker

- Cargo only via `nix develop /Users/mm/dev/memory#rust --command sh -c 'cd <worktree>/rust && …'`,
  one cargo job at a time. Combine nextest `-E` conditions into one expression.
- No `unsafe`. The only new crate is `rusqlite` (and what it pulls in); `cargo deny check` must pass.
- Never touch `~/.local/state/memory*`, `~/.codex`, `~/.claude`, `~/.pi`, `~/Library`, the
  installed binary, `.env` or R2. Do not open any real Codex database. A production daemon runs on
  this Mac: do not inspect or signal it. No `git stash`. Never commit `.proptest-regressions`.
- No gate is loosened: no timeout raised, no test deleted or weakened, no coverage floor lowered.
  Hegel and proptest properties that model snapshots must cover the SQLite fingerprint.
- Report: commits, deviations with reasons, exact commands with test counts, `cargo deny` result,
  `git status --short`.

## Acceptance (parent)

Read the whole diff. Teeth: `-shm` in the fingerprint; `-wal` left out of the fingerprint;
`immutable=1`; `logs_*` captured; piece reuse ignores the digest (reuses by offset); the interval
not applied; the temp file left behind on failure; `check` comparing live bytes. Then the full gates
once, the Linux container run of the store tests, the hosted gates, evidence 39, DESIGN (7.2, 7.3,
13 and D56), the handoff; push, merge, install on this Mac and dev-01; the first snapshot measured
(time, bytes uploaded, peak RSS, temp-file size); restore of the shared owner on each host into a
temp directory with `integrity_check` and row counts against the preserved copies.

## Ledger

- 2026-11-20 23:40Z: brief written on `648c25a`.
- 2026-11-21: STORES accepted locally at `79fcad1` (9 worker commits; the parent found that a
  failed backup was counted nowhere, fixed in `dab21d0`, and asked for the PUT count, the `sim`
  override test and property coverage). Teeth 11 of 11; gates 14/14, seed 3074996175, nextest 844,
  coverage 95.46 %; Linux container 154 store and locator tests passed.
  [Evidence 39](evidence/39-codex-stores/). Next: hosted gates, merge, install on both hosts, the
  first snapshot measured, restore of the shared owner checked against the preserved copies.
- 2026-11-21 01:19Z: merged as `b20ee05` and installed on this Mac; the first store snapshot landed,
  but the footprint peaked at 561 MB. Cause: the SQLite read grew its buffer with `read_to_end`.
  Fixed in `90029c4` (exact-size buffer; probe 327 MB to 25.6 MB), with a regression test; dev-01
  waits for the fixed build. Details in evidence 39.
