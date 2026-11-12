# P2 report: Landed (case `landed`)

Branches (worktree /Users/mm/dev/memory-wt/ds-landed): base fd3c1fb (main 41a07ff + bench), A proto/ds-landed-a 505ebf4, B proto/ds-landed-b dff9f7a, C proto/ds-landed-c 637f3a5.

## 1. Design constraints read
- DESIGN 7.4 step 5: after a failed/ambiguous guarded PUT, GET current.json; ours => landed, else classify.
- A6: `known_ours` (every manifest built since last landing); a verify naming an earlier attempt is adopted, not Foreign; `BaseUnchanged` exhaustion has its own class.
- A13.5: verification GET attempted 4 times. 11.2: error classes/dispositions (Failure -> Outcome; Landed -> Succeeded).
- Adoption means this capture's bytes did NOT land: local updates and hint-done must not apply.

## 2. Options
- A: `enum Landed { Committed{etag,built}, AlreadyOurs{etag,built}, AdoptedEarlier{etag,built} }` + `etag()/built()/base()`.
- B: `struct Landed { pub how: LandedHow, pub etag, pub built }`, `enum LandedHow { Committed, AlreadyOurs, AdoptedEarlier }`.
- C: `commit_known -> Result<CommitEnd, Failure>`; `enum CommitEnd { Landed(Landed), Adopted(Adopted) }`; `Landed{how: LandedHow{Committed,AlreadyOurs}, etag, built}` and `Adopted{etag, built}` have private fields, no constructor, accessors; `CommitEnd::base()`. `Daemon::apply_landed(&mut self, _: &Landed, updates, hints, hint_owner: Option<&Owner>)` holds both effects; `land` calls it only inside `if let CommitEnd::Landed(l) = end`. `disposition`/`settle` take `Result<CommitEnd,_>` unchanged (Ok => Succeeded), so behaviour is unchanged.

## 3. Measurements
| | base | A | B | C |
|---|---|---|---|---|
| M1 memory 4 binaries (nextest) | 92/92 | 92/92 | 92/92 | 92/92 |
| M1 memory-core core_verify+core_manifest | 28/28 | 28/28 | 28/28 | 28/28 |
| fmt --check, gates clippy | ok | ok | ok | ok |
| whole `nextest -p memory -p memory-core` | not run | not run | not run | 429 pass, 3 skipped |
| M2 representable/valid | 10/3 | 3/3 | 3/3 | 3/3 |
| M4 src +/- | - | +50/-32 | +23/-16 | +104/-44 |
| M4 tests +/- | - | +32/-47 | +24/-40 | +70/-69 (incl. 4/4 in bench) |
| M5 bug compiles? | yes (omit `if`) | yes (omit `matches!`) | yes (omit `!=`) | passing Adopted to helper: ERROR; hand-copying effects into Adopted arm compiles |
| size_of success type / Result | 520/520 | 480/480 | 480/480 | 480/480 (CommitEnd) |
| M7 predicate advance-on-fail / if-match | 1/1 | 1/1 | 1/1 | 0/1 |
| non-test users of CommitOutcome success variants | verify.rs + 3 in commit.rs | verify.rs only | verify.rs only | verify.rs only |

Invalid baseline shapes: `Committed`+`follow_up:true`; failure-side CommitOutcome (BaseUnchanged/Foreign/NewerSchema, x2 bool) inside Landed; `outcome` etag != `etag` field. Removed by A, B, C.

M3 clippy cognitive complexity (threshold 1; command in p2-scratch/measure.sh; output p2-scratch/measure.out):
- commit_known: 14 base, 14 A, 14 B. C: NOT captured (my line-range filter missed it; rerun).
- Daemon::land: base 5, A 7, B 5, C 3 (+ apply_landed 3). settle: 4 in every option (unchanged).
- Landed::base: base 2; A 3; B n/a (field struct, no method); C `CommitEnd::base` 3.
- Decision points reading the type in land: base 2 `if !follow_up` (+`attached &&`); A 2 `matches!`; B 2 `!=`; C 1 `if let CommitEnd::Landed`. Wildcard `_` arms: none added anywhere.
- Box forced by clippy `large_enum_variant`: only in A, test `Report::Landed(Box<Landed>)` in commit_protocol.rs and capture_property.rs. B and C needed it nowhere (C's Report holds `LandedHow`).

M5 compiled (cargo check -p memory --locked, scratch edits reverted, status clean):
- A: bug (`if true`/`if attached`) compiles.
- C: `CommitEnd::Adopted(a) => self.apply_landed(a, ...)` fails: `error[E0308]: mismatched types ... expected &Landed, found &Adopted`. Re-writing the two effects inline in the Adopted arm compiles: C prevents the accidental reuse, not deliberate duplication.
- B: same class as A (not compiled separately; the guard is a `!=` on a field).

M6 under contention (2 other builds running), release, n=10000 per commit, 7 interleaved rounds, median [min-max] ns:
| | plain | verified | adopted |
|---|---|---|---|
| base | 8540 [8016-11229] | 10719 [9419-15389] | 12233 [10924-13199] |
| base2 (noise) | 9185 [8079-9589] | 10369 [9457-11141] | 12253 [10821-14242] |
| A | 9415 [8329-10762] | 10984 [9449-12454] | 11821 [10829-13190] |
| B | 8586 [7966-11398] | 10763 [9492-11895] | 12155 [11035-13974] |
| C | 9222 [8254-9680] | 10653 [9407-10936] | 11978 [11108-13890] |
Base vs base2 differ up to ~7.5%: every option is inside the noise floor. No difference.
Rerun unchanged: `/private/tmp/claude-501/-Users-mm-dev-memory/c32b7bd9-0169-47d4-bf70-da528e7ec2a3/scratchpad/review/proto/p2-scratch/bench.sh 7` (checks out each branch in the worktree, builds, interleaves; leaves worktree on base).
Measurements script: `p2-scratch/measure.sh`.

M7: files touched: A 4, B 4, C 5 (adds landed_bench.rs, 4 lines). Test call-site edits: A about 20, B about 15, C about 25 (several need `CommitEnd::Landed(..)` destructuring: `Rig::landed`, `absorb` x2, disposition test, late-write test). Public API: yes in all (CommitOutcome gone from Landed; C also changes `commit`/`commit_known`/`disposition*` return type and exports `CommitEnd, Adopted, LandedHow`).

check-mutations.py: `if-match` unchanged (1) in all. `advance-on-fail` still 1 in A and B (body lines changed: mutant text must replace `if !matches!(landed, Landed::AdoptedEarlier { .. }) {` in A, `if landed.how != LandedHow::AdoptedEarlier {` in B, but the script's mutant presumably targets the old `!landed.follow_up` text: parent must check). In C it is 0: the predicate lines become
`        if let Ok(end) = &result {\n            // Remote is authoritative`
and a mutant meaning "apply though not landed" is replacing
`            if let CommitEnd::Landed(landed) = end {\n                self.apply_landed(landed, got.updates, hints, attached.then_some(owner));\n            }`
with
`            self.apply(got.updates);` (and the hint loop if attached). That mutant is in-bounds for C because it bypasses the helper.

CommitOutcome (memory-core): success variants still used by `Verdict::outcome` (verify.rs:46) and core_verify tests. No option makes them dead; in A/B/C commit.rs no longer uses them. Not removed.

Integration with P1: all options touch `land` only (C also adds `apply_landed` beside it and changes `settle` signature type); `OwnerRt` untouched.

## 4. Recommendation (fixed rule)
Eligible: all (M1 passes, no test weakened). M2: A, B, C tie at 0 invalid. M5: only C turns the named mistake (reusing the landed path for adoption) into a compile error; A and B rely on tests/mutants. So C, by the rule, then M3 favours C too (land 3 vs 5/7/5). M4 costs C: src +60 net vs +18 (A), +7 (B). M6 no veto. If lines or API churn matter more than prevention, B is the cheapest M2 fix (smallest diff, no Box, removes the bool). What would change my mind: a quiet-machine rerun showing C's `CommitEnd` slower, or the parent judging that deliberate duplication (which C does not stop) makes the extra ~40 net src lines not worth it; then pick B.

## 5. Limits
- Quiet-machine benchmark not done. C's commit_known cognitive complexity not captured. Whole-crate nextest ran only on C. B's M5 not compiled (same shape as A). The 7.5% noise floor hides small differences.
