# 26. Walking skeleton: one file to an S3 store and back (2026-11-12)

Coordinator brief for plan step 1 of [report 25](25-engineering-practice-review.md). The parent
session owns this file and acceptance. Status: in progress; nothing pushed.

## Goal

The first path that runs end to end: one real file is captured, encoded as zstd pieces, written
through a real S3 client with a manifest and `current.json`, read back by `memory-reader`, and
the restored bytes equal the transformed source bytes. First against a local HTTP server, then
once on R2 under `rust-acceptance/<run-id>/` (amendment A2).

## Checkout

- Stack worktree: `/Users/mm/dev/memory-wt/ds-integrate`. This brief is on
  `rust/skeleton-deps`, above `docs/engineering-practice-review` (`f74071f`). Local only.
- Worker worktrees, both branched from `rust/skeleton-deps`:
  `/Users/mm/dev/memory-wt/sk-zstd` (`rust/zstd-pieces`) and
  `/Users/mm/dev/memory-wt/sk-s3` (`rust/s3-store`). The parent restacks them in that order.

## Increments

| # | Increment | Owner | State |
|---|---|---|---|
| 1 | Dependencies and the licence exception (`9645e73`) | parent | done |
| 2a | zstd piece encoding in the shell; piece decode and source restore in `memory-reader` | Sonnet worker A | started |
| 2b | S3 adapter behind `Store`, tested against a local HTTP server | Sonnet worker B | started |
| 3 | End-to-end test: real filesystem, real adapter, local server, restore through the reader | after 2a and 2b | not started |
| 4 | The same once on R2 under `rust-acceptance/<run-id>/`, prefix deleted and verified empty | parent, after 3 | not started |

## Decisions the parent made for this step

- Licence: `cargo deny` allows `CDLA-Permissive-2.0` for `webpki-roots` only (the open P2
  decision). `advisories`, `bans`, `licenses` and `sources` pass on `9645e73`; the pure-core
  bans still pass; one `base64` version; 107 normal crates under `memory` (P2 measured 99
  without zstd).
- `memory-core` gets no compression dependency. Encoding happens in `memory`, decoding in
  `memory-reader`. The manifest schema does not change: the encoding stays `zstd-3` and no
  `identity` value is added (A13.2).
- The adapter takes a key prefix, empty in production, so acceptance runs stay inside
  `rust-acceptance/<run-id>/` (A2). It has no retry loop of its own (P2 reason 1).
- An HTTP 400 (`BadDigest` after a damaged upload) maps to `StoreError::Server`, which retries
  with the offline backoff. No new `StoreError` variant.
- Not in this step: the upload thread pool, the run loop, config changes, `tracing`, the
  audit, hint retention. A13.2 still forbids a production write until those exist; increment 4
  writes only under the acceptance prefix.

## Acceptance

- `rust/scripts/gates.sh` passes on the top of the stack (parent), with no test, privacy
  assertion, lint or gate weakened and `cargo xtask schemas --check` unchanged.
- Every piece descriptor a capture produces has `encoded_len` and `encoded_sha256` of the bytes
  actually stored, and the stored bytes decode to bytes whose SHA-256 is the piece's `sha256`.
- The adapter's mapping of replies to `StoreError` matches the P2 table, and a commit driven
  through it reaches `AlreadyOurs` after a lost reply and adopts through `known_ours` (A6).
- Increment 3 fails if either the encoding or the adapter is replaced by an identity or a fake.
- Increment 4: synthetic file content only; the run records request counts and the cleanup
  listing; credentials are loaded into the process environment and never printed.

## Exclusions

No host changes, no installed hooks or services, no reads of agent transcripts, `.env` or
credential files by the parent or workers, no R2 before increment 4.

## Progress ledger

- 2026-11-12: increment 1 committed (`9645e73`); fmt, clippy, `cargo deny` and core bans pass
  on it. Workers A and B started.
