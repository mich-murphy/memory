# Requirements and data contract register for a Rust rewrite

Reviewer role: requirements and data contract. Read-only. No repo edits, no live access.
Principles applied (read in full): foundational thinking (data shapes first, so section 2 precedes behavior), subtract before you add (section 4 separates frozen from free, section 1 tags legacy-only items so they are not carried), type-system discipline (section 2 flags every invariant the Go code holds by runtime check that Rust types can hold by construction).

Labels. "measured" means I read the cited code in this session. "doc says" means I only read prose. "inferred" and "guess" as stated.
Tags. contract = agreed in docs/session-capture-contract.md. implemented = exists in the `capture-v1` candidate code. legacy-only = only `sync`/`backfill`/`internal/syncer` path does it.

## Headline findings

1. The wire contract is small and closed. `session-publication/v2` is five JSON documents (archive, manifest, current, export metadata, current-heads page) plus content-addressed `.zst` objects. All are defined by 147 lines in `internal/publication/types.go:1-147` (measured). A Rust port can derive it from one schema.
2. The Go wire types are looser than the invariants they carry. `Role`, `State`, `Agent`, `Encoding`, `Coverage.Discovery/Enrichment` are bare strings. The "selected vs withheld" state is a bag of fields (`Source.Optional`, `Source.Withheld`, `Source.Object`, `SourceID`, `Path`, `Associations`) whose legal combinations are enforced only at runtime in `validateSource`/`validateWithheld` (`publication/publication.go:211-235`, `selection.go:35-42`). This is the clearest sum-type opportunity (section 2.4).
3. Because the operator accepts rebuilding R2, nothing about the publication byte format is frozen. What is truly frozen is local and non-wire: machine IDs, exclusion records, learned roots, source files, research DBs, credentials (section 4).
4. The one external consumer interface is a CLI subprocess contract, not a library. Python calls `agent-sessions capture-v1 current-heads` and `export` and reads `metadata.json` plus `bodies/NNNN.bin` (measured: `tools/analytics/publication_refresh.py:116-130`). Exit status 4 means export budget exceeded. A Rust binary must keep subcommand names, flags, JSON shapes and that exit code, or the Python importer must change with it.
5. Several privacy behaviors are sharp and surprising and must be specified before porting, not rediscovered. The strongest example is that one Codex exclusion denies all Codex shared resources under the root (section 3.4).

## 1. Functional requirements

Citations are `file:line` (measured) or `doc §` (doc says). Where I cross-checked a doc claim in code I say so.

### 1.1 Identity and layout

- R-01 MUST identify each publication unit by `Identity{archive_id, machine_id, agent, session_id}` with every field nonempty, valid UTF-8, no NUL. [contract, implemented] `publication/publication.go:19-22,33-40`; contract §1.
- R-02 MUST derive the session key prefix as `capture-v1/archives/<hex(archive)>/machines/<hex(machine)>/agents/<hex(agent)>/sessions/<hex(session)>`, lowercase hex of UTF-8 bytes. [implemented] `publication/types.go:62-66`.
- R-03 MUST bind a backend to exactly one archive by a create-only `capture-v1/archive.json` `{"contract","archive_id"}`; never rewrite it. [contract, implemented] `publication/publication.go:92-113`.
- R-04 MUST reject any manifest dependency whose object key is not `<BaseKey>/objects/<encoded_sha256>.zst` for the same session. [implemented] `publication/publication.go:239-245`.
- R-05 MUST reject identities whose longest derived key (`BaseKey` plus `/manifests/<64 hex>.json`) exceeds 1024 bytes, for every backend kind. [implemented] doc phase-3 "Admission refuses"; `sessioncapture/keylen.go` (not read, doc says).
- R-06 MUST derive `session_id` for discovered sources as `session/<sha256hex(root path)>/<adapter session key>` for session-owned files and `resource/<sha256hex(root)>/<key>` for agent-wide resources. [implemented] `capturesource/classify.go:140-150`.
- R-07 SHOULD NOT use a short or hostname-derived root tag; the full SHA256 of the clean absolute root is the namespace. [contract-adjacent, implemented] doc phase-3 "Discovery"; `classify.go:140-142`.

### 1.2 Manifest, epochs, current reference

- R-10 MUST publish a complete immutable manifest per publication; readers MUST NOT replay predecessors. [contract §4, implemented] `publication/publication.go:300-312` `resolve`.
- R-11 MUST give each manifest `sequence` in (0, MaxInt64], strictly increasing per session, and a `publication_id` different from its predecessor. [implemented] `publication.go:27,170,254-256`.
- R-12 MUST address manifests by `sha256(exact stored bytes)`; consumers MUST pin and store that digest and not re-serialise. [implemented] `publication.go:262-272,296-302`; doc publication-v1 "Export".
- R-13 MUST cap manifest metadata at 8 MiB and sources at 1024, and MUST measure the prospective export envelope against the same 8 MiB at write time. [implemented] `types.go:16-18`, `publication.go:188-201`.
- R-14 MUST reject unknown JSON fields, trailing values and invalid UTF-8 at every metadata level. [implemented] `publication.go:44-62` (`DisallowUnknownFields`, trailing-token check).
- R-15 MUST keep every previously published source/epoch in every later manifest, never lower its `revision`, and never change its object commitment at equal revision. [contract §1, implemented] `publication.go:254-276` `advances`.
- R-16 MUST advance an epoch (same `epoch_id`, `revision+1`) only when the new object is proven to start with the previous bytes (prefix SHA256 equal and size not smaller); otherwise seal the old epoch and begin a new `epoch_id`. [contract, implemented] `sessioncapture/capture.go:302-319`.
- R-17 MUST derive `epoch_id` as `sha256("<BaseKey>:<SourceID>:<sequence>:<object sha256>")` for a new epoch. [implemented] `capture.go:302`. Derivation need not be reproduced if R2 is rebuilt, but it must be stable per accepted capture.
- R-18 MUST derive `publication_id` as sha256 of the manifest JSON with `publication_id` set to `""`. [implemented] `capture.go:543-549`. This ties the ID to Go's `json.Marshal` field order (inferred). A new writer only needs the ID to be stable across retries of the same accepted job.
- R-19 MUST set `published_at_ns` to 0 (unknown) at acceptance; local "published" state advances only after remote CAS acknowledgement. [implemented] `capture.go:332-336`; doc implementation §"Explicit boundaries".
- R-20 MUST write `current.json` only by compare-and-swap with the token from the preceding read; empty token means create-if-absent. [contract §4, implemented] `publication.go:530-543`, doc publication-v1 "Backend".
- R-21 MUST upload and verify every selected object before the manifest and before CAS. [contract §4, implemented] `publication.go:488-503`.
- R-22 MUST treat a lost CAS acknowledgement by reading `current` back and accepting only an exact match to the intended `Current`; never blindly retry a conditional write. [implemented] `publication.go:535-543`.
- R-23 MUST treat a required fsync failure (`ErrDurability`) as an error even when the bytes are visible. [implemented] `types.go:23`, `publication.go:514-516`.
- R-24 MUST let a Local backend fence file and every parent directory entry, including on idempotent retries. [implemented, Local-only] doc publication-v1 "Local safety"; `publication/local.go` (not read).
- R-25 SHOULD cap object read decoding with a 64 MiB zstd window/memory and read at most declared sizes plus one byte. [implemented] `publication.go:324-330,336-345`.

### 1.3 Reader and export

- R-30 MUST provide one authoritative reader that resolves `current`, checks archive binding, verifies manifest digest, and stream-verifies every selected object's encoded and decoded size and SHA256. [contract §4, implemented] `publication.go:262-300,466-472`.
- R-31 MUST return explicit errors for missing, corrupt or unsupported content; never a successful empty session. [contract §4, implemented] `publication.go:466-472`.
- R-32 MUST export a pinned publication: select `current` once, check cumulative decoded size against `--max-bytes` before any body access or destination creation, write bodies as `bodies/NNNN.bin` mode 0600 in 0700 dirs, then write `metadata.json` last by temp file and rename. [implemented] `publication/export.go:14-60`; doc publication-v1 "Authoritative reader".
- R-33 MUST default the export budget to 1 GiB, max 1 TiB, and report overrun as `ErrExportBudget` mapped to process exit status 4. [implemented] `export.go:13-18`; `tools/analytics/publication_refresh.py:30,116` (doc and code agree).
- R-34 MUST include a body record only for selected sources; withheld sources contribute metadata only. [implemented] `export.go:36-47`; doc.
- R-35 MUST provide bounded current-root enumeration (`current-heads`, `session-publication-heads/v1`, page of at most 256, `next_cursor`, `complete`) that reads `current.json` only and never the manifest. [implemented] `publication/heads.go:15-55`; doc README analytics.
- R-36 MUST restrict enumeration scope to agents `claude`, `codex`, `pi` and a prefix of at most 800 bytes. [implemented] `heads.go:60-76`.

### 1.4 Optional withholding

- R-40 MUST allow `optional=true` only for role `artifact`. [implemented] `publication.go:228-230`.
- R-41 MUST represent a withheld source with empty `source_id`, `path`, `associations`, zero `object`, and `{source_id_hash, snapshot_commitment, reason}` where reason is one of `policy|integrity|unavailable`. [implemented] `selection.go:35-42,25-27`.
- R-42 MUST require, for any withheld source, manifest `coverage.enrichment == "partial"` and `coverage.missing` containing `source-withheld`. [implemented] `publication.go:181-183`.
- R-43 MUST define effective source identity as `sha256(source_id)` for selected sources or `source_id_hash` for withheld, and unique `(effective id, epoch_id)`. [implemented] `selection.go:12-17`, `publication.go:171-179`.
- R-44 MUST define `snapshot_commitment = sha256(key NUL sha256 NUL encoded_sha256 NUL encoding NUL decimal size NUL decimal encoded_size)`. [implemented] `selection.go:20-24`.
- R-45 MUST NOT allow a required epoch to become optional later. [implemented] `publication.go:273`.
- R-46 MUST NOT treat hook-supplied labels or associations as privacy authorization. [contract-adjacent] doc enrichment §"Versioned deliverable selection".

### 1.5 Capture, spool and scheduling

- R-50 MUST own accepted bytes durably (encoded payload fsynced, then SQLite job row with WAL and `synchronous=FULL`) before reporting accepted. [contract §2, implemented] `sessioncapture/capture.go:150-170` (partial read), `queue.go:458`.
- R-51 MUST NOT confuse a queued pathname with captured bytes. Hints are coalescible metadata. [contract §1, implemented] doc phase-3 "Durable coverage".
- R-52 MUST double-hash a stable observation and compare provenance rather than trust size/mtime for unchanged detection. [implemented] doc phase-3 "CaptureIfChanged".
- R-53 MUST keep per-session ordering and let unrelated sessions proceed. [contract §3, implemented] doc phase-3; known limit R-53a below.
- R-53a Known limit, SHOULD be fixed in the rewrite. Each snapshot admission calls `usage()` which `ReadDir`+stats every payload (`capture.go:38-56,111`). Capture of session A therefore depends on spool size owned by B. Doc phase-3 admits this.
- R-54 MUST persist retries with capped jittered backoff (1 s to 5 min) and no attempt limit for transient, timeout, throttle and spool-full classes. [contract §3, implemented] doc phase-3 "Failure dispositions", resource table.
- R-55 MUST consume the quarantine budget (default 8 attempts, configurable 1 to 100) only for deterministic classes: `policy-blocked`, `integrity-error`, `quota`, `rejected`, `conflict`. [implemented] doc phase-3; `capturedaemon/config.go:209`.
- R-56 MUST pause the destination, not quarantine sessions, on destination authentication or configuration failure. [contract §3, implemented] doc backends §"Capture independence".
- R-57 MUST keep quarantine durable, inspectable and replayable with coordinates, reason, attempts, contract and software version; replay MUST NOT erase history. [contract §3, implemented] `runtime_store.go:49` (schema), doc phase-3.
- R-58 MUST debounce a changed source until quiet for `quiet_seconds` (default 30) and at most `max_delay_seconds` (default 300). [implemented] doc phase-3 "Capture cadence".
- R-59 MUST supersede undelivered, proven-subsumed append jobs at acceptance so k deferred appends deliver as one PUT and one CAS. [contract §3 "coalesce proven-subsumed", implemented] doc phase-3 resolution of GAP-03.
- R-60 MUST NOT evict accepted evidence silently. A full spool stops accepting and shows `spool-full`. [contract §3, implemented] doc phase-3.
- R-61 MUST park a session at `source-limit` (1024 epochs) as a visible terminal state, keep prior epochs and the previous publication, and retry on new observation. [implemented] doc phase-3 GAP-05.
- R-62 MUST rebase a lagging or rebuilt spool onto the remote current by merging local view at remote sequence+1, retaining every remote epoch and sealing a displaced remote active epoch; same epoch+revision with a different commitment is `conflict`. [implemented] doc phase-3 "Rebase (GAP-06)"; `sessioncapture/rebase.go` (not fully read).
- R-63 MUST run discovery periodically (default 30 s) and from hooks; a hook MUST only enqueue a hint and never open the queue or deliver. [contract §3, implemented] doc phase-3 "Opt-in CLI".
- R-64 SHOULD reuse one local coordinator process under an exclusive lifetime flock; read-only status commands open SQLite read-only while it runs; replay and resume require it stopped. [implemented] doc phase-3 "Durable coverage".
- R-65 MAY chunk or delta-encode objects only after measured justification and without changing the logical contract. [contract §4] No chunking exists in the candidate.
- R-66 MUST NOT run automatic remote GC or epoch deletion. [contract §4, implemented] doc implementation.

### 1.6 Sources and discovery

- R-70 MUST perform metadata-only bounded discovery over explicit roots (canonical defaults plus learned), with at most 4096 entries per call divided fairly across roots, at most 256 directory descriptors, at most 128 roots. [implemented] doc phase-3 table; `capturecoordinates.MaxRoutes=128` (`coordinates.go:20`).
- R-71 MUST classify roles as exactly `main`, `subagent`, `tool-result`, `artifact` at publication level; adapter-level roles are kept only as `adapter-role:<x>` associations. [implemented] `classify.go:128-139,150-156`.
- R-72 MUST mark an artifact `optional` only when it belongs to a session (`SessionKey != ""`); agent-wide resources are required. [implemented] `classify.go:156`.
- R-73 MUST set source `state` to `active` or `sealed`. [implemented] `capture.go:302,312-313`. The Go code never validates this set (section 2.4).
- R-74 MUST transform recognized sources in the queue, never from a caller-supplied path: `.jsonl.zst` decoded; SQLite via readonly VFS and bounded incremental backup including WAL; JSON/TOML config scrubbed; `~/.claude.json` as one exact standalone scrubbed resource. [implemented] `capturesource/derived.go:17-73`; doc phase-3.
- R-75 MUST fail rollback-journal SQLite (`ErrSQLiteRollbackJournal`) rather than copy raw. [implemented] doc phase-3.
- R-76 MUST include transform kind, `transformPolicyVersion` and sidecar stat identities in the discovery fingerprint and bump the version when scrubber rules, backup procedure or decode limits change. [implemented] doc phase-3; `scrub/digest.go:17-27` `RulesDigest` golden-tests this.
- R-77 MUST honor declared root and leaf aliases only as exact declared equivalences (max 128 routes, 4096 leaves, depth 8), never as traversal authority. [implemented] `capturecoordinates/coordinates.go:19-28,53-70,172-179`.
- R-78 MUST classify hook paths and sweep paths by the same longest-matching-root rule. [implemented] `classify.go:204-218` `owningRoot`.
- R-79 MUST reject executable files, non-regular files, depth over 64 components, relative or NUL-bearing paths. [implemented] `classify.go:117-120`, `coordinates.go:101-108`.

### 1.7 Enrichment

- R-80 MUST run enrichment in a separate bounded lane that never holds the session capture lock across scrub, hash or network work and attaches by a short non-waiting lock. [contract-refinement, implemented] doc enrichment §"Why an independent lane".
- R-81 MUST make enrichment failure independent of safe transcript capture and expose partial provenance. [contract §3, implemented] doc enrichment.
- R-82 MUST supersede a blocked composite with a higher-sequence selection view built from the latest accepted state; never mark the old job published, never upload forbidden bytes, never delete its repair record. [implemented] doc enrichment §"Versioned deliverable selection".
- R-83 MUST ship the harness snapshot as one deterministic TAR (`snapshot.json`, `harness.json`, optional `memory.json`, `assets/NNNNNN.bin`) with generated member names only, no links or devices, within 64 MiB selected content, 2 MiB evidence, 8 MiB metadata, 128 MiB TAR. [implemented] doc enrichment; phase-3.
- R-84 MUST store native launch evidence in an immutable inbox (private files, fsync, deletion only after queue adoption) separate from the queue lock. [implemented] doc enrichment §"Durable work".
- R-85 MAY defer enrichment entirely in the rewrite's first slice. The contract treats it as optional and it is separately opt-in (`enrichment` config flag). [inference] subtract-first. See Open question 4.

### 1.8 Hooks, install, operations

- R-90 MUST make hooks exit 0 and print nothing, finish in about 0.1 s, and detach any further work. [legacy-derived, implemented in candidate] `docs/hooks.md` "One rule for every agent"; candidate `hook` command doc phase-3.
- R-91 MUST preserve unrelated and legacy hooks on install, back up before edit, replace only own entries, and refuse to rewrite unparseable settings. [implemented] doc hooks "Edge cases", phase-3.
- R-92 MUST never trust or write Codex `trusted_hash` entries. [operator rule] handoff "Working rules".
- R-93 MUST support a persistent per-session opt-out via `AGENT_SESSIONS_DISABLE=1` that records an exclusion and emits no launch bytes, queue work or wake. [implemented] doc enrichment §"Durable work"; README.
- R-94 MUST persist an agent-scoped privacy barrier when a disabled identity cannot be resolved, clearable only by exact nonce plus `--apply` plus acknowledgement. [implemented] doc phase-3 "Opt-in CLI".
- R-95 MUST load runtime config from a strict private (0600, owned, regular, not symlink) `capture-runtime/v1` JSON of at most 64 KiB with unknown fields rejected; it MUST NOT resolve credentials during parse, hint or inspection. [implemented] `capturedaemon/config.go:23-24,73-100`; doc backends.
- R-96 MUST resolve R2 credentials only from two explicitly named environment variables; no default credential chain, no instance metadata, no legacy config lookup, no insecure endpoint override. [implemented] doc backends §"Selection and credentials".
- R-97 MUST use R2 region `auto`, path-style addressing, static credentials, bounded HTTP transport, no redirects and no SDK retries; the coordinator owns retry. [implemented] doc backends.
- R-98 MUST use size-aware object transfer budgets: `30 s + size/min_throughput` clamped to 60 s to 15 min, 30 s idle limit, flat 30 s for metadata and CAS. [implemented, unmeasured] doc phase-3 "Object transfer budgets".
- R-99 MUST machine ID be explicit, never guessed from hostname, never auto-minted on config load. [legacy-implemented, contract-preserved] `docs/machine-identity.md`; contract §6.2.

### 1.9 Analytics consumer

- R-100 MUST make Python (or any consumer) call the authoritative reader rather than recreate selection; metadata and bodies MUST come from one pinned publication. [contract §4, implemented] doc; `publication_refresh.py:130`.
- R-101 MUST make consumer imports idempotent and carry `(source_id, epoch_id)` through to the event index so overlapping epochs do not collide. [contract §5, implemented] doc phase-3; `tools/analytics/publications.py:209-225` (tables seen, collision logic not read).
- R-102 MUST surface unsupported contract, missing body and oversize as typed failures (`export_too_large`, `reader_digest_missing`, `process_failed`, `current-unreadable`), not green freshness. [contract §5, implemented] README analytics; `publication_refresh.py:116-117`.
- R-103 MUST validate with the same bounds as Go: metadata at most 8 MiB, `sequence`/`revision` in (0, MaxInt64], at most 1024 sources, unknown fields rejected. [doc says; code not cross-read]. The gap register (GAP-08) records Python accepted 16 MiB earlier; doc says fixed.

### 1.10 Legacy-only (do not carry unless the operator says so)

- L-01 `v3/...` layout: plans/commits per stream, `v3/blobs/zstd|plain`, `v3/snapshots/sets`, `v3/store.json`, checkpoint per machine. legacy-only. `docs/storage-layout.md:1-24` (doc says).
- L-02 Per-machine remote exclusion records under `v3/machines/<machine>/exclusions/<sha>.json` and their recovery before launches. legacy-only. `storage-layout.md:277-285`. The candidate keeps only local exclusions; see Open question 2.
- L-03 Detached `sync --path --delay 3` and a sweep at session start/end, `pending/` markers, per-sync lock with 30 s and 60 s give-up. legacy-only. `docs/hooks.md`.
- L-04 `MachineIDError`/`machine-id --new`, `doctor`, `install`, `backfill`, `uninstall` command set. legacy-implemented; the candidate has its own `service`/`hooks` commands.
- L-05 Concurrent cold recovery, pruned-piece repair, retired schema compatibility. Retired by AGENTS.md and contract. Not requirements.

## 2. Logical data model

### 2.1 Entities, identity, key fields

| Entity | Identity | Key fields (exact names) | Where defined |
| --- | --- | --- | --- |
| Archive | `archive_id` | `contract`, `archive_id` | `publication/types.go:68-71`, key `capture-v1/archive.json` |
| Machine | `machine_id` (opaque, up to 256 bytes, no NUL/CR/LF in config) | none beyond the ID; file `machine-id` beside config | `capturedaemon/config.go:262-264`; `docs/machine-identity.md` |
| Agent / producer | `agent` in {`claude`,`codex`,`pi`} | adapter name | `capturecoordinates.validAgent` `coordinates.go:44`; `heads.go:76` |
| Session | `Identity` four-tuple. `session_id` = `session/<sha256 root>/<adapter key>` or `resource/<sha256 root>/<key>` | n/a | `types.go:55-60`, `classify.go:140-150` |
| Source | `source_id` (adapter file key, string) within a session; effective id `sha256(source_id)` | `source_id`, `role`, `path`, `associations` | `types.go:84-98` |
| Epoch | `(effective source id, epoch_id)` | `epoch_id`, `state` (`active`/`sealed`), `revision` | `types.go:84-98`, `capture.go:302` |
| Object | content address `encoded_sha256` | `key`, `encoding`=`zstd`, `sha256`, `encoded_sha256`, `size`, `encoded_size` | `types.go:108-115` |
| Capture (accepted view) | `(session BaseKey, sequence)` in SQLite `jobs` PK | `manifest`, `evidence` blobs, `state`, `attempts`, `last_error`, `next_retry` | `queue.go:458` |
| Job | same row as Capture | state `pending|retry|published|quarantined|superseded` (inferred from doc and gaps) | `queue.go:109-121` |
| Publication | `publication_id` (sha256 hex) + `sequence` | whole `Manifest` | `types.go:73-83` |
| Manifest | `sha256(stored bytes)` | `contract`, `identity`, `publication_id`, `sequence`, `observed_at_ns`, `captured_at_ns`, `published_at_ns`, `coverage{discovery,enrichment,missing[]}`, `sources[]` | `types.go:73-83` |
| Current reference | session `current.json` | `contract`, `identity`, `publication_id`, `manifest_key`, `manifest_sha256`, `sequence` | `types.go:116-123` |
| Export (reader output) | pinned `manifest_sha256` | `contract`=`session-reader/v2`, `manifest_sha256`, `manifest`, `bodies[]{source_id,epoch_id,path,sha256,size}` | `types.go:124-140` |
| Heads page | cursor | `contract`=`session-publication-heads/v1`, `archive_id`, `heads[]`, `failures[]`, `next_cursor`, `complete` | `heads.go:26-33` |
| Enrichment artifact | an optional `artifact` source with associations `enrichment:<kind>`, `basis:<publication_id>`; body is TAR | `Source.Optional=true`, role `artifact` | `sessioncapture/enrichment_worker.go:268` |
| Enrichment work/result | `enrichment_work.id`, basis (publication, sequence, source, epoch, revision, sha256, selector version `snapshot/v1`) | `spec`, `state`, `attempts`, `next_retry`, `contract`, `software` | `enrichment_store.go:41-44,105-115` |
| Quarantine record | `runtime_quarantine.id` | `kind`, `source_id`, `session`, `sequence`, `request`, `reason`, `attempts`, `created_at`, `contract`, `software_version`, `active` | `runtime_store.go:49` |
| Exclusion record | `id = sha256(agent NUL resolved_root NUL session_name)` | `schema`=1, `id`, `agent`, `root`, `resolved_root`, `session_key`, `paths[]`, `recorded_at` (float seconds) | `exclusion/exclusion.go:25-34,86-88` |
| Hint | pathname plus agent; durable inbox of at most 1024 hints, each at most 64 KiB | `path`, `agent`, optional native metadata | doc phase-3 |
| Runtime source row | `UNIQUE(session, source)` | `request`, `version`, `captured_version`, `state`, `observed_state`, `attempts`, `next_retry`, `claim` | `runtime_store.go:38` |

Python projection (DuckDB), measured: `publication_binding(version, archive_id)`, `sessions(session_key PK, archive_id, ...)`, `sources(session_key, trace_id, archive_id, ...)`, `failures(session_key PK, archive_id, ...)` (`tools/analytics/publications.py:207-225`). `session_key` is a join of the four identity fields (`:319`).

### 2.2 Invariants between entities

I-01 `Current.identity == Manifest.identity`, `publication_id` and `sequence` equal, and `manifest_key == BaseKey/manifests/<manifest_sha256>.json`. `publication.go:249-258,286-290`.
I-02 Each selected object key equals `BaseKey/objects/<encoded_sha256>.zst`. `publication.go:239-245`.
I-03 Each `(effective source id, epoch_id)` is unique within a manifest. `publication.go:171-179`.
I-04 At most one `active` epoch per `source_id` (assumed). The Go code does not enforce it in the wire validator; `activeSource` returns the first match (`capture.go:224-230`). Inferred, unproven.
I-05 Sequence strictly increases; no epoch disappears; revision never regresses; equal revision keeps its commitment; required never demotes to optional. `publication.go:254-276`.
I-06 `Withheld != nil` implies optional artifact, empty `source_id`/`path`/`associations`, zero object, valid hashes and closed reason, and manifest coverage partial plus `source-withheld`. `selection.go:35-42`, `publication.go:181-183`.
I-07 `Optional` implies `Role == "artifact"`. `publication.go:228`.
I-08 Timestamps are int64 ns, nonnegative; `published_at_ns` may be 0. `publication.go:168-170`.
I-09 Spool: every `jobs.manifest` object dependency has a payload file under the spool or is published/superseded; payload refs tracked in `payload_refs(sha, session)`. `reclaim.go:59-60` (schema only; invariant inferred).
I-10 Export `bodies` correspond exactly to selected `(source_id, epoch_id)` pairs with paths `bodies/NNNN.bin`. `export.go:36-60`, doc.
I-11 Archive ID in the runtime config equals the archive binding on the backend and in the spool `binding` table. `queue.go:458`, doc backends §"Capture independence".

### 2.3 Cross-entity rules for the Rust model

- An epoch is the unit of "proven append". A Rust type should make `Append(prev: EpochRef, new_object)` constructible only after a prefix proof value exists (`prefix_sha == prev.object.sha256` and `new.size >= prev.size`). Go computes this inline in `acceptSnapshot` (`capture.go:303`) as a boolean expression.
- A publication is a pure function of the accepted job. `publication_id` is derived from the manifest content, so retry identity is content identity. Keep that.

### 2.4 What Go enforces in types, by runtime check, or by convention

| Invariant | Go mechanism | Evidence | Rust opportunity |
| --- | --- | --- | --- |
| Identity fields nonempty UTF-8 | runtime `validIdentity` at publish and read; plain `string` fields | `publication.go:19-22` | newtype `Identity` with parse-once constructor |
| Hex digests lowercase 32 bytes | runtime `validDigest` | `publication.go:36-40` | `Sha256([u8;32])` with hex serde |
| Sequence and revision in (0, MaxInt64] | `uint64` plus runtime `validOrder` | `publication.go:28` | `NonZeroU63` newtype |
| Role in {main, subagent, tool-result, artifact} | **not validated on the wire**, plain string. Only `"artifact"` is checked | `publication.go:228`, `classify.go:128-139` | `enum Role` with exhaustive match (gap: unknown roles from a future writer need a policy) |
| State in {active, sealed} | **not validated**, string | `capture.go:302,313` | `enum EpochState` |
| Encoding == "zstd" | runtime check | `publication.go:230` | `enum Encoding { Zstd }` |
| Withheld reason closed set | runtime `validWithheldReason` | `selection.go:25-27` | `enum WithheldReason` |
| Selected vs withheld source | one struct, `Withheld *WithheldSource`, zero-value `Object` with `omitzero`, runtime `validateWithheld` | `types.go:84-98`, `selection.go:35-42` | `enum Source { Selected{..}, Withheld{..} }` with custom serde to the same JSON |
| Optional only for artifacts | runtime | `publication.go:228` | put `optional` inside `Artifact` variant only |
| Withheld implies coverage partial plus marker | cross-field runtime check in manifest validation | `publication.go:181-183` | smart constructor `Manifest::new` computing coverage from sources |
| Coverage status strings | free strings; doc "recommended" `complete|partial|unknown`, "ordinary views preserve nonempty producer statuses" | doc publication-v1; `publication.go:164-166` | `enum Status` plus `Other(String)` if forward compatibility is wanted; or close it, since the operator rebuilds |
| Object key matches session namespace | runtime string compare | `publication.go:241` | derive the key from `(Identity, EncodedSha256)`; do not store it |
| Manifest/Current agreement | runtime | `publication.go:286-290` | derive `Current` from `Manifest` + digest |
| Unknown fields rejected | `DisallowUnknownFields` | `publication.go:49` | `#[serde(deny_unknown_fields)]` |
| Duplicate JSON keys rejected | **not implemented**, doc says producers must emit unique keys | doc publication-v1 | use a duplicate-rejecting parser in the reader |
| Max sources 1024, metadata 8 MiB | runtime | `publication.go:162-163,196-201` | `BoundedVec<1024>`, size check at encode |
| Exclusion `Record` validity | runtime `validate()` on save and load | `exclusion.go:119-136` | parse into validated newtype at load |
| Agent set | `adapters.Names` slice contains check; separate hardcoded set in `capturecoordinates` and `heads.go` | `exclusion.go:132`, `coordinates.go:44`, `heads.go:76` | one `enum Agent`; the Go code has three independent copies (leak) |
| Spool job state machine | text column `state` with ad hoc updates | `queue.go:458`, `runtime_store.go:491` | `enum JobState` with transition methods |
| Config bounds | table in `validateBudgets` | `config.go:201-221` | newtypes per field or a validated `Budgets` struct |
| "Never overwrite a newer remote" | CAS at backend plus `advances` pre-check | `publication.go:254-276,530` | keep as backend trait contract and test it |

Convention-only (no code enforcement I could find): "one writer per machine ID" (doc machine-identity), "do not delete lock files", "enrichment is optional while main/subagent/tool-result are required" is encoded only in `Optional` being set by the classifier at `classify.go:156`.

## 3. Privacy and safety

### 3.1 Fail-closed rules (all contract or security.md; implemented unless noted)

- F-01 Unknown or unreadable exclusion registry fails closed for the affected scope. Both `PolicyDir` and `PolicyDir/exclusions` must exist as directories; an empty initialized registry is distinct from a missing one. `capturedaemon/config.go:223-234`; doc implementation §"Explicit boundaries".
- F-02 Exclusions are rechecked before delivery, including immediately before the guarded CAS, not only at source read. [contract §3] doc phase-3.
- F-03 Retained historical path identities cannot disappear from append-policy evidence; alias identities are unioned across appends. `capture.go:306-309`.
- F-04 No raw fallback for recognized protected config, SQLite with rollback journal, malformed or credential-alias inputs. doc phase-3, security.
- F-05 Every resolved symlink identity (not only the leaf) is checked against policy before reading, and every recognized identity is scrubbed after reading. `capturepolicy/resolve.go:45-95`; `scrub/scrub.go:156-171`.
- F-06 Reads bind pre-authorization file identity through no-follow descriptors; no unrestricted reopen. doc phase-3.
- F-07 Enrichment reads each asset only after independent authorization; outside-agent-root access goes through the trusted selector, never by widening the runtime allowlist to Home. doc enrichment.
- F-08 An unresolved disabled identity creates an agent-scoped barrier, not merely a failing hook. doc phase-3.
- F-09 Archive reset never re-enables excluded sessions; preserve exclusions, learned roots, identities. contract §6.2; security.md.

### 3.2 What "exclusion" does

An exclusion record is a persisted opt-out of one session (`exclusion/exclusion.go:25-34`). `Registry.Excludes(sf)` returns true when:
1. same agent AND (record.Root equals sf.Root OR resolved roots are the same location) AND (`sf.SessionKey == ""` OR session names equal), or
2. any recorded path alias (canonicalized through existing ancestors) is the same filesystem location as the file's path or source path.
`exclusion.go:176-199` (measured).

Consequence, measured and matching the handoff: `sf.SessionKey == ""` (an agent-wide shared resource such as Codex SQLite or `history.jsonl`) is excluded by any record in the same root. One opted-out Codex session therefore withholds all Codex shared resources under that root. The handoff reports about 7,900 `policy_denied` issues from this (handoff resume table, doc says). This is by design (`TestRuntimePrivacyScopesAggregatesAndUntrustedIdentity`, doc says). It is also a likely source of operator surprise (Open question 3).

Record ID is `sha256(agent NUL resolved_root NUL session_name)` where `session_name` strips a `roots/<tag>/` or `dirs/<tag>/` prefix (`exclusion.go:74-88`). Records are written atomically at mode 0600 as `exclusions/<id>.json`, never deleted by the library ("Save ... never clears an earlier opt-out", `exclusion.go:151`). The registry is the union of all files. Timestamp is a float64 seconds, which loses ns precision; irrelevant to behavior (inferred).

Note the validation rule: `SessionKey` must be relative and clean; `Paths` must be absolute; all identities valid UTF-8; `Agent` in `adapters.Names` (`exclusion.go:119-136`).

### 3.3 What "scrub" does

Scrub redacts literal secrets in known config files and keeps all other bytes so the result still parses and diffs. `scrub/scrub.go:1-5`, doc security.

- Table of protected filenames and rule sets (`ruleTable`, `scrub.go:89-101`): `claude.json`, `.claude.json`, `settings.json`, `settings.local.json`, `managed-settings.json`, `.mcp.json`, `mcp.json`, `managed-mcp.json`, `models.json`, `config.toml`, `managed_config.toml`. Filename match is after `capturepolicy.BaseName` (lowercase, repeated backup suffixes stripped, `capturepolicy/policy.go:88-100`).
- Schema is chosen by filename, not by the collecting agent. All recognized names in the symlink chain apply in turn. `scrub.go:156-171`.
- Rule kinds: `keepReferences` (keep `${VAR}` and Pi `!command` refs), `keepNothing`, `keepReferencesAndEnvNames`, `keepReferencesAndCommands`, `keepURLWithoutCredentials`, `keepArgs`. `scrub.go:26-34`.
- Selected literals and equivalent decoded string copies elsewhere in the document are also redacted, keys are not rewritten. In-place byte replacement is tried first, then re-encode if escapes or multi-line strings defeat it. `scrub.go:173-188`.
- Malformed JSON/TOML, non-object JSON, duplicate decoded object names, invalid Unicode, trailing documents: error, file withheld. doc security.
- Redaction token: `[redacted]`. `scrub.go:23`.
- `scrub.Text` is a second guard for non-config text snapshot assets: withhold small (at most 1 MiB) valid UTF-8 text with a shell assignment or fish export of a secret-named variable with a literal value. Source-code extension files are skipped. It MUST NOT receive main, subagent or superseded transcripts. `scrub/text.go:28-60`. Returns `ErrLiteralSecret` which must be reported as skipped capture.
- Secret-name heuristic: whole words `token|key|auth`; substrings `secret|passw|passphrase|credential|cookie|apikey|authorization`. `scrub.go:192-199`.
- Scrub is NOT transcript DLP. Unrecognized filenames and transcript bodies pass through byte-for-byte. doc security, `capturepolicy/policy.go:1-3`.
- `RulesDigest` hashes the rule table and a golden test forces a `transformPolicyVersion` bump when the table changes; logic outside the table needs a manual bump. `scrub/digest.go:10-27`.
- Config input cap to the scrubber is 5 MiB. doc phase-3.

### 3.4 What "policy" (credentials and noise) does

`capturepolicy.IsCredential` denies `.credentials.json`, `auth.json`, `oauth.json`, `mcp-auth.json`, `config.env`, `*.key`, `*.pem`, `.claude.json*` backups, `.env`, `.env.*`, plus `.git-credentials`, `.netrc`, `.npmrc`, `.pypirc`, SSH key names, and any path under `.ssh`, `.gnupg`, `.aws`, `.azure`, `.kube`, `.config/gcloud` (`capturepolicy/policy.go:17-24`, `credentials.go:12-24`). `IsNoise` denies `.app` bundles, `migration-backups`, `.DS_Store`, `.codex-global-state.json`, `*_cache.json`, `*-cache.json`, Codex `logs_*.sqlite`. Collector config and state directory are also denied, compared by filesystem identity so case aliases are caught without conflating distinct case-sensitive dirs (`policy.go:34-76`). The single exception is the primary `~/.claude.json`, allowed only through the scrubbed export path (`ExcludesClaudeConfig`, `coordinates.go:28-31`).

### 3.5 Other safety rules

- S-01 Never print credentials, identities or transcripts in logs or status. AGENTS.md; handoff.
- S-02 No installs, host changes, live ingestion or remote cleanup as an incidental action. AGENTS.md.
- S-03 Do not delete a live lock file to bypass single-writer protection. security, README.
- S-04 Original sources, local research, credentials, identities and opt-outs are never deletion targets of the archive reset. contract preamble.
- S-05 Local-file safety: keys reject absolute paths, backslashes, dot traversal, NUL; configured roots and object path components reject symlinks. doc publication-v1 "Local safety".
- S-06 Local `long-data` mapping: logical keys over 768 bytes or components over 240 bytes map to `<root>/long-data/<sha256(key)>`. doc. Local-backend only; irrelevant if the rewrite drops the Local backend except for tests.

## 4. Wire formats: frozen versus free

Because the operator accepts destroying and rebuilding R2, I classify by what a consumer outside the new Rust binary depends on.

### 4.1 Allowed to change (nothing reads old bytes after cutover)

- The whole `capture-v1/...` remote layout, `session-publication/v2` JSON, `epoch_id` and `publication_id` derivations, hex-of-UTF-8 key encoding, object format (`zstd`), and the 8 MiB/1024-source bounds. A fresh archive identity with fresh namespace is required by contract §6.3 and doc ("New archive instances need a fresh backend namespace"). `v3/...` legacy layout is dead.
- The prefix name `capture-v1` and the contract names. The docs already say the prefix is not the wire version. The Rust rewrite may rename but should keep a single versioned contract string and fail loudly on unknown values (contract §4).
- SQLite spool schema. The spool is local and rebuildable only if empty; a Rust writer needs a new schema unless it adopts the Go spool in place. Accepted unpublished bytes in the Go spool are authoritative, so cutover must drain or migrate pending work (Open question 5).

### 4.2 Frozen (outside the wire)

| Item | Why frozen | Evidence |
| --- | --- | --- |
| Machine IDs (Mac, BC-01) | Operator contract: "preserve distinct machine identities"; chosen explicitly, never hostname-derived. A new ID creates a separate namespace. | contract §6.2; `docs/machine-identity.md`; README |
| `machine-id` file beside config (`~/.config/agent-sessions/machine-id`, 0600) and `AGENT_SESSIONS_MACHINE_ID` override semantics | Existing installs read it; sanitization of override preserved "for compatibility" | machine-identity.md |
| Exclusion records `exclusions/<sha256>.json` schema 1 (field names above) and the `record id` formula | Existing opt-outs must be reloaded and still match; changing the ID formula would orphan records and silently re-enable sessions | `exclusion.go:25-34,86-88`; contract §6.2 |
| Learned custom roots | Operator: preserve through rebuild. Where they are stored is in legacy state (not read by me); the candidate takes them from `capture-runtime/v1` `roots` | contract §6.1-2; README |
| `capture-runtime/v1` private config schema | Already installed and running on both hosts, LaunchAgent and systemd unit use it | handoff resume table; `config.go:30-52` |
| Local research DBs and classification evidence | Operator forbids deleting them; Rust must not overwrite | contract preamble; doc implementation |
| Source files and agent hook installs (Claude/Codex/Pi) | They are the data and the trigger. The hook command shape `agent-sessions hook <agent>` and Pi extension file are installed in user agent config | `docs/hooks.md` |

### 4.3 The Python consumer interface (frozen unless Python changes too)

This is the only cross-language dependency. Measured from `tools/analytics/publication_refresh.py` and README:

- Subcommands: `agent-sessions capture-v1 export`, `current-heads` (discovery `remote-current`), `publication-status` (discovery `queue`).
- Export flags include `--config`, `--max-bytes`, identity flags and `--output NEW_DIR`; optional archive/machine flags must match the loaded config. doc backends.
- Export output: `<output>/metadata.json` (the `session-reader/v2` document) written last, plus `bodies/NNNN.bin` files. Absent metadata means incomplete.
- Heads wire `session-publication-heads/v1`; pages of 256; at most 1024 pages per endpoint per sweep; each sweep restarts from the beginning.
- Exit status 4 means `export_too_large`; other nonzero means `process_failed`. `publication_refresh.py:30,116-117`.
- The importer requires `manifest_sha256` in the export metadata, else `reader_digest_missing`. README.
- Python integers must remain exact (ns timestamps, sequence). doc.

Recommendation, motivated by the above: freeze these four JSON documents and the exit code at the CLI boundary. A Rust rewrite then needs no Python change, and Python tests (97 to 117, doc says) become the acceptance oracle. If the operator prefers to change the wire anyway, the Python importer, `publications.py` and its tests move in the same change; the contract §5 demands that anyway.

## 5. Non-functional budgets observed

All values are candidate defaults or observed operating numbers, not accepted budgets. The contract says budgets "still require operating acceptance". Labels as above.

### Limits and defaults (doc says unless noted)

| Item | Value | Source |
| --- | --- | --- |
| Manifest/metadata max | 8 MiB (measured) | `types.go:16` |
| Max sources per manifest | 1024 (measured). Measured real maxima 379 (Mac), 198 (BC-01), doc says | `types.go:17`; phase-3 |
| Source decoded max / encoded object max | 1 GiB each (measured) | `capture.go:21`, `types.go:22` |
| Export default / max | 1 GiB / 1 TiB (measured) | `export.go:13-14` |
| Largest observed real object | 615,165,952 B single object; encoded p50 about 2 KB, p99 about 1.1 MB, max 215 MB Mac and 35 MB BC-01 | publication-v1; phase-3 |
| Encoded spool | doc: 1 GiB example. Handoff live: 8 GiB Mac, 6 GiB BC-01 configured | phase-3; handoff |
| Prepared derived working space | 4 slots x 1 GiB | phase-3 |
| Free-disk reserve | 256 MiB | phase-3 |
| Config input to scrubber | 5 MiB. `scrub.Text` guard 1 MiB (measured) | phase-3; `text.go:28` |
| zstd decoder window | 64 MiB (measured) | `publication.go:324` |
| Discovery timer | 30 s | phase-3 |
| Workers | capture 4, delivery 4; config range 1 to 32 (measured) | phase-3; `config.go:203-204` |
| Operation context | 60 s; delivery and repair add `TransferTimeout` 15 min | phase-3 |
| Metadata and CAS deadline | flat 30 s | phase-3 |
| Object transfer budget | `30 s + size/min_throughput`, clamped 60 s to 15 min, idle 30 s, `min_throughput_kib` default 1024 (unmeasured against live R2) | phase-3 |
| Step bound | `OperationTimeout + sum(2 x TransferBudget(size))` | phase-3 |
| Retry backoff | jittered 1 s to 5 min | phase-3 |
| Quarantine attempts | default 8 (config 1 to 100, measured) deterministic classes only | `config.go:209` |
| Inventory records | live default 4096, max 65536; settled rows ceiling 65536 (about 3.8 KB/row, worst about 250 MB); measured inventories 19k Mac, 29k BC-01 | phase-3 |
| Hint inbox | 1024 hints, each at most 64 KiB | phase-3 |
| Entries per discovery call | at most 4096, divided among roots (config 1 to 4096, measured) | `config.go:212` |
| Directory descriptors / roots / alias routes / leaf aliases | 256 / 128 / 128 / 4096; alias depth 8 (measured for last three) | `coordinates.go:19-28` |
| Config file | 64 KiB (measured) | `config.go:24` |
| Capture debounce | quiet 30 s, max delay 300 s (simulated evidence only) | phase-3 |
| Health target | capture to publication within 60 s of stable observation: a target, not measured | implementation |
| Repair metadata | 4096 rows, 32,768 aggregate histories, 128 per repair | phase-3 |
| Snapshot limits | selected content 64 MiB, evidence 2 MiB, metadata 8 MiB, TAR 128 MiB; per-file 5 MB, 3000 files per scope (legacy doc) | enrichment; storage-layout |

### Observed operating load (handoff resume table, doc says, 2026-11-05)

- Mac coordinator 0.13 cores averaged over 3 h (handoff next-actions say 0.13 to 0.29); BC-01 0.06 cores over 2.5 h.
- Mac refresher about 0.47 cores at `--interval 60`; sweep slowed from about 19 min to about 2 h after 18:38Z; 57k targets. Status degraded. This is the main open performance problem and sits in Python plus subprocess-per-head, not in the Go capture path.
- Mac jobs 21,610 published, 511 superseded; BC-01 47,788 published, 58 superseded. Payloads 2,778 MiB of 8 GiB (Mac), 930 MiB of 6 GiB (BC-01). Queue DB 1,300 MiB (Mac), 1,447 MiB (BC-01).
- Mac queue manifests 578 MiB against about 8 MiB for latest-only because old job rows keep full manifests (quadratic growth with retained epochs). Direct motive for a Rust rewrite storing deltas of manifests or only the latest.
- BC-01 hookless discovery generation about 5 min.
- Epoch growth for rewritten artifacts: Mac `thread_history_1.sqlite` 6 epochs, 1,232 MiB encoded, about 205 MiB per run; `~/.claude.json` 206 epochs in 8 days; two targets exceed the refresher's 768 MiB `--max-bytes`.
- Measured step cost after rebase per delivery: Current 5, Open 6/8/8, PutFile 1/2/2, CAS 1 (doc, `TestRebaseSteadyStateEachCaptureIsOneStep`).
- Gap-register model: before supersession, bytes grew as `E*k/2`; 50 appends gave 50 PUTs and 50 CAS (doc, measured by them as E3). Fixed by R-59.

## 6. Open questions for the operator

1. Wire compatibility. Is a fresh wire format acceptable (new contract string, new key layout) given R2 is rebuilt, or must the Python importer and `capture-v1` CLI JSON stay byte-compatible so no Python change ships with the Rust cutover? I recommend freezing the CLI JSON boundary only (section 4.3). This decides the size of the Rust acceptance suite.
2. Remote-only exclusions. contract §6.2 and security.md require preserving "effective local and remote exclusions". The candidate reads only the local `exclusions/` directory (`config.go:223-234`). Where do remote-only records live today, and should the Rust version still ingest the legacy `v3/machines/<m>/exclusions/*.json` before R2 is reset? The v3 layout (storage-layout.md:277) is legacy-only.
3. Shared-resource over-denial. Should one session exclusion continue to deny all shared multi-session stores (Codex SQLite, `history.jsonl`) in the same root? Handoff lists this as an undecided "whether shared stores should be captured whole at all". Current behavior is safe but blind (about 7,900 denied items on the Mac).
4. Enrichment scope. Is harness/launch/memory enrichment required in the first Rust slice, or is raw transcript plus subagent plus tool-result capture enough? Contract §1 forbids silently dropping custom roots, launches, harness snapshots and DB exports, so dropping it needs an explicit decision.
5. Spool cutover. The Go spool holds accepted, unpublished bytes that are authoritative (contract §2). Should the Rust writer drain the Go queue to R2 before cutover, or read its SQLite schema, or may pending bytes be re-captured from sources (loses vanished sources)?
6. Epoch retention for rewritten artifacts. Open in the handoff (item 4). Latest-only versus all epochs decides manifest size, the 1024 cap, and the `export_too_large` cases. Until decided I treat "retain every unproven rewrite epoch" as the contract (contract §1).
7. Chunking. Is a measured case for delta objects expected, or is full-object plus coalescing final? Affects the Backend trait (range reads, multipart).
8. Agents list. Hard-coded to claude, codex, pi in three places. Is a fourth agent expected?
9. Backends. Should the Rust code keep a Local filesystem backend (with CAS flock, `long-data`, fsync fence) or only R2 plus an in-memory test double? Local is 419 lines (`publication/local.go`) plus extensive durability tests; it exists for tests, fixtures and `capture-v1 fixture`.
10. Coverage statuses. Closed enum or open strings? Doc allows any nonempty producer string.
11. Machine ID bounds. Config allows up to 256 bytes with no NUL/CR/LF; legacy overrides are "sanitised for compatibility" (machine-identity.md). Should Rust reject rather than sanitize?

## 7. Things I could not verify

- Whether Python `publications.py` now enforces the 8 MiB / 1024 bounds and unknown-field rejection (R-103). Gap register GAP-08 says it did not at 2026-11-02; the README says it does. Not cross-read.
- The Local backend (`publication/local.go`), the R2 adapter (`internal/publicationr2`) and actual conditional-write semantics. Doc says R2 conditional behavior is unproven live; only injected HTTP peers were used.
- Exact job state names. I inferred `pending|retry|published|quarantined|superseded` from docs; the SQL uses a TEXT `state` and I did not enumerate every writer.
- Whether I-04 (at most one active epoch per source) is ever violated; code appears to maintain it but no validator checks it.
- Where learned custom roots are persisted in the legacy state and how the candidate imports them (they appear in `capture-runtime/v1` `roots`). Not read.
- Native-evidence inbox record format, hook stdin parsing, Pi extension SDK field names and the launch/harness `snapshot.json` member schemas. Enrichment payload formats are not specified in what I read beyond file names and limits.
- The adapters' key conventions (`SessionKey`, `Key` per agent: Claude `<slug>/<id>`, Codex, Pi tasks/forks/run-N). These define `source_id` and `session_id` content and must be reproduced exactly if a source is to map to the same publication; I read only fragments (`adapters/claude.go`, `codex.go`).
- Current installed health on either host. The handoff is dated 2026-11-05 20:57Z; I did not inspect hosts, per brief.
- Doc claims about test names, CI results, coverage numbers and timings (622 s `make check`, 117 analytics tests). Not run.
- `docs/quality-gates.md`, `docs/session-capture-offline-evidence.md` and `docs/holistic-review-20261029.md`, which I did not read; they may add acceptance obligations.
