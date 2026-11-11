# 15 Capture wiring (slice 2): partial, steps 1-2 done

Synthetic in-memory filesystem and fake store only. Not the real filesystem, not zstd (piece
encoding is the identity, `encoded_*` equal the raw fields), not R2/P2, not the daemon loop.

## Done
1. `capture.rs`: `capture()` observes via `FileSystem::stat`, runs `privacy::admit`, calls
   `planner::plan` (answers `NeedWindows` by reading the head and tail windows through the seam
   and re-planning; `Adopt` by a full prefix read), reads the planned range, runs `transform` and
   `authorize`, slices pieces and returns a `Commit<Published>` plus local `Update`s. Closed
   `SourceOutcome`. Withheld, blocked and unreadable sources add no change and no upload.
   `environment.rs`: added `Stat` and `FileSystem::stat -> Result<Option<Stat>, _>` (`None` is
   absence). `Store` and `StoreError` are untouched. `harness.rs` adapted (`stat` added).
2. Table tests in `tests/capture.rs`: append extend and unchanged-no-write, rewrite and truncate
   give a new epoch, two-observation deletion, snapshot replace, withheld `.env` never read or
   stored, protected-config canary absent from every stored object with a positive control
   (the same search finds a canary in an unprotected transcript).

## Type-level privacy argument
`Published` has a private field. Its piece constructor is private and takes an `AuthorizedBody`,
which only `privacy::authorize` yields (it consumes `AdmittedSource` and `RedactedBytes`, which
have no public constructors). `capture` builds only `Commit<Published>`. A production `Store`
adapter binds `Body = Published`. The public `Published::document` exists solely for the writer's
own manifests and `current.json`. The `compile_fail` doctest on `memory::capture` shows the tuple
constructor is not callable from outside. The doctest was not run separately. Do not read
that as run evidence.

## NOT done
- Property 5 for argv and env at the store sink (only protected-config canary is done).
- Property 4 with the real capture step on Hegel and proptest-state-machine, the independent
  model, the coverage guard and its negative control.
- Dedicated NeedWindows table test (the extend case reaches it implicitly, not asserted).
- Coverage measurement against the 98% floor; `NeedAuditRecheck`, `Blocked`, `Raced` and
  `Unavailable` branches are probably uncovered.
- Known limitations: the whole-stream digest is a fresh read of `[0, to)`; the racy-stat refresh
  is not persisted; audit evidence is always `NotDue`.

## Commands
Run with a pinned Nix shell, `CARGO_TARGET_DIR=target/w-capture`. Clippy (`--all-targets
-Dwarnings`): exit 0. `cargo test -p memory --test capture --test commit_protocol --test harness
-- --test-threads=2`: capture 8 passed, commit_protocol 46 passed, harness 3 passed (about 7 s
wall). That run did NOT set HEGEL_TEST_CASES=2000 or PROPTEST_CASES=2000; the exit status of
cargo itself was not captured (results were read from the grep of its output).

## Requested changes elsewhere
None.

## sha256
87be4de0914b14d6d6f3ea07e756ec144deedcaa518716065381658f42672fe6  rust/crates/memory/src/capture.rs
0afa34dceecf65882f2b06fac10b4ecc31468862c89f902ad9559bb89311e4b5  rust/crates/memory/src/environment.rs
8851d16384b303be820bd82f78cbc7ab3f0b4b491f7c9a25e97a261fa932beb4  rust/crates/memory/tests/harness.rs
220b0cb92864281680f7957c8227012cddf97432a776b1549092956240c589c7  rust/crates/memory/tests/capture.rs

## Round 7 (capture sink: launch evidence, property 5, NeedWindows, branches)

### Launch design (DESIGN.md 6.3, 8.4)
`capture::attach_launches(&mut Commit<Published>, &[SanitizedLaunch]) -> Result<(), ManifestError>`
is additive. `capture()` and every public type are unchanged. The manifest `Header` already
carries `launch: Vec<LaunchRecord>`, so no change to commit.rs or memory-core is needed. Only a
`SanitizedLaunch` becomes a `LaunchRecord` (`LaunchRecord::from_sanitized`, private fields).
Records are de-duplicated by digest, against the existing header records and within the input.
This also satisfies `check_header`, which rejects duplicate digests.

### Property 5 at the store sink (tests/capture.rs)
- `launch_canaries_never_reach_any_stored_object`: argv has `--api-key CANARYKEYARGV`, a
  free-form prompt `summarize CANARYPROMPT please` and `--model sonnet`. The env has
  `MY_PRIVATE_VAR=CANARYENVVALUE` (not allowlisted) and `HOME`. They go through `sanitize_launch`,
  `attach_launches`, `capture` and `commit` into the fake store. Every stored object body
  (pieces, manifests, `current.json`) is searched for each canary, its lowercase form and the
  env var name. The test also asserts that a manifest object exists.
- Positive control: a transcript containing the same canaries is captured, and the same search
  finds each one.
- `attach_launches_deduplicates_by_digest`: 3 inputs with 2 distinct digests give 2 records.
  Re-attaching keeps 2, and attaching an empty slice keeps 2.

### NeedWindows
`need_windows_reads_head_and_tail_through_the_seam` uses a recording `Spy` filesystem. It drives
the planner into NeedWindows with an old boundary of 10 bytes.
- Matching windows (append to 16 bytes): outcome `Planned`, change `extend`, reads
  `[0..10, 0..10, 0..16]`.
- Mismatching windows (first byte rewritten): outcome `Planned`, change `epoch`, same reads.

### Branch table (`failure_branches_have_literal_outcomes_and_store_nothing`)
Each case asserts the literal outcome, no commit and an unchanged store object count.
| Case | Outcome |
|---|---|
| invalid policy context | `Blocked(UnknownReason::ConfigInvalid)` |
| stat error | `Unavailable` |
| read error | `Unavailable` |
| short read (source changed between stat and read) | `Raced` |
| head/tail window read error | `Unavailable` |
| head/tail window short read | `Raced` |

`NeedAuditRecheck` is unreachable. Audit evidence is always `AuditEvidence::NotDue`, and the
planner returns it only for `RecheckRequired`. The two arms that return `Unavailable` for it
(snapshot and append) stay uncovered. This is a small coverage gap, not a behaviour gap. Closing
it needs audit evidence from the shell, which is a later slice. I did not add an artificial hook.

### Commands
Pinned Nix, `CARGO_TARGET_DIR=target/w-csink`. `cargo clippy -p memory --all-targets --locked
-- -Dwarnings`: exit 0. `HEGEL_TEST_CASES=2000 PROPTEST_CASES=2000 cargo test --locked -p memory
--test capture --test commit_protocol --test harness -- --test-threads=2`: exit 0 (capture 12,
commit_protocol 46, harness 3 passed). The first clippy run failed on `indexing_slicing` in my
new tests, which I fixed. Workspace coverage was NOT measured, and neither gates.sh nor the
doctests were run. tests/capture_property.rs did not break `--all-targets`.

### Requested changes elsewhere
None.

### Limitations
Coverage against the 98% floor is unmeasured. The `NeedAuditRecheck` arms are uncovered. The
launch records are attached by the caller, and nothing yet derives them from hint scanning.

## Parent correction (2026-11-09): stale append index

`append()` now uses the local index only when its position equals the remote's latest
committed position (DESIGN 9.1, 9.3). This was found by the property-4 worker (report 16),
and a reverted-fix mutant is killed by both engines. The `capture.rs` hash above is
superseded by `33c20992d78152d34960b201117d079f76f6a8b32bbbc80c062ee2c09b408d04`.

## Parent correction 2 (2026-11-09): stale deletion flag

Integrated gate 6, attempt 1 (seed 730207), failed proptest property 4 with "removed source not
recorded deleted". The minimal sequence was append, reconcile, remove, settle, re-create, a lost
reply while offline, then remove again. A lost reply leaves `Local.deletion_recorded = true` while
the remote holds a live entry, so the next removal planned no `Deleted`. `step()` now takes
`recorded` from the remote entry's `deleted_at` when an entry exists, and from the local flag
only when the remote has no entry (DESIGN 9.1, 9.3). This is the same defect class as
correction 1. Before the fix, seed 730207 fails (cargo exit 101). After the fix, seeds 730207,
730208 and 730209 pass `capture_property` 6/6 and `capture` 12/12 at 2,000 cases. Evidence is in
`evidence/10-slice2-round7/attempt1-stale-deletion-flag/`.

## Coverage round

Test-only (tests/capture.rs). No file under src/ changed.

New tests and literal expectations:
- `admission_denials_map_to_literal_withhold_reasons`: collector path -> `Withheld(CollectorState)`; Profile::Unknown -> `Withheld(UnknownKind)`; owner None -> `Withheld(UnknownOwner)`; `.credentials.json` -> `Withheld(Credential)`.
- `transform_denials_map_to_literal_withhold_reasons`: `{not json` in settings.json -> `Withheld(MalformedProtected)` (snapshot and append); `export API_KEY=literalvalue123` in a TextAsset -> `Withheld(SecretInText)`; a settings.json over 8 MiB -> `Withheld(SourceLimit)`. Each commits no change and stores no object.
- `snapshot_read_failures_have_literal_outcomes`: read error -> `Unavailable`; short read -> `Raced`.
- `snapshot_unchanged_then_changed_after_commit`: replace, then `Unchanged`, then replace.
- `adopt_prefix_read_failures_have_literal_outcomes`: lost local index, read error -> `Unavailable`, short read -> `Raced`.

Uncovered lines (capture.rs), before: 228, 246-252, 257, 258, 263, 265, 266, 390, 393, 443-461, 480, 490, 528, 529, 557, 574, 589, 593, 602, 628.
After (llvm-cov, 50 cases): 228, 252, 257, 258, 263, 265, 266, 393, 444, 446, 447, 450, 456, 459, 461, 480, 490, 528, 529, 557, 574, 589, 593, 628.

Believed unreachable without a production change (requests, not made):
- 228: `LaunchRecord::from_sanitized` error needs an unencodable SanitizedLaunch; no public constructor yields one.
- 252: `ScrubFailed | ContextChanged` arm: `document::scrub` and `text::check` return no ScrubFailed, and ContextChanged needs a binding mismatch that `authorize` cannot see because `admitted_clone` rebuilds from the same context. Request: a test seam or collapse the arm.
- 257, 258, 263, 265, 266, 480, 490, 450, 459, 461: `usize`/`u64` conversion failures cannot occur on 64-bit. Request: use `try_from(..).unwrap_or` helpers shared, or an infallible `as`-free conversion.
- 393, 557, 574, 589, 593: planner results (NeedWindows/NeedAuditRecheck in snapshot, more than 4 rounds, missing index, AuditEvidence::NotDue) cannot be produced by the current inputs. Request: make these `unreachable`-free by type, or inject the planner.
- 444, 446, 447, 456, 528, 529: authorize/admit disagree with the first admit on the same context. Same request as 252.
- 628: `Plan::Withheld` is never returned with `withheld: None`.

Statuses: clippy=0, test=0 (HEGEL/PROPTEST 2000), llvm-cov exit 101 on two attempts (the capture_property and commit property tests' coverage guards, "lost-reply" and "crash-after-manifest" saw 0 hits at 50 cases, not my files), then cov=0 with `--ignore-run-fail`.
