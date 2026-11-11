# Test audit: memory-core (tests/ and #[cfg(test)])

## Summary verdict
186 tests (nextest, `cargo nextest list -p memory-core`): 185 in 15 files under `tests/`, 1 in `src/privacy/names.rs`. About 836 assertion macros (assert, assert_eq, assert_ne, prop_assert*, ensure!; counted with grep, so approximate).

The suite is mostly sound. Most files are independent-oracle properties, wire goldens, negative fixtures or privacy fail-closed vectors, and those are real requirements. I propose **15 REMOVE, 6 MERGE, 14 STRENGTHEN, 151 KEEP**.

The waste is concentrated in three places:
1. Tests of std/serde/schemars (scaffold, schema introspection).
2. Tests that duplicate a table-driven or property sibling (URL scrubbing x4, the eleven protected names x2, r2/history smoke twins).
3. Meta-tests of test helpers (coverage-guard negative controls, a self-contained should_panic).

Empirical weaknesses found while checking (details in findings): the hint canary property does not kill a mutant that disables value sanitisation, privacy_property has no proptest twin, and one hegel guard is flaky at low case counts.

## Sources (fetched this session)
- Software Engineering at Google, ch. 12 (https://abseil.io/resources/swe-book/html/ch12.html): "a brittle test is one that fails in the face of an unrelated change to production code"; "rather than writing a test for each method, write a test for each behavior"; "interaction tests check how a system arrived at its result, whereas usually you should care only what the result is"; pure refactorings should not require test changes.
- Fowler, UnitTest (https://martinfowler.com/bliki/UnitTest.html): tests exist so "should I accidentally break something, I want to know right away"; sociable tests use real collaborators. This supports keeping the pure-core tests that use real types.
- Khorikov, four pillars (protection against regressions, resistance to refactoring, fast feedback, maintainability): secondary summaries only (https://notesbylex.com/4-pillars-of-good-unit-tests, search result). **Unverified against the book itself.** Used as the lens "a test with no regression protection and some maintenance cost has negative value".
- Mutation evidence: repo scripts `rust/scripts/check-mutations.py` and `check-window-mutation.py` give only 3+1 mutants, and no `cargo mutants` results are stored anywhere (DESIGN section 15 says "monthly", but there is no output in the repo). So I ran my own hand mutants (below).

## Method for empirical checks
I copied `rust/` to `scratchpad/review/work/rust` (never touched the repo; `git status` clean). For each mutant I applied one exact single-occurrence replacement, ran the full `cargo nextest run -p memory-core --no-fail-fast` with `HEGEL_TEST_CASES=100 PROPTEST_CASES=100` (baseline 186/186 pass), and recorded every failing test. A test is "unique" only if no other test fails. Scripts: `work/mut.py`, `work/batch*.sh`; raw logs `work/batch.log`, `batch2.log`, `batch3.log`. Caveat: two runs (O and an aborted P/Q) were disturbed by a tool timeout; P and Q were rerun cleanly, and the copy was verified identical to the repo afterwards.

### Mutants and results (17 runs)
| id | mutation | killed by | consequence for verdicts |
|---|---|---|---|
| A | slice::bytes swallows error | scaffold x3 + core_hint reader_pending_tail | scaffold tests pin only std; consumer test exists -> REMOVE scaffold |
| B | SourceSet dedupe off | hegel/proptest admission, sources_cap_and_never_evict | **rewrites_of_one_source_use_one_slot SURVIVES** -> REMOVE |
| C | Agent::Pi as_str "p" | key_exact_spelling, key_boundary_shared, agent_wire_matches_serde, hegel_coordinates | agent_wire not unique -> REMOVE |
| D | planner window check off | property1 hegel+proptest, 2 planner tests | **negative_control_disabled_window_check does NOT fail**; real control is D -> REMOVE |
| E | drop managed_config.toml from protected list | row1a, row10b, privacy_port all_eleven, rule_digest_golden, privacy_property x2 | all_eleven redundant with row1a -> MERGE |
| F | URL control-char check off | privacy_coverage malformed_url_forms, privacy_port toml_datetime_url | parent_review URL tests do not kill |
| G | URL path escape check off | parent_review invalid_escape, privacy_coverage malformed_url_forms, privacy_port toml_datetime_url | parent_review escape redundant -> MERGE |
| H | flushes() drops Interrupted | only_session_end_and_interrupted_flush (alone) | unique -> KEEP |
| I | withhold_reason UnknownKind->UnknownOwner | withhold_reasons test, property7 total_and_countable x2 | withhold test redundant -> REMOVE |
| J | object_key prefix changed | 23 tests including schemas_are_derivable | redundant -> REMOVE |
| K | launch safe() always true | row1b x2, privacy_coverage launch | **all hint canary tests survive** (finding 1) |
| L | adopt_stored no-op | r2_both_orders + hegel/proptest r2 | r2_both_orders redundant -> REMOVE |
| M | Sha256 accepts uppercase | core_boundaries opaque_fence, delta_negative_fixtures, core_mechanics boundaries_strict | mechanics copy redundant -> MERGE |
| N | fold limit 64 -> 65 | fold_rejects_broken_chains, hegel/proptest deltas, history_smoke | smoke not unique -> REMOVE |
| O | HintRecord deny_unknown_fields off | reader_rejects_and_counts_bad_own_lines, schema_is_generated_and_strict | schema test redundant with the reader test (+ drift gate) -> REMOVE |
| P | snapshot rules-change check off | core_planner sqlite_db_and_wal test, coordinates hegel (flake) | planner sqlite test unique -> KEEP |
| Q | positional argv leaked | 10 tests incl. canaries_fixed_seed | fixed seed not unique -> REMOVE |

REMOVE claims verified empirically: 13 distinct tests/groups (A,B,C,D,I,J,L,N,O,Q plus E,G,M for MERGE). Others (explicit_alias_pair, parent_review port test, url_rewrite...) are hand-argued subsets and marked as such.

## Findings (most severe first)

1. **Hint canary property is weaker than DESIGN property 5 claims** (medium, confidence high). Mutant K (`safe()` in `privacy/launch.rs:~43` returns true) is killed by no hint test (`hegel/proptest_no_canary_in_any_line`, `canaries_fixed_seed`, `golden_launch_line`). The generated argv in `core_hint.rs:check_canaries` puts canaries only in positions the scrubber rewrites wholesale (api-key flags, positional, secret env names). It never plants one in a VALUE_FLAGS value or an allowlisted env value that merely looks safe. Failure scenario: a regression that lets a secret-shaped value through `--model` reaches the hint log while the "property 5" test stays green. Action: STRENGTHEN as listed; today only row1b and `launch_empty_unknown_missing_and_duplicate_metadata` catch it.
2. **`hegel_coordinates_order_proof_and_retention` is flaky at low case counts** (medium, confidence medium). It failed in 5 of 17 mutant runs (C, E, F, J, P) where the mutated code is unrelated, at 100 cases under machine load; baseline passed. Cause is inferred (the `Stats::guard` requires proof-only/resolved > 0 and fails when the generator misses). docs/quality-gates.md already says low-case runs fail guards "by design", so it is a known property. A mutation-killed list polluted by a flake is not trustworthy, and CI at 500 cases may still flake. Experiment: run the test 50x at 500 cases with random seeds.
3. **privacy_property is a single-engine, fixed-structure "property"** (medium, high). DESIGN 15 gate 6 requires a proptest cross-check; the file has no proptest, and `check_case` varies only a hex canary inside 16 fixed templates. The inventory test (`independent_inventory_...`) is what actually does the work. Mutant E and Q are killed by example tests, not by generation.
4. **No reusable mutation evidence for memory-core**: the repo has three production mutants plus the window mutant; there is no `cargo mutants` output, although DESIGN 15 specifies a monthly run. cargo-mutants 27.1.0 is in the Nix shell. This audit's 17 mutants are the only per-test kill data. (low/medium, high)
5. **Coverage-guard helper copy-pasted** in core_mechanics, core_manifest, core_coordinates (`Shapes`/`require_all_shapes`/`Stats::guard` with three separate negative-control tests). Maintenance cost without extra protection. (low, high)
6. **Dead abstraction**: `slice::bytes` exists to be tested; its only consumer is hint.rs (two call sites). Inline `.get().ok_or` and drop the module and `RangeError`. (low, high)
7. **Change detectors** (low): exact `Display` strings in `errors_never_echo_input`; exact 953 ns and u64::MAX Duration in `budget_boundaries`; schemars introspection (`/oneOf/1`, title/pattern) in owner_wire_goldens and opaque_fence; hand-copied 29-kind list in locator reachability test. The schema export has its own drift gate, so these double-pin.

## Counts and per-module view
Tests per file: boundaries 6, coordinates 14, f2_gaps 12, hint 22, limits 30, locator 15, manifest 23, mechanics 10, planner 8, verify 8, parent_privacy_review 6, privacy_coverage 12, privacy_port 14, privacy_property 2, scaffold 3, names.rs 1.

Source module -> where it is tested:
- planner: planner(8), mechanics(property1), boundaries(1), f2_gaps(row5a/5b). Strong; both window mutants die.
- coalescer: mechanics (property2 + 3 unit) and boundaries (3). Strong.
- limits/disposition: core_limits (30; includes disposition). Strong; some boundary duplication.
- verify: core_verify (8). Complete truth table.
- domain.rs (927 lines): **no dedicated file**; covered incidentally by boundaries, mechanics, limits. Wire shapes of `Plan`, `Disposition`, `CommitOutcome`, `ErrorClass` serde are untested directly (only `LocalError`, `Owner`, `SourceKind`, `FileId` are).
- manifest/{schema,build,state,recovery,error}: core_manifest (23) + row12. Strong.
- locator: core_locator (15). Strong.
- coordinates: core_coordinates (14). Strong.
- hint: core_hint (22). Good, with the canary weakness above.
- privacy/{document,text,names,launch,mod}: ~34 tests across four files, heavy overlap on URL and launch handling.
- slice: scaffold (3), all low value.

Weak or no meaningful coverage:
- Weak: hint launch value sanitisation (K); the privacy property (finding 3).
- Untested from DESIGN 15 inside this crate (they belong to other crates or are not implemented yet): idle-minute zero syscalls, 30k-source reconcile, 59 KB append read bound, schema-drift and journey test. Property 3 (idempotence), property 4 (state machine) and property 6's "every schema round-trips" for `hint` and `current` have no memory-core test beyond `golden_checkpoint_delta_and_current` and `schema_is_generated_and_strict`; `Current` round-trips only on one example.
- Missing tests DESIGN implies: (a) property 5 on "env" and "symlink chains" beyond a fixed alias list is only unit-level here; (b) a generated round-trip for `Current` and `HintRecord` (property 6); (c) coalescer `cap`/`quiet` boundary at `quiet == cap`: `equal` window covered once in boundaries; fine.

## What is sound and why
- Independent oracles (property1 model, `justified` in coordinates, history `model_view` in manifest) never call the planner or builder for expectations: matches DDIA "oracles stated independently" per DESIGN 15.
- Dual-engine twins (hegel + proptest) are required by DESIGN gate 6, so I do not treat them as duplicates. Mutants D, J, N, P die in both.
- Negative fixtures in manifest (`delta_negative_fixtures`, 60+ cases) are wire-contract validations on untrusted input; each case maps to a named error.
- Privacy tests assert fail-closed on unknown/unresolved and that unrelated bytes are preserved; E, F, G, Q are killed by several independent tests.

## Open questions and experiments
1. Is the coordinates hegel flake real at CI settings? Run `HEGEL_TEST_CASES=500` 50 times, seeds random.
2. Would a full `cargo mutants -p memory-core --test-tool nextest` surface more survivors like B and K? Run it in a copy (cost estimate: several hundred mutants x ~40 s). I did not, because of machine contention.
3. Does removing the scaffold slice module change coverage floor? Floor is 80% (97.03% measured); removing 18 lines should be harmless. Check with the repo's llvm-cov gate.

## Not covered
Other crates (memory, memory-reader, xtask); gates.sh behaviour; hosted CI; a full cargo-mutants run; tests run at 2,000 cases (I used 100 to bound cost, which is what exposed the flake).

## Machine-readable table (file | test name | verdict | one-line reason)
core_boundaries.rs | changed_protected_rules_replay_remote_epoch_before_adopt_digest_is_requested | KEEP | boundary semantics
core_boundaries.rs | every_coordinate_brand_validates_its_own_boundary_and_preserves_valid_spelling | STRENGTHEN | loop over 4 brands via macro; drop serde-roundtrip repetition; keep 1024/1025 and invalid set
core_boundaries.rs | idle_slots_are_evicted_and_next_due_reports_earliest_deadline | KEEP | boundary semantics
core_boundaries.rs | opaque_fence_and_canonical_digest_serde_schemas_describe_their_distinct_domains | STRENGTHEN | drop schemars title/pattern asserts (schema drift gate); keep ETag/Sha256 validation (kills mutant M)
core_boundaries.rs | unmatched_completion_does_not_consume_pending_signal_or_advance_owner_clock | KEEP | boundary semantics
core_boundaries.rs | valid_nondefault_window_roundtrips_and_controls_both_deadlines | KEEP | boundary semantics
core_coordinates.rs | bounds_and_alias_depth | KEEP | privacy-relevant root ownership (Go fixture parity)
core_coordinates.rs | case_insensitive_alias_needs_proof_and_distinct_dirs_stay_distinct | KEEP | privacy-relevant root ownership (Go fixture parity)
core_coordinates.rs | default_and_custom_roots_need_no_filesystem | KEEP | privacy-relevant root ownership (Go fixture parity)
core_coordinates.rs | explicit_alias_pair_is_accepted | REMOVE | subset of nested_projection/parent_alias tests (hand argument, not mutated)
core_coordinates.rs | hegel_coordinates_order_proof_and_retention | STRENGTHEN | guard fails spuriously at low case counts: failed in 5 of 17 unrelated mutant runs at 100 cases; seed or raise floor
core_coordinates.rs | nested_projection_keeps_nested_identity_and_ignores_order | KEEP | privacy-relevant root ownership (Go fixture parity)
core_coordinates.rs | parent_alias_owns_nested_without_explicit_route | KEEP | privacy-relevant root ownership (Go fixture parity)
core_coordinates.rs | proptest_coordinates_order_proof_and_retention | KEEP | property, dual-engine per DESIGN 15 gate 6
core_coordinates.rs | rejected_declarations | KEEP | privacy-relevant root ownership (Go fixture parity)
core_coordinates.rs | stats_guard_bites | STRENGTHEN | same: shared guard module
core_coordinates.rs | unproven_physical_aliases_are_never_authorized | KEEP | privacy-relevant root ownership (Go fixture parity)
core_coordinates.rs | unproven_siblings_are_not_authorized | KEEP | privacy-relevant root ownership (Go fixture parity)
core_coordinates.rs | unrelated_observations_are_ignored_and_chain_is_kept_verbatim | KEEP | privacy-relevant root ownership (Go fixture parity)
core_coordinates.rs | var_alias_resolves_to_one_namespace | KEEP | privacy-relevant root ownership (Go fixture parity)
core_f2_gaps.rs | row10b_protected_config_with_lone_surrogates_or_invalid_utf8_is_malformed | KEEP | F2 row counterpart; independent oracle
core_f2_gaps.rs | row10b_transcript_bytes_with_invalid_utf8_and_lone_surrogates_are_byte_exact | KEEP | F2 row counterpart; independent oracle
core_f2_gaps.rs | row12_three_hundred_epoch_rewrite_history_retains_appends_and_latest_snapshot | KEEP | F2 row counterpart; independent oracle
core_f2_gaps.rs | row1a_backup_aliased_spellings_of_all_eleven_names_select_structural_rules | KEEP | F2 row counterpart; independent oracle
core_f2_gaps.rs | row1a_non_protected_alias_is_untouched_control | KEEP | F2 row counterpart; independent oracle
core_f2_gaps.rs | row1b_argv_bound_is_thirty_two | KEEP | F2 row counterpart; independent oracle
core_f2_gaps.rs | row1b_env_bound_is_eight_accepted_entries_in_input_order | KEEP | F2 row counterpart; independent oracle
core_f2_gaps.rs | row1b_env_value_grammar_accept_and_reject_vectors | KEEP | F2 row counterpart; independent oracle
core_f2_gaps.rs | row5a_db_only_wal_only_and_unchanged_fingerprints | KEEP | F2 row counterpart; independent oracle
core_f2_gaps.rs | row5a_wal_identity_is_covered_by_admission_when_supplied_as_facts | KEEP | F2 row counterpart; independent oracle
core_f2_gaps.rs | row5b_changed_and_failed_are_closed_local_retries_never_done | KEEP | F2 row counterpart; independent oracle
core_f2_gaps.rs | row7c_codex_sessions_archive_and_zst_share_one_owner_session_and_id | KEEP | F2 row counterpart; independent oracle
core_hint.rs | bad_paths_degrade_to_absent_and_unknown_keys_are_tolerated | KEEP | DESIGN 7.6/10.1 hint contract
core_hint.rs | canaries_fixed_seed | REMOVE | one fixed input of the canary property; no unique kill (mutants K,Q)
core_hint.rs | errors_never_echo_input | STRENGTHEN | keep canary-not-echoed; drop three exact Display-string asserts
core_hint.rs | event_tables | KEEP | DESIGN 7.6/10.1 hint contract
core_hint.rs | golden_launch_line_carries_digest_and_allowlisted_values | KEEP | DESIGN 7.6/10.1 hint contract
core_hint.rs | golden_lines_per_agent | KEEP | DESIGN 7.6/10.1 hint contract
core_hint.rs | hegel_interleavings_and_truncations | KEEP | property, dual-engine per DESIGN 15 gate 6
core_hint.rs | hegel_no_canary_in_any_line | STRENGTHEN | mutant K (launch safe()->true) survives all hint canary tests; put canaries in VALUE_FLAGS values and secret-named values
core_hint.rs | hegel_reader_never_panics_on_arbitrary_bytes | KEEP | property, dual-engine per DESIGN 15 gate 6
core_hint.rs | hegel_size_bound_and_roundtrip | KEEP | property, dual-engine per DESIGN 15 gate 6
core_hint.rs | hook_input_negative_fixtures | KEEP | DESIGN 7.6/10.1 hint contract
core_hint.rs | only_session_end_and_interrupted_flush | KEEP | unique killer of mutant H; DESIGN 7.6 flush rule
core_hint.rs | proptest_interleavings_and_truncations | KEEP | property, dual-engine per DESIGN 15 gate 6
core_hint.rs | proptest_no_canary_in_any_line | STRENGTHEN | same as hegel twin
core_hint.rs | proptest_reader_never_panics_on_arbitrary_bytes | KEEP | property, dual-engine per DESIGN 15 gate 6
core_hint.rs | proptest_size_bound_and_roundtrip | KEEP | property, dual-engine per DESIGN 15 gate 6
core_hint.rs | reader_handles_overlong_fragments_and_lines | KEEP | DESIGN 7.6/10.1 hint contract
core_hint.rs | reader_pending_tail_resume_and_midfile_fragment | KEEP | DESIGN 7.6/10.1 hint contract
core_hint.rs | reader_rejects_and_counts_bad_own_lines | KEEP | DESIGN 7.6/10.1 hint contract
core_hint.rs | reductions_are_deterministic_and_ordered | STRENGTHEN | drop build-twice determinism assert; keep launch-then-paths reduction order
core_hint.rs | schema_is_generated_and_strict | REMOVE | tests schemars/serde attr; drift gate + reader_rejects_and_counts_bad_own_lines kill mutant O
core_hint.rs | subagent_transcript_is_a_separate_hint | KEEP | DESIGN 7.6/10.1 hint contract
core_limits.rs | a6_jitter_windows_hold_for_every_draw | KEEP | DESIGN 6.2/7.3/11.2 boundary or property
core_limits.rs | a6_retry_after_is_clamped_to_one_second_and_fifteen_minutes | KEEP | DESIGN 6.2/7.3/11.2 boundary or property
core_limits.rs | a6_retry_after_is_honoured_and_transient_is_its_own_class | KEEP | DESIGN 6.2/7.3/11.2 boundary or property
core_limits.rs | agent_wire_matches_serde | REMOVE | serde derive vs as_str; key_exact_spelling/key_boundary_shared also kill mutant C
core_limits.rs | auth_pauses_host_without_touching_streak | KEEP | DESIGN 6.2/7.3/11.2 boundary or property
core_limits.rs | backoff_bounds | KEEP | DESIGN 6.2/7.3/11.2 boundary or property
core_limits.rs | budget_boundaries | STRENGTHEN | drop 953 ns and u64::MAX exact-Duration pins (change detector); keep 60/61 s and 8 MiB rows
core_limits.rs | cancel_swallows_only_retryable | KEEP | DESIGN 6.2/7.3/11.2 boundary or property
core_limits.rs | cancelled_keeps_streak_and_never_parks | KEEP | DESIGN 6.2/7.3/11.2 boundary or property
core_limits.rs | current_key_len_matches_current_key_over_generated_inputs | KEEP | DESIGN 6.2/7.3/11.2 boundary or property
core_limits.rs | different_fault_or_success_resets_streak | KEEP | DESIGN 6.2/7.3/11.2 boundary or property
core_limits.rs | every_deterministic_fault_parks_with_its_own_reason | KEEP | DESIGN 6.2/7.3/11.2 boundary or property
core_limits.rs | hegel_admission | KEEP | property, dual-engine per DESIGN 15 gate 6
core_limits.rs | hegel_budget | KEEP | property, dual-engine per DESIGN 15 gate 6
core_limits.rs | hegel_cancel | KEEP | property, dual-engine per DESIGN 15 gate 6
core_limits.rs | hegel_key_monotonic | KEEP | property, dual-engine per DESIGN 15 gate 6
core_limits.rs | key_boundary_session_1024_ok_1025_denied | KEEP | DESIGN 6.2/7.3/11.2 boundary or property
core_limits.rs | key_boundary_shared | KEEP | DESIGN 6.2/7.3/11.2 boundary or property
core_limits.rs | key_exact_spelling | KEEP | DESIGN 6.2/7.3/11.2 boundary or property
core_limits.rs | key_len_measures_utf8_bytes_not_chars | KEEP | DESIGN 6.2/7.3/11.2 boundary or property
core_limits.rs | key_len_saturates | MERGE | into key_boundary_* (1024/1025 duplicated); keep usize::MAX
core_limits.rs | owner_fault_is_distinct_from_source_park | KEEP | DESIGN 6.2/7.3/11.2 boundary or property
core_limits.rs | parks_after_three_failures_spanning_three_hours | KEEP | DESIGN 6.2/7.3/11.2 boundary or property
core_limits.rs | proptest_admission | KEEP | property, dual-engine per DESIGN 15 gate 6
core_limits.rs | proptest_budget | KEEP | property, dual-engine per DESIGN 15 gate 6
core_limits.rs | proptest_cancel | KEEP | property, dual-engine per DESIGN 15 gate 6
core_limits.rs | proptest_key_monotonic | KEEP | property, dual-engine per DESIGN 15 gate 6
core_limits.rs | rewrites_of_one_source_use_one_slot | REMOVE | survives mutant B (dedupe off) because HashSet is idempotent; sources_cap + admission property kill it
core_limits.rs | sources_cap_and_never_evict | KEEP | DESIGN 6.2/7.3/11.2 boundary or property
core_limits.rs | timeout_and_offline_retry_not_cancelled | KEEP | DESIGN 6.2/7.3/11.2 boundary or property
core_locator.rs | ambiguity_is_absorbing_and_order_independent | KEEP | locator matrix / property 7
core_locator.rs | every_kind_but_generated_snapshot_and_every_asset_is_reachable | STRENGTHEN | hand-copied 29-kind list is a change detector; derive from enum/iterator so adding a kind forces a row
core_locator.rs | hegel_property7_locate_total_and_countable | KEEP | property, dual-engine per DESIGN 15 gate 6
core_locator.rs | hegel_property7_result_stable_under_unrelated_facts | KEEP | property, dual-engine per DESIGN 15 gate 6
core_locator.rs | hegel_property7_unmapped_paths_are_withheld | KEEP | property, dual-engine per DESIGN 15 gate 6
core_locator.rs | matrix_rows_match_independent_expectations | KEEP | locator matrix / property 7
core_locator.rs | namespace_and_index_validation | KEEP | locator matrix / property 7
core_locator.rs | namespaces_separate_roots_with_equal_names | KEEP | locator matrix / property 7
core_locator.rs | non_claude_roots_ignore_claude_facts_and_foreign_layouts_map_nothing | KEEP | locator matrix / property 7
core_locator.rs | oversized_ids_become_stable_valid_hashes | KEEP | locator matrix / property 7
core_locator.rs | path_validation_rejects_without_echo | KEEP | locator matrix / property 7
core_locator.rs | proptest_property7_locate_total_and_countable | KEEP | property, dual-engine per DESIGN 15 gate 6
core_locator.rs | proptest_property7_result_stable_under_unrelated_facts | KEEP | property, dual-engine per DESIGN 15 gate 6
core_locator.rs | proptest_property7_unmapped_paths_are_withheld | KEEP | property, dual-engine per DESIGN 15 gate 6
core_locator.rs | withhold_reasons_are_fixed_and_countable | REMOVE | 3-line mapping; property7 total_and_countable kills mutant I
core_manifest.rs | apply_transition_rules | KEEP | wire contract / property 6 / negative fixture
core_manifest.rs | build_errors | KEEP | wire contract / property 6 / negative fixture
core_manifest.rs | checkpoint_negative_fixtures | KEEP | wire contract / property 6 / negative fixture
core_manifest.rs | delta_negative_fixtures | KEEP | wire contract / property 6 / negative fixture
core_manifest.rs | duplicate_key_scanner_covers_all_value_kinds | KEEP | wire contract / property 6 / negative fixture
core_manifest.rs | fold_rejects_broken_chains | KEEP | wire contract / property 6 / negative fixture
core_manifest.rs | golden_checkpoint_delta_and_current | KEEP | wire contract / property 6 / negative fixture
core_manifest.rs | hegel_deltas_from_checkpoint_equal_next_checkpoint | KEEP | property, dual-engine per DESIGN 15 gate 6
core_manifest.rs | hegel_r2_dedup_winner_descriptor | KEEP | property, dual-engine per DESIGN 15 gate 6
core_manifest.rs | hegel_roundtrip_of_arbitrary_valid_manifests | KEEP | property, dual-engine per DESIGN 15 gate 6
core_manifest.rs | history_smoke_and_r3_checkpoint_sizes | REMOVE | 3 seeds of the history property; asserts the generator makes >64 pieces (test of the test); mutant N killed by 3 others
core_manifest.rs | projection_reads_positions_and_tolerates_other_fields | KEEP | wire contract / property 6 / negative fixture
core_manifest.rs | projection_rejects_inconsistent_envelopes | KEEP | wire contract / property 6 / negative fixture
core_manifest.rs | property6_coverage_guard_negative_controls_bite | STRENGTHEN | same: shared guard module, one negative-control test
core_manifest.rs | proptest_deltas_from_checkpoint_equal_next_checkpoint | KEEP | property, dual-engine per DESIGN 15 gate 6
core_manifest.rs | proptest_r2_dedup_winner_descriptor | KEEP | property, dual-engine per DESIGN 15 gate 6
core_manifest.rs | proptest_roundtrip_of_arbitrary_valid_manifests | KEEP | property, dual-engine per DESIGN 15 gate 6
core_manifest.rs | r2_both_orders | REMOVE | two seeds of r2 property; property covers both parities (mutant L)
core_manifest.rs | raw_document_rejections | KEEP | wire contract / property 6 / negative fixture
core_manifest.rs | schema_dispatch_is_typed | KEEP | wire contract / property 6 / negative fixture
core_manifest.rs | schemas_are_derivable_and_object_key_is_canonical | REMOVE | misnamed; one object_key format assert; 22 others kill mutant J (golden pins the key)
core_manifest.rs | timestamp_validation | KEEP | wire contract / property 6 / negative fixture
core_manifest.rs | verify_encodings_reports_missing_and_mismatched | KEEP | wire contract / property 6 / negative fixture
core_mechanics.rs | boundaries_strict_deserialization_and_dotted_wire_tags | MERGE | into core_boundaries; repeats ID/ETag/Sha256 cases, keeps SourceKind tag + FileId deny_unknown; mutant M killed by 2 others
core_mechanics.rs | coalescer_config_wake_and_backwards_are_explicit | KEEP | property 1/2 or boundary
core_mechanics.rs | hegel_property1_independent_capture_and_eligible_audit | KEEP | property, dual-engine per DESIGN 15 gate 6
core_mechanics.rs | hegel_property2_monotonic_quiet_cap_end | KEEP | property, dual-engine per DESIGN 15 gate 6
core_mechanics.rs | immediate_session_end_and_independent_owner_monotonic_clocks | KEEP | property 1/2 or boundary
core_mechanics.rs | owner_wire_goldens_and_schema_use_session_id_and_distinct_shared_coordinates | STRENGTHEN | keep wire goldens + rejects; drop /oneOf/1 schema pointer introspection
core_mechanics.rs | property1_coverage_guard_negative_controls_bite | STRENGTHEN | guard helper copy-pasted per file; hoist to shared tests/common and test once
core_mechanics.rs | property1_negative_control_disabled_window_check_must_fail | REMOVE | self-contained should_panic on test-local mutant; passes under real window-off mutant D; check-window-mutation.py is the real control
core_mechanics.rs | proptest_property1_independent_capture_and_eligible_audit | KEEP | property, dual-engine per DESIGN 15 gate 6
core_mechanics.rs | proptest_property2_monotonic_quiet_cap_end | KEEP | property, dual-engine per DESIGN 15 gate 6
core_planner.rs | adoption_requires_full_digest_then_can_extend_without_new_epoch | KEEP | decision table; kills window mutant D/P
core_planner.rs | committed_deletion_repeat_has_no_capture_work | KEEP | decision table; kills window mutant D/P
core_planner.rs | every_append_table_branch_and_independent_evidence_request | KEEP | decision table; kills window mutant D/P
core_planner.rs | full_audit_is_explicit_and_pinned_and_rules_are_not_writer_version | KEEP | decision table; kills window mutant D/P
core_planner.rs | settled_same_size_changed_mtime_requires_windows_and_refreshes_stat | KEEP | decision table; kills window mutant D/P
core_planner.rs | snapshot_audit_compares_transformed_bytes_even_when_fingerprint_is_stable | KEEP | decision table; kills window mutant D/P
core_planner.rs | sqlite_db_and_wal_and_absence_are_each_part_of_snapshot_fingerprint | KEEP | unique killer of mutant P (snapshot rules change) beside coordinates flake
core_planner.rs | window_ranges_clamp_at_zero_and_old_committed_endpoint_without_overflow | KEEP | decision table; kills window mutant D/P
core_verify.rs | a6_known_ours_late_write_is_adopted_as_already_ours | KEEP | pins DESIGN 7.4 step 5/A6 classification order
core_verify.rs | first_write_has_no_base_so_previous_hash_is_foreign | KEEP | pins DESIGN 7.4 step 5/A6 classification order
core_verify.rs | known_ours_wins_over_base | KEEP | pins DESIGN 7.4 step 5/A6 classification order
core_verify.rs | newer_schema_manifest_parks_instead_of_foreign | KEEP | pins DESIGN 7.4 step 5/A6 classification order
core_verify.rs | ours_wins_over_known_and_base | KEEP | pins DESIGN 7.4 step 5/A6 classification order
core_verify.rs | own_manifest_is_already_ours | KEEP | pins DESIGN 7.4 step 5/A6 classification order
core_verify.rs | previous_is_base_unchanged | KEEP | pins DESIGN 7.4 step 5/A6 classification order
core_verify.rs | unknown_hash_is_foreign_after_one_probe | KEEP | pins DESIGN 7.4 step 5/A6 classification order
src/privacy/names.rs.rs | static_patterns_compile | KEEP | 
parent_privacy_review.rs | escaped_duplicate_config_keys_fail_closed_and_launch_prompt_is_dropped | KEEP | named parent-review regression
parent_privacy_review.rs | intermediate_credential_and_unknown_root_are_not_admitted | KEEP | named parent-review regression
parent_privacy_review.rs | malformed_selected_url_does_not_preserve_invalid_escape_literal | MERGE | into privacy_coverage malformed_url_forms (mutant G killed there too)
parent_privacy_review.rs | malformed_selected_url_does_not_preserve_invalid_port_literal | REMOVE | add the port case to privacy_coverage malformed_url_forms; mutants F,G not killed by it
parent_privacy_review.rs | scrubbed_toml_retains_unrelated_datetime_type | MERGE | into privacy_coverage toml_nested_arrays (datetime already there)
parent_privacy_review.rs | transformed_capability_cannot_be_rebound_to_a_protected_source | KEEP | named parent-review regression
privacy_coverage.rs | absent_rules_only_withhold_protected_profiles | KEEP | adversarial privacy fixture
privacy_coverage.rs | args_odd_shapes_and_header_references_keep_go_known_file_semantics | KEEP | adversarial privacy fixture
privacy_coverage.rs | collector_ancestor_alias_and_other_known_profile | KEEP | adversarial privacy fixture
privacy_coverage.rs | fish_nonassignments_preserve_exact_bytes_as_in_go_text_fixtures | KEEP | adversarial privacy fixture
privacy_coverage.rs | fish_unfinished_values_and_non_utf8_text_match_go_boundary | KEEP | adversarial privacy fixture
privacy_coverage.rs | launch_empty_unknown_missing_and_duplicate_metadata | KEEP | adversarial privacy fixture
privacy_coverage.rs | malformed_url_forms_never_escape_as_literals | KEEP | adversarial privacy fixture
privacy_coverage.rs | missing_observations_have_distinct_closed_outcomes | KEEP | adversarial privacy fixture
privacy_coverage.rs | protected_depth_and_conservative_expansion_fail_closed | KEEP | adversarial privacy fixture
privacy_coverage.rs | selected_nested_shapes_redact_strings_not_json_primitives | KEEP | adversarial privacy fixture
privacy_coverage.rs | toml_nested_arrays_preserve_native_values_when_strings_change | KEEP | adversarial privacy fixture
privacy_coverage.rs | url_scrubbing_preserves_safe_spelling_and_reference_query_components | KEEP | adversarial privacy fixture
privacy_port.rs | all_eleven_names_all_agents_aliases_and_whole_document_digest | MERGE | into f2_gaps row1a (add Profile::TextAsset); row1a kills mutant E too
privacy_port.rs | capability_binding_rejects_cross_source_cross_profile_and_changed_policy | KEEP | privacy parity vector (fail-closed)
privacy_port.rs | codex_and_claude_all_rule_families_including_args_and_urls | KEEP | privacy parity vector (fail-closed)
privacy_port.rs | denied_identities_backups_collector_aliases_and_unknowns | KEEP | privacy parity vector (fail-closed)
privacy_port.rs | escaped_copies_nested_arrays_and_keys_are_not_lost | KEEP | privacy parity vector (fail-closed)
privacy_port.rs | generated_equivalent_copies_and_safe_negative_values | KEEP | privacy parity vector (fail-closed)
privacy_port.rs | native_launch_drops_prompt_secret_flags_and_nonexact_env | MERGE | into privacy_coverage launch test; mutant Q killed by 9 others
privacy_port.rs | rejects_malformed_duplicates_unicode_trailing_and_nonobjects | KEEP | privacy parity vector (fail-closed)
privacy_port.rs | rule_digest_golden_requires_explicit_semantic_review | KEEP | privacy parity vector (fail-closed)
privacy_port.rs | rules_union_only_encountered_identities_and_supported_reference_syntax | KEEP | privacy parity vector (fail-closed)
privacy_port.rs | text_guard_shell_fish_quotes_separators_and_negative_controls | KEEP | privacy parity vector (fail-closed)
privacy_port.rs | toml_datetime_types_and_url_parser_parity | KEEP | privacy parity vector (fail-closed)
privacy_port.rs | unrelated_json_numeric_lexemes_survive_rewrite | KEEP | privacy parity vector (fail-closed)
privacy_port.rs | url_rewrite_preserves_safe_spelling_not_whatwg_normalization | REMOVE | subset of privacy_coverage url_scrubbing_preserves_safe_spelling table
privacy_property.rs | hegel_generated_canaries_do_not_escape_pure_privacy_outputs | STRENGTHEN | no proptest cross-check (DESIGN 15 gate 6); only the canary string varies, structure is fixed
privacy_property.rs | independent_inventory_exercises_every_family_and_credential_variant | KEEP | property 5 canary inventory
scaffold.rs | checked_ranges_cover_edges | REMOVE | asserts std slice::get; consumer covered by core_hint reader_pending_tail (mutant A)
scaffold.rs | hegel_checked_ranges_match_independent_iterator_oracle | REMOVE | property whose oracle re-implements slice::get; tests std
scaffold.rs | proptest_checked_ranges_match_independent_iterator_oracle | REMOVE | same as hegel twin; also delete slice::bytes and inline .get()