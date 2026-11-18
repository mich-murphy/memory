# Evidence 26: the empty-prefix refusal lifted; `prefix` is a required key (brief 35)

Dates are UTC; the work ran on 2026-11-18 from 09:16Z to 09:43Z. Status: increment PREFIX accepted
locally as `616132b` (one commit over `b046705`). Nothing was installed, sent to R2 or run against
the acceptance run's state root. The acceptance run `20261117-a28bfa4` kept running on its own
release binary and config throughout.

[Brief 35](../../35-prefix-guard.md) sits on `b046705` (on top of `main` `7a6221d`).

## Why now

DESIGN 18 lists the prerequisites of the first production write: zstd pieces, the audit of 11.4,
hint retention, `tracing` to stderr and (D45) streaming capture. All were on `main` at `7a6221d`
(briefs 26 to 31 and 34). The refusal (`config_prefix_required`, brief 27) existed only to hold
the line until then. It also covered a forgotten `prefix` line, because the key defaulted to empty;
that job moves to the config parser.

## Commit

| Commit | By | Change |
|---|---|---|
| `616132b` | worker (Sonnet, medium) | `Raw.prefix: Option<String>`; missing -> `ConfigError::PrefixMissing` (`config_prefix_missing`, exit 2); `AssembleError::PrefixRequired` and the `owners` refusal removed; every test config gains an explicit `prefix` line; new and replaced tests below |

Tests:

- `state.rs` `config_prefix_is_a_required_key`: a config without the key is `PrefixMissing`;
  `prefix = ""` parses to `""`. The accept and reject tables now swap the `prefix` line of `VALID`.
- `harness.rs` `a_missing_prefix_is_refused_with_exit_2_by_every_command` (real binary): `daemon`,
  `check`, `export`, `restore` and `owners` each exit 2 with exactly
  `memory: config_prefix_missing\n` and empty stdout. Replaces
  `an_empty_prefix_is_refused_with_exit_2`. `a_refusal_with_a_closed_stderr_still_exits_2` uses the
  missing key.
- `s3_store/assemble.rs` `an_empty_prefix_writes_only_at_the_bucket_root_and_restore_and_owners_find_it`
  (fake store): with `prefix = ""` the daemon captures one session; every request path is under
  `/<bucket>/sessions/`, `/<bucket>/objects/` or `/<bucket>/manifests/`, with a PUT in each; the
  real-binary `owners` lists exactly that owner and `restore` gives back the source bytes.
- `s3_store/assemble.rs` `refusals_are_closed_codes_and_leave_no_lock`: the first refusal is the
  missing key, and a daemon starts afterwards (the lock was released).
- `s3_store/owners.rs` `owners_with_an_empty_prefix_lists_sessions_at_the_bucket_root` replaces
  `owners_empty_config_prefix_is_exit_2`; a key under `other/sessions/` is not listed.

## Teeth (parent)

Script in the parent's scratchpad (`prefix_teeth.py`); each tooth replaced exact anchors, ran the
`state`, `harness` and `s3_store` binaries (property and consumer-gate tests excluded) and restored
the files.

| Tooth | Caught by |
|---|---|
| missing key becomes `""` (the old `#[serde(default)]`) | `config_prefix_is_a_required_key`, `refusals_are_closed_codes_and_leave_no_lock`, both `harness` refusal tests |
| missing key mapped to `config_parse` | `config_prefix_is_a_required_key`, `refusals_are_closed_codes_and_leave_no_lock`, `a_missing_prefix_is_refused_with_exit_2_by_every_command` |
| the daemon's empty-prefix refusal restored | `an_empty_prefix_writes_only_at_the_bucket_root_and_restore_and_owners_find_it` |
| the `owners` refusal restored | `owners_with_an_empty_prefix_lists_sessions_at_the_bucket_root`, the bucket-root test |
| a fixed prefix substituted for `""` in `build_store` | the same two |

5 of 5 caught.

## Checks on `616132b`

`nix develop .#rust --command sh -c 'cd rust && MEMORY_ANALYTICS_PYTHON=<repo>/.analytics-venv/bin/python
PROPERTY_CASES=500 bash scripts/gates.sh'`: all 14 sub-gates 0, seed 2480786143; 04-nextest 717
passed, 05-hegel 32, 06-proptest 33, 10-consumer 2; coverage 96.69 % (9,148 of 9,461 lines).

## Effect and limits

- The daemon now accepts `prefix = ""`, the production root of the bucket. Nothing installed uses
  this build: the acceptance run's config has its `rust-acceptance/20261117-a28bfa4/` prefix and
  its own binary. The first production write needs an installed config with `prefix = ""`, which is
  the cutover step (DESIGN 18 step 2).
- Any existing `config.toml` without a `prefix` line is now refused. The acceptance run's config
  has one.
- Not run: `cargo mutants` (operator decision), anything against R2.
