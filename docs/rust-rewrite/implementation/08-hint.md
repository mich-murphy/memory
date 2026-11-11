# 08 Hint worker report (slice 1)

Scope: pure, I/O-free hint layer in `rust/crates/memory-core/src/hint.rs`, tests in `rust/crates/memory-core/tests/core_hint.rs`. Dated 2026-11-08. Uncommitted.

## Schema

- Owned line, schema string `memory.hint/1` (`HintSchema::V1`, always the first serialised field). `HintRecord` has `deny_unknown_fields`, serde and schemars.
- Fields: `schema`, `agent`, `event`, `session`, `cwd`, `transcript`, `launch`, `reduction`.
- Closed `HintEvent`: `session_start`, `turn_end`, `turn_failed`, `subagent_stop`, `pre_compact`, `task_completed`, `teammate_idle`, `session_end`. `flushes()` is true only for `session_end` (DESIGN 7.1).
- No `Option`: `PathHint {absent | present{path}}`, `LaunchHint {none | digest_only{digest} | full(LaunchRecord)}`, `Reduction {none | launch_dropped | paths_dropped}`.
- `LaunchRecord` is built only from `SanitizedLaunch` (`from_sanitized`). It carries a SHA-256 digest over length-prefixed argv and env. The reader rejects a `Full` record whose digest does not match its content.
- The hook-input DTO is separate and tolerant. `parse_hook_input(agent, stdin)` reads only the event name (`hook_event_name` or `event`), `session_id`, `cwd` and `transcript_path` (or Pi `session_file`). Unknown keys are ignored. Paths must be absolute, at most 1024 bytes and free of control characters; a bad path degrades to `Absent`. Errors are the typed `HookInputError`, which never carries input text. Input over 1 MiB is refused.

## Size bound

`build_line` caps a line at 4096 bytes including its newline. Reduction is deterministic and in order:

1. Full record.
2. Launch argv/env dropped, digest kept (`launch_dropped`; only when there is a launch).
3. Path hints dropped as well (`paths_dropped`).
4. Otherwise `BuildError::TooLarge`.

Serialisation is `serde_json::to_vec`, so there is no interior newline. `build_line_with_limit` exposes the limit for tests.

## Resync semantics

`scan(buffer, start)` returns `HintScan`:

- `records`: each with its absolute end offset.
- `dropped`: garbage lines and torn fragments.
- `rejected_version`: lines with the own prefix `memory.hint/` and another version.
- `next_offset`: just past the last complete line.
- `pending_bytes`: an incomplete trailing line, which waits for its newline and is neither parsed nor dropped.

A torn fragment glued in front of a valid record is found by searching for the `{"schema":"memory.hint/` marker within the last 4095 bytes of the line. The fragment is counted as dropped and the record is recovered. A `start` beyond the buffer returns `RangeError`. Byte ranges go through `slice::bytes`.

## Privacy oracle

The test plants canaries in four places:

- argv secret flags, an inline `--mcp-config` JSON and positional prompt text;
- env values;
- unknown hook keys, a prompt, `tool_input` and `last_assistant_message`.

It byte-searches the built line, in the original and lowercased forms. A positive control puts a canary in an allowlisted `--model` value and expects the oracle to find it, so the oracle cannot pass vacuously.

## Requested domain/xtask changes

None.

## Commands

All ran from `/Users/mm/dev/memory` under pinned Nix with `CARGO_TARGET_DIR=target/w-hint`.

- `cargo clippy -p memory-core --all-targets --locked -- -Dwarnings`: exit 0 on the final file state.
- `HEGEL_TEST_CASES=2000 PROPTEST_CASES=2000 cargo test --locked -p memory-core --test core_hint -- --test-threads=2`: exit 0, 21 passed, 0 failed, about 135 s.
- Earlier runs failed on clippy (`indexing_slicing`) and on a wrong test premise in `reductions_are_deterministic_and_ordered`. Both were fixed in the test file only.

The gates script and coverage were not run, as instructed.

## Parent correction 1

Superseding statements above: the Pi names `turn_end`, `agent_end` and `session_before_compact` are removed (not accepted, tested as `UnknownEvent`); the "Pi event names are a best guess" limitation no longer applies. The schema gained a `agent_transcript` `PathHint` field (between `transcript` and `launch`), and `HintEvent` gained `interrupted`.

### Evidence-cited event mapping (matching is case- and underscore-insensitive)

| Agent | Hook event | HintEvent | Evidence |
|---|---|---|---|
| Claude | SessionStart | session_start | internal/capturehooks/json.go:20 |
| Claude | Stop | turn_end | same |
| Claude | StopFailure | turn_failed | same |
| Claude | SubagentStop | subagent_stop | same |
| Claude | PreCompact | pre_compact | same |
| Claude | TaskCompleted | task_completed | same |
| Claude | TeammateIdle | teammate_idle | same |
| Claude | SessionEnd | session_end (flush) | same |
| Codex | SessionStart | session_start | json.go:22 |
| Codex | Stop | turn_end | same |
| Codex | SubagentStop | subagent_stop | same |
| Codex | PreCompact | pre_compact | same |
| Codex | Interrupt | interrupted (flush) | same; codexschema.go:35 |
| Codex | SessionEnd | session_end (flush) | same |
| Pi | session_start | session_start | pi.go:66, setup.go:47-56 |
| Pi | agent_settled | turn_end | pi.go:68 |
| Pi | session_shutdown | session_end (flush) | pi.go:69; hook.go:43 |

Everything else is `UnknownEvent`, including Codex `StopFailure`/`TaskCompleted`/`TeammateIdle` and Claude `Interrupt`.

### Decisions

- **Interrupt**: a new closed variant `Interrupted`, and `flushes()` is true. Reason: an interrupted Claude/Codex turn fires no `Stop`, so nothing else would flush it; and internal/install/trust.go:38 caps the Interrupt hook like SessionEnd (1 s), so waiting out the quiet window gains nothing. The session may continue, so it is not `session_end`.
- **agent_transcript_path** (hook.go:91, hook_test.go:79-80): parsed into `HookInput.agent_transcript` and stored as its own `PathHint`, never overwriting `transcript`. The `paths_dropped` reduction drops it with `cwd` and `transcript`.

### Tests added or changed

- `event_tables`: positive for every row above, per-agent negatives for all other names and the removed guesses.
- `subagent_transcript_is_a_separate_hint`: separate field survives build/scan, is dropped by `paths_dropped`, and an invalid path degrades to `Absent`.
- Goldens updated with `"agent_transcript":{"state":"absent"}`. `flushes()` test and generators cover `Interrupted`.

### Encode / fallback arms

Not covered by tests; each is unreachable by construction, not by a test:
- `BuildError::Encode`: `serde_json::to_vec` on `HintRecord` cannot fail (only string-keyed maps, no non-finite floats, no custom fallible serialisers).
- `u64::try_from(len).unwrap_or(u64::MAX)` in the digest and `scan`: `usize` is at most 64 bits on supported targets.
- Coverage was not measured here; the parent's coverage gate decides whether these arms need an `#[expect]`-free restructure.

### Commands

From /Users/mm/dev/memory, pinned Nix, `CARGO_TARGET_DIR=target/w-hint`:
- `cargo clippy -p memory-core --all-targets --locked -- -Dwarnings`: exit 0.
- `HEGEL_TEST_CASES=2000 PROPTEST_CASES=2000 cargo test --locked -p memory-core --test core_hint -- --test-threads=2`: exit 0, 22 passed, 0 failed, about 133 s.
- Intermediate runs: one failed (exit 101) on a wrong reduction-limit premise in the new test; fixed in the test file only.
- `rustfmt --edition 2024` on my two files only.

### sha256 of owned files (before this report edit)

- `src/hint.rs`: `d534fc6fbfb06eae8c20ab539f6899ce9c72c212a29004892c5cf940c2d7a2d1`
- `tests/core_hint.rs`: `1398e27be4537b47b192dc0158b6b6573dd2702fbb3e8f40f515faaff3c18262`

Requested domain/xtask changes: none.

I release my owned paths: `src/hint.rs`, `tests/core_hint.rs` and `08-hint.md`.
