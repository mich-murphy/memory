# Independent enrichment refinement

**Status (2026-10-24):** normative refinement with an implemented, reviewed offline
candidate; not release-accepted or deployed. This refines the
[capture contract](session-capture-contract.md) for independent native launch evidence,
harness/memory snapshots and post-acceptance policy isolation. The queue-native lane,
immutable native inbox, context/policy-aware closed bundles, raw/optional delivery and
crash-safe repair, declared aliases and authorized raw/derived/WAL reads are now
implemented. Publication/reader/report contracts are v2; `capture-v1` command prefix
and namespace are unchanged. Enrichment remains independently opt-in.

Fresh unchanged development gates pass: check **622.40s**, test **460.36s**, all nine
**30s** fuzz targets (**285.31s** total), analytics **73 tests / 23.744s**, including
**11 actual-process journeys**. See [offline evidence](session-capture-offline-evidence.md)
and the [handoff ledger](session-capture-handoff.md) for exact checks and review scope.
The requirements below remain normative; passing offline checks does not establish
actual native-agent fields/trust, operating-resource/freshness, conditional R2 or
two-machine installed-path acceptance. No inventory, install, activation, cutover or
deletion is authorized by this document. Existing accepted evidence remains retained.

## Why an independent lane

A normal `Request.Transform` runs under the session capture lock; a normal runtime
source claim also excludes another claim for that session. Neither is an enrichment
lane. Selection, bounded reads, scrubbing, passive Git observations, bundle creation,
hashing and compression must happen without a raw-source claim/session lock. One
separate bounded worker is sufficient initially. Attachment takes a nonwaiting short
session lock and rebases onto the latest local source view. It never restores its old
basis or waits on network I/O.

Separate workers alone do not solve isolation. A newly forbidden optional artifact in
an ordered complete publication must not permanently block a later safe transcript.
No implementation may solve that by uploading forbidden bytes, pretending an old job
was published, deleting its payload/history, or weakening source-epoch regression
checks.

## Versioned deliverable selection

The earlier v1 protocol had unconditional object dependencies. The implemented
`session-publication/v2` / `session-reader/v2` candidate explicitly retains all
source-epoch coordinates while distinguishing selected content from an optional,
withheld artifact; these are not silent new semantics under v1. Existing v1 readers
must reject v2, not report empty success. No retired-schema compatibility is a prerequisite to this undeployed
candidate. The experimental command/namespace name `capture-v1` is independent of
wire version; do not silently reset existing bound stores/spools.

Implemented fields in `internal/publication/types.go` (exact wire semantics are in
[the publication reference](session-publication-v1.md)):

- `Source.Optional bool`, default false, allowed only for role `artifact`.
- `Source.Withheld *WithheldSource`, absent for selected sources.
- `WithheldSource` contains `SourceIDHash`, `SnapshotCommitment`, `Reason`.
- Reasons are closed, content-free codes (`policy`, `integrity`, `unavailable`).
- A withheld source has empty `SourceID`, `Path`, associations and Object. Its source
  identity is the SHA256 of the original UTF-8 SourceID; its epoch/revision and role
  remain explicit. No forbidden path, association, object hash/size or body is copied
  into a new remote manifest merely to report withholding.
- The zero Object is omitted (`omitzero`); selected sources still require a fully
  valid object and its complete verified dependency closure.
- `SnapshotCommitment` is SHA256 of UTF-8 Object fields in this exact order, separated
  by NUL: Key, SHA256, EncodedSHA256, Encoding, decimal Size, decimal EncodedSize.
  Valid object fields cannot contain NUL. This binds retained evidence without
  exposing the full descriptor. Private originals/descriptors remain in queue evidence.
- Effective source key is SHA256(SourceID) when selected, or SourceIDHash when
  withheld. Pair this with EpochID for uniqueness and transition checks.
- No coordinate can disappear or regress. At equal revision, object commitment
  cannot change. Required/optional status cannot be demoted for an existing epoch.
  Withholding does not establish completeness: enrichment coverage must be partial
  with an explicit missing/withheld reason.
- Reader/export never fetches withheld objects. Export metadata retains redacted
  coordinates; body records cover exactly selected pairs. Importer creates explicit
  withheld issues, no events or fake tails, and keeps normalization partial.

Only session-associated enrichment is optional. Main/subagent/tool-result evidence
and agent-resource primary bodies retain required dependency semantics. A source
label/association supplied by a hook is not privacy authorization.

Queue delivery must durably supersede a blocked composite with a higher-sequence
selection view built from the **latest** accepted local state. Earlier jobs are not
marked published; their safe accepted bytes/epochs remain represented or retained for
repair with explicit history. All local payloads remain charged. Rechecking optional
policy must not gate raw acceptance; delivery still checks every selected original
identity, including immediately before CAS. Recovery can reselect retained optional
bytes only after successful policy/integrity checks and never fabricates a new byte
capture time. Failure history is not erased.

## Snapshot closure and effective policy

`snapshot.BuildWithOptions(ctx, adapter, root, cwd, sessionKey, options)` is the
implemented explicit context/options entry point. `BuildContext` supports context-aware
legacy callers; `Build` remains its Background-context compatibility wrapper. The new
runtime must inject its actual policy/config,
not read globals belonging to the old installed collector. Reuse current selection:
user/project/ancestor declared patterns, managed settings, selected plugins, bounded
imports/scripts, memory, safe MCP subsets and Pi package/revision provenance. Home
scope is declared patterns, **not** recursive Home discovery.

Each selected original asset is independently authorized **before bytes are read**:
selection route and privacy/exclusion are different checks. Record original and
resolved identities, including generated subset inputs, for delivery rechecks after
source disappearance. Outside-agent-root access is allowed only through the trusted
selector, never by broadening the normal runtime path allowlist to Home. Preserve
credential/collector-state/binary protection and successful scrubbing; no raw fallback.

Decoded snapshot body is one self-contained deterministic TAR:

- `snapshot.json`: safe member coordinates, hashes/lengths, provenance, skips and
  scoped completeness.
- `harness.json`, optional `memory.json`.
- `assets/000000.bin`, etc.; every selected byte is included in this bundle.

Only generated regular member names; no original-path extraction, links or devices.
Normalize headers/order and validate complete internal closure. Deduplicate equal
safe bytes inside the bundle. No base64 blobs and no dependency on legacy global
blob storage. Separate volatile observation time from canonical content so equivalent
safe selections do not manufacture publications/capture freshness. Changed snapshots
retain distinct artifact epochs/bases. Keep current walk/file/content bounds, account
serialization/scrub memory and working disk explicitly, and reserve manifest
entry/metadata headroom for primary capture.

## Durable work and native evidence

Use the existing SQLite WAL/FULL authority. Work/result rows have bounded count,
stable typed basis, claim, attempts/retry time, software/contract, safe error,
completeness and attachment outcome. Source-based work is enqueued in the same short
acceptance transaction; unchanged polls and enrichment publications do not recursively
create work. A basis includes publication/sequence, exact source/epoch/revision/hash,
root/adapter coordinate, native launch ID and selector version. Stale results may be
retained against that basis but do not green a newer transcript. Shared coverage
reduction must preserve those distinctions.

Path hints remain coalescible metadata; they are not accepted launch bytes. The
implemented separate immutable native-evidence inbox must retain bounded records/bytes,
private files,
checked atomic fsync, stable retry identity, independent short lock, and deletion only
after durable queue adoption. Two accepted starts/resumes before drain both survive.
Metadata acceptance must not require an already-readable transcript.

Capture context in the transient native hook/extension, not the daemon's ancestry or
environment: event/source/cwd, native session/path coordinates, supported permission,
model/runtime context, scrubbed agent arguments and allowlisted nonsecret environment.
Allowlist payload fields; never copy prompt/transcript or arbitrary native JSON.
Pi context comes from its SDK handler and must follow the installed SDK docs. Missing
native fields/proof remain explicit. Disabled hooks first durably preserve the legacy
session exclusion, and emit no launch bytes, queue work or discovery wake.

## Required independent acceptance

The offline implementation and its focused/process tests now exercise the invariants
below; the latest ledger records repaired/re-reviewed failures and exact check scope.
This remains the acceptance checklist, not a claim that every scenario was run against
real native clients, real R2 or both installed machines.

Actual writer -> reader -> importer -> event index -> report must cover:

1. Same-session stalled snapshot while main appends publish normally.
2. Poisoned enrichment A, healthy B, durable retry/quarantine/restart without a new hook.
3. Stale result after newer transcript, rewrite retention, two launches plus retry.
4. Native acceptance before source existence/disappearance; honest later-time snapshots.
5. Remove synthetic originals/prepared/spool and restore every selected bundle byte.
6. Project/ancestor/import/plugin/managed policy, aliases, credentials and generated inputs.
7. Revocation before upload and before CAS: no forbidden body/metadata and no safe-main
   head-of-line deadlock; recovery preserves latest main and original artifact basis.
8. Crash points at inbox commit/adoption, enqueue, payload sync, result and attachment.
9. Quota/reserve/manifest headroom, incompressible data, fairness and cancellation.
10. Equivalent selection no-op and explicit incomplete normalization/coverage.

Then serial full unchanged development gates, followed separately by actual native,
resource, conditional-R2 and two-machine inventory/installed-path acceptance. None of
those external gates blocks implementing and testing the above offline.
