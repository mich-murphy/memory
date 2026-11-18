# 34. Streamed `memory check` (2026-11-18)

Coordinator brief. The parent session owns this file and acceptance. It follows
[brief 33](33-owner-listing.md). Evidence goes to `evidence/25-streamed-check/`. Text that states a
plan stays as written; later changes are marked.

The acceptance run `20261117-a28bfa4` keeps running while this brief is worked on. Its check loop
runs the run's own release binary, so nothing here changes it. Nothing is installed, sent to R2 or
run against the run's state root.

## Goal

`memory check` still holds whole sources (brief 31 open finding; evidence 22). For each local
source it reads `[0, size)` into one buffer (`read_settled`), copies it through `publishable`, and
restores the whole latest stored epoch into a second buffer (`restore_source`). One 100 MB
transcript therefore costs at least 200 MB. The short post-run run on real data measures peak
memory and runs `memory check`. This brief makes the check stream like capture and audit do
(brief 31, STREAM), so a large source cannot skew that run.

**CHECK**: for a prefix-stable source, `judge` streams the local file and the stored epoch side by
side: one local chunk (at most one piece, 8 MiB) and one stored plus one decoded piece alive at a
time. Its verdict, reason code included, is the one the current whole-buffer `judge` gives for the
same input. Sources that are not prefix-stable keep the whole path unchanged. Their transform scans
the whole document, and the protected ones are capped at 8 MiB (`MAX_PROTECTED_BYTES`).

Not in this brief: the open finding that an append-class file with a protected basename takes the
whole-input path before the 8 MiB refusal (capture, not check); output format changes; the
`remote_only` gap, which `memory owners` could close later.

## Checkout

`main` is `b3a08d2`. This brief is on `docs/brief-34`. The worker gets `../memory-wt/b34-check` on
`rust/streamed-check` from `docs/brief-34`. Crate paths below are under `rust/crates/`.

## Facts checked on `b3a08d2`

| Fact | Where |
|---|---|
| `judge`: admit (deny or unknown -> `Withheld`); `read_settled` (`None` -> `Unsettled`); `publishable` on the whole buffer (error -> `Withheld`); no live entry -> `Uncaptured`; `restore_source` (fetch `NotFound` or any decode, gap, size or digest error -> `Mismatch("restore")`, other fetch error -> `StoreFailed`); then `compare` (`Match`, `Behind`, `Unsettled`, `Mismatch("bytes")`) | `memory/src/check.rs:235-273` |
| `read_settled`: stat, read `[0, size)`, stat; `None` on a seam error, a short read or a stat change | `memory/src/check.rs:226-233` |
| `compare(restored, local, prefix_stable)` | `memory/src/check.rs:58-75` |
| `restore_epoch` checks contiguity from 0 per piece, then the epoch's `size`, then its `sha256`; `decode_piece` checks stored length, stored digest, zstd, raw length and raw digest | `memory-reader/src/lib.rs:68-122` |
| `prefix_stable()` is true only with no recognized protected identity and a profile other than `Config`/`TextAsset`; then `transform_owned` is the identity | `memory-core/src/privacy/mod.rs:155-164`, `:345-367` |
| `authorize` depends only on the binding and `admit(context, facts)`, never on the bytes, so for a prefix-stable source every chunk, and the empty buffer, gets the same outcome | `memory-core/src/privacy/mod.rs:370-387` |
| Capture and audit already stream prefix-stable sources one piece at a time (`write_streamed`, `stream_digest`); `capture.rs` authorizes an empty buffer when no chunk was read | `memory/src/capture.rs:714-800`, `:875-908` |
| `MAX_PIECE_BYTES` is 8 MiB; the daemon uses it as `piece_len` | `memory/src/capture.rs:67`; `memory/src/daemon.rs:854` |
| Real-binary check tests use `daemon_restore::setup`; the daemon peak test measures under `/usr/bin/time -l` (macOS) or `VmHWM` (Linux) against `LIMIT = 128_000_000` | `memory/tests/s3_store/check.rs`; `memory/tests/s3_store/peak_memory.rs` |

## Decisions

Each is reversible. A worker that finds one wrong reports it and does not decide.

| Topic | Decision |
|---|---|
| Split | `judge` dispatches on `admitted.prefix_stable()`: streamed path, or the current whole path unchanged (moved to a function the tests can call as the oracle) |
| Seam | Both paths are generic over `F: FileSystem` and a fetch closure `FnMut(&str) -> Result<Vec<u8>, StoreError>`, so a property can drive them without a socket. `Verdict` becomes a public closed enum, `#[non_exhaustive]` not needed |
| Chunk | The streamed path takes the chunk length as a parameter; production passes `MAX_PIECE_BYTES`; tests use small values to cross boundaries |
| Equivalence | For every input, the streamed verdict equals the whole verdict, with the same reason code. To get this the streamed path always drains both sides: it reads the local file to its stat size and fetches and decodes every stored piece, even after the first difference |
| Precedence (as today) | admission withheld > local unsettled (seam error, short read, stat before != stat after) > publishable withheld > uncaptured > restore failure (`StoreFailed` for a non-`NotFound` fetch error, `Mismatch("restore")` otherwise) > byte comparison |
| Authorization | The empty buffer is authorized first; its outcome decides `Withheld`. Every chunk is still passed through `publishable_owned` before it is compared (as capture does); a chunk outcome that differs from the empty one is `Unsettled` (cannot happen today; it is the defensive answer) |
| Restored checks | Contiguity per piece, then the epoch `size` and `sha256` over the decoded stream (incremental `sha2`), exactly as `restore_epoch` checks them |
| Comparison | Equal length and all bytes equal: `Match`; local longer and the stored stream is its prefix: `Behind`; anything else: `Mismatch("bytes")` (prefix-stable is always true on this path, so `Unsettled` from `compare` cannot arise) |
| Memory bound | At most one local chunk, one stored piece and one decoded piece alive at once |

## Increment

| # | Increment | Branch | Owner |
|---|---|---|---|
| 1 | CHECK | `rust/streamed-check` | worker (Sonnet, medium) |

Owns `memory/src/check.rs` (and its module comment), new tests in
`memory/tests/s3_store/check.rs` and a new property test file `memory/tests/check_property.rs`
(or a module in an existing test binary if helpers make that simpler). Read-only everywhere else.
If a helper elsewhere has to become `pub`, report it.

- Proof (each a named test):
  - **`hegel_streamed_check_verdict_equals_whole`**: generated local bytes; a stored epoch built from
    related bytes (equal, a strict prefix, longer, one byte different at a generated offset, empty)
    with generated piece lengths; chunk length 1 to 64; optional faults: a stored piece missing
    (`NotFound`), a fetch `Server` error, a corrupted stored byte, an epoch whose recorded `sha256`
    or `size` is wrong, a local seam error at a generated offset, a short local read. The verdict
    of the streamed path equals that of the whole path. Under `gates.sh` it runs at
    `PROPERTY_CASES`.
  - A file that changes between the stats is `Unsettled` on the streamed path.
  - A difference only after the local end, together with a corrupt later piece, gives `restore`, not
    `bytes` (the drain rule).
  - Peak memory, real binary: after the daemon captured one 100 MB incompressible prefix-stable
    source, `memory check` reports `match=1` and its peak is below 128,000,000 bytes. macOS:
    `/usr/bin/time -l`, `peak memory footprint`; Linux: poll the child's `VmHWM` until it exits and
    assert on the last reading. Name it so that it runs in 04 (no `hegel_`, `proptest_` or
    `consumer_gate_` in the name).
  - All existing check tests still pass unchanged.
- Teeth (the parent applies each; each must fail a named test): local read whole on the streamed
  path; stored epoch restored whole; epoch `sha256` check dropped; stop at the first difference
  without draining; stat-after removed; `Behind` without the prefix comparison; a chunk compared
  against the wrong offset (off by one at a boundary).

## Rules for the worker

The rules of [brief 28](28-production-prerequisites.md#rules-for-workers) apply, with those of
briefs 29 to 33. No new dependency. No change to what any state file, manifest or object stores,
or to `memory check` output. Never inspect, signal or read the state of the running acceptance
daemon or anything under `~/.local/state/memory-acceptance*`; never read `.env`; never contact R2;
never use `git stash`. Cargo only through
`nix develop /Users/mm/dev/memory#rust --command sh -c 'cd /Users/mm/dev/memory-wt/b34-check/rust && ...'`,
one cargo job at a time. Commit after each working step. Stress the new real-binary test 30 times
alone and report failures.

## Acceptance (parent)

Whole diff read; fmt, clippy, rustdoc, nextest, doctests, schemas, `cargo deny`, lints; the teeth
above; `scripts/gates.sh` once on the tip with `MEMORY_ANALYTICS_PYTHON` set; push (secret check
of the range), hosted gates, merge.

## Ledger

- 2026-11-18 06:20Z: brief written on `b3a08d2`.
