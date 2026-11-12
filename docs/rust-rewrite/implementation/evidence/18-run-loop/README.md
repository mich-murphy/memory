# Evidence 18: run loop, `memory daemon` and `memory restore` (brief 27, R1 to R4)

Date 2026-11-12. Local only, nothing pushed. The branches sit on top of `30f02f6`, the top of
pushed stack #95, in this order:

| Branch | Tip | Content |
|---|---|---|
| `docs/design-fold` | `dc3833f` | Amendments folded into DESIGN.md, hash pin removed |
| `docs/run-loop-brief` | `c25df8a` | [Brief 27](../../27-run-loop.md) |
| `rust/run-config` | `bd25c4e` | R1: a daemon built from `config.toml` and the token file |
| `rust/run-loop` | `9c8355b` | R2: the run loop, signals, `memory daemon` |
| `rust/restore-cmd` | `038c714` | R3: `memory restore <owner> --out DIR` |
| `rust/daemon-restore-e2e` | `2c10eb0` | R4: daemon, SIGTERM, restore on the real binary |
| `docs/run-loop` | this record | |

Over `c25df8a` the four code branches change 37 files under `rust/`: R1 +1148/−16, R2
+708/−54, R3 +1072/−30, R4 +317/−0.

## Commits

| Increment | Commit | By | Change |
|---|---|---|---|
| R1 | `7c59b96` | worker | `Config` gains `endpoint`, `region`, `prefix`; its `Debug` omits the endpoint |
| R1 | `6d50460` | worker | Token file reader: TOML, 0600 checked on the open handle, 4096-byte bound, redacting `Debug` |
| R1 | `b00e3d9` | worker | `SystemClock` over `Instant` and `SystemTime` |
| R1 | `93f6e3f` | worker | Configured roots become locator `Roots` through `memory_core::coordinates` |
| R1 | `6499a3c` | worker | `assemble::start_from_config`; end-to-end test through the local S3 server |
| R1 | `53393c3` | worker | Overlapping configured roots are refused (review finding) |
| R1 | `ef206af` | worker | Review fixes: doc, `Debug` test, namespace assertion |
| R1 | `bd25c4e` | parent | `rust/README.md` brought up to date |
| R2 | `e97a6f5` | worker | `signal-hook` `=0.4.6`, `iterator` feature only |
| R2 | `eca0c41` | worker | `run.rs`: startup delay, loop, stop |
| R2 | `9c8355b` | worker | `memory daemon` wired; start and stop tests on the real binary |
| R3 | `32082d9` | worker | An owner's text form (its key prefix) and its parser, with a round-trip property |
| R3 | `25b4af2` | worker | One function builds the S3 store from the config, for the daemon and for restore |
| R3 | `17233fd` | worker | `memory restore`: library, entry and tests |
| R3 | `6ec12c2` | worker | The hook test expects `restore` to be wired |
| R3 | `038c714` | parent | The missing-config test asserts the exact code |
| R4 | `8d6ec96` | worker | Two tests on the real binary: capture, append, SIGTERM, restore; restart between |
| R4 | `2c10eb0` | parent | The restart test counts uploaded session bytes |

R2 and R3 were built in parallel from `bd25c4e`. R3 was rebased onto R2; the only conflict was
`main.rs` (the header and the two command arms).

## What the binary does now

- `memory daemon` registers SIGTERM and SIGINT, takes the lock, builds its store, clock,
  sources and policy context from `<state root>/config.toml`, waits 5 s to 60 s (uniform, from
  one splitmix64 draw over the clocks and the pid), runs one safety reconcile and then one
  every 15 minutes. It exits 0 only on a requested stop, after `Daemon::shutdown`. Exit 2 is a
  config refusal, 75 a held lock, 1 anything else. Each failure writes one line,
  `memory: <closed code>`, to stderr; stdout stays empty.
- The daemon refuses an empty key `prefix` (`config_prefix_required`), so it cannot write
  outside an acceptance prefix until the A13.2 prerequisites exist.
- `memory restore <owner> --out DIR` takes the owner's stored key prefix, reads the manifest
  chain through `memory-reader` with GET requests only, validates every source path before the
  first write, and writes the latest epoch of each live source to `DIR/<source path>` (files
  0600, directories 0700). It refuses a non-empty `DIR` and colliding paths, and removes what
  it created when a write fails. Exit 64 is a usage error, 66 an absent owner, 2 a config or
  token refusal.
- `MEMORY_SIM_TIMING=<startup ms>,<interval ms>` replaces the two durations only when the
  crate is built with the `sim` feature, which tests enable and the release binary does not.

## Checks

- `gates/`: full local gates (`scripts/gates.sh`, 500 cases), all 13 sub-gates 0 on each tip.

  | Tip | Seed | Tests (nextest + Hegel + proptest) | Doctests | Coverage |
  |---|---|---|---|---|
  | `bd25c4e` (R1) | 1799213729 | 486 (428 + 30 + 28) | 12 | 6285/6442 = 97.56 % |
  | `038c714` (R2 + R3) | 2438750836 | 517 (457 + 31 + 29) | 12 | 6596/6775 = 97.36 % |
  | `2c10eb0` (R4) | 2202692434 | 519 (459 + 31 + 29) | 12 | 6596/6775 = 97.36 % |

  The three production mutants and the window mutant are killed on each tip.
- The parent's seven checks (fmt, clippy with `-Dwarnings`, rustdoc with `-Dwarnings`, nextest,
  doctests, `cargo xtask schemas --check`, `cargo deny --locked check`) all exit 0 on
  `ef206af` (486 tests), `9c8355b` (503), `0a91639` (R3 before the rebase, 500), `038c714`
  (517), `8d6ec96` (519) and `2c10eb0` (519). No gate runs rustdoc.
- The lockfile gained `signal-hook` 0.4.6 and `signal-hook-registry` 1.4.8 and nothing else
  moved. Both are MIT OR Apache-2.0.
- `teeth.txt`: 22 parent-run mutants, each failing the test that guards it. One note: the
  `extend-from-zero` mutant is caught by the bounded wait (the append never commits), not by
  the byte comparison. `append-as-epoch` is the mutant that commits and restores correctly
  while sending old bytes again; only the byte count added in `2c10eb0` catches it (10,712
  bytes uploaded for a 6,700-byte file).
- `sim`: the test-built binary contains the string `MEMORY_SIM_TIMING` once; the plain debug
  build and the release build contain it 0 times (`strings`, on `9c8355b`).
- Repeats: `--test harness` 10 runs of 10 passing on `9c8355b`; the `daemon_restore` module 10
  of 10 on `2c10eb0`. No `memory daemon` process was left after any run.
- R4 request counts in one worker run (not asserted by the test): 4 for the first capture, 3
  for the append round, 5 GET for the restore.
- Not run: 2,000 cases, hosted CI, `cargo mutants`, anything against R2 (local server only).

## Known limits

- The loop is the safety reconcile only. The watcher and scheduler are not connected, so a
  change is captured up to 15 minutes late.
- No wake-gap detection (DESIGN 7.1). The wait counts monotonic time, which excludes suspend,
  so after a sleep the next reconcile is late by the time slept.
- A stop is seen between rounds, not inside one. A round's requests each have a timeout
  (30 s plus body time at 1 MiB/s, at least 60 s), but a round can make many, so a stop during
  a long round can take minutes. A second signal is queued and ignored. Read from the code,
  not tested.
- `signals_unavailable` and `state_root_unresolved` (both exit 1) have no test.
- No `tracing`, hint retention or audit yet (R5, the A13.2 prerequisites).
- Left from plan step 3 of report 25: the parked-owner sort, the daemon's direct `std::fs`
  hint reads, `recover_base` delegating to `read_owner`, rustdoc in a gate. `coordinates.rs`
  is now wired (R1).
