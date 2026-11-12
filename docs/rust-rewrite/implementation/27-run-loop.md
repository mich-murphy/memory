# 27. Run loop: `memory daemon` runs the skeleton path, `memory restore` reads it back (2026-11-12)

Coordinator brief. The parent session owns this file and acceptance. It follows
[report 26](26-walking-skeleton.md) (the path exists as a test) and
[evidence 17](evidence/17-structural-fixes/). Status: increment R1 dispatched.

## Goal

`memory daemon` builds its store, clock, sources and policy context from `config.toml` and the
token file, runs the safety reconcile of DESIGN 10.3, and stops cleanly on a signal.
`memory restore <owner> --out DIR` reads an owner back through `memory-reader`. A test spawns
the real binary against the local S3 server, captures a file, stops the daemon, restores the
owner and compares bytes.

Not in this brief: the coalescer and watcher driving per-owner captures (`Daemon` has no
per-owner entry yet), the audit (A12.2), hint retention (A11.2), `tracing` (A13.2). Until those
exist no production write is allowed (A13.2), which R1 enforces by refusing an empty key prefix.

## Checkout

- Base `30f02f6` (`docs/structural-fixes`, top of stack #95, PRs #83 to #94, in hosted CI; a
  Sonnet worker watches it and merges when the ready rule holds).
- This brief: branch `docs/run-loop-brief`, worktree `/Users/mm/dev/memory-wt/rl-brief`.
- R1: branch `rust/run-config` on top of this brief, worktree `/Users/mm/dev/memory-wt/sk-s3`.
- In parallel, unrelated: `docs/design-fold` in `/Users/mm/dev/memory-wt/sk-zstd` folds the
  amendments into DESIGN.md (operator approval 2026-11-12).
- Do not change the checked-out branch of `/Users/mm/dev/memory-wt/ds-integrate` while the
  merge worker is running.

## Operator decisions, 2026-11-12

- Push the stack, open PRs, merge when CI is ready (done as stack #95).
- Annotation 3 (`/no-comments`) is dropped.
- Fold the amendments into DESIGN.md; remove the baseline hash pin.
- Minimum delay before a restarted daemon's first commit: 5 s (A13.4).
- The 2-day criterion of A13.1 also replaces the 7-day shadow runs of DESIGN 17 slices 2b and 3.
- Capture staying off (A0) needs no separate bound. Session files stay on disk for the Rust
  daemon's first reconcile; the remedy is to deliver this brief.

## Decisions the parent made where the design is silent

Each can be reversed before the first production write without migrating data, because only
`rust-acceptance/` prefixes are written until then.

| Topic | Decision | Reason |
|---|---|---|
| Storage settings | `config.toml` gains `endpoint` (required; `https`, or `http` only for a loopback host), `region` (default `auto`) and `prefix` (default empty; otherwise ends with `/` and does not start with `/`) | `Config` had only `bucket` and `token_path`; `S3Store::new` needs the rest. No environment override (A5 allows only `MEMORY_STATE_DIR`) |
| Token file | TOML with exactly `access_key_id` and `secret_access_key`, unknown fields rejected, size bounded, read only after the existing 0600 check | DESIGN 6.1 says only "a 0600 file named by the config". The Go file mixed the endpoint and bucket with the secret |
| Root namespace | `<agent>-<32 hex of sha256(root)>` from `memory_core::coordinates` (`Inventory::compile` then `resolve`), with no aliases or observations yet | The rule is already written and tested (report 11) and report 06 named a root hash as the intended namespace. This wires `coordinates.rs` instead of deleting it. The namespace is part of session and source ids, so it is persisted |
| Root layout | Every configured root is `RootLayout::AgentDir` | `ClaudePrimaryHome` serves a config kind withheld until slice 3; `PiCustomRoot` needs the learned `roots.json`, which has no code |
| Production clock | Monotonic from `std::time::Instant`, wall from `SystemTime` | std documents `CLOCK_UPTIME_RAW` on Darwin and `CLOCK_MONOTONIC` on Linux (read 2026-11-12); both exclude suspend, as DESIGN 7.1 requires. std says this can change between versions, so the comment names the pinned toolchain |
| Production-write guard | The daemon refuses an empty `prefix` with a config-class exit until the A13.2 prerequisites exist | Makes the acceptance-only rule hold by construction |
| First loop | Startup delay of 5 s plus jitter up to 60 s in total, one reconcile, then one every 15 min, stop flag checked between rounds, `Daemon::shutdown` on stop | DESIGN 10.3 and 11.2, A13.4. The coalescer path needs a per-owner entry in `Daemon` and is a later increment |
| Test-only timing | Shorter delay and interval only under the existing `sim` cargo feature, never in the release binary | The binary test must not wait up to 60 s, and production defaults must not weaken |
| Signals | `signal-hook`, pinned exactly, safe API only | The workspace forbids unsafe code; nothing in the lockfile handles signals |
| Owner on the command line | The owner's key prefix as stored: `sessions/<archive>/<machine>/<agent>/<session_id>` or `shared/<archive>/<machine>/<agent>` | `Owner` has no text form; this one mirrors `current_key` and can name another machine's owner |
| Restore output | One file per live source at `DIR/<source path>`, latest epoch, transformed bytes; refuses a non-empty `DIR` and a path collision | Smallest layout that cannot overwrite silently |

## Increments

| # | Increment | Owner | State |
|---|---|---|---|
| R1 | Config fields, token reader, production clock, roots to `Roots`, policy context with collector identities, one builder for the daemon's `Parts`; test through the local S3 server | Sonnet worker | dispatched |
| R2 | `run.rs`: startup delay, loop, signal stop, exit codes; `memory daemon` calls it; binary start-stop test replaces the exit-78 test | Sonnet worker, after R1 | not started |
| R3 | `memory restore <owner> --out DIR`; owner key-prefix parser with a round-trip property | Sonnet worker, after R1, beside R2 | not started |
| R4 | Binary end-to-end test: daemon captures, SIGTERM, `memory restore`, bytes equal | after R2 and R3 | not started |
| R5 | `tracing` to stderr, hint retention, the audit (the A13.2 prerequisites) | later brief or extension | not started |

## Acceptance (parent)

For each increment the parent reads the whole diff, reruns fmt, clippy, rustdoc, nextest,
doctests, schemas and `cargo deny` on the tip, checks the mutation predicates still occur once,
reruns at least one teeth check per new guarantee, and runs `scripts/gates.sh` on the top of
the stack. Workers do not run `gates.sh`, coverage or `cargo mutants`.

Privacy criteria for R1 in particular: no error, `Debug` output or log line carries the secret,
the key id or the endpoint; a config or token file that sits under a configured root is never
uploaded.

## Exclusions

No host changes, no installed hooks or services, no reads of agent transcripts, `.env` or
credential files, no R2 access in R1 to R4 (local server only).

## Progress ledger

- 2026-11-12: brief written; R1 dispatched.
