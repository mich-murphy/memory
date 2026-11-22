# 52. Slice 3, part 8: store causes, a retry budget and a race-free check (2026-11-22)

Coordinator brief. The parent session owns this file and acceptance. It closes three open findings
of handoff items 27 and 28. Evidence goes to `evidence/47-robustness/`. Text that states a plan
stays as written; later changes are marked.

Production daemons run on this Mac (launchd) and on dev-01 (systemd), both on the `ecb1d74` build,
while this brief is worked on. Nothing here touches `~/.local/state/memory*`, `~/.codex`,
`~/.claude`, `~/.pi`, `~/Library`, the installed binaries, agent settings, `.env` or R2.

## Why

Operator, 2026-11-22: the three findings "should be solved by research against best practice,
particularly the DDIA textbook, and experimentation to determine the best solution". Three fresh
research agents each read DDIA (2nd edition, chapters 1 to 8) and web sources, reproduced the
problem on the test rigs and prototyped options. The parent checked every DDIA quote against the
text, fetched the web sources itself, and sent the retry research back once because the fetched
AWS guidance argued against its first answer. Reports and prototypes stay in the parent's scratch
directory; the facts that drive the decisions are below.

### Finding 1: `export`, `restore` and `owners` print only `store`

Every `StoreError` is dropped at the fetch boundary (`export.rs:88,122`, `restore.rs:222,263`,
`owners.rs:61`): a 503, a 403, a 500, a dropped reply and a refused connection all give
`memory: store`, exit 1. `memory check` and the daemon already keep the closed cause (D49).
DDIA ch5 ("a network request is very different from a local function call"): "if you don't get a
response from the remote service, you have no way of knowing whether the request got through or
not." These three commands only read, so every transport fault is safe to retry; an auth fault is
not. sysexits(3): `EX_TEMPFAIL` 75 "temporary failure ... user is invited to retry", `EX_NOPERM`
77 "insufficient permission". 75 already means "retry later" in this CLI (`unpark`'s `lock_held`).

### Finding 2: "no retries within a full round"

Imprecise. A failed owner gets `Turn::RetryAt` (`daemon.rs:849`) and the driver captures it at that
deadline (`driver.rs:325-366`, `daemon.rs:1495-1530`), before the next round, with per-owner full
jitter. What is missing is a bound on the host's total retry load: with the store down, retry
requests grow with the number of owners (simulated: 148 requests in the first minute at 20 owners,
1,472 at 200). DDIA ch2 names the "retry storm" and a circuit breaker; the AWS Builders' Library
("Timeouts, retries, and backoff with jitter") prefers a local token bucket because circuit breakers
"introduce modal behavior into systems that can be difficult to test, and can introduce significant
addition time to recovery"; AWS "Introducing Retry Throttling": failures drain a retry pool,
successes refill it, first attempts are unaffected. Measured on a simulated driver over the real
`Daemon` (20 and 200 owners):

| Policy | Requests, first minute, store down | Recovery after a 120 s outage | Uncaptured 10 min after a 120 s outage |
|---|---|---|---|
| today | 148 / 1,472 | 227 / 426 s | 0 / 0 |
| host gate (circuit breaker) | 7 / 7 | 17 s | 0 / 0 |
| token bucket (below) | 33 / 213 (first attempts are not limited) | 21 / 29 s | 0 / 0 |
| adaptive throttling (SRE, K = 2) | 4 / 4 | 404 / 1,261 s | 0 / **199** |

With 10 % random failures for 10 minutes no policy changes anything. The bucket was chosen: it
follows the fetched AWS guidance, bounds retries at about 4 per minute whatever the owner count,
and meets the 10 minute target of section 15.

### Finding 3: the `check` race

`check()` walks local files, then reads each owner's remote state, then judges; the daemon keeps
writing. Reproduced on the fake S3 store with a phase hook: a commit between walk and fetch gives a
false `remote_only` (the 2026-11-21 event); a commit between fetch and judge gives a false
`uncaptured` and **exit 1**, which the 12-hourly loop reads as a failure. DDIA ch8 ("Analytic
queries and integrity checks"): such checks "are likely to return nonsensical results if they
observe parts of the database at different points in time." The local filesystem has no snapshot
and the daemon must not be paused (D51), but pieces and manifests are immutable and only
`current.json` moves, and S3 and R2 are strongly consistent, so a second read sees equal or newer
state (DDIA ch6, monotonic reads). Re-reading only what disagrees removed every false result in the
prototype while a remotely deleted object, a corrupted piece and a never-captured file were still
reported; a quiet owner costs no extra request.

## Decisions

Each is reversible before merge. A worker that finds one wrong reports it and does not decide.

| Topic | Decision |
|---|---|
| Store line (1) | `export`, `restore` and `owners` print `memory: store cause=<StoreError::code()>` (the closed codes of D49). A fetcher with no typed error (test fetchers) still prints `store` |
| Store exit codes (1) | 75 for `Offline`, `Ambiguous`, `Throttled` and `Server`; 77 for `Auth`; 1 for everything else (`object_missing`, `source_corrupt`, `chain_corrupt`, `not_found` from a list). 66 `owner_absent` and the other existing codes are unchanged. The rustdoc exit tables of the three `main_entry`s are updated |
| Unparsable list body (1) | `owners` on an unparsable list reply prints `store cause=protocol`, exit 1 (not retryable). If this needs a `StoreError` change that the daemon or `check` would see differently, stop and report. (Amended 2026-11-22 by the parent after the worker's stop: `map_list_reply` returns `Offline(Cause::Protocol)` and the exit is 75 like every `Offline`. No new variant; a garbled 200 is most likely a truncated reply or a proxy, and these commands only read) |
| Retry budget (2) | A host-level token bucket in the daemon: capacity 10 tokens, refill 1 token per 15 s, plus 1 per landed commit, never above 10. A first attempt (an owner with no failure streak) never needs a token. A retry (streak > 0) takes one token; without one the owner is `Deferred` to now plus a jittered 1 to 30 s (the same jitter source as D44). The verify GET of an owner whose last outcome was `LandingUnknown` never waits for a token. The bucket only delays: an owner's own `RetryAt` (incl. `Retry-After`) is never shortened. `DestinationAuth` pausing, per-owner backoff and parking are unchanged. In memory only; a restart starts full |
| Retry visibility (2) | A denied retry logs nothing per owner (it would flood the log); `memory status` gains no field in this brief |
| Check confirmation (3) | After the judge, an owner with an `uncaptured` or `mismatch` verdict has its `current.json` read once more; if the head moved, only those sources are judged again against the fresh chain (at most 64 manifests, as today) and the fresh verdict replaces the first. `remote_only` candidates are confirmed by a second walk after the last owner read; an id the second walk sees is not `remote_only`. One confirmation only; what survives it is reported exactly as today |
| Check test seam (3) | The phase hook used to inject a concurrent commit exists only under the `sim` feature (as other simulation seams do); the public `check()` signature and output are unchanged |
| Out of scope | Analytics (separate briefs), capture scope (R1), `Retry-After` in output, status fields |

## Increments

| # | Increment | Branch | Owner |
|---|---|---|---|
| 1 | STORECAUSE | `rust/store-cause` | worker (Sonnet, medium) |
| 2 | RETRYBUDGET | `rust/retry-budget` | worker (Sonnet, medium) |
| 3 | CHECKCONFIRM | `rust/check-confirm` | worker (Sonnet, medium) |

Run in parallel in separate worktrees off this brief; the parent stacks them in this order.

Ownership:
1. STORECAUSE: `memory/src/{export,restore,owners}.rs`, `memory/src/s3.rs` only for the list-body
   cause, `memory/tests/s3_store/{export,restore,owners}.rs`, a new `memory/tests/s3_store/errors.rs`
   and its `mod` line in `tests/s3_store.rs`.
2. RETRYBUDGET: `memory/src/daemon.rs`, `memory/tests/daemon.rs`, `memory/tests/driver.rs`.
3. CHECKCONFIRM: `memory/src/check.rs`, `memory/tests/s3_store/check.rs`.

Read-only everywhere else; if another file must change, stop and report.

Steps (one commit each; `cargo fmt`, clippy `-Dwarnings` and the focused tests green before each):

1. STORECAUSE: (a) the typed cause through the three commands, the line and the exit table, with a
   real-binary test table over the loopback fake (503 with `Retry-After`, 403, 500, dropped reply,
   refused connection, piece 403/503/dropped, piece 404, wrong digest, `current.json` 404, owners
   403/500/503/unparsable); (b) a unit test that the exit mapping covers every `StoreError` variant
   without a wildcard arm, and the existing no-input-text test extended to every cause; (c) docs.
2. RETRYBUDGET: (a) the bucket; (b) tests: with 20 and with 200 owners failing `Offline` in one
   round, retried store requests in the next 60 s stay under a bound that does not depend on the
   owner count; a landed commit refills; a first attempt is never denied; a denied owner is
   deferred at most 30 s; `Retry-After` is never shortened; a `LandingUnknown` verify runs at its own
   deadline while the bucket is empty; auth still pauses; a new `Daemon` starts full; (c) docs.
3. CHECKCONFIRM: (a) the confirmation pass and the `sim` seam; (b) real-binary tests: a commit
   between walk and fetch is not `remote_only`; a commit between fetch and judge is not
   `uncaptured` and exits 0; a file deleted after the walk is never a failure; a file really gone
   locally is still `remote_only`, a never-captured file still `uncaptured` (exit 1), a corrupted
   piece still `mismatch`; a quiet owner costs no extra request (count them on the fake);
   (c) module docs.

## Rules for the workers

- Cargo only through the shared lock:
  `<scratch>/research/cargo-lock.sh <worktree> '<command run in <worktree>/rust>'` (the path is in
  the prompt), one job at a time across workers. Combine nextest `-E` conditions into one
  expression. The consumer gate needs
  `MEMORY_REQUIRE_CONSUMER=1 MEMORY_ANALYTICS_PYTHON=/Users/mm/dev/memory/.analytics-venv/bin/python`.
- The research prototype is a behaviour reference only (path in the prompt); it is not production
  quality.
- No `unsafe`, no new crate, no `unwrap`, `expect` or panic outside tests in `memory-core`; follow
  the `memory` crate's lint settings. No `git stash`. Never commit `.proptest-regressions`.
- Never touch `~/.local/state/memory*`, `~/.codex`, `~/.claude`, `~/.pi`, `~/Library`, the
  installed binary, `.env` or R2; do not list or read real agent directories. Use temporary
  directories. A production daemon runs on this Mac: do not inspect or signal it.
- No gate is loosened: no timeout raised, no test deleted or weakened, no coverage floor lowered.
  A changed existing expectation gets a comment naming this brief and is listed in the report.
- If a decision looks wrong or cannot be implemented cleanly, stop on that point and report; do
  not decide it yourself. Do not edit the brief, DESIGN.md or the handoff. Do not run
  `scripts/gates.sh`. Do not push.
- Report: commits, deviations with reasons, changed expectations, exact commands with test counts,
  `git status --short`.

## Acceptance (parent)

Read every diff. Teeth per increment, at least: (1) a cause dropped, 75/77 swapped, auth mapped to
75, a wildcard arm; (2) first attempts charged, refill on landing dropped, `LandingUnknown` charged,
the deferral unbounded, the bucket shortening `RetryAt`; (3) confirmation dropped for each of
`remote_only`, `uncaptured`, `mismatch`, the second head ignored, a confirmed defect dropped. Then the
full gates once on the stacked tip, the Linux container on the changed test binaries, the hosted
gates, evidence 47, DESIGN (7.4, 11.2, the check section, D62 to D64), the handoff; merge; install on
both hosts; first rounds, `memory status`, `memory check`, peak memory.

## Ledger

- 2026-11-22: brief written on `1968cfe`.
- 2026-11-22: three workers in parallel. STORECAUSE stopped on the list-body row (no `StoreError`
  with code `protocol` and exit 1); the parent chose `Offline(Protocol)`, exit 75 (row amended).
  Teeth 29 of 33 at first; the parent added tests for the four survivors (jitter, `RetryAt` and
  parking under an empty bucket, a raced `mismatch`), then 33 of 33. Gate `03-deny` failed on
  `RUSTSEC-2026-0592` (`url` 2.5.8, also on `main`), fixed by PR #252 (`4d6729f`); the stack was
  rebased onto it. Accepted locally at `034be38`: gates 14/14, Linux container passed. Details
  in evidence 47.
- 2026-11-22: merged as `25fae57` (stack #258). The release build of `main` failed: the non-`sim`
  `Phase` macro did not compile, and no gate built `memory` without `sim`. Nothing installed.
- 2026-11-22: follow-up fixes the macro and the variant names, and gate `02-clippy` gains a
  libs-and-bins pass without dev features (evidence 47, follow-up). Merged as `94833f6` (PR #259).
- 2026-11-22 14:40Z to 15:20Z: `94833f6` installed on this Mac and dev-01. `memory check` exit 0
  on both, `remote_only=0`, withheld 3 and 26; peak 90 MB here, dev-01 `VmHWM` 109 MB. Exit
  criteria met. Details in evidence 47.
