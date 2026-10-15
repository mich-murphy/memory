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
An intermediate integrated candidate passed all of `make check` with 89.0% total coverage
and zero functions above the CRAP limit. Later chunk-concurrency and privacy refinements
require a fresh final gate; the intermediate pass is not substituted for it.

The updated independent decoder also audited the retained old mirror: 26,232 objects,
8,296 file generations and 1,257 manifest generations, with no integrity failure. Local
race-enabled CLI and fake-S3 chunk acceptance passed after the concurrent PUT integration;
these are not native-agent or live-R2 results.

Final candidate verification and deployment results will be recorded here when executed;
this document does not yet claim that the final candidate passed or was deployed.
