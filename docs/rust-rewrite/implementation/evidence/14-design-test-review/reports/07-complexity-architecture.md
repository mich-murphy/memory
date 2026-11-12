# 07 Complexity and architecture review of rust/crates (production src/ vs tests/)

Repo state: branch rust/coverage-floor-80, HEAD 6ea015e. Read-only; clippy ran on a scratchpad copy.
Raw output, all in `.../scratchpad/review/07/` (prefix `S/`):
`S/scc-*.txt` (SLOC), `S/rca-json/` (rust-code-analysis per-function JSON), `S/functions.json` (merged per-function table), `S/top.txt` (top-20 lists), `S/params.txt`, `S/perfile.txt`, `S/pub-surface.txt`, `S/panics.txt`, `S/allows.txt`, `S/clippy-raw.txt` (pedantic + low-threshold clippy run), `S/ws/clippy.toml` (thresholds used), `S/fn.py`, `S/params.py` (scripts).

## 1. Summary verdict

- Production code is small: 8,846 code lines (scc: memory-core 5,326 over 22 files; memory 3,435 over 12; xtask 85; memory-reader 0 code, 3 comment lines). Tests: 19,162 code lines (core 8,892, memory 10,270). Ratio 2.17 : 1.
- Complexity is low on median (median function: cyclomatic 3, cognitive 0, 7 lines) but has a heavy tail concentrated in `memory/src/capture.rs`, `memory-core/src/privacy/{text,document}.rs`, `planner.rs::plan`. 53 of 479 functions exceed McCabe 10; 14 exceed cognitive 15; 5 exceed 25 (RCA metric).
- Core/shell split is real in `memory-core` (no std::fs/env/net/process/thread, no clock, no RNG; randomness and time are passed in as values). Verified by grep and by a separate cargo-deny ban list.
- Safety hygiene is real: 0 `unsafe`, 0 unwrap/expect/panic in production code (only `assert!` inside two `#[cfg(test)]` modules), 7 lint suppressions, all `#[expect]` with reasons.
- Biggest structural issue is not complexity but incompleteness: the shipped binary reaches almost none of the shell. No production `Store`, no production `Clock`; `watch`, `resolve`, `realfs`, `capture`, `commit` and the `Daemon` round loop are reachable only from tests (finding 1).
- Second: the shell's hot spot is a few 100+ line functions in `capture.rs` and `daemon.rs::run_owner` with 6-7 parameters and 40-50 branch paths (findings 2, 3).
- Third: duplicated code that wants a macro or shared module: four identical ID newtypes, three near-identical directory-vetting routines, three places that spell the object-key layout (findings 4, 5, 6).

## 2. Part A: measurement

Tools (all via `nix shell nixpkgs#...`, nothing installed): scc 4.1.0, rust-code-analysis-cli 0.0.25 (Mozilla; cyclomatic, cognitive, nargs, SLOC per function), clippy from the pinned `nix develop` shell run with `-W clippy::cognitive_complexity -W clippy::too_many_lines -W clippy::too_many_arguments -W clippy::excessive_nesting -W clippy::pedantic -W dead_code -W unused` and a scratch `clippy.toml` (cognitive 5, lines 40, args 4, nesting 3). lizard is not in nixpkgs for aarch64-darwin.

Caveats (verified): RCA `nargs` counts closure parameters too (it reported 15 for `capture.rs::step`, real 6), so I recomputed parameter counts from signatures (`S/params.py`). RCA cyclomatic for a function includes nested closures; it counts `&&`/`||`/match arms/`?`-style branches by its own rules, so it is higher than a strict decision-point count. Max nesting is my brace-depth approximation (`S/fn.py`), not a tool metric. Clippy's cognitive_complexity is a different (older) implementation from RCA's and gives smaller numbers (max 15 at `text.rs:11`); both agree on the same top functions.

### 2.1 SLOC per crate and per file
From `S/scc-src.txt` (code lines, excluding comments/blank): largest production files: `memory-core/src/manifest/schema.rs` 997 lines total, `domain.rs` 927, `locator.rs` 924, `privacy/document.rs` 610, `hint.rs` 581, `memory/src/capture.rs` 729, `state.rs` 709, `daemon.rs` 810, `commit.rs` 500. Per-file function stats (function count, max cc, max cog, max length, max nesting, sum cog) are in `S/perfile.txt`. Highest summed cognitive complexity: `privacy/document.rs` 143, `locator.rs` 98, `capture.rs` 73, `privacy/text.rs` 73 (3 functions), `schema.rs` 72 (79 functions, so flat and boring).

### 2.2 Top 20 by cognitive complexity (RCA; cc, length, real params, nesting)
| # | file:line | function | cog | cc | len | params | nest |
|---|---|---|---|---|---|---|---|
| 1 | memory-core/src/privacy/text.rs:11 | words | 36 | 26 | 70 | 2 | 6 |
| 2 | memory-core/src/planner.rs:155 | plan | 33 | 30 | 105 | 1 | 4 |
| 3 | memory-core/src/privacy/text.rs:81 | literal | 31 | 34 | 74 | 7* | 4 |
| 4 | memory/src/capture.rs:447 | Job::write | 28 | 45 | 137 | 4 | 4 |
| 5 | memory/src/realfs.rs:192 | walk_root | 28 | 13 | 54 | 4 | 5 |
| 6 | memory-core/src/privacy/launch.rs:58 | sanitize_launch | 24 | 15 | 52 | 3 | 3 |
| 7 | memory-core/src/coordinates.rs:255 | Inventory::resolve | 20 | 15 | 50 | 6 | 4 |
| 8 | memory/src/capture.rs:604 | append | 19 | 29 | 126 | 6 | 5 |
| 9 | memory-core/src/privacy/document.rs:494 | url | 19 | 18 | 76 | 5 | 3 |
| 10 | memory-core/src/manifest/state.rs:93 | OwnerState::apply_one | 19 | 23 | 71 | 3 | 5 |
| 11 | memory/src/daemon.rs:587 | Daemon::run_owner | 16 | 19 | 87 | 7 | 3 |
| 12 | memory/src/resolve.rs:102 | resolve | 16 | 16 | 52 | 5 | 5 |
| 13 | memory-core/src/privacy/mod.rs:264 | admit | 16 | 19 | 46 | 5 | 2 |
| 14 | memory-core/src/manifest/recovery.rs:145 | recover_projection | 16 | 20 | 65 | 3 | 4 |
| 15 | memory-core/src/disposition.rs:120 | transition_paced | 15 | 17 | 59 | 5 | 5 |
| 16 | memory-core/src/coordinates.rs:210 | Inventory::validate_routes | 15 | 8 | 20 | 1 | 3 |
| 17 | memory-core/src/hint.rs:401 | build_line_with_limit | 13 | 10 | 58 | 6* | 3 |
| 18 | memory-core/src/privacy/document.rs:324 | restore_toml_strings | 13 | 10 | 27 | 2 | 4 |
| 19 | memory-core/src/privacy/document.rs:387 | redact_all | 13 | 14 | 39 | 4 | 3 |
| 20 | memory/src/capture.rs:303 | step | 12 | 25 | 121 | 6 | 5 |
(* the params column for rows 3 and 17 is the RCA figure; recomputed values from signatures are in `S/params.txt` for the top params only.)

### 2.3 Top 20 by length (lines)
capture.rs:447 `Job::write` 137; capture.rs:604 `append` 126; capture.rs:303 `step` 121; planner.rs:155 `plan` 105; commit.rs:360 `commit_known` 94; daemon.rs:587 `run_owner` 87; document.rs:494 `url` 76; text.rs:81 `literal` 74; state.rs:93 `apply_one` 71; text.rs:11 `words` 70; locator.rs:795 `pi_session` 66; recovery.rs:145 `recover_projection` 65; build.rs:57 `build` 63; state.rs:324 `decode` 61; disposition.rs:120 `transition_paced` 59; hint.rs:401 `build_line_with_limit` 58; daemon.rs:482 `scan_hints` 54; realfs.rs:192 `walk_root` 54; resolve.rs:102 `resolve` 52; launch.rs:58 `sanitize_launch` 52. (Paths: crates/memory{,-core}/src/...; full list `S/top.txt`.)

Highest cyclomatic: `Job::write` 45, `literal` 34, `pi_session` 30, `plan` 30, `append` 29, `event_kind` 27 (hint.rs:179; a flat match, benign), `commit_known` 26, `words` 26, `step` 25, `document.rs::parse` 24.
Largest real parameter counts (incl. self): `Delta::new` 8 (the only `#[expect(clippy::too_many_arguments)]`, schema.rs:696), `run_owner` 7, `Checkpoint::new` 7, `stream_span`/`step`/`append`/`to_source`/`verify` 6.
Deepest nesting (approx.): `words` and `document.rs::parse` 6; `step`, `append`, `resolve`, `verify`, `commit_known`, `walk_root`, `transition_paced`, `apply_one` 5. 24 functions have nesting >= 4.

### 2.4 Against thresholds
Sources fetched this session: Rust Clippy lint index (https://clippy.rust-lang.org/master/index.html: `cognitive-complexity-threshold` default 25, verified). Rust API Guidelines type-safety page (C-NEWTYPE, C-CUSTOM-TYPE quoted below). McCabe limit 10: a web search returned secondary sources (Microsoft docs say McCabe's limit of 10 has supporting evidence and limits up to 15 have been used; NIST SP 500-235 has a section "Limiting cyclomatic complexity to 10"); I could not fetch the NIST text itself (404 on the csrc URL), so treat the NIST wording as secondary. Sonar's cognitive complexity paper PDF could not be parsed by the fetch tool; the "default rule threshold 15" is unverified, from memory. Clippy defaults too_many_arguments 7, too_many_lines 100, excessive_nesting off: unverified, from memory (the fetched excerpt was cut off before them).

| Threshold | Exceeders (production) |
|---|---|
| McCabe cc > 10 | 53 of 479 functions (RCA counting; some inflated by closures and flat matches). Worst: write 45, literal 34, pi_session 30, plan 30, append 29. |
| Sonar-style cognitive > 15 | 14 functions (rows 1-14 above with cog > 15; row 11 `run_owner` and 12-14 sit exactly at 16). > 25: `words`, `plan`, `literal`, `write`, `walk_root`. |
| Clippy default cognitive 25 (RCA scale) | 5 as above. With clippy's own scale none exceeds 25 (max 15, `words`); clippy default would pass. |
| Clippy default too_many_arguments (7) | `Delta::new` 8, suppressed with `#[expect]` (no reason string, schema.rs:696). `run_owner` 7 and `Checkpoint::new` 7 pass. |
| Clippy default too_many_lines (100) | 4: `write` 137 (clippy: 128), `append` 126 (114), `step` 121 (110), `plan` 105 (103). |
| Nesting >= 4 | 24 functions (`excessive_nesting` at threshold 3 gave 100 hits, `S/clippy-raw.txt`). |

The repo's own policy (docs/quality-gates.md) deliberately leaves size/complexity metrics off as gates; so these numbers are not violations of project rules, only of the cited rules of thumb.

### 2.5 Public API surface, fan-in/fan-out, cycles
`S/pub-surface.txt` lists pub items and pub fields per file. Largest: `domain.rs` 51 items + 26 pub fields, `manifest/schema.rs` 62 items + 32 pub fields (+15 `pub(super)`), `state.rs` (memory) 43, `daemon.rs` 29 items + 31 pub fields, `hint.rs` 29. 218 struct/enum types in total. Only 29 `pub(crate)`/`pub(super)` uses across both crates.
- `memory` is a binary whose `lib.rs` declares all ten modules `pub` (lib.rs:1-11). The binary uses two functions (`hook::main_entry`, `daemon::main_entry`, main.rs:7-15). The pub surface (90+ pub fns in src/*.rs) exists for tests. Tests live in `tests/` and need `pub`, hence the `sim` feature and `#[cfg(feature = "sim")] pub` hatches (daemon.rs:233,377; environment.rs:159; state.rs:421-567).
- Inter-crate: memory -> memory-core only (use counts: domain 19, privacy 6, manifest 5, hint 4, locator/disposition/coalescer 2, others 1). xtask -> memory-core (+serde_json). memory-reader -> memory-core (declared, no use). No cycles between crates. memory-core has 86 normal transitive packages; memory 97.
- Intra-memory (crate::) edges: daemon -> capture, commit, config, environment, hook, state; capture -> commit, environment; commit -> environment; resolve -> capture, daemon, environment, realfs; realfs -> environment. No cycle. Odd edge: `daemon` imports `hook::{is_day_name, utc_day}` (daemon.rs:24): date helpers living in the hook module (hook.rs:100,113) are a utility shared by two features. `resolve -> daemon` exists only to implement `daemon::Sources` (resolve.rs:287).
- Intra-memory-core: all modules depend on `domain`; hint -> privacy, slice; manifest::schema -> privacy, domain, state. Cycles: `manifest::schema <-> manifest::state` (schema.rs imports `super::state`, state.rs imports `super::schema`) is a real two-module cycle within one parent module; `privacy::{document,text,launch,names} -> privacy (mod.rs)` (`use super::{Reason, names}`) is a parent-child back-edge, and names.rs:122 uses `super::super::launch`. Rust permits all of this; it signals `manifest::schema` (997 lines) and `state` should be considered one unit or schema split.

### 2.6 Test-to-production ratio per module (code lines, scc; tests mapped by name)
| module | prod | test | ratio | warranted? |
|---|---|---|---|---|
| memory-core privacy (4 test files) | ~1.4k | ~1.4k | 1.0 | yes; it is the security boundary, properties are the evidence |
| memory-core locator | 924 | 1,696 | 1.8 | plausible: 3 agents x many path shapes; table tests |
| memory-core manifest | ~1.7k | 1,759 | 1.0 | yes |
| hint | 581 | 901 | 1.6 | yes |
| coordinates | 313 | 630 | 2.0 | ok |
| limits | 124 | 732 | 5.9 | high: 31 tests for 124 lines, many exact-spelling and boundary asserts (core_limits.rs:60ff). Worth a look for redundancy. |
| planner | 269 | 304 | 1.1 | arguably low for a 33-cognitive function; property tests elsewhere cover it |
| memory commit.rs | 500 | 2,432 (commit_protocol) | 4.9 | warranted by protocol complexity (DESIGN 7.4) but 199 tests with a private FakeStore (commit_protocol.rs:295) |
| memory daemon.rs | 810 | 2,339 + 312 | 3.3 | high; part is duplicated fakes |
| memory capture.rs | 729 | 771 + 1,345 | 2.9 | ok, 97 property cases |
| hook.rs | 341 | 901 | 2.6 | defensible (hook is a fail-closed privacy path) |
| state.rs | 709 | 1,018 | 1.4 | ok |
| config.rs | 229 | no dedicated file; exercised via tests/state.rs and daemon.rs | low | gap: config parsing and permission checks are on the secret path |
| memory-reader, xtask | 0 / 85 | 0 / 22 | n/a | |
Fact: MemFs is re-declared in 5 test files (capture.rs:37, capture_property.rs:100, counters.rs:71, daemon.rs:83, mutation_daemon.rs:68), MemStore in 5, Clk in 3. This duplication inflates the 2x ratio; a shared `tests/common` module would likely cut several hundred lines without removing assertions (inference; estimate not measured). The ratio is warranted for privacy, manifest, protocol and hook; it looks inflated for limits and the daemon/capture fakes.

### 2.7 Traits, generics, lifetimes
Production traits: 4, all in `memory`, none in `memory-core`.
| trait | production implementors | test implementors | note |
|---|---|---|---|
| `Clock` (environment.rs:8) | `Counting<T>` wrapper only | Clk (3 files), FClk | no production implementor at all |
| `FileSystem` (environment.rs:30) | `RealFs` (realfs.rs:95), `SharedFs` (resolve.rs:215), `Counting<T>` | MemFs x5, Spy | 2 real impls; associated `Error` type is erased by every caller (`.map_err(|_| Unavailable)` 10x in capture.rs) |
| `Store` (environment.rs:174) | `Counting<T>` wrapper only | MemStore x5, FakeStore x2, FStore, Ghost | "deliberately no production implementation" (environment.rs:171) |
| `Sources` (daemon.rs:208) | `WalkSources` (resolve.rs:287) + blanket `impl for FnMut` | closures in tests | a one-method trait plus a blanket closure impl; a plain `FnMut() -> Vec<..>` bound would do |
Trait objects: zero `dyn Trait`; only `&mut dyn FnMut(u32)` (commit.rs:290,348,365, always `&mut |_| {}` from daemon.rs ~ run_owner) and `Box<dyn Error>` in xtask. Generic structure: `Daemon<F, S, C, E>` four type parameters with `Parts<F,S,C,E>` (daemon.rs:219,340). Static dispatch throughout; cost is monomorphization and signature noise, benefit is simulation. Closure params in schema.rs:922, verify.rs:66-67 use `impl Fn`.
Lifetime-heavy: `locator.rs` has 65 lifetime tokens; `Res<'a>` threaded through ~12 helper functions with `parts: &[&'a str]` plus `roots: &'a Roots` (locator.rs:482-755), zero-copy parsing of short paths where an owned result would cost nothing measurable. `capture.rs` `Job<'a>` and `Env<'a>` hold 3 references (capture.rs:436,139).
Traits with one production implementor and a test fake: `Sources`; `FileSystem` is the only one with more than one real implementor. `Clock` and `Store` have zero.

### 2.8 Unsafe, panics, clones, suppressions
- unsafe: 0 (workspace `unsafe_code = "forbid"`; the 3 grep hits are doc comments).
- unwrap/expect/panic/todo/assert in production: 0 outside `#[cfg(test)]` (names.rs:120-129, xtask main.rs:72-90 are test modules). `unwrap_or_default`/`unwrap_or` appear 30 times; all are total. Workspace denies unwrap_used, expect_used, panic, indexing_slicing (Cargo.toml `[workspace.lints.clippy]`).
- `as` casts (pedantic): 5 in src, e.g. daemon.rs:459 `self.clock.wall_ns() as u64` (i128 truncation, used only to seed jitter), realfs.rs:61, build.rs:140, commit.rs:471.
- Lint suppressions in src: 7, all `#[expect]`: six `dead_code` on serde envelope fields that must be accepted but not read (manifest/recovery.rs:23-77, reason strings present), and one `too_many_arguments` on `Delta::new` (schema.rs:696) with no reason. 3 `#[expect]` in tests (core_manifest.rs:1740, core_f2_gaps.rs:899, commit_protocol.rs:2413). No `#[allow]` anywhere (deny on allow_attributes).
- Clone hot spots (`.clone()` counts): capture.rs 33, daemon.rs 20, manifest/state.rs 15, commit.rs 13, coordinates.rs 12, disposition.rs 8, hint.rs 7. Typical: `src.facts.clone()` (capture.rs `step` and `admitted_clone`) re-run `admit` twice to obtain a second `AdmittedSource` (capture.rs:~598-603, `admitted_clone` calls `admit` again) which clones `ResolvedSourceFacts`; `rt.base.as_ref().map(|b| b.state.clone())` every round (daemon.rs ~ run_owner) clones a whole OwnerState per owner per reconcile. No measurement of cost was done; flagged as inference (reconcile is every 15 min, so probably irrelevant).
- Dead code: `-W dead_code -W unused` on libs and bins: zero warnings. Caveat: all modules are `pub` in a lib, so rustc cannot report unused pub items; reachability from the binary is the real dead-code measure (finding 1).

## 3. Part B: architecture critique

### Finding 1 (high, confidence high): the production binary reaches almost none of the shell; Store and Clock have no production implementation
- Evidence: main.rs:7-15 calls only `hook::main_entry` and `daemon::main_entry`. `daemon::main_entry` (daemon.rs:276-287) loads state and config, then returns `EXIT_NO_ADAPTERS` 78. `Store` has "deliberately no production implementation" (environment.rs:171); `Clock` has none (only `Counting<T>` delegating). No non-test code names `watch::`, `resolve::`, `realfs::` outside their own files (grep in daemon.rs, hook.rs, config.rs, state.rs, capture.rs, commit.rs: no hits). So watch.rs (223), resolve.rs (319), realfs.rs (245), capture.rs (729), commit.rs (500) and the Daemon round loop (daemon.rs 276-810) are compiled, tested and covered at 97% but not executable by a user. lib.rs line 1 says "Runtime boundary scaffold. No production environment exists yet."
- Source: DESIGN.md 17 slice 2 lists the watcher, commit protocol and client chosen by P2 as slice 2 builds; DESIGN 13 lists `zstd`, `tracing`, `clap`, an HTTP client as deps, none present in memory/Cargo.toml. So this is staged, not hidden, and the README says so; but the "coverage 97%" and "391 tests" headline measures simulated shell code, not a running system. Also `memory-reader` (3 comment lines) is a placeholder (declared "reserved"; DESIGN slice 2b), `status`, `reconcile`, `restore`, `export`, `unpark` subcommands do not exist (main.rs `_ => 78`).
- Failure scenario: the design-critical unknowns (real R2 conditional-write semantics, real `F_FULLFSYNC` cost, FSEvents overflow behavior, 5-way async client choice) remain unmeasured, and the `FileSystem` trait (u64 handles, `read(range)`, no open/lifetime semantics, no error detail) and `Store` trait (`&str` keys, `Published` body) were shaped against fakes. A real S3 client with streaming/timeouts or `object_store` async may force trait changes that ripple through `Daemon<F,S,C,E>`, 5 duplicated test fakes and ~2,500 test lines.
- Action: treat the real adapters (R2 `Store`, system `Clock`, real watcher wiring in `main_entry`) as the next vertical slice before more simulated-shell hardening. Size: medium-large (new adapter + one trait revision); risk medium (trait signature churn, but the test double count is the cost driver; see finding 7).

### Finding 2 (medium, confidence high): `capture.rs` concentrates the shell's complexity in three 120-137-line functions with 6 parameters
- Evidence: `Job::write` capture.rs:447 (137 lines, cc 45, cog 28), `append` :604 (126 lines, cc 29, nest 5), `step` :303 (121 lines, cc 25). `append` is a `loop` with `adopt`, `windows`, `prefix`, `known`, `rounds` mutable locals (capture.rs:615-625) holding a mini state machine that DESIGN section 4 says is "plan (pure)". The planner (planner.rs:155) is pure and has the other 33-cognitive function.
- Source: Ousterhout, "A Philosophy of Software Design" favors deep modules (simple interface, rich implementation) and is not against long functions per se, so the criticism is narrower: these functions mix three concerns (policy admission, byte reading with race handling, state-machine looping), return `SourceOutcome` (a 6-variant enum that erases which I/O failed; `.map_err(|_| SourceOutcome::Unavailable)` 10x in capture.rs) and thread `fs, job, local, remote, live: bool, acc` (capture.rs:604-611; bool param `live` violates Rust API Guidelines C-CUSTOM-TYPE: "Core types like `bool`, `u8` and `Option` have many possible interpretations").
- Failure scenario: a future change to resume/epoch handling must touch a 120-line function with 5-deep nesting that has an oracle-based mutation check only for the three listed invariants (docs/quality-gates.md).
- Action: extract `read_range_checked` (the same read-then-length-check appears at capture.rs:253-262, :287-291 and inside `write`), replace `live: bool` with an enum, split `write` into read/transform/slice. Size: small-medium (200-300 lines moved); risk low-medium; the 97% line coverage plus property tests are a good safety net, but mutation coverage in this file is thin.

### Finding 3 (medium, confidence medium): `Daemon::run_owner` is a pass-through coordinator doing six jobs
- Evidence: daemon.rs:587-686 (87 lines, 7 params, `&mut self` plus out-params `rt`, `hints`, `report`): recovery, header construction with a hard-coded `commit: "unknown"` writer field (daemon.rs ~ 622), capture, launch attachment (`attached = attach_launches(..).is_ok()`: failure silently disables hint advancement, no log or count), commit with a no-op `&mut |_| {}` pause callback, settlement, hint bookkeeping. The backoff pacing `next_draw` seeds a PRNG from `clock.monotonic_ns() ^ wall_ns as u64` (daemon.rs:459): randomness is derived from the Clock seam rather than a third seam, a quiet design choice with a lossy cast.
- Failure scenario: `attach_launches` failing (launch evidence lost) is invisible; per DESIGN 7.6 launch evidence matters for provenance.
- Source: Bernhardt "Boundaries"/"Functional Core, Imperative Shell" (destroyallsoftware.com; the fetch returned only the page title, so the one-line statement of the pattern, pure decisions in a core with the shell doing I/O, is from memory). `run_owner` mixes decisions (what is the next header, when to settle) with effects.
- Action: move "build header", "launch attach result" and "settle" decisions to memory-core pure functions returning a value the shell applies; surface an `attach_launches` error as an ErrorClass count. Size: small; risk low.

### Finding 4 (medium, confidence high): four copy-pasted identifier newtypes plus six copy-pasted `JsonSchema` impls
- Evidence: domain.rs:26-170: `ArchiveId`, `MachineId`, `SessionId`, `SourceId` each define the identical 12-line `TryFrom<String>` validator (empty, >1024 bytes, `.`/`..`, `[A-Za-z0-9._:-]`), `From<..> for String`, `as_str`, and a byte-identical JSON-schema block (domain.rs:875-925). About 170 lines for one rule. `ETag` and `Sha256` follow the same shape.
- Source: Rust API Guidelines C-NEWTYPE (fetched) supports the newtypes; the duplication is the cost. A `macro_rules!` or a single `Ident<Tag>` generic would remove it.
- Failure scenario: tightening the identifier rule (e.g., `..` handling, max length) in 4 places; test `every_coordinate_brand_validates_its_own_boundary` (core_boundaries.rs:6) guards this by testing all four, i.e. the tests are also quadruplicated.
- Action: macro. Size: small (~120 lines removed); risk low (schema drift is gated by `xtask schemas --check`).

### Finding 5 (medium, confidence high): invariants and key layout knowledge duplicated between core and shell
- Object-key layout is spelled in three places: `limits.rs:19-55` (`current_key` and `current_key_len`, the latter re-derives the length from magic strings, limits.rs:43-55, instead of `current_key(..).len()`), `manifest/schema.rs:289` (`objects/{}.zst`), `memory/src/commit.rs:33` (`manifests/{}.json`). Keys are `String`/`&str` in the `Store` trait (environment.rs, `put_if_absent(key: &str..)`). The key format is domain knowledge that lives half in core, half in shell. Stringly-typed.
- Chain bound `CHECKPOINT_INTERVAL` is checked in `build.rs:140` `fold` and again before the same value in `commit.rs:471` `recover_base` (`chain.len() as u64 > CHECKPOINT_INTERVAL`); fold already rejects with `TooManySteps`; the shell check adds `Failure::Corrupt` instead, so it is an intentional early-out but with different error mapping, a subtle divergence.
- Directory vetting is implemented three times: `hook.rs:204-221` (`vet_dir`, with `Err(_)` details dropped to `HookResult`), `state.rs:116-140` (`vet`), `state.rs:650-660` (`vet_dir`, adds uid). Rules: 0700 create, refuse symlink/non-dir/0o022 mode. hook.rs deliberately does not depend on state.rs (hook must be tiny and fast, DESIGN 10.1), but the logic could live in a leaf module both use.
- Action: introduce `ObjectKey` newtype (or `keys` module in core) owning all three formats and use `Store::put_if_absent(&ObjectKey, ..)`; one `fsutil::vet_dir`. Size: small-medium; risk low (keys are pinned by tests like core_limits.rs:60).

### Finding 6 (low-medium, confidence high): error handling is typed, but errors are erased at the seams
- What is good: errors are `thiserror` enums per module (domain `ValidationError`, `ManifestError`, `StoreError`, `StateError`, `ConfigError`, `StartError`, `RoundError`, ...), matching DESIGN 14 and Rust API Guidelines practice; `ErrorClass`/`Disposition`/`Outcome` in core (domain.rs:770,821; disposition.rs:38) implement DESIGN 11.2 closed classes (and add `Transient` per amendment A6); the retry/park rules are pure and tested.
- What erodes it: `FileSystem::Error` is an associated type (environment.rs:33) that no caller inspects: capture.rs discards it (`map_err(|_| SourceOutcome::Unavailable)`, 10 sites), resolve.rs 3, daemon.rs 4, config.rs 3, hook.rs 2. The distinction DESIGN 11.2 draws (`Local` disk full vs permission vs transient) cannot reach `ErrorClass::Local(LocalError)` from reads because the class is never derived from the fs error. `ValidationError::Overflow` ("Offset arithmetic overflow", domain.rs:21) sits in an identifier-validation enum. Status `last_error` as class-with-time (DESIGN 11.3) cannot be populated for filesystem failures.
- Source: DESIGN.md 11.2 table; DESIGN 14 ("Errors are enums per module, built with thiserror").
- Action: have `FileSystem` return a closed `FsError { NotFound, Permission, NoSpace, Io(kind) }` (like `StoreError`) and map it to `ErrorClass::Local`. Size: small-medium; risk low-medium (touches the 5 MemFs fakes).

### Finding 7 (medium, confidence high): test double and helper duplication inflates the 2:1 ratio
- Evidence in 2.6: MemFs x5, MemStore x5, Clk x3, FakeStore x2 across `crates/memory/tests/*.rs`; nothing like a `tests/common/mod.rs`. Each seam change (finding 1, 6) is a 5-10 file edit. Action: shared `memory::sim` module behind the existing `sim` feature, or `tests/common`. Size: medium (1-2 days); risk low (tests only), but note the independent-oracle design of some tests may intentionally avoid sharing a fake with production sim code; keep an oracle copy where DESIGN requires independence (mutation_daemon.rs).

### Finding 8 (medium, confidence medium): core/shell split, what is real and what is a convention
- Real (verified): memory-core imports no `std::fs/env/io/net/process/thread`; `std::time::Duration` only as a value type (limits.rs:5, domain.rs:7, disposition.rs:4). No `Instant`/`SystemTime`/rand. The only global state is a `OnceLock<Option<Regex>>` (names.rs:7-8). Time is passed (`at: Duration`), randomness is passed (`Pacing { draw, retry_after }`, disposition.rs:20). crates/memory-core/deny.toml bans tokio, notify, libc (except via cpufeatures), reqwest, nix, rustix etc.; gate `03-core-bans` runs it. This is a clean functional core, and also a ports-and-adapters arrangement where the ports (traits) live in the shell, which suits Cockburn's hexagon (the core has no outbound port; it never calls out).
- Convention only: `cargo-deny` bans crates, not `std`. Nothing prevents `std::fs` being added to memory-core; only review and grep. DESIGN 13 says "No I/O crates, enforced by cargo-deny bans", which is correct as written but weaker than "pure". A cheap hardening: a `clippy.toml` in `crates/memory-core/` with `disallowed-methods`/`disallowed-types` for `std::fs::*`, `std::env::*`, `std::time::{Instant,SystemTime}`, `std::net::*`, `std::thread::*`. Size: ~20 lines; risk low (the deliberate-lint crate in scripts/check-lints.sh already shows the pattern for proving it). I did not run this; open question.
- Shallow-module note (Ousterhout): `slice.rs` (19 lines, one function wrapping `buffer.get(range).ok_or(RangeError)`) is a pass-through whose only purpose is to satisfy `indexing_slicing` without scattering `#[expect]` (DESIGN 14). It is a justified shallow module (lint policy), but the name `RangeError` carrying `Serialize/Deserialize/JsonSchema` derives for an internal error is speculative generality. `Counting<T>` (environment.rs:245-293) wraps all three traits to count calls, pass-through by design.
- Information leakage: capture.rs, commit.rs and daemon.rs all import `memory_core::manifest::{Built, Change, Header, ...}`; commit.rs reimplements the chain walk (recover_base) that core's `fold` consumes; the walk is I/O-interleaved so it belongs in the shell but the algorithm (follow `previous` until Checkpoint, max 64) is domain logic. A pure `next_fetch(&partial_chain) -> Fetch|Done` step function in core would make the loop trivially testable. Size: small; risk low.

### Finding 9 (low-medium, confidence medium): primitive obsession at the seams
- `FileSystem` addresses sources by `u64` handles (environment.rs: `stat(source: u64)`, `read(source: u64, range: Range<u64>)`; realfs.rs:59 `register(..) -> u64`; capture.rs:455 `from.get()` unwraps `Offset` into raw `u64` arithmetic immediately). `Offset` is a newtype in core (domain.rs:184) but the shell does `u64` math (`disk_end`, `start.max(from) - from`, capture.rs:285-300), partly checked via `try_from`. `Store` keys are `&str`. `Stat.mtime_ns: i64`, `Env.now_ns: i64` and `Clock::wall_ns -> i128` mix `i64`/`i128`/`u64` nanoseconds with lossy casts at the joints (daemon.rs:459, `i64::try_from(..).unwrap_or(i64::MAX)` daemon.rs ~ 621, stamp_of daemon.rs:327). No `Nanos` newtype.
- Boolean parameters (26 `bool` fields/params in src; function params: planner.rs:136,141 `refresh_stat`, disposition.rs:122 and commit.rs:168,179 `cancel_requested`, state.rs:116 `vet(path, create)`, schema.rs:405 `check_run(.., from_zero)`, daemon.rs:711,780 `write_index(force)`, `write_status(force)`, capture.rs:609 `live`, xtask `schemas(check)`). `cancel_requested: bool` passed through 3 layers is the sharpest (a `Cancel` enum would also remove the "swallow retryable only" doc caveat in disposition.rs:~115).
- Positive: parse-don't-validate is well applied for identifiers, digests, ETag, `Timestamp`, windows (`Window::try_from(WindowSeconds)`, coalescer.rs:36), `Piece`/`Delta` constructors are `pub(super)` so only the builder produces them, plus capability types `Authorized<Redacted<_>>` and `Published` (only privacy-authorized bytes can be uploaded; environment.rs `Published::metadata` is `pub(crate)`, `sim` hatch is feature-gated). This is the strongest part of the design and follows Alexis King (fetched: parsing "returns a more precise type that keeps that knowledge").
- Residual re-validation: manifests are built by `build` then parsed again by `parse_manifest`/`fold` ("validated by the builder and by parsing, never by the caller", schema.rs:695): intentional self-check, not a defect, but doubles validation code (schema.rs 997 lines, the biggest file).
- Action: introduce `Cancel`, `Force`, `Liveness` enums and a `Nanos`/`Handle` newtype. Size: small each; risk low.

### Finding 10 (low, confidence high): a few abstractions with one use
- `Sources` trait with a blanket closure impl (daemon.rs:208-217): single method, 1 production impl; accept `impl FnMut() -> Vec<..>` or a function pointer. `Parts<F,S,C,E>` plus `Daemon<F,S,C,E>` with four type parameters: justified for DST, but the `Sources` parameter adds a fourth generic for one method.
- `StartOptions` (daemon.rs:235) and `start_with` exist only under `sim`: a single field (`sync`). `SyncPolicy` is a legitimate test lever (A7 cut a 6-minute test to 14-20 s per quality-gates.md).
- `memory-reader` is a 3-comment-line crate that nothing imports; xtask depends on core for schema export; `memory-reader` as a crate boundary is defensible only because DESIGN 13 gives it a separate reader contract ("one authoritative reader"); until implemented it is a placeholder.
- `Counters.wakeups` is always 0 ("Clock is delegated untouched ... so `wakeups` stays 0", environment.rs doc), a field with no writer in production; `grep` of src shows no increment.
- Dead code proper: none by rustc.

### Finding 11 (low, confidence medium): `locator.rs` and `privacy` size are domain-driven; split only if they grow
- `locator.rs` (924 lines, 38 functions, 65 lifetime tokens) is a table of path shapes for three agents, which DESIGN 5 calls "pure locator, one per agent". Splitting into `locator/{claude,codex,pi}.rs` matches DESIGN wording and would have per-file maxima of ~300 lines. Size: small, mechanical; risk very low (tests are black-box in core_locator.rs). `privacy/text.rs` `words` (cog 36, nest 6) and `literal` (cog 31, cc 34) are hand-rolled scanners for secret-bearing text. The tests are heavy (privacy test files ~1.4k lines); simplify only with the property tests in place, as rewrites here are fail-open risks. Do not refactor without keeping the independent-oracle properties.
- `manifest::schema` / `state` cycle (2.5): merge `state.rs` (164 lines) into schema.rs or move shared types to a `types` module; small, low risk.

## 4. What is sound (brief)
- Pure core by construction (findings 8): time and randomness injected; no I/O crate or std I/O in memory-core; a crate-level ban list and a gate.
- Capability typing: only `Authorized<Redacted<_>>` bytes can become `Published` (environment.rs), a good use of Rust API Guidelines C-NEWTYPE ("Newtypes can statically distinguish between different interpretations of an underlying type").
- Zero unsafe, zero production panics, 7 reasoned `#[expect]`s, `allow_attributes = deny`.
- Closed error and disposition enums matching DESIGN 11.2, with pure, tested retry/park transitions.
- Median function is tiny (median length 7 lines, cognitive 0, cyclomatic 3); flat files like schema.rs (79 functions, max cc 12) are low-risk size.
- Clippy `dead_code` and `unused` clean.

## 5. Open questions and experiments
1. Does a `clippy.toml` with `disallowed-methods`/`disallowed-types` for std I/O in memory-core fire only on injected violations and stay clean now? Experiment: add the file in a scratch copy, run `cargo clippy -p memory-core`, then inject `std::fs::read` into a scratch copy of limits.rs.
2. How much test code is duplicated fakes? Experiment: extract MemFs/MemStore/Clk into one module in a scratch copy, run `cargo nextest -p memory`, compare scc test lines (I estimate several hundred lines, not measured).
3. Is `Job::write`/`append` mutation-resistant? Experiment: `cargo mutants -p memory --file src/capture.rs` in a scratch copy (expensive; not run). The repo's check-mutations.py covers only 3 injected faults.
4. Does `attach_launches` failure silently drop launch evidence (daemon.rs ~ run_owner)? Experiment: a daemon.rs test where `attach_launches` returns Err and assert hint progress and counters. Not run.
5. Do FileSystem error details ever matter for `ErrorClass::Local`? Experiment: grep DESIGN 11.2 examples (disk full, permission) against a MemFs that returns permission errors; expect today's code reports `Unavailable` regardless. Not run.
6. Sonar's published default (15) and Clippy's too_many_arguments/lines defaults are unverified from memory; fetch https://www.sonarsource.com/docs/CognitiveComplexity.pdf with a PDF reader and the clippy config page, offset past 100,000 characters.

## 6. Not covered
Tests were measured by line only, not by function metrics. Go code, rust/artifacts/, xtask/lint-check, scripts/*.py not reviewed. No cost measurement of clones; no runtime profile; no actual dependency-cycle tool (cargo-modules) used, cycles derived from `use` statements and spot-checked. DESIGN sections other than 4, 5, 11, 13, 14, 17 not read. Bernhardt and Cockburn pages were not fetched successfully (the Bernhardt page returned only a title); Ousterhout cited from memory.

## 7. Recommendation summary (size / risk)
| rec | size | risk |
|---|---|---|
| Real Store/Clock adapters and wire `main_entry` (F1) | large | medium |
| Split capture.rs big three, enum for `live`, shared `read_checked` (F2) | small-medium | low-medium |
| Move run_owner decisions to core, count attach failures (F3) | small | low |
| Identifier macro (F4) | small | low |
| ObjectKey newtype and one `vet_dir` (F5) | small-medium | low |
| Typed FsError into ErrorClass::Local (F6) | small-medium | low-medium |
| Shared test fakes (F7) | medium | low |
| Core clippy disallowed-types for std I/O (F8) | small | low |
| Enums for bools, Nanos/Handle newtypes (F9) | small each | low |
| Replace `Sources` trait by FnMut, merge schema/state, split locator by agent (F10-11) | small | low |
