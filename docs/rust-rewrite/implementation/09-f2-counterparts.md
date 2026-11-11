# 09 F2 counterparts and slice-1 coverage guard

Worker: slice-1 acceptance (guard). Owned files: `core_mechanics.rs`, `core_manifest.rs` (counters only), this report. No `core_coverage_guard.rs` was created (see Limitations). Nothing was committed or staged.

## Evidence rules

- "Exists" means verified by grep of test names in `rust/crates/memory-core/tests/`.
- Every cited test was opened and its assertions read against acceptance-map section 1 (verifier pass, see "Verification method"). Status cells cite `file:line` and what is asserted. No citation is left unread. A status of `implemented-S1` means the S1 acceptance in the map is asserted; `partial-S1` lists what is missing.

## Go fix -> Rust counterpart table (acceptance map section 1, compound rows split)

| F2 | Go fix evidence | Rust counterpart (exists now) | Status |
|---|---|---|---|
| 1a config scrub | `internal/scrub/scrub.go`, `crossconfig_test.go` | `privacy_port::all_eleven_names_all_agents_aliases_and_whole_document_digest`, `privacy_port::rejects_malformed_duplicates_unicode_trailing_and_nonobjects`, `privacy_port::rules_union_only_encountered_identities_and_supported_reference_syntax` | **implemented-S1 (round 4).** History: partial-S1 (verified: privacy_port.rs:52 runs all 11 basenames under claude/codex/pi identities and asserts no canary, digest = SHA-256 of published bytes and differs from the raw digest; :154 asserts nine JSON and three TOML malformed, duplicate, escaped-duplicate, lone-surrogate, invalid-UTF-8, trailing and non-object inputs fail with `MalformedProtected`/"malformed_protected"; :188 asserts the union of only the encountered identities and `$VAR`/`${VAR}`/`!cmd`/env-name reference handling. Missing: backup-aliased spellings of each of the 11 basenames per agent are not asserted here, only one `models.json.bak.old~` alias at :188). **Round 4: implemented-S1**: `core_f2_gaps.rs:125` runs 11 basenames x 19 backup suffixes x 2 case spellings x 3 agents x 2 profiles (Config, OtherKnown); every spelling scrubs (no canary, bytes changed) and a malformed document fails `MalformedProtected`. Backup spellings of `.claude.json` (not the exact primary) are `Deny(Credential)` by `names::is_credential`, asserted with literal counts. Control `:177` shows non-protected near-misses are untouched. |
| 1b copies / env / argv | `scrub.go`, `copies.go`, `launch/native.go` | `privacy_port::escaped_copies_nested_arrays_and_keys_are_not_lost`, `privacy_port::native_launch_drops_prompt_secret_flags_and_nonexact_env`, `core_hint::golden_launch_line_carries_digest_and_allowlisted_values`, `core_hint::hegel_no_canary_in_any_line` | **implemented-S1 (round 4).** History: partial-S1 (verified: privacy_port.rs:124 asserts escaped/newline copies in nested arrays are `[redacted]` while object keys are kept; :477 asserts no canary in the sanitized launch, `--model` value and `ANTHROPIC_MODEL` kept, argv <= 32; core_hint.rs:96 asserts the persisted hint line has the prompt as `[redacted]`, allowlisted env, matching digest and no prompt bytes; core_hint.rs:831/881 `check_canaries` asserts no canary or its lowercase in any built line from argv, env or hook fields, with a positive control. Missing: the 8-env bound and exact env value grammar are not asserted in the tests read). The hook that sanitizes before persistence is S2. **Round 4: implemented-S1**: the 8-env figure is correct (`privacy/launch.rs` `env.len() == 8`, Go `native.go:58`; 9 names are allowlisted so it is observable). `core_f2_gaps.rs:210` asserts the bound under all 9 rotations (first 8 accepted entries kept), that rejected/malformed/unknown entries do not consume budget, repeated keys, and the ninth is dropped. `:248` asserts the value grammar `^[A-Za-z0-9][A-Za-z0-9._:/@+-]{0,255}$` plus the secret-name guard with 11 accept and 23 reject vectors (256 bytes accepted, 257 rejected). `:304` asserts the argv bound of 32. |
| 1c credentials / backups / aliases | `capturepolicy/*` | `privacy_port::denied_identities_backups_collector_aliases_and_unknowns`, `privacy_coverage::collector_ancestor_alias_and_other_known_profile`, `parent_privacy_review::intermediate_credential_and_unknown_root_are_not_admitted` | implemented-S1 (verified: privacy_port.rs:263 asserts 20 credential names x 5 backup suffixes, upper-cased, are `Deny(Credential)`, credential directories, non-credential near-misses, collector paths and ancestor identity alias deny, unknown kind denies, unresolved path/rules give `Unknown`; privacy_coverage.rs:91 asserts collector identity/prefix deny for `OtherKnown`; parent_privacy_review.rs:109 asserts an intermediate `auth.json` alias denies and an unresolved root is not admitted). Safe resolution (cycles, >255 links) and open are later slice 2. |
| 2 key bytes | `publicationr2/backend.go:78` | `src/limits.rs` `current_key`/`current_key_len`/`check_key_len`; `core_limits::key_exact_spelling`, `key_boundary_session_1024_ok_1025_denied`, `key_boundary_shared`, `key_len_measures_utf8_bytes_not_chars`, `key_len_saturates`, Hegel/proptest `key_monotonic` | implemented-S1 (parent-verified: exact 1,024 admitted / 1,025 `KeyTooLong` for both owner variants; multibyte measured on raw segments because domain ids are ASCII-only). No upload/count path until slice 2. |
| 3 size-scaled timeout | `publicationr2/budget.go` | `src/limits.rs` `request_budget`; `core_limits::budget_boundaries`, Hegel/proptest `budget` (monotonic, floor 60 s) | implemented-S1 (parent-verified: 0, median, 8 MiB = 60 s, `u64::MAX` saturates). Transport enforcement is slice 2. |
| 4a 1,024 source cap | `liveness_test.go` | `src/limits.rs` `SourceSet::admit`; `core_limits::sources_cap_and_never_evict`, `rewrites_of_one_source_use_one_slot`, Hegel/proptest `admission` | implemented-S1 (parent-verified: distinct SourceIds counted, not epochs; known source stays admitted at cap; 1,025th distinct → `SourceLimit`; no eviction). |
| 4b inventory caps | `discover.go`, `inventory_settled_test.go` | None. | not applicable as a mechanism (Go row/call budgets are replaced by reconcile and classified files). Resumable traversal bounds are later slice 2. |
| 5a SQLite WAL | `sqlite_authorized.go` | `core_planner::sqlite_db_and_wal_and_absence_are_each_part_of_snapshot_fingerprint` | **implemented-S1 (round 4; read-only backup later slice 3).** History: partial-S1 (verified: core_planner.rs:211 asserts identical db+absent-WAL is `Unchanged`, WAL absent to present gives `Snapshot`, a changed rules digest gives `Replay(RulesChanged)`, no previous gives `Snapshot`. Missing: a db-only change with unchanged WAL is not asserted, and no WAL-aliased or denied case). The read-only backup is later slice 3. **Round 4: implemented-S1**: `core_f2_gaps.rs:390` asserts db-only change (each of dev, ino, birth, size, mtime; WAL absent or present) gives `Snapshot`, WAL-only change gives `Snapshot`, both unchanged gives `Unchanged`, absent<->present and Plain<->Sqlite give `Snapshot`. `:445` shows `privacy::admit` already covers a WAL identity supplied as facts (credential alias or backup alias denied, collector spelling/final/ancestor identity denied, unresolved path `Unknown`). Deriving those facts for a real `-wal` file is shell work, later slice 3. |
| 5b changed vs failed | `sqlite_authorized.go:36-41` | The closed variants `LocalError::{BackupFailed,SourceChanged}` exist. No test asserts how they are used. | **implemented-S1 for types and disposition mapping (round 4); backup behaviour later slice 3.** History: partial-S1 (types only). **Round 4: S1 part implemented**: `core_f2_gaps.rs:486` asserts `BackupFailed`/`SourceChanged` wire names, and `disposition::transition` maps both to `Retry{Local(..)}` with backoff 1,2,8,512,900,900 s and retries+1, never `Done`; with cancel they give `Cancelled` and an unchanged streak. Distinguishing changed from failed in the backup path is later slice 3. |
| 6a cancellation | `runtime_cancel_test.go` | `src/disposition.rs` `transition`; `core_limits::cancelled_keeps_streak_and_never_parks`, `cancel_swallows_only_retryable`, `timeout_and_offline_retry_not_cancelled`, Hegel/proptest `cancel` | implemented-S1 (parent-verified: cancel leaves streak unchanged and never parks; timeout is `Retry{Offline}`; deterministic/owner/auth outcomes are not swallowed by cancel). Shutdown integration is slice 2. |
| 6b item isolation | `runtime.go`, `classify_test.go` | Scope types `ParkReason`, `OwnerParkReason`, `Disposition::PauseHost` exist. No pairing tests exist. | later slice 2 (supervisor) |
| 7a identity race | `config.go:175-201` | `core_boundaries::every_coordinate_brand_validates_its_own_boundary_and_preserves_valid_spelling` | implemented-S1 (verified: core_boundaries.rs:6 asserts `MachineId` accepts `root-qualified.0123456789_ABC:def-ghi` with spelling preserved and serde round trip, rejects "", ".", "..", "a/b", newline and non-ASCII, accepts 1,024 and rejects 1,025 bytes). Temp-write plus exclusive link and crash completeness are later slice 2. |
| 7b modes | `config_test.go:218-235` | None. | later slice 2 (real filesystem) |
| 7c siblings / custom identity | `pi.go`, `identity_test.go` | `core_locator::namespaces_separate_roots_with_equal_names`, `core_locator::hegel_property7_result_stable_under_unrelated_facts`, `core_locator::proptest_property7_result_stable_under_unrelated_facts` | **implemented-S1 (round 4).** History: partial-S1 (verified: core_locator.rs:1138 asserts two roots with equal session names give distinct owner `r1:-p:abc` vs `r2:-p:abc` and ids, same kind; :1516 `prop_stable_rows` (run by the hegel :1667 and proptest :1687 wrappers) asserts a random row's result equals its expectation under unrelated noise facts and reversed root order, and a conflicting owner fact for the row's UUID only withholds (`UnknownOwner`); custom-root promotion equals the agent root. Missing: no assertion that a Codex archive move or recompression keeps the logical rollout identity was read). **Round 4: implemented-S1**: existing `core_locator.rs:516-545` already has per-path rows (sessions/ and archived_sessions/ `.jsonl`, `.jsonl.zst`) with the same literal owner and id, but does not compare them. `core_f2_gaps.rs:548` asserts all four spellings give one equal owner (`r1:rollout-..` session, Codex) and one equal id `r1:rollouts:<name>`, with a different UUID as control. |
| 7d `/var` alias | `capturecoordinates/*` | `src/coordinates.rs`; `core_coordinates::var_alias_resolves_to_one_namespace`, `explicit_alias_pair_is_accepted`, `unproven_physical_aliases_are_never_authorized`, `case_insensitive_alias_needs_proof_and_distinct_dirs_stay_distinct`, Hegel/proptest `coordinates_order_proof_and_retention` | implemented-S1 (pure; alias accepted only with shell-supplied identity proof or declared pair; no case folding; all spellings retained). Not yet wired into locator/privacy; native filesystem observation is slice 2. |
| 8 sleep / AC | DESIGN.md 500-510 | `core_mechanics::coalescer_config_wake_and_backwards_are_explicit` (**re-read**, core_mechanics.rs:447: a gap of 180 s gives no action, 181 s gives reconcile with 30 s jitter, and a backwards clock is an error), `core_mechanics::immediate_session_end_and_independent_owner_monotonic_clocks` (**re-read**, :493: `SessionEnd` polls true at its own instant while another owner's `Stop` is not yet due; owners have independent clocks), `core_mechanics::hegel_property2_monotonic_quiet_cap_end` (**re-read**, :394 via `coalescer_oracle`: poll true exactly at 60 s quiet or 300 s cap, one in flight, later end signal preserved) | implemented-S1 for the synthetic clock. Real Mac sleep/wake and AC evidence is later slice 2 (native). |
| 9 delete source after durable boundary | `crash_process_test.go` | `core_planner::committed_deletion_repeat_and_snapshot_repeat_have_no_capture_work` (**read**: core_planner.rs:262 asserts `Missing{consecutive:2}` plans `Deleted`, after `DeletionRecorded` the plan is `Unchanged` for old and new writers, and an unchanged plain snapshot is `Unchanged`). It covers the planner side only. | later slice 2 (the restore-after-source-delete journey needs store plus reader) |
| 10a Codex mirrors | `test_publications.py` | `core_mechanics::capture_oracle` (property 1) appends `0xff 0x80 \n` and checks byte-exact reassembly (**read**). Pairing is downstream. | not applicable to core (consumer pairing). The importer journey is later slice 2b. |
| 10b lone surrogates | `test_events.py`, `crossconfig_test.go` | The same property-1 non-UTF-8 bytes (**read**), plus `privacy_port::rejects_malformed_duplicates_unicode_trailing_and_nonobjects` (**read**: privacy_port.rs:154 asserts `\ud800` and `0xff` in protected config give `MalformedProtected`) | **implemented-S1 (round 4; importer isolation later slice 2b).** History: partial-S1 (raw bytes and config rejection). Importer isolation is later slice 2b. **Round 4: implemented-S1**: `core_f2_gaps.rs:606` asserts invalid UTF-8, JSON lone-surrogate escapes and raw encoded surrogates in Transcript/OtherKnown bytes come out byte-exact with the SHA-256 of the input; `:627` asserts all 11 protected names give `MalformedProtected` for invalid UTF-8 and lone surrogate escapes. Importer isolation stays slice 2b. |
| 11 shared exclusions | DESIGN.md 643-648 | `core_locator::matrix_rows_match_independent_expectations` (**read**: core_locator.rs:1006 asserts each of >150 rows' kind, owner and role equals an independent expectation; shared-once-per-change and no-opt-out behaviour is not asserted in core) | not applicable (opt-out retired by decision). Shared-store matrix is later slice 3. |
| 12 epoch/manifest growth | `liveness_test.go:470-482` | `core_manifest::history_smoke_and_r3_checkpoint_sizes`, `core_manifest::hegel_deltas_from_checkpoint_equal_next_checkpoint`, `core_manifest::proptest_deltas_from_checkpoint_equal_next_checkpoint` (**re-read**: core_manifest.rs:1490 `history_shapes` asserts builder state equals the independent model, canonical round trip, recovered projection equals the model, and delta/checkpoint cadence at seq % 64; :1168 and :1612 assert a 65th fold step fails with `TooManySteps`; :1632 asserts a checkpoint lists more than 64 pieces (R3). Earlier report: the model equals the builder state, snapshot checkpoints keep 1 epoch, checkpoint k plus 63 deltas plus changes equals checkpoint k+64, a 65th fold step fails with `TooManySteps`) | **implemented-S1 (round 4).** History: partial-S1 (no generator for more than 206 rewrites of one source; no byte-size accounting assertion beyond `> 0`). **Round 4: implemented-S1**: `core_f2_gaps.rs:744` builds 300 manifests (200 append epochs of one source, snapshot replaced 300 times) and asserts at every seq the builder state equals a test-written model, every checkpoint (0,64,128,192,256) retains all append epochs and only the latest snapshot epoch, the 64-manifest fold plus changes equals the next checkpoint, and the final fold equals the model. Checkpoint bytes grow strictly and deltas do not grow with history. Runtime about 3 s. |

## Coverage guard design

Each property has a per-test-binary `static` set of `AtomicU64` shape counters. The `#[hegel::test]` and `proptest!` wrappers became plain `#[test]` functions. Hegel runs through `hegel::Hegel::new(..).run()` (default settings, so `HEGEL_TEST_CASES` still applies). Proptest runs through `TestRunner::default().run(..)` (so `PROPTEST_CASES` still applies). After the run, `require_all_shapes(counts)` returns `Err` when any count is zero or when the list is empty, and the test asserts `Ok`. The two engines have separate statics, so one engine cannot satisfy the guard for the other.

- **Property 1** (`core_mechanics.rs`):
  - multi-epoch: a `Plan::Replay` occurred.
  - rewrite-detected-at-window: a replay at operations 1..=4 (head start, head end, tail start, tail end).
  - interior-rewrite-with-audit: operation 8 reached the audit repair.
  - truncate-regrow: a replay at operation 6.
  - identity-replacement: operation 5 replayed with `ReplayReason::IdentityChanged`.
- **Property 6** (`core_manifest.rs`):
  - multi-epoch: an append-class source got a new epoch.
  - snapshot-replacement: a snapshot-class source was replaced.
  - delete: a source was deleted.
  - fold-across-checkpoint: the fold reached a checkpoint boundary (the fold property only; the round-trip property checks the first three).

**Negative controls.**
- `property1_coverage_guard_negative_controls_bite` (all-zero counts, empty counts, each single missing shape).
- `property6_coverage_guard_negative_controls_bite` (the same, plus a real 2-manifest history that cannot reach a checkpoint fold).
- Each control also checks that the error names the missing shape.

The existing negative control `property1_negative_control_disabled_window_check_must_fail` and the test name `property1_independent_capture_and_eligible_audit` (as a substring of the two engine test names) are unchanged, so the mutation gate's filter still matches.

**DESIGN guard cases that belong to property 4 / slice 2.** Restart, crash-after-manifest, lost-reply and delete-index need a store, a current-pointer commit and a process lifecycle. Pure S1 code has none of these: there is no PUT, no 412, no index file and no process. Counting them here would be a vacuous counter. They are guarded in the slice-2 property 4 state machine.

## Requested production changes (outside my ownership)

1. A pure owner-key length function returning `KeyTooLong` at 1,025 UTF-8 bytes and admitting 1,024 (F2-2).
2. A pure owner source-count admission function returning `SourceLimit`, counting distinct source IDs and not epochs (F2-4a).
3. A pure `request_budget(size)` function (F2-3).
4. A pure disposition transition (cancel does not advance the failure streak; fatal is not swallowed) (F2-6a/6b).
5. A pure coordinate-alias module (F2-7d).

## Limitations

- `core_coverage_guard.rs` was not created, because of the turn budget. Items 1-5 above would be its tests.
- Rows still partial after verification are listed with their missing assertions in the table.
- The more-than-206-rewrite generator for F2-12 is not implemented.
- The property-6 round-trip property does not require a fold across a checkpoint, because its length is at most 6.

## Verification method

Read-only verifier pass (no commands other than grep/sed, nothing run, nothing in Rust edited). Files read: `rust/crates/memory-core/tests/privacy_port.rs` (52-375, 477-540), `core_hint.rs` (96-140, 800-892), `privacy_coverage.rs` (85-135), `parent_privacy_review.rs` (100-162), `core_planner.rs` (200-324), `core_boundaries.rs` (1-130), `core_locator.rs` (1000-1060, 1130-1190, 1516-1560, 1600-1696), `core_mechanics.rs` (164-260, 394-577), `core_manifest.rs` (1487-1748), `privacy/launch.rs` (grep). Also read: acceptance map section 1 and this file. Rows 2, 3, 4a, 6a and 7d are pending the parent's round-3 helper. Acceptance-map section 1 has 21 subcases (1a..12) and each has exactly one row here.

## Commands and exit statuses (pinned Nix, `CARGO_TARGET_DIR=target/w-guard`)

- `cargo clippy -p memory-core --all-targets --locked -- -Dwarnings`: exit 0.
- `HEGEL_TEST_CASES=2000 PROPTEST_CASES=2000 cargo test --locked -p memory-core --test core_mechanics --test core_manifest -- --test-threads=2`: exit 0. `core_manifest` 23 passed in 211 s. `core_mechanics` 11 passed in 128 s.
- `python3 scripts/check-window-mutation.py` (2,000 cases per engine): exit 0, "PASS: unchanged baseline passed; actual window-check mutant compiled and failed the independent property1 runtime byte oracle". Both engine tests were killed by the mutant.
- `core_coverage_guard.rs` does not exist, so the test command did not name it.

## sha256 of owned files (before this section was appended to this report)

- `core_mechanics.rs`: ea3189db2720e7fc399956c0ac54f14c786fbb8f9c2078fba8d7dce0b99fe6ed
- `core_manifest.rs`: 0af06fc62aa0dcf279ce4f596de59f3014c14ba0a9336fd4a74a41f1f83b57cd

Ownership released.

## Round 4 (F2 gap worker)

Owned: `rust/crates/memory-core/tests/core_f2_gaps.rs` (12 tests) and the 7 rows above. No production code was changed.

| Row | From | To | Evidence |
|---|---|---|---|
| 1a | partial-S1 | implemented-S1 | `core_f2_gaps.rs:125`, `:177` |
| 1b | partial-S1 | implemented-S1 | `:210`, `:248`, `:304` |
| 5a | partial-S1 | implemented-S1 (shell WAL facts: slice 3) | `:390`, `:445` |
| 5b | partial-S1 | S1 types and mapping implemented; behaviour slice 3 | `:486` |
| 7c | partial-S1 | implemented-S1 | `:548` |
| 10b | partial-S1 | implemented-S1 (importer: slice 2b) | `:606`, `:627` |
| 12 | partial-S1 | implemented-S1 | `:744` |

Requested production changes: none.

Findings:
- A backup spelling of `.claude.json` is `Deny(Credential)` (the primary is scrubbed). Only the 10 other names are scrubbed under every alias.
- The 8-env bound in this report was correct. Rust skips entries without `=`, Go cuts them to an empty value that the grammar rejects, so the outcomes match.
- Codex session coordinates carry the namespace prefix (`r1:rollout-..`). One initial expectation of mine omitted it and was fixed in the test, not the code.

Commands (pinned Nix, `CARGO_TARGET_DIR=target/w-f2gaps`):
- `cargo clippy -p memory-core --all-targets --locked -- -Dwarnings`: exit 0.
- `HEGEL_TEST_CASES=2000 PROPTEST_CASES=2000 cargo test --locked -p memory-core --test core_f2_gaps -- --test-threads=2`: exit 0, 12 passed. None of these tests uses Hegel or proptest, so the case counts do not apply.
- `rustfmt --edition 2024` on the test file only.

Limitations: no real filesystem, no importer, no shell facts; row 12 uses one fixed deterministic history, not a random generator. Gates, coverage and mutation scripts were not run.

Ownership released.
