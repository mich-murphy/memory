# 26. Walking skeleton: one file to an S3 store and back (2026-11-12)

Coordinator brief for plan step 1 of [report 25](25-engineering-practice-review.md). The parent
session owns this file and acceptance. Status: increments 1 to 4 done and accepted locally
(2026-11-12); nothing pushed. Raw records are in
[evidence 16](evidence/16-walking-skeleton/).

## Goal

The first path that runs end to end: one real file is captured, encoded as zstd pieces, written
through a real S3 client with a manifest and `current.json`, read back by `memory-reader`, and
the restored bytes equal the transformed source bytes. First against a local HTTP server, then
once on R2 under `rust-acceptance/<run-id>/` (amendment A2).

## Checkout

- Stack worktree: `/Users/mm/dev/memory-wt/ds-integrate`, a local `gh stack` on `41a07ff`.
  Above `docs/engineering-practice-review` (`f74071f`), bottom to top: `rust/skeleton-deps`
  (`8b41d04`), `rust/zstd-pieces` (`8355cec`), `rust/s3-store` (`7be1de1`),
  `rust/skeleton-e2e` (`8f10261`), `docs/walking-skeleton` (this revision). Local only.
- The worker worktrees `/Users/mm/dev/memory-wt/sk-zstd` and `/Users/mm/dev/memory-wt/sk-s3`
  are detached at `8355cec` and `8f10261` and can be removed.

## Increments

| # | Increment | Owner | State |
|---|---|---|---|
| 1 | Dependencies and the licence exception (`9645e73`) | parent | done |
| 2a | zstd piece encoding in the shell; piece decode and source restore in `memory-reader` (`4ca0298`, `8355cec`) | Sonnet worker A | done |
| 2b | S3 adapter behind `Store`, tested against a local HTTP server (`a8ff0c4`, `7be1de1`) | Sonnet worker B | done |
| 3 | End-to-end test: real filesystem, real adapter, local server, restore through the reader (`0192dc8`, `0a3bc97`, `8f10261`) | Sonnet worker, finished by the parent | done |
| 4 | The same once on R2 under `rust-acceptance/<run-id>/`, prefix deleted and verified empty | parent, after 3 | done |

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

## Result (2026-11-12)

What now exists, on the stack only:

- `memory` stores each piece as a zstd level 3 frame and records the length and SHA-256 of the
  stored bytes in the piece descriptor. The object key is still the digest of the transformed
  bytes before compression (A10.2).
- `memory::s3::S3Store` implements `Store` over `rusty-s3` and `ureq`: path-style URLs, a key
  prefix, one HTTP exchange per call, no retries, a `Debug` that prints no credential.
- `memory-reader` has `decode_piece`, `restore_epoch`, `restore_source` and `read_owner`, which
  walks `current.json` and the manifest chain (at most 64 manifest fetches, each checked
  against its key) and folds it.
- `rust/crates/memory/tests/s3_store/skeleton.rs` drives the real `Daemon` with the real
  filesystem seam, `WalkSources` and `S3Store`: two landing rounds, a read-back through the
  reader, a restart on an empty state root that recovers its base from the store and writes
  nothing, one more landing round and a second read-back. The restored bytes equal the file on
  disk each time.

Checks:

- Gates on `8f10261`: 500 cases, seed 335062796, all 13 sub-gates 0; 471 tests (413 nextest,
  30 Hegel, 28 proptest), 12 doctests; coverage 6140/6290 lines = 97.62 %; the three invariant
  mutants and the window mutant killed; `cargo xtask schemas --check` unchanged;
  `verify-evidence.py` PASS. Not run: 2,000 cases, hosted CI, `cargo mutants`.
- The skeleton test fails when the encoding is the identity, when the reader skips decoding and
  when the adapter ignores its prefix. The reader tests fail when `read_owner` skips the
  manifest hash check.
- Requests the local server saw per phase, as (GET, PUT): round 1 (1, 3), round 2 (0, 3),
  read-back (5, 0), idle restart (3, 0), round 4 (0, 3), read-back (7, 0). In total 16 GET,
  7 `If-None-Match` PUT and 2 `If-Match` PUT for 7 keys.
- R2, run id `20261112-8f10261`: the same test passed in 5.1 s with the same 16, 7 and 2
  requests and 7 keys. The prefix held 0 keys before, the 7 expected keys after, and 0 after
  deletion (7 deleted). The cleanup tool made 5 LIST and 7 DELETE requests. Nothing outside the
  prefix was touched.

Deviations from the increment 3 worker brief, all made by the parent:

- The worker reached its turn limit after committing `read_owner` and writing the test, which
  did not yet pass clippy. The parent finished the test and reran every mutant and check.
- The skeleton is a module of the `s3_store` test binary, not its own file under `tests/`. The
  workspace denies `#[allow]`, so a helper file must be used in full by every test binary that
  includes it (`tests/common/store.rs` states the rule), and the skeleton uses only part of
  the fake server.
- "The marker does not appear in the stored bytes" became "the stored frame holds fewer copies
  of the marker than the raw bytes": zstd keeps the first occurrence as a literal.
- The R2 test counts requests with a wrapper local to the test, not `Counting` from `src`.

Found on the way:

- The fake server stored objects under the undecoded request path. `rusty-s3` encodes `:` as
  `%3A` and session ids contain `:`, so every owner's `current.json` was held under a key no
  real server would use. No adapter test used such a key. Fixed in `0a3bc97`; the R2 listing
  shows the key with a literal `:`.
- Three rustdoc links were unresolved on `main` (`transition`, twice, and `parse_manifest`).
  Fixed in `b96f6d8`. No gate runs rustdoc, so nothing stops this recurring.
- `recover_base` (`rust/crates/memory/src/commit.rs`) and `read_owner` are now the same walk
  written twice. `recover_base` also needs the ETag of `current.json` and its own error
  classes; a fetch closure can supply both, so it looks able to delegate, but that was not
  attempted here.
- Nothing outside `config.rs` reads `Config::bucket`, `Config::token_path` or `Config::roots`
  (`config.rs` only validates them). Building the store and the sources from the configuration
  belongs to the run loop.

What this does not yet give an operator: no command runs the path. `memory daemon` still exits
78 and `memory restore` is not implemented; there is no run loop and no production `Clock`
(the test uses a fixed one). A13.2 still forbids a production write.

Next, in order: (1) the run loop, a production clock and the store and sources built from the
configuration, so `memory daemon` runs the path the test runs; (2) `memory restore` over
`memory-reader`; (3) the four items kept from plan step 2 (audit A12.2, hint retention A11.2,
`tracing` A13.2, startup delay A13.4) before any production write.

## Exclusions

No host changes, no installed hooks or services, no reads of agent transcripts, `.env` or
credential files by the parent or workers, no R2 before increment 4.

## Progress ledger

- 2026-11-12: increment 1 committed (`9645e73`); fmt, clippy, `cargo deny` and core bans pass
  on it. Workers A and B started.
- 2026-11-12: increments 2a and 2b accepted after the parent read both diffs. Identity-encoding
  mutant on `8355cec`: 20 of 75 focused tests fail. `rust/s3-store` rebased onto
  `rust/zstd-pieces`; 461 tests pass on the combined tip. Stale `Store` and crate doc lines
  fixed (`7be1de1`).
- 2026-11-12: increment 3. Rustdoc links fixed (`b96f6d8`); `read_owner` (`0192dc8`, worker);
  fake server decodes the request path (`0a3bc97`, parent); skeleton test (`8f10261`, parent).
  Mutants M1 to M4 killed. Full gates pass on `8f10261`.
- 2026-11-12: increment 4. One run on R2 under `rust-acceptance/skeleton-20261112-8f10261/`
  between 05:29:37Z and 05:30:00Z; prefix deleted and listed empty.
- 2026-11-12: after the skeleton, plan step 3 first batch on `rust/structural-fixes`
  (`6fd536a`): four structural fixes, and a fix for a manifest bound that rejected a launch
  with all nine allowlisted environment names. Full gates pass; record in
  [evidence 17](evidence/17-structural-fixes/).
