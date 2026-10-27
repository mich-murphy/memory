# Session publication and reader v2

The filename is retained for existing documentation links. The repository contract
requires v2; it does not silently reinterpret v1 archives or reset their binding.
The new explicit selection semantics support privacy-safe optional-artifact isolation.
See [independent enrichment](session-enrichment-contract.md) for the implemented
opt-in producer lane and normative isolation requirements. The command/namespace name `capture-v1` is not the wire
contract version.

**Implementation status:** implemented and reviewed v2 candidate with
queue/CLI/enrichment/backend/analytics integration. Unchanged development gates and
all eleven actual-process journeys passed at the historical 2026-10-24 checkpoint;
see [dated offline evidence](session-capture-offline-evidence.md) for exact results and
[queue/CLI/analytics checkpoint](session-capture-implementation.md) for integration.
The handoff's [current scorecard](session-capture-handoff.md#current-session-checkpoint)
records later failures, private unqualified fixes, historical experimental deployments
and unknown current service health. No current full-source qualification is implied.
This library alone is not deployment, R2 endpoint acceptance, a scheduler or complete
release acceptance. The [session capture contract](session-capture-contract.md)
remains authoritative.

## Namespace and identity

`internal/publication` implements `session-publication/v2`; the consumer export
contract is independently `session-reader/v2`. There is no legacy interpretation.
The prefix is `capture-v1`, with the create-only archive binding at
`capture-v1/archive.json`:

```json
{"contract":"session-publication/v2","archive_id":"operator-chosen-stable-id"}
```

Every session Identity has `archive_id`, `machine_id`, `agent`, `session_id`.
All must be nonempty, valid UTF-8. Lossy JSON replacement of unsupported identities
or provenance is rejected explicitly. Each component below is lowercase hexadecimal
of its UTF-8 bytes, not hostname substitution or URL escaping:

```
capture-v1/archives/<archive>/machines/<machine>/agents/<agent>/sessions/<session>/
  current.json
  manifests/<sha256-of-exact-manifest-json>.json
  objects/<sha256-of-encoded-object>.zst
```

`Identity.BaseKey()` returns the session directory without trailing slash;
`CurrentKey()` appends `/current.json`. Object keys must match this exact session
namespace. Cross-session or retired namespace dependencies are rejected.
Changing the archive descriptor to another identity is forbidden. New archive
instances need a fresh backend namespace rather than replacement of this binding.

## Exact JSON model

The exported Go structs in `internal/publication/types.go` are the wire model.
Every field uses explicit snake_case JSON tags. Ordinary fields are not omitted;
`optional` and `withheld` are omitted at their default values, and a zero withheld
`object` is omitted. Nil slices
may encode as `null`; readers must accept either null or empty arrays. There are
no base64 bodies. Integers are JSON integers (Python consumers must preserve integer
precision; nanosecond timestamps are not floating-point seconds).

- Manifest: `contract`, `identity`, `publication_id`, `sequence`, `observed_at_ns`,
  `captured_at_ns`, `published_at_ns`, `coverage`, `sources`.
- Coverage: `discovery`, `enrichment`, `missing` (strings). Both status strings are
  required. Recommended producer values are `complete`, `partial`, `unknown`;
  ordinary views preserve nonempty producer statuses rather than enforcing an enum.
  A withheld view requires enrichment `partial` and missing marker `source-withheld`.
  A complete
  dependency closure does **not** imply complete discovery or enrichment.
- Source: `source_id`, `epoch_id`, `role`, `path`, `state`, `revision`,
  `observed_at_ns`, `captured_at_ns`, `object`, `associations` (strings), optional
  `optional` (boolean), and optional `withheld` (object). Effective source/epoch
  pairs are unique. Selected identity, role, state and positive revision are required. `path` is provenance only, never an export destination. Associations
  are producer-declared provenance, not dependencies automatically resolved by the
  reader. The capture producer must explicitly include associated artifacts as
  sources, or declare missing coverage; the library does not discover artifacts.
- Object: `key`, `encoding` (only `zstd`), `sha256`, `encoded_sha256`, `size`,
  `encoded_size`. Hashes are lowercase hex SHA-256; sizes count exact bytes.
- Current: `contract`, `identity`, `publication_id`, `manifest_key`,
  `manifest_sha256`, `sequence`. Its manifest reference is content addressed and
  its identity, sequence, and publication ID must exactly match the manifest.
- Export: `contract` (`session-reader/v2`), `manifest`, `bodies`.
- Body: `source_id`, `epoch_id`, `path`, `sha256`, `size`. Body paths are relative
  `bodies/0000.bin` etc., not original source paths.

### Optional withholding

`optional=true` is allowed only for role `artifact`. A selected source has no
`withheld` field and still requires its full verified object. A withheld source has
empty `source_id`, `path`, associations and Object, with:

```json
{"source_id_hash":"<64 lowercase hex>","snapshot_commitment":"<64 lowercase hex>","reason":"policy"}
```

Closed reasons are `policy`, `integrity`, `unavailable`. No original source path,
name, associations or object descriptor/body is republished to explain withholding.
Original evidence remains private in the producer queue. Effective identity is
SHA256(UTF-8 source_id) for selected content, or `source_id_hash` for withheld content.
The snapshot commitment hashes the Object's UTF-8 Key, SHA256, EncodedSHA256,
Encoding, decimal Size and decimal EncodedSize, in that order separated by NUL.
At equal revision, the commitment cannot change across selection transitions.
Required sources cannot become optional within their retained epoch.

The manifest still lists every retained coordinate. Withholding is not missing
required evidence, successful-empty capture, deletion or a completeness claim.
Readers/fences fetch only selected objects; body records cover exactly selected
pairs. A withheld-only view is explicitly partial and has no invented bodies/events.

Observed/captured/published timestamps are signed int64 Unix nanoseconds,
nonnegative (zero is allowed for unknown fixture timestamps); wall clocks never
elect a winner. Sequence and revision are positive integers no larger than signed
int64 maximum (Go stores them in uint64; Python/DuckDB consumers use BIGINT). JSON decoding rejects
unknown fields, trailing values, unsupported contracts, malformed digests and
metadata larger than 8 MiB. A manifest has at most 1024 sources. JSON duplicate-key
rejection is not implemented: producers must emit unique keys (the Go writer does).

## Backend and writer API

```go
type Backend interface {
    Open(context.Context, string) (io.ReadCloser, error)
    PutFile(context.Context, string, string) error
    Current(context.Context, string) ([]byte, string, error)
    CompareAndSwap(context.Context, string, string, []byte) error
}
```

`Open`/`Current` return `ErrNotFound` for absence. `Current` returns an opaque token.
CAS with empty token means create-if-absent; other tokens mean exact replacement.
Precondition failure is `ErrConflict`. `ErrDurability` identifies a failed required
sync and must never be converted into success by visible read-back. It supports
`errors.Is`, including when joined with the underlying I/O error.
`PutFile(ctx,key,filename)` is immutable: identical content is idempotent, different
content at the same key conflicts.
Implementations must preserve these semantics; a HEAD followed by unguarded PUT is
not a valid CAS backend. The current implementation is `NewLocal(root) *Local`.
No R2 backend is implemented in this package. `internal/publicationr2` supplies the
SDK adapter; `internal/capturebackend` constructs explicitly selected Local/R2
backends. The CLI supports configured init/export and lazy remote runtime delivery
without making credentials/network a raw-capture startup prerequisite. Tests use
injected nonnetwork HTTP protocol peers; actual R2 semantics remain unverified.
See [backend construction](session-capture-backends.md).

An optional `DurabilityFence` interface exposes `Sync(context.Context, string) error`.
Backends where visibility alone does not prove durability implement this interface;
wrappers around them must forward it. Backends without it promise durable visibility
after successful writes. Local implements the fence under its writer lock, syncing
the existing file and every required parent entry even after reopening the store.

`ValidateManifest(Manifest) error` validates the complete wire shape without backend
access; producers can reject invalid generated manifests before durable acceptance.
It does not verify dependency existence/content, archive binding, or source ordering.

`WriteArchive(ctx,backend,Archive) error` creates or confirms the identical archive;
`ReadArchive(ctx,backend) (Archive,error)` verifies the supported archive contract.

Capture uploads compressed objects first. Then
`Publish(ctx,backend,manifest,expectedToken) (Current,error)`:

1. Validate archive/session binding and complete manifest.
2. Check selected current and caller token. An exact stable publication retry is
   recognized even with an obsolete token. Otherwise sequence must increase and
   publication ID must differ. Every previous source/epoch must remain present;
   revisions cannot regress and equal revision cannot change its object commitment.
   Effective source identities bridge selected and redacted withheld coordinates;
   required/optional identity cannot change to demote a required epoch.
3. Stream-verify all selected dependencies; upload immutable complete manifest.
4. CAS current. A required sync failure (`ErrDurability`) remains an error even
   if current is visible. For ambiguous transport/lost-ack errors, read back and
   accept only the exact selected Current, applying any backend durability fence.
   An unsuccessful read-back does not authorize blind replacement or prove an
   earlier request cannot still commit. Caller retains pending work.

An already-current retry verifies dependencies and applies the backend fence to
current, manifest, archive binding, and source objects before success. Confirming an
already-existing archive also fences its file/parent entries. These retry paths do
not substitute visibility for durability.

The producer owns epoch/append proof and stable job identity. This library cannot
prove a new higher-revision object is an append, nor infer privacy eligibility.
It does not implement remote GC, deletion, catalog/list discovery, retention,
publication fairness, or operation time budgets. Callers provide contexts and
bounded scheduling. Unselected objects/manifests may remain after failures.

## Authoritative reader and pinned export

`Read(ctx,backend,identity) (Manifest,error)` selects current once, checks the
manifest digest/binding, and verifies every selected dependency. It never replays
history. Missing/corrupt required or selected dependencies return errors, not
successful-empty sessions. Explicit optional withholding remains visible and partial.

`ExportSession(ctx,backend,identity,dest) (Export,error)` selects once and writes
that pinned manifest's exact selected decoded source bytes. Withheld entries have
metadata only and cannot cause object access. Destination must not exist;
its parent must be trusted/private. It creates 0700 directories, 0600 files, syncs
bodies, and writes `metadata.json` last via a temporary file and rename. A reader
must treat absent metadata as incomplete. Errors may leave a private partial
export; callers own cleanup. The metadata/body publication does not change if
current advances during export. Consumers must use the returned/exported body
paths rather than resolving current independently.

Objects stream through a single-worker zstd decoder with a 64 MiB memory/window
limit. Encoded and decoded byte counts and hashes are verified, with at most the
declared sizes plus one byte read/output for overflow detection. There is no fixed
total body-size quota or disk-space reservation; streaming bounds memory, not disk
usage or elapsed time. A declared size near int64 overflow is rejected. Producers
must use compatible zstd windows. Export errors must not be treated as valid bodies.

## Local safety and durability scope

Short logical keys use `<root>/data/<key>`. A key over 768 bytes, or with any
component over 240 bytes, uses `<root>/long-data/<sha256(full-logical-key)>` instead.
The namespaces are disjoint: short logical `long-data/...` remains under `data/`.
This physical mapping leaves wire keys/identity unchanged and avoids filesystem
component limits for full-root-qualified Codex and other valid identities. Every
read/write/CAS/fence uses the same mapping and actual physical parent chain. R2's
1024-byte logical key limit is still separate; Local accepting a longer logical key
does not establish remote compatibility. `<root>/.cas.lock` is outside the object
namespace. All CAS and immutable writes use the same filesystem flock, including
independent processes. Keys reject absolute paths, backslashes, dot traversal,
NULs and noncanonical paths. Configured roots and existing object path components
reject symlinks. `os.Root` additionally confines resolution. Root ancestors are
trusted and may include platform aliases such as macOS `/var`. Untrusted concurrent
filesystem mutation, hostile hard links, and hostile root replacement are not
supported; use a private owned root. Existing directory ownership/permissions are
not automatically rewritten or audited.

Writes check file Sync and Close, atomic rename, destination-directory Sync, and
parent-directory Sync. Local also fences all ancestors through the filesystem root,
so a prior failed directory-creation sync cannot be bypassed by a later retry that
finds that directory already visible. Identical immutable `PutFile` retries re-sync
the file and directory chain rather than returning success on matching bytes alone.
Required sync errors are tagged `ErrDurability`; publication/archive reconciliation
preserves them. A private per-Local sync injection seam tests post-rename failures
and retry behavior, including reopening the store before retry.

This establishes the ordinary OS/filesystem fsync boundary, not a claim about faulty
storage, network filesystems or physically power-cut-tested hardware. No
crash-recovery scavenger is implemented. Temporary unselected files can remain after
process death; do not delete lock files to fence writers.

## Focused evidence and remaining acceptance

**Historical integrated checkpoint — 2026-10-24:** passed check **622.40s**, test **460.36s**, nine unchanged
**30s** fuzz targets (**285.31s** total), analytics **73 tests / 23.744s**, including
all **11 process journeys**. [Final offline evidence](session-capture-offline-evidence.md)
records exact coverage/CRAP and limitations. Native/live/two-machine and representative
operating-resource acceptance remain outstanding.

### Historical focused library evidence (2026-10-23)

These package-only results precede the historical integrated gates; the commands and
dated observations are retained, not instructions to restart completed integration.
Pinned environment commands, run serially with live opt-ins unset:

```
nix develop --command golangci-lint run ./internal/publication/...
nix develop --command env -u AGENT_SESSIONS_TEST_R2 -u AGENT_SESSIONS_LIVE \
  CGO_ENABLED=1 go test -race -shuffle=on ./internal/publication -count=1
```

Both passed for the durability bugfix: lint zero issues; final focused tests 4.298s. Tests exercise actual
cross-process first-create CAS, lost acknowledgement, stable retries, stale
sequence/token, retained epochs, missing-manifest retry, archive binding, immutable collisions, symlink/key confinement,
unsupported archive/manifest contracts, metadata/source limits, missing/corrupt
objects, exact non-UTF-8 body bytes and pinned export racing an update. Injected
post-rename directory-sync failures cover current, archive, and immutable writes:
first attempts remain errors despite visible bytes, failed retries remain errors,
and successful retries must actually sync both file and directory.

Subsequent local-key implementation: race/shuffle **5.947s**, **83.4%**, lint zero,
CRAP **26.79**. Independent read-only audit passed with separate whole-package
race/shuffle **6.136s**. Versioned selection package/R2 tests then passed **6.541s /
2.192s**, coverage **85.0% / 93.1%**, lint zero, scoped CRAP **27.24**. These checks
prove focused behavior, not integrated consumer acceptance. Logs/profiles:
`/tmp/publication-long-keys.cover`, `/tmp/publication-v2-tests.log`,
`/tmp/publication-v2-cover.out`.

At that focused checkpoint, full repository tests/check/coverage/fuzz and integrated
consumer acceptance had not yet been rerun. They subsequently passed at the dated
checkpoint above, including synthetic process-crash/restart journeys; this is not
physical power-cut evidence. Later failures remain open. Historical native/R2 and
experimental host operations occurred, but complete installed two-machine operating
acceptance and final cutover remain outstanding. Passing this package alone does not establish the
complete release journey; the [handoff ledger](session-capture-handoff.md) records
integrated acceptance and the remaining external gates.
