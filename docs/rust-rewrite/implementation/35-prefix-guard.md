# 35. Lift the empty-prefix refusal; `prefix` becomes a required key (2026-11-18)

Coordinator brief. The parent session owns this file and acceptance. It follows
[brief 34](34-streamed-check.md). Evidence goes to `evidence/26-prefix-guard/`. Text that states a
plan stays as written; later changes are marked.

The acceptance run `20261117-a28bfa4` keeps running while this brief is worked on, on its own
release binary and config (which has a `prefix` line). Nothing here is installed, sent to R2 or
run against the run's state root. Lifting the refusal in code writes nothing anywhere: the first
production write needs an installed config with `prefix = ""`, which is the cutover step (DESIGN
18 step 2), not this brief.

## Goal

The daemon and `memory owners` refuse an empty key prefix (`config_prefix_required`, exit 2) "until
the A13.2 prerequisites (tracing, hint retention, the audit) exist" (`memory/src/assemble.rs:130`;
brief 27, "Production-write guard"). DESIGN 18 lists the prerequisites of the first production
write: zstd pieces, the audit of 11.4 with its property, hint retention, `tracing` to stderr, and
since D45 streaming capture. All are on `main` (`7a6221d`): `zstd-3` pieces
(`memory/src/capture.rs`), `memory/src/audit.rs`, `RETENTION_DAYS` and `past_retention`
(`memory-core/src/hint.rs`), `memory/src/log.rs`, briefs 31 and 34 (streaming capture, audit and
check). The guard's reason is gone, and cutover on this Mac needs the daemon to write to the
bucket root.

The guard also did a second job: `prefix` defaults to empty (`#[serde(default)]`), so today a
config that forgets the line is refused. Lifting the guard alone would make a forgotten line write
to the production root silently.

**PREFIX**: `prefix` becomes a required key of `config.toml`. A config without it is refused by
every command that loads the config, with the closed code `config_prefix_missing` (exit 2, as
every config refusal). An explicit `prefix = ""` means the bucket root and is accepted by every
command, the daemon and `memory owners` included. `config_prefix_required` and
`AssembleError::PrefixRequired` are removed.

Not in this brief: an install path (plist, hooks, Pi extension) for cutover; deleting
`rust-acceptance/` prefixes; any change to key layout, stored formats or log lines.

## Checkout

`main` is `7a6221d`. This brief is on `docs/brief-35`. The worker gets `../memory-wt/b35-prefix`
on `rust/prefix-guard` from `docs/brief-35`. Crate paths below are under `rust/crates/`.

## Facts checked on `7a6221d`

| Fact | Where |
|---|---|
| `Raw.prefix` has `#[serde(default)]`; `valid_prefix` accepts `""` or a relative `/`-terminated path of clean segments, at most 256 bytes; bad -> `ConfigError::Prefix` (`config_prefix`) | `memory/src/config.rs:128-143`, `:149`, `:172-184`, `:262` |
| `Config` is parsed only through `Config::parse` / `Config::load`; `deny_unknown_fields` | `memory/src/config.rs:244-290` |
| `assemble::build` refuses `config.prefix.is_empty()` with `AssembleError::PrefixRequired`; doc comments on `build_store` and `start_from_config` say so | `memory/src/assemble.rs:27-55`, `:88-92`, `:125-134`, `:192-198` |
| `owners` repeats the refusal itself (`config_prefix_required`, exit 2) | `memory/src/owners.rs:107-112` |
| `check`, `export`, `restore` load the config and call `build_store`, which does not judge the prefix | `memory/src/check.rs:529`, `memory/src/export.rs:197`, `memory/src/restore.rs:323` |
| `S3Store::new` already accepts an empty prefix | `memory/src/s3.rs:235` |
| Tests that expect `config_prefix_required`: `harness.rs` `an_empty_prefix_is_refused_with_exit_2`, `a_refusal_with_a_closed_stderr_still_exits_2` (uses the empty prefix only to get a refusal); `s3_store/assemble.rs` `refusals_are_closed_codes_and_leave_no_lock`; `s3_store/owners.rs` `owners_empty_config_prefix_is_exit_2` | as named |
| Test configs written without a `prefix` line: `tests/{mutation_daemon,audit,resolve,counters,roots,daemon}.rs` (daemon.rs also in `window_defaults_fill_the_missing_half`), `tests/s3_store/skeleton.rs`, and `tests/state.rs` (`VALID`, the minimal config in `config_accept_reject_table`, which asserts the default `""`) | `grep -rn 'token_path = ' rust/crates --include=*.rs` |

## Decisions

Each is reversible. A worker that finds one wrong reports it and does not decide.

| Topic | Decision |
|---|---|
| Missing key | `Raw.prefix: Option<String>` (no serde default); `None` -> new `ConfigError::PrefixMissing`, code `config_prefix_missing`, checked in the place the other field rules are checked, before `valid_prefix`. Not left to serde: a missing field must not become the generic `config_parse` |
| Empty prefix | Accepted everywhere. Remove `AssembleError::PrefixRequired` (variant, code, the check in `build`) and the check in `owners.rs`; fix the doc comments that mention the refusal |
| Order of checks | Unchanged otherwise: `PrefixMissing` sits where `Prefix` is checked today |
| Other commands | No change beyond the config rule; `hook` does not load the config and is untouched |
| Test configs | Every test config gains an explicit `prefix` line: `prefix = ""` where the test does not care. The `harness.rs` closed-stderr test gets another refusal (a config without `prefix`, now `config_prefix_missing`) |

## Increment

| # | Increment | Branch | Owner |
|---|---|---|---|
| 1 | PREFIX | `rust/prefix-guard` | worker (Sonnet, medium) |

Owns `memory/src/config.rs`, `memory/src/assemble.rs`, `memory/src/owners.rs` and the test files
named above. Read-only everywhere else.

- Proof (each a named test):
  - `state.rs`: a config without `prefix` is `config_prefix_missing`; `prefix = ""` parses to `""`;
    the existing accept and reject tables still pass with the line added.
  - `harness.rs` (real binary): `daemon` with a config without `prefix` exits 2 with exactly
    `memory: config_prefix_missing\n` and empty stdout; the same for `check`, `export <owner> --out
    DIR`, `restore <owner> --out DIR` and `owners` (a loop or one test each; no network call:
    the endpoint is `http://127.0.0.1:9`).
  - `s3_store/assemble.rs` or `s3_store/event_loop.rs` (fake store): a daemon with `prefix = ""`
    starts, captures one session and the fake holds its keys at the bucket root
    (`/bkt/sessions/...`, `/bkt/manifests/...` or as the layout names them, `/bkt/objects/...`),
    with no key under any other prefix; `memory restore` and `memory owners` with `prefix = ""`
    find that owner.
  - `s3_store/owners.rs`: `owners_empty_config_prefix_is_exit_2` is replaced by a test that lists
    `sessions/` at the root with `prefix = ""`.
  - `refusals_are_closed_codes_and_leave_no_lock`: its first refusal becomes the missing key, and
    the lock is still free afterwards.
  - Every other existing test passes unchanged apart from the added `prefix` line.
- Teeth (the parent applies each; each must fail a named test): `#[serde(default)]` restored on
  `prefix` (missing becomes `""`); missing mapped to `config_parse`; the daemon's empty-prefix
  refusal restored; the owners refusal restored; the empty prefix replaced by a fixed one inside
  `build_store`.

## Rules for the worker

The rules of [brief 28](28-production-prerequisites.md#rules-for-workers) apply, with those of
briefs 29 to 34. No new dependency. No change to what any state file, manifest or object stores,
to key layout or to log lines. Never inspect, signal or read the state of the running acceptance
daemon or anything under `~/.local/state/memory-acceptance*`; never read `.env`; never contact R2;
never use `git stash`. Cargo only through
`nix develop /Users/mm/dev/memory#rust --command sh -c 'cd /Users/mm/dev/memory-wt/b35-prefix/rust && ...'`,
one cargo job at a time. Commit after each working step.

## Acceptance (parent)

Whole diff read; fmt, clippy, rustdoc, nextest, doctests, schemas, `cargo deny`, lints; the teeth
above; `scripts/gates.sh` once on the tip with `MEMORY_ANALYTICS_PYTHON` set; the parent updates
the `config.toml` row of DESIGN 9.1 (it names the keys) and the handoff; push (secret check of the
range), hosted gates, merge.

## Ledger

- 2026-11-18 09:20Z: brief written on `7a6221d`.
- 2026-11-18 09:43Z: PREFIX accepted locally at `616132b` (worker, one commit). The five refusal
  tests are one real-binary test looping over the commands. Teeth 5 of 5. Full gates on `616132b`:
  14 sub-gates 0, seed 2480786143, coverage 96.69 %. DESIGN 9.1 `config.toml` row updated by the
  parent. Record: [evidence 26](evidence/26-prefix-guard/). Next: push, hosted gates, merge.
