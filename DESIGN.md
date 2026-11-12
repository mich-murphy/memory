# Session capture in Rust: design and specification

Status: approved direction 2026-11-07, revised and final 2026-11-07 after the challenge
review in `docs/rust-rewrite/challenge/`. This is the authoritative design for the Rust
rewrite. For the replacement only, it supersedes the spool and work-queue shape in
`docs/session-capture-contract.md`. The contract's goals still hold: session-local
publication, immutable evidence, fail-closed privacy, at-least-once idempotent writes and
one authoritative reader. The mechanism is replaced by the one below. Operator decisions,
corrections of fact and measured results of 2026-11-09 to 2026-11-12 (amendment ids A0 to A14)
are folded into the text below.

## 0. Start here in a fresh session

1. Read this document in full. Then read `AGENTS.md`, then `docs/session-capture-contract.md`
   for the goals it keeps. Do not take implementation direction from the contract's
   mechanism sections, the handoff's Go operating steps or the Go candidate code. They
   describe the legacy and candidate Go paths. The Go capture daemons and the publication-refresh job
   are stopped (A0). Only the Go hooks remain, writing to a local spool, until cutover (section 18
   step 2). Two Go
   areas are deliberate exceptions. The secret protections in section 8 are ported from Go
   code and its tests, and the edge cases in section 17's slice 1 table come from the Go
   history.
2. Why each part is the way it is: `docs/rust-rewrite/challenge/00-synthesis.md` (verdict,
   blockers, majors, operator answers) and `07-ddia-crosscheck.md` (textbook basis). Section
   20 maps every challenge finding to the section that answers it.
   `docs/rust-rewrite/design-amendments-20261109.md` is the dated record of why each later change
   (A0 to A14, 2026-11-09 to 2026-11-12) was made. It is folded into this document, which wins.
   Read it for the reasoning and evidence behind a change, not to learn the design. Amendment ids
   in parentheses mark the changed places.
3. The Rust workspace goes in this repository at `rust/`, beside the Go code. The Nix dev
   shell gains a pinned Rust toolchain in slice 0. `rustc` 1.98.0 is on this Mac outside Nix.
4. The evidence for every number is in `docs/rust-rewrite/`: the nine reviewer reports, the
   six challenge reports, the DDIA cross-check, the experiment scripts and the Hegel probe.
   Rerun a script before changing a number here.
5. Begin with slice 0 (section 17) and the prototypes in section 16. The first gate task is
   the Hegel build on BC-01. Prototypes that need R2 use the `rust-acceptance/<run-id>/` prefix of
   the existing bucket (section 6.1).
6. Two earlier HTML artifacts from the design conversation are superseded by this document
   and must not be used: "Rust Rewrite Design" and "Capture Path Design".

Every claim is labelled:

- **[measured]**: a script in `docs/rust-rewrite/experiments/` produced it on this Mac on
  2026-11-07.
- **[review]**: a challenge reviewer measured or read it. The report is cited, and the
  parent did not rerun the measurement.
- **[vendor]**: read that day from the vendor's documentation or source.
- **[DDIA]**: a passage of *Designing Data-Intensive Applications*, 2nd edition, supplied by
  the operator on 2026-11-07. Chapters 11 and 12 are quoted in section 3.6. Chapters 4, 6, 8,
  9, 13 and 14 are applied in `challenge/07-ddia-crosscheck.md`.
- **[target]**: a number we chose and will assert in tests.
- **[decided]**: an operator decision, dated.
- **[default]**: a default adopted by the parent. It applies unless the operator says
  otherwise, and implementation does not wait on it.
- **[prototype]**: settled by a measured prototype in section 16 before the slice that needs it.

## 1. Scope

In scope:

- Observing agent session sources on a host, proving what changed, and writing it to R2 so
  that the bytes, their order and their provenance can be recovered.
- A capture ends when the content objects, the manifest and the guarded `current.json` are
  durable.
- A minimal reader in slice 2b: a `memory-reader` library, `memory restore`, and an export to
  the existing `session-reader/v2` envelope for the analytics importer. Acceptance needs
  writer, reader, importer, event index and report (`AGENTS.md`). [decided 2026-11-07]

Out of scope:

- Analytics redesign, retention and garbage collection, and enrichment.
- Any migration, import or compatibility with data or layouts in the existing R2 archive. None
  of the current R2 data is kept. The Rust writer starts with a fresh archive id in the existing
  bucket, and the old archive was deleted on 2026-11-09, before any Rust write (section 18).
  [decided 2026-11-07; A2]

Hosts: this Mac (FSEvents) and BC-01 (inotify, kernel 7.0.0-1017-azure). Both write to one
shared bucket, so every session can be seen in one place. [decided 2026-11-07]

## 2. Requirements

R1. Capture every source kind the contract names (14 kinds across Claude, Codex and Pi)
without silent drops. Hooks announce 3 of them, filesystem notifications see 13, and launch
context is hook-only. [measured, `docs/rust-rewrite/reports/09-repo-hooks-vs-scan.md`] Any
source that is withheld or cannot be attributed is counted and shown by `memory status`.

R2. Fail closed on privacy. No byte leaves the host unless it is an
`Authorized<Redacted<Bytes>>`, which only the protected-source pipeline in section 8 can
construct. Unknown policy state is an error, never a default allow.

R3. Integrity over timeliness. A wrong or missing byte is a defect. A lag of minutes is not.
CDC "is usually asynchronous" and "all the issues of replication lag apply". [DDIA ch. 12,
Implementing CDC] The lag is still quantified: the primary health metric is the age of the
oldest uncaptured change (section 11.3). [DDIA ch. 6, Monitoring staleness]

R4. Idempotent and convergent. Any operation may run twice or crash at any point, and the next
run converges to the same R2 state.

R5. Lightest footprint on the host. The measures are: the hook's in-process time, CPU per
capture, wakeups per idle minute, resident memory and disk. Uploads happen continually. Reads
happen daily, so read cost may be higher.

R6. Bytes uploaded are proportional to bytes appended, for both content and manifests.
Re-uploading whole objects costs 10.7x on Claude history and 155x on Pi history. [measured,
`policy_sim.py`, 30 s window (A10.1)] A full manifest rewrite on every capture costs 11.4% of transcript bytes.
[measured, `manifest_cost.py`]

R7. Strict, versioned schemas for everything we write. Tolerant key reads of what agents write
to us.

R8. Human-readable code. Prefer well-known crates over our own mechanisms. Complexity must be
justified by a measurement in this document.

R9. Stored content is plaintext behind bucket access control. Section 8.6 gives the threat
model. [decided 2026-11-07]

## 3. Evidence

### 3.1 Upload policy, quiet 30 s, cap 300 s [measured, `policy_sim.py`, 30 s window, not measured at 60 s] (A10.1)

| Policy | Claude 945 sessions 1.90 GB | Pi 3,650 sessions 2.07 GB | Max pieces |
|---|---:|---:|---:|
| Whole object per capture | 20.25 GB, 10.7x | 320.97 GB, 155.3x | 1 |
| Append piece per capture | 1.90 GB, 1.0x | 2.07 GB, 1.0x | 235 / 1,700 |
| Append pieces, re-upload whole past 64 | 1.95 GB, 1.0x | 6.98 GB, 3.4x | 64 |

### 3.2 Window sweep, all agents, 4,621 sessions, 599,697 records [measured, `batch_sweep.py`]

| Quiet, cap | Captures | PUTs | Pieces p99, max | Lag mean, p99 | Piece p50, p99 |
|---|---:|---:|---:|---:|---:|
| 30 s, 300 s | 44,732 | 134,196 | 87, 1,700 | 92 s, 298 s | 31 KB, 842 KB |
| 60 s, 300 s | 27,481 | 82,443 | 51, 898 | 145 s, 322 s | 59 KB, 1.3 MB |
| 120 s, 600 s | 16,310 | 48,930 | 35, 389 | 294 s, 643 s | 116 KB, 2.0 MB |

Bytes are identical across windows. Only requests, piece count and lag move.

### 3.3 Per-piece compression penalty, zstd level 3 [measured, `piece_compress.py`]

| Session size | Pieces | Penalty vs whole-file zstd | Whole-per-capture cost |
|---|---:|---:|---:|
| 669 KB | 4 | 1.40x | 2.7x |
| 5.0 MB | 53 | 1.19x | 29.9x |
| 29.5 MB | 44 | 1.01x | 19.7x |
| 35.6 MB | 52 | 1.01x | 31.7x |

### 3.4 Host costs [measured, `walk.py` and shell timings, unless marked]

| Operation | Cost |
|---|---:|
| Stat walk, 24,127 files, 14,947 dirs, Python | 360 ms |
| 30,000 hash-map lookups | 1 ms |
| SHA-256 of 19 MB | 70 ms |
| zstd -3 of 19 MB | 30 ms |
| zstd -3 plus SHA-256 of 103 MB | 190 ms |
| Hegel: cold build, 2 property tests, static engine | 16 s build, 0.07 s run |
| Spawn of a trivial release Rust binary from Python, 500 runs [review, 06 F5] | p50 3.02 ms, p99 10.80 ms |
| Spawn of `/usr/bin/true`, same method [review, 06 F5] | p50 2.06 ms, p99 2.62 ms |
| `O_APPEND`, 16 writers x 2,000 lines, one `write(2)` each, 200 B to 1 MB, APFS [review, 03 m1] | 0 torn or interleaved |
| Write after `unlink` of a renamed hint file [review, 03 M1] | succeeds and is lost |
| Second `flock(LOCK_EX\|LOCK_NB)` on a held lock [review, 03 B1] | EWOULDBLOCK (errno 35) |
| Same-size rewrite in a tight loop, APFS [review, 01 M2] | 0 of 2,000 kept equal (size, mtime_ns) |
| SQLite backup API of an unchanged WAL source, three runs [review, 01 M3] | identical digests |

### 3.5 R2 and client facts [vendor]

- PUT conditionals `If-Match` and `If-None-Match` are evaluated atomically with the write.
  Writes and lists are strongly consistent. A `NoSuchKey` after a lost reply is "not final".
- There is no append operation. Multipart has 5 MiB minimum parts and no conditionals.
- R2 allows one concurrent write per second to the same key and returns 429 above that.
  [review, 02 m3]
- Prices: Class A operations 4.50 USD per million, Class B 0.36 USD per million, storage
  0.015 USD per GB-month. Egress and deletes are free. Keys are at most 1,024 bytes.
- Encryption at rest with AES-256-GCM is always on. SSE-C and bucket locks with prefix
  retention rules exist. Neither is used (sections 8.6 and 19).
- Long-lived API tokens scope only to buckets. Temporary credentials can be scoped to prefixes
  and actions, but minting them needs the parent secret.
  (https://developers.cloudflare.com/r2/api/tokens/,
  https://developers.cloudflare.com/r2/api/s3/temporary-credentials/)
- `object_store` 0.14.2 has no Content-MD5 attribute on put (`src/aws/client.rs:374-388`).
  `PutMode::Update` sets `retry_on_conflict(true)`, and the client resends after 5xx, 429 or
  408 without checking idempotence (`src/client/retry.rs` about 405-412,
  `src/aws/mod.rs` about 274-300). A commit that lands and then gets a 5xx is resent with the
  old ETag, and the resend returns 412. Section 7.4 handles this.
- `notify` 8.2.0 hard-codes `kFSEventStreamEventIdSinceNow` (`fsevent.rs:299`), so it cannot
  replay FSEvents history across restarts. FSEvents drop flags and inotify `IN_Q_OVERFLOW`
  both map to `need_rescan()`. 8.2.0 adds watches for a new directory without reporting files
  already inside it. [review, 03 M2, M3]

### 3.6 Textbook basis [DDIA, chapters 11 and 12]

Each row is a passage from the supplied chapters and the decision it fixes. Chapters 4, 6, 8,
9, 13 and 14 are applied to the challenge findings in `challenge/07-ddia-crosscheck.md` and
cited where they change a section.

| Passage | Decision |
|---|---|
| "polling becomes expensive if the datastore is not designed for this kind of usage. The more often you poll, the lower the percentage of requests that return new events ... it is better for consumers to be notified when new events appear." (ch. 12, Transmitting Event Streams) | The Go 10 s walk was polling. Hooks and filesystem notifications replace it as the driver. |
| "A log is simply an append-only sequence of records on disk ... a consumer receives messages by reading the log sequentially. If a consumer reaches the end of the log, it waits for a notification that a new message has been appended." (ch. 12, Using logs for message storage) | The hint log. Hooks append, the daemon tails. |
| "the broker does not need to track acknowledgments for every single message but only to periodically record the consumer offsets ... This can cause the consumer to see some messages twice." (ch. 12, Consumer offsets) | One hint offset per day file. Duplicate hints are harmless because a hint only says "look here". |
| "To reclaim disk space, the log is divided into segments, and from time to time old segments are deleted" (ch. 12, Disk space usage) | One hint file per day is a segment. Old segments are deleted. No rename. |
| Direct messaging systems "generally assume that producers and consumers are constantly online. If a consumer is offline, it may miss messages" (ch. 12, Direct messaging) | A unix socket from hook to daemon is direct messaging. Removed in favour of the log. |
| "dual writes have serious problems ... one of the writes may fail while the other succeeds" (ch. 12, Keeping Systems in Sync) | No local job table beside R2. The file is the leader, R2 the follower, the index a cache. |
| "CDC makes one database the leader ... and turns the others into followers" (ch. 12, Implementing CDC) | We are the CDC connector for the agents' JSONL. We never write to their files. |
| "The snapshot of the database must correspond to a known position or offset in the change log" (ch. 12, Initial snapshot) | An epoch's base pieces are bytes [0, N), and the manifest records N with the whole-file digest at N. That is the position a rebuilt index adopts (section 9.3). |
| With event sourcing "later events typically do not override prior events, so you need the full history of events" (ch. 12, CDC and schemas) | Append-class sources keep every epoch. The writer never compacts. |
| Log compaction "keeps only the most recent update for each key" (ch. 12, Log compaction) | Snapshot-class sources keep the latest epoch only in checkpoints. |
| "CDC systems typically use the upstream database's schema when replicating its data, which turns these schemas into public APIs" and outboxes "decouple internal from external schemas" (ch. 12, CDC and Database Schemas) | The writer never parses transcript content. The manifest is our outbox schema, strict and versioned. Protected config files are the only content the writer parses, in order to scrub them (section 8). |
| "There is no fundamental need to keep a database at all; the log contains all the information there is." (ch. 12, State, Streams, and Immutability) | The index exists for retrieval performance only and is rebuildable. |
| "objects are immutable once written. To update an object, it must be fully rewritten" and renames "are nonatomic" (ch. 11, Object Stores) | Immutable content-keyed pieces and manifests. The only mutable object per owner is `current.json`. |
| Object stores "are generally optimized for large objects ... and less frequent, larger reads" (ch. 11, Object Stores) | The 60 s window rather than 30 s: pieces are p50 59 KB rather than 31 KB, with half the requests. |
| "any change to the input data, even a single byte, requires the batch job to reprocess the entire input dataset" (ch. 11, intro) | Whole-object upload per capture is that inefficiency. Append pieces and delta manifests are the fix. |
| "upon completion batch jobs must send a notification to downstream systems that their job is done" (ch. 11, Serving Derived Data) | `current.json` is that notification. Readers follow current to manifest to pieces and never list `objects/`. |
| "A session window has no fixed duration ... the window ends when the user has been inactive for some time" (ch. 12, Types of windows) | The coalescer is a session window on processing time. |
| Microbatch size "is the result of a performance compromise" (ch. 12, Microbatching and checkpointing) | Section 3.2 is that trade-off measured. |
| "many stream processing frameworks use the local system clock on the processing machine ... it breaks down with any significant processing lag" (ch. 12, Event time versus processing time) | Processing time is used only to batch. Readers use the records' own timestamps. |
| Checkpointing: "restart from its most recent checkpoint and discard any output generated between the last checkpoint and the crash" (ch. 12, Microbatching and checkpointing) | `current.json` is the checkpoint. Pieces and manifests written before a crash and never referenced are discarded output. |
| An operation "can often be made idempotent with a bit of extra metadata" and "fencing may be required" (ch. 12, Idempotence) | Content keys and `If-None-Match: *` make piece writes idempotent. The ETag on `current.json` is the fence. |
| Dead letter queues: "the message is moved to a different queue ... any message in the queue is an error." (ch. 12, Acknowledgments and redelivery) | The parked list. Only the poison source is parked, and the rest of the session keeps flowing. Any parked source is an error in `memory status`. |
| "it's not sufficient to just append another event to the log to indicate that the prior data should be considered deleted, you actually want to rewrite history" (ch. 12, Limitations of immutability) | Secrets are removed before upload, because after upload the only remedy is deleting objects. No bucket lock, so that remedy stays possible (section 19). |

### 3.7 Manifest cost [measured, `manifest_cost.py`, rerun 2026-11-07]

Inputs: a 60 s / 300 s window, one transcript source per session, 220 B per piece and a 600 B
header. Corpus: 4,650 sessions, 27,683 captures, 4.01 GB of transcript.

| Manifest shape | Manifest bytes uploaded | Share of transcript bytes | Worst session |
|---|---:|---:|---:|
| Full manifest rewritten every capture | 457 MB | 11.4% | 89.3 MB |
| Delta per capture, checkpoint every 64 | 29 MB | 0.7% | bounded by 64 pieces per checkpoint |

This undercounts sessions with several sources. Reviewer B measured the same quadratic growth
on synthetic manifests: 87 MB of manifests for one 898-piece session. [review, 02 M2]

### 3.8 Commit retries, ETags and writes from a dead process [measured, P11, 2026-11-09; live probes, 2026-11-11]

- P11 ([prototype](docs/rust-rewrite/prototypes/P11-commit-retry.md)): a real R2 probe (889 requests) and a seeded
  simulation (100 owners, 12 seeds, 8 approaches plus the parent's hybrid run). The choice in
  7.4 step 5 rests on it. [DDIA ch. 8, Handling errors and aborts; ch. 2, When an overloaded
  system won't recover]
  - Rejected: fail fast with no in-commit retry (p99 325 s against 23 s at 20 % errors); the
    hybrid that fails fast once a streak exists (p99 40.5 s against 23.2 s at 20 %, no better
    under bursts).
  - Unproven, not adopted: a shared retry token bucket (never bound in the simulation).
  - Unlanded bytes at the end of every simulated run: 0.
  - Real R2: 28 of 28 stale ETags got 412; 60 of 60 two-writer races had exactly one winner,
    and the stored body was always the winner's.
- ETags (operator-approved live probes under `rust-acceptance/<run-id>/`, synthetic bytes,
  prefixes deleted and verified empty; one client, one network path, one day). For a single-part
  PUT the ETag is the hex MD5 of the body (50 of 50). Rewriting identical bytes keeps the ETag,
  so `If-Match` with the earlier ETag still succeeds after another writer's identical rewrite
  (5 of 5) and fails with 412 after a different rewrite (5 of 5). `x-amz-version-id` changes on
  every write. Section 7.4 states the consequence.
- A PUT whose client is killed after the whole request was handed to the kernel still lands:
  8 of 10 unconditional and 7 of 10 with `If-None-Match: *`, complete bodies only, first visible
  205 to 507 ms after the kill, nothing later in polls out to 10 minutes. Killed after half the
  body or before any body byte: 0 of 10 each, which is not proof it cannot happen. Section 11.2
  states the consequence.

### 3.9 Durable writes and the watcher [measured, 2026-11-09 and 2026-11-10]

- P12 ([prototype](docs/rust-rewrite/prototypes/P12-durable-state-writes.md), APFS microbenchmark; end-to-end
  daemon properties at 2,000 cases on patched copies): two state writes a minute cost roughly
  12 ms of flush time. With `SyncPolicy::None` the two daemon simulation properties go from
  421 s to 14 s, all 30 tests passing (section 15). Skipping unchanged bodies was no faster,
  because status carries counters and `updated_at`. Not measured: Linux (ext4) costs.
  [DDIA ch. 8, Durability]
- `sync_all` is `F_FULLFSYNC` (2026-11-10, rustc 1.99.0 as pinned in `flake.nix`): std's
  `os_fsync` is `libc::fcntl(fd, libc::F_FULLFSYNC)` under `target_vendor = "apple"`. A dyld
  `__interpose` of `fcntl` and `fsync` showed 43 `F_FULLFSYNC` calls for 43
  `sync_all`/`sync_data` calls, including a directory handle, and no plain `fsync`. Limits: it
  shows the call is issued and returns 0, not that media is durable; APFS only.
- `notify` 8.2.0 with `RecursiveMode::Recursive` on FSEvents (2026-11-10, 5 runs per
  configuration, each file created, appended and renamed). 1k files: no loss. 10k files in about
  0.8 s: loss in 2 of 5 runs (15 and 309 files, up to 3.09 %). 50k files in about 4.5 s: loss
  in 3 of 5 runs (up to 0.23 %). Every run with missing files also had at least one rescan flag
  and vice versa; no silent loss in 50 runs. That supports `Rescan -> ReconcileAll` as the loss
  cover and makes the 15 min timer a backstop (10.3). Limits: one Mac, FSEvents only, not
  exercised through the real scheduler; a directory moved in with 1,000 children yielded no
  child events (found only by a rescan of that directory or the timer).

### 3.10 Mutation baseline [measured, 2026-11-10]

`cargo-mutants` 27.1.0, 500 cases, example tests only (303 tests, `hegel_` and `proptest_`
excluded), 222 of 223 mutants finished (disk full): `capture.rs` 62.5 % (35 caught, 21 missed,
14 unviable), `commit.rs` 89.5 %, `state.rs` 91.6 %, total 81.9 %. `memory-core` (1,157 mutants)
was not run.

## 4. Architecture

```
  Claude / Codex / Pi hooks ──one write(2)──▶ hints/YYYY-MM-DD.jsonl ─┐
                                                                      │ (watched)
  FSEvents / inotify on agent roots ──────────────────────────────────┼─▶ Coalescer (per owner)
                                                                      │    quiet 60 s, cap 300 s,
  Safety reconcile: start, rescan flag, wake gap, every 15 min ───────┘    SessionEnd flushes
                                                                                │
                                                                                ▼
                     reconcile(owner): locate → stat → plan (pure) → protected-source pipeline
                       Unchanged | Append | Adopt | Replay | Snapshot | Deleted | Withheld
                                                                                │
                                                                                ▼
                     capture: read ≤ 8 MiB pieces, transform, Authorized<Redacted<_>>, SHA-256, zstd
                       PUT objects/<sha256>.zst            If-None-Match: *
                       PUT manifests/<sha256>.json         If-None-Match: *   (delta or checkpoint)
                       PUT <owner>/current.json            If-Match: <etag>
                       on error or 412: GET current.json and compare (section 7.4)
```

The design is event-driven in Fowler's sense. Hooks and filesystem events are Event
Notifications: they carry coordinates, not bytes. The upload is Event-Carried State Transfer.
The agents' JSONL files are already event-sourced logs, and we are the change-data-capture
connector for them. [DDIA ch. 12, Change Data Capture; Fowler 2017]

Every signal is an edge that says "look here". The reconcile reads the level (disk against
index against R2) and never trusts the event's content beyond using it as a location to check.

## 5. Domain model

Pure types live in `memory-core`. The shell converts I/O into these types and back.

```rust
// Branded identifiers. Validated once at the boundary.
pub struct ArchiveId(String);   pub struct MachineId(String);
pub struct SessionId(String);   pub struct SourceId(String);
pub struct Sha256([u8; 32]);    pub struct Offset(u64);

pub enum Agent { Claude, Codex, Pi }

pub struct SessionKey { archive: ArchiveId, machine: MachineId, agent: Agent, session: SessionId }

// Who owns a source's manifest chain. Shared stores have no session (A-M4).
pub enum Owner { Session(SessionKey), Shared { archive: ArchiveId, machine: MachineId, agent: Agent } }

// Pure locator, one per agent. A path that maps to nothing is counted, never uploaded.
pub enum Located { Source { owner: Owner, kind: SourceKind, id: SourceId }, UnknownKind, UnknownOwner }
pub fn locate(path: &RootRelativePath, roots: &Roots) -> Located;

pub enum SourceClass { Append, Snapshot }     // JSONL transcripts against SQLite stores and config

pub enum SourceKind { ClaudeTranscript, ClaudeSubagent, ClaudeToolResults, ClaudeTodos,
    ClaudeTasks, ClaudeTeams, ClaudeFileHistory, ClaudeDebug, ClaudeHistory, ClaudeConfig,
    CodexRollout, CodexCompressed, CodexStore, CodexHistory, CodexConfig,
    PiSession, PiSettings, PiModels, PiSnapshot }

// File identity (A-M1). Birth time is absent on some Linux filesystems.
pub struct FileId { dev: u64, ino: u64, birth: Birth }
pub enum Birth { Known(i64), Unavailable }

// What the shell observed on disk right now.
pub struct Observed { id: FileId, size: u64, mtime_ns: i64, observed_at_ns: i64 }

// Stat trust (A-M2). Racy when mtime is within 2 s of the stat that recorded it.
pub enum StatTrust { Settled, Racy }

// What the index remembers per source. Derived and rebuildable (section 9.3).
pub struct SourceState {
    id: FileId, size: u64, mtime_ns: i64, trust: StatTrust,
    epoch: u32, captured_to: Offset,
    head: Sha256,                 // digest of [0, min(4 KiB, captured_to))
    tail: Sha256,                 // digest of [captured_to - 64 KiB, captured_to), clamped at 0
    whole: Sha256State,           // resumable whole-file digest; a cost optimisation only
    rules: RulesDigest,           // transform rules applied, protected kinds only
    audit: AuditState,            // last full check and consecutive deterministic failures
}

// Output of the pure planner.
pub enum Plan {
    Unchanged,
    Append { from: Offset, to: Offset },     // same identity, grew, head and tail windows match
    Adopt { remote: EpochPosition },         // no index entry, R2 has an epoch: verify locally
    Replay { reason: ReplayReason },         // new epoch from byte 0
    Snapshot,                                // class Snapshot, fingerprint changed
    Deleted,                                 // missing on two consecutive reconciles
    Withheld { reason: WithholdReason },     // nothing uploaded, counted locally
}
pub enum ReplayReason { FirstCapture, IdentityChanged, Truncated, WindowMismatch,
    AdoptMismatch, AuditMismatch, RulesChanged }
pub enum WithholdReason { Credential, CollectorState, UnknownKind, UnknownOwner,
    MalformedProtected, ScrubFailed, SecretInText, KeyTooLong, SourceLimit }

// Privacy is a capability (section 8). Only transform::apply constructs Redacted,
// and only policy::authorize constructs Authorized. The store accepts nothing else.
pub struct Redacted<T>(T);
pub struct Authorized<T>(T);
pub enum PolicyDecision<T> { Allow(Authorized<Redacted<T>>), Deny(WithholdReason), Unknown(UnknownPolicy) }
pub enum UnknownPolicy { ConfigInvalid, RootUnresolvable, PathUnresolvable, RulesUnavailable }

// Closed error classes (D-B1, DDIA ch. 8 Handling errors and aborts).
pub enum ErrorClass { Offline, Throttled, Transient, DestinationAuth, Local(LocalError), Deterministic(Fault) }

// Outcome of one capture attempt.
pub enum Disposition {
    Done,
    Retry { after: Duration, class: ErrorClass },
    PauseHost { class: ErrorClass },          // DestinationAuth: stop uploads, keep watching
    Park { reason: ParkReason },              // one source, after repeated deterministic failures
    ParkOwner { reason: OwnerParkReason },    // foreign writer or newer remote schema
    Cancelled,
}
pub enum ParkReason { RepeatedAuditMismatch, RepeatedReadbackMismatch, SourceUnstable }
pub enum OwnerParkReason { ForeignWriter { theirs: Sha256, ours: Sha256 }, NewerRemoteSchema }

// Commit result after the guarded current.json PUT and any verification GET (section 7.4).
pub enum CommitOutcome { Committed { etag: ETag }, AlreadyOurs { etag: ETag },
    BaseUnchanged, Foreign { theirs: Sha256 }, NewerSchema }
```

Rules applied:

- No optional-field bags. Every enum is matched exhaustively.
- Wire enums are `#[non_exhaustive]` only in the reader's copy, never in the writer's.
- Every reason is an enum, never free text, because parser messages can echo a credential.
  [review, 05 B1]

## 6. R2 layout and schemas

### 6.1 Buckets and credentials

- **One bucket.** The existing `agent-sessions` bucket, shared by both hosts and used by
  prototypes, acceptance (P2, P2b, slices 2, 2b and 3) and production. There is no separate
  acceptance bucket and no fresh production bucket. [decided 2026-11-07; operator decision
  2026-11-09, confirmed twice (A2)]
- **Acceptance prefix.** Acceptance runs write only under `rust-acceptance/<run-id>/`, delete
  that prefix at the end and verify it empty. They never list, read or delete other keys.
  Before rollout every `rust-acceptance/` prefix is deleted, and the bucket is verified to hold
  only `delivery-artifacts/` and data written by the accepted Rust writer. (A2)
- **Credentials.** Prototypes and acceptance use the credentials in the repository's ignored
  `.env` (mode 0600), loaded into a process environment only and never printed, logged or
  written (A2). Production: one long-lived Object Read and Write token per host, scoped to the
  bucket.
  [default] This limits revocation scope and accidents, not a determined attacker. DDIA notes
  that an attacker who compromises one node can probably compromise all of them. [DDIA ch. 9,
  Byzantine Faults]
- **Token storage.** The token is in a 0600 file named by the config. The file is excluded as
  collector state (section 8.2).
- **No bucket lock.** See section 19, decision 1.

### 6.2 Keys

- `objects/<sha256 of the transformed piece bytes>.zst`, where transformed means after the
  section 8.3 scrub and before compression. Immutable, written with `If-None-Match: *`. A 412
  means the object is already stored, which is success.
  - Keying by the hash of the transformed bytes means a zstd upgrade changes no keys, and the
    key is known before compression. This is how git and restic key objects. [review, 02 M4]
  - After a scrub-rule fix the same input scrubs differently, so the key and the manifest
    change. Hashing the raw bytes would give the old key, a 412, and adoption of the old, less
    scrubbed object. The previously uploaded object stays in the bucket, unreferenced, until
    the deletion tool of section 19, decision 4 exists. (A10.2)
  - The namespace is global across sessions and hosts. Sessions are never deleted, which
    removes the main argument for per-session keys. [decided 2026-11-07]
- `manifests/<sha256 of manifest bytes>.json`. Immutable, written with `If-None-Match: *`.
- `sessions/<archive>/<machine>/<agent>/<session_id>/current.json`. The commit pointer for a
  session owner.
- `shared/<archive>/<machine>/<agent>/current.json`. The commit pointer for an agent's shared
  stores (Codex SQLite, `history.jsonl`, config).
- `hosts/<archive>/<machine>/heartbeat.json`. Best effort, written at most every 15 min. It
  holds a copy of the status summary in section 11.3: `last_commit_at`, the oldest uncaptured
  age, the current error class and the parked count. It gives one place to see both hosts'
  health. It carries `last_commit_at` so that a gray failure, alive but committing nothing,
  does not look healthy. [DDIA ch. 9, Degraded performance] Cost is about 0.013 USD a month.
  [review, 04 B1]

`current.json` is written with `If-Match: <etag>`, or with `If-None-Match: *` on the first
write. The ETag is the fence that idempotent writes need when "no other node may concurrently
update the same value". [DDIA ch. 12, Idempotence] R2 allows one write per second per key.
`current.json` is written at most once per window per owner. [review, 02 m3]

Admission: an owner whose `current.json` key would exceed 1,024 bytes is withheld as
`KeyTooLong` and counted. [review, 06 F2 item 2] More than 1,024 sources in one owner is
`SourceLimit`, which is counted and visible rather than retried. [review, 05-history L4]

### 6.3 Manifest, schema `memory.manifest/1`

Manifests form a chain through `previous`. Two kinds share one schema:

- A **checkpoint** lists every retained epoch of every source of the owner.
- A **delta** lists only what changed since the previous manifest.

Every manifest whose `seq` is a multiple of 64 is a checkpoint, and so is the first.
[decided 2026-11-07, measured in 3.7] This is Delta Lake's commit-plus-checkpoint shape.
Each manifest is an immutable copy-on-write root, so a reader at any `current.json` sees a
consistent snapshot. [DDIA ch. 8, Indexes and snapshot isolation; ch. 6, Databases Backed by
Object Storage]

```json
{
  "schema": "memory.manifest/1",
  "kind": "delta",
  "owner": {"session": {"archive": "…", "machine": "…", "agent": "claude", "session_id": "…"}},
  "seq": 17,
  "previous": "<sha256 of manifest seq 16>",
  "checkpoint": "<sha256 of manifest seq 0>",
  "captured_at": "2026-11-07T10:30:00Z",
  "writer": {"name": "memory", "version": "0.1.0", "commit": "<git sha>"},
  "launch": [{"digest": "<sha256>", "argv": [], "cwd": "…", "env": {"…": "…"}}],
  "changes": [
    {"id": "transcript", "kind": "claude.transcript", "path": "projects/<slug>/<id>.jsonl",
     "class": "append", "op": "extend",
     "epoch": {"n": 1, "writer": {"version": "0.1.0", "commit": "…"}},
     "size": 123456, "sha256": "<whole file [0, size)>",
     "pieces": [
       {"offset": 100000, "len": 23456, "sha256": "<transformed>", "object": "objects/<transformed>.zst",
        "encoding": "zstd-3", "encoded_len": 5000, "encoded_sha256": "…"}
     ]},
    {"id": "todos", "kind": "claude.todos", "class": "snapshot", "op": "replace",
     "epoch": {"n": 3, "writer": {"…": "…"}, "rules": "<rules digest>"},
     "size": 2048, "sha256": "…", "pieces": [{"offset": 0, "len": 2048, "…": "…"}]},
    {"id": "teams", "kind": "claude.teams", "op": "deleted", "deleted_at": "…"}
  ]
}
```

- **Change ops.** `extend` appends pieces to the current epoch. `epoch` starts epoch n+1 from
  offset 0. `replace` is a snapshot epoch. `deleted` records a deletion.
- **Checkpoints.** A checkpoint uses `"kind": "checkpoint"` and `"sources"` in place of
  `"changes"`. It lists every append-class epoch with all of its pieces, and the latest epoch
  only for snapshot-class sources.
- **Pieces are byte ranges, not lines.** A piece may end in the middle of a line or a UTF-8
  sequence, and readers must not assume line alignment. [review, 01 m1]
- **Withheld sources are not recorded in R2.** They are counted locally by reason (section
  11.3). This keeps credential paths and unknown files out of the bucket.
- **Launch evidence** is attached once per distinct launch digest, after the allowlist in
  section 8.4. The hook records environment facts only and no argv, so `argv` is empty from the
  hook and stays in the schema (A3).
- **Writer stamps.** The manifest records the writer, and so does every epoch. A buggy version's
  output can then be found per epoch, not only per manifest object. [review, 04 M4] Finding them
  needs a LIST of `manifests/` plus a GET of each, not "one list". [review, 02 M5]

Current, schema `memory.current/1`:
`{"schema": "memory.current/1", "manifest": "<sha256>", "seq": 17, "updated_at": "…"}`.

Reader rule: GET `current.json`, then follow `previous` back to the manifest named by
`checkpoint`, then apply the deltas forward. A reader needs at most 64 manifest GETs plus one
GET per piece. The mean is about 6 captures per session. [measured, 3.7] Checkpoints sit at `seq % 64 == 0`,
so the longest valid chain is a checkpoint plus 63 deltas, and base recovery gives up after 64
manifest GETs (A12.3).

### 6.4 Epoch rules

- An append whose window checks pass extends the current epoch.
- An identity change, a truncation, a window mismatch, an adopt mismatch, an audit mismatch or
  a rules change starts epoch n+1 from byte 0.
- Class `append` retains every epoch. Class `snapshot` retains the latest epoch in checkpoints.
- Unreferenced objects are not deleted (section 19, decision 4).

### 6.5 Schema policy

- Every type we write derives `serde` and `schemars`, with `deny_unknown_fields` on read.
- The `schema` string carries the version. JSON Schema files are generated by
  `cargo xtask schemas` and committed, and CI fails on drift.
- The writer's recovery path reads remote manifests only for the ETag, `seq`, `previous`,
  `checkpoint` and epoch positions. A remote manifest with a newer schema parks the owner as
  `NewerRemoteSchema` instead of looping on errors. Strict validation of everything else
  belongs to the reader. [review, 04 M4]

## 7. Capture algorithm

### 7.1 Coalescer

The coalescer is a session window on processing time, per owner, held in memory. [DDIA ch. 12,
Types of windows]

- Any signal naming an owner records the time.
- A capture is emitted when the owner has been quiet for `quiet` (default 60 s), or `cap`
  (default 300 s) has elapsed since the first unflushed signal.
- `SessionEnd` and Codex `session_end` flush at once, because no more bytes follow and short
  sessions may expire. Claude `Stop` fires on every turn and stays debounced. [review, 03 M5]
- One capture is in flight per owner. Later signals queue the next one.
- The defaults come from section 3.2. One config value sets them: `window = { quiet, cap }`.

Clocks [DDIA ch. 9, Monotonic clocks; prototype P6]:

- Windows and backoff use the monotonic clock. On macOS that is `mach_absolute_time`, through
  `Instant`. On Linux it is `CLOCK_MONOTONIC`. Both exclude sleep, so deadlines pause across
  sleep rather than firing at once on wake.
- A 60 s tick compares elapsed wall time with elapsed monotonic time. A gap of more than 120 s
  means the host slept. It sets the reconcile flag and staggers queued captures with jitter
  over 30 s. [review, 03 M2, M5]
- Manifest `captured_at` uses the wall clock. Nothing orders by it.

### 7.2 Reconcile

```
reconcile(owner):
  for path in sources_of(owner):       // index entries for owner, plus readdir of every
                                       // directory locate() maps to this owner; the walk
                                       // skips symlinks (8.2)
    resolved = resolve(path)           // link chain and final identity, section 8.2
    match policy.admit(resolved):      // credential, collector state, kind, owner
      Deny(r) | Unknown(_) -> count Withheld, continue
      Admit(kind)          -> {}
    observed = stat(resolved)
    plan = core::plan(observed, index.get(source), remote_position(owner, source))
    execute(plan)                      // section 7.3
```

Planner rules for class `append`. The function is pure, total and property-tested.

| Index entry | Observation | Plan |
|---|---|---|
| None, and no remote epoch | any | `Replay{FirstCapture}` |
| None, and a remote epoch at size N | size ≥ N | `Adopt`: hash [0, N) locally; equal digest rebuilds the state, then `Append` if larger; a different digest is `Replay{AdoptMismatch}` |
| None, and a remote epoch at size N | size < N | `Replay{Truncated}` |
| Some | identity differs | `Replay{IdentityChanged}` |
| Some | size < `captured_to` | `Replay{Truncated}` |
| Some | size = `captured_to`, mtime equal, `Settled` | `Unchanged` |
| Some | size = `captured_to`, mtime changed or `Racy` | check the windows; a match is `Unchanged` with the stat updated, a mismatch is `Replay{WindowMismatch}` |
| Some | size > `captured_to` | check the windows; a match is `Append{captured_to, size}`, a mismatch is `Replay{WindowMismatch}` |
| Some | missing on two consecutive reconciles | `Deleted` [review, 01 m3] |

The window check re-reads the head window `[0, min(4 KiB, captured_to))` and the tail window
`[captured_to - 64 KiB, captured_to)` from disk, about 68 KiB, and compares them with the
stored digests. It reads bytes before `captured_to`, which the reviewed design's proof never
did. [review, 01 B1]

The system model states the guarantee honestly. [DDIA ch. 9, System Model and Reality;
`07-ddia-crosscheck.md` blocker 1]

- Sources are assumed append-only. The agents own them, so this is an assumption, not a
  contract.
- A rewrite that changes identity, or touches the head or tail window, is detected at capture.
  No wrong byte is committed.
- A rewrite strictly between the windows, with the same identity and growing size, is not
  detected at capture. It is detected by the bounded daily audit in section 11.4, which starts
  a new epoch. Until then the latest epoch can reassemble to bytes the file never held.
  How often agents do this is measured from the running daemon's own counters
  (`Replay{WindowMismatch}`, `Replay{AdoptMismatch}`), not by a prototype (A1). No production
  write happens before the audit exists and a property covers this case (section 18, A12.2).
- The resumable `Sha256State` saves only the cost of the whole-file digest. It proves nothing.

Class `snapshot` uses a fingerprint, not windows. A plain file's fingerprint is identity, size
and mtime. A SQLite store's fingerprint is the identity, size and mtime of the db, plus those of
its `-wal`. An unchanged fingerprint is `Unchanged` without opening the store. A changed one is
`Snapshot`. [review, 01 M3; prototype P9] A changed `RulesDigest` on a protected kind is
`Replay{RulesChanged}`.

### 7.3 Capture

Pieces are at most 8 MiB. [default, unmeasured; prototype P4 does not justify a change] A delta or replay larger than that is
split into several pieces in one manifest. This bounds memory and the request timeout, and no
local staging is needed for append-class sources. [review, 04 M5] For each piece:

1. Read `[from, to)` into a buffer. The read opens the file with `O_NOFOLLOW | O_NONBLOCK` and
   checks on the open handle that it is a regular file and the file the last stat observed.
   Otherwise nothing is read and the source is retried (A11.5).
2. Pass the bytes through the transform. For non-protected kinds the transform is the identity.
3. Obtain `Authorized<Redacted<Bytes>>` from policy.
4. Feed the transformed bytes to the piece SHA-256 and to the whole-file `Sha256State`.
5. Compress with zstd level 3 and digest the encoded bytes.
6. PUT `objects/<sha256 of the transformed bytes>.zst` with `If-None-Match: *`. After a 412 the
   stored object is read, and the manifest records its encoding (`encoded_len`,
   `encoded_sha256`), not the writer's (A10.2).
   - If the chosen client can cheaply set `Content-MD5`, send it. [prototype P2]
   - The end-to-end checks are the reader's digest verification and the restore sample (section
     11.4), not a transport checksum. Content-MD5 is therefore not a gate. [DDIA ch. 13, The
     end-to-end argument; `07-ddia-crosscheck.md` blocker 5]

A replay captures `[0, size_at_stat)` only, even while the file grows. Under the append-only
assumption those bytes are stable, and the next reconcile appends the rest. After the last
piece, the shell stats the file again. If its identity changed, or it shrank below
`size_at_stat`, the capture is abandoned before the manifest is written. There is no two-pass
"learn the key" read, so a growing file cannot livelock the replay. [review, 01 M3]

`captured_to` advances only when the commit lands. The bytes of a failed capture are therefore
re-read by the retry, and bytes that arrive after the stat ride in the next capture. The
coalescer keeps one capture in flight per owner and queues exactly one follow-up (A6).

A snapshot of a plain file reads its pieces and then compares the fingerprint again. If the
fingerprint changed, the capture is retried at most 3 times in the window. After that it counts
as `SourceUnstable`. After 3 windows in a row it is parked.

SQLite snapshot:

- Open read-only, never with `immutable=1`, which ignores the WAL. [review, 05-history D7]
- Run the backup API with all pages in one step into a 0600 temporary file under `state/tmp/`.
  Pieces are read from that file, which is deleted after the commit.
- Two errors stay distinct, as Go's `ErrSQLiteSourceChanged` was: `SourceChanged` is a retry,
  and `BackupFailed` is `Local`. [review, 05-history D8]

Request timeout: 30 s plus size at 1 MiB/s, with a floor of 60 s. At the 8 MiB cap this is at
most 60 s. [review, 05-history L2]

Memory bound. At most 2 hash or compress jobs run at once, and at most 8 uploads
[default, unmeasured]. For incompressible data the payload is 2 × 8 MiB raw plus 8 × 8 MiB
encoded, about 80 MiB (83.89 MB). That is arithmetic, not a measured peak. The target is a peak
RSS under 128 MB (section 15). Prototype P4's Python process measured 135.12 MB at 8 × 8 MiB
random buffers, which does not settle it; the settling measurement is the peak RSS of the Rust
daemon with 8 concurrent 8 MiB incompressible PUTs and a 30k-entry index (A10.4). The reviewed
design's whole-object buffers allowed 368 MB. [review, 04 M5]

### 7.4 Commit protocol

1. PUT all pieces (section 7.3). A 412 is success.
2. Build the manifest:
   - `seq` is the previous seq plus one.
   - `previous` is the manifest named by the ETag we hold.
   - It is a checkpoint when `seq % 64 == 0`.
   - Pending launch records are attached.
   PUT it with `If-None-Match: *`.
3. PUT `current.json` with `If-Match: <etag>`, or `If-None-Match: *` on the first write.
   Client-level retries are disabled for this call, so that a 5xx after a landed write cannot
   become a spurious 412. [vendor, 3.5; prototype P2]
4. On success, record the returned ETag and update the index. Then remove the committed
   launch records from the pending set.
5. On any error, timeout or 412, GET `current.json` and compare. The GET is attempted 4 times
   with 3 pauses between them (A13.5). The content-addressed manifest hash is the request id. [DDIA ch. 9, Figure 9-1; ch. 13, Uniquely identifying
   requests]
   - It names our manifest, or any manifest hash this daemon built for the owner since its last
     landed commit (`known_ours`, held in memory): `AlreadyOurs`. The commit landed. Adopt its
     ETag and position and queue a follow-up. A late `current.json` write from an earlier failed
     capture is therefore not a false `Foreign` park. The owner key contains the machine id, so
     outside a cloned machine id this cannot hide a real foreign writer. (A6)
   - It names our `previous`: `BaseUnchanged`. The commit did not land. Retry from step 3 with
     the fresh ETag, up to 4 times with full-jitter pauses. After that the capture ends as
     `Retry` of class `Transient`, not `Throttled`, with the backoff of section 11.2 and its
     own counter (A6). [review, 02 M1]
   - It names anything else: `Foreign`. Another writer advanced this owner, which breaks the
     one-writer-per-owner rule. Do not merge: two hosts' piece lists for one source do not
     commute. Park the owner with both hashes and count it. [DDIA ch. 6, Dealing with
     Conflicting Writes; review, 02 M1]
   - Its manifest has a newer schema: `NewerSchema`. Park the owner. [review, 04 M4]
   - 404: the result is "not final". Retry the GET with backoff. If the index says this is a
     first write, retry step 3 with `If-None-Match: *`, which is safe because it fails if the
     key exists.
6. Invariant: the `previous` of a committed manifest equals the manifest that `current.json`
   named under the ETag we fenced on. [review, 02 M1]

For a single-part PUT the R2 ETag is the MD5 of the body (3.8), so the fence cannot see an
identical-bytes rewrite of `current.json`. That is harmless only because the bytes of
`current.json` name a manifest hash that is unique per commit. This must stay true. Test fakes
treat the content-derived ETag as the real behaviour. (A13.4)

A crash between steps leaves only unreferenced objects and manifests, which is discarded
output. [DDIA ch. 12, Microbatching and checkpointing]

### 7.5 Shared stores

Codex SQLite, `history.jsonl` and config files have no session. They are owned by
`Owner::Shared` and committed under `shared/<archive>/<machine>/<agent>/`. Each is captured once
per change, not once per session. [default, review, 01 M4] Session opt-out is retired, so the Go
rule "withhold the whole store if any session is excluded" no longer applies. That rule produced
7,909 `policy_denied` issues. [decided 2026-11-07; review, 06 F2 item 11]

### 7.6 Launch evidence

- `memory hook` records environment facts only and no launch argv (A3): no process-tree,
  `kern.procargs2` or `/proc` read, and no `--argv` flag. It applies the allowlist in section
  8.4 before it appends the line, so the hint log never holds a raw value.
- The daemon keeps pending launch records by owner. They are attached to the owner's next
  manifest, deduplicated by digest.
- A hint day file is the system of record for its launch records until a manifest carries
  them. Hint files are kept for 7 days [default, unmeasured] (section 9.1).

## 8. Protected sources and privacy

This section ports Go's secret protections. Go code and tests are the reference, and the
synthetic test vectors are reused as fixtures. [review, 05 B1] These protections are
independent of session opt-out, which is retired. [decided 2026-11-07]

### 8.1 Pipeline

`locate` → `resolve` → `policy::admit` → read → `transform::apply` → `Redacted` →
`policy::authorize` → `Authorized<Redacted<Bytes>>` → store.

The store's PUT accepts only `Authorized<Redacted<Bytes>>`, so an unscrubbed config reaching R2
is a compile error. Scrubbing happens before upload, because nothing after upload is reversible
except deleting objects. [DDIA ch. 12, Limitations of immutability; ch. 14, Legislation and
Self-Regulation]

### 8.2 Admission (from `internal/capturepolicy`)

Symlinked files are skipped (A4). The directory walk counts symlinks and never yields them, so
no file is captured through a link, and the watcher never treats a symlink to a directory as a
directory (A13.6). Path resolution still checks every link of a symlink chain and the final
identity, not only the logical path, for paths reached directly (hint paths) and for ancestor
and root identities. A chain of more than 255 links, or a cycle, is `PathUnresolvable`. A link
to a credential is denied either way.

The credential denylist is checked against every identity in the chain:

- Names: `.credentials.json`, `auth.json`, `oauth.json`, `mcp-auth.json`, `config.env`,
  `.git-credentials`, `.netrc`, `.npmrc` and `.pypirc`.
- Prefixes and suffixes: `.env` and `.env.*`, `*.key`, `*.pem`, and `.claude.json*` other
  than the primary `.claude.json`, which goes through the scrubber.
- SSH key names: `id_rsa`, `id_dsa`, `id_ecdsa`, `id_ed25519` and the `_sk` variants.
- Directories: `.ssh`, `.gnupg`, `.aws`, `.azure`, `.kube` and `.config/gcloud`.
- Known backup suffixes are normalised first: `~`, numbered tildes, `.bak`, `.backup`,
  `.orig`, `.old`, eight-digit dates, and archive extensions.
- Sources: `internal/capturepolicy/policy.go:16-21`, `credentials.go:10-20`,
  `docs/security.md`.

Collector state is excluded by filesystem identity: the config, the token file, and the state
directory with all its files. A path that `locate` maps to no kind is `UnknownKind`, and one
with no derivable owner is `UnknownOwner`. Both are withheld and counted, never passed through.
Go's pass-through of unrecognised names is not ported. [review, 05 m2]

Hint paths are untrusted input. A hint's `transcript_path` or `cwd` is used only if `locate`
maps it under a configured or learned root. It then passes the same admission check. [review,
05 M4]

The source read refuses a changed file: see step 1 of section 7.3 (A11.5). A residual race
remains, an intermediate directory component swapped for a symlink, because source paths are
not guaranteed canonical (resolution can fall back to the raw path, and legitimate paths have
symlinked parents such as `/var` on macOS). Closing it needs a rule that the source adapter
reads only canonical paths, carried by a type that only resolution builds (A12.4).

### 8.3 Transform (from `internal/scrub`)

- **Structural scrub.** Eleven config basenames are scrubbed, selected by the filename of every
  identity in the chain, not by the collecting agent: `claude.json`, `.claude.json`,
  `settings.json`, `settings.local.json`, `managed-settings.json`, `.mcp.json`, `mcp.json`,
  `managed-mcp.json`, `models.json`, `config.toml` and `managed_config.toml`
  (`internal/scrub/scrub.go:94-106`).
- **Malformed protected files are withheld.** Protected JSON or TOML that is malformed is
  `MalformedProtected`. JSON must be an object, and duplicate decoded keys, invalid Unicode
  and trailing documents are rejected. `serde_json` accepts duplicate keys silently, so the
  port needs a duplicate-rejecting deserializer. [review, 05 B1]
- **Small text assets.** Pi skills, extensions and tools that assign a literal to a
  secret-named variable are `SecretInText` (`internal/scrub/text.go:44-61`).
- **Rules digest.** `RulesDigest` covers the rule table. It is stored in `SourceState` and in
  each protected epoch. A rule change re-captures protected kinds.
- **Integrity checks compare transformed bytes.** The audit and read-back in section 11.4
  compare `transform(local)` with R2, not the raw local file.
- **Errors carry a fixed reason and never echo values.**

Transcripts are not scrubbed. Go does not scrub them either. This is a known-file policy, not
DLP (`docs/security.md:5-8`).

### 8.4 Launch allowlist (from `internal/launch/native.go`)

The Rust hook records environment facts only and reads no process argv (A3). This is an accepted
regression from the Go hook. `LaunchRecord.argv` stays in the schema and is empty from the hook,
and the argv rules below stay as a protection for any argv that reaches the allowlist code.

Argv, at most 32 entries after `scrub.Args`:

- `argv[0]` is reduced to its basename if it is one of `claude`, `codex`, `pi`, `node`,
  `nodejs` or `bun`. Anything else becomes `[redacted]`.
- These flags are kept without a value: `--resume`, `--continue`, `--no-session`,
  `--dangerously-skip-permissions`, `--full-auto` and `--version`.
- These flags are kept with their value: `--model`, `-m`, `--provider`, `--permission-mode`,
  `--session-id`, `--session`, `--config`, `--config-dir` and `--cwd`. The value must pass the
  value rule below. Otherwise the value becomes `[redacted]`.
- Every other argument becomes `[redacted]`, so free-form prompt text is dropped.

Environment: the closed allowlist of nine exact names `ANTHROPIC_MODEL`, `ANTHROPIC_SMALL_FAST_MODEL`,
`ANTHROPIC_DEFAULT_OPUS_MODEL`, `ANTHROPIC_DEFAULT_SONNET_MODEL`,
`ANTHROPIC_DEFAULT_HAIKU_MODEL`, `CLAUDE_CONFIG_DIR`, `MAX_THINKING_TOKENS`,
`MAX_MCP_OUTPUT_TOKENS` and `DISABLE_PROMPT_CACHING`. The allowlist is the bound, with no
separate cap: every allowlisted name whose value passes the value rule is published, and any
other entry is omitted (A13.3).

Value rule (A12.1). A launch value, whether an argv value after a value flag or an allowlisted
env value, is published only if all of the following hold. Otherwise argv publishes `[redacted]`
and the env entry is omitted.

1. It matches the shape of its slot. Model (`--model`, `-m`, the five `ANTHROPIC_*_MODEL`
   names): `^[A-Za-z0-9][A-Za-z0-9._:/@+-]{0,255}(\[[0-9a-z]{1,8}\])?$`. `--provider`:
   `^[A-Za-z][A-Za-z0-9._-]{0,63}$`. `--permission-mode`: `^[A-Za-z][A-Za-z-]{0,31}$`.
   `MAX_THINKING_TOKENS`, `MAX_MCP_OUTPUT_TOKENS`: `^[0-9]{1,9}$`. `DISABLE_PROMPT_CACHING`:
   `0`, `1`, `true` or `false` in any case. Session, config and cwd slots and
   `CLAUDE_CONFIG_DIR`: the safe-value shape `^[A-Za-z0-9][A-Za-z0-9._:/@+-]{0,255}$`.
2. It is not a secret name.
3. Split on `/ : @ + .`, no segment starts, ASCII case-folded, with `sk-`, `sk_`, `ghp_`,
   `gho_`, `ghu_`, `ghs_`, `ghr_`, `github_pat_`, `glpat-`, `xoxb-`, `xoxp-`, `xoxa-`, `xoxs-`,
   `xapp-`, `hf_`, `npm_`, `pypi-` or `eyJ`, or is `AIza` plus 16 or more characters, or
   `AKIA`/`ASIA` plus 16 or more uppercase letters or digits.
4. No segment of 32 or more bytes is opaque: all alphanumeric with a letter and a digit, or
   containing an uppercase letter, a lowercase letter and a digit.

Accepted limits: a key of unknown format shorter than 32 characters passes in a model or path
slot; a long hyphenated lowercase-and-digit run passes because real model names need it; a
model name with one segment of 32 or more bytes that mixes upper case, lower case and digits is
redacted, which fails closed.

### 8.5 Unknown policy state

| Producer | Effect |
|---|---|
| Config missing or invalid | The daemon refuses to start, with a distinct exit code |
| Root unresolvable | That root is withheld, counted and shown in status |
| Path unresolvable | That path is withheld and counted |
| Rules table unavailable or digest mismatch | Protected kinds are withheld |

### 8.6 Threat model and storage privacy [decided 2026-11-07]

Content is stored as plaintext. Cloudflare's encryption at rest (AES-256-GCM) protects against
stolen disks at Cloudflare.

Trusted: the Cloudflare account, both hosts, and both hosts' R2 tokens. Any one of them can
read every stored transcript.

Controls:

1. Secrets are removed before upload (8.2 to 8.4).
2. Tokens are per host, stored 0600, and excluded from capture (6.1, 8.2).
3. There is no bucket lock, so a leaked secret can be removed after the fact (section 19,
   decision 1).

Accepted residual risks:

- Transcripts can contain secrets pasted into a conversation and data about third parties,
  such as clients or colleagues. [DDIA ch. 14, Consent and Freedom of Choice]
- Sessions are never deleted, which sits in tension with DDIA's "purge it as soon as it is no
  longer needed". [DDIA ch. 14; `07-ddia-crosscheck.md` callout 6]
- Adding encryption later is a separate project. It would have to download, encrypt and
  re-upload sessions whose local sources have expired. The operator accepted this.

Rejected: SSE-C, because the key would sit on the same hosts as the token. Client-side `age`,
because it would need off-host key custody and a restore test tied to the key holder. Both are
recorded in `challenge/00-synthesis.md`.

## 9. Local state

### 9.1 Files

Each file is classified as system of record or derived. [DDIA ch. 13, Reasoning about
dataflows]

| File | Class | Written by | Notes |
|---|---|---|---|
| Agent session files | System of record (leader) | Agents | Never written by us |
| `config.toml` | System of record | Operator | Holds the archive id, machine id, agent roots, window, bucket and token path. `deny_unknown_fields`. The daemon refuses to start if it is invalid |
| Token file | System of record | Operator | 0600. Excluded from capture |
| `state/roots.json` | System of record | Daemon | Learned custom roots. Monotonic union: a root is never removed automatically |
| `state/hints/YYYY-MM-DD.jsonl` | System of record for launch evidence | `memory hook` | Directory 0700, files 0600. Kept for 7 days [default, unmeasured] and until every launch record in the file is committed |
| `state/index.json` | Derived cache | Daemon | Holds `SourceState`, hint offsets, ETags and pending launch digests. May be deleted |
| `state/status.json` | System of record for the parked list and counters | Daemon | Section 11.3 |
| `state/daemon.lock` | Lock | Daemon | `flock`, section 12 |
| `state/tmp/` | Transient | Daemon | SQLite backups only. Emptied at start |

The state root is `$HOME/.local/state/memory` on both hosts. `MEMORY_STATE_DIR` overrides it, and
tests always set it to a temporary directory with a cleared environment (A5).

Machine ids are created once with `O_EXCL`. [review, 05-history D3] At install, the existing Go
machine ids are copied into `config.toml`. This keeps identities and is not a data migration.
Every state file is 0600 and every state directory is 0700. [review, 05 M4] The daemon refuses
a hint directory that is not owned by its user, or that is group- or world-writable. One user
per host is assumed.

Removed from the Go design: the payload spool, four SQLite work tables, the 10 s walker,
retained manifest copies, the unix socket, the inbox directory, the failure log and the
session opt-out registry.

### 9.2 Durable writes

`index.json`, `status.json` and `roots.json` are written by the same steps. [review, 04 M1;
DDIA ch. 8, Durability; ch. 4, checksums]

1. Write a temporary file. Its header carries `schema`, `writer_version`, the body length and
   the body's SHA-256.
2. fsync the file. On macOS, use `F_FULLFSYNC`, which `sync_all` issues (3.9).
3. Rename it into place.
4. fsync the directory.

A failed checksum, a short body or an unknown version means the file is treated as missing. For
the index that triggers the rebuild in section 9.3. For `status.json`, the counters restart and
the event is logged. `index.json` and `status.json` are written on authoritative change or at
most every 60 s when dirty, and at shutdown: about two writes a minute, roughly 12 ms of flush
time (3.9). `F_BARRIERFSYNC` only orders writes and is not adopted. A state file body is at
most 64 MiB (67,108,864 bytes) and its header line at most 256 bytes. [default, unmeasured]
Prototype P7 measures the checksum cost. (A7, A11.4)

### 9.3 Index rebuild

Rebuilding the index is bootstrapping a follower from a snapshot at a known position. [DDIA
ch. 6, Setting Up New Followers]

1. Walk the configured and learned roots through `locate`. That is 0.4 s of stats at 24k files.
   [measured]
2. For each owner found, GET `current.json` and walk `previous` back to its checkpoint, which
   gives every source's epoch position: size N and the digest at N.
3. Plan `Adopt` per source. That hashes `[0, N)` locally, about 15 s of CPU for the 4 GB
   corpus. Only a mismatch opens a new epoch.
4. Re-read the hint files still retained, and re-attach launch records whose digest no manifest
   in the chain carries.

The reviewed design opened a new epoch for every source, which re-uploaded the host. [review,
01 m2, 04 B3]

## 10. Signals

### 10.1 Hooks

Hooks are installed per agent as today (`docs/hooks.md`). The hook command is `memory hook`:

- It parses stdin by key.
- It records environment facts under the launch allowlist and no argv (A3).
- It builds one line of at most 4 KiB in a `Vec<u8>`.
- It appends the line to today's file with a single `write(2)` on an `O_APPEND` descriptor,
  creating the file 0600 if needed. A short write counts as a failure. [review, 03 m1]
- It exits 0 within budget on every path, including a missing directory, `ENOSPC` or
  `EACCES`.
- It never prints to stdout, never blocks on the daemon and never retries. [review, 03 m2]
- It does not start `tokio` and does not parse with `clap`. Argv is read by hand.

The reader of the hint files bounds one file read at 64 MiB. [default, unmeasured] It
resynchronises on newline. A torn fragment in the middle of a file
is dropped. A torn last line waits for its newline. Codex trust and Claude `--bare` can silence
hooks, and the other two signals cover that.

### 10.2 Filesystem

- `notify` is pinned exactly in slice 0, after its version is read. It uses FSEvents on macOS
  and inotify on Linux, recursive on each agent root.
- The crate documents that with very many watched files it "may fail to receive all events",
  and that inotify counts every directory against the watch limit. The Mac has about 15k
  directories under the roots. BC-01 has about 7.7k, against a limit of 259,533. [measured]
- `need_rescan()` and any watcher error set the reconcile flag.
- On a directory Create event, that directory is read and its sources reconciled at once,
  because 8.2.0 does not report files already inside a new directory. [review, 03 M3]
- A configured root that does not exist (an agent that is not installed) is skipped and counted.
  The other roots are watched, and the safety reconcile finds the directory once it exists. A
  root that exists but is not a directory, or cannot be inspected, is an error. (A13.6)
- notify 8.x cannot replay FSEvents history across restarts. Downtime is covered by the start
  reconcile. [vendor, 3.5]

### 10.3 Safety reconcile

The safety reconcile runs `reconcile` over every owner. It runs at daemon start, when the flag
is set, after a detected wake gap (section 7.1), and every 15 min [default]. It is trust-but-verify,
not the driver, and the timer is a backstop: `Rescan -> ReconcileAll` is the loss cover (3.9). Its cost is at most one stat per source plus the 68 KiB window read for each racy
source.

### 10.4 The two messaging questions

The two questions are what happens when producers outrun consumers, and what happens when the
consumer is offline. [DDIA ch. 12, Messaging Systems]

| Signal | Producer outruns consumer | Daemon offline or crashed |
|---|---|---|
| Hint files | Buffered on disk; a hook never blocks | Nothing lost; the daemon resumes from its per-file offset |
| Filesystem events | Kernel queue overflows, `notify` reports a rescan, flag set | Events during downtime are lost; the start reconcile covers them |
| Safety reconcile | Not applicable, it reads the level | Runs at next start |

A lost hint is a latency bug, not data loss. The reconcile finds the change within 15 min.
Launch evidence is the exception, and section 7.6 keeps its file. That covers the daemon side
only. When the hook's own write fails (`ENOSPC`, a refused directory), the launch record for
that session is lost without trace. This is accepted, because launch evidence is reduced to
environment facts (A3), and no hook failure log is added (A11.1).

## 11. Failure handling

### 11.1 System model

DDIA says system models "take a binary approach toward faults" while "in reality, it is more a
question of probabilities". [DDIA, Trust, but Verify] So this section states the design's
assumptions and what it checks anyway.

| Assumed to happen | Handled by |
|---|---|
| Process crash, power loss, laptop sleep at any point | R4, idempotent PUTs, `current.json` checkpoint, start reconcile, wake detection |
| Network delay, drop, lost reply, committed then 5xx | Content keys, `If-None-Match`, verify-by-GET on `current.json` (7.4) |
| Hooks silenced, watcher overflow, events lost | Filesystem events, rescan flag, directory readdir, safety reconcile |
| Source rewritten, truncated, renamed over, deleted | Identity, windows, epochs, two-miss deletion, daily audit |
| Index lost or torn ("amnesia") | Checksum, rebuild by adopt (9.3) [DDIA ch. 9, Mapping system models to the real world] |
| Token revoked or expired | `DestinationAuth`: pause uploads host-wide, keep watching, re-probe every 15 min |
| Clock skew between hosts | No cross-host ordering by clock; offsets and CAS only |
| Two writers on one owner | ETag fence; foreign writer parks the owner (7.4) |
| Two daemons on one host | `flock` (section 12) |

| Assumed not to happen, verified anyway | Check |
|---|---|
| Resumable hash state, offsets or epoch rules wrong | Daily audit and read-back (11.4) |
| Index drifts from disk or R2 | Safety reconcile every 15 min |
| Stored piece no longer matches its key | Reader verifies raw and encoded digests on every restore |

Assumed and not verified: SHA-256 is collision-free, `F_FULLFSYNC` and fsync are durable, and
R2's conditional writes are atomic as documented.

### 11.2 Error classes and dispositions

| Class | Examples | Disposition |
|---|---|---|
| `Offline` | DNS or connect error, TLS error, timeout | `Retry`, exponential backoff with full jitter, 1 s to 15 min |
| `Throttled` | 429, 503 | `Retry`, honouring `Retry-After`, falling back to the `Offline` schedule |
| `Transient` | `BaseUnchanged` after 4 rebase retries (7.4) | `Retry`, full jitter `uniform(1 s, min(120 s, 2 × 2^n))`, counted separately, no `Retry-After` |
| `DestinationAuth` | 401, 403 | `PauseHost`; persisted in `status.json` |
| `Local` | disk full, permission, backup failed | `Retry` and counted; disk full is its own `LocalError` |
| `Deterministic` | non-JSON 200 (captive portal), schema error, repeated audit mismatch | Counted; `Park` after 3 consecutive failures of one source spanning at least 3 h |

The 3 h park span is `[default, unmeasured]`.

Retry only transient errors, and back off. [DDIA ch. 8, Handling errors and aborts] A daemon
restart starts each source's backoff again after a startup delay of 0 to 60 s jitter, so a
crash loop does not hammer R2. [review, 04 m2] The first commit of a restarted daemon is never
earlier than 5 s after start (A14.1). A killed predecessor's fully sent PUT can still land up
to about 0.5 s after its death (3.8), and the daemon must not verify `current.json` before it
lands.

### 11.3 Status

`status.json` is daemon-owned and survives restarts. [review, 04 B1, M2, M3] It holds:

- `last_commit_at` and `last_error` as a class with a time, never free text.
- The age of the oldest uncaptured change. This is the primary health metric.
  [target: under 10 min on a reachable network]
- The parked list. This file is the authority, and the heartbeat object carries a copy.
- Withheld counts by reason, and unattributed path counts.
- The loss-window counter, per day. It is defined as a rewrite or truncation detected when
  `captured_to` is greater than the size the last committed manifest confirmed. Those bytes
  were never uploaded and the source no longer holds them. A spill buffer is added only if this
  is nonzero after a month on both hosts (D8).
- Audit and read-back mismatch counts, and `SourceUnstable` counts.

`memory status` answers in three tiers:

1. Local view: sources with size greater than `captured_to`, and the oldest age. Always
   available.
2. `status.json` fields.
3. `--verify`: GET each owner's `current.json` and compare its ETag with the index.

It says "up to date as of <time>", never "all captured". `memory unpark <source|owner>` clears
a parked entry.

### 11.4 Bugs in this writer (D10, revised)

Checksums below the application "would not be caught" for software bugs. [DDIA, Maintaining
integrity in the face of software bugs] The parts most likely to carry such a bug are ours: the
resumable hash state, the offset bookkeeping and the planner's epoch rules. Three measures:

1. **Writer stamps.** Every manifest and every epoch records the writer's name, version and
   commit. A fixed writer re-captures from the source and supersedes a buggy version's epochs
   without touching what a correct version wrote.
2. **Bounded audit.** Once a day per active source, and at every epoch's first capture, the
   daemon re-hashes `[0, captured_to)` from disk with a fresh hasher. It compares the result
   with the committed whole-file digest at `captured_to`.
   - The audit runs only if identity, size and mtime are unchanged since the capture, so it
     compares the same input. [DDIA ch. 13, Designing for auditability]
   - On a mismatch it re-reads once. A stable mismatch is `Replay{AuditMismatch}` plus a counter,
     and this is also how a rewrite between the windows is caught.
   - The source is parked only after repeated mismatches on new epochs.
   - Cost: 70 ms per 19 MB. [measured]
   - The reviewed design quarantined on any mismatch, which included normal appends. [review,
     01 B2, 04 B2]
3. **Read-back restore.** Once a day the daemon restores up to ten sessions captured that day
   through the `memory-reader` library. It compares the bytes with
   `transform(local)[0, captured_to)` where identity is unchanged.
   - A mismatch is counted, logged by class, and the source is replayed. A repeat parks it.
   - This is the restore the backup is proven by. [DDIA ch. 13, Don't just blindly trust what
     they promise]
   - It runs on a low-priority lane and never blocks captures. [review, 04 m3]

Neither check trusts the index, the planner or the hasher it is checking.

### 11.5 Logs and supervision

- `tracing` writes to stderr, and the supervisor owns retention.
- Info level logs owner ids, kinds and classes. Paths appear only at debug. Content never
  appears. [review, 05 m5]
- launchd uses `KeepAlive` with `SuccessfulExit=false`. systemd uses `Restart=on-failure` with a
  start limit.
- Exit codes: 0 only on a requested stop, and distinct codes for "lock held" and "config
  invalid".
- A corrupt index is rebuilt, never a crash loop. [review, 03 m3]

## 12. Concurrency and single instance

- **Lock first.** The daemon takes `flock(state/daemon.lock, LOCK_EX|LOCK_NB)` before
  anything else, and exits with the "lock held" code if another process holds it. The kernel
  releases the lock when the process dies.
- **Two mechanisms, two scopes.** `flock` protects the local files. The ETag on
  `current.json` fences any PUT a killed daemon left in flight, and content keys make late
  duplicates harmless. A killed client's fully sent PUT can still land (3.8), which the startup
  delay of 11.2 covers. [DDIA ch. 9, Fencing off zombies; review, 03 B1]
- **Ownership and limits.** One task owns the index and status, and they are updated by
  message. At most 2 hash or compress jobs run at once, and at most 8 uploads host-wide
  [default, unmeasured]. One
  capture is in flight per owner.
- **Core and runtime.** `memory-core` is synchronous and has no I/O or runtime dependency. The
  shell's runtime follows the client chosen in prototype P2.
- **Simulation testing.** The clock, the filesystem and the store sit behind traits that the
  simulator controls, so property 4 runs deterministically and replays from a seed.
  [DDIA ch. 9, Deterministic simulation testing]

## 13. Crates and dependencies

Workspace:

- `memory-core`, library. Types, locator, planner, coalescer, manifest and schema types, policy
  and transform. No I/O crates, enforced by `cargo-deny` bans.
- `memory-reader`, library. Restore and export. Used by the daemon's read-back and by
  `memory restore`.
- `memory`, binary. Subcommands `daemon`, `hook`, `status`, `reconcile`, `restore`, `export`
  and `unpark`. `reconcile` takes the daemon lock and refuses to run beside a daemon.
- `xtask`. Schema export and the lint-check crate.

| Crate | Use | Note |
|---|---|---|
| `object_store` 0.14.2 or `rusty-s3` 0.11.0 + `ureq` | R2 PUT, GET, LIST, conditionals | [prototype P2] |
| `notify` 8.2.x, pinned exactly | FSEvents and inotify | No debouncing in the crate; ours is the coalescer |
| `tokio` | Runtime, binary only, only if P2 picks `object_store` | |
| `zstd` | Compression, level 3 | |
| `sha2` 0.11.1 | Digests and resumable state | `SerializableState` has an internal format; the index stores the crate version and re-hashes on mismatch |
| `serde`, `serde_json`, `schemars`, `toml` | Schemas and config | A duplicate-key-rejecting JSON path for scrubbing |
| `tracing`, `tracing-subscriber`, `clap`, `thiserror` | Logs, CLI, errors | `clap` is not used by `memory hook` |
| `rusqlite` bundled | SQLite backup API | Slice 3 |
| `rustix`, direct dependency of `memory`, features `std` and `fs` | Named open flags and errno values in the source read | Pinned to the version in the lockfile (A12.4) |
| `hegeltest` `=0.52.0`, `static-engine` | Property tests, dev only | 255 releases since 2026-03-16. Pinned exactly [review, 06 F4] |
| `proptest`, `proptest-state-machine` | Standing cross-check of properties 1 and 4 | |
| `cargo-nextest`, `cargo-deny`, `cargo-llvm-cov`, `cargo-mutants` | Gates | |

No custom retry framework, queue or storage engine.

## 14. Coding standards

The lint table is adapted from BeautifulRust's `lints/lints.toml`, workspace-wide:

- `deny`: `unwrap_used`, `expect_used` outside tests, `panic`, `indexing_slicing`, `todo`,
  `dbg_macro` and `print_stdout`.
- `forbid`: `unsafe_code`.
- `deny` on `allow_attributes`, so every suppression is `#[expect(lint, reason = "…")]` and
  fails when stale.
- `missing_docs` and `missing_errors_doc` are `warn`, promoted to `deny` in CI for
  `memory-core` and `memory-reader`.
- Byte-range access goes through one `slice` helper module that returns `Result`, so
  `indexing_slicing` does not spread `#[expect]` across offset code. [review, 06 F7]
- The lint table is proven by a crate of deliberate violations, one per lint, run by
  `scripts/check-lints.sh`.

Type rules:

- Sum types for every state, and newtypes for every identifier and offset.
- Parse at every boundary, and match exhaustively.
- Derive wire types from the schema module.
- A runtime assertion marks a type to strengthen.

Naming follows the Rust API Guidelines, with modules named for the noun they own. Errors are
enums per module, built with `thiserror`. Comments explain a non-obvious why only, and test
names document the what.

## 15. Testing

Gates, in order, all required:

1. `cargo fmt --check`
2. `cargo clippy` with the lint table
3. `cargo deny check`
4. `cargo nextest run`
5. Hegel properties at 500 cases in CI, with the seed recorded on failure
6. The proptest cross-check
7. Schema drift
8. `cargo llvm-cov`, with a fixed line-coverage floor of 80 % (`rust/coverage-floor.txt`)
9. The lint-check crate

Each test runs once: gate 4 runs every test that is not a named property, gate 5 the `hegel_`
tests and gate 6 the `proptest_` tests, all with nextest and at the same case counts (A12.5).

The coverage floor is a backstop, not the latest measurement. It is a hard gate and is not
lowered to make a run pass. It is raised only by a recorded decision, never automatically from
a measurement. The evidence comes from independent-oracle properties with their coverage
guards, production mutation checks and the consumer gate. Tautological tests (an oracle
re-derived from the code under test, a fake asserted against itself, derive-only) are removed
rather than kept for line count. [default, unmeasured: no measurement supports 80] (A9, A10.5)

Monthly: `cargo mutants` on `memory-core`.

Properties are labelled safety (nothing bad happens) or liveness (something good eventually
happens). [DDIA ch. 9, Distinguishing between safety and liveness] Oracles are stated
independently of the implementation. [DDIA ch. 9, Defining the correctness of an algorithm]

1. **Capture correctness.** Generators cover appends, same-size overwrite, rename-replace,
   truncate-then-regrow and mid-file rewrite. [review, 01 B3]
   - 1a, safety: for every change that alters identity or touches a window, no committed
     manifest's pieces differ from the bytes the source held at those offsets. The oracle is
     `file_at_capture.starts_with(reassembled)`, computed from the generator's own model and
     never from the planner.
   - 1b, liveness: for any rewrite, after one audit run the latest epoch reassembles the
     current file.
   - Mutation check: with the window check disabled, 1a must fail. CI runs this.
2. **Coalescer.** Safety: it never emits before `quiet` unless `cap` elapsed. Liveness: it
   always emits within `cap`, and at once on `SessionEnd`.
3. **Idempotence.** Safety: `reconcile` run twice against an unchanged disk and R2 makes zero
   PUTs, including after a writer version change.
4. **State machine.** Steps: `append`, `rewrite`, `truncate`, `delete`, `rename-replace`,
   `crash-before-put`, `crash-after-piece`, `crash-after-manifest`,
   `committed-reply-lost`, `committed-then-5xx`, `restart`, `delete-index`,
   `remove-source-after-commit`, `go-offline`, `go-online` and `revoke-token`. The fake store
   is deterministic.
   - Safety: every object referenced exists, and every committed epoch satisfies 1a.
   - Liveness: after `go-online` and a final reconcile, the latest epoch reassembles the file
     wherever the source still exists.
   - Removing the source after commit stops a later recapture from masking a loss. [review,
     05-history D9]
   - Written in both Hegel and `proptest-state-machine`.
   - Crash point, safety: drop the daemon between `stage` and `publish` of a state file, reload,
     and assert that the previous or the new body is read, never a mix. (A7)
   - Late write, safety: a late `current.json` write from an earlier failed capture lands
     before the next verification. The result is `AlreadyOurs`, not a `Foreign` park. (A6)
   - Durable state writes take a `SyncPolicy { Full, None }` on `StateDir`, `Full` by default.
     Only the daemon simulation properties select `None`, which keeps temp, write, rename and
     directory open and skips only the flush calls. `state.rs`'s own tests, the daemon example
     tests and the binary always use `Full`. A flush cannot change what a live process reads
     back, so no assertion loses evidence. (A7)
5. **Privacy.** Safety: for any source set with synthetic canaries planted in protected
   basenames, credential paths, symlink chains, backup suffixes, unknown kinds, argv and env,
   no PUT body and no hint line contains a canary.
6. **Schemas.** Every schema round-trips, and an unknown `schema` string is rejected. Applying
   deltas forward from checkpoint k gives the same state as checkpoint k+64.
7. **Locator.** `locate` is total, its result is stable when unrelated siblings are created,
   and every unmapped path is counted. [review, 05-history I1, I2]

Coverage guard: a property run fails if its generator never produced multi-epoch, restart,
crash-after-manifest, lost-reply and delete-index cases. [review, 06 F9]

Counter tests:

- An idle minute makes zero file syscalls through our I/O wrapper, and at most the listed
  timer wakeups.
- A full reconcile at 30k synthetic sources makes at most one stat per source and finishes in
  under 1 s.
- A capture of a 59 KB append reads at most 59 KB plus 68 KiB.

Journey test: fixtures replayed from this Mac's session timelines (timestamps and sizes only)
through the real daemon against the fake store. It asserts the byte and request totals in
section 3, then restores every session through `memory-reader`.

Footprint targets [target, measured in slices 2 and 3]:

| Metric | Target |
|---|---:|
| `memory hook` in-process time, `main` entry to exit, p99 | 1 ms |
| Spawn latency of `memory hook` against `/usr/bin/true` on the same host | informational |
| CPU per capture at the median piece | 20 ms |
| Wakeups per idle minute | at most the listed timers (60 s tick, index flush when dirty) |
| Resident memory at 30k sources, idle | 40 MB |
| Resident memory, peak during backlog | 128 MB |
| Disk: index, status and 7 days of hints | 20 MB |
| Full safety reconcile at 30k sources | 1 s, at most one stat per source |
| Oldest uncaptured age, reachable network | 10 min |

The reviewed design's 2 ms p99 wall-clock hook target is dropped. Spawn alone measured p99
10.8 ms. [review, 06 F5]

## 16. Prototypes

Sonnet workers run these once the design is final. Each writes
`docs/rust-rewrite/prototypes/<id>-<name>.md` with labelled measurements, and the result
decides between the alternatives. Workers follow the safety rules in
`docs/rust-rewrite/challenge/README.md`: synthetic data in their own scratchpad, no transcript
content, no credentials, and no touching installed hooks or services. The only R2 they may use
is the `rust-acceptance/<run-id>/` prefix of the bucket (section 6.1).

| Id | Question | Alternatives | Decides on | Needs | Before |
|---|---|---|---|---|---|
| P1 | Change detection | 4 KiB head + 64 KiB tail windows; full re-hash per capture; windows sized 16 or 256 KiB | Detection rate on generated rewrites; bytes read per capture | none | slice 1 |
| P2 | S3 client | `object_store` + tokio; `rusty-s3` + `ureq` on a 4-thread pool | Conditional PUT correctness including lost reply and committed-then-5xx; disabling retries on `current.json`; deterministic simulation (MadSim against trait-level DST); binary size; RSS under 8 PUTs; maintenance | bucket credentials (A2) | slice 2 |
| P2b | R2 checksum enforcement | send a wrong Content-MD5 and `x-amz-checksum-sha256` | Information only, not a gate | bucket credentials (A2) | slice 2 |
| P3 | Hint log | per-day files; rename rotation | Lines lost with writers descheduled across the boundary | none | slice 2 |
| P4 | Hook timing and memory | `memory hook` with hand-parsed argv; piece cap 4 or 8 MiB; uploads 4 or 8 | In-process p99; RSS peak against the 128 MB target | none | slice 2 |
| P5 | Prefix rewrite frequency | waived (A1) | Correctness never depended on it: a prefix rewrite is detected by the window check (7.2) and becomes `Replay`. The frequency is measured later from the running daemon's counters `Replay{WindowMismatch}` and `Replay{AdoptMismatch}`; no collector reads private sources | none | not a prerequisite |
| P6 | Clock across sleep | `Instant` (monotonic); `CLOCK_BOOTTIME` / `mach_continuous_time` | Coalescer and backoff behaviour across a natural sleep; wake-gap detection fires | none | slice 2 |
| P7 | Local file checksums | SHA-256; CRC32C | Cost at a 20 MB index; torn-write injection detected | none | slice 2 |
| P8 | Cross-session duplicate share | count only | Informational; the namespace is already decided | none | any |
| P9 | SQLite unchanged test | db + WAL fingerprint; backup and hash | False "unchanged" rate under a concurrent writer; cost | none | slice 3 |
| P10 | Linux racy mtime | ext4 on BC-01 | Same-size rewrites in one tick leaving equal (size, mtime) | BC-01 scratch dir | slice 3 |
| P11 | Commit retry after `BaseUnchanged` exhaustion | 8 approaches including fail fast and a hybrid; retry class | p99 latency at 20 % errors; unlanded bytes (3.8) | a `rust-acceptance/` prefix | done 2026-11-09 |
| P12 | Durable state write cost | `F_FULLFSYNC`, `F_BARRIERFSYNC`, flush skipped in simulation tests | Flush time per minute; simulation test time (3.9) | none | done 2026-11-09 |

## 17. Delivery slices

0. **Scaffold.** Workspace, lint table and check crate, `cargo-deny` bans, nextest, Hegel
   pinned with `static-engine` building on the Mac and BC-01, proptest, schema export, a
   counter-test harness, and the DST traits. Prototypes P1, P3 to P8 can run in parallel.
   Exit: every gate is green on an empty core.
1. **Core.** Types, locator, planner, coalescer, delta and checkpoint manifests, the policy and
   transform ports with the Go fixtures, the launch allowlist, and hint parsing. Properties 1,
   2, 3, 5, 6 and 7 with the coverage guard.

   The slice also produces a "Go fix to Rust counterpart" table for every edge case in
   `challenge/06-strategy-engineering.md` F2. Any row marked "not applicable" needs a reason.
   [review, 06 F2]

   Exit: properties green at 2,000 cases locally, the proptest cross-check green, and the
   table complete.
2. **Daemon on the Mac.**
   - Builds: the lock, hint files, watcher, index and status with durable writes, the commit
     protocol, and the client chosen by P2.
   - Runs against the fake store and the `rust-acceptance/` prefix of the bucket.
   - Captures append-class sources for all three agents. Snapshot and config kinds stay
     withheld until slice 3.
   - Adds property 4 and the counter tests. Footprint is measured, and remeasured in slice 3.
   - Exit: a day of this Mac's sessions captured, with totals matching section 3.
2b. **Reader.**
   - Builds: `memory-reader`, `memory restore <owner> --out DIR`, the daemon's read-back reusing
     the library, and `memory export` to the `session-reader/v2` envelope.
   - The existing Python importer, event index and report run against the export unchanged.
   - Shadow run on the Mac into the `rust-acceptance/` prefix, comparing every restored session
     with `transform(local)`. Local session files that have no captured owner are counted as the
     coverage cross-check. [review, 06 F1, F2; A13.1]
   - Exit criterion of the shadow run and of cutover: 2 consecutive days with zero restore
     mismatches, zero parked owners and zero local session files without a captured owner. It
     replaces any longer fixed shadow-run length. [A13.1, A14.2]
   - Abort triggers: any restore mismatch, any owner parked as `Foreign`, or any privacy
     canary found in an export stops the run until the cause is found. [A13.1]
   - Exit: every captured session restores to its source bytes, and the consumer gate is green.
3. **Linux and snapshot classes.**
   - Builds: BC-01 inotify, SQLite and config snapshots, shared stores, deletions, launch
     evidence for every agent, the loss-window counter, and reconcile tuning.
   - Prototypes P9 and P10.
   - A shadow run on BC-01 with the exit criterion of slice 2b. [A14.2]
   - Exit: both hosts green, and the gap register updated.

## 18. Cutover

Cutover follows the contract's sequence (`docs/session-capture-contract.md` section 6),
simplified because no R2 data is kept. [decided 2026-11-07]

No production write happens before all of these exist (A12.2, A13.2): zstd encoding of pieces,
so the `zstd-3` label and the `.zst` key suffix are true (the schema gains no `identity`
encoding); the audit of 11.4, with a property that covers a rewrite between the windows; hint
retention (7.6, 9.1); and `tracing` to stderr as sections 11.5 and 13 specify.

1. No production bucket is created. Both hosts use the existing `agent-sessions` bucket (A2).
2. Install `memory` and its hooks on one host. The Go capture daemons and the
   publication-refresh job are already stopped (A0). Remove the Go hooks' launch paths, then
   repeat on the other host. One collector runs per host. [review, 04 M4]
3. Remove the one existing session opt-out record from `~/.local/state/agent-sessions/exclusions`
   once the Go path is stopped. Before that, the Go path still honors it.
4. Let the safety reconcile capture every available local source into the fresh archive.
   Nothing is imported from the old R2 archive. Sessions that exist only in the old archive are
   not recovered, which the operator accepted.
5. Verify fresh automatic capture, restart and retry, restore, and analytics on both hosts,
   against the exit criterion of slice 2b (A13.1).
6. The old archive was deleted on 2026-11-09 (A2). Before rollout, delete every
   `rust-acceptance/` prefix and verify that the bucket holds only `delivery-artifacts/` and
   data written by the accepted Rust writer.

## 19. Decisions

Operator decisions, 2026-11-07:

- Storage is plaintext (R9, 8.6).
- One shared bucket for both hosts.
- Sessions are never deleted.
- The reader is in scope.
- Manifests are deltas with a checkpoint every 64.
- Session opt-out is retired.
- None of the existing R2 data is kept, and nothing is migrated.

Defaults adopted. Each applies unless the operator says otherwise:

1. **No bucket lock.** With plaintext storage, an indefinite lock would make a scrubber bug
   permanent. [`07-ddia-crosscheck.md` blocker 3, callout 6] Revisit with the reader project.
2. **Credentials.** One Object Read and Write token per host, scoped to the bucket. The
   alternative is short-lived prefix- and action-scoped credentials minted off the host.
3. **Low-value kinds.** `ClaudeDebug` and file-history are captured, for the holistic view
   the operator asked for. [`07-ddia-crosscheck.md` callout 6]
4. **Removing a leaked secret.** This is the only planned deletion. With a global `objects/`
   namespace it needs a reference check over every manifest chain before an object is deleted.
   The tool belongs to the reader project. [review, 02 M3, 05 M6]
5. **Hegel on BC-01.** If it fails to build in the Nix shell on aarch64 Linux, the proptest
   versions of the properties become the gate.
6. **Code location.** `rust/` in this repository.

## 20. Decision log

| Id | Decision | Evidence |
|---|---|---|
| D1 | Append piece per quiet window; identity plus head and tail windows before an append; replay from byte 0 on mismatch; no writer-side consolidation | 3.1, 3.3, 7.2; 01 B1, M1 |
| D2 | Window quiet 60 s, cap 300 s; `SessionEnd` flushes | 3.2; 03 M5 |
| D3 | Signals: hooks appending to per-day hint files, filesystem notifications with directory readdir, safety reconcile every 15 min and on wake | 3.4, section 10; 03 M1, M2, M3 |
| D4 | Local state classified as system of record or derived; no database, spool or failure log; index rebuilt by adopting R2 positions | section 9; 01 m2, 04 B3, DDIA ch. 6 |
| D5 | Strict versioned schemas for ours, key reads for theirs; writer recovery reads only positions and parks on a newer schema | R7, 6.5; 04 M4 |
| D6 | Standards: adapted BeautifulRust table, type rules, Hegel pinned plus a proptest cross-check | 13, 14; 06 F4, F7 |
| D7 | Scope ends when `current.json` is durable; a minimal reader is in scope; nothing from the old R2 archive is kept | section 1 |
| D8 | Loss window counted, as defined in 11.3, before any spill buffer | 11.3; 04 M2 |
| D9 | Revised: Content-MD5 is sent only if the chosen client can cheaply set it; end-to-end integrity is reader verification plus the restore sample | 7.3; 02 B1, DDIA ch. 13 |
| D10 | Revised: writer per epoch; bounded, input-pinned audit that replays rather than quarantines; read-back through `memory-reader` | 11.4; 01 B2, 04 B2 |
| D11 | Verify-by-GET commit protocol; the manifest hash is the request id; a foreign writer parks the owner; no merge | 7.4; 02 B2, M1, DDIA ch. 8, 9, 13 |
| D12 | Protected-source pipeline ported from Go; `Authorized<Redacted<Bytes>>` is the only PUT body | section 8; 05 B1, M4, M5 |
| D13 | Objects keyed by the hash of the transformed bytes in a global namespace (A10.2) | 6.2; 02 M4, M3 |
| D14 | Delta manifests with a checkpoint every 64 | 3.7, 6.3; 02 M2 |
| D15 | Durable `status.json`, closed error classes, `DestinationAuth` pause, heartbeat with `last_commit_at` | 11.2, 11.3; 04 B1, DDIA ch. 6, 9 |
| D16 | `flock` single instance; ETag fences R2 | section 12; 03 B1 |
| D17 | Parked list instead of R2 quarantine markers; DLQ parks one source; `memory unpark` | 11.2, 11.3; 04 B2 |
| D18 | 8 MiB piece cap, 2 hash jobs, 8 uploads `[default, unmeasured]`, size-scaled timeouts | 7.3; 04 M5 |
| D19 | Plaintext storage with a stated threat model; no bucket lock | 8.6, 19; 05 M1, M2 |
| D20 | Session opt-out retired; shared stores owned by `Owner::Shared`; withheld sources not recorded in R2 | 7.5, 6.3; operator 2026-11-07 |
| D21 | Slice 2b reader, shadow runs, Go fix table, acceptance under `rust-acceptance/` | 17; 06 F1, F2, F3; A2, A13.1 |
| D22 | 2026-11-09: Go capture daemons and refresh job stopped; Go hooks remain until cutover | A0; 0, 18 |
| D23 | 2026-11-09: P5 waived; rewrite frequency measured from the daemon's counters | A1; 7.2, 16 |
| D24 | 2026-11-09: one bucket, the existing `agent-sessions`; acceptance under `rust-acceptance/<run-id>/`; no production or acceptance bucket is created | A2; 6.1, 18 |
| D25 | 2026-11-09: the hook records no launch argv; 2026-11-10: a failed hook write loses the record, accepted | A3, A11.1; 7.6, 8.4, 10.4 |
| D26 | 2026-11-09: symlinked files are skipped by the walk | A4; 8.2 |
| D27 | 2026-11-09: state root `$HOME/.local/state/memory`, `MEMORY_STATE_DIR` override | A5; 9.1 |
| D28 | 2026-11-09: `BaseUnchanged` exhaustion is class `Transient`; `known_ours`; full jitter | A6; 7.4, 11.2; P11 |
| D29 | 2026-11-09: durable writes unchanged in production; `SyncPolicy` in tests; crash-point property | A7; 9.2, 15; P12 |
| D30 | 2026-11-09: prototype ids P11 and P12 | A8; 16 |
| D31 | 2026-11-09: coverage floor fixed at 80 %, not the latest measurement | A9; 15 |
| D32 | 2026-11-10: corrections of fact and evidence labels: 3.1 is 30 s data, the object key hashes the transformed bytes, `[default, unmeasured]` constants, watcher loss evidence, `sync_all` is `F_FULLFSYNC` | A10.1 to A10.6; 3.1, 3.9, 6.2, 7.3 |
| D33 | 2026-11-10: launch values need a safe shape, not only a safe name | A12.1; 8.4 |
| D34 | 2026-11-10: source read refuses a changed file; state file bounds; base recovery reads at most 64 manifests; `rustix` is a direct dependency; gates 4 to 6 run each test once | A11.4, A11.5, A12.3 to A12.5; 6.3, 7.3, 9.2, 13, 15 |
| D35 | 2026-11-10 and 2026-11-11: prerequisites of the first production write: zstd pieces, audit, hint retention, `tracing` | A12.2, A13.2; 18 |
| D36 | 2026-11-11: shadow run and cutover exit criterion, abort triggers; the Go cross-check is dropped | A13.1; 17, 18 |
| D37 | 2026-11-11: no separate cap on allowlisted environment names | A13.3; 8.4 |
| D38 | 2026-11-11: ETag and dead-process write behaviour measured on R2; the fence's reliance on unique manifest hashes | A13.4; 3.8, 7.4, 11.2 |
| D39 | 2026-11-11: the verification GET is attempted 4 times; the watcher skips a missing root | A13.5, A13.6; 7.4, 10.2 |
| D40 | 2026-11-12: a restarted daemon's first commit is at least 5 s after start | A14.1; 11.2 |
| D41 | 2026-11-12: the 2-day exit criterion replaces the 7-day shadow runs of slices 2b and 3 | A14.2; 17 |
| D42 | 2026-11-12: the baseline hash pin is removed; amendments A0 to A13 are folded into this document; DESIGN.md and AGENTS.md are edited directly | A14.3; 0 |

Finding-to-section map for the challenge review:

| Finding | Section |
|---|---|
| Blocker 1, prefix proof (01 B1, M1) | 5, 7.2 |
| Blocker 2, property oracle (01 B3) | 15 property 1 |
| Blocker 3, scrubbing and denylist (05 B1) | 8 |
| Blocker 4, lost reply on `current.json` (02 B2) | 7.4 |
| Major (was blocker 5), Content-MD5 (02 B1) | 7.3, P2b |
| Blocker 6, index loss (01, 04 B3, 05, 06) | 9.1, 9.3 |
| Blocker 7, audit false quarantine (01 B2, 04 B2) | 11.4 |
| Blocker 8, durable failure state (04 B1) | 11.2, 11.3 |
| Blocker 9, single instance (03 B1, 04 M4) | 12 |
| Blocker 10, reader (06 F1) | 1, 17 slice 2b |
| Majors: namespace, raw keys, manifest cost | 6.2, 6.3, 3.7 |
| Majors: capture-whole livelock and SQLite, locator, racy stat | 7.2, 7.3, 5 |
| Majors: hint rotation, new-directory race, coalescer and wake, footprint targets | 10, 7.1, 15 |
| Majors: memory, upgrades, fsync, delivery, Hegel, async | 7.3, 6.5, 9.2, 17, 13, 16 P2 |
| Majors: exclusion registry and timing (05 M3), withheld traces (05 M5) | retired with opt-out, 6.3 |
| DDIA callouts 1 to 8 | 12, 7.1, 9.2, 6.3, 3.6, 8.6, 8.6, 6.1 |
