# Isolated candidate live-R2 acceptance

These tests exercise the candidate, not installed 0.5.0 or legacy ingestion. They
change no shared runtime, service, config, policy or source. Offline rehearsal is
not R2-semantics evidence. The coordinator deliberately ran this acceptance in new
synthetic namespaces; dated results are below and in the
[delivery ledger](../../docs/session-capture-delivery.md). Invocation remains explicit:
it is not part of `make check` or normal live flags.

## Exact isolation and binding semantics

**An ArchiveID alone does not isolate initialization.** Candidate production uses the
bucket-global `capture-v1/archive.json`. This test must never access or alter it.
The production factory has no arbitrary physical-prefix option. Rather than introduce
one in shared runtime, `acceptanceNamespace` is an **acceptance-only Backend adapter**:

- Generate `candidate-live-acceptance-<32 lowercase random hex digits>` from 128 random
  bits on **every invocation**; no caller-supplied/resumable namespace.
- Allow backend operations only beneath
  `capture-v1/archives/<UTF-8 archive_id encoded as lowercase hex>/`.
- Map the single logical `publication.ArchiveKey` to the reserved synthetic identity's
  `current.json`, i.e.
  `<prefix>machines/<hex synthetic-machine>/agents/<hex pi>/sessions/<hex __archive_binding__>/current.json`.
  This contains an archive binding, **not** a session publication; it is not exported.
- All ordinary writer/reader keys remain canonical candidate keys, unmodified, under
  that same prefix. Probe uses session `__conditional_probe__`; publications use `A`, `B`.
- Archive binding is create-only through the actual SDK's `If-None-Match: *`.
  `publication.WriteArchive` verifies any conflicting/ambiguous result and rejects a
  differently initialized binding. No shared/global binding is read, rewritten or reset.
- Confinement applies to reads and writes and is checked before calling the real backend.
  LIST is restricted to the exact archive/machine/agent session common-prefix subtree;
  its budget is synchronized across CURRENT workers. There is no delete, copy,
  multipart, legacy path or shared blob dependency.

Thus this establishes (when deliberately run) **isolated synthetic namespace** factory
→ actual R2 transport → queue → reader → analytics acceptance. It does **not** establish
production CLI prefix support, installed runtime/global-binding acceptance, native
hooks, two-machine coverage, cutover or safe deletion. The SDK does its normal signing
on the mapped canonical keys; signed requests are not rewritten by an HTTP proxy.

## Coverage and bounds

The live route constructs `capturebackend.Open` with endpoint/bucket and the **named**
`R2_ACCESS_KEY_ID` / `R2_SECRET_ACCESS_KEY` references. It uses the factory's actual SDK,
constrained HTTPS endpoint, static credentials, no redirects/default credential chain
or SDK retries. No mock transport participates in the live route.

Assertions cover:

1. Native conditional create, duplicate-create conflict, successful ETag update,
   stale-token conflict, exact successful bytes retained after conflict.
2. Synthetic Pi transcript → durable queue → close/reopen queue → publication.
3. **Injected caller-boundary lost ACK after a real successful current-reference CAS**;
   writer reconciles by stable publication identity. This is not a natural outage or
   transport-level lost-ACK experiment.
4. Captured append for A, injected failure **before** A's next current CAS, previous
   successful A still readable, then healthy B publishes/exports while A remains pending.
5. Retry A without another capture; remove only generated source files; authoritative
   export asserts exact raw bytes, publication/session/source/epoch/role identities.
6. Real Python importer, event index and report: initial A then updated A + healthy B,
   exact identity/epoch coordinates and user/assistant counts, zero normalization issues,
   idempotent repeated imports/event IDs, publication identity and coverage in report.
7. Actual `StartAfter`/delimiter pagination through authoritative current-root discovery,
   with page size one across all three agents. Valid A/B coordinates remain discoverable
   while the reserved archive-binding/probe references produce exactly two declared
   `current-unreadable` failures. Those reserved synthetic references are intentionally
   not session publications; they are never hidden or treated as successful empty heads.

Analytics dependency preflight happens **before the first remote request**. Child
analytics processes receive no destination credentials. Diagnostics do not stringify
SDK errors, response bodies, endpoint/bucket values or secrets. Test logs show only the
new synthetic archive identity/prefix, assertions and operation count.

Maximum **160 Backend operations**, therefore **at most 320 SDK requests**:
`PutFile` may issue one conditional PUT plus one exact-byte reconciliation GET;
other Backend operations issue at most one SDK request, with SDK retries disabled.
This is not a 160-network-request claim. Factory HTTP-client wrapping caps every
non-2xx body at **64 KiB plus one overflow probe byte before SDK deserialization**;
overflow closes the response and fails with a fixed content-free error. Successful
object responses remain streaming and are not truncated by this error-only cap.
Other bounds: **64 KiB per immutable payload**, **1 MiB spool**, **3-minute journey
context**, factory **30-second operations**, **4-minute Go test process**. Fixture normally uses 54 backend operations in the offline rehearsal, three
small source snapshots and three manifests. R2 namespace contains only a tiny bounded
set of bindings/probe/current/object/manifest evidence (at most 160 written objects,
far fewer normally); no automatic GC. Failed runs also leave their bounded evidence.
All remote objects are intentionally retained; there is **no remote cleanup command**.
Local generated sources, spool, exports and analytics DB live only in Go `t.TempDir`;
keep `-v` output externally if a permanent namespace/evidence record is desired.

## Offline review / rehearsal (safe to execute)

From `/Users/mm/dev/memory`, pinned Nix, existing analytics environment (no install):

```sh
nix develop --command bash -c '
  unset AGENT_SESSIONS_TEST_R2 AGENT_SESSIONS_LIVE CANDIDATE_R2_LIVE_ACCEPTANCE
  PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s tools/candidate-live-r2 -p "test_*.py" -v &&
  CANDIDATE_R2_ANALYTICS_PYTHON="$PWD/.analytics-venv/bin/python" CGO_ENABLED=1 \
    go test -race -shuffle=on ./internal/publicationr2 -run "TestCandidate" -count=1 -v
'
```

Offline uses the actual Local candidate backend as peer for the same complete journey;
this is not a claim of offline actual-SDK semantics. Existing SDK peer tests remain
unchanged. The live test skips unless its distinct explicit opt-in is set.

## Deliberate live command — review each new invocation

This consumes the existing private config as **data**, never as shell code. The launcher
requires a current-user-owned regular non-symlink file with no group/world permissions
and at most 64 KiB. It accepts the four named R2 assignments, optional `export`,
whitespace around `=`, and whole single/double quotes; unrelated keys are ignored.
It rejects duplicate required keys, missing/empty values, shell expansion/escaping and
ambiguous whitespace. It does not print values or amend the file. File values explicitly
supply the four named settings, rather than accidentally selecting inherited overrides.

```sh
cd /Users/mm/dev/memory
nix develop --command bash -c '
  set +x
  unset AGENT_SESSIONS_TEST_R2 AGENT_SESSIONS_LIVE CANDIDATE_R2_LIVE_ACCEPTANCE
  exec python3 tools/candidate-live-r2/run.py \
    --config-env "$HOME/.config/agent-sessions/config.env" \
    --analytics-python "$PWD/.analytics-venv/bin/python" \
    --live-new-synthetic-namespace-only
'
```

No `source`, `env` dump, credential arguments, default AWS lookup, real-source reads,
service install, remote deletion or old-archive initialization occurs. The launcher
also clears inherited legacy live flags and selects **only**
`^TestCandidateR2LiveAcceptance$`, `-count=1` (never cached).
Do not turn this into a generic full-suite invocation with credentials present.

## Implemented vs. evidence

Deliberate coordinator evidence — 2026-10-24 AEDT:

- Original complete isolated R2 journey: PASS, 4.60s, 54/160 backend operations.
- Current-root discovery extension: PASS, 5.48s journey / 5.891s package,
  **70/160 backend operations** (at most 140 SDK requests under the conservative
  two-request bound). It used a fresh retained synthetic archive
  `candidate-live-acceptance-04d7f15e21d0069dfa48b5cde673755d` and passed the new
  exact partial-failure/head/pagination assertions. Log:
  `/tmp/memory-delivery.SW23JL/candidate-real-r2-current-heads.log`.
- No global binding access, production ingestion, service installation, old archive
  deletion or installed-path acceptance is implied. No remote cleanup occurred.

Historical implementation-agent checks below predate those deliberate runs; their
“not run” statements describe that earlier ownership window, not current live status.

Implemented-check evidence (pinned Nix, live opt-ins unset):

- Launcher unit tests: **5 PASS**, 0.010s; all credentials synthetic, child mocked.
- Whole `internal/publicationr2` package, `-race -shuffle=on -count=1`, including
  complete Local→queue→reader→real analytics rehearsal: **PASS**, 4.028s.
- Final focused `-run TestCandidate -race -shuffle=on -count=1 -v`: **PASS**, 2.543s,
  including differently initialized binding fail-closed/global-sentinel protection;
  complete consumer journey used **54/160** backend operations; live test **SKIPPED**.
- Focused `golangci-lint run ./internal/publicationr2`: **PASS**, zero issues.
  `git diff --check`: **PASS**. Initial formatting/string-comparison lint findings
  were fixed without changing gates.

Pre-live safety follow-up (still **no remote run**): reviewer d626 identified that
SDK error deserialization could buffer unbounded non-2xx bodies. Fixed at the
**production factory** HTTP-client seam (`internal/capturebackend/error_response.go`),
with a one-line `backend.go` wiring change covering both default and injected clients.
Successful streams are passed through untouched. New focused tests prove exactly
65,537 maximum original bytes read per oversized response, original-body closure,
fixed overflow/read/close diagnostics, ordinary auth/not-found/conflict classification,
exact-limit acceptance, no successful-stream truncation, and actual SDK GET/PUT paths
including the two-request immutable-PUT reconciliation path (not an SDK retry).

Fresh post-fix pinned-Nix checks, live flags unset:

- Race/shuffle whole `capturebackend`: **PASS**, 1.626s.
- Race/shuffle whole `publicationr2`, including complete offline reader/analytics
  rehearsal: **PASS**, 2.993s; live test skipped.
- Focused lint across both packages: **PASS**, zero issues.
- Launcher unit tests: **5 PASS**, 0.004s. `git diff --check`: **PASS**.

Status-preservation correction: the first cap implementation above discarded HTTP
status on overflow/read/close failure, incorrectly turning malformed 401/403 responses
into generic failures. That regression is fixed, not accepted as changed behavior.
The production wrapper now returns **a sanitized status-only response plus a fixed
Smithy ResponseError**, after closing the original body. It retains no original body,
headers, request or status text. The non-nil response is essential: Smithy otherwise
creates an outer status-zero ResponseError which shadows the inner status in
`errors.As`. It is still an explicit failed operation, never a successful synthetic
response. Tests verify `errors.As` through the actual SDK, all three body failure modes,
401/403 destination pause (no immutable PUT reconciliation), 404 not-found and 409/412
conflict semantics (including bounded PUT reconciliation), private-field removal,
and unchanged byte/closure/request bounds. The old generic-403 assertion was corrected.

Fresh post-correction pinned-Nix results: both-package lint **PASS**, zero issues;
whole race/shuffle `capturebackend` **PASS 1.726s**, whole race/shuffle `publicationr2`
with complete offline reader/analytics rehearsal **PASS 4.020s**; live test skipped;
`git diff --check` **PASS**. Nix emitted an ignored evaluation-cache SQLite busy
warning, then completed successfully. An initial new status test run failed and exposed
the SDK's outer status-zero wrapping; the sanitized response/error pair fixed it
without weakening the status assertions or gates.

No private config/credentials were read and no remote, native, host/install, full
repository test/check/fuzz or full analytics suite was run. Results are reported to the
coordinator for the shared handoff ledger; this isolated ownership does not edit that
shared file. Full/native/live gates are **not** implied by focused checks. Update the
shared handoff after the reviewed live command actually runs, including its printed
synthetic namespace, result, bounds and any retained failure evidence.
