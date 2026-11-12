# W2: flake rates of Hegel and proptest properties (unmodified working tree, debug profile)

Method: 55 properties (29 hegel_, 26 proptest_) in 15 test binaries, copy of the working tree rust/ in W2/ws. Each property run as its own process (`--exact`), cwd = crate dir, with distinct random seeds (HEGEL_SEED = PROPTEST_RNG_SEED = seed, HEGEL_PRINT_BLOB=true, as gates.sh) at (default) env case vars unset and (500) HEGEL_TEST_CASES=PROPTEST_CASES=500. N=100 where fast, otherwise >=20 (some stopped short of 100 after ~10 min). 3 processes in parallel. Raw per-run JSONL (seed, ok, secs, panic tail on failure) in W2/logs/; runner W2/run.py, analysis W2/analyze.py, failure dump W2/fails.py.

## Headline
- No real counterexample, deadline, or health-check failure in any of ~9,000 runs. Every failure was a coverage-guard miss.
- Only 6 properties ever failed: the two daemon state machines, the two property4 hegel state machines (capture, commit), and hegel coordinates. proptest property4 (capture/commit) and proptest coordinates: 0 failures.
- At 500 cases only `proptest_daemon_state_machine` failed (5/59 = 8.5%, Wilson 3.7%-18.4%), always the `rewrite-mid-file` guard. Seed 98595426 reproduced the same guard miss twice (deterministic: identical shape counts).
- P(one full gate run fails by chance at 500) = 1 - (1-5/59) ~= 8.5% (Wilson 3.7%-18.4% from that property alone). All other 54 properties: 0 failures at 500, each with an upper 95% bound of 3.7%-16% depending on N (so the true total could be somewhat higher; rule-of-three sum of the other uppers is a loose ceiling, not an estimate).
- P(plain default `cargo nextest run` fails by chance) = 1 - product over properties ~= 62.5% (point estimate, default settings; includes all 6 failing properties).

## Per-property table (failure rate with 95% Wilson interval; median seconds per run)
| binary | property | default fail/n (rate, 95% Wilson) | med s | 500 fail/n (rate, 95% Wilson) | med s | failure causes |
|---|---|---|---|---|---|---|
| capture_property | hegel_property4_capture_state_machine | 2/100 (2.0%, 0.6%-7.0%) | 3.5 | 0/33 (0.0%, 0.0%-10.4%) | 18.3 | default: guard:lost-reply x1; default: guard:crash-after-manifest x1 |
| capture_property | proptest_property4_capture_state_machine | 0/72 (0.0%, 0.0%-5.1%) | 7.8 | 0/38 (0.0%, 0.0%-9.2%) | 14.1 |  |
| commit_protocol | hegel_property4_commit_state_machine | 6/100 (6.0%, 2.8%-12.5%) | 2.7 | 0/43 (0.0%, 0.0%-8.2%) | 13.3 | default: guard:lost-reply x4; default: guard:crash-after-manifest x2 |
| commit_protocol | proptest_property4_commit_state_machine | 0/90 (0.0%, 0.0%-4.1%) | 6.3 | 0/46 (0.0%, -0.0%-7.7%) | 12.9 |  |
| core_coordinates | hegel_coordinates_order_proof_and_retention | 23/100 (23.0%, 15.8%-32.2%) | 0.1 | 0/100 (0.0%, 0.0%-3.7%) | 0.4 | default: OTHER:generator never produced proof-only x21; default: OTHER:generator never produced resolved x2 |
| core_coordinates | proptest_coordinates_order_proof_and_retention | 0/100 (0.0%, 0.0%-3.7%) | 0.1 | 0/100 (0.0%, 0.0%-3.7%) | 0.1 |  |
| core_hint | hegel_interleavings_and_truncations | 0/100 (0.0%, 0.0%-3.7%) | 2.6 | 0/36 (0.0%, 0.0%-9.6%) | 15.6 |  |
| core_hint | hegel_no_canary_in_any_line | 0/100 (0.0%, 0.0%-3.7%) | 2.8 | 0/36 (0.0%, 0.0%-9.6%) | 16.1 |  |
| core_hint | hegel_reader_never_panics_on_arbitrary_bytes | 0/100 (0.0%, 0.0%-3.7%) | 0.0 | 0/100 (0.0%, 0.0%-3.7%) | 0.1 |  |
| core_hint | hegel_size_bound_and_roundtrip | 0/100 (0.0%, 0.0%-3.7%) | 3.5 | 0/34 (0.0%, 0.0%-10.2%) | 17.5 |  |
| core_hint | proptest_interleavings_and_truncations | 0/35 (0.0%, 0.0%-9.9%) | 17.3 | 0/20 (0.0%, -0.0%-16.1%) | 35.0 |  |
| core_hint | proptest_no_canary_in_any_line | 0/64 (0.0%, 0.0%-5.7%) | 8.7 | 0/36 (0.0%, 0.0%-9.6%) | 16.0 |  |
| core_hint | proptest_reader_never_panics_on_arbitrary_bytes | 0/100 (0.0%, 0.0%-3.7%) | 0.7 | 0/100 (0.0%, 0.0%-3.7%) | 1.1 |  |
| core_hint | proptest_size_bound_and_roundtrip | 0/20 (0.0%, -0.0%-16.1%) | 28.9 | 0/20 (0.0%, -0.0%-16.1%) | 46.8 |  |
| core_limits | hegel_admission | 0/100 (0.0%, 0.0%-3.7%) | 1.6 | 0/100 (0.0%, 0.0%-3.7%) | 5.5 |  |
| core_limits | hegel_budget | 0/100 (0.0%, 0.0%-3.7%) | 0.0 | 0/100 (0.0%, 0.0%-3.7%) | 0.1 |  |
| core_limits | hegel_cancel | 0/100 (0.0%, 0.0%-3.7%) | 0.0 | 0/100 (0.0%, 0.0%-3.7%) | 0.1 |  |
| core_limits | hegel_key_monotonic | 0/100 (0.0%, 0.0%-3.7%) | 0.0 | 0/100 (0.0%, 0.0%-3.7%) | 0.1 |  |
| core_limits | proptest_admission | 0/100 (0.0%, 0.0%-3.7%) | 2.8 | 0/78 (0.0%, 0.0%-4.7%) | 5.8 |  |
| core_limits | proptest_budget | 0/100 (0.0%, 0.0%-3.7%) | 0.0 | 0/100 (0.0%, 0.0%-3.7%) | 0.0 |  |
| core_limits | proptest_cancel | 0/100 (0.0%, 0.0%-3.7%) | 0.0 | 0/100 (0.0%, 0.0%-3.7%) | 0.0 |  |
| core_limits | proptest_key_monotonic | 0/100 (0.0%, 0.0%-3.7%) | 0.0 | 0/100 (0.0%, 0.0%-3.7%) | 0.0 |  |
| core_locator | hegel_property7_locate_total_and_countable | 0/100 (0.0%, 0.0%-3.7%) | 0.0 | 0/100 (0.0%, 0.0%-3.7%) | 0.1 |  |
| core_locator | hegel_property7_result_stable_under_unrelated_facts | 0/100 (0.0%, 0.0%-3.7%) | 0.0 | 0/100 (0.0%, 0.0%-3.7%) | 0.1 |  |
| core_locator | hegel_property7_unmapped_paths_are_withheld | 0/100 (0.0%, 0.0%-3.7%) | 0.0 | 0/100 (0.0%, 0.0%-3.7%) | 0.2 |  |
| core_locator | proptest_property7_locate_total_and_countable | 0/100 (0.0%, 0.0%-3.7%) | 0.0 | 0/100 (0.0%, 0.0%-3.7%) | 0.1 |  |
| core_locator | proptest_property7_result_stable_under_unrelated_facts | 0/100 (0.0%, 0.0%-3.7%) | 0.0 | 0/100 (0.0%, 0.0%-3.7%) | 0.1 |  |
| core_locator | proptest_property7_unmapped_paths_are_withheld | 0/100 (0.0%, 0.0%-3.7%) | 0.1 | 0/100 (0.0%, 0.0%-3.7%) | 0.1 |  |
| core_manifest | hegel_deltas_from_checkpoint_equal_next_checkpoint | 0/20 (0.0%, -0.0%-16.1%) | 29.5 | 0/20 (0.0%, -0.0%-16.1%) | 182.5 |  |
| core_manifest | hegel_r2_dedup_winner_descriptor | 0/100 (0.0%, 0.0%-3.7%) | 0.1 | 0/100 (0.0%, 0.0%-3.7%) | 0.8 |  |
| core_manifest | hegel_roundtrip_of_arbitrary_valid_manifests | 0/100 (0.0%, 0.0%-3.7%) | 1.5 | 0/78 (0.0%, 0.0%-4.7%) | 7.1 |  |
| core_manifest | proptest_deltas_from_checkpoint_equal_next_checkpoint | 0/20 (0.0%, -0.0%-16.1%) | 92.8 | 0/20 (0.0%, -0.0%-16.1%) | 181.9 |  |
| core_manifest | proptest_r2_dedup_winner_descriptor | 0/100 (0.0%, 0.0%-3.7%) | 0.3 | 0/100 (0.0%, 0.0%-3.7%) | 0.6 |  |
| core_manifest | proptest_roundtrip_of_arbitrary_valid_manifests | 0/100 (0.0%, 0.0%-3.7%) | 5.4 | 0/59 (0.0%, 0.0%-6.1%) | 9.1 |  |
| core_mechanics | hegel_property1_independent_capture_and_eligible_audit | 0/20 (0.0%, -0.0%-16.1%) | 31.8 | 0/20 (0.0%, -0.0%-16.1%) | 67.2 |  |
| core_mechanics | hegel_property2_monotonic_quiet_cap_end | 0/100 (0.0%, 0.0%-3.7%) | 0.1 | 0/100 (0.0%, 0.0%-3.7%) | 0.4 |  |
| core_mechanics | proptest_property1_independent_capture_and_eligible_audit | 0/20 (0.0%, -0.0%-16.1%) | 31.9 | 0/20 (0.0%, -0.0%-16.1%) | 57.1 |  |
| core_mechanics | proptest_property2_monotonic_quiet_cap_end | 0/100 (0.0%, 0.0%-3.7%) | 0.1 | 0/100 (0.0%, 0.0%-3.7%) | 0.1 |  |
| counters | hegel_converged_reconcile_makes_zero_puts | 0/100 (0.0%, 0.0%-3.7%) | 1.0 | 0/68 (0.0%, 0.0%-5.3%) | 9.9 |  |
| counters | hegel_delta_capture_matches_independent_whole_digest | 0/100 (0.0%, 0.0%-3.7%) | 1.8 | 0/71 (0.0%, 0.0%-5.1%) | 9.3 |  |
| daemon | hegel_daemon_state_machine | 34/100 (34.0%, 25.5%-43.7%) | 2.8 | 0/54 (0.0%, 0.0%-6.6%) | 10.1 | default: guard:revoke x1; default: guard:lost-reply, rewrite-mid-file x1; default: guard:rewrite-mid-file x28; default: guard:multi-epoch x1; default: guard:remove-source x2; default: guard:lost-reply x1 |
| daemon | proptest_daemon_state_machine | 20/100 (20.0%, 13.3%-28.9%) | 3.5 | 5/59 (8.5%, 3.7%-18.4%) | 7.3 | default: guard:rewrite-mid-file x20; 500: guard:rewrite-mid-file x5 |
| hook | hegel_core_never_panics_and_writes_at_most_one_whole_line | 0/100 (0.0%, 0.0%-3.7%) | 0.3 | 0/100 (0.0%, 0.0%-3.7%) | 1.9 |  |
| hook | proptest_core_never_panics_and_writes_at_most_one_whole_line | 0/100 (0.0%, 0.0%-3.7%) | 4.0 | 0/93 (0.0%, 0.0%-4.0%) | 6.4 |  |
| privacy_property | hegel_generated_canaries_do_not_escape_pure_privacy_outputs | 0/100 (0.0%, 0.0%-3.7%) | 0.2 | 0/100 (0.0%, 0.0%-3.7%) | 1.5 |  |
| realfs | hegel_read_equals_range_intersection | 0/100 (0.0%, 0.0%-3.7%) | 0.1 | 0/100 (0.0%, 0.0%-3.7%) | 0.3 |  |
| realfs | proptest_read_equals_range_intersection | 0/100 (0.0%, 0.0%-3.7%) | 0.1 | 0/100 (0.0%, 0.0%-3.7%) | 0.5 |  |
| state | hegel_crash_between_stage_and_publish_reads_old_or_new | 0/100 (0.0%, 0.0%-3.7%) | 1.3 | 0/60 (0.0%, -0.0%-6.0%) | 9.4 |  |
| state | hegel_single_byte_mutation_never_yields_a_different_body | 0/100 (0.0%, 0.0%-3.7%) | 0.0 | 0/100 (0.0%, 0.0%-3.7%) | 0.1 |  |
| state | hegel_write_read_is_identity | 0/100 (0.0%, 0.0%-3.7%) | 1.0 | 0/81 (0.0%, -0.0%-4.5%) | 6.5 |  |
| state | proptest_crash_between_stage_and_publish_reads_old_or_new | 0/83 (0.0%, 0.0%-4.4%) | 6.5 | 0/51 (0.0%, 0.0%-7.0%) | 12.1 |  |
| state | proptest_single_byte_mutation_never_yields_a_different_body | 0/100 (0.0%, 0.0%-3.7%) | 0.4 | 0/100 (0.0%, 0.0%-3.7%) | 0.9 |  |
| state | proptest_write_read_is_identity | 0/100 (0.0%, 0.0%-3.7%) | 3.7 | 0/100 (0.0%, 0.0%-3.7%) | 3.8 |  |
| watch | hegel_scheduler_matches_model | 0/100 (0.0%, 0.0%-3.7%) | 0.0 | 0/100 (0.0%, 0.0%-3.7%) | 0.1 |  |
| watch | proptest_scheduler_matches_model | 0/100 (0.0%, 0.0%-3.7%) | 0.1 | 0/100 (0.0%, 0.0%-3.7%) | 0.2 |  |

P(gate all-pass at 500, point est) = 0.9153; chance fail = 0.0847
P(plain run all-pass at default, point est) = 0.3745; chance fail = 0.6255

## Guard shapes responsible
- `daemon::proptest_daemon_state_machine`: `rewrite-mid-file` (guard at crates/memory/tests/daemon.rs ~line 1369, counted only when a Rewrite step has `after.mid` and `after.effect`). 20/100 default, 5/59 at 500. All 25 failures: count 0 of that shape while other shapes counted 10s-100s.
- `daemon::hegel_daemon_state_machine` at default: `rewrite-mid-file` x28, plus single misses of revoke, multi-epoch, remove-source, lost-reply (1-2 each). 0/54 at 500 (upper bound 6.6%), so the 500 setting is probably but not certainly safe for hegel.
- `commit_protocol::hegel_property4_commit_state_machine` default: `lost-reply` x4, `crash-after-manifest` x2. `capture_property::hegel_property4_capture_state_machine` default: `lost-reply` x1, `crash-after-manifest` x1. 0 failures at 500 (N=43 and 33, upper bounds 8-10%).
- `core_coordinates::hegel_coordinates_order_proof_and_retention` default: `generator never produced proof-only` x21, `resolved` x2 (core_coordinates.rs Stats::guard, asserted once per process over all cases, with a process-global atomic). 0/100 at 500.

Note the guards are all "whole-run aggregates" already (not per case), so re-expressing them as aggregates will not help; the problem is generators that produce the rare shape too seldom per run. proptest at its default (256 cases) is much safer than hegel at its default (100 cases) for the property4 pair and coordinates: proptest default 0 failures.

## Recommendations
1. Daemon `rewrite-mid-file`: this single shape dominates gate risk (about 8.5% per gate run, 20-34% at default). Fix by making the generator reach it deterministically: e.g. a mandatory seed step/prefix in the model (always include one mid-file Rewrite in a sequence, or bias Step::Rewrite so `mid` is true with high probability), or move the shape to a separate deterministic test (one scripted mid-file rewrite through the oracle) and drop it from the probabilistic guard. Expected gate failure after fix: about 0 from this source.
2. Same treatment for `lost-reply` and `crash-after-manifest` (hegel property4 capture/commit, and daemon hegel): add one forced fault-injection step, or a deterministic companion test; then the guard can stay as a sanity check.
3. Coordinates: `proof-only` is missing 21% of hegel default runs; make a deterministic unit test for proof-only / resolved, or seed one example (hegel `#[hegel::explicit_test_case]`-style, or construct one in the test), and keep the guard as a backstop only.
4. Default case counts: plain runs are unreliable (62.5% chance one of the guarded properties fails). Setting both libraries to 500 by default (e.g. a `.config/nextest.toml` env or `ProptestConfig::with_cases` / Hegel settings in the test code, and not only in gates.sh) would drop the hegel failures to 0/54..0/100 observed, but not proptest daemon (8.5%). So case count alone is not sufficient; fix recommendation 1 first. With 1-3 done, the defaults can stay as they are for speed.
5. If the generators stay as is, 500 is not a reliable gate for the proptest daemon property: an estimated ~1000-2000 cases would be needed (miss rate fell from 20% at 256 to 8.5% at 500; extrapolating geometrically ~0.7% at 1000 and ~0.06% at 1500 is a rough guess, not measured). The documented 2,000-case local runs are consistent with that.

## Caveats
- Runs were in debug profile test binaries built from the working tree (uncommitted test changes included). The gate's nextest runs the same binaries.
- Zero-failure properties with N=20 have wide intervals (upper 16%); the slow ones (core_manifest deltas, core_mechanics property1, core_hint proptest_size_bound) could hide a ~5% flake.
- Hegel default is 100 cases and proptest default 256 unless overridden in code; the actual defaults used are whatever the test binaries use with the env vars unset.
