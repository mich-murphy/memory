# DDIA cross-check of the challenge blockers (2026-11-07)

Parent-owned. Sources: DDIA 2nd edition chapters 4, 6, 8, 9, 13 and 14, supplied by the
operator on 2026-11-07. Chapters 11 and 12 are already quoted in DESIGN.md section 3.6.
Quotes are short. Each section reference is a DDIA heading. Verdicts:

- **Supported**: DDIA's reasoning leads to the same finding.
- **Refined**: supported, but DDIA changes the shape of the fix.
- **Downgraded**: DDIA shows the issue matters less than the challenge claimed.

## Summary

| # | Blocker | Verdict | Main passage |
|---|---|---|---|
| 1 | Prefix proof cannot fail | Supported, refined | ch. 9 System Model and Reality; ch. 13 Trust, but Verify |
| 2 | Property 1 uses the predicate it tests | Supported (DDIA basis exists) | ch. 9 Defining the correctness of an algorithm; Formal Methods and Randomized Testing |
| 3 | Config scrubbing missing | Supported | ch. 14 Legislation and Self-Regulation; Data as Assets and Power |
| 4 | Lost reply on `current.json` | Supported, refined | ch. 9 Figure 9-1; ch. 8 Handling errors and aborts; ch. 13 Uniquely identifying requests |
| 5 | Content-MD5 not settable | **Downgraded to Major** | ch. 13 The end-to-end argument; ch. 9 Weak forms of lying |
| 6 | Index loss is not free | Supported, refined | ch. 6 Setting Up New Followers; ch. 13 Reasoning about dataflows |
| 7 | Re-hash quarantines live sessions | Supported | ch. 9 Timeouts and Unbounded Delays; ch. 8 Handling errors and aborts |
| 8 | No durable failure state | Supported, refined | ch. 6 Monitoring staleness; ch. 9 Degraded performance (gray failure) |
| 9 | No single-instance lock | Supported, refined | ch. 9 Fencing off zombies and delayed requests |
| 10 | No reader slice | Supported (DDIA basis exists) | ch. 13 Don't just blindly trust what they promise |

The parent's earlier note that blockers 2 and 10 have no DDIA basis was wrong. Both are
grounded in ch. 9 and ch. 13.

## Per blocker

### 1. Prefix proof. Supported, refined

- DDIA's CDC (ch. 12, already in DESIGN.md) reads a log that the database promises is
  append-only. Our "log" is a file another program owns, so the append-only promise is an
  assumption, not a contract. Ch. 9 System Model and Reality says to state such assumptions
  and to handle their violation in code even when they are "assumed to be impossible".
- Ch. 9 Weak forms of lying recommends "checksums in the application-level protocol" against
  bugs and misconfiguration. A window hash ending at `captured_to`, plus file identity, is
  that check, applied to the source.
- Ch. 13 Trust, but Verify: "If you want to be sure that your data is still there, you have to
  read it and check."
- **Refinement.** A window check is probabilistic for rewrites far from both windows. DESIGN.md
  section 10 should state the system model honestly: sources are assumed append-only. A
  rewrite near the tail or the head, or one that changes identity, is detected at capture. A
  rewrite anywhere else is detected by the bounded daily re-hash. The resumable hash state is
  then only a cost optimisation.

### 2. Property 1 oracle. Supported

- Ch. 9 Defining the correctness of an algorithm: correctness is a set of properties stated
  independently of the algorithm, like the definition of "sorted". Property 1's second clause
  is written in terms of the implementation's own predicate.
- Ch. 9 Distinguishing between safety and liveness. **Refinement:** label each property.
  Safety: "no committed manifest's pieces differ from the bytes the source held at that
  offset" (the A-B3 oracle). Liveness, under partial synchrony: "a changed source is
  eventually committed once R2 is reachable". Properties 2 and 4 mix the two today.
- Ch. 9 Deterministic simulation testing names Rust's MadSim, which has deterministic
  versions of Tokio and the S3 client. That is a new input to the async decision (F6), see
  the new callouts below.

### 3. Scrubbing. Supported

- Ch. 14 Data as Assets and Power calls data "hazardous material". Ch. 14 Legislation and
  Self-Regulation says to minimise what is collected, because "data you don't have is data that
  can't be leaked". Uploading live provider keys from `models.json` is the clearest case of
  collecting what is not needed.
- Ch. 4 Disk space usage: in immutable storage, deleted data can persist until compaction.
  Together with ch. 12 Limitations of immutability (already quoted), scrubbing has to happen
  before upload. This also argues **against an indefinite bucket lock** while transcripts are
  plaintext. A lock makes a scrubber bug permanent.

### 4. Lost reply on `current.json`. Supported, refined

- Ch. 9 Figure 9-1 lists this exact case: "may have processed your request, but the response
  has been lost". After a timeout "you still don't know whether the remote node got your
  request".
- Ch. 8 Handling errors and aborts: if a transaction succeeded but the acknowledgement was
  lost, retrying repeats it "unless you have an additional application-level deduplication
  mechanism". Ch. 8 Conditional writes: "check whether the update took effect and retry if
  necessary".
- Ch. 13 Uniquely identifying requests: deduplication needs an end-to-end request ID.
  **Refinement:** the content-addressed manifest hash already *is* that ID. "GET `current.json`
  and compare its manifest hash with ours" is the textbook fix, and it needs no new metadata.
- On a foreign writer (B-M1), ch. 9 separates fencing, which is permanent, from optimistic
  concurrency failures, which can be retried. Ch. 6 Conflict avoidance routes every write for
  a record through one leader, and warns that this "breaks down if you allow the leader to be
  changed", for example a reinstall or a copied machine id. Ch. 6 Dealing with Conflicting
  Writes shows that merging is safe only when operations commute. Two hosts' piece lists for
  one source do not commute. So the DDIA position is: avoid conflicts by design, *detect* the
  violation, and do not merge. That supports quarantining the session over "rebuild and retry
  once".
- Ch. 9 also says a storage service with conditional writes makes a separate lock service
  redundant. The ETag fence is therefore the right mechanism, and no lease is needed.

### 5. Content-MD5. Downgraded to Major

- Ch. 13 The end-to-end argument: checksums in Ethernet, TCP and TLS catch network
  corruption "but they cannot detect corruption due to bugs in the software at the sending and
  receiving ends ... or corruption on the disks". A lower-level check is "useful as a
  performance enhancement" but is not the end-to-end check.
- Ch. 9 Weak forms of lying: "TLS-encrypted connections also offer protection against
  corruption."
- **Consequence.** Over HTTPS, Content-MD5 adds only a narrow guarantee: R2 confirms on
  receipt that it got the buffer we hashed. The end-to-end checks are the reader's digest
  verification on GET and the read-back or restore sample (blocker 10). D9 becomes "send it if
  the chosen client can do so cheaply". It no longer decides the S3 client choice. The probe
  checking whether R2 rejects a bad checksum stays, but as information, not a gate.

### 6. Index loss. Supported, refined

- Ch. 6 Setting Up New Followers: take a snapshot "associated with an exact position" in the
  log, copy it, then fetch the changes since that position. Ch. 13 Creating an index makes the
  same point. Rebuilding the index after loss is setting up a new follower: the R2 manifest is
  the snapshot and `epoch.size` plus its digest is the position. Hashing `[0, size)` and
  adopting the manifest is that procedure. Opening a new epoch for every source is the
  equivalent of re-cloning every follower from scratch.
- Ch. 13 Reasoning about dataflows: be clear about "which representations are derived from
  which sources". **Refinement:** sort the local state with that rule.

  | State | Class | Consequence |
  |---|---|---|
  | Agent files | System of record (leader) | Never written by us |
  | R2 objects and manifests | Derived (follower or backup) | Rebuildable from sources while they exist |
  | Index snapshot | Derived cache | May be deleted |
  | Operator config, exclusion registry, learned roots | System of record | Durable, not in the index |
  | Pending launch evidence | System of record (hook stdin is its only source) | The hint segment holding it is kept until a manifest carries it |

- Ch. 9 Mapping system models to the real world: stable storage may suffer "amnesia". Name
  index loss as an assumed fault, with the rebuild above as its handler.

### 7. Re-hash quarantines live sessions. Supported

- Ch. 9 Timeouts and Unbounded Delays: declaring a healthy node dead too early is harmful,
  and the transferred load can cascade. A detector that fires on normal appends has exactly
  that false-positive shape.
- Ch. 8 Handling errors and aborts: retry only transient errors. A source rewrite is neither
  transient nor a writer fault. It is a new epoch.
- Ch. 13 Designing for auditability: audits rerun a deterministic derivation over an
  immutable input. Our input is mutable, so the audit must pin the input: identity, size and
  mtime unchanged since the capture, with the hash bounded to `captured_to`. Otherwise the
  audit compares two different inputs.

### 8. Durable failure state. Supported, refined

- Ch. 6 Monitoring staleness: "for operability it's important to be able to quantify
  'eventual'". The age of the oldest uncaptured change is this system's replication lag.
  DESIGN.md should name it as the primary health metric and set a target for it.
- Ch. 9 Degraded performance and partial functionality: a process that stops some of its
  work while staying alive is a gray failure. A revoked token is one: the daemon is alive and
  watching, but commits nothing. **Refinement:** D's heartbeat object must carry
  `last_commit_at`, not just liveness, or it will show a gray failure as healthy.
- Ch. 8 Handling errors and aborts: treat overload differently, limit retries, back off,
  and do not retry permanent errors. That is the closed error-class enum (`Offline`,
  `Throttled`, `DestinationAuth`, `Local`, `Deterministic`).

### 9. Single instance. Supported, refined

- Ch. 9 Fencing off zombies and delayed requests: a former holder's request can be delayed
  and arrive after a new holder has taken over (Figure 9-5).
- **Refinement: two mechanisms, two scopes.** `flock` on a local file is released by the
  kernel when the process dies and has no clock, so it is not a lease and has none of ch. 9's
  lease problems. It protects the local files (index, hint log, status). It does **not** stop
  a PUT that a killed daemon left in flight from reaching R2 later. The ETag on `current.json`
  covers that case, and content-addressed objects make late duplicates harmless. DESIGN.md
  should say which mechanism protects which state.

### 10. Reader. Supported

- Ch. 13 Don't just blindly trust what they promise: "try restoring from your backups from
  time to time—otherwise, you may find out that your backup is broken when it is too late".
  Ch. 6 Backups and Replication: this system is a backup, and a backup is proven only by a
  restore. Slice 2b with `memory restore` is the DDIA position.

## New callouts from the chapters

1. **Deterministic simulation testing (ch. 9).** MadSim provides deterministic versions of
   Tokio and the S3 client. That weighs against F6's blocking-client suggestion, because a
   thread pool is harder to make deterministic. The alternative is DDIA's "application-level"
   DST: keep the core synchronous (already the plan) and put the clock, filesystem and store
   behind traits that the simulator controls. Add to the client spike: which option gives
   property 4 a deterministic, replayable run with injected lost replies?
2. **Which clock across sleep (ch. 9 Monotonic clocks).** Linux `CLOCK_MONOTONIC` excludes
   suspend and `CLOCK_BOOTTIME` includes it. The coalescer's `cap`, retry backoff and the
   15-minute reconcile need an explicit choice. C's wake detection (wall time against
   monotonic) is the correct DDIA-style check. Manifest `captured_at` from the wall clock is
   fine because nothing orders by it.
3. **Checksums on local files (ch. 4 Constructing and merging SSTables; ch. 8 Durability).**
   DDIA notes that logs carry checksums to detect torn records, and that fsync is "hard to use"
   (PostgreSQL got it wrong for 20 years). The index and `status.json` should carry a
   checksum, so that a torn write is detected and handled by the blocker 6 rebuild.
4. **Immutable manifests are copy-on-write roots (ch. 8 Indexes and snapshot isolation;
   ch. 6 Databases Backed by Object Storage).** `current.json` pointing at an immutable
   manifest is the copy-on-write B-tree pattern: each root is a consistent snapshot, so
   readers get snapshot isolation for free. Delta manifests with checkpoints (decision 5,
   option B) keep this property. Ch. 4 cites Delta Lake on object storage as the same
   log-plus-compaction shape. This confirms the commit protocol and favours option B.
5. **Batching against per-request fees (ch. 6 Databases Backed by Object Storage)** confirms
   the coalescer window as the right lever. No change.
6. **Retention and minimisation (ch. 14).** "Purge it as soon as it is no longer needed" sits
   in tension with the operator's "sessions are never deleted" combined with plaintext
   storage. This does not override the operator. It does mean the encryption decision and the
   bucket-lock decision should be made together, and that low-value kinds (`ClaudeDebug`,
   file-history) are worth a deliberate keep-or-drop choice.
7. **Third parties in transcripts (ch. 14 Consent and Freedom of Choice).** Data from one
   user "also says things about other people". The operator is the data subject for their own
   sessions, but transcripts can contain client or colleague data. This is relevant to the
   encryption decision, not to session exclusion.
8. **Byzantine limits (ch. 9 Byzantine Faults).** "If an attacker can compromise one node,
   they can probably compromise all of them." Per-host tokens limit accidents and revocation
   scope, not a determined attacker. Encryption and access control remain the protection.
   This keeps the per-host token proposal modest.

## Effect on the synthesis

- Blocker 5 moves to the Majors. The client spike is decided on correctness of the
  conditional PUT, determinism for DST, footprint and maintenance, not on Content-MD5.
- Blockers 4 and 6 get the precise fixes above: manifest hash as the request ID, and
  "bootstrap a follower".
- Blocker 9's text names both scopes, `flock` locally and the ETag in R2.
- Bucket lock: an indefinite lock is not recommended while storage is plaintext (callout 6,
  blocker 3). Use an age-limited lock or none until encryption is decided.
