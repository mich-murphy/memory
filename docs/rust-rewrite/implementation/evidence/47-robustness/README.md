# Evidence 47: store causes, a retry budget and a race-free check (brief 52)

All dates are UTC, 2026-11-22. Status: increments STORECAUSE, RETRYBUDGET and CHECKCONFIRM
accepted locally at `034be38`. That is 13 commits (9 by workers, 4 by the parent) over the brief
`2bcb2cc`, on `main` `4d6729f`. Nothing was installed and nothing was sent to R2 during the work.
No worker read a production state root or real agent data. Both production daemons kept running
on `ecb1d74`.

[Brief 52](../../52-robustness.md) records the research behind the decisions. The research covered
DDIA (2nd edition, chapters 1 to 8), the AWS Builders' Library and the AWS retry-throttling note,
Google SRE "Handling Overload", sysexits(3), and the S3 and R2 consistency documents. Each finding
was reproduced on the test rigs and its options were prototyped.

## Commits

| Commit | By | Change |
|---|---|---|
| `76b06a5` | worker | STORECAUSE. `export`, `restore` and `owners` keep the `StoreError` and print `memory: store cause=<code>`. `store_exit` is an exhaustive match: 75 for `Offline`, `Ambiguous`, `Throttled` and `Server`; 77 for `Auth`; 1 for `PreconditionFailed` and `NotFound`, and for a fetcher with no typed error (`store` alone). `map_list_reply` returns `Offline(Cause::Protocol)` for an oversized, non-UTF-8 or unparsable 200 body |
| `0265fea` | worker | A test table on the real binary over the loopback fake (`tests/s3_store/errors.rs`). A no-wildcard witness match over `StoreError` and `Cause`. Every cause line is checked to be closed text without input |
| `cc1d6a4` | worker | Module docs and the three `main_entry` exit tables |
| `8ad3ee9` | parent | Doc lines wrapped at 100 columns |
| `153fc1c` | worker | RETRYBUDGET. A host token bucket in `daemon.rs`, in memory only. It has capacity 10, refills one token per 15 s of the monotonic clock and one per landed commit, and is capped at 10. A retry (streak > 0) takes a token. Without one, the owner is deferred to now plus `jittered(30 s)` from the D44 draw and counted `deferred`. First attempts, parked owners, owners not yet due and a `LandingUnknown` verify are never charged. `capture_owner` reports `Deferred` for a denied retry (it was `Unchanged`), so nothing is logged for it |
| `205d398` | worker | Seven daemon tests (the bound, refill on landing, first attempt, deferral at most 30 s, `Retry-After` kept, `LandingUnknown` verify, auth pause, a new daemon starts full) |
| `7ef91d6` | worker | Module docs |
| `5052026` | parent | Three tests for the surviving teeth: denied retries are spread over the window; a full round with an empty bucket keeps a later `Retry-After`; a parked owner with a streak stays parked |
| `9166fd8` | worker | CHECKCONFIRM. After the judge, an owner with an `uncaptured` or `mismatch` verdict has `current.json` read once more. If the head moved, only those sources are judged again against the fresh chain, and the fresh verdict replaces the first. `remote_only` candidates are confirmed by a second walk after the last owner read. The hook (`check_with`, `Phase`) is public only with the `sim` feature. `check()` and its output are unchanged |
| `a9fc9a4` | worker | Seven real-binary or in-process race tests, including a count of the requests sent to the fake |
| `52e351c` | worker | Module docs |
| `c73851a` | parent | Test comment wrapped |
| `034be38` | parent | Test for the surviving tooth: a rewrite captured between fetch and judge is not a `mismatch` |

Deviations, all accepted:

- **Unparsable list body.** The brief said `cause=protocol` with exit 1. No `StoreError` has that
  code and exit 1 without a new variant, which the daemon and `check` would see. The worker stopped
  and reported, and the parent chose 75 (option B). These commands only read, so a retry is safe,
  and a garbled 200 is most likely a truncated reply or a proxy. Only `owners` calls `list_page`.
  The brief row is marked as amended.
- **`capture_owner` outcome.** A denied retry returns `Deferred`, not `Unchanged`. The only caller
  logs nothing for `Deferred`, as it already did for an owner whose own deadline had not passed.
- **`index.json` read in `check`.** Not changed. It is checksum-guarded and written by rename, so a
  torn read gives `None`, which only the Codex-store judgement uses and turns into `unsettled`. That
  is never a failure.
- **`check_inner`** uses `#[expect(clippy::too_many_lines, reason = …)]`, because the repository's
  `allow_attributes` lint refuses `#[allow]`.

Changed existing expectations (each commented with brief 52):

- `s3::oversized_body_is_server` and `unparsable_body_is_server` are renamed to `…_is_protocol` and
  now expect `Offline(Protocol)`.
- `owners::owners_a_500_fails_as_store` now expects `store cause=server` with exit 75.
- `owners_a_403_fails_as_store` now expects `store cause=auth` with exit 77.
- `owners_unparsable_body_fails_as_store` now expects `store cause=protocol` with exit 75.
- `restore::errors_and_codes_carry_no_input_text` now uses `Store(None)`.

## Exit codes of `export`, `restore` and `owners`

| Fault (fake server) | Before | After |
|---|---|---|
| 503 with `Retry-After` | 1 `store` | 75 `store cause=throttled` |
| 500 | 1 `store` | 75 `store cause=server` |
| Reply dropped | 1 `store` | 75 `store cause=reset` |
| Connection refused | 1 `store` | 75 `store cause=refused` |
| 403 | 1 `store` | 77 `store cause=auth` |
| Piece 403 / 503 / dropped | 1 `store` | 77 / 75 / 75 |
| `owners` 403 / 500 / 503 / unparsable body | 1 `store` | 77 / 75 / 75 / 75 `cause=protocol` |
| `owners` 404 | 1 `store` | 1 `store cause=not_found` |
| Piece 404, wrong digest, `current.json` 404 | 1 `object_missing`, 1 `source_corrupt`, 66 `owner_absent` | unchanged |

## Measured on the rigs

- **Retry budget.** One round in which every owner fails `Offline`, then 60 s of the driver's due
  captures. Retried store requests: 13 at 20 owners and 13 at 200 owners, where the bound is
  10 + 60/15 = 14. The research simulation without the budget gave 148 and 1,472 requests in the
  first minute.
- **Check cost.** A quiet owner costs 3 GETs (`current.json`, one manifest, one piece), the same as
  before. An owner with an anomaly and an unchanged head costs one more `current.json` GET. The
  chain and the pieces are fetched again only when the head moved. The second walk sends no
  request. The 100 MB peak test still passes (about 43 MB).
- **Race reproduction.** The 2026-11-21 false `remote_only` (a commit between walk and fetch) and a
  false `uncaptured` with exit 1 (a commit between fetch and judge) both come out clean. A file
  really gone locally is still `remote_only`, a never-captured file is still `uncaptured` with
  exit 1, and a corrupted piece is still a `mismatch` even when the owner moved.

## Parent acceptance

- Every diff read. The parent also checked:
  - A paused host spends no token: `capture_owner` returns `Paused` first, and `reconcile_once`
    clears the pause before its loop.
  - A `Cancelled` disposition keeps the `LandingUnknown` flag.
  - The bucket acts only on owners already due.
- Teeth (`b52_teeth.py`, three focused filters; baselines 38, 137 and 28 tests): 29 of 33 caught
  at first. Four survived:
  - The deferral was not jittered.
  - The bucket ignoring an owner's own `RetryAt` in a full round.
  - The bucket ignoring parking.
  - The confirmation not covering `mismatch`.

  The parent added a test for each (`5052026`, `034be38`), each failing without the fix. After
  that, 33 of 33 were caught:
  - STORECAUSE: a cause dropped at each of the 5 fetch sites, 75 and 77 swapped, auth mapped to 75,
    `Server` mapped to 1, each of the 3 exit mappings ignored, the list body as `Server`.
  - RETRYBUDGET: first attempts charged, `LandingUnknown` charged or never flagged, refill on
    landing dropped, refill by time dropped, refill too fast, capacity 1000, starting empty,
    deferral unbounded or not jittered, `RetryAt` shortened, parked owners charged, the `Deferred`
    outcome dropped.
  - CHECKCONFIRM: confirmation dropped for `uncaptured` or for `mismatch`, the second head ignored,
    the fresh chain unused, `remote_only` unconfirmed or all dropped, a confirmed defect dropped, a
    quiet owner confirmed.
- **Gate failure unrelated to brief 52.** Gate `03-deny` failed on `RUSTSEC-2026-0592` (`url` 2.5.8,
  a stack overflow in `Url::origin()` on nested `blob:` URLs), which also affected `main`. It was
  fixed separately: PR #252 bumps the lockfile only (`url` 2.5.9, `form_urlencoded` 1.2.3). Local
  `cargo deny` ok and nextest 911 passed. Merged as `4d6729f`, and the stack was rebased onto it.
- Full gates on `034be38`: 14 of 14. Seed 2084831141, nextest 933 passed, Hegel 33, proptest 36,
  consumer 3, line coverage 95.68 %. Clippy `-Dwarnings` passes with and without `memory/sim`.
  (Corrected 2026-11-23: wrong. Both clippy runs, like every gate, built `memory` with `sim`,
  because the dev-dependency `memory = { features = ["sim"] }` turns it on under
  `--all-targets`. The non-`sim` `phase_enum!()` did not compile, so `main` `25fae57` passed
  every gate and hosted CI but failed `cargo build --release`. Nothing was installed from it.
  Fixed in the follow-up below.)

## Follow-up, 2026-11-23: the release build

- Cause: `macro_rules! phase_enum { ($vis:vis) => … }` invoked as `phase_enum!()`. An empty
  `vis` fragment at the end of the input does not match. With the macro fixed, clippy's
  `enum_variant_names` lint fired on the now-private `Phase` (every variant started with
  `After`); a `pub` enum is exempt from it, which is why the `sim` build never saw it.
- Fix: the macro takes `$($vis:tt)*`, and the variants are `Walked`, `Fetched` and `Judged`.
- Gate: `02-clippy` also runs `cargo clippy --workspace --lib --bins --locked -- -Dwarnings`.
  With resolver 2 that pass builds without dev-dependency features, as the release build does.
  It failed on `25fae57` (exit 101) and passes on the fix. The gate count stays 14.
- Linux container (Ubuntu 24.04), `cargo test -p memory`, all passed: lib 10, `s3_store` 146
  (1 ignored), `daemon` 105.
