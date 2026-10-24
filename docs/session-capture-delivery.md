# Corrected capture delivery ledger

Status: delivery in progress, not release acceptance. Implementation checkpoint:
`b68a412`. Requirements remain the [capture contract](session-capture-contract.md).
This ledger records new work after the checkpoint; prior passing gates remain dated
evidence, not automatic proof for later changes.

## Delivery ownership

The operator requires autonomous coordination through delivery. A checkpoint, failed
gate or subagent result is not the endpoint. The coordinator diagnoses, fixes, reviews,
validates and continues the controlled sequence. Preserve privacy/access boundaries;
record actual blockers and attempted checks rather than inventing human prerequisites.
No legacy archive/analytics migration or refresh-compatibility work is required.
Capacity/retention changes belong in a measured, validated vertical slice.

## Current work

- Fresh checkpoint acceptance failed one of eleven journeys. Investigation reproduced
  the unchanged native-enrichment journey failing 7/20 times: the test observed the
  native launch/snapshot pair before the independent transcript-basis snapshot finished.
- The candidate test correction waits for all three durable enrichment jobs and their
  readable publication, with exact four-source/body closure and source-basis checks.
  The original final manifest equality and timeout remain. Independent medium-effort
  static review found no blocker within scope. The corrected journey passed 100/100
  repeats (148.564s), then all eleven process journeys passed (13.189s).
- Actual Claude 2.1.308, Codex 0.163.0 and Pi 1.4.0 passed connected scratch-native
  capture/Local/reader/import/event-index/report acceptance. Actual disabled fresh
  sessions and Codex wrong-trust-hash rejection were also exercised. The analytics
  subprocess is now confined to the same loopback-only sandbox; denial probes and
  independent closure review passed. The latest all-three run passed in 32.22s.
  Fresh-session Pi loading/removal is not an in-process `/reload` claim.
- Isolated actual R2 acceptance passed (4.60s, 54/160 backend operations): real
  conditional create/conflict/update, injected lost acknowledgement and failed-A/B
  isolation, exact authoritative export and actual importer/event index/report.
  The unique synthetic namespace is retained; the bucket-global binding and old
  archive were not accessed by this test. An initial attempt failed analytics
  preflight before remote requests because resolving a virtualenv executable symlink
  lost its packages; the launcher now preserves that path and has a regression test.
- Pre-live review found unbounded SDK error-body buffering. The factory now caps
  error responses before SDK decoding, closes bodies, sanitizes failures and retains
  auth/conflict/not-found status classification. Independent re-review closed both
  byte-bound and auth-classification findings; focused race/lint checks pass.
- Delivery review confirmed a missing autonomous consumer path beyond per-session
  acceptance. Read-only `publication-status` now enumerates locally acknowledged heads
  without opening a writable queue or taking its owner lock. The endpoint refresher
  uses authoritative Go exports, checks owned body hashes before unchanged skips,
  repairs replaceable projections, and isolates import failures. No second Python
  storage resolver is introduced.
- A real separate-process DuckDB writer/reader conflict was reproduced and corrected
  with bounded cooperative SH/EX projection locks through connection closure; network
  and export work stays outside those locks. Actual concurrent HTTP/refresh journeys
  and two cross-process lock tests pass. Publication-mode health is separate from
  legacy serving.
- Independent review caught a false-green expected-endpoint window. Full endpoint
  configuration is now bound atomically before enumeration; add/remove/repoint
  invalidates active completion. Missing, omitted, unattempted or stale endpoints
  cannot provide healthy coverage. Status schema v2 refuses incomplete v1 without
  automatic migration/reset. This finding was independently closed; later per-head
  failure work below requires another explicit status schema revision.
- Operating checks exposed an unsuitable autonomous dependency: BC SSH uses an
  approval-gated agent, whose signatures later became unavailable. New `current-heads`
  discovery therefore lists only canonical current-root namespaces through the Go R2
  reader, with bounded pages, at most eight CURRENT reads, canonical scope-bound
  cursors and a 2 MiB successful LIST-response bound. It needs neither source/spool
  access nor SSH. Endpoint `remote-current` selects this path; configuration changes
  invalidate previous health. Queue discovery remains available explicitly.
- Initial current-root review passed scope/bounds/binding checks, but follow-up found
  a genuine per-session liveness defect: unreadable A discarded healthy B from the
  same page and prevented cursor advancement. Corrected and independently closed:
  bounded identity-attributable failures now accompany healthy heads, advance the
  cursor, and remain durable/unhealthy in consumer status v3 while B imports. Each
  CURRENT has a five-second child budget; scope/auth/caller cancellation remains fatal.
  Status versions 1/2 are refused without migration/reset. Actual SDK and real importer
  tests assert A/B progress, prior-good A retention, restart, retry and recovery.
- Final unchanged development gates **all passed serially** on the settled, hashed
  tree. Operating deployment/cutover has not started; offline green is not delivery.

### Fresh validation ledger

- The first full `make check` attempt failed the existing
  `TestScripts/sweep_on_session_end`: its object/marker barrier raced the later durable
  run record. A test-only, read-only `waitrun sweep:session_shutdown` now waits within
  the existing 20-second bound before the unchanged doctor assertions. Runtime and
  legacy behavior were not modified. Helper plus script race/shuffle tests passed
  20 repetitions in 73.586s; focused lint reports zero issues.
- After the endpoint-health correction, full pinned-Nix `make check` passed in
  **612s**. The subsequent `make test` was deliberately interrupted before new remote
  current-root implementation; it is not a full-suite pass. An earlier gate attempt
  was also interrupted for the confirmed health correction. Original `cover.out` was
  restored after every interruption (SHA256
  `70386d5160553c62f5b9cef9c38af0f7273336aebd491000ab9259fd94f117b9`).
- Pinned `nix build .# --no-link` exposed the stale Go vendor hash and then passed
  after recording the actual locked dependency hash. The development shell now
  explicitly supplies `zstd`, an existing analytics-fixture CLI dependency, rather
  than relying on the host PATH. This build predates the current-root slice and must
  be repeated on the final tree.
- After the A/B correction, four-package pinned-Nix race/shuffle tests passed:
  publication 15.084s, publicationr2 2.766s, capturebackend 1.814s, CLI 34.319s;
  focused lint reports zero issues. Consumer tests passed 16 refresher + 11 dashboard
  cases, including real importer/body/index/status assertions. Independent closure
  review found no remaining scoped blocker.
- Fresh unchanged `make capture-acceptance` passed: 12 actual-process journeys in
  14.509s, six R2 launcher tests and six supervisor launcher tests. The Nix package
  build was repeated successfully after the current-root changes.
- Deliberate bounded real-R2 acceptance with current-root discovery passed in 5.48s
  (5.891s package), **70/160 backend operations**, including actual delimiter/StartAfter
  limit-one paging and exact declared reserved-reference failures beside healthy A/B.
  New retained synthetic archive:
  `candidate-live-acceptance-04d7f15e21d0069dfa48b5cde673755d`. This still remaps only
  the test binding and is not production/global-binding acceptance.
- Final serial gates **ALL PASS**: `make check` 803s, `make test` 451s,
  nine unchanged fuzz targets at `FUZZTIME=30s` in 283s, all **95 analytics tests**
  in 29.551s, JavaScript syntax and whitespace checks. Merged coverage is **86.9%**;
  package/total thresholds pass; zero reported functions exceed CRAP 30. All 412
  source/build/test inputs matched `gate-inputs-validated-reader.json` after completion.
  Original `cover.out` hash was restored exactly. Logs and progress are under
  `/tmp/memory-delivery.SW23JL/gates-validated-reader/`.
- Launcher credential handoff tests passed 6/6 after adding `current-heads`. Native
  hooks and local metadata commands still neither open the credential file nor inherit
  the two named R2 values. Full/native/live outcomes remain separately scoped above.
- Private logs: `/tmp/memory-delivery.SW23JL/gates/check.log` (first failure),
  `gates-complete/check.log` (612s pass), `/tmp/memory-current-heads-{tests,lint}.log`,
  and `/tmp/memory-waitrun-final.log`. The final all-green evidence is the later
  `gates-validated-reader/` run above, not the earlier interrupted runs.

## Two-machine metadata inventory — 2026-10-24 AEDT

Deliberate read-only checks, not ingestion or cutover:

- Both hosts were reached and inventoried using existing trusted SSH authentication.
  That SSH result was point-in-time: subsequent signatures were refused by the
  approval-gated agent. No approval bypass, key extraction/copy or SSH configuration
  change was attempted. An already-authenticated Azure management channel was matched
  through exact public DNS → NIC → VM binding and the original private machine-identity
  hash; read-only commands run as the original `dev` UID 1000. It provides a legitimate
  operating channel, not background analytics authentication.
- Both installed collectors report `0.5.0`; Claude, Codex and Pi executables exist on
  both machines. This does not establish native candidate integration.
- Both existing machine identities were privately inventoried and are nonempty and
  distinct. Credential configuration exists privately on both hosts; values were not
  printed or committed.
- Both legacy state databases have zero learned-root rows. All three default roots
  exist on each host. No local exclusion files were found; bounded read-only LIST/GET
  reconciliation of each machine's remote exclusion prefix also found zero records.
  This is a policy inventory result, not permission to skip future exclusion checks.
- Private inventories are under `/tmp/memory-delivery.SW23JL/`, with a remote metadata
  inventory under `/tmp/memory-delivery-candidate/`. They are not repository artifacts.

A separate metadata-only walk counted transcript-suffix candidates, without reading
conversation bodies. These are **sizing candidates, not classified capture coverage**:

| Host | Agent | Candidate files | Source bytes | Largest candidate bytes |
| --- | --- | ---: | ---: | ---: |
| Mac | Claude | 807 | 1,756,002,972 | 37,701,484 |
| Mac | Codex | 25 | 18,261,800 | 3,494,028 |
| Mac | Pi | 2,377 | 1,308,917,480 | 102,957,091 |
| BC-01 | Claude | 366 | 501,122,189 | 13,607,304 |
| BC-01 | Codex | 138 | 460,032,250 | 45,472,706 |
| BC-01 | Pi | 1,438 | 837,714,909 | 37,469,591 |

The walks reported no filesystem errors and did not hit their 200,000-entry bound.
They skipped symlinks rather than inventing ownership (Mac 11; BC-01 12); those skips
are not silently claimed captured. Available filesystem space was approximately
66.7 GiB on Mac and 13.6 GiB on BC-01 at inspection. These are point-in-time observations,
not retained-capacity guarantees or combined RSS/disk measurements.

## Remaining controlled sequence

1. Complete independent fix review and fresh unchanged development gates.
2. Prove real R2 behavior in a bounded new synthetic namespace and real native agents
   in scratch-only sandboxed homes; distinguish these from installed operation.
3. Preserve and validate effective policy/identity/root bindings, configure bounded
   production queues and explicit supervisor secret provisioning without printing keys.
4. Rebuild/reconcile available sources and catch up; fence legacy workers and hooks
   before final writer cutover. Preserve originals and local research evidence.
5. Verify both installed paths, restart/retry, exact restores and publication-backed
   analytics freshness. Retire only reviewed old remote keys after acceptance.

No deployment, archive reset, original-source deletion or retired-key deletion is
claimed by the inventory and implementation work above.
