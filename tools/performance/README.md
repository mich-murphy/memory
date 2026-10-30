# Offline matched runtime diagnostic (not qualification)

This is a deliberately small, opt-in Linux Go test overlay. **No completed primary
comparison yet.** Parent reported distinct baseline warm-up/setup failures, then
module verification/all four units x20 passing and an identical focused filesystem
probe failing on Mac host-bind but passing on task-owned VM-native scratch. See
[dated failure/probe evidence and limits](DIAGNOSTIC-FAILURE.md). One full baseline
on VM-native scratch is authorized/in progress; no result is implied here.
The historical runs used overlay `ea7ce33a08d044cc21feb18c4c0f8e8526fb11ae5f2204a22571447d90486f5a`.
The attribution-v4 overlay below is a new test-only diagnostic, not yet built or run.
It compares `b8cbed2` with `c8fdf7a`; it is not a new quality gate
or evidence of an optimization. No production source replacements, instrumentation
package, hook, service, installed spool, source inventory, cloud or credentials.

## Attribution v4 — opt-in, not primary timing

This version adds only the first paired attribution probe; all fixture data,
composition, discovery, polling, readiness predicates and limits stay unchanged.
Inside the separately authorized admitted worker, enable both:

```sh
MEMORY_PERF_DIAGNOSTIC=1 MEMORY_PERF_WORK_COUNTS=1 \
MEMORY_PERF_PROFILE=/ABS/PRIVATE/profile-dir \
  /ABS/PRIVATE/old.test -test.run '^TestOfflineMatchedPerformance$' -test.v -test.timeout=300s
```

The existing `MEMORY_PERF_ROOT` private fresh-root requirement still applies. Use
identical overlay bytes and settings on each ref. This command is an interface,
not execution authorization. Do not also enable external `-test.cpuprofile`.
The runtime owner must retain the **aggregate hard 64 MiB profile cap**; the Go
writer's per-file check alone does not provide an aggregate hard cap.

- With work counts enabled, the original `Options.CheckPolicy` is wrapped once:
  each call is counted as `policy_calls/<agent>/<transform>` and forwarded with
  unchanged context, request and error. Fixed agent buckets: claude/codex/pi/other;
  transforms: raw/zstd/sqlite-backup/scrubbed-config/other. Queue policy only:
  **discovery policy is not intercepted**; the actual `r.discovery` closure and
  existing discovery callback/request counters are unchanged.
- `status_polls` counts the existing 20ms wait-loop `RuntimeStatus` invocations.
  Counts remain cumulative, with phase deltas through `perfDelta`. No new timer,
  per-policy resource sampling, live SQL observer or extra status poll is added.
- Existing combined `cpu_seconds` arithmetic is unchanged; samples/deltas also
  expose `user_cpu_seconds` and `system_cpu_seconds` separately.
- Profiles: `active.pprof`, **`warmup.pprof`**, `settled.pprof`, `append.pprof`.
  Each finish is idempotent and registered for cleanup, so ordinary test failure
  flushes after runtime cancel/join defers. A failed warm-up profile can include
  failure diagnostics and teardown before that cleanup; it excludes fixture setup
  and initial delivery, but is not an exact guard-instant cutoff.
- `PERF_WARMUP_SCALAR` records a 16-bin histogram of existing warm-up status
  observations. Bits 0..3 mean DiscoveryComplete, PendingSources==0,
  PendingPublications==0, Sources==201. **NONATOMIC:** those fields come from
  separate SQL statements. No bin proves an atomic overlap or starvation.
  On the unchanged CPU guard, the existing `perfFailureDiagnostic` receives the
  last observed status, and the warm-up row records resource/count deltas at the
  guard observation, before diagnostic reads. No guard or limit is relaxed.
- Work counts require diagnostic mode. Profiled and diagnostic `PERF_SCALAR`
  rows have `primary_timing:false`; warm-up rows are always diagnostic/non-primary.
  With work counts off there is no policy wrapper, poll counter or histogram work.
  Primary timing remains unprofiled with all diagnostic flags unset.

New deterministic units (in addition to the existing four):
`TestPerfPolicyCountsForwardOnceAndPreserveError` and
`TestPerfWarmupHistogramExactBoundaries`. The existing delta unit also checks
nonzero split-CPU deltas and immutability. No build or test result is implied.

**Unchanged fixture limitation:** `Save` receives `Config` by value and this
fixture does not reload it; `c.ConfigPath()` is empty on both refs. Queue policy
protects synthetic-home `.config/agent-sessions/config.json` plus spool/policy/store;
discovery separately receives explicit `runtime.json`. This probe does not repair
that fidelity gap, add executable/cache protection, or change fixture context.

## Declared diagnostic bounds

- Total runtime payload deadline 300 seconds (external worker timeout 300 seconds
  must cover the test; preparation/build is separately admitted).
- Each measured phase at most 30 process CPU seconds; checked at observation
  boundaries, not a CPU hard-limit mechanism. Settled target <=3 CPU seconds per
  30 wall seconds and append target <=10 wall seconds are diagnostics, **not test
  gates**. Failures at resource limits are diagnostics, never passes.
- Fixture tree <=256 MiB logical file bytes, spool <=64 MiB (also configured queue
  encoded-payload cap). Runtime metadata is included in the measured spool tree.
  Bounds are sampled during settled work and checked at completion, not filesystem
  quotas; cgroup/worker admission and external deadline remain necessary.
- Each private CPU profile <=64 MiB. Use a separate profiled run, never compare it
  with a primary timed run. Profiles contain synthetic-only paths; keep private.

## Fixtures / seam / correctness

One byte-identical `fixture_test.go.txt` is injected as
`internal/cli/performance_diagnostic_test.go` in each source tree via Go `-overlay`.
The test uses the actual CLI `runtimeComposition` queue options and discovery
callback, `Queue.Run`, supported adapter classification and `publication.Local`.
The backend wrapper forwards `DurabilityFence.Sync`. There is no mock writer,
transformation replacement, hand-built manifest or second storage reader.

Fixed seed, 32 ordinary 64-KiB JSONL mains (16 deterministic 4-KiB records each) and 32 4-KiB artifacts per adapter:
Claude project tool results, Codex logs, Pi session-directory auxiliary files.
**Codex logs are independent resource publications, not invented rollout children.**
Add one compressed transcript, one safe scrubbed config and one closed SQLite DB
per adapter: 201 expected sources. Supported layouts determine publication counts;
no assertion equates 96 mains to 96 publications. Each config's expected bytes come
from the actual scrubber; SQLite expected bytes are a pre-timing authorized backup
plus an independent SQL row check (same-adapter normalization oracle, not an
independent backup implementation). Compressed expected bytes are decoded originals.
Credential-path negatives reject three synthetic `auth.json` fixtures.

All requests are accepted by `Queue.Capture` before timing. A first `Queue.Run`
with discovery disabled measures delivery of that already accepted unpublished
queue only, then stops/joins. A second runtime starts real CLI discovery and warms
up outside timing before settled/append measurements. Initial delivery ends its
reported sample at the final observed durable ACK, not at the success polling tick. All sources must be captured, no pending publications,
no quarantine/pause/step error, and a complete discovery sweep observed. Then:

1. Authoritative `ExportSessionBounded` checks every expected source and body,
   exact hashes/sizes/path/identity, rejects extra source epochs. Settled also
   asserts zero new PUT/CAS/upload/manifest attempts and ACKs.
2. Actual runtime continues unchanged for 30 seconds; checks continuing discovery,
   exports again and asserts no revision/object/source/epoch advance.
3. Append one raw main per adapter after settling, without hooks/hints/wakes. End
   sample at final durable ACK. Export all again; exactly three revisions advance
   by one, source/epoch identities stable, all other revisions and objects unchanged.

Enrichment is **off** in this first slice. Launch/snapshot assets, alias roots,
active SQLite/WAL mutation, retries/fault injection, live backends and installed
producer paths are **not** qualified. A low settled cost does not resolve a
production performance report: optional work and larger scales remain unqualified.
No production optimization should be inferred or made without attribution.

## Counts and attribution limits

`PERF_SCALAR` JSON records process user+system CPU from `getrusage`, wall time,
Linux `/proc/self/io` deltas (`rchar/wchar`, syscall counts and device-facing
`read_bytes/write_bytes/cancelled_write_bytes`), backend attempted operations and
bytes, discovery callback/request counts and all existing queue Fault hook counts.
Logical IO includes SQLite and the observer itself. Cached reads may have zero
device bytes. Backend read bytes are consumed bytes; PUT/CAS bytes are offered bytes.
Durability Sync counts are not backend PUT counts. Baseline archive setup, fixture
creation, reference normalization and reader assertions are outside timed phases.

`prepared-before-sync`/cleanup show continuing transform work even with no backlog;
`payload-before-sync`, upload/manifest/ACK hooks expose durable work. These are
**hook events, not exact hash calls/bytes or housekeeping operations**. Internal
fingerprint hashing, policy filesystem probes, accounting and housekeeping lacking
hooks are explicitly uncounted. Do not claim a zero count means no such work.
Separate active/warmup/settled/append CPU profiles provide attribution, not timed evidence.

The identical observer polls `RuntimeStatus` every 20ms during active/append and
samples resource counters at ACKs; that overhead is included. Status success can
lag final ACK; timed endpoint samples are taken inside the ACK hook. The recorder
publishes its sequence and sample coherently. An in-progress callback is not a
ready endpoint, even if its backend counter or committed DB state is visible.
The active boundary is the number of actual pending jobs enumerated by supported
`Queue.Status` before timing, excluding already superseded/published jobs—not the
201-source count. No capture/discovery/enrichment runs in that phase;
`deliverAccepted` visits remaining jobs in sequence order and `acknowledge` updates
one job per hook. Afterward every initially pending (identity,sequence) must be
published, and the endpoint must include exactly that many ACKs. Append requires
three further recorded ACKs for its three distinct affected identities, plus the
usual drained/source-capture checks. Delta maps are independent of cumulative
samples so emitting a row cannot rewrite a saved ACK endpoint. Settled work
includes 1-second diagnostic footprint walks. Stop/join, export checks and profile
start/stop are excluded from primary phase deltas. Not a release benchmark.

## Focused filesystem prerequisite probe (separate diagnostic)

Parent reported a subsequent diagnostic job failed in direct staged capture before
`q.Run`, with fixed signature `safefile: replaced or nonregular input`. This is not
proof of a capture defect: the worker output was a Docker Mac host-bind. No
security check is relaxed. `TestOfflineSafeFileIdentityDiagnostic` is a separate
opt-in prerequisite probe, not another 201-source run or primary timing evidence.

Interface, **inside an explicitly admitted pinned worker only**:

```sh
MEMORY_PERF_IDENTITY_ROOT=/scratch/synthetic-fixture \
MEMORY_PERF_KEEP_FAILED=1 \
  /ABS/PRIVATE/old.test -test.run '^TestOfflineSafeFileIdentityDiagnostic$' -test.v -test.timeout=90s
```

The runtime owner/parent completed two independent admitted runs with the same
binary, overlay and `/scratch/synthetic-fixture` root: Mac host-bind failed
(1000 safe opens/0 failures; 60 capture attempts/7 fixed safefile failures), while
**task-owned VM-native scratch** passed (1000/0; 60/0). External original-path
pre/post stats did not identify the internally rejected descriptor. This supports
only bounded VM-scratch prerequisite suitability, not an exact root cause or
universal filesystem claim. The reported filesystem types were host-bind
`UNKNOWN0x6a656a63` and VM-native `ext2/ext3`; do not equate the display label alone
with qualification. See the evidence document for job IDs/limitations.
Any additional authorized paired probe must preserve the same mountpoint,
toolchain, source, settings, overlay bytes and admitted resource profile. This document does not authorize
creating/mounting volumes, host changes, or native execution. The root must not
exist. All source data and spool are created exclusively within that private root;
no installed/original sources or spool are used. Failed-root retention follows
queue close; parent owns volume/evidence lifetime.

Explicit bounds: three small closed SQLite DBs (source indices 0=Claude, 1=Codex,
2=Pi), 1000 total safe-open-loop target-file attempts **including follow-up descriptors**,
at most 20 total capture rounds (initial `Capture`, then `CaptureIfChanged`, three
sources per round, 60 capture attempts), 60-second internal deadline, 30 process
CPU seconds checked at observation boundaries, existing 256-MiB tree/64-MiB spool
caps. There is no 200-source warm-up and no installed activation.

Each safe-open attempt obtains `os.Lstat` immediately before actual
`safefile.OpenResolved`; successes compare returned descriptor stat with expected
using unchanged `os.SameFile`/regular-file checks. Rejection remains a failure.
Because `OpenResolved` closes rejected descriptors, a separately opened read-only
`O_NOFOLLOW` descriptor may supply a **labeled follow-up** stat observation; it is
not the rejected descriptor and never substitutes for successful authorization.
The open-attempt budget includes these follow-ups. SQLite sources stay closed and
static; the existing actual composition/authorization, normalized-backup reference,
exact normalized hash/size and stable epoch/revision/object checks remain intact.
Capture failures log source path metadata before/after, explicitly not descriptor
metadata from inside capture. All open/capture failures fail the probe.

`PERF_IDENTITY_SCALAR` emits only fixed phase/observation, source index/round,
availability/regular/SameFile flags, numeric device/inode, and the existing safe
fixed error signatures/typed numeric SQLite code/errno. No paths, arbitrary type
names, payloads or wholesale errors. Success opens are sampled once per source;
failures and a bounded-count summary are emitted. No causal timing conclusion is
valid from this prerequisite probe. The full 201-source primary remains unchanged.

## Prepare, then request worker admission

Preparation is Python-only and refuses a wrong source revision, tracked dirty
source, or existing output directory. Use absolute paths in private worker inputs:

```sh
python3 /ABS/OVERLAY/tools/performance/prepare.py \
  --source /ABS/OLD --output /ABS/PRIVATE/old-overlay
python3 /ABS/OVERLAY/tools/performance/prepare.py \
  --source /ABS/CURRENT --output /ABS/PRIVATE/current-overlay
```

Both `identity.json` overlay hashes must match. `prepare.py` only prepares and
attests inputs; it does not choose or validate the runtime filesystem, launch a
worker, or grant resource admission. Run it against the exact tracked reference
checkout before staging its sanitized snapshot. The preparer rejects untracked
Go files under cmd/internal and hashes tracked cmd/internal and Go/flake inputs. Parent must stage only sanitized tracked inputs
and verify their hash; never bind the whole checkout, research, .env or HOME.
Default overlay mappings are worker `/src` and `/overlay`; override explicitly
with `--worker-source`/`--worker-overlay` if necessary. Both revisions must use
the SAME worker paths. Host source/output paths need not match. Overlay JSON naturally
has different source/output paths; **the injected Go bytes must match exactly**.

Parent/runtime worker owns the pinned Linux Nix environment, exact source identity,
resource leases, cgroup limits and command execution. Never run the following on
bare Darwin, a parallel unadmitted container, or without the worker-side semaphore
composition required by `~/.config/agents/validation.md`.

Inside separate admitted pinned workers, expose each sanitized revision at the
same `/src` path and its overlay at `/overlay`. Build separately (not timing
compilation), with identical toolchain/environment/build flags/cache policy:

```sh
cd /src
CGO_ENABLED=0 GOTOOLCHAIN=local go test -c -overlay /overlay/overlay.json \
  -o /out/diagnostic.test ./internal/cli
# Repeat with the other sanitized revision at /src and its overlay at /overlay.
```

**Runtime captured state must live on the task-owned VM-native scratch volume**,
not a Mac host-bind. Place synthetic sources, spool, local backend, exports,
reference databases and runtime temp there; use the same absolute root path and
resource-admission profile for both revisions. `/out` may carry bounded scalar
logs/binary artifacts separately, but it must not silently become the captured-state
root if it is host-bound. Parent/runtime owner prepares/validates scratch and owns
its lifecycle; do not invent another resource pool or use a shared/production volume.

Before the payload, run these two deterministic overlay-only regressions on each
binary (no fixture-root variable is needed):

```sh
/ABS/PRIVATE/old.test -test.run '^TestPerf(DeltaPreservesInputs|ACKEndpointWaitsForFinalSample)$' -test.v -test.timeout=30s
# Repeat current.test; independently admit the same selected tests with -race.
# Diagnostic safety regressions:
/ABS/PRIVATE/old.test -test.run '^TestPerf(SafeDiagnosticCategories|FallbackDiagnosticPreservesClassAndPrivacy)$' -test.v -test.timeout=30s
```

The ACK regression holds the final callback **before it starts**, with a previous
sample available and simulated drained status, then separately holds it during
sampling after its counter becomes visible. Neither barrier may expose the prior
sample as the final endpoint. These are channel barriers, not sleep/timing tests.

Use the same absolute private **VM-native scratch** fixture path in separate
sequential executions, for example `/scratch/synthetic-fixture`. The test refuses
an existing root and removes only the root it created. For a diagnostic failure
rerun, explicitly set `MEMORY_PERF_KEEP_FAILED=1`; root retention is on the owned
VM-native volume, not by moving captured state onto Mac host-bound job output. On failure only, the synthetic root remains there **after runtime
stop/join, discovery close and queue close**. This opt-in neither reads nor retains
existing/original sources. A success still removes its root. The public retention
line is scalar-only; obtain its private path from the worker invocation, not logs.
For the approved fallback-error diagnostic, also set `MEMORY_PERF_DIAGNOSTIC=1`.
This attaches the supported `RuntimeOptions.Classify` callback to both runtimes:
known classes are handled by production before this fallback; the callback always
returns exactly `FailureTransient`, preserving the previous default. On fallback
errors only, `PERF_FALLBACK_SCALAR` emits `errors.As` numeric SQLite code/errno and
fixed exact allowlisted signatures from the actual SQLite/capture source errors.
Unknown error text becomes `unknown`; no raw errors, types, paths or substring
matches are emitted. These runs label `PERF_SCALAR` rows `diagnostic:true` and must
not be mixed into primary timing evidence. The separate bounded filesystem probe
above is prerequisite evidence, not part of a primary timing run.

The unchanged runtime failure guard now emits `PERF_FAILURE_SCALAR` with phase,
fixed operation, quarantine/pause/step/source/pending counters, discovery-complete,
and allowlisted category aggregates from bounded diagnostic-only source/quarantine
reads. No status structs, paths, identities, transcripts or raw error strings are
logged by this diagnostic. Unknown category text becomes `other`. The extra reads
run only on failure, so do not alter successful phase timing. Set private runtime
TMPDIR on VM-native scratch and isolated GOCACHE/output directories in the worker. Do not pass host HOME or wholesale
host environment; clear credential-related environment at the worker boundary.

```sh
MEMORY_PERF_ROOT=/scratch/synthetic-fixture \
  /out/diagnostic.test -test.run '^TestOfflineMatchedPerformance$' -test.v -test.timeout=300s
# Repeat with the other revision's binary and identical VM-native root/profile.
# Reverse order only in another separately admitted pair if warranted.
```

Retain exact exit status, `PERF_SCALAR`/`PERF_CORRECTNESS`, source+overlay+toolchain
hashes, worker/cgroup CPU/throttling/IO/memory/events/swap, cache/order/contention
metadata and bounded private logs. Only scalar synthetic lines are public evidence;
never share raw profiles/logs with credentials or source payloads. Primary timing
runs are unprofiled and non-race; independently admit a focused race correctness
run, not a timing comparison. Full/native/live gates remain unrun by this harness.

Later, repeat separately with `MEMORY_PERF_PROFILE=/ABS/PRIVATE/profile-dir` (existing
0700 directory). It creates exclusive `active.pprof`, `settled.pprof`, `append.pprof`.
These rows are labeled `profiled:true`; keep them out of timing tables. A primary
failure must be diagnosed before collecting profiles or claiming a comparison.
