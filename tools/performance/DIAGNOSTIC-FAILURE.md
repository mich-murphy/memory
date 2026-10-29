# Diagnostic failure history and filesystem prerequisite evidence

**Status (2026-10-29): no completed matched primary performance comparison.**
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
Parent initially reported build/all-four-unit checks apparently passing; do not
promote that pending owner summary into stronger evidence here.

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

## Current next action / interpretation boundary

Parent authorized one full baseline primary on task-owned VM-native scratch,
owned by the runtime worker. Its result is pending at this documentation update;
no success is implied. The `ea7…` Go overlay remains byte-identical while it runs.
Future matched baseline/current runs must use the same VM-native root paths,
toolchain, overlay, seed, settings and admitted resource profile. Mac host-bind is
not an accepted captured-state location for the full measurement. Host-side log
copies or prepared read-only source input are separate from runtime captured state.
No production optimization or source repair is justified by the evidence so far.

## Corrected early Pi-layout hypothesis

An early parent hypothesis considered classifying a Pi main before creating its
sibling artifact directory. Parent then checked baseline `isAgentDir`: canonical/
Dir roots are already agent roots, and this fixture uses canonical
`home/.pi/agent`. The hypothesis was unproven and likely inapplicable, not an
established cause or CPU attribution. Construction order, all 201 sources,
quarantine/error/identity/freshness guards and diagnostic budgets remain intact.
