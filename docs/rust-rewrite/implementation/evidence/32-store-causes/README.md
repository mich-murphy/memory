# Evidence 32: why a store request failed, in the log and in `memory check` (brief 40)

All dates are UTC, 2026-11-19, from 19:52Z to 20:41Z. Status: increment CAUSES accepted locally at
`a8ca4da` (5 worker commits over `1a5cae2`). Nothing was installed, sent to R2 or run against the
production state root; the production daemon (`launchd` label `memory`, `56c04aa` build) kept
running throughout.

[Brief 40](../../40-store-failure-causes.md) sits on `1a5cae2` (on top of `main` `29b8144`).
Operator request, 2026-11-19: resolve "`check` does not say which class a `store_error` had", and
review whether the three production connection failures were handled gracefully.

## Review of the three production failures

From the daemon log, `status.json` and the macOS unified log; counts and closed codes only. The
full table is in the brief ("Why").

- All three were classed `offline`, retried with full-jitter backoff and landed later. No owner
  was parked, the host never paused, and the next `memory check` matched all 6,045 sources. The
  local file is the system of record, so nothing was lost.
- The first (during the first full reconcile, about 14:22Z) waited for the 43-minute round to end
  before its retry, because a full round offers no retries until it ends. That is the driver's
  design (`a_retry_that_becomes_due_during_a_reconcile_runs_in_the_next_turn`), not a fault.
- The second took exactly 60 s (the request timeout floor of DESIGN 7.3), so it was most likely a
  timeout. The third failed after 9 s, so it was not. The owner landed on the next attempt.
- Wi-Fi beacon loss at about -80 dBm appears in both windows and in windows without a failure, so
  it is a background condition, not a cause.
- An earlier reading was corrected: `log show --start/--end` without an offset is local time
  (UTC+11), and `nw_path_libinfo_path_check` entries are DNS lookups, which ureq makes on every
  request even when it reuses a pooled connection. They are not new connections.
- **Gap:** nothing recorded which owner failed, why, after how long, or when it would retry.
  `map_transport` folded every transport failure into one value, `Failure` kept only the class,
  and `check` counted `store_error` without a detail line.

## Commits

| Commit | By | Change |
|---|---|---|
| `d2a8ca0` | worker | `environment.rs`: closed `Cause` (14 codes); `StoreError::{Offline(Cause), Ambiguous(Cause), code}`. `s3.rs` `map_transport` maps every ureq error to a cause; the `Offline`/`Ambiguous` rule for a write is unchanged; a 200 without an ETag is `Ambiguous(no_etag)` |
| `2a21068` | worker | `commit.rs`: `Failure::{NotLanded, LandingUnknown}` carry the `StoreError`; `Failure::code`. The two auth sites carry `Auth`, the two 404 sites `NotFound` (class `offline`, as before) |
| `e8574c6` | worker | `log.rs`: event `retry owner class cause landing attempt elapsed_ms after_ms`, logged by `Daemon::settle` for every `Disposition::Retry`; `log_lines.rs` parser and `assert_clean_log` learn it |
| `810e2bd` | worker | `check.rs`: `Verdict::StoreFailed(StoreError)`; one detail line `store_error owner=<prefix> kind=<kind|chain> cause=<code>` per store error |
| `a8ca4da` | worker | `check.rs` `main_entry` documents the detail lines |

No backoff, class, timeout, disposition, summary line or exit code changed.

### Worker deviations accepted

- `settle` also takes `now` (the monotonic reading at settle time) for `elapsed_ms`.
- Two tests that compared whole log lines now separate the new `retry` lines and assert their
  exact count, with the park line unchanged (`audit::a_park_is_recorded_when_the_owners_commit_fails`,
  `daemon::a_source_park_is_logged_with_its_reason`).
- `commit_protocol` tests that cared only about the class compare it through a helper that keeps
  the `NotLanded`/`LandingUnknown` distinction.
- The daemon `retry` tests run the real binary against the S3 fake (`s3_store/event_loop.rs`)
  rather than the in-memory daemon harness.
- A lost `current.json` reply followed by four 500s on its GET logs `cause=server
  landing=unknown`: the verify step keeps the last GET error.

### ureq 3.5.1 under a hang

With only `timeout_global` set, a server that accepts and stays silent is reported as
`Timeout(Global)`, so a hang logs `cause=timeout` (not `recv_timeout`). Recorded in
`a_hang_is_ambiguous_for_a_write_and_offline_for_a_read`.

## Tests

- `s3_store`: `transport_mapping_table` (every arm, write and read), `reply_mapping_table`
  (`no_etag`), `a_refused_connection_is_offline_with_cause_refused`,
  `a_hang_is_ambiguous_for_a_write_and_offline_for_a_read`, `a_dropped_reply_is_ambiguous_and_sent_once`.
- `log`: `retry_renders_the_exact_line`.
- `s3_store::event_loop`: `a_dropped_piece_put_logs_a_retry_with_its_cause_before_the_failed_capture`
  (`class=offline cause=reset landing=no attempt=1`, before `result=failed`, then a landed
  capture), `a_lost_current_reply_that_cannot_be_verified_logs_landing_unknown`. Both pass
  `assert_clean_log`.
- `s3_store::check`: the unreachable-store test asserts one `kind=chain cause=refused` line; the
  queued 500 test `kind=chain cause=server`; new `a_piece_get_fault_names_the_source_kind_and_cause`.

## Teeth (parent)

Script `causes_teeth.py` in the parent's scratchpad; each tooth replaced an exact anchor, ran the
`s3_store`, `log`, `daemon`, `audit` and `commit_protocol` binaries (258 tests) and restored.

| Tooth | Caught by |
|---|---|
| cause folded back to one value | `transport_mapping_table`, refused, hang and dropped-reply tests, `check::an_unreachable_store…`, the dropped-piece retry test |
| `Ambiguous`/`Offline` rule changed for a write | `transport_mapping_table`, `a_refused_connection_is_offline_with_cause_refused` |
| `Failure` cause replaced by a constant | both `event_loop` retry tests |
| `retry` not logged | both `event_loop` retry tests, the two adjusted park tests |
| `attempt` off by one | both `event_loop` retry tests |
| `check` source line dropped | `a_piece_get_fault_names_the_source_kind_and_cause` |
| `check` chain line dropped | the unreachable-store and queued-500 check tests |
| `landing` always `no` | `a_lost_current_reply_that_cannot_be_verified_logs_landing_unknown` |

8 of 8 caught.

## Checks

- Worker: fmt, clippy `-Dwarnings`; workspace nextest with the consumer gate: 851 passed.
- Parent, `a8ca4da`: `nix develop .#rust --command sh -c 'cd rust &&
  MEMORY_ANALYTICS_PYTHON=<repo>/.analytics-venv/bin/python PROPERTY_CASES=500 bash
  scripts/gates.sh'`: all 14 sub-gates 0, seed 3820085030; 04-nextest 783 passed, 05-hegel 32,
  06-proptest 34, 10-consumer 2; coverage 96.33 % (10,102 of 10,487 lines); `07-schema` 0.

## Limits

- A full round still offers no retries until it ends (out of scope; the wait is bounded by the
  round and loses nothing).
- `export`, `restore` and `owners` still print the closed code `store` on a fetch failure.
- Not run: 2,000 property cases, `cargo mutants` (operator decision).

## Installed on this Mac (2026-11-19, after merge)

- Stack #190 merged as `1b4f981`; post-merge Rust gates green, CI failed only in the two accepted
  `govulncheck` steps.
- Release build of `1b4f981` (sha256 prefix `cd5bfc25fe1406c5`) replaced
  `~/.local/libexec/memory/memory` in place at 21:38:56Z (same path, so the plist, hooks and Pi
  extension are unchanged). The previous `56c04aa` binary is kept as
  `~/.local/state/memory-cutover/backup/memory-56c04aa`.
- `launchctl kickstart -k gui/$(id -u)/memory`: the old process exited 0, `runs = 3`, the new
  process logged `started` at 21:38:57Z, and its start round ended 21:43:11Z with `landed=1
  failed=0`.
- `memory check` with the new binary, 21:43:27Z to 22:06:23Z, exit 0: `owners=1076 sources=6046
  match=6046`, `store_error=0`, every other failure count 0. No `WARN` or `ERROR` line and no
  `retry` line yet.
