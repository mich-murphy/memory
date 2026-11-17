# Evidence 22: streaming capture, directory reads, round progress, PUT connections, test timeouts (brief 31)

Dates are UTC; all work here is from 2026-11-17. Status: increments TIMEOUT, CONN, DIRSTORM, STREAM and
PROGRESS accepted locally as `ef87ec2` (11 commits over `d7d40cb`). Nothing was installed or sent to R2.
The acceptance run `20261117-a28bfa4` kept running on its own release binary throughout.

[Brief 31](../../31-streaming-and-run-findings.md) sits on `d7d40cb` (on top of `main` `4db2975`).
Seven checks and parent mutants ran per increment, then on the combined tip; the full gates ran once
on `ef87ec2`.

## Commits

`git log --reverse d7d40cb..ef87ec2`. "Worker" is a delegated Sonnet session (medium), "parent" the
coordinating session.

| Branch | Commit | By | Change |
|---|---|---|---|
| `rust/nextest-timeout` | `43d508b` | parent | nextest `slow-timeout`: 3 min per test, 15 min per property test |
| `rust/put-conn-reuse` | `c070b3e` | worker | `put` reads its reply body (at most 64 KiB) so the connection returns to the pool |
| `rust/put-conn-reuse` | `24d2b94` | parent | `get` reads an error reply body (a 404 above all) before returning the error |
| `rust/dir-read` | `2413da3` | worker | a directory event reads only that directory (`Work::read_dir`, `walk_under`) |
| `rust/streaming-capture` | `c336a9c` | worker | audit and adopt reads streamed one piece at a time |
| `rust/streaming-capture` | `3e6ac66` | worker, committed by parent | capture pieces go to a `PieceSink` as produced; `StoreSink` PUTs each at once; the manifest is built after every piece is stored |
| `rust/streaming-capture` | `077bc95` | worker | equivalence property against an oracle and the collect path; stop, failed PUT, 412 |
| `rust/streaming-capture` | `4add674` | worker | one piece alive; audit, adopt and capture reads at most `piece_len` |
| `rust/streaming-capture` | `e450b73` | worker | exact read allocation in `RealFs::read`; by-value transform for streamed chunks |
| `rust/streaming-capture` | `c7eec17` | worker | real-binary peak-memory test |
| `rust/round-progress` | `ef87ec2` | worker | `progress` log line inside a long round |

The branches were first committed on `d7d40cb` each and then rebased into this order without changes
(pre-rebase hashes: CONN `5b3b628`, `8887bb5`; DIRSTORM `83f5be1`; STREAM `482b4eb`, `62b18b8`,
`a18af4e`, `c60b9e1`, `b3aed2b`, `63002f1`; PROGRESS `4106a07`).

## What exists now

- **STREAM (D45).** A prefix-stable source (every source the slice-2 walk yields) is read, authorized,
  hashed, zstd-encoded and PUT one piece (at most 8 MiB) at a time; the head/tail windows are kept as
  edges only. The stop is checked before each piece PUT; a 412 adopts the stored encoding; the manifest
  and `current.json` follow only after every piece of every source is stored. A failed or stopped piece
  stops reading the owner's later sources and builds no manifest. The audit read and the adopt read
  stream with a fresh hasher. Non-prefix-stable sources keep the whole-input path. `transform` behaves
  as before (`transform_owned(bytes.to_vec())`); stored bytes are unchanged (oracle property).
- **DIRSTORM (DESIGN 10.2).** `Create(Folder)` still becomes `DirCreated`; the driver now collects the
  batch's directories (deduplicated) and calls `Work::read_dir` once each. A path that is not a real
  directory (per-component `lstat`) or lies under no root is ignored; otherwise `walk_under` walks only
  that directory with the full walk's limits and rules, and each append source found is noted and
  signalled `Changed`. A truncated read sets the full flag with `Cause::Dir`. No `round` line, the
  15-minute timer is not moved.
- **PROGRESS (DESIGN 11.5).** `progress done= total= landed= failed= elapsed_s=` after an owner's turn
  when 60 s of monotonic time passed since the round started or the last line. No owner, no path.
- **CONN.** ureq 3.5.1 pools a connection only once its reply body is read to the end (`run.rs:733-759`
  `ended` -> `cleanup` `run.rs:622-627`, `pool.rs:183`); a reply with no body is pooled at its head
  (`run.rs:255-257`), so a 200 PUT was already reused. A PUT reply with a body (412 and errors) and a GET
  error reply (404 for an absent `current.json`, once per new owner in the first round) left their
  connections unusable. Both are now read up to 64 KiB. That the 404 path caused the run's "new TLS
  connection about every 10 s" is likely but not confirmed against R2.
- **TIMEOUT.** `[profile.default] slow-timeout = { period = "60s", terminate-after = 3 }`; override for
  `test(/hegel_/) | test(/proptest_/)` with `terminate-after = 15`. Measured: slowest non-property test
  8.9 s; at 500 cases `hegel_property4_capture_state_machine` 105.3 s, the `deltas_from_checkpoint` twins
  54.2 s and 52.5 s.

## Peak memory (real binary, loopback S3 fake, `/usr/bin/time -l` `peak memory footprint`)

Test `s3_store::peak_memory`, limit 128,000,000 bytes, macOS only (the Linux `VmHWM` branch did not run
here).

| Case | Before `e450b73` | After (30 parent runs) |
|---|---|---|
| (a) one 100 MB incompressible source, capture | 138,379,912 | 31,982,048 to 32,031,200 |
| (a) audit round | 129,548,888 | 22,528,456 to 22,626,760 |
| (b) one owner, five 60 MB files, capture | 357,401,488 | 23,888,352 to 24,150,496 |
| (b) audit round | 356,893,608 | 22,741,448 to 22,790,600 |

Before `e450b73` the peak grew per file (60 MB files: 1 -> 88 MB, 2 -> 155 MB, 3 -> 223 MB, 5 -> 357 MB):
`RealFs::read` grew its buffer by doubling and the transform copied each chunk, and the freed blocks
stayed resident. After: flat in file count.

## Checks

Seven checks on `ef87ec2`: fmt, clippy `-Dwarnings`, rustdoc `-Dwarnings`, `cargo deny`, schemas all 0;
12 doctests; nextest 670 passed (non-property). The four mutation predicates each occur exactly once.
Real-binary stress, alone, on `ef87ec2`: the two DIRSTORM event-loop tests 30 runs 0 failed; the
peak-memory test 30 runs 0 failed (12.6 to 18.2 s each).

Full gates on `ef87ec2`: [`gates/`](gates/) (500 cases, seed 391027579, all 13 sub-gates 0, 732 tests
under coverage, coverage 8413/8694 = 96.77 %, floor 80).

## Parent mutants

Each applied alone, the focused tests run, the file reverted with `git checkout`.

| Increment | Mutant | Result |
|---|---|---|
| CONN | `get` error body not read | killed (`ten_gets_of_absent_keys_share_one_connection`, 11 against 1) |
| CONN (worker) | `put` body dropped unread | killed on the 412 half and the small-body test; the 200 half passes by design (empty body) |
| DIRSTORM | `read_dir` sets the full flag | killed (3 driver tests) |
| DIRSTORM | driver ignores truncation | killed (`a_truncated_directory_read_sets_the_flag_with_cause_dir`) |
| DIRSTORM | `plain_dir` check removed | killed (`read_dir_does_not_enter_a_symlinked_directory`) |
| DIRSTORM | walk starts at the root | killed (`walk_under_examines_only_entries_of_that_directory`, `read_dir_finds_the_owner_of_a_new_project_directory_and_notes_it`) |
| DIRSTORM | `WalkSources::read_dir` drops truncation | killed (`read_dir_defers_snapshots_and_reports_a_truncated_walk`) |
| STREAM | streamed read step = whole range | killed (8 tests) |
| STREAM | no stop check before a piece PUT | killed (7 tests) |
| STREAM | audit skips the stat after the read | killed only in the full `memory` run (`audit::a_source_that_moves_between_the_stats_is_ineligible`, `ineligible_audits_are_counted`) |
| STREAM | 412 winner not adopted | killed (5 tests) |
| STREAM | daemon ignores a failed piece | killed (2 daemon tests) |
| STREAM | capture keeps reading after a halted sink | killed (2 `stream_proofs` tests) |
| PROGRESS | `done` off by one | killed (2 tests) |
| PROGRESS | `>` for `>=` | killed (`progress_is_spaced_60_seconds_from_the_last_line_not_the_round_start`) |
| PROGRESS | spacing from the round start | killed (same) |

## Changed existing assertions

- `capture::extend_from_an_empty_capture_reads_nothing_beyond_the_delta`: reads `[0..0, 0..0, 0..10]`
  became `[0..0, 0..0, 0..4, 4..8, 8..10]` (the delta is streamed in `piece_len` 4 chunks).
- `capture::need_windows_reads_head_and_tail_through_the_seam`: the exact planned read became "the reads
  after the two windows are contiguous, start and end at the planned range, each at most `piece_len`".
- `driver::rescan_error_and_new_directory_each_reconcile_once` became
  `rescan_and_error_each_reconcile_once` (the directory case moved to
  `directories_of_a_batch_are_read_once_each_and_nothing_reconciles` and
  `a_truncated_directory_read_sets_the_flag_with_cause_dir`).
- `driver::many_causes_in_one_batch_give_one_reconcile_with_the_first_cause`: expects the read entry, and
  `Cause::Rescan` first because reads run after the batch's inputs.
- `tests/common/log_lines.rs` allowlist gains `progress` (outside PROGRESS's owned list; the shared
  parser rejects unknown events).

## Process notes

- The first STREAM worker stopped at its turn limit with the sink change uncommitted; the parent checked
  and committed it (with the two test adaptations above and a rustdoc link fix) and gave the proofs to a
  fresh worker.
- **Boundary incident.** The proofs worker ran `footprint`, `heap` and `vmmap` against the live acceptance
  daemon (pid 71615); `heap` and `vmmap` suspend their target briefly. The parent checked at 14:00Z: same
  pid, one launchd run, never exited, rounds clean, nothing but `round` and `capture` lines in its log.
  The figure taken from it was voided and the worker measured only daemons its tests spawned afterwards.

## Limits and open findings

- A protected identity matches on the basename, so an append-class file named e.g. `settings.json`
  (possible under `tool-results/`, `debug/` or agent logs) takes the whole-input path and is read whole
  before the 8 MiB `MAX_PROTECTED_BYTES` refusal. Config and TextAsset kinds are all snapshot class and
  deferred by the slice-2 walk. Not tested.
- `memory check` still holds whole sources. No real-binary test of a truncated directory read. The
  peak-memory test ran on macOS only. A new Claude session's `debug` file has no owner until the next
  full reconcile. The CONN cause of the run's per-PUT connections is not confirmed live.
- Not run: 2,000 property cases, `cargo mutants`, a Linux stress run, anything against R2.

## Stack #143 and hosted CI

Pushed as stack #143 (PRs #136 to #142) without a rebase, after a check of the whole range for the
values in `.env` (no added line holds one), and merged into `main` as `2e5f193` on 2026-11-17
(`gh stack merge 143 --yes --merge`). Before the merge, "Rust rewrite gates" succeeded on all six jobs
of its latest attempt on every branch (the Linux jobs, including ARM, all passed under the new
`slow-timeout`), and "CI" failed only in the two accepted `govulncheck` steps on every branch.
Post-merge on `2e5f193`: "Rust rewrite gates" run 44145426842 success; "CI" run 44145426856 attempt 2
failed only in the two accepted steps.

Reruns, each for a reason the ready rule names:

- macOS jobs cancelled with zero steps (no runner) were rerun: "Rust coverage (macos-26)" on
  `docs/brief-31`, `rust/nextest-timeout` (twice) and `rust/streaming-capture`; "Rust gates (macos-26)"
  on `docs/brief-31`; CI "Tests (macos-26)" on `rust/round-progress` (three times), on
  `docs/brief-31-record` and post-merge on `main`.
- **Finding (Go, existing flake):** CI "Tests (macos-26)" step `make crap` failed in the Go e2e test
  `cmd/agent-sessions` `TestScripts/hook_detaches_and_syncs` on `rust/nextest-timeout` (run 44107218200
  attempt 1) and `rust/round-progress` (run 44107297555 attempt 3): the hook printed "coverage meta-data
  emit failed ... rename ... tmp.covmeta... no such file or directory" to stderr, where the script
  expects nothing. The stack changes no file outside `rust/` and `docs/`, the same Go tree passed on the
  other branches, and the same step failed on a Dependabot branch on 2026-11-16 (`316531f`). It looks
  like a race between the hook and its detached sync child writing the same `GOCOVERDIR` metadata. Both
  passed on rerun. Not fixed: the Go hooks remain until cutover; recorded here.
- A local DNS failure for `api.github.com` (about 15:31Z) interrupted `gh run watch`; the acceptance
  daemon logged `offline` failures in the same minute and landed the owner at 15:32:56Z.
