# DESIGN.md amendments, 2026-11-09

**Status: authoritative where it conflicts with DESIGN.md or AGENTS.md.** Both of those files are
hash-pinned by `implementation/verify-evidence.py`, which forbids admitting them as revisions,
so they are not edited. Read DESIGN.md, then this file. Each amendment names the operator decision
or measured evidence it rests on. The handoff brief records the operations themselves.

## A0. Go writers are stopped (AGENTS.md "Go candidate (frozen, running until cutover)"; contract status note)

Operator instruction, 2026-11-09. The Go capture daemons on the Mac and BC-01 and the Mac
publication-refresh job are stopped and disabled; the dashboard still runs against local data.
The Go hooks remain installed and write only into the local Go spool. They are removed at
cutover (DESIGN 18 step 2). Statements that the Go candidate keeps running until cutover
describe the earlier plan.

## A1. P5 is waived (DESIGN 16 P5; 17 slice-1 prerequisite)

Operator decision. Slice 1 is not held on P5. Correctness never depended on it: a prefix rewrite is
detected by the window check (7.2) and becomes `Replay`. The rewrite frequency is measured later
from the running daemon's own counters (`Replay{WindowMismatch}`, `Replay{AdoptMismatch}`). No
collector reads private sources for it.

## A2. One bucket: the existing `agent-sessions` (DESIGN 0.5, 6.1, 16 "Needs", 17, 18; AGENTS.md "fresh shared bucket")

Operator decision, confirmed twice ("stop the current process, cleanup R2 and use that").

- There is no separate acceptance bucket and no fresh production bucket. Prototypes, acceptance
  (P2, P2b, slices 2, 2b, 3) and production all use the existing `agent-sessions` bucket with the
  credentials in the repository's ignored `.env` (mode 0600). Credentials are loaded into a process
  environment only, never printed, logged or written.
- Acceptance runs write only under `rust-acceptance/<run-id>/` and delete that prefix at the end,
  verifying it empty. They never list, read or delete other keys.
- The old archive is already gone. On 2026-11-09 all Go writers were stopped (Mac capture daemon
  and refresh job, BC-01 capture unit), the bucket was inventoried, and the `capture-v1/` and `v3/`
  prefixes (435,618 objects) were deleted. Two unrelated `delivery-artifacts/` deployment bundles
  remain. DESIGN 18 step 6 (inventory and delete the old archive, then delete the acceptance bucket)
  is therefore replaced by: before rollout, delete every `rust-acceptance/` prefix and verify that
  the bucket holds only `delivery-artifacts/` and data written by the accepted Rust writer.
  DESIGN 18 step 1 (create a production bucket) is dropped.
- One token per host (6.1) is not decided by this amendment; the existing credentials are used.

## A3. The hook records no launch argv (DESIGN 6.3 `launch.argv`, 8.4, 10.1, 16 P4)

Operator decision: drop it. The Rust hook records env facts only. There is no process-tree,
`kern.procargs2` or `/proc` read, and no `--argv` flag. `LaunchRecord.argv` stays in the schema and
is empty from the hook. The argv allowlist code stays as a protection for any argv that reaches it.
This is an accepted regression from the Go hook.

## A4. Symlinked files are skipped (DESIGN 7.2 "every symlink link", 8.2)

Operator decision. `realfs::walk` counts symlinks and never yields them, so no file is captured
through a link. The resolver's link-chain and identity checks stay as defence for paths reached
directly (hint paths) and for ancestor and root identities. A link to a credential is denied
either way.

## A5. State root (DESIGN 9)

Operator decision: `$HOME/.local/state/memory` on both hosts. `MEMORY_STATE_DIR` overrides it, and
tests must always set it to a temp directory with `env_clear()`.

## A6. `BaseUnchanged` exhaustion gets its own class (DESIGN 7.4 step 5, 11.2)

Evidence: [P11](prototypes/P11-commit-retry.md) (real R2 probe, 889 requests; seeded simulation,
100 owners, 12 seeds, 8 approaches plus the parent's hybrid run). DDIA basis: ch. 8 "Handling
errors and aborts" (retry only transient errors, limit retries, exponential backoff, treat overload
differently) and ch. 2 "When an overloaded system won't recover" (randomised backoff against retry
storms).

- In-commit: keep up to 4 rebase retries of step 3, with full-jitter pauses.
- After exhaustion the capture ends `Failed(Transient)`, a new `ErrorClass`, not `Throttled`.
  `Transient` retries with full jitter, `uniform(1 s, min(120 s, 2 × 2^n))`, and is counted
  separately. It carries no `Retry-After`.
- `Offline` gets the full jitter DESIGN 11.2 already requires; `disposition::backoff` has none
  today. `Throttled` honours `Retry-After`, falling back to the `Offline` schedule.
- `verify` accepts every manifest hash this daemon built for the owner since its last landed
  commit (`known_ours`, in memory). A late `current.json` write from an earlier failed capture
  then becomes `AlreadyOurs` (adopt its ETag and position, queue a follow-up) instead of a false
  `Foreign` park. The owner key contains the machine id, so outside a cloned machine id this
  cannot hide a real foreign writer. A Rust test must drive that interleaving.
- Rejected, measured: fail fast with no in-commit retry (p99 325 s against 23 s at 20 % errors);
  the hybrid (fail fast once a streak exists: p99 40.5 s against 23.2 s at 20 %, no better under
  bursts). Unproven, not adopted: a shared retry token bucket (never bound in the simulation).

How data arrival and late commits are coordinated, as implemented and measured:

- Bytes arriving during a capture: a replay or append reads to the stat-time size; `captured_to`
  advances only when the commit lands, so a failed capture's bytes are re-read by the retry, and
  bytes after the stat ride in the next capture. The coalescer keeps one capture in flight per
  owner and queues exactly one follow-up. Unlanded bytes at the end of every simulated run: 0.
- Lost reply: the verification GET compares the content-addressed manifest hash (the request id)
  and returns `AlreadyOurs`.
- Delayed write from an old attempt: fenced by `If-Match` on the ETag. Real R2: 28 of 28 stale
  ETags got 412; 60 of 60 two-writer races had exactly one winner, and the stored body was always
  the winner's.
- Another host: one writer per owner, enforced by the owner key's machine id and the daemon lock;
  a real foreign write parks the owner and never merges.

## A7. Durable state writes (DESIGN 9.2, 15)

Evidence: [P12](prototypes/P12-durable-state-writes.md) (APFS microbenchmark; end-to-end daemon
properties at 2,000 cases on patched copies). DDIA basis: ch. 8 "Durability" (fsync is what makes a
write durable, and it is easy to get wrong).

- Production is unchanged: temp file, `F_FULLFSYNC` (`sync_all`), rename, directory sync; status
  and index written on authoritative change or at most every 60 s, and at shutdown. That is about
  two writes a minute, roughly 12 ms of flush time. `F_BARRIERFSYNC` only orders writes (SDK:
  "fsync + issue barrier to drive") and is not adopted.
- Tests: `StateDir` gets a `SyncPolicy { Full, None }`, `Full` by default. `None` keeps temp, write,
  rename and directory open and skips only the flush calls. Only the daemon simulation properties
  select `None`; `state.rs`'s own tests and the daemon example tests keep `Full`; the binary always
  uses `Full`. Measured: the two properties go from 421 s to 14 s, all 30 tests passing. A flush
  cannot change what a live process reads back, so no current assertion loses evidence.
- Add a deterministic crash-point property: drop the daemon between `stage` and `publish`, reload,
  and assert the previous or the new body is read, never a mix. Neither the old nor the new setup
  modelled this.
- Rejected, measured: skipping unchanged bodies (no faster, because status carries counters and
  `updated_at`, and it changed what `shutdown_table` checks).
- Not measured: Linux (ext4) costs; BC-01 SSH was unavailable from the session.

## A8. Prototype ids

DESIGN 16 already assigns P9 (SQLite unchanged test) and P10 (Linux racy mtime); both stay slice-3
work. The 2026-11-09 prototypes are P11 (commit retry) and P12 (durable state writes).

## A9. Line-coverage floor is 80 %, not a ratchet (DESIGN 15 gate 8; quality-gates.md)

Operator decision, 2026-11-09. `rust/coverage-floor.txt` is 80. DESIGN 15 gate 8 ("the current
percentage as a floor") is replaced: the floor is a fixed backstop, not the latest measurement.

- Why: the 98 % floor was the scaffold's first measurement (49/50 lines,
  `implementation/00-scaffold.md`), carried forward to about 5,400 lines. It left 9 uncovered
  lines of headroom and rewarded removing error branches or adding coverage-driven tests.
  `challenge/06-strategy-engineering.md` F8.1 and `reports/04-tests.md` finding 7 recommended
  against a "current percentage" ratchet. The Go gate is 75 % total.
- What carries the evidence instead: independent-oracle properties with their coverage guards
  (DESIGN 15, unchanged), production mutation checks, and the consumer gate. Tautological tests
  (oracle re-derived from the code under test, fake asserted against itself, derive-only) are
  removed rather than kept for line count.
- The floor is still a hard gate and is not lowered to make a run pass. Raising it is allowed only
  as a recorded decision, never automatically from a measurement.

## A10. Corrections of fact and evidence labels (2026-11-10)

Source: the design and test review ([24](implementation/24-design-and-test-review.md); reports 01,
02, 03, W2 to W5 in the review scratchpad, not preserved in the repo). The operator agreed to
correct factual errors. No design decision or value changes. DESIGN.md is not edited. Line numbers
below are DESIGN.md as of commit `aa04050`. Where an entry conflicts with DESIGN.md, the entry wins.

### A10.1 Table 3.1 is 30 s data (DESIGN 2 R6, lines 96-97; 3.1, lines 111-117)

Wrong text: line 111 "### 3.1 Upload policy, quiet 60 s, cap 300 s unless stated [measured,
`policy_sim.py`, 30 s window]"; R6 "10.7x on Claude history and 155x on Pi history. [measured,
`policy_sim.py`]". Evidence: `experiments/policy_sim.py:24` is `def captures(ev,Q=30,M=300)` and
the only call is `captures(ev)`; the table's max pieces "1,700" equals the 30 s row of 3.2, while
the 60 s row has 898. Corrected reading: read the 3.1 heading as "quiet 30 s, cap 300 s". Every
cell of the table, and R6's 10.7x and 155x, are 30 s data, tagged `[measured, 30 s]` and not
measured at 60 s. No 60 s value is invented. The only 60 s figures are the all-agent counts in 3.2
(27,481 captures, max 898 pieces) and 3.7 (27,683 captures, 11.4% manifest share). The script reads
private local transcripts and no raw output was saved, so a reader cannot regenerate the table.

### A10.2 The object key hashes the transformed bytes (DESIGN 4 lines 240-241; 6.2 lines 371-374; 7.3 lines 564-568)

Wrong text: line 371 "`objects/<sha256 of raw piece bytes>.zst`", line 373 "Keying by the raw
hash...", step 2 "Feed the raw bytes to the piece SHA-256 and to the whole-file `Sha256State`",
step 6 "PUT `objects/<raw sha256>.zst`". Evidence: `capture.rs` runs `transform` then `authorize`
(about lines 477-480) before slicing; `Piece::from_authorized` (memory-core `manifest/schema.rs`
about lines 325-350) sets `sha256 = manifest_hash(chunk)` over the authorized chunk and
`object = object_key(&sha256)`; the whole-stream digest is over the same body (`capture.rs`
about 513-523, read only there). The manifest records `sha256` (pre-encoding) plus `encoded_len`
and `encoded_sha256`. In `commit.rs` (`upload_pieces`, `adopt_winner`), a 412 reads the stored
object and the manifest records the winner's stored encoding.

Corrected reading: the key is `objects/<sha256 of the transformed piece bytes>.zst`, where
transformed means after the section 8.3 scrub and before compression. Order in 7.3: transform,
authorize, digest the transformed bytes (piece and whole-file), compress and digest the encoded
bytes, PUT. After a 412 the manifest carries the stored object's encoding, not the writer's. The
zstd-upgrade benefit in line 373 still holds (the key does not depend on compression). Why this is
right: after a scrub-rule fix the same input scrubs differently, so the key and manifest change;
hashing the raw bytes would give the old key, a 412, and adoption of the old, less scrubbed
object. Residual: the previously uploaded object stays in the bucket, unreferenced, until a
deletion tool exists (DESIGN 19 decision 4). The 6.3 example's `<raw>` placeholders mean this
digest.

### A10.3 Open: encoding label versus identity bytes (DESIGN 6.3 line 429; 7.3 line 566)

Manifests carry `"encoding": "zstd-3"` and keys end `.zst` (`Piece::from_authorized` sets
`Encoding::Zstd3`), while `capture.rs` (module doc, about line 21) says "piece encoding is the
identity (no zstd yet; `encoded_*` equal the raw fields)". No decision is made here. It must be
resolved before the first production write, by one of: (a) label identity now (a new `Encoding`
value and key suffix), or (b) land zstd before any write. Otherwise a reader that decodes per the
label fails, and a later zstd writer would 412 against identity objects and adopt them under the
same label.

### A10.4 The 8 MiB piece cap and the 80 MiB bound are unmeasured (DESIGN 7.3 lines 559, 595-597; D18 line 1281)

Wrong text: "Pieces are at most 8 MiB raw. [default, prototype P4]" and "at most 80 MiB for
incompressible data. [target, prototype P4]". Evidence: `prototypes/P4-synthetic-footprint.md`,
row 8 MiB x 8 random buffers: 83.89 MB payload and 135.12 MB process peak, in a Python process,
against the 128 MB peak target (line 1146). It says the benchmark "does not justify a change to
the 8 MiB / 8 uploads defaults" and calls the target "not measured acceptance". Corrected
reading: the cap, the 2 hash jobs and the 8 uploads are `[default, unmeasured]`. "80 MiB" is payload
arithmetic (2 x 8 MiB + 8 x 8 MiB = 83.89 MB), not a measured peak. The measurement that settles
it: peak RSS of the Rust daemon with 8 concurrent 8 MiB incompressible PUTs and a 30k-entry index,
against 128 MB.

### A10.5 Constants without measurements (DESIGN 7.6 line 650; 9.1 line 786; 10.3 line 871; 11.2 line 926; 15 line 1081)

Values unchanged. Corrected reading: each of these is `[default, unmeasured]`: the 3 h park span
(line 926; a reviewer's "for example", `challenge/04`, no measurement), 7-day hint retention (lines
650, 786), the 2 and 8 concurrency limits (A10.4), and the 80% coverage floor (line 1081 as
replaced by A9; actual 97.03%, so it cannot catch a fall from 97 to 81, and no measurement supports
80; it is a backstop). The 64 MiB hint-read bound (`daemon.rs` `MAX_HINT_FILE`) and 64 MiB state
body bound (`state.rs` `MAX_FILE_BYTES`) appear only in code and have no evidence.

The "every 15 min" safety reconcile (line 871) is `[default]`, backed by walk cost only. New dated
evidence (2026-11-10, W4 Q3): notify 8.2.0 with `RecursiveMode::Recursive` on FSEvents, 5 runs
per configuration, each file created, appended and renamed. 1k files: no loss. 10k files in about
0.8 s: loss in 2 of 5 runs (15 and 309 files, up to 3.09%). 50k files in about 4.5 s: loss in 3 of 5
runs (up to 0.23%). Every run with missing files also had at least one rescan flag and vice versa;
no silent loss in 50 runs. That supports `Rescan -> ReconcileAll` as the loss cover and makes the
timer a backstop. Limits: one Mac, 5 runs per configuration, FSEvents only, not exercised through
the real scheduler; a directory moved in with 1,000 children yielded no child events (found only
by a rescan of that directory or the timer).

### A10.6 `sync_all` is F_FULLFSYNC (DESIGN 9.2 lines 802-811)

Added evidence (2026-11-10, W4 Q1, rustc 1.99.0 as pinned in `flake.nix`): std 1.99.0
`library/std/src/sys/fs/unix.rs` `os_fsync` is `libc::fcntl(fd, libc::F_FULLFSYNC)` under
`target_vendor = "apple"`. A dyld `__interpose` of `fcntl` and `fsync` showed 43 `F_FULLFSYNC`
calls for 43 `sync_all`/`sync_data` calls, including a directory handle, and no plain `fsync`.
Limits: shows the call is issued and returns 0, not that media is durable; APFS only.

### A10.7 Open: slice 2b cross-check and section 18 criteria (DESIGN 17 lines 1206-1208; 18 lines 1216-1231)

Text affected: "Sessions the Go candidate captured and Rust did not are counted as a coverage
cross-check" (slice 2b), and section 18 as a whole. After A0 and A2 the Go writers are stopped and
the old archive is deleted (2026-11-09), before any Rust acceptance. The cross-check therefore
cannot run as written, and section 18 has no numeric exit criteria and no abort triggers (a search
of DESIGN.md and this file finds none). Nothing is invented here; both are open items needing an
operator decision. Candidate substitutes found in the repo, not chosen: the local Go spool that the
still-installed hooks write (A0; `docs/session-capture-handoff.md`, cap `max_spool_bytes`); local
sources on both hosts, left untouched by the deletion; the `transform(local)` versus restored
comparison already in slice 2b (shares the transform code with the writer, so it cannot catch a
transform fault); the Rust daemon's own counters (A1: `Replay{...}`, parked, oldest uncaptured age).

### A10.8 Monthly cargo-mutants is not implemented; flake rates (DESIGN 15 line 1084)

Wrong text: "Monthly: `cargo mutants` on `memory-core`." reads as existing. Nothing in
`rust/scripts/gates.sh`, `.github/workflows/rust.yml` or any schedule runs it. Corrected reading:
not yet implemented. The only mutation evidence is the hand-written checkers. First measured
baseline, 2026-11-10, cargo-mutants 27.1.0, 500 cases, example tests only (303 tests, `hegel_` and
`proptest_` excluded), 222 of 223 mutants finished (disk full): `capture.rs` 62.5% (35 caught, 21
missed, 14 unviable), `commit.rs` 89.5%, `state.rs` 91.6%, total 81.9%. `memory-core` (1,157
mutants) not run. Property flake rates (W2, 500-case runs): `proptest_daemon_state_machine` failed
5 of 59 runs (8.5%, Wilson 3.7% to 18.4%), always the `rewrite-mid-file` coverage guard, so about
8.5% of gate runs fail by chance; a plain default-case run fails about 62%. That is why the guard
shapes are being made deterministic.

### A10.9 Gate descriptions (quality-gates.md, rust/README.md)

At `aa04050`, gates `05-hegel` and `06-proptest` run every `hegel_` and `proptest_` test in the
workspace (`cargo test --workspace --locked hegel_` and `proptest_`). The xtask cli test no longer
runs `xtask schemas` in write mode, so `07-schema` (`--check`) can fail. The 391/391 nextest count
is from `d8e80a6`; the test review removed 24 tests and merged 9, so the count changed and has
not been re-recorded. Both documents are updated.

## A11. Implementation findings from the review round (2026-11-10)

Source: [24](implementation/24-design-and-test-review.md). No design value changes.

### A11.1 A failed hook write loses that launch record without trace (DESIGN 9.1, 10.1, 10.4)

DESIGN 10.1 makes the hook silent and exit 0 on every path, and 9.1 removes the failure log.
10.4 says launch evidence is the exception to "a lost hint is a latency bug" and that 7.6 keeps
its file. That covers the daemon side only: when the hook's own write fails (`ENOSPC`, a refused
directory), the launch record for that session is lost and nothing records it. This is accepted,
not fixed: A3 already reduced launch evidence to environment facts. No hook failure log is added.
The hook's unused result codes (`HookResult::code`) are removed because nothing consumes them.
Open option, not adopted: the daemon counts sessions committed with no launch record.

### A11.2 Hint retention is not implemented (DESIGN 7.6, 9.1)

9.1 keeps a hint day file "for 7 days and until every launch record in the file is committed".
The daemon reads hint files and never deletes one. This is a prerequisite of the slice that
wires the daemon: a sweep at start and on tick that removes a file only when both conditions hold.

### A11.3 State-load events are counted, not logged (DESIGN 9.2, 11.3)

9.2 treats a damaged or newer-schema `index.json` or `status.json` as missing and says the event
is logged. The `memory` crate has no logging dependency yet. Until it does, startup classifies
each load (loaded, missing, damaged, newer schema) and `status.json` carries an `events` block
that counts damaged and newer-schema loads and failed launch attachments. "Treat as missing" is
unchanged. Open, tied to the logging decision: the log line itself, keeping the damaged file,
and whether an older build may overwrite a newer-schema `status.json`.

Note, 2026-11-12: the count of failed launch attachments is gone (`baf8b1a`). Attaching a
launch record could only fail while its digest was computed through a JSON encoding; with the
one length-prefixed digest (`d877196`) it cannot fail, so nothing could increment the counter.

### A11.4 State file size bounds (DESIGN 9)

DESIGN states no number. The code bounds a state file body at 64 MiB (67,108,864 bytes) and its
header line at 256 bytes; both are now pinned by boundary tests. `[default, unmeasured]`

### A11.5 Source read refuses a changed file (DESIGN 7.2, 8.2)

The source read opens with `O_NOFOLLOW | O_NONBLOCK` and checks on the open handle that the file
is regular and is the file the last stat observed; otherwise nothing is read and the source is
retried. Measured before the fix: a symlink swapped in between stat and read was followed.
Residual, not addressed by DESIGN: an intermediate directory component swapped for a symlink.

## A12. Operator decisions from the review round (2026-11-10)

### A12.1 Launch values need a safe shape, not only a safe name (DESIGN 8.4)

Operator decision. 8.4 published a launch value when its name was not a secret name, so
`--model sk-ant-...` or `ANTHROPIC_MODEL=sk-ant-...` would have been published. A value (argv
value after a value flag, or an allowlisted env value; the same rule for both) is now published
only if all of the following hold. Otherwise argv publishes `[redacted]` and the env entry is
omitted, the existing 8.4 outcomes.

1. It matches the shape of its slot. Model (`--model`, `-m`, the five `ANTHROPIC_*_MODEL`
   names): `^[A-Za-z0-9][A-Za-z0-9._:/@+-]{0,255}(\[[0-9a-z]{1,8}\])?$`. `--provider`:
   `^[A-Za-z][A-Za-z0-9._-]{0,63}$`. `--permission-mode`: `^[A-Za-z][A-Za-z-]{0,31}$`.
   `MAX_THINKING_TOKENS`, `MAX_MCP_OUTPUT_TOKENS`: `^[0-9]{1,9}$`. `DISABLE_PROMPT_CACHING`:
   `0`, `1`, `true` or `false` in any case. Session, config and cwd slots and
   `CLAUDE_CONFIG_DIR`: the existing safe-value shape.
2. It is not a secret name (unchanged).
3. Split on `/ : @ + .`, no segment starts, ASCII case-folded, with `sk-`, `sk_`, `ghp_`, `gho_`,
   `ghu_`, `ghs_`, `ghr_`, `github_pat_`, `glpat-`, `xoxb-`, `xoxp-`, `xoxa-`, `xoxs-`, `xapp-`,
   `hf_`, `npm_`, `pypi-` or `eyJ`, or is `AIza` plus 16 or more characters, or `AKIA`/`ASIA`
   plus 16 or more uppercase letters or digits.
4. No segment of 32 or more bytes is opaque: all alphanumeric with a letter and a digit, or
   containing an uppercase letter, a lowercase letter and a digit.

Changes in what is published: a model value with a bracket suffix such as `[1m]` is now
published; a 32-character hex session id without dashes is now redacted; numeric and switch env
values are limited to their shapes. Known limits, accepted: a key of unknown format shorter than
32 characters passes in a model or path slot; a long hyphenated lowercase-and-digit run passes
because real model names need it; a model name with one segment of 32 or more bytes that mixes
upper case, lower case and digits (for example `Llama-4-Maverick-17B-128E-Instruct-FP8`) is
redacted, which fails closed.

### A12.2 The audit is a prerequisite of the first production write (DESIGN 7.2, 16 P5, A1)

Operator decision. A rewrite of already captured bytes that falls between the head and tail
windows is detected only by the audit (7.2), and the audit is not implemented (capture always
reports it as not due). The capture property found this case and now avoids generating it.
No production write happens before the audit exists and a property covers that case.

### A12.3 Base recovery reads at most 64 manifests (DESIGN 6.3)

Operator decision. Checkpoints sit at `seq % 64 == 0`, so the longest valid chain is a
checkpoint plus 63 deltas. The code accepted a 65th GET; it now gives up after 64, matching 6.3
("at most 64 manifest GETs").

### A12.4 `rustix` is a direct dependency of the `memory` crate (DESIGN 13)

Operator decision. Pinned to the version already in the lockfile, features `std` and `fs`, for
named open flags and errno values in the source read. It does not close the residual race in
A11.5: source paths are not guaranteed canonical (resolution can fall back to the raw path, and
legitimate paths have symlinked parents such as `/var` on macOS). Closing it needs a rule that
the source adapter reads only canonical paths, carried by a type that only resolution builds.

### A12.5 Gates 04 to 06 run each test once (supersedes the commands in A10.9)

With the properties also running in `04-nextest`, hosted runs on `ubuntu-24.04` and `macos-26`
passed every test but exceeded the 30 minute job limit during `08-coverage` (run 42787616630;
before the review the job took 24 to 25 minutes). The limit is unchanged. `04-nextest` now runs
every test that is not a named property (361), `05-hegel` the `hegel_` tests (30) and
`06-proptest` the `proptest_` tests (28), all with nextest: 419 tests, each once, at the same
case counts.

## A13. Operator decisions on the open review items (2026-11-11)

### A13.1 Shadow run and cutover criteria (DESIGN 17 slice 2b, 18; resolves A10.7)

Operator decision.

- The slice 2b cross-check "sessions the Go candidate captured and Rust did not" is dropped: the
  Go archive was deleted on 2026-11-09 (A2), so it cannot run. Its coverage purpose is served by
  counting local session files that have no captured owner. The slice's main check, comparing
  every restored session with `transform(local)`, is unchanged.
- Exit criterion for the shadow run and for cutover: 2 consecutive days with zero restore
  mismatches, zero parked owners, and zero local session files without a captured owner. This
  replaces the "at least 7 days" and "7-day" shadow-run lengths in 17 slices 2b and 3.
- Abort triggers: any restore mismatch, any owner parked as `Foreign`, or any privacy canary
  found in an export stops the run until the cause is found.
- Section 18 steps 1 and 6 are already overtaken by A2 (one bucket, old archive deleted).

### A13.2 Prerequisites of the first production write (DESIGN 6.3, 7.3, 11, 13; resolves A10.3)

Operator decision. No production write happens before all of these exist:

- zstd encoding of pieces, so the `zstd-3` label and the `.zst` key suffix are true. No
  `identity` encoding is added to the schema.
- the audit (A12.2);
- hint retention (A11.2);
- `tracing` to stderr as DESIGN 11 and 13 specify, replacing the interim event counters of A11.3.

Logging and hint retention land with the slice that wires the daemon.

### A13.3 No separate cap on allowlisted environment names (DESIGN 8.4)

Operator decision. 8.4 says "at most 8 exact names" and lists nine. The cap came from the Go
code (`internal/launch/native.go:58`, `len(out) == 8`) with no stated reason, and which name was
dropped depended on the order of the process environment. The closed allowlist of nine names is
the bound; every allowlisted name whose value passes A12.1 is published.

Note, 2026-11-12: the manifest check still carried the cap of 8 and rejected a record with
nine names as `BadLaunch`, failing the owner's commit. Fixed in `4eea7ab`; the manifest now
takes its bound from the allowlist.

### A13.4 Measured on R2, 2026-11-11: ETags and writes from a dead process (DESIGN 7.4, 15)

Operator-approved live probes under `rust-acceptance/<run-id>/`, synthetic bytes, prefixes
deleted and verified empty (39 keys created, 0 remaining). One client, one network path, one day.

- ETag. For a single-part PUT the ETag is the hex MD5 of the body (50 of 50). Rewriting
  identical bytes keeps the ETag, so `If-Match` with the earlier ETag still succeeds after another
  writer's identical rewrite (5 of 5) and fails with 412 after a different rewrite (5 of 5).
  `x-amz-version-id` changes on every write. Consequence: the fence cannot see an
  identical-bytes rewrite of `current.json`. That is harmless only because the bytes of
  `current.json` name a manifest hash that is unique per commit; this must stay true. Test fakes
  treat the content-derived ETag as the real behaviour.
- A PUT whose client is killed after the whole request was handed to the kernel still lands:
  8 of 10 unconditional and 7 of 10 with `If-None-Match: *`, complete bodies only, first visible
  205 to 507 ms after the kill, nothing later in polls out to 10 minutes. Killed after half the
  body or before any body byte: 0 of 10 each, which is not proof it cannot happen.
- Open for the slice that wires the daemon: a process that starts within about a second of its
  predecessor's death can verify `current.json` before the predecessor's last write lands. The
  startup delay of DESIGN 11.2 (0 to 60 s jitter, not implemented) would cover the observed
  window; the minimum safe delay needs an operator decision.
- The probe used several thousand list requests, more than the roughly 200 planned, because of
  the polling schedule and one aborted attempt. Stored data stayed at a few MiB and is deleted.

### A13.5 The verification GET is attempted 4 times (DESIGN 7.4 step 5)

Operator decision. 7.4 says "retry the GET with backoff" and gives no number. The value is 4
attempts with 3 pauses between them, the same count as the rebase retries. Tests assert the
literal.

### A13.6 The watcher skips a root that does not exist (DESIGN 10.2)

Operator decision. A configured root that does not exist (an agent that is not installed) is
skipped and counted; the other roots are watched, and the safety reconcile finds the directory
once it exists. A root that exists but is not a directory, or cannot be inspected, is still an
error. Also fixed: a symlink to a directory is never treated as a directory by the watcher
(DESIGN 7.1, 8.2).

