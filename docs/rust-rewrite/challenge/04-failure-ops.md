# Challenge D: failure handling, durability, observability, operations

Reviewer D, 2026-11-07. Scope: DESIGN.md sections 8, 10, 11, 16, 17 (D4, D8, D10). Read: DESIGN.md in full, reviewer-brief, reports/05-history-lessons.md, reports/02-runtime-architecture.md, contract section 2 onward, gap register, reviewer A's report (01, B1/B2/M1). No code was run. No scratchpad probes were needed; every finding is from reading and is labelled read or inferred. Fetched pages are in my scratchpad under D/f.

## Verdict

The design removes the Go failure machinery and replaces it with almost nothing. The "no local failure state" decision (D4) leaves the daemon with no durable health record, no auth-failure disposition, and a quarantine that needs the very R2 write that is failing. Section 8 says "deleting [the index] loses nothing", but section 6 turns every missing index entry into a new retained epoch with a whole-object upload, and launch evidence lives only in the index. The section 10 daily checks can quarantine live sessions (builds on A's B2). Upgrade, rollback, single-instance and memory-bound behaviour are unspecified. Three Blockers, five Majors.

## Findings

### B1 (Blocker). Failures have no durable record, and auth failure has no disposition

Claim (section 10): "Offline is `Retry`. Nothing is written locally for failures; `tracing` output is the operator's view, and `memory status` computes pending as disk minus index minus R2." Section 5 `Disposition { Done, Retry, Quarantine, Cancelled }`.

Problems.
1. No destination-failure variant. The contract's own table says "Destination authentication/configuration: Pause that destination and alert; do not poison every session" (contract line 102). The Go code mapped 401/403 to `ErrDestinationPaused` (02-runtime report line 307). The Rust enum drops it. A revoked or expired R2 token is a 403 on every PUT. It either becomes `Retry` forever (backoff cap 15 min, invisible except in logs) or `Quarantine` on every source, and the marker write fails with the same 403. Inferred from sections 5 and 10; the table contradicts neither because the design never lists error classes.
2. Retry state is in memory (section 10). `memory status` is a separate subcommand (section 12) and cannot read another process's memory. The unix socket was removed (section 8). So status cannot report attempts, last error or next retry. It can only report disk vs index, and the index is a snapshot up to 60 s old.
3. After weeks offline or with a dead credential, the operator's only evidence is a log whose destination, rotation and retention the design never states (no log sink in sections 8, 12). A restart loses even the retry history.
4. History says this exact area failed in Go. The gap register GAP-01 quarantined healthy sessions on a two-minute flap; 05-history L1, L7, and 02-runtime item 1 ("default is retry-forever", an EACCES source retried for ever, probe at line 373). History recommendation 1 (05 report line 220) was a closed `Failure` enum where only `Deterministic` and `Limit` can quarantine, and recommendation 9 (line 228) says health is derived by one function from monotonic facts. DESIGN.md adopted neither. The design's `Quarantine { reason: String }` is a free string, which is the shape the history warns about.
5. Captive portal and decode errors. A portal returns HTTP 200 HTML. `current.json` parse then fails with "unknown schema is an error" (section 6). Section 10 does not say whether that is Retry, Quarantine or fatal. Same for 403 `RequestTimeTooSkewed` after sleep (inferred, recalled from S3 behaviour).

Industry.
- Litestream exposes `litestream_sync_error_count`, `litestream_replica_operation_errors_total`, `litestream_disk_full`, `litestream_txid` and `litestream_replica_operation_total` on a Prometheus endpoint. read (https://litestream.io/reference/metrics/).
- Fluent Bit exposes `fluentbit_output_errors_total`, `fluentbit_output_dropped_records_total`, retry counters, and a Health Checks section keyed on error and retry-failure counts. read (https://docs.fluentbit.io/manual/administration/monitoring).
- Vector exposes `component_errors_total` and `uptime_seconds` from `internal_metrics`. read (https://vector.dev/docs/reference/configuration/sources/internal_metrics/).
- restic gives stable exit codes (documented list incl. 10 repository does not exist, 11 lock failed, 12 wrong password; recalled, page fetched at https://restic.readthedocs.io/en/stable/075_scripting.html shows an "Exit codes" table) so cron wrappers can alert. read (URL) for the table, recalled for the specific numbers.
- Common pattern: error counters plus last-success time, available without parsing logs. None of them depends on the failing destination to report that it is failing.

Minimal durable status (fits D4, not a database). One small JSON file `status.json`, atomically rewritten (temp, fsync, rename) at most every 10 s when changed, owned by the daemon, read by `memory status`:
- `daemon_started_at`, `version`, `pid`, `heartbeat_at`
- `last_success_at` (last `current.json` commit), and per host the count of sessions committed in the last hour
- `oldest_uncaptured_change_at`: min mtime over sources with size greater than `captured_to`, computed in the reconcile that already stats everything
- `error: { class, first_at, last_at, consecutive, next_retry_at }` where class is a closed enum: `Offline`, `Throttled`, `DestinationAuth`, `Local(Io|Disk|Perm)`, `Deterministic`
- counters (rehash mismatches, readback mismatches, loss-window events, replays by reason), quarantined sources list

Plus a dead-man's switch for a host that is off or crashed: one PUT `hosts/<machine>/heartbeat.json` per 15 min when online (about 2,900 Class A per month, roughly 0.013 USD at 4.50 per million, from section 3.5). Without it, "daemon dead" and "nothing to capture" are indistinguishable from R2, which the operator reads. Add `DestinationAuth` to `Disposition` as "pause all uploads, keep watching, stop spending backoff on every source". Optional: a Prometheus text file or endpoint later; do not add it before an operator asks (R8).

Risk removed: GAP-01/L1/L7-class silent stalls, and the 05-history "observability of the consumer" gap (line 184).

### B2 (Blocker). Quarantine is unrecoverable by design and the daily checks will trigger it falsely

Claims (section 10): quarantine "writes the marker object and stops retrying that source until the marker is removed"; the daily re-hash "A mismatch is a writer bug, not a source change. It quarantines the source."

Problems.
1. Builds on A's B2 (01-capture-correctness.md B2): the re-hash compares a fresh whole-file hash with the digest at the last capture, so a normal append in between, or a prefix rewrite, mismatches. Section 10 says the source cannot have changed, which is false for any active session, and "active source" is the very population the re-hash targets ("once a day per active source"). Result: false-positive quarantine of exactly the sessions in use. I agree with A's fix (bound to `[0, captured_to)`, re-read, classify rewrite as Replay). I add that for a stable file the remedy for bug and rewrite is identical: the R2 content disagrees with the disk, so start a new epoch from the current bytes. Quarantine is needed only if the replayed epoch mismatches again.
2. The daily read-back sample (measure 3) has the same race. It compares R2 bytes to the local file "up to the captured offset". If the prefix was rewritten after capture, that is a source change; the design assigns no action for a mismatch at all (no quarantine, no replay, no counter semantics). With up to 10 sessions of about 4,600 per day, a per-session bug is sampled at about 0.2 percent per day, so it only catches systemic bugs (inferred).
3. The marker is the only record. Writing it needs R2. When the failure is offline or auth, the marker cannot be written, so the quarantine exists only in memory and a restart retries the poison source. When the marker is written, nothing in sections 7 to 12 says how the daemon learns it was removed: a GET per source per attempt, a LIST per reconcile, or never. There is no `unquarantine` or `replay` subcommand (section 12 lists `daemon`, `hook`, `status`, `reconcile`). The contract requires quarantine to be "durable, inspectable, and replayable" (contract line 111); this is not replayable.
4. "Visible to both hosts" (section 6) is unused: hosts write distinct session keys (section 7.2).
5. A quarantined source stops capture of its later appends. Transcripts are append-only, so every byte appended after the quarantine is lost from R2 until a human acts. The rule was copied from DDIA's DLQ wording but dropped the key part: in a DLQ only the poison message is parked and the stream keeps flowing. Kafka Connect's `errors.deadletterqueue.topic.name` with `errors.tolerance=all` and SQS redrive policy (`maxReceiveCount`) both move the failed item aside and continue (recalled; Kafka docs and AWS docs, not fetched). The Go history shows the failure mode: a quarantined head froze a session (02-runtime item 6, 05 L1).

Recommended change.
- Make integrity mismatch and prefix mismatch routine `Replay` with a counter. Quarantine (park one source, not the session) only after N consecutive deterministic failures of the same source (for example 3 across at least 3 hours), and keep capturing other sources of the session.
- Keep the parked list in `status.json` (local, durable even offline). Write the R2 marker best-effort as a courtesy copy. The local file is the authority for "stops retrying"; the marker is not read back unless a `memory unquarantine <source>` command is run.
- A parked source is still re-examined once at the next daily reconcile so that an appended healthy file resumes without human action. Risk removed: silent loss of the most important live data.

### B3 (Blocker). "Deleting the index loses nothing" is false, and the 412 rebuild path is undefined

Claims. Section 8.3: "Deleting it loses nothing: startup rebuilds from a stat walk (0.4 s) and one GET per session." Section 6: "a missing index entry for a file R2 already knows starts epoch n+1 with a whole-object base piece." Section 7.2: on a 412 "re-read it, rebuild the manifest from its contents plus ours, and retry once." Section 7.5: launch evidence "held in the index".

Problems.
1. Contradiction. Rebuilding with no index entry means `ReplayReason::IndexMissing` (section 5), so every session in R2 gets a new epoch and a whole-object upload. Class `append` retains every epoch forever and nothing deletes unreferenced objects (sections 6, 16.2). Cost: a full re-upload of everything on this host (section 3: 1.9 GB Claude plus 2.07 GB Pi, plus others), a permanent duplicate epoch per source in every manifest, and a manifest bloat on the biggest sessions. Same for a hasher-state mismatch after a `sha2` bump, because "re-hashes on mismatch" (section 7.2) is not stated to be rehash-and-compare rather than epoch bump. A disk-full truncation of the snapshot or a corrupt file would do it too. Inferred from sections 5, 6, 8.
2. A cheap correct rebuild exists: the manifest already stores each epoch's whole-file `sha256` and `size`. On a missing entry, GET `current.json` and the manifest, hash `[0, epoch.size)` from disk (70 ms per 19 MB, section 3.4, so a few seconds for the whole host), and if it equals the stored digest adopt the remote state with no upload and rebuild the resumable hasher on the way. Only a mismatch opens epoch n+1. That makes "deleting it loses nothing" true. Note this uses a full read that A's B1 wants anyway.
3. Stale index after a crash (cases 3.3 and 7.2). The index snapshot is up to 60 s old. If R2 advanced `current.json` after the snapshot, the next capture starts at the old `captured_to` and uses the old ETag. The PUT gets 412 (this is the "stale ETag" path). The rebuild rule "contents plus ours" is not defined for the case where remote already covers `[N1, N2)`: appending our piece `[N1, N3)` creates overlapping offsets in one epoch, which a reader that concatenates pieces would double-count. The rule must be: on 412 adopt remote state first, set `captured_to` to the remote's last piece end, verify per item 2, then capture only the remainder. Not specified (read: section 7.2 has one sentence).
4. Launch evidence is hook-only (R1) and its only copy beyond the hint log is the index (section 7.5). The hint log is renamed daily and the old file deleted after 10 s (section 8.2). Evidence pending across a rotation, or across a rebuild, is gone permanently and cannot be recovered from a stat walk. Same for the hint offset: losing it replays the current hint file (harmless) but any hint in a deleted segment is lost. Evidence that has not been attached to a manifest should be appended to a durable place that is not cleaned until a manifest carries it, for example keep the hint segment until the offset passed it AND the snapshot containing the offset is durable AND the launch evidence has been committed.

### M1 (Major). fsync and directory fsync are not specified for the index (hypothesis 3)

Section 8.3 says "temporary name and renamed". Section 10 lists "fsync on the index snapshot is durable" as an assumption, but no section says the file is fsynced or the directory is. Without file fsync, a crash can leave a zero-length or partial file after the rename on some filesystems (ext4 `auto_da_alloc` papers over replace-via-rename but is not guaranteed; recalled). On macOS plain `fsync` does not flush the drive cache; `F_FULLFSYNC` is needed for power-loss durability (recalled from Apple's `fsync(2)` man page, not fetched). With an index that is only a cache and idempotent re-upload, the cost of a bad snapshot is B3's re-upload, not data loss. Recommended: fsync file, rename, fsync parent directory (Linux), `F_FULLFSYNC` on macOS only for `status.json` and the index, and a checksum or `version` plus length in the header so a torn snapshot is detected and handled by B3's rebuild rather than parsed as a short document. The hint log needs no fsync (a lost line is caught by FS events and reconcile). Idempotence of the recapture itself holds: pieces are content-keyed (412 is success, section 6) so re-uploading after a stale index is safe; only the manifest step needs B3 item 3.

### M2 (Major). "Offline" and the loss-window counter are undefined and not durable (hypothesis 4)

Section 10 counts the loss window "as a `Replay{PrefixMismatch}` while offline". Problems:
- Offline is not defined. Candidates: connect/DNS error, 5xx, 429, 403 (auth or clock skew), 200 with a body that is not our JSON (captive portal), TLS error. Each needs a class (B1). A counter keyed on "while offline" will miss auth outages and count portal HTML as a schema error.
- The counter is in memory (nothing is written locally). A restart, which laptop sleep and updates cause often, resets it. The only decision rule is "nonzero after a month on both hosts" (D8, section 10), which a non-durable counter cannot support.
- It conflates two things. A prefix mismatch found online is a normal rewrite. A mismatch found after an offline period may have lost bytes only if the old bytes were never uploaded, which needs `captured_to` and the file's earlier size vs the last committed manifest. The counter should count "rewrite detected with `captured_to` greater than the last remote-committed size", not "while offline".
- Given A's B1 (prefix proof can pass on a rewritten file) the counter will also undercount.
Recommended: put these counters in `status.json` (B1), keyed by day, defined by the commit boundary (bytes the index held that R2 did not confirm). Risk removed: the spill-buffer decision rests on a number nobody can currently trust.

### M3 (Major). `memory status` cannot make a definite claim and its cost and offline behaviour are unspecified (hypothesis 2)

"Pending as disk minus index minus R2" needs per-session R2 reads. With about 4,600 sessions (section 3.2) that is at least 4,600 GETs of `current.json` plus the manifests, about 9,000 Class B at 0.36 USD per million, roughly 0.003 USD per run (section 3.5 prices; inferred). Money is trivial. Time and behaviour are not: serial 9,000 GETs takes minutes, offline it fails entirely, and the design gives no degraded mode. It can never state "all captured" because:
1. The source can change between the stat and the answer (live sessions append every few seconds).
2. The index is a cache that may be wrong (B3, A's B1), so "disk minus index" is no proof.
3. R2 has no listing of which `current.json` is up to date without GET.
Define status as three explicit tiers: (a) local view, always available: sources with size greater than `captured_to`, age of the oldest; (b) last confirmed commit time from `status.json` (B1); (c) optional `--verify`: GET only the `current.json` ETag per session and compare with the index (one request per session, no manifests), plus the manifest for sessions that differ. Output text must say "up to date as of <t>" and never "all captured". Litestream's equivalent is the `litestream_txid` and last-sync gauges, not a global claim (read, litestream metrics URL).

### M4 (Major). Upgrades, rollback and the writer stamp (hypothesis 7)

1. The index format has no stated version or schema. Go's equivalent was a recorded defect: "No SQLite `user_version`" and persisted JSON blobs with no version (gap register line 295; 02-runtime line 257). Section 8 says `deny_unknown_fields` for config, section 6 for manifests, but nothing for the index. A new binary reading an old index, or a rolled-back binary reading a newer one, has no rule. Required: `schema` plus `writer_version` in the header, unknown newer is "ignore and rebuild from R2 per B3 item 2", never an error that stops capture.
2. Rollback. Section 6: "unknown version is an error", and `deny_unknown_fields`. Roll back from a binary that wrote `manifest/2` and the old binary does a GET of `current.json` on every first capture and every 412 (B3 item 3). It sees an unknown manifest schema and errors. If that is `Retry`, capture stops silently (B1). If it is treated as missing, it starts epoch n+1 and may overwrite the newer manifest with a chain that omits the newer fields. Even an additive field in `manifest/1` breaks the old reader under `deny_unknown_fields`, so every additive change forces a version bump and a rollback incompatibility. Recommended: the writer needs only the ETag, `previous` chain and epoch list. Keep the last committed manifest bytes in the index or a local file, and on an unknown remote schema write nothing and raise `DestinationConfig` status ("remote written by a newer writer; upgrade or run `memory adopt`"). Strictness belongs in the reader project, not the writer's recovery path.
3. D10 stamps one `writer` per manifest, but the manifest is "rewritten in full on each capture" (section 6). Every old piece and epoch is then restated under the newest writer version, so "manifests written during its period are findable by one list" holds only for the manifest objects, not for which pieces a buggy version produced. Add `writer` (version and commit) per epoch, or per piece. Cheap: one short string per epoch.
4. Cutover coexistence with the Go candidate. Contract section 6 step 3 says a fresh archive identity and an isolated namespace; DESIGN.md keys everything under `<archive>`, so the same bucket is workable provided the archive id differs and a `memory` token cannot write the old namespace. Not stated: whether both processes may run on the same host and watch the same agent roots and hooks. The hook command changes from Go to `memory hook`, and both daemons would double the I/O load and, for Go, the spool. This is an operator step in the contract, not a design gap; I flag only that DESIGN.md does not say a hard cutover per host is the rule.
5. No single-instance guard. Searches for `flock`, `lock`, `pidfile`, `launchd`, `systemd` in DESIGN.md found nothing (grep). Two daemons (launchd restart overlap, a manual `memory daemon`, a `memory reconcile` run beside the daemon) race on the index temp+rename, the hint-log rotation and the consumed offset. The ETag fence protects R2 from two writers of one session but not the local files, and a rename race can leave the stale index that B3 punishes. Add `flock` on a lock file at daemon start. One line of design, and the history says the Go design needed a "do not delete lock files as a substitute for fencing" rule (contract section 6 step 5).

### M5 (Major). Memory, piece size and disk exhaustion (hypothesis 8)

1. Memory. Section 8 target: 40 MB resident at 30k sources. Section 7.2 holds each PUT body in memory ("zstd level 3 into memory", "no local staging") and the largest whole object is 46 MB encoded. Eight concurrent uploads (section 11) can hold up to 8 x 46 MB = 368 MB, nine times the target (read: sections 7.2, 8, 11; arithmetic mine). The conditional PUT cannot fall back to multipart, because "Multipart has 5 MiB minimum parts and no conditionals" (section 3.5, vendor claim not rechecked by me). So whole-object captures must buffer. These are rare in steady state, but B3's rebuild or a weeks-offline reconnect makes them common (all sessions at once, bounded by 8 in flight).
2. No maximum piece size. `Append{from,to}` reads the whole delta into a buffer. After weeks offline a session with a long transcript can produce a delta of tens of MB in one capture; no cap is stated (section 7.2). Recommend a maximum raw piece of, say, 8 MiB: a delta larger than that is split into several pieces in one manifest. Offsets already allow it. This bounds memory to about 8 x 2 x piece and bounds retry cost.
3. Backlog after weeks offline. The coalescer is one capture in flight per session and 8 uploads host-wide, so reconnect is bounded in concurrency but not in memory (item 1) or in rate; nothing limits R2 429s except `Retry`. Fine in principle; needs the Throttled class from B1. Reconcile cost at 30k sources is bounded (1 s, section 8).
4. Disk full. The hook appends with `O_APPEND` and "exits 0" (section 9); an ENOSPC on append silently drops the hint, which FS events cover. The index snapshot write fails; with no `status.json`, nothing says so. Litestream exposes `litestream_disk_full` for this reason (read, metrics URL). Needs a `Local(Disk)` class in `status.json` and rule: never delete the previous snapshot until the new one is durably renamed (temp+rename already does this).
5. File descriptors. The design opens files transiently and uses one inotify instance. macOS launchd gives daemons a soft limit of 256 descriptors by default (recalled), which is enough for 8 uploads plus reads, so low risk. I flag only that `max_user_instances` on Linux and the launchd limit are untested (inferred, minor).

### Minor

- m1. Section 10 says `tracing` is the operator's view but sets no sink. Launchd stdout and journald are different retention models; an unrotated launchd log grows without bound. Specify: log to stderr, rely on the supervisor, and write a single `last_error` line into `status.json`.
- m2. Backoff reset on restart is called harmless (section 10). After a crash loop, every restart immediately retries every source at 1 s, which becomes a thundering herd against R2 when the daemon is killed and restarted by the supervisor repeatedly. Persist `next_retry_at` in `status.json` or add a startup jitter.
- m3. The daily re-hash and read-back run inside the same daemon, same 2-thread runtime and same index. A slow read-back GET must not block captures. Spawn on a low-priority lane; state it.
- m4. Contract requires recheck of exclusions before delivering pending work (contract line 112). The design has no pending work after a restart, but in-memory retries mean a capture retried after an exclusion change must pass `policy::check` again. Probably true by construction (`Authorized<T>` token); worth one sentence.

## Claims checked

| Claim | Status |
|---|---|
| Section 8.3 "deleting it loses nothing" | Contradicted by sections 5, 6, 7.5 (B3), read |
| Section 10 "assumed and not verified: fsync on the index snapshot is durable" | Not backed by any fsync step in section 8 (M1), read |
| Section 10 daily re-hash "mismatch is a writer bug, not a source change" | Contradicted for active sources, builds on A's B2, read |
| Section 3.4 re-hash cost 70 ms per 19 MB | Not rerun; plausible, used only for cost arithmetic |
| Section 3.5 R2 multipart has no conditionals | Could not check, used as given |
| Section 3.2 4,621 sessions | Not rerun, used for the status cost estimate |
| DDIA quotes | Not available to me, not checked |
| Litestream, Fluent Bit, Vector, restic expose health through metrics and exit codes | Confirmed by fetching their docs (URLs above) for metric names and exit-code tables |
| Go history: retry-forever default, quarantine on flap, no `user_version` | Read in 05-history, 02-runtime, gap register |

## Open questions for the operator

1. Is silent capture stoppage on a revoked R2 token acceptable if the operator would learn it only from logs? If not, `DestinationAuth` and `status.json` and the heartbeat are required in slice 2, not later.
2. Is the heartbeat object (about 0.013 USD per month) acceptable, and where will the operator look at it?
3. After a source rewrite does the operator want the old epoch kept (design as written) or is a rewritten-but-identical-prefix file a case for in-place epoch continuation? This decides how much B3's rebuild must prove.
4. Is a hard per-host cutover (stop Go, then start Rust) the rule, with no period of both running?
5. May old binaries stop capturing when they see a newer manifest (my recommendation) or must rollback keep capturing?

## Things I could not verify

- No websearch tool: the DLQ practice of Kafka Connect and SQS, macOS `F_FULLFSYNC`, ext4 `auto_da_alloc` behaviour, launchd descriptor limits and S3 `RequestTimeTooSkewed` are recalled, not fetched.
- I did not run the design's code (none exists) or any failure injection; B3's overlapping-pieces consequence and B1's auth behaviour are inferred from the text.
- I did not measure R2 request latency, so the serial `--verify` time is a guess.
- `object_store`'s buffering behaviour for a conditional `put` of a 46 MB payload was not checked in source; M5.1 assumes `PutPayload` is in memory.
