# 10. Limits and disposition (slice 1)

Pure code, no I/O, no clock. Owned files: `rust/crates/memory-core/src/limits.rs`,
`src/disposition.rs`, `tests/core_limits.rs`.

## Rules

| Rule | Implementation | Citation |
|---|---|---|
| `current.json` key | `current_key`: `sessions/<archive>/<machine>/<agent>/<session_id>/current.json` or `shared/<archive>/<machine>/<agent>/current.json`, wire agent spelling, measured in UTF-8 bytes. 1,024 admitted, 1,025 is `KeyTooLong`. | DESIGN 6.2; Go `publicationr2/backend.go:78`, `mutation_boundary_test.go:65-82`; acceptance row 2 |
| Source cap | `SourceSet::admit` counts distinct `SourceId`s. A known id is always admitted, the 1,025th distinct id is `SourceLimit`, nothing is evicted, rewrites use no slot. | DESIGN 6.2; Go `liveness_test.go:457-543`, `runtime.go:49-65`; row 4a (Go counts epoch entries, this counts sources) |
| Request budget | `request_budget(size) = max(60 s, 30 s + size / 1 MiB/s)`, exact to the nanosecond, saturating. 8 MiB gives 60 s. | DESIGN 7.3; Go `budget.go:13-98`; row 3 |
| Cancelled | `Outcome::Aborted` gives `Cancelled` and returns the prior streak unchanged. With `cancel_requested`, retryable outcomes (timeout, Offline, Throttled, Local) are also `Cancelled` with the streak unchanged. | Go `runtime_cancel_test.go:52-180`; row 6a |
| Not swallowed by cancel | Success, `OwnerFault`, `DestinationAuth` and deterministic faults keep their own disposition when cancel is requested. | row 6a |
| Timeout / Offline | `Retry{Offline}` with exponential backoff 1 s to 15 min, no jitter (the shell adds jitter). Timeout is never `Cancelled` unless shutdown was requested. | DESIGN 11.2 |
| DestinationAuth | `PauseHost`, streak untouched. | DESIGN 11.2 |
| Deterministic park | `Park` when the same fault has failed 3 consecutive times and the latest failure is at least 3 h after the first. Until then `Retry`. A different fault restarts the streak. Success resets it. | DESIGN 11.2 (3 failures, spanning at least 3 h) |
| Scopes | `Park` (source) and `ParkOwner` (owner) are distinct, as are `PauseHost` and `Retry`. | DESIGN 5; row 6b |

## Choices and limitations

- The threshold (3) and span (3 h) come from DESIGN 11.2. Backoff constants 1 s and 15 min come from the same table; the doubling shape is my choice.
- `Fault::NonJsonResponse` and `Fault::Schema` have no `ParkReason` in domain.rs, so they never park and stay `Retry`. DESIGN 11.2 lists them as deterministic but 5 names only three park reasons. A `ParkReason` variant is a domain.rs change, which I was not allowed to make. This needs a decision.
- A deterministic fault with a simultaneous cancel is not swallowed, but `Retry` is returned so the shell decides on shutdown. The streak is counted.
- `at` is a monotonic offset supplied by the shell.
- Key length tests cover raw segments through `current_key_len`. Identifiers in domain.rs are ASCII only, so multibyte coverage is of the byte-measuring function, not of constructible owners.
- Requested domain changes: none required.
- Hegel and proptest properties: key length monotonic with an exact boundary, admission never evicts and rewrites do not change it, budget monotonic and at least 60 s, cancellation never changes the streak.

## Parent correction 1 (2026-11-08): every deterministic fault parks

The worker's open question was resolved by the parent against DESIGN 11.2, which lists
non-JSON 200 and schema errors as `Deterministic` with "`Park` after 3 consecutive failures
of one source spanning at least 3 h". Leaving `NonJsonResponse`/`Schema` retrying forever
contradicted that. Parent added `ParkReason::{RepeatedNonJsonResponse, RepeatedSchemaError}`
to `domain.rs`; `park_reason` is now total (`Fault -> ParkReason`), and the transition parks any
deterministic fault at the same threshold. The test that asserted the old "never parks"
behaviour was deliberately replaced by `every_deterministic_fault_parks_with_its_own_reason`,
which also asserts a below-threshold streak still retries. Focused checks (pinned Nix,
`CARGO_TARGET_DIR=target/w-parent`): rustfmt then `cargo fmt --check` 0; workspace
`cargo clippy --all-targets -- -Dwarnings` 0; `core_limits` at 2,000 cases 26/26 passed;
`cargo xtask schemas --check` 0. Integrated gate rerun is recorded in the handoff.
