# Adversarial resolutions: implementation and acceptance evidence

**Status:** all R1–R10 and the two additional integration items I1/I2 are implemented,
independently reviewed and accepted by the parent for **offline/development scope**.
There are no outstanding code-review corrections in that scope. This is not installed,
Linux/native, live-R2 or operating/cutover acceptance; no deployment occurred.

Original findings: [adversarial review](adversarial-review-20261029.md).
Coordination/current deployment boundary: [handoff](session-capture-handoff.md).

## Source and ownership

- Original production baseline: `3ea6752599ca5ea59d1c26d08969e92043b3e04b`.
- Implementation worktree base: `468bc638489d7071c427203658eba6e5bcb3bea9`
  (review/plan documentation only; same production source).
- **Combined qualified source: `c8fdf7ac3ceecc6b6ecfef92f68542f055927374`.**
  Full gates and final process proofs below ran at this commit with only the
  parent-owned handoff documentation dirty. Subsequent evidence/handoff documentation
  does not change the qualified production, tests or Makefile.
- Stack child: `delivery/adversarial-resolutions`, above
  `delivery/fresh-archive-qualification`. No push, PR or hosted CI run was performed.
- Five implementation owners and independent reviewers used **`openai/gpt-6.1-sol`,
  medium thinking**. Workers used isolated worktrees. The parent reviewed actual diffs,
  challenged unsafe expansions, integrated commits, owned serial full gates and made
  the acceptance decisions—not the workers.
- Original sources, installed services/queues, cloud archives, credentials, identities,
  exclusions and frozen untracked research were not changed. Synthetic tests created
  and removed only their own temporary fixtures.

## Per-item acceptance

Every row below is covered by the final combined gates, not merely a worker completion
claim. Test names are committed and can be inspected/re-run. A **before failure** means
an intended assertion failed against the defective implementation; it is not a failed
product gate being relabeled green. Some tests intentionally assert a rejected operation.

### R1 — Destination-independent launcher startup: accepted

**Integrated commits:** `2ecdf10`, `5401cc8`; mandatory process-test wiring `efbf16d`.

- Only `run` continues when the explicit credential file is missing, unsafe or malformed.
  It uses a credential-empty destination environment and a content-free warning.
  Remote-only init/export/current-head commands remain strict.
- The wrapper reads only bounded private runtime-config credential references, clears
  both fixed and configured names, then maps **only** successfully parsed explicit-file
  values. Alternate environment names and case-insensitive Go JSON field spellings
  cannot retain ambient credentials. Ambiguous duplicate fields/references fail closed.
- No default credential discovery, background credential refresher, network override or
  generic config parser was introduced. Repair requires deliberate stopped-state resume
  and process restart; running processes do not reload environment values magically.

**Before:** original wrapper plus the actual production binary exited before capturing
for both missing and malformed credentials. Additional alternate-reference tests showed
ambient sentinels reaching the **non-network** SDK peer despite file failure.

**After:** committed `tools/capture-delivery/test_launch_journey.py` proves actual wrapper
→ unmodified production binary → durable captured bytes, pending publication and visible
pause, followed by clean stop and reopen after deleting the synthetic original. With the
separately injected SDK peer, alternate ambient refs cause **zero runtime requests**;
explicit-file repair/resume/restart publishes retained work without the source or another
hook, including lost-ACK handling. Final integrated run: **12 tests, no skips** with peer.

The mandatory gate now passes its already-built binary into launcher discovery, so the
production startup case cannot silently skip there. The alternate-ref runtime-peer case
is separately opt-in and was actually run by the parent; it is not claimed from a skip.

**Review:** the first independent review found the alternate-ref hole; correction review
approved `4a07de0` (integrated `5401cc8`) after actual before/after evidence.

### R2 — Atomic durable importer failure: accepted

**Commit:** `abc7ee8`. **Code:** `tools/analytics/publications.py`.

Failure replacement now uses a short explicit transaction with `BaseException` rollback;
older retries preserve a newer failure watermark and increment attempts correctly.

**Before:** injected interruption between real DELETE and INSERT erased the outstanding
failure. **After:** `test_failure_replacement_interruption_retains_durable_watermark`
checks both `KeyboardInterrupt` and `OSError`, reopens the DB/report, retains the exact
prior row and stale old success, then verifies successful replacement and older retry
monotonicity. Independent review approved the transaction/invariant changes.

**Limit:** this is interruption at the real replacement boundary, not an OS-kill/power-cut
campaign or separately injected COMMIT failure. It relies on DuckDB transaction atomicity.

### R3 — No premature green health on retry: accepted

**Commit:** `abc7ee8`. **Code:** `tools/analytics/publication_refresh.py`.

Expectation registration no longer clears the old error. Existing verified-success paths
clear it; endpoint binding and imported-fingerprint checks remain intact.

**Before:** interrupted same-coordinate retry changed actual health to green.
**After:** `test_interrupted_same_target_retry_preserves_dashboard_error_until_verified`
reopens real SQLite status after interruption and asserts actual dashboard health stays
unhealthy with the prior completion timestamp. Verified success then clears the error
without changing actual import freshness. Independent review approved.

**Limit:** transport and initial import are mocked by the scheduling fixture; persistence
and exposed dashboard-health computation are real. Actual process journeys also pass in
the final gate, but are not substituted for this precise interruption regression.

### R4 — Shutdown does not spend optional-work poison budgets: accepted

**Commits:** `d374948`, `6b81b33`. **Code/tests:** `internal/sessioncapture/`.

Pure caller cancellation refunds only the owned claim, using expected-attempt and state
guards. Ready results and verified attachment state survive; terminal/new-owner states
are not reset. Genuine deadlines and mixed cancellation errors remain failures. Optional
source absence remains isolated rather than becoming a new run-wide PathError failure.

**Before:** both workers quarantined healthy work at the terminal attempt; a saved result
was lost from ready state. **After:**

- `TestOptionalWorkersShutdownBudget` and `TestEnrichmentShutdownKeepsSavedResult`;
- `TestRepairShutdownKeepsVerifiedAttachment`;
- `TestShutdownRefundDoesNotChangeAnotherClaim`;
- `TestEnrichmentSourceAbsenceRemainsIsolated`;
- `TestEnrichmentShutdownBeforeFirstResultPointerSave`.

The last test starts with **no** result pointer, builds/stores a real result, cancels
before its first pointer save, verifies pointer/payload/refunded budget, reopens and
attaches with the callback unavailable. A targeted mutation reverting the protected save
context failed with `pointer=false,state=retry,attempts=7`; corrected count2 race/shuffle
checks passed18.373s. Independent review required this stronger test; a second reviewer
approved its exact correction. No broad fatal-error reinterpretation was retained.

### R5 — Stable Pi source/epoch identity: accepted

**Commits:** `b0b4f9c`, `c8fdf7a`. **Code:** `internal/adapters/pi.go`,
`classify_logical.go` and candidate runtime tests.

Ordinary and declared-coordinate classification share one structural interpretation.
A sibling `sessions/` directory no longer elects transcript identity. Related-source
namespace comes from the validated session key. Recognized top-level `.log` resources
retain their established key/role instead of being demoted by the new interpretation.

**Before:** unchanged flat transcript became another resource identity after mkdir;
independent review also caught the initial repair's custom-root `.log` regression.
**After:** `TestPiStructuralIdentity`, `TestPiStructuralLogResource`,
`TestAdversarialPiIdentityMutation`, and `TestAdversarialPiCustomLogResourceIdentity`
cover ordinary/logical, canonical/custom/environment-selected roots, absent roots,
main/task/fork/run layouts and unrelated alias toggles. Actual repeated append captures
assert identical full Identity/SourceID/EpochID/role, **exactly +1 revision**, and exact
byte growth through mkdir/removal/alias-on/alias-off transitions. Scoped independent
review and correction recheck approved. No sibling inference or migration layer added.

### R6 — Framed Unicode normalization isolation: accepted

**Commit:** `abc7ee8`. **Code:** `tools/analytics/events.py`.

All normalized components are checked before any event/model/dedupe state is emitted.
Unrepresentable Unicode becomes a safe framed-record issue; raw bytes are unchanged.
Storage/DB failures are not swallowed as malformed records.

**Before:** an escaped lone surrogate caused whole-publication `import_failed`, dropping
healthy siblings from the new projection. **After:**
`test_malformed_unicode_isolated_with_raw_coordinates_and_idempotence` imports two healthy
events alongside20 malformed issues with exact byte offsets/lengths, keeps paired
surrogates valid, verifies owned raw bytes and repeat-import idempotence. Additional tests
cover Pi inherited model, reused native IDs after discarded records, I/O/DB propagation,
and existing duplicate-key/numeric-lexeme JSON staging semantics. Independent review
approved; final full analytics tests pass.

### R7 — One immutable-payload verification pass: accepted

**Commits:** `d374948`, `6b81b33`. **Code:** `internal/sessioncapture/deliver.go`.

The initial full byte/hash/decode verification stays before optional delivery marking.
The nested helper rechecks mutable policy/latches without rehashing all immutable bytes.
Pre-upload, manifest and CAS privacy checks and remote dependency validation remain.

**Before/after:** `TestAttemptVerifiesPayloadsOnce` measured **24 → 12 verifier read
checks** for three objects, matching a positive calibrated single pass. An explicit
`expected > 0` assertion prevents vacuous instrumentation success. Corruption after the
local pass still fails publication and preserves the previous readable publication
(`TestAttemptCorruptionAfterLocalVerificationCannotPublish`). Independent review approved.

**Performance limit:** five-attempt synthetic medians were611.003→608.899ms wall and
297.464→294.960ms CPU, with overlapping samples. This establishes halved redundant local
verification work, **not a reliable wall-time/CPU speedup or production freshness gain**.
No persistent verification cache or upload-ACK bitmap added.

### R8 — No legacy ownership rediscovery in alias policy: accepted

**Commit:** `b0b4f9c`. **Code:** CLI alias/claimed-session policy translation.

Alias exclusion uses the independently validated owner; preserved exclusions use their
full durable tuple. Only the proven root prefix is translated. Empty/unknown identities
remain conservative. No new ownership model or blanket legacy safety deletion.

**Before/after:** baseline instrumentation observed **15 classifier invocations → 0**.
`TestAliasPolicyNeverReclassifiesOwnership` and
`TestClaimedArtifactAliasesUsePreservedTupleWithoutEnumeration` provide deterministic
mechanism checks rather than timing guesses. The actual queued-artifact test also proves
opt-out denial/no remote current under duplicate UUID/old-root aliases. Independent review
approved R8 separately; later R5 correction leaves these changes intact.

The original full-identity guard already denied the reviewed opted-out artifact. This
repair does not claim to close a demonstrated byte-disclosure exploit; it removes
unbounded work and contradictory helper/discovery ownership interpretation.

### R9 — Safe teardown/inspection independent of capture readiness: accepted

**Commits:** `2ecdf10`, `5401cc8`.

`LoadControl` shares strict bounded/private/owned parsing, identity/path/declaration/
overlap validation, omitting only live policy-directory readiness for nonactivating
local controls. Capture/start/replay/resume stay fail-closed. Hook removal does not
require the old executable to exist. Installed-service checks precede supervisor calls
or deletion; start/restart require installed argv to match the qualified desired argv.

**Before:** policy loss blocked CLI stop/uninstall/status; old executable deletion blocked
hook removal. **After:** `TestBrokenPolicyTrustedLifecycle`,
`TestControlLoaderRetainsTrustBoundaries`, `TestRemovalWithoutOldExecutable`,
`TestServiceActivationRequiresExecutable`, and
`TestManagedServiceActivationRejectsExecutionDrift` cover trusted controls and activation
negatives. Paths, unknown fields, overlap, ownership and foreign plans remain protected.

Independent review caught Program/Exec overrides and activation of unqualified old
coordinates in the first service recognizer. `TestManagedServiceRejectsExecutionOverrides`
now asserts **no runner call and no deletion/replacement** for duplicates, resets,
whitespace overrides, extra execution directives, unknown/ambiguous grammar and unsafe
paths. Generated templates with supported numeric tuning and managed upgrades pass.
The corrected implementation was independently approved. No real supervisor ran.

### R10 — Writer/export envelope agreement: accepted

**Commit:** `e5393fc`. **Code:** `internal/publication/export.go`, `publication.go`.

One deterministic prospective export serializer is shared by writer validation and
export preflight. The inclusive **8MiB** limit is unchanged; full identities and
selected/withheld closure are preserved. Inevitable overflow is rejected before durable
publication backend access or export body GET/destination creation.

**Before:** a limit+1 export envelope passed writer validation and publication, and
export rejection occurred after two body GETs and destination creation.
**After:** `TestExportMetadataEnvelopeBoundaryJourney`, `TestExportMetadataPreflightNoIO`,
and `TestExportMetadataStoredOversizeNoIO` cover exact limit−1/limit/limit+1, multiple
selected/withheld entries, actual Publish→Read→Export and direct/public no-I/O negatives.
Entire publication package passed race/shuffle (40 top-level tests,20.853s), then final
combined gates. Independent review approved. Python consumer limits were not expanded.

### I1 — Declared root aliases compose with native hook targets: accepted

**Commit:** `2ecdf10`. Existing inventory equivalence resolves the single safe physical
hook root, including inherited parent routes; no second source classifier.

**Before:** a logical symlink failed planning; an absent logical root could target the
wrong location. **After:** `TestDeclaredAliasNativeHookTargets` performs synthetic CLI
installation at the declared physical target for both cases. `TestHookRootsProjectInheritedDeclaredRoute`,
`TestHookRootRejectsUndeclaredAndAmbiguousResolution`, and
`TestHookRootsIgnoreLeafAliasesAndRetainMissingOrdinaryRoot` preserve undeclared-link,
multiple-target and leaf-to-parent authority negatives. Existing backup/privacy/race
coverage and final gates pass. No real native-agent installation occurred.

### I2 — Readable supervisor directory without weakening secret storage: accepted

**Commits:** `2ecdf10`, `5401cc8`.

Supervisor-file placement accepts an owned non-group/world-writable parent, keeps the
unit0600 and never chmods an existing directory. Secret/config `WritePrivate` keeps its
private-directory rule; only atomic-write mechanics are shared.

**Before/after:** `TestReadableSupervisorParent` demonstrates0755 parent install through
an injected runner, unchanged0755 parent/private0600 file, rejection of0777 parent, and
continued secret-writer rejection of0755. Managed upgrades/drifted teardown and strict
foreign-unit rejection are covered by the R9 tests above. UID checks are source-reviewed;
no privileged foreign-owner fixture or real host permission change was attempted.

## Final integrated gates

All commands ran serially on this Mac, on AC under `caffeinate -i`, inside pinned Nix.
Go1.27.3; shell Python3.14.8; the existing analytics venv is Python3.13.15/DuckDB1.5.6.
No timeouts, race checks, coverage/CRAP thresholds, simulation counts or fuzz durations
were reduced. The only Makefile change passes the already-built binary to new launcher
process tests in the existing acceptance target.

| Check on `c8fdf7a` | Result | Local log |
| --- | --- | --- |
| `make check` | **PASS**,511.76s; coverage **87.4% (13933/15943)**, package/CRAP thresholds, format/lint/fix/tidy/deadcode/vulnerability, benchmark smoke/actionlint, consumer/launcher acceptance | `/tmp/memory-resolutions-check.log` |
| `make test` | **PASS**,352.22s; full race/shuffle, CLI77.603s, publication53.619s, sessioncapture163.437s, legacy syncer296.339s; acceptance journeys included | `/tmp/memory-resolutions-test.log` |
| `make fuzz` | **PASS**,286.07s; all9 configured targets at unchanged30s | `/tmp/memory-resolutions-fuzz.log` |
| Full analytics discovery | **PASS**,117 discovered,12 skipped,17.101s; the12 actual-process journeys passed separately in both make gates | `/tmp/memory-resolutions-analytics.log` |
| Final actual launcher, unmodified integrated binary | **PASS**,12 discovered/1 SDK-only skip,1.623s; missing/malformed production startup journey ran | `/tmp/memory-resolutions-launcher-final.log` |
| Final launcher with additional non-network SDK-peer binary | **PASS**,12 tests/no skips,2.938s; alternate refs/no ambient runtime request + repaired retained-work delivery | same log |
| `node --check tools/analytics/site/app.js`; `git diff --check` | **PASS** | parent command results |

Exact gate wrapper (run independently for `check`, `test`, `fuzz`):

```sh
/usr/bin/caffeinate -i nix develop --command env \
  -u AGENT_SESSIONS_TEST_R2 -u AGENT_SESSIONS_LIVE \
  -u AGENT_SESSIONS_CAPTURE_NATIVE -u CANDIDATE_R2_LIVE_ACCEPTANCE \
  -u CAPTURE_V1_BINARY -u CAPTURE_V1_REPAIR_BINARY \
  /usr/bin/time -p make check
```

Full analytics:

```sh
nix develop --command env -u AGENT_SESSIONS_TEST_R2 -u AGENT_SESSIONS_LIVE \
  -u AGENT_SESSIONS_CAPTURE_NATIVE -u CANDIDATE_R2_LIVE_ACCEPTANCE \
  -u CAPTURE_V1_BINARY -u CAPTURE_V1_REPAIR_BINARY PYTHONDONTWRITEBYTECODE=1 \
  .analytics-venv/bin/python -m unittest discover -s tools/analytics -p 'test_*.py' -v
```

Final process proofs rebuilt from the integrated checkout (no deployment):

```sh
# In the same pinned environment, with live opt-ins unset:
CGO_ENABLED=0 go build -trimpath -buildvcs=true \
  -o /tmp/memory-resolutions-final/agent-sessions ./cmd/agent-sessions
python3 /tmp/memory-resolutions-final/prepare_repair_overlay.py
CGO_ENABLED=0 go build -trimpath -buildvcs=true \
  -overlay=/tmp/memory-resolutions-final/overlay.json \
  -o /tmp/memory-resolutions-final/agent-sessions-repair ./cmd/agent-sessions
CAPTURE_V1_BINARY=/tmp/memory-resolutions-final/agent-sessions \
  .analytics-venv/bin/python -m unittest discover -s tools/capture-delivery -p 'test_*.py' -v
CAPTURE_V1_BINARY=/tmp/memory-resolutions-final/agent-sessions \
CAPTURE_V1_REPAIR_BINARY=/tmp/memory-resolutions-final/agent-sessions-repair \
  .analytics-venv/bin/python -m unittest discover -s tools/capture-delivery -p 'test_*.py' -v
```

The scratch overlay reuses `capturefixture.Peer{LostACK:true}` as the backend HTTP client,
seeds a synthetic archive and records a content-free marker for runtime requests. It
cannot dial/DNS. Runtime credential lookup, launcher, coordinator, durable queue and
publication logic remain actual code. It is **not** the unmodified production binary
or actual R2. Generator, generated source, overlay and binaries are retained under
`/tmp/memory-resolutions-final/` for inspection; the assertive journey tests are committed.
Build VCS metadata may say modified because the handoff was dirty; no production/test
source diff existed at build time apart from the explicitly described scratch transport.

## Independent review and parent acceptance

| Slice | Initial review | Correction verification |
| --- | --- | --- |
| Analytics R2/R3/R6 | `92827ab8-d1ab-44c`: approved62deae2 | Parent inspected/integratedabc7ee8; final gates pass |
| Queue R4/R7 | `e16a15e2-127d-496`: production invariants sound; required first-pointer proof | `2913e2df-f51b-450`: approvedeb66ed7; parent integrated6b81b33 |
| Discovery R5/R8 | `128da02e-59e7-4a0`: R8 approved; R5 log correction required | `c600f9e8-898b-421`: approved79d6b13; parent integratedc8fdf7a |
| Lifecycle R1/R9/I1/I2 | `a879bb00-e792-481`: L1–L4 blockers, not accepted | `344cc1ad-0ac7-4b6`: approved4a07de0; parent integrated5401cc8 |
| Publication R10 | `9e9182f2-fc14-470`: approved9a8af9b | Parent inspected/integratede5393fc; final gates pass |

Detailed worker before/after commands and review reports remain in
`/tmp/memory-resolution-{analytics,queue,discovery,lifecycle,publication}/`.
No worker completion, single green test, or initial review was treated as acceptance.
All material corrections above were implemented, inspected, independently rechecked,
and covered by the final unchanged combined gates.

## Remaining boundary

**Not performed:** actual native agents/supervisors, live R2, installed Mac/BC-01 upgrade,
Linux full/native gates, hosted CI, capacity/retention acceptance, source inventory
reconciliation, archive retirement or legacy removal. No claim of universal efficiency,
power-loss certification or complete production qualification follows from these results.

The approved direction remains one durable capture path and one authoritative reader.
No generic job framework, second reader, speculative chunking/GC, legacy migration
engine or ambient credential chain was added. The remaining deployment/operating work
is still governed by the current handoff and controlled acceptance sequence.
