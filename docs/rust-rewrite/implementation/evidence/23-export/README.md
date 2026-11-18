# Evidence 23: `memory export` and the consumer gate (brief 32)

Dates are UTC; the work ran from 2026-11-17 22:50Z to 2026-11-18 00:25Z. Status: increments EXPORT
and GATE accepted locally as `1e922d8` (12 commits over `0d12d64`). Nothing was installed, sent to
R2 or run against the acceptance run's state root. The acceptance run `20261117-a28bfa4` kept
running on its own release binary throughout.

[Brief 32](../../32-export-and-consumer-gate.md) sits on `0d12d64` (on top of `main` `988ab88`).

## Commits

`git log --reverse 0d12d64..1e922d8`. "Worker" is a delegated Sonnet session (medium), "parent" the
coordinating session.

| Branch | Commit | By | Change |
|---|---|---|---|
| `rust/export` | `732e68e` | worker 1 | `Timestamp::unix_ns`; `export_role(kind, path)`; oracle tests against a table, `hint::day_number` and `locate` |
| `rust/export` | `b593fd8` | worker 1 | `read_owner_head`; `memory-reader/src/export.rs` (envelope builder, streaming writer); `memory export`; `CodexCompressed` -> `artifact` (parent correction) |
| `rust/export` | `7cedcf0` | worker 2 | typed `Failure::Shared` in place of matching the string `owner_shared` (parent review finding) |
| `rust/export` | `33b2702`, `1c85aa2`, `c9e2be6`, `725bea6` | worker 2 | reader tests (decision table, importer key sets, streaming bound, corruption, limits, head times), binary tests against the loopback fake |
| `rust/consumer-gate` | `f0aba5d` | worker 3 | `Rig::add_root` for Codex and Pi roots |
| `rust/consumer-gate` | `f60b0f8` | worker 3 | the consumer gate: Rust test (Claude, Codex, Pi, canary scan) and `tools/analytics/test_rust_export_journey.py` |
| `rust/consumer-gate` | `441fc12` | worker 3, committed by parent | no indexing in the Rust test (clippy) |
| `rust/consumer-gate` | `d9d3050` | parent | `manifest_sha256` checked against the stored head digest; re-export byte-identical; credential-denylist canaries in an append-class folder |
| `rust/consumer-gate` | `1e922d8` | parent | `gates.sh` sub-gate `10-consumer` (and its exclusion from 04); pinned analytics venv in the `gates` part of `rust.yml`; `docs/quality-gates.md` |

Each of the three workers ran out of turns: worker 1 after the code and before any export test,
worker 2 with its last clippy fix committed, worker 3 with its clippy fix uncommitted (the parent
checked and committed it). No worker report was taken as evidence; every check below was run by the
parent.

## Correction during the work

`export_role` first mapped `CodexCompressed` (`rollout-*.jsonl.zst`) to `main`. The Rust writer stores
those files' on-disk bytes, which are zstd-compressed (its only zstd call compresses pieces,
`memory/src/environment.rs:161`); Go decompressed them before upload (`internal/syncer/prepare.go:147`).
Exported as `main`, the importer would parse compressed bytes as JSONL. The parent steered worker 1
(brief amended, `11c2688`); uptake is in `b593fd8` with a named row and the oracle exception.

## EXPORT: parent checks on `725bea6` (identical `rust/` tree to the pre-rebase `f33770a`)

- fmt, clippy `-Dwarnings`, rustdoc `-D warnings`, nextest without properties (692 passed), doctests,
  hegel (31), proptest (33), `cargo xtask schemas --check`, `cargo deny` and core bans, lints, window
  mutation and the four mutation predicates: all 0.
- Teeth, applied one at a time by a parent script, each reverted (worktree clean after), 13 of 13 caught:

| Tooth | Failing test(s) |
|---|---|
| `sequence = seq` | `unit_identity_digest_sequence_times_and_coverage`, `shared_owner_heads_and_overflowing_sequence_are_refused` |
| `revision = n` | `sources_one_per_epoch_ordered_deleted_skipped_with_role_state_revision` |
| state always `active` | same |
| `object.size` = encoded size | `object_descriptors_for_zero_one_and_several_pieces` |
| multi-piece commitment reversed | same |
| budget with wrapping `+` | `limits_refuse_before_anything_exists` |
| destination created before the limits | `export::the_binary_exports_a_session_and_its_exit_table`, `export::a_corrupt_object_exits_1_and_leaves_no_directory` |
| existing destination allowed | `an_existing_destination_is_refused_and_left_unchanged` |
| final epoch digest check removed | `every_decode_corruption_a_missing_object_a_gap_and_bad_epoch_totals_leave_nothing` |
| Claude subagent -> `main` | `export_role_named_rows`, `sources_one_per_epoch_…` |
| deleted source exported | `sources_one_per_epoch_…`, `object_descriptors_…`, `bodies_paths_digests_and_destination_contract` |
| exit 1 instead of 4 for too large | `export::the_binary_exports_a_session_and_its_exit_table` |
| `CodexCompressed` -> `main` | `export_role_equals_the_locator_role_for_every_short_path`, `sources_one_per_epoch_…` |

- Streaming bound: a 70-piece epoch is exported with a fetch whose buffers count themselves; no stored
  buffer is alive at any fetch, the peak is 1.
- The three real-binary export tests: 30 of 30 alone (about 0.9 s per run).

## GATE: what it proves

Real writer (`start_from_config` + `reconcile_once` against the loopback fake, Claude, Codex and Pi
roots) -> the real `memory export` and `memory restore` binary -> the unchanged importer and report:

- five owners land; each export's active bodies equal the files on disk, and `memory restore` agrees;
- `manifest_sha256` and `publication_id` equal the digest `current.json` names, which is checked against
  the stored manifest bytes; a second export of the same head is byte-identical;
- Python half (`test_rust_export_journey.py`, run by the Rust test, which requires `Ran ` and `OK` and
  rejects `skipped`): import is `imported` with the expected source, event, issue and role counts; a
  second import is `unchanged`; report `session-analytics-report/v2` with no failures; a Claude
  subagent and a Pi task have no human events; the tool-results artifact gives one
  `artifact_not_normalized` and no events; Codex mirror pairing marks one mirror; an append gives a
  higher `sequence` and a new `publication_id`; a truncate-and-rewrite gives `sealed` and `active`
  epochs that both import; a flipped body byte is `body_integrity` and keeps the old projection; a
  non-ASCII session id and path import under the independently computed `session_key`;
- canaries (eight) only where the writer must withhold them: `.env` and `key.pem` beside sessions, an
  unknown-kind file, a `.jsonl.bak`, a Codex `auth.json`, a symlink to a file outside the root, and
  `.env` and `id_rsa` inside `tool-results/`, where only the credential denylist withholds them. The
  scan reads `metadata.json`, every body and all bodies concatenated, checks each canary file exists,
  and has a positive control (transcript text is found).

Gate teeth, applied by the parent to EXPORT and privacy code on the GATE branch. The first pass caught
5 of 7: an unstable `manifest_sha256` and a skipped credential denylist survived, because the gate did
not compare the digest with the stored head and every canary was withheld by an earlier rule. After
`d9d3050`, 7 of 7 fail `consumer_gate_claude_codex_pi_export_imports_and_reports` (the denylist tooth
also fails `consumer_gate_withheld_canaries_never_reach_an_export`):

| Tooth | Importer or test signal |
|---|---|
| `encoding` `"zstd-3"` | `invalid_metadata` |
| artifacts as `transcript` | Python assertion (roles and issues) |
| `optional` true for `main` | `invalid_metadata` |
| a body `sha256` wrong | `body_metadata_mismatch` |
| a source dropped from `bodies` | `body_coverage` |
| `manifest_sha256` not the stored head digest | Rust assertion (head digest) |
| credential denylist skipped (`privacy/mod.rs`) | canary `leaked` |

- Stress: the two `consumer_gate_` tests 30 of 30 alone, about 2.2 to 2.4 s per run.
- Without the venv and without `MEMORY_REQUIRE_CONSUMER` the test prints `consumer gate skipped`;
  with `MEMORY_REQUIRE_CONSUMER=1` and a missing interpreter it fails
  (`MEMORY_REQUIRE_CONSUMER=1 but the analytics python or duckdb is unavailable`).

## Full gates on `1e922d8`

`nix develop .#rust --command sh -c 'cd rust && MEMORY_ANALYTICS_PYTHON=<repo>/.analytics-venv/bin/python
PROPERTY_CASES=500 bash scripts/gates.sh'`: all 14 sub-gates 0 (`01-fmt` to `09-lints` and the new
`10-consumer`), seed 900846677, 500 cases, 04-nextest 692 passed, 10-consumer 2 passed, coverage
96.69 % (8,810 of 9,112 lines). The local venv holds duckdb 1.5.6, the version `requirements.txt`
pins.

## Limits and open findings

- Per-source times are the head manifest's `captured_at`; no per-epoch time is stored.
- A multi-piece epoch's `object` descriptor is a documented commitment, not a fetchable object.
- A Codex rollout that Codex has recompressed (`.jsonl.zst`) reaches the importer as an unparsed
  artifact; Go decompressed it. Decompressing at capture or export is an open decision.
  (Later, 2026-11-18: decided as export, DESIGN D46; built in brief 37, evidence 28.)
- No Rust equivalent of the Go refresher's heads pages or `capture-v1` CLI; export is one owner per
  call and cutover needs a way to enumerate owners.
- Not run: 2,000 property cases, `cargo mutants`, the gate on real data (after the acceptance run),
  anything against R2.

## Hosted CI, first push (stack #151, PRs #147 to #150)

On `1e922d8` (run 44207967760) the Linux gates and every coverage job passed, `10-consumer`
included, but "Rust gates (macos-26)" ran into the 30-minute job limit: the runner's `python3` is
3.15, duckdb 1.5.6 publishes wheels only up to cp314, and pip built it from the source tarball for
18 minutes before the gates step started. Fix `b043b38`: `actions/setup-python` (v7.1.0, pinned by
SHA) provides Python 3.13 for the venv only (`update-environment: false`), and pip runs with
`--only-binary=:all:`, so a missing wheel fails in seconds instead of building. actionlint and
zizmor (pedantic) are clean on the change.
