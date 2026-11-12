# 08. Hegel property-based testing audit (rust/ workspace)

Date: 2026-11-10. Repo HEAD 6ea015e (branch rust/coverage-floor-80), read-only. All experiments ran in a scratchpad copy (`/private/tmp/claude-501/-Users-mm-dev-memory/83693829-eddb-4e4e-a499-380d68c821f5/scratchpad/review/08/rust`, own target dir). Scripts and raw results are in that directory (`mut.py`, `seeds.py`, `res-*.txt`, `seeds-*.txt`, `k3-full.txt`, `p1s1.txt`, `baseline-*.txt`).

Labels: (F) verified fact, (I) inference, (Q) open question. Severity/confidence per finding.

## 0. Summary verdict

Hegel is used competently where it is used as a state-machine engine, and poorly or decoratively elsewhere.

- Strong (F): the three `#[hegel::state_machine]` properties (`capture_property.rs`, `commit_protocol.rs`, `daemon.rs`) are independent-oracle, model-based tests with fault injection, a coverage guard with negative controls, and good shrinking. In my 21-mutant experiment they killed 7 of the 13 production-code mutants within 2 to ~350 cases, and the shrunk counterexamples are 4 steps long and readable.
- Weak (F): the properties do not reach the regions where the design's riskiest decisions live. `capture_property` never produces a file above 64 bytes in 99.6% of cases and never above 4,096 bytes, so the 4,096/65,536-byte head/tail windows are always "the whole file". `daemon.rs` does reach big files but only 3 of 8 seeds at 500 cases catch a "tail window ignored" bug (3 of 4 at 2,000). No memory-crate state machine can express rename-replace (identity change), and a mutant that ignores identity change survives all memory-crate Hegel tests and examples.
- Decorative (F): `privacy_property.rs` is a 16-row table test with a random hex canary substituted into a fixed template. It adds nothing over the example test next to it. The "generators" in `core_mechanics` (property 1), `core_manifest`, `core_hint::no_canary` and `privacy_property` are one integer seed fed to a hand-rolled PRNG or a fixed 10-scenario script, so Hegel's shrinking and structure-aware generation do nothing.
- Of 21 injected bugs, Hegel alone caught 12. Nine escaped Hegel; example tests caught 7 of those; two escaped everything.
- Configuration is mostly good (random seed per CI run, recorded and uploaded; blob printing; 500-case floor) with three real gaps: no failure-database or regression pinning for the most valuable properties (`Hegel::new` without `database_key`), the coverage guards pass on events that cannot exercise the bug class, and a default `cargo test` run (100 cases) can fail the daemon guard (verified, seed 13).

Overall: "strong value" for commit protocol, crash/lost-reply and append/rewrite capture logic; "little or no value beyond examples" for privacy, limits and several seed-driven properties; "boundary region untested by properties" for windows, which only a single pure-planner table (core_mechanics property 1) covers.

## 1. What Hegel is and how it is meant to be used (primary sources fetched this session)

Crate: `hegeltest` (library name `hegel`). Pinned `=0.52.0` with feature `static-engine` in `rust/Cargo.toml:23`; Cargo.lock has `hegeltest`, `hegeltest-c` and `hegeltest-macros`. docs.rs `latest` is 0.52.1 (F). The GitHub repo `hegeldev/hegel-rust` was archived 2026-10-19 and the Rust frontend now lives in the `hegeldev/hegel` monorepo (`frontends/rust/`, `libhegel/`); the crate names are unchanged and new releases are published only from the monorepo (F, github.com/hegeldev/hegel-rust).

Sources:
- docs.rs crate root: https://docs.rs/hegeltest/latest/hegel/
- Settings: https://docs.rs/hegeltest/0.52.0/hegel/struct.Settings.html
- Stateful: https://docs.rs/hegeltest/0.52.0/hegel/stateful/index.html
- Changelog: https://raw.githubusercontent.com/hegeldev/hegel/main/frontends/rust/CHANGELOG.md
- Landing page: https://hegel.dev (no Rust guide content; "How Hegel works" URL guess returned 404)
- Hypothesis reference: https://hypothesis.readthedocs.io/en/latest/reference/api.html
- Hypothesis stateful: https://hypothesis.readthedocs.io/en/latest/stateful.html

Verified facts from them:
- Generators: primitives (`integers`, `floats`, `text`, `booleans`) with `min_value`/`max_value`/`max_size`; combinators `vecs`, `tuples`, `one_of`, `compose`; custom via `#[hegel::composite]`, `#[hegel::compose]`, `#[derive(DefaultGenerator)]`; later draws may depend on earlier ones; `tc.note` prints only when replaying the minimal failing case; `tc.event`/`tc.event_value` record statistics (HEGEL_STATISTICS).
- Shrinking: failures are shrunk to a minimal counterexample. With `stop_on_first_failure=false` only the smallest failure is shrunk and reported (0.50.1).
- Database: enabled by default; `Settings::database` (`Database` enum, 0.51.0) and `database_key`. "a hand-built `Hegel` without a key neither stores nor replays examples." Failures can be replayed with `reproduce_failure("<blob>")`; `print_blob` defaults true.
- Settings and defaults: `test_cases` 100; `seed` random; `derandomize` off, but the shipped `ci` profile turns it on; profiles `development`/`ci`/`workload`, chosen from the environment (`ci` on a CI server); env vars `HEGEL_TEST_CASES`, `HEGEL_SEED`, `HEGEL_DERANDOMIZE`, `HEGEL_PRINT_BLOB`, `HEGEL_STATISTICS`, `HEGEL_CONFIG`, `HEGEL_SUPPRESS_HEALTH_CHECK`; env overrides profiles but NOT values set in test code. Health checks `TestCasesTooLarge`, `FilterTooMuch`, `TooSlow`. Choice limit 2^20 per case. I found no documented `deadline` setting (Q: unverified whether it exists).
- Stateful: `#[hegel::state_machine]` with `#[rule(weight = w)]` (a suggestion, not a guarantee), `#[invariant]` (run on initial/final state and sampled in between; `always_run` checks every step), `machine(m).steps(n).run(tc)`; "A failed assumption doesn't undo changes already made in a rule: put `tc.assume` at the start". Concurrent machines exist (`concurrent_state_machine`, `run_concurrent`), plus value pools. 0.52.1 redraws rule sets and concurrency between rounds.
- Backend: the engine `libhegel_c` is a shared library built by the build script; `static-engine` links it in instead. Pitfalls documented: engine version must match the crate exactly; static-engine unifies the engine's dependencies into yours (example: a `PartialEq<serde_json::Value>` impl makes `assert_eq!(true, ...)` ambiguous); `TestCase` is Send but not Sync; `HEGEL_TEST_CASES` does not override `test_cases` set in the attribute.
- Provisioning here (F): `static-engine` means no external backend (no Python/uv download, no `HEGEL_C_LIB_DIR`). A fresh scratch build with the repo toolchain compiled in ~40 s (`cargo test -p memory-core --test privacy_property --no-run --locked`). CI runs `nix develop ... bash scripts/gates.sh` (`.github/workflows/rust.yml`), ubuntu x86_64/arm64 and macOS. I did not inspect flake.nix beyond a grep (no Hegel-specific entry).

General best practice sources:
- Hypothesis health checks (F): `filter_too_much` "when the test is filtering out too many test cases, either through use of assume() or .filter()"; `data_too_large`, `too_slow`; "all health checks warn about performance problems, not correctness errors". Database is "a cache that you never need to invalidate" and "Do not rely on the database for correctness; to ensure Hypothesis always tries an input, use @example." The built-in CI profile derandomizes, disables the deadline and the database, and prints reproduction blobs.
- Hypothesis stateful (F): model comparison ("a simplified in memory model of its behaviour"), invariants after every step, `precondition()` preferred over `assume()` "when only a few rules are applicable".
- QuickCheck manual (https://www.cse.chalmers.se/~rjmh/QuickCheck/manual_body.html; seen only via a search-result summary, not fetched directly): `classify`/`collect` report the distribution of test data; "the `==>` operator can skew the distribution of test data badly"; custom generators control distribution.
- Hughes, "How to Specify It!" (https://research.chalmers.se/en/publication/517894): the abstract (via search) says five approaches, including inductive and model-based properties; metamorphic properties relate outputs for related inputs; a Lambda Days summary reports metamorphic and model-based properties as most effective at finding bugs; the paper stresses testing generators. I could not read the PDF (compressed); the exact five-family names "invariant, postcondition, metamorphic, inductive, model-based" are from the brief and from memory (unverified against the text).

## 2. Inventory: 30 Hegel properties

Counts: 19 in memory-core, 11 in memory. Runtimes are from my 500-case run (`baseline-core.txt`, `baseline-mem.txt`, seed 424242, shared and loaded machine, so upper bounds). Almost every property has a proptest twin (DESIGN.md 15 gate 6). Families per Hughes: I invariant, P postcondition, M metamorphic, Ind inductive, MB model-based.

| Property (file:line) | Claim | Family | Generator | Oracle | Time at 500 |
|---|---|---|---|---|---|
| `hegel_property4_capture_state_machine` (memory/tests/capture_property.rs:1069) | Real `capture()` + `commit()` over a fake store: every committed epoch is a prefix of bytes the source once held; head never goes back; after settle the latest epoch equals the source; deleted sources recorded | MB (stateful) | 18 rules, `steps(40)`, 2 sources, files built by appends of 1-9 bytes (alphabet {0,1,2}), rewrite pos <= 24, piece_len 4 | Reference model + store-only reader (`read_chain`, `reassemble`); independent of capture | 7.8 s |
| `hegel_property4_commit_state_machine` (commit_protocol.rs:1159) | Same properties for the commit protocol with a stand-in planner | MB | same step set, `steps(40)` | same style | 7.2 s |
| `hegel_daemon_state_machine` (daemon.rs:1678) | Daemon rounds on a real temp dir + fake store; same safety/liveness; masks unaudited mid-file rewrites | MB | 16 rules, `steps(12)`, 1 source, bulk appends of 1-8 or 69-90 KiB, zoned rewrites | Reference history + `observe_masked` | 10.9 s |
| `hegel_converged_reconcile_makes_zero_puts` (counters.rs:407) | After convergence, further rounds, restart and delete-index make zero PUTs (DESIGN prop 3) | Ind/P | Rig script, store-counted PUTs | PUT counter at the store | in 6.9 s (2 tests) |
| `hegel_delta_capture_matches_independent_whole_digest` (counters.rs:440) | Resumable digest equals an independent SHA-256 of the final file after appends, index loss, corrupt index | P | 1-6 rounds of 2-3000 lines, 4 index fates | `sha2` of the file | (above) |
| `hegel_property1_independent_capture_and_eligible_audit` (core_mechanics.rs:272) | Planner decisions for 10 edit kinds on ~73 KB files never lose bytes (1a) and audit repairs (1b) | P (table of edge cases) | One `u16` seed; the 10 operations are hard-coded (head 0/4095, tail start/end, rename, truncate-regrow, racy, interior, truncate) | `current.starts_with(reassemble(...))` using a test-local `windows()` | ~41 s with property 2 |
| `hegel_property2_monotonic_quiet_cap_end` (core_mechanics.rs:391) | Coalescer never emits before quiet unless cap; always emits within cap | MB | event script | model | (above) |
| `hegel_scheduler_matches_model` (watch.rs:372) | Scheduler equals a re-written model | MB | `Vec<u32>` script | model repeats `flush || now-last>=60 || now-first>=300` | 0.09 s |
| `hegel_property7_*` x3 (core_locator.rs:1659-1674) | locate total/countable; stable under unrelated facts; unmapped withheld | I / M | `Vec<u8>` decoded by `Pick` into paths, noise facts, a ROWS table | independent path oracle, metamorphic stability | 0.11 s |
| `hegel_roundtrip_of_arbitrary_valid_manifests`, `hegel_deltas_from_checkpoint_equal_next_checkpoint` (core_manifest.rs:1665,1669), `hegel_r2_dedup_winner_descriptor` (:1292) | Manifest round trip; fold of deltas equals checkpoint; R2 dedup winner | Ind (roundtrip, fold), P | one `u64` seed into a local xorshift `Rng` (core_manifest.rs:1307) | independent history | 54 s (3 tests) |
| `hegel_coordinates_order_proof_and_retention` (core_coordinates.rs:603) | Coordinates order/proof/retention | I | bytes + u16 seed | in-test | 0.19 s |
| `hegel_size_bound_and_roundtrip`, `hegel_interleavings_and_truncations`, `hegel_reader_never_panics_on_arbitrary_bytes`, `hegel_no_canary_in_any_line` (core_hint.rs:639-890) | Hint line size bound/round trip; reader on interleaved/truncated input; no panics; no canary in any line | I/P | byte opcode vectors; `no_canary` is `u64` + `u8` seed | size bounds, scanner | 11.1 s (4) |
| `hegel_key_monotonic`, `hegel_admission`, `hegel_budget`, `hegel_cancel` (core_limits.rs:537-564) | Key length model/boundary; admission cap; request budget monotone and >=60 s; cancel keeps streak | I / P | small ints; fixed 1,100 extra ids | arithmetic model (key), set model (admission), monotone+floor only (budget) | 2.4 s (4) |
| `hegel_generated_canaries_do_not_escape_pure_privacy_outputs` (privacy_property.rs:230) | No canary leaves pure privacy outputs | I | selector u8 % 16, nonce u64, salt u64 | substring absence + marker retention | 0.47 s |
| `hegel_checked_ranges_match_independent_iterator_oracle` (scaffold.rs:25) | `slice::bytes` equals an iterator oracle | P | vec + two u8 | iterator oracle | 0.04 s |
| `hegel_write_read_is_identity`, `hegel_single_byte_mutation_never_yields_a_different_body`, `hegel_crash_between_stage_and_publish_reads_old_or_new` (state.rs:886-996) | State file write/read identity; checksum catches single-byte corruption; crash between stage and publish reads old or new | P / I | `Vec<u8>` (<=28 B), crash point, policy | {old,new} + point table | 8.5 s (3) |
| `hegel_read_equals_range_intersection` (realfs.rs:421) | RealFs read equals range intersection | P | vec + two u16 | slice | 0.18 s |
| `hegel_core_never_panics_and_writes_at_most_one_whole_line` (hook.rs:792) | Hook never panics; <=1 whole line | I | stdin bytes, shaped JSON | line count, scan parse | 2.4 s |

Also read: DESIGN.md section 15 (properties 1-7, coverage guard), `docs/rust-rewrite/implementation/16-capture-property4.md` (property 4 design, "2000 cases each", parent correction), and the hegel-probe experiment `docs/rust-rewrite/experiments/hegel-probe/tests/coalesce.rs` (a 2-test probe: a correct reassembly and a deliberately buggy coalescer; shows Hegel shrinks a drifting offset; it is a smoke test of the toolchain, not a property of the product).

Total Hegel wall time at 500 cases: about 2 m 05 s in memory-core plus 1 m 15 s in memory on a heavily loaded machine (load average 50-125 at times).

## 3. Findings (most severe first)

### F1. Window boundaries (4,096 / 65,536) are effectively untested by the stateful properties; only one pure table covers them. Severity high, confidence high.
Evidence (F):
- `capture_property.rs:44-46,978-1000`: 2 sources, appends of 1-9 bytes, 40 steps, piece 4. Instrumented 500-case run: average max file length 8.9 bytes; files > 64 B in 2 of 500 cases; > 4,096 B in 0. Head and tail windows (`planner.rs:10-19`) are `min(4096, at)` and `at-65536..at`, i.e. always the entire prefix. The window logic is therefore equivalent to a whole-file comparison here.
- `daemon.rs:1599-1620` does generate 69-90 KiB bulks (`big` half the time), but only 115/500 cases (23%) reach `> HEAD+TAIL`. Instrumented (seed 424242): 3 cases with a mid-file rewrite after the first commit, 11 with a tail-only rewrite after commit, and only 3 Settle steps that followed an unreplayed tail-only rewrite. Across 8 seeds at 500 cases the daemon observed tail-only-after-commit rewrites 2-21 times and mid-zone-after-commit 0-11 times (seed 1: zero of both, yet see Q1).
- Mutant P1 (`planner.rs:~237`, ignore the tail window): killed by `core_mechanics` property 1 (operation 3) but MISSED by capture_property, commit_protocol, daemon (seed 424242), the memory example suite. Daemon at 500 cases: caught in 3 of 8 seeds (1,3,7); at 2,000 cases in 3 of 4 seeds. The shrunk counterexample (seed 1) is 4 steps (`bulk 69 KiB`, `round`, `rewrite zone=1 delta=0`, `settle`), but shrinking took 87 s because the files are large.
- Mutant P4 (window constants set to 4 bytes): killed by capture_property (case 346 of 500 at seed 424242) and daemon, not by core_mechanics (the planner takes window evidence as input).
- Experiment X1 (F): setting only the window constants to 8/16 bytes plus mutant P1 makes the capture_property state machine catch the bug in 5 of 6 seeds at 500 cases (first failing case 36, 51, 83, 254, 423). Making window sizes small in the test configuration brings the boundary into the reachable region of a cheap, small-file state machine.
Impact: an off-by-one or wrong-range bug in window reading for files above 4 KiB (`capture.rs:246,544,652`, `stream_span` at ~`capture.rs:277`) is caught only by property 1's planner-level table, which does not exercise the shell's reading of windows. Mutant C6 (off-by-one in `stream_span`'s delta window computation) survived everything, Hegel and examples.
Action: add window sizes to `Env` (default 4,096/65,536) and have the state machines run with 8/16 (and the daemon with real sizes in a smaller number of deliberately big-file cases). Keep core_mechanics' real-size table as a boundary test but call it what it is.

### F2. No generator can produce rename-replace (identity change); a mutant that ignores identity change survives all memory-crate properties and examples. Severity high, confidence high.
Evidence (F): DESIGN.md 15 property 1 lists rename-replace and property 4 lists a `rename-replace` step. `capture_property.rs:855-874` and `daemon.rs:1180-1197` have no such step. Mutant P3 (`planner.rs:~225`, `if observed.id != state.observed.id` -> `if false`) was killed only by `core_mechanics` property 1 (operation 5); the memory state machines and the whole memory example suite passed. The identity-change replay path in the real `capture()`/`FileSystem` seam is therefore never exercised end to end.
Action: add a rename-replace step (new `FileId`, new content or same content) to `capture_property` and the daemon model. Verify with P3.

### F3. Coverage guards pass on events that cannot exercise the bug class, and the daemon guard can fail by chance at low budgets. Severity medium, confidence high.
Evidence (F):
- `daemon.rs` `shapes.hit("rewrite-mid-file")` fires when `after.mid` is set (file `> HEAD+TAIL`) regardless of whether anything was ever committed. Seed 1 at 500 cases: zero mid-zone or tail-only rewrites after a commit were counted, and the guard still passes (the property ran "ok").
- At the default 100 cases, seed 13 fails the daemon guard: `coverage guard: run never produced ["rewrite-mid-file"]` (verified; capture_property and commit_protocol passed for seeds 11-13). The docs acknowledge this ("fail the property coverage guards by design") but `cargo test`/nextest locally with no `HEGEL_TEST_CASES` is a trap, and CI at 500 cases with a random seed has a nonzero flake chance on this guard (rate not measured; 6 mid events per 500 cases on average at seed 424242, range 0-11 over 8 seeds).
- The `capture_property` guard counts presence only (`require_all`: any count > 0). Measured per-500-case frequencies: crash-after-manifest 73 effective events in 691 steps (10.6%), lost-reply (AlreadyOurs) 37 in 1,169 steps (3.2%), delete-index 215 effective.
Action: count shapes by what matters (rewrite after commit; tail-only; crash with pending data; mid-file with committed epoch) and require a minimum frequency (QuickCheck `cover`-style), reported once at the end. Alternatively use Hegel's `tc.event` and `HEGEL_STATISTICS` for reporting plus the guard for floors.

### F4. Generator efficiency in the state machines: many steps are no-ops and many cases are empty. Severity medium, confidence high.
`capture_property` (500 cases, seed 424242, 19,760 steps, 39.5 steps/case, 97.8% of cases reach 40 steps):
- 257 of 500 cases (51.4%) never put a byte in any source (`cases-no-bytes-ever`). (I) Consistent with Hegel enabling only a subset of rules per case; the 0.52.1 changelog says the machine redraws "each worker's enabled rules"; I did not verify this for 0.52.0.
- Rewrite and truncate steps: 2,380; 1,656 (69.6%) hit a missing or empty file (no-op); 557 (23.4%) landed after a commit; 167 (7.0%) before the first commit.
- Remove-source: 460/668 (68.9%) on a missing source.
- Crash steps: 1,728/2,532 (68.2%) with no data on disk (no-ops).
- Restart/delete-index before any commit: 1,220/1,964 (62.1%).
- Settle: 2,312 steps; liveness checks ran in 2,133 (92%; 8% waived by foreign writer), but only 369 of those (17%) had a committed epoch, so most liveness checks are vacuous.
- Foreign writer waives liveness in 174/500 cases (34.8%); an owner was parked in 54 (10.8%).
- Files > 64 B: 2 cases; > 4,096 B: 0 (see F1).
`daemon.rs` (500 cases, 12 steps each): 159 cases (31.8%) with no bytes; 337 of 557 rewrite steps (60.5%) on an empty source; 735 of 871 Settle steps (84%) vacuous (no source); only 136 settles with a nonempty source, 96 after a commit.
No `assume`/`filter` is used anywhere (F, grep): no health-check risk, but the equivalent waste appears as no-op steps. The Hypothesis stateful guidance is `precondition()` for rules that apply in few states (F, Hypothesis stateful docs); Hegel's `#[rule]` has no documented precondition in what I fetched (Q), so the equivalent is weighted rules plus drawing the target from existing sources (e.g., choose a source that exists).
Action: bias rewrite/truncate/remove/crash to existing, committed sources; start cases with an initial append; give `steps` larger budgets for the capture machine in a small number of long cases.

### F5. Several "properties" are table or seed tests; Hegel adds no search and shrinking is meaningless. Severity medium, confidence high.
- `privacy_property.rs:230-236`: draws `selector: u8`, `nonce: u64`, `salt: u64`; the selector picks one of 16 fixed fixtures (`:10-56,144-227`), the hex nonce/salt only change the canary text. The structure being redacted (JSON/TOML shapes, key names, nesting, escaping, encodings, lines) is never generated. 500 cases = about 31 runs of each fixture. The sibling example test (`:238-248`) already runs all 16 cases x 5 salts. Mutant V1 (remove the `mcpServers.*.oauth.clientSecret` rule) is killed, but equally by the example. V2/V3/V4 (credential name, `passw`, `key` token removed) are missed by the property and caught by examples. Cases 14 and 15 assert that canaries DO appear in transcripts and other known files (passthrough), so the test name "canaries do not escape" overstates it.
- `core_mechanics.rs:204-272` property 1: 10 hard-coded operations on 73,728-74,751 byte files, parameterised by one `u16`. Good boundary content, but it is a parametrized example test; 500 cases cost about 41 s for what is one scenario table repeated with a shifted length and byte offset. The failing output shows it: `draw_1 = 0`, `operation=3 seed=0` with no structure shrunk.
- `core_manifest.rs:1307,1496,1655-1661`: a `u64` seed feeds a local xorshift `Rng`, so Hegel shrinks only the integer; the roundtrip/fold properties otherwise use their own generator (54 s of the 2 minutes).
- `core_hint::no_canary_in_any_line` (`:889`) is `u64` + `u8` seed into `check_canaries`.
- Several others encode ops as `Vec<u8>` and decode with a `Pick` helper (`core_locator`, `core_hint`, `hook`). This is workable (the vector shrinks by length and byte value) but weaker than typed generators: an opcode byte of 255 versus 254 are unrelated shrink steps.
Severity note: this is a value and honesty issue, not a correctness bug. These tests do detect bugs (V1, P1, P3) but through their fixed tables.
Action: generate structure with Hegel (JSON/TOML documents with canary placed by a generator at a random path, random key names from the protected list and near-misses, text lines with secret-like names and values, unicode, escapes, CRLF). Otherwise rename these to table tests and drop the Hegel wrapper.

### F6. The default `Vec<u8>` size is tiny; Hegel twins test strictly smaller inputs than their proptest twins. Severity medium, confidence high.
Measured (F, `dist_probe.rs` in the scratch copy, 500 cases): `gs::vecs(gs::integers::<u8>())` has p50 6, p90 14, p99 26, max 28, empty in 5.2%. `integers::<u64>()`: 1.6% zero, 1.8% max, 70.6% below 1 MiB, 79.4% below 100 MiB; `u16`: 30.8% below 256. The integer distribution is well shaped (favours small and boundary values), contrary to my initial expectation of uniform u64. But collection sizes are small unless `max_size` is set. The proptest twins use `0..4096` (state.rs) and `0..6000` (core_hint reader) while the Hegel versions are limited to about 28 bytes: `hegel_write_read_is_identity`, `hegel_single_byte_mutation...`, `hegel_reader_never_panics_on_arbitrary_bytes`, `hegel_core_never_panics...` (stdin), `hegel_read_equals_range_intersection`. No call sets `max_size`.
Action: set explicit `max_size` (e.g. up to 4-8 KiB for byte bodies, 64 KiB occasionally for reader properties) on the Hegel generators so the engine is not the weaker of the two.

### F7. Failure database and regression pinning are absent where they matter. Severity medium, confidence high.
Evidence (F): `rust/.gitignore:4` ignores `.hegel/`. `#[hegel::test]` properties get a database (two entries appeared in `crates/memory-core/.hegel/examples` after failing runs); the Hegel docs say a hand-built `Hegel` "neither stores nor replays examples". Every state machine and `counters`, `realfs`, `core_mechanics`, `core_manifest`, `core_coordinates` uses `hegel::Hegel::new(...).run()` without `database_key` (`capture_property.rs:1070`, `commit_protocol.rs:1160`, `daemon.rs:1679`, etc.). After the K3, K5 etc. failures in the scratch copy, `crates/memory/.hegel` contained nothing. There is no `#[hegel::explicit_test_case]` anywhere and no pinned counterexamples (the repo's examples are hand-written elsewhere; e.g. 16-capture-property4.md records the `Invalid(Overlap)` minimal script in prose). In CI the database is not persisted (fresh checkout; gitignored). Hypothesis's own advice: "Do not rely on the database for correctness; to ensure Hypothesis always tries an input, use @example."
Impact: a failure found once by random search is not re-run unless someone remembers the seed. For this repo the seed and (with `HEGEL_PRINT_BLOB`) the reproduction blob are recorded, so reproduction is possible (verified below), but regressions are never pinned.
Action: set `database_key` for the `Hegel::new` runs (local only; keep CI database off) and turn every real shrunk failure into a directed example test or `explicit_test_case`.

### F8. Configuration review. Severity low-medium, confidence high (verified experimentally).
- Case budget: `gates.sh:7-10` exports `HEGEL_TEST_CASES=${PROPERTY_CASES:-500}` and refuses below 500; CI uses 500, local slice acceptance 2,000 (quality-gates.md). Nothing in code sets `Settings`/`test_cases`, so the env var governs everything (good: docs say code settings would override it). Default with no env is 100 and the guards then fail (F3).
- Seed: `gates.sh:8` picks a random 32-bit seed per run, exports `HEGEL_SEED`, writes it to `artifacts/seeds.txt`, and `rust.yml` uploads `rust/artifacts/*.txt` with `if: always()`. Verified experiment (capture_property, 60 cases, STAT fingerprints): with `CI=true` and no seed the run is derandomized (identical fingerprints on two runs); with a seed set, the fingerprint depends on the seed and is identical across repeats, with or without `CI=true`. So `HEGEL_SEED` takes precedence over the CI profile's derandomize, CI explores a new region each run, and a failure reproduces from the logged seed plus the same case count. Reproducing from the printed blob requires adding `.reproduce_failure("...")` to the builder (printed instructions), which is fine locally.
- Exploration vs determinism: random seed per CI run trades flakiness (a new failure can appear on an unrelated PR, see F3 guard) for exploration. It is a defensible choice for a project in this phase, as long as the artifact is always uploaded (it is). I would add a nightly job with several seeds at 5,000+ cases rather than relying on PR runs for exploration.
- Deadlines/health checks: no `suppress_health_check`, no deadline settings, no use of `assume`/`filter`. Fine.
- Gate 05 (`gates.sh:28`) runs only `cargo test -p memory-core --test scaffold hegel_`, one test (0.04 s). The name suggests the Hegel gate; the real Hegel tests run inside gate 04 (nextest, env inherited). Not wrong, but 05 is nearly vacuous and could hide a nextest-only regression in how HEGEL_* variables reach the tests. Low.
- Runtime: at 500 cases the Hegel suite is about 3 minutes (loaded machine); daemon is 7-14 s (A7 `SyncPolicy::None`), capture_property 7.8-11 s. Shrinking big-file failures is expensive (87 s for the P1 failure).
- Provisioning: `static-engine` is a good choice for hermetic Nix/CI: nothing to provision. Cost: the engine's deps are compiled into every test binary (build time) and the documented feature-unification pitfall. Version pinned `=0.52.0`; 0.52.1 exists; upstream moved to a monorepo (F1 sources). No action beyond a deliberate upgrade review.

### F9. Oracle quality: mostly independent and well-built for the state machines; some weak or partially derived. Severity low-medium, confidence medium-high.
Independent and strong (F): capture_property's reference model never reads the driver, and the checker reads only store contents (doc 16; `observe` verifies chain hashes, folds with memory-core, verifies descriptors, reassembles epochs). It has a tamper test with 9 cases and a positive control (`oracle_detects_tampered_store_contents`, doc 16) and guard negative controls. Liveness after settle compares the latest epoch to the source bytes.
The daemon masking (commit e94a09b): masked positions are only those changed by same-size rewrites between windows (`daemon.rs:1212-1233`, `Reference::masked`), per byte, never cleared. A masked byte may hold any value the source ever held at that position. This still detects: a committed byte that was never held at that position, any change outside masked positions, length mismatches. It cannot detect: a stale-but-once-held value at a masked position after a later truncate-and-regrow (positions stay masked forever, even when they later fall in the head or tail window). Is this significant? (I) Low: such a position would need a mid-file rewrite followed by a shrink and regrow past it and a stale commit; the instrumented rate of mid-file rewrites after commit is 0.6% of cases, so the masking rarely engages and, when it does, is narrow. I did not mutate this class; it is an open question (Q2). Note the masked set is the documented limit of window checks (DESIGN 7.2: only audits catch interior rewrites, and audit evidence is always `NotDue` in the daemon, README), so 1b audit liveness is untested at the daemon level (README says so).
Weaker or partly re-derived (F/I):
- `hegel_budget` (`core_limits.rs:507-514`) asserts only monotonic and floor 60 s; mutant L2 (wrong rate) survives it, killed by an example (`budget_boundaries`).
- `hegel_key_monotonic`: the model `SESSION_FIXED_CLAUDE + a + m + s` is a second implementation of the same formula; the boundary (sum equal to 1,024) is hit rarely (a, m, s in 1..700); mutant L1 (`>=`) survives, killed by examples. `hegel_admission` is a good oracle: mutant L3 (cap `>`), which I expected to pass, was caught, because the 1,100 distinct ids drive a later rejection that the oracle flags.
- `hegel_scheduler_matches_model` re-states the production rule (`flush || now-last >= 60 || now-first >= 300`); it is an independent re-implementation, not a different specification. Mutant Co1 (cap `>` instead of `>=`) was caught by core_mechanics property 2; I did not run it against the watch property.
- `hegel_core_never_panics...` is an invariant-family property ("never panics, at most one whole line") whose power is limited by the 28-byte stdin (F6).

### F10. Stateful testing is used where the system is a state machine, but not for the coalescer/scheduler or crash enumeration. Severity low, confidence medium.
Hegel's stateful support is used for the commit protocol, capture and daemon (3 machines). The scheduler (`watch.rs`) and coalescer (`core_mechanics` property 2) are driven by `Vec<u32>` scripts compared to a model; this is the same idea without Hegel's rule weights, invariants and readable step-by-step shrunk output. Crash recovery is exercised by randomly chosen crash steps (`CrashBeforePut/AfterPiece/AfterManifest`) rather than enumerating a crash at every PUT/fsync index; 68% of crash steps are no-ops (F4). No property uses `#[invariant(always_run)]` except via the explicit `check` call inside each step.
Action: port scheduler and coalescer to `#[hegel::state_machine]` (the output format in `k3-full.txt` shows how much clearer the shrunk traces are), and add a deterministic "crash after k-th store request" enumeration for a fixed small script.

### F11. Property/example duplication; removal candidates. Severity low, confidence medium.
Evidence (F): examples that already catch what the Hegel version catches and where Hegel adds no search: `privacy_property` (the sibling example loops over every case; V1 caught by both; V2-V4 only by examples), `core_limits` (`key_boundary_*`, `budget_boundaries` caught L1/L2 that the Hegel versions missed), `scaffold::hegel_checked_ranges...` (+ proptest twin + edge example). Almost every Hegel property also has a proptest twin: the cross-check was a DESIGN decision (gate 6); its detection value was not measured here. For simple pure functions (`scaffold`, `realfs` ranges, `state` identity) two engines doubling the same random byte vectors adds build time and maintenance, but little evidence of independent value.
Action: keep the dual-engine only for the state machines (where an engine bug would be most damaging) and for one or two pure properties as a harness smoke; drop the proptest twin of trivially pure properties or give each twin a different oracle.

## 4. Detection-power experiment (empirical)

Setup: scratch copy, one mutant at a time applied to production or test-visible source, cargo test of the Hegel tests of the relevant crates at `HEGEL_TEST_CASES=500 HEGEL_SEED=424242` (`mut.py`). "case N" is the first failing case in `capture_property` (instrumented in the scratch copy). Examples: all non-`hegel_`, non-`proptest_` tests of the crate. A first batch was invalidated (a leftover mutant, see "What I did not cover" for how) and every result below is from a clean rerun; `contaminated/` holds the discarded files.

| ID | Injected bug | Hegel result | Example suites | Shrunk counterexample |
|---|---|---|---|---|
| P1 | tail window ignored (`planner.rs`) | KILLED by core_mechanics prop 1; MISSED by capture_property, commit_protocol, daemon at seed 424242; daemon kills in 3/8 seeds at 500, 3/4 at 2000 | mem examples missed | daemon: bulk 69 KiB, round, rewrite zone 1, settle (4 steps, 87 s to shrink) |
| P2 | truncation check removed (Indexed) | KILLED by capture_property (case 8) and daemon; missed by core_mechanics | n/a | "Settle: liveness: latest epoch != source" |
| P3 | identity change (rename-replace) ignored | KILLED only by core_mechanics prop 1; MISSED by all memory-crate Hegel tests | mem examples missed | `operation=5 seed=...` (seed integer, no structure) |
| P4 | window constants 4096/65536 -> 4 | KILLED by capture_property (case 346) and daemon; missed by core_mechanics | n/a | not reviewed |
| P5 | deletion recorded on first missing observation | MISSED by all Hegel | KILLED by 2 examples (`removed_source_is_deleted_only_after_two_observations`, `removed_source_needs_two_observations_then_is_deleted`) | n/a |
| C1 | final partial piece dropped | KILLED (case 2) by capture_property, counters delta digest, daemon | n/a | not reviewed |
| C5 | stale local deletion flag wins | MISSED | MISSED (whole mem suite) | n/a (possibly an equivalent mutant: (Q)) |
| C6 | `stream_span` delta window off-by-one | MISSED | MISSED | n/a (I: causes spurious epochs, not data loss) |
| K1 | manifest not PUT before `current.json` | KILLED (case 2), 4 properties | n/a | n/a |
| K2 | piece upload error swallowed | MISSED (all 4 memory Hegel tests) | KILLED (`ambiguous_piece_put_is_retried_as_412_success`) | n/a |
| K3 | no verify after ambiguous reply (lost reply) | KILLED (case 5), 3 properties | n/a | 4 steps: `append`, `committed_reply_lost`, `append`, `settle`, readable (`k3-full.txt`) |
| K5 | manifest PUT treats 412 as failure (not idempotent after crash-after-manifest) | KILLED: seed 424242 at case 289; 100 cases: 7 of 10 seeds (first fail 5-96); 500 cases: 6 of 6 seeds (first fail 5-330) | n/a | n/a |
| K6 | piece PUT 412 not idempotent | KILLED (case 4), 5 properties | n/a | n/a |
| V1 | `clientSecret` redaction rule removed | KILLED by privacy_property | (I) the sibling example also kills it | n/a |
| V2 | `.npmrc` not a credential | MISSED | KILLED | n/a |
| V3 | `passw` not a secret name | MISSED | KILLED | n/a |
| V4 | `key` token not a secret name | MISSED | KILLED (4 tests) | n/a |
| L1 | key limit `>=` instead of `>` | MISSED | KILLED (4 tests) | n/a |
| L2 | budget rate 1000 KiB/s instead of 1 MiB/s | MISSED | KILLED (`budget_boundaries`) | n/a |
| L3 | source cap `>` instead of `>=` | KILLED by `hegel_admission` | n/a | n/a |
| Co1 | coalescer cap boundary `>` instead of `>=` | KILLED by core_mechanics prop 2 | n/a | n/a |
| X1 | small windows (8/16) + tail ignored (experiment) | capture_property kills in 5/6 seeds at 500 cases (first fail 36-423) | n/a | n/a |

Totals (21 mutants P1-P5, C1, C5, C6, K1, K2, K3, K5, K6, V1-V4, L1-L3, Co1): Hegel alone killed 12; 9 escaped Hegel (P5, C5, C6, K2, V2, V3, V4, L1, L2). Example suites killed 7 of those 9; C5 and C6 escaped everything. Memory-crate Hegel state machines killed 7 (P2, P4, C1, K1, K3, K5, K6) and missed P1, P3, P5, C5, C6, K2. Caveats: the mutants are my choice, 21 is a small sample, most runs use one seed; sensitivities such as K5 and P1 vary widely with seed (listed).

Repo's own mutation evidence (F): `scripts/check-mutations.py` kills three mutants (blind If-Match, advance-on-fail, skip credential denylist) using dedicated tests (`tests/mutation_daemon.rs`, `privacy_port::denied_identities`, `privacy_property`), not the state machines; `scripts/check-window-mutation.py` kills a disabled window comparison with `core_mechanics` property 1. So the Hegel state machines are not the required killer for any of the repo's four required production mutants; only property 1 (a table) and `privacy_property` (a table) are. DESIGN.md says "Mutation check: with the window check disabled, 1a must fail. CI runs this", which is satisfied by property 1 alone, not by the stateful properties.

## 5. Coverage of DESIGN invariants

| Invariant | Property | Status |
|---|---|---|
| Idempotent at-least-once writes | capture/commit/daemon state machines (lost-reply, 5xx, crash-after-manifest), counters prop 3 | Good (K3, K5, K6 killed). Writer-version change untested (README) |
| Crash recovery at every step | random crash steps at 3 points; `state.rs` crash points | Partial: random, 68% no-ops, no enumeration of every store request; file-level crash points in `state.rs` are good |
| Manifest/checkpoint consistency | `core_manifest` (seed-driven), `read_chain` in state machines | Present but seed-driven (F5) |
| Fail-closed privacy | `privacy_property` (table), core_hint canary | Weak; no generated structure; V2-V4 escape |
| Monotonic progress | capture_property `check` (head never back, manifest never replaced); coalescer prop 2 | Good |
| No lost bytes | 1a prefix oracle, liveness | Good for small files; boundary region weak (F1, F2) |
| Window/identity logic | core_mechanics prop 1 only | Narrow (F1, F2) |
| Coalescer/scheduler | prop 2 and `watch.rs` | Adequate; candidates for state-machine form |

## 6. What is sound (brief)

- Independent-oracle design with explicit negative controls for the oracle and the guard (doc 16; `guard_has_negative_controls`, `oracle_detects_tampered_store_contents`) follows Hypothesis's model-comparison pattern and Hughes's model-based family.
- Hegel's stateful shrinking gives short, readable counterexamples (K3: 4 steps; P1: 4 steps).
- Seed policy, case floor and artifact upload make CI failures reproducible (verified).
- No `assume`/`filter`, so no health-check/discard risk (F, grep).
- Hegel's integer generators are well shaped for boundary values (measured F6).
- `static-engine` gives hermetic provisioning in Nix (built in ~40 s on the pinned toolchain here).

## 7. Recommendations, ranked by value

1. Add rename-replace (identity change) to `capture_property` and the daemon model and verify P3 dies in the memory crate. (F2)
2. Make window sizes configurable through `Env`/planner for tests and run the state machines at 8/16 bytes; keep one big-file run at the real sizes. Verify with X1/P1 (5/6 seeds already shown). (F1)
3. Replace the presence-only coverage guards with frequency floors on bug-relevant events (rewrite or truncate after commit; tail-only and mid-file rewrite after commit; crash with pending data; settle after rewrite) and make the guard not fail at the default 100 cases (either skip the guard below a configured threshold or raise the default in-code budget for these tests via `Settings::test_cases`, accepting that env then cannot lower it). (F3, F8)
4. Reduce no-op steps: choose rewrite/truncate/remove/crash targets from existing sources; start with an append; add a precondition-style rule filter (check Hegel's attribute for rule preconditions; Q). (F4)
5. Rebuild `privacy_property` as a real property: generate JSON/TOML documents and argv/env with canaries placed under random key names (protected list, near misses, nested, unicode, escapes), assert canary absence and structure retention; add credential-name coverage so V2-V4 die. Or delete it and keep the example. (F5)
6. Strengthen weak oracles: `hegel_budget` (exact formula for a drawn size), `hegel_key_monotonic` (generate near 1,024 total length). (F9)
7. Convert seed-driven properties (`core_manifest`, `core_mechanics` property 1 remainder, `core_hint::no_canary`) to generated structure, or rename them table tests. Property 1's table is valuable; leave it as a named boundary test and run it with fewer cases (e.g. 20) to save about 35 s. (F5)
8. Set explicit `max_size` on byte vectors so Hegel is not the smaller-input twin; add `database_key` to `Hegel::new` runs locally; pin real shrunk failures with `explicit_test_case` or directed tests. (F6, F7)
9. Port scheduler/coalescer properties to `#[hegel::state_machine]`; add a deterministic crash-at-every-store-request enumeration for one small script. (F10)
10. Drop or de-duplicate proptest twins of trivially pure properties; make the daemon/capture machines the only dual-engine pair. (F11)
11. Add a nightly job running the state machines with 5-10 seeds at 5,000+ cases, and decide whether to adopt 0.52.1. (F8)

## 8. Open questions and experiments

- Q1: Daemon seed 1 at 500 cases: instrumented baseline recorded zero tail-only or mid-zone rewrites after a commit, yet mutant P1 was killed at that seed with exactly such a sequence (bulk 69 KiB, round, rewrite zone 1, settle). Either my instrumentation misses a path (e.g. `peek` after round) or case generation differs between the clean and mutated binaries. Experiment: run the clean daemon test at seed 1 with a print of every step and compare it with the mutated run's step trace (`p1s1.txt`); also run the same mutant twice and compare.
- Q2: Does daemon masking hide a stale value after truncate-and-regrow? Experiment: mutant that keeps the old index after a truncation that crosses a masked position; check whether the daemon fails; if not, clear masked positions on truncate/remove (the oracle's justification no longer holds once the position is in a window).
- Q3: Are C5 and C6 equivalent mutants or real untested behavior? Experiment: directed tests: reappearing source after a committed deletion with a stale local flag; Extend with a tail window crossing `from`, asserting no spurious epoch (counters-style PUT count).
- Q4: Is the proptest twin independently valuable? Experiment: run P1-P5, C1, K1-K6 through the proptest twins and compare kill sets and cases-to-kill with Hegel's.
- Q5: Does Hegel 0.52.0 support a precondition on rules or a deadline setting? Source check of `hegeltest-macros` 0.52.0 docs; I fetched no page that documents either.
- Q6: Flake rate of the daemon `rewrite-mid-file` guard at 500 cases over 100+ seeds (cheap: 7 s each).
- Q7: Whether the real shrink time of large-file counterexamples (87 s) can be bounded (e.g., smaller bulk sizes under small windows).

## 9. What I did not cover / process notes

- I did not run the full workspace or the proptest twins (except via `--skip proptest_` in the example runs), did not run live ingestion, did not touch the repo (all edits and mutants were in the scratchpad copy; the repo's `git status` was clean when I started and I did not run git state changes).
- Properties not individually mutation-tested: hook, realfs, state.rs, coordinates, locator, watch, scaffold, hint (apart from the V and canary runs), counters (only C1/K6 killed them).
- I did not read every property body; for those marked in the table I relied on a reading of the oracle function and the generator lines cited.
- Hughes's paper text and the QuickCheck manual were not read in full (PDF unreadable; manual seen via a search summary).
- Process errors, disclosed: (1) I ran `pkill -f "cargo test"` at one point to stop my own mutation batch; on this shared machine that may also have killed another agent's `cargo test` process. (2) That kill left one mutant (P3) applied in my scratch copy, which contaminated the next batch of results; I detected it by diffing against the repo, discarded those results (moved to `contaminated/`), made the runner signal-safe and verify a clean tree before each mutant, and re-ran everything. Only clean results are reported.
- Timings were taken on a machine with load average 50-125 (other agents), so absolute times are inflated.
