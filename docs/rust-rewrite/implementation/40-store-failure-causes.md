# 40. Say why a store request failed: causes in the log and in `memory check` (2026-11-19)

Coordinator brief. The parent session owns this file and acceptance. It follows the cutover on this
Mac ([evidence 31](evidence/31-short-run-and-cutover/)). Evidence goes to `evidence/32-store-causes/`.
Text that states a plan stays as written; later changes are marked.

The production daemon (`launchd` label `memory`) keeps running on its `56c04aa` build while this
brief is worked on. Nothing here touches `~/.local/state/memory*`, the installed binary, the
agents' settings or R2. Every test uses the fakes or a temporary state root.

## Why

Operator, 2026-11-19: "let's resolve this [`check` does not say which class a `store_error` had]
and we should also review the 3 failures to determine whether connection failures were handled
gracefully".

Review of the three production failures (all `offline`; parent, from the daemon log, `status.json`
and the macOS unified log, counts only):

| When (UTC) | What the log shows | Handling |
|---|---|---|
| About 14:22 (first full reconcile) | `progress` turns `failed=1` between 14:21:06 and 14:22:36; the round ends 14:55:06 with `offline=1` | Retry deadline 1 s to 2 s later, but a full round offers no retries until it ends (driver design, test `a_retry_that_becomes_due_during_a_reconcile_runs_in_the_next_turn`); the owner landed after the round. Nothing lost: the local file is the system of record |
| 16:56:32 to 16:57:32 | First attempt's DNS lookup at 16:56:32, `capture … result=failed` at 16:57:32: 60 s, the request timeout floor (DESIGN 7.3) | Most likely a request timeout. Retried at once (full-jitter backoff `[1 s, 1 s]` for the first retry) |
| 16:57:32 to 16:57:41 | Second attempt failed after 9 s: not a timeout | Retried at 16:57:41 (third attempt, backoff up to 2 s); lookups continued; landed 17:00:03 |

In both windows the Wi-Fi log shows beacon loss at about -80 dBm, but so do windows without a
failure, so that is a background condition, not a cause. No owner was parked, the host never
paused, and the next `memory check` matched all 6,045 sources. **Handling was correct; the
diagnosis is blind**: nothing records which owner failed, why, after how long, or when it retries.

Where the cause is lost (checked on `29b8144`):

1. `s3.rs` `map_transport` folds DNS failure, refused or failed connect, TLS, every timeout phase,
   reset and protocol errors into `StoreError::Offline` (or `Ambiguous` for a write).
2. `commit.rs` `not_landed` and `verify` keep only `ErrorClass` in `Failure::NotLanded(ErrorClass)`
   and `Failure::LandingUnknown(ErrorClass)`; the `StoreError` is dropped.
3. `daemon.rs` `settle` counts the class and sets `Turn::RetryAt`; nothing is logged per failure.
   `log.rs` has only `capture … result=failed` for a per-owner capture and per-class counts in
   `round`.
4. `check.rs` counts `store_error` (owner fold at :582, a piece fetch at :411, the whole path at
   :305) and prints no detail line, unlike `mismatch`, `uncaptured`, `withheld` and `parked`.

## Checkout

`main` is `29b8144`. This brief is on `docs/brief-40`. The worker gets `../memory-wt/b40-causes` on
`rust/store-causes` from `docs/brief-40`. Crate paths are under `rust/crates/`.

## Facts checked on `29b8144`

| Fact | Where |
|---|---|
| `StoreError` is `Copy + Hash`, variants `PreconditionFailed NotFound Offline Throttled(Option<u32>) Server Auth Ambiguous`; `class()` maps `Auth`→`DestinationAuth`, `Throttled`→`Throttled`, the rest→`Offline` | `memory/src/environment.rs:58-108` |
| `map_reply` gives `Ambiguous` for a 200 without a usable ETag, `Server` for other 5xx | `memory/src/s3.rs:101-121` |
| `map_transport(write, &ureq::Error)`: `HostNotFound ConnectionFailed Tls Rustls`, `Timeout(Resolve|Connect)`, `Io(ConnectionRefused)` → `Offline`; `BodyExceedsLimit` → `Server`; else `Ambiguous` for a write, `Offline` for a read | `memory/src/s3.rs:123-135` |
| ureq 3.5.1 `Error` variants include `Io`, `Timeout(Timeout)`, `HostNotFound`, `ConnectionFailed`, `Protocol`, `Tls`, `BodyExceedsLimit`; `Timeout` is `PerRead PerWrite Global PerCall Resolve Connect SendRequest Await100 SendBody RecvResponse RecvBody` | `~/.cargo/registry/src/*/ureq-3.5.1/src/{error,timings}.rs` |
| Every request sets only `timeout_global` (`TimeoutPolicy::for_body`, 30 s + size at 1 MiB/s, floor 60 s); which `Timeout` variant ureq reports for a hang under it is not established | `memory/src/s3.rs:47-75`, `:276`, `:322`, `:376` |
| `Failure::NotLanded(ErrorClass)` is built in `not_landed` (:273) and for a PUT `Auth` (:654); `LandingUnknown(ErrorClass)` in `verify` (:477 auth, :488 last error) and for a 404 after a fenced write or after the rebase limit (:685, :693) | `memory/src/commit.rs` |
| `NotLanded`/`LandingUnknown` are matched in tests: `commit_protocol.rs` (about 20), `capture_property.rs`, `stream_proofs.rs`, `s3_store.rs` | `grep -rn 'NotLanded\|LandingUnknown' rust/crates/memory/tests` |
| `Daemon::settle(rt, result, at, draw, report)` is the one place a failure becomes a disposition; called from `recover` (:837) and `run_owner` (:969, :978); `at` is the turn's monotonic start | `memory/src/daemon.rs:747-793` |
| `Streak.retries` counts failed attempts since the last success | `memory-core/src/disposition.rs:53-62` |
| Log grammar: closed events and keys; the test parser `tests/common/log_lines.rs` `parse` rejects an unknown event or key order; `assert_clean_log` allows `round capture parked host_paused host_resumed` between `started`/`state_load` and `stopped` | `memory/src/log.rs:1-49`; `memory/tests/common/log_lines.rs:53-160` |
| `check` detail lines: `parked owner=… reason=… scope=…`, `mismatch owner=… kind=… reason=…`, `uncaptured …`, `withheld …`; `Verdict::StoreFailed` has no payload and no test matches it | `memory/src/check.rs:186-202`, `:538-622` |
| `export`, `restore`, `owners` print the closed code `store` on a fetch failure | `memory/src/export.rs:118`, `restore.rs:263`, `owners.rs` |
| The S3 fake can queue `Fault::{ApplyThenStatus, ApplyThenDrop, ApplyThenHang(d), Status(s, retry_after)}` per method and key suffix | `memory/tests/common/s3_fake.rs:18-132` |

## Decisions

Each is reversible before merge. A worker that finds one wrong reports it and does not decide.

| Topic | Decision |
|---|---|
| Cause | New closed `Cause` in `environment.rs`, `Copy + Eq + Hash`, with `const fn code(self) -> &'static str`: `dns`, `dns_timeout`, `connect`, `connect_timeout`, `refused`, `tls`, `send_timeout`, `recv_timeout`, `timeout`, `reset`, `io`, `protocol`, `no_etag`, `other`. Doc comment per variant |
| StoreError | `Offline(Cause)` and `Ambiguous(Cause)`; the other variants unchanged. `class()` unchanged in meaning. New `const fn code(self) -> &'static str`: the cause's code for `Offline`/`Ambiguous`, else `precondition_failed`, `not_found`, `throttled`, `server`, `auth` |
| Mapping | `HostNotFound`→`dns`; `ConnectionFailed`→`connect`; `Tls`/`Rustls`→`tls`; `Timeout(Resolve)`→`dns_timeout`; `Timeout(Connect)`→`connect_timeout`; `Timeout(SendRequest|SendBody|Await100|PerWrite)`→`send_timeout`; `Timeout(RecvResponse|RecvBody|PerRead)`→`recv_timeout`; `Timeout(Global|PerCall)`→`timeout`; `Io` with kind `ConnectionRefused`→`refused`, `ConnectionReset|ConnectionAborted|BrokenPipe|UnexpectedEof`→`reset`, other `Io`→`io`; `Protocol`→`protocol`; anything else→`other`. Whether it is `Offline` or `Ambiguous` follows today's rule exactly (refused/DNS/connect/TLS/resolve and connect timeouts always `Offline`; the rest `Ambiguous` for a write). `BodyExceedsLimit` stays `Server`. A 200 without an ETag is `Ambiguous(Cause::NoEtag)` |
| Failure | `NotLanded(StoreError)` and `LandingUnknown(StoreError)`; the class comes from `StoreError::class()`. The two auth sites pass `StoreError::Auth`; the two 404 sites pass `StoreError::NotFound` (class `Offline`, as today). `Failure::outcome`, `base_may_be_stale` and every disposition unchanged. New `Failure::code(&self) -> &'static str`: the store error's code for those two; `throttled`, `base_unchanged`, `foreign`, `newer_schema`, `stopped`, `invalid`, `corrupt` for the others |
| Log event | New event `retry`, keys in this order: `owner class cause landing attempt elapsed_ms after_ms`. `class`: `offline throttled transient destination_auth local deterministic`; `cause`: `Failure::code`; `landing`: `unknown` for `LandingUnknown`, else `no`; `attempt`: `Streak.retries` after the failure (1 for the first); `elapsed_ms`: monotonic ms from the turn's `at` to the settle; `after_ms`: the retry delay. Logged by `settle` (which gains the owner) for every `Disposition::Retry`, so in a full round it comes before the `round` line and in a per-owner capture before its `capture` line. Nothing logged for `Done`, `Cancelled`, `PauseHost` (already `host_paused`), `Park*` (already `parked`). `log.rs` module table and `log_lines.rs` parser and `assert_clean_log` learn it |
| `check` | `Verdict::StoreFailed(StoreError)`. Each store error adds one detail line `store_error owner=<prefix> kind=<kind> cause=<code>`; for a failed owner fold the kind is `chain` and there is one line for the owner. Counts, summary line, exit codes unchanged |
| Out of scope | Retrying inside a full round (the first-round wait above); per-phase timeouts; `export`/`restore`/`owners` codes; any change to backoff, classes, timeouts or the commit protocol |

## Increment

| # | Increment | Branch | Owner |
|---|---|---|---|
| 1 | CAUSES | `rust/store-causes` | worker (Sonnet, medium) |

Owns `memory/src/{environment,s3,commit,daemon,log,check}.rs` for exactly the changes above, the
test files that match the changed variants, `tests/common/log_lines.rs`, and new tests. Read-only
everywhere else; `memory-core` is not touched.

Ordered steps, one commit each, `cargo fmt`, clippy `-Dwarnings` and the focused tests green before
each commit:

1. `Cause`, `StoreError::{Offline(Cause), Ambiguous(Cause), code}`, `map_transport`/`map_reply`;
   fix every match. Tests: a table test of `map_transport` over every arm (build the
   `ureq::Error` values directly; `io::Error::from(kind)` for the `Io` arms); a fake-server test
   that a hang under a short `TimeoutPolicy` maps to the cause the table says (record in the test
   comment which `Timeout` variant ureq 3.5.1 reported); refused connection → `refused`.
2. `Failure::{NotLanded(StoreError), LandingUnknown(StoreError), code}`; fix every construction
   and test match (asserts keep their meaning: compare the class via `.class()` where a test only
   cared about the class). No behaviour change: the whole `commit_protocol`, `capture_property`,
   `stream_proofs` and `s3_store` suites pass unchanged in what they assert.
3. The `retry` log event. Tests: `log.rs` render tests for the exact line; a daemon test (fake
   store, `Fault`) where one owner's first PUT fails with a dropped connection: the log has
   `retry owner=<o> class=offline cause=<…> landing=no attempt=1 elapsed_ms=<n> after_ms=<n>`
   before the `round` line, then the owner lands in a later turn; a test with a lost reply on
   `current.json` gives `landing=unknown`; `assert_clean_log` accepts `retry`.
4. `check` detail lines. Tests in `s3_store/check.rs`: the existing unreachable-store test also
   asserts one `store_error owner=… kind=chain cause=refused` line; the queued 500 test asserts
   `cause=server`; a piece GET fault gives a line with the source kind.
5. Docs in code: `log.rs` module table, `check.rs` `main_entry` doc (detail lines).

## Rules for the worker

- Cargo only via `nix develop /Users/mm/dev/memory#rust --command sh -c 'cd <worktree>/rust && …'`,
  one cargo job at a time. Combine nextest `-E` conditions into one expression.
- Never touch `~/.local/state/memory*`, `~/Library/LaunchAgents`, agent settings, the installed
  binary, `.env` or R2. No `git stash`. Never commit `.proptest-regressions` files.
- No gate is loosened: no timeout raised, no test deleted or weakened; a test whose assert changes
  keeps what it proved.
- Report: commits, deviations with reasons, the ureq `Timeout` variant for a hang, and the exact
  commands run with results.

## Acceptance (parent)

Read the whole diff; teeth (each tooth replaces an exact anchor, runs the focused tests, restores):
cause folded back to one value; `Ambiguous`/`Offline` rule changed for a write; `Failure` cause
replaced by a constant; `retry` not logged in a full round; `attempt` off by one; `check` detail
line dropped; `landing` always `no`. Then the full gates once (`MEMORY_ANALYTICS_PYTHON=… 
PROPERTY_CASES=500 bash scripts/gates.sh`), record evidence 32, DESIGN 11.5 and D49, the handoff,
push, hosted gates, merge. Installing the new build on this Mac is a separate step after merge.

## Ledger

- 2026-11-19 20:00Z: brief written on `29b8144`, with the review of the three failures.
- 2026-11-19 20:41Z: CAUSES accepted locally at `a8ca4da` (worker 5 commits; it hit its turn
  limit once after step 1 and was resumed). Deviations accepted: `settle` also takes `now`; two
  whole-log asserts separate the `retry` lines and count them. ureq 3.5.1 reports a hang under
  `timeout_global` as `Timeout(Global)` (`cause=timeout`). Teeth 8 of 8. Full gates on
  `a8ca4da`: 14 sub-gates 0, seed 3820085030, coverage 96.33 %. Record:
  [evidence 32](evidence/32-store-causes/). Next: push, hosted gates, merge, then install the
  new build on this Mac.
