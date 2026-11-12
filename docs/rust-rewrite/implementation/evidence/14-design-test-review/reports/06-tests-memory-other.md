# 06 - Test audit: memory crate (capture, state, hook, realfs, resolve, watch) and xtask

Scope: rust/crates/memory/tests/{capture,state,hook,realfs,resolve,watch}.rs, rust/crates/xtask/tests/cli.rs, the one `#[cfg(test)]` module (xtask/src/main.rs). The memory crate src has no `#[cfg(test)]` modules. Read-only on the repo; all mutants ran in a private copy (`scratchpad/review/06/rust`, runner `06/mut.py`, raw output `06/mut.out`, `06/mut2.out`).

## Summary verdict
Roughly 85% of the 113 tests earn their place (real OS behaviour, privacy canaries, table-driven failure classification, crash points). Removable: 12 tests outright, 6 merges, 4 strengthens. Two serious findings are about what the suite does NOT do:
1. **The durability steps of DESIGN 9.2 are untested.** Deleting every `sync_all` (file and directory) fails zero tests (mutants `fsync-removed`, `fsync-dir-removed`). The "crash" tests only drop a `Staged` value in-process; they can never observe a missing fsync, and the `none` flag in the crash property makes the expectation identical for both policies.
2. **xtask `cli.rs` neutralises gate 7 (schema drift).** `xtask schemas` (no `--check`) rewrites `rust/schemas/*.json`; the CLI test runs it, and gates.sh runs nextest (step 04) before `schemas --check` (step 07). Reproduced: stale `memory.hint.1.json` -> `--check` rc=1; run `cargo test -p xtask --test cli`; `--check` rc=0. The drift gate can never fail inside gates.sh, and a local `cargo test` silently rewrites tracked files.

## Authoritative sources (fetched this session)
- Software Engineering at Google ch. 12 (https://abseil.io/resources/swe-book/html/ch12.html): "write a test for each behavior" not each method; brittle tests are those that "fail in the face of an unrelated change to production code that does not introduce any real bugs"; "interaction tests check how a system arrived at its result, whereas usually you should care only what the result is".
- SWE at Google ch. 13 (https://abseil.io/resources/swe-book/html/ch13.html): interaction-heavy tests are "change-detector tests"; a fake "should maintain fidelity to the API contracts of the real implementation"; "Interaction testing should be avoided when possible."
- Khorikov, Unit Testing: Principles, Practices, and Patterns: four pillars (protection against regressions, resistance to refactoring, fast feedback, maintainability). Fetched via search result citing the Manning ch. 1 excerpt https://enterprisecraftsmanship.com/files/Unit-Testing-Chapter-1-Excerpt.pdf (direct page fetches 404; pillars confirmed from the search summary only, not from reading the excerpt text).
- DESIGN.md section 15 (read first): properties 1-7, counter tests, journey test, footprint targets; gates incl. "Monthly: cargo cargo mutants on memory-core" (not on the memory crate).

## Numbers
| binary | tests | runtime (debug, 100 cases, 2 threads) |
|---|---|---|
| capture | 16 | 0.05 s |
| state | 25 (incl. 2 hegel, 3 proptest-style) | 2.5-3.0 s (real F_FULLFSYNC) |
| hook | 26 + 2 ignored | 2.1-3.9 s (spawns the binary ~60 times) |
| realfs | 15 | 0.06 s |
| resolve | 12 | 0.02 s |
| watch | 16 | 0.37 s |
| xtask unit / cli | 1 / 1 | 0.02 s / 0.37 s |
Wall times including cargo: 0.7-6 s per binary. Total 111 run + 2 ignored.
Existing mutation evidence: only 3 production mutants (scripts/check-mutations.py: if-match, advance-on-fail, privacy-deny) plus the window mutant; none touches state/hook/realfs/watch/resolve. I ran 23 new mutants (list below).

## Mutation results used (failed-test sets, workspace `memory` tests, Hegel/proptest 100 cases, fixed seeds; daemon state-machine properties skipped because their coverage guards fail at low case counts by design)
| mutant | tests that failed |
|---|---|
| hook-group-writable (`0o022`->`0o002`) | core_refuses_group_or_world_writable_dir, binary_refuses_writable_dir_without_file |
| hook-exit-failure | 8 binary tests (binary_appends, failure_paths, refuses_writable, canaries, complete_object, concurrent, missing_state, stdin_left_open) |
| hook-file-mode-644 | core_creates_missing_dirs_0700_and_file_0600, binary_appends_for_each_agent_with_modes |
| hook-truncate-not-append | second_hook_appends_second_line, concurrent_hooks, binary_appends, canaries, launch_evidence |
| hook-code-dup | result_codes_are_fixed_and_distinct ONLY |
| realfs-display | error_display_is_closed ONLY |
| realfs-invalidrange-kind | read_errors_carry_the_io_kind, read_exact_short_and_past_eof |
| realfs-stat-follows-symlink | stat_absent_present_and_symlink ONLY |
| realfs-read-no-take | read_exact_short, hegel+proptest read_equals_range |
| capture-snapshot-blind | snapshot_change_is_replaced, snapshot_unchanged_then_changed_after_commit |
| object-key-prefix | 41 tests, including captured_pieces_are_stored_under_object_keys and all of commit_protocol |
| privacy-credential-skip | admission_denials_map_to_literal_withhold_reasons, symlink_to_credential_is_denied, withheld_source_is_never_read_or_stored |
| coalescer-quiet-gt | hegel+proptest scheduler, quiet_expiry, stop_stays_debounced, in_flight_owner_queues |
| coalescer-cap-gt | cap_expiry_under_continuous_signals ONLY (the property misses it) |
| coalescer-flush-ignored | hegel+proptest scheduler, session_end_flushes_at_once |
| watch-stop-as-flush | hegel+proptest scheduler, stop_stays_debounced |
| watch-wake-ignored | wake_gap_over_120_seconds_reconciles ONLY |
| watch-unmapped-uncounted | unmapped_path_is_counted_and_ignored ONLY |
| watch-access-as-change | translate_covers_every_event_shape ONLY |
| fsync-removed / fsync-dir-removed | NOTHING |
| state-code-dup | error_text_is_closed, daemon hints_dir_symlink_refuses |

## Findings, most severe first
1. **fsync/rename durability unverified** (high, high confidence). Evidence: mutants above; state.rs crash_point (lines 950-993) only drops values. The honest oracle needs a seam: inject a `Sync` trait/counter into `write_synced`/`fsync_dir` and assert order (write -> file sync -> rename -> dir sync), or an fault-injecting FS. Missing-test item below. Action: add the ordering test; until then the docs' "crash simulation" wording overstates (it covers process death with intact page cache, not power loss).
2. **cli.rs defeats the schema-drift gate** (high, high). Action: REMOVE cli.rs's `schemas` (write) invocation; test dispatch with `--check` only or with a temp `--root`; the xtask unit test already round-trips in a fixed repo path (`rust/artifacts/schema-diagnostic`, not unique per run; racy for concurrent invocations; low).
3. **Property misses the cap boundary** (medium, high): `coalescer-cap-gt` is killed only by the table `cap_expiry_under_continuous_signals`; watch.rs `hegel_scheduler_matches_model` draws gaps in 0..200 s but rarely holds continuous sub-quiet signals for 300 s, and the model hard-codes 60/300. STRENGTHEN the generator (bursts of dt<60 with total reaching cap), keep the table. DESIGN 15.2 says the coalescer property must show "emits within cap".
4. **Wire-timing tests are wall-clock dependent** (medium, medium): hook `stdin_left_open_exits_within_bound` (4.5 s bound vs 2 s deadline), `complete_object_with_stdin_open_does_not_wait` (1.5 s, spawn under load), watch `fs_watcher_rejects_missing_root_and_times_out_quietly` (drain 300 ms then 50 ms silence against FSEvents latency), and `wait_for` 10 s polls. They are behaviour-relevant but not deterministic; replace the 1.5 s one with `try_wait` polling and a longer ceiling, and assert "exited before stdin closed" instead of an elapsed bound.
5. **Tests that pin scaffolding / dead API** (medium, high): hook `result_codes_are_fixed_and_distinct` tests `HookResult::code()`, which has no production caller (grep: only daemon uses StateError/ConfigError codes); `other_subcommands_keep_explicit_failure` and `harness.rs` pin the placeholder `_ => 78`; P4 tests are `#[ignore]`, assert nothing about time, and are in no gate (DESIGN footprint target "1 ms p99" is therefore unenforced, only printed).
6. **Duplicated fixtures** (low, high): `MemStore` is copy-pasted in capture.rs and resolve.rs (and daemon/commit_protocol per the other agents); `MemFs/Rig` helpers likewise. These are fakes with real precondition semantics (fine under SWE ch.13 fidelity rule) but should be one `tests/support` module; drift between copies would be silent.
7. **Vacuous assertion** (low, high): resolve.rs:385 `assert!(!contains(&store, b"secret"))`. The credential file holds `CREDENTIAL-MARKER` (no "secret"); the line can never be the one that fails. The previous assertion already guards it. Delete the line, also `let _ = landed;`.
8. **walk_counts_non_utf8_names_and_non_regular_files** (low): on macOS (APFS) the bad name cannot be created, so the test degenerates to `invalid_names == 0 && files == 1`; the name also claims "non_regular" which it never tests (the socket test does). Strengthen with a Linux-only `#[cfg]` assertion and rename. Also: `walk_skips_sockets_and_fifos...` creates no fifo; and macOS `sun_path` limit (104) can make `UnixListener::bind` fail when TMPDIR is long (environment-dependent; not observed).
9. **root/permission-dependence** (low): chmod-000 tests (realfs x2, hook core_unreadable, state read_failures) pass vacuously or fail as root; only state/realfs guard for root.
10. **Two engines for the same property** (info): state (2 properties x hegel+proptest + crash), hook, realfs, watch. Justified by DESIGN gate 6 "proptest cross-check"; cost is small (sub-3 s). Keep.

## Does realfs.rs / watch.rs test real OS behaviour?
- realfs.rs: yes for what it covers: `symlink_metadata` semantics (mutant `metadata` is killed), FileId stable on append / changes on rename-replace, exact-range reads vs a slice oracle, no-follow walk, depth/entry bounds, unreadable dirs, sockets. Deterministic (no sleeps). It does not and cannot test fsync/rename durability (that is state.rs, finding 1). Not covered: birth-time Known/Unavailable branch, hardlinks, inode reuse, a 30k-source walk (DESIGN 15 counter tests are with the counters.rs agent).
- watch.rs: the scheduler tests are pure and deterministic. The three FsWatcher tests use the real macOS FSEvents backend (through notify): they prove the plumbing delivers PathChanged/DirCreated for a write and a new directory, eventually (10 s ceiling), but not Rescan/overflow, rename, delete or DESIGN 10.2's "directory created with existing children produces no events" (the new-dir test accepts either DirCreated or any child event, so it cannot tell). Timing-dependent in latency but not in outcome, except the quiet-timeout test.
- state.rs: real flock, real chmod, real rename; `second_lock...` correctly tests the open-file-description semantics a second process would see (a real second process is never spawned).

## Verdict reasoning highlights (empirical column = evidence)
REMOVE (verified by mutant unless stated):
- hook::second_hook_appends_second_line: truncate mutant also kills concurrent_hooks (24 lines) and binary_appends (3).
- hook::binary_refuses_writable_dir_without_file: group-writable mutant also kills core_refuses_...; the binary always exits 0 (`main_entry` ignores the result), so the extra assertion adds only "no file", which core asserts.
- hook::result_codes_are_fixed_and_distinct: only test killed by a code-dup mutant; `code()` unused in production.
- hook::p4_core_latency, p4_spawn_latency: no timing assertion, ignored, in no gate (by reading; cannot fail on any mutant). Move to a bench/xtask if wanted.
- hook::other_subcommands_keep_explicit_failure: pins the scaffold `78` exit (by reading, not mutated); remove with the placeholder or when adapters land (and harness.rs, owned elsewhere).
- realfs::error_display_is_closed: only killed by editing a Display string; no consumer depends on the text.
- realfs::read_errors_carry_the_io_kind, unreadable_file_is_permission_denied_on_read: MERGE into read_exact_short (invalid-range mutant also kills it; PermissionDenied part tests std::fs and is accepted as `Err or Ok` so it is a weak oracle). Keep only "directory read is Io" and "failed reads are not counted".
- capture::captured_pieces_are_stored_under_object_keys: key-prefix mutant kills 40 other tests.
- capture::withheld_source_is_never_read_or_stored: credential-skip mutant also kills admission_denials (which asserts the same changes-empty/store-unchanged); neither asserts "never read", add a Spy read-count to the surviving one (STRENGTHEN).
- capture::snapshot_change_is_replaced: blind-fingerprint mutant also kills snapshot_unchanged_then_changed_after_commit, which also covers the same-size mtime-only case. MERGE.
- watch::quiet_expiry, stop_stays_debounced, session_end_flushes_at_once: killed by the scheduler properties on the quiet-gt / stop-as-flush / flush-ignored mutants. Remove; keep cap_expiry, in_flight, wake_gap, unmapped, clock_regressions (properties miss them: cap and wake-gap mutants killed only by the tables).
- state::error_text_is_closed: only it (plus an incidental daemon test) kills a code-dup; asserts uniqueness and `starts_with`, change-detector. Remove, or fold the "no '/' in error text" privacy rule into config_errors_never_echo_values.
- hook::binary_appends_for_each_agent_with_modes mode asserts, resolve::shared_owner_and_profiles_are_literal third block: MERGE (duplicated by core_creates... and missing_root_and_directory_target_fail_closed).
- realfs: `d.max_depth == 32 && max_entries == 500_000` line in walk_depth_and_entry_bounds: change detector; drop. walk_entry_bound_stops_partway overlaps the `cut` case: MERGE.

STRENGTHEN: capture::snapshot_replaces_and_protected_config_never_stores_the_canary (accepts `Planned | Withheld`; assert the exact outcome, the privacy property in memory-core carries the wide coverage); state::crash property + sync_policy test (inject sync seam, finding 1); watch property (cap bursts, Tick/wake inputs and DirCreated/WatcherError in the model); hook::complete_object... (try_wait).

KEEP everything else; notably: state corruption table, lock/vet refusals, config tables and no-echo, machine-id once-only, hook golden lines / refusals / concurrency / canaries / write_line fault injection, capture failure and withhold-reason tables, need_windows read budget (DESIGN 15 counter), all resolve fail-closed tests, translate table (only killer of access-event mutant), walk symlink/bounds tests.

## Missing tests DESIGN implies
- fsync ordering/durability seam (9.2) and a crash simulation that discards unsynced writes.
- Footprint/performance: hook in-process p99 <= 1 ms and 30k-source full reconcile <= 1 s, one stat per source (gates, not printed numbers); journey test (replay timelines through daemon -> reader).
- Hook: SessionEnd -> immediate flush chain across hook -> hint reader -> Scheduler (hook and watch are only tested separately; `Input::Hint` is built by hand).
- Watch: real Rescan/overflow, delete/rename events, DirCreated-with-children race (10.2), watcher over a root that is removed later.
- Schemas: xtask test that an unknown `schema` string is rejected is in core (not audited here); cli test should use a temp root.
- Capture: property over withhold/transform canaries at store sink with Snapshot class is covered by capture_property/privacy (other agents); the 59 KB append "reads at most 59 KB + 68 KiB" is counters.rs.

## Open questions and experiments
- Does `Staged::publish` ordering matter on APFS in practice? Experiment: a dtruss/fs_usage trace of write_durable to confirm fcntl(F_FULLFSYNC) calls on file and dir (not run; needs privileges).
- Is the scheduler property killing cap at 2000 cases? Run mutant `coalescer-cap-gt` at HEGEL_TEST_CASES=2000 (I used 100).
- Hosted CI OS: are Linux-only branches (non-UTF-8 names) ever run? Check .github/workflows/rust.yml matrix (not read).

## Not covered
commit_protocol/daemon/mutation_daemon/counters/capture_property/harness.rs (other agents); memory-core tests; line-coverage numbers; Linux behaviour; a 2000-case run.

## Machine-readable table
file | test name | verdict | one-line reason
capture.rs | append_extends_then_unchanged_uploads_nothing | KEEP | idempotence (15.3) and kinds; unique extend/epoch assertions
capture.rs | rewrite_and_truncate_start_a_new_epoch | KEEP | epoch kinds not asserted by capture_property
capture.rs | removed_source_is_deleted_only_after_two_observations | KEEP | delete debounce, unique
capture.rs | snapshot_replaces_and_protected_config_never_stores_the_canary | STRENGTHEN | accepts Planned or Withheld; assert exact outcome
capture.rs | snapshot_change_is_replaced | MERGE | into snapshot_unchanged_then_changed_after_commit (same mutant kills both)
capture.rs | withheld_source_is_never_read_or_stored | MERGE | into admission_denials (same mutant); add read-count spy
capture.rs | captured_pieces_are_stored_under_object_keys | REMOVE | 40 tests fail on key-prefix mutant; assertion is weak any()
capture.rs | launch_canaries_never_reach_any_stored_object | KEEP | property 5 at store sink with positive control
capture.rs | attach_launches_deduplicates_by_digest | KEEP | small unit, unique
capture.rs | need_windows_reads_head_and_tail_through_the_seam | KEEP | read-budget contract (15 counters)
capture.rs | failure_branches_have_literal_outcomes_and_store_nothing | KEEP | fault table
capture.rs | admission_denials_map_to_literal_withhold_reasons | KEEP | privacy mapping; killed credential mutant
capture.rs | transform_denials_map_to_literal_withhold_reasons | KEEP | fail-closed mapping incl. 8 MiB limit
capture.rs | snapshot_read_failures_have_literal_outcomes | MERGE | into failure_branches table (same shape)
capture.rs | snapshot_unchanged_then_changed_after_commit | KEEP | strongest snapshot test (mtime-only change)
capture.rs | adopt_prefix_read_failures_have_literal_outcomes | MERGE | into failure_branches table
state.rs | round_trip_and_modes | KEEP | 0600/0700 modes and envelope (9.1)
state.rs | older_schema_version_is_readable | KEEP | schema compat rule
state.rs | every_corruption_reads_as_missing_with_its_reason | KEEP | corruption table, fail-closed
state.rs | read_failures_are_missing | KEEP | oversize/unreadable boundaries
state.rs | invalid_names_and_sizes_are_refused | KEEP | boundaries
state.rs | crash_before_rename_leaves_old_file_readable_and_start_sweeps | KEEP | tmp/ sweep not in the property
state.rs | publish_failures_are_closed_errors_and_clean_up | KEEP | fault injection with real rename failure
state.rs | second_lock_is_held_in_process_and_on_a_second_description | KEEP | real flock semantics
state.rs | lock_refusals | KEEP | symlink/permission refusals
state.rs | hint_dir_refusals | KEEP | vetting; hook has its own copy of the rule
state.rs | prepare_refuses_bad_tmp | KEEP | refusal
state.rs | state_root_rule_matches_the_hook | KEEP | only HOME fallback coverage; name overclaims (never compares with hook)
state.rs | error_text_is_closed | REMOVE | change detector; fold no-'/' rule into config no-echo test
state.rs | machine_id_is_created_once_and_never_overwritten | KEEP | identity preservation (AGENTS.md)
state.rs | machine_id_race_has_one_winner | KEEP | real thread race, deterministic outcome
state.rs | config_accept_reject_table | KEEP | strict schema table
state.rs | config_errors_never_echo_values | KEEP | privacy
state.rs | config_load_and_token_checks | KEEP | token permission contract; trailing code-uniqueness loop is low value
state.rs | hegel_write_read_is_identity | KEEP | wide-input round trip (gate 6 cross-check)
state.rs | hegel_single_byte_mutation_never_yields_a_different_body | KEEP | checksum safety property
state.rs | proptest_write_read_is_identity | KEEP | required cross-check engine
state.rs | proptest_single_byte_mutation_never_yields_a_different_body | KEEP | required cross-check engine
state.rs | sync_policy_default_is_full | STRENGTHEN | assert real sync calls via an injected seam; fsync removal survives all tests
state.rs | hegel_crash_between_stage_and_publish_reads_old_or_new | STRENGTHEN | cannot see missing fsync; add power-loss/ordering oracle
state.rs | proptest_crash_between_stage_and_publish_reads_old_or_new | STRENGTHEN | same as hegel twin
hook.rs | golden_line_per_agent | KEEP | wire format contract
hook.rs | second_hook_appends_second_line | REMOVE | concurrent_hooks and binary_appends fail on append mutant
hook.rs | launch_evidence_only_on_session_start | KEEP | privacy allowlist + SessionStart rule
hook.rs | session_start_without_launch_evidence_has_none | MERGE | into launch_evidence_only_on_session_start
hook.rs | oversize_launch_is_reduced_within_bound | KEEP | 4096-byte line bound regression
hook.rs | core_rejects_bad_agent_date_and_input | KEEP | refusals, no dir creation
hook.rs | core_refuses_group_or_world_writable_dir | KEEP | killed group-writable mutant
hook.rs | core_refuses_non_directory_and_symlink | KEEP | symlink refusal
hook.rs | core_unreadable_parent_and_readonly_dir | KEEP | real EACCES paths (root caveat)
hook.rs | core_creates_missing_dirs_0700_and_file_0600 | KEEP | killed 0644 mutant
hook.rs | result_codes_are_fixed_and_distinct | REMOVE | tests API with no production caller; change detector
hook.rs | utc_day_known_values | KEEP | own date code vs hand-computed oracle
hook.rs | read_bounded_stops_at_end_of_value | KEEP | parser boundaries
hook.rs | read_bounded_caps_at_limit_plus_one_and_survives_errors | KEEP | input bound, error survival
hook.rs | binary_appends_for_each_agent_with_modes | MERGE | drop mode asserts (core test kills 0644 mutant too); keep 3-record e2e
hook.rs | concurrent_hooks_append_whole_lines | KEEP | real O_APPEND atomicity across processes
hook.rs | binary_refuses_writable_dir_without_file | REMOVE | redundant with core_refuses; exit always 0
hook.rs | binary_failure_paths_exit_zero_silently | KEEP | hook must never fail the agent
hook.rs | missing_state_dir_environment_exits_zero | KEEP | no-env path
hook.rs | other_subcommands_keep_explicit_failure | REMOVE | pins scaffold exit 78; remove with placeholder
hook.rs | stdin_left_open_exits_within_bound | KEEP | real deadline behaviour; wall-clock flake risk
hook.rs | complete_object_with_stdin_open_does_not_wait | STRENGTHEN | replace 1.5 s elapsed bound with try_wait
hook.rs | canaries_never_reach_the_file | KEEP | privacy at binary level (15.5)
hook.rs | write_line_short_and_failed_writes_are_failures_without_retry | KEEP | fault injection via sink
hook.rs | hegel_core_never_panics_and_writes_at_most_one_whole_line | KEEP | independent oracle property
hook.rs | proptest_core_never_panics_and_writes_at_most_one_whole_line | KEEP | cross-check engine
hook.rs | p4_core_latency | REMOVE | ignored, no assertion, in no gate; make it an enforced gate or a bench
hook.rs | p4_spawn_latency | REMOVE | ignored, informational only, cannot fail
realfs.rs | stat_absent_present_and_symlink | KEEP | only test killed by metadata-follows-symlink mutant
realfs.rs | file_id_changes_on_replace_and_stays_on_append | KEEP | real-OS identity semantics (7.1)
realfs.rs | read_exact_short_and_past_eof | KEEP | absorbs read_errors; counters
realfs.rs | error_display_is_closed | REMOVE | only killed by editing a message; nothing consumes the text
realfs.rs | walk_synthetic_layouts | KEEP | deterministic order, locator integration
realfs.rs | walk_does_not_follow_symlinks | KEEP | safety (7.1)
realfs.rs | walk_depth_and_entry_bounds | MERGE | with walk_entry_bound_stops_partway; drop documented-default assertion
realfs.rs | walk_counts_unreadable_dirs_and_missing_root | KEEP | closed counts (root caveat)
realfs.rs | walk_counts_non_utf8_names_and_non_regular_files | STRENGTHEN | degenerate on APFS; add Linux cfg; fix name
realfs.rs | hegel_read_equals_range_intersection | KEEP | slice oracle, killed read mutant
realfs.rs | proptest_read_equals_range_intersection | KEEP | cross-check engine
realfs.rs | read_errors_carry_the_io_kind | MERGE | into read_exact_short (invalid-range mutant kills both)
realfs.rs | unreadable_file_is_permission_denied_on_read | MERGE | tests std; accepts Err or Ok; fold into read_errors
realfs.rs | walk_entry_bound_stops_partway_through_a_directory | MERGE | with walk_depth_and_entry_bounds
realfs.rs | walk_skips_sockets_and_fifos_without_counting_them_as_files | KEEP | real file types; name claims fifo but only socket
resolve.rs | plain_file_resolves_and_is_admitted | KEEP | basic fact contract
resolve.rs | ancestors_include_root_and_parents | KEEP | privacy ancestor identities
resolve.rs | symlink_inside_root_and_chain_record_every_spelling | KEEP | identity union
resolve.rs | symlink_to_credential_is_denied_through_identity_union | KEEP | killed credential mutant
resolve.rs | loop_dangling_and_hop_overflow_fail_closed | KEEP | fail-closed boundaries
resolve.rs | missing_root_and_directory_target_fail_closed | KEEP | fail-closed
resolve.rs | file_removed_between_walk_and_resolve_fails_closed | KEEP | TOCTOU
resolve.rs | handles_are_stable_and_withheld_rows_are_counted | KEEP | counts contract
resolve.rs | shared_fs_reports_busy_instead_of_panicking | KEEP | re-entrancy guard; low value but cheap
resolve.rs | end_to_end_commits_transcript_and_never_a_symlinked_credential | STRENGTHEN | delete vacuous `secret` assertion and `let _ = landed`
resolve.rs | shared_owner_and_profiles_are_literal | MERGE | third block duplicates missing_root_and_directory test
resolve.rs | unknown_owner_rows_are_counted_not_yielded | KEEP | counted-not-yielded rule
watch.rs | quiet_expiry | REMOVE | scheduler property killed by quiet-gt mutant too
watch.rs | cap_expiry_under_continuous_signals | KEEP | only killer of cap-gt mutant (property gap)
watch.rs | session_end_flushes_at_once | REMOVE | property killed flush-ignored mutant too
watch.rs | stop_stays_debounced | REMOVE | property killed stop-as-flush mutant too
watch.rs | in_flight_owner_queues_exactly_one_follow_up | KEEP | follow-up and NotInFlight error
watch.rs | unmapped_path_is_counted_and_ignored | KEEP | only killer of unmapped mutant (7 locator counting)
watch.rs | dir_created_reads_the_directory | KEEP | 10.2 rule, tiny
watch.rs | rescan_and_error_reconcile_all | KEEP | 10.2 reconcile on loss
watch.rs | wake_gap_over_120_seconds_reconciles | KEEP | only killer of wake mutant; boundary 120/121
watch.rs | clock_regressions_are_counted_not_fatal | KEEP | fault boundary
watch.rs | translate_covers_every_event_shape | KEEP | only killer of access-event mutant
watch.rs | hegel_scheduler_matches_model | STRENGTHEN | add cap bursts, Tick, DirCreated, WatcherError
watch.rs | proptest_scheduler_matches_model | KEEP | cross-check engine
watch.rs | fs_watcher_reports_file_write | KEEP | real FSEvents; eventual, 10 s ceiling
watch.rs | fs_watcher_reports_new_directory_with_file | STRENGTHEN | accepts any child event; cannot verify DirCreated rule
watch.rs | fs_watcher_rejects_missing_root_and_times_out_quietly | STRENGTHEN | timing flake risk (300 ms drain); split missing-root from quiet check
xtask/tests/cli.rs | schema_cli_dispatches_and_rejects_unknown_commands | STRENGTHEN | rewrites rust/schemas before the drift gate; use --check or temp root
xtask/src/main.rs | schemas_detect_missing_drift_and_roundtrip | KEEP | only real test of drift detection; use unique temp dir
