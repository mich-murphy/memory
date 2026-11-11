# P11: exhausting commit retries on a stale base (`BaseUnchanged`)

> **Renamed 2026-11-09** from P9 to P11: DESIGN.md section 16 already assigns P9 to the SQLite unchanged test. Scripts, patch env vars, scratch names and the R2 prefix used (`rust-acceptance/p9-<ts>/`) keep their original labels, so recorded hashes and commands stay valid.

Status: research, 2026-11-09. Nothing outside this file and `p11-commit-retry/` was changed.
Raw data: `p11-commit-retry/r2-raw.json` (real R2), `p11-commit-retry/sim-results.json` (simulation).
Code: `p11-commit-retry/sim.py` (`python3 -I sim.py 12`), `p11-commit-retry/probe/` (Go R2 probe).

## 1. Explanation

**What `BaseUnchanged` means.** Step 3 (`PUT current.json If-Match`) failed or was not confirmed, then the
verification GET showed `current.json` still naming our `previous` (`commit.rs:265`, `Verdict::Base`).
So the write did not land. With one writer per owner nobody else moved the pointer, so the cause is transport
or server (5xx, timeout, reply lost before the server applied the write), not contention. The loop re-fences on the
fresh ETag and retries up to `REBASE_RETRIES = 4` (`commit.rs:21-22`, loop `commit.rs:330-377`). Past that it returns
`Failure::BaseUnchanged` (`commit.rs:94-95`, `370-375`). `outcome()` maps that to `Failed(Throttled)` (`commit.rs:125`),
which `transition` turns into `Retry{after: backoff(retries)}` (`disposition.rs:54-64, 101`). `settle` drops the base
and sets `needs_recover` (`daemon.rs:443-449`), so the next attempt starts with a GET.
Only a fence bug could make it contention. Two retries of the same capture reuse one manifest hash, so a second hit is
indistinguishable from the first.

**Bytes arriving during a capture.** A capture covers `[from, size_at_stat)` (DESIGN 7.3). Growth after the stat is not
in this manifest; the next reconcile plans `Append{captured_to, size}`. The coalescer keeps one capture per owner in
flight and queues exactly one follow-up (DESIGN 7.1). A failed commit loses nothing: `captured_to` advances only on
success, so the retried capture re-reads from the old position and includes the new bytes. In the sim `bytes_unlanded` at
end was 0 in every run and `unlanded_owner_runs` was 0.

**Late commits, by form.**

| Form | What coordinates it | Outcome |
|---|---|---|
| Lost reply (write landed) | verification GET; manifest hash is the request id (DESIGN 7.4 step 5; `commit.rs:349-355`) | `AlreadyOurs` |
| Delayed PUT from an old attempt | `If-Match` ETag: after any later write its ETag is stale, so R2 answers 412. Measured: 28/28 stale ETags returned 412 | fenced |
| Delayed PUT of an old attempt that still holds the current ETag | nothing but the hash | see gap 1 |
| Another host | single writer per owner, local daemon lock, `Foreign` park | `Foreign` |

R2 race measurement: 60 rounds, two concurrent `If-Match` writers on one ETag. Exactly one 200 and one 412 every
round, final object always the winner's body. No 412-after-success was observed (client retries off).

**Gaps found between DESIGN, code and these answers.**

1. *A late write from an earlier capture attempt is parked as `Foreign`.* `verify` returns `Ours` only for the
   current manifest hash (`commit.rs:249-266`). A retried capture builds a new manifest (`captured_at` is the round
   time, `daemon.rs:502`, so its hash differs). If the earlier attempt's PUT lands late and `current.json` names that old
   manifest, the GET matches neither `ours` nor `previous`, and the owner is parked. Sim counted this path as `own_adopt`:
   rare (0 to 1 in 12 seeds x 100 owners at 20% errors) but non-zero, and a park is a human-visible event.
   I did not run it against the Rust code; this follows from the cited lines.
2. *DESIGN 11.2 says `Throttled` "honours `Retry-After`".* `BaseUnchanged` carries none, and its class is wrong
   (it is not a 429/503). It also escalates on the same streak counter as real throttling, up to 15 min.
3. *`backoff()` has no jitter* (`disposition.rs:54`), while DESIGN 11.2 says "with jitter" for `Offline`.
   100 owners that fail together retry together.
4. *DESIGN 7.4 step 5 says "jittered backoff" inside the commit*, but `pause(n)` is supplied by the shell
   (`commit.rs:300`); I did not verify the shell jitters.

## 2. Approaches (DDIA: ch. 8 "Handling errors and aborts", ch. 2 "When an overloaded system won't recover")

| Id | Approach | DDIA basis |
|---|---|---|
| a | Current: 4 rebase retries with ~0.1 s pause, then capture-level `Retry` as `Throttled`, deterministic 2^n s | baseline |
| b | Full-jitter exponential pause inside the commit | "exponential backoff with jitter" |
| c | Decorrelated jitter inside the commit | same |
| d | No in-commit retry; refresh base, wait for the next coalescer window (~60 s) | "limit retries" |
| e | b plus shared retry token bucket (10 burst, 1/s) | retry storms, load shedding |
| f | b plus a `Transient` class: full-jitter capture backoff, 1 s to 120 s cap | "handle overload differently" |
| g | f plus the bucket | combination |
| h | a plus full-jitter capture-level backoff | jitter |

## 3. Measurement

### 3.1 Real R2 probe (parameters for the sim)

Prefix `rust-acceptance/p9-<UTC ts>/`, 889 requests, 108 s, client retries disabled. Cleanup listed that prefix only,
deleted 201 objects, re-listed it: 0 remaining. No other key was addressed; the `delivery-artifacts/` objects were never
named. Credentials and endpoint were not printed or written.

| Op | status | n | p50 ms | p99 ms |
|---|---|---|---|---|
| PUT `If-None-Match: *` | 200 | 200 | 161.6 | 343.1 |
| PUT `If-Match` (current.json size) | 200 | 200 | 174.0 | 267.1 |
| GET | 200 | 320 | 66.9 | 155.0 |
| PUT with stale ETag | 412 | 28 | 56.6 | 132.8 |
| PUT create on existing key | 412 | 20 | 59.7 | 79.4 |
| Race loser | 412 | 60 | 189.3 | 261.1 |

Measured from this Mac's network path only. BC-01 was not measured.

### 3.2 Simulation

100 owners, 4 h (arrivals for 3 h, fault at 1 h), quiet 60 s and cap 300 s, bursty producers. Server capacity 4 req/s
(about 10x healthy load); above it requests are rejected proportionally (so retries add load). Latencies are log-normal
fitted to 3.1. 5xx splits 45% not-landed, 35% landed-but-reply-lost, 20% zombie (lands 2 to 20 s later). Timeout 10 s.
12 seeds (9000 to 9011) per config, 56 configs, 25.7 s wall, exit 0. About 16k landed commits per config.
Latency is emit to landed (s); lag is data produced to landed.

Invariants over all 56 x 12 runs: two-in-flight 0, duplicate landing 0, non-monotone landing 0, `Foreign` events 0
(sim writers never act as a second host), unlanded bytes 0 at end. Recovery: every storm and outage run recovered; no
metastable state was seen at this load (peak offered load 4 to 12 req/s against capacity 4, with storm capacity 0.3).

Selected results (full table below):

| Scenario | Approach | commit p99 s | lag p99 s | req/landed | wasted uploads/landed | BaseUnchanged exhaustions |
|---|---|---|---|---|---|---|
| err5 | a | 10.6 | 301 | 3.32 | 0.157 | 0 |
| err5 | b | 10.6 | 301 | 3.32 | 0.163 | 0 |
| err5 | d | 116.9 | 345 | 3.33 | 0.213 | 444 |
| err20 | a | 20.6 | 304 | 4.33 | 0.837 | 1 |
| err20 | b | 22.1 | 304 | 4.30 | 0.813 | 0 |
| err20 | d | 325.0 | 501 | 4.31 | 1.041 | 1720 |
| err20 | f | 23.2 | 305 | 4.31 | 0.819 | 0 |
| outage5m | a / b | 335.4 | 427 | 3.21 | 0.148 | 0 / 1 |
| outage5m | d | 246.5 | 400 | 3.11 | 0.067 | 7 |
| outage5m | f | 246.6 | 403 | 3.21 | 0.147 | 0 |
| storm5m | a | 256.5 | 395 | 3.31 | 0.187 | 1 |
| storm5m | b | 256.4 | 391 | 3.31 | 0.183 | 5 |
| storm5m | f | 192.1 | 359 | 3.32 | 0.190 | 2 |
| storm5m | h | 211.8 | 425 | 3.33 | 0.197 | 3 |

### 3.3 What the numbers say

- **Exhausting retries is rare** under independent errors: 0 to 1 `BaseUnchanged` exhaustions per 12 runs at 20% error for
  a, b, c, f. Most commit time goes to the 4-attempt verification GET and capture-level failures, not to rebase retries.
  So the in-commit schedule (a vs b vs c) hardly matters; they are within noise (p99 20.6 / 22.1 / 23.0 s at 20%).
- **d (fail fast) is the worst** at errors: p99 325 s vs 22 s at 20%, and 1720 exhaustions. Handing a transient failure to a
  60 s window multiplies latency. It is better in the two sustained faults: outage5m p99 246.5 s vs 335.4 s (a) and wasted uploads
  0.067 vs 0.148; storm5m p99 164.8 s vs 256.5 s (a) and 192.1 s (f), wasted uploads 0.077 vs 0.187. Requests per
  landed commit barely differ (3.11 vs 3.21). So d wins under bursts and loses under independent errors; a hybrid
  (fast in-commit retry, then a wait of about one window once failures cluster) was not simulated and is the obvious next test.
  The recommendation below (f) is therefore the best single approach measured across all scenarios, not the best in each.
- **f (own class, wider-spread capture backoff)** cut p99 by 25% in the storm (192 vs 256 s) and by 26% in the outage
  (247 vs 335 s), with no cost at 1 to 20% errors. h (jitter alone, 15 min cap) was worse than a in the storm, so the cap
  and class matter more than jitter.
- **Token bucket (e, g) never bound**: `bucket_denied` was 0, so e = b and g = f exactly. Its benefit is unmeasured at
  this scale. N=100 owners with 60 s windows is far below the load needed for a retry storm. Not claimed.
- **Metastability was not reproduced**; this is a result about this load model, not a proof.

## 4. Recommendation

Adopt **f**: keep 4 in-commit retries (jitter from the shell, full jitter), add a `Transient` error class for exhausted
`BaseUnchanged`/`LandingUnknown`, and fix gap 1. Do not adopt d. Keep the token bucket out until a measured need; if
added, it is cheap insurance, not a measured win.

Proposed text (exact):

- DESIGN 7.4 step 5, `BaseUnchanged` bullet, replace "After that, `Retry` the capture." with: "After that the capture ends
  as `Failed(Transient)`; the capture is retried with full-jitter backoff (section 11.2). `captured_to` does not advance,
  so bytes that arrived meanwhile ride in the retried capture."
- DESIGN 7.4 step 5, add after the `Foreign` bullet: "A `current.json` that names a manifest this daemon wrote for the
  same owner in an earlier attempt of the same capture is `AlreadyOurs`, not `Foreign`: adopt its ETag, set `captured_to`
  from that manifest, and queue a follow-up capture. Attempts are tracked in memory per owner."
- DESIGN 11.2, add row: "`Transient` | `BaseUnchanged` after 4 rebases, verification GET exhausted | `Retry`, full jitter in
  `uniform(1 s, min(120 s, 2 x 2^n))`, counted separately; not `Throttled` and carries no `Retry-After`". Change the
  `Offline` row text so backoff names full jitter, and cap `Throttled` at 15 min only when `Retry-After` is absent.
- `commit.rs:125`: `Self::BaseUnchanged => Outcome::Failed(ErrorClass::Transient)`.
- `disposition.rs`: add `ErrorClass::Transient`, a `retry_jittered(streak, rng)` using the formula above, and use the same
  jitter for `Offline` (`backoff` currently has none). `settle` is unchanged.
- `verify` (`commit.rs:249`): accept a `known_ours: &[Sha256]` set for gap 1.

## 5. Not measured

BC-01 network path and latency; large-N or real throttling (R2 returned no 429/503 in 889 requests); retry-bucket
benefit; the actual jitter implementation in the Rust shell; gap 1 against the Rust code (derived from reading).
The error split (45/35/20) and capacity figure are assumptions, not R2 measurements.

## 6. Commands

| Step | Command | Exit |
|---|---|---|
| probe build | `GOPROXY=off nix develop --max-jobs 2 --cores 2 --command go build -o <scratchpad>/p9-probe .` (in `probe/`) | 0 |
| probe run | `set -a; . ./.env; set +a; p9-probe run <prefix> r2-raw.json` | 0 |
| probe cleanup | `p9-probe cleanup <prefix>` (deleted 201, remaining 0) | 0 |
| sim smoke | `python3 -I sim.py 1` | 0 |
| sim | `python3 -I sim.py 12` (seeds 9000 to 9011) | 0 |

## 7. Full simulation table

| cfg | landed | p50 | p99 | lag p99 | req/landed | wasted/landed | peak rps | recov max s | viol |
|---|---|---|---|---|---|---|---|---|---|
| healthy/a | 16109 | 0.5 | 0.7 | 301 | 3.08 | 0.003 | 6.9 | None | 0 |
| healthy/b | 15927 | 0.5 | 0.7 | 301 | 3.08 | 0.002 | 5.3 | None | 0 |
| healthy/c | 15817 | 0.5 | 0.7 | 301 | 3.08 | 0.002 | 5.2 | None | 0 |
| healthy/d | 15940 | 0.5 | 0.7 | 301 | 3.08 | 0.004 | 5.9 | None | 0 |
| healthy/e | 15927 | 0.5 | 0.7 | 301 | 3.08 | 0.002 | 5.3 | None | 0 |
| healthy/f | 15893 | 0.5 | 0.7 | 301 | 3.08 | 0.002 | 6.0 | None | 0 |
| healthy/g | 15893 | 0.5 | 0.7 | 301 | 3.08 | 0.002 | 6.0 | None | 0 |
| healthy/h | 16143 | 0.5 | 0.7 | 301 | 3.08 | 0.002 | 6.0 | None | 0 |
| err1/a | 15874 | 0.5 | 1.9 | 301 | 3.12 | 0.032 | 7.7 | None | 0 |
| err1/b | 15976 | 0.5 | 1.9 | 301 | 3.12 | 0.029 | 5.9 | None | 0 |
| err1/c | 16237 | 0.5 | 2.0 | 301 | 3.12 | 0.032 | 8.8 | None | 0 |
| err1/d | 15964 | 0.5 | 70.4 | 301 | 3.12 | 0.043 | 5.0 | None | 0 |
| err1/e | 15976 | 0.5 | 1.9 | 301 | 3.12 | 0.029 | 5.9 | None | 0 |
| err1/f | 16450 | 0.5 | 2.6 | 301 | 3.12 | 0.033 | 5.9 | None | 0 |
| err1/g | 16450 | 0.5 | 2.6 | 301 | 3.12 | 0.033 | 5.9 | None | 0 |
| err1/h | 16030 | 0.5 | 1.9 | 301 | 3.12 | 0.030 | 5.6 | None | 0 |
| err5/a | 15742 | 0.5 | 10.6 | 301 | 3.32 | 0.157 | 5.8 | None | 0 |
| err5/b | 15661 | 0.5 | 10.6 | 301 | 3.32 | 0.163 | 6.4 | None | 0 |
| err5/c | 15826 | 0.5 | 11.2 | 301 | 3.33 | 0.164 | 7.1 | None | 0 |
| err5/d | 15779 | 0.5 | 116.9 | 345 | 3.33 | 0.213 | 4.7 | None | 0 |
| err5/e | 15661 | 0.5 | 10.6 | 301 | 3.32 | 0.163 | 6.4 | None | 0 |
| err5/f | 15808 | 0.5 | 10.6 | 301 | 3.32 | 0.161 | 4.8 | None | 0 |
| err5/g | 15808 | 0.5 | 10.6 | 301 | 3.32 | 0.161 | 4.8 | None | 0 |
| err5/h | 15564 | 0.5 | 10.5 | 301 | 3.33 | 0.165 | 9.4 | None | 0 |
| err20/a | 15841 | 0.7 | 20.6 | 304 | 4.33 | 0.837 | 9.2 | None | 0 |
| err20/b | 15798 | 0.7 | 22.1 | 304 | 4.30 | 0.813 | 7.4 | None | 0 |
| err20/c | 16180 | 1.3 | 23.0 | 304 | 4.33 | 0.829 | 7.5 | None | 0 |
| err20/d | 15436 | 0.6 | 325.0 | 501 | 4.31 | 1.041 | 4.2 | None | 0 |
| err20/e | 15798 | 0.7 | 22.1 | 304 | 4.30 | 0.813 | 7.4 | None | 0 |
| err20/f | 15736 | 0.7 | 23.2 | 305 | 4.31 | 0.819 | 6.8 | None | 0 |
| err20/g | 15736 | 0.7 | 23.2 | 305 | 4.31 | 0.819 | 6.8 | None | 0 |
| err20/h | 15725 | 0.8 | 18.5 | 303 | 4.34 | 0.848 | 11.9 | None | 0 |
| outage30s/a | 15843 | 0.5 | 0.8 | 301 | 3.08 | 0.007 | 4.7 | 0.0 | 0 |
| outage30s/b | 15948 | 0.5 | 0.7 | 301 | 3.08 | 0.007 | 4.6 | 0.0 | 0 |
| outage30s/c | 15930 | 0.5 | 0.8 | 301 | 3.08 | 0.007 | 4.9 | 0.0 | 0 |
| outage30s/d | 16340 | 0.5 | 0.7 | 301 | 3.08 | 0.005 | 4.5 | 0.0 | 0 |
| outage30s/e | 15948 | 0.5 | 0.7 | 301 | 3.08 | 0.007 | 4.6 | 0.0 | 0 |
| outage30s/f | 15621 | 0.5 | 0.8 | 301 | 3.09 | 0.008 | 5.8 | 0.0 | 0 |
| outage30s/g | 15621 | 0.5 | 0.8 | 301 | 3.09 | 0.008 | 5.8 | 0.0 | 0 |
| outage30s/h | 16018 | 0.5 | 0.7 | 301 | 3.08 | 0.007 | 6.6 | 0.0 | 0 |
| outage5m/a | 15719 | 0.5 | 335.4 | 427 | 3.21 | 0.148 | 5.6 | 130.0 | 0 |
| outage5m/b | 15881 | 0.5 | 335.4 | 426 | 3.21 | 0.147 | 9.6 | 130.0 | 0 |
| outage5m/c | 15817 | 0.5 | 335.4 | 427 | 3.20 | 0.145 | 5.2 | 370.0 | 0 |
| outage5m/d | 16236 | 0.5 | 246.5 | 400 | 3.11 | 0.067 | 4.9 | 90.0 | 0 |
| outage5m/e | 15881 | 0.5 | 335.4 | 426 | 3.21 | 0.147 | 9.6 | 130.0 | 0 |
| outage5m/f | 15596 | 0.5 | 246.6 | 403 | 3.21 | 0.147 | 5.6 | 120.0 | 0 |
| outage5m/g | 15596 | 0.5 | 246.6 | 403 | 3.21 | 0.147 | 5.6 | 120.0 | 0 |
| outage5m/h | 16014 | 0.5 | 260.5 | 439 | 3.22 | 0.159 | 6.5 | 500.0 | 0 |
| storm5m/a | 15926 | 0.5 | 256.5 | 395 | 3.31 | 0.187 | 4.4 | 260.0 | 0 |
| storm5m/b | 16165 | 0.5 | 256.4 | 391 | 3.31 | 0.183 | 7.1 | 250.0 | 0 |
| storm5m/c | 16277 | 0.5 | 256.4 | 391 | 3.30 | 0.186 | 6.4 | 250.0 | 0 |
| storm5m/d | 15969 | 0.5 | 164.8 | 339 | 3.16 | 0.077 | 5.2 | 110.0 | 0 |
| storm5m/e | 16165 | 0.5 | 256.4 | 391 | 3.31 | 0.183 | 7.1 | 250.0 | 0 |
| storm5m/f | 15805 | 0.5 | 192.1 | 359 | 3.32 | 0.190 | 6.5 | 90.0 | 0 |
| storm5m/g | 15805 | 0.5 | 192.1 | 359 | 3.32 | 0.190 | 6.5 | 90.0 | 0 |
| storm5m/h | 15689 | 0.5 | 211.8 | 425 | 3.33 | 0.197 | 6.0 | 850.0 | 0 |

## Parent verification and hybrid run (2026-11-09)

- **Reproduced.** `python3 -I sim.py 12` in a clean scratch copy, exit 0 in 25 s. Its
  `results` are identical to `p11-commit-retry/sim-results.json`.
- **R2 after the probe.** A full listing shows 2 objects, both `delivery-artifacts/`, and 0
  multipart uploads. No credential value, endpoint or account id appears in any prototype
  file (checked by fixed-string search against the loaded `.env`).
- **Hybrid (approach `i`, untested by the worker).** `p11-commit-retry/sim_hybrid.py` is
  `sim.py` with one change: the in-commit rebase limit is 0 when the owner already has a
  failure streak, otherwise 4, with the `f` capture backoff. It runs a, d, f and i only,
  over the same 12 seeds (exit 0, 13 s). Its a, d and f rows equal the original exactly.
  Results in `p11-commit-retry/sim-hybrid-results.json`:

  | Scenario | f p99 s | i p99 s | f exhaustions | i exhaustions |
  |---|---|---|---|---|
  | err5 | 10.6 | 10.8 | 0 | 58 |
  | err20 | 23.2 | 40.5 | 0 | 726 |
  | outage5m | 246.6 | 246.3 | 0 | 2 |
  | storm5m | 192.1 | 196.7 | 2 | 71 |

  The hybrid is no better under bursts and worse under independent errors. **`f` stands.**
- **Gap 1 is narrower than stated.** Rebase retries inside one commit reuse the same manifest
  hash (`commit.rs` verify, `current.manifest == ours`), so a late landing from those is
  already `AlreadyOurs`. The misclassification needs a late `current.json` write from an
  earlier, failed capture to land after `recover_base` and before the next capture's step 3.
  The manifest header records `writer` {name, version, commit} and no host, but the owner
  key contains the machine id, so outside a cloned machine id a `Foreign` verdict for a hash
  this daemon built is always its own late write. The in-memory `known_ours` set per owner
  is the right size of fix. It still needs a Rust test that drives this interleaving.
