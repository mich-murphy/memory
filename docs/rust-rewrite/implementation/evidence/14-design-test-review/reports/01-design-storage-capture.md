# Review 01: storage and capture design (DESIGN.md sections 1-7 + amendments 2026-11-09)

Reviewer scope: read-only. I did not rerun the experiment scripts: `policy_sim.py`, `batch_sweep.py`, `manifest_cost.py`, `piece_compress.py` and `walk.py` read the private transcript corpus under `~/.claude`, `~/.pi` and `~/.codex`, which the brief forbids. I judged them from source. Labels: (a) verified fact, (b) inference, (c) open question.

## 1. Verdict

The shape is sound: immutable content-addressed pieces and manifests, a delta chain with checkpoints, one guarded pointer per owner, verify-by-GET after an ambiguous commit. It is close to how Iceberg commits (atomic swap of a metadata pointer) and Delta (log plus checkpoint). Its protocol-level reasoning is better than most: the lost-reply and 5xx-after-landed-write hazards are real, were reproduced (P2), and are handled.

The weaker part is the evidence layer, not the protocol:

- Every headline cost number comes from one private corpus, with no saved outputs or inputs, so nobody else can rerun it. The simulation replays record timestamps as if they were file-change events.
- The one assumption the whole append model rests on (agents never rewrite the prefix) was never measured. P5 was blocked, then waived (A1).
- Snapshot-class sources (SQLite, config) are absent from every cost model, although R6 claims "bytes proportional to bytes appended".
- Several implementation-time fixes (transformed-byte hashing, adopting the winning encoding after a 412) are not in DESIGN.md. The design text, taken literally, has holes.

## 2. Findings, most severe first

### F1. The append-only assumption is unmeasured and its failure mode is costly and silent for a while. High, confidence high

- Evidence: DESIGN 7.2 states "Sources are assumed append-only", "a rewrite strictly between the windows ... is not detected at capture", and "Until then the latest epoch can reassemble to bytes the file never held". P5, the prototype meant to measure rewrite frequency, is "Blocked measurement" (`prototypes/P5-prefix-rewrite-frequency.md:3`). It was waived by A1, which defers measurement to the running daemon's counters.
- P1 (`prototypes/P1-change-detection.md`) is a synthetic generator whose rates "reflect this arbitrary generator, never agents". The audit in that model "literally substitutes generator current bytes on mismatch", so it proves nothing about the real audit. With 4+64 windows, 307/360 synthetic rewrites are detected, so 53 reassemble wrongly until the audit runs.
- Concrete interleaving:
  1. The agent rewrites bytes in the middle of the file (compaction or edit-in-place), same inode, size growing.
  2. The head and tail windows are unchanged, so the planner returns `Append`.
  3. The manifest's `sha256` is the resumable hasher's state (old bytes [0,N) plus new bytes), so it equals the digest of the reassembled pieces, not of any state the file held.
  4. A reader's digest verification therefore passes on a Frankenstein file. Only the daily audit (11.4) can catch it, up to a day later.
  5. If the source is gone or has rotated before then, the audit cannot recover it.
- Cost side: each mismatch starts a new epoch from byte 0 and `append` class retains every epoch forever (6.4). The design measured whole-object re-upload at 10.7x on Claude history and 155x on Pi history (3.1). If any agent rewrites on every turn, the epoch-replay path reproduces roughly that cost, with no GC to recover it.
- Action: do not treat P5's waiver as closing the risk. Before cutover, run the replay and window-mismatch counters on a shadow period and set a numeric tripwire (replay bytes divided by append bytes). Alternatively persist a per-capture rolling hash of a few interior sample blocks. Make the manifest `sha256` a digest the audit independently verifies at commit time for small files.
- Open question: how often do Claude, Codex and Pi rewrite their JSONL? Experiment: hash [0,N) per file at two times, hours apart, on names-only metadata (path, N, digest), no content. Count files where the digest changed with the size unchanged or grown.

### F2. Snapshot-class and multi-source costs are unmodelled, so R6 is not shown. High, confidence medium

- R6: "Bytes uploaded are proportional to bytes appended, for both content and manifests." Every experiment covers only JSONL transcripts (`policy_sim.py:4-5` globs Claude and Pi transcripts; `batch_sweep.py` and `manifest_cost.py` add Codex rollouts). `SourceKind` has 19 variants (DESIGN 5). Codex SQLite stores, histories, todos, tasks, file-history, debug and configs are snapshot-class: `replace` re-uploads the whole object on any fingerprint change (6.3, 7.2). SQLite backup staging copies every page each time.
- No file under `docs/rust-rewrite/` measures snapshot bytes. I grepped the experiments and prototypes and found none. `manifest_cost.py` assumes one source per session (`PIECE=220`, `HEAD=600` are constants, not measurements). DESIGN 3.7 concedes it "undercounts sessions with several sources". Checkpoints list every epoch of every source, and a session may hold up to 1,024 sources (6.2), so checkpoint size and rewrite cadence are unmodelled.
- Inference (b): a hot Codex store in WAL mode changes on nearly every turn. Whole-snapshot upload every window could dominate the transcript bytes by an order of magnitude.
- Action: measure bytes per day of snapshot-class sources (sizes and change frequency from stat only, no content) before slice 3. Add a snapshot policy if needed (minimum interval, size cap).

### F3. The cost and lag numbers rest on a simulation that replays record timestamps; none of it can be rerun. Medium-high, confidence high

- (a) `policy_sim.py`, `batch_sweep.py`, `manifest_cost.py` and `piece_compress.py` build "events" from each record's own `timestamp` field plus byte offsets, then simulate quiet and cap windows on those event times. That is event time standing in for the processing times a real hook or FSEvents would deliver. FSEvents coalescing latency, daemon scheduling, upload time and the 15-minute reconcile fallback are not modelled.
- Records without a parseable timestamp are skipped for window logic but still advance the byte offset (`off+=len(line)` before the parse).
- No outputs are committed (grep for "44,732" and "20.25" finds only quotes in DESIGN-adjacent docs). The scripts have no corpus snapshot or seed, so the tables in 3.1, 3.2 and 3.7 are not reproducible by anyone, including the operator after the sessions rotate. DESIGN section 0.4 says "Rerun a script before changing a number here." The scripts cannot be rerun to the same result.
- Internal inconsistencies:
  - 3.1's heading says "quiet 60 s, cap 300 s" but also "30 s window", and the code has `Q=30`. The "max pieces 235 / 1,700" matches the 30 s row of 3.2, not the 60 s default.
  - 3.3 (compression penalty) was measured at 30 s. The chosen default is 60 s, so the penalty at the default is not measured.
  - The 3.2 lag model caps capture time at the next event (`ct=min(ct,t)`) and ignores upload time and the 15 min reconcile.
- Piece compression (3.3) is n=4 Claude sessions picked at quantiles of size. No variance, no Pi or Codex.
- 3.1 row "Append piece per capture 1.0x" is true by construction (the sum of deltas equals the file size), so it is not evidence.
- Action: commit anonymised per-session aggregate inputs (offset and time series only, no content) so the numbers can be recomputed. Re-run at 60/300 including a stat-based file-change time. Record the corpus size and date with each table.

### F4. DESIGN text leaves the 412 and encoding rules open; the implementation resolved them, the design did not. Medium, confidence high

- DESIGN 7.3 step 2 hashes the raw bytes, then step 3 transforms (scrubs), step 6 PUTs `objects/<raw sha256>.zst` and says "A 412 means the object is already stored, which is success" (6.2).
- Read literally this is a hazard. For protected kinds the key is the hash of the unscrubbed bytes while the stored bytes are scrubbed. After a scrub-rule fix (`RulesChanged` replay), the raw hash is unchanged, so the new epoch's PUT gets a 412 and the stale scrubbed output remains under that key. The fix never reaches R2.
- The Rust code avoids this: `Piece::from_authorized` hashes the transformed bytes (`rust/crates/memory-core/src/manifest/schema.rs:~335`, doc "raw (transformed, unencoded)"). It also adopts the winner's encoding after a 412 (`adopt_stored`, `schema.rs:~385`, `commit.rs:241`).
- DESIGN 6.2 still says a zstd upgrade "changes no keys", true, but two hosts with different libzstd can store different encodings under one key. That is why `encoded_sha256` must come from the stored object. The design does not say how the shell learns it (an extra GET or HEAD per 412).
- (b) The `encoded` encoding is currently the identity ("until zstd lands").
- Action: amend DESIGN 7.3 and 6.2: the key is the hash of the bytes after the transform; on a 412 the descriptor adopts the stored object's encoded digest. State the cost (one extra request per dedup hit).

### F5. Same-machine late write after a restart is parked as `Foreign`. Low-medium, confidence medium

- Interleaving:
  1. Process P1 sends PUT `current.json` with If-Match E0 (manifest M1), then is killed or the network stalls.
  2. launchd restarts the daemon (P2). `recover_base` GETs `current.json`, which still names M0 with ETag E0.
  3. P2 builds M2 (previous = M0) and sends PUT If-Match E0.
  4. P1's delayed request lands between P2's GET and PUT, so current names M1 with ETag E1.
  5. P2's PUT returns 412; the verification GET shows M1, which is not M2, not in the in-memory `known_ours` (lost at restart), and not previous (M0). The verdict is `Foreign` and the owner is parked.
- A6 deliberately keeps `known_ours` in memory only and the test harness drops the late write at restart (`rust/crates/memory/tests/daemon.rs:1345`: "A write still in flight from the dead process dies with its connection"). A request already received by Cloudflare does not die with the client socket. How late a request can land after a kill is unmeasured; P11 measured 28/28 stale-ETag 412s, not post-kill landing.
- The owner key contains the machine id and `daemon.lock` rules out two live daemons, so on this key space "foreign" can only be a clone or a late self-write. Parking is the wrong default for the second.
- Action: classify a `current.json` manifest whose `owner` and `writer` stamp match this machine and whose `seq` is exactly previous+1 as adopt-and-follow-up. Persist a writer-instance id per run if distinguishing clones matters. Or probe how long a killed request can land on real R2.

### F6. Timeout formula versus the 8-way concurrent upload. Low-medium, confidence medium

- 7.3: timeout = 30 s plus size at 1 MiB/s, floor 60 s, with up to 8 concurrent 8 MiB uploads. On a link whose total uplink is below about 1 MiB/s (hotel Wi-Fi, tethering), eight uploads share it: each needs 64 s or more against a 60 s limit. All time out together as `Offline`, then retry together. A6 adds jitter, which helps, but the cap and concurrency never scale to measured throughput.
- P4's "135.1 MB RSS at 8 MiB / 8 encoded buffers" (`00-scope.md`) is above the "at most 80 MiB" target in 7.3. The target counts buffers only; the process base is excluded. P4 is a Python and C analogue and explicitly "NOT" acceptance.
- Action: derive concurrency from observed throughput, or size the timeout against the total in-flight bytes.

### F7. Schema strictness and rollback. Medium-low, confidence medium

- 6.5: `deny_unknown_fields` on read, version in the `schema` string. An old writer meeting a newer manifest in its own chain parks the owner (`NewerRemoteSchema`). Inference: after a bad release is rolled back, every owner whose chain head was written by the newer version parks until a human unparks or the newer binary returns, so capture stops host-wide. Delta (PROTOCOL.md, "Readers SHOULD NOT make any assumptions about the existence or frequency of checkpoints" and versioned reader/writer features) and Iceberg gate on declared versions and features, not on field presence. The design has no additive-compatible minor revision. Strict-and-park is defensible for a single operator but leaves rollback undefined.
- Action: allow an additive `schema` minor (reader tolerates unknown fields when the minor is greater), or write a rollback runbook.

### F8. Global `objects/` namespace versus session-local publication and the stated privacy remedy. Medium-low, confidence medium

- The contract goals kept by DESIGN include "session-local publication". 6.2 chooses a global keyspace, justified by "Sessions are never deleted". 8.6 rejects bucket locks so that "a leaked secret can be removed after the fact". Removal needs: (1) the owner's chain, from `current.json` (fine); (2) pieces shared with other sessions, which cannot be told apart without scanning every manifest; (3) orphaned objects and manifests from failed or abandoned commits (A6: up to 4 rebase retries each leave an orphan manifest), which have no owner and are found only by listing everything. No GC or reverse index is designed.
- P8 (dedup prevalence) is blocked, so the benefit of global keys is unmeasured (`prototypes/P8-cross-session-duplicates.md`).
- Action: measure P8 on hash metadata only, or scope keys per owner and accept duplication of small configs.

### F9. Design text and numbers that do not match. Low, confidence high

- R1 says 14 source kinds; `SourceKind` has 19 variants (DESIGN 5; Claude 10, Codex 5, Pi 4).
- 3.5 "Multipart has 5 MiB minimum parts and no conditionals": the R2 API page (https://developers.cloudflare.com/r2/api/s3/api/) says nothing about a minimum part size and lists no conditional headers on multipart calls; the limits page (https://developers.cloudflare.com/r2/platform/limits/) gives only the 10,000-part maximum. Unverified from my sources; multipart is unused, so this is immaterial.
- 3.5 quote "R2 allows one concurrent write per second to the same key and returns 429 above that" is confirmed by the limits page ("1 per second"; "Concurrent writes to the same object name (key) at a higher rate return HTTP 429").
- DDIA citations use "2nd edition" chapter numbers; I could not fetch the book and did not verify the quoted passages. The challenge synthesis relies on the operator's copy.
- `If-Match` ETag is a compare-and-swap, not a fencing token in DDIA's sense (a monotonically increasing token). Functionally sufficient for one writer per owner; the DESIGN 3.6 wording is loose.

## 3. Claim-to-evidence table (sections 2-7)

| Claim | Evidence | Assessment |
|---|---|---|
| R1: 3 of 14 kinds hooked, 13 seen by FS | `reports/09-repo-hooks-vs-scan.md` | Review report, not rerun. Kind count disagrees with `SourceKind` (F9). |
| R6: whole-object 10.7x Claude, 155x Pi | `policy_sim.py`, 30 s window | Corpus-specific, not rerunnable, transcripts only (F3). Table caption says 60 s, code uses 30 s. |
| R6: append = 1.0x | `policy_sim.py` | Tautological; says nothing on snapshot-class (F2). |
| 11.4% vs 0.7% manifest cost | `manifest_cost.py` | Piece and header sizes are assumed constants; one source per session. Quadratic effect corroborated by challenge/02 M2 (87 MB for 898 pieces), synthetic. |
| 60 s / 300 s window, 82,443 PUTs, lag 145 s | `batch_sweep.py` | Event-time simulation; PUTs are 3 per capture by construction (pieces above 8 MiB, GETs, heartbeat, snapshot sources not counted). |
| Piece compression penalty 1.01-1.40x | `piece_compress.py` | n=4 Claude sessions, 30 s windows, no variance (F3). |
| Host costs: stat walk 360 ms, SHA-256 19 MB 70 ms | `walk.py` and shell timings | Python on one Mac. The Rust cost is not measured. Plausible. |
| Spawn 3.0 ms, O_APPEND 16x2000, flock EWOULDBLOCK | challenge reports 06 F5, 03 m1, 03 B1 | Single reviewer runs; the O_APPEND test is APFS only, no Linux. |
| R2 conditionals atomic; strong consistency | [vendor] | Confirmed: https://developers.cloudflare.com/r2/reference/consistency/ ("Strongly consistent"; the condition is "evaluated against the committed state of the bucket at the moment the write commits") and the S3 API page. P2 and P11 reproduced it on real R2 (28/28 stale ETags rejected, 60/60 two-writer races with one winner). Good evidence. |
| `object_store` resends a landed `current.json` write | P2 on a fake server (`[200, 412]` server-side) | Reproduced on the fake only; P2's own note says it did not reproduce on real R2. Fine as a hazard proof. |
| Window checks 4 KiB head + 64 KiB tail | P1 synthetic | Generator-defined rates, no real agent evidence (F1). |
| Agents are append-only | P5 | Blocked then waived: no evidence (F1). |
| 8 MiB piece cap, 8 uploads, 80 MiB peak | P4 (C and Python analogue) | Self-declared not acceptance; RSS 135 MB at 8/8 (F6). |
| Monotonic clock, 120 s wake gap | P6 | Simulation with independent clock inputs; never calls Rust `Instant` or sleeps a host. Whether macOS `Instant` excludes sleep is unverified from my side (from memory: it uses a monotonic clock that stops in sleep). |
| Delta every capture, checkpoint every 64 | 3.7 plus Delta analogy | Interval 64 chosen from the cost model; no sweep of the interval. Reader cost, at most 64 serial manifest GETs, is not measured. |
| Global `objects/` namespace | decision plus P8 | P8 blocked (F8). |
| Commit protocol, verify by GET, `AlreadyOurs` | P2, P11 (real R2 889 requests plus a seeded simulation) | Strongest evidence in the design. Simulation seeds and the Go probe are committed (`prototypes/p11-commit-retry/`). |
| Durable state writes | P12, APFS | Linux not measured (BC-01 unreachable). |
| Reader walks `current` to checkpoint to deltas | `memory-reader/src/lib.rs` is 3 lines | The acceptance chain (writer, reader, importer) is untested: no reader code exists yet. |

## 4. Comparison with established practice

- Delta Lake PROTOCOL.md (https://github.com/delta-io/delta/blob/master/PROTOCOL.md): commits add "a new entry to the log"; "Checkpoints for a given version must only be created after the associated delta file has been successfully written"; `_last_checkpoint` lets readers avoid listing; "A catalog must not ratify version v until it has ratified version v - 1". (The page was truncated at 100k characters, so I did not read the section on put-if-absent mutual exclusion; "unverified" for that detail.) The design's checkpoint-every-64 matches the shape. Difference: Delta's commit is the conditional create of a version-numbered file, with no separate mutable pointer, so a retried commit is resolved by reading that one key. The design uses a content-addressed manifest plus a mutable `current.json`, which creates the ambiguous-commit problem that 7.4 step 5 then solves with verify-by-GET. The pointer saves listing but costs the verify step and the `Foreign` / `known_ours` machinery. The deviation is justified for a single writer per owner, but the trade-off is not written down. A `manifests/<owner>/<seq>` key with `If-None-Match: *` would make the commit idempotent by key and remove the F5 class; the reader would then need the head via a hint or a LIST (R2 lists are strongly consistent).
- Iceberg spec (https://iceberg.apache.org/spec/, Optimistic Concurrency): "An atomic swap of one table metadata file for another provides the basis for serializable isolation." The design's `current.json` If-Match swap is the same idea. Iceberg additionally merges or retries on conflict; the design refuses to merge ("two hosts' piece lists for one source do not commute"), which is correct given per-owner single writership.
- R2 (consistency and API pages above): conditional PutObject is atomic, strong read-after-write and list consistency, and the 1 write/s per-key limit. The design's use (If-None-Match for immutables, If-Match for the pointer, no multipart) is within the documented behavior. The "not final NoSuchKey" nuance (challenge/02 B2) I did not re-verify.
- Content addressing: git and restic key by content hash (from memory, unverified in this session). The design's "global namespace" gives dedup but needs GC and a reverse index for deletion (F8).
- DDIA chapters 11-12 passages: not fetched; unverified, from memory only at the level of "idempotence needs unique request ids, fencing for concurrent writers, checkpoints for stream processors".

## 5. Correctness hazards checked

- Two machines writing to one bucket: owner keys include the machine id, so no shared mutable key between hosts. Shared hazards are only `objects/<sha>` (idempotent, a 412 is fine; a possible 429 for identical concurrent PUTs) and the `shared/` owner (per machine). Sound. A session file copied to the other host creates two owners; how the one authoritative reader merges or deduplicates them is not specified (c).
- Manifest and checkpoint races: pieces, then manifest, then pointer. A crash leaves unreferenced objects only. Readers see a consistent chain thanks to strong consistency. Sound. A checkpoint is full-state at `seq % 64 == 0`; `recover_projection` enforces `seq`, `previous` and `checkpoint` links (`recovery.rs:~150`).
- Partial uploads: single-PUT up to 8 MiB, no multipart; atomic. Sound.
- Clock: windows on monotonic, `captured_at` unused for ordering. Sound. The racy-mtime check uses wall time against file mtime; a backward wall step makes mtimes appear future, which the design should treat as racy (not stated).
- Listing cost: readers never list `objects/`. Enumeration lists `sessions/` (one `current.json` per session, cheap). "Writer-stamp" bug hunts need a LIST and GET of all manifests (acknowledged in 6.3). Scalability beyond the 4.6k-session corpus is not tested; R2 prefix hot-spot behavior unverified.
- Replays after crash: sound apart from F5. `recover_base` reads the store on every start, so a crash between PUT and index update is handled.
- Unbounded growth: no GC; every epoch of every append source retained; checkpoints grow with total pieces. Acknowledged, deferred.

## 6. Domain model (section 5) and schema practice

Sound:
- Newtype ids, `Owner` as an enum (shared stores have no session), `Plan`, `Disposition` and `CommitOutcome` as closed enums, `Authorized<Redacted<_>>` as a capability type so the store cannot accept unscrubbed bytes, `Piece` with private fields and a compile-fail doctest (`schema.rs:~295`). Reasons are enums, not strings.
- Schemas: `serde` + `schemars` with committed generated JSON Schemas and a drift check; versioned `schema` string; duplicate-key rejection called out.

Gaps:
- Illegal states still representable in the sketch: `SourceState` holds `captured_to`, `head`, `tail` and `whole` for every source, including snapshot-class (which uses a fingerprint, not windows), and `epoch: u32` with `trust` is independent of `id`. A split by `SourceClass` (`AppendState` / `SnapshotState`) would remove the unused fields. `Birth::Unavailable` is a representable but untested identity weakness.
- The wire format is flat JSON with per-op optional fields (for example a `deleted` change has no `kind`, `class` or `epoch`); the code splits it into variants, but the schema documentation does not state which fields each op requires. "No optional-field bags" (section 5) is true of the Rust types, not of the wire.
- Manifest invariants (pieces contiguous, last offset + len equals `size`, `epoch.n` monotonic, `seq` and `previous` linkage) are enforced in code, but DESIGN 6.3 lists them only partly. The reader contract should state them.
- A newer schema is treated as a hard stop (F7).
- Withheld sources are not recorded in R2 (6.3). That is a privacy choice; it also means a reader cannot tell "never captured" from "withheld".

## 7. What is sound

- Commit protocol and its evidence (P2, P11). Idempotent immutable writes, a single guarded pointer, verify-by-GET with the manifest hash as the request id. Matches the Iceberg swap idea and real-R2 measurements.
- Delta manifests plus checkpoints, bounded reader walk, the 64-manifest bound.
- Fail-closed privacy typing at the store boundary.
- The explicit system model (11.1) and the honest statement of what the window check does not catch.
- Rejecting merge on conflicts.

## 8. Open questions and experiments

1. Prefix-rewrite frequency (F1): metadata-only shadow measurement; settles whether replay cost is negligible.
2. Snapshot-class bytes per day (F2): stat-only sampler over a week, no content.
3. Late-landing of a killed PUT on real R2 (F5): send a PUT with If-Match, kill the client at once, poll `current.json` for 60 s; measure how long landing can lag.
4. Reader cost at 64 deltas and at 1,000 sources: build a synthetic chain against a scratch prefix and time restore; also checkpoint size at 1,024 sources.
5. Dedup share (P8): per-piece hash counts from names-and-hashes only.
6. Does `Instant` exclude sleep on this macOS: sleep the laptop 3 minutes and compare the monotonic and wall deltas (settles P6's pending natural-sleep case).
7. Cross-host duplicate session handling in the reader (c).

## 9. Not covered

I did not read sections 8-20 beyond the parts cited (privacy, signals, failure handling skimmed), the full challenge reports, or the Rust implementation beyond spot checks. I did not run any cargo command, rerun any script, or contact R2. DDIA and the git and restic practice claims were not fetched. I fetched only Delta (truncated), Iceberg (partial) and the three Cloudflare R2 pages. I did not verify Rust `Instant` semantics or the "not final NoSuchKey" nuance.
