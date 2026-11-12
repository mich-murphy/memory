# 27. Run loop: `memory daemon` runs the skeleton path, `memory restore` reads it back (2026-11-12)

Coordinator brief. The parent session owns this file and acceptance. It follows
[report 26](26-walking-skeleton.md) (the path exists as a test) and
[evidence 17](evidence/17-structural-fixes/). Status: R1 to R4 done and accepted locally
([evidence 18](evidence/18-run-loop/)); R5 not started.

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

State on 2026-11-12 after R4. Everything up to `30f02f6` was stack #95 (PRs #83 to #94) and
is merged into `main` as `cf99a3d`. Everything above it is the next stack, in this order.

| Branch | Tip | Content |
|---|---|---|
| `docs/design-fold` | `dc3833f` | Amendments folded into DESIGN.md (operator approval 2026-11-12) |
| `docs/run-loop-brief` | `c25df8a` | This brief as first written |
| `rust/run-config` | `bd25c4e` | R1 |
| `rust/run-loop` | `9c8355b` | R2 |
| `rust/restore-cmd` | `038c714` | R3, rebased onto R2 |
| `rust/daemon-restore-e2e` | `2c10eb0` | R4 |
| `docs/run-loop` | | This update and evidence 18 |

## Operator decisions, 2026-11-12

- Push the stack, open PRs, merge when CI is ready (done as stack #95).
- Annotation 3 (`/no-comments`) is dropped.
- Fold the amendments into DESIGN.md; remove the baseline hash pin.
- Minimum delay before a restarted daemon's first commit: 5 s (A14.1, which settles the open
  point of A13.4).
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
| First loop | Startup delay drawn uniformly from 5 s to 60 s, one reconcile, then one every 15 min, stop flag checked between rounds, `Daemon::shutdown` on stop | DESIGN 10.3 and 11.2, A14.1. The coalescer path needs a per-owner entry in `Daemon` and is a later increment |
| Test-only timing | Shorter delay and interval only under the existing `sim` cargo feature, never in the release binary | The binary test must not wait up to 60 s, and production defaults must not weaken |
| Signals | `signal-hook`, pinned exactly, safe API only | The workspace forbids unsafe code; nothing in the lockfile handles signals |
| Owner on the command line | The owner's key prefix as stored: `sessions/<archive>/<machine>/<agent>/<session_id>` or `shared/<archive>/<machine>/<agent>` | `Owner` has no text form; this one mirrors `current_key` and can name another machine's owner |
| Restore output | One file per live source at `DIR/<source path>`, latest epoch, transformed bytes; refuses a non-empty `DIR` and a path collision | Smallest layout that cannot overwrite silently |
| Overlapping roots (R1 review) | Refused at startup (`config_root_overlap`), compared by path component | The walker pairs a file with every root above it, so a file under two roots would be uploaded under two owners |
| Restore and the prefix (R3) | `restore` accepts an empty `prefix`; only the daemon refuses it | Reading is not a production write |
| Arguments (R3) | Parsed by hand; `clap` waits for a later increment | One positional and one flag |
| Broken signal channel (R2) | The wait reports a failure and the loop exits 1 (`wait_failed`) after a best-effort shutdown | A dead signal thread must not look like a clean stop |

## Increments

| # | Increment | Owner | State |
|---|---|---|---|
| R1 | Config fields, token reader, production clock, roots to `Roots`, policy context with collector identities, one builder for the daemon's `Parts`; test through the local S3 server | Sonnet worker | done, `bd25c4e` |
| R2 | `run.rs`: startup delay, loop, signal stop, exit codes; `memory daemon` calls it; binary start-stop test replaces the exit-78 test | Sonnet worker, after R1 | done, `9c8355b` |
| R3 | `memory restore <owner> --out DIR`; owner key-prefix parser with a round-trip property | Sonnet worker, after R1, beside R2 | done, `038c714` |
| R4 | Binary end-to-end test: daemon captures, SIGTERM, `memory restore`, bytes equal | Sonnet worker, after R2 and R3 | done, `2c10eb0` |
| R5 | `tracing` to stderr, hint retention, the audit (the A13.2 prerequisites) | later brief or extension | not started |

## Result, R1 to R4 (2026-11-12)

The goal's test exists and passes. `rust/crates/memory/tests/s3_store/daemon_restore.rs`
spawns the real binary against the local S3 server, captures a file, appends to it, stops the
daemon with SIGTERM, runs `memory restore` and compares bytes. A second test restarts the
daemon between the two writes and checks that the session's bytes were uploaded exactly once.
Full gates pass on `2c10eb0` (500 cases, seed 2202692434, 519 tests, coverage 97.36 %).
Commits, checks, teeth and known limits are in [evidence 18](evidence/18-run-loop/).

What the work showed that the next brief has to settle:

- The loop is the safety reconcile only, so a change is captured up to 15 minutes late until
  the watcher and scheduler drive a per-owner capture.
- There is no wake-gap detection (DESIGN 7.1). The wait counts monotonic time, which excludes
  suspend, so the reconcile after a sleep is late by the time slept.
- A stop is seen between rounds, not inside one. A long round can delay it by minutes; a
  prompt stop needs a check between owners or requests.

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
- 2026-11-12: R1 accepted at `bd25c4e` after one review round (overlapping roots refused);
  rebased onto this brief; full gates pass.
- 2026-11-12: R2 (`9c8355b`) and R3 (`0a91639`, `038c714` after the rebase onto R2) built in
  parallel and accepted; full gates pass on `038c714`.
- 2026-11-12: R4 accepted at `2c10eb0`; the parent added the uploaded-byte count to the
  restart test. Full gates pass. Record in evidence 18.
- 2026-11-12: stack #95 merged into `main` as `cf99a3d`; these branches are submitted as the
  next stack. Next: R5.
