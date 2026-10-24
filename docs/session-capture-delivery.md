# Corrected capture delivery ledger

Status: controlled operating delivery in progress, not release acceptance.
Reviewed implementation checkpoint: **`c9749da`** (after `b68a412`). Requirements remain the [capture contract](session-capture-contract.md).
This ledger records new work after the checkpoint; prior passing gates remain dated
evidence, not automatic proof for later changes.

## Delivery ownership

The operator requires autonomous coordination through delivery. A checkpoint, failed
gate or subagent result is not the endpoint. The coordinator diagnoses, fixes, reviews,
validates and continues the controlled sequence. Preserve privacy/access boundaries;
record actual blockers and attempted checks rather than inventing human prerequisites.
No legacy archive/analytics migration or refresh-compatibility work is required.
Capacity/retention changes belong in a measured, validated vertical slice.

## Current operating state — 2026-10-24

- Final serial gates passed and scoped checkpoint review found no confirmed blocker;
  committed `c9749da` locally (not pushed). Fresh static Darwin/arm64 and Linux/amd64
  binaries were built from that exact revision inside pinned Nix.
- Refreshed both private inventories and R2 policy: original distinct machine identities,
  three available default roots each, zero learned roots/local exclusions/remote policy
  records at this observation. Originals, credentials and prior evidence are preserved.
- Candidate binary, credential-scoped launcher and private runtime config are now
  installed on **both machines**. The previously absent real shared capture binding was
  initialized on Mac and verified with BC's own existing credentials. This is actual
  production binding use, not the synthetic acceptance binding/remapping.
- Owned launchd/systemd supervisors are **started on both machines**. Independent source
  capture/backfill/publication is active; read-only `work-status` succeeds beside each
  live owner. Initial bounded spool budgets: Mac 8 GiB, BC 6 GiB; free-space reserve
  4 GiB each, chosen against measured source metadata/free space, not a retention proof.
- Mac's existing owned `LaunchAgents` directory was 0755; the unchanged private-file
  gate refused service installation. Its prior mode was recorded privately and the
  directory was deliberately tightened to 0700; installation then succeeded. No unit or
  unrelated file was deleted, and no gate was relaxed. BC's owned service directory was
  also prepared privately. Existing authenticated Azure management—not refused SSH or
  copied keys—delivered a byte-verified **public-code-only** artifact to UID 1000.
- Backfill is not yet classified complete. Broad inventory explicitly reports protected,
  denied, executable, orphan-owner and unsupported resource gaps; these are not hidden
  or claimed captured. No source quarantine was present in the first Mac observation.
- Both owned PATH entries are now fenced: legacy `sync` exits 2, old managed hooks
  were safely uninstalled and new explicit candidate callbacks installed. Mac's one
  identified old Go writer was stopped; BC had none. Four old managed config/extension
  files and the old binary on each host were privately backed up before replacement.
  Cached legacy native callbacks retain the bounded privacy-aware bridge. No original
  source or remote key was deleted.
- Autonomous Mac refresher and publication-only loopback dashboard are supervised on
  staging port **8766**; old dashboard **8765** remains unchanged. Actual writer -> R2 ->
  authoritative reader -> owned importer/cache/index -> served report is connected:
  an early sample had 225 observed targets / 486 selected sources. Health correctly
  remained false during initial catch-up; this is not classified completeness.
- Analytics software is copied from exact checkpoint sources with an owned, byte-checked
  portable Python **3.13.15** runtime and DuckDB **1.5.6** / Boto3 **1.43.114**. The
  validated venv was not Nix Python 3.14; its portable base and dependencies were copied
  and loader dependencies checked rather than changing ABI or relying on uv/repo cache.
  All **95 tests PASS, 32.324s** from the actual installed runtime/source copy.
- **Measured blocker:** retained source and discovery-issue tables each reached their
  4096-record admission bound. A bounded optional `max_inventory_records` correction
  preserves default 4096 and all input/detail/coverage gates; focused race suites/lint
  and independent review pass. It is **not deployed**; fresh final serial gates are
  required before raising the operating setting (proposed 32768, ceiling 65536).
- **Operating diagnosis:** two, later three, small opaque artifact targets intermittently
  recorded `import_failed`. Exact same published exports import unchanged successfully
  in fresh/combined/copied projections and through isolated actual `_target`; no source
  normalizer bug is established. A temporary bounded, privacy-safe actual refresher
  probe captured no exception; both exact original failing coordinates subsequently
  imported, with **0 target errors / 0 head failures** at 2,579 targets. The original
  unit was byte-restored/running. Served report observed 2,578 sessions / 4,325 sources /
  0 projection failures, HTTP 200; one intervening HTTP 503 remains explicitly observed.
  Installed-runtime analytics tests again **95 PASS, 39.079s, zero skips**. No Python
  change, failure suppression or data deletion in that first diagnostic window. It
  proved retry recovery, not the original exception's root cause or completeness.
  A later bounded actual probe **captured four `FileNotFoundError` exceptions** in
  the export monitor's `disk_bytes` entry inspection, before normalization/import:
  an enumerated temporary metadata entry was atomically renamed before `lstat`.
  A deterministic separate-process rename regression was RED on unchanged code.
  The narrow correction uses one no-follow inspection and tolerates only that vanished
  entry; permission/other errors, entry/byte limits, symlink rejection and strict
  completed-export/import validation remain. Focused **19 PASS / 4.716s**, independent
  scoped review PASS, then full analytics **97 PASS / 33.073s** in pinned Nix with the
  owned runtime/current test binary. Fix is **not installed**; later live failures
  remain visible. Final frozen serial gates must include both changed Python files.
- Read-only, bounded **old `v3/` metadata inventory** completed after fencing:
  81,896 objects / 2,823,793,063 bytes / 82 scoped LIST pages; no body GET, new-prefix
  access or deletion. Private manifest SHA256
  `ca3a80c40fcd3c5f9bdff382f99a9280863a9e9f426dd5239f2a7974bed326f4`.
  It is retirement input, not evidence that every old byte exists locally or authority
  to delete before operating acceptance. The first list attempt rejected URL-encoded
  key spelling before recording anything; the corrected metadata parser explicitly
  decodes declared URL encoding and completed the scoped inventory.
- Mac pre-upgrade spool point: 4,023 files, 889,053,684 apparent bytes / 909,807,616
  allocated bytes, no spool symlinks, 64,911,896,576 filesystem bytes free. Both retained
  inventory tables are exactly 4,096 rows. This isolates the measured record quota from
  byte-space exhaustion; it is not completed capacity/retention acceptance.
- BC installed-Pi plan now passes after separately recording/tightening only the owned
  canonical session-parent directory from 0775 to 0700. Source contents were unchanged,
  assertions were not relaxed. Reviewed plan: 43 RO binds, 32 runtime software entries,
  48 exact aliases, four existing active metadata-inbox binds, four denied original/
  credential/unrelated/repo coordinates, no shadow spool. No native/model execution yet.
- **Not yet done:** installed native-hook acceptance, classified two-host final catch-up,
  restart/freshness/exact restore acceptance, final dashboard switch and old-key
  retirement. Original-source and old remote archive deletion have **not occurred**.

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
  tree. The later operating state above begins controlled deployment; offline green
  alone is not delivery.

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

1. **Done:** independent closure review, fresh unchanged serial gates and checkpoint.
2. **Done, scoped:** isolated real R2 and scratch-native agents. These are not installed
   operating acceptance.
3. **Started:** refreshed effective policy/identity/root inventory, installed bounded
   production queues, real binding and both supervisors with named credential handoff.
4. Rebuild/reconcile available sources and catch up. Legacy PATH writers and managed
   hooks are already fenced/replaced on both hosts, with originals retained. The next
   correction source passed unchanged full check/test/fuzz, 97 analytics and JavaScript
   gates; exact-revision rebuild and controlled 32768-record/monitor upgrades remain pending.
   Preserve originals and local research evidence.
5. Verify both installed paths, restart/retry, exact restores and publication-backed
   analytics freshness. Retire only reviewed old remote keys after acceptance.

Original sources and retired remote keys remain untouched. A new capture binding and
both active candidate supervisors are now real; complete operating acceptance, final
writer/dashboard cutover and remote retirement are still outstanding.
