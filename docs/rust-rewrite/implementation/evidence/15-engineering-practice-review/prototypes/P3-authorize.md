# P3 authorize: report (worktree /Users/mm/dev/memory-wt/ds-authorize; scratch scripts in p3-scratch/ beside this file)

## 1. Design constraints (what the final authorize step must re-check)
- docs/rust-rewrite/implementation/03-privacy-port.md:30-31 `authorize(AdmittedSource, RedactedBytes, &PolicyContext) -> Authorization::Allow|Deny|Unknown`. Line 36: "Cross-source/profile reuse and changed context deny (tests/privacy_port.rs:361)". Nothing there says the value must be consumed; by-value is a signature as built, not a requirement.
- 05-privacy-review.md:46: "authorize's final Deny/Unknown arms cannot be reached under identical bound context/facts ... They should not be removed or exposed with test-only constructors." (arms to keep: Deny/Unknown arms of the final check.)
- Code doc, privacy/mod.rs:193 "Result of final policy recheck."; :339 "Recheck policy and bind the exact transformed bytes to their source." (base lines)
- DESIGN.md 8.1 (pipeline `policy::authorize` -> `Authorized<Redacted<Bytes>>`), 8.2/8.5 (unknown state fails closed) and the amendments (rg authorize/binding: only manifest/capture evidence at amendments :165-186) say NOTHING more: no sentence requires re-evaluating policy in authorize, and none says binding equality suffices. The recheck requirement rests on 05:46 and the code docs.
- Ruling: option C (no recheck) is ineligible: a governing review says the final arms stay; no governing text says binding equality suffices. Measurements reported anyway.

## 2. Options (all from base 7dd3023 = main 41a07ff + benchmark `crates/memory/tests/authorize_bench.rs`)
- base `proto/ds-authorize-base` 7dd3023: `authorize(source: AdmittedSource, ..)`; capture.rs `admitted_clone` re-admits per piece.
- A `-a` b9e02a3: `authorize(source: &AdmittedSource, redacted: RedactedBytes, context: &PolicyContext)`; inside `admit(context, source.facts.clone())`; `admitted_clone` deleted.
- AB `-ab` 286b3cc: A + 44f20af (cherry-pick clean): `binding()` destructures `PolicyContext` and `ResolvedSourceFacts` without `..`.
- AB2 `-ab2` 41f6b43: AB + `AdmittedSource { facts, owner: String, binding }`; admit does `let Some(owner) = facts.owner.clone().filter(|o| !o.is_empty()) else { Deny(UnknownOwner) }`; authorize re-admits and takes `owner: source.owner.clone()`; the `None` arm is gone. 20 extra lines, no shared function.
- B `-b` 723382b: A + private `enum Decision<'a>{Allow(&'a str),Deny(Reason),Unknown(UnknownReason)}` and `fn decide<'a>(&PolicyContext,&'a ResolvedSourceFacts) -> Decision<'a>`, called by `admit` and `authorize`; no clone, no `None` arm.
- BB `-bb` c269816: B + 44f20af.
- C `-c` b41c905: A + exhaustive binding, no re-admit; owner `Some/None` match kept.
- binding-only `-binding` 44f20af: base + exhaustive binding (+26/-9, 35 lines).
- Owner: `facts.owner: Option<String>` is set by the shell; base admit (mod.rs:302) `if facts.owner.as_ref().is_none_or(String::is_empty) { Deny(UnknownOwner) }` precedes `Allow`, so admit can never return Allow with owner None/empty: the arm is unreachable (05:46 agrees). A cheap removal without B exists: AB2 (+20 lines, built).

## 3. Measurements
M1 each option: `cargo nextest run -p memory-core --locked`: 198 passed (base: "1 slow", 0 skipped) on all seven; `cargo test -p memory-core --doc --locked -- --test-threads=2`: 7 passed on all; `cargo nextest run -p memory --locked -E 'binary(capture) | binary(capture_property) | binary(resolve) | binary(mutation_daemon)'`: 41 passed on all (per binary: capture 11, capture_property 7, mutation_daemon 2, resolve 12: capture_property WAS included); `cargo fmt --check` ok; `cargo clippy --workspace --all-targets --locked -- -Dwarnings` clean. No test weakened.

| | base | A | AB | AB2 | B | BB | C |
|---|---|---|---|---|---|---|---|
| M4 src lines (+/-) | 0 | 14 | 49 | 69 | 79 | 114 | 67 |
| M4 test lines (8 call sites, mechanical `&`) | 0 | 16 | 16 | 16 | 16 | 16 | 16 |
| authorize arms/ifs (1 if + arms) | 6 | 6 | 6 | 4 | 4 | 4 | 3 |
| cognitive: authorize / admit / binding / decide | 4/8/9/- | 4/8/9 | 4/8/9 | 3/7/9 | 3/2/9/9 | 3/2/9/9 | 3/8/9 |
| cognitive: authorized_body / admitted_clone | 2 / 2 | 2 / gone | same | same | same | same | same |
| `None =>` owner arm | yes | yes | yes | no | no | no | yes |
| privacy-deny predicate occurrences | 1 | 1 | 1 | 1 | 0 (1 as Decision::) | 0 (1 as Decision::) | 1 |
| unbound new field | Deny | Deny | compile error | compile error (same code as AB; not separately run) | Deny | compile error | compile error (Allow if silenced) |
| 4 KiB per piece, median ns [min-max] | 10191 [9894-11224] | 6650 [6525-7637] | 6717 [6620-7452] | 6852 [6621-7396] | 6215 [6066-6977] | 6141 [6101-7016] | 3078 [3033-4044] |
| 1 MiB per piece, median ns | 506442 [493235-855626] | 502793 | 505576 | 505266 | 498155 | 499818 | 494113 |
| size_of AdmittedSource | 144 | 144 | 144 | 168 (not re-measured; see note) | 144 | 144 | 144 |
Note: AB2 adds a String (24 bytes), so expect 168; the bench prints it, but the AB2 value was not read out here.
M3 other: decision points at `authorized_body`: 3 arms in all; `admitted_clone` (3 arms) removed in every option; no `_ =>` arms added or removed.
M6 (release, 8 rounds interleaved, base 16, UNDER CONTENTION; synthetic JSONL; 4 identities, 6 ancestors): base 4 KiB admission/clone share 35.0%, transform 8.1%; at 1 MiB admission 0.7%, transform 3.8% (authorize is ~95%: SHA-256 and a copy). Options show ~0.3% admission at 4 KiB. Noise floor: base vs base 4 KiB total 9894-11224 (±7%), 1 MiB 493-856 µs outliers (median stable ±1%). A/AB/AB2 are the same code path: 6650/6717/6852 differ by ≤3%, inside noise; B/BB 6.1-6.2 µs are ~7% below A/AB: borderline, not ranked. C 3.1 µs is real. 1 MiB: no difference between options.
Rerun unchanged (prebuilt release binaries in p3-scratch/bin from the hashes above): `bash /private/tmp/claude-501/-Users-mm-dev-memory/c32b7bd9-0169-47d4-bf70-da528e7ec2a3/scratchpad/review/proto/p3-scratch/bench.sh 8`. To rebuild one option: checkout it and `nix develop /Users/mm/dev/memory#rust --command sh -c 'cd rust && cargo test -p memory --release --locked --test authorize_bench --no-run'`.

M2 (authorize inputs): AdmittedSource+bytes for another source: representable by type in every option, denied at runtime by `redacted.binding != source.binding` (all). Changed context between admit and authorize: representable; denied by binding equality for bound fields (all); for a field not in the binding, only by the re-admit/decide (base, A, B, AB2-for-nothing since AB2's binding is exhaustive), and in C by nothing once the destructuring is silenced. Admitted source without owner: representable in base/A/AB/B/BB/C (Option facts), unreachable; AB2 stores the owner as a non-optional String so authorize cannot observe a missing owner (B/BB prove it via decide).

M5 measured (scratch edits, all reverted; `git status --short` empty after each option and at the end):
(i) new `pub deny_all: bool` in PolicyContext consulted by admit/decide (deny Credential), not added to binding(), flipped false->true after admit, then `authorize`:
- base: compiles; result `Deny(Credential)`. A: compiles, `Deny(Credential)`. B: compiles, `Deny(Credential)`. So the re-admit does real work whenever a field is forgotten in binding().
- AB, BB, C (and AB2, same binding code): do not compile: `error[E0027]: pattern does not mention field 'deny_all'` at privacy/mod.rs:219 (binding()), with help "or always ignore missing fields here".
- C silenced with `deny_all: _`: compiles; test result `Allow`. So C with a silenced destructuring ships a denied source.
(ii) bytes transformed for X (transcript.jsonl, Transcript) authorized with admission of Y (settings.json, Config): `Deny(ContextChanged)` on base, A, AB, B, BB, C (AB2 not run separately; same check). Pinned by the committed test `rust/crates/memory-core/tests/privacy_port.rs:361` (`capability_binding_rejects_cross_source_cross_profile_and_changed_policy`, assertion at :371).
(iii) from crates/memory/tests: `error: cannot construct 'AdmittedSource' with struct literal syntax due to private fields` (and likewise for AuthorizedBody and RedactedBytes as written in the doctests) on all options; the compile_fail doctests pass on all.

privacy-deny mutant (script not run or edited): base/A/AB/AB2/C: the two-line predicate `        if is_credential(path) {\n            return Admission::Deny(Reason::Credential);` occurs exactly once. B/BB: 0 occurrences; replacement predicate `        if is_credential(path) {\n            return Decision::Deny(Reason::Credential);` occurs once (in `decide`). The mutant then affects admit and authorize at once, so it cannot be caught "through authorize alone"; it is caught wherever a test expects a Credential denial through admit. Files that mention `Reason::Credential` (candidates; I did not run the mutant): memory-core tests privacy_port.rs, privacy_property.rs, core_f2_gaps.rs, core_planner.rs; memory tests capture.rs, resolve.rs.

M7: files touched A: 7 (mod.rs, capture.rs, 5 test files); AB +0 files; B/BB same set; public API: `authorize` takes `&AdmittedSource` (yes, in all options). 8 mechanical `&` call-site edits.

## 4. Recommendation (fixed rule)
Eligible: A, AB, AB2, B, BB (C ineligible). M2/M5-by-type: AB, AB2, BB prevent the unbound-field bug by type (A, B do not, and rely on the re-admit); AB2 and BB additionally remove the unrepresentable-owner arm. M3: AB2/BB/B have 4 arms in authorize (AB: 6). M4: AB2 is 69 lines vs BB 114. M6 is only tie-break/veto: all options remove the ~3.6 µs admission (35% at 4 KiB, <1% at 1 MiB); no veto.
=> Recommend AB2: borrow + exhaustive binding + stored non-optional owner, recheck kept exactly, no clone helper, no change to check-mutations.py. If the unreachable arm is not worth 20 lines, AB (49 lines). Keep baseline is not warranted: the clone helper and unbound-field gap are real.
Would change my mind: a governing text saying binding equality suffices (then C+exhaustive binding saves ~3 µs/piece but M5 shows a silenced `_` yields Allow); a wish for one decision function (BB).

## 5. Limits
No full gates, coverage or mutants run; check-mutations.py not run. M5 not rerun on AB2 (shares AB's binding code). AB2's size_of not read from output. Benchmarks under contention; parent should rerun quietly. Cognitive numbers used a scratch clippy.toml (not the M1 clippy). Benchmark lives in crates/memory/tests (memory-core clippy.toml bans Instant).
