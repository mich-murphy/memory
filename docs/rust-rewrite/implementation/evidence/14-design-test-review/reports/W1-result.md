# W1 result

Diff: 16 files, +97/-557 lines (working tree, nothing committed). Tests: 392 listed (390 run + 2 ignored p4) -> 358 run + 2 ignored.

## 1. Schema gate
rust/crates/xtask/tests/cli.rs now runs only `schemas --check` plus the three failure paths (renamed schema_cli_check_dispatches_and_rejects_unknown_commands). Proof in scratch copy (git archive HEAD + new cli.rs, schemas/range-error.json set to `{}`): cli test FAILED, file still `{}`, `cargo xtask schemas --check` exit=1. Write-mode arm `schemas(false)` of `run()` is now only covered by the in-src test via schemas_at, not via CLI dispatch (src is not mine; an output-dir option would be needed to test it).

## 2/3. REMOVE and MERGE verdicts
Removed: core_hint schema_is_generated_and_strict; core_limits agent_wire_matches_serde, rewrites_of_one_source_use_one_slot; core_locator withhold_reasons_are_fixed_and_countable; core_manifest history_smoke_and_r3_checkpoint_sizes (plus dead Stat/history plumbing), r2_both_orders, schemas_are_derivable_and_object_key_is_canonical; core_mechanics property1_negative_control_disabled_window_check_must_fail; scaffold.rs (3 tests); commit_protocol directed_foreign_writer_parks..., directed_lost_reply_is_already_ours..., directed_lost_reply_then_offline..., directed_offline_and_revoked..., guard_shapes_arise_from_scripted_runs, parse_current_roundtrips..., r2_without_adoption...; daemon first_round_commits_and_persists...; capture captured_pieces_are_stored_under_object_keys; hook second_hook_appends_second_line; watch quiet_expiry, session_end_flushes_at_once, stop_stays_debounced.
Merged (all assertions kept): key_len_saturates -> key_boundary_session_1024_ok_1025_denied; parent_privacy_review x3 -> privacy_coverage (added two URL cases, stamp datetime assertions); base_unchanged_exhaustion_is_transient_not_throttled -> base_unchanged_retries (class Transient asserted); realfs read_errors_carry_the_io_kind + unreadable_file_is_permission_denied_on_read -> read_exact_short_and_past_eof; walk_entry_bound_stops_partway -> walk_depth_and_entry_bounds (defaults assertion kept); hook session_start_without_launch_evidence_has_none -> launch_evidence_only_on_session_start.
Not removed: canaries_fixed_seed (privacy canary); explicit_alias_pair_is_accepted (hand argument only); url_rewrite_preserves_safe_spelling... (privacy, hand-argued subset); harness.rs scaffold_binary_refuses_capture (it also pins "never a simulated success"; not mutant-verified); other_subcommands_keep_explicit_failure (by reading only); hook binary_refuses_writable_dir_without_file (security assertion); held: error_text_is_closed, error_display_is_closed, result_codes_are_fixed_and_distinct; ignored p4_core_latency, p4_spawn_latency.
Not merged (no clean way to keep every assertion, or privacy): boundaries_strict_deserialization..., privacy_port all_eleven and native_launch, commit_protocol committed_then_5xx / etag_churn / both guard_has_negative_controls (+daemon copy), daemon missing_hints_dir / prepare_sweeps / state_api_failure_table / unwritable_* x2, capture snapshot_change / withheld_source / snapshot_read_failures / adopt_prefix_read_failures, hook binary_appends modes, resolve shared_owner third block.

## 4. Constant as oracle
base_unchanged_retries_exactly_four_times: literals 5 PUTs, 4 pauses, cites DESIGN.md:619. GET_ATTEMPTS left: DESIGN.md states no number for the verification GET (report's "DESIGN 7.4 says 4" refers to REBASE only); the coordinator must decide a value.

## 5. Gates 05/06
Now `cargo test --workspace --locked hegel_ ...` and `... proptest_ ...` (61 and 58 tests ok in the logs). Docs to update: docs/quality-gates.md line ~35 and 41 mention "scaffold/property pass" and "scaffold's first measurement" (historical, probably fine); rust/README.md:38 same; neither describes steps 05/06 by scaffold name. slice::bytes in src is now only used by hint.rs (audit suggests inlining; src not mine).

## Verification
cargo fmt --check ok; clippy --workspace --all-targets --locked -Dwarnings ok; nextest 500 cases: 358 passed, 2 skipped; gates.sh with PROPERTY_CASES=500 (seed 3352165921): all 13 subgates 0, exit 0. Coverage identical before/after: 5595/5766 = 97.03% (floor 80). Per-binary counts (before->after): core_hint 22->21, core_limits 30->27, core_locator 15->14, core_manifest 23->20, core_mechanics 10->9, parent_privacy_review 6->3, scaffold 3->0, capture 16->15, commit_protocol 48->40, daemon 30->29, hook 26->24, realfs 15->12, watch 16->13, xtask cli 1->1.

## STRENGTHEN list (not done)
Per the three reports' tables (e.g. hint canary K mutant, cap-burst generator, commit_protocol C4/C7/C10/D8/D13 survivors, snapshot exact outcome, fsync seam, GET_ATTEMPTS literal once DESIGN fixes a value).
