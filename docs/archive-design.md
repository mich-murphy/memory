# Append-only archive design

> **Status (2026-10-23):** Legacy v3 design, not the replacement contract. The guarantees and machine-wide recovery below describe the existing archive architecture, including historical retention promises; they are not requirements to carry forward. See the agreed canonical [session-capture contract](session-capture-contract.md): greenfield session-local capture with a durable spool, per-source progress and isolated retries. The opt-in [candidate](session-capture-phase3.md) implements a different v2 publication/reader path with [passing offline development gates](session-capture-offline-evidence.md), not release acceptance or default activation. Legacy repair/compatibility is not a rollout prerequisite.

## Authority and durability

The v3 bucket is sufficient to recover file generations, current selections, previous contents,
source provenance, sessions, launches, snapshots, roots and exclusions after complete SQLite loss.
A plan's exact canonical body is durable before any new dependency upload. The same body becomes
an immutable commit after dependencies exist with the required checksum identities and sizes.
Encoded-content hashes determine compressed object keys, avoiding assumptions about compression
output across library upgrades. No collector path deletes remote data, including legacy data.

A lost PUT acknowledgement is unknown, not proof of absence. Small records are reconciled by
exact read-back; ambiguous blob writes are streamed and checked. A late completion cannot replace
other bytes at its content-addressed key. Complete pending plans can be committed only when they
are the unique exact child of the current tip. Obsolete plans remain attributable provenance;
multiple eligible children fail closed. A partial plan cannot restore bytes never uploaded.

Every committed file generation remains reconstructible, including empty contents, append
intermediates, rewrites and folded representations. Database generations have unlimited archive
retention. This deliberately trades storage for no acknowledged-history loss. External deletion,
corruption, lifecycle expiry and bytes never uploaded are outside the collector's durability
control; detected inconsistency is an error, not a reason to overwrite or discard history.

## Cache, recovery and writers

A Syncer is one publication lifecycle: recover, discover/record launches, then call Run once.
A subsequent run requires a new Syncer; a second Run fails explicitly before mutation.
Restore may read committed generations after that lifecycle finishes.

SQLite is a bound disposable projection. Completion domain methods atomically import immutable
evidence, project sources/current content/history/session metadata, mark manifest work and record
stat observations. Close rolls back unfinished operations; it never commits hidden partial work.

A machine/destination lock is independent of the selectable SQLite directory. One writer per
machine ID is the normal contract; duplicated cross-host identities are configuration conflicts,
not a distributed merge service. Unique parent chains select current; timestamps do not elect a
winner. Touched streams are reconciled around publication to expose divergent writers.

A small checkpoint is only an acceleration hint. Matching confirmed-clean local/remote tokens
allow warm stat-unchanged files to avoid remote archive requests. Local then remote evidence is
marked dirty before mutations, and only a successful complete run marks both clean. Missing,
dirty, mismatched or ambiguous checkpoints force full paginated immutable enumeration. A restored
stale SQLite cache therefore cannot silently skip new remote generations. Enumeration validates
record schemas/identity/body hashes and referenced dependency HEAD identities/lengths. Previously
known missing immutable objects fail closed. Actual-body audits use streamed restore checksum
verification rather than downloading every historical transcript on every hook. A clean warm
checkpoint assumes archived objects have not been externally modified; it is not an audit.
Unchanged warm captures do not HEAD every old dependency. Use cold reconciliation for metadata
checks and streamed restore for actual-byte verification. External lifecycle expiry is unsafe
under the v3 history promise; this is not a prohibition on the separately authorized controlled
reset after replacement acceptance or on explicit new-contract retention.

## Capture and privacy

Aliases are grouped before lifecycle preparation. Equal decoded contents may share authority;
proven append prefixes may choose the longer source. Divergence is reported without arbitrary
mtime/response-order selection. Unrelated keys retain bounded upload parallelism.

Large plain and compressed source inputs stream through private staging. Cryptographic prefix
validation preserves tail-only upload efficiency. Hashes describe captured staged bytes, not an
earlier scan. Complete-line deferral is distinct from an actual empty file. Discovery errors are
observable and incomplete discovery cannot publish deletions. Source aliases and original export
paths ensure a transient SQLite backup path never becomes the logical archive identity.

Central capture policy checks all intermediate symlink identities before reads. Recognised config
schemas use the same scrubber across raw capture and snapshots; known credentials and collector
locations remain excluded. The intentionally scrubbed primary Claude config export is a narrow
explicit exception, not a generic state-directory bypass. Exclusions are recovered and durably
published before new capture, and are monotonic. Launch envelopes are durable independently of
session manifests. Snapshot time is actual capture time, without a pre-session provenance claim.

## Limits and non-goals

Under this legacy v3 contract, archive growth is unbounded by design: immutable plans/commits, retained file pieces, database
generations and captured snapshot sets accumulate. Content addressing shares identical blobs,
but append history, folds and rewrites may retain multiple representations. There is no automatic
remote migration of legacy namespaces.

No automatic archive GC, remote leases, bucket versioning or conditional multipart completion
assumptions. Metadata records and recognised config parsing are bounded; large source bodies and
restore use streaming. Full cache-loss recovery is proportional to historical metadata. SQL
cursors bound descriptor replay to one body at a time, and LIST pages to 1000 identities.
Stream lists and session launch/snapshot/version projections still grow with cardinality;
16 MiB metadata limits are per record, not a fixed bound on all machine metadata. Warm
idle request counts are independent of archive history; changed streams may paginate their
metadata but do not HEAD every historical descriptor. Source mutation without filesystem snapshot
cooperation cannot guarantee a point-in-time view; the guarantee is the integrity of captured
bytes. Losing the machine-ID file requires explicit operator identity selection, never hostname
matching. The complete wire contract is [storage-layout.md](storage-layout.md).
