# Session capture: current delivery handoff

This is the sole current deployment/qualification/next-action brief. For Rust,
[DESIGN.md](../DESIGN.md) is authoritative; read its section 0 first. The
[capture contract](session-capture-contract.md) retains goals and Go maintenance
requirements. The [holistic review](holistic-review-20261029.md) records historical
Go cleanup rationale, not the Rust mechanism.
The previous handoff is preserved **verbatim** in the
[October 30 archive](session-capture-handoff-archive-20261030.md). Its pre-merge tasks,
worker assignments, installation tables and earlier “current” checkpoints are dated
history, not current execution instructions or renewed operating authority.

## Current session checkpoint

### Rust implementation delivery — active coordinator brief

**Acceptance: pending.** User requested implementation of DESIGN.md with
`gpt-6.1-sol` workers at medium effort, parent-owned quality and verification.
This is the single active task brief; dated operating records below remain history.

> **Resume here (updated 2026-11-12, after the engineering-practice review).** Read DESIGN.md, then
> [design-amendments-20261109.md](rust-rewrite/design-amendments-20261109.md) (A0–A13, wins
> where they conflict), then the "Session 2026-11-12" block directly below, which is the
> current state, and [report 25](rust-rewrite/implementation/25-engineering-practice-review.md),
> the active coordinator brief, with
> [evidence 15](rust-rewrite/implementation/evidence/15-engineering-practice-review/). The
> "Session 2026-11-10/11" block after it is history: stack #56 (PRs #54–#81) is merged.
> [Report 24](rust-rewrite/implementation/24-design-and-test-review.md) is the
> full record and [evidence 14](rust-rewrite/implementation/evidence/14-design-test-review/)
> its raw material. The "Session 2026-11-09, review and fix round" block is now history; its
> "Next" line (slice 2) still stands, with the additions below. The "Handover 2026-11-09" and
> "Progress 2026-11-09, continued session" blocks further down are history; their next-action
> lists are done except step 4 (S3 adapter, zstd, run loop) and step 5. Earlier blocker lists
> in this brief (P5 authorization, acceptance bucket/token, hook argv, symlinks, state root)
> are resolved by the amendments and kept only as history.

**Session 2026-11-12, engineering-practice review and data-structure stack (current).**
Operator-directed read-only review of the Rust code at `41a07ff` by eight Sonnet workers with
parent acceptance, then prototypes of three data structures; record in report 25.
- Review: the code is mostly sound in the small. Delivery has been horizontal: 17 of 25
  implementation reports are components, 7 are review rounds, 1 is a thin end-to-end step.
  Nothing runs end to end (`memory daemon` exits 78, no S3 client, zstd, run loop or reader).
- Operator-approved plan: (1) walking skeleton: zstd, the S3 adapter, a one-file capture
  under `rust-acceptance/`, a minimal restore; (2) hold further review rounds and mutant
  triage until the run loop is wired, taking only the audit, hint retention, `tracing` and
  the startup delay; (3) structural fixes as the skeleton touches the code; (4) fold the
  amendments into DESIGN.md and fix the stale AGENTS.md lines.
- Prototypes, measured against a decision rule fixed in advance: `OwnerRt` becomes
  `RemoteBase` + `Turn` with `recover` returning the base; the commit result becomes
  `CommitEnd { Landed, Adopted }` with local effects behind `apply_landed(&Landed, ..)`;
  `authorize` borrows the admitted source, keeps its re-admit, and `binding()` destructures
  exhaustively. The idle-round benchmark also found `reconcile_once` quadratic in owners
  (30,000 owners: 3.24 s a round); an index map brings that to 72 ms.
- Stack, local only, nothing pushed, in the worktree `/Users/mm/dev/memory-wt/ds-integrate`
  on `41a07ff`: `rust/round-grouping` (`f9d8b50`), `rust/owner-turn-base` (`d60ec49`),
  `rust/commit-end` (`b1e45f6`), `rust/authorize-borrow` (`0f3cb7d`),
  `docs/engineering-practice-review` (this entry, report 25, evidence 15).
- Gates on `0f3cb7d`: local, 500 cases, seed 374449330, all 13 sub-gates 0, 431 tests, 12
  doctests, coverage 5869/6006 = 97.72 %, `verify-evidence.py` PASS. Not run: 2,000 cases,
  hosted CI, `cargo mutants`.
- Open operator decisions: push the stack and open its PRs; a bound on how long capture stays
  off (Go writers stopped 2026-11-09, A0); how to fold the amendments into DESIGN.md, which
  `verify-evidence.py` hash-pins; plus the two still open in the block below.
- Next: plan step 1 on a branch above the stack, starting with the part that needs no R2
  (zstd piece encoding, the S3 adapter behind `Store` against a local HTTP server).

**Session 2026-11-10/11, design and test review (history).** Operator-directed review of the
design, tests, complexity and Hegel properties by Sonnet workers with parent acceptance;
record in report 24. Result on `main` (`c239d1b`):
- Design: amendments A10 (corrections of fact and evidence labels), A11 (hook failure, hint
  retention, state-load events, state size bounds, changed source file), A12 (launch value
  shapes, audit before the first production write, 64-manifest recovery cap, `rustix`, gates
  04–06 run each test once), A13 (shadow-run exit is 2 consecutive days, Go cross-check
  dropped, first-write prerequisites, env cap removed, R2 probe results, 4 verification GETs,
  missing watch root skipped). DESIGN.md and AGENTS.md are unchanged.
- Code: source read opens `O_NOFOLLOW` and rechecks identity; watcher handles moved-in
  directories, never treats a symlink as a directory, has a bounded inbox; secret-shaped
  launch values are redacted; damaged state is classified and counted; `run_owner` and the
  capture job are split into named steps; pure-core bans enforced by `clippy.toml`.
- Tests: 24 removed and 9 merged in the cut; durable-write ordering, mutant-killing and
  generated privacy tests added; one shared in-memory store, filesystem and clock; the store
  fake models R2's measured content ETag; coverage guards read only random cases; the schema
  gate can fail again. Two daemon state-machine failures found after that were test-model
  faults, fixed with regression tests.
- Gates on the final tree: local `PROPERTY_CASES=500`, seed 3481505894, all 13 sub-gates 0,
  431 tests, coverage 5840/5976 = 97.72 %, `verify-evidence.py` PASS; 0 failures in 160
  random-seed runs of the four daemon and commit state machines. Hosted "Rust rewrite gates"
  on `main`, run 42990315805: all three runners success (macOS 23m47s, 30 minute limit not
  raised; hosted runs use the `.#rust` Nix shell and cache the built `cargo-llvm-cov`).
- Hosted "CI" on `main` is red in the two jobs that run `make vuln`: GO-2026-6859 (Go
  standard library, fixed in go1.27.4). Operator decision 2026-11-11: no Go toolchain bump;
  replacing the Go code is the fix. Until then the `check` targets after `vuln` do not run
  in hosted CI; run `make crap bench-smoke actionlint capture-acceptance` locally for changes
  that touch Go or workflows.
- Dependabot PRs #32 (Actions pins) and #33 (Go modules) are separate from this work. On
  2026-11-11 Dependabot did not act on a rebase request, so their branches were updated from
  `main` by hand; see GitHub for their state.
- Open operator decisions: minimum startup delay before a restarted daemon's first commit
  (A13.4, 5 s suggested); whether 2 days also replaces the 7-day shadow runs of DESIGN 17
  slices 2b and 3 (A13.1 says it does).
- Next: slice 2 as in the block below (license decision, S3 adapter, zstd, run loop), with
  the A13.2 prerequisites (zstd, audit, hint retention, `tracing`) before any production
  write. Independent of it, from report 24: triage the missed mutants, the canonical-path
  type (A12.4), finish `cargo mutants` for `state.rs` and `watch.rs`, remove
  `FileSystem::stat_len` if unused.

**Session 2026-11-09, review and fix round (history).** Branch `rust/coverage-floor-80` on
stack #56 (9206235: A9, coverage floor 80 %). Baseline gate at 0f726a6, seed 730212, 2,000
cases: all 12 subgates 0, 378/378 nextest, 98.17 %. Plan, sequential Sonnet workers (medium),
one owner at a time because core type changes ripple into the shell and every test `Store`:
(1) remove/tighten tautological tests (four full audits, parent-verified) and fix the
`core_coordinates` guard weight; (2) shell: privacy sink type (no raw-bytes store body),
`SyncPolicy::None` test-only, no empty-body state writes, retry scheduling, verify
classification into core; (3) core type tightening and lows; (4) test evidence: daemon
state-machine crash/truncate/revoke steps, zero-PUT property, production mutation checks
(If-Match, advance-after-land, privacy deny), real counters. Then integrated gate. S3
adapter, zstd and the run loop remain slice-2 delivery, not part of this round.

Progress (2026-11-10): commits 9206235..e94a09b (10). 136cf0d tests; 2a18883 shell
(`Published` private, `Store::document` removed, `SyncPolicy::None`/`start_with` behind
`sim`, serialization errors returned, per-owner `not_before`, `verify::classify` in core);
6c8846e core types (derived trust, `AppendBasis`, `MissingOnce/Confirmed`, unit
`PauseHost`); a1920b3 manifest split, `Piece::from_authorized`, private Delta/Checkpoint,
fail-closed credential regex; 295a092 `Counting` wrapper, counter tests through `Daemon`,
zero-PUT property (Hegel); 9482e9f delta-only Extend reads with a resumable `Sha256State`
(59 KB append to a 200 kB source: 328,632 -> 128,632 bytes read, bound 128,632);
e2e3cd9 `scripts/check-mutations.py` (gate 06-mutations: If-Match, advance-on-fail,
credential deny, all killed); 42e8826 daemon machine steps (rewrite, truncate, remove,
revoke, crash, late write; 12-shape guard with per-shape negative controls); e94a09b the
1a oracle exempts only byte positions of unaudited mid-file rewrites (DESIGN 1a scope,
7.2), found when the first gate run at 2,000 cases failed on a model gap, not a daemon bug.
Integrated gate at e94a09b, seed 730212, 2,000 cases: all 13 subgates 0, nextest 391/391
(2 ignored benchmarks), 12 compile-fail doctests, coverage 5595/5766 = 97.03 % (floor 80),
sources unchanged across the run; `verify-evidence.py` 0. Rerun on the final source
`d8e80a6` (after the 8794e19 gate-script change), seed 730213, 2,000 cases: identical
results, 87 source files unchanged during the run; recorded in
`implementation/evidence/13-review-fix-round/` and `implementation/23-review-fix-round.md`
(which also maps DESIGN 15 and 17 progress). This supersedes gate 8 as the current
integrated result.
CI (PR #60): the first Rust run hit the macOS 30 min job limit 8 s after all 13 subgates
returned 0 (Nix setup 13.6 min). 8794e19 lets `gates.sh` share one temp target between
the two sequential mutation scripts (second script 14 s instead of a full dependency
build); the timeout was not raised. Run 42627740003 at 8794e19: all three Rust runners
success (macOS 21m45s), CI workflow 42627739897 success.
Open: `LaunchRecord` unification (P3) not done; property 3 has no writer-version seam
(`WRITER_VERSION` is compile-time) and no proptest twin; 1b "after one audit" untested
(audit evidence is always `NotDue`); idle-minute wakeups not counted (`Counters.wakeups`
stays 0); 30k reconcile 1.24 s debug, not asserted (footprint measurement); damaged
`status.json` clears parks without a logged event (DESIGN 9.2 tension); no `rename-replace`
step in either property-4 state machine; the macOS llvm-cov "unconfirmed failure" of
`hegel_daemon_state_machine` (run 42565602976, seed 938868371) was not root-caused, only not
seen again (local gates and three CI runs green since); P2 decisions still open (`cargo deny` license for
`CDLA-Permissive-2.0`, R2 `BadDigest` class, single checksum). Next: slice 2 (decide the
license, S3 adapter `rusty-s3`+`ureq`, zstd, run loop wiring `FsWatcher`/`Scheduler`,
`WalkSources`, `SharedFs` with the 60 s tick), then the slice-2 exit (a day of this Mac's
sessions captured into `rust-acceptance/`, amendment A2), after the stack is reviewed.

**Session 2026-11-08.** Operator instruction: continue from this brief
with **Sonnet** subagents (supersedes the gpt-6.1-sol worker choice; medium thinking
requested via tool argument). Reconciled at start: HEAD `57d8400`, branch
`rust/scaffold`, no commits/staging; `git diff --check` 0; `verify-evidence.py` 0;
all 45 `02-corrected/source-before.json` hashes match, so that local green result
still applies to the pre-dispatch source. Parent then added empty module stubs
`memory-core/src/{hint,locator,manifest}.rs` and declared them in `lib.rs` (parent
owns `lib.rs`, `domain.rs`, Cargo, xtask, schemas, scripts, gates). This invalidates
the 45-file snapshot for integrated-gate reuse; rerun required before acceptance.
Slice-1 assignments (disjoint; workers may not edit domain.rs/lib.rs/Cargo/xtask —
requested changes go to parent in their report):

| Worker | Owns | Scope |
|---|---|---|
| locator (Sonnet) | `src/locator.rs`, `tests/core_locator.rs`, `implementation/06-locator.md` | Pure `locate`, Roots/ownership facts input (R4), mapping matrix, property 7 |
| manifest (Sonnet) | `src/manifest.rs`, `tests/core_manifest.rs`, `implementation/07-manifest.md` | `memory.manifest/1` + `memory.current/1` delta/checkpoint types, builder/apply, R2/R3/R5, property 6 |
| hint (Sonnet) | `src/hint.rs`, `tests/core_hint.rs`, `implementation/08-hint.md` | Owned hint-line schema, ≤4 KiB builder, resync parser, tolerant hook-input DTO |

P5 remains blocked as recorded in its report; no private-source collector is authorized
by this session.

**Worker state (all completed and paths explicitly released; actual model
`claude-5.5-sonnet`, medium requested; startup acknowledgements observed):**
locator `7bb7ee9b-2324-482`, manifest `ddd8a95d-40f1-44e` (ended with a tool abort
*after* its final passing run and report; on-disk hashes match its report), hint
`a0d4fede-d13a-4f5`. Parent owns all paths again.

**Corrections (all acknowledged, uptake verified in source and tests):**
1. Hint: the event mapping did not match the Go installers. Codex SubagentStop,
   PreCompact and Interrupt were dropped, and the Pi names were guesses. Fixed: Codex
   has all six events, with a new `Interrupted` that flushes (trust.go:38). Pi maps
   only `session_start`, `agent_settled` and `session_shutdown`. Claude
   `agent_transcript_path` is a separate `agent_transcript` hint.
2. Locator: Pi session-dir non-transcript files gave UnknownKind (Go keeps them as
   Aux), and learned custom Pi roots had no layout. Parent added `SourceKind::PiAux`
   (`pi.aux`) to domain.rs. Worker added the PiAux mapping and
   `RootLayout::PiCustomRoot` (session layouts only).
3. Manifest: the worker deleted a failing negative assertion (`sources: []` on a
   delta) instead of fixing the decoder. Steered mid-run. The assertion was restored
   plus a `changes: []`-on-checkpoint case. The decoder is now strictly kind-tagged
   per-kind structs with no defaults. The failure and correction are recorded in 07.

**Parent integration:** `xtask schemas` now exports and drift-checks
`memory.manifest.1.json`, `memory.current.1.json` and `memory.hint.1.json`, alongside
range-error; its drift test covers a second file.

**Integrated gate (parent, unchanged wrapper):** `PROPERTY_CASES=2000
PROPERTY_SEED=730201 bash scripts/gates.sh` in pinned Nix: **exit 0, all 12
subgates 0**. Nextest 125/125, doctests pass, window mutation killed, schema drift 0.
Coverage **2,769/2,815 = 98.3659%** (floor 98%, unchanged). All 53 snapshotted source
files were unchanged during the run. Evidence and checksums:
`implementation/evidence/05-slice1-integrated/`. Afterwards `git diff --check` and
`verify-evidence.py` both exit 0. Not hosted CI, not native Linux.

**Parent decisions:** locator class/owner `*` proposals are accepted as the slice-1
working mapping. They agree with acceptance-map section 2, and evidence can revisit
them. Report 06's original matrix table predates correction 1; its "Parent
correction 1" section is authoritative for Pi rows. Hint `Interrupted` flushes:
accepted.

**Slice-1 exit: NOT yet accepted. Open gaps:**
- *Pi coverage narrowing:* under `sessions/` with no cwd dir, a multi-part path whose
  first part is not a session dir gives UnknownKind. Go captures it as a sessionless
  Aux. It is counted, not silent, but it narrows coverage, so Shared PiAux or a
  recorded exclusion must be decided before acceptance.
- *F2 "Go fix → Rust counterpart" table:* map 01 lists targets. Each row still needs
  an implemented test pointer, or "not applicable/later slice" with a reason.
- *Coverage guard (DESIGN 15):* no generator counters exist for multi-epoch, restart,
  crash-after-manifest, lost-reply or delete-index. Most belong to property 4
  (slice 2). The guard's slice-1 subset must be pinned and implemented.
- *Property 5:* only the hint-line and transform levels exist. The PUT-body sink is
  slice 2.
- *P5:* blocked (above). P1 synthetic only.

**Round 2 (all workers completed and released; Sonnet; acknowledgements observed):**
- Locator: original worker `7bb7ee9b` session expired (resume unavailable, paths
  were released). Replacement `3c1e04ed-2817-436` did correction 2 (Pi `sessions/`
  non-session-dir multi-part → Shared `PiAux`; single top-level unknown stays
  UnknownKind) and correction 3. **Parent retracted its own correction-1 premise**:
  Go `Pi.Classify` (pi.go:158-168) promotes custom-root paths to agent-dir rules for
  `sessions/…`, piRoles names/dirs and single-part `*.log`; only the rest become
  Shared PiAux. Implemented by reusing `pi_asset`; recognised config identities keep
  config kinds (privacy-relevant). 06 matrix rewritten as authoritative.
  Pi narrowing gap: **closed**.
- Acceptance worker `69bd8a8f-13e5-4d2`: coverage guard for property 1 (multi-epoch,
  window rewrite, interior audit, truncate-regrow, identity replacement) and property
  6 (multi-epoch, snapshot replace, delete, fold across checkpoint), both engines,
  with negative controls. Parent verified the plain-`#[test]` wrappers still honour
  `HEGEL_TEST_CASES/SEED` (hegeltest runner.rs:264-305) and `PROPTEST_CASES/RNG_SEED`
  (proptest config.rs:26,40); mutation gate still kills. F2 table
  `09-f2-counterparts.md` delivered **incomplete**: 14 rows "name only" (unverified
  assertions), 7 partial-S1; worker hit its turn budget.

**Integrated gate 2 (parent, unchanged wrapper):** seed 730202, 2,000 cases: **exit 0,
all 12 subgates 0**; nextest 127/127; coverage **2,786/2,832 = 98.3757%**; 53
snapshotted files unchanged during run. Evidence:
`implementation/evidence/06-slice1-guard/`. `git diff --check` and
`verify-evidence.py` 0. Supersedes gate 1 for current source.

**Remaining slice-1 gaps:** (1) verify or downgrade the 14 "name only" F2 rows;
(2) pure S1 counterparts requested by 09: owner-key UTF-8 byte limit
(1,024 ok / 1,025 `KeyTooLong`), distinct-source `SourceLimit`, `request_budget`
`max(60s, 30s+size/1MiB/s)`, disposition transition (cancel does not advance
streak), `/var`↔`/private/var` coordinate alias; (3) P5 blocked; property 4 guard
cases (restart, crash-after-manifest, lost-reply, delete-index) are slice 2 by design.

**Round 3 (all completed and released; Sonnet; acknowledgements observed):**
- Parent added stubs `src/{limits,disposition,coordinates}.rs` to `lib.rs`.
- Limits/disposition `347e7230-1a84-47f`: `current_key` (1,024 ok / 1,025
  `KeyTooLong`), distinct-source `SourceSet::admit` (`SourceLimit`, no eviction),
  `request_budget`, pure `transition` (cancel keeps the streak and never parks;
  timeout is `Retry{Offline}`; auth is `PauseHost`; park after 3 failures over at
  least 3 h). Report 10.
- Coordinates `d0c381aa-ceca-46d`: pure alias module. An alias is accepted only with
  a shell-supplied identity proof or a declared pair, no case folding, and every
  spelling is kept. Not yet wired into the locator or privacy modules. Report 11.
- F2 verifier `a7d1b59e-9c85-424`: read every cited assertion and removed all "name
  only" markers. 1a, 1b and 7c were over-claimed and are now partial-S1; 7a was
  under-claimed and is now implemented. 21/21 subcases have one row.
- **Parent correction (DESIGN 11.2):** the worker left `NonJsonResponse`/`Schema`
  retrying forever. The parent added `ParkReason::{RepeatedNonJsonResponse,
  RepeatedSchemaError}` and made `park_reason` total. The test that asserted "never
  parks" was deliberately replaced, and the change is recorded in report 10. The parent
  also filled F2 rows 2, 3, 4a, 6a and 7d as implemented-S1 after reading the tests.

**Integrated gate 3 (parent, unchanged wrapper):** seed 730203, 2,000 cases: **exit 0,
all 12 subgates 0**; nextest 167/167; coverage **3,104/3,151 = 98.5084%**; 58
snapshotted files unchanged during the run. Evidence:
`implementation/evidence/07-slice1-round3/`. `git diff --check` and
`verify-evidence.py` both exit 0. This supersedes gates 1–2 for the current source.

**Slice-1 exit: still NOT accepted.** The F2 table is complete, with a status on
every row, but 7 rows are partial-S1 with pure test gaps: 1a (backup-alias
spellings of each protected basename per agent), 1b (8-env bound and env value
grammar), 5a (db-only change with the WAL unchanged), 5b (types only), 7c (Codex
archive move or recompression keeps the rollout id; locator rows may already cover
this and need re-verifying), 10b, and 12 (long rewrite histories). P5 is still
blocked. The property-4 guard cases are in slice 2 by design.

**Round 4 (completed and released; Sonnet; acknowledgement observed):** worker
`3f1ad91f-c830-4e4` added `tests/core_f2_gaps.rs` (12 tests, no production change)
closing rows 1a, 1b, 5a, 5b (S1 part), 7c, 10b and 12. Parent read the 1a and 12
tests (literal-count expectations; 300-manifest history against a test-written
model), then normalised 09 so each row leads with its current status, keeping the
earlier verdict as history. 09 now has 21/21 rows with no partial-S1 status.

**Integrated gate 4 (parent, unchanged wrapper):** seed 730204, 2,000 cases: **exit 0,
all 12 subgates 0**; nextest 179/179; coverage **3,105/3,151 = 98.5401%** (floor 98%
unchanged); 59 snapshotted files unchanged during the run. Evidence:
`implementation/evidence/08-slice1-round4/`. `git diff --check` and
`verify-evidence.py` both exit 0. Supersedes gates 1–3 for the current source.

**Slice-1 decision (parent, 2026-11-08):** the DESIGN section 17 slice-1 *exit*
criteria are **met on the current source**: properties 1, 2, 3, 5 (hint and
transform level), 6 and 7 green at 2,000 cases on both engines, the proptest
cross-check green, the window mutation killed, the coverage guard implemented
with negative controls, and the F2 table complete. **Slice 1 is NOT fully
accepted**, because prerequisite **P5** ("before slice 1") is still blocked: it
needs operator authorization to collect historical offset and prefix-hash metadata
from this Mac's sources (no content read). This is an operator decision, not
something tools can resolve within the current remit. Other known limits: the
coordinates module is not yet wired into the locator or privacy modules (slice-2
shell wiring); property 5 at the PUT sink and the property-4 guard cases are
slice 2; not hosted CI, not native Linux.

**External blockers (operator):** (1) P5 metadata-collection authorization;
(2) the acceptance bucket and scoped token for P2/P2b (https://dash.cloudflare.com/).
Natural-sleep P6 needs elapsed real time.

**Next action:** once P5 is authorized, run a metadata-only collector (sizes,
offsets and prefix hashes; no content persisted) and record the result in
`prototypes/P5-prefix-rewrite-frequency.md`. Independent of the blockers, slice-2
work that needs no bucket can start: the `memory hook` binary (hand-parsed argv,
no tokio, uses `hint::build_line`) with actual Rust P4 timing and RSS, and the
fake-store property-4 state machine. Commit and stack the task-owned changes
(`gh stack`) before the hosted-CI run.

**Slice-2 groundwork (parent, 2026-11-08):** `crates/memory` now depends on
`memory-core` and has hegel/proptest/proptest-state-machine dev-dependencies (lock
diff: only those `memory` edges; `cargo check --locked` and `cargo deny check bans`
pass). Stubs `src/{hook,commit}.rs` were declared in `lib.rs`.

**Round 5 (active; Sonnet, medium thinking; acknowledgements observed):**
- Hook `5f446c9a-781b-4dc`: owns `crates/memory/src/{main,hook}.rs`,
  `tests/hook.rs`, `implementation/12-hook.md`. `memory hook <agent>` per
  DESIGN 10.1, plus a synthetic P4 measurement.
- Commit protocol `79a65718-6c22-472`: owns `crates/memory/src/{environment,commit}.rs`,
  `tests/{harness,commit_protocol}.rs`, `implementation/13-commit-protocol.md`.
  Typed store seam, 7.4 driver, fake store, property 4 on both engines against a
  fake store (not the daemon).

**Round 5 result (both completed and released; the parent reviewed the code):**
- Hook: `memory hook <agent>` writes with one `write(2)` call on an O_APPEND file
  created 0600, into a hints directory created 0700. It refuses a group- or
  world-writable directory or a symlink, has a 2 s stdin deadline, always exits 0, and
  prints nothing to stdout. 26 tests, with Hegel and proptest over arbitrary stdin.
  **P4 (synthetic, Apple M2, release build):** in-process p50 30.5 µs and p99 75.7 µs,
  against the 1 ms target. Max RSS is about 2.5 MiB. Spawn p99 is 4.15 ms, against
  1.33 ms for `/usr/bin/true`. The parent rechecked fmt and tests (26/26).
- Commit protocol: typed `Store` seam (`put_if_absent`/`put_if_match`/`get`, closed
  `StoreError`). The 7.4 driver adopts the stored descriptor on a 412 (R2), checks the
  invariant-6 fence before every step 3, verifies with up to 4 GETs, rebases up to 4
  times, and does not treat a 404 under a fence as final. `recover_base` covers 9.3.
  Property 4 runs against a fake store on both engines, with an independent model, a
  coverage guard with negative controls, and an oracle tamper control in 8 cases.
  46 tests. The capture step is a test stand-in, not the real planner or shell.
- **Parent correction (DESIGN 11.2):** added `StoreError::Throttled` (429/503 →
  `Throttled`). Any other 5xx (`Server`) is now `Offline`. 4 expectations changed on
  purpose and 1 table row was added (report 13).
- **Parent correction (coverage harness):** `tests/hook.rs` passes `LLVM_PROFILE_FILE`
  through `env_clear()` spawns (report 12). Attempt 1 failed coverage at 96.9923% and is
  kept in `evidence/09-slice2-round5/attempt1-coverage-fail/`.

**Integrated gate 5 (parent, unchanged wrapper, floor 98%):** seed 730206, 2,000
cases: **exit 0, all 12 subgates 0**. Nextest 251/251, with 2 skipped (ignored P4
measurement tests). Coverage **3,572/3,624 = 98.5651%**. The 63 snapshotted files did
not change during the run. Evidence is in `implementation/evidence/09-slice2-round5/`.
`git diff --check` and `verify-evidence.py` both exit 0. This supersedes gate 4 for
the current source.

**Open decisions from round 5 (none is an operator blocker):**
1. Hook launch argv: Go walks the process tree and Pi passes `--argv`. The Rust hook
   records env only. The planned approach is a bounded `kern.procargs2` or
   `/proc` read of the direct parent, timed against P4. It must be decided before the
   cutover.
2. The strict hint-directory owner check belongs in the daemon (DESIGN 9.1), which
   does not exist yet.
3. `BaseUnchanged` exhaustion maps to `Retry`/`Throttled`, because `ErrorClass` has
   no closer class.
4. The state root (`$HOME/.local/state/memory`) is an assumption until the install
   layout is fixed.

**Next action:** slice-2 work that needs no bucket: the daemon skeleton (lock,
state layout and the 9.1 owner check, an index cache that may be deleted), and wiring
the real planner and locator into the commit driver so the fake-store property 4
uses actual capture. P2/P2b still need the acceptance bucket and token, and P5 still
needs operator authorization.

**Round 6 groundwork (parent, 2026-11-09):** stubs `src/{state,config,capture}.rs`
declared in `lib.rs`; `crates/memory` gained serde, serde_json, sha2 and toml workspace
dependencies (lock diff: only those four `memory` edges; `--locked` check and `deny bans`
pass).

**Round 6 (active; Sonnet, medium thinking; acknowledgements observed):**
- Daemon state `6583b582-2d54-43d`: owns `crates/memory/src/{state,config}.rs`,
  `tests/state.rs`, `implementation/14-daemon-state.md`. Config (deny_unknown_fields),
  state dir modes, daemon lock, 9.2 durable envelope, machine id O_EXCL, hint-dir vetting.
- Capture wiring `1d480dd6-9667-498`: owns `crates/memory/src/{capture,environment}.rs`
  (Store semantics frozen), `tests/{harness,capture}.rs`,
  `implementation/15-capture-wiring.md`. Real planner/privacy capture step into the
  commit driver; property 4 and property 5 at the store sink against a fake store.

**Round 6 results (parent-verified 2026-11-09):**
- Daemon state `6583b582-2d54-43d` (completed, released): config with
  deny_unknown_fields and a metadata-only token-mode check; `DaemonLock` via
  `File::try_lock` (flock; `LockHeld` exit 75, not yet wired into `main.rs`); 9.2 envelope
  (temp file, `sync_all`/F_FULLFSYNC, rename, dir fsync; every failure reads as
  `Missing(reason)`); `prepare` needs the lock before sweeping `tmp/`; machine id
  create-once via `hard_link` (never overwrites); hint-dir owner check against a probe
  file's uid. Parent reread lock/durable/machine-id code; fmt 0; `tests/state` 22/22.
  Open: std facts were verified against 1.98.0 rust-src (the pinned 1.99 has none); some
  I/O failure branches are untested.
- Capture wiring `1d480dd6-9667-498`: the first run aborted after a parent steer (48 min
  with no code); resumed, it finished steps 1-2 only and is released. `capture()` (observe,
  admit, plan, NeedWindows re-plan, read, transform, authorize, slice, `Commit<Published>`);
  `Published` can only be built from `privacy::AuthorizedBody`; `FileSystem::stat` added;
  `Store` unchanged. 8 table tests, including a protected-config canary absent from the
  store with a positive control. Parent check: hashes match report 15; fmt 0; workspace
  clippy 0; `cargo test -p memory` at 2,000 cases exit 0.
  **Remaining:** property 4 with the real capture step on both engines (guard and
  negative control), argv/env canary at the store sink, a NeedWindows assertion, and the
  coverage of the NeedAuditRecheck/Blocked/Raced/Unavailable branches.

**Round 7 (active; Sonnet, medium thinking; acknowledgements observed):**
- Capture property 4 `22becdd8-ff3c-4dc`: owns new `tests/capture_property.rs` and
  `implementation/16-capture-property4.md`. Property 4 on both engines driven by the real
  `capture()` step, with an independent model, guard plus negative control, tamper control.
- Capture sink `fd23e3c5-788b-475`: owns `src/capture.rs`, `tests/capture.rs` and a
  Round 7 section of report 15. Public capture API is frozen (additions only). Launch
  records into the commit, argv/env canaries at the store sink with a positive control,
  NeedWindows assertions, and the Blocked/Raced/Unavailable/NeedAuditRecheck branches.

**Round 7 result (both completed and released; parent-verified 2026-11-09):**
- Capture property 4 `22becdd8-ff3c-4dc`: `tests/capture_property.rs` drives property 4
  through the real `capture()` then `commit()` against a fake store, on both engines. It has
  an independent reference, a store-reading oracle (9 tamper cases plus a positive control),
  and a guard with negative controls. Its scope is 2 append sources, 4-byte pieces, identity
  encoding and a synthetic filesystem. Liveness is waived once a foreign writer has parked
  the owner.
- Capture sink `fd23e3c5-788b-475`: `capture::attach_launches` (only `SanitizedLaunch`
  becomes a `LaunchRecord`; digests de-duplicated). Argv, env and prompt canaries never
  reach any stored object, with a transcript positive control. NeedWindows matching and
  mismatching reads go through a recording seam. The failure branches have literal outcomes
  and store nothing. `NeedAuditRecheck` is unreachable while audit evidence is always
  `NotDue`, which is a known coverage gap. Nothing derives launches from hint scanning yet.
- **Parent correction 1 (stale append index):** the property-4 worker found that after a
  lost reply whose commit landed, `capture()` planned from a stale index (`Invalid(Overlap)`)
  and had worked around it in the driver. The parent fixed `capture::append` so it uses the
  index only when its position equals the remote's committed position (DESIGN 9.1, 9.3), and
  removed the driver workaround. A reverted-fix mutant fails both engines (exit 101).
- **Parent correction 2 (stale deletion flag):** gate 6 attempt 1 (seed 730207) failed
  proptest property 4 ("removed source not recorded deleted") after a lost reply. The remote
  entry's `deleted_at` is now authoritative, and the local flag counts only when the remote
  has no entry. Seeds 730207, 730208 and 730209 pass. Attempt 1 is kept in
  `evidence/10-slice2-round7/attempt1-stale-deletion-flag/`.

**Integrated gate 6 (parent, unchanged wrapper, floor 98%):** seed 730207, 2,000 cases:
**exit 0, all 12 subgates 0**. Nextest 291/291 with 2 skipped (P4 measurements). Coverage
**4,361/4,444 = 98.1323%**, which is close to the floor, so new code must bring its own
coverage. The 69 snapshotted files did not change during the run. Evidence is in
`implementation/evidence/10-slice2-round7/`. `git diff --check` and `verify-evidence.py`
both exit 0. This supersedes gate 5.

**Slice-2 status: NOT accepted.** Done against fakes: hook, commit protocol, daemon state,
capture wiring, property 4 with real capture, and property 5 at the store sink. Remaining:
the daemon loop (lock wiring in `main.rs`, the 9.3 index rebuild, notify watcher, coalescer,
15-min reconcile, 9.1 hint-dir owner check at start), hint-scan-to-launch wiring, the real
filesystem adapter, zstd, and the P2/P2b R2 client. Open decisions 1-4 from round 5 still
stand.

**Next action:** a daemon-skeleton round (no bucket): `memory daemon` takes the lock first,
prepares state, vets hints, rebuilds the index from a `Store` and runs reconcile over a real
`FileSystem` adapter against the fake store, with tests that bring their own coverage.
External blockers are unchanged: P5 authorization, and the P2/P2b bucket and token.

**Round 8 groundwork (parent):** stubs `src/{realfs,daemon}.rs` declared in `lib.rs`;
`cargo check --workspace --all-targets --locked` passes.

**Round 8 (active; Sonnet, medium thinking; acknowledgements observed):**
- Real filesystem `c0ac3590-5c38-4ed`: owns `src/realfs.rs`, `tests/realfs.rs`,
  `implementation/17-realfs.md`. `RealFs: FileSystem` (trait frozen) and a bounded,
  deterministic, non-following `walk` through `locate`. Synthetic temp dirs only.
- Daemon skeleton `de6bf429-d347-4e9`: owns `src/daemon.rs`, `src/main.rs` (daemon
  dispatch only), `tests/daemon.rs`, `implementation/18-daemon-skeleton.md`. Lock-first
  startup, index rebuild, `reconcile_once` generic over `FileSystem`/`Store`, hint-scan
  launch attachment, durable index/status. Does not depend on realfs; the binary never
  simulates a successful capture.

**Round 8 results (parent-verified 2026-11-09):**
- Real filesystem `c0ac3590-5c38-4ed` (completed, released): `RealFs` (symlink_metadata,
  NotFound gives `Ok(None)`, `FileId` = dev+ino+birth or `Birth::Unavailable`, `read` bounded
  by `take(len)`) and a deterministic, non-following `walk` (depth 32, 500k entries) with
  closed counts. Parent check: hashes match report 17; fmt 0; `tests/realfs` 11/11 at 2,000.
- Daemon skeleton `de6bf429-d347-4e9` (released, **partial**): lock-first startup, rebuild
  via `recover_base`, `reconcile_once`, durable index/status, hint scan. `memory daemon` runs
  startup then exits 78 (lock held 75, config 2, other 1). 5 tests.
- **Incident and parent correction:** the daemon change made `tests/harness.rs` spawn
  `memory daemon` against the host's real `~/.local/state/memory`. It created an empty lock
  file and two empty dirs there (no config, no data, no capture). The parent removed exactly
  those, isolated the test in a temp state root (still asserting 78) and recorded the rule in
  report 18. `cargo test -p memory` at 2,000 cases now exits 0.

**Round 9 (all completed and released; Sonnet; acknowledgements observed):**
- Daemon tests `77934662-df7c-468`: lost reply, foreign-writer parking, hint launch
  attached once with the offset advanced only after landing, PauseHost, clock error,
  damaged index/status, and Hegel and proptest daemon state machines with a guard and a
  negative control. The parent killed a 20+ min run and steered the worker.
- **Parent correction (daemon.rs, status write frequency):** profiling showed most of the
  time under `write_durable`/`F_FULLFSYNC`. Status is now written at once when
  `host_paused` or the parked list changes (and on the first round), otherwise at most
  every 60 s, and always at shutdown. Durability is unchanged and no fsync-skipping seam
  was added (report 18, correction 2).
- Gate 7 attempt 1 (seed 730208) failed only coverage, at 97.6945%. It is kept in
  `evidence/11-slice2-round8/attempt1-coverage-fail/`. Two test-only workers added tests
  with literal expectations, and no `src/` change was made: coverage A `e23906e9-caf1-4c3`
  (`tests/{capture,realfs}.rs`) and coverage B `9d218d67-e8fe-4d7` (`tests/daemon.rs`, 16
  tests). They list the remaining unreachable lines with requested changes in reports 15,
  17 and 18.

**Integrated gate 7 (parent, unchanged wrapper, floor 98%):** seed 730209, 2,000 cases:
**exit 0, all 12 subgates 0**. Nextest 341/341 with 2 skipped (P4 measurements), 1,026 s.
Coverage **4,905/4,988 = 98.3360%**. The 73 snapshotted files did not change during the
run. Evidence is in `implementation/evidence/11-slice2-round8/`. `git diff --check` and
`verify-evidence.py` both exit 0, and `~/.local/state/memory` is absent. This supersedes
gate 6.

**Open items from rounds 8-9 (none is an operator blocker):**
1. The daemon properties take about 6 min at 2,000 cases (real F_FULLFSYNC per round).
   Options: smaller clock steps, or a simulation-only durability seam in `state.rs`.
2. At 50 cases, the property coverage guards (commit_protocol, capture_property, daemon)
   fail because the required shapes do not arise. They pass at the gate's 2,000 cases.
   Ad-hoc `llvm-cov` runs must use 2,000 cases or `--ignore-run-fail`.
3. Lines that cannot be reached without a seam: capture usize conversions,
   authorize/admit re-check arms and planner arms; realfs `read_dir`/`file_type` faults;
   daemon:218/404/542; state I/O fault arms. A small dead-code cleanup pass could remove
   them.
4. Round-5 open decisions 1-4 (hook argv, daemon owner check (now implemented in
   state.rs), BaseUnchanged class, state root) still stand. Item 2 is resolved by
   `StateDir::vet_hints_dir`.

**Slice-2 status: NOT accepted.** Done against fakes and synthetic dirs: hook, commit
protocol, daemon state, capture wiring, property 4 with real capture, property 5 at the
store sink, real filesystem adapter and walk, daemon skeleton (startup and
`reconcile_once`). Remaining: the notify watcher and coalescer timing, wiring
`RealFs`/`walk` into `memory daemon` (the binary still exits 78 after startup), zstd, and
the P2/P2b R2 client.

**Next action:** a watcher/coalescer round plus wiring `RealFs` and `walk` into the daemon
behind the fake store. Then commit the task-owned changes as a `gh stack` and run hosted
CI. External blockers are unchanged: P5 authorization and the P2/P2b bucket and token
(https://dash.cloudflare.com/).

**Round 10 groundwork (parent):** stubs `src/{watch,resolve}.rs` declared in `lib.rs`;
`cargo check --workspace --all-targets --locked` passes.

**Round 10 (active; Sonnet, medium thinking; acknowledgements observed):**
- Watcher `01e18367-66dc-424`: owns `src/watch.rs`, `tests/watch.rs`,
  `implementation/19-watch.md`. Pure `Scheduler` over memory-core `Coalescer` plus a thin
  notify `FsWatcher`; temp dirs only.
- Resolver `07de85fd-6dde-426`: owns `src/resolve.rs`, `tests/resolve.rs`,
  `implementation/20-resolve.md`. Symlink-chain facts, `to_source`, a `Sources`
  implementation over `realfs::walk`, and an end-to-end synthetic-tree test into a fake
  store.

**Round 10 results (both completed and released):**
- Watcher: `Scheduler` (pure, over memory-core `Coalescer`) plus notify `FsWatcher`; 16
  tests including Hegel and proptest against an independent model; `watch.rs` 107/108.
  Not wired into the daemon loop (report 19).
- Resolver: `resolve_facts`, `to_source`, `SharedFs`, `WalkSources` implementing
  `daemon::Sources`; 10 tests plus an end-to-end capture/commit into a fake store. The parent
  added 2 tests (shared owner/profiles, unknown-owner count). `resolve.rs` 188/193 (report
  20). Open: `walk` skips symlinks, so symlinked sources never reach `WalkSources`.

**Integrated gate 8 (parent, unchanged wrapper, floor 98%):** seed 730210, 2,000 cases:
**exit 0, all 12 subgates 0**. Nextest 369/369 with 2 skipped, 1,116 s. Coverage
**5,200/5,289 = 98.3173%**. The 77 snapshotted files did not change during the run. Evidence
is in `implementation/evidence/12-slice2-round10/`. `~/.local/state/memory` is absent.
This supersedes gate 7.

**PAUSED (operator request 2026-11-09: "pause at completion of the current slice").**
Slice 2 cannot complete without P2/P2b (bucket and token), so the pause point is round 10
gated. Slice 2 is NOT accepted. Remaining slice-2 work: wire `FsWatcher`/`Scheduler`,
`WalkSources` and `SharedFs` into `memory daemon` (still exits 78 after startup), with the
60 s tick and `ReadDir`/`ReconcileAll` execution; decide whether walk should report symlinks;
zstd; the P2 R2 client; the hook argv decision. Nothing is committed. On resume, commit
the task-owned changes as a `gh stack` and run hosted CI first.

**Operator decisions (2026-11-09, answering the pause questions):**
1. **P5 waived.** Slice 1 is not held on P5. Prefix-rewrite frequency is measured later from
   the daemon's own replay counters (`Replay{WindowMismatch}`, `Replay{AdoptMismatch}`).
2. **Bucket.** No separate acceptance bucket. Acceptance (P2/P2b, slices 2/2b/3) uses the
   existing `agent-sessions` bucket with the `.env` credentials, and the bucket is cleaned
   before rollout. This replaces DESIGN 6.1's separate acceptance bucket and fresh
   production bucket.
3. **Hook launch argv dropped.** The Rust hook records env facts only; no process-tree or
   `kern.procargs2` read.
4. **Symlinks are skipped** by the walk (counted, never captured). DESIGN 7.2's "resolve
   every link" is to be corrected; the resolver's link handling stays as defence for paths
   reached directly.
5. **State root stays** `~/.local/state/memory` on both hosts.
6. **`BaseUnchanged` exhaustion:** researched and measured as P11; decided as amendment A6
   (new `Transient` class, full jitter, `known_ours`).
7. **Slow daemon properties:** researched and measured as P12; decided as amendment A7
   (production unchanged; test-only `SyncPolicy::None` plus a crash-point property).
8. After research: finalise direction, align every project doc with the actual state, then
   hand over to a new session.

**Operations performed 2026-11-09 (operator-confirmed twice: "stop the current process,
cleanup R2 and use that"):**
- Stopped and disabled on this Mac: `agent-sessions-capture-experimental` (Go
  `capture-v1 run`) and `com.agent-sessions.publication-refresh` (`launchctl bootout` +
  `disable`, exit 0). `com.agent-sessions.publication-dashboard` left running: it reads
  local DuckDB only. Plists are untouched; re-enable with `launchctl enable` + `bootstrap`.
- Stopped and disabled on BC-01: user unit `agent-sessions-capture-experimental.service`
  (`systemctl --user stop` + `disable`, exit 0; now inactive/disabled).
- Still installed: Go capture hooks in Claude (8), Codex (6) and the Pi extension. They
  write local hints/launch evidence into the Go spool only and never touch R2; with the
  daemon stopped the spool grows (cap `max_spool_bytes` 8 GiB). They are removed at
  cutover (DESIGN 18).
- R2 inventory before deletion (ListObjectsV2, full): 435,620 objects, 15,157,294,405
  bytes, oldest 2026-10-16, newest 2026-11-09T00:56Z; prefixes `capture-v1/` 353,722,
  `v3/` 81,896, `delivery-artifacts/` 2; 0 multipart uploads. Inventory JSONL sha256
  `4fec8738f28949c34b62beda23a1d456fb4ddcc29f85a9547f60fc2c2bcace09`.
- Deleted `capture-v1/` (353,722, 1,384 s) and `v3/` (81,896, 247 s), DeleteObjects
  batches of 1,000, zero per-key errors.
- Kept: 2 `delivery-artifacts/<commit>/…/bundle.tar.gz` deployment bundles (13,939,328
  bytes; one is the dashboard's runtime commit). They are not capture data; the contract
  section 6 step 6 excludes unrelated objects. After-inventory: 2 objects, sha256
  `b0571d6307c5bdb7f96cca1db47f344eb9286ab578c523f6d07e93d28f4ab812`.
- Key lists are not in the repo (keys encode source paths); they were kept 0600 in the
  session scratchpad, which is ephemeral. The deleted data is not recoverable from R2;
  local sources on both hosts are untouched.

**Handover 2026-11-09 (research done, direction final, docs aligned; nothing committed).**
- Research (Sonnet, medium; acknowledgements observed; both released):
  - P11 commit retry `6fa43f0a-129e-47b`: real R2 probe (889 requests under
    `rust-acceptance/p9-<ts>/`, prefix deleted and verified empty) and a seeded simulation.
    Parent reproduced the simulation exactly, confirmed R2 holds only the 2
    `delivery-artifacts/` objects, found no credential/endpoint/account id in the files, and
    ran the untested hybrid (rejected). `prototypes/P11-commit-retry.md`.
  - P12 durable writes `4fb98828-4d5a-4e0`: APFS microbenchmark and end-to-end daemon
    property runs on patched copies (421 s baseline, 14 s with sync skipped, all 30 pass).
    Parent checked logs, SDK barrier semantics and that `rust/` is unchanged.
    `prototypes/P12-durable-state-writes.md`.
  - Both were renamed from P9/P10 because DESIGN 16 already uses those ids (amendment A8).
- Direction: `docs/rust-rewrite/design-amendments-20261109.md` A0–A8. DESIGN.md, AGENTS.md
  and the contract are hash-pinned and not edited.
- Docs aligned: README, docs/README, security, gap register (revision recorded in
  `handoff-documentation-identities.json`), rust/README, quality-gates, rust-rewrite README,
  P5, 00-scope, reports 12, 18, 20. `verify-evidence.py` 0, `git diff --check` 0.
- Source: `rust/` unchanged since gate 8 (all 77 snapshot hashes match), so gate 8 remains
  the current integrated result. `~/.local/state/memory` absent.
- Host state: Go capture daemons (Mac, BC-01) and Mac refresh job stopped and disabled; Go
  hooks still installed and filling the local Go spool (4.8 GiB of an 8 GiB cap on the Mac;
  at the cap the Go hook should refuse further spool writes, unverified). BC-01 is reachable
  only when the Mac SSH agent has a key loaded (`ssh-add -l` was empty at handover).
- Gaps found and not yet fixed: `disposition::backoff` has no jitter although DESIGN 11.2
  requires it; a late `current.json` write from an earlier failed capture is misread as
  `Foreign` (A6 fix).

**Next actions for the new session (in order):**
1. Commit the task-owned changes on `rust/scaffold` as a `gh stack` and run hosted CI
   (`gh run watch <id> --interval 60 --exit-status`). Base CI run 41988501354 had a failing
   Go fuzz job, so it is not a green baseline.
2. Implement A7: `SyncPolicy` in `state.rs`, `None` in the daemon property `Sut`, a
   default-is-`Full` test, and the crash-point property. Rerun the daemon tests at 2,000.
3. Implement A6: `ErrorClass::Transient`, full-jitter backoff for `Offline`/`Transient`,
   `Throttled` honouring `Retry-After`, `known_ours` in `verify`, with a test that drives the
   late-write interleaving.
4. P2: choose the S3 client against the `agent-sessions` bucket under `rust-acceptance/`
   (amendment A2), then zstd, then wire `FsWatcher`/`Scheduler`, `WalkSources` and
   `SharedFs` into `memory daemon` with the 60 s tick.
5. Integrated gate (new evidence dir, next seed 730211), then slice-2 exit: a day of this
   Mac's sessions captured. Remove the Go hooks only at cutover (DESIGN 18 step 2).
6. Unmeasured: P12 on BC-01 (Linux fsync cost), natural-sleep P6.

**Progress 2026-11-09, continued session (Sonnet workers, medium thinking; paused at the
operator's request after A6).** This supersedes "nothing committed" above and in the
Checkout bullet below.
- Stack #56, all draft PRs: main ← `rust/scaffold` #54 (a6779d8 docs, b116965 CRAP fix) ←
  `rust/workspace` #55 (d8fbc4f) ← `rust/sync-policy` #57 (3e29ebf, A7) ←
  `rust/p2-s3-client` #58 (a6203c2, P2) ← `rust/transient-known-ours` #59 (c1ab3ec, A6).
  `rust/.gitignore` now ignores `*.profraw`, so 48 coverage leftovers in `rust/crates/memory/`
  stay untracked.
- Step 1 is done. The first Go CI run failed `make crap`: the P11 probe `main` is a separate
  module that `go test ./...` never covers, so it scored 0%. b116965 excludes
  `docs/rust-rewrite/prototypes/` from the scan, which removes only that function. Hosted CI is
  green on every branch. CI: 42557988848, 42559422321, 42559429114, 42559633115, 42563074511.
  Rust gates: 42559422228, 42559428769, 42559633049, 42563074454.
- Step 2 (A7) is done. Worker `778bac8e-8569-48e` completed and released its paths. The parent
  reran fmt and clippy, and the daemon (30) and state (25) tests at 2,000 cases with seed
  730211. Report: `implementation/21-sync-policy.md`.
- Step 3 (A6) is done. Worker `81505ea7-f8fb-4be` aborted before handing over, and the parent
  took over its paths. The parent then:
  - reviewed the diff;
  - clamped `Retry-After` to [1 s, 15 min], because 0 had given a hot retry;
  - reran fmt, clippy, doctests, schemas and lints;
  - ran nextest (378 passed) and the A6 suites at 2,000 cases with seed 730211.
  Report: `implementation/22-transient-known-ours.md`.
- Step 4, first part (P2 research) is done. Worker `07d193e2-bf21-4b6` completed and released
  its paths. It recommends `rusty-s3` 0.11.0 with `ureq` 3.5.1: neither retries, so
  `current.json` writes are never resent. On cleanup, prefix
  `rust-acceptance/p2-20261109T033619Z/` was deleted, and the parent's own prefixed LIST of
  `rust-acceptance/` returned 0 keys. The `.env` value scan of its files found 0 hits.
  Report: `prototypes/P2-s3-client.md`.
- Decisions still open from P2:
  - Both clients fail `cargo deny` licenses on `CDLA-Permissive-2.0` (`webpki-roots`), so
    allowing it in `rust/deny.toml` needs a decision.
  - Map R2 400 `BadDigest` to a non-retryable class.
  - Send only one checksum: R2 rejects MD5 and SHA-256 together.
- Gaps:
  - Flake predating A6: `core_coordinates::hegel_coordinates_order_proof_and_retention` fails
    its coverage guard ("never produced proof-only") on seeds 6 and 13 of 1 to 30 at 500
    cases. Fix it by raising that generator weight, never by lowering the guard. (Resolved
    136cf0d by constructing a proof-only case; guard unchanged, seeds 1 to 30 pass.)
  - The daemon Hegel guard flake that the A7 worker saw did not reproduce at 500 cases for
    seeds 1 to 20.
  - Rust gates run 42565602976 (58c4347, docs-only change): `08-coverage` failed on macos-26
    only. Under llvm-cov, `hegel_daemon_state_machine` with seed 938868371 reported an
    "unconfirmed failure: failed 0 of 10 replays". Locally the same seed passed 5 of 5 at 500
    cases, and the same Rust source passed every gate at c1ab3ec. It needs a look at what can
    fail nondeterministically under instrumentation (timing or ordering in the daemon
    simulation). Do not retry it away.
  - `Transient` is not counted apart from `Throttled` in status, because there are no
    per-class counters yet.
  - The daemon computes the retry delay but does not schedule on it. (Resolved 2a18883:
    per-owner `not_before`, per-class failure counts.)
  - There is no daemon-level late-write state-machine step. (Resolved 42e8826.)
- Next action on resume:
  1. Fix the `core_coordinates` guard weight.
  2. Step 4, rest: decide the deny license, add the `rusty-s3`/`ureq` `Store` adapter (sketch in
     P2), add zstd, then wire `FsWatcher`/`Scheduler`, `WalkSources` and `SharedFs` into
     `memory daemon` with the 60 s tick.
  3. Step 5.

- Checkout: `/Users/mm/dev/memory`, initial branch `main`, base
  `57d8400f632ab47a81e34d060f86d3687123153e`; parent created stack
  `main ← rust/scaffold` with `gh stack init --base main rust/scaffold`.
  Current branch is `rust/scaffold`; HEAD is still that base. All rewrite changes
  remain **uncommitted**, nothing staged or pushed, and no Rust PR/hosted CI was
  created. At initial inspection open PRs were dependency updates #32/#33; that list
  was not refreshed during documentation handoff. Base CI run
  https://github.com/mich-murphy/memory/actions/runs/41988501354 failed its Go fuzz
  job; other six jobs passed. This is inherited evidence, not a green Rust baseline.
- Pre-existing unstaged changes: AGENTS.md, README.md, docs/README.md,
  docs/production-gap-register-20261102.md, docs/security.md,
  docs/session-capture-contract.md, and this handoff (926 added/changed lines).
  Pre-existing untracked: DESIGN.md and docs/rust-rewrite research. No staged changes.
  Preserve all; frozen reports/experiments are not worker output.
- Authority: implement and test the Rust replacement in slices; no incidental live
  ingestion, service/hook changes, installs, credential reads, archive migration or
  deletion. Do not re-enable capture. BC-01 synthetic scratch build only for the
  explicit slice-0 gate. Acceptance bucket/token remain external prerequisites;
  never substitute the existing archive credentials.
- Acceptance: DESIGN.md sections 15–17, all nine development gates, independent
  property oracles and mutation sensitivity, source-bound evidence, then actual
  writer→reader→unchanged importer→event index→report. Native/R2 and seven-day
  shadow gates on each host are distinct from offline checks and cannot be claimed
  from unit tests. Slice 0 precedes slice 1; P1/P5 precede slice 1 and P2 precedes
  slice 2.
- Capabilities: subagent launch, status/tool activity, resume and in-flight steering
  are available. Requested model and medium thinking are supported tool arguments;
  startup/model success must still be observed. Git and gh stack available.
- Parent owns this brief, acceptance, integration review and shared documentation.
  Workers own disjoint implementation/prototype paths (assignments recorded below).
  At session start no implementation workers were recorded. Historical challenge
  workers are completed research, whose files remain frozen.
- Evidence: implementation reports under `docs/rust-rewrite/implementation/`,
  prototype reports/scripts under `docs/rust-rewrite/prototypes/`. Reports must
  include exact commands, exit statuses, source hashes, limits and remaining gaps.
  No secrets, private paths or transcript content in evidence.

#### Ownership / progress

- All dispatched workers used actual `openai/gpt-6.1-sol`, requested medium.
  Startup and intermediate work were observed; aborts were resumed under the
  same ownership, not silently taken over. Current states:
- Scaffold `a113d593-47d7-430`: completed and explicitly released all paths;
  parent owns workspace integration, Cargo, flake, gates and new Rust workflow.
- Prototypes `7bece277-e8ba-48b`: completed/released all prototype paths.
  Parent verified hashes/deterministic outcomes with `verify-evidence.py` (0).
- Mapper/privacy `4beb9bdb-26c5-4c7`: completed/released reports 01/03,
  privacy module and tests. Parent reviewed mapping and implementations;
  exact source-qualified results are in those reports, not whole-system proof.
- Mechanics `0c64a3da-6f9f-492`: completed coverage correction, explicitly
  released again; five new external boundary tests, no production edits.
- Privacy reviewer `3c71c653-aade-40c`: completed/released twelve independent
  fixtures, report 05 and minimal Args parity fix after explicit delegation.
- BC retry worker `df3c0ad7-0d12-406`: completed and explicitly released report
  00/evidence03 after final-toolchain native probe passed. Actual gpt-6.1-sol,
  medium requested; startup and progress observed. Prior scaffold worker had
  released paths but its session retention expired; replacement was explicit.
- Read-only handoff auditor `ec236af4-f61c-488`: completed, no owned/reserved paths;
  parent applied its bounded navigation/security/report-status corrections.
- All workers are completed/released; no background task or CI watcher remains.
  Parent owns implementation/evidence paths.
- Locator and manifests have not been assigned or implemented.
- These pure prerequisites were staged without claiming slice-1 entry/acceptance.
  The final BC native probe is now green; P5 evidence remains missing. Neither the
  existing pure ports nor their tests waive the remaining prerequisite gates.
- Parent: initial diffs reconciled; design and initial scaffold reviewed, prototype
  source identities verified, frozen 33-file baseline unchanged. Owns brief,
  integration wiring and independent acceptance. Reviewed privacy/mechanics and
  correction diffs; reran invalidated canonical checks on a stable source snapshot.

#### Decisions / verification

- DESIGN.md read in full; contract goals, quality gates and challenge synthesis/DDIA
  read. Final design supersedes historical Sonnet worker choice: current user asks
  for gpt-6.1-sol medium. No design numbers changed.
- No slice accepted yet. Isolated scaffold and corrected integrated local gates
  passed; final-toolchain native BC Hegel probe also passed after the SSH retry.
  P2/P2b need an operator-created acceptance bucket/token; natural-sleep P6 and
  seven-day runs require elapsed observation, not simulation relabelled as live.
- Parent review (`implementation/02-parent-review.md`) identified and the mapper
  independently confirmed: published protected-content digests/offsets must be
  over transformed bytes; an existing raw-hash object may have different encoded
  bytes and its actual descriptor must be verified before publication; 64 bounds
  delta traversal, not the retained checkpoint piece count. These are correctness
  constraints for implementation, not changes to measured targets or decided formats.
- Mapper's final F2/source-kind/fixture map reviewed. Known required artifact
  families missing from illustrative enum need explicit variants/subtypes, not
  silent unknown-kind treatment. No broad arbitrary-path fallback is allowed.
- Mechanics corrections acknowledged and observed in source/tests: quoted opaque
  ETags, dotted kind names, canonical hashes/schema constraints, snapshot audit
  mismatch repair, exact `session_id` wire golden, and clippy correction. Current
  2,000-case normal tests and actual compiled production-source mutation proof
  passed. A manual bad-Plan control was insufficient and was supplemented, not
  misrepresented as the actual mutation proof.
- Privacy corrections acknowledged and observed: preclone bounds, canonical
  capability binding, typed consumed rule table/digest, URL parser validation,
  TOML native value retention, duplicate-strict lossless JSON numeric handling.
  Parent-authored `parent_privacy_review.rs`: canonical pinned Nix **6/6 pass,
  exit 0** after raw_value wiring. Own properties/doctests and integrated checks
  passed on the recorded source; Args correction below invalidates affected prior
  evidence. No actual sink/daemon privacy acceptance claimed.
- Coverage toolchain repaired: official Rust 1.99 + matching LLVM23 tools pinned
  independently. Instrumented hello and cargo-llvm-cov upstream tests passed;
  parent verified original nixpkgs/Go lock node unchanged. Source-isolated scaffold
  measured 49/50 lines (98%); the failed integrated attempt and corrected pass are
  both recorded below.
- Ownership resolved from actual worker transcript before wiring: scaffold added
  url/raw_value and released Cargo; parent owns subsequent integration. No
  overlapping source edits were performed.
- Parent added `.github/workflows/rust.yml`: native Linux x86_64/ARM and Mac gates,
  SHA-pinned actions, read-only permissions, preserved seed/status artifacts.
  Canonical `make actionlint` and pedantic zizmor on new workflow **exit 0**;
  zizmor was offline mode. Workflow configuration is NOT hosted CI evidence.
- Initial final-toolchain BC retry failed SSH signing/publickey (255). On operator
  request, authentication recovered and a fresh-scratch check passed using the
  exact final flake/lock and official Rust 1.99/LLVM23.1.1. Earlier nixpkgs-toolchain
  evidence was not substituted for this new check; actual BC architecture is x86_64.

#### Integrated gate history and final correction

Parent ran the unchanged integrated wrapper at 2,000 cases, seed 730107:
**all subgates passed except coverage (exit 1)**. Coverage was 1,261/1,363 lines,
92.5165%, below the independently measured scaffold floor 98% (49/50); floor is
unchanged. All 43 source-snapshot files stayed unchanged during this run. Full
logs, source snapshot and missing lines are under `implementation/evidence/01-integrated/`.
Scaffold-only evidence is separately under `evidence/00-scaffold/`; parent checksum
verification passed. Do not conflate the two source states.

- Scaffold `a113d593-47d7-430` completed and explicitly released remaining paths,
  including Cargo manifests/lock, scripts and memory/xtask diagnostics. Final
  native BC official-toolchain probe subsequently passed (evidence03 below).
- Privacy `4beb9bdb-26c5-4c7` completed/released: canonical focused tests,
  2,000-case properties and compile-fail doctests passed. Earlier clippy blocker
  was corrected by mechanics owner and integrated strict clippy passed.
- Mechanics was explicitly resumed for coverage correction, completed/released;
  external tests verify brands, schema, timing, duplicate completion and rules replay.
- Independent privacy reviewer found scalar/object-leaf Args over-redaction against
  Go keepArgs. Parent delegated minimal fix, reviewed regression, then advanced
  transform version to `memory.privacy/2` and refreshed the digest golden. Reviewer
  completed/released. Prior privacy hashes remain historical, not current proof.

Final parent unchanged canonical wrapper: **exit 0**, 2,000 cases, seed **730108**.
All subgates passed; **66/66 nextest**, **5/5 compile-fail doctests**, actual compiled
source mutation killed by both engines; coverage **1,338/1,363 = 98.1658%**, immutable
floor **98%**. No exclusions/removed assertions. All **45** captured source files
unchanged during run. Evidence: `implementation/evidence/02-corrected/`; detailed
independent review/limits: `implementation/02-parent-review.md`. Parent baseline
verification and diff whitespace checks both 0; 33 frozen identities unchanged.

#### Verified BC retry

Operator-requested SSH retry succeeded. Final pinned native BC synthetic gate:
**exit 0**, official Rust **1.99.0**, LLVM **23.1.1**, Hegel **0.52.0** static engine,
**2,000 cases**, seed **730052**, Linux **x86_64**, not ARM. One selected positive
property passed; deliberate wrong research property filtered as in the original
probe. Default-PATH probe 127 was absent cargo outside Nix, not authentication.

Parent independently inspected compiler/test output and scripts, checked all
artifact checksums, compared remote-returned inputs to local source hashes, and
reverified all 45 local green-gate source files unchanged. Evidence:
`implementation/evidence/03-bc-recheck/`. **SSH/final-toolchain native probe blocker
cleared**, not full integrated Linux/ARM/live acceptance. No services, capture or
production source changed; no unchanged local full suite rerun.

#### Next session — ordered continuation

1. Read DESIGN.md in full/section 0, AGENTS.md, this active brief and
   [parent review](rust-rewrite/implementation/02-parent-review.md). Preserve the
   pre-existing dirty files and frozen research. Do not restart scaffold/toolchain
   repair or retry an already-cleared SSH blocker merely because an old report says
   pending. All worker paths are released; a new scoped assignment is safe, but
   worker IDs may be beyond their resumable retention window.
2. Reconcile `git status --short`, HEAD and actual diffs. Run `git diff --check` and
   `python3 docs/rust-rewrite/implementation/verify-evidence.py`. Verify artifact
   checksum manifests and compare current source hashes with
   `docs/rust-rewrite/implementation/evidence/02-corrected/source-before.json` before
   reusing the local green result. That snapshot is HEAD **plus dirty-file hashes**;
   the archive is an overlay, not a replacement for the repository. Existing Go host
   health was not freshly checked. Do not infer permission for live probes/changes.
3. Resolve **P5**: identify an approved safe historical offset/prefix-hash metadata
   input and measure prefix-rewrite frequency. If unavailable, record the precise
   missing input; do not replace real-corpus evidence with synthetic results or read
   private transcripts incidentally. P1 synthetic evidence exists, with limitations.
4. Prepare **P2** once the separate acceptance bucket/scoped token is provisioned
   (https://dash.cloudflare.com/), never use production credentials. Complete actual
   Rust **P4** footprint and natural-sleep **P6** before slice 2; C/Python analogues
   and simulated clocks do not satisfy them. **P8 and P2b are informational, not
   blocking gates**; missing P8 data must not block slice entry.
5. Continue the implementation below with gpt-6.1-sol medium workers, disjoint
   ownership, parent review and unchanged canonical gates. Use the pinned Nix
   commands in [quality gates](quality-gates.md#rust-rewrite-gates-implementation-in-progress);
   local properties require 2,000 cases and coverage stays at or above 98%.

| Delivery stage | Implemented / remaining |
|---|---|
| Foundation | Workspace, strict gate tooling, pure types/planner/coalescer/privacy/launch ports and independent tests implemented. Local gates and BC synthetic probe verified; no full slice/release acceptance recorded. |
| Core (slice 1) | Locator, delta/checkpoint manifests, hint parsing and full required property/Go-regression counterpart coverage remain. Acceptance-map targets are not implemented counterparts. |
| Mac daemon (slice 2) | Lock, real hooks/hints/watchers, durable index/status, reconciliation, chosen client, object publication and conditional commit/recovery protocol; fake-store faults and acceptance-bucket runs; append sources for all three agents and one-day totals. |
| Reader (slice 2b) | Actual authoritative reader, restore/export, unchanged importer/index/report journey, seven-day Mac shadow comparison. Reader crate is a placeholder. |
| Linux/snapshots (slice 3) | BC-01 integration, SQLite/WAL/config/shared-store snapshots, deletions, launch evidence, loss-window counts, P9/P10, footprint remeasurement and seven-day BC shadow. |
| Delivery/cutover | Selectively commit task-owned changes, run hosted CI, keep inherited Go fuzz failure distinct; controlled fresh-production-bucket cutover only after acceptance, then inventory/deletion per DESIGN section 18. No live or destructive action authorized by this handoff update. |

No production code, runtime behavior or canonical gate configuration changed during
handoff reconciliation. README/doc map, Rust guide, gate descriptions and worker
reports now point here; older work/host records below remain historical.

The original 33-file preservation check correctly flagged the four now-authorized
navigation/security document edits. **Original `baseline-identities.json` is unchanged.**
`implementation/handoff-documentation-identities.json` records exact before/after
hashes for only README.md, docs/README.md, docs/security.md and the gap-register
navigation banner. The evidence verifier now enforces those four exact revisions
plus **29 unchanged baseline identities**, including DESIGN, AGENTS, contract and
frozen research; it does not silently exclude changed files. Six isolated verifier
checks pass, including rejection of changed research, unrecorded doc edits, expanded
exception scope and false original hashes.

Handoff checks: artifact checksums for all four prior evidence sets passed (103
files); all 45 integrated Rust source hashes still match; 109 local documentation
link targets exist; `git diff --check` and the evidence verifier pass. Logs/replay
script are in `implementation/evidence/04-handoff/`. A full Nix shell preparation
hit the tool's 60-second cap before running checks while restoring dependencies;
that attempt is recorded, not green. The small checks then passed using the exact
Python 3.14.8 component resolved from the same locked nixpkgs input. No quality
threshold/test timeout was changed and no unchanged Rust/Go suite was rerun.

No commits, pushes, application installs, service changes, ingestion, archive
cleanup or credential reads in this cleanup. All workers and task commands ended.

## Historical operating ledger

The following dated checkpoints preserve earlier observations and instructions.
They are **not current Rust assignments, current host-health claims, or renewed
operating authorization**. Use the active brief above for continuation.

### November 7: direction change, Rust rewrite designed

**Historical status at design approval (superseded above): design final, implementation not yet started; Go candidate recorded running on both hosts.**

Revision, 2026-11-07 (later the same day). Six Sonnet reviewers challenged the design
(`docs/rust-rewrite/challenge/01-06`), the parent synthesised and cross-checked the
blockers against DDIA 2e (`00-synthesis.md`, `07-ddia-crosscheck.md`), and the operator
decided: plaintext storage, one fresh shared bucket for both hosts, sessions never
deleted, reader in scope (slice 2b), delta manifests with a checkpoint every 64, session
opt-out retired, and none of the existing R2 data kept or migrated. DESIGN.md was
rewritten for all ten blockers and the majors; its section 20 maps each finding to a
section. The bullets below describe the original approval and are superseded where
DESIGN.md differs (local state is now section 9, hint log is per-day files).

Original startup plan at that checkpoint (historical, not current assignments):

1. Read DESIGN.md section 0.
2. Slice 0 (section 17) and the prototypes that need no R2 (section 16: P1, P3 to P8)
   can start at once with Sonnet workers.
3. External prerequisite: the operator creates an acceptance bucket and token (section
   6.1). P2 (S3 client) and P2b need it; slice 2 needs P2.
4. The one existing session opt-out record is removed at cutover (section 18 step 3),
   not before, because the running Go path still honors it.

Checks run for the revision: `manifest_cost.py` rerun (4,650 sessions, 27,683 captures,
4.01 GB; full rewrite 457 MB, delta 29 MB). No code, gates or live systems were touched.

The operator reviewed the Go candidate and directed a Rust rewrite. The design is
[DESIGN.md](../DESIGN.md) at the repository root; its section 0 is the entry point for a
fresh session. Evidence, reviewer reports and experiment scripts are in
[docs/rust-rewrite/](rust-rewrite/). What changed in direction, all measured on this Mac
on 2026-11-07:

- Upload unit is an append piece per session window, not a whole source-epoch object.
  Whole objects cost 10.7x the bytes on Claude history and 155x on Pi.
- No spool, no SQLite job tables, no unix socket, no failure file. Three local files:
  operator config, an append-only hint log the hooks write, one rebuildable index snapshot.
- Hooks and filesystem notifications drive capture; a 15 min safety reconcile verifies.
- Window defaults quiet 60 s, cap 300 s. Scope ends when `current.json` is durable.
- Standards: adapted BeautifulRust lint table, Hegel property tests (verified building on
  the Mac), `cargo-deny`, nextest, schema drift, monthly mutants on core.

Nothing in the November 6 checkpoint below is an instruction for the Rust work. The Go
operating steps, authority and containment commands remain valid for the running Go paths.
The next action at that checkpoint was slice 0 and the prototypes. That startup work
has since progressed; the active Rust brief above is the current continuation point.

### November 6 (20:57Z Nov 5): resume here

**Status: merged and running live on both hosts; not yet operationally accepted.**
Mac step 3 has passed. BC-01 step 3 is complete except for two Codex checks, which wait on
an operator approval. Step 4 (combined operation and budgets) has not started. A fresh
session should read this subsection, then the
[contract](session-capture-contract.md), then the dated entries under
[Production delivery — November 4](#production-delivery--november-4). The newest entries
are at the top of the wave 4 operating block.

| Area | State at 2026-11-05 20:57Z |
| --- | --- |
| Source | `main` = `57d8400` (PR #53). The local checkout was fast-forwarded to it. This handoff is the only uncommitted change. Open PRs: only dependabot #32 and #33. |
| Mac capture | LaunchAgent `agent-sessions-capture-experimental` is enabled and running, pid 48472 since 17:52:43Z. Binary `dc361b24…` (`57d8400`), launcher `91a49ec3…`. Coordinator 0.13 cores averaged over 3 h. Jobs: 21,610 published, 511 superseded, 1 pending (in flight). 0 quarantined, destination unpaused. Payloads 2,778 MiB of 8 GiB; queue 1,300 MiB. |
| BC-01 capture | systemd user unit enabled and active, pid 224893, 0 restarts. Binary `a572a0bd…` (`57d8400`). 0.06 cores averaged over 2.5 h. Jobs: 47,788 published, 58 superseded. 0 undelivered, 0 quarantined. Payloads 930 MiB of 6 GiB; queue 1,447 MiB; 38 GB free. |
| Mac analytics | Refresher (pid 19096) and dashboard (`http://127.0.0.1:8766/`, HTTP 200) run source `f94670a` (`tools/` is identical to `57d8400`). **Degraded, open (item 1 below).** |
| Hooks | Mac: Claude, Codex and Pi all proven live. BC-01: Claude and Pi proven. **BC-01 Codex 0.163.0 hook trust is stale** (a bypass probe shows the hooks work). |
| Privacy | One Codex exclusion record on the Mac (from the step-3 probe). By design (`exclusion.Registry.Excludes`, pinned by `TestRuntimePrivacyScopesAggregatesAndUntrustedIdentity`), it denies **all** Codex shared resources under `~/.codex`: about 7,900 active `policy_denied` discovery issues, and no Codex resource captured since 12:02:55Z. Do not delete the record: that would publish the excluded session's rows inside the shared Codex SQLite stores. BC-01 has 0 exclusion records. |

**Authority (operator, 2026-11-04):**
- Allowed:
  - operating steps 1–4 (activation, acceptance, BC-01 SSH and R2 inventory);
  - publishing branches and PRs, CI, and merging once green.
- Excluded:
  - step 5 (final defaults, legacy retirement, key deletion, reset);
  - release tags;
  - any reset of BC-01's queue;
  - enrichment or epoch retention changes without a measured design and operator approval.
- Containment when a regression appears:
  - Mac: `<wrapper> capture-v1 service --action stop --apply --executable <wrapper> --config ~/.config/agent-sessions/capture-runtime.json`, then `launchctl disable gui/$UID/agent-sessions-capture-experimental`.
  - BC-01: the same `service stop --apply` command.
  - `<wrapper>` = `~/.local/libexec/agent-sessions-capture/agent-sessions`.

**Working rules learned in this rollout:**
- Report aggregates only. Never print identities, transcripts or credentials.
- Open queue databases read-only (`file:...?mode=ro`).
- BC-01 has no `sqlite3` CLI. Run `/usr/bin/python3` through `ssh bc-01 sh -s < script`.
- The local login shell is fish, so wrap POSIX shell in scripts.
- **Never write Codex `trusted_hash` entries.** Re-trusting is an operator action in Codex `/hooks`.
- `--dangerously-bypass-hook-trust` is a per-invocation diagnostic only.
- Probes are throwaway "Reply with the single word OK" sessions run in an empty temporary directory:
  - Codex: `codex exec --skip-git-repo-check -s read-only`.
  - Claude and Pi: `claude -p` / `pi -p`.
- Outage simulation without root:
  - Mac: `sandbox-exec` with outbound IP denied, plus `taskpolicy -b`.
  - BC-01: `systemd-run --user -p RestrictAddressFamilies=AF_UNIX`. `unshare -rn` is blocked by AppArmor.
- Measure CPU as a `ps -o time=` delta under the real supervisor. Background QoS changes Mac CPU about 3×.
- Go, Python builds and tests run only in the private Docker harness (`launch.py`), never on the host.
- The shared validation semaphore was retired on 2026-11-05. `~/.config/agents/validation.md` no longer exists.
- Private evidence root: `~/.local/state/machines-validation-pilot/memory-delivery-20261102/` (`RUNBOOK.md`, `WAVE4-RULES.md`, `launch.py`, `reports/`, `scripts-*`).

**Next actions, in order:**
1. **Diagnose the refresher regression first** (it started after 18:38Z; nothing has been changed yet).
   - BC-01 endpoint `process_failed` since its 20:45:17Z attempt. Last success was at 18:38:00Z.
   - Head failures `current-unreadable` first appeared at 19:58Z. At 20:57Z they stood at 66: 46 Mac and 20 BC-01, all Pi and Codex resources that had been imported before.
   - The Mac endpoint sweep slowed from about 19 minutes to about 2 hours (completed 18:43Z, then 20:45Z).
   - A manual first page of `capture-v1 current-heads` with `capture-reader-bc01.json` at 20:55Z returned rc 0 in 22 s with 232 heads and 6 failures. So this is intermittent, or it happens on a later page.
   - `run()` discards stderr, so reproduce one full paginated sweep with aggregate output to find the failing page or exit code.
   - Suspects:
     - per-head 5 s `CurrentHead` timeouts or R2 throttling with about 57k targets;
     - the 300 s per-process timeout;
     - load.
   - This blocks step 4 freshness. The dashboard must not be described as healthy while it persists.
2. **BC-01 Codex.** Operator: on BC-01 run `codex`, open `/hooks` and approve the 6 agent-sessions hooks. Then:
   - run a normal (non-bypass) Codex probe and confirm a hook-driven capture (hints and native-evidence directory mtimes change, the job publishes);
   - run a Codex privacy-exclusion probe (`AGENT_SESSIONS_DISABLE=1`) mirroring the Mac. Expect an exclusion record and no queue rows or analytics target. Codex shared resources on BC-01 will then be denied by design.
   - Then record BC-01 step 3 as passed.
3. **Step 4, combined operation.** Reconcile both inventories and run fresh-reader sweeps. Measure and agree budgets for:
   - coordinator CPU: Mac about 0.13–0.29 cores, BC-01 about 0.06;
   - refresher CPU: about 0.47 cores at `--interval 60`;
   - refresher sweep duration and freshness;
   - BC-01 discovery generation time: about 5 minutes, which is the wait for a hookless session;
   - largest records;
   - spool and retention horizon.
4. **Decisions that need operator approval** (record them; do not implement unilaterally):
   - **Epoch retention for in-place-rewritten artifacts.** Every unproven rewrite starts a full new epoch, and the latest manifest keeps all sealed epochs.
     - Mac `thread_history_1.sqlite`: 6 epochs / 1,232 MiB encoded, about 205 MiB per Codex run (now frozen by the exclusion).
     - BC-01: 4 epochs / 91 MiB.
     - `~/.claude.json`: 206 epochs in 8 days.
     - Two targets (one per host) are `export_too_large` against the refresher's 768 MiB `--max-bytes`.
     - Old job rows keep full manifests, so queue manifests grow quadratically: Mac 578 MiB, against about 8 MiB for latest-only.
   - Enrichment retention (earlier ER item).
   - Whether shared multi-session agent stores (Codex SQLite, `history.jsonl`) should be captured whole at all.
5. **Follow-ups**, not acceptance blockers:
   - discovery change detection or cadence;
   - refresher cost;
   - `reclaim.go` mutation survivors;
   - the burst-erase-newer mutant;
   - macOS syncer test margin;
   - blob-before-state layout;
   - inbox inode-reuse limitation.

### October 30 (historical): source integration complete; live operation not accepted

**The PR/integration work is done. Memory has not been proved working live end to end
on the merged source.** This documentation reconciliation does not deploy or activate it.

| Area | Accepted state / remaining limit |
| --- | --- |
| Source integration | [PR30](https://github.com/mich-murphy/memory/pull/30) **merged** into `main` at `b0f949c6a8de76b4a8cd3f0bdf48ae29d44b1026`. Its tree equals qualified source `ef0f3d31aa76d9ba7c1370431006e1a3c2bb1f62`. All 139 integration commits / 458 changed paths landed, including the 88 previously local-main commits. |
| Local development qualification | Actual canonical `make check` aggregate **exit 0** on literal `ef0f3d3`, not only a staged prerequisite union. Coverage **87.5% (13,963/15,963)**; maximum CRAP **29.40**, unchanged limit 30. Full analytics **117 tests / no skips**. See [source-bound evidence](session-capture-offline-evidence.md). |
| Hosted source/platform qualification | All seven jobs passed for both [exact-head PR CI](https://github.com/mich-murphy/memory/actions/runs/41017288817) and [exact merged-main push CI](https://github.com/mich-murphy/memory/actions/runs/41018839999): Linux amd64/arm64, native macOS, static, actual consumer/full analytics, fuzz, workflow security. This is not installed-host or actual-R2 acceptance. |
| PR cleanup | #23 closed as contract-retired recovery work. #26/#27/#28 closed as superseded **after their exact useful maintenance updates landed through #30**. No open PRs remained at integration completion; none were found during this documentation reconciliation. Historical branches/worktrees remain preserved. |
| Release artifacts | Private `0.5.0-SNAPSHOT-74c00f4` was verified against **earlier `74c00f4`**, not the merged source. It is unpublished, not a current-source release artifact, and not installable through the normal immutable-release installer. No new tag or release was published. |
| Installed capture | Last recorded installed source on **both hosts: `b8cbed2`**. The October 29 containment review found **both collectors disabled/off**. Installed coordinates, supervisor state and consumer health were **not freshly inspected** during source integration or this documentation work. Do not describe that dated observation as a fresh health check. |
| Operating acceptance | **Incomplete and separately gated:** merged-source native hooks, automatic capture, actual R2 publication, reader/importer/index/report freshness, restart/outage/privacy behavior, two-host coverage and representative resource budgets. |

Final independently verified integration disposition:
https://github.com/mich-murphy/memory/pull/30#issuecomment-6516354560

### What landed and what did not

- Retained `881b04e` per-lane scheduling repair and `74c00f4` portable stat adapters,
  preserving privacy, ordering, exact fingerprint bytes and quota behavior.
- Integrated the missing CLI failed-sync logging/durable run-recording leaf as
  `7b55448`, with real failure/cancellation regressions; final fixture lint fix is
  `ef0f3d3`. Independent medium-effort source review found no blocking defect.
- Retained PR26→`063277a` (gopls 0.24 / x/mod 0.41 / x/text 0.42), PR27→`04aef71`
  (pinned zizmor-action 0.7.1), and PR28→`267bd3f` (nixpkgs `d3b13ad` / cacert 3.131.1).
  Qualification used the actual newly locked Nix closure/CA and separately scanned
  the real 24-module gopls tool graph; collector `make vuln` alone does not cover it.
- Committed the relevant previously dirty handoff and **62 Git-visible research files /
  8,162,474 bytes** unchanged as `1a317db`. Bounded publication review is not universal
  DLP certification. Ignored raw query data and source-bearing cache databases remain
  local/ignored; never force-add them. Preserve frozen evidence and recorded hashes.
- Retired concurrent/pruned cold recovery, nongate-passing preservation snapshots,
  archived cost probes and the old **legacy** Linux-native prototype remain preserved,
  not shipping prerequisites. That prototype does not prove the independent candidate's
  Linux native hook→queue→reader→analytics path; this remains a real operating gap.
- The seven-stage delivery history is integrated and preserved. No PR audit, source
  integration, CI babysitting or validator worker remains outstanding from that task.
  Do not restart those tasks from the archived ledger.

### Historical live evidence is real, but not current acceptance

The replacement was experimentally installed **before** the latest repairs. The
[archived installed-qualification record](session-capture-handoff-archive-20261030.md#existing-bucket-reset-and-installed-qualification--october-29)
records the controlled existing-bucket binding switch, earlier actual R2 journeys,
selected installed Mac/BC publications through reader/importer/index/report, and native
Mac scratch gates. Do not rewrite that history as “never installed”, “no live tests ever
passed”, or “no binding change ever occurred”. Those selected older-source journeys did
**not** establish full inventory, sustained operation or acceptance of today's source.

The later [October 29 observation review](session-capture-handoff-archive-20261030.md#operating-measurements-reviewed--october-29)
recorded partial coverage, high Mac CPU after backlog drain, an unexplained Mac exit/
restart, and no completed BC discovery sweep before containment. Both collectors were
then confirmed off. No fixed 24-hour wait is required; elapsed time alone is not acceptance.
The subsequent scheduling repair has deterministic RED/GREEN evidence and a completed
bounded synthetic comparison, but **settled/background CPU and production operating
budgets remain unresolved**. Do not infer that the fairness fix solved them or start
more speculative performance runs from this brief.

The archive also records previously authorized generated-state deletion and the later
fresh binding. Some old rollback points no longer exist. Never treat old inventories,
backup paths or local source availability as proof that all prior accepted/unpublished
bytes remain recoverable. No bulk old-object retirement or final legacy cutover is
recorded. Both old `b8cbed2` and the merged candidate use `session-reader/v2`: no protocol
migration, archive reset or rebind is a prerequisite for coordinating their software.

## Completed source review — October 30

The earlier operator request authorized source architecture/API/data-structure review,
production-readiness analysis, test-oracle cleanup, and evidence-led offline prototypes
and improvements. It does **not** activate the separately gated operating sequence below.

- **Base:** `/Users/mm/dev/memory`, `main`, `b0f949c6a8de76b4a8cd3f0bdf48ae29d44b1026`.
  Pre-existing changes are README, documentation index, production-readiness, delivery,
  this handoff, offline evidence and phase3, plus the untracked October 30 handoff archive.
  Preserve that reconciliation and all frozen research; no production/test changes existed
  at review start. No fresh PR/CI or installed-state inspection is claimed.
- **Acceptance:** rank concrete findings against current source; verify hypotheses with
  bounded synthetic regressions/prototypes before adopting changes; replace weak oracles
  without deleting safety coverage; independently review changed invariants and run relevant
  unchanged gates. Distinguish maintainability suggestions, measured mechanisms and actual
  production budgets. No rewrite, speculative GC/chunking, legacy migration or live access.
- **Ownership/capability:** parent owns this brief, integration, admission and acceptance.
  Background workers support status, conversation inspection and steering; three may run
  concurrently. Requested workers use `gpt-6.1-sol`, medium thinking. Initial assignments
  were read-only: core architecture `664d320f-7afc-48d`; test oracles
  `b7368fb2-b679-400`; performance/scheduling `152dd951-7250-4e9`; consumers/operator
  APIs `df44d707-0f1f-424`. Tool results confirm the actual model is
  `openai/gpt-6.1-sol`; startup/activity verified for the first three reviewers.
  Validation owner `101a6d61-0e02-4e0` prepares private admitted runtime/evidence only.
- **Selected work/ownership:** initial core, test and performance audits are complete.
  Test worker's completed ownership covered only `internal/sessioncapture/queue_test.go` and
  `internal/capturedaemon/{config,lifecycle}_test.go`: independent postcommit/no-source
  recovery and exact supervisor command oracles, with targeted mutation proofs.
  Core worker's completed ownership covered only `internal/sessioncapture/runtime_{delivery,store}.go` and new
  `runtime_delivery_lane_test.go`: deterministic wrong-lane selection race proof/repair.
  Performance worker owned private synthetic overlays only (unchanged transforms and
  due-selector scaling), not production optimization. Consumer review is complete;
  `df44d707-0f1f-424` owned only `tools/analytics/{events,publications,test_events,test_publications}.py`
  for framed recursion isolation and trace-scoped Codex mirror pairing with regressions.
  Parent verified the lane windows and both consumer control flows against current code;
  the subsequent RED/GREEN results below established the defects and repairs. Reviewer withdrew an apparent
  `head_failures` issue because returning valid heads correctly clear discovery failures.
- **Evidence/status:** `test-oracles-v1` admitted proof completed: all three deliberately
  broken variants passed old focused tests (exit 0), failed stronger tests at the intended
  assertion (exit 1), and unchanged behavior passed the strengthened restart/supervisor
  targets (race/shuffle, count1, 600s/package). Source manifest
  `0e0b6ebab93a603de3d515ff12d0c0231cc7b6019deb2ee263e26d25982d9d83`;
  parent inspected exact argv/results in `jobs/test-oracles-v1/out/proof-results.json`.
  At that intermediate checkpoint full acceptance was pending; final results follow below.
  Medium worker reached its 4GiB cap
  (196 max events, no OOM, swap zero); do not claim zero resource pressure.
  Private evidence root: `~/.local/state/machines-validation-pilot/memory-review-20261030/`.
  Parent corrected the new test's mistaken `present` source-state expectation to the
  implementation's `active` via owner steering; correction observed and covered by proof.
  No general rewrite is justified. Retain integrity checks, no GC, and one reader.
  At that checkpoint the decoded/encoded 1GiB envelope remained unproven; the implementation
  continuation below records its subsequent proof and repair.
- **Further evidence:** both bounded performance prototypes passed their exact body/work/
  selection controls (see [review findings](architecture-review-20261030.md)); no performance
  production change adopted. `lane-combined-v1` proved both old-source wrong-lane windows RED,
  candidate focused tests GREEN at count3, and full sessioncapture/capturedaemon race/shuffle
  packages GREEN. Parent's paused-order correction is implemented/tested. Independent reviewer
  `2ef4e21d-f5ff-467` approved final lane source hashes and analytics/test-oracle invariants,
  contingent on execution. Parent documented mirror helper's one-shot fresh-scope precondition
  and removed redundant count-only assertions now subsumed by exact supervisor argv checks.
- **Correction verified:** `analytics-focused-v1` remains a real failed attempt: both
  recursion tests failed their fixture premise before the system under test, and GREEN
  had 2/40 failures. The bounded actual-byte probe established decoder overflow at
  depth60,000/120,031 bytes and normalization overflow at depth512 after successful decode.
  Corrected v2 proves each baseline defect with the intended failure and passes all40
  focused tests. No skipped assertion, stack-limit change or broad catch. Parent read the
  actual results and corrected fixtures. Primary CPython research confirms the C-stack/
  Python-recursion-counter distinction. Existing unchanged projections are not automatically
  re-normalized; no operating refresh/migration was introduced.
- **Final acceptance: source/offline scope complete.** All implementation owners released
  paths and independent review is clear. Parent removed only redundant old count assertions,
  reproved both supervisor mutations and compared 610 code/test/tool/build files against
  `final-v1` with zero drift. Final snapshot manifest:
  `ce9fe805086a5b8f6ec5b84c173491bb16beedcd831163f7805bacf07c6cfc49`.
  Literal canonical `make check` **exit 0**: coverage **87.5% (13,973/15,976)**, maximum
  CRAP **29.40**, all unchanged gates pass. Consumer journeys 12/12, R2 launcher safety
  6/6, delivery launcher 12 discovered/one explicit SDK-peer-only skip. Separate complete
  actual-binary analytics discovery **122 tests/no skips, exit 0**. Existing dashboard
  HTTPError cleanup warning was retained. Parent inspected exact logs/results and source
  identities; details/commands/limits live in the [review report](architecture-review-20261030.md).
  Final Large workers had no memory max/OOM events or swap. No gates were weakened.
  Parent independently reverified all **10,739 sealed evidence files**; seal digest
  `819d57cab989a10fd72866eaefbb068f3f785e1232806a266969be006d7abf7b`.
  Validation owner completed; no review-owned containers remain.
- **Delivery boundary/next:** changes remain uncommitted in this working tree; pre-existing
  documentation and all original/live state are preserved. No source worker or corrective
  item remains outstanding in accepted scope. No new CI, native Darwin, timed fuzz, installed/
  actual-R2/two-host acceptance, deployment or activation was performed. Readiness remains
  blocked on the separately authorized operating sequence below, representative budgets,
  and then-unproven encoded-envelope qualification—not another unmeasured rewrite. The
  continuation below addresses the envelope in source, not operating acceptance. Documentation-
  only evidence updates do not change the code identity qualified at this checkpoint.

## Implementation continuation — October 30

The operator now requests delegated implementation with parent-owned acceptance, quality
and verification. Continue source/offline work; this is not activation or live-data authority.

- **Reconciled base:** same `main`/`b0f949c6`, unstaged prior review plus pre-existing docs;
  no staged changes. All ten previously accepted code/test hashes match the sealed review.
  All six previous worker IDs freshly report completed/released; prior evidence remains
  frozen. Do not redo accepted repairs or overwrite their diffs. No new PR/CI inspection
  is claimed for this source-only continuation.
- **Capabilities:** background workers, status/transcript inspection and steering are
  available; at most three concurrent agents. New workers request `gpt-6.1-sol`, medium
  effort. Parent owns this brief and integration; one validation owner executes admitted
  checks. No missing coordination capability is currently identified.
- **Scope/acceptance:** (1) prove and resolve the decoded/encoded capture/transport envelope
  mismatch without silently weakening limits or losing accepted work; (2) compare and, if
  justified, implement a maintainable due-selector optimization with exact eligibility,
  ordering, lane and sparse-history safety. Prototype before selecting changes; retain
  previous success/privacy/quotas/durability. No speculative GC, stat-only transform cache,
  second queue/reader, protocol migration, live source inventory, release or cutover.
- **Ownership:** envelope `174889d8-7aa9-414`, initially private prototype/proposal, possible
  capture-admission/new boundary tests/shared size definitions; excludes runtime scheduling.
  Selector `a0a0fcfc-db42-486`, initially private query comparison, possible `runtime_store.go`
  selector/index and new focused tests; preserve accepted lane changes. Validator
  `be41504d-0c3b-4f2` owns new private runner/snapshots/logs only. All three startups/model
  identities were verified from actual activity/acknowledgements. Validator completed
  setup/selector/envelope proofs, final source-bound validation and evidence sealing.
  All four workers are completed/released. Selector owner made no production edits.
  Envelope owner released five production
  paths; parent and independent reviewer `3226dd9e-2f8b-4a5` verified their exact hashes.
  Reviewer startup/model and completion were verified; no blocking source finding.
- **Evidence:** new root `~/.local/state/machines-validation-pilot/memory-implementation-20261030/`;
  prior `memory-review-20261030/` stays read-only/sealed. Initial state: no new checks run,
  no new production change accepted. Required final gates retain pinned Nix, shared worker
  admission, source/dependencies read-only, native EXT4 temp, network-none, race/shuffle and
  default timeouts/thresholds. Previous checks remain applicable only to unchanged source.
- **First measured decision:** selector v1 passed exact row/order/head/claim controls across
  8,104 actual synthetic captures and 576 samples (six alternating-order rounds × 16 queries).
  Parent inspected logs/bindings/raw samples/summary: `jobs/selector-v1/`, source manifest
  `baf133b8b9574cbdf3bcbfeff8f160569facd1df410411b9dc31f4ebe6b6922a`.
  Session-driven lookup improved eight-head/100-revision SQL median 887.8→67.4µs but worsened
  inactive-prefix 42.5→745.4µs; rejected for adoption. Another due-session shape scanned
  retained history under the measured planner and regressed badly; also rejected. Worker
  was redirected to due-index aggregation/head equality and future-dominant controls.
  These are SQL mechanism measurements, not end-to-end performance acceptance.
- **Selector disposition:** parent accepts retaining baseline SQL, not an optimization.
  v2 passed 9,704 actual-capture fixtures/540 samples, manifest
  `dab99ef0040fbb698ddbd0fa3ac27a27e8c10cfb9f5e579b6a736e0592bc2af7`.
  Due aggregation improved eight-head/100-revision cursor-zero median 635.8→423.0µs,
  but worsened 256-head median 432.3→572.2µs and small/sparse cases by roughly 8–11µs.
  The additional grouping B-tree explains a structural tradeoff; no representative
  runtime workload/budget establishes that adopting it helps. Stop further variants.
  `jobs/selector-v2/` preserves the result; no production SQL, index or new queue state
  changed. Actual claimSession integration proof remains required if later adopted.
- **Envelope implemented and source/offline accepted:** shared encoded bound in
  `internal/publication/types.go`, unchanged R2 bound alias in `internal/publicationr2/backend.go`,
  `ErrEncodedSize` wrapping `ErrQuota` in `internal/sessioncapture/queue.go`, streaming guard
  in `internal/sessioncapture/quota.go`, and new `internal/sessioncapture/encoded_size_test.go`.
  Both 1GiB bounds remain; no reader change, public test-only option, migration or deletion.
  `envelope/production-release-v1.sha256` binds the five released files. Parent corrected
  the private unit's impossible aggregate usage seed before shipping: current units include
  the writer's existing bytes plus unrelated spool usage. Relocations into existing files
  and the specific error message are recorded separately from frozen private v1 inputs.
- **Actual envelope proof:** scaled baseline captured 65,536 decoded bytes as 65,550 encoded,
  then failed delivery; the same baseline failed the intended preacceptance assertion.
  Candidate passed preservation/reopen/exact-decoded controls, and unscaled cheap units
  passed. Full-size baseline then accepted **1,073,741,824 → 1,073,766,413 bytes** and failed
  transport; actual production source rejected before acceptance with prior source, job
  status and readable remote publication preserved. Both full-size runs published/read
  back the exact-1GiB compressible control (**114,691 encoded bytes**) through the actual
  SDK-backed synthetic disk peer and authoritative reader. No actual R2 service was used.
  Evidence: `jobs/envelope-{scaled-*,unscaled-unit,full-baseline,full-production}-v1/`.
  Full production proof manifest `f905be7303a9a81a45752ca412996f3a6d04a9ed363c62e7102047fc67c94ac4`
  adds only a private probe to the clean production source. No full-size repeat is needed.
- **Validation state:** setup/selector workers exited 0, source pre/post hashes verified,
  no max/OOM/swap events; selector peak 1,407,352,832 bytes. Nonzero I/O pressure is retained.
  Scratch inspection found 96,264,335,360 bytes free on native executable EXT4. Selector
  package timeout 600s, enclosing bound 1200s, actual v1 wall 50.978s. v2 wall 48.74s,
  peak 1,642,741,760 bytes, likewise no max/OOM/swap; I/O pressure retained. Envelope
  stage 1 authorizes four serial Medium race/shuffle jobs with 600s bounds: scaled
  baseline reproduction, intended-assertion RED, candidate GREEN, unscaled cheap units.
  These four jobs passed their expected outcomes (the intentional RED is exit 1, not a
  suite pass). Full baseline/production runs exited 0 with peak 3,639,156,736 / 2,531,053,568
  bytes, no max/OOM/swap, source pre/post checks passed; independent compile/runtime bounds
  remained 600s each (1250s enclosing worker). Full probes are non-race; cheap/scaled and
  ordinary source/package gates retain race/shuffle. Focused three-package race/shuffle
  checks and pinned formatting/scoped lint passed on clean manifest
  `d79c4ea8ad43055c76c8876425c570e4cdffc22b59d7645226fd54317ea5288d`, with no private probes.
  Parent independently compared 614 non-documentation files: zero drift. The stronger
  actual-Capture assertion for both error sentinels arrived after the full candidate had
  already completed; no full-size repeat occurred. A separate scaled final-source v2
  proof passed both assertions. Its first preparation attempt assumed a standalone constant
  instead of the released const-block member: no payload ran, and the failed setup remains
  preserved separately. Parent inspected corrected v2 source/binding/logs. Previous sealed
  evidence was independently reverified unchanged (10,739 files / 78,675,454 bytes).
- **Canonical gate passed:** literal `make check` exit 0 on clean production-v1; coverage
  **87.5% (13,979/15,980)**, maximum CRAP **29.40**, unchanged limit 30. Consumer suites
  12 + 6 + 12 discovered, with the existing explicit SDK-peer launcher skip retained.
  Parent inspected binding/log/result/resource records; actual wall 779.624s within 1800s,
  peak 5,265,522,688 bytes, no memory-max/OOM/swap events. Full analytics subsequently
  acquired the shared lease without bypass: freshly built actual-source binary, **122 tests,
  no skips, exit 0**, 28.369s test time; overall 280.512s includes admission waiting. Peak
  1,130,504,192 bytes, no memory-max/OOM/swap. The existing HTTPError403 cleanup
  ResourceWarning is retained, not a warning-free claim. Parent inspected final bindings,
  build argv, logs, exact results and resource counters; no check threshold was changed.
- **Parent acceptance:** continuation source/offline scope is complete: new-admission
  envelope defect repaired and validated; selector comparison concluded without an unsafe
  optimization. Independent review is clear; no source correction is outstanding. Existing
  accepted oversized objects are not repaired or deleted. Changes remain uncommitted;
  pre-existing edits, original/live data and prior sealed evidence are preserved. No new
  CI, native Darwin/other-architecture, timed fuzz, installed-host, actual-R2, deployment
  or two-host qualification is claimed. Operating acceptance and representative budgets
  remain separate requirements, not evidence supplied by these synthetic tests.
- **Evidence sealed and independently reverified:** new root
  `~/.local/state/machines-validation-pilot/memory-implementation-20261030/`,
  **16,615 files / 96,482,309 bytes**, zero hash mismatches. `evidence.sha256` digest:
  `32df65a2be32ff172e2d9f42389d2d103b6398075a76d3be0d23c48653fe81b4`.
  Parent acceptance is recorded in `parent-implementation-review.json`; exact execution
  results are in `FINAL-RESULTS.json` and `LEDGER.md`. Parent confirmed all 14 exact owned
  container IDs removed. Evidence is frozen; use a new root for later work. Final
  `git diff --check` and 60 relative documentation links/anchors pass.
- **Next:** no source/offline continuation item remains outstanding. The separately
  authorized operating sequence below remains the production-readiness boundary.

## PR publication and merge request — October 31

The operator now authorizes publishing this reviewed change bundle as a PR, delegated
babysitting with `gpt-6.1-sol` at medium effort, and merging when ready. This extends
source delivery authority only; all operating exclusions below remain in force.

- Parent reconciled `main` at `b0f949c6`, 20 modified and four untracked task files,
  no staged changes, and no open PRs. The prior implementation/review workers completed
  and released ownership. The related pre-existing documentation reconciliation and
  verbatim archive are included unchanged alongside the accepted review/implementation
  work; unrelated history, research, worktrees and sealed evidence must be preserved.
- Use one PR on a new single-layer `gh stack` branch rooted at current `main`, with
  logical commits for source fixes/tests and documentation. Preserve the old merged
  stack; do not force-push `main` or clean up historical worktrees/branches.
- Parent owns acceptance and this brief. Caretaker **`9781c418-91df-46f`**, confirmed
  running `openai/gpt-6.1-sol` at medium effort, owns branch/commit/push, review/CI
  supervision and eventual merge. Startup and code identity were independently checked.
  **PR: https://github.com/mich-murphy/memory/pull/31**
  Branch `review/capture-hardening-20261030` contains all 24 accepted paths in four
  logical source/documentation commits; this final brief update is documentation-only.
  The prior merged stack is preserved. Initial stack submission encountered a collaborator
  permission error; command-scoped use of the existing authorized owner account resolved
  it without changing global authentication or exposing credentials.
- This checkpoint records publication and the parent-approved final scope, **not a claim
  that CI or merge has completed**. The linked PR and its final disposition comment are
  authoritative for exact final-head checks, review resolution, merge SHA/tree comparison
  and merged-main CI. This avoids inventing a future merge result in its own pre-merge
  commit. Parent has released this brief update to the caretaker for the final commit;
  no additional code change is authorized without review and appropriate local checks.
- Acceptance: committed code must match qualified clean manifest `d79c4ea8…` unless a
  reviewed fix invalidates and reruns affected local gates. Inspect review feedback and
  all hosted jobs on the exact final head, not merely required-check labels. Use exactly
  one `gh run watch <run-id> --interval 60 --exit-status` at a time; no `gh pr checks` or
  polling loops. Respect rate-limit reset and all repository protection/approval rules.
  Merge only a ready, conflict-free, accepted head, then verify merged-tree identity and
  the merged-main CI run. Post full URLs immediately for any genuine human gate.
- New source-bound PR evidence belongs in a fresh private root, not either sealed review
  root. No new live/R2/native installed-host actions, refresh, activation, reset or release
  are authorized. Current source/offline gates already pass; do not rerun unchanged
  expensive validators merely to package commits. If a fix is needed, follow the pinned
  Nix/shared-worker policy and retain all timeouts, security assertions and quality gates.

## Production-readiness analysis — November 2

Operator request: Sonnet-worker exploratory analysis of goals/current state; offline
experiments across every historical problem class; verify structures, contracts and
schemas give correct, efficient session loading into R2; document gaps in enough detail
to resolve them; identify tautological tests and needed property/mutation testing.
This is source/offline authority only; it does not grant the operating sequence below.

- **Base:** `main` = `origin/main` = `2deac62` (PR31 merged), clean at task start.
- **Capabilities:** background workers with status/transcript/steering; at most three
  concurrent. Workers request `sonnet`, medium thinking. Parent owns this brief,
  synthesis, admission and acceptance. Tests/builds run only through the shared
  worker-side Docker admission in a new private root
  `~/.local/state/machines-validation-pilot/memory-analysis-20261102/`; prior roots stay sealed.
- **Phase 1 (read-only, reports only to the private root `reports/`):** history/failure
  catalog; architecture/contract/schema conformance; test-oracle/property/mutation audit.
- **Phase 2:** parent-selected bounded experiments as private snapshot overlays, never
  checkout edits; RED/GREEN premises checked before claims. Results feed one repository
  gap register. No production change is accepted without its own review and gates.
- **Exclusions:** no live R2/BC-01/original-source inventory, install, activation,
  refresh, release, reset/deletion, protocol migration or speculative rewrite/GC/chunking.
- **Workers (all completed/released, `claude-5.5-sonnet`, medium):** phase 1 history
  `bafa72af-7588-478`, conformance `66bdff21-eddb-45f`, oracles `eb5e4b1b-e68d-4e5`;
  phase 2 gaps `83f8b1e5-891d-4c7` (e-), oracle mutants `654804bb-3441-492` (t-),
  model/privacy `68de17a5-3fd2-469` (p-), crash/hints/discovery `b5f2b9de-3518-4fa` (c-),
  analytics `10ae7096-bf2d-4d6` (a-), fuzz/mutation `47f9fcc3-73b8-431` (f-).
  Corrections: p- held all three container slots → steered to one at a time
  (acknowledged; later jobs complied). f- reported 60s fuzz campaigns that stalled
  after about 6–12s, and an invalid mutation run (40/41 timed out) → steered to report
  effective time and use a valid coefficient (uptake verified in `f-mut-quota-v2` and its report).
- **Parent verification:** every promoted claim was rechecked against the cited code
  and against each job's argv, overlay binding (added versus declared-mutant paths),
  exit status, decisive log lines and source post-verify. Analytics jobs exit 0 through a
  `tail` pipe, so their RED results were read from the unittest lines.
- **Result:** [production gap register](production-gap-register-20261102.md):
  11 gaps (GAP-01 to GAP-07 RED on baseline), verified-sound areas, proven weak oracles
  (W1/W4/W5/W7 confirmed, W2/W10 refuted), ready-to-adopt test overlays and a resolution
  order. **No production or test code in this repository changed;** the only repository
  changes are this brief, the register and the documentation index link. Not run: `make check`,
  full suites, native Darwin, CI, live R2/installed/two-host checks.
- **Next:** with source-change authority, resolve GAP-01/GAP-02 first, adopting the
  E1/E6 acceptance tests from the private overlays, then the cap policies
  (GAP-04/05/07), then measure captures-per-session before GAP-03. Evidence is sealed
  (`evidence.sha256` in the private root); use a new root for later work.

## Gap resolution delivery — November 2

Operator request: drive Sonnet workers to resolve every register gap with tests;
strengthen property/mutation tests so they represent realistic session workloads; derive
settings from experiments on actual local and BC-01 session history. Parent owns this
brief, verification against code standards and original intent, integration and acceptance.

- **Authority:** source changes on new `gap/*` branches (worktrees under
  `/Users/mm/dev/memory-wt/gap-*`, base `2deac62`), published later as one `gh stack`.
  The operator's instruction authorizes **read-only, aggregate-only** reads of local
  (`~/.claude/projects`, `~/.codex/{sessions,archived_sessions}`, `~/.pi/agent/sessions`)
  and BC-01 session history plus read-only aggregate queries of installed queue
  databases. No transcript content, paths, project names or session IDs leave the reading
  process. Still excluded: live R2, installs, hooks/services, activation, refresh, release,
  reset/deletion, writes on BC-01.
- **Capabilities:** background workers with status/transcript/steering, at most three
  concurrent, `sonnet` at medium thinking. All builds/tests run through private root
  `~/.local/state/machines-validation-pilot/memory-delivery-20261102/` (snapshots taken
  from a worktree; local real-data jobs mount the session roots read-only with the network
  off, and their scratch volumes are purged afterwards). Smoke `setup-smoke-v1` passed.
- **Plan:** wave 1: data/workload measurement (D), liveness GAP-01/02/05 (L1),
  consumer GAP-07/08 (C). Wave 2: GAP-04/06/09/10/11 (L2) and test infrastructure
  (model/property/crash/fuzz/mutation, workload-shaped from D). Wave 3: GAP-03
  coalescing/cadence chosen from D's replay experiments. The parent runs canonical
  `make check` on the integrated stack.
- **Workers / corrections / acceptance:** recorded below as they occur.
- **D (data, `345c244b-a8cd-4c4`, completed):** private `reports/d-workload.md`,
  `data/d-summary.json` (aggregate-only; parent scanned for paths/IDs). Parent-verified
  against the installed Mac queue: jobs 20,308 published / 5 superseded, 19,413 runtime
  sources, `runtime_quarantine` 0 rows. **Correction:** D's "installed discovery never
  completed" holds only for the first two operating snapshots; Mac post-repair shows
  `CompletedSweep=8`, `DiscoveryComplete=false`, no error; BC-01 pre-final-repair shows 0
  sweeps, no error. Key inputs: amplification under today's full-object model is
  4.5–10× (hook per turn) up to 228–1294× (every append); max sources/session 379;
  max cumulative decoded 615 MB (one Codex artifact); BC-01 inventory 29,082 (89% of
  installed 32,768; default 4,096 unusable); no upload throughput was ever measured.
  Relayed to L1 (GAP-01/02/05) and C (GAP-07/08).
- **L1 (`975c9d90-0f8a-496`, `gap/liveness`):** RED proven (`l1-red-job` exit 1: E1, E5,
  E6 fail at intended assertions; controls pass). Acknowledged D's data: GAP-02 default
  body budget 30 s + size/1 MiB/s (floor 60 s, cap ≈15 min, idle timeout), marked
  conservative/unmeasured; transient/timeout get no attempt limit; 1024-source cap stays
  as a typed, visible, non-transient limit.
- **C (`97e003af-250e-49d`, `gap/consumer`):** RED proven (`c-red1`: A7 strict + three A8
  probes fail; A1–A6 pass). **Parent decision:** keep a cumulative total cap (default
  raised to 1 GiB, which clears the measured 615 MB maximum) with typed
  `export_too_large`, instead of strict A7. Conditions: named coverage gap in status and
  report, sibling isolation, import once the cap allows, boundary at cap and cap−1,
  honest test name, documented cumulative semantics, no silent reinterpretation of the
  installed `805306368` flag; partial latest-epoch import deferred until a real session
  approaches 1 GiB. Acknowledged; uptake verified in `dc2c68d`/`1638b31`.
  **Parent review correction:** legacy local-hash projections would have hit a false
  `publication_conflict`, and a pre-field reader would have failed as generic
  `invalid_metadata`. Fixed in `1638b31` (legacy-hash re-import adopts the remote digest;
  `reader_digest_missing`; coordinated-deployment docs). **Worker completed.** Evidence:
  `c-check3` (snapshot `c-11` = `1638b31`, no drift) fmt/lint/vet/race for publication, cli,
  cmd exit 0; `c-green5` full analytics 151 tests OK; Python hand-mutation 17/17 killed
  (`c-mut2`, before `1638b31`); scoped gremlins on `internal/publication` 88%, survivors in
  untouched `export.go:129-136`. **Parent: accepted for integration**, pending the
  integrated `make check` and the rebase onto the liveness/test-infra stack.
- **T (`4cc57302-d1f6-4b1`, `gap/test-infra`):** GAP-09 seam, GAP-10 open-time sweep,
  property/crash/privacy/fuzz/mutation tests. First check `t-gap9-10-a` failed lint only
  (`Open` gocognit 24 > 20); tests passed. Fixed (`t-gap9-10-b` exit 0); commits `8d924b1`
  (GAP-09/10), `81020ec` (crash matrix), `f612a58` (model history + privacy canary,
  `t-model-a` 40 histories 36 s race, 0 violations). Finding: a literal repeated CAS is
  impossible by design (lost CAS ACK is reconciled by read-back), so the floor asserts the
  already-applied path instead.
- **Shared files:** L1 and T both edit `quota.go`/`queue.go` (L1: `ErrSpoolFull` returns +
  `Status.CapturedAtNS`; T: free-space seam + Open sweep). Parent 3-way merge is clean.
  Integration order: `gap/liveness` → `gap/test-infra` (rebased) → `gap/consumer`. Both
  notified; T keeps `errors.Is(ErrQuota)` asserts until the rebase.
- **L1 correction:** `FailureQuota` conflated permanent limits with spool-full
  backpressure; contract §3 forbids quarantining on spool full. Steered to split them
  (`ErrSpoolFull` wraps `ErrQuota`, no budget), keep lost-ACK conflict reconciliation, and
  measure status-query cost at ~10k pending. Acknowledged; uptake verified in `cc96583`
  (GAP-01 commit; `l1-g1e-job` fmt/lint/vet/race exit 0; status over 10k pending 21.5 ms).
  **Second L1 correction:** enrichment (`finishEnrichment`) and optional repair
  (`saveOptionalRepairFailure`) still quarantined every class after `MaxAttempts`, which
  is the same outage defect. Steered to apply the deterministic-only rule there, with tests.
  Acknowledged; verified in `e4949da` (GAP-05 typed `source-limit` + GAP-01 optional-work
  follow-up; `l1-g5d-job` fmt/lint/vet/race exit 0).
  **Third L1 correction (parent EXPLAIN QUERY PLAN, 30k rows, 99.9% captured):** GAP-05
  added `source-limit` to the claimSource exclusion list but not to the partial index
  `runtime_runnable_sources`. The claim query then falls back from the index to a rowid
  range scan over settled rows, and `CREATE INDEX IF NOT EXISTS` would keep the stale index
  on installed queues. Steered to drop it and create a renamed v2 index with an identical
  predicate, plus a query-plan test and an old-DB reopen test. Verified in `c5e1108`:
  `DROP INDEX IF EXISTS runtime_runnable_sources` plus `runtime_runnable_sources_v2`, built
  from one shared `runnableSourcePredicate` const that `claimSourceSQL` also uses, with a
  query-plan test (`l1-g5j-job` exit 0). L2 adds partial indexes to the same InitRuntime
  list, so the parent resolves that at rebase.
- **L2 GAP-04 review:** live-only admission accepted. Steered away from evicting
  settled rows by id at the 4096 default: on the measured 19k/29k inventories that
  evicts and re-captures thousands of present sources each sweep. Required: a default
  settled retention above the measured inventories (justified by bytes per row),
  absent-first then least-recently-observed eviction, a visible eviction counter,
  exact per-sweep re-hint counts, and no eviction of claimed or quarantine-referenced rows.
  Uptake reported (`l2-green-g4e` exit 0). Settled retention defaults to 65,536, based on
  the installed Mac queue's ~3.8 KB per row including indexes (worst case ~250 MB). Eviction
  removes absent rows first, then the least recently observed, and runs once per discovery
  batch (48.6 ms to evict 1 of 65,537 rows; no-op check 3.75 ms). It skips claimed and
  quarantine-referenced rows, and `InventoryEvictions` appears in status. Not yet committed;
  the parent reviews the commit.
- **L1 GAP-02 review (in-tree diff):** accepted the size-aware `BodyBudget` (30 s +
  size/1 MiB/s, 60 s–15 min, 30 s idle; throughput unmeasured, configurable via
  `min_throughput_kib`), removal of `http.Client.Timeout` with the transport bounds kept,
  and the flat metadata/CAS budget. **Fourth L1 correction:** the delivery step budget was
  a flat ~16 min, but one step uploads every untrusted object in the manifest.
  Multi-large-object manifests would be cut off on every attempt, and on each retry the
  412 read-back costs a full download, so the job never publishes. Steered to make the
  step budget OperationTimeout + Σ Total(size) and to add a RED/GREEN 3-object test.
  Acknowledged by L1; uptake pending.
- **L2 GAP-04 committed `c7bcf85`** (`l2-check-g4f` fmt/lint/vet/race exit 0). The parent
  accepted the code. Follow-up requested: docs and comments said settled retention uses
  the configured cap, but the code uses the fixed 65,536 ceiling. Add `SettledCap` to
  status. Mutation run pending.
- **L2 discovery-timeout lead (unmeasured):** the installed policy callback rebuilds
  exclusion/policy state (`exclusion.Load`, `capturepolicy.NewSet`, `Resolve`) on every
  `allowed()` call, at least twice per entry. D's 1.4–2.4 s sweep used a no-op policy, so it
  did not exercise this. L2 is measuring it with the real CLI policy; a cached per-sweep
  snapshot must keep mid-sweep exclusions safe.
- **L2 discovery measurements:** `l2-m2` with the real CLI policy and an empty exclusion
  registry ran 0.69 s per 1365-entry call (~0.5 ms/entry), so the policy cost alone did not
  reproduce the timeout. Revised hypothesis: per-row autocommit admission under
  `synchronous=FULL` (tmpfs probes hide the fsync cost). The fix in progress is chunked
  `HintBatch` transactions. Parent required: on-disk timing, a check of whether the installed
  timeout snapshots predate fingerprint skip `4a1cfec`, a cause for `DiscoveryComplete=false`
  with 8 completed sweeps (suspected EntriesPerRoot truncation), and batch tests for
  per-request isolation, ownership in the same transaction, exact cap admission, and crash
  loss limited to uncommitted chunks.
- **L1 GAP-02 uptake:** `TransferTimeout` removed; a `publication.TransferBudgeter` optional
  interface; the per-attempt context is OperationTimeout + Σ budgets over selected objects
  (`TestManifestOfSlowObjectsPublishesInOneStep`). `l1-g2e-job` is running. The docs formula
  and RED proof are still pending.
- **L1 turn budget exhausted (first run):** committed `cc96583`, `e4949da` and `c5e1108`. GAP-02 is
  in the worktree but uncommitted. `l1-g2e-job` exit 1 in sessioncapture:
  `TestManifestOfSlowObjectsPublishesInOneStep` (suspected test setup: the head is not
  a 3-object manifest) and `TestShutdownDuringDependencyDecodeRestoresAttempts` (0 reads;
  possibly the per-attempt context wrapper). Resumed with orders to diagnose without
  weakening the shutdown test, then RED on the flat allowance, the docs formula
  `OperationTimeout + Σ 2·Total(size)`, the full check, the commit and scoped mutation.
- **L1 resume diagnosis (parent reviewed):** `TestShutdownDuringDependencyDecodeRestoresAttempts`
  observes cancellation through `tripCtx.Err()`. A `context.WithTimeout` child of a custom
  context only follows the parent's `Done`, not its `Err()`, so the trip never fired. L1
  wraps the attempt only when the backend reports a transfer budget and left the test
  unchanged. Accepted, because production cancellation still propagates through `Done`.
  Residual: under a budgeted (R2) backend the delivery step context has no timeout
  outside `attempt`; those DB reads are bounded by `busy_timeout(5000)`. `l1-g2f-job` is running.
- **L2 discovery root cause (measured, `l2-fsync2` exit 0):** the dominant cost is the
  per-new-row live-cap `COUNT(*)` in `Hint`, which is quadratic over a cold sweep:
  702 µs/row at 10k and 1685 µs/row at 30k (50.5 s for 30k, close to the 60 s budget).
  Counting slots once per 256-row chunk gives 77 and 69 µs/row (2.1 s at 30k). Batching
  fsyncs alone gave about 1.3×. These are Linux-VM figures; APFS is an estimate. L2 also
  corrected the parent's steering: ownership was never recorded in the upsert's
  transaction (it uses a separate transaction after admission), and `HintBatch` keeps
  that order. Accepted. `DiscoveryComplete=false` with 8 completed sweeps is, by code
  analysis, a snapshot taken during the next sweep (`prepareDiscoverySweep` resets
  `complete`); this is not measured. Pending: the batch semantics tests, `SettledCap`, and
  whether a root larger than `EntriesPerRoot` completes through cursor resumption.
- **L2 committed `cc54259`:** `HintBatch`/`admitChunk` (live count once per 256-row chunk;
  isolation, exact cap admission, crash at `runtime-hint-commit`, ownership after admission,
  write-free settled sweep, each tested) plus `SettledCap` and the docs fix
  (`l2-check-disc2` fmt/lint/vet/race exit 0). Remaining: GAP-11, GAP-06, the key-length
  check, scoped mutation, and the EntriesPerRoot cursor-completion test.
- **L1 GAP-02 committed `3ba5fc3`** (`l1-g2g-job` exit 0 across publication, publicationr2,
  capturebackend, cli, sessioncapture and cmd/agent-sessions). The parent reviewed and accepted
  it: the `publication.TransferBudgeter` optional interface, `2·Total(size)` per object (upload
  plus read-back), the lazily bound CLI destination forwarding `Spec.TransferBudget`, local
  backends adding nothing, and the docs formula. Minor drift risk: `Spec.TransferBudget` and
  `Backend.TransferBudget` each encode the factor of 2. Still pending: RED on the flat
  allowance and scoped mutation.
- **L2 GAP-11 committed `b821e31`** (`l2-check-g11b` exit 0). Accepted in direction. A
  follow-up was requested for three things: owned external SQLite artifacts
  (`ownership.go`) still fingerprint only when untransformed; a golden-digest guard that
  forces a `transformPolicyVersion` bump when scrub rules change; and a queue-level test
  with zero re-hints when unchanged and exactly one after a `-wal` change.
- **L2 GAP-11 follow-up `d499066`** (`l2-check-g11c` exit 0 for capturesource, scrub and
  sessioncapture): the owned-external fingerprint, `scrub.RulesDigest()` with a golden guard,
  and the queue-level settled/WAL re-hint test are accepted. Parent note: the claim that a new
  `rulesFor` case is caught is not true. The test checks only the listed names and a count,
  so a single-table fix or a reworded comment was requested. Scrub logic outside the rule
  tables still needs a manual version bump. L2 is now on GAP-06.
- **L1 GAP-02 RED:** `l1-red2-job` exit 0 exposed a test defect: the test bypassed the step
  context. It was fixed in `ff9ce7b` and now runs through `stepContext`. `l1-red3-job` with
  the flat-allowance shim exited 1 for the expected reason (`state=retry attempts=1
  last=timeout uploads=2`). The GREEN rerun (`l1-g2h-job`) and publicationr2 mutation
  (`l1-mut-r2-job`) are running.
- **T scoped mutation:** `t-mut-dry` exit 0 found 51 runnable quota.go mutants. A per-mutant
  driver (`t-mut-drv`, focused regex `Quota|Reserve|Sweep|Temporar|Spool`, unbuffered log) is
  running. It is reported as scoped mutation, not full-package gremlins.
- **L1 publicationr2 mutation** (`l1-mut-r2-job` exit 0, whole package): 137 killed, 22
  lived, 1 not covered, efficacy 86.16%. In `budget.go`, `150:9` lived (EOF disarm negation)
  and `54:71` (`TransferBudget` factor 2) is not covered in-package. Triage of the remaining
  lived mutants and the sessioncapture allowance mutation is still pending. The GREEN rerun
  `l1-g2h-job` exited 75 (slot refusal, no payload ran) and must be rerun.
- **L1 second run completed:** commits on `gap/liveness` are `cc96583`, `e4949da`, `c5e1108`,
  `3ba5fc3` and `ff9ce7b`. RED is valid (`l1-red3-job` exit 1, timeout reason). Still open:
  `ff9ce7b` (test-only) has not had a full check; the factor 2 in `TransferBudget` isn't
  tested directly and is duplicated in `capturebackend.Spec`; `budget.go:150:9` lived; and
  the sessioncapture mutation pass is outstanding. Resumed with orders to add the direct and
  cross-package budget tests, kill or justify 150:9, run one full check on HEAD, and run
  scoped per-mutant sessioncapture mutation of the GAP-01/02/05 functions. The 20
  pre-existing publicationr2 survivors are only to be listed, not triaged. GAP-02 defaults
  (1 MiB/s, 30 s base, 60 s–15 min, 30 s idle) remain unmeasured.
- **L2 `c7d79bd` (GAP-11 fixup), accepted:** `scrub.ruleTable` is the single table that both
  `rulesFor` and `RulesDigest` read, iterated in sorted order. The golden was regenerated
  because the digest algorithm changed; the rules did not. The comment states that
  `scrubDoc`/`IsSecretName` changes still need a manual bump of `transformPolicyVersion`.
- **L2 GAP-06 committed `26ba65c`** (`l2-check-g6c` exit 0 for sessioncapture, scrub and
  capturesource). Accepted by the parent. Remote-only sources pass through `recheckScopes`
  (claimed-session and publication-path exclusions) before any upload or CAS and fail closed
  with `ErrPolicy`+`ErrSessionExcluded`. There are 11 rebase tests. The steady state is one
  step per capture, measured at current=5 and opens=6/8/8 per delivery: every later delivery
  repeats Current+Read+merge, which is a bounded follow-up. Requested: phase3 docs, including
  which checks a remote-only source gets, and exact cost assertions. Scoped mutation
  `l2-mut-rebase` is running.
- **T final:** scoped quota.go mutation (`t-mut-drv`, focused regex, baseline 38.6 s, 193 s
  per-mutant timeout) hit its 3600 s deadline after 50 of 51 mutants. Of those 50, all were
  killed (21 by timeout) and 0 lived. The earlier survivors (`quota.go` MinFreeBytes
  arithmetic and `>`/`>=`) are now killed through the `freeBytes` seam. Mutant `26:28` never
  ran. `t-prop-count3` exit 0 (race, shuffle, -count=3, 204 s; TestModelHistories 34–36 s).
  Not done: the W2 barrier, the SIGQUIT dump, tier-1 mutation of publication/deliver, and the
  crash-matrix RED run on base.
- **Integration branch `gap/integration`** (worktree `memory-wt/gap-integration`), base
  `ff9ce7b` (L1), then T's 9 commits (applied cleanly), then L2's 6 commits through `26ba65c`.
  Parent conflict resolutions:
  - `runtime_store.go` index list keeps L1's `DROP INDEX` plus `runtime_runnable_sources_v2`
    and adds L2's live/settled partial indexes.
  - The Hint upsert takes L2's live-only count and L1's `source-limit` re-pend; this carried
    into L2's `upsertHintTail` const.
  - The W7 test was deleted, as L2 intended.
  Parent commit `669593f`: `rebase.go` keeps `ErrQuota` deliberately, because delivery-side
  `FailureSourceLimit` doesn't spend the quarantine budget and would retry indefinitely; the
  `FailureConflict` comment was updated. Snapshot `p-int-1`; focused check `p-int-check1` is
  running.
- **`p-int-check1` exit 0** (snapshot `p-int-1` = `669593f`, medium profile): fmt, lint
  (0 issues), vet and race/shuffle tests for sessioncapture (460.8 s), capturesource, scrub,
  publication, publicationr2, capturebackend, capturehooks, cli and cmd/agent-sessions. This
  is not the canonical `make check`, which runs once the final stack is integrated.
- **Later worker commits to cherry-pick onto integration:**
  - L1 `4d9b836`: TransferBudget factor table, idle-guard-disarmed test, and
    `Spec.TransferBudget` parity with R2 (`l1-g2j-job` exit 0).
  - L2 `c2dee0e`: phase3 rebase section; exact steady-state cost assertions
    (5/6/1/1, 5/8/2/1, 5/8/2/1); tests for the lived rebase mutants (`l2-check-g6d` exit 0).
    Rebase mutation: 33 killed, 5 lived, 2 not viable. The metadata-size boundary `76:12`
    is still untested; L2 says it is too expensive to test and gave no equivalence argument.
- **L1 scoped sessioncapture mutation** (`l1-mut-sc-job` exit 0; regex in
  `reports/l1-mutation.md`, baseline 39.3 s, 196 s per mutant): 12 killed, 3 lived, 1
  unmapped. No mutant lived in `spendsQuarantineBudget`, `classify`, `stepContext`,
  `transferAllowance` or the delivery allowance block. Lived: `runtime.go:168:43` (jitter
  `rand.Read` check, a real gap), `169:77` (equivalent: shifts the jitter edge by 2 ns),
  `321:41` (pre-existing `discoverLoop`). L1 was resumed to add the jitter test and rerun
  the mutation on `runtime.go` 160–175 and `budget.go` (to confirm `150:9` and `54:71`).
- **L2 `93b7462` (admission key length), accepted:** `ErrKeyLength` wraps `ErrQuota`, with
  an exact 432/433 boundary on both Hint and HintBatch. Requested follow-up: a cli parity
  test against `capturebackend.ValidateIdentity` (the 1024 rule is restated) and a
  document noting the R2 limit applies to every backend kind.
- **L2 final (`b0bf8eb`), accepted.** `l2-check-fin` exit 0 (sessioncapture, cli). It adds:
  - a cli parity test pinning admission key length to `capturebackend.ValidateIdentity`
    (sessions 431–434);
  - a document noting the R2 limit applies to every backend kind;
  - `TestDiscoveryResumesBoundedRootToCompletion`: 12 files, EntriesPerRoot=4, every
    intermediate batch reports `bounded` and Examined=4, the last batch is complete and all
    12 files are seen; no code change was needed;
  - an inclusive `MaxMetadata` rebase boundary test.
  `93b7462` and `b0bf8eb` are cherry-picked onto integration (`71ea144`, `eb40801`). L2 was
  resumed to rerun rebase mutation (five lived mutants have tests that are not yet confirmed)
  and to run first scoped mutation on GAP-04, GAP-11 and key length.
  Open, not proven: the installed discovery timeout is not confirmed against the installed
  binary, and the Mac cost is extrapolated from a Linux VM.
  **Deferred follow-up (recorded, not in scope):** after a rebase, every later delivery
  repeats Current+Read+merge (asserted at 5/6–8/1–2/1 calls). Folding the merged manifest
  into the local base would need local payloads for remote-only sources, which do not exist.
- **L1 `ed7e449` (GAP-01):** `TestDefaultRetryJitterIsAppliedWithinBounds`, `l1-g2k-job` exit
  0. Mutation reruns (`l1-mut-sc2-job`, then `budget.go`) are in progress.
- **L2 rebase mutation rerun** (`l2-mut-rb2` exit 0, baseline 4.3 s, timeout 60 s): all five
  previously lived positions (63:14, 76:12, 102:22, 105:23, 179:16) are now killed; 8 killed,
  0 lived. Scoped mutation on GAP-04, GAP-11 and key length is in progress (`l2-mut-store`).
  `ed7e449` is cherry-picked onto integration (`ad6969c`).
- **L1 closed (accepted).** `ed7e449` (`l1-g2k-job` exit 0) is on integration as `ad6969c`.
  Mutation reruns: `l1-mut-sc2-job` exit 0 (`runtime.go` 160–175: 10 killed, 1 lived
  `169:77`, which is equivalent because it shifts the jitter edge by 2 ns, and 1 unmapped);
  `l1-mut-r2b-job` exit 0 (`budget.go`: 22 killed, 0 lived). The targeted mutants
  `runtime.go:168:43`, `budget.go:150:9` and `budget.go:54:71` are now killed.
  Still recorded: `runtime.go:321:41` (pre-existing `discoverLoop`), and 21 pre-existing
  publicationr2 survivors in `backend.go`/`heads.go` (`reports/l1-mutation.md`).
  External blocker: the GAP-02 throughput defaults (1 MiB/s, 30 s base, 60 s–15 min, 30 s
  idle) can only be measured against live R2, which is excluded by authority. They stay
  conservative and unmeasured. With GAP-06, `conflict` now covers only irreconcilable
  divergence, so quarantine after `MaxAttempts` is the intended outcome, not an interim one.
- **G3 D1 interim review (uncommitted):**
  - `coalesce.go` `supersedeSubsumed`/`subsumes` runs inside the acceptance transaction. It
    considers only pending/retry jobs with no enrichment or optional-repair gap, and never
    proves withheld or optional sources.
  - Two existing tests were changed, and the parent accepts both edits:
    `TestEpochsAndRetainedPublication` now expects earlier jobs `superseded` and the final job
    `published`, with the root still holding all 3 epochs; the liveness subtest "newer job is
    not blocked" now expects job 1 `superseded` and job 2 `published`.
  - Steered to: add a non-subsumable sibling that preserves the original head-of-line intent;
    add the required in-flight supersede race test; comment the unmarshal skip; and before
    any reclamation code, verify the case of a job superseded locally whose CAS landed.
- **G3 D1 review fixes (uncommitted), accepted so far:**
  - There is a non-subsumable sibling liveness subtest (job 1 has an enrichment gap).
  - The race test asserts exact outcomes. At `current-before` the stale CAS lands at remote
    seq 1; job 1 stays `superseded`, and the final current is job 2's `PublicationID`.
  - `TestRebuiltSpoolRebasesAboveRemoteCurrent` now expects job 1 `superseded` and job 2
    published at seq 4 instead of 5. The remote-ahead intent is kept, along with the sealed
    remote and active local epoch assertions.
  - Perf fix: `pendingJobsIndexSQL` is now created in `initializeDB`, shared with
    `InitRuntime`. On first Open of a large legacy queue, `TestSourceLimit` went from 19 s to
    80 s; the cause was that supersession could not use the index. Integration note: this
    one-time `CREATE INDEX` cost on queues with about 30k jobs, and `rebase_test.go`
    overlapping with L2's `c2dee0e`, both need checking at cherry-pick.
- **L2 `runtime_store.go` scoped mutation** (`l2-mut-store`, baseline 6.1 s, timeout 60 s):
  - Equivalence arguments for `131:15` (`MaxMetadata` can't be reached under the 64 KiB
    hint cap), `151:21` (`slots==0`, same `ErrQuota`) and `325:171` (`LIMIT 0`).
  - `377:95` is killed by the new `TestClaimSourceRetryEqualToNowIsDue`.
  - The 16 timeouts are error-path flips in claim/delivery code outside GAP-04.
  - Key-length mutation `l2-mut-kl` is running.
- **L2 closed (accepted).** `a031a87` adds `TestClaimSourceRetryEqualToNowIsDue`
  (`l2-check-final2` exit 0 over sessioncapture, capturesource, scrub and cli). It is on
  integration as `f504c4f`. Scoped mutation (`reports/l2-mutation.md`, 60 s per-mutant floor):
  - `rebase.go`: 33 killed, 5 lived, 2 not viable; the rerun killed all 5 (8/8).
  - `fingerprint_transform.go`: 4 killed, 0 lived. `keylen.go`: 5 killed, 0 lived.
  - `scrub/digest.go`: no mutable operators (`l2-mut-dg` exit 1 is a tooling result, not
    RED).
  - `runtime_store.go`: the GAP-04 lived mutants are 1 killed and 3 argued equivalent
    (`131:15`, `151:21`, `325:171`); the parent accepts all three arguments. About 40 lived
    and 16 timeouts in claim/delivery/migration code from line 400 onward are pre-existing
    and outside GAP-04, so they go to the mutation-hardening worker.
  - `ownership.go` was not mutated (the change adds an `else` branch and no operators).
- **M (`0b03e7f1-9547-44d`, `gap/mutation` from `f504c4f`) dispatched for mutation
  hardening:** scoped per-mutant runs on `publication.go`/manifest validation, `deliver.go`,
  the 21 publicationr2 survivors and `runtime_store.go` claim code (line 400 onward).
  M must kill each lived mutant with an exact, workload-shaped test or argue equivalence.
  Production changes need parent approval. M must not touch G3's files.
- **Integration snapshot `p-int-2`** (`f504c4f`, adding L1 `ed7e449` and L2 `93b7462`,
  `b0bf8eb`, `a031a87`): focused check `p-int-check2` is running.
- **`p-int-check2` exit 0** (`f504c4f`, medium profile; same 9 packages; sessioncapture
  500.2 s).
- **G3 D1 `6cf2b41` accepted** (RED `g3-red2-job` exit 1, GREEN `g3-grn1-job` exit 0, check
  `g3-chk4-job` exit 0). The parent verified that all 8 changed files are byte-identical to
  the checked snapshot `g3-s5`. It is on integration as `1380f1d` (clean). The D2
  reclamation design is accepted: refs kept by triggers in the same transaction, pins for
  in-flight work, and for superseded-but-landed jobs no local payload need remains after
  N+1 (grep-verified by G3).
  **Risk:** sessioncapture race wall time is trending toward the 600 s package timeout
  (461 → 500 → 533 s). G3 is held to ≤500 s on its branch, and must fix the cause of any
  regression in production code rather than relax the timeout or shrink tests.
- **M `1b3c5e2` accepted** (test-only; `m-chk2` exit 0). Before these tests, the scoped
  `publication.go` mutation (`m-pub1` exit 0) gave 145 mutants: 124 killed, 11 lived, 9 not
  viable, 1 unmapped. The new tests pin exact behaviour at these boundaries: metadata limits
  (exactly `MaxMetadata` vs +1), the object descriptor `MaxInt64`/negative sizes, the
  `advances` sequence and ID rule, and Read resolve checks (with exact error strings). The
  commit is on integration as `8fae9ce`. Confirmation rerun `m-pub2` is running.
- **M `publication.go` confirmation** (`m-pub2` exit 0): lived count went from 11 to 1. The
  remaining `494:15` is argued equivalent in `reports/m-mutation.md`. The `deliver.go`
  baseline `m-del1` is running.
- **G3 D2 interim review (uncommitted `reclaim.go`):**
  - Accepted: the candidate commits in the same transaction as the supersede or ack; the root
    check and unlink happen under the install mutex; ENOENT counts as success; Open drains
    candidates before accounting; `staleAttempt`; `ReclaimPending`/`ReclaimFailed` in status.
  - Steered on five points:
    1. Reclaim errors after a durable accept or ack must not be reported as the capture or
       delivery failing.
    2. `payloadKept` uses `instr` scans over enrichment and optional-repair bodies. The
       installed Mac queue has 4096 rows (9.2 MB) and 1534 repairs. Use indexed refs, or
       measure the cost; also measure the `initReclaim` backfill at about 30k jobs.
    3. Optional-detachment candidates must not destroy unique, never-published bytes without
       a contract justification.
    4. Exact drain oracle: spool file set == root-referenced objects, which detects both
       over-deletion and leaks.
    5. Exact `cmp` in `TestSealedEpochPayloadsAreRetained`.
- **G3 D2 `g3-chk5-job` exit 1. This is a D2 safety defect, so D2 is not committed:**
  - Over-deletion: `TestRawAcceptanceNeverChecksOptionalPolicyAndFullWorkAdmission` fails
    with `publication object not found`, meaning a payload a live view still references was
    reclaimed.
  - Livelock: `TestOptionalRepairQuotaTerminalReserveKeepsRawPublishing/bytes` hung for
    9m18s. Delivery repeatedly goes `terminalOptionalQuota` → `supersedeOptional` →
    `acceptView`/`retireJobs` and never converges.
  - The sessioncapture and cli packages hit the 600 s timeout, and the cli isolation fixtures
    were killed at 15 s.
  G3 was steered to write a root cause for each first, then fix the cause in production
  code with a test that fails on the current tree, with no timeout changes. D2 is held until
  the exact drain oracle passes.
- **G3 D2 diagnosis:**
  - (B) livelock root cause accepted: G3's own De Morgan lint rewrite in `retireJobs` mixed
    `&&`/`||`, so selection supersession stopped superseding every undelivered job, and each
    terminal-quota supersede added a new pending job that delivery picked up forever. Fixed by
    a `retirable()` helper. `TestSelectionSupersessionSupersedesEveryUndeliveredJob` fails on
    the old tree; `g3-dg3-job` exit 0 (the 9-minute hang now takes 1.9 s).
  - (A) over-deletion: probable cause, not yet proven. Selection supersession created
    candidates for objects the new view holds only as `Withheld` descriptors, and the root
    check scanned only public manifests. Selection supersession now creates no candidates
    (unique local bytes kept). Still required: a RED on `g3-s11`, roots counting evidence
    `Original` objects, full check-v and the exact drain oracle, which names the retained
    selection-superseded class.
  - Housekeeping: a stray empty `zz_diag_test.go` is staged in G3's index; G3 was told to
    unstage it before committing.
- **M `deliver.go`:** `m-del1` hit the 1800 s deadline (exit 124, tooling), so M split the run
  by line range. `m-del2` (lines 248–340) exit 0: 7 killed, 5 lived. `m-del1`'s partial log
  shows `135:17` lived, plus error-path timeouts. `m-del3` (341–432) is running.
- **G3 D2 `g3-chk6-job` exit 1, two further defects found and diagnosed:**
  - (C) Performance: `TestSourceLimit` took 351.9 s (base 19 s, after D1 80 s), because
    `recordPayloadRefs` inserted one ref per source on every acceptance (about 1M
    statements at 1024 sources). Fix: record only the freshly installed object.
  - (D) `staleAttempt` hid an injected failure in the existing
    `TestDeliveryFailureAfterSupersedeCannotReviveJob/immediate`. Fix: only an `ErrIntegrity`
    failure of a superseded job counts as stale.
  - Also in progress: post-commit reclaim made best-effort, enrichment/repair holders indexed
    by triggers (no body scans), exact `cmp` in the sealed test. `g3-chk8-job` is running
    (`chk7` was a compile failure, not evidence). (A) still needs its RED proof.
- **M `deliver.go`:** `m-del3` (lines 341–432) exit 0: 21 killed, 3 timeouts, 0 lived. Lived
  total for the file: `135:17`, `274:42`, `289:11`, `296:43`, `314:42`, `338:8`. M is writing
  tests for them.
- **M `f95ab0c` accepted** (test-only `deliver_mutation_test.go`; `m-chk3` exit 0,
  sessioncapture 487.2 s). It uses a multi-source claude-shaped fixture: a rewritten main
  transcript with a sealed epoch, two subagents and an optional artifact. Tests pin:
  - the per-source policy recheck in check/upload when the scope pass succeeds;
  - the evidence-count mismatch giving `ErrIntegrity`;
  - the optional-unavailable latch, per repair state;
  - a missing evidence slot failing closed;
  - cancellation being reported when the job is superseded at cancel.
  On integration as `d2d7d5e`. Confirmation reruns (`m-del4`, `m-del5`) are running.
- **G3 D2 `g3-chk8-job` exit 1, further root causes:**
  - (E) G3's scripted edit had deleted the original superseded→`errSelectionSuperseded`
    block in `attemptManifest`. It is restored verbatim.
  - (F) A stale job's payload reclaimed mid-attempt surfaces as `fs.ErrNotExist`; this is
    now treated as stale only for a superseded job.
  - `TestSourceLimit` is still 276 s (base 19 s); G3 is profiling it (`g3-sl2-job`).
  D2 is still held.
- **G3 timing correction (measured):** under `-race`, `TestSourceLimit` takes 258 s on the
  pre-GAP-03 snapshot (`g3-sl2-job`) and 299 s on `g3-s18` (`g3-sl3-job`). The earlier
  19 s/80 s figures were non-race. D1+D2 add about 40 s under race, from the per-capture
  `retireJobs` scan over 1024-source manifests; G3 is reducing it. `g3-s18` passes the
  (E)/(F) regressions. Full check `g3-chk9-job` is running. Still open before the D2 commit:
  the (A) RED proof, evidence-`Original` roots, docs.
- **M `deliver.go` confirmed:** `m-del4`/`m-del5` exit 0, lived 6 → 0. Error-path loop
  timeouts are listed in `reports/m-mutation.md`. M has moved to the publicationr2
  `backend.go`/`heads.go` survivors (`m-r2a`).
- **G3 D2 `e3ee9db` accepted** (check `g3-chk11-job` exit 0, sessioncapture 489.5 s; targeted
  `g3-t1-job` and `g3-dg3-job` exit 0). The parent verified that all 12 files are
  byte-identical to the checked snapshot `g3-s21` and that no diagnostic file is staged.
  - Root cause of the (A) over-deletion: the `retireJobs` precedence bug made selection
    supersession create candidates for objects held only as `Withheld`.
  - Selection supersession now creates no candidates; those optional artifacts and raw
    companions are a named, retained class of unique local bytes.
  - `retiredObjects` skips Optional sources as defence in depth.
  - Enrichment and repair holders are indexed `payload_refs` rows maintained by triggers,
    so there are no body scans.
  - Post-commit reclaim is best-effort: a failure leaves the candidate with `last_error`,
    visible as `ReclaimPending`/`ReclaimFailed`, retried on the next pass or Open, and it is
    tested exactly.
  - `staleAttempt` covers only `ErrIntegrity`/`ErrNotExist` failures of superseded jobs.
  - The fault points `payload-accept-before` and `reclaim-before`/`after-unlink` are in the
    crash matrix.
  - Under race, `TestSourceLimit` went from 258 s to about 277 s.
  On integration as `3c4ef7b`. Open in later G3 deliverables: measure the `initReclaim`
  backfill at about 30k jobs, confirm there is no delete/un-repair path that bypasses the
  holder triggers, and add the exact spool oracle to the model-history property.
  Snapshot `p-int-3`; combined check `p-int-check3` is running.
- **`p-int-check3` (`3c4ef7b`, snapshot `p-int-3`) exit 0 across all 9 packages.**
  sessioncapture took 523.8 s, which is over the 500 s target and leaves 76 s before the
  fixed 600 s timeout. The trend is 461 → 500 → 523.8 s. The timeout is not relaxed. The
  verbose per-test measurement `p-int-v3` is running to attribute the time.
- **G3 resumed** for D2 follow-ups: (A) RED on `g3-s11`, scoped mutation of
  `reclaim.go`/`coalesce.go`, `initReclaim` backfill at about 30k jobs, and an audit of
  delete/un-repair paths. D3–D5 follow.
- **BLOCKER: the sessioncapture race time is at the 600 s timeout on integration.** `p-int-v3`
  (`-v`, snapshot `p-int-3`, 3 containers running) timed out at 600 s. Slowest tests:
  `TestSourceLimitIsTypedAndNotQuarantinedAsTransient` 305 s, which was already 258 s under
  race before GAP-03 because it does 1030 rewrite captures that grow to 1024 sources;
  `TestModelHistories` 45 s; `TestCrashProcess` 21.8 s. The package has no `t.Parallel()`.
  Worker PF (`gap/perf` at `f17f37b`, prefix `pf-`) was dispatched to profile first, then
  prefer production per-capture fixes (real sessions reach 379 sources). Test-structure
  changes are allowed only for the pre-existing quadratic cost, with identical assertions and
  a written justification. Target ≤420 s under contention. The timeout stays fixed.
- **G3 follow-ups accepted:**
  - `dece4d0` is a guard test only, not a cause. `g3-rc1`/`g3-rb1` show that (A) was the
    same precedence livelock: all payloads were present and no current pointer was ever
    published, so nothing was over-deleted.
  - `0443bc6` closes a real hole: `ReplayOptionalRepair` re-pended a repaired row after
    its holder was released, so a payload could be reclaimed while a replay needed it.
    Fixed by a `payload_rehold_repair` trigger. RED `g3-rd1-job` exit 1, GREEN
    `g3-rd2-job` exit 0.
  - Grep found no DELETE paths on `optional_repairs` or `enrichment_results`.
  On integration as `80874b5` and `f17f37b`.
- **G3 D2 follow-ups (in progress):**
  - `initReclaim` backfill measured (`g3-bf1-job`, exit 0, non-race): a synthetic queue of
    30,000 jobs (12–41 sources per job, every 3000th job with 1024 sources, 254,600 refs)
    takes 2.80 s in one transaction, once. A second Open takes 215 µs. That is about 4.7% of
    the 60 s operation budget for a one-time cost, so no chunking was added. Accepted.
  - Scoped mutation:
    - `reclaim.go` (`g3-mutr-job`): 31 mutants, 20 killed, 6 lived, 4 timeout.
    - `coalesce.go` (`g3-mutc-job`): 25 mutants, 23 killed, 2 lived.
    - Tests for the lived mutants are being written; reruns `g3-mutr2`/`g3-mutc2` are running.
- **PF race profile (`pf-prof2`, `TestSourceLimit` alone, race):** the test took 275.4 s.
  Cumulative time:
  - `Capture` 99.7 s;
  - `acceptView` 55.6 s;
  - `latestPolicyChecked` 41.1 s (`latest()` 22.1 s);
  - `encodeAcceptedView` 27.7 s (JSON Marshal 25.6 s);
  - `retireJobs` 19.4 s;
  - `checkObserved` 19.0 s;
  - `ValidateManifest` 12.8 s.
  Per-capture manifest decode and encode dominate.
- **M `3404cbe` accepted** (test-only `publicationr2/mutation_boundary_test.go`; `m-chk5`
  exit 0, publicationr2 67.1 s). It pins:
  - the key-length boundary 1023/1024/1025, using a sweep across kinds;
  - the 4xx class boundary (399/400/499/500);
  - Open/Current declared-length checks, and closing the body of an invalid response;
  - redaction of stream-read failures;
  - `validToken` printable/length edges;
  - the CAS body-limit boundary;
  - PutFile size edges;
  - idle-guard stall detection for upload and reconcile read-back;
  - list status/size bounds and namespace-listing advancement.
  The two stall tests assert an upper wall-clock bound of 4 s against a 200 ms idle guard.
  This is accepted as detection-latency evidence, not a relaxed timeout. On integration as
  `9399ead`. Scoped confirmation reruns of `backend.go`/`heads.go` (`m-r2d`) are running.
- **G3 scoped mutation round 2 (`g3-s25`):** `reclaim.go` 31 mutants: 26 killed, 0 lived,
  4 timeout (`err != nil` → `== nil` after Query/Exec hangs; counted separately, not as
  kills), 1 not viable. `coalesce.go`: 25/25 killed. Tests for the round-1 lived mutants
  (cross-session shared payload, rooted candidate, multi-object candidates, per-candidate
  error, legacy backfill, multi-job retire) are awaiting `g3-chk12-job` before commit.
- **G3 `fcfad75` accepted** (test-only; `g3-chk12-job` exit 0). It kills the round-1 survivors:
  - a payload shared across sessions survives one session retiring it, and B still delivers;
  - a rooted candidate is kept and its row dropped;
  - every object becomes a candidate, and every failed candidate keeps its own error;
  - the legacy `payload_refs` backfill works;
  - multi-job retire only touches plain states.
  Final scoped results: `reclaim.go` 26 killed, 0 lived, 4 timeout, 1 not viable;
  `coalesce.go` 25/25 killed. sessioncapture took 503.2 s on G3's branch, 3 s over the 500 s
  target (the tree varies 489–503 s between runs). This feeds the PF blocker. Slowest:
  `TestSourceLimit` 270.6 s, `TestModelHistories` 34.8 s, `TestCrashProcess` 14.3 s.
- **M `d537a16` accepted** (test-only; `m-chk6` exit 0). It pins `New` with a positive
  timeout, read-back close failure as an error, and `start-after` being sent only with a
  cursor. `m-r2e`/`m-r2f`/`m-r2g` reruns are pending.
- On integration as the cherry-picks of `fcfad75` and `d537a16`.
- **G3 D3 (cadence) in progress:**
  - Design: `dirty_since`/`debounce_until` columns, due time = min(now+Q, dirty_since+M),
    `ErrCadence` validation, debounce flush at Run start, and Status
    `DebouncedSources`/`DebounceLagNS`.
  - Config: `quiet_seconds`/`max_delay_seconds`. Defaults stay 0 until the D4 report.
  - Targeted `g3-d3b-job` exit 0; full check `chk13` pending.
- **PF profile done; first pass ran out of turns with no fix, so the blocker is still open.**
  Race profile of `TestSourceLimit` (`pf-prof2`, 265.8 s of samples):
  - `acceptView` 55.6 s, which includes:
    - `encodeAcceptedView` 27.7 s (about 4 manifest marshals per accept, plus
      `ValidateManifest`);
    - `retireJobs` 19.4 s (`undeliveredJobs` unmarshals every undelivered manifest,
      12.2 s).
  - `latestPolicyChecked` 41.1 s, which includes:
    - `latest()` decode 22.1 s;
    - per-source `checkObserved` 19.0 s.
  - Status from the test's final check: 12.0 s.
  Non-race the test takes 29.1 s; race instrumentation inflates the JSON reflection.
  PF was resumed at integration `95acce8` to implement, in order:
  1. a lazy manifest parse in `undeliveredJobs`;
  2. a single encode reused for the `MaxMetadata` check and export, with an
     accept-path boundary test;
  3. optionally, Status column selection;
  4. optionally, an invariant policy hoist. The policy is still evaluated for every source.
  Each fix gets scoped mutation. Target ≤420 s.
- **G3 D3 `chk13` exit 1:** a lint finding, and `TestRunnableSourceIndexMatchesClaimQuery`
  lacked a bind argument after `claimSourceSQL` gained a `debounce_until` placeholder.
  Changing that existing test from 2 to 3 bind args is accepted: the plan assertion
  (`runtime_runnable_sources_v2`) is unchanged. `g3-d3c-job` exit 0; `chk14` is running. In
  that run sessioncapture took 509 s; PF's fixes should restore the margin.
- **G3 D3 `c2ec0a3` accepted** (`g3-chk14-job` exit 0; the parent verified all 9 files are
  byte-identical to `g3-s31`). What it does:
  - Hints set `dirty_since` (keeping the first unconsumed observation) and
    `debounce_until = min(now+Q, dirty_since+M)`.
  - The claim query filters on `debounce_until<=now`.
  - Completion clears `dirty_since` only when the captured version is the claimed version;
    otherwise it is reset to now.
  - Run validates the settings (`ErrCadence`: non-negative, Q ≤ 1 h, M ≤ 24 h, M ≥ Q when
    Q > 0) and flushes the debounce at start, because observations are durable.
  - Status reports `DebouncedSources`/`DebounceLagNS`, and config adds
    `quiet_seconds`/`max_delay_seconds`. Defaults stay 0 (capture immediately) until D4.
  - Tests: exact Q/M release on a deterministic clock (hints at 0/4/8/11 s; released at
    12 s by M), lag of exactly 11 s, and immediate claim with zero Q.
  On integration as `f17d420`. sessioncapture took 525.6 s on G3's branch, which falls
  under the PF blocker.
- **PF fix 2 `2ac2951` (pending its full check):** `undeliveredJobs` fetches and parses the
  manifest only for plain jobs during coalescing (the SQL `CASE` matches the Go `plain`
  predicate). `TestRetireJobsKeepsUnparsablePlainJob` checks exact state counts. Focused
  run `pf-f3` exit 0.
- **M:** all 21 publicationr2 survivors are killed (`m-r2c`..`m-r2j` exit 0, 4 hang-type
  timeouts recorded). M is now on `runtime_store.go` lines 400 onward (`m-rs1`/`m-rs2`).
- **PF result: production fixes alone can't clear the blocker.** Under race, `TestSourceLimit`
  went from 275.4 s to 269.3 s with fix 2 (`pf-t1`) and to 263.4 s with fix 1 as well
  (`pf-fx1`, draft). That is about 12 s, or 4%. The remaining cost is spread across
  per-capture `latest()` decode, per-source policy checks, which can't be cached, and JSON
  reflection under tsan.
- **Parent decisions:**
  - **Fix 1 API:** fix 1 becomes `publication.MarshalValidManifest`, which marshals once
    and validates and returns those exact bytes. That replaces the draft's
    caller-trusted `ValidateManifestEncoded`. A table test checks equivalence with
    `ValidateManifest`/`json.Marshal` at the `envelopeFixture` −1/0/+1 boundary.
  - **Accept-path boundary:** PF's finding that the export envelope always binds first on
    the accept path is accepted, so there is no unreachable accept-path boundary test.
  - **Parallel tests:** option 3a is authorised. A parallel top-level test runs only
    alongside other parallel tests, after the serial ones finish. So `TestSourceLimit` is
    marked parallel together with other audited CPU-heavy, self-contained tests. Excluded:
    tests with package globals or hooks, env/cwd/umask changes, wall-clock or latency
    assertions, or crash subprocesses. Assertions stay unchanged.
  - **Acceptance:** sessioncapture ≤420 s on check-v, two independent green runs, and the
    full check.
- **G3 D4 (pending review):** `reports/g3-cadence.md` gives defaults Q=30 s and M=300 s, wired
  as daemon config with nil meaning default and 0 meaning disabled. `RuntimeOptions` zero
  stays immediate. Check `chk15` is running.
- **G3 D4 report reviewed (`reports/g3-cadence.md`, SCOPED simulation):**
  - Replay of D's 128 anonymised timelines (78 Mac, 50 BC-01; claude, codex and pi). Sweep
    Q ∈ {0…300} s, M ∈ {60…1800} s.
  - Stated rule: pick the smallest Q that cuts pooled p99 captures per session by at least
    70%, then the smallest M that bounds worst-case lag to about 5 minutes. That gives
    Q=30 s, M=300 s: p99 captures 509 → 142 (−72%), uploaded bytes −77%, PUTs −82%, lag
    p50 61 s, p99 277 s, max 301 s.
  - This agrees with D's independent recommendation of a quiescence debounce of at least
    30 s.
  - Limitations are stated: downsampled traces, append-only, one source per session, and
    no throughput data. The defaults rule is accepted on that basis.
  - Wiring: daemon config `*int`, where nil means default and 0 disables. `RuntimeOptions`
    zero stays immediate.
  - `chk15` exit 1: two cli tests assumed prompt capture with the new 30 s daemon default.
    The cli test-config helper now sets 0/0 explicitly. This change to an existing test is
    accepted: those tests assert capture behaviour, not cadence, and
    `TestCadenceDefaultsAndOverrides` pins the default. `g3-d4b-job` exit 0; `chk16` is
    running.
- **PF fix 1 revised** to `MarshalValidManifest` with an equivalence table test. `pf-fx2`:
  fmt, lint and vet clean, focused tests pass. Scoped mutation `pf-mut2` is running.
- **Harness incident, 2026-11-03 about 02:08: the Docker VM disk filled up** (162 MB free
  of 125 GB). 229 preserved scratch volumes from this delivery held 79.8 GB.
  - Invalid runs: `m-chk7` and `g3-chk17-job` both show ENOSPC and are not evidence.
    `pf-v1` (its `TestSourceLimit` stopped at 992 sources on a non-limit error) ended in
    the same minute and is treated as invalid.
  - Clean-up: the parent removed only idle volumes and stopped containers owned by this
    delivery (`machines.validation.owner=memory-delivery-20261102`, no running mounts).
    Volumes of other deliveries were not touched. 76.7 GB is now free.
  - Fix: `launch.py` now purges each job's scratch volume at job end (backup
    `launch.py.bak-20261103`). Evidence stays in `jobs/<job>/out`.
- **Accepted and on integration:**
  - G3 D4 `6d78c2f` → `db71c21` (`g3-chk16-job` exit 0; files byte-identical to `g3-s35`).
    `TestCadenceDefaultsAndOverrides` pins 30/300, disabled 0/0 and partial overrides.
  - G3 D5 `bddc3f2` → `9c7d866`: the model-history drain oracle. Roots are always a subset
    of the spool; spool equals roots exactly when no fault fired and no ack was lost; CAS
    writes ≤ jobs + republishes. GREEN `g3-m1-job` exit 0. RED by mutation `g3-m2-job`
    exit 1 (`RECLAIM`, with the unlink disabled).
  - G3 D5 `04039ec` → `85dbc5f`: PUT/CAS count property test, 12 seeds × 30 steps, exact
    1/1/1 per pending delivery and 0 otherwise. `g3-p1-job` exit 0.
  - PF `2ac2951` → `76a8ab5`. The cherry-pick conflicted only because both sides
    appended a test at the end of `coalesce_test.go`; the parent kept both.
  - PF `e99993a` → `f25e6b3`: `MarshalValidManifest` with an equivalence table test.
    `pf-mut3` killed 5 of 5 scoped mutants.
- **PF parallel option 3a:** the audit found 5 eligible tests (`TestSourceLimit`,
  `TestModelHistories`, the two inventory ceiling tests, `TestRestartAndLostAck`).
  `worker.sh` sets `GOMAXPROCS=1` for ad-hoc jobs, while the canonical gate
  (`canonical-check.sh`) exports `GOMAXPROCS=2`. PF will measure baseline `pf-base2` and
  parallel `pf-par1`/`pf-par2` under `GOMAXPROCS=2` on `f25e6b3`, then the full check.
  Commit only if every run is green and the parallel tree is ≤420 s.
- **`pf-base2` measured** (`f25e6b3`, no parallel, `GOMAXPROCS=2`, medium): sessioncapture
  ok in 515.7 s. That is over 420 s, so 3a is needed. CHECK_RC=1 came only from the
  `sqlclosecheck` lint in G3's model oracle. Slowest: `TestSourceLimit` 255.1 s,
  `TestModelHistories` 37.1 s, `TestCrashProcess` 15.7 s. `pf-par1` (3a) is running. PF
  was told to move 3a onto `37e11fa` before `pf-par2` and the full check.
- **G3 `e8a6310` accepted** (test-only lint fix: the oracle's root query is extracted to
  `rootFiles` and closed with defer; `g3-chk17b-job` exit 0, sessioncapture 534.8 s). On
  integration as `37e11fa`. G3's cadence.go scoped mutation (`g3-mutd2`): 16 mutants, 13
  killed, 3 lived. `36` (`>`→`>=`) is argued equivalent. The two default-constant mutants
  get `TestDefaultCadenceValues`; rerun `g3-mutd3` is running.
- **M:** `m-chk7` is invalid (ENOSPC). It is being rerun as `m-chk8` for the
  `runtime_store_claim_test.go` survivors (`479:28`, `569:8`, `620:37`).
- **`pf-par1`** (`f25e6b3` + 3a on 5 tests, `GOMAXPROCS=2`): sessioncapture ok in 483.2 s,
  against 515.7 s for the baseline. That is short of 420 s, because parallel tests resume
  only after all serial tests finish: the serial part is about 210 s, plus
  `TestSourceLimit` at 272 s. 3a is not committed.
  - The GAP-03 production slowdown is already recovered: `TestSourceLimit` is 255.1 s on
    `pf-base2`, against 258 s on the pre-GAP-03 base. The rest of the growth is new tests.
  - Parent decision: a broader parallel audit (option 1) on `7372f16`. Every candidate test
    is audited per test for globals, env/cwd, process-wide state, subprocesses and
    wall-clock assertions. Optionally, `TestSourceLimit`'s final Status read can be replaced
    by a direct query asserting the same publication-id fact.
  - Acceptance: `pf-par3`/`pf-par4` ≤420 s and lint clean, plus the full `pf-full2`.
    Option 3b (database seeding) stays ruled out.
- **Accepted and on integration:**
  - G3 `5b39ebd` → `094a4b8`: `TestDefaultCadenceValues`. cadence.go scoped mutation
    `g3-mutd3`: 15 killed, 1 lived, which is equivalent (`36` `>`→`>=`).
  - M `747b843` → `7372f16` (test-only `runtime_store_claim_test.go`; `m-chk8` exit 0,
    sessioncapture 465.5 s). It pins:
    - `sourceCapturedAt` per-source selection;
    - the `dueIdentity` boundary at ±1 ns and for a quarantined job;
    - the deferred claim release when clearing the claim fails.
- **PF broad parallel set (75 tests, `pf-par3` 428.3 s, lint clean) is not accepted.** The
  parent traced each test through its helpers and found that PF's screen missed helper calls
  and `time.After` real-time deadlines. Nine tests would flake or weaken an assertion under
  parallel CPU contention, so they go back to serial:
  - `TestObjectLongerThanOperationBudgetPublishes`: throttled real-time backend via
    `runThrottledDelivery`;
  - `TestOptionalNetworkStall…`: 2 s;
  - `TestOptionalRepairQuotaTerminalReserve…`: a 30 ms window that passes without checking
    anything under load;
  - `TestVerifiedOptionalRepairRestart…`: 3 s;
  - `TestRuntimeDiscoveryExpiredCallback…`: 2 s;
  - `TestRuntimeDiscoveryHintRestartAndNonceSweepFence`: 3 s;
  - both `testRuntimeNetworkRetryRawACK` users: 5 s;
  - `TestRuntimeFatalOptionalBoundaries`: 5 s.
  `TestRuntimeDeliveryProgressIdleScheduling` stays parallel because it runs on synctest's
  virtual time. PF must redo the audit through helpers and may add eligible tests under
  0.5 s. Acceptance is unchanged: `pf-par5`/`pf-par6` ≤420 s and lint clean, plus
  `pf-full3`. If the honest eligible set can't reach 420 s, PF reports and the parent
  decides; the eligibility rules are not stretched. The step-3 query, which asserts the
  identity, sequence and `PublicationID` of `last.Sequence`, is accepted as its own commit.
  `pf-par4` is superseded.
- **Integration HEAD `1dc9fe2` (snapshot `p-int-4`, 716 files)** now holds every accepted
  commit:
  - PF `f3b7ce6` → `9e0e8b7` (the step-3 direct query) and `e2f0403` → `7ad1703`
    (71 `t.Parallel()` lines, 0 other lines). Both are byte-identical to the green snapshot
    `pf-p5`. Evidence: `pf-par5` 377.6 s and `pf-par6` 408.2 s, both lint clean;
    `pf-full3` exit 0 across all 9 packages (sessioncapture 384.5 s). The ≤420 s target
    is met with the 600 s timeout unchanged; the baseline was 515.7 s.
  - C `be3b1b3` → `ca489a7`, `dc2c68d` → `802519b`, `1638b31` → `1dc9fe2` (GAP-07/08).
    One mechanical conflict in `ValidateManifest`: PF's `MarshalValidManifest` form
    `(data, err)` was combined with C's fixed-width digest-placeholder argument to
    `exportMetadata`. No other stale call sites.
  - `p-int-canon4` is **invalid as evidence**. It was a harness invocation error: it was
    launched without `--coverage-output`, so `go tool covdata` could not write `cover.out`
    into the read-only `/source`, and `cover` exited 2 (Makefile:75). Every Go package had
    already passed (sessioncapture 403.1 s, syncer 272.7 s). It is rerun as
    `p-int-canon5` with the flags of the previous green canonical run
    (`--profile large --deadline 1800 --seed-cache --coverage-output`).
  - `p-int-canon5` **exit 2 (real regression, not harness)**. Every gate before
    `capture-acceptance` passed, including cover/crap (sessioncapture 354.1 s). Two
    `test_capture_journey.py` journeys failed with `coordinator acceptance deadline
    exceeded` (the 45 s `until()`). Cause: G3 D4's daemon defaults Q=30 s and M=300 s
    apply to the real binary because the journey configs omit `quiet_seconds` and
    `max_delay_seconds`; with `debounce_until = now+Q`, each chained capture waits at
    least 30 s. The `candidate-live-r2` and `capture-delivery` suites did not run because
    make stopped first.
  - G3's session had expired, so the fix went to a fresh worker J (`999ab41c-86fb-4da`,
    prefix `j-`, progress `reports/j-progress.md`) on `gap/journey` (from `1dc9fe2`).
  - **Accepted:** J `08de567` → integration `1150db5` (4 test files, no Go, byte-identical to
    snapshot `j-1`). Explicit `quiet_seconds=0, max_delay_seconds=0` in all four
    `capture-runtime/v1` configs under `tools/`; assertions and deadlines unchanged. Only the
    `CoordinatorJourney` config is proven necessary; the other three are precautionary.
    New `test_configured_cadence_debounces_hinted_source_in_actual_binary` (Q=2 s, M=4 s):
    not-before-Q lower bound, then capture within the existing `until()`. RED `j-one-red`
    (runtime forced to Q=M=0: captured before Q, `ONE_RC=1`), GREEN `j-one1`.
    `j-ca1` `make capture-acceptance` `CA_RC=0`: analytics 13 OK, candidate-live-r2 6 OK,
    capture-delivery 12 OK (1 skip guarded by `CAPTURE_V1_REPAIR_BINARY`).
  - **Canonical `make check` green:** `p-int-canon6` on snapshot `p-int-5` (HEAD `1150db5`,
    `--profile large --deadline 1800 --seed-cache --coverage-output`),
    `CANONICAL_MAKE_CHECK_EXIT=0`, `SOURCE_POST_VERIFY_EXIT=0`, 17 min 8 s wall. It covers
    fmt, lint, fix, tidy, deadcode, vuln ("No vulnerabilities found"), cover (package 60% and
    total 75% thresholds PASS), crap (0/10 above CRAP 30), bench-smoke, actionlint and
    capture-acceptance (analytics 13 OK, candidate-live-r2 6 OK, capture-delivery 12 OK with
    1 guarded skip). sessioncapture 372.7 s under coverage+race, within the unchanged 600 s.
- **Gap closure on `gap/integration` (`2deac62..1150db5`, 51 commits, 121 files):**

  | Gap | Resolving commits (integration) | State |
  |---|---|---|
  | GAP-01 | `cc96583`, `e4949da`, `ad6969c` | Fixed, tested |
  | GAP-02 | `3ba5fc3`, `ff9ce7b`, `11482fa` | Fixed and tested; throughput defaults unmeasured (live R2 excluded) |
  | GAP-03 | `1380f1d`, `3c4ef7b`, `80874b5`, `f17f37b`, `8c63190`, `f17d420`, `db71c21`, `9c7d866`, `85dbc5f`, `37e11fa`, `094a4b8`, `1150db5` | Fixed, tested; cadence Q=30 s/M=300 s from Mac+BC-01 replay |
  | GAP-04 | `ad7b274`, `c17b69e`, `71ea144`, `eb40801`, `f504c4f` | Fixed, tested |
  | GAP-05 | `e4949da`, `c5e1108`, `669593f` | Fixed, tested |
  | GAP-06 | `97842b0`, `8fc0e80`, `669593f` | Fixed, tested; repeated-rebase cost deferred |
  | GAP-07 | `802519b` | Fixed, tested |
  | GAP-08 | `ca489a7`, `1dc9fe2` | Fixed, tested |
  | GAP-09 | `8617382` | Seam and tests added |
  | GAP-10 | `8617382`, `2c7bf7d` | Fixed, crash matrix asserts reclaim |
  | GAP-11 | `085e85f`, `73a41c0`, `aa179a9` | Fixed; transformed re-hint cost not reproducible on current local roots |

  Test-quality work in the same range: model-history and privacy canary (`64482d9`), hint
  burst/inbox bounds (`8e8458d`), strengthened W4/W5/W7 oracles (`9ff7c6c`), fuzz targets
  (`ed554df`, `3774512`, `ce7d913`), SCOPED mutation hardening (`8fae9ce`, `d2d7d5e`,
  `9399ead`, `95acce8`, `7372f16`), perf (`76a8ab5`, `f25e6b3`) and the audited
  parallel set (`9e0e8b7`, `7ad1703`).
- **PF final report (completed, no new commits).** It confirms `f3b7ce6`/`e2f0403` as
  already integrated (`9e0e8b7`/`7ad1703`). PF ran its gates on the working tree before
  splitting it into two commits. The parent's byte comparison against `pf-p5` and the
  canonical `p-int-canon6` cover the split commits. Recorded margin risk: on the same tree
  `pf-par5` took 377.6 s and `pf-par6` 408.2 s, so the ≤420 s target has only about 12 s
  of headroom under contention. `TestSourceLimit` (226–256 s) remains the floor of the
  package time. The 600 s timeout is unchanged.
- **Not run or not proven (recorded, not claimed):** live R2 and installed/BC-01 writes
  (excluded by authority); GAP-02 upload throughput defaults; native Darwin and CI; C4 WAL
  backup and P16 oversized-object repair; the installed discovery timeout; a live-traffic
  validation of the cadence replay (simulated, append-only); 19 hang-type timeout mutants
  with no fast test; the redundant `publication.go:494` guard (equivalent mutant, removal
  is a production change); model-history generator produces no held optional/repair
  objects (unit tests only).
- **Superseded next action:** publication is now authorized; see "Production delivery —
  November 4" below. The defaults and deadlines stay
    unchanged. Prompt-capture journeys set explicit 0/0 cadence, with justification. A new
    end-to-end journey proves the binary honours configured cadence: it asserts a
    not-before-Q lower bound and capture within the existing deadline, and is RED on a
    cadence-ignoring tree. The whole `capture-acceptance` target must pass, then the
    canonical check is rerun on integration.
- **M final (completed, no real defects).** All SCOPED survivors were killed except the
  `publication.go:494:15` `>`→`>=` mutant. It is argued equivalent because
  `ValidateManifest` rejects first, which makes the guard redundant; removing it is a
  production change and stays out of scope. Recorded: 19 hang-type timeout mutants
  (deliver.go, publicationr2, runtime_store.go) with no fast test, and coverage left
  unrerun (deliver.go 99-127; runtime_store.go 400-520 except 479). Details are in
  `reports/m-mutation.md`.
- **G3 final (D1–D5 complete).** The open items are recorded rather than blocking:
  - the model-history generator produces no held optional or repair objects, which unit
    tests cover instead;
  - the cadence replay models append-only sessions and is simulated, not live;
  - the optional BC-01 aggregate read was not done.
- **PF corrected parallel set** (`pf-p5`, on `7372f16`): 71 parallel top-level tests, with
  the 9 timing-sensitive tests moved back to serial, plus the step-3 query.
  - The parent's own transitive helper trace agrees: the only flagged hits are
    `TestModelHistories` (`time.Since` feeds `t.Logf` only) and
    `TestRuntimeDeliveryProgressIdleScheduling` (`Sleep` in a synctest bubble). The diff
    has 13 non-`t.Parallel()` lines, all of them the step-3 query.
  - `pf-par5`: sessioncapture ok in 377.6 s (baseline 515.7 s), CHECK_RC=0, lint clean.
    Slowest: `TestSourceLimit` 255.8 s, `TestModelHistories` 46.2 s, `TestCrashProcess`
    15.0 s.
  - Pending before commit: flake guard `pf-par6` and full check `pf-full3`.
- **M finished all targets.** `runtime_store.go` from line 400: lived 3 → 0 (`m-rs3`..`m-rs6`
  exit 0), with 6 hang-type timeouts recorded. M's final report is pending.
- **G3 (`7cf92667-4fb8-4dc`, `gap/coalesce` from `669593f`) dispatched for GAP-03:**
  supersession of proven-subsumed jobs, local payload reclamation (not remote GC),
  quiescence/max-delay cadence, a replay simulator over D's timelines to choose Q/M, model
  history extension and scoped mutation.
- **L2 GAP-06 review (uncommitted `rebase.go`):** the parent accepted the direction: rebase above
  a verified remote current, retain every remote epoch and seal superseded active ones, raise a
  typed conflict on an equal-revision commitment mismatch, CAS on the original token, and
  converge after a lost ACK through `covered`. `l2-red-g6` exit 1 for the expected reason.
  The commit is held for three required changes. First, remote-only selected sources were not
  rechecked against current exclusions, so a rebase could republish an excluded source; this
  must fail closed with `ErrPolicy`. Second, after a rebase every later delivery repeats the
  Current+Read+merge; this needs a 3-capture convergence test with per-delivery backend call
  counts, plus either a safe fix or a documented, measured follow-up. Third, conflict, covered,
  concurrent-CAS, lost-ACK and source-limit tests with exact assertions, then scoped mutation.
- **T mutation (third attempt):** `t-mut-quota3` was killed at its 3600 s deadline (container
  exit 143, no result.json, empty log). The parent diagnosed two causes: the `cut` pipe
  block-buffered and dropped per-mutant output, and coefficient 100 lets one hanging mutant
  run for ~100× the package baseline. Steered to unbuffered output, a `--dry-run` mutant
  inventory, and, if needed, a targeted per-mutant driver with a focused `-run` regex and
  a measured timeout, reported as scoped mutation.
- **T mutation:** `t-mut-quota` and `t-mut-quota2` hit exit 124 at the 1800 s deadline
  (gremlins reruns the whole package suite per mutant; no result). These are tooling
  limits, not RED. `t-mut-quota3` is running with a 3600 s deadline.
- **T progress:** commits through `4f580f7` (hint burst/inbox, W4/W5/W7 oracles each
  killing its mutant and passing base at `-count=3`, fuzz targets + `FUZZ_TARGETS`,
  bounded fuzz minimization + mutation coefficient variable). Fuzz-stall cause per T:
  the engine minimizes a newly interesting input for up to the default 60 s with one
  worker and reports no execs meanwhile; toggling `-fuzzminimizetime` flips the
  behaviour. The SIGQUIT dump was not captured, so this rests on that A/B, not a dump.
- **L2 (`833310fa-5a28-49c`, `gap/discovery` from `cc96583`):** GAP-04 (inventory cap
  must not refuse new sessions because of settled history; BC-01 rows are 99.9%
  `captured`, so absent-row expiry alone is insufficient), discovery completion/timeout
  root cause, GAP-11 transformed-source fingerprints covering every input, GAP-06
  remote-ahead convergence, optional admission-time key-length check. File rules keep
  it off L1's in-flight files. Started (progress file, first job `l2-m1`).
- **Pending:** L2 (GAP-04/06/11 plus discovery truncation/completion) after L1 lands,
  stacked on `gap/liveness`; GAP-03 after L1/L2.

## Production delivery — November 4

Operator request (2026-11-04):
1. Publish the gap work as a `gh stack`, review it, run CI (including native macOS) and
   merge it.
2. Run operating acceptance now, which is steps 1–4 of the plan below: Mac first, then
   BC-01, then combined operation.
3. Resolve these items now:
   - C4/P12 SQLite WAL backup under a live writer;
   - the installed discovery timeout (root cause, then fix);
   - the 19 hang-type mutants (root cause, then fix);
   - removal of the redundant `publication.go:494` guard;
   - optional and repair objects in the model-history generator;
   - an investigation of the repeated-rebase cost.

   The test-time margin is deferred to future work.

- **Authority:** publishing branches and PRs, CI, and merging to `main` once CI is green.
  Operating-plan steps 1–4 are now authorized: installed-state and R2/BC-01
  inventory, coordinated build from merged source, activation and acceptance on Mac then
  BC-01. Step 5 (final defaults, legacy retirement, reset or deletion of keys) stays
  **excluded**, as does release tag publication. The preservation rules below still apply.
- **Sequence:** step 3 source work is merged before activation, so live acceptance runs on
  one source state. The read-only installed-state reconciliation (plan step 1) starts at
  once.
- **Stack (step 1):** a parent-built stack from `delivery-stack`
  (`/Users/mm/dev/memory-wt/delivery-stack`). Its final code tree is byte-identical to the
  green `1150db5` (`p-int-canon6`). J's `08de567` is split by file into layers 3 and 4: in
  layer 3 the cadence defaults arrive with the journey-config fix, so no layer is red by
  construction.

  | Layer | Branch | Content |
  |---|---|---|
  | 1 | `delivery/1-liveness` (`ce7d913`) | GAP-01/02/05/09/10, test infrastructure |
  | 2 | `delivery/2-admission` (`f504c4f`) | GAP-04/06/11 |
  | 3 | `delivery/3-coalesce` (`ab5b5aa`) | GAP-03, mutation hardening, perf, parallel tests |
  | 4 | `delivery/4-consumer` (`c38f77f`) | GAP-07/08 consumer |
  | 5 | `delivery/5-docs` | gap register, README index, this brief |

- **Published:** stack #39, draft PRs https://github.com/mich-murphy/memory/pull/34 ←
  https://github.com/mich-murphy/memory/pull/35 ← https://github.com/mich-murphy/memory/pull/36
  ← https://github.com/mich-murphy/memory/pull/37 ← https://github.com/mich-murphy/memory/pull/38.
  CI runs `41742160189` (#34), `41742170947` (#35), `41742181140` (#36), `41742194293`
  (#37), `41742205567` (#38). The CI test matrix includes `macos-26`, which is the native
  Darwin run.
- **Wave 4 workers (step 3), all from `9220f28`, rules in private `WAVE4-RULES.md`:**
  - DT `b0818be4-a557-4ae`, `fix/discovery-timeout` (`memory-wt/discovery-timeout`):
    installed discovery timeout root cause and fix.
  - WAL `d1379b0f-9cca-46a`, `fix/sqlite-wal-backup` (`memory-wt/sqlite-wal`): C4/P12
    SQLite backup under a live writer.
  - MO `1dcd7939-f9f4-4e6`, `test/model-optional-repair` (`memory-wt/model-optional`):
    optional and repair objects in the model-history generator and oracle.
  - Queued for the next free slot: hang-type mutants plus the redundant `publication.go:494`
    guard; the repeated-rebase cost investigation.
- **PR CI blocker (2026-11-04):** every stack layer fails `Tests` on at least one OS.
  `internal/sessioncapture` hits the unchanged 600 s timeout under `make crap` on
  ubuntu-24.04, arm and macos-26. `main`'s CI ran the package in 127–194 s; stack runs that
  pass take 517–541 s. The dominant cost is layer 1's `TestSourceLimitIsTypedAndNotQuarantinedAsTransient`:
  1030 real captures with O(sources) cost each, which is quadratic. It runs 4m48s–7m50s
  alone on macOS CI. The local canonical green (372 s) did not predict CI-runner time.
  - **Parent decision:** an unexported per-Queue source limit, defaulting to
    `publication.MaxSources` and never above it, with no package global because the tests
    run in parallel. The test exercises the same real capture path at a small limit, keeping
    every assertion's intent, and cheap tests pin the 1024 default and reject invalid
    values. This is not database seeding or skipped captures (option 3b stays ruled out).
    Production behaviour is unchanged at the zero value.
  - Worker SL `fix/source-limit-cost` (`memory-wt/source-limit-cost`, from layer-1
    `ce7d913`). After it lands, the parent restacks layers 2–5 on top of it and reruns the
    canonical check plus PR CI.
  - MO is paused, because model-history growth spends the same package budget. It resumes
    once the new margin is known.
- **SL accepted and stack restacked (2026-11-04):**
  - `aec56da` is byte-identical to snapshot `sl-1`.
    - `sl-check2` exit 0: sessioncapture 435.3 s on layer 1 (medium, GOMAXPROCS=2).
    - `sl-red1` RC=1 on a mutated default/bounds.
    - `sl-mut1`: 11/11 killed.
  - Top-of-stack port `sl-topcheck` exit 0: sessioncapture 282.6 s. The unchanged-baseline
    comparison was not run.
  - Layer 1 gained `cb38ac9` (cherry-pick `-x` of `aec56da`), and layers 2–5 were rebased
    onto it. Two mechanical conflicts were resolved:
    - `queue.go`: kept both test-only fields.
    - `liveness_test.go`: kept `t.Parallel()`, `const limit = 32`, and the stricter
      identity/sequence/PublicationID/source-count final check.
  - The final tree `f710e4b` equals the `sl-topcheck` tree exactly.
  - New heads: `cb38ac9`, `3d82ee8`, `70a464a`, `33603cc`, `88ea0f4`. They were pushed to
    stack #39, and CI runs `41750085431`, `41750088224`, `41750100350`, `41750104317` and
    `41750112805` started.
- **Layer 1 CI `41750085431` green (all 7 jobs):** `make crap` sessioncapture 363.5 s
  ubuntu-24.04, 356.4 s macos-26, 246.4 s arm (it was 600 s FAIL before the fix); syncer
  (unchanged) 424.3 / 494.0 / 284.0 s.
- **Restacked CI (2026-11-04):** layers 1–4 (`41750085431`, `41750088224`, `41750100350`,
  `41750104317`) are green on all jobs. Layer 5 `41750112805` failed only on macos-26:
  `internal/syncer` timed out at 600 s (running `TestLargePlainAndCompressedCaptureHasBoundedHeap`).
  The stack makes no syncer code change.
  - macOS syncer took 406–446 s on main and 494–600 s across the stack layers. sessioncapture
    went from 150–164 s to 356–495 s. Layers 4 and 5 have identical code and differ by 60 s,
    which points to a CPU-contention margin problem, not a hang.
  - On medium `sl-topcheck`, the 95 new sessioncapture tests sum to about 214 s and the 219
    pre-existing ones to about 165 s.
  - **Decision:** reduce the new tests' CPU cost without weakening any assertion, rather
    than rerunning until green.
    - MO2 must first cut TestModelHistories' CPU by at least 40% with identical histories and
      assertions, and its optional/repair additions must be CPU-neutral.
    - Worker CM (`fix/ci-margin`, from `88ea0f4`) reduces the other heavy tests by at least
      50% under race+cover, using the per-Queue knob precedent. No raw seeding and no skips.
- **WAL (C4/P12) delivered:** `baf1d01` and `bdad5e0` on `fix/sqlite-wal-backup`.
  - No torn snapshots were found. Concurrent-writer failures are now typed as
    `ErrSQLiteSourceChanged`, and they are attributed to a change only when a recheck proves
    the source moved.
  - `wal-m2`: 11/11 mutants killed. Port `wal-c5` on `88ea0f4`: tests green, sessioncapture
    267.1 s. Its `CHECK_RC=1` comes only from bare `go vet` unsafeptr findings in the
    untouched `sqlite_authorized_vfs.go`; `make check` uses golangci govet, which reports
    0 issues.
  - Parent review returned one fix: `rejectSQLiteJournal` runs twice per check.
- **Recovery after Pi reload (2026-11-04):** the reload cleared every worker record (DT,
  WAL, MO2; CM had never started), and the Docker Desktop backend had stopped. Jobs
  `dt-topcheck1`, `wal-c6`/`c7` and `mo-cpu11` were killed by the daemon loss and are not
  evidence.
  - Docker was restarted at its unchanged settings (Cpus 4, MemoryMiB 10240). MemTotal now
    reads 64 KiB below the pin, so the `launch.py` guard accepts MemTotal within 1 MiB;
    NCPU stays exact.
  - Durable state recovered from disk: DT `acba80f` (`fix/discovery-timeout`), with the port
    staged in `dt-top`; WAL `1421de3` (journal probe deduplicated, `wal-m3` 10/10 killed),
    with the port staged in `wal-top`. Both snapshots (`dt-top1`, `wal-11`) are
    byte-identical to the committed/staged trees.
  - The parent relaunched `dt-topcheck2` and `wal-c8`. Fresh workers MO3 (model CPU, then
    optional/repair) and CM (other heavy tests' CPU) were started with full briefs.
- **WAL accepted (2026-11-04):** `baf1d01`, `bdad5e0` and `1421de3` on `fix/sqlite-wal-backup`.
  `1421de3` probes the journal once per check, and its new test proves
  `ErrSQLiteRollbackJournal` is never wrapped as `ErrSQLiteSourceChanged` (`wal-m3` 10/10
  killed). Port check `wal-c8` (`wal-11` = `88ea0f4` + patch, medium): lint 0 issues,
  adapters 9.8 s, safefile 1.0 s. `CHECK_RC=1` comes only from bare `go vet` unsafeptr
  findings in `sqlite_authorized_vfs.go`, which is unchanged since `main` (`b68a412`); the
  repository gate (golangci govet enable-all) is clean.
- **DT accepted (2026-11-04):** `acba80f` on `fix/discovery-timeout`.
  - Root cause: the installed discovery timeout on the Oct 29 queues was the pre-HintBatch
    quadratic admission COUNT (L2, about 50 s at 30k), already fixed in the stack. At 30k
    sources the base stack takes at most 4–6 s per call against the 60 s budget. The
    remaining per-entry cost was rebuilding the exclusion registry and filesystem policy
    set on every policy callback (about 25% of walk CPU).
  - Fix: one lazily built policy snapshot per discovery call. Capture still rechecks policy
    live, so an exclusion saved after the snapshot still prevents capture. Cold walk went
    from 27.9 s to 13.4 s and the maximum call from 4.1 s to 2.1 s (`dt-prof1`/`dt-after1`).
  - `DiscoveryComplete` is documented as current-sweep only (the Mac queue's
    generation 56/completed 55 is that state) and pinned by a test.
  - RED `dt-red1` (registry loads=184, want 1). Port `dt-topcheck2` (`88ea0f4` + patch,
    medium) `CHECK_RC=0`: sessioncapture 286.3 s, cli 40.6 s. Parent scoped mutation
    `p-dt-mut1`: 11/11 killed.
  - Noted: `TestDiscoveryInstalledScale` is an env-gated measurement (`DT_SCALE`) that
    skips by default; it makes no assertion.
- **MO3 decision:** its profile shows the harness and oracle are under 10% of
  TestModelHistories CPU. `Queue.Open` is about 31% (roughly 163 opens at about 79 ms;
  `installBudgetTriggers` drops and recreates 20 triggers per open). The parent approved a
  production change: skip trigger DDL only when `sqlite_master` holds byte-identical SQL,
  and read `table_info` once per table. Each case gets RED tests, and the target is revised
  to about 25–30%. CM baseline `cm-base2` (race+cover, medium): package 294.9 s wall,
  345.5 s CPU.
- **CM accepted in part; stack restacked again (2026-11-04):**
  - Accepted test-only commits, byte-identical to `cm-2` (`cm-chk1` `CHECK_RC=0`):
    `83c647f` (sealed 1024-source rebase fixtures; real MaxSources bound kept),
    `5cf92c2` (at-ceiling inventory tests use the existing per-Queue `settledLimit`; default
    pinned) and `7ddefcb` (smaller hint burst/inbox; inbox still crosses the real 1024 bound).
  - **Rejected** `508c3de` (oracle seeds 12 → 6): it thins a property test.
  - Evidence (race+cover, medium, interleaved A/B in one job): targeted tests about 120 s →
    54 s CPU (`cm-ab2` rep2, without 508c3de); package 364.2 s → 247.7 s CPU, 320.7 s →
    219.5 s wall (`cm-pkg3`, one pair under load). RED `cm-red1`–`cm-red3`: every rewritten
    test fails its mutant except the burst test, whose final-state assertions cannot see a
    lost newer observation (a limit of the original test, recorded).
  - Not reducible test-side: TestCrashProcess (33 re-exec children) and the inbox bound
    (1024 real files; `MaxInboxHints` is a package const).
  - **MO3's Open schema-init change `c398748` declined:** correct and RED-covered, but
    `mo-pab31` showed no package-level saving, and it changes schema init just before the
    first upgrade of the real installed queues. Kept only on `test/model-optional-repair`.
  - `7ddefcb` → layer 1 as `a0f0c44`; `83c647f`/`5cf92c2` → layer 2 as `22c237c`/`417d98b`.
    Conflicts were only layer 3's `t.Parallel()` lines. Final tree `c0f1ac22` equals
    `7ddefcb^{tree}` exactly. New heads `a0f0c44`, `417d98b`, `a28f270`, `4738df8`,
    `a8bfde5`; CI runs `41774296803`, `41774297549`, `41774308069`, `41774310108`,
    `41774320493`. Pre-restack heads are kept as `backup/pre-cm-*`.
- **Stack #39 merged (2026-11-04):** after the CM restack, every layer passed CI on all
  jobs (`41774296803`, `41774297549`, `41774308069`, `41774310108`, `41774320493`).
  The tightest margin was macOS syncer at 530.1 s on layer 1 (main itself: 406–446 s);
  sessioncapture took 246–449 s. The PRs were marked ready and merged atomically with
  `gh stack merge 39 --merge` as `d975961`. `main`'s tree equals the tested `c0f1ac22`.
  Post-merge `main` CI `41776192211` passed all 7 jobs, including macos-26.
  - The active gh account `michaelmbc` has pull-only access; owner-only operations used
    the logged-in `mich-murphy` token per command, with no global auth change.
- **HM accepted (2026-11-04):** `0f19f9b` (`Publish` uses the exact `MarshalValidManifest`
  bytes; the unreachable second size check is removed; `TestPublishRejectsOversizeManifestBeforeBackendAccess`
  asserts the typed error with zero backend calls; RED `hm-pubred3`) and `096dc80` (step-budget
  `TestHang*` tests). All 19 former hang-type timeouts are class (a), a missing fast test; no
  liveness defect was found. `hm-mut3`: 19/19 KILLED in 1–6 s each. `hm-chk2` `CHECK_RC=0`.
  The new tests cost about 4 s CPU under race against a 3 s budget; accepted, because
  trimming saves about 1 s against a margin of about 70 s. The ordering note on
  `TestHangAddColumnIfMissing...` matters only to the unshuffled mutation driver.
- **RB accepted (2026-11-04):** `d5ff235` makes `sealSuperseded` linear with byte-identical
  output (600-case property test against a verbatim copy of the old code; 5/5 RED in
  `rb-red1`; `rb-mut1` 37 killed, 0 lived, 6 not viable). 256 active sources: 17.5 ms /
  261k allocs → 0.33 ms / 1k (plain); 8 consecutive CURRENT moves at 256 sources:
  1.65–1.9 s → 40 ms under race. The largest observed session has 379 sources.
- **Step-3 integration `integration/step3` (from merged `d975961`):** clean cherry-picks
  `ce6ef2d`, `4ce3d33`, `2dd8bbb` (WAL), `953def8` (DT), `a983e29`, `fd3faec` (HM),
  `e75e9b4` (RB). Canonical gate `p-s3-canon1` on snapshot `p-s3-1`:
  `CANONICAL_MAKE_CHECK_EXIT=0`, `SOURCE_POST_VERIFY_EXIT=0`, coverage PASS,
  capture-acceptance OK (1 guarded skip), 1008 s wall.
- **MO3 accepted (2026-11-04):** `806359a` adds `TestModelOptionalHistories` (12 histories,
  164 ops; fake clock; no globals or `t.Parallel`; every scenario class is required to be
  non-zero), byte-identical to `mo-61`, `mo-chk61` `CHECK_RC=0`, 6.1 s CPU under race+cover
  (`mo-occ62`, budget 8 s). RED: missing withheld marker killed (`mo-redC2`); Optional skip
  plus enrichment hold killed (`mo-redD`/`E`). **Limits:** optional payloads have three
  overlapping protections, so dropping the rehold trigger, the Optional skip or the
  enrichment hold alone is not observable at model level (`mo-redA2`/`B2`/`F`/`G2`
  survived). Scoped mutation with this test alone: `reclaim.go` 15 killed / 11 lived /
  4 timeouts (`mo-mut70`); `enrichment_selection.go` 36 / 13 / 0 (`mo-mut71`). The survivors
  include the line 62 `payload_refs` backfill; they stay open. TestModelHistories is unchanged.
  Cherry-picked as `c8b4aeb`; canonical gate `p-s3-canon2` on `p-s3-2`: both exits 0,
  govulncheck clean, coverage and CRAP PASS, 874 s wall.
- **Step-3 stack #46 published (2026-11-04):** PRs #41–#45, layers `step3/1-wal-backup`
  `2dd8bbb`, `step3/2-discovery-policy` `953def8`, `step3/3-hang-mutants` `fd3faec`,
  `step3/4-rebase-cost` `e75e9b4`, `step3/5-model-optional` `c8b4aeb` (top tree `5a5eb62e`
  is the tree gated by `p-s3-canon2`). CI: layers 2–5 green on all jobs; layer 1 run
  `41784370713` failed only on ubuntu-24.04-arm: `TestHintBurstCoalescesAndKeepsLatestBytes`
  "runtime did not reach idle in 60s (lost wakeup?)" (shuffle `1793773024889103650`). That
  test is already on `main` and is untouched by layer 1 (adapters only), so this is an
  intermittent liveness failure. **Not rerun as a fix:** worker HB (`fix/hint-burst-liveness`
  from `d975961`) is classifying it as a production lost wakeup, CPU starvation against a
  60 s wall deadline, or a test defect, with RED/GREEN as applicable. Stack #46 merge waits on it.
- **Hint-burst failure root-caused: test defect, not a lost wakeup (2026-11-04):**
  `runtimeIdle` treated `superseded` jobs as unpublished, but production coalescing
  (`acceptView` → `retireJobs`) legitimately supersedes an undelivered earlier job when
  capture runs ahead of delivery (as on a contended runner). The predicate could then
  never become true. Parent diagnostic (snapshot only, never committed), with a backend
  gate holding writes until after the FINAL hints: 20 of 28 jobs superseded; old predicate
  failed exactly as CI ("did not reach idle in 60s", 64.6/64.4 s, `p-hbdiag3`); new predicate
  passed in about 5 s (`p-hbdiag2`). Ungated stressed runs had 0 superseded (`p-hbdiag1`,
  deadline-cut after 5 passes), which is why HB's stress runs (`hb-rep1/2`, `hb-str1/2`,
  `hb-pkg1`) never reproduced it.
  - **HB accepted:** `d2a5638` (test only). Idle now requires no open source, ≥1 job, no job
    outside {published, superseded}, and each session's newest job published (stricter than
    before for the newest job). New deterministic `TestRuntimeIdleTreatsSupersededAsSettled`:
    RED `hb-red2` on the old predicate, GREEN `hb-grn2`, `hb-chk2` `CHECK_RC=0`; byte-identical
    to `hb-7`. The 60 s bound, stable-idle recheck, rows/attempts and exact-bytes checks are
    unchanged.
  - Folded into stack #46 layer 1 by cherry-pick; layers 2–5 rebased (pre-restack heads kept
    as `backup/pre-hb-step3-*`). New heads `bbba14d`, `c763046`, `430bff4`, `0440a79`, `9858ccd`;
    the top differs from the `p-s3-canon2` tree only by this test file. Canonical
    `p-s3-canon3` on `p-s3-3`: both exits 0, govulncheck clean, coverage PASS, 963 s.
- **Stack #46 CI after the HB restack:** layers 2–5 green on all jobs (`41798401238`,
  `41798405783`, `41798415832`, `41798420324`). Layer 1 `41798396838` failed only on
  macos-26: `internal/syncer` timed out at 600.36 s, while
  `TestWarmIdleRequestsDoNotGrowWithFilesOrHistory` was running. Syncer code is unchanged
  by both stacks; macOS syncer times with identical code are 429–580 s, including 543.6 s
  on post-merge `main`. That is a margin failure: CPU contention with sessioncapture for
  the first ~6 min, then a mostly serial package (7 parallel tests) on 3 cores. **Not
  rerun as a fix:** worker SM (`fix/syncer-test-cost` from `d975961`) is cutting syncer
  wall time by ≥35% under race+cover, with audited `t.Parallel()` and no weakening.
  Heap-bound tests stay serial.
- **SM: syncer margin fixed by CI scheduling, not test changes (2026-11-04):**
  - Measured (medium, race+cover): `TestSimulatedSessions` is ~212 s of a ~367 s syncer wall
    (8 seed child processes, 4 at a time, already parallel). The other ~177 tests sum to
    ~156 s and are serial because `testenv.New` uses `t.Setenv`. No genuine fixture waste.
  - **Declined (e)**, starting the simulation children early from `TestMain`: on 1 CPU,
    package CPU rose from ~391 s to ~579 s (`sm-sh11/22/33` timed out; base `sm-shb11`
    passed at 412 s), and the real-time
    `TestArchiveRunHonorsItsOwnDeadlineAfterBackgroundRecovery` flaked (98 ms vs a 40 ms
    deadline). Patch kept as `$R/scripts-sm/e-overlap-declined.patch`.
  - **Accepted (f):** `85c3a66` makes `make cover` run two sequential `go test` invocations
    with identical flags: every package except `COVER_ISOLATED` (`./internal/syncer`), then
    syncer alone. Same coverage dirs and merge; union = `go list ./...`, disjoint (`sm-f2`);
    either failure fails the recipe (`sm-cv7-b`, `sm-cv8-b` make rc 2); merge path proven
    (`sm-cv3-b`). Byte-identical to `sm-3`. Parent follow-up `b3de5c2`: `go list` runs in
    its own assignment so `set -e` sees its failure (it was hidden behind `grep` in a
    pipeline). No test, timeout or gate changed.
  - Folded into stack #46 layer 1; layers rebased (pre-restack heads `backup/pre-sm-step3-*`).
    New heads `83a9a5a`, `be61c6d`, `c6a5059`, `aca80fb`, `2dcc37d`. Canonical `p-s3-canon4`
    on `p-s3-4`: both exits 0, govulncheck clean, coverage PASS, 950 s; the two invocations
    ran (sessioncapture 203.0 s, syncer 272.1 s alone).
  - Push: the SSH agent now has no identities, so pushes use HTTPS with the `mich-murphy`
    token per command (no global config change). The first HTTPS attempts were rejected
    with 500 / `failure` during an active GitHub incident (Git operations, Pull Requests).
- **Stack #46 CI after the SM restack (2026-11-04):** layer 1 `41814983396` (`83a9a5a`)
  failed only on ubuntu-24.04-arm: `TestRuntimeDiscoveryExpiredSaveCannotResolvePriorIssue`
  `runtime_discovery_admission_test.go:130: context deadline exceeded` (shuffle
  `1793788488503719633`). This is a pre-existing test (b8cbed2), serial, untouched by the stack. Its second, clean
  `discoverOnce` pass must succeed but reuses the 20 ms real-time `OperationTimeout` meant
  only for the expiring first pass, so a contended save times out. Parent diagnosis: a test
  defect (production default 60 s). **Not rerun as a fix:** worker RT
  (`fix/realtime-deadline-tests` from `d975961`) is fixing it with a deterministic
  delay RED, and auditing sessioncapture tests for the same pattern (small real-time budgets
  on steps that must succeed).
  - Layers 2–5 at the same heads passed on all jobs (`41817779694`, `41817779037`,
    `41817776699`, `41817795533`). macOS times with syncer in its own invocation:
    sessioncapture 297–320 s, syncer 388–500 s (before: 429–600 s). The margin is better
    but still only about 100 s at worst on macOS; recorded as a residual risk.
- **RT accepted; stack #46 merged (2026-11-04):**
  - RT `97a00ac` (test only, byte-identical to `rt-4`): pass 2 of
    `TestRuntimeDiscoveryExpiredSaveCannotResolvePriorIssue` uses `time.Second`; pass 1 keeps
    its 20 ms expiry; assertions unchanged. RED `rt-red2` (30 ms stall after `tx.Commit`,
    old test fails at line 130), GREEN `rt-green-mut`, `rt-check` `CHECK_RC=0`, `rt-c20`
    (`^TestRuntimeDiscovery` ×20, small) green. Audit: the other short budgets either have
    expiry as their subject or a ≥10× margin (`ExpiredCallbackAdmitsReturnedRequests`, 200 ms;
    left, no RED). A `Now`-delay RED (`rt-red1`) was invalid and is not evidence.
  - Folded into layer 1; heads `9366cd7`, `9dbd1a2`, `e65c142`, `ac779f9`, `bf3c21d`
    (pre-restack `backup/pre-rt-step3-*`). Canonical `p-s3-canon5` on `p-s3-5`: both exits 0,
    953 s. CI green on all jobs for all layers (`41820180067`, `41822029585`, `41822027711`,
    `41822032217`, `41822037526`); macOS syncer 406–467 s.
  - Merged atomically with `gh stack merge 46 --merge` as `f94670a`; `main`'s tree equals the
    gated `a6df3379`. Post-merge `main` CI `41824671891`: all 7 jobs green.
  - **Residual risk:** macOS syncer alone took 534.6 s on post-merge `main` (range 388–535 s
    with its own invocation). The package itself is slow on macOS runners; the margin is
    about 65 s at worst. Next lever, if it fails again: split `TestSimulatedSessions`'s 8
    seed children into their own invocation (no seed or step reduction).
- **Operating step 2, dormant staging on the Mac (2026-11-04):**
  - Reproducible cross-build of merged `f94670a` in the Docker admission (`p-rel-build1`,
    two clean passes, identical): darwin/arm64 `365db4df…`, linux/amd64 `ca002180…`;
    Go 1.27.3, `-trimpath`, CGO 0, `main.version=f94670a` (snapshot has no `.git`, so no VCS
    stamp). Staged 0700 in `<release>/merged-f94670a/` with `SHA256SUMS`; nothing installed
    points there. Installed `.bin` unchanged (`6beae15a…`).
  - Staged binary, credentials stripped, real config: `policy-pauses` → `null`; service plan
    unit is byte-identical to the installed plist; hook plan: Claude and Codex unchanged,
    **Pi extension drift** (`5042d95e → 68b02350`), which the installed b8cbed2 binary
    plans identically: the installed file is the pre-`65759df` template (Oct 25) without the
    `--no-session` guard in `launch`. Applying it is `hooks install --apply` (step 3).
  - Staged read-only `work-status` fails on the un-migrated queue (`no such column:
    dirty_since`); the column is added on the first writable open. Real queue hash unchanged
    by every check (`509aa2cb…`).
  - **Migration rehearsal on an independent copy** (APFS clone of payloads + SQLite-API
    backup, `quick_check` ok, config copy redirecting only `spool`, credentials stripped):
    staged `resume` rc 0 in 3.6 s, 41 MB RSS. Added `dirty_since`/`debounce_until`,
    `payload_refs`/`payload_reclaim`, hold triggers and new indexes; replaced
    `runtime_runnable_sources` by `_v2`. Jobs (20,308 published / 5 superseded), 19,413
    captured sources, 18,456 payload files, destination state and `quick_check` unchanged.
    The installed b8cbed2 binary still reads the migrated copy, so binary-only rollback is
    possible.
  - Copy also shows the installed Mac state: `Coverage=partial`, all three scopes `bounded`
    at 1365 examined, 42 step errors (last `discovery: context deadline exceeded`, the DT
    defect), 3,459 active discovery issues (mostly `protected_source_requires_scrub`:
    claude 1626, codex 1507; `executable_source_requires_review` 224; `policy_denied` 52;
    `unsupported_compressed_source` 18; `adapter_omission` 25).
- **Operating step 3, Mac activation attempt 6 (2026-11-05 07:42Z–, `57d8400`), running; Mac step 3 passed 12:47Z** (current pid 48472 since the 17:52Z restart; see the checkpoint):
  - PR #52 (RS: `ResolveWith` + per-call `FSMemo`, sharing Lstat/Readlink across
    `evaluateAllowed`, the snapshot policy resolve and the `privacyOwner` leaf Lstat) merged
    as `f2b6205`. 6 hand mutants killed, canonical `p-rs-canon1` exit 0, CI 7/7 green.
  - PR #53 (HI/HI2: inbox tree fenced once per inbox identity `c5a7ca4`; constant hint SQL
    with tx-scoped prepared statements `f91cc80`; EF test-budget fix `2f39987`) merged as
    **`57d8400`**. `BenchmarkHintBatchUnchanged` 331 ms to 91 ms; 6 hand mutants killed;
    canonical `p-hi-canon4` exit 0. Earlier canonical kills (`p-hi-canon1..3-contended`,
    `p-ab-*-contended`) were host contention: paired base-vs-branch runs under the same
    load timed out or slowed equally.
  - First PR #53 CI run `41977817760` failed `TestRuntimeEnrichmentEnabledRunPublishesAndStatusDoesNotTakeLease`.
    Pre-existing: reproduced on unchanged `ec43cff` (2/100 under contention). EF traced it
    to slow serialized startup under fsync contention (`Open` up to 2.1 s, `InitRuntime` up
    to 2.4 s, `synchronous=FULL` DDL) with no errors, retries or backoff; the fixed 5 s test
    budget was wrong (9366cd7 pattern). Deadline 5 s to 30 s; RED with a 6 s startup-stall
    mutant; paired repro 25/150 to 0/150.
  - Harness: the operator retired the shared validation semaphore at 03:11Z (global
    AGENTS.md; locks/tools volumes removed). `launch.py` no longer mounts or runs it;
    admission is the 3-owned-container limit plus per-container `--cpus/--memory` caps.
  - Build `p-rel-build6`, REPRODUCIBLE=yes: darwin `dc361b24…`, linux `a572a0bd…`; launcher
    `91a49ec3…` unchanged. Staged at `merged-57d8400/` on the Mac and BC-01. Backup
    `<release>/backup-pre-57d8400-20261105T074157/` (`quick_check` ok, 19,367 payloads,
    21,421 published, 511 superseded, quota gap 1,778).
  - The attempt-5 soak (01:35–07:41Z, about 6 h) stayed correct throughout: 0 undelivered at
    every check except one transient, 0 unrepaired.
  - Started 07:42:14Z (pid 8136). **Settled 0.294 cores under launchd** (10-minute CPU-time
    delta after 5 minutes warm-up), down from 0.50 (attempt 5). 0 undelivered, 0 unrepaired.
    Restarted 08:08:23Z (pid 13875) after a bounded diagnostic profile; 0.294 cores again over
    20 minutes.
  - Diagnostic profile (`p-diag-build5` `f9e7920a…`, background QoS, 0.277 cores,
    `p-diag-pprof7/8`): discovery about 60 % (exclusion identity walk `isWithinStat` Stat
    22 %, `privacyOwner` leaf Lstat 11 %, directory `os.Open` 9 %), hint upserts 9 %, native
    inbox 10 %. Remaining cost is per-entry syscalls from re-walking unchanged trees every
    10 s poll: a structural follow-up (change detection or cadence), not another per-call memo.
  - **Analytics source switched** 08:33Z: refresher and dashboard plists changed only in the
    source path `e8c96f8` → `f94670a` (prepared tree verified 32/32 files against git;
    `requirements.txt` unchanged, so the existing runtime is reused; `tools/` identical at
    `57d8400`). Backup `<release>/backup-analytics-pre-f94670a-20261105T083303/` (plists,
    DuckDB and bodies APFS clones, `refresh.sqlite3` via `backup()`, `quick_check` ok).
    Two post-switch sweeps: 2 endpoints, 33,268 expected targets, 0 discovery/endpoint/target
    errors, 0 unimported; dashboard HTTP 200; empty stderr.
  - **End to end (aggregate only):** the 200 most recently published Mac sessions in the
    queue (171 Pi, 27 Claude, 2 Codex), matched on the decoded identity tuple, are all
    imported by the refresher at or beyond their queue sequence; `mac` targets 14,469/14,469
    imported, `bc01` 18,799/18,799, no head failures.
  - Step-4 budget item: the refresher itself used about 0.47 cores averaged over 22 minutes
    (60 s interval), more than the coordinator.
  - **Hooks (aggregate, 08:58Z):** `hooks --action plan` shows no drift for Claude, Codex or
    Pi. Hints inbox drained (0). Captured in the last 24 h: Claude 247, Pi 504, Codex 5;
    last 2 h: Claude 4, Pi 8. Native evidence from Pi in the last 2 h (3 files), so the
    updated Pi extension fires. Codex: each of the 6 agent-sessions hook commands has a
    `hooks.state` trusted-hash entry, but `config.toml` (trust, 07:41 Oct 30) predates the
    last `hooks.json` rewrite (08:38 Oct 30), and Codex's own log has no entries for 6.5 days,
    so Codex hook firing is **not yet demonstrated**. Needs the operator to run Codex once and
    approve `/hooks` if prompted; then confirm a Codex hook-driven capture.
  - **Codex hook probes (09:00–09:06Z, throwaway "reply OK" sessions in an empty scratch
    directory, read-only sandbox):** a normal `codex exec` ran only another tool's
    `UserPromptSubmit` hook; none of the 6 agent-sessions hooks ran and the hints inbox was
    untouched (the session was still captured by the 10 s discovery poll). A single
    `--dangerously-bypass-hook-trust` invocation (per-invocation only; persisted trust not
    changed) ran our `SessionStart` and `Stop` hooks, wrote to both inboxes, and the
    coordinator drained them (hints 0, 11 Codex sources captured, 0 undelivered). So the
    hooks work and the **persisted Codex trust for the rewritten hooks is stale**. The stored
    `trusted_hash` values cannot be recomputed from outside Codex, and re-trusting is an
    operator approval: run Codex interactively and approve the agent-sessions hooks in
    `/hooks`. Then re-run a normal (non-bypass) probe and confirm a hook-driven Codex capture.
    The probe sessions contain only the throwaway prompt and were captured like any session.
  - **Codex hooks accepted (11:54Z):** after the operator approved the hooks in `/hooks`
    (`config.toml` rewritten 22:50 local; 14 `hooks.state` entries, all 6 agent-sessions hooks
    with a `trusted_hash`), a normal non-bypass `codex exec` probe ran our `SessionStart` and
    `Stop` hooks, both inboxes changed, the coordinator drained the hints (0 left) and captured
    3 Codex sources; 0 undelivered. Claude, Codex and Pi hooks are now all demonstrated live.
  - **Privacy exclusion (12:02Z):** a throwaway Codex probe run with `AGENT_SESSIONS_DISABLE=1`
    ran our hooks in disabled mode: no hint or native-evidence write, and one session
    exclusion record persisted (policy `exclusions/` 0 → 1). Its rollout file exists on disk,
    but after 6+ discovery polls it has no queue rows and no analytics target, while the
    positive-control probe (same matcher) has both. Effective exclusion holds for discovery,
    not only for hooks.
  - **Source absence (12:05–12:19Z, own throwaway probe):** rollout file moved aside (not
    deleted). With no trigger nothing changed; one explicit `capture-v1 hint` for the missing
    path moved the source to `absent`, while the published job and the refresher import
    (sequence 1) were preserved. Restoring the file plus one hint recaptured it
    (pending → captured, sequences 2 and 3 published, refresher imported 3/3, attempts 0, no
    error), then it settled with no further churn over 6 minutes. File restored to its
    original path.
  - **Destination outage/retry without another hook (12:21–12:47Z):** `service stop --apply`,
    then the same wrapper/binary/config ran in the foreground under `taskpolicy -b` inside a
    `sandbox-exec` profile denying all outbound IP (IPv4/IPv6 verified blocked, local DNS
    allowed). A new throwaway Codex probe fired its hooks; its job was captured and went
    `pending → retry` with `last_error=transient`, attempts climbing to 9 (above
    `max_attempts` 8) with **no quarantine** and no destination pause (transport failures are
    transient by design; `capturebackend.Open` makes no network call). The sandboxed
    coordinator stayed up for the whole outage (stderr empty, exit 0 on TERM). After
    `service start --apply` (pid 61435) with **no new hook**, the persisted backoff
    elapsed and both outstanding jobs published (probe at 12:29:37, attempts 10, error
    cleared); the refresher imported it (1/1). Afterwards: 0 undelivered, 0 quarantined,
    analytics stderr empty, coordinator 0.28 cores over 600 s.
  - **Mac step 3 result:** all required exercises pass on live state: Claude/Codex/Pi hooks,
    automatic new-session capture through durable work, R2 publication, refresher import
    and dashboard, restart, outage/retry without another hook, source absence with the
    previous publication preserved, and effective privacy exclusion. Correctness is accepted.
    Efficiency (coordinator about 0.29 cores, refresher about 0.47 cores) is measured and
    understood; the numeric budget is a step-4 agreement, not a step-3 gate. Next: BC-01 step 3.
- **Precautionary containment 17:46–17:52Z, lifted: suspected shared-store privacy leak
  disproved.**
  - Trigger: the refresher reported one typed `export_too_large` target (Mac Codex resource
    `~/.codex/thread_history_1.sqlite`, 6 epochs, 3,520 MiB decoded against the 768 MiB
    cap; the size check reads only the manifest, so failing sweeps download nothing).
  - Suspicion: the excluded Codex probe (`AGENT_SESSIONS_DISABLE=1`) also has rows in the
    shared stores `thread_history_1.sqlite` (1 turn, 3 items) and `state_5.sqlite` (2 rows),
    which are captured whole as `sqlite-backup` artifacts. `history.jsonl` and
    `session_index.jsonl` have no matching lines. Both hosts were stopped (Mac `service stop
    --apply` + `launchctl disable`; BC-01 `service stop --apply`), each with 0 undelivered.
  - **Disproved (implemented behavior):** `exclusion.Registry.Excludes` treats any source
    with an empty session key under an excluded record's root as excluded, pinned by
    `TestRuntimePrivacyScopesAggregatesAndUntrustedIdentity`. The record (12:03:02Z) is
    scoped to `~/.codex`. Live evidence: 7,909 Codex `policy_denied` discovery issues are
    active, 5,932 first seen at generation 419 and 1,957 at 420, including all five shared
    stores; no Codex resource was captured or observed after 12:02:55Z (newest 11:56:58Z),
    while Codex sessions and Claude resources kept being captured. Nothing excluded reached
    R2, and the next epoch would not have been taken.
  - Consequence (limitation, fail-closed by design): one Codex exclusion stops all Codex
    shared-resource capture on that root while the record exists. It is reported explicitly
    as `policy_denied` discovery issues, not silently. BC-01 has 0 exclusion records.
  - Lifted at 17:52:43Z: Mac `launchctl enable` + `service start` (pid 48472); BC-01
    `service start` (pid 4183307, 0 restarts). Two minutes later: Mac 21,558 published /
    511 superseded, 0 quarantined, destination unpaused, still 0 Codex resources after the
    exclusion; BC-01 47,756 / 58, 0 quarantined, unpaused.
  - **Unbounded epoch growth (design question, not contained by itself):** every unproven
    rewrite of an artifact begins a new full epoch and the latest manifest retains all
    sealed epochs (contract §1), so local reclaim keeps them too. Mac: `~/.claude.json`
    (scrubbed config) 206 epochs in 8 days; `thread_history_1.sqlite` gains 587 MiB per
    Codex run; Claude model-catalog cache 20 epochs; 1,064 MiB of sealed-epoch payload
    across 65 sessions. BC-01: 37 MiB across 94. Old job rows keep full manifests, so
    storage grows quadratically: Mac queue 1,290 MiB (manifests 578 MiB, 544 MiB of it
    in 135 sessions with ≥ 10 rows against 8 MiB latest-only); BC-01 1,447 MiB. Needs a
    measured retention design and operator approval (like enrichment retention).
- **Operating step 3, BC-01 activation (2026-11-05 12:49Z–, `57d8400`), drained (contained 17:48–17:52Z, see above):**
  - Preflight: unit `disabled`/`inactive`, no coordinator, installed `.bin` `9373dca4…`,
    launcher `6498537e…`; queue 10,470 pending / 19,113 published / 5 retry, destination
    unpaused; spool 1.5 GB, 41 GB free.
  - Backup `<release>/backup-pre-57d8400-20261105T124920/` (queue via `backup()`,
    `quick_check` ok; payloads 27,541, hints 534, native evidence 250 copied and counted;
    installed wrapper/bin/launcher/`path_fence.py`, unit + drop-in, Claude/Codex/Pi hook
    configs; 28,338 files in `SHA256SUMS`, 1.6 GB).
  - Schema `f94670a..57d8400`: only two additive `CREATE INDEX IF NOT EXISTS` partial
    indexes. Rehearsal on a copy with the staged binary: `resume` rc 0, 15.9 s, 39 MB RSS,
    job counts unchanged, both indexes present, installed `9373dca` still reads the migrated
    copy, real queue hash unchanged; copy removed.
  - Swapped `.bin` (`a572a0bd…`, `--version 57d8400`) and launcher (`91a49ec3…`) by
    same-directory rename after `sha256sum -c`. `path_fence.py` left in place (unused by the
    new launcher). `hooks install --apply`: only the Pi extension changed
    (`3ea4f4bd → b3ec22b0`, the drift predicted in step 2); plan clean afterwards.
  - `service start --apply` 12:51:10Z (`daemon-reload` + `enable --now`; unit file
    unchanged), pid 1832897, 0 restarts.
  - **Catch-up, not a loop:** the coordinator was off since Oct 29, so discovery found
    17,903 new sources (16,737 Pi, mostly `artifact` role, mtimes mostly < 7 d) and 6,148
    changed versions. 26,154 sessions got 0 new sequences and 4,449 got 1; the 15 with
    ≥ 8 have 55–425 sources each. Pending jobs rose 10,433 → 19,596 while sources were
    captured (pending sources 19,587 → 1 by 14:11Z, about 340/min), then began draining
    (19,596 → 16,221 by 14:11Z). Throughout: 0 quarantined, destination unpaused, no
    restarts, RSS 63–283 MB, about 1.9–2 cores of 4 (load1 2–5.6), spool 1.5 → 2.5 GB,
    38.7 GB free.
  - Drained to 0 pending by 17:23Z (the slow tail came from 36 sessions, then 3 Claude
    sessions, delivered serially per session; 1 pending row had an attempt). Settled
    0.054 cores over 600 s, RSS 155 MB, 0 restarts. Refresher: `bc01` 41,466/41,467
    imported, 0 errors, newest import 2 s old; `mac` 14,478/14,479 (the one
    `export_too_large` target above).
  - Restart: exercised by the 17:48–17:52Z containment stop/start and the outage exercise,
    with no lost or stuck work (0 restarts by systemd).
  - **Hooks (18:00–18:16Z, own throwaway "Reply with the single word OK" probes in empty
    temp dirs):** Claude 2.1.318 and Pi 1.5.0 `-p` probes each changed both inbox
    directories, added one native-evidence row (267 → 270 over the three hook-driven probes)
    and drained to 0 undelivered. **Codex 0.163.0: persisted hook trust is stale** (same as
    the Mac before re-approval; `config.toml` unchanged since 00:59Z, 11 `trusted_hash`):
    a normal `codex exec` ran no hook and the inboxes did not change. A per-invocation
    `--dangerously-bypass-hook-trust` diagnostic probe fired SessionStart/UserPromptSubmit,
    changed both inboxes and drained, so the installed hooks work. We do not write trust
    hashes; the operator must re-approve in BC-01 Codex `/hooks`.
  - Discovery-only capture of the unhooked Codex probe: rollout written 18:00:11Z, observed
    18:03:32Z, captured 18:04:03Z, both jobs published. A BC-01 discovery generation takes
    about 5 minutes (the Pi root needs many 1,365-entry slices; Codex/Claude walks finish
    early and wait), so hookless sessions wait up to one generation. Freshness item for
    step 4.
  - **Destination outage/retry without another hook (18:17–18:25Z):** `unshare -rn` is
    blocked by AppArmor (`apparmor_restrict_unprivileged_userns=1`); a transient
    `systemd-run --user -p RestrictAddressFamilies=AF_UNIX` unit was verified to block
    outbound IP (`EAFNOSUPPORT`). `service stop --apply`, then the identical wrapper/config
    ran in that unit; a throwaway Pi probe fired its hook. Nine Claude/Codex/Pi jobs went
    `pending → retry`/`transient`, attempts reaching 9–10 (above `max_attempts` 8), **no
    quarantine**, destination never paused. After stopping the unit and `service start
    --apply` (pid 224893, 0 restarts), with **no new hook** (hints dir mtime unchanged), all
    nine published by 18:25:25Z. Transient unit collected (0 remaining).
  - **Source absence (18:26–18:30Z, own first Codex probe):** rollout moved into a private
    hold dir. No trigger: unchanged. One `capture-v1 hint` → `pending → absent`, published
    sequences 1–2 preserved. Restored (bytes identical by SHA-256) plus one hint →
    recaptured, sequence 3 published, attempts 1, no error; no churn over the next 80 s.
  - Consumer (Mac refresher, 18:43Z): of 12 BC-01 sessions captured since 18:00Z, 10
    imported at or beyond their queue sequence, 1 Codex session behind (sweep lag), and the
    BC-01 `~/.codex/thread_history_1.sqlite` resource now also `export_too_large` (our
    Codex probes each rewrite the 442 MiB DB, adding a full epoch). Status contract:
    57,335 expected targets, 0 endpoint/incomplete errors, 2 `export_too_large`. A full
    refresher sweep now takes about 19 minutes (status lines 18:19 and 18:38Z; native
    sample shows DuckDB planning/reads, not a hang), a step-4 freshness/budget item.
  - State at 18:45Z: Mac pid 48472, BC-01 pid 224893 (0 restarts, 38 GB free); both 0
    undelivered, 0 quarantined. `thread_history_1.sqlite` latest manifest: Mac 6 epochs /
    1,232 MiB encoded (about 205 MiB per Codex run; no new epochs while the Mac Codex
    exclusion record denies Codex shared resources), BC-01 4 epochs / 91 MiB (about 23 MiB
    per run). Payloads: Mac 2,778 MiB of an 8 GiB quota, BC-01 929 MiB of 6 GiB. The
    retention horizon is a step-4 measurement and an operator retention decision.
  - Remaining for BC-01 step 3: operator Codex `/hooks` re-approval on BC-01, then a
    normal Codex probe and the effective privacy exclusion probe (Codex, mirroring the Mac;
    a Codex exclusion denies Codex shared resources on that root, the least-impact agent).
- **Operating step 3, Mac activation attempt 5 (2026-11-05 01:09Z–07:41Z, `ec43cff`), interim soak:**
  - PR #51 (OW: per-call `allowed` memo `4f46e11`, per-call ownership memo `41d4d9f`,
    tests and benchmark `fdf9bb8`, macOS temp-home test fix `86d233a`) merged as
    **`ec43cff`**. The first CI run failed on `Tests (macos-26)` (`Policy calls = 1, want 2`)
    because of `/var` → `/private/var` in the test home. Root-caused and fixed in the test;
    then all 7 jobs green. Canonical gate `p-ow-canon1` exit 0. Benchmark with external
    artifacts: about 3,067 ms to 144 ms per discovery call.
  - Build `p-rel-build5`, REPRODUCIBLE=yes: darwin `2d334eb5…`, linux `0dc45ea1…`, staged
    at `merged-ec43cff/` on the Mac and BC-01. Backup
    `<release>/backup-pre-ec43cff-20261105T010938/` (`quick_check` ok, 19,167 payloads).
  - **Settled about 0.50 cores under launchd** (10-minute CPU-time delta, 1 job), down from
    0.78 (attempt 4) and 1.1–2.3 (attempt 3). 0 undelivered, 0 unrepaired; the quota gap
    grows only with new sessions.
  - Background-QoS profile of a diagnostic-only `ec43cff` build (`p-diag-pprof6`, about
    0.52 cores): ownership cost removed (`classifyExternal` 48 s to 0.4 s). Remaining:
    `capturepolicy.Resolve` 25 % (Lstat per component, twice per entry: `evaluateAllowed`
    and `runtimeFilesystemPolicyWith`), `privacyOwner` leaf Lstat 12 %, hint upserts 12 %
    (mostly statement parse/exec on unchanged rows), inbox-lock `syncQueueTree` fsync of
    every ancestor on each 100 ms tick about 4 %, native inbox about 8 %.
  - Restarted under launchd at 01:35:09Z as an interim soak (behaviour correct, bounded
    background cost). Delegated: **RS** (`perf/discovery-resolve-memo`) per-call Lstat memo
    for discovery `Resolve`/leaf metadata; **HI** (`perf/runtime-idle-io`) hint-upsert
    statement reuse and inbox-lock fsync only when durability requires it.
- **Operating step 3, Mac activation attempt 4 (2026-11-04 22:28–22:52Z), correct but contained on efficiency:**
  - PR #49 (EQ: quota-gate backoff `bf4a60e`, partial claim indexes `f2e1000`, macOS
    test portability `2b41faf`) merged as `62a37a2`. The first CI run failed on
    `Tests (macos-26)`: `policy callbacks 9, want 6`, because `/var` is a symlink to
    `/private/var` and the path picked up an extra identity. Root-caused and fixed in the
    test, not rerun; then all 7 jobs passed. PR #50 (DP: per-call policy memo `d926699`,
    tests and benchmark `1526e4c`; benchmark 2.43 s to 1.15 s per discovery call) merged
    as **`068b013`**, all 7 jobs green. Canonical gates `p-eq-canon1` and `p-dp-canon1`
    exit 0.
  - Build `p-rel-build4`, REPRODUCIBLE=yes: darwin `69e25e79…`, linux `ca0a258b…`,
    staged at `merged-068b013/` on the Mac and BC-01 (launcher `91a49ec3…` unchanged).
    Backup `<release>/backup-pre-068b013-20261104T222812/` (`quick_check` ok, 19,133
    payloads). Swapped `.bin`, ran `launchctl enable`, then `service start --executable`.
  - **Behaviour correct:** 0 undelivered throughout, 1,562 repairs `repaired`, and the
    quota gap grows only with new sessions (1,409 to 1,446). Inbox files are kept
    (316 to 321), with no spin.
  - **Settled CPU about 0.78 cores under launchd**, measured as a 10-minute CPU-time
    delta with 13 real jobs. Down from 1.1–2.3 cores in attempt 3, but too high.
    Contained at 22:52:51Z (`service stop`, then `launchctl disable`).
  - Diagnostic-only build `p-diag-build3` (snapshot `p-diag-068b013`; `p-diag-build2`
    used a stale snapshot name and is not evidence). Two bounded foreground runs on live
    state: about **0.23 cores** at default priority and about **0.64 cores** under
    `taskpolicy -b`, the same QoS as the plist's `ProcessType=Background`, with
    comparable job counts. Efficiency-core scheduling roughly triples CPU time for the
    same work.
  - Profile under background QoS (`p-diag-pprof5`): discovery is about 73% of samples.
    Per-entry `ReadOwnership` opens a new read-only SQLite connection for every Claude
    external artifact (about 28%, through `privacyOwner` and `LookupOwner`).
    `classifyExternal` re-runs `allowed` on the owner's main and `.zst` coordinates for
    every artifact (about 14%). `Resolve` Lstat per component in
    `runtimeFilesystemPolicyWith` takes about 14%, hint upserts about 7%, and the native
    inbox now about 5% (was 22%).
  - Delegated: **OW** (`perf/discovery-ownership-memo`, worktree
    `memory-wt/discovery-ownership`) is to add per-discovery-call ownership and
    owner-coordinate memoization, with decisions unchanged and live capture and
    delivery rechecks untouched.
- **Operating step 3, Mac activation attempt 3 (2026-11-05 05:22–05:42 local / 18:22–18:42Z), contained again:**
  - PR #48 merged as **`c339e58`** (tree = `632d596`) after `Tests (macos-26)` passed on run
    attempt 6 (attempts 1–5 cancelled with 0 steps: hosted macOS arm64 runner not acquired
    during a GitHub Actions incident). Post-merge CI `41881213837` all 7 green.
  - Backup `<release>/backup-pre-632d596-20261104T182039/` (SQLite-API queue backup, APFS
    payload clone 19,122 files, `.bin`/launcher, `SHA256SUMS`). Swapped `.bin` to `632d596`
    (`54d5e07d…`). `service --action start --apply` needs `--executable <wrapper>` (plan with
    it is byte-identical to the installed plist; without it `SafePath` rejects the empty path).
  - **Livelock fixed live:** within 2 min the churning session published its newest sequence
    (527/527, was 73/510), all 1,562 repair rows `repaired`, no new jobs for 15 min.
  - **Idle CPU remained ~1.1–2.3 cores** with no work; contained at 18:41:58Z
    (`service stop`, `launchctl disable`). Queue (WAL-consistent `mode=ro`): `quick_check` ok,
    0 pending, dest unpaused. Note: `immutable=1` ignores an existing `-wal`; use it only when
    no `-wal` file exists (an immutable copy produced a stale ref and a false
    "payload integrity failure" in a disposable replay).
  - **Root cause (CPU profile of a diagnostic-only uncommitted build of `632d596`, foreground
    on live state, 421 s):** `enrichment_work` is at `MaxEnrichmentWork` = 4,096 rows, all
    terminal `attached`; nothing ever removes attached rows, so the "bounded count" is a
    lifetime cap. Every `AcceptNativeEvidence` then fails `ErrQuota` in
    `enqueueEnrichmentTx`, rolls back, and `drainNativeEvidence` leaves the file and retries
    32 files every 100 ms (311 files stuck, each with a full policy check): ~22 % of samples.
    `retryEnrichmentAdmission` re-tries the same quota-gap job (1,409 jobs with
    `enrichment_gap='quota'`) every enrichment tick. Discovery per-file policy
    `Stat`/`Lstat` is ~39 % (separate efficiency item, measured after the fix).
    Copy replays cannot reproduce delivery (no credentials pauses the destination; a local
    backend lacks remote objects), so live foreground profiling was used.
  - Delegated: **EQ** (`fix/enrichment-quota-spin`, worktree `memory-wt/enrichment-quota`):
    stop quota-blocked native-evidence/admission retries every tick (autonomous retry on
    capacity change or bounded backoff; files deleted only after adoption; gap stays
    visible) + partial indexes for the `enrichment_work`/`optional_repairs` claim scans.
    **DP** (`perf/discovery-policy-cost`, worktree `memory-wt/discovery-policy`): per-Discover
    memoization of policy inputs, decisions unchanged, live capture rechecks untouched.
  - **Retention decision (ER design brief, read-only):** live budgets: enrichment metadata
    29.9 MB / 128 MiB (22 %), native ~0 %, enrichment-held payloads 4 MB (16 objects, all
    shared with raw manifests). Only the 4,096 row count binds. Counting only non-terminal
    rows would just move the lifetime limit to ≈18k rows (byte budget), so the real fix is
    retirement of attached rows/results/holds once no retained job head references them
    (plus DELETE budget triggers, which are missing today). Retirement deletes local spool
    evidence and changes the enrichment contract ("Work/result rows have bounded count"),
    so it is **not** part of this acceptance: recorded as a follow-up gap needing a measured
    design and operator approval. Until then enrichment for new sessions stays a visible
    `enrichment_gap='quota'` (contract disposition: expose missing provenance), and raw
    capture is unaffected.
- **Operating step 3, Mac activation attempt 2 (2026-11-05 00:50–01:37), contained again:**
  - HA fix `f0edd33` accepted (one clause in `recordHintOwnership`; RED `ha-red1`, GREEN
    `ha-grn1`, check `ha-chk2` `CHECK_RC=0`, hand mutants killed; limit: no dedicated
    "tolerate any ENOENT" mutant). PR #47 CI `41836779407` all 7 green; merged as
    **`0721bdf`** (tree = `f0edd33`); post-merge CI `41839084452` green. Reproducible build
    `p-rel-build2`: darwin `40a2e19b…`, linux `6a273703…`; staged in `<release>/merged-f0edd33/`
    on both hosts (launcher unchanged). Disposable-copy replay of the live Mac spool: alive at
    the 90 s timeout, 338 hints drained, the 5 absent-source hints became `absent`.
  - Swapped Mac `.bin` to `f0edd33`, `launchctl enable`, `service start --apply` at 00:50:59.
    Coordinator stayed up (runs=1, never exited). Backlog: pending sources peaked ~6,200 and
    drained by ~01:25 (≈230/min); 20,020 captured, 5 absent, 0 quarantine; published
    20,308 → 20,848; RSS 110–227 MB; CPU 230–325 % throughout.
  - **After the drain CPU stayed ~2.6 cores** (the Oct 29 containment symptom): one Claude
    session (24 optional artifact sources, one source with 23 epochs) produced ~25 superseded
    jobs/min (last published seq 73, newest 510), withheld count sawtooth 14 → 5 → 14, its
    repairs at 14–15 attempts. Two smaller sessions showed 46 and 12 superseded. Contained
    at 01:37 (`service stop --apply`, `launchctl disable`); queue `quick_check` ok, dest
    unpaused, 0 quarantine, 1 pending job.
  - **Diagnosis (code-read, to be confirmed by RED test): raw-delivery / optional-repair
    livelock.** `beginOptionalDelivery` upserts every selected optional row to `delivering`
    (including rows already `repaired`); concurrently `optionalRepairNext` repairs one epoch
    and calls `selectionView`, writing a new view that supersedes the job in delivery;
    delivery's `finish()` then sets **every** `delivering` row of the session to `retry`;
    `unavailableLatch` re-latches them, the next attempt fails `errOptionalUnavailable`, the
    forced re-selection withholds them again and no job reaches CAS. Each new view also
    re-verifies every non-optional payload (`selectionView`), consistent with the CPU.
  - Fix delegated to worker **OR** (worktree `memory-wt/repair-livelock`, branch
    `fix/optional-repair-livelock` from `0721bdf`). Mac state now: `f0edd33` installed,
    capture contained, hooks current, refresher healthy on the old analytics source.
  - **OR accepted: `632d596`** (diagnosis confirmed). `beginOptionalDelivery` records rows
    that were `repaired`/`verified_attachment_pending` before marking them `delivering`;
    `finish()` restores exactly those rows, and only when the job was superseded. Other rows
    still become `retry`; completed deliveries still settle `repaired`; never-verified epochs
    stay latched; restart conversion is unchanged. `unavailableLatch` split into
    `optionalRepairState` + `latchBlocks` (no behaviour change). New
    `optional_livelock_test.go` (3 tests), no existing tests changed. RED `or-red1` (rc 1:
    wrong states after superseded delivery; "published optional sources after 6 rounds: 0,
    want 3"), GREEN `or-green1` rc 0, full check `or-check3` `CHECK_RC=0` (sessioncapture
    196 s). Hand mutants: latch-never-blocks, no-prior-recording killed; restore-regardless-
    of-superseded survived until `TestCompletedDeliverySettlesVerifiedAttachmentPending` was
    added (killed `or-mut5`). Committed files byte-identical to `or-4`. Limits: hook-driven
    deterministic race (not a real concurrent race); each repair still writes a new view
    (bounded churn, now convergent); no gremlins/standalone gocognit/CRAP run.
  - PR **#48**, CI `41853992792`: 6 jobs green; `Tests (macos-26)` cancelled three times
    with **0 steps run** ("job was not acquired by Runner of type hosted", macOS arm64
    capacity notice; githubstatus showed an open Actions incident). Not a code result;
    re-queued for runner acquisition only. Merge waits for a native macOS pass.
  - Reproducible build `p-rel-build3` (REPRODUCIBLE=yes): darwin `54d5e07d…`, linux
    `70309c28…`; staged and checksum-verified in `<release>/merged-632d596/` on the Mac and
    BC-01 (launcher unchanged, `91a49ec3…`). Installed binaries are untouched. A copy replay
    would need a live R2 or an empty local backend (which would republish everything and
    not reproduce the remote state), so live confirmation is a watched reactivation.
- **Operating step 3, Mac activation attempt 1 (2026-11-04 23:46–23:58), contained again:**
  - Preflight ok (agent not loaded, no writer, dest unpaused). Backup at
    `<release>/backup-pre-f94670a-20261104T234646/` (SQLite-API queue backup `quick_check` ok,
    APFS-cloned payloads 18,456 files, b8cbed2 `.bin`/launcher, pre-update Pi extension,
    refresher/dashboard plists, `SHA256SUMS`).
  - Swapped `.bin` (`365db4df…`, `--version f94670a`) and launcher (`91a49ec3…`) by
    same-directory rename. Next refresher sweep on the new binary: 0 discovery/endpoint/
    target errors, 0 unimported, stderr empty. `hooks install --apply`: only the Pi
    extension changed (`5042d95e → 68b02350`); plan now clean for all three agents.
  - `service start --apply` first failed: launchd held the Oct 29 containment **disabled
    override**. Activation authority covers lifting it (`launchctl enable`, archive
    precedent). The coordinator then started, migrated the queue (staged `work-status`
    reads it) and **exited rc 1 after ~20 s**; foreground reproduction:
    `sessioncapture: source absent` in 0.8 s.
  - **Root cause (production defect, f94670a and earlier):** `drainHints → Hint →
    recordHintOwnership → RecordOwnership → check(…, false)` Lstats the original source; a
    classified Claude `main` hint whose source was deleted returns bare `*sourceAbsence`,
    which neither `recordHintOwnership` nor `drainHints` tolerates, so `inboxLoop` stops
    `Run`. The hint is never removed, so every restart crash-loops. Mac inbox: 337 hints,
    5 with absent sources (2 main, 3 subagent). Disposable-copy repro: all hints → rc 1;
    absent-source hints removed → runs to timeout and drains the rest. BC-01 inbox: 499
    hints, 89 absent (3 main), so BC-01 would fail the same way. `HintBatch` (discovery)
    shares the path.
  - Containment restored: `service stop --apply`, then `launchctl disable` (override back to
    `disabled`). Current Mac state: f94670a binary/launcher and updated hooks installed
    (hooks keep writing hints; the inbox format is unchanged), queue migrated, **no
    coordinator**, refresher healthy on old analytics source. Policy directory untouched.
    Binary-only rollback remains possible (b8cbed2 reads the migrated queue).
  - Fix delegated to worker **HA** (`5c4b6b56-8b58-46e`, worktree
    `memory-wt/hint-absence`, branch `fix/hint-source-absence` from `f94670a`): treat pure
    source absence in ownership recording as non-fatal (raw hint already admitted; capture
    later classifies `absent`), keep every other failure fatal; RED/GREEN, scoped mutation,
    full check. Then stack → CI → merge → rebuild → re-stage → resume step 3.
- **Operating step 2, BC-01 staging and coordinated switch plan (2026-11-04):**
  - BC-01 (x86_64, 41 GB free, unit inactive, installed `.bin` `9373dca4…` untouched):
    linux/amd64 `ca002180…` staged 0700 in `<release>/merged-f94670a/`, `sha256sum -c` OK,
    `--version` `f94670a`. Staged read-only checks match the Mac (policy-pauses `null`,
    service unit byte-identical, same pre-existing Pi drift `3ea4f4bd → b3ec22b0`,
    `work-status` needs the migration). Installed status: 29,082 sources, 22 pending,
    10,475 pending publications, 5 retrying, `Coverage=partial`, 20 step errors (last
    `delivery: context deadline exceeded`), 2,431 active discovery issues.
  - BC-01 migration rehearsal on a removed copy (Python SQLite backup; no `sqlite3` CLI):
    staged `resume` rc 0, 6.9 s, 35 MB RSS; 10,470 pending / 19,113 published / 5 retry
    and 27,541 payload files unchanged; 32,258 `payload_refs` backfilled, 0 reclaim;
    installed binary still reads it; real queue hash unchanged.
  - Launcher: installed `launcher.py` equals `b8cbed2:tools/capture-delivery/launch.py`;
    merged `launch.py` (`91a49ec3…`) projects config credential references (same R2 names
    here) and keeps `run` local-only on credential failure. Staged beside both binaries.
  - Mac analytics: refresher/dashboard run the c9749dac Darwin runtime with e8c96f8 source;
    `requirements.txt` is identical at c9749dac/e8c96f8/f94670a, so the runtime is reused.
    f94670a `tools/analytics` staged at `~/.local/lib/agent-sessions-analytics/<f94670a>/source`
    (32 files byte-verified against git). No DuckDB DDL change; the merged importer accepts
    the legacy manifest fingerprint.
  - **Order is fixed:** the merged reader requires the export `manifest_sha256` that
    b8cbed2 lacks, while the installed reader tolerates the extra key. So swap capture
    binary + launcher first, then the analytics source. The hint inbox is unchanged
    b8cbed2..f94670a, so hooks firing during the swap stay readable.
- **Operating step 2 read-only survey (2026-11-04, no changes):**
  - Installed Mac `agent-sessions.bin` is the b8cbed2 Darwin build (`6beae15a…`), not merged
    source. The capture plist exists and matches `ServicePlan`, but is not loaded. The
    refresher (`com.agent-sessions.publication-refresh`) runs e8c96f8 analytics source and
    calls `capture-v1 current-heads` through the capture wrapper, so **swapping `.bin` in
    place is not dormant**. It changes a live consumer, and new artifacts must be staged
    at a separate path until the coordinated switch.
  - Hooks: Claude 8 and Codex 6 `capture-v1 hook` entries, plus the Pi extension; all point
    at the wrapper, with no legacy entries. `hooks --action plan` (no `--apply`) is the
    read-only drift check.
  - Upgrade precedent: stop → preflight (SHA, `policy-pauses`, binding row, job counts)
    → SQLite-API backup with `quick_check` → atomic `.bin` swap → `resume` only if a
    destination pause exists → start (activation). The old b8cbed2 installer used
    `restart`, which fails on an unloaded label; it must not be replayed.
  - Open decisions for step 2: build from post-#46 `main` (not `d975961`); cross-compile
    darwin/arm64 and linux/amd64 inside the Docker admission, with a recorded revision
    and SHA256 (snapshots carry no `.git`, so no Go VCS stamp); the first writable open
    of the real queues installs the new schema and triggers, so it belongs to activation
    (step 3), not staging; analytics source/runtime staging must not interrupt the
    running refresher's projection lock.
- **Wave 4 workers HM, MO3 and RB completed and were accepted** (see entries above);
  merged in stack #46 (`f94670a`).
- **Operating step 1, read-only reconciliation (2026-11-04, aggregates only, no activation):**
  - **Mac:** the capture LaunchAgent `agent-sessions-capture-experimental` has a plist with
    RunAtLoad, but its service is **not loaded**. The wrapper script runs `launcher.py`
    with `agent-sessions.bin`. `com.agent-sessions.publication-refresh` is running (60 s
    interval, `--max-bytes` 805306368) and the dashboard plist is present.
    - Queue `20261029-e3d6dd6e8bab4e8f` was last written on Oct 29 (DB 995 MB, WAL 12.5 MB).
    - Jobs: 20,308 published and 5 superseded. Runtime sources: 19,413 captured.
      0 quarantine rows and an empty `discovery_error`.
    - Config: entries_per_root 4096, max_inventory_records 32768, operation_seconds 60,
      max_attempts 8, poll 10 s, spool 8 GiB, min_free 4 GiB. No cadence keys, so
      Q=30 s and M=300 s apply once the new binary runs.
  - **BC-01:** the user unit file `agent-sessions-capture-experimental.service` (with
    drop-in) is present, but **no unit is loaded or active** and no capture process runs.
    - The queue was last written on Oct 29 (744 MB).
    - Jobs: **10,470 pending**, 19,113 published, 5 retry. Runtime sources: 29,060
      captured and 22 absent. 0 quarantine rows. `discovery_error='transient'`.
    - Config as on the Mac except spool 6 GiB.
    - The 10,470 pending jobs are accepted, unpublished work. They must be preserved and
      drained by the new binary; never reset them. They are also the first live test of
      the GAP-03 coalescing and reclamation on a real backlog.

## Next action: separately authorized operating acceptance

**Progress (2026-11-05):** steps 1 and 2 are done. Step 3 has passed on the Mac and is
complete on BC-01 except the Codex hook and Codex exclusion checks. Step 4 has not
started. The operator authorized steps 1–4 on 2026-11-04 (see
[Production delivery — November 4](#production-delivery--november-4)); step 5 stays
excluded. The current ordered actions are in the
[resume-here checkpoint](#november-6-2057z-nov-5-resume-here).

The original text below is kept as the plan. The October 30 source-publication/merge
request and documentation request did **not** grant
activation, deployment, original-source/R2/BC-01 SSH inventory, host/runtime changes,
analytics refresh, release publication, archive reset/rebind/deletion or legacy retirement.
The operating plan and authorization boundary are posted at:
https://github.com/mich-murphy/memory/pull/30#issuecomment-6515658801

Once compatible operating authority and supported execution arrangements are in place,
the **assistant owns execution and follow-through**, not a list of manual operator chores:

1. **Reconcile current state without activation.** Read the contract and this brief;
   inspect actual installed binaries, hooks/wrappers, supervisors and consumer coordinates
   on Mac then BC-01. Inventory eligible sources and pending/accepted work under the
   permitted access scope. Distinguish excluded, unreadable, absent and unavailable
   history. Preserve current identities, effective local/remote opt-outs, custom roots,
   credentials and archive binding. Do not rely on the dated `b8cbed2`/off observation.
2. **Prepare a source-aligned coordinated delivery.** Build/verify required artifacts
   from the merged source, not the old `74c00f4` snapshot. Coordinate capture, the shared reader,
   credential launchers/static wrappers and Mac analytics source/runtime. The prepared
   Linux analytics venv is **not Darwin ABI-compatible**. BC remote-current analytics
   uses a Mac-local reader bound to BC identity/archive, not interactive SSH as a reader.
   Dormant staging must not call commands that start capture or refresh consumers.
3. **Accept Mac first, then BC-01.** Under explicit activation authority verify actual
   native Claude/Codex/Pi hooks and automatic new-session capture through durable work,
   actual conditional R2 publication/readback, the authoritative reader, importer, event
   index and report. Retain exact identity/body/epoch coverage and visible partial/errors.
   Candidate Linux-native behavior must be demonstrated, not borrowed from the legacy
   prototype. Exercise restart, destination outage/retry without another hook, source
   absence and effective privacy exclusions; preserve the previous readable publication.
4. **Accept combined operation.** Reconcile both inventories, fresh-reader consumer
   sweeps, two-host independence and ongoing automatic capture. Measure representative
   CPU/RSS/I/O, request cost, settled work, freshness, largest records and offline spool/
   retention horizon in the actual vertical slice. Agree and meet operating budgets;
   do not substitute a timer, HTTP 200, upload success, green CI or old report for health.
   Diagnose/fix ordinary failures and rerun the unchanged relevant gates before acceptance.
5. **Only after acceptance, with separately reviewed scope:** consider final defaults/
   legacy retirement and deletion of reviewed retired keys. Recheck writer fences and
   inventory drift; exclude every new/current/in-flight/policy key and the already replaced
   global binding. Never blindly replay an old inventory, reset or deletion plan.

Until then, the accurate status is **merged, development-qualified and running live on
both hosts, not operationally accepted**. A new artifact, commit, green CI run or source merge does not cross this gate.

## Preservation, execution and evidence rules

- Preserve accepted unpublished payloads, pending spool and queue DB/WAL/SHM, original
  sessions/research, effective privacy, identities/roots/binding, advanced analytics/body
  caches and live locks. Use supported SQLite backups when authorized; never restore a
  stale database over newer accepted state, delete a lock to bypass a writer, or recreate
  deleted generated state incidentally. Preserve unrelated branches/worktrees/stashes;
  do not restore intentionally deleted `CLAUDE.md`.
- `service install --apply` **starts capture**; `capture-v1 run` and
  `publication_refresh --once` are activation/mutation, not dormant staging.
- Follow [quality gates](quality-gates.md). The shared validation semaphore and
  `~/.config/agents/validation.md` were retired on 2026-11-05; the private `launch.py`
  harness now admits by its owned-container limit and per-container CPU/memory caps.
  Use pinned Nix in Docker;
  source/dependencies/runtime and every module-cache alias/prepared tool must be read-only,
  actual validation network-none, both temporary variables on executable VM-native EXT4.
  Preserve native Go 600s/package, race/shuffle, fixtures/history counts and safety gates.
  Current policy supplies no local native-Darwin admission: do not invent one, use bare
  host validators, reinstall `bc-heavy`/Pueue or re-enable capture for validation.
- Parent owns the sole brief, integration, scheduling and final evidence acceptance.
  Delegated workers use **medium effort**, with explicit scope/ownership. One CI watcher
  at a time: `gh run watch <run-id> --interval 60 --exit-status`; no polling loops or
  `gh pr checks`. Post full URLs immediately when human approval is actually required.
- This documentation-only reconciliation archived the prior handoff body verbatim,
  retained the earlier `74c00f4`/October 24 evidence body unchanged, checked `git diff --check`
  (including the new archive), and resolved all 86 relative documentation links/anchors
  in the edited current pages. No Go/Python test suites, builds, new CI runs or
  native/live/installed gates were rerun for these documentation edits.
- Current integration evidence: private
  `~/.local/state/machines-validation-pilot/memory-pr-integration-20261030/`, especially
  `parent/FINAL-ACCEPTANCE.json`, `parent/final-evidence.sha256`, and
  `ci-runtime/{LEDGER.md,local-evidence.sha256,ci-evidence.sha256}`. Parent verified
  817 local evidence files and a separate 19-file CI seal. Failed/noncanonical attempts
  remain failures/history, not green results. Earlier release/performance evidence and
  full progress ledgers remain linked from the [October 30 archive](session-capture-handoff-archive-20261030.md).
