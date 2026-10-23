# Production-readiness verification

> **Status (2026-10-23):** Historical v3 readiness and deployment evidence, not present production health or replacement acceptance. The dated successful runs below do not supersede the subsequently degraded deployment. Follow the agreed canonical [session-capture contract](session-capture-contract.md) for the controlled Mac + BC-01 rebuild, not the old repair/rollout stack. An opt-in [replacement candidate](session-capture-phase3.md) is implemented with [passing 2026-10-24 offline gates](session-capture-offline-evidence.md), but is not release-accepted or deployed and has not activated defaults; an R2 reset is permitted only after replacement acceptance and reviewed cutover, preserving identities, exclusions, learned roots and original local sources.

This change follows the two-machine archive audit (a Mac and a Linux VM). The audit found
correct checksums and retained generations, but also a secret-bearing Pi migration backup,
missing Pi child-session associations, excessive database uploads, and repeated serial
snapshot dependency checks. Rotating the exposed provider keys does not replace fixing
capture policy. Raw transcripts remain byte-exact: analytical de-duplication belongs in a
rebuildable normalisation stage, not destructive filtering in the collector.

## Recorded rollout status (2026-10-16)

The operator approved local checks in place of unavailable hosted CI, publication of
`v0.3.0`, and cleanup of the experimental archive/state. This section records that dated
rollout, not current deployment health; later missing-piece recovery and consumer gaps
motivate the agreed replacement. The dated evidence below remains historical evidence.

- `v0.3.0` points to `9ff2b42` and is published as the latest immutable release.
  All ten uploaded asset digests matched the clean local build; GitHub release
  attestation verification passed. This attests published content, not build provenance.
- The tagged source passed local `make check` and all nine fuzz targets. The subsequent
  workflow-only tag-selection fix (`032c787`) also passed a fresh `make check`.
- Both machine configurations target the same bucket. With neither collector running
  and no collector hooks installed, its full inventory matched the preserved offline
  backup: **26,624 objects / 1,350,478,068 bytes**. Every backup SHA-256 and single-part
  ETag matched; the independent v3 audit again passed before deletion.
- Cleanup deleted only those exact inventoried keys. The bucket then contained **zero
  objects and zero multipart uploads**. No remote exclusions were present.
- Both machines' SQLite databases passed integrity checks. Their old state directories
  and stale `3a59dd8-dirty` binaries were quarantined under private
  `~/.local/share/agent-sessions/archive-backups/cleanup-v0.3.0-*` directories. Fresh
  active state directories were empty before the production backfills below.
  Identities, credential configs and original agent sources were not changed by
  cleanup. No persistent state exclusions were present.
- The complete historical archive remains at
  `~/.local/share/agent-sessions/archive-backups/before-v0.2.0` on the Mac. Preserve it:
  old generations may no longer exist in agent source directories. Cleanup inventories,
  verification results and private operation logs are also retained under that backup
  parent, outside temporary storage.
- The operator subsequently chose the verified release installer instead of Nix on
  both machines. The collector Nix dependency was removed from the host configuration
  repository; both host configurations evaluate without private-repository access. The
  explicit post-login install wrapper verifies the pinned installer asset before
  execution, installs only the binary, and refuses hosts not approved for it.

### Executed production deployment and cold R2 audit

The operator then authorised deployment and populated-backfill verification on the Mac
and the Linux VM only. No other host was connected to or deployed.

- Both normal login shells resolve `~/.local/bin/agent-sessions`, version **0.3.0**.
  Both installed binary SHA-256 digests match the immutable release assets. The
  release installer itself was verified before execution. The Linux VM used a credential
  passed through encrypted stdin into the installation process environment only;
  its existing GitHub login was not changed or supplemented on disk.
- Immediately before capture, independent R2 listings again verified **zero objects
  and zero multipart uploads**, and both active state directories were empty. The
  original machine identities and R2 configs were retained.
- Managed Claude hooks (**8/8**), Codex hooks (**6/6**) and the Pi extension were
  installed on both machines. Claude retention was preserved/raised to 3650 days.
  Doctor's R2 write/read/delete probes passed; its only remaining failures are Codex
  trust (**0/6** on each machine). The operator must review `/hooks` in Codex on both
  machines; no trust hashes were manufactured. Existing Pi processes need `/reload`.
- The Mac's initial backfill completed in **2472.15 s**: **5,404 files / 998 manifests,
  zero errors**. The Linux VM's real Pi startup sweep populated its archive first; the
  initial explicit backfill hit lock contention, then a successful retry uploaded
  **2 files, 3,087 unchanged, one manifest, zero errors**. Both subsequent manual
  sweeps passed with zero errors; both local SQLite integrity checks passed and
  neither state had pending manifests at verification.
- Real Pi hooks produced automatic start/shutdown captures on the Mac, and
  start/settled/shutdown captures plus sweeps on the Linux VM. A fresh local Pi prompt hit a
  provider usage limit, so this is lifecycle/capture evidence, not successful
  real-provider response evidence. Its launch and persisted session were captured.
- Capture-policy skips were retained: literal secret assignments, symlink directories
  and unresolved symlinks were not bypassed to inflate completeness counts.

At **2026-10-16 06:18:20 UTC**, a fresh private mirror matched a stable full-bucket
inventory across acquisition: **28,442 objects / 1,475,823,435 encoded bytes**. Every
single-part ETag matched local bytes; there were no multipart uploads, legacy roots,
unknown roots or unexpected machine IDs. The strict independent decoder passed all
committed descriptor chains, plan/commit pairs, dependencies, piece hashes and
whole-file reconstructions. Its per-agent current stream/session counts were:

| Machine | Agent | File streams | Session manifests |
| --- | --- | ---: | ---: |
| Mac | Claude | 3,643 | 424 |
| Mac | Codex | 132 | 18 |
| Mac | Pi | 1,636 | 558 |
| Linux VM | Claude | 1,483 | 78 |
| Linux VM | Codex | 228 | 114 |
| Linux VM | Pi | 1,390 | 66 |

All child-file streams had their own-session manifest membership. Known credential-file
and migration-backup path counters were zero (not a comprehensive transcript secret
scan). One stable original transcript per agent per machine matched its cold R2
reconstruction. All **12 archived Codex database copies** (seven Mac, five Linux VM)
passed SQLite integrity checks. The archive continues to grow after this snapshot.

Private commands, logs, inventories, mirror, source comparisons, database checks and
audit reports are retained on the Mac under
`~/.local/share/agent-sessions/rollouts/v0.3.0-20261016T052823Z/`. These contain private
session data; do not publish them. A fresh local `make check` also passed during rollout.

## Rollout gates

The following are the historical v3 gate categories, with reusable safety checks. They
are not a requirement to restore every legacy generation before replacement. For the
new candidate, the agreed [session-capture contract](session-capture-contract.md) governs
acceptance: prove durable spool/restart/retry, per-source progress, failure isolation,
complete source-epoch snapshots, and the actual shared reader → analytics → report path
using writer-generated fixtures and real source samples. Collector exit status alone is
not acceptance. Keep unsupported formats and per-session failures explicit.

Historical v3 gate list (retain its exact test meaning when reproducing old evidence):

1. `make check` with live-test opt-ins disabled: formatting, lint, go fix, tidy, deadcode,
   vulnerabilities, race-enabled tests, coverage, CRAP, benchmark smoke and workflow checks.
2. Fuzz targets, including capture-policy and chunk-boundary regressions where applicable.
3. Isolated CLI verification, with exact reconstructed content and cold-cache recovery.
4. Sandboxed native Claude, Codex and Pi lifecycle acceptance, without an unsandboxed fallback.
5. Namespace-isolated live R2 acceptance with verified object/multipart cleanup.
6. Before/after benchmark comparison, reporting workload changes and noisy samples rather
   than calling unlike workloads a speedup.

An offline/local-store pass is not a live R2 pass. A cross-build is not Linux runtime
acceptance. A successful explicit backfill does not prove that installed hooks or Codex
hook approval work.

## Historical v3 wire-format and compatibility boundary

In that v3 update, key templates stayed unchanged. The updated v3 contract adds proven `append` generations,
retained-base folds, fixed database chunks, bounded transcript observations and child roles;
file dependency arrays are now derived from the ordered pieces. See
[storage-layout.md](storage-layout.md). The golden bucket changes intentionally remove idle
folds and redundant immutable requests; duplicate new-piece PUTs no longer inflate byte totals.
The new reader accepts matching legacy explicit dependencies. Older readers must be upgraded
before consuming the new append/derived-dependency records; mixed-version writers are not a
supported rollout strategy.

## Replacement deployment boundary (agreed direction, not executed)

The operator accepts a controlled rebuild from eligible local sources on this Mac and
BC-01, with an R2 reset after replacement acceptance. No legacy compatibility engine,
pruned-piece repair, or old cold-recovery patch stack is a prerequisite. Inventory both
machines' available sources and report the loss boundary: remote-only transcripts,
removed epochs or provenance may not be reproducible. Do not claim the local rebuild
recreates all previous archive history.

Before cutover, preserve each machine's existing `machine-id`, private config, effective
opt-outs/exclusions (including remote-only records), learned nondefault roots and original
source directories. Reset does not revoke exclusions. Do not indiscriminately remove
pending bytes/state or use a new machine ID to work around a cache problem. Stop or replace
old workers and hook launch paths on both machines; verify they cannot resume writing the
retired namespace. Removing lock files is not a way to stop writers.

Validate an isolated new namespace before deletion. Establish a fresh archive-instance
identity and state binding, rebuild through the normal durable queue with resumable
per-source progress, and rebuild analytics. Verify exact eligible session/source/epoch
coverage, restored body hashes, import idempotency, incoming-update/report freshness and
explicit failures on both machines. New publications must depend only on new-namespace
objects. Preserve local research/classification artifacts and explicitly preserve or
retire old citations; never silently attach them to new publications.

Only then remove the reviewed exact retired R2 destination/prefix inventory, without
removing validated new publications or unrelated bucket data; verify the result. This is
not a requirement for a permanent legacy backup. Existing private historical evidence is
not deleted by this authorization or this documentation cleanup.

Installation changes real agent settings. Verify each installed hook and Pi extension;
Codex hook trust may still require the operator's interactive `/hooks` approval. Use a
traceable committed binary, not `-dirty` version metadata. The dated v3 descriptor-chain
checks remain useful historical evidence, not a substitute for real consumer acceptance.

## Local merge acceptance (2026-10-15)

The operator explicitly accepted local CI in place of unavailable hosted CI for merging.
On the unchanged `c88e190` Go source, a fresh Darwin arm64 / Go 1.27.2 run passed:

- `go mod verify` and the complete `make check`, with the original execution settings:
  **89.2% coverage (5,675/6,363)**, zero CRAP >30 (combined 3,597.29, average 5.54);
  syncer race/coverage 501.555 s, plus the complete benchmark smoke run.
- All nine `make fuzz FUZZTIME=30s` targets.
- Zizmor 1.30.1, pedantic persona, no findings. Its default offline mode does not
  establish provider-dependent online audit results.

Linux coverage remains **incomplete**, not silently green: the isolated amd64 run hit
600 s both with two Go CPUs and with the four-core host's normal allocation. A diagnostic
20-minute package window also timed out; that unsuccessful Makefile experiment was fully
reverted. No timeout, test assertion, race flag, scenario or quality threshold was changed
for the merge. This is local macOS merge evidence, not a complete hosted OS-matrix pass.
Release/rollout remains a separate decision; no archive reset or installation occurred.

## Publication hold (2026-10-15)

The `v0.2.0` tag points to `acf839d`; **no release was published**. Its GitHub Linux
coverage jobs reached the existing 600-second package timeout; macOS, fuzz, static and
workflow-security jobs passed. No timeout, coverage, CRAP, lint or acceptance gate was relaxed.

Follow-up Go source `c88e190` removes ordinary-filename regexp allocation churn using a
conservative necessary-condition gate (the original regexp remains authoritative), with
oracle/fuzz equivalence and allocation regressions. Two tests now observe immutable-object
mtimes once per assertion phase instead of quadratically rescanning the bucket; every
comparison, dataset, seed and scenario remains. Its local `make check` passed: **89.1%
(5,672/6,363)**, zero CRAP >30; syncer race/coverage completed in **477.250 s**.

The follow-up GitHub run **38721025170** started **zero steps** in every job because of
an account-level CI-provider restriction requiring owner action. CI availability must be
restored before it can establish whether the Linux timeout is resolved and the release
workflow can publish. Do not retag the already
pushed `v0.2.0`; use a new version for a successful follow-up candidate. Fresh final-source
full benchmarks/native/R2 evidence still needs recording after the follow-up is frozen.

At this checkpoint production remained unchanged: **no installation, hook rollout,
reset or fresh backfill**. The recorded October 16 rollout above supersedes this historical snapshot, not the agreed replacement direction.
A private durable offline backup now preserves all **26,624 old objects / 1,350,478,068
encoded bytes**, including the 392 legacy objects. All 26,623 single-part ETags matched;
the strict v3 audit again verified all 8,296 file and 1,257 manifest generations. This copy
must survive temporary-workspace cleanup because old versions can outlive their local
sources. Both identities/configs and all observed opt-outs remain untouched.

## Earlier candidate evidence status

The unmodified baseline at commit `3a59dd8` passed `make check` before implementation.
The final Go-source candidate `a95976a` passed `make check`: **89.2% total coverage
(5,668/6,357)**, all package/total thresholds, and zero functions above CRAP 30
(combined 3,597.40, average 5.55). The earlier 89.0% intermediate pass was not
substituted for this final gate. All nine fuzz targets independently completed
the standard 30-second runs without failure.

The updated independent decoder also audited the retained old mirror: 26,232 objects,
8,296 file generations and 1,257 manifest generations, with no integrity failure. Local
race-enabled CLI and fake-S3 chunk acceptance passed after the concurrent PUT integration;
these are not native-agent or live-R2 results.

Additional executed gates on that Go source:

- Full verification-helper local journeys: setup, capture, privacy, hooks and regression;
  35 recorded command actions passed, with resulting state and strict reconstruction checks.
- macOS sandboxed native lifecycle: Claude 2.1.296, Codex 0.162.1 and Pi 1.1.0 all
  passed; the actual agents produced the synthetic assistant reply and all required
  start/turn/end events. Final sources reconstructed exactly (234,854 / 40,396 / 3,362 bytes),
  without an explicit sync/backfill after the agents. All three proof trees were imported.
- Linux amd64 test host / Go 1.27.2: all eight acceptance-prefixed CLI journeys passed
  under race (174.487 s), plus focused chunk, deletion-fence and Linux ancestry tests
  (syncer 72.774 s). This is runtime evidence, not merely a cross-build.
- Exact live R2 acceptance: **218.61 s**, including the new 22 MiB database-role fixture;
  two historical generations restored after two entire cache losses. Cleanup deleted
  72 owned objects and verified zero remaining objects and multipart uploads.
- Both ten-sample full benchmark runs and benchstat completed. See [benchmarks.md](benchmarks.md)
  for the allocation/small-backfill regressions, noisy baseline and non-equivalent fixture
  exclusions; not every elapsed-time row is a valid speedup claim.

The actual installed Linux versions were checked separately: **Claude 2.1.296,
Codex 0.162.0, Pi 1.2.0**, using their unchanged public package/runtime files (Node 22.23.3).
An operator-only, test-only adaptation of `a95976a` (`4664334`, not a production-code
change; [reproducible fixture patch](verification/linux-native-2026-10-15.patch)) ran the entire Go/fake-server/agent/observer/collector process tree inside bubblewrap's
fresh mount, PID and network namespaces. No host home, configuration or credentials
were mounted. Mandatory positive/negative controls proved loopback and scratch access,
read-only/unmounted synthetic canaries, numeric external IPv4/IPv6 `ENETUNREACH`, and
identical confinement in a real `setsid` descendant before any agent launch. Linux's
namespace-local loopback scope is broader than macOS's exact-port network rule.

All three strict native lifecycle/archive assertions passed twice (one diagnostic trace,
one fresh untraced run). The untraced run's agent phase took **18.27 s**: Claude 5.20 s,
Codex 7.32 s, Pi 5.75 s. No explicit sync/backfill replaced real hooks. An earlier test
process exited with SIGPIPE before any native case began; that unexplained fixture/tooling
failure is retained with the evidence, not rewritten as a pass. Both complete subsequent
runs kept the original lifecycle, trust and byte-exact assertions unchanged.
Normal installed Git package layouts were observed on both machines (one repository each,
no split indexes or linked worktrees, no observation failures). Split-index, SHA-256 and
attached linked-worktree compatibility are not established by this run.

At this earlier checkpoint release, real installation, archive reset and backfill had
**not yet occurred**; see the recorded October 16 rollout above for subsequent operations. These gate
results are not a claim of real Codex hook approval, real-provider acceptance or every hook
lifecycle edge.
