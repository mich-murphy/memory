# 25. Engineering-practice review and data-structure prototypes (2026-11-12)

Coordinator brief. The parent session owns this file and acceptance. Status: review complete;
prototype choices made and integrated as a local five-branch stack (below), gates green on
its code tip `0f3cb7d`; nothing pushed.
Raw reports, scripts and results: [evidence 15](evidence/15-engineering-practice-review/).

## Checkout and continuation

- `/Users/mm/dev/memory`, branch `main`, HEAD `41a07ff`, clean. This file and evidence 15
  exist only on the stack's top branch until it merges.
- Integration worktree: `/Users/mm/dev/memory-wt/ds-integrate`. The stack, bottom to top, on
  `41a07ff`: `rust/round-grouping`, `rust/owner-turn-base`, `rust/commit-end`,
  `rust/authorize-borrow`, `docs/engineering-practice-review`. Local only.
- Prototype worktrees (local `proto/*` branches from `41a07ff`, never pushed; keep until the
  stack merges): `/Users/mm/dev/memory-wt/ds-owner-state`, `ds-landed`, `ds-authorize`.

## Scope and acceptance

- Request 1 (done): review against sound engineering practice, alignment to the design and
  goals, and whether delivery was in vertical slices. Reports `evidence/15/reports/W1` to `W8`.
- Request 2 (choices made): for the three data structures that do not earn their keep,
  identify options, prototype, measure, and confirm the final choice.
- Request 3 (blocked): run the `/no-comments` skill through a Sonnet subagent. The harness
  reports the skill as disabled by the operator and forbids loading it by any path.
- Operator approval (2026-11-12): the four-step plan below.
- Acceptance for request 2: baseline and at least two options per case on one table; the
  decision rule fixed before measuring (`evidence/15/prototypes/PROTO-COMMON.md`); the parent
  reruns the decisive measurements; no test, privacy assertion or gate weakened.
- Exclusions: no R2, no host changes, no transcript or credential reads.

## Review result (parent-checked unless marked)

| Criterion | Verdict |
|---|---|
| Boundaries | mostly sound; `daemon.rs` holds about five jobs and reads hints with `std::fs` |
| Extensibility | mostly sound; the locator is per-agent branching code |
| Easy to reason about | mostly sound; `plan()` 105 lines, `commit_known` 94 lines |
| Names | mostly sound; `OwnerRt`, `known_ours`, `Env`, `Acc`, `Job`, history-named test files |
| Data structures | mixed; the three cases below |
| DRY | weak in places; two `LaunchRecord` types with different digests, three fault-store copies |
| YAGNI | weak; `coordinates.rs` has no caller, `stat_len` and `Counting` have no production caller |

Delivery: 17 of 25 implementation reports are horizontal components, 7 are review rounds, 1 is
a thin end-to-end step (reviewer classification). Nothing runs end to end: `memory daemon`
opens its state and exits with `EXIT_NO_ADAPTERS`. Go capture has been stopped since
2026-11-09 (A0) with no recorded bound on how long it may stay off.

## Prototype results and choices

Decision rule, fixed in advance: eligible only if tests pass unweakened; then fewer invalid
representable states and the named bug prevented by type; then fewer decision points; then
fewer lines; runtime only vetoes or breaks ties. Timings below are the parent's reruns of the
workers' release binaries, interleaved, with no builds running (`evidence/15/parent-rerun/`).
"Compiled" misuse results were rerun by the parent unless marked [worker].

### `OwnerRt` (`daemon.rs`): choose D

| | base | A `RemoteBase` | B A + `Turn` | D B + `recover` returns the base |
|---|---|---|---|---|
| Invalid shapes representable | 7 | 3 | 0 | 0 |
| Base known while recovery needed | compiles | E0609 [worker] | E0609 [worker] | E0609 [worker] |
| Parked with a retry time | compiles | compiles | E0609 [worker] | E0609 [worker] |
| Capture without recovering | compiles | compiles [worker] | compiles | E0425 |
| `daemon.rs` lines (+/−) | – | 33/17 | 64/38 | 78/53 |
| Focused tests (78) | pass | pass | pass | pass; whole crate 231 |
| Idle round, 1,000 owners, µs (unchanged / deferred / parked) | 5667 / 4200 / 10753 | 5744 / 4233 / 10754 | 5744 / 4207 / 10713 | 5865 / 4289 / 10808 |

Base against itself: 5776 / 4252 / 10809, so no option differs. C (one enum for the whole
phase) was rejected by analysis, not built: it removes no state B does not, and adds a
`Parked` arm to three functions that `due` already excludes. Limit of D: writing
`let base: Option<Base> = None` in place of the `recover` call still compiles; D stops
omission, not fabrication. D's `due` reads the clock before checking for parked, which the
baseline did not; restored at integration (`396c424`).

### `Landed` (`commit.rs`): choose C

| | base | A three-variant enum | B struct + `LandedHow` | C `CommitEnd { Landed, Adopted }` |
|---|---|---|---|---|
| Invalid shapes representable | 7 | 0 | 0 | 0 |
| Apply updates on adoption | compiles | compiles | compiles | via the helper: E0308; copied inline: compiles |
| `land` cognitive complexity | 5 | 7 | 5 | 3 (+3 in `apply_landed`) |
| src / tests lines (+/−) | – | 50/32, 32/47 | 23/16, 24/40 | 104/44, 66/65 |
| Focused tests (92 + 28) | pass | pass | pass | pass; whole 429 |
| ns per commit (plain / verified / adopted) | 4772 / 5557 / 6302 | 4791 / 5607 / 6332 | 4835 / 5645 / 6395 | 4727 / 5563 / 6266 |

Base against itself: 4864 / 5507 / 6385. `size_of` 520 to 480 in every option. C changes the
`advance-on-fail` predicate lines of `check-mutations.py`; the script is updated with it.
`CommitOutcome`'s success variants stay (used by `Verdict::outcome`).

### `authorize` (`privacy/mod.rs`): choose AB2

Required of the final step: `05-privacy-review.md:46` (the final Deny/Unknown arms "should not
be removed") and `03-privacy-port.md:36` (cross-source reuse and a changed context deny).
DESIGN 8 and the amendments say nothing more. C (no re-admit) is therefore ineligible.

| | base | A borrow | AB A + exhaustive `binding()` | AB2 AB + owner stored | B shared `decide` | C no re-admit |
|---|---|---|---|---|---|---|
| New policy field left out of `binding()` | compiles; denied by re-admit [worker] | compiles; `Deny(Credential)` | E0027 [worker] | E0027 | compiles; denied [worker] | E0027; with `field: _` it compiles and **allows** [worker] |
| Unreachable owner `None` arm | yes | yes | yes | no | no | yes |
| src lines changed | – | 14 | 49 | 69 | 79 | 67 |
| Per piece at 4 KiB, ns | 9980 | 6490 | 6610 | 6714 | 6097 | 3061 |
| Per piece at 1 MiB, ns | 499931 | 491130 | 489414 | 496051 | 492800 | 488317 |

Base against itself: 9981 and 501227. The saving at 4 KiB is the dropped `admitted_clone`
admission (3.5 µs); 1 MiB differences are noise. Tests: memory-core 198, doctests 7, shell
suites 41 on every option, with 8 mechanical `&` edits. AB2 keeps both protections: the
compile error and the runtime recheck. `size_of::<AdmittedSource>()` 144 to 176.

### Found by the benchmark: a quadratic round

`reconcile_once` grouped sources by owner with a linear `find`, so a round was quadratic in
owners: 30,000 idle owners took 3.24 s per round (1,000: 5.7 ms; 3,000: 37 ms; 10,000:
410 ms). The existing 30k test uses 30 owners of 1,000 sources and did not show it. Grouping
through an index map (3 lines, same order): 72 ms at 30,000 and 2.1 ms at 1,000; focused
tests 78 pass. With that fixed, D against base at 30,000 owners: unchanged 72.9 against
72.3 ms, deferred 20.1 against 20.5 ms, parked 322.8 against 309.8 ms (4 %, ranges touch;
the clock read above was the suspected cause; see the remeasure under Integration).

## Integration (accepted by the parent, 2026-11-12)

Built by one Sonnet worker in the integration worktree from the chosen prototype commits; the
parent read the whole source diff, fixed three comments that named removed things, and ran
the gates. 14 files, +359/−207 against `41a07ff` (src 4 files +229/−130, tests 9 files
+128/−75, `check-mutations.py` 2 lines).

| Branch | Tip | Content |
|---|---|---|
| `rust/round-grouping` | `f9d8b50` | the index-map grouping; the ignored `idle_rounds` measurement in `tests/counters.rs` |
| `rust/owner-turn-base` | `d60ec49` | `RemoteBase`, `Turn`, `recover` returns the base; a parked owner is skipped without a clock read |
| `rust/commit-end` | `b1e45f6` | `CommitEnd { Landed, Adopted }`, `apply_landed(&Landed, ..)`; the `advance-on-fail` predicate of `check-mutations.py` follows the new binding name, same mutant |
| `rust/authorize-borrow` | `0f3cb7d` | `authorize(&AdmittedSource, ..)` with the re-admit kept, exhaustive `binding()`, stored owner, `admitted_clone` deleted |
| `docs/engineering-practice-review` | this commit | this brief, evidence 15, the handoff and quality-gates entries |

- Conflicts: only `Daemon::land` (D and C both change it). It takes the recovered base and
  the hint owner, sets `rt.base` from `CommitEnd::base()`, and reaches `apply` and the hint
  marking only through `apply_landed`. The landed and authorize benchmark sources are not in
  the tree; they are in `evidence/15/prototypes/p2-scratch` and `p3-scratch`.
- Gates (`rust/scripts/gates.sh`, 500 cases, parent): all 13 sub-gates 0 on `08c37f0`, seed
  673264974, and again on the final code tip `0f3cb7d`, seed 374449330 (the two differ in
  three comments only). 431 tests (373 + 30 Hegel + 28 proptest), 12 doctests, coverage
  5869/6006 = 97.72 % (floor 80), the three hand-written mutants and the window mutant
  killed, `verify-evidence.py` PASS. Not run: 2,000 cases, hosted CI (nothing is pushed),
  `cargo mutants`. Records: `evidence/15/gates/`.
- Remeasure of idle rounds at 30,000 owners, 15 interleaved runs, medians in ms
  (`evidence/15/parent-rerun/integrated-idle-rounds-30k-bisect.txt`):

  | | base + grouping | same binary again | `owner-turn-base` | `commit-end` | `authorize-borrow` |
  |---|---|---|---|---|---|
  | unchanged | 73.4 | 73.8 | 75.7 | 73.3 | 72.8 |
  | deferred | 20.5 | 21.2 | 20.8 | 20.2 | 20.7 |
  | parked | 314.9 | 308.3 | 312.7 | 316.3 | 320.4 |

  The clock-read fix removed the parked difference on `owner-turn-base`. The top of the
  stack is 2 to 4 % above the baseline in the parked row only, where the baseline differs
  from itself by 2 % and the interquartile ranges overlap; parked owners do not reach the
  commit or privacy code the upper two branches change. Recorded as not resolved from
  noise, not as a regression. At 1,000 owners no row differs
  (`integrated-idle-rounds.txt`).
- Seen, not measured or changed: a parked round costs four times an unchanged one. The
  likely cause is `parked.sort_by_key(|p| format!("{:?}", p.owner))` in `reconcile_once`,
  which formats a key at every comparison. It matters only with thousands of parked owners.
- Left as they are: the tests' reference models keep their own `needs_recover` fields
  (`commit_protocol.rs`, `capture_property.rs`); they model the protocol, not `OwnerRt`.

## Approved plan

1. Walking skeleton: zstd, the S3 adapter, a one-file capture under `rust-acceptance/`, a
   minimal restore.
2. Hold further review rounds and mutant triage until the run loop is wired; take only the
   audit, hint retention, `tracing` and the startup delay.
3. Structural fixes: the three choices above and the grouping fix (this stack); then, as the
   skeleton touches the code: unify `LaunchRecord`; make `resolve.rs:97` exhaustive; delete
   `stat_len`; move `Counting` to tests; delete or wire `coordinates.rs`.
4. Fold the amendments into `DESIGN.md` and fix the stale `AGENTS.md` lines (operator decision
   needed: `verify-evidence.py` hash-pins the design).

## Ownership

- Parent: this brief, acceptance, full gates, the docs commit.
- Integration worker (Sonnet): `/Users/mm/dev/memory-wt/ds-integrate` only. Finished.

## Open operator decisions

- Push the stack and open its PRs (`gh stack submit`), or keep it local.
- Re-enable `/no-comments`, or drop request 3.
- A bound on how long capture stays off.
- Step 4 (the hash pin).

## Next action

Step 1, the walking skeleton, on a branch above this stack. Its first increment needs no
R2: zstd piece encoding and the S3 adapter (`rusty-s3` + `ureq`) behind `Store`, tested
against a local HTTP server, with the open `cargo deny` licence decision
(`CDLA-Permissive-2.0`) settled when the dependency lands. The live one-file capture under
`rust-acceptance/<run-id>/` and the minimal restore follow (A2).
