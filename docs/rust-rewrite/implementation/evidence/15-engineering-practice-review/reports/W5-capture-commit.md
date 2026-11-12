# W5 review: capture, commit, environment, realfs, hook, main, workspace (C1 to C7)

Read-only. Read in full: capture.rs, commit.rs, environment.rs, realfs.rs, hook.rs, main.rs, memory-reader, both Cargo.toml, deny.toml, DESIGN 13 and 7.4. Not read: memory-core internals beyond `limits.rs`/`schema.rs` signatures, DESIGN 4/6.2/7.3/10.1/14, amendments A3/A10.3/A11.5/A12.3/A13.2/A13.5, P2 prototype, tests (grepped only to count users), `ManifestError` definition.

## 1. Verdict

| Crit | Verdict | Why |
|---|---|---|
| C1 boundaries | mostly sound | `Published` capability + `Store` seam are clean; `environment.rs` carries test-only `Counting` and `attach_launches` sits in capture.rs |
| C2 extensibility | mostly sound | Store/zstd seam is narrow (`slice`, `Published::piece`); main.rs argv ladder and no agent-agnostic issues in scope |
| C3 maintainability | mostly sound | `Failure` is a clean closed enum; `commit_known` is a 94-line loop with 4 mutable locals; capture has no single orchestrator |
| C4 names | mostly sound | strong type names; weak generic ones (`Env`, `Acc`, `Job`, `Mode`, `step`) and `manifest_hash` used for arbitrary bytes |
| C5 data structures | mostly sound | `Published`, `StoreError`, `HookResult` remove branches; `Landed{outcome, follow_up: bool}` and positional `Mode::Extend(..)` add them |
| C6 DRY | mostly sound | few real duplications (admit twice, epoch-number twice, `known.clear()+Landed` x3, two `Counters` types) |
| C7 YAGNI | mostly sound | `stat_len`, non-paced `disposition`/`commit`, test-only `Counting` in lib, empty `memory-reader` dep |

## 2. What is good

- `environment.rs:142` `Published(Vec<u8>)`: private field, two crate-private constructors, five `compile_fail` doctests. Privacy ("only authorized bytes leave") is carried by type, not convention.
- `environment.rs:59` `StoreError` is a closed `Copy` enum with no payload but `Option<u32>`; `class()` (97) is the single mapping to `ErrorClass`. Error-text privacy is enforced by type here.
- `commit.rs:93` `Failure` has one variant per outcome class in DESIGN 7.4/A6: `NotLanded`, `LandingUnknown`, `Throttled`, `BaseUnchanged` (own variant, A6), `Foreign`, `NewerSchema`, `Invalid`, `Corrupt`; `outcome()` (132) is an exhaustive match.
- `commit.rs:279` `verify` returns `Option<Verdict>`; the decision logic is the pure `classify` in memory-core, so the I/O driver has no classification branches of its own.
- `commit.rs:238` `adopt_winner` already handles "a 412 means a different encoding won", so a real zstd store lands without touching the driver.
- `hook.rs:188` `write_line` does exactly one `write(2)` and maps short write to a variant; no clap, argv passed empty (`hook.rs` `process`, `argv: &[]`), nothing printed.

## 3. Findings (ranked)

**F1 (medium, C3/C5) `commit.rs:357-450` `commit_known` is the whole protocol in one 94-line loop with flag state.**
Locals `fence`, `named`, `rebases`, `absent` mutate across the loop. The three success exits repeat `known.clear(); return Ok(Landed{..})` (391-399, 407-415, 419-424). `absent` (405, 437, 442) is a flag that selects the exhaustion variant, i.e. the outcome is decided by a boolean, not by a verdict-shaped value. `Landed.follow_up: bool` (77) plus `outcome: CommitOutcome` encodes three cases (Committed / AlreadyOurs / Adopted) as 2 fields; `Adopt` is `AlreadyOurs + follow_up:true`.
Why: the sequence is readable in order (upload 371, build 372, manifest PUT 376, pointer 377, guarded PUT 386-389, verify 406) but the rebase/exhaustion rules are interleaved with success exits. Remedy: split a `fn guarded_put(...) -> Attempt` and a `fn landed(known, outcome, etag, built, follow_up)`; make `Landed` carry a 3-variant enum (`Committed`, `AlreadyOurs`, `Adopted(earlier)`) so the caller's `follow_up` branch becomes a match. About 40 lines moved, 2 h. Also the `Failure::commit_outcome` + `Failure::outcome` + `CommitOutcome` + `Outcome` quartet (123, 132) means three outcome enums for one result; no non-commit.rs caller of `commit_outcome()` (rg) so it is dead outside tests.

**F2 (medium, C1/C7) `environment.rs:208-298` test instrumentation lives in the production lib.**
`Counters` + `Counting<T>` (90 of 298 lines) have zero production users: `rg Counting crates/*/src` outside environment.rs is empty; only `tests/counters.rs` (3 hits) uses it. `realfs.rs:43` defines a second, different `Counters` type (stats/reads/bytes_read) and `realfs.rs:75` `counters()` has no caller in `src`. Two types with one name and overlapping purpose. Remedy: move `Counting`/`Counters` to `tests/common` (or `#[cfg(feature="sim")]`), rename realfs's to `FsCounts` or delete until the 10.3 cost accounting is wired. About 90 lines out, 1 h. Not intrusive at call sites (no production function mentions it).

**F3 (medium, C7) `FileSystem::stat_len` is dead in production (confirmed).**
`rg stat_len crates`: trait `environment.rs:43`, `Counting` 256, `RealFs` `realfs.rs:132`, `SharedFs` forwarder `resolve.rs:223`, two test fakes (`tests/capture.rs:367`, `tests/common/fs.rs:61`), and tests in realfs/resolve. No call in `capture.rs`, `daemon.rs`, `watch.rs`. It is also the only producer of `RealFsError::Absent` (realfs.rs:29). Remedy: delete the method, `Absent`, and 3 forwarders; ~35 lines, 0.5 h.

**F4 (medium, C2/C5) compression seam will cut through `capture.rs::slice` and `Published::piece`, not the commit driver.**
`capture.rs:560-583` builds each piece twice from the same raw range: `Published::piece(body, at, end)` (upload bytes) and `Piece::from_authorized(body, base, at, end)` (descriptor, memory-core `schema.rs:321`). Today encoding is identity so both agree. With zstd they must agree on encoded bytes; the two-call shape invites divergence. A10.2 key definition is single: `object_key(raw)` at `memory-core/src/limits.rs:53` (and `commit.rs:31` re-exports `manifest_key` from `limits.rs:48`, so that is the single definition, no copy). Remedy now: make `slice` call one `encode_piece(body, at, end) -> (Published, Piece)` in `environment.rs`/core so the later zstd change is one function; ~20 lines, 1 h. Commit driver and `adopt_winner` need no change. Real `Store` (ureq + rusty-s3) is a sync `&mut self` trait: fits [inferred from P2 choice, not re-read].

**F5 (medium, C3) capture has no function that reads as DESIGN 7.3.**
Responsibilities in capture.rs (812 lines): local-state types and `Update::apply` (51-119), outcome enum, `attach_launches` (205, a commit-header concern, 1 caller `daemon.rs:766`), privacy admission/authorization, planner rounds (`resolve` 705), digest resume (586-606), window evidence (610, 245, 679), piece slicing, index update. `capture` (169) is only a loop over `step` (331, 81 lines, 6 params); the algorithm is spread `step` -> `snapshot`/`append` -> `resolve` -> `Job::write` (473-537, 65 lines). Longest: `step` 81, `Job::write` 65, `resolve` 56. Parameters: `step`/`snapshot`/`append` 5-6, `stream_span` 6, `absence_plan` 5. Generics: only one parameter `<F: FileSystem>` (8 fns), no `<S,F,C>` pileup in this file; `Job` already is the context struct, adding `fs` to it would fight the borrow. `Clock`+`Store`+`FileSystem` triple appears only in `daemon.rs:454` (W4). Remedy: split into `capture/{mod,append,snapshot,digest}.rs` and move `attach_launches` and `Local/Update` out; no logic change, 1-2 h. Not urgent.

**F6 (low, C6) duplicated knowledge in capture.rs.**
(a) `admit(...)` matched with identical Allow/Deny/Unknown arms at 339-343 and 659-665 (`admitted_clone`, because `authorize` consumes). Make `admit_or_outcome(&Env,&Source)->Result<AdmittedSource,SourceOutcome>` and call it twice: -8 lines. (b) `epoch.unwrap_or(self.next_epoch.saturating_sub(1))` at 527 and 650. (c) `Known` (450) and `Segments` (452) alias the same Vec/slice shape. (d) `from <= to` in `resume_state` (542) is always true because `to = from.max(size)` (481): dead condition. (e) `manifest_hash` (memory-core) is used for the digest of arbitrary bytes at capture.rs:242, 596, 627 and `commit.rs:478` plus a wrapper `sha256_of` (commit.rs:221) whose comment says "not a manifest identity": rename the core function `sha256_of`, delete the wrapper.

**F7 (low, C2/C3) `main.rs` hand-parse is fine for 2 subcommands, will become a ladder at 4.**
`main.rs:8-18`: `word == "hook"` / `"daemon"` arms, `unwrap_or_default` agent, everything else exit 78. Seven subcommands (DESIGN 13) with args (`restore <owner>`, `unpark <owner>`) means a ladder. Keep the hook path argv-free (A3/footprint), but add `enum Command { Hook(String), Daemon, ... } fn parse(args) -> Result<Command, ExitCode>` with the first real subcommand; ~25 lines, 1 h, and then dispatch is a flat match. clap only behind the non-hook arms (DESIGN 13 note). `memory daemon` today (`daemon.rs:349-360`): `default_root()` (exit 1 if none) -> `open(&root, root/config.toml, SyncPolicy::Full)`; on error exits with that error's code; on success drops the opened state and exits `EXIT_NO_ADAPTERS`. No loop, no watching, no capture, no store. `main.rs` doc "startup and refusal only" is accurate. Unknown subcommands also exit 78, which main.rs says is the same "not implemented" code: cannot be told apart from `EXIT_NO_ADAPTERS` unless the constant differs [not checked].

**F8 (low, C7) `hook.rs` does a little unneeded work before it knows it needs it.**
`hook.rs` `process` collects the whole environment into `Vec<String>` (all vars, formatted) on every event, but `run` uses it only for `SessionStart` (`run`: `if input.event == HintEvent::SessionStart`). Under the 1 ms target this is the avoidable part. Remedy: pass a closure/`fn env()` evaluated lazily, or collect only after `parse_hook_input`; ~10 lines, 0.5 h. Otherwise minimal: one `write(2)` (188), exit 0 on all paths, bounded stdin thread with 2 s deadline (`read_with_deadline`), no clap, no argv recording. `HookResult` has 11 variants that `main_entry` discards (`let _ = process`); they serve tests only, but cost nothing and keep the outcome closed. Hand-rolled `utc_day` (Hinnant, 11 lines) justified: avoids a time dependency.

**F9 (low, C6/C7) DESIGN 13 says cargo-deny bans enforce "no I/O crates" in memory-core; `rust/deny.toml` has no `[bans] deny`/`wrappers` entry.**
`deny.toml` bans only `multiple-versions` and `wildcards`. Either the enforcement is elsewhere (lint-check crate or xtask; not read) or the claim is not implemented. Remedy: confirm, then add a `deny` list for `ureq`, `rustix`, `notify`, `tokio` scoped with `wrappers`, ~8 lines.

**F10 (low, C7) `memory-reader`**: `lib.rs` is 3 doc lines; `Cargo.toml:13` depends on `memory-core` that nothing uses. Slice 2b not started. It does no harm (compiles in ~0), and the stub documents "an empty library is not a successful restore", but it is a placeholder with a dependency edge. Prefer to drop the member and the dependency until 2b (3 lines), or keep and drop only the dependency. Not worth debate.

**F11 (low, C7) pub surface with no non-test user** (counts from `rg` over `rust/crates/*/src`, excluding the defining file): `commit::disposition` (non-paced wrapper, daemon uses `disposition_paced`), `commit::commit` (wrapper over `commit_known`), `commit::check_fence` (pub, internal use only), `Failure::commit_outcome`, `REBASE_RETRIES`, `GET_ATTEMPTS`, `Published::sim` (feature-gated, fine), `Counting`/`Counters`, `FileSystem::stat_len`, `realfs::Counters::reads`/`counters()`. The first three are test conveniences: demote to `pub(crate)` or move to tests: ~25 lines.

Notes on C5 (type earns its keep, with examples):
- Earns: `Published` (removes any runtime "was it redacted?" check), `StoreError::class` (one mapping), `Mode` -> `Change` is a 1:1 mapping (514-518) so `Mode` is nearly a duplicate of `Change` variants; positional `Mode::Extend(Offset, Option<Sha256State>, Known)` (457) wants named fields.
- Ceremony: `Update` enum (85) is a command object applied in `daemon`; its 5 variants are each one-line mutations, so acceptable, but `Missing`/`Present`/`Deleted` could be one `Presence` update.

Errors (Q7): enforced by type in `StoreError` (no String), `RealFsError` (`Io(ErrorKind)` is a closed std enum; Display prints the kind only), `HookResult` (no payload), `SourceOutcome` (carries only closed core enums). `FileSystem::Error` is an unbounded associated type (environment.rs:32), but `capture.rs:263` drops it with `|_|`, so privacy depends on that one discard rather than on a bound. `Failure::Invalid(ManifestError)` is payload-closed only if `ManifestError` is: [inferred, not read]. No module-specific `Error` enum in capture/commit: they use outcome enums instead, which fits.

## 4. Names

Ten least clear (current -> better):
1. `Env` (capture.rs:139) -> `StepInputs` (clashes with process env in hook.rs and `Env` vars).
2. `Acc` (162) -> `StepOutput`/`Pending`.
3. `Job` (462) -> `SourceCapture`.
4. `Mode` (454) -> drop in favour of `Change` or `WriteKind`; also confusable with `rustix::fs::Mode` in realfs.
5. `step` (331) -> `capture_source`.
6. `snapshot`/`append` (414, 763) -> `capture_snapshot`/`capture_append`; `append` also names `hook::append` (hook.rs).
7. `manifest_hash` (core) used for any bytes -> `sha256_of`; delete `commit::sha256_of`.
8. `Local` (70) -> `SourceLocal`/`LocalCache` (and `Locals`).
9. `Failure::outcome` vs `Failure::commit_outcome` (123/132) -> `attempt_outcome` / `core_outcome` (or delete the latter).
10. `Known`/`Segments` (450/452), `Resolved.known` -> `DiskSegments`; `absent` (commit.rs:405) -> `first_write_404`.

Three exemplary: `Published` (environment.rs:142); `StoreError::class` (97); `Failure::base_may_be_stale` (commit.rs:155) says what the caller does with it.

## 5. Q1 step comparison: code vs DESIGN 7.4 (as amended)

| Step | In design | In code |
|---|---|---|
| 1 PUT pieces, 412 is success | yes | `upload_pieces` 252; goes further: GET on 412, adopt the winning encoding (R2, A10) |
| 2 build + PUT manifest If-None-Match | yes | 372-376; 412 ignored (`put_document` 329), safe because content-addressed [inferred] |
| 3 guarded PUT, first write If-None-Match | yes | 386-389 |
| 4 record ETag, update index, remove committed launch records | yes | ETag returned in `Landed`; index and launch removal are in `daemon.rs` (W4); `known.clear()` is extra (A6) |
| 5 GET on error/412; Ours/Base/Foreign/NewerSchema/404 | yes | `verify` 281 + `classify`; GET retried 4x on error/404 (`GET_ATTEMPTS`), 404 with a fence => `LandingUnknown(Offline)` (435), first write retries step 3 |
| 5 BaseUnchanged retry up to 4, then Retry | yes | `REBASE_RETRIES` 440-447 -> `Failure::BaseUnchanged` (A6 class) |
| 6 invariant: previous == named | yes | `check_fence` 199, each loop iteration 385 |
| in code only | | `Auth` short-circuit at 400 and 311 (no GET); `Adopt` verdict (416, A6 known_ours); 412 piece readback |
| in design only | | none missing in commit.rs; launch-record removal and index update are the caller's |
Reading order: top to bottom yes; outcomes: `Result<Landed, Failure>` with each class a variant, yes.

## 6. Checked and fine
- Dependencies: serde, serde_json (daemon.rs:297,874), toml (config.rs:168), sha2 (state.rs:7), thiserror, notify (watch.rs), rustix (realfs.rs:150; A12.4 covers it) are all used by `memory`. Not yet present and listed in DESIGN 13: zstd, rusty-s3, ureq, clap, tracing, rusqlite: consistent with slices not started. `schemars` is in workspace deps only for other crates. Nothing used beyond DESIGN 13 except rustix (amended).
- Lints deny unwrap/expect/panic/indexing; capture/commit use `.get()` throughout.
- `commit.rs:31` is the single `manifest_key` definition (re-export of `limits.rs:48`).
- `RealFs::read` (realfs.rs:138-177): NOFOLLOW, NONBLOCK, identity re-check against last stat; counters inside `RealFs` are two `+=` lines, not intrusive. Minor: `register` returns `(len - 1) as u64` and `read` uses `buf.len() as u64` (lines 71, 175) where the rest of the crate uses `try_from`.
- `Clock` has one production consumer (`daemon.rs:454`) plus the `Counting` delegate; fine as a seam for simulation.
