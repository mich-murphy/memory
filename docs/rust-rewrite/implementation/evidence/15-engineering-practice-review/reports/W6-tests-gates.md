# W6: maintainability and structure of test and gate code

Read-only review at HEAD 41a07ff. Nothing was run. Counts come from `rg`/`wc`. Items marked [inferred] were not read line by line.

## 1. Verdict

| Criterion | Verdict | Why |
|---|---|---|
| C1 boundaries | mostly sound | `memory-core/tests/common` and `memory/tests/common/{store,fs,clock}.rs` own the shared fakes. Three fault-injecting store decorators still sit on top of `MemStore`. |
| C3 maintainable | mostly sound | The tests are mostly literal and linear. Daemon tests repeat 6 to 8 lines of setup, and the state-machine files are 1,800 to 3,100 lines. |
| C4 names | mostly sound | Test function names are good. Five file names record history (`core_f2_gaps`, `parent_privacy_review`, `privacy_port`, `fake_store`, `harness`). |
| C6 DRY | weak to mixed | The state-machine scaffold is copied three times. The `sha/len64/owner/ts/header/src_id` helpers are copied in 5 files. Both mutation scripts share a copy-and-run skeleton. `gates.sh` and the workflow are DRY. |
| C7 YAGNI | mostly sound | The test code has little speculative generality. The one abstraction beyond need is the `EtagModel` switch, which is justified. |

## 2. What is good

- `rust/scripts/gates.sh:1-43` is a 43-line single source of truth. It has a `run name cmd` helper, one status file, and a seed recorded before any gate runs. `.github/workflows/rust.yml` has one line that calls `bash scripts/gates.sh`, and the artifact upload globs match what `gates.sh` writes.
- `check-mutations.py:100-103` refuses to run unless each predicate occurs exactly once. It runs the baseline first. It counts a mutant as killed only on exit 101 plus a "panicked at" message plus a "test result: FAILED" line, and it treats a compile failure as invalid (`:123-125`). The patch cannot silently no-op. `check-window-mutation.py:50` has the same guard and also checks that the source, oracle and lock file did not change (`:73`).
- `check-lints.py:36-40` matches the specific lint code in the JSON diagnostics, not just "clippy failed". It ends with a safe control that must pass.
- `memory/tests/fake_store.rs:1-4` tests the shared fake directly against R2 behaviour measured in P2. The oracle gets its own tests.
- `memory-core/tests/coverage_guard.rs:1-41` gives negative controls for the coverage guard (`common/mod.rs:33`). The same is done for the 3 daemon-side guards (`daemon.rs:1236`, `capture_property.rs:1621`).
- `rust/artifacts/` is **not tracked**: `rust/.gitignore:3` ignores it and `git ls-files` returns nothing for it. There is no tracked `scaffold-baseline`, `coverage-probe` or `lint-check` output. The artifact question in the brief is closed.

## 3. Inventory (Q1)

Lines / test-attribute count. Counts come from `rg -c` on `#[test]`, `hegel::test`, `proptest!`. A single `proptest!` block can hold several tests, so those counts are lower bounds.

memory-core/tests:

| File | Lines | Tests | Owns |
|---|---:|---:|---|
| core_boundaries | 234 | 6 | crate and dependency boundary checks |
| core_coordinates | 640 | 14 | offsets, order proof, retention |
| core_f2_gaps | 913 | 12 | **history name**. Mixed rows: privacy aliasing, env/argv grammar, fingerprints, disposition, codex ids, UTF-8, 300-epoch history |
| core_hint | 1546 | 35 | hint line format and canaries |
| core_limits | 742 | 30 | limits and keys |
| core_locator | 1748 | 17 | property 7 and locator tables |
| core_manifest | 1640 | 21 | manifest schema, fold, golden documents |
| core_mechanics | 448 | 9 | properties 1 and 2 |
| core_planner | 343 | 10 | planner tables |
| core_verify | 125 | 8 | verify |
| coverage_guard | 41 | 1 | guard negative controls |
| parent_privacy_review | 119 | 3 | **history name**. Privacy regressions from one review |
| privacy_coverage | 381 | 12 | privacy URL and TOML tables |
| privacy_port | 650 | 16 | **history name**. Go-parity privacy vectors |
| privacy_property | 858 | 10 | property 5 |

memory/tests:

| File | Lines | Tests | Owns |
|---|---:|---:|---|
| capture | 841 | 20 | capture against a spy filesystem |
| capture_property | 1822 | 8 | property 4 at the capture layer |
| commit_protocol | 2827 | 47 | commit outcome tables and property 4 at the commit layer |
| counters | 409 | 6 | counter tests and property 3 |
| daemon | 3125 | 38 | daemon rounds, status, hints, property 4 at the daemon layer |
| fake_store | 104 | 6 | `MemStore` semantics (an unhelpful name) |
| harness | 34 | 1 | a scaffold check that the binary exits 78 |
| hook | 912 | 28 | hook |
| mutation_daemon | 279 | 2 | oracle tests for `check-mutations.py` (the name says nothing about that) |
| realfs | 640 | 17 | real filesystem adapter |
| resolve | 377 | 12 | resolve |
| state | 1208 | 35 | state files |
| watch | 582 | 20 | watch scheduler |

A maintainer adding a planner rule would go to `core_planner.rs` (343 lines, 10 tests), which is the right place. A maintainer adding a privacy rule would face four files with overlapping scope: `privacy_port`, `privacy_coverage`, `parent_privacy_review`, `core_f2_gaps` (rows 1a, 1b). Nothing says which to pick. Two files are named for process:
- `core_f2_gaps.rs` uses names like `row1a_...` and `row5b_...`. The row keys point to `09-f2-counterparts.md`, which only a historian has.
- `parent_privacy_review.rs` is named for who found the bug.

## 4. Shared support and duplication (Q2)

After the consolidation commits there are 3 shared fakes in `memory/tests/common/` (`MemStore` at store.rs:109, `MemFs` at fs.rs:47, `TestClock` at clock.rs:23). Fault-injecting wrappers on top of the shared store that are still duplicated:
- `FakeStore` at `commit_protocol.rs:289` and at `capture_property.rs:277`: each has its own `Kind`, `Op`, `Foreign`, `Effect`, `Rule`, `kind_of`, `arm`, `intercept`, `gate`, `put`. The commit_protocol version is a superset (it adds `Churn`, `Revoke`, `skip` and `times`). The `Op`/`Foreign`/`Effect`/`Rule` blocks are about 35 lines each and nearly identical (`commit_protocol.rs:122-160` against `capture_property.rs:134-170`). Estimated shared knowledge: about 120 lines (types and `intercept`/`gate`/`put`).
- `FStore` at `daemon.rs:431` carries a third lineage of the same thing (own `rules`, `offline`, `revoked`, `crash_at`, `put_log`). `Judged` (`mutation_daemon.rs:83`) and `Ghost` (`daemon.rs:2653`) are small and fine.
- Free helpers `sha`, `len64`, `owner`, `ts`, `header`, `src_id` are defined in `commit_protocol.rs:57-89`, `capture_property.rs:64-96` and `daemon.rs:72-218`, with variants in `capture.rs:29-49` and `watch.rs:30`. A diff of 44 lines between two of them shows 26 changed lines, which is mostly cosmetic. About 50 to 80 lines are duplicated.
- The `Shapes` guard struct is copied per file (`daemon.rs:1167`, `commit_protocol.rs:602`, `capture_property.rs:501`) even though `memory-core/tests/common/mod.rs` already has a shared `Shapes` for the core crate. The `memory` crate does not share its own.
- `memory/tests/*.rs` attach the `common` files with `#[path = "common/store.rs"] mod mem_store;` in 6 places instead of a `common/mod.rs`. It works and is a mild eyesore.

Overall estimate: about 600 to 900 duplicated lines across the three big files. These are fake and scaffold lines. The model and scenario content is different in each file.

## 5. Property 4, Hegel and proptest (Q3)

Within each file there is **one** model: a single `Step` enum, `Reference` and `Sut`. Two thin drivers sit on top:
- `hegel_*` (`capture_property.rs:1477`) plus a `World` that draws steps (`:1363-1476`, about 115 lines).
- `proptest_*` (`:1601`) plus `step_strategy` (`:1503-1544`) plus `RefMachine`/`PtMachine` (`:1545-1600`). That is about 100 lines.

So the generator is written twice per file (about 100 lines each) and the model once. That meets the design's "written in both" requirement without a doubled model.

Across files the whole stack is tripled: three `Step` enums (`commit_protocol.rs:922`, `capture_property.rs:984`, `daemon.rs:1270`), three `Reference`, three `Sut`, three `World`, three `RefMachine`/`PtMachine`. The layers differ (commit, capture, daemon) and so do their steps. I did not read all three `Reference` bodies in full, so the claim that the three share no semantics is [inferred]. The scaffolding (the `hegel::Hegel::new(...)` body at `commit_protocol.rs:1249-1262` against `daemon.rs:2088-2101`, and the `require_all` + `end_case` block) is a verbatim copy and could be one helper.

## 6. Size of the three largest files (Q4)

Mixed. About 35% of each file is the state machine and fake. The rest is scenario tables.

- `daemon.rs` has 38 `#[test]` functions but 83 `fn` items in total. Its tests are distinct: they cover start error codes, shutdown, status deferral, hint partial progress, rebase exhaustion and more. The repeated setup is real: `let tmp = Tmp::new()?; let cfg = tmp.config(true)?;` appears 30 times, `let mut d = boot(&tmp, &cfg, ...)` 23 times, `d.clock_mut().mono = N_000_000_000` 17 times, and `d.reconcile_once().map_err(e)?` 46 times. Round-count assertions `assert_eq!((r.landed, r.failed, r.deferred), ...)` repeat throughout.
- `commit_protocol.rs` (47 tests): its `FakeStore` is about 160 lines and its state machine about 600 lines (`:675-1260`). The tables are distinct.
- `capture_property.rs` is almost entirely one property (8 tests), with 1,000+ lines of model and fake.

Sketches (not applied):

1. A `rig` builder. Today at `daemon.rs:2872-2880`:
   ```
   let tmp = Tmp::new()?; let cfg = tmp.config(true)?;
   let list = vec![src_of("s1", 1)?];
   let store = FStore { offline: true, ..FStore::default() };
   let mut d = boot(&tmp, &cfg, files_with(b"hello\n"), store, &list, WALL)?;
   ```
   Could be `let mut rig = Rig::one_source(b"hello\n").offline()?;` Saves about 5 lines times 20 tests, so about 100 lines.
2. A clock step helper. `d.clock_mut().mono = 61_000_000_000; let r = d.reconcile_once().map_err(e)?;` becomes `let r = d.round_at(61)?;`. `round_at` already exists at `daemon.rs:2899` and is used 9 times. The other 17 sites could use it. Saves about 30 lines.
3. A report assert. `assert_eq!((r.landed, r.failed, r.deferred), (0, 1, 0))` becomes `assert_report!(r, landed=0, failed=1, deferred=0)`. Marginal, but each failure message would then say which field differs.

Combined, builder and helper changes would remove about 150 to 250 lines from `daemon.rs` and make its scenarios easier to scan. Moving the shared scaffold into `common/` would take out about 500 lines more across the three property files.

## 7. Test names (Q5)

Good (say what happens):
- `lost_reply_converges_without_duplicate_epoch` (`daemon.rs:697`)
- `unwritable_index_is_a_round_error_when_status_is_not_due` (`daemon.rs:2469`)
- `if_none_match_on_an_existing_key_is_412_and_changes_nothing` (`fake_store.rs:20`)
- `removed_source_needs_two_observations_then_is_deleted` (`capture_property.rs:1736`)
- `transformed_capability_cannot_be_rebound_to_a_protected_source` (`parent_privacy_review.rs:42`)

Poor:
- `row1a_backup_aliased_spellings_of_all_eleven_names_select_structural_rules` (`core_f2_gaps.rs:140`): the `row1a` prefix is a history reference and the name is too long.
- `row5b_changed_and_failed_are_closed_local_retries_never_done` (`:496`): the name says "never done" and nothing about the subject.
- `shutdown_table` (`daemon.rs:2813`), `start_error_code_table` (`:2364`), `state_api_failure_table` (`:2409`): the name says "a table" and nothing about the rule being tested.
- `scaffold_binary_refuses_capture_instead_of_success` (`harness.rs:3`): a scaffold-era name for a standing check.
- `window_defaults_fill_the_missing_half` (`daemon.rs:2392`): needs context to understand.

Property tests' names follow the design's numbering (`hegel_property4_...`), which is good. Report 24 renamed or removed some tests already, so this is a short list, not a pervasive problem. About a dozen `row*` names remain.

## 8. Gates (Q6)

- `gates.sh` runs 13 sub-gates (DESIGN lists 9; the extras are core bans, doctests and two mutation scripts, all recorded in `docs/quality-gates.md`). It is readable. The comment at `:23-26` explains why properties are split across 04/05/06.
- DRY between `gates.sh`, the workflow, `docs/quality-gates.md` and `rust/README.md`: the workflow calls `gates.sh`. `docs/quality-gates.md:28` and `README.md:21` give the command and then re-list the gates in prose. `quality-gates.md:35` is a long single line that enumerates every mutation and gate. It must be edited whenever `gates.sh` changes. [Medium-low: three places to update.]
- The numeric prefixes are shared (two gates named `03-`, three named `04-`/`06-`), so `status.tsv` ordering is a weak signal.
- Both mutation scripts duplicate: environment setup (`HEGEL_*`, `PROPTEST_*`, minimum 500 cases check), `copytree` ignore patterns, `MEMORY_MUTATION_TARGET` handling, and a `run()` wrapper. About 40 lines are the same in each. `check-window-mutation.py` is the older single-mutant form of `check-mutations.py`: the window mutant could be a fourth entry in `MUTANTS`. It would need one extra field (a different success predicate: `operation=` and `seed=` in the output). The special `import hashlib` mid-function (`check-window-mutation.py:46`) suggests it grew by accretion.
- Robustness: strong on the apply side (exact-once predicate, baseline, compile check, `finally` restore). Residual risk: the mutants are text patches against formatted source, so a rustfmt change that wraps those lines would make the guard refuse (a loud failure, not a silent pass). A "killed" verdict needs a test failure, not the intended test failure; with a clean baseline that is acceptable.
- Artifacts: untracked, nothing stale in git. The local `artifacts/` directory holds logs only (and the lint-check scratch crate).

## 9. DESIGN 15 coverage map (Q7)

| Item | Status | Where |
|---|---|---|
| P1 capture correctness 1a/1b | implemented | `core_mechanics.rs:228` (hegel), `:235` (proptest); mutation check `check-window-mutation.py` |
| P2 coalescer | implemented | `core_mechanics.rs:287`, proptest at `:292` |
| P3 idempotence | implemented | `counters.rs:343` (hegel; no proptest twin, not required by design) |
| P4 state machine, both engines | implemented in three layers | `commit_protocol.rs:1249/1422`, `capture_property.rs:1477/1601`, `daemon.rs:2088/2287`. The step list in the design (e.g. `go-offline`, `revoke-token`, `delete-index`) is spread across the three layers. I did not check that every named step exists in some layer [inferred]. |
| P5 privacy | implemented | `privacy_property.rs:655`, `core_hint.rs:1504/1535` |
| P6 schemas | partial | round-trip `core_manifest.rs:1584`; checkpoint k vs k+64 fold list at `core_manifest.rs:26` ("fold-across-checkpoint") [inferred to cover it]; unknown `schema` rejection not located by name |
| P7 locator | implemented | `core_locator.rs:1646/1653/1660` |
| Coverage guard | implemented | `common/mod.rs:33`, `coverage_guard.rs:1`, `daemon.rs:1236`, `capture_property.rs:1621` |
| Counter: idle minute makes zero syscalls and at most the listed timer wakeups | partial | `counters.rs:241` checks zero PUTs and reads for an idle round; timer wakeups are not asserted (no timer exists yet; daemon loop is a later slice) |
| Counter: 30k reconcile at most one stat per source | implemented | `counters.rs:251`; the 1 s target is printed, not asserted (stated at `counters.rs:3`) |
| Counter: 59 KB append reads at most 59 KB + 68 KiB | implemented | `counters.rs:271` |
| Journey test (real daemon, fixtures from session timelines, restore via `memory-reader`) | absent | needs the reader (`memory-reader` is a stub) and the real shell; later slice |
| Footprint: hook latency | partial | `hook.rs:831`, `:869` (ignored measurements, not gated) |
| Footprint: CPU per capture, RSS 30k idle and peak, disk 20 MB, oldest uncaptured age | absent | later slice (needs the real daemon loop) |

## 10. Proportion (Q8)

23,600 test lines to 11,700 source lines is about 2.0:1. For a privacy, data-loss and commit-protocol codebase that is not excessive in itself; report 24 already pruned weak tests. Where it is specifically over- or under-invested:
- Over-invested: the commit protocol is tested at three layers (`commit_protocol.rs`, `capture_property.rs`, `daemon.rs`), each with its own store fake and its own Hegel and proptest drivers. That is about 7,800 lines for one property. The marginal fault found per layer is unknown here; report 24 W2 found no real counterexample in about 9,000 runs, only coverage-guard misses. This supports trimming the number of layers and fakes (not the property itself).
- Over-invested: privacy tests are spread over `privacy_port`, `privacy_coverage`, `privacy_property`, `parent_privacy_review`, `core_hint`, `core_f2_gaps` (about 3,500 lines). For the highest-risk area that is deliberate, but the split by history makes gaps hard to see.
- Under-invested: `capture.rs` stream/span reading. Report 24 W3 found 17 surviving mutants in `stream_span`, with `Ok(vec![])` surviving; `fsync_dir` to `Ok(())` survived. These are data-loss paths. Whether the later rounds closed them was not checked [inferred open].
- Under-invested: `realfs.rs` (640 lines) is the only real-filesystem test and report 24 W4 noted a symlink swap between stat and read is followed (`realfs.rs:126`); I did not check whether a test now covers it.
- Under-invested: mutation checking covers 4 mutants by script. `cargo mutants` on `memory-core` is monthly per DESIGN and was not in the gate; run once only on three `memory` files by W3.
- Under-invested: journey and footprint (section 9), both slice-bound.

## 11. Findings, ranked

| Id | Sev | Crit | Where | Observation | Remedy / size |
|---|---|---|---|---|---|
| F1 | medium | C6 | `commit_protocol.rs:122-300`, `capture_property.rs:134-300`, `daemon.rs:297-440` | Three lineages of the same fault-injecting store decorator (`Op`, `Kind`, `Effect`, `Rule`, `intercept`, `gate`) sit on top of the shared `MemStore`. | Move one `FaultStore` into `memory/tests/common/fault_store.rs` (take the commit_protocol superset). Net about -300 lines, 4 to 6 hours. |
| F2 | medium | C6 | `commit_protocol.rs:57-89`, `capture_property.rs:64-121`, `daemon.rs:72-218`, `capture.rs:29-49` | `sha/len64/owner/ts/header/src_id` and per-file `Shapes` copied. | `common/ids.rs`, plus a shared `Shapes` as in `memory-core/tests/common`. About -150 lines, 1 to 2 hours. |
| F3 | medium | C4 | `core_f2_gaps.rs`, `parent_privacy_review.rs`, `privacy_port.rs`, `fake_store.rs`, `harness.rs`, `mutation_daemon.rs` | Files are named for history or role-less terms. A privacy rule has four candidate files. | Rename: `core_f2_gaps` split by subject into `privacy_*`/`disposition`/`history`; `fake_store` to `mem_store_semantics`; `mutation_daemon` to `mutation_oracles`; fold `parent_privacy_review` into `privacy_regressions`. Pure moves, 2 hours. |
| F4 | low-medium | C3 | `daemon.rs` (30 repeats of `Tmp::new` + `config`, 23 `boot`, 17 mono sets) | Repeated setup hides the scenario. A `Rig` builder exists in `counters.rs:148` but not in `daemon.rs`. | `Rig` builder plus `round_at` everywhere. About -200 lines, 3 hours. |
| F5 | low-medium | C6 | `check-mutations.py`, `check-window-mutation.py` | Two scripts with about 40 duplicated lines of env/copy/run; the window mutant is a fourth entry of `MUTANTS` with a different success predicate. | Merge into one script with a per-mutant `killed_when`. About -50 lines, 2 hours. |
| F6 | low | C6 | `quality-gates.md:35`, `README.md:25-35` | The gate list is restated in two docs besides `gates.sh`. | Docs say "see `gates.sh`" and list only what is not obvious from it. 30 minutes. |
| F7 | low | C3 | `capture_property.rs`, `commit_protocol.rs`, `daemon.rs` Hegel/proptest driver bodies | The driver (`Hegel::new ... require_all ... end_case`) is repeated verbatim. | One `run_hegel_machine(steps, World::new)` helper. About -30 lines, 1 hour. |
| F8 | low | C4 | `core_f2_gaps.rs:140-755`, `daemon.rs:2364/2409/2813` | `rowNx_` prefixes and `*_table` names do not state the rule. | Rename about 12 tests while splitting F3. |
| F9 | low | C7 | `harness.rs` | A scaffold-era check that the binary exits 78. Not a gate any more (report 24 W1 notes docs still naming the scaffold gate; README:38 line was flagged). | Keep as a CLI contract test or delete when the daemon loop lands. 15 minutes. |

## 12. Checked and found fine

- No `#[allow]` use in the sampled tests (lint table denies `allow_attributes`).
- `gates.sh` fails closed on missing tools and runs under `set -uo pipefail` without hiding statuses.
- Property case floor of 500 enforced in `gates.sh:9` and in both mutation scripts.
- Coverage floor of 80 is the amended A9 value, with 97.7% last measured; not a ratchet by design.
- The workflow pins action SHAs and uses the same command as local runs.

Not read: bodies of the `Reference`/`Sut` models, `hook.rs`, `state.rs`, `watch.rs`, `resolve.rs`, `core_locator.rs`, `core_hint.rs`, `core_limits.rs`, `release.yml`, `ci.yml`, `Makefile`. I did not run anything, so every statement about behavior is from reading only.
