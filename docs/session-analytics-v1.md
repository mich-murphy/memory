# Replacement analytics projection (offline v2 candidate)

This filename is retained for existing links. The current undeployed consumer is
v2-only; prior v1 gate results in the handoff remain historical evidence, not
proof of this candidate or a claim that a database migration occurred.

**Current checkpoint (2026-10-24):** actual writer → authoritative v2 reader → importer
→ event index → report and opt-in server/query callers are implemented and reviewed
offline. Fresh complete analytics discovery passes **73 tests / 23.744s**, without
skips/warnings, including all **11 actual-process journeys**. Full unchanged check
(**622.40s**), test (**460.36s**) and nine **30s** fuzz targets (**285.31s** total)
also pass. See [final offline evidence](session-capture-offline-evidence.md). These
are offline development results, not default activation, deployment, real native/R2
acceptance, representative resource/freshness acceptance or two-machine cutover.

`tools/analytics/publications.py` consumes **only** the authoritative reader's
`session-reader/v2` export: `metadata.json` plus relative decoded body files. It
never lists remote storage, selects current publications, decompresses objects, or
reconstructs history. The Go reader is responsible for storage dependency closure
and encoded-object verification; this consumer additionally checks exact **selected effective source /
epoch** pair coverage, pinned decoded hashes and lengths, then streams each body
into its own private content store before indexing it. Declared array fields accept
both JSON null (Go nil slices) and empty arrays; missing fields remain errors.

This is an independent DuckDB projection. The **existing dashboard and local query
entry points now support explicit `--source publications` mode**; their default
remains legacy. It is not connected to the legacy analysis/classifier pipeline or old
research databases. Existing research is not migrated or relabeled. GET requests do
not refresh/import, read original source paths, contact storage, or execute classifiers.

## API and commands

From `tools/analytics` on Python's module path:

- `import_export(export_dir, db_path, *, reset=False)` returns a count/status dict.
  Success is `status='imported'` or `'unchanged'`. Errors raise `ImportFailure` with
  a content-free reason code. Unsupported wire versions fail before creating or
  opening the database. Other malformed metadata also fails before DB writes.
- `report(db_path, *, now_ns=None, stale_after_ns=86400 * 10**9)` returns a
  JSON-safe report with actual indexed-event / issue counts, exact source roles,
  source coordinates, per-session coverage, integer nanosecond stage timestamps,
  capture age, stale flags, failures, and report generation (`served_at_ns`). This
  served timestamp is **not** a claim that a dashboard served this projection.
- `export_session(binary, store, archive, machine, agent, session, output, *, config=None)` invokes
  the built authoritative reader with an argv list and suppressed output, never a
  shell. Export has a 300-second subprocess budget; timeout kills/waits for the
  reader and raises the content-free `reader_export_timeout` diagnostic. The caller
  owns a private output directory and handles export failures. Exactly one of `store`
  or `config` is required; for configured backends pass `store=None`. Credentials are
  never supplied in subprocess argv: the Go backend resolves explicit named references.

```sh
python tools/analytics/publications.py import /private/reader-export \
  --database /private/analytics/publications.duckdb
python tools/analytics/publications.py report \
  --database /private/analytics/publications.duckdb
```

The same existing caller can serve or query this projection explicitly:

```sh
python tools/analytics/serve.py --source publications \
  --database /private/analytics/publications.duckdb
python tools/analytics/query.py --source publications \
  --database /private/analytics/publications.duckdb --sql 'SELECT count(*) FROM events'
```

Publication mode defaults to `.analytics/publications.duckdb`, not the historical
serve default `.analytics/events.duckdb` or query default `.analytics/sessions.duckdb`. A legacy `--report` is refused in publication serve mode.
The server validates the binding read-only before binding loopback; report, trace,
issue and raw-line routes use locally owned selected evidence only. Withheld coordinates
and opaque artifacts have no raw-line fallback, including retained old cache bodies.
Known-zero publication timestamps render as unknown; exact nanosecond values and large
revision/sequence fields are strings at the browser boundary. Classification and old
research review are explicitly unavailable, not misleading successful-empty pages.
Historical browser review state remains separate and untouched.

The dedicated database schema is explicitly `publication_binding.version = 2`;
the report contract is `session-analytics-report/v2`. There is no migration from
v1 projection databases, pre-watermark development databases or legacy schemas. An existing
binding with another version, or a failures table lacking that sequence column,
is rejected as `unsupported_database`; use a new dedicated projection path.
Existing databases are checked read-only before any writable open. In particular,
an old dedicated v1 projection is refused without mutation, even with `--reset`.
`--reset` does not bypass schema validation or migrate databases.

Use a **new database path**. Existing non-projection databases are rejected even
with `--reset`. Archive identity is bound on the first accepted import/attempt;
other archives require explicit `--reset`, which clears this dedicated projection
only. Raw body files in `<database>.bodies/` are retained, including unused files;
there is no automatic garbage collection. The database and export directories
must remain private and trusted; no hostile concurrent filesystem mutation is
supported. DB file and owned body modes are 0600, created body directories 0700.

## Projection semantics and limits

One import replaces only its session inside a DuckDB transaction. A failed known
publication attempt records a safe error/attempt count and does not advance the
previous success's publication or freshness. Each session's failure target is a
monotonic sequence watermark: an older failed retry increments outstanding
`attempts` but preserves the newer target's publication ID, sequence, reason and
failure timestamp. `attempts` counts all failed attempts since the last cleared
failure; `failed_at_ns` describes the retained target's latest failure, not an
older retry. Equal-sequence failed attempts may update the target diagnostic.
Only a successful import of that corresponding publication (same sequence and ID)
or a strictly newer sequence clears the failure. In particular, successful
idempotent revalidation of P1 cannot erase an outstanding P2 failure or mark P1
fresh. A healthy session can still import.
Invalid metadata rejected before identity validation, reader invocation failures,
and unavailable Python dependencies are caller-visible failures, not session rows.
No archive-wide discovery or successful-empty-archive inference is implemented.

Publication sequence prevents stale replacement; equal sequence with a different
manifest is a conflict. Repeated identical import verifies raw bodies again but
does not advance successful freshness. The same publication ID cannot acquire a
different manifest. Explicit reset clears the projection before its replacement
import, so reset is an intentional invalidation, not a rollback-safe refresh.

Trace IDs hash archive/machine/agent/session/effective-source/epoch coordinates.
The effective source key is lowercase SHA256 of the UTF-8 SourceID for selected
sources, or `Withheld.SourceIDHash` for withheld sources, matching the Go helper.
Body records retain raw SourceID and map through that hash; duplicate effective
source/epoch pairs (including selected/withheld collisions) are errors. Event IDs
append raw line and normalized component ordinal. Advancing a revision deliberately
replaces that epoch's projection; overlapping epochs remain distinct even when
their bytes/native IDs match. No cross-epoch deduplication occurs. Within a trace,
legacy `events.iter_events` normalization and native-ID duplicate flags are reused.
Subagent user-role assignments remain nonhuman. Exact source roles, associations,
state, original path, selected publication, and revision are stored in `sources`.

### Optional withheld artifacts

Only `session-publication/v2` inside `session-reader/v2` is accepted; v1 and unknown
versions fail before writes, without fallback. The importer is not a storage reader:
it does not choose current, reconstruct manifests or fetch remote objects.

`optional` defaults false and must be a boolean; true is legal only for `artifact`.
A withheld source must be such an optional artifact with empty SourceID/path/
associations, **absent** Object, valid lowercase SHA256 source identity and snapshot
commitment, and a closed reason code: `policy`, `integrity`, or `unavailable`.
Unknown extra withheld/source fields are rejected rather than retaining private
metadata. Epoch, revision, state, observation and original capture times remain
explicit. Coverage must be enrichment `partial` with missing `source-withheld`.

Withheld coordinates are not body dependencies. Body records must cover exactly
selected effective pairs: a missing selected primary or an injected withheld body
is an error. A withheld-only import never opens, verifies, copies or parses a body.
It creates a source row with no owned/original path, decoded hash or size, no events
and no fake incomplete tail, plus `source_withheld_<reason>` and partial normalization.
Reports expose `source_key_hash`, `optional`, `withheld_reason`, total/selected/withheld
source counts and explicit per-reason withheld counts. A withheld-only session is
not a green empty import. The snapshot commitment is validated as a hash, not
reconstructed from forbidden descriptor fields by this consumer.

A later selected -> withheld -> reselected publication replaces the event index
for that session using the same effective coordinates. Previously accepted private
body-cache evidence is retained, not deleted or reindexed while withheld. This is
not remote deletion or local evidence erasure. Successful idempotent retries still
cannot clear a newer failure target or manufacture freshness.

Raw malformed lines remain accessible through verified owned bodies and exact byte
offsets in `issues`. They are not included in report diagnostics. Unknown semantic
records remain indexed with issues. A nonempty final line without newline is
conservatively marked `incomplete_tail`, even if its JSON happens to parse; a
session with any normalization issues is visibly `partial`. The body stream is
bounded for copying/hash verification; existing JSONL normalization is line-based
and therefore requires memory proportional to the largest individual record.
Non-JSON artifacts retain raw bytes, roles, coordinates and normalization issues;
this slice does not invent semantic parsers for binary databases or snapshots.

Every timestamp named `_ns` is an integer Unix nanosecond value; no DuckDB timestamp
conversion loses precision. Zero denotes an unknown stage time. The offline writer
sets immutable `published_at_ns` to zero because commit time is not known when its
stable candidate is accepted; the importer does not replace that unknown with a
capture timestamp or falsely report transport completion time. `normalized_at_ns` means this normalization pass
completed, not that malformed content was successfully normalized. Coverage and
`normalization` expose that distinction. Freshness staleness defaults to one day
from capture (configurable at the report API) and also marks previous successes
with outstanding import errors stale. It does not claim a production freshness SLA. Aggregate `freshness` reports actual
maximum captured/published/imported timestamps and explicit unknown capture/publication
counts; zero stays unknown and an empty projection yields null maxima, not fabricated
completion times. Per-session timestamps remain authoritative for individual age.

## Validation boundary

`test_publications.py` tests the consumer boundary with synthetic reader exports:
unsupported versions, dependency errors, body absence/corruption, exact coverage,
roles, raw malformed offsets, incomplete tail, epoch overlap, revision replacement,
idempotence, monotonic failure targets across older successful/failed retries,
schema rejection, bounded reader invocation, archive binding and protection of unrelated research DBs.
The original 16 tests retain their assertions with v2 fixtures; additional cases cover
withheld-only and mixed body guards, redaction/coverage/injection rejection, effective
identity collisions, selected/withheld/reselected updates, retained cached evidence and
newer failures, and byte-for-byte preservation of refused v1 databases. These are
**not writer acceptance fixtures**. Release compatibility requires the separate
actual Go writer -> shared reader -> importer -> event index -> report journey.
Native/live rollout remains separate. Opt-in dashboard/query caller integration and
an actual writer → reader → importer → server process journey now exist; default
collector/dashboard activation, full replacement acceptance and production cutover
have not occurred.

## Explicit endpoint publication refresh candidate

`tools/analytics/publication_refresh.py` adds opt-in all-session delivery from an
explicit private Mac + BC-01 endpoint set, without adding a second storage resolver.
Each full bounded sweep calls Go with `--config … --after-session … --limit 256`.
The optional endpoint `discovery` enum selects `queue` (default, existing
`capture-v1 publication-status` locally ACKed heads) or `remote-current`
(`capture-v1 current-heads`, bounded current-root enumeration through the authoritative
Go R2 reader, no queue/SSH requirement). Unknown modes are rejected. Both use
`session-publication-heads/v1`, then the existing actual reader exporter and
`publications.import_export`. Production endpoints for both machines use a local
binary/explicit credential-handoff wrapper and per-machine private reader config with
`remote-current`, not interactive SSH or background 1Password approval. On Mac the
BC config binds BC's machine ID and shared archive/backend named credential references,
with Mac-local paths and default roots disabled. Local configured Go export remains
the body reader; no Python R2 SDK, resolver or direct-key access is added. Optional
SSH transport remains available for offline/manual use. No persistent lexical
watermark or known-session list is the discovery authority. Import pins the actual
reader coordinate, which may advance beyond the enumerated target but cannot change
identity, regress, or conflict at equal sequence.

The heads wire additively permits an optional `failures` array of
`{identity: fullIdentity, reason: "current-unreadable"}`. Python validates the exact
failure fields, full archive-coherent identities, strict fixed reason, a combined
heads+failures limit of 256 and uniqueness across heads/failures/pages. A failed current
for A does not discard healthy B or prevent subsequent pages/agents/endpoints importing.
Scope/LIST/auth/cancellation errors still fail enumeration, never successful-empty.

Consumer enumeration attempt/completion/failure and expected/unimported/failed targets
are durable in an independent private SQLite WAL/FULL database, not mixed into or
silently migrated into the publications projection schema. Previous success survives
failed updates. Each endpoint and session can fail independently; repeated full sweeps
recover after outages and rebuild empty projections. Counts and endpoint health use
`session-publication-refresh-status/v1`; errors are fixed safe codes, not transcript or
credential-bearing subprocess output. `--once` and bounded autonomous polling are
available; the destination has single-refresh-writer flock ownership. No network call
runs inside a status or projection transaction. Status schema v3 adds `head_failures`
keyed by endpoint/full identity, with fixed reason, attempts and first/last observed
nanoseconds, not an invented publication coordinate. Prior successful targets/projections
are preserved. Only a returning valid head clears its identity's failure; starting or
completing another sweep, or an empty page, cannot hide it. Full sweeps retry automatically.
`discovery_errors` counts failures for active configured endpoints and must be zero for
one-shot success and dashboard health. Expected targets are the union of valid target
and active failure keys, without double-counting a previously imported A. Omitted
endpoint failure history is retained but does not count as active discovery coverage.

See the [analytics README](../tools/analytics/README.md#explicit-all-machine-publication-refresh-candidate)
for the endpoint configuration, private remote staging/cleanup, process/byte/time
bounds, commands and focused tests. The actual-process two-machine-queue journey
uses the real Go CLI and reader rather than handwritten reader JSON. This is an
implemented offline candidate; mocked command-selection/binding tests do not establish
the remote-current SDK/live journey or production two-machine acceptance. It is not
evidence that private endpoints were configured,
services installed, real sources imported, or Mac/BC-01/live-R2 acceptance completed.

### Concurrent local access and independent dashboard health

`projection_access.py` coordinates participating publication readers and imports using
an owned/private/no-follow `.projection.lock` shared/exclusive flock, with bounded
10-second waits. Dashboard read connections, report generation, publication-mode
operator SQL and refresher verification use shared access; local imports use exclusive
access. Reader exports/network never hold this lock. Uncoordinated direct importer
callers remain responsible for writer coordination; the projection schema is unchanged.

`serve.py --source publications --refresh-status PATH` adds read-only local independent
consumer health (`session-publication-refresh-health/v1`) to report/v2: endpoint errors,
expected/unimported/failed targets, active discovery errors, oldest endpoint completion and staleness (default
300 seconds; `--refresh-stale-seconds` configures it). Missing/unavailable status is
unhealthy; omission explicitly means partial imported-subset mode, not archive coverage.
The UI warns accordingly. GET never invokes imports, exports or network discovery.

Refresh status **schema v3** binds the full expected endpoint configuration atomically
before any enumeration or export. A configuration digest covers each endpoint's name,
binary, config path, optional SSH host and explicit discovery mode; changed binding
(including switching to remote-current) invalidates active completion
before work begins. Every configured endpoint must complete under that binding for
health to be green. Unattempted endpoints survive crashes as incomplete, and removed
endpoints are retained explicitly as omitted, not counted as active completion. Health
without a valid binding fails closed. Status schemas v1/v2 are explicitly rejected with
no automatic migration/reset; use a deliberately selected new status path. This is separate from
unchanged publication projection schema v2 and existing report wire versions.
