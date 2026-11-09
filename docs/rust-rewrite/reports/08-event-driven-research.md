# Event-driven collector research (sections 2 to 6)

Labels: doc = read in the cited source this session. inferred = my reasoning. memory = from prior knowledge, not re-read.
Section 1 skipped per brief.

## 2. Log shippers

### Vector file source and aws_s3 sink
- Discovery: Vector "will continually look for new files matching any of your include patterns." Globbing re-runs every `glob_minimum_cooldown_ms`, default 1000. doc. https://vector.dev/docs/reference/configuration/sources/file/
- Change detection is polling. The FileServer doc comment says it "must poll for changes. That is, no event notification is used by `FileServer`." The user docs page is silent on this. doc (source). https://raw.githubusercontent.com/vectordotdev/vector/master/lib/file-source/src/file_server.rs
- Read loop sleeps with doubling backoff, capped at 2048 ms, when no bytes were read. Rescan and checkpoint writes happen on the glob cooldown interval, not every iteration. doc (source). Same file URL.
- Fingerprint: default `checksum` (CRC over first `fingerprint.lines`, default 1 line). Alternative `device_and_inode`. doc. https://vector.dev/docs/reference/configuration/sources/file/
- Checkpoints live in `data_dir`. Default `read_from` is `beginning`. `max_line_bytes` default 102400. Header says `delivery: best effort`, `acknowledgements: yes`. doc. Same URL.
- Buffer model: sink buffer default `memory`, 500 events. Disk buffer is a write-ahead log, min about 256 MiB, fsync every 500 ms, survives crash. `when_full` default `block` (backpressure upstream). Memory buffers are lost on crash. doc. https://vector.dev/docs/architecture/buffering-model/ and https://vector.dev/docs/reference/configuration/sinks/aws_s3/
- aws_s3 batching: `batch.max_bytes` default 10 MB uncompressed, `batch.timeout_secs` default 300. `request.retry_attempts` effectively unlimited, `retry_max_duration_secs` default 30, `key_prefix` default `date=%F`. doc. https://vector.dev/docs/reference/configuration/sinks/aws_s3/
- Sink outage: sink buffer fills, `block` pushes backpressure to the source, source stops advancing its checkpoint (acks enabled). The buffering page does not spell this chain out; the chain is inferred. inferred. https://vector.dev/docs/architecture/buffering-model/

### Fluent Bit tail input and s3 output
- Discovery: `refresh_interval` default 60 s ("interval of refreshing the list of watched files"). doc. https://docs.fluentbit.io/manual/data-pipeline/inputs/tail
- Change detection: `inotify_watcher` default true. Set false for stat watcher. So reads are event-driven, new-file discovery is still a periodic refresh. doc. Same URL.
- Position state: `db` option, a SQLite file, no default (unset means no persistence). `db.sync` default `normal`. `read_from_head` default false. `rotate_wait` default 5. doc. Same URL.
- Buffering: input `mem_buf_limit` default none, "if the limit is reached, it will be paused." `storage.type` default `memory`, alternative `filesystem` (mmap chunk files, needs global `storage.path`), `memrb` drops oldest. doc. https://docs.fluentbit.io/manual/administration/buffering-and-storage
- Per-output `storage.total_limit_size`: when reached, the oldest chunk is discarded. doc. Same URL.
- s3 output: `total_file_size` default 100000000, `upload_chunk_size` default 5 MiB, `upload_timeout` default 10m, `store_dir` default `/tmp/fluent-bit/s3` ("Directory to locally buffer data before sending"), `store_dir_limit_size` default 0 (unlimited; when set and hit, new data is discarded), `preserve_data_ordering` default true, `retry_limit` default 5. doc. https://docs.fluentbit.io/manual/data-pipeline/outputs/s3
- The s3 output always buffers to local disk. "S3 requires a writeable filesystem." On crash, unsent data stays in `store_dir` and upload metadata is kept so multipart uploads can finish after restart. doc. Same URL.

### Filebeat filestream
- Discovery: `prospector.scanner.check_interval` default 10s. "At the moment only simple file system scanning is supported." No fs-event notification. doc. https://www.elastic.co/docs/reference/beats/filebeat/filebeat-input-filestream
- Identity: `file_identity: fingerprint` default (hash bytes 0 to 1024). `native` (inode and device) was the default before 9.0.0. doc. Same URL.
- Harvester lifecycle: `close.on_state_change.inactive` default 5m. `harvester_limit` default 0 (none). `clean_inactive` default -1 (off). `close.reader.on_eof` default off. doc. Same URL.
- Truncation: with fingerprint identity in 9.5+ the input detects copytruncate rotation itself. doc. Same URL.
- State: registry at `${path.data}/registry`, `registry.flush` default 1s, perms 0600. doc. https://www.elastic.co/docs/reference/beats/filebeat/configuration-general-options
- Delivery: "guarantees that events will be delivered to the configured output at least once." Registry stores delivery state per event. If output is unreachable it "keeps track of the last lines sent" and retries until acknowledged. Unacked events are resent after restart, so duplicates occur. doc. https://www.elastic.co/docs/reference/beats/filebeat/how-filebeat-works

### Promtail and Grafana Alloy loki.source.file
- Promtail: positions file, `sync_period` default 10s ("How often to update the positions file"). doc (source docs at tag v3.4.2). https://raw.githubusercontent.com/grafana/loki/v3.4.2/docs/sources/send-data/promtail/configuration.md and positions code https://raw.githubusercontent.com/grafana/loki/v3.4.2/clients/pkg/promtail/positions/positions.go (default `10*time.Second`).
- Promtail target manager imports `fsnotify` and creates an `fsnotify.Watcher` for directories. Separate `sync_period: "10s"` resyncs watched directories and tailed files "to discover new ones or stop watching removed ones." doc (source). https://raw.githubusercontent.com/grafana/loki/v3.4.2/clients/pkg/promtail/targets/file/filetargetmanager.go and the configuration.md above.
- Alloy: `file_match.sync_period` default 10s ("How often to sync filesystem and targets"). `file_watch` block polls, `min_poll_frequency` and `max_poll_frequency` default 250ms, doubling when idle. The page mentions no fsnotify. `tail_from_end` default false. Positions are stored under `--storage.path`. doc. https://grafana.com/docs/alloy/latest/reference/components/loki/loki.source.file/
- Memory: Promtail is deprecated in favour of Alloy. I did not fetch a deprecation notice this session. memory.

### Comparison

| Shipper | New file discovery | Change detection | Position state | Source to sink buffer | Sink outage |
|---|---|---|---|---|---|
| Vector | glob rescan, 1 s | polling, backoff to 2 s | checkpoints in data_dir | memory 500 events, optional disk WAL | block, backpressure to source |
| Fluent Bit | refresh 60 s | inotify (default) or stat | SQLite `db` (optional) | memory, optional filesystem chunks; s3 output always stages in store_dir | pause on mem_buf_limit; drop oldest when total_limit_size hit |
| Filebeat | scan 10 s | polling | registry, flush 1 s | in-memory queue (not in fetched pages) | retry until ack, resend after restart |
| Promtail | fsnotify plus 10 s resync | tailer | positions file, sync 10 s | client batch (not verified) | not verified |
| Alloy | sync 10 s | polling 250 ms to max | positions in storage.path | not covered on page | not covered on page |

### Conclusion for section 2
- None of the five is purely event-driven. Every one has a periodic scan or resync (1 s, 10 s, 10 s, 10 s, 60 s). inferred from the doc rows above.
- None is stateless. Every one persists a per-file position (data_dir checkpoint, SQLite db, registry, positions file). Only the sink side differs. doc rows above.
- Only Fluent Bit uses inotify as the primary read trigger, and it still carries a 60 s refresh and a SQLite db. doc.
- Position state is local in all five because the sinks (Elasticsearch, Loki) cannot answer "how far did you get". Our R2 sink can, via the object itself. inferred.
- The one shipper whose sink is S3 (Fluent Bit s3, Vector aws_s3) still stages locally: Fluent Bit on disk always, Vector in memory by default with optional disk. doc.

## 3. Event-driven design principles

### Edge vs level, and reconcile
- Edge-triggered: the signal "would happen once, at the time of the addition." Level-triggered: seen "continuously from the time of the addition, until the next event occurs." doc. https://hackernoon.com/level-triggering-and-reconciliation-in-kubernetes-1f17fe30333d
- "If a system does not sample the signal frequently enough, it may miss a trigger." Joe Beda: "If you are edge triggered you run risk of compromising your state and never being able to re-create the state." Level triggering "is very forgiving." doc. Same URL.
- Kubernetes controllers are non-terminating loops that move "the current cluster state closer to the desired state." doc. https://kubernetes.io/docs/concepts/architecture/controller/
- controller-runtime FAQ: reconcilers should "adjust to skipped or coalesced events" and read all state they need instead of reacting to the event. doc (fetch tool paraphrased partly; quotes are from its summary). https://github.com/kubernetes-sigs/controller-runtime/blob/main/FAQ.md
- Kubebuilder: "the controller's reconciliation loop needs to be idempotent." Event-specific logic "breaks the recommendation of operator pattern." doc. https://book.kubebuilder.io/reference/good-practices
- Mapping to our case: a hook is an edge. A directory listing compared to remote state is a level. inferred.
- Note: Kubernetes controllers still resync on a timer and on start, not events alone. I did not fetch a page for that. memory.

### inotify
- "an IN_Q_OVERFLOW event is always generated" when the queue is full, and excess events are dropped. doc. https://man7.org/linux/man-pages/man7/inotify.7.html
- `max_user_watches` caps watches per real user ID. `max_queued_events` caps queued events per instance. doc. Same URL.
- "Inotify monitoring of directories is not recursive." doc. Same URL.
- Race: "by the time you create a watch for the new subdirectory, new files (and subdirectories) may already exist." doc. Same URL.
- Identical unread events are "coalesced into a single event." Remote events on network filesystems are not caught. doc. Same URL.
- Not relevant on macOS (our Mac), relevant on BC-01 if Linux. inferred.

### FSEvents (macOS)
- The `MustScanSubDirs` flag is set when events are coalesced between a directory and its subdirectory. "you must recursively rescan the path listed in the event." doc. https://developer.apple.com/library/archive/documentation/Darwin/Conceptual/FSEvents_ProgGuide/UsingtheFSEventsFramework/UsingtheFSEventsFramework.html
- "When an event is dropped, the `kFSEventStreamEventFlagMustScanSubDirs` flag is also set." doc. Same URL.
- Events persist across reboots. Store the last event ID and pass it as `sinceWhen` to replay. doc. Same URL.
- Treat the list as "advisory rather than a definitive list." "Backup software should still periodically perform a full sweep." doc. Same URL.
- "To avoid missing changes, you must start monitoring the directory before you start scanning it." doc. Same URL.
- Events are per directory, not per file, unless the file-level flag is used. The page says "something in a given directory changed." doc. Same URL.
- The Apple flags reference page returned no body text to the fetch tool. Flag semantics above come from the programming guide. https://developer.apple.com/documentation/coreservices/1455361-fseventstreameventflags (not read).
- Apple itself recommends a periodic full sweep. That is a direct doc counter to "no periodic scanning". doc.

### Dual write and transactional outbox
- Problem: "How to atomically update the database and send messages to a message broker?" Solution: store the message in the DB in the same transaction, and a relay forwards it. doc. https://microservices.io/patterns/data/transactional-outbox.html
- The relay may publish twice, so "a message consumer must be idempotent." doc. Same URL.
- Mapping: the agent writes the transcript (source of truth, durable). Our upload is the second write. The transcript file is already the outbox. inferred.

### Kafka producer
- `buffer.memory` default 33554432. `max.block.ms` default 60000, bounds waiting for metadata and buffer allocation in `send()`. `enable.idempotence` default true. `delivery.timeout.ms` default 120000. `retries` default 2147483647. `acks` default `all`. doc. https://kafka.apache.org/41/configuration/producer-configs/
- Behaviour when the broker is down: after the buffer fills, `send()` blocks up to `max.block.ms`, then throws. Records in the in-memory buffer expire after `delivery.timeout.ms`. The Kafka producer has no disk spill. Derived from the defaults above. inferred.
- Lesson: even the reference in-memory producer drops or errors after about 2 minutes. It relies on the application's own durable source to retry. inferred.

### S3 and R2 conditional writes
- `If-None-Match: *` fails with 412 if the key exists. First concurrent writer wins, others get 412. A 409 can occur with a concurrent delete. Works on PutObject, CompleteMultipartUpload and CopyObject. doc. https://docs.aws.amazon.com/AmazonS3/latest/userguide/conditional-writes.html
- `If-Match` compares ETag. 412 on mismatch. 404 if the object is gone. doc. Same URL.
- Conditional writes ignore in-progress multipart uploads. A conditional Complete fails with 412 if another client wrote the key first. doc. Same URL.
- R2 PutObject: `If-Match` and `If-None-Match` implemented, "evaluated atomically against the committed bucket state." At most one `If-None-Match: *` write wins. doc. https://developers.cloudflare.com/r2/api/s3/api/
- R2 CompleteMultipartUpload: the R2 page documents no conditional support. This matches the brief. doc (absence). Same URL.
- Idempotency key pattern: content-addressed key plus `If-None-Match: *`. A 412 means "already stored", which is success. inferred.

### What "no local durable state" costs
- Lost trigger means lost data until something rescans. The source files are durable, so the fix is a rescan, not a log. inferred.
- Without a local position, every start must ask R2 what it holds (HEAD or LIST per file). Cost is O(files) requests per reconcile. inferred.
- No local record of failures. Pending and failed work is invisible unless derived from (source listing minus remote listing). inferred.
- Crash mid-upload: multipart parts are orphaned. Needs an `AbortIncompleteMultipartUpload` lifecycle rule. doc. https://docs.aws.amazon.com/AmazonS3/latest/userguide/conditional-writes.html (note at end). R2 lifecycle support for this: not verified.
- Source mutated while offline, truncated or rewritten, is only detectable by comparing with the remote copy. Needs prefix hash or generation id stored remotely. inferred.
- Standard mitigations: reconcile on start and periodically (level triggering); idempotent keyed writes; remote state as checkpoint (Filebeat registry equivalent kept in R2); backlog bounded by the source files themselves; optional small disk spill only for data that no longer exists at the source. inferred.

## 4. Streaming to object storage without append

| System | Size trigger | Time trigger | Local staging |
|---|---|---|---|
| Vector aws_s3 | `batch.max_bytes` 10 MB uncompressed | `batch.timeout_secs` 300 | memory buffer 500 events by default, optional disk buffer |
| Fluent Bit s3 | `total_file_size` 100000000 (1 MB to 50 GB range) | `upload_timeout` 10m | always `store_dir` on disk, default `/tmp/fluent-bit/s3`, multipart in 5 MiB parts |
| Firehose to S3 | 1 to 128 MiB, default 5 | 0 to 900 s, default 300; first condition wins | in memory, managed service |
| Loki ingester | `chunk_target_size` 1572864 (1.5 MB compressed) | `chunk_idle_period` 30m, `max_chunk_age` 2h | WAL enabled by default, dir `wal`; `flush_on_shutdown` false |

Sources, all doc.
- Vector: https://vector.dev/docs/reference/configuration/sinks/aws_s3/
- Fluent Bit: https://docs.fluentbit.io/manual/data-pipeline/outputs/s3
- Firehose: https://docs.aws.amazon.com/firehose/latest/dev/create-configure-backup.html (S3 row: size 1-128 (5), interval 0-900 (300); intervals under 60 s use multipart upload).
- Loki: https://raw.githubusercontent.com/grafana/loki/main/docs/sources/shared/configuration.md (`chunk_idle_period` 30m, `max_chunk_age` 2h, `chunk_target_size` 1572864, `flush_check_period` 30s, wal `enabled` true, `flush_on_shutdown` false).

Findings.
- Standard windows are 5 to 10 MB or 5 to 10 minutes, whichever first. Loki is hours because chunks are tiny and compressed. doc rows above.
- Every system that writes whole objects to S3 stages data first. Firehose in managed memory, Fluent Bit on disk, Vector in memory (disk optional), Loki in memory plus WAL. doc.
- No shipper appends to an object. They create new objects per batch. inferred from the batch rows.
- Our case differs: the source file is already the durable staging file. A hook-time upload can read from the source, not from a buffer. inferred.
- Key point for R2: whole-object PUT with `If-None-Match` for immutable objects (content-addressed segments) avoids needing multipart conditionals. inferred, from https://developers.cloudflare.com/r2/api/s3/api/ (no documented conditional CompleteMultipartUpload).

## 5. Dedup patterns for append-only logs

- Content addressing: key by hash. restic references all content "according to its SHA-256 hash." doc. https://restic.readthedocs.io/en/stable/100_references.html
- Content-defined chunking: restic uses Rabin fingerprints with a 64-byte window, blobs 512 KiB to 8 MiB, about 1 MiB average, files under 512 KiB not split. doc. Same URL.
- borg buzhash defaults: min 512 KiB, max 8 MiB, target about 2 MiB, window 4095 B. doc. https://borgbackup.readthedocs.io/en/stable/internals/data-structures.html
- borg change detection uses size, mtime_ns and inode: "If the file's size, mtime_ns and inode number is still the same, it is considered to not have changed." doc. Same URL.
- FastCDC: "about 10x faster than the best of open-source Rabin-based CDC" with "nearly the same deduplication ratio." doc. https://www.usenix.org/conference/atc16/technical-sessions/presentation/xia
- Prefix hash proof (hash of first N bytes equals stored hash, so the file only grew): this is my design, not a cited system. The closest cited analogue is Filebeat's fingerprint of bytes 0 to 1024 for identity. https://www.elastic.co/docs/reference/beats/filebeat/filebeat-input-filestream doc. inferred for the proof use.
- When each is worth it.
  - Content addressing of whole immutable segments: worth it always. It is the idempotency key. inferred.
  - Prefix hash: worth it for append-mostly JSONL. It distinguishes append from rewrite and truncate at O(1) remote state per file. inferred.
  - CDC: worth it for large files edited in the middle, such as rewritten SQLite or `~/.claude.json`. For append-only JSONL it saves little over append segments. Backup tools target 1 to 2 MiB chunks, which is larger than most transcripts. inferred from the chunk sizes above.
  - Whole-file snapshot with hash check: enough for small config JSON. inferred.
- SQLite store: a consistent copy needs the SQLite backup API or `VACUUM INTO`, not a raw file copy while writers run. memory, not re-read this session.

## 6. Synthesis

Architectures. A: hook-inline upload, no daemon, no state. B: hook-woken resident daemon, fs notifications, in-memory queue, R2 as checkpoint, reconcile on start and on watcher overflow. C: B plus disk spill only on sink failure. D: Go design (durable spool, SQLite jobs, 30 s scan).

| Row | A | B | C | D |
|---|---|---|---|---|
| Lost-event handling | None. A missed hook is lost until a later hook for the same session happens to upload it. Hooks do not fire on interrupt or in `-p` async (known). | Reconcile on start and overflow recovers. Silent loss between overflow-free events is not possible only if notifications are lossless, and Apple and inotify docs say they are advisory. A slow safety sweep is still advised. | Same as B | Periodic scan is the net. Best coverage. |
| Offline laptop 3 days | Hooks fail, nothing retries. Data is in the source files only. | Daemon retries in memory with backoff. On restart or reconnect, reconcile finds everything missing. Works because sources are durable. | Same, plus spill holds data deleted at the source. Marginal | Spool holds jobs. Works. |
| Source truncated while offline | Not detected. Next hook may overwrite or append wrongly. | Reconcile compares prefix hash with remote and detects it, if remote stores prefix hashes. | Same as B | Detected via SQLite state, if recorded. |
| Hookless session | Never captured. | Captured by watcher or reconcile. | Same | Captured by scan. |
| Shared SQLite store rewritten | Hook cannot snapshot safely at an arbitrary time. | Debounce then snapshot via backup API. Needs a timer, so not purely event-driven. | Same | Scan plus snapshot on a schedule. |
| Crash mid-upload | Orphan multipart. Retry only on next hook. | Restart reconcile redoes it. Orphan cleanup by lifecycle rule. | Same, spill replayed | Job row stays pending and retries. |
| Duplicate uploads | Idempotent only with content-addressed keys plus `If-None-Match`. | Same keys. 412 treated as done. | Same | Same keys plus job dedup. |
| Inspect pending or failed | None. Logs only. | Derivable: source listing minus remote listing. No durable failure reason. | Spill directory is inspectable | SQL queries. Best. |
| Resource cost | Zero idle. Hook adds latency to the agent, up to a timeout. | Resident process, one watch per directory. A reconcile costs O(files) HEAD or LIST calls. | B plus bounded disk | Resident process, SQLite I/O, periodic scan. |
| Code size | Smallest | Small to medium | Medium | Largest |

Row basis: rows marked inferred are my reasoning from sections 2 to 5. Cited facts behind them: hook limits in the brief (known), inotify and FSEvents advisory statements (sections 3), shippers all keep local position state (section 2), Kafka producer drops after timeout without a durable source (section 3).

### Test of the hypothesis
- "No periodic scanning": contradicted. All five shippers scan or resync on a timer, and Apple's guide says to do a periodic full sweep. doc (sections 2 and 3).
- "No local state": partly right for us only because the sink can answer position (R2 HEAD with stored prefix hash and length). Every shipper stores positions locally because its sink cannot. inferred.
- "No local spool": right for upload bytes. The source file is the spool, except when the source is destroyed or rewritten before upload. The cited S3 shippers still stage, but they ingest from streams that have no durable source. inferred.
- "Bytes streamed straight, dedup along the way": supported if keys are content addressed and writes use `If-None-Match`. R2 documents this for PutObject only. doc.
- "Strict event schemas": no evidence found either way in these sources. Not assessed.
- "Hooks only": hooks are hints, not a feed. Use them as a wake-up edge and reconcile as the level. inferred. This is the controller-runtime posture. doc (section 3).

### Recommendation
- Choose B as the base, with a slow safety reconcile (for example every few minutes, interval to be measured) and a hook-triggered immediate reconcile for the named session. This keeps zero local durable state, which the evidence says is feasible when the sink can serve as checkpoint.
- Do not drop the periodic reconcile. Even Apple's FSEvents guide requires it. A timer-driven reconcile is not the 30 s scan of D. It can be cheap because it compares size and mtime against an R2-held manifest first. inferred.
- Add C's spill only if measurement shows source files are deleted or rewritten before upload in practice (for example Codex rewrites, retention cleanup). Until then it is code with no evidence. inferred.
- Reject A as the sole path: it cannot meet hookless sessions, offline retry or truncation detection.
- D's durability is the wrong default for this sink, but its operator inspectability is real. Provide it via a `status` command that computes source minus remote, plus a small append-only local failure log (not a job table). inferred.
- Principle applied: make operations idempotent (read in full, /Users/mm/dev/harness/skills/principle-make-operations-idempotent/SKILL.md). It changed the design choice to reconcile-on-start with content-addressed `If-None-Match` writes, so any run at any crash point converges to the same remote state.

## Open questions
- What is the R2 round-trip cost and rate for a per-file HEAD reconcile at the real file count? Needs a measurement on this Mac and BC-01.
- Where is the remote manifest kept, one object per session with `If-Match` updates, or immutable segment objects only? Concurrent writers (Mac and BC-01) share a prefix?
- How often do sources vanish before upload (retention cleanup in each agent)? This decides whether C is needed.
- How should Codex SQLite and `~/.claude.json` be snapshotted without a timer?
- Does R2 honor `AbortIncompleteMultipartUpload` lifecycle rules for orphan cleanup?
- Does the operator accept hook latency (A or B inline wake-up) inside the agent process, given timeouts?

## Could not verify
- Vector's user docs do not state polling. Polling comes from source code only (`lib/file-source/src/file_server.rs`, master branch at fetch time).
- Filebeat memory queue defaults and Promtail client batch defaults: pages did not include them.
- Alloy buffer behaviour and sink-outage behaviour: not on the fetched page.
- Apple's FSEventStreamEventFlags reference page returned no body text. Flag semantics rely on the FSEvents Programming Guide, which is archived documentation.
- Fluent Bit Backpressure page and `storage.sync` semantics: not fetched.
- Kubernetes resync-on-timer claim: memory only.
- Promtail deprecation: memory only.
- SQLite backup API or `VACUUM INTO` guidance: memory only.
- R2 lifecycle support for aborting incomplete multipart uploads: not fetched.
- Firehose "buffers in memory" is from the AWS page, but its durability behaviour on outage was not fetched.
