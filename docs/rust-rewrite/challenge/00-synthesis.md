# Synthesis: challenge of DESIGN.md (2026-11-07)

Parent-owned. Sources are the six reviewer reports in this directory. Each finding below is
marked **verified** where the parent checked it against DESIGN.md text, vendor source code
or vendor documentation. It is marked **accepted** where the parent read the reviewer's
evidence and found no contradiction but did not reproduce it. Reviewers A and E had no web
access, so their industry comparisons are recalled and must be fetched again before they
are cited.

## Verdict

The direction holds. The design treats the agents' JSONL as change data capture, uploads
immutable content-keyed pieces, and commits a manifest through a compare-and-swap pointer.
That matches how Iceberg, Delta and restic commit, and no reviewer argued for going back to
the spool and work queue. The problems are in the mechanism. Several sections claim a
guarantee the mechanism does not provide. The design is not ready for slice 1. It needs one
revision pass first, with about a dozen concrete changes and seven operator decisions. Slice
0 (scaffold and gates) can start now, because nothing below changes it apart from the client
spike in item 12.

## Blockers: the design as written produces wrong data or leaks secrets

DDIA cross-check (`07-ddia-crosscheck.md`, 2026-11-07): blockers 1–4 and 6–10 are
supported. Blocker 5 is downgraded to a Major by the end-to-end argument (ch. 13), because
the end-to-end integrity checks are the reader's digest verification and restore, not
Content-MD5.

1. **The prefix proof is not a proof. Verified (A-B1).** Section 7.2 resumes the stored
   SHA-256 state and hashes only `[captured_to, size)`. It then compares that state with
   itself, so the check never reads `[0, captured_to)`. Any in-place rewrite or
   rename-replace that keeps or grows the size is uploaded as an `Append`. The pieces then
   reassemble to bytes that are not the file. A's probe reproduced this. Log shippers
   (Filebeat, Vector, the OTel filelog receiver) use a head fingerprint plus file identity.
   This design promises more than a log shipper does. Change: add `dev`, `ino` and
   birthtime to `SourceState` and `Observed` (A-M1). An identity change never plans
   `Append`. Hash a bounded window ending at `captured_to`, plus the head, on every append
   (about 68 KiB of reads). Say plainly that the resumable state only saves cost on the
   whole-file digest.
2. **Property 1 cannot catch item 1. Accepted (A-B3).** Its clause about epoch boundaries
   uses the same predicate it is meant to test. Replace it with an independent test oracle,
   `!file.starts_with(previously_captured_bytes)`. Add generators for same-size overwrite,
   rename-replace and truncate-then-regrow. Prove the property fails when the window check is
   disabled.
3. **Config scrubbing and the credential denylist are missing. Verified (E-B1).** Go scrubs
   11 config basenames (`internal/scrub/scrub.go:94-106`). Examples are `models.json`
   provider keys and `.claude.json` `primaryApiKey`. Go also withholds malformed config,
   denylists credential files and checks every link in a symlink chain. It allowlists launch
   argv (`internal/launch/native.go:15-47`). DESIGN.md says only "it moves bytes" and lists
   the config classes by name. Change: add a "Protected sources" section with a `Transform`
   stage. The only body type the PUT accepts should be `Authorized<Redacted<_>>`. Port the
   scrubber and argv allowlist in slice 1. Unknown kinds and config kinds stay withheld until
   then. Rules digest goes in `SourceState`.
4. **A lost reply on `current.json` makes success look like failure. Verified (B-B2).** In
   `object_store` 0.14.2, `PutMode::Update` sets `retry_on_conflict(true)` and is not marked
   idempotent. The retry branch for HTTP status codes resends after 5xx, 429 or 408
   (`src/client/retry.rs` ~405-412, `src/aws/mod.rs` ~274-300). A commit that lands and then
   gets a 5xx is resent with the old ETag, and that resend returns 412. Section 7.2 then
   "rebuilds from contents plus ours", which duplicates our own pieces. Change: on any error
   or 412, GET `current.json`. If it already names our manifest, the commit is done. If it
   names our `previous`, retry. Anything else means a foreign writer: quarantine the session
   and do not merge (B-M1). Property 4's fake store must inject both "committed, reply lost"
   and "committed, then 5xx".
5. **`Content-MD5` (D9) cannot be sent through `object_store`. Verified (B-B1).**
   `with_attributes` has no Content-MD5 attribute. The crate offers
   `x-amz-checksum-sha256`, and R2's PutObject compatibility row does not list that header.
   Change: mark D9 unverified. Slice 0 adds a probe against an acceptance bucket that sends a
   deliberately wrong checksum and must see R2 reject it. That probe decides the client
   (item 12).
6. **The claim "deleting the index loses nothing" is false. Verified from the text (A, D-B3,
   E, F).** Four reviewers found this independently. Section 6 turns a missing entry into
   `IndexMissing`, which means a new retained epoch and a whole-host re-upload, and epochs
   are kept forever. Learned roots and pending launch evidence exist only in the index, and
   the hint log is deleted 10 s after rotation. Change: on a missing entry, adopt the remote
   manifest and hash `[0, epoch.size)` locally. Open a new epoch only on a mismatch. Move
   learned roots and the exclusion registry out of the index and into durable files. Keep a
   hint segment until its launch evidence has been committed.
7. **The safety checks quarantine live sessions. Accepted (A-B2, D-B2).** The daily re-hash
   calls any mismatch "a writer bug". But appends and rewrites change the file legitimately.
   Quarantine also stops the source's later appends, needs the very R2 write that may be
   failing, and has no way back. A dead letter queue parks only the poison item and lets the
   rest of the stream keep flowing. Change: bound the re-hash to `captured_to` and re-read
   once. A stable difference is `Replay` plus a counter. Park one source only after repeated
   deterministic failures. The parked list is kept locally. Add `memory unquarantine`.
8. **No failure state survives a restart, and there is no auth-failure disposition.
   Accepted (D-B1).** A revoked token is visible only in logs, and `memory status` cannot see
   the in-memory retry state. Litestream, Fluent Bit and Vector all expose error counters
   and last-success times, and none of them needs the failing destination to report the
   failure. Change: add a small daemon-owned `status.json` holding last success,
   oldest-uncaptured age, a closed error class (`Offline`, `Throttled`, `DestinationAuth`,
   `Local`, `Deterministic`) and the counters. Add `DestinationAuth` to `Disposition` with
   the rule "pause uploads, keep watching". Optionally write a heartbeat object, at about
   0.013 USD a month.
9. **There is no single-instance lock. Accepted, measured by C (C-B1, D-M4).** Two daemons
   would race on hint rotation, the index rename and `current.json`. Take
   `flock(daemon.lock)` before doing anything else. C confirmed that a second holder gets
   EWOULDBLOCK.
10. **No slice delivers a reader, so nothing can reach acceptance. Verified against AGENTS.md
    (F1).** AGENTS.md acceptance requires writer → reader → importer → index → report. The
    D10 read-back sample is already a reader, just in the wrong place. Change: add slice 2b
    with a `memory-reader` library and `memory restore`, and have the daemon's read-back
    reuse it. Exit when every captured session restores to its source bytes, followed by an
    export to the existing `session-reader/v2` envelope.

## Majors: under-specified, or contrary to established practice

- **Global `objects/<sha>` namespace (B-M3, E-M6).** Deleting one session after the fact
  becomes a mark-and-sweep over every manifest. Cross-session dedup was never measured, so
  R8 does not justify the coupling. Measure duplicate piece hashes. If dedup is small, key
  objects under the session prefix.
- **Key by the raw content hash, not the zstd output (B-M4).** This is how git and restic do
  it. A zstd upgrade then changes no keys, and whole-object capture skips the "learn the
  key" pass. Record the encoding and `encoded_sha256` in the manifest.
- **Rewriting the whole manifest is quadratic (B-M2, measured).** At 898 pieces a session
  writes 87 MB of manifests. At 1,700 pieces it writes 313 MB. Use delta manifests chained
  through `previous`, with a checkpoint every N captures (Delta's shape), or accept the cost
  explicitly.
- **Capture-whole can livelock, and SQLite has no cheap "unchanged" test (A-M3).** Hash only
  `[0, size_at_stat)`. Bound the retries. Decide `Unchanged` from a db plus WAL fingerprint
  before running a backup. Port Go's `ErrSQLiteSourceChanged` distinction.
- **`sources_of(session)` and the path→session mapping are undefined (A-M4).** Add a pure
  `session_of` locator and an `Owner { Session, Shared, Unknown }` type. Unknown is withheld
  and counted (E-M3).
- **The exclusion registry is read only at start and is not monotonic (E-M3).** Port the Go
  append-only registry and check it again just before the `current.json` PUT.
- **Hint-log rotation drops late writers (C-M1, measured).** Switch to one file per day with
  no rename. Require one `write(2)` per line (C-m1).
- **Recursive inotify misses files in a new directory (C-M3).** On a directory Create event,
  readdir that directory immediately.
- **Coalescer (C-M5).** Flush at once on `SessionEnd`. Cap concurrent hash jobs. Stagger work
  on wake. Add a clock-jump check that runs a reconcile after sleep (C-M2).
- **Footprint targets are not measurable as written (C-M4, F5 measured).** A trivial Rust
  binary had a spawn p99 of 10.8 ms on this Mac. "Zero idle syscalls" conflicts with the
  design's own timers. Redefine the targets as in-process hook time, wakeups per minute, and
  zero file syscalls from our own I/O wrapper.
- **Memory (D-M5).** Eight uploads of up to 46 MB is about 368 MB, against a 40 MB target.
  Cap the raw piece at about 8 MiB and split larger deltas.
- **Upgrades and rollback (D-M4).** Version the index, and treat an unknown version as
  "rebuild". The writer should refuse to commit over a newer remote schema rather than
  error-loop. Stamp the writer version on each epoch, not only on each manifest.
- **fsync (D-M1).** Index and status writes need fsync of the file, rename, then fsync of the
  directory. macOS needs `F_FULLFSYNC`.
- **Delivery (F2, F3).** Add a shadow run of at least 7 days against the Go candidate as a
  slice 2 exit. Add a "Go fix → Rust counterpart" table as a slice 1 exit, since F lists 12
  uncovered Go edge cases. Define the "acceptance namespace" as a separate bucket with its
  own credentials.
- **Hegel (F4).** It shipped 255 releases in 8 months. Pin it exactly and keep properties 1
  and 4 in `proptest-state-machine` as a standing cross-check.
- **Async (F6).** Tokio is justified only by `object_store`. Item 5 already forces a client
  spike. Compare `rusty-s3` + `ureq` against `object_store` on Content-MD5 and conditionals,
  binary size and RSS.

## Correction to reviewer E (verified by the parent)

E said from memory that R2 has no prefix-scoped or action-scoped credentials. The current
docs contradict that. R2 temporary credentials can be scoped to one bucket, to path prefixes
and, through local JWT signing, to specific actions, for example `PutObject` and `GetObject`
without `DeleteObject`
(https://developers.cloudflare.com/r2/api/s3/temporary-credentials/). Long-lived tokens scope
only to buckets (https://developers.cloudflare.com/r2/api/tokens/). Local minting needs the
parent secret, so a host that mints for itself gains nothing. The real choices are a
per-host bucket, or short-lived prefix- and action-scoped credentials minted off the host.
Neither makes `current.json` immune to overwrite by its own host.

## Operator answers, 2026-11-07

- Credentials (2): one bucket shared by both hosts, so all sessions can be seen in one
  place. The credential scope inside that bucket is still open, see the proposal in the
  parent's reply.
- Namespace (3): individual sessions will never be deleted. That removes the deletion
  argument against a global `objects/` namespace (B-M3, E-M6).
- Reader (4): in scope. Add slice 2b.
- Excluded sessions (6): leave no trace. The operator questions whether session exclusion is
  needed at all. It is an existing feature (`AGENT_SESSIONS_DISABLE=1`,
  `internal/hook/hook.go:309-343`, `internal/exclusion/`) that README.md:105-107,
  AGENTS.md:39 and the contract (182-184) all require us to preserve. Dropping it is waiting
  on the operator's confirmation and on updates to those documents.
- Encryption (1) and manifest shape (5): waiting on the parent's explanation.
- Operator answers, 2026-11-07 (second round):
  - Manifest shape (5): **B**, delta manifests with a checkpoint every 64 captures.
  - Session exclusion: **dropped**. README.md, AGENTS.md and the contract (intro and
    section 6 step 2) are edited to say so. The one existing record is removed at cutover,
    not before, because the frozen Go path still honors it. Decisions 6 and 7 are moot.
    The secret protections stay.
  - Encryption (1): still open. The parent restated the options.
- Operator answers, 2026-11-07 (third round, final):
  - Encryption (1): **(a) plaintext** behind bucket access control. No bucket lock, so a
    leaked secret can still be deleted (DESIGN.md 8.6, section 19 default 1).
  - None of the existing R2 data is kept and nothing is migrated. The Rust writer starts
    in a fresh bucket with a fresh archive id; the old archive is deleted after cutover
    (DESIGN.md sections 1 and 18).
  - The design is final. DESIGN.md was rewritten on 2026-11-07 for all ten blockers, the
    majors and the DDIA refinements. Its section 20 maps each finding to the section that
    answers it. The prototypes are DESIGN.md section 16 (P1 to P10).
- Manifest cost, measured (`experiments/manifest_cost.py`, 60 s / 300 s window, one
  transcript source per session, 220 B per piece, 600 B header): 4,633 sessions, 27,569
  captures, 4.00 GB transcript. A full rewrite every capture uploads 457 MB of manifests
  (11.4% of transcript bytes), and the worst session alone uploads 89.3 MB. Delta manifests
  with a checkpoint every 64 captures upload 29 MB (0.7%). This undercounts sessions that
  have several sources.
- Never deleting sessions makes R2 bucket locks possible: an indefinite retention rule on
  `objects/` and `manifests/` (prefix rules, docs read 2026-11-07). `current.json` stays
  mutable. This is unverified against conditional PUT and is a prototype candidate.
- Session opt-outs: this Mac has 1 exclusion record (`~/.local/state/agent-sessions/exclusions`;
  the file count was read, not its contents).
- Blockers get a DDIA 2nd edition cross-check once the operator supplies the chapters.
- When the design is final, Sonnet workers prototype the alternatives for each Major and
  each new callout. The measured results decide between them.

## Parent proposals awaiting operator decision (2026-11-07)

- Credentials in one shared bucket. Long-lived R2 tokens scope only to a bucket, so either
  host's token can overwrite the other host's keys. Proposal: give each host its own Object
  Read & Write token, and put a bucket lock on `objects/` and `manifests/` so history can't
  be overwritten or deleted. Only `current.json` stays mutable, and a rollback of it shows
  up as a break in the `previous` chain. Trade-off: anything inside a locked object can't be
  deleted, including a secret leaked through a scrubber bug, so an age-limited lock may be
  better than an indefinite one. The lock's interaction with conditional PUT is unverified
  and goes to the prototype phase.
- Encryption options: (a) plaintext with Cloudflare's at-rest AES-256-GCM, (b) SSE-C, which
  `object_store` supports through `with_ssec_encryption`, or (c) client-side `age` to a
  recipient key held off the hosts. The trade-offs are in the parent's reply. Adding
  encryption later is cheap only for sources still on disk. Sessions whose local files have
  expired need a download, encrypt and re-upload migration, and an indefinite bucket lock
  would block that migration. Decide encryption before slice 2 and before any lock.
- Manifest: full rewrite against delta plus checkpoint, measured above. A full rewrite grows
  quadratically with captures, which breaks R6 (bytes uploaded proportional to bytes
  appended).
- Session exclusion: dropping it deletes the registry, the `Owner` withholding of shared
  stores (decision 7 becomes moot), withheld manifest entries and the policy recheck before
  commit. Credential denylist, config scrubbing, the argv allowlist and withholding unknown
  kinds are a different control and stay. The one existing record needs a cutover decision.

## Decisions needed from the operator (original list)

1. **Encryption.** Keep plaintext behind bucket access, or encrypt client-side to an `age`
   recipient key held off the hosts (E-M1)? This also decides object keying (HMAC versus a
   plain hash).
2. **Credentials.** Per-host bucket or off-host-minted scoped credentials (above)?
3. **Object namespace.** Global or per session? Depends on whether post-hoc per-session
   deletion is expected within a year (B-M3).
4. **Reader.** Is a minimal `memory restore` and export in this project's scope (F1)? The
   recommendation is yes, because acceptance requires it.
5. **Manifest shape.** Delta chain with checkpoints, or accept the quadratic full rewrite
   (B-M2)?
6. **Excluded sessions.** Should they leave any trace in R2, such as `withheld` entries or
   launch evidence (E-M5)?
7. **Shared stores.** Keep the "withhold all if any session is excluded" default, which
   produced 7,909 `policy_denied` issues in Go (F2 item 11)?

## Proposed order

1. Revise DESIGN.md for Blockers 1–10 and the uncontested Majors. Record each change in the
   decision log, citing the challenge report.
2. Start slice 0 with two probes added: (a) the R2 checksum and conditional probe in an
   acceptance bucket, and (b) the client spike. Both need an operator-created acceptance
   bucket and token. Until those exist they are an external prerequisite, not a gap.
3. Two measurements before slice 1: the cross-session duplicate-hash share, and how often
   agents rewrite a transcript prefix (A open question 2). Both can be taken from offsets
   and hashes only, without reading content.

## Not verified by anyone

- Linux behaviour: ext4 appends, the inotify race, coarse mtime.
- SQLite backup under a concurrent writer.
- Whether R2 actually rejects a bad checksum.
- zstd output stability across crate versions.
- notify 8.2.1 itself (8.2.0 and `main` were read).
- Hegel's state-machine shrinking.
- Every claim labelled "recalled" in reports 01 and 05.
