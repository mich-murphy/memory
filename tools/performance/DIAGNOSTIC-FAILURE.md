# Diagnostic failure history and filesystem prerequisite evidence

**Status (2026-10-30 01:14 UTC): no accepted matched primary performance comparison;
execution is blocked by the missing pinned worker image.**
The distinct attempts below must not be collapsed into a passing result or a
production root-cause claim. Parent owns integrated evidence/acceptance. Results
are parent/runtime-owner reports except where explicitly marked as the fixture
owner's read-only synthetic-spool inspection. No production repair, source reset,
security-check weakening, deployment or installed-source access occurred.

All private job evidence below is under:
`~/.local/state/machines-validation-pilot/memory-perf-20261029/runtime/owner-20261029-linuxprep/jobs/`.
Preserve the original job logs, metadata and aggregate result files unchanged.

## 1. Original primary: warm-up failure, active-only sample

Parent reported both development builds and deterministic unit tests x20 passed.
Baseline `primary-1793315653259407000` then failed in second-runtime discovery
warm-up at the unchanged runtime failure/partial-work guard (reported overlay
line 732). Active delivery alone emitted **35.523 seconds wall / 11.730356 process
CPU seconds**. No settled or append result; no candidate primary or profiles.
This is not a passing diagnostic or a performance comparison.

Evidence: that job's `worker.log`, `job.json`, related worker metadata, and the
owner's `DEVELOPMENT-RESULTS.json`. The synthetic root auto-removed; no spool remains
from this attempt. The fixture owner did not rerun or independently reread this
job's logs.

## 2. Scalar diagnostic v2: sole Pi SQLite quarantine

Overlay `f2748a7579a528a3f4eeeb02084ed9c53ed37ce46ded1897771b9da22a91dcf4`:
parent reported baseline build and all three then-existing units x20 passed.
`primary-1793316070264664000` failed warm-up with:

- Quarantined 1; paused false; step errors 0.
- Sources 201; pending sources 1; pending publications 0; discovery complete false.
- Fixed categories `source/pi/transient=1`, `quarantine/transient=1`; two capture
  attempts; all 201 jobs published.

Its failed synthetic fixture was retained at that job's
`out/synthetic-fixture`. Fixture-owner read-only inspection confirmed the sole
failed request was relative layout `home/.pi/agent/state.sqlite`, role `artifact`,
transform `sqlite-backup`, observed version 8 / captured version 6. The three
closed source databases were byte-identical 8192-byte files, mode 0600, rollback
SQLite headers, no WAL/SHM/journal sidecars; prepared directory contained no files.
Claude/Codex captured version 8. Earlier successful Pi recaptures therefore existed.
The durable queue retained only `transient`, not the underlying exception. These
facts did not establish an exact error mechanism or justify a fixture/production
repair. See the parent's diagnostic-v2 aggregate files for full private metadata.

## 3. Fallback diagnostic: failure before the runtime callback

Overlay `212f89bb4a81ca885e7211c9adf0ba0144ec0b8a2afd444577b026f2ed481756`
added only an opt-in supported fallback-classification diagnostic. Parent inspected
`diagnostic-1793316844155291000/worker.log`: the test failed in the direct
`Queue.Capture` setup loop **before `q.Run`**, reported line 889, in 4.39 seconds,
with fixed signature `safefile: replaced or nonregular input`. The root was
preserved. The runtime fallback callback could not observe this pre-runtime error.
Parent subsequently verified the runtime owner's completed result: baseline build
`build-1793316805505676000` passed module verification and all four then-existing
units x20. That build pass does not make the failed diagnostic a pass.

The captured state was on a Mac host-bind exposed through Docker. This made
filesystem prerequisite suitability a concrete diagnostic question, not proof
of an application defect. `OpenResolved` rejects a nonregular or non-`SameFile`
descriptor before reads and closes that descriptor. No guard was bypassed.

## 4. Identical focused filesystem probe: host-bind versus VM-native

Current Go overlay SHA-256:
`ea7ce33a08d044cc21feb18c4c0f8e8526fb11ae5f2204a22571447d90486f5a`.
Independent reviewer `e41` accepted the bounded probe. Parent reported:

- `build-1793317643779187000`: module verification and all four unit tests x20
  **PASS**.
- Same binary, probe bytes and root mount path `/scratch/synthetic-fixture`:

  | Job | Captured-state filesystem | Result | Safe opens | Capture/recapture |
  | --- | --- | --- | --- | --- |
  | `fs-probe-1793317682526270000` | Mac host-bind; reported type `UNKNOWN0x6a656a63` | FAIL, exit 1 | 1000 attempts, 0 failures | 60 attempts, 7 failures |
  | `fs-probe-1793317714477135000` | Task-owned VM-native scratch; reported `ext2/ext3` | PASS, exit 0 | 1000 attempts, 0 failures | 60 attempts, 0 failures |

All seven host-bind capture failures had fixed signature
`safefile_replaced_or_nonregular`. External original-path pre/post observations
remained regular with unchanged device/inode. **Those observations do not identify
the internally rejected descriptor**, and no direct safe-open failures occurred.
Do not infer universal host-bind behavior, an exact filesystem/SQLite root cause,
or production performance from this bounded difference.

Evidence: the two probe jobs' `worker.log`/job metadata and the runtime owner's
`FS-PROBE1-RESULTS.json` aggregate. These probe results were inspected/reported by
parent; the fixture owner did not execute or independently reread those jobs.
They accept only the measured VM-native scratch environment as suitable for this
bounded prerequisite probe. They do not qualify a full primary, race/native/live
capture, optional work or operating-scale performance.

## 5. Full native-state pilot, then corrected native-TMP pair

`primary-1793318183068977000` passed all 201 exact reader hashes, stable epochs,
credential negatives and three exact appends. Its settled sample used 8.206265
process CPU-seconds over 30.008671 wall seconds, 6,030 discovery requests and 270
preparations with zero publication churn. Parent then found `TMPDIR=/out/tmp`
remained host-bound; `publication.writeMetadata` uses the default temp directory.
This is explicitly an **unmatched pilot**, not all-native performance acceptance.

Runtime TMP was moved to `/scratch/runtime-tmp` identically for both refs:

| Job / ref | Actual result | Active wall / CPU | Settled wall / CPU | Append wall / CPU |
| --- | --- | --- | --- | --- |
| `primary-1793318817849421000` / `b8cbed2` | PASS, full correctness | 36.289658s / 3.911607s | 30.002873s / 10.274496s | 0.804011s / 0.450022s |
| `primary-1793318924869156000` / `c8fdf7a` | FAIL, original 30-CPU-second discovery-warm-up bound | 38.006161s / 11.674618s | Absent | Absent |

Both active samples had 201 ACKs, 201 CAS, 402 PUTs and 530 backend opens. This is
one partial pair, **not** an accepted speedup/regression estimate or complete
candidate result. The three-CPU-second settled target is diagnostic, not a release
budget. No bound or readiness predicate was weakened. Roots and exact failures
were preserved; OOM was false and exact owned CIDs removed.

## 6. Whole-test failure profile, canonical bounded analysis

`failure-profile-1793319639828564000` reproduced the candidate warm-up CPU failure
in 86.88 seconds. It used the unchanged `ea7…` overlay and an external Go test CPU
profile, explicitly diagnostic/non-primary. Its `profiled:false` scalar field
referred only to the internal phase profiler; it does not make this externally
profiled run a primary timing run.

The ordinary test failure flushed a 155,949-byte profile, SHA256
`26489b041ee4dad2d689f81435ea203eee6209e232d9f417c3305c4f3722f08c`.
Canonical `failure-embedded-analysis-1793320154117077000` passed with the original
8-second per-command / 60-second aggregate limits, embedded symbols only and no
binary argument. It decoded 4,135 samples / 41.35 sampled CPU-seconds over 86.89
wall seconds. No loss marker was observed; sampling loss was not independently
measured. Inclusive policy 16.88s, discovery 15.93s, filesystem policy 10.83s and
delivery-lane selection 9.55s **overlap**; Syscall6 was 12.16s flat. Setup, active
and failed warm-up are combined, not phase-isolated or comparative attribution.

The initial local-symbol analysis timed out at eight seconds. An intermediate
45-second-per-command analysis is preserved as **noncompliant/noncanonical and
rejected for acceptance**. Only the independently reproduced original-bound
embedded analysis is canonical; the diagnostic workload itself never passed.

Closed synthetic progress showed 201 jobs / zero pending publications, stable
raw versions and repeated completed discovery generations. These post-shutdown
snapshots do not establish the live readiness conjunction, identity churn,
starvation or a causal link to the historical Mac service exit.

## 7. Attribution-v4 build passes, unexpected baseline stall

Parent and independent reviewer accepted the test-only instrumentation statically:
queue policy forwarding/counts, split user/system rusage, warm-up profile and a
fixed non-atomic status histogram. Actual discovery, source fixtures, guard
predicates, polling and limits remain unchanged. Overlay SHA256:
`f35cfc3e9f2816beb8f02a72f9ac9a0c21fa83e3ca6aa29c35a3d725fd13e9d7`.

Both `build-1793321643112997000` (baseline) and `build-1793321678885400000`
(candidate) passed offline module verification and all six units x20. Nevertheless,
baseline `profile-1793321712170839000`, native slot07, timed out at the original
300-second payload bound: actual Docker exit124, OOMfalse. It emitted only test
start, **no** phase scalars/work counts/histogram. `active.pprof` was zero bytes
and unusable; the other profiles were missing; no analysis ran. The EXIT trap's
`PAYLOAD_STATUS=0` under signal was misleading; actual final exit124 is authoritative.
Candidate attribution was not launched and slot08 was not reserved. The standalone
unit passes do not prove this runtime harness can complete or that its stall is a
production defect.

## 8. Shorter-watchdog diagnostic blocked before execution

Parent authorized one baseline-only stall diagnostic using a **lowered 120-second
Go test watchdog**, unchanged original 300-second internal context/outer payload
and CPU guards, fresh slot09, private traceback preservation and optional read-only
prior metadata. `profile-1793322681083342000` failed at Docker create: pinned image
`sha256:b60c73ed15920a0fd3faf286a269ccb1e207bf7b1a0deed19a7635559e0bad05`
was absent. Driver70 / CIDnull means **no worker, lease payload, Go test, prior
metadata query or traceback ran**. It is a prerequisite failure, not a gate result.

Parent independently checked at 2026-10-30 01:14:23 UTC: exact image inspect exit1,
image inventory0, running containers0, daemon still4CPU/10,417,442,816B; expected
task/shared volumes remained. The disappearance's cause and relation to the prior
stall are unknown. No pull, replacement, daemon/service restoration, source reset
or volume cleanup was attempted. Slot09's reservation remains evidence, not reusable
space. Private `STALL-V5-BLOCKED-RESULTS.json` SHA256:
`12a0d680f9e91fc54857c7ec863ed671462a132e086ee57bb946195706c61b65`.

## Current interpretation boundary

The [delivery brief](../../docs/session-capture-handoff.md) owns current next action
and authority. Supported pinned-worker availability must be restored before another
admitted diagnostic; native Darwin admission is separately absent. Neither a new
image nor the unrelated services may be silently substituted/restored. Retain all
old roots, volumes, reservations and exact failures. No production fix, completed
performance comparison, race/native/live acceptance, deployment or activation has
been established. Mac host-bind remains unqualified for captured runtime state;
future comparisons require matched VM-native state/temp paths, overlay, toolchain,
seed, settings, resource profile and honest contention/cache evidence.

## Corrected early Pi-layout hypothesis

An early parent hypothesis considered classifying a Pi main before creating its
sibling artifact directory. Parent then checked baseline `isAgentDir`: canonical/
Dir roots are already agent roots, and this fixture uses canonical
`home/.pi/agent`. The hypothesis was unproven and likely inapplicable, not an
established cause or CPU attribution. Construction order, all 201 sources,
quarantine/error/identity/freshness guards and diagnostic budgets remain intact.
