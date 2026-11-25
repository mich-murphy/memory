# 59. The archive is for analytics: remove `restore`, `check` and the read-back (2026-11-25)

Coordinator brief. The parent session owns this file and acceptance. Evidence goes to
`evidence/54-no-restore/`.

## Why

Operator, 2026-11-25: "there does not need to be a restore. we are exporting session to R2 for
wholistic analysis across all development machines. it's not being used to restore any session
history. as long as we have geurantees that the data stroed in R2 is good (the data corrupted on
the way to R2 use case), then this is all we should need. anything else would seem to be
unnecessary code and complexity".

## What guarantees the stored data (kept)

| Guarantee | Where |
|---|---|
| Every PUT carries `content-md5`; R2 rejects a body that does not match (P2: 400 `BadDigest`) | `s3.rs` `put` |
| Pieces and manifests are immutable (`If-None-Match: *`); `current.json` moves only by `If-Match` | `s3.rs`, `commit.rs` |
| Each commit reads `current.json` back and classifies it (landed, ours, foreign) | `commit.rs` `verify` |
| The manifest records each piece's stored digest (`encoded_sha256`) and each epoch's `sha256` | `environment.rs` `Published::piece`, manifest schema |
| The daily audit re-hashes `[0, captured_to)` from disk against the committed digest; a stable mismatch replays the source. It catches writer offset/hash-state bugs and rewrites between windows before wrong bytes are trusted, which `content-md5` cannot see | `audit.rs`, planner |
| `memory export`, the one read path analytics uses, checks every piece's stored and decoded digest and each epoch's digest, and fails closed (`Corrupt`) | `memory-reader` `decode_piece`, `export.rs` `write_body` |

## Decisions

| Topic | Decision |
|---|---|
| `memory restore` | Deleted (subcommand, `restore.rs`, its tests) |
| `memory check` | Deleted (subcommand, `check.rs`, `check_property.rs`, `s3_store/check.rs`, the check peak-memory test). Nothing schedules it on either host |
| Daemon read-back (DESIGN 11.4 item 3) | Never built; its `Fault::ReadbackMismatch` and `ParkReason::RepeatedReadbackMismatch` (never produced, so never persisted) are deleted |
| `memory-reader` | Keeps what export uses: `decode_piece`, `read_owner_head`, `OwnerHead`, `OwnerError`, `ChainFault`, `DecodeError`, `export`. Deletes `read_owner`, `restore_source`, `restore_epoch`, `RestoreError` |
| Shared helpers | Exit codes and `store_line`/`store_exit`/`StoreFault` (from `restore.rs`) and `read_state`/`read_status`/`parked_line`/`Refusal` (from `check.rs`, used by `status`) move to the modules that use them or one small shared module; no behaviour or output change for `export`, `owners`, `install`, `status` |
| Tests | Where a test used `restore` or `restore_source` as its oracle, it uses `memory export` bodies or a test-only decode through `decode_piece`; no assertion about captured bytes is lost. Tests that only tested `check`/`restore` go with them |
| Kept | The audit, `content-md5`, commit verify, export's digest checks, `status`, `owners`, `unpark`, `reconcile` |

## Increments

| # | Increment | Branch | Owner |
|---|---|---|---|
| 1 | RUST: delete restore/check/read-back, move shared helpers, re-oracle tests | `rust/no-restore` | worker (Sonnet, medium) |
| 2 | DOCS: DESIGN body (11.3, 11.4, 12, 13, 15 to 18 text naming restore/check), install guide, READMEs | `docs/no-restore` | worker (Sonnet, medium) |
| 3 | Record, install on both hosts | `docs/b59-record` | parent |

## Ledger

- 2026-11-25: brief written on `b358ebc`.
- 2026-11-25: accepted at `31519cc`: full gates 14/14 (seed 1881541311, 95.77 %), 5 of 5
  teeth caught after the decode tests were restored; evidence 54.
- 2026-11-25: merged as `126ac48` (stack #298) and installed on both hosts; start rounds clean,
  `memory export` exits 0 on both (evidence 54, Install).
