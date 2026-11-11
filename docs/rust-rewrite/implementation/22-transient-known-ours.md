# 22. A6: Transient class, full-jitter backoff, known_ours

Status: implemented and committed (c1ab3ec on `rust/transient-known-ours`, PR #59). No secrets or transcript content. Later: per-class failure counts and per-owner `not_before` scheduling landed in 2a18883, and a daemon-level late-write step in 42e8826 (report 23), which close the gaps listed below.

## Changed files
- memory-core/src/domain.rs: `ErrorClass::Transient`.
- memory-core/src/disposition.rs: `Pacing {draw, retry_after}`, `transition_paced`, `jittered`, `transient_ceiling`, `TRANSIENT_MAX`. `transition` is kept and uses `Pacing::FLOOR` (draw 0, so every jittered wait is 1 s).
- memory/src/environment.rs: `StoreError::Throttled(Option<u32>)` (Retry-After seconds) and `retry_after()`.
- memory/src/commit.rs: `KnownOurs`, `commit_known` (`commit` wraps it), `Failure::Throttled{retry_after}`, `Landed.follow_up`, `disposition_paced`, `verify` adoption. `BaseUnchanged` maps to `Transient`.
- memory/src/daemon.rs: per-owner in-memory `known_ours`, splitmix64 draw from a counter and the clocks, follow-up handling (adoption keeps the base and applies no updates, so the next round captures again).
- tests: core_limits.rs, commit_protocol.rs.

## Choices
- Offline: `uniform(1 s, min(15 min, 2^n s))`. Transient: `uniform(1 s, min(120 s, 2*2^n s))`, ignores Retry-After.
- Throttled: Retry-After honoured, capped at 15 min; otherwise the Offline schedule.
- Local and Deterministic keep the unjittered `backoff` (11.2 and A6 do not say otherwise).
- known_ours is cleared on any landed commit (Committed, AlreadyOurs, adoption).

## Changed assertions
- core_limits `timeout_and_offline_retry_not_cancelled`: Throttled retry 2 s becomes 1 s. With draw 0 the full-jitter window minimum is 1 s.
- commit_protocol `store_errors_classify_and_display`: `StoreError::Throttled` becomes `Throttled(None)` (signature).
- `disposition_maps_every_commit_result` is unchanged. BaseUnchanged still yields a Retry, now with class Transient, checked by the new test.

## New tests
- commit_protocol: `late_write_of_an_earlier_failed_capture_is_adopted_and_followed_up` (AlreadyOurs, follow-up commits, chain reassembles via `assert_safe`); `late_write_of_an_unknown_manifest_is_foreign` (negative control); `base_unchanged_exhaustion_is_transient_not_throttled`.
- core_limits: `a6_jitter_windows_hold_for_every_draw` (bounds, cap, Throttled fallback), `a6_retry_after_is_honoured_and_transient_is_its_own_class`.

## Evidence (exit status 0 unless stated)
- fmt, clippy -Dwarnings, `nextest run --workspace` (377 passed, 2 skipped), doctests, `xtask schemas --check` and check-lints.sh all passed. The schemas and lints statuses in my script came from a pipe, so they are not strictly reliable. Schemas needed no regeneration.
- 2,000 cases, HEGEL_SEED=730211 and PROPTEST_RNG_SEED=730211: memory daemon + commit_protocol 49 passed (28.2 s) and 30 passed (15.4 s, wall 46 s); memory-core scaffold 4 passed (0.16 s).
- Mutation: `known.get(...)` forced to miss. The late-write test failed with `Foreign{..}`; the negative control stayed green. Reverted (checked by grep).
- Seed sweep: `hegel_daemon_state_machine`, 500 cases, seeds 1 to 20: 20 of 20 passed, no guard failure (3 to 7 s each). Seed 6 passes now. The flake did not reproduce, so no weights were changed. Before and after counts are not available because the counters are only printed on failure.

## Parent takeover and verification (2026-11-09)

The worker's run aborted after its checks and before it handed over, so the parent took over these paths. The parent reviewed the whole diff. It confirmed that after an adoption the index no longer matches the remote position, so the next capture runs `Adopt` and then `Append`, the same path as a lost reply. It also confirmed that the mutation was reverted and that no regression files are left.

- Fix: a `Retry-After` was clamped only above (`min(15 min)`), so `Retry-After: 0` gave a 0 s retry. It is now clamped to [1 s, 15 min]. New test: `a6_retry_after_is_clamped_to_one_second_and_fifteen_minutes`.
- Parent rerun on source diff hash `10a754394f9919d2`:
  - fmt 0, clippy `-Dwarnings` 0, doctests 0, `xtask schemas --check` 0, check-lints.sh 0.
  - `nextest run --workspace --no-fail-fast`: 378 passed, 2 skipped. The first fail-fast run stopped on the flake below.
  - 2,000 cases at seed 730211:
    - memory `daemon` + `commit_protocol`: 49 + 30 passed, 36.8 s.
    - memory-core `core_limits` + `scaffold`: 29 + 4 passed.
- Flake found, which predates A6 because the coordinates files are unchanged: `core_coordinates::hegel_coordinates_order_proof_and_retention` fails its coverage guard ("generator never produced proof-only").
  - At the default case count it fails for 14 of seeds 1 to 30.
  - At 500 cases (the CI count, random seed) it fails for seeds 6 and 13 of 1 to 30.
  - It is not fixed here. The fix is to raise the proof-only generator weight and never lower the guard. It is the next gate-hygiene item.

## Gaps
- No daemon-level late-write test, and no state-machine step with its own guard. Only the commit-level interleaving is driven.
- RoundReport and StatusBody have no per-class counters, so there is nowhere yet to count Transient apart from Throttled. The Failure/Outcome classes are distinct.
- The daemon computes the retry delay but does not schedule on it yet (no timing there).
- The seed 6 flake was not reproduced under this change; it may depend on step counts or timing.
