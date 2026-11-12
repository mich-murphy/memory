# P1 owner-state report (worktree /Users/mm/dev/memory-wt/ds-owner-state), numbers UNDER CONTENTION

## 1. Design constraints read
- DESIGN 7.4: after LandingUnknown/BaseUnchanged the base is re-read from current.json before the next commit; the first write has no base (fence None). `recover_base` None = "no current.json yet", a known state.
- DESIGN 11.2/11.3: Retry -> retry time; Park/ParkOwner -> parked list (authoritative, in status.json). Parked owners are skipped (`skipped`), retry-delayed are `deferred`.
- A6: BaseUnchanged exhaustion is Transient; `known_ours` is memory only. A13.4: recover after restart is normal.
- Code fact: nothing in daemon.rs unparks an owner (grep `unpark` finds only a comment in my Turn doc). A parked owner's reason is rebuilt from status.json at start (`OwnerRt::new(Some(reason))`, base Unknown); the status builder (`reconcile_once`) reads only the reason.

## 2. Options (all branch from base 182f6b7 = main 41a07ff + benchmark commits 647a676, 182f6b7)
- base: proto/ds-owner-state-base 182f6b7.
- A: proto/ds-owner-state-a 83d8d13
  `enum RemoteBase { Unknown, Absent, Known(Base) }`; `OwnerRt.base: RemoteBase`; `needs_recover` removed; `fence()` helper; capture_owner has merged arm `Absent | Unknown => OwnerState::default()` with a comment.
- B: proto/ds-owner-state-b 341a941 = A + `enum Turn { Ready, RetryAt(Duration), Parked(ParkedReason) }`; `parked` and `not_before` removed.
- D: proto/ds-owner-state-d f7ddbe7 = B + `recover` returns `ControlFlow<bool, Option<Base>>` (None = Absent); `capture_owner(.., base: Option<&Base>, ..)` and `land(rt, base: Option<&Base>, ..)` take it; `fence()` and the merged arm are gone. No `unreachable!`, no `unwrap_or_default`, no `#[expect]`.
  Side edit forced by clippy `too_many_arguments` (8/7): `land` lost its `owner` parameter; `attached: bool` became `attached_for: Option<&Owner>` (`attached.then_some(owner)`); same behaviour.
  Clone: D clones `Base` (state, etag, position) in `recover` once per owner per round when Known. Base also cloned `b.state` once per capture; D removes that one (`map_or(&empty, |b| &b.state)`). So one clone per round remains either way; D's is slightly larger (etag + position). 30k bench shows no regression.
- C: proto/ds-owner-state-c 182f6b7, NOT BUILT, rejected by analysis:
  `enum Phase { Parked(reason), Active { base: RemoteBase, retry_at: Option<Duration> } }`. Folding loses nothing the code needs: a parked owner's base and retry time are never read (nothing unparks; restart rebuilds from the reason). But the rest of the daemon reaches the base only through `Active`: `recover`, `capture_owner`, `land` would each need a `Parked` arm that `due` has already excluded, i.e. the invariant-in-a-comment again (the exact `match rt.phase` arms at recover/land), and `settle` must update `base` and `retry_at` through a match on the same variant. C is B with an extra nesting level and more unreachable arms; it removes no invalid state B does not. Not measured.
- Type definitions above are verbatim in the branches (daemon.rs ~line 362-400).

## 3. Measurements (base | A | B | D). Script: p1-scratch/run.sh (see below)
| | base | A | B | D |
|--|--|--|--|--|
| M1 focused nextest (78 tests) | 78 pass, 1 skip | 78/1 | 78/1 | 78/1 |
| fmt --check, clippy workspace -Dwarnings | clean | clean | clean | clean |
| whole `nextest -p memory` | - | - | - | 231 pass, 3 skip |
| M2 representable/valid/invalid (base x turn shapes) | 16/9/7 | 12/9/3 | 9/9/0 | 9/9/0 |
| decision points settle/due/recover/capture_owner/land (regex: if, match, matches!, is_some, map_or) | 3/4/2/0/3 | 3/4/4/1/3 | 3/2/4/1/3 | 3/2/3/1/3 |
| clippy cognitive settle/due/recover/capture_owner/land | 4/2/3/<=1/5 | 4/2/4/2/5 | 4/3/4/2/5 | 4/3/2/<=1/5 |
| wildcard / merged arms | 0 | 1 merged arm | 1 merged arm | 0 |
| M4 src lines vs base (+/-) | 0 | +33/-17 | +64/-38 | +78/-53 |
| M5 (i) base known + recover needed / clear w/o mark | compiles | ERROR E0609 no field `needs_recover` | same | same |
| M5 (ii) parked + retry time | compiles | compiles | ERROR E0609 no field `not_before` | same |
| M5 (iii) capture with base unknown (skip `recover`) | compiles | compiles | compiles | ERROR E0308 expected `&Base`, found `&RemoteBase` |
| size_of OwnerRt | 288 | 288 | 272 | 272 |
| advance-on-fail predicate occurrences | 1 | 1 | 1 | 1 |
| M7 files / test edits / public API | - | daemon.rs / 0 / no | same | same |
M5 notes: compiled with `cargo check -p memory --locked`, scratch edits reverted, worktree clean. (i)/(ii) are prevented because the field no longer exists (the bug cannot be spelled); in A/B the single-field form `Unknown` is the only way to mark recovery. (iii) for D: skipping `recover` leaves no `base` value to pass, my scratch edit tried to pass `&rt.base` instead and got E0308; for base/A/B skipping `recover` compiles and silently captures against an empty remote.
Predicate: D does not touch the two `if let Ok(landed) = &result {` / `// Remote is authoritative` lines; no mutant text change needed. I did not run check-mutations.py nor check its other patch targets.
M6 idle `reconcile_once`, release, 1,000 owners, 8 interleaved rounds (base,a,b,d,base2), median us (min-max), under contention:
| phase | base | base2 (noise floor) | A | B | D |
|--|--|--|--|--|--|
| unchanged | 12514 (11101-26408) | 11487 (11200-13991) | 11541 | 11993 | 11833 |
| deferred | 8700 | 8290 | 8459 | 8459 | 8376 |
| parked | 23518 | 22722 | 22012 | 21896 | 22094 |
Base vs base2 differ by up to 9%; all options sit inside that: no difference.
30,000 owners, 3 rounds, base vs D (median ms; per-round times are about 1.6x the quiet earlier run, 3.2-3.6 s, due to contention): unchanged 5329 | 4919; deferred 5280 | 4593; parked 5080 | 5549. Spread (min-max) overlaps or is within ~10%: no difference.
Commands (rerunnable unchanged; ~25 min, checks out each branch in the worktree, builds release once per option, runs M1, M3, M4, M5, M6):
`bash /private/tmp/claude-501/-Users-mm-dev-memory/c32b7bd9-0169-47d4-bf70-da528e7ec2a3/scratchpad/review/proto/p1-scratch/run.sh > run.log 2>&1`
(log of this run: p1-scratch/run.log; raw benchmark: bench1k.txt, bench30k.txt.) Benchmark source: `idle_rounds_by_owner_phase` in tests/counters.rs on base; the script temporarily lets env `IDLE_OWNERS` choose N (uncommitted edit, reverted).

## 4. Recommendation (fixed rule)
All options pass M1 with no test edits. By fewest invalid states and named bug prevented by type: B and D tie at 0 invalid shapes; only D also prevents (iii). Lines: D net +25 vs B net +26. M6: no veto. => **Adopt D.** It removes the invariant comment and the merged arm, drops `recover` complexity from 3 to 2, and `capture_owner` back to no decision. Cost: one Base clone in `recover` replaces the state clone in capture_owner; `land` signature changes (attached_for).
If you want the smallest change: A (+16 net) prevents only (i). B adds (ii) for +10 lines.
Would change my mind: a quiet-machine rerun showing D regress beyond noise at 30k owners; a planned unpark path that must keep a parked owner's base (favours keeping parked separate, i.e. B/D over C); or building C and finding it removes a state D cannot.
Names: OwnerRt -> OwnerPhase / OwnerRuntime; known_ours -> unconfirmed_manifests.
Interaction with P2's option C (`Landed` split into landed vs adopted-earlier types): `land` writes the new base from the result; with separate types it is one `match` producing `Known(base)` for both (adopted keeps follow-up). D composes most simply: the fence is a parameter, so `land` only writes `rt.base` and does not read it; A/B read `rt.base.fence()` inside `land`.

## 5. Limits
Option C not built (rejected by analysis above). Noise floor is base vs base2 under contention, not a quiet machine. 30k run only 3 rounds, base and D only. Regex decision points are approximate; cognitive numbers from clippy at threshold 1 (functions scoring <=1 are not reported). check-mutations.py not run; only the advance-on-fail predicate text checked. M5 (iii) for D is a variant (cannot skip `recover` without also dropping the argument).
