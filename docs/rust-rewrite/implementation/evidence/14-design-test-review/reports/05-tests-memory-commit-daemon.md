# Test audit: memory crate, commit_protocol / daemon / mutation_daemon / counters / harness

Scope: `rust/crates/memory/tests/{commit_protocol,daemon,mutation_daemon,counters,harness}.rs`, 87 tests, against `src/commit.rs`, `src/daemon.rs` (and `environment.rs`, `capture.rs` where called). Repo was not modified; all mutation work ran in a copy at `.../scratchpad/review/ws` (raw data: `mut/results.json`, `mut/mutants.json`, `mut/mutants2.json`, `05-nextest*.txt`).

## Summary verdict

The suite is better than most: the two state machines in each file use an independent reference model and an independent chain reader, `oracle_detects_tampered_store_contents` proves that oracle can fail, and `mutation_daemon.rs` is a genuine production-mutant kill test. Of 87 tests: **59 KEEP, 9 REMOVE, 10 MERGE, 9 STRENGTHEN**. The removals are modest in size (a dozen small tests). The real findings are not bloat but (1) fidelity and coverage gaps that the fakes hide, (2) assertions that use the production constants as the oracle, (3) flaky guard tests under default settings, and (4) five copies of the same fakes.

Standards relied on (fetched this session): "Software Engineering at Google" ch. 13, https://abseil.io/resources/swe-book/html/ch13.html: "a fake should maintain fidelity to the API contracts of the real implementation", "A fake must have its own tests to ensure that it conforms to the API of its corresponding real implementation", "we prefer realistic tests", and change-detector tests "fail in response to any change to the production code"; emphasise state over interaction testing. AWS S3 conditional writes, https://docs.aws.amazon.com/AmazonS3/latest/userguide/conditional-writes.html: If-None-Match on existing key gives 412; If-Match mismatch gives 412; If-Match on a missing key gives 404; concurrent requests can give 409 Conflict. Khorikov, "Unit Testing Principles, Practices, and Patterns" (resistance to refactoring, protection against regressions; unverified, from memory, not fetched). DDIA ch. 9 is the design's own source for safety/liveness.

## Findings (most severe first)

### F1 (high, confidence high): the store fake is validated against nothing, and diverges from S3/R2 in ways the driver depends on
`FakeStore` (commit_protocol.rs:150-300), `FStore`/`MemStore` (daemon.rs:121-250, 483-575), counters.rs, mutation_daemon.rs are all hand-written, none has contract tests against a real or recorded R2 (SWE@Google: fakes need their own tests). Concrete divergences, verified against the AWS doc above:
- ETag is a monotonic version counter (`"v{n}"`). In S3 (and R2, unverified) the ETag derives from content, so the `Churn` effect ("rewrite same bytes, new ETag", commit_protocol.rs:243, and `stale_if_match...` which bumps `o.1`) models something that cannot happen with identical bytes. The tests prove the driver survives an event reality does not produce, and cannot show the opposite (an ABA where a re-PUT of identical `current.json` bytes leaves the ETag unchanged). `current.json` embeds seq and manifest hash so real ABA is unlikely, but the fake is not evidence of it.
- No 409 ConditionalRequestConflict path. `StoreError` has no variant for it; the AWS doc says concurrent conditional writes can return 409. Whatever the real adapter maps it to is untested.
- If-Match on a missing key returns `NotFound` in the fakes (matches S3's 404). R2's behaviour here is unverified; the design leans on 404-vs-412 in `verify` (commit.rs:436-440). Needs a recorded R2 trace (prototype P2) replayed against the fake.
- Faults are scripted per test, there is no fault ordering/concurrency; "reply lost after landing" is the only partial-failure shape. Network timeouts mid-body, a 200 with a truncated body, and read-after-write on a different edge are not modelled.
Action: write one shared `tests/common/store.rs` fake plus a contract test run against recorded R2 behaviour; add 409.

### F2 (high, confidence high): filesystem fake is too forgiving for the capture design
`MemFs` (daemon.rs:61-110, counters.rs:76-95, mutation_daemon.rs:68-87) has no rename-replace (inode never changes; `Birth::Unavailable`), no file change between `stat` and `read`, no partial reads, no notification model. DESIGN 15.1 lists rename-replace as a generator class, and the daemon state machine (daemon.rs:1170-1445) has no rename step. In counters/mutation_daemon `mtime_ns = len`, so a same-size rewrite is invisible to stat, unlike real files. The daemon-level properties therefore cannot catch regressions in identity change handling or TOCTOU; only capture_property.rs (other agent) might. DESIGN 15 property 2 (coalescer) and the idle-minute wakeup counter are not in these files at all (`Counters.wakeups` is always 0).

### F3 (high, confidence high): flaky property tests under default settings
Mutation-free baseline experiment (built binaries, random seeds): `proptest_daemon_state_machine` at default 256 cases failed the coverage guard 4/8 runs (message `run never produced ["rewrite-mid-file"]`); default Hegel cases: `hegel_daemon_state_machine` 2/6, `hegel_property4_commit_state_machine` 2/6, `proptest_daemon_state_machine` 1/6. At `*_CASES=500` (the gates.sh minimum): 0/8 for all four I ran. One plain `nextest run -p memory` during this audit failed `proptest_daemon_state_machine` exactly this way (daemon.rs:1770). So `cargo test`/`nextest` as documented in a developer loop is red by chance, and the guards are statistical (rare shape `rewrite-mid-file` needs len>68 KiB and zone 2). 500 cases passing 8/8 is not proof of zero flake at CI's random seed: unverified beyond 8 samples. Recommendation: make shape generation deterministic (e.g. a scripted prefix run before the random walk) rather than relying on case count; or skip guards unless cases >= 500 with a loud message.

### F4 (medium, confidence high): assertions that use the production constant as the oracle
`base_unchanged_retries_exactly_four_times...` asserts `1 + REBASE_RETRIES`; mutant C2 (REBASE_RETRIES 4 to 3) is killed by **no test** in the five files. GET_ATTEMPTS 4 to 3 (C3) is killed only by `hegel_daemon_state_machine`, not by the three commit_protocol tests that import `GET_ATTEMPTS`. DESIGN 7.4 says 4. Fix: literals.

### F5 (medium, confidence high): survivors of 35 hand-made mutants = missing tests
Survived everything: C4 (404 under a fence retried instead of returned; result-equivalent, only request count differs), C7 (`recover_base` skips manifest hash check; `recover_base_failures_are_classified` only writes invalid JSON so parse rejects it anyway), C10 (`known_ours` not cleared on landing; A6 state grows), D2 (stale base kept after LandingUnknown/BaseUnchanged), D7 (index interval boundary), D8 (A6 follow_up applies updates of a manifest that never landed; DESIGN A6), D13 (`Transient` counted as `throttled`, DESIGN 11.2 says distinct), D18 (shutdown skips index flush), X4 (recover_base skips head==current check), X5 (no chain length bound). Each is a missing or too-weak assertion; D8, D13, C10 are protocol-significant.

### F6 (medium, confidence high): the daemon properties do not subsume `mutation_daemon.rs`
Mutant D1 (index advanced after a failed commit; the script's `advance-on-fail`) is killed by `mutation_daemon::failed_commit_does_not_advance_the_local_index` and by **neither daemon property at 500 cases, seed 730104**. `stale_if_match...` kills C12 and is the only test that asserts at the store (412 count). Keep both, and note that the DESIGN 15.4 liveness property is blind to a safety regression of this shape.

### F7 (medium, confidence medium): duplicated infrastructure
`Tmp`, `config()`, `MemFs`, `MemStore`/`FStore`/`FakeStore`/`Ghost`, `Clk`, `source()`, `need/ensure/e`, observe/read_chain/reassemble and the `Shapes`/`require_all` guard are copied across these five files (daemon.rs alone has three store fakes; `reassemble` differs subtly: daemon.rs treats stored bytes as raw, commit_protocol decodes variants). A fix to fake semantics (F1) must be made in 5+ places. Move to `tests/common/mod.rs`. `harness.rs` is a misnomer: it is not a harness.

### F8 (low, confidence high): environment fragility
chmod-based tests (`state_api_failure_table`, `unwritable_*`, `shutdown_table`) assert PermissionDenied and fail when run as root (container CI). `retry_delay_defers...` depends on the deterministic jitter draw (`next_draw`) giving a delay in (0, 61 s]; any change to the PRNG changes the result. `two_parked_owners_are_listed_sorted` uses 2 owners, so a missing sort passes 50% of runs (HashMap order); mutant D12 was killed in my single run by luck of order.

### F9 (low): tests of the harness itself
`guard_has_negative_controls` (x2), `guard_shapes_arise_from_scripted_runs`: test test-helper counters. One shared negative-control test is justified (a broken `require_all` would silently disable the DESIGN 15 coverage guard); the rest is noise. The `REQUIRED_NEW` loop (daemon.rs:1152) compares two constants in the same file.

## Empirical checks

Method: `mut/run.py` copies are mutated one production predicate at a time, then the five binaries run with `--no-fail-fast` at `HEGEL/PROPTEST_CASES=500`, seeds 730104 (matches `gates.sh` minimum). 40 mutants (35 + 5), baseline clean. One mutant (C13, my hack for a blind first write) killed nearly everything and is excluded; D1 was re-run after a SIGTERM (rc -15) invalidated the first attempt.

Evidence for REMOVE/MERGE verdicts (killers other than the candidate in brackets):
1. `directed_lost_reply_is_already_ours...`: only mutant killed is C15; also killed by `lost_reply_after_landing`, `committed_then_5xx`, hegel and proptest property 4. REMOVE safe.
2. `committed_then_5xx...` (MERGE): same C15 set. 
3. `directed_foreign_writer_parks...`: only C9; C9 killed by 7 other tests incl. `foreign_writer_parks_the_owner_with_both_hashes`. REMOVE safe.
4. `etag_churn...` (MERGE): only C12; also killed by `auth_loss_during_verification`, both A6 late-write tests, `mutation_daemon::stale_if_match`.
5. `directed_crash_after_manifest...`: only C1; also killed by `r2_a_412_dedup...` and both properties (name claims adoption, assertion is a manifest count).
6. `first_round_commits_and_persists_then_rebuild_does_not_reupload`: only D14 (no base recovery); D14 also killed by 7 tests incl. `counters::hegel_converged_reconcile_makes_zero_puts`, `damaged_index_*`.
7. `directed_lost_reply_then_offline_recovers_after_settle`: killed none of 40 mutants.
8. `directed_offline_and_revoked_commit_nothing_until_settle`: killed none.
9. `guard_shapes_arise_from_scripted_runs`: killed none.
10. `r2_without_adoption_the_descriptor_would_not_verify`: killed none; calls no driver code (memory-core `build` + `verify_encodings`).
11. `parse_current_roundtrips_what_the_driver_writes`: killed none; every `read_chain`/`recover_base` already parses it.
12. `harness::scaffold_binary_refuses_capture...`: killed none; asserts a stub's constant.
13. `directed_delete_index_and_removed_source...` (STRENGTHEN): does not kill X1 (deletions never recorded); that mutant is killed only by the two daemon properties. The stand-in planner of commit_protocol cannot exercise the real deletion path.
14. `disposition_maps_every_commit_result` (STRENGTHEN): C8 (Invalid/Corrupt class Offline) survives it (shape-only `Retry{..}` match); C8 is killed by `unreadable_current_is_corrupt...` and `corrupt_remote_parks...`.
Caveat: "kills none of 40 mutants" is weak evidence by itself (my mutant set is small); those verdicts rest equally on reading the assertions (model-only or helper-only).

Mutation-script finding: `check-mutations.py` covers if-match, advance-on-fail and privacy-deny only; the monthly `cargo mutants` on the memory crate is not wired (README: not run at bootstrap). The 40-mutant kill matrix above is a cheap template for it.

## Counts, timing, flake risks

| binary | tests | time (cargo test) |
|---|---|---|
| commit_protocol | 48 | 3.6 s (proptest 4.6 s, hegel 1.5 s under nextest) |
| daemon | 30 | about 5 s; proptest 4.2 s, hegel 2.3 s, spawn test 0.5 s |
| counters | 6 | about 12 s: 30k reconcile 11.6 s (slowest in the set), two Hegel 1.2-1.4 s |
| mutation_daemon | 2 | under 0.1 s |
| harness | 1 | under 0.1 s |
Whole set under nextest: 87 passed, 18.4 s wall.

Flakiness risks: (a) guards at low case counts (F3); (b) chmod tests under root (F8); (c) `Tmp` paths use pid + counter in `std::env::temp_dir()`, safe within a process, unsafe if two runs share a pid after a crash (mutation_daemon removes first, others do not); (d) wall time printed not asserted in the 30k test: the DESIGN "under 1 s" target is unchecked, and the test is 11 s because it runs unoptimised; (e) fixed `Instant` use only for printing; no sleeps found; (f) property tests print nothing about seed unless `HEGEL_PRINT_BLOB`.

## Missing tests implied by DESIGN 15 / amendments
- Idempotence after a **writer version change** (15.3; counters.rs:288 admits it is untestable without a seam: add the seam).
- Rename-replace and stat/read race in the daemon state machine (15.1, 15.4 steps list `rename-replace`; the Step enum in daemon.rs has none; commit_protocol has none either).
- `Transient` vs `Throttled` counters in `FailureCounts` (DESIGN 11.2): no test (D13).
- A6: `follow_up` must not apply local updates (D8) and `known_ours` bounded/cleared (C10).
- Idle minute: zero file syscalls and listed timer wakeups (`Counters.wakeups` is dead); 1 s reconcile at 30k asserted.
- Journey test (fixtures replayed through the real daemon, byte/request totals, restore through memory-reader): none in these files.
- Fake-store contract test against recorded R2 behaviour, including 409 (F1).
- recover_base with a valid-but-wrong manifest body and head != current (C7, X4).

## What is sound
Independent oracles (reference models never read the driver), the `masked` 1a exemption documented with its DESIGN 7.2 reason and never cleared, negative controls for the oracle, per-fault tables for each `Failure` variant, and a real production-mutant gate. The commit-driver table tests are mostly non-redundant: each kills a distinct mutant.

## Open questions and experiments
1. Does real R2 return 404 or 412 for If-Match on a missing key, and does it ever return 409? Experiment: replay P2 prototype traces (docs/rust-rewrite/prototypes) against the fake. Not run (no network).
2. Is CI's random-seed failure rate at 500 cases really zero? Run each of the four properties 100x with random seeds (about 10 minutes); I ran 8.
3. Do the properties catch D2/D8/C10 with more steps (40/12 per run now)? Re-run kill matrix at 5,000 cases.

## Not covered
Other test files (capture, hook, realfs, resolve, state, watch, capture_property), `memory-core` tests, real R2, hosted CI. `cargo mutants` itself was not run (monthly job); hand mutants only.

## Machine-readable table

| file | test name | verdict | one-line reason |
|---|---|---|---|
| commit_protocol.rs | a_404_on_the_first_write_is_not_final_and_retries_if_none_match | KEEP | 7.4 step 5 404 is not final; GET_ATTEMPTS boundary (uses constant) |
| commit_protocol.rs | a_404_under_a_fence_is_landing_unknown | STRENGTHEN | C4 (retry 404 under fence) survives: assert request count / that no extra put_if_match is issued |
| commit_protocol.rs | a_failed_readback_after_412_lands_nothing | KEEP | kills C14; fault on readback |
| commit_protocol.rs | a_persistent_404_on_the_first_write_gives_up_without_claiming_a_landing | KEEP | boundary: retry exhaustion on absent current |
| commit_protocol.rs | ambiguous_piece_put_is_retried_as_412_success | KEEP | at-least-once piece PUT idempotence |
| commit_protocol.rs | an_invalid_manifest_is_deterministic_and_never_reaches_the_store | KEEP | fail-closed: invalid manifest never uploaded |
| commit_protocol.rs | auth_loss_during_verification_pauses_the_host | KEEP | auth fault injection at verification GET |
| commit_protocol.rs | base_unchanged_exhaustion_is_transient_not_throttled | MERGE | into base_unchanged_retries_exactly_four_times (one assertion, pins DESIGN 11.2 class) |
| commit_protocol.rs | base_unchanged_retries_exactly_four_times_then_retries_the_capture | STRENGTHEN | uses REBASE_RETRIES constant so C2 (4->3) survives; assert literal 4 and add class Transient (absorbs base_unchanged_exhaustion_*) |
| commit_protocol.rs | committed_then_5xx_is_already_ours_and_never_a_spurious_412 | MERGE | into lost_reply_after_landing_is_already_ours as table over {Ambiguous,Server} x {first,fenced}; same driver path |
| commit_protocol.rs | directed_crash_after_manifest_then_new_writer_version_adopts_winner_encoding | STRENGTHEN | manifests==2 is an incidental stamp difference not adoption; assert final descriptor equals winner bytes (C1 also killed by r2_a_412 + both properties) |
| commit_protocol.rs | directed_delete_index_and_removed_source_recover_from_the_store | STRENGTHEN | stand-in planner cannot catch X1 (deletions not recorded); first assert is on the model itself; use real capture via daemon test or assert deleted_at in folded state |
| commit_protocol.rs | directed_foreign_writer_parks_without_merging_and_settle_is_exempt | REMOVE | asserts reference.parked && sut.parked (two test constructs agree); C9 also killed by 6 others |
| commit_protocol.rs | directed_lost_reply_is_already_ours_and_next_commit_uses_the_verified_etag | REMOVE | only kills C15, which lost_reply_after_landing, committed_then_5xx and both properties also kill; asserts on model state |
| commit_protocol.rs | directed_lost_reply_then_offline_recovers_after_settle | REMOVE | asserts sut.base.is_some() only; kills no mutant; then_offline is a property step input |
| commit_protocol.rs | directed_offline_and_revoked_commit_nothing_until_settle | REMOVE | kills no mutant; offline/revoked pinned by offline_store_*, revoked_token_* and the properties |
| commit_protocol.rs | disposition_maps_every_commit_result | STRENGTHEN | shape-only matches (Retry{..}); assert class and streak per row or fold into table rows above |
| commit_protocol.rs | etag_churn_is_base_unchanged_then_committed_with_the_fresh_etag | MERGE | into unlanded_5xx test: same Verdict::Base path; only unique assertion is fresh etag (kills C12, also killed by 4 others) |
| commit_protocol.rs | first_write_uses_if_none_match_and_returns_committed | KEEP | DESIGN 7.4 step 3 first write; object counts depend on stand-in piece size 4 (mild change-detector) |
| commit_protocol.rs | foreign_first_write_is_detected_through_if_none_match | KEEP | first-write foreign path |
| commit_protocol.rs | foreign_manifest_with_a_missing_body_is_still_foreign | KEEP | boundary: classify NewerSchema probe when manifest body missing |
| commit_protocol.rs | foreign_writer_parks_the_owner_with_both_hashes | KEEP | DESIGN 7.4/11.3 foreign park; kills C9 |
| commit_protocol.rs | guard_has_negative_controls | MERGE | into one shared require_all test in tests/common (duplicate of daemon.rs copy); tests a test helper |
| commit_protocol.rs | guard_shapes_arise_from_scripted_runs | REMOVE | tests that a test counter increments; no production mutant kills it; real guard is require_all after the runs |
| commit_protocol.rs | hegel_property4_commit_state_machine | KEEP | DESIGN 15.4 state machine; kills C1,C6,C9,C15 mutants; guard flaky below 500 cases |
| commit_protocol.rs | invariant_6_fence_check | KEEP | only test killing C5; DESIGN 7.4 invariant 6 |
| commit_protocol.rs | late_write_of_an_earlier_failed_capture_is_adopted_and_followed_up | KEEP | A6 amendment; kills C12 |
| commit_protocol.rs | late_write_of_an_unknown_manifest_is_foreign | KEEP | A6 negative; kills C9,C12 |
| commit_protocol.rs | later_write_is_fenced_by_the_etag_and_chains_previous | KEEP | invariant 6 previous chain; drop manifests==2 count; name over-claims fence (fake logs no fence type) |
| commit_protocol.rs | lost_reply_after_landing_is_already_ours | KEEP | DESIGN 7.4 step 5; kills C15; absorb committed_then_5xx as a parameter |
| commit_protocol.rs | manifest_put_failure_lands_nothing_and_a_repeat_412_is_success | KEEP | manifest PUT idempotence; kills C6 |
| commit_protocol.rs | newer_current_schema_parks | KEEP | distinct parse_current newer-schema path |
| commit_protocol.rs | newer_manifest_schema_parks_instead_of_looping | KEEP | forward-compat park, DESIGN 11.3 |
| commit_protocol.rs | offline_store_lands_nothing_and_retries_as_offline | KEEP | offline fault; disposition class asserted |
| commit_protocol.rs | oracle_detects_tampered_store_contents | KEEP | negative controls for the independent oracle (8 tamperings); the only guard that the 1a oracle can fail |
| commit_protocol.rs | parse_current_roundtrips_what_the_driver_writes | REMOVE | every observe()/read_chain/recover_base call already parses the driver's current.json; no mutant kills it |
| commit_protocol.rs | proptest_property4_commit_state_machine | KEEP | DESIGN 15.4 requires the second engine; shares model code so it is a driver cross-check only |
| commit_protocol.rs | r2_a_412_dedup_adopts_the_encoding_that_actually_won | KEEP | named cause R2 (design 7.3); kills C1 |
| commit_protocol.rs | r2_without_adoption_the_descriptor_would_not_verify | REMOVE | calls only memory-core build + verify_encodings (not the driver); premise check; C1 already pinned by r2_a_412 and properties |
| commit_protocol.rs | recover_base_failures_are_classified | STRENGTHEN | kills X2 but C7 (no manifest hash check) and X4 survive: add valid-JSON different manifest under the key and a head!=current case |
| commit_protocol.rs | recover_base_reproduces_the_committed_base | KEEP | DESIGN 9.3 recovery equals committed base |
| commit_protocol.rs | revocation_before_the_pointer_write_leaves_only_unreferenced_objects_then_recovers | KEEP | crash/fault point between manifest and pointer; kills C6,C11 |
| commit_protocol.rs | revoked_token_pauses_the_host_and_lands_nothing | KEEP | revoke-token step; PauseHost |
| commit_protocol.rs | seventy_commits_cross_a_checkpoint_and_still_recover | KEEP | DESIGN 15.6 / CHECKPOINT_INTERVAL 64 boundary; asserts literal 64 on purpose |
| commit_protocol.rs | store_errors_classify_and_display | STRENGTHEN | keep class table (feeds disposition); drop Display strings and Box<dyn Error> (std/change-detector); move to environment unit test |
| commit_protocol.rs | unlanded_5xx_reads_base_unchanged_and_retries_from_step_three | KEEP | 7.4 step 5 BaseUnchanged retry with pause==[1] |
| commit_protocol.rs | unreadable_current_is_corrupt_and_deterministic | KEEP | kills C8 (Invalid/Corrupt class) |
| commit_protocol.rs | verification_that_cannot_read_is_landing_unknown_with_its_class | KEEP | GET failure classification; pause count GET_ATTEMPTS-1 (constant) |
| counters.rs | append_of_59kb_reads_at_most_59kb_plus_68kib | KEEP | DESIGN 15 counter test |
| counters.rs | hegel_converged_reconcile_makes_zero_puts | KEEP | DESIGN 15.3 idempotence; kills D14; writer-version-change case missing |
| counters.rs | hegel_delta_capture_matches_independent_whole_digest | KEEP | independent sha oracle across index loss/corruption; kills D14 |
| counters.rs | idle_round_makes_no_puts_and_reads_nothing | KEEP | DESIGN 15 counter test (idle) |
| counters.rs | non_prefix_stable_source_still_reads_in_full | KEEP | negative control proving the read counter detects full reads |
| counters.rs | reconcile_at_30k_unchanged_sources_stats_each_once | KEEP | DESIGN 15 counter <=1 stat/source; slowest test 11.6 s; 1 s target only printed |
| daemon.rs | auth_failure_pauses_host_and_recovers | KEEP | host pause durable; kills D15 |
| daemon.rs | authoritative_park_is_written_inside_the_interval | KEEP | DESIGN 11.3 durable park; kills D4,D5 |
| daemon.rs | corrupt_remote_parks_the_source_after_three_hours | KEEP | DESIGN 11.2 deterministic park threshold; kills C8,D4,D14 |
| daemon.rs | damaged_index_and_status_are_treated_as_missing | KEEP | DESIGN 9.2/9.3 damaged derived state rebuilds |
| daemon.rs | first_round_commits_and_persists_then_rebuild_does_not_reupload | REMOVE | convoluted (first daemon discarded); same claim pinned wider by counters::hegel_converged_reconcile_makes_zero_puts (delete-index), damaged_index_*; kills only D14 which 7 others kill |
| daemon.rs | foreign_writer_parks_owner_only | KEEP | DESIGN 11.3 park persists across restart; kills C9,D4,D11,D14 |
| daemon.rs | guard_has_negative_controls | MERGE | single shared helper test with commit_protocol copy; drop the SHAPE_NAMES.contains(REQUIRED_NEW) constant-vs-constant loop |
| daemon.rs | hegel_daemon_state_machine | KEEP | DESIGN 15.4 daemon-level; kills 6 mutants incl. X1,D10,D17; lacks rename-replace and stat/read race |
| daemon.rs | held_lock_refuses_before_tmp_is_swept | KEEP | DESIGN 12/9.1: lock before any state mutation |
| daemon.rs | hint_launch_attached_once_and_offset_advances_after_commit | KEEP | hint offset after commit, canary privacy; kills D9,D16 |
| daemon.rs | hint_partial_progress_commits_prefix_and_torn_tail_waits | KEEP | hint offset prefix/torn-tail semantics |
| daemon.rs | hints_dir_symlink_refuses | KEEP | DESIGN 9.1 hints dir vetting; kills D16 |
| daemon.rs | lost_reply_converges_without_duplicate_epoch | KEEP | daemon-level idempotent at-least-once |
| daemon.rs | missing_hints_dir_is_an_empty_scan | MERGE | into replaced_hint_file_restarts...: one-line scenario |
| daemon.rs | prepare_sweeps_tmp_entries_and_stale_root_temp_files | MERGE | into state.rs (crash_before_rename_leaves_old_file_readable_and_start_sweeps already covers sweep) |
| daemon.rs | proptest_daemon_state_machine | KEEP | second engine; FLAKY: guard fails at default 256 cases (4/8 runs) |
| daemon.rs | replaced_hint_file_restarts_and_unreadable_entries_are_skipped | KEEP | kills D17; absorb missing_hints_dir |
| daemon.rs | retry_delay_defers_the_owner_until_the_clock_passes_it | KEEP | DESIGN 11.2 backoff defer; depends on deterministic jitter draw (brittle); kills D3 |
| daemon.rs | second_process_exits_lock_held | KEEP | process-level supervisor contract exit 75 through main |
| daemon.rs | shutdown_table | KEEP | DESIGN 9.2 flush at shutdown; d.root() assertion trivial; D18 (no index flush) survives: assert index.json written |
| daemon.rs | start_error_code_table | KEEP | operator-visible exit codes 2 and 1 |
| daemon.rs | state_api_failure_table | MERGE | into state.rs tests; chmod-based so fails when run as root; tests StateDir/DaemonLock not the daemon |
| daemon.rs | status_inside_interval_is_deferred_then_flushed_at_shutdown | KEEP | DESIGN 9.2 60 s rule with exact boundary; kills D6 |
| daemon.rs | two_parked_owners_are_listed_sorted | STRENGTHEN | 2 owners pass a HashMap order bug 50% of runs; use 8+ owners; kills D12 |
| daemon.rs | two_sources_of_one_owner_commit_once | KEEP | owner grouping |
| daemon.rs | unlanded_first_write_resets_the_base_and_converges | STRENGTHEN | base is None on first write so the reset is untestable here (D2 survives); use a later write with Ghost store; third bespoke store fake |
| daemon.rs | unrenderable_wall_clock_is_a_round_error | KEEP | fail-closed boundary year>9999; kills D19 (low value) |
| daemon.rs | unwritable_index_is_a_round_error_when_status_is_not_due | MERGE | with unwritable_state_dir_*: same propagation contract, incidental ordering |
| daemon.rs | unwritable_state_dir_is_a_round_error_on_first_status_write | MERGE | with unwritable_index_* as one table over which write fails; chmod-based (root fragile) |
| daemon.rs | window_defaults_fill_the_missing_half | KEEP | only test of config window defaults (quiet 60, cap 300); move to config tests |
| harness.rs | scaffold_binary_refuses_capture_instead_of_success | REMOVE | pins placeholder exit 78 of a stub that will be deleted; no logic; main wiring covered by second_process_exits_lock_held |
| mutation_daemon.rs | failed_commit_does_not_advance_the_local_index | KEEP | SOLE killer of advance-on-fail (D1): both daemon properties survive it |
| mutation_daemon.rs | stale_if_match_is_refused_by_the_store_before_the_rebase | KEEP | kill test for blind If-Match (C12); only test with a store-side 412 oracle |
