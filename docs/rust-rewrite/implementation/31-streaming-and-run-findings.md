# 31. Streaming capture, directory reads, round progress, test timeouts (2026-11-17)

Coordinator brief. The parent session owns this file and acceptance. It follows
[brief 30](30-acceptance-run.md) and the findings of the 2-day acceptance run
([evidence 21](evidence/21-acceptance-run/)). Evidence goes to `evidence/22-streaming/`. Text that
states a plan stays as written; later changes are marked.

The acceptance run `20261117-a28bfa4` keeps running while this brief is worked on. It uses its own
release binary under `~/.local/libexec/memory-acceptance/`, so nothing here changes it. Nothing in
this brief is installed or sent to R2.

## Goal

Close the run's findings that block the first production write or make the daemon hard to
operate:

1. **STREAM** (D45, DESIGN 7.3, 7.4, 18): neither a capture nor an audit read holds a source's
   whole range, and a capture does not hold all its encoded pieces until the commit. Peak memory
   is measured against 128 MB.
2. **DIRSTORM** (DESIGN 10.2): a directory Create reads that directory, not every root.
3. **PROGRESS** (DESIGN 11.5): a long round logs a progress line.
4. **CONN**: a PUT does not leave its connection unusable for the next request.
5. **TIMEOUT**: a hung test fails with its name instead of running into the 30-minute job limit.

## Checkout

`main` is `4db2975`. This brief is on `docs/brief-31`. Each worker gets one worktree under
`../memory-wt/` on its own branch from `docs/brief-31`; the parent stacks the accepted branches in
the order of the increment table.

## Facts checked on `4db2975` before writing

A read-only planner scoped the work; the parent rechecked each fact below. Paths are under
`rust/crates/`.

| Fact | Where |
|---|---|
| `Job::write` reads `[from, to)` or `[0, to)` into one `Vec` through `read_checked` | `memory/src/capture.rs:529-545`, `:295-309` |
| `RealFs::read` returns the whole requested range as one `Vec` (`read_to_end`) | `memory/src/realfs.rs:129-168` |
| `transform` starts with `bytes.to_vec()` (a second whole copy); `authorize` hashes the whole body and moves it into `AuthorizedBody` | `memory-core/src/privacy/mod.rs:336-380` |
| For a prefix-stable source `transform` is the identity: no protected-document scrub, no Config/TextAsset check | `memory-core/src/privacy/mod.rs:154-165`, `:336-358` |
| `Job::slice` encodes every piece of the body up front and pushes each into `acc.uploads`; `capture` runs every source of the owner into one `Acc`, so all encoded pieces of all the owner's sources live until the commit | `memory/src/capture.rs:652-678`, `:195-221` |
| `commit::upload_pieces` PUTs the collected uploads in order, checks the stop before each, and on a 412 GETs the stored object and records its encoding | `memory/src/commit.rs:306-335`, `:456-472` |
| `Published::piece` zstd-3 compresses one range of an `AuthorizedBody`; `Piece::from_authorized` hashes that range | `memory/src/environment.rs:152-167`; `memory-core/src/manifest/schema.rs:323-350` |
| Pieces are at most `MAX_PIECE_BYTES` = 8 MiB; the daemon uses that as `piece_len` | `memory/src/capture.rs:67`, `memory/src/daemon.rs:845` |
| `audit_read` reads `[0, size)` whole, transforms and authorizes it, then hashes | `memory/src/capture.rs:632-645` |
| `resolve` answers `Plan::Adopt` by reading `[0, captured_to)` whole and keeps it as `Resolved.prefix` for `windows_of` and `whole_state_of` | `memory/src/capture.rs:857-862`, `:904-910` |
| The non-resumed `Extend` hashes the re-read prefix `[0, from)` against the committed digest | `memory/src/capture.rs:546-563` |
| Windows: head 4 KiB, tail 64 KiB | `memory-core/src/planner.rs:8-19` |
| `Create(Folder)` always becomes `Input::DirCreated`, even for a directory gone when looked at; `Scheduler::handle` makes `Action::ReadDir { path }`; `Driver::apply` drops the path and sets the full reconcile flag (`Cause::Dir`). The hint directory is the one exception | `memory/src/watch.rs:199-228`, `:128`; `memory/src/driver.rs:252-268` |
| `walk_root` is the bounded walk (no symlink entered, `WalkLimits`, `locate`); `WalkSources::note` locates one path and remembers it for `of` | `memory/src/realfs.rs:252-318`; `memory/src/resolve.rs:404-430` |
| `Work` has `classify`, `hints`, `capture`, `reconcile`, `next_retry`, `retries_due`, `shutdown`; production is `DaemonWork` | `memory/src/driver.rs:95-119`; `memory/src/wired.rs:84-133` |
| `reconcile_once` loops over owners with no log until the round ends (`log::round`) | `memory/src/daemon.rs:1056-1100`; `memory/src/log.rs:201-223` |
| The S3 `ureq::Agent` is built once; `put` drops the response body unread (`let (parts, _body)`), `get` reads it | `memory/src/s3.rs:178-183`, `:222`, `:271-281` |
| The S3 fake answers with `Connection: close` | `memory/tests/common/s3_fake.rs:149` |
| `rust/.config/nextest.toml` sets no `slow-timeout`; `gates.sh` passes no `--profile`, so only `[profile.default]` applies, locally and in CI | `rust/.config/nextest.toml`; `rust/scripts/gates.sh:41-58` |
| `unsafe_code = "forbid"` for the workspace; `rustix` has `std`, `fs` only (no `getrusage`) | `rust/Cargo.toml:30`, `:49` |

Measured on this Mac on 2026-11-17:

- Test durations: the slowest non-property test is `memory::counters
  reconcile_at_30k_unchanged_sources_stats_each_once` at 8.9 s (644 tests, 43.6 s). At 500 cases
  the slowest property test is `memory::capture_property hegel_property4_capture_state_machine`
  at 105.3 s, then two `deltas_from_checkpoint` twins at 54 s and 52.5 s (60 tests, 258 s).
- `/usr/bin/time -l` prints `peak memory footprint` and `maximum resident set size` for its child.
- In the run: peak `phys_footprint` 1,254 MB, in the audit round right after the start round;
  431 MB during the start round. The largest local file is 98 MB; a Pi owner has many child
  files, and all of an owner's sources are captured into one `Acc`.

## Decisions the parent made where the design is silent

Each is reversible before the first production write. A worker that finds one wrong reports it as
a question and does not decide.

| Topic | Decision | Reason |
|---|---|---|
| What streams | Prefix-stable sources (every source the slice-2 walk yields is one) are read, hashed, encoded and uploaded one piece at a time: read at most `piece_len` bytes, authorize them, feed the piece hash, the whole-stream `Sha256State` and the head/tail windows, compress, PUT, drop. Sources that are not prefix-stable keep the whole-input path; the worker reports which caps bound them and whether the slice-2 walk can yield one | DESIGN 7.3 steps 1 to 6 are already per piece; the identity transform makes per-piece authorization equal to whole-body authorization |
| Who PUTs a streamed piece | Capture hands each piece to an upload sink as it is produced; the daemon's sink is the commit's piece step (stop check before each PUT, 412 then GET and adopt the stored encoding). The manifest is still built and PUT only after every piece of every source in the commit is stored (7.4 step 1) | No second read of the file between hashing and upload; at most one encoded piece alive |
| A source that fails after some pieces | Pieces already PUT stay (content-addressed, unreferenced, harmless); the source's outcome is what it is today (`Raced`, `Unavailable`, ...) and the owner's commit is not built if it would be today | DESIGN 7.3: "abandoned before the manifest is written" |
| What stays byte-identical | Object keys, encoded bodies, every manifest field and hash, `index.json` and `status.json` contents, audit verdicts, outcomes. Pinned by a property: streamed capture equals today's whole-body capture for random sizes, piece lengths, deltas and resumes | No stored format change |
| Audit and adopt reads | Stream the same way: stat, chunked read with a fresh hasher, stat; the adopt read hashes `[0, captured_to)` chunk by chunk and keeps only the window bytes | Same memory bound |
| How 128 MB is measured | A real-binary test: the daemon against the loopback S3 fake with (a) one 100 MB incompressible source, (b) one owner whose sources total more than 256 MB, then (c) an audit round over them. macOS: run the daemon under `/usr/bin/time -l` and read `peak memory footprint`; Linux: read `VmHWM` from `/proc/<pid>/status` before the stop. Assert below 128,000,000 bytes. If the target is not met, the worker reports the number and the largest remaining buffer; the threshold is never raised | No new dependency, no unsafe; a counting allocator needs `unsafe impl GlobalAlloc` |
| `memory check` | Not streamed in this brief (a separate read-only process, not the daemon); recorded as a known limit | D45 names capture and audit |
| Directory read | `Action::ReadDir { path }` no longer sets the full flag. The driver collects the paths of a batch (deduplicated) and calls a new `Work::read_dir(path)`. Production: a path that is not a real directory now is ignored; a path under no root is ignored; otherwise a walk bounded like the full walk (same `WalkLimits`, no symlink entered) runs under that directory only, and each located append source is noted (`Sources::note`) so `of` sees it. Each owner found is fed to the scheduler as `Signal::Changed`, so it is captured after the coalescer window like a changed file. A truncated walk sets the full flag with `Cause::Dir` | DESIGN 10.2 |
| Log of a directory read | No `round` line (it is not a round) and the 15-minute timer is not reset. Captures it causes log their `capture` lines as today | Keeps `round` meaning "full reconcile" |
| Claude session index on a new `projects/<slug>/` | Unchanged: rebuilt by the next full reconcile; a new transcript itself locates without the index. Recorded as a known limit (a `debug` file of a session created since the last full reconcile has no owner until then) | Minimal change; the safety reconcile is the backstop |
| Progress line | New log event `progress` with closed numeric keys `done`, `total`, `landed`, `failed`, `elapsed_s`, emitted inside `reconcile_once` when at least 60 s of monotonic time passed since the round started or since the last `progress` line. No owner, no path. Added to the event table in `log.rs` | DESIGN 11.5; the first round took 1 h 36 min with no line |
| Connection reuse | `put` reads the reply body to its end (bounded; an error reply is small) before dropping it. Proven by a keep-alive mode of the S3 fake that counts accepted connections | ureq returns a connection to its pool only once the body is read |
| Test timeouts | `[profile.default] slow-timeout = { period = "60s", terminate-after = 3 }` (3 min, against 8.9 s measured); an override for `test(/hegel_/) \| test(/proptest_/)` with `terminate-after = 15` (15 min, against 105 s at 500 cases; leaves room for coverage instrumentation and the slower ARM runner and still ends before the 30-minute job limit) | Measured above; nothing is relaxed |

## Increments

| # | Increment | Branch | Owner | Wave |
|---|---|---|---|---|
| 1 | STREAM | `rust/streaming-capture` | worker | 1 |
| 2 | DIRSTORM | `rust/dir-read` | worker | 1 |
| 3 | CONN | `rust/put-conn-reuse` | worker | 1 |
| 4 | TIMEOUT | `rust/nextest-timeout` | parent | 1 |
| 5 | PROGRESS | `rust/round-progress` | worker | 2 (after STREAM; both touch `daemon.rs`) |

### STREAM

Owns `memory/src/capture.rs`, `memory/src/commit.rs`, `memory/src/environment.rs` (the piece
builder and, if needed, a sink type), in `memory/src/daemon.rs` only `capture_sources`, `land` and
what wires the sink, `memory-core/src/manifest/schema.rs` only for a per-piece constructor that
takes the piece bytes, `memory-core/src/privacy/mod.rs` only if a per-piece authorization needs a
new public function (the identity path; `prefix_stable` unchanged), and their tests, plus a new
real-binary test file under `memory/tests/s3_store/`. Must not touch `realfs.rs`, `resolve.rs`,
`driver.rs`, `watch.rs`, `wired.rs`, `log.rs`, `s3.rs`, `check.rs`.

- Proof: the equivalence property (streamed equals whole-body: uploads, manifest, index updates,
  outcomes, audit counts) over random file sizes around piece boundaries, `piece_len` 1 to 8 MiB
  scaled down in tests, `Extend` resumed and not, `Epoch`, `Adopt`, a raced shrink mid-stream; a
  test that at most one piece body is alive (a sink that records the bodies it holds and a spy
  `FileSystem` that records each read's length: no read longer than `piece_len` for a
  prefix-stable source); a stop between pieces still ends before the manifest; the existing
  commit-protocol, capture, capture-property and daemon tests pass unchanged or each changed
  assertion is named; the real-binary peak-memory test above.
- Teeth: read the whole range in one call; drop the per-piece stop check; off-by-one at a chunk
  boundary; skip the stat-after-read of the audit; build the manifest before the last piece is
  stored; skip the 412 adopt for a streamed piece; keep every encoded piece until the commit.

### DIRSTORM

Owns `memory/src/driver.rs`, `memory/src/watch.rs`, `memory/src/wired.rs`, `memory/src/realfs.rs`,
`memory/src/resolve.rs`, `memory/src/daemon.rs` only for a pass-through to `Sources` if needed,
and their tests (`tests/driver.rs`, `tests/watch.rs`, `tests/wired.rs`, `tests/realfs.rs`,
`tests/resolve.rs`, `tests/s3_store/event_loop.rs`). Must not touch `capture.rs`, `commit.rs`,
`log.rs`, `s3.rs`.

- Proof: the driver with a fake `Work`: N `DirCreated` events in a batch give one `read_dir` per
  distinct path and no `reconcile`; owners it returns are captured after the window; a truncated
  read sets the flag with `Cause::Dir`; the hint-directory exception is unchanged; the 15-minute
  timer is not moved by a directory read. The real walk: a vanished directory reads nothing; a
  directory under no root reads nothing; a symlinked directory is not entered; a new
  `projects/<slug>/` with a transcript yields that owner; files outside the directory are not
  stat'ed (counters). In the real-binary event loop: creating and removing `x.json.lock/` under
  a root 20 times logs no `round cause=dir`; a directory moved in with a transcript is captured.
- Teeth: `read_dir` sets the full flag; the existence check removed; a symlink entered; the
  walk starts at the root instead of the directory; truncation ignored.

### CONN

Owns `memory/src/s3.rs` and `memory/tests/common/s3_fake.rs` (a keep-alive mode, default
unchanged), plus a test in `tests/s3_store.rs`. Must not touch anything else.

- Proof: against the fake in keep-alive mode, ten PUTs and a GET use one connection (accept
  count 1), for 200 and for 412 replies; a reply body larger than a bound is not read whole (the
  connection is dropped instead); existing S3 tests unchanged.
- Teeth: the body dropped unread (accept count 10).

### TIMEOUT (parent)

Edit `rust/.config/nextest.toml` as decided. Proof: a scratch test outside the repository that
sleeps past a scratch config's tiny limit is reported `TIMEOUT` by nextest; the gates' 04, 05, 06
and 08 still pass on the combined tip.

### PROGRESS

Owns `memory/src/log.rs`, in `memory/src/daemon.rs` only the loop of `reconcile_once`, and
`tests/log.rs`, `tests/daemon.rs`. Starts from the accepted STREAM branch.

- Proof: with the test clock advanced 61 s per owner, a round of 3 owners logs 3 `progress` lines
  with exact keys and counts; a round under 60 s logs none; no line holds a path or owner; the
  event appears in the module's event table.
- Teeth: never emitted; emitted every owner; a path or owner in the line.

## Rules for workers

The rules of [brief 28](28-production-prerequisites.md#rules-for-workers) apply unchanged, with
those of briefs 29 and 30: an existing test is removed only when the report names the test that
now carries the same assertion; no new dependency; no change to what `status.json`,
`index.json`, a manifest or an object stores. Real-binary tests are timing-sensitive: a worker
stresses a new one 30 times alone (no other cargo job) and reports the failures.

## Acceptance (parent)

As in brief 30: whole diff read; the seven checks (fmt, clippy, rustdoc, nextest, doctests,
schemas, `cargo deny`); the four mutation predicates each still occur once; the teeth above run by
the parent on the intended assertion; `scripts/gates.sh` once on the combined tip. For STREAM also
the peak-memory test run by the parent on this Mac. Then push the stack (secret check of the
range first), hosted gates, merge.

After this brief: end the acceptance run (two clean days), teardown, prefix deleted and listed
empty; a second short run with the streamed binary that measures the peak on real data; then
`memory export` and the consumer gate (answer 9), lifting the empty-prefix refusal, and cutover
(DESIGN 18).

## Known limits carried

Deleted files are never recorded as deleted (slice 3); no `unpark`; `memory check` holds whole
sources; a new Claude session's `debug` file has no owner until the next full reconcile; a round
does not end early when the network is down.

## Progress ledger

- 2026-11-17 13:00Z: brief written on `4db2975` from the run findings of evidence 21 (day 0 and
  day 0 continued); planner claims rechecked; test durations measured.
- 2026-11-17 14:50Z: all five increments accepted locally; stacked as TIMEOUT, CONN, DIRSTORM,
  STREAM, PROGRESS, tip `ef87ec2` (11 commits over `d7d40cb`). Seven checks 0 (670 passed, 12
  doctests); full gates all 13 sub-gates 0, 500 cases, seed 391027579, coverage 96.77 %. Peak
  footprint in the real-binary test 32 MB capture, 23 MB audit (was 138 MB and 357 MB), 30 runs 0
  failed. Changes at review: the parent added the GET error-body read to CONN; the first STREAM
  worker hit its turn limit, the parent committed its sink change and a fresh worker wrote the
  proofs and found the per-file growth (exact read allocation, by-value transform). A worker
  inspected the live acceptance daemon with `heap`/`vmmap` (boundary incident, run unaffected).
  Record: [evidence 22](evidence/22-streaming/). Next: push the stack, then end the run after two
  clean days, then a short run with the streamed binary on real data.
