# Fixed-chunk database publication acceptance

> **Status (2026-10-23):** Historical v3 chunk-publication probe and dated evidence, not acceptance of the replacement. Exact thresholds, PUT counts and historical-restore assertions below belong to this existing test. The agreed canonical [session-capture contract](session-capture-contract.md) starts with complete source-epoch snapshots and requires real reader/analytics consumer acceptance before production cutover; chunking and legacy restoration are not prerequisites.

The existing `internal/store/TestR2LiveAcceptance` and `TestR2AcceptanceFake`
include the same synthetic database-role scenario through normal `syncer.New`.
This document describes the probe and its bounded executed result below, not an overall
release-readiness claim.

## Scenario and oracles

- Use the acceptance test's **existing owned namespace**, confinement adapter,
  preflight and failure-safe cleanup. No second namespace or production option.
- A separate disposable HOME, source, config path and SQLite state directory hold
  only deterministic synthetic bytes. A fake adapter copies one closed fixture to
  a private consistent export under the engine's export directory and returns
  `RoleDB` with `SourcePath`. Normal discovery and capture then stage and chunk it.
  No launch/cwd is supplied; no real agent sources or managed settings are read.
- The 22 MiB binary page-like fixture exceeds the production **8 MiB** database
  threshold. Descriptors must contain exactly **22 fixed 1 MiB pieces**, at the
  exact contiguous offsets with matching content digests. There are **21 unique
  chunks**, more than the 16-worker dependency pool; the last chunk repeats chunk
  five (zero-based). All initial piece objects must be absent before publication.
- Initial publication must PUT each unique chunk exactly once. An identical
  export must retain its descriptor and PUT no blobs. A one-byte header-like
  update must publish the next descriptor with the correct parent, change only
  the first piece and PUT only that new chunk. Namespace instrumentation counts
  attempted blob PUTs, so redundant overwrites cannot hide in an unchanged final
  object set. Engine uploaded-byte totals must equal the unique new encoded bytes.
- Delete the source and the **entire** local state directory, including exports
  and staging. Twice, recover into an empty SQLite cache and restore both immutable
  generations and current content byte-for-byte. Independently reconstruct each
  version from physical pieces using the existing testenv decoder. Run the physical
  `testenv.Audit` after each publication/control pass and after each recovery.
  Recovery/restore must PUT no blobs.
- The earlier protocol primitive still forces three multipart parts for its 11 MiB
  source. Before normal engine scenarios, the test resets `store.SetMultipart` to
  the production **64 MiB** single-PUT threshold and **16 MiB** part size. Database
  chunks therefore exercise the ordinary single-PUT path, not the forced threshold.

The fixture includes SQLite-like header bytes but is **not a valid SQL database**.
It proves fixed-chunk publication, reuse, retained history and remote-only byte
reconstruction from a stable private export; it is not SQL integrity or SQLite
backup-API evidence. Native CLI acceptance covers actual SQLite/SQL behaviour.
No private database is imported, and no new dependencies or CLI options are used.

## Commands and evidence boundaries

Offline scenario (explicitly disable any inherited live opt-in):

```sh
AGENT_SESSIONS_TEST_R2=0 go test -race -count=1 -v -timeout=3m \
  ./internal/store -run '^TestR2AcceptanceFake$'
```

Focused confinement checks can be selected with
`-run '^(TestNamespace.*|TestR2AcceptanceFake|TestR2TestMultipart.*)$'`.
Normal lint and gopls diagnostics apply to the changed test files as well.

Only the authorised parent/operator should run the exact live acceptance command
in [r2-acceptance.md](r2-acceptance.md), **after the final gate**. A trusted operator
launcher supplies credentials only in the test child's environment; the test must not
open/source config or `.env` or dump its environment. Never select legacy live tests
or run a full suite with live opt-in enabled. Existing
owned-prefix and multipart cleanup requirements are unchanged.

`db_chunks` logs identify `backend=gofakes3` or `backend=r2`, phase, numeric publish
seconds, source length, piece count and newly uploaded encoded bytes. The cold
phase measures discovery/export, remote recovery and publication together; it is
not a microbenchmark and has **no timing pass/fail budget**. The fake backend is
not R2 latency/throughput evidence. Logs and failures must not reveal fixture
bodies, paths, credentials or SDK errors. Review live proof and successful cleanup
separately; timings alone cannot establish a pass.

## Executed result (2026-10-15)

The exact race-enabled live test passed on candidate Go source `a95976a` in **218.61 s**.
The scenario's asserted unique PUT counts and observed bytes/times were:

| Phase | New blob PUTs | New encoded bytes | Publish seconds |
| --- | ---: | ---: | ---: |
| Cold | 21 | 22,020,957 | 6.936 |
| Identical export | 0 | 0 | 1.111 |
| Header-like change | 1 | 1,048,617 | 2.574 |

Both historical generations and current bytes restored exactly after two complete cache
losses with the source absent. Overall acceptance cleanup deleted **72 owned objects**,
then verified **zero remaining objects and zero multipart uploads**. The earlier protocol
multipart checks also passed. These are synthetic, bounded R2 observations, not private
SQLite measurements or native-hook-to-R2 throughput benchmarks.
