# Quality-gate runtime investigation

**2026-10-23, uncommitted on `9078a3e`.** Synthetic offline fixtures only. This
investigation is maintenance of the existing quality gates, not a return to the
retired recovery implementation plan or evidence of replacement rollout readiness.

## Failure and diagnosis

The earlier `make test` and `make check` runs exhausted Go's unchanged ten-minute
**package** timeout in `internal/syncer`, at different late tests. Simulation children
inherit the parent's remaining deadline; a child started late does not get another
ten minutes. The final test named by a timeout was not established as a hang.

Medium-effort independent subagents investigated runtime code, fixture/oracle costs,
and gate/subprocess configuration without running concurrent profiling workloads.
The parent ran serial profiles in the pinned Nix environment with both live opt-ins
unset. Neither timeouts nor race/coverage instrumentation were disabled.

Two independent reproductions completed under the original timeout:

| Race-instrumented diagnostic | Before test elapsed | After metadata/list fix | Before CPU profile duration |
| --- | ---: | ---: | ---: |
| `TestAVersionAfterALostWholeUploadKeepsOnlyTheRightBytes/foldLanded` | 19.46 s | 16.08 s | 19.64 s |
| `TestSimulatedSessions/0`, direct isolated worker, 40 steps | 24.80 s | 25.23 s | 24.91 s |

This first round improved the fold diagnostic about 17%; allocation sampling fell
from about 882 MiB to 727 MiB and SQLite BLOB copying from 144 MiB to 75 MiB. The
direct simulation did **not** improve in this single paired run. The first full rerun
passed `make test` (syncer **573.329s**) but still failed `make check` at **601.037s**,
seed `1792744831070852000`, during `TestEverythingTheCollectorWritesIsPrivate`.
That was insufficient margin, not completion.

Further serial diagnostics with both `-covermode=atomic` and `-coverpkg=./...`
identified remaining page-by-page metadata and per-root tip-query amplification.
The second optimization round reduced CPU-profile duration from **16.48s to 13.42s**
for the fold and **28.06s to 23.48s** for direct simulation 0; sampled fold allocations
were about **605 MiB**. These profiles preceded the final SQL memory-bound correction
described below. They are diagnostic observations, not statistical benchmarks;
final unchanged gate runs, rather than profile timings, establish acceptance.

The fold profile attributed 54.1% cumulative sampled CPU to `refreshStream`, 33.3%
to record enumeration, and 56.4% flat CPU to system calls. Allocation sampling
reported about 882 MiB total, including 144 MiB copying SQLite BLOB columns. The
simulation profile attributed 43.5% of sampled CPU to modernc SQLite WAL lock
`fcntl` calls and 28.8% cumulative CPU to `State.CurrentArchive`.

This identifies excessive repeated metadata operations, not stalled agent/network
I/O, in these reproductions:

- Current-chain lookup issued three independent autocommit SELECTs for aggregate
  validation, parent validation, and tip selection. Each acquires/releases WAL read
  locks, even though all three facts belong to one coherent database snapshot.
- Warm listing fetched entire historical descriptor BLOBs only to compare their
  byte lengths with LIST metadata.
- Every enumerated object committed its own scan-marker write transaction.
- Historical replay issued a separate applied-status query for each commit in
  addition to its descriptor cursor. Transition validation itself must remain.
- Local LIST walked unrelated bucket subtrees for narrow prefixes and sorted the
  retained result set after every matching object.
- Even after replacing BLOB reads with sizes, warm listing still issued a separate
  autocommit read per object. A bounded LIST-page query removes those WAL lock cycles.
- Tombstone discovery repeatedly selected and decoded every file stream separately
  for every agent/root, only then filtering for that root. Fresh bounded pages can
  validate the same chains with fewer SQLite statement/lock cycles.
- Derived-file descriptor decoding parsed the whole body a second time solely to
  distinguish an absent dependency field from an explicit list/null.

The old repeated-history algorithm also has inherent growth costs. This work must
not turn a warm checkpoint into publication authority, skip transition validation,
replace real append-boundary fixtures with handwritten records, or add legacy
compatibility/recovery requirements. Oracle caching was identified as a possible
secondary cost but is not a measured primary cause in these profiles.

A separate deterministic blocker was found behind the timeout: the new
`publication.ValidateManifest` had cyclomatic complexity 33. Its minimum possible
CRAP score was therefore 33, above the existing threshold 30 regardless of coverage.
Manifest-envelope and individual source/object validation are separate concerns and
can be expressed/tested separately without dropping any validation.

## Reproduction

Use private temporary diagnostics outside the checkout. `-o` prevents profile-enabled
`go test` from leaving a test executable in the worktree; do not print binary profile
contents. Profile summaries and per-test elapsed JSON facts are sufficient evidence.

```sh
nix develop --command env -u AGENT_SESSIONS_TEST_R2 -u AGENT_SESSIONS_LIVE \
  CGO_ENABLED=1 go test -race -shuffle=on ./internal/syncer \
  -run '^TestAVersionAfterALostWholeUploadKeepsOnlyTheRightBytes$/^foldLanded$' \
  -count=1 -json -cpuprofile="$scratch/fold.cpu" \
  -memprofile="$scratch/fold.mem" -o "$scratch/syncer.test"

nix develop --command env -u AGENT_SESSIONS_TEST_R2 -u AGENT_SESSIONS_LIVE \
  CGO_ENABLED=1 go test -race -shuffle=on ./internal/syncer \
  -run '^TestSimulatedSessions$/^0$' -count=1 -json \
  -cpuprofile="$scratch/sim0.cpu" -memprofile="$scratch/sim0.mem" \
  -o "$scratch/sim0.test" -args -simulation-worker -simulations=1
```

The direct worker profile is important: profiling only the simulation orchestrator
misses its children's CPU and allocation work. All eight normal histories, their
40 steps, real publication-boundary fixtures, corruption assertions, and child
coverage-counter assertions remain required by the full gates.

## Fix and acceptance checkpoint

The metadata fix retains complete chain/transition/cold dependency validation. It
uses one snapshot for current selection, one metadata-only read per bounded LIST
page (at most 1,000 keys), batches at most 1,000 scan-marker writes in database-only
transactions, and selects applied markers in the existing historical cursor. No
history validation was bypassed. Dynamic placeholder counts do not enter the
prepared-statement cache; keys remain bound SQL parameters.
Local LIST retains at most `limit+1` candidates in a max heap, sorts once, and prunes
only directory key ranges that cannot match the literal prefix/token. It still
starts at the root to preserve directory-symlink no-follow behavior.

Tombstone selection now uses fresh pages of at most 64 validated tips, not cached
current heads. All streams, including irrelevant agents/roots, remain subject to the
same branch/gap/parent checks. A nonunique covering index preserves representable
corruption and avoids reading historical bodies for chain metadata. Pages close their
cursor before source checks or remote deletion I/O. Source/privacy/deletion fences
and checkpoint writer checks remain unchanged.

Independent review caught a memory-bound defect in the initial page query: sorting
64 tip bodies inside SQLite could bypass the Go accumulator's 16 MiB limit. The final
query orders metadata and computes its byte-bounded prefix using lengths **before**
fetching bodies. A direct-SQL regression verifies this limit before Go accumulation;
only the first body may exceed the budget to ensure progress. Query-plan coverage,
reference-chain parity, nil/empty boundaries, byte/page limits, freshness, irrelevant
corruption, and cursor-closed publication tests pass.

Common derived-file descriptor decoding now parses the body once. An unchanged
fallback retains unusual existing duplicate/case/null/error semantics; differential
reference tests cover them. No additional retired schema is accepted.

Manifest/source/object validation was separated into coherent checks with all
original rejection conditions preserved. New tests also cover previously untested
CLI capture/delivery/status flows and private-store failure boundaries, rather than
changing the CRAP threshold or hiding functions.

### Final unchanged development gates (2026-10-23)

All commands used the pinned Nix environment with both live opt-ins unset, serially:

| Gate | Result | Exact evidence |
| --- | --- | --- |
| `make test` | **PASS**, 409 s wall clock | Full race/shuffle suite; syncer **384.263s**; all 3 actual writer/reader/import/report journeys pass. |
| `make check COVER_DIR=<isolated-temp>/coverage` | **PASS**, 621 s wall clock | Full atomic race/coverage suite; syncer **465.828s**, below the unchanged 600-second package limit. |
| Merged coverage | **PASS** | **89.0%**, 7,184 / 8,071 statements; unchanged package 60% and total 75% thresholds. |
| Full CRAP scan | **PASS** | No function above unchanged **30**; highest **29.36**. |
| Format/lint/fix/tidy/deadcode/vulnerability/actionlint | **PASS** | Zero lint issues; no known vulnerabilities. |
| All benchmark smoke cases | **PASS** | Unchanged one-iteration smoke; syncer benchmark package **105.147s**, not a resource-budget benchmark. |

The 621-second outer `make check` includes static checks and all benchmarks; Go's
600-second timeout is **per test package**, not a limit on the whole make command.
All eight simulation histories and 40 steps, real 64-piece append boundaries,
corruption assertions, subprocess race/coverage accounting, and existing gate settings
were retained. Existing `cover.out` was restored byte-for-byte; the new merged profile
was retained only in temporary diagnostics.

This unblocks development, not production release. Native/hooks, actual R2, resource
budgets, two-machine inventory/acceptance, deployment and cutover remain unverified.
No production state, original sources, hooks, deployment, or R2 data was changed by
this investigation. At that dated checkpoint the handoff identified phase 3 as the next implementation step. The [2026-10-24 offline evidence](session-capture-offline-evidence.md) now supersedes this gate checkpoint: the opt-in phase-3/enrichment/backend/dashboard candidate passes fresh full development gates and eleven journeys. The diagnostics and dated results above are unchanged; release acceptance and default activation have not occurred.
