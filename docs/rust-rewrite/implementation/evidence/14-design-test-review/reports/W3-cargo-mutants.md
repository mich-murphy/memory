# W3: cargo-mutants on the working tree (post-removal suite)

Seeds: HEGEL_TEST_CASES=PROPTEST_CASES=500, HEGEL_SEED=PROPTEST_RNG_SEED=3352165921. cargo-mutants 27.1.0 from the pinned dev shell, `--jobs 2`.
Artifacts: W3/mutants.out/a/mutants.out (outcomes.json, missed.txt, diff/), W3/step4.log, W3/s5.log, W3/a.log. Scratch copies: W3/ws (new), W3/new2, W3/old/rust (git archive HEAD).

## Scope and trade-off (what was NOT done)
- Baseline: full suite (nextest, 358 tests, 500 cases) = 447 s on this loaded machine (4 tests >60 s; hegel daemon state machine and core_mechanics proptest property1 are the slow ones). Per mutant that is infeasible (a: 223 mutants x ~8 min).
- Chosen: mutants run with ONLY the `memory` crate's example tests and the non-hegel_/proptest_ filter (`-E 'not test(/^(hegel_|proptest_)/)'`, 303 tests, 37 s). So the headline score below is **example-test score**, a floor for the full suite. To measure the property effect I re-ran selected missed mutants with the FULL memory crate suite (step 4) and the capture property binaries (s5).
- Only priority (a) was run: commit.rs, capture.rs, state.rs. Priorities (b) memory-core (1157 mutants) and (c) rest of memory (630 total in crate) were NOT run: no time (run took ~2h15m under contention with another worker).
- 222 of 223 mutants finished. The run died on "No space left on device" while writing the diff for capture.rs:100:9 (one mutant, untested). I could not kill the process (kill was denied by the harness) but it exited on its own.

## Scores (example tests only; unviable excluded from denominator)
| file | generated | caught | missed | timeout | unviable | score |
|---|---|---|---|---|---|---|
| capture.rs | 71 (70 run) | 35 | 21 | 0 | 14 | 62.5% |
| commit.rs | 52 | 34 | 4 | 0 | 14 | 89.5% |
| state.rs | 100 | 76 | 7 | 0 | 17 | 91.6% |
| total | 223 (222 run) | 145 | 32 | 0 | 45 | 81.9% |

## Missed mutants (32)
capture.rs (21)
- 274:5 stream_span body -> Ok(vec![]) / Ok(vec![0]) / Ok(vec![1]) (3)
- 282:14 `<`->`<=`; 286:21 `<`->`==`,`>`,`<=` ; 290:39 `!=`->`==`; 290:54 `-`->`+`,`/`; 295:12 `>`->`==`,`>=`; 296:50 `-`->`/`; 297:38 `-`->`/` (all in stream_span, 13)
- 461:48 match guard `prefix_stable() && from <= to` -> true; 461:78 `&&`->`||` (Job::write)
- 626:16 `rounds += 1` -> `*=`; 627:19 `rounds > 4` -> `==`, `>=` (append)
- 722:9 delete arm `Plan::Withheld{reason} => return Withheld(reason)`; 708:38 guard `live` -> true (append)
commit.rs (4)
- 152:9 Failure::retry_after -> None; 153:13 delete arm Throttled{retry_after}; 471:31 `chain.len() as u64 > CHECKPOINT_INTERVAL` -> `==`, `>=` (recover_base)
state.rs (7)
- 22:46 and 22:65 `MAX_BODY_BYTES + MAX_HEADER_BYTES + 1` `+`->`-`,`*` (3); 305:19 `body.len() > MAX_BODY_BYTES` -> `>=` (encode); 331:32 `MAX_HEADER_BYTES + 1` `+`->`-`,`*` (decode, 2); 426:5 fsync_dir -> Ok(()) (state.rs line 426 in this tree).
(The fsync_dir entry is in missed.txt as the last line.)

## Re-checks with property tests
- step 4 (below): 10 missed mutants run against the full memory-crate suite (examples + hegel + proptest, 500 cases, same seed): ALL SURVIVED, in both old and new suite.
- s5: stream_span mutants 274:5 (Ok(vec![])), 295:12 (`>`->`==`), 290:54 (`-`->`+`) run against binaries `capture` + `capture_property` (21 tests incl. property4 hegel/proptest): all survived. Other stream_span mutants not re-run; treat as the same class.

## Step 4: attribution to removed tests (exact)
Method: mutant source span from outcomes.json applied (text replacement) to (a) git archive HEAD rust/ ("old", 203 tests run + 2 ignored) and (b) the current tree ("new", 185 run + 2 ignored; note 185 vs 358 because only `-p memory` is run), `cargo nextest run -p memory`, 500 cases, seed fixed.

| mutant | old suite | new suite |
|---|---|---|
| capture.rs:722:9 delete Withheld arm | survived (203 pass) | survived |
| capture.rs:708:38 live -> true | survived | survived |
| capture.rs:626:16 += -> *= | survived | survived |
| capture.rs:627:19 > -> == | survived | survived |
| capture.rs:461:48 guard -> true | survived | survived |
| capture.rs:461:78 && -> \|\| | survived | survived |
| commit.rs:152:9 retry_after -> None | survived | survived |
| commit.rs:153:13 delete Throttled arm | survived | survived |
| commit.rs:471:31 > -> == | survived | survived |
| commit.rs:471:31 > -> >= | survived | survived |

Result: 0 of 10 caught by the old suite. No missed mutant in this scan is attributable to the removed tests. Limits: the 10 were chosen as plausibly related (capture/commit_protocol tests were among those removed); state.rs mutants and memory-core mutants (core_* / scaffold removals) were not tested against the old suite because the core crate was not mutated. Also notable: the old suite survives mutants that delete the Withheld arm and Throttled retry_after, although `withheld_source` and committed_then_5xx tests exist: they check outcomes not these exact paths, i.e. these are pre-existing gaps.

## Triage
(i) Equivalent or unobservable
- state.rs:22 (3 mutants): `MAX_FILE_BYTES` = 64 MiB + 256 + 1. Only used as a read cap; mutation to `-` or `*` changes the cap to ~64MiB-255 or a huge value. Observable only with a file within ~512 bytes of the cap; effectively unobservable without a 64 MiB test. Arguably a real boundary gap (see theme D) but cheap to fix with a constant/range assertion.
- state.rs:305 `>`->`>=` on MAX_BODY_BYTES: boundary at exactly 64 MiB; testable with a 64 MiB zero body (feasible, 0.1 s) so a real gap, not equivalent.
- state.rs:331 (2): `MAX_HEADER_BYTES + 1` in the header scan `take`; the `+1` allows a header of exactly 256 bytes plus newline. Observable with a 256- vs 257-byte header test; real boundary gap.
- capture.rs:626 `rounds += 1` -> `*=`: rounds starts 0, so 0*1=0 forever; `rounds > 4` never trips; loop bound is lost. Only observable if a scenario needs more than 4 rounds; this is the "bounded rounds" guard, so real (theme C), not equivalent.
(ii) Real gaps, grouped by theme
A. Delta/resume read path `stream_span` (capture.rs:274-297, 17 mutants incl. whole-body replacement; DESIGN section 6/7 delta pieces and checkpoints; also commit/hash correctness): replacing the whole function by Ok(vec![]) survives even the hegel/proptest capture property. Either the function's result is not compared byte-for-byte against the source anywhere, or the cache/delta branch is not reached. Highest value.
B. Resume guard in Job::write (capture.rs:461, 2 mutants): `prefix_stable() && from <= to` can be true/or'd with no test failing; non-prefix-stable transform or truncation must force a whole-stream read (comment at 455 says so; DESIGN section 6).
C. Bounded loops/counters (capture.rs:626-627, commit.rs:471): the 4-round cap and checkpoint-chain-length cap off-by-one/removal are untested (DESIGN sections 7 and 9: bounded retries, checkpoint interval).
D. Error/classification: Plan::Withheld arm deletion (capture.rs:722, privacy fail-closed, DESIGN section on privacy/withheld: the arm falling to `_ => Unchanged` silently drops a withheld source), `live` guard (708), Failure::retry_after / Throttled (commit.rs:152-153: Retry-After must reach the scheduler, DESIGN section 7 backoff).
E. State-file boundaries (state.rs:22, 305, 331) and fsync_dir (state.rs:426, DESIGN section 9 durable state: directory fsync after rename). fsync can only be asserted with an fs/sync seam; W1 already lists "fsync seam" as a STRENGTHEN item.
(iii) Not reachable from the shipped binary: none found; all missed lines are in daemon-reachable code.

## Ten tests to add (value order)
1. Property (differential): for random sources with appends, truncation and rewrites, bytes piece-assembled by stream_span (with and without `known` cache, with `from` inside/at/past the range) equal a direct read of the file. Kills 274:5 (3), 282-297 (13). Property fits (range arithmetic).
2. Example: Extend/resume with a non-prefix-stable transform and with from > to must read the whole stream (assert read ranges on a recording fs). Kills 461:48, 461:78.
3. Example: a source classified Withheld in a plan round after state exists returns SourceOutcome::Withheld(reason) and uploads nothing. Kills 722:9.
4. Example: Throttled reply with Retry-After: 7 returns a Failure whose retry_after() == Some(7s); non-throttled returns None, and the scheduler honours it (daemon level). Kills commit.rs:152, 153.
5. Example: capture loop that needs exactly 4 and 5 planning rounds (scripted racing fs) -> 4 ok, 5 Unavailable. Kills 626, 627 (3). 
6. Example: manifest chain of exactly CHECKPOINT_INTERVAL and CHECKPOINT_INTERVAL+1 links in recover_base: former recovers, latter Corrupt. Kills commit.rs:471 (2).
7. Example: state.rs encode with body of exactly MAX_BODY_BYTES ok and +1 TooLarge. Kills 305.
8. Example: decode with header exactly MAX_HEADER_BYTES and +1 byte without newline (BadHeader), plus assert MAX_FILE_BYTES relation (`MAX_BODY_BYTES + MAX_HEADER_BYTES + 1`) as a const check. Kills 331 (2), 22 (3).
9. Example with injectable sync seam: write/replace calls file sync and fsync_dir (count or order recorded). Kills 426 and was already on W1's STRENGTHEN list. 
10. Run mutation for the remaining scope (memory-core 1157, daemon/hook/watch) in a scheduled job per DESIGN section 15 with the property suite filtered to fast tests (see trade-off) — not a test but the way to learn the rest.

## Environment notes
- Full 358-test run: 447 s; example-only memory crate run ~25-30 s per mutant plus 13-55 s build.
- Disk filled mid-run (ENOSPC) because of other workers; my large scratch targets (old/rust/target, new2/target) were not removed because the deletion command was refused by the harness. Safe to delete: W3/old, W3/new2, W3/ws/target.
