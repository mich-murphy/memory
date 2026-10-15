# Production-readiness verification

This change follows the two-machine archive audit (macbook and bc-01). The audit found
correct checksums and retained generations, but also a secret-bearing Pi migration backup,
missing Pi child-session associations, excessive database uploads, and repeated serial
snapshot dependency checks. Rotating the exposed provider keys does not replace fixing
capture policy. Raw transcripts remain byte-exact: analytical de-duplication belongs in a
rebuildable normalisation stage, not destructive filtering in the collector.

## Rollout gates

Do not reset an archive or install a candidate until the integrated candidate has passed:

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

## Wire-format and compatibility boundary

The key templates stay unchanged. The updated v3 contract adds proven `append` generations,
retained-base folds, fixed database chunks, bounded transcript observations and child roles;
file dependency arrays are now derived from the ordered pieces. See
[storage-layout.md](storage-layout.md). The golden bucket changes intentionally remove idle
folds and redundant immutable requests; duplicate new-piece PUTs no longer inflate byte totals.
The new reader accepts matching legacy explicit dependencies. Older readers must be upgraded
before consuming the new append/derived-dependency records; mixed-version writers are not a
supported rollout strategy.

## Deployment boundary

The operator authorised replacing this fresh experimental archive, including its legacy
sandbox objects, after the fixes pass. Retain each machine's existing `machine-id` and
private config, and preserve any existing persistent opt-outs while replacing the upload
projection. Never select a new identity to work around a cache problem. Stop all
writers before resetting remote objects or local projections; preserve a reviewed object
inventory and verify the destination before any deletion. Do not run old and new writers
against the same machine namespace. Rebuild both machines' local caches after an archive
reset. Use a traceable committed binary, not `-dirty` version metadata.

Installation changes real agent settings. Verify each installed hook and Pi extension;
Codex hook trust may still require the operator's interactive `/hooks` approval. After
backfill, repeat an unchanged sync and a growing-session sync, then independently verify
bucket descriptor chains, pieces, whole-content checksums, secret exclusions, database
integrity, child-session manifest membership and package provenance. Remove private
analysis copies only after the final evidence has been retained safely.

## Evidence status

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
- `bc-01` Linux amd64 / Go 1.27.2: all eight acceptance-prefixed CLI journeys passed
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

Release, real installation, archive reset and backfill have **not yet occurred**. These gate
results are not a claim of real Codex hook approval, real-provider acceptance or every hook
lifecycle edge. Deployment results will be added after execution.
