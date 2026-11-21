# Evidence 39: consistent snapshots of the Codex SQLite stores (brief 45)

All dates are UTC, 2026-11-20 23:40Z to 2026-11-21. Status: increment STORES accepted locally at
`79fcad1` (9 worker commits over `9816b6d`). Nothing was installed, sent to R2 or run against a
production state root or a real Codex database during the work; both production daemons kept
running.

[Brief 45](../../45-codex-stores.md) sits on `9816b6d` (on top of `main` `648c25a`). Reason: 873
Codex threads on this Mac survive only in `thread_history_1.sqlite` (handoff item 18), and D55
made dropping `generated_images` depend on that database being captured.

## Commits

| Commit | By | Change |
|---|---|---|
| `916ea7b` | worker | Locator: `logs_*.sqlite` is `UnknownKind`; the walk yields `CodexStore` sources and still defers every other snapshot kind |
| `25fd67e` | worker | `rusqlite` `=0.43.0` (`bundled`, `backup`, no default features, `memory` only); `Encoding::Sqlite` in the real filesystem seam; seam methods `snapshot_fingerprint`, `begin_snapshot`, `end_snapshot`; db plus `-wal` fingerprint, never `-shm`; read-only open, 5 s busy timeout, backup in one step into a new 0600 file under `state/tmp/`, removed after every attempt; `Backup::Changed` maps to `Raced`, `Backup::Failed` to `SourceOutcome::BackupFailed` |
| `9357303` | worker | Piece reuse by transformed digest only (`SourceIndex.snapshot_pieces`, `snapshot_at_ns`, both `#[serde(default)]`; encodings that won a 412 are adopted); the 6 h interval for a changed store (`MEMORY_SIM_SNAPSHOT_INTERVAL` under `sim` only); `SourceOutcome::Deferred` |
| `7c93cc9` | worker | `memory check` for a store: fetch and verify the stored epoch, then compare the local fingerprint with the indexed one (`match`, `behind`, or `unsettled` with no index entry); never live bytes |
| `62e57c8` | worker | Real-binary test: reconcile, check `match`, a change, check `behind`, restore of the shared owner, `integrity_check` `ok` and row counts; `logs_2.sqlite` not captured; `tmp/` empty |
| `f07445e` | worker | Module docs |
| `dab21d0` | worker (parent finding) | A `BackupFailed` outcome bumps the round's `Local` failure count (`local=`); no owner retry or park |
| `750da85` | worker (parent request) | Real binary against the fake S3 store: a re-snapshot after one changed row PUTs only the changed pieces plus manifest and `current.json`, and issues no object GET (about 3.9 s); the `sim` interval override lands, its absence defers |
| `79fcad1` | worker (parent request) | `proptest_sqlite_fingerprint_interval_and_reuse` over the real capture and commit with a simulated seam |

### Parent review findings

- The first round counted `BackupFailed` nowhere: with no other change the owner's round ended
  silently, so a store whose backup always fails would have shown `failed=0 local=0` forever. Fixed
  in `dab21d0`. A failed backup now gives `local=1`; `memory reconcile` still exits 0, because its
  exit contract counts owner failures and parks only.
- The worker's own gaps (no PUT count, interval override untested, no property coverage) were
  closed by `750da85` and `79fcad1`.

### Deviations accepted

- `rusqlite`'s `backup` is its own cargo feature; `bundled` alone does not expose it.
- `run_to_completion(-1)` panics in 0.43.0, so `i32::MAX` pages are passed: still one step.
- The existing capture state machine (`capture_property.rs`) models append sources only. Instead of
  rewriting it, a standalone proptest covers the store fingerprint, the interval and piece reuse.
  No Hegel twin was added; the Hegel suite has no snapshot model.
- The release binary's interval is a `#[cfg(not(feature = "sim"))]` constant. Every test binary
  is built with `sim`, so the non-`sim` path is checked by review only.
- `assemble.rs` (outside the ownership list) builds the filesystem with `SharedFs::with_tmp`.

## Teeth

Each mutation was applied by a script, the store, S3-store, resolver and locator tests were run
(186 tests), and the files were restored (`restored True`). The two `.proptest-regressions` files
the mutated runs wrote were deleted, not committed.

| Tooth | Result |
|---|---|
| `-shm` fingerprinted instead of `-wal` | caught (6 tests) |
| `-wal` left out of the fingerprint | caught (6) |
| source opened with `immutable=1` | caught (6) |
| `logs_*.sqlite` captured | caught (5, including Hegel and proptest locator properties) |
| piece reuse by offset instead of digest | caught (5, including the proptest) |
| interval not applied | caught (3) |
| `sim` interval override ignored | caught (2) |
| temp copy left after a failed backup | caught (3) |
| `end_snapshot` skipped after a capture | caught (7) |
| `check` comparing live bytes | caught (1) |
| `BackupFailed` not counted | caught (2) |

11 of 11 caught.

## Gates

- `PROPERTY_CASES=500 bash scripts/gates.sh` on `79fcad1`, macOS: all 14 sub-gates 0, seed
  3074996175. nextest 844 passed (75 skipped by the gate filter), consumer 3, coverage lines
  95.46 % (floor 80).
- Linux (local Ubuntu 24.04 container, aarch64, Rust 1.99.0): `sqlite_store` and `s3_store` 128
  passed (1 ignored), 10 passed; `core_locator` 16 passed.
- `cargo deny check`: advisories, bans, licenses, sources ok; no licence added. memory-core bans
  ok (`rusqlite` is in `memory` only).

## Install on this Mac and a memory finding

- Stack #219 (PRs #216 to #218) merged as `b20ee05`; Rust gates green on all three runners. The
  release build (sha256 prefix `f0cd8270f255e1ec`) was installed in place at 01:19:17Z
  (`runs = 8`; the `ecc0489` binary kept as `backup/memory-ecc0489`).
- Start round (01:24:27Z): `landed=2 failed=0 local=0`; `tmp/` held at most 606 MB during the copy
  and was empty afterwards. No WARN or ERROR line.
- Finding: the footprint sampler showed `phys_footprint` rising from 42 MB to **561 MB** over about
  80 s of the snapshot, then back to about 40 MB. The 128 MB target (DESIGN 7.3) was exceeded.
- Diagnosis by measurement, not by reading:
  - The SQLite backup alone (rusqlite 0.43.0, the same flags, a copy of the preserved store) peaked
    at 6.5 MB, in one step and in 2,048-page steps alike.
  - An uncommitted probe captured a 308 MB synthetic store through `RealFs` into a sink that drops
    every piece: 327 MB peak through the SQLite read path, 21 MB through the plain read path.
  - The only difference: the SQLite branch of `RealFs::read` read into `Vec::new()` grown by
    `read_to_end`, while the plain branch allocates the exact size (its comment already warned that
    a growing buffer leaves large freed blocks behind). With an exact-size buffer the probe peaked
    at 25.6 MB.
- Fix `90029c4`: exact-size buffer for the copy. Test
  `a_read_of_the_copy_allocates_exactly_the_bytes_it_returns` (capacity equals length for four
  ranges) fails without the fix and passes with it. Gates 14/14 on `90029c4`, seed 211066811,
  nextest 845, coverage 95.46 %.
- dev-01 was not updated with `b20ee05`; it gets the fixed build.

## Limits

- `generated_images` and `attachments` are not captured (D55).
- Analytics does not read the stores; restore is the recovery path for the 873 threads.
- The 6 h interval is a default, not a measurement.
