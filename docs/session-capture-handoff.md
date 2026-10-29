# Session capture: current delivery handoff

This is the sole current deployment/qualification/next-action brief. Requirements live
in the [capture contract](session-capture-contract.md); the accepted cleanup direction
and evidence live in the [holistic review](holistic-review-20261029.md).
The previous checkpoint and detailed October 28 ledger are preserved verbatim in the
[October 29 archive](session-capture-handoff-archive-20261029.md). Earlier histories:
[October 27 archive](session-capture-handoff-archive-20261027.md) and
[progress history](session-capture-progress-history.md). Historical instructions are not
current rollout authority.

## Current session checkpoint

**2026-10-29: latest installed/qualified source is `b8cbed2` on both hosts.** The original
corrective/simplification source `e8c96f8` and discovery follow-ups `36dcef4`/`b8cbed2` pass
unchanged full local gates; both follow-ups also pass the actual Mac native gate.
The operator approved completing
all six phases, with parent-owned quality, independent review and final verification.
Representative operating budgets, installed acceptance and cutover remain incomplete.
**Latest operator direction:** reset the **existing configured R2 bucket**, replacing all
previous remote contents; no new bucket and no retention of the old remote archive is
required. This supersedes the separate-bucket approval and provisioning request below.
Parent owns cloud/host actions. Local identities, privacy, original sources and research
remain protected. Inventory, live validation and old-writer fencing precede deletion.
The existing bucket's global binding was conditionally switched and readback-verified
at **2026-10-29 00:38:59 UTC**. Exact gated `e8c96f8` binaries and fresh configurations
were installed and capturing on both hosts; both have since been upgraded to qualified
`b8cbed2`. The temporary 1Password SSH signing block was resolved before BC's upgrade.
Mac refresh/dashboard serve the fresh archive. Selected actual two-host
source-to-report journeys and the native Mac gate pass.
**Backfill/coverage and operating acceptance remain incomplete; the replacement is not
release-accepted or production-ready.** No bulk old-object deletion or legacy code removal
has happened. Repository defaults remain legacy; these hosts are explicitly opted in.
The abandoned new-bucket creation403 is historical, not a current access blocker.
**Latest operating correction is installed:** failed discovery metadata persistence now
records a sticky failure under a bounded release budget or stops rather than falsely
completing a generation. Older completion counters are not accepted coverage proof.
Finite read-only24h measurements started on both hosts at03:27 UTC; they do not grant
acceptance or perform cleanup.

### Checkout and preservation

- Integration checkout `/Users/mm/dev/memory`, branch
  `delivery/fresh-archive-qualification`; local `gh stack`: `main` →
  `delivery/holistic-baseline` → `delivery/correctness` →
  `delivery/queue-and-resource-simplification` → current branch. Local `main`
  already contains 88 commits beyond `origin/main`; this session has not pushed them.
- Starting implementation `4c9aca5c5cb178316e9eed32626dbf56f7dbaac9`; adopted the review
  report and handoff edits as `06c6da4`. No preexisting implementation changes in this checkout.
- Preserve frozen untracked `docs/research/README.md`, `agent-navigation-context-graphs/`
  and `agent-navigation-handover.md`; `CLAUDE.md` stays absent. Preserve stashes and
  unrelated `asset-checks`, `log-failed-sync`, `pruned-pieces` worktrees.
- External repair `/Users/mm/dev/memory-wt/capture-source-absence-20261028` has released
  source ownership per the machines handover. Its tracked diff and untracked
  `source_absence_test.go` were copied into a new task worktree. The original remains
  unchanged, uncommitted and not release-qualified.
- Prior dirty candidate/research evidence is preserved on
  `preserve/pre-simplification-20261027` and in the private historical trees identified
  by the archived handoff. Do not resume the retired recovery patch stack.

### Installed state and authority

| Area | Evidence / current limit |
| --- | --- |
| Mac capture | Qualified `b8cbed2` installed/restarted03:07:56 UTC without config/queue reset. At03:27 PID28171/exit0;964 pre-upgrade pending jobs subsequently published, no prior jobs/manifests lost or changed. SQLite/binary backups retained privately. |
| Mac consumers | Refresh and dashboard enabled/running against qualified analytics source and fresh release-private database/status. Dashboard loopback8766 returns HTTP200 with correct fresh archive and explicit incomplete health. Existing8765 dashboard unchanged. |
| BC-01 | SSH alias `bc-01`; signing access restored. Qualified `b8cbed2` installed/restarted03:08:09 UTC, skipping intermediate36dcef4. At03:27 PID3483559, enabled/active/running,0 automatic restarts/exit0;1,541 prior pending jobs subsequently published with no prior job/manifest loss/change. |
| Authority | Parent owns all host/cloud/configuration actions. Existing bucket reuse/reset authorized; no provisioning credential or new bucket needed. Both PATH wrappers fence legacy commands; native managed hooks enqueue candidate hints. |
| Cutover | Global binding switch and controlled activation completed for qualification. Full operating acceptance, old-object retirement and legacy-only code removal remain incomplete. No bulk deletion before accepted replacement. |

The machines handover's newest top section withdraws the custom `bc-heavy` tool; it was
removed deliberately. Do not reinstall it or follow historical admission instructions.
Parent serializes expensive gates directly in pinned Nix, on AC with sleep prevented.

### Fresh-state rebuild boundary (material reconciliation)

The machines handover now records subsequent **operator-authorized generated-data
cleanup, October 28 21:37–21:45 UTC**. Mac/BC experimental spools (including unpublished
backlog), Mac analytics and capture backups were deleted. The former `step4g/step4h`
rollback points and quarantines are historical, NOT available continuation inputs.
Original agent sources, identities/privacy configuration, credentials and installed
software remain; no cloud archive objects were deleted. Older legacy archives/state
outside the experimental root were explicitly not deleted. Native hooks may recreate
small intake stubs; that is not an intact queue or bulk capture.

Therefore phase 5 requires intentional fresh-state inventory/rebuild, not a blind
binary swap/replay. Read-only planning confirmed an empty Queue starts at sequence 1
and cannot reconstruct prior source epochs or unpublished evidence; publishing against
existing R2 heads is not safe continuation. Production `capture-v1/archive.json` is a
global bucket binding: changing archive ID alone is not independent isolation. Existing
configuration supports a separate bucket, which was initially approved but superseded
by the operator's explicit instruction to reuse/reset the existing bucket. Parent and
independent reviewer accepted a one-time administrative CAS replacement of only the global
binding, preserving production create-only safeguards. This replacement is now complete;
never replay it blindly or use old generated-state backups as continuation inputs.
Do not recreate deleted generated data incidentally or treat local source availability
as proof every deleted accepted byte can be recovered. Preserve the containment plist
(failure-only KeepAlive, throttle 300s), build exact gated source, and back up any new
SQLite state via its backup API. Historical performance findings remain useful test
inputs, not current spool statistics. Parent informed the operator of this boundary.

## Scope and acceptance plan

| Phase | Scope | Exit condition | State |
| --- | --- | --- | --- |
| 1 | Reconcile baseline, ownership, criteria and tools | Each finding has owner/check; preservation and authority clear | Baseline reconciled; resource budgets still to qualify |
| 2 | Source absence, analytics scope/disappearance, current-state docs | Defects have before/after regressions; fail-closed boundaries retained | Implemented, independently reviewed; local full gates passed; installed R1 acceptance remains phase 5 |
| 3 | Typed queue state, inventory reuse, evidence correspondence, shared delivery outcomes, malformed work | Fewer interpretations/invalid states; invariant tests unchanged or stronger | Implemented, independently reviewed; local full gates passed |
| 4 | Representative measurements; bounded report/import/scheduling improvements where justified | Stated resource/freshness/retention envelope demonstrated | Synthetic measurements justify three narrow changes; representative operating envelope remains open |
| 5 | Combined full gates, installed Mac/BC-01/native/R2 acceptance and autonomous observation | Actual installed end-to-end path, privacy negatives, restart/retry and sustained operation | Local/native Mac/isolated R2 gates and selected actual two-host journeys passed; installed rebuild running; complete coverage, restart/retry and sustained operating acceptance remain |
| 6 | Controlled cutover, reviewed remote retirement, legacy code removal | One supported architecture; preserved data/policy; post-cutover checks | Dependent on phase 5 |

Non-negotiable: preserve source epochs, durable pending ownership, privacy exclusions,
learned roots/identity, optional-artifact isolation and one authoritative reader.
No gate weakening, generic workflow framework, second reader, speculative GC/chunking,
upload-ACK bitmap or legacy migration prerequisite. A simplification must actually
remove duplicated interpretation/control flow, not merely add helper layers.

## Ownership and completed workers

Parent owns this brief, integration checkout, commits/stack, expensive gates, all host
operations and requirements acceptance. Sol/medium workers use isolated worktrees;
background status, steering and resume are available. They may run scoped offline tests,
not concurrent full suites or host operations.

| Worker | ID | Worktree under `/Users/mm/dev/memory-wt/` | Owned domain |
| --- | --- | --- | --- |
| Source absence | `e3a0ec8e-06a8-41b` | `review-source-absence` | Copied repair + tests; sessioncapture source/fatal boundaries; capturedaemon service backoff |
| Analytics health | `db7d1d43-00b7-45c` | `review-analytics-health` | Refresh/dashboard health and coupled tests |
| Runtime inventory | `5b42a090-9295-42a` | `review-runtime-inventory` | CLI policy/source composition and coordinate config APIs; no sessioncapture/service edits |
| Status docs | `1f82dee1-a781-417` | `review-status-docs` | README and current reference headers; no handoff or historical evidence edits |

Additional owners: queue `9856b5b1-e75f-4ef` / `review-queue-simplification`; analytics
resources `a6a0477e-fccf-4e8` / `review-analytics-resources`. Source/docs/inventory owners
have released implementation ownership. Independent source review `3d4e62d0-1ff2-413`,
queue review `b6dfa436-69d2-414`, and analytics recheck `f5330d9e-b67f-468` are read-only.
No automatic acceptance from completion/commit; workers use their recorded integration
bases rather than assuming one shared baseline.

### Material corrections

- C1 analytics worker briefly edited the integration dashboard through a relative tool
  path. Parent detected it immediately; worker instructed to transfer only its own edit
  to the assigned worktree and reverse that accidental edit. Integration dashboard now
  clean; worker explicitly acknowledged correction and parent verified both worktree
  statuses. All workers reminded to use absolute tool paths and explicit bash cwd.
- C2 analytics upgrade: parent identified Python SQLite DDL transaction gap; worker
  instructed to wrap v3→v4 ALTERs and completion invalidation in an explicit transaction
  with interruption/rollback regression. Implemented in integrated analytics changes.
- C3 inventory: test-only wrappers removed from production in `c5d1319`; parent verified
  shared composition and test helpers. All initial implementation startups confirmed.
- C4 source review: actual CLI exclusion callback erased fatal store classification.
  Queue exported safe `ErrPolicyUnavailable` in `2134744` (integrated `e490b51`);
  production callback fix `a1c980a` → `c80a938` now covers the actual `Run` path and safe
  fatal diagnostic. Independent reviewer approved closure at `ee09321` (read-only recheck).
- C5/C6 analytics: `b2dad68` → `f57de35` binds upgraded expectations and forces new-reader
  qualification on repoint. Parent caught a further retry loophole: updated target binding
  could reuse old imported binding on the second failed attempt. `41d3baa` → `47ae92d`
  keys reuse on `imported_fingerprint`, with repeated-failure/restart regression.
  Independent reviewer `f5330d9e-b67f-468` approved all four analytics fixes at `47ae92d`;
  focused refresh26/dashboard13 passed in pinned Nix.
- C7/C8 queue review: late failures must not revive superseded/downgrade published jobs;
  quarantine only when guarded transition wins. Shared handling must preserve immediate
  transient retry behavior rather than applying autonomous MaxAttempts. Implemented below.
- C7–C9 correction `eaaf3ce` → `d74c6c1`: terminal guarded failure/quarantine, explicit
  transient semantics, ACK/supersede regressions, and private EpochID association.
  Historical unbound rows are readable after identity/commitment checks, not retroactively
  proven; new writes require epoch binding. Reviewer `271384d0-a2ef-429` approved scoped
  code/tests at `d74c6c1` without running gates.
- C10 batch review: Python reparsing JSON tool arguments changed their representation.
  `b9b07d1` → `08fb3af` stages JSON as VARCHAR then casts in SQL; duplicate keys/numeric
  lexemes retained, regression added. Reviewer `2af17bba-8f94-49a` approved scoped
  code/tests at `d74c6c1` without running gates.
- Tools aborted two workers mid-run; resumed same IDs/worktrees after status reconciliation,
  no duplicate implementation owners. Queue worker also resumed after tool abort.
- Review corrections already accepted: no unconditional poison-row isolation without
  trustworthy coordinates; verified attachment retry is not a new upload attempt;
  no blanket role immutability rule; no per-GET cross-database qualification or second
  fixture executable. Same-name endpoint rebinding needs more than active-name filtering.

## Verification and progress ledger

### Baseline evidence only

- Historical source `bf99752` (implementation identical to starting `4c9aca5`): unchanged
  `make check`, `make test`, `make fuzz` passed; exact dated evidence in archive.
- Holistic review on `4c9aca5`: pinned-Nix capture-acceptance 12+6+6 passed; analytics
  97 discovered/12 skipped (journeys separately run); ten candidate Go packages passed
  `-race -shuffle=on -count=1`. Two synthetic refresh-health defects reproduced.
  Commands/logs in review §7. These results do not qualify implementation changes.
- Fresh readiness checks: AC power, no existing Go gate processes at initial inspection;
  `gh stack` available. Open GitHub PRs 23/26/27/28 are unrelated and were not modified.

### Current implementation

- Source repair worker `3029551` → integration `1d7bad9`: parent reviewed narrow source
  marking/service guards. Worker full race/shuffle sessioncapture104.310s/capturedaemon1.529s,
  lint0; two mutations killed. Independent reviewer `3d4e62d0-1ff2-413` found C4, so final
  C4 closed by real callback regression and independent recheck; combined results below.
- Docs worker `be84448` → `c892719`: current-status duplication removed from 11 references,
  net reduction; parent inspected contract/install/README; link paths and diff check pass.
  Parent archived exact old handoff and compacted current brief (`a1360f1`).
- Analytics `dac9642` → `3112872`: completed remote sweep missing-state and binding-aware
  summary/atomic upgrade; worker refresh24 (one journey skip), dashboard12 pass. Independent
  reviewer `da8bed5b-2d37-4e7` required C5/C6; corrections integrated and independently approved above.
- Inventory `c5d1319` → `8682dc3`: immutable shared composition, fresh live checks;
  worker race/shuffle cli/capturedaemon/capturecoordinates and lint pass. Parent requested
  stronger immutable-snapshot test and C4 production path now integrated; focused four-package
  race/shuffle and lint passed. Independent review found fresh exclusions still enforced.
- Queue worker `9856b5b1-e75f-4ef`, `review-queue-simplification`: typed singleton state,
  evidence correspondence, delivery-result convergence, malformed work and measured sparse
  scheduler fix; implementation ownership released. Parent actual-method overlay on `e490b51` with20k inactive
  rows+1due: source/session each need313 claim calls (36.8ms/266.6ms SQL), with up to100ms
  worker wait per empty turn. This justifies due selection before page truncation.
  `10f6463/9fb6885` → `a827c14/32de1e9` select due work before page truncation;
  one-call regression failed against old query. The same20k probe now needs one call
  (~0.31ms source/~0.86ms session); focused head-blocking/fairness tests pass.
  Log `/tmp/memory-sparse-scheduler-before.log`; no live performance claim.
- Resource worker `a6a0477e-fccf-4e8`, `review-analytics-resources` base3112872: bounded
  dashboard `965cf69` → `1db3475`; exact SQL totals/50 details, exhaustive offline report
  retained. Synthetic 20,001 sessions/5,000 failures: 22.59MB/0.412s/188.8MiB RSS →
  38.7KB/0.065s/59.8MiB. Row cap is not a universal byte cap.
  Batch `32851c3` → `bda8512`: 1,000 rows/1MiB staged event/issue batches in existing
  transaction (one giant record remains line-sized). Synthetic identical event/issue
  digests on final JSON-preserving version: 1k2.417s→0.054s, 10k18.727s→0.182s. Reproducer:
  `tools/analytics/bench_publication_import.py`. Focused publications25/dashboard13 and
  actual Go-writer journey passed; C10 correction integrated. Largest real record,
  fresh sweep I/O, discovery/freshness, offline spool and retention horizon unqualified.

### Combined development qualification — `e8c96f890d199f8066e4b01d22b438d1a7d14051`

Parent ran commands serially on this Mac in pinned Nix, on AC with `caffeinate -i`;
`AGENT_SESSIONS_TEST_R2` and `AGENT_SESSIONS_LIVE` were unset. No gate configuration,
timeout, race check, simulation count or quality threshold changed.

| Check | Actual result / log |
| --- | --- |
| `make check` | PASS: format/lint/fix/tidy/deadcode/vulnerability scan, coverage **87.2% (13719/15732)**, package/CRAP thresholds, benchmark smoke/actionlint and capture acceptance. `/tmp/memory-holistic-check.log` |
| First `make test` | FAIL: isolation crash subprocess exceeded unchanged15s; legacy syncer suite exceeded unchanged10m. `/tmp/memory-holistic-test.log` |
| Focused failure investigation | Both isolation stages repeated10 times with race and the failed shuffle seed; PASS79.111s. Exact command below. `/tmp/memory-holistic-isolation-repeat.log` |
| Unchanged `make test` rerun | PASS: all packages race/shuffle; cli58.016s, sessioncapture154.164s, legacy syncer312.732s. `/tmp/memory-holistic-test-rerun.log` |
| `make fuzz` | PASS: all9 configured targets, default30s each. `/tmp/memory-holistic-fuzz.log` |
| `.analytics-venv/bin/python -m unittest discover -s tools/analytics -p 'test_*.py' -v` | PASS:111 discovered/12 skipped,16.757s. Skipped actual-process journeys ran separately in both make gates (12/12 plus6+6 launcher tests), not skipped acceptance. `/tmp/memory-holistic-analytics.log` |
| `node --check tools/analytics/site/app.js`; `git diff --check` | PASS |

Focused investigation command (same pinned environment/live opt-outs):

```sh
go test -race -shuffle=1793229077115693000 -count=10 \
  -run '^TestCaptureIsolationFixture(Process|ReturningStages)$' ./internal/cli
```

The failed test invocation is retained, not overwritten by its green rerun. Inspection
found host load/swap pressure and no surviving gate processes; the isolated failure did
not reproduce and the same source/full command subsequently passed. This supports a
transient timing explanation, not a proven root-cause fix. No timeout was relaxed and
no unrelated host process was stopped. No hosted CI or Linux full gate was run. Native,
live R2 and selected installed journey evidence was added subsequently below; local green
alone is not release acceptance.

### Finding dispositions and remaining dependencies

| Finding | Parent disposition |
| --- | --- |
| R1 source absence | Implemented/offline-validated, including actual production callback fatal policy handling; installed acceptance still required in phase5. |
| R2/R3 health | Fixed and validated: missing-current, inactive scope, same-name repoint/retry/restart, atomic v3 upgrade. |
| R4/R5 delivery/evidence | Shared guarded results and epoch-bound evidence validated. Intentional immediate/due semantics retained; historical unbound local evidence cannot retroactively prove epoch association. |
| R6 inventory/ownership | One compiled immutable inventory validated; filesystem/exclusions remain fresh. No extra ownership wrapper added: `RecordOwnership` already separates validated `ownershipClaim` from descriptive associations and policy checks; forged/duplicate/conflicting-claim tests remain. Another representation did not justify its conversion cost. |
| R7 scheduling | Indexed due selection and fairness validated with failing-before regression and synthetic measurement. Actual idle CPU/due latency and retained-payload `usage()` cost remain phase4/5 measurements; no speculative accounting cache. |
| R8 analytics | Bounded summary and typed transactional batching validated, including exact JSON/BIGINT semantics and actual writer journey. Refresh hashing/I/O, largest actual record, representative RSS/throughput and retention envelope remain phase4/5 measurements; no verification removed or arbitrary line cap added. |
| R9 documentation | Current status centralized here; historical handoff preserved, entry points/reference contracts corrected. |
| R10 local state/corruption | Typed singleton fields and explicit malformed enrichment quarantine validated; no speculative lock eviction. |
| Retirement/retention | Pending controlled acceptance/cutover. No GC, old-key deletion, legacy removal or indefinite-retention claim. |

### Separate-destination preflight — October 29 (superseded destination choice)

These checks preceded the existing-bucket reset. Their disabled/inactive observations
are historical, not the current installed state. Parent performed:

- Existing private credential parser passed; old-bucket `HeadBucket` succeeded.
  New-name `HeadBucket` and `CreateBucket` returned403 (creation `AccessDenied`). No
  new bucket/binding was confirmed. No ambient Cloudflare provisioning token was present;
  `op whoami` failed for each of the three configured accounts. Credentials were not printed.
- Planned bucket **`agent-sessions-capture-20261029-e3d6dd6e8bab4e8f`**, archive
  **`session-capture-20261029-e3d6dd6e8bab4e8f`**. The new-bucket name is now abandoned;
  retain only the fresh archive ID and private staging paths, and repoint staged configs
  to the verified existing bucket under the operator's explicit reset instruction.
- Bounded metadata-only walk on both hosts:30k entries/root,30s/root,60s/host; no source
  bodies opened, symlinks not followed, `.git`/`node_modules` omitted. All six configured
  roots enumerated within bounds; no unreadable entries,16 Mac/20 BC symlinks skipped.
  Disk-size candidates (JSONL/compressed JSONL/SQLite/DB suffixes, **not eligible coverage**):
  Mac4,150 files/4,366,522,606bytes; BC2,192/2,334,244,660bytes. Largest candidate files:
  Mac615,165,952bytes; BC231,567,360bytes. No largest-line, deduplication or retention claim.
- Both configured machine IDs match their preserved identity files and differ from one
  another. Both old archive IDs match. Legacy root registries queried read-only: zero
  learned roots on each; existing candidate configs also have no custom roots/aliases.
  Mac's first scratch query omitted Python's `uri=True` and could not open its URI;
  corrected read-only query succeeded. Use `mac-inventory-corrected.json`, not the first result.
- Local exclusion registries and unresolved native-policy markers: zero on both hosts,
  no parse/access failures. Bounded old-bucket `v3/machines/<preserved-id>/exclusions/`
  listings completed for both machines: zero remote records, no missing local opt-outs.
  These are scoped observations, not permission to omit fresh pre-capture/pre-upload checks.
- Built darwin/arm64 and linux/amd64 from a clean detached **`e8c96f8`** worktree using
  pinned Nix, `CGO_ENABLED=0 go build -buildvcs=true -trimpath`. Both build records show
  Go1.27.3, exact qualified revision and `vcs.modified=false`. SHA256:
  Mac`d2fe528f5e32b0a011b9e95d1e3f1e5651bff746332788fb71a0bad2dbe5e58d`;
  Linux`eeaabf4ebe460f062f3ae8d7a19251155ba633b45ce220f3987a053daa212b77`.
  Copied Linux binary into its private staging directory and verified identical SHA256;
  did not replace an installed executable. Removed only this newly created clean build
  worktree after completion; unrelated worktrees untouched.
- Staged private configs on both hosts, changing **only** archive ID, bucket and new
  spool path; preserved machine ID, policy, roots, aliases and all budgets. Verified old
  config bytes unchanged. Gated binaries on each host passed `capture-v1 policy-pauses`
  (zero markers) and `capture-v1 service --action plan` (valid JSON), with credentials
  removed from their environments. The service plan used the staged binary only for
  config validation; the final supervisor must use the credential-scoped launcher wrapper.
  Neither `init`, `run` nor service/hook apply was run.
  Fresh spools remain absent; final launchd/systemd checks confirm capture disabled and
  BC inactive. Mac refresh/dashboard remain disabled.

Private evidence/staging on each host:
`~/.local/state/agent-sessions-release/20261029-e3d6dd6e8bab4e8f/`.
Mac contains `destination.json`, the bounded `inventory.py`, corrected Mac and BC inventory
JSON and `remote-policy-inventory.json`; both hosts have inactive `capture-runtime.json`
and their gated binary. Do not commit private inventories/configuration or credentials.

Sol/medium read-only preflight worker `5ec650de-241b-4e9` completed and released ownership.
It confirmed there is no source-only inventory or one-shot candidate backfill command:
`run` captures and can grow the spool even when destination access fails. Legacy `backfill`
is not a substitute, and `service install --apply` starts the supervisor. Parent's actual
preflight used only the safe existing commands above; no new runtime code or gates changed.

### Existing-bucket reset and installed qualification — October 29

- Isolated live writer→R2→reader→analytics gate passed:76/160 backend operations,8.31s.
  Complete private pre-reset inventory of `agent-sessions`:225,196objects,
  7,480,288,690bytes,226pages; SHA256
  `4196410f5fcae1e39f6157a25265f960345450b3687c67be34e4c0953e3d246e`.
  Inventory includes143,298 `capture-v1/`,81,896 `v3/`,2 other keys.
- Before switching, both PATH wrappers were verified byte-identical candidate-only fences;
  legacy `sync` exits2. Capture supervisors inactive, no agent processes/cron entries at
  checks; managed native hooks invoke the owned candidate wrapper. Preserve those fences.
- Parent and Sol/medium reviewer `b43328b1-1ac7-431` corrected wipe-first ordering:
  bulk retirement remains post-acceptance. Conditional DELETE did not demonstrate its
  precondition on an owned probe and was **not** used on the old binding. A separate owned
  probe verified stale/valid conditional PUT. Parent then rechecked old binding bytes/ETag,
  CAS-PUT exact qualified-writer-generated bytes without automatic retries, and verified
  exact readback. `binding-cutover.json`: `fresh_binding_verified`,00:38:59.115887 UTC.
  Production binding replacement guards remain unchanged. Old binding bytes are private
  evidence, not a rollback instruction. Fresh canonical prefix was absent from inventory.
- Both installed wrapper `init` calls passed. Exact prebuilt qualified binaries were installed
  with backups; runtime changes only archive ID/spool, preserving existing backend, identity,
  policy, roots, aliases and budgets. Capture services installed/started on both hosts.
  Archive: `session-capture-20261029-e3d6dd6e8bab4e8f`; bucket remains `agent-sessions`.
- Mac analytics source extracted from `e8c96f8`;111 tests passed35.407s in existing Python
  runtime. BC reader config preserves BC identity/backend and points to fresh archive/spool.
  Refresh/dashboard plists backed up and repointed to qualified source and release-private
  `analytics/publications.duckdb` / `analytics/refresh.sqlite3`, then enabled/started.
  Runtime remains the existing installation under `c9749dacf73c05a07d53d84386a871f2ad8fa323/runtime`;
  source is under `e8c96f890d199f8066e4b01d22b438d1a7d14051/source/tools/analytics`.
- Selected **actual installed** Mac Pi and BC Codex publications passed durable-job identity,
  publication/sequence, source/epoch and byte-hash agreement through the sole R2 reader,
  importer and event index;62/199 events, repeat import `unchanged`,0.766s/0.604s respective
  full probes. Shared report bound to fresh archive and contained both machine identities
  (681 sessions/684 sources at that observation). Private `actual-journeys.json` and
  `verify_installed_journeys.py` preserve evidence; source contents/identities not committed.
  These selected journeys do **not** prove complete inventory or sustained acceptance.
- Measured an actual published615,165,952-byte SQLite artifact through the sole reader:
  3.829s/37,339,136bytes maximum RSS; importer0.805s/95,387,648bytes RSS, one source,
  zero events/one opaque-format issue (not a claim to normalize SQLite). Probe bounded at
  768MiB. The existing512MiB consumer budget cannot accommodate this real source. Based on
  this validated slice, parent set the installed refresher's explicit budget to768MiB,
  preserving300s timeout and all quality-gate thresholds. Refresh was stopped while holding
  the cooperative projection lock, repointed and restarted; all3,236 session rows remained.
  `large-artifact-measurement.json` and `refresh-budget-adjustment.json` are private receipts.
  This qualifies one artifact, not the largest JSONL line or an indefinite retention horizon.
- Pinned-Nix actual native scratch gate passed unchanged: `go test ./cmd/agent-sessions
  -run '^TestCaptureNativeLive$' -count=1 -v`, with `AGENT_SESSIONS_CAPTURE_NATIVE=1` and
  explicit installed analytics Python; unrelated live/R2 flags unset, under `caffeinate -i`.
  Claude2.1.311, Codex0.164.0 and Pi1.4.0 all passed native hook→durable queue→local backend→
  sole reader→importer→event index→report, idempotence and uninstall negatives; Codex wrong
  trust hash rejected. Test26.22s/package27.870s. `/tmp/memory-native-e8c-20261029.log`.
  Scratch is loopback-sandboxed on Mac; this is not a Linux native gate or actual R2 journey.
- First consumer sweep exposed one `current-unreadable` Mac coordinate. A later independent
  installed-reader retry succeeded; refresh status was **not** manually cleared or marked
  healthy. Await autonomous reconciliation/full sweeps. Dashboard HTTP200 is not green health.
- Retirement metadata: bounded multipart listing completed with0 uploads. Versioning API
  returned403; do not claim an observed empty version list. Cloudflare's current
  [S3 compatibility reference](https://developers.cloudflare.com/r2/api/s3/api/) lists
  Get/PutBucketVersioning as unimplemented. No bulk old-object deletion has occurred.

At **01:04 UTC**, metadata-only operating samples (`*-operating-1.json` privately):

| Metric | Mac | BC-01 |
| --- | --- | --- |
| Discovered sources / pending capture | 7,517 /0 | 14,566 /2,482 |
| Published jobs / pending publication | 2,174 /5,740 | 2,878 /~9,549 |
| Sweep / completed sweep | 3 /0 | 2 /0 |
| Discovery / coverage | timeout /partial | timeout /partial |
| Quarantine / destination pause / step errors | 0 /false /0 | 0 /false /0 |
| Allocated spool / configured cap | 1,562,619,904bytes /8GiB | 587,911,168bytes /6GiB |
| Free bytes | 192,209,985,536 | 64,741,437,440 |
| Process observation | RSS169,132,032bytes;182.7% CPU sample | systemd MemoryCurrent583,233,536bytes |
| Native evidence rows | 2 | 0 |

These are initial-backfill samples, not idle/capacity/retention measurements. Discovery
issues are predominantly protected-source scrubbing, executable review, ownership pending,
policy denial, adapter omission, compressed sources and missing launch integration.
Issue presence must not be equated to lost raw sessions or silently waived as harmless.
At01:12, Mac8,369sources/0pending capture/2,800published/5,973pending publication;
BC19,443sources/3,004pending capture/3,933published/12,853pending publication. Neither
completed a clean sweep. BC growth is predominantly artifact inventory, not demonstrated
unchanged-publication churn. Native evidence now3/1 rows. Scalar snapshots and role/transform
aggregates are in `*-operating-2.json`; no idle/retention claim follows from these deltas.

Read-only audit found a concrete timeout boundary to repair: discovery can advance its
in-process cursor and return requests after its operation deadline, then `Hint` admission
uses that same expired context. A later sweep may recover them, but this is not durable
admission or a bounded freshness guarantee. Ordinary fingerprinted files are no-ops when
unchanged; transformed sources intentionally re-prepare/hash. Discovery cursor position
is in-process, not durably checkpointed, and restart begins re-traversal. These limits must
remain explicit. Parent reassigned `dbb9cacd-4a19-4e9` to a narrow regression/fix in isolated
`/Users/mm/dev/memory-wt/review-discovery-admission`, branch `delivery/discovery-admission-fix`:
only runtime discovery admission and related tests, no host/cloud authority or handoff edits.
The worker committed `922aaae`, integrated as `36dcef4`; implementation ownership is now
released. Independent Sol/medium reviewer `46e1e90a-cd41-404` approved the scoped repair
and ran focused race/shuffle tests. The fix records the callback deadline before canceling
it and uses the existing separate bounded persistence budget for admission. No timeout
was extended; timed-out batches cannot falsely complete. Parent cancellation is respected.
Admission/save-budget expiry and non-durable cursor recovery remain explicit limitations.

The parent probe initially selected top-level `Sources` (details), not `Runtime.Sources`
(count); use **`work-status.Runtime` scalar fields only** in public summaries. Detailed
source identifiers belong in private evidence, not routine output.

### Discovery admission follow-up qualification — `36dcef4143c05a4f84ec7368e0e43df5068368b6`

- Deterministic regression failed before the fix: a valid returned request was not admitted
  after callback expiry. New tests prove admission without false completion, actual `q.Run`
  publication without another hint, and parent-cancellation behavior. Worker focused
  race/shuffle repeats and lint passed; independent review approved scope, not release.
- Parent unchanged pinned-Nix `make check` passed: coverage87.3% (13731/15733), all package/
  CRAP thresholds, lint/format/tidy/deadcode/vulnerability/benchmark/actionlint and12+6+6
  process/launcher acceptance. `/tmp/memory-discovery-admission-check.log`.
- Unchanged `make test` passed race/shuffle: CLI64.623s, sessioncapture155.275s,
  syncer303.876s, plus12+6+6 acceptance. `/tmp/memory-discovery-admission-test.log`.
  All nine default30s fuzz campaigns passed; `/tmp/memory-discovery-admission-fuzz.log`.
- Actual Mac native gate reran against the clean qualified source: all three agents,
  byte-exact export/index/report, idempotence, uninstall and Codex trust-hash negatives
  passed; test25.26s/package28.029s. `/tmp/memory-native-36dcef4.log`.
  No hosted CI or Linux full/native gate was run. Analytics source is unchanged from the
  qualified `e8c96f8` installation; no unrelated suite evidence is substituted for live checks.
- Built clean detached36dcef4 with pinned Go1.27.3, CGO0, trimpath/buildvcs;
  both `vcs.modified=false`. Darwin SHA256
  `25a665433b0bff0363af9e0f693485e6c0a26d71fbfea987cbe1dd8104d32e98`;
  Linux `c68b667e77d9036db64593364b169556c3601fa1caaa45651a3a404fef6de4a7`.
  Removed only the newly created clean build worktree after use.
- Mac identity and empty policy-marker checks passed; queue SQLite backup API and
  `quick_check` passed. Atomically replaced only the binary and restarted the owned service.
  Config bytes unchanged. At the first restart verification, all prior jobs/manifests
  remained exact and117 pre-restart pending jobs were now published. Current sweep8 had
  no discovery error yet, but **no completed sweep**; source counts11,022/pending capture89,
  pending publication~5,560. Nine cumulative delivery-deadline step errors had timestamps
  **before** this restart; retries remain visible. Do not claim the admission fix repairs
  delivery timeouts, fully captured inventory or completed operating observation.
- BC transfer attempt timed out60s. Bounded diagnostic reached server-key acceptance but
  not signing/authentication/command execution; a further45s check likewise stalled.
  Parent immediately requested 1Password unlock/approval, not new bucket/credential authority.
  No BC installed-binary replacement was issued; staged-transfer completion is unconfirmed.
  [SSH authorization guidance](https://developer.1password.com/docs/ssh/agent/security/).
- Additional actual main-transcript resource slice:84,656,404bytes,9,522 lines, largest
  line353,973bytes within that selected body. Sole reader1.383s/37,044,224bytes maximum RSS;
  importer1.082s/302,612,480bytes RSS,16,635 events/0 issues. This was the largest main body
  among published Mac jobs at selection, **not** an exhaustive largest-record claim.
  Private `large-main-measurement.json` retains the bounded probe.
- Full frozen-inventory metadata check confirms only2 legacy machine namespaces and
  **zero exclusion keys across both**, consistent with prior per-machine reconciliation.
- Read-only Sol/medium refresh audit `ee981de0-a18c-44a` completed/released: current-head
  validation already has8 bounded workers and5s per-coordinate budgets. Do **not** add
  speculative concurrency. Actual page samples:16 Mac heads0.759s;64 BC heads0.959s,
  zero failures. Initial export/import backlog differs from steady-state page/projection-
  hash cost. Measure a complete settled sweep before changing scheduling or verification.
  Later consumer sample:6,758 sessions/7,321 sources, no head failures (the earlier error
  resolved autonomously), one target `process_failed`, BC sweep incomplete/stale. That
  target independently exported successfully in4.07s; status was not manually cleared.

Follow-up binaries, SQLite/binary backups and receipts live under the existing private
release directory's `discovery-admission-36dcef4/`; parent installer script is
`install_discovery_admission.py`. It refuses unexpected prior binary/config identities.
Parent retains all host/cloud authority. The subsequent operating correction was identified
and then closed in code as follows:

- At02:20 Mac reported completed sweep8/current9 and no discovery error, but cumulative
  step errors increased9→20; latest was `discovery: context deadline exceeded` at02:18.
  Source count12,766/pending capture0;7,619 published/~5,803 pending publication;
  spool2,358,558,720bytes, native evidence6, no quarantine/destination pause. These are
  retained in private `mac-operating-post-repair.json`, not accepted as clean coverage.
- Parent identified an unclosed failure path: admission may exhaust the shared save budget,
  so metadata persistence fails before recording `sweep_error`. The discovery loop can
  swallow that deadline as transient and only update generic step-error diagnostics;
  a subsequent complete batch can falsely complete the generation/resolve unseen issues.
  This is not excused by the documented non-durable cursor limitation.
- Original worker's session was released after retention; its completed ownership is clear.
  New Sol/medium owner `dfe52705-d8c4-4b4`, worktree
  `/Users/mm/dev/memory-wt/review-discovery-error-persistence`, branch
  `delivery/discovery-error-persistence`, base36dcef4, owns only narrow discovery/error
  persistence source and regression tests. Required evidence: failure-before/later-complete
  regression, retained prior issues, fail-closed authoritative error durability and cancellation.
  No timeout extension, admission discard, policy bypass or generic cursor/work framework.
  Worker committed `b45c1c2`, integrated as `b8cbed2`, and released implementation ownership.
  Independent Sol/medium reviewer `47a483ee-5e2c-4c4` approved the scoped correction and ran
  focused race/shuffle tests twice. Failure recording retries only metadata, never request
  admission. If that bounded retry fails, a non-transient sentinel stops the runtime.
  Dedicated before-fail/after-pass tests retain prior issues across a later complete batch
  and verify an unrecordable failure stops the discovery loop. No durable cursor guarantee.

### Final discovery follow-up — `b8cbed27f79718a0f13eee808cfbe03e6c4bc019`

- Parent reran unchanged full gates serially in pinned Nix with live opt-ins unset and
  `caffeinate -i`: **make check/test/fuzz all passed**. Coverage87.2% (13732/15739), all
  package/CRAP thresholds; CLI74.995s, sessioncapture190.404s, syncer373.362s under race/
  shuffle; all9 default30s fuzz campaigns. Both make gates include12+6+6 process/launcher
  acceptance. Logs `/tmp/memory-discovery-persistence-{check,test,fuzz}.log`.
- Actual Mac native gate passed again from clean source: Claude/Codex/Pi, exact export,
  import/index/report, idempotence and trust/uninstall negatives; test24.94s/package27.661s.
  `/tmp/memory-native-b8cbed2.log`. No hosted CI or Linux full/native gate claimed.
- Clean pinned Go1.27.3, CGO0/trimpath/buildvcs builds, `vcs.modified=false`:
  Darwin SHA256 `6beae15af8c0957ac19c2eff4130df63441ad4a509e651677c11d6c067246d66`;
  Linux SHA256 `9373dca451e89b91cc1574b912781f7344da674bf98cb6e1e874fc5b8e73ef90`.
  Newly created clean build worktree removed afterward; unrelated trees untouched.
- SSH signing access recheck succeeded3.82s. BC was freshly reconciled before installation:
  original e8c96f8 service still active with0 restarts,28,700 sources/16,207 published jobs,
  no quarantine/pause. Final Linux binary transferred and hash checked; intermediate36dcef4
  was not installed there. Both final installs verified machine identity, empty policy
  markers, prior/new binary hashes, SQLite backup API/quick_check and unchanged config bytes.
- Final binary-only restarts03:07:56 Mac/03:08:09 BC. At03:27, all pre-upgrade job rows and
  manifest bytes remained exact;964/1,541 previously pending jobs were now published.
  Mac published11,963/pending~4,715; BC18,018/pending~11,546. Both still report partial
  discovery/timeout, no quarantine or destination pause. Mac's last generic step error
  predates this final restart; BC has6 capture-deadline step errors and visible retries.
  **No clean post-final-install complete sweep or settled operating budget is claimed.**
- Later consumer sample at03:41 UTC:14,723 sessions/15,303 sources,0 projection failures; one
  target error, BC sweep incomplete and health stale. No head failures. Report HTTP200
  and increasing counts do not imply complete source coverage or healthy refresh.
- Current release-private subdirectory `discovery-persistence-b8cbed2/` on each host has
  `install.json`, `queue-before.sqlite`, `previous-agent-sessions.bin` and
  `restart-verification.json`. Current guarded installer is `install_discovery_persistence.py`.
  Do not replay older installers or archive-switch scripts.

### Bounded operating observation in progress

Parent started a **temporary, read-only, finite24h sampler** on each host, using the
already validated scalar `operating_snapshot.py`. It does not read source bodies, mutate
configuration/services, access R2, decide acceptance or delete anything. No permanent
watcher/service or new runtime authority was added. Every300s it records local queue,
role/transform debt, source/sweep/error counters, supervisor state, disk/RSS and binary/
config digests. Sample failures and wall-clock gaps remain visible; sleep/reboot gaps do
not become synthetic catch-up samples or a claim of uninterrupted operation.

- Mac observer PID70279, started2026-10-29T03:27:25.983693Z.
- BC observer PID3667502, started2026-10-29T03:27:34.734970Z.
- Both first two samples verified,0 sampler failures. Intended end approximately
  **2026-10-30T03:28 UTC**, subject to actual host availability.
- Artifacts per host: `discovery-persistence-b8cbed2/observation/{started.json,samples.jsonl,completed.json}`;
  process diagnostics in the parent directory's `observation-process.log`.
- Sol/medium read-only deferred reviewer `39f3f357-a580-473` is verified running its one
  bounded wait. It may read only the Mac aggregate observation/installation receipts and
  this brief, then report gaps via the supported completion notification. No SSH/cloud/
  host-action authority. Parent must inspect BC and consumer evidence separately; session
  termination may interrupt the reviewer, while local sampler evidence remains private.

## Next action / actual release boundary

1. Parent reviews the completed observation and any earlier material failures; verify an
   actual completed-sweep timestamp **after the final03:07/03:08 install**, stable identities/
   policy/configuration, backlogs, retries and representative capacity. Earlier36dcef4
   completion counters are not qualification. Inspect both host artifacts and supervisor
   state; do not equate a finished sampler or reviewer with acceptance. No queue reset or
   archive rebind. No active implementation worker remains.
2. Complete full fresh-reader consumer sweeps; independently verify actual installed
   restart/retry/privacy/source-absence behavior and two-host independence. No status edits,
   queue resets, timeout increases or gate bypass to obtain green.
3. Qualify representative resources, largest records, offline spool/retention horizon and
   24–48h autonomous operation. Preserve unavailable/deleted unpublished-history caveats.
4. Only after acceptance: separately reviewed deletion of frozen old inventory keys, excluding
   the **now replaced** global binding and every fresh-archive key; recheck fences and inventory
   drift first. Never blindly delete the original inventory wholesale. Then remove retired
   legacy-only code/operator paths and rerun checks.

All six phases are **not** complete. Existing development qualification remains applicable
because production code is unchanged. Binding replacement and controlled activation are
completed milestones, not full release/operating acceptance.
