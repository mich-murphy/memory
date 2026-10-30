# Replacement implementation checkpoints

**Scope:** repository implementation reference, not release or runtime-health evidence.
The opt-in candidate includes independent capture, enrichment, backend selection and
publication-backed analytics; wire/reader/report contracts are v2 despite the
`capture-v1` command prefix. Unselected defaults remain legacy. For deployment,
qualification and next action, see the [current handoff](session-capture-handoff.md#current-session-checkpoint).
The [2026-10-24 offline checkpoint](session-capture-offline-evidence.md) is dated
historical evidence, not permission to install, ingest, reset or delete.

## First-slice chronology (2026-10-23)

The initial `agent-sessions capture-v1` commands were deliberately separate from legacy
`sync`, `backfill`, hooks and install. This first slice accepted an explicit local store,
archive identity and existing machine identity without R2 credential discovery.
The commands below remain available; current configured init/export and runtime
backend selection are described in [backend construction](session-capture-backends.md).

- `init --store DIR --archive ID`: guarded archive binding; no silent archive reset.
- `capture --spool DIR --archive ID --machine ID --agent claude|codex|pi
  --session ROOT_QUALIFIED_ID --source SOURCE_ID --path PATH --policy-dir DIR`:
  durably accept a raw transcript observation. `--role main|subagent` is explicit.
- `deliver` with the same identity, spool and policy directory plus `--store DIR`:
  deliver accepted work without requiring another source read or hook.
- `status --spool DIR --archive ID`: inspect durable local progress.
- `export --store DIR --archive ID --machine ID --agent AGENT --session ID
  --output NEW_DIR`: the authoritative reader exports a pinned complete publication.
- `fixture --output NEW_DIR`: create synthetic sources, capture append + rewrite and
  subagent evidence through the actual durable writer, publish, then export through
  the shared reader. No private source or production configuration is required.

See the current [wire/reader v2](session-publication-v1.md). Metadata and bodies in an export belong
to the same immutable publication. Python consumes the resolved export rather than
reimplementing archive selection. `tools/analytics/publications.py` has a separate
archive-bound event index/report path; existing local research DBs are not overwritten.

## Explicit boundaries

The capture CLI requires an explicit effective local exclusion registry directory.
Unreadable or missing policy fails closed: both the explicit policy directory and
its `exclusions/` registry directory must exist. A deliberately initialized empty
registry is distinct from an unavailable one. Exclusions are reloaded for delivery,
including immediately before guarded current publication; retained historical path
identities cannot disappear from append-policy evidence. This is **not** a
remote policy recovery mechanism. An empty isolated fixture registry must never be
substituted for a production machine's preserved effective local/remote opt-outs.
The raw first-slice CLI withholds known credential/protected configuration identities.
The phase-3 runtime adds internally verified scrubbing, bounded WAL-aware database
backup, compressed sources, explicit learned roots and indexed artifact ownership.
Native launch provenance, self-contained harness/memory bundles and declared alias
integration are now implemented and reviewed offline. Their actual native-agent,
representative inventory/resource and installed-path acceptance remains required before
final controlled acceptance/cutover. Explicit partial discovery/enrichment is not
a claim to capture missing artifacts.

The default retained encoded spool quota is 1 GiB (override downward for tests); each
raw and encoded source snapshot is also limited to 1 GiB. There is no automatic
evidence eviction or remote GC. Retained snapshots are charged, not
just currently pending jobs. Full objects are the baseline, without legacy graph replay.
The first slice intentionally does not claim that this quota is adequate for real
79 MB transcripts / 615 MB exports: distribution and traffic measurements are required.
Spool initialization fences the entire created ancestor directory chain as well as
file/payload boundaries. The first slice serialized snapshot I/O globally; the
phase-3 candidate replaces that with exact quota reservation and independent sessions.
Prepared derived working space has its own conservative bound, not encoded spool
quota; see the phase-3 resource table.
An immutable candidate cannot know its successful remote commit time: the manifest's
`published_at_ns` is explicitly zero/unknown, not capture time relabeled as remote
success. Durable local published status advances only after remote confirmation.

The candidate's operational defaults include 30-second discovery, 4 capture and
4 delivery workers, bounded separate raw/optional delivery lanes with per-session
ordering and supersession guards, 60-second cooperative operation
budgets, retries capped at 5 minutes with jitter, and a 256-MiB free-disk reserve.
Healthy capture-to-publication within 60 seconds after a stable observation remains a
**target, not a measured service guarantee**. Polling cannot preserve bytes that disappear between captures;
producer-atomic snapshots cannot be promised for arbitrary mutable source files.

No automatic epoch deletion, remote GC, or pruning is enabled. Durable queue corruption
is an integrity failure, not an invitation to delete the DB and recapture newer paths.

## Offline acceptance entry point

```sh
nix develop --command env -u AGENT_SESSIONS_TEST_R2 -u AGENT_SESSIONS_LIVE \
  make capture-acceptance
```

This requires the existing pinned analytics Python environment (or an explicit
`ANALYTICS_PYTHON` with `tools/analytics/requirements.txt` installed). The gate does not
install dependencies. It builds the actual candidate executable, generates fresh
writer output and passes it through the shared reader, importer, event index and actual
report. `make test` and `make check` require this gate in addition to all existing
quality gates. CI has a separate pinned-dependency job for it. No gate was removed or
weakened.

## Remaining release requirements

The integrated coordinator, quarantine/replay, destination pause, source progress,
enrichment/repair and opt-in service/hooks candidates have dated offline evidence;
current checkout qualification belongs in the handoff. Complete real-inventory artifact coverage, measured combined
resource/freshness bounds, actual native agents, live conditional-R2 behavior, both
machines' source/policy inventories and installed-path acceptance remain separate work.
Analytics dashboard/query candidate wiring exists with `--source publications`; default
activation remains deliberately deferred to controlled cutover. Offline local CAS and
an SDK protocol peer cannot establish actual R2 endpoint behavior. Production cutover and
reviewed retired-key deletion remain blocked by the authoritative contract.
