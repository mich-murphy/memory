# Pure core mechanics — progress and scope

> **Completed historical worker report; all paths released.** The parent subsequently
> passed the corrected integrated gates at 98.1658% coverage against the unchanged
> 98% floor. Earlier pending aggregate/coverage statements below are snapshots.
> Locator/manifests/store/reader and full requirements acceptance remain outstanding;
> see [parent review](02-parent-review.md) and the
> [active handoff](../../session-capture-handoff.md#current-session-checkpoint).

Status: scoped implementation and focused checks complete; ownership released; independent safety prerequisite, NOT slice 1 acceptance.

Owned paths: `memory-core/src/{domain,planner,coalescer}.rs`, `tests/core_*.rs`, this report; parent subsequently assigned `rust/scripts/check-window-mutation.py` for the actual isolated mutation gate. Parent owns wiring/manifests; privacy and scaffold remain separate. No I/O, locator, manifests, live operations or property 4 implementation. P1 is validated synthetic evidence only; P5 real-agent prevalence remains pending. Slice 0 coverage remains a separate prerequisite.

API: `domain` holds validated brands and exhaustive source/owner/observation/position/outcome types; `planner::plan(&PlanInput) -> Planning` consumes independent window/adopt/audit evidence; `coalescer::Coalescer` accepts caller-supplied monotonic `Duration` times with signal/poll/complete, preserving later signals while a capture is in flight. Audit eligibility is explicit and windows never prove interior integrity. Repeat planning has no writer-version input and yields no work, not a store PUT claim.

Source-kind extensions explicitly name missing families from acceptance-map section 2, without implementing locator admission or widening arbitrary config paths. Exact finite path/subtype/owner matrix remains parent/locator acceptance work.

Validation and source hashes will be appended after focused checks. No commits; no shared manifest edits.

## Implemented APIs / invariants

- `domain`: four separately branded coordinate IDs validated on construction **and serde deserialization**; empty/dot/traversal/control/non-ASCII spelling rejected, bounded at 1,024 ASCII bytes. Actual non-ASCII/path source names must become root-qualified stable content-independent IDs at the shell boundary (e.g. a hash of coordinates), retaining provenance separately. Rejected identifier syntax is not `UnknownKind`. This is not a locator or a claimed root-collision solution.
- `SessionKey.session` uses the exact wire name `session_id`; owner session/shared golden roundtrips and schema assertions distinguish session coordinates from sessionless shared resources and reject the old field spelling.
- `ETag` is deliberately **not** an ID: opaque nonempty token, quotes and punctuation preserved exactly for fencing, control characters rejected. SHA-256 owned spellings accept exactly 64 lowercase hex characters. Manual `JsonSchema` implementations expose the same string restrictions, rather than claiming an unrestricted String schema validates them.
- `SourceState` contains committed `EpochPosition`, independent head/tail digests, physical observation/trust and audit bookkeeping. The resumable hasher implementation/cache format is intentionally absent: it is an optimization, never evidence. Snapshot fingerprints explicitly represent DB plus present/absent WAL. Local physical fingerprints do not publish unsafe raw protected hashes.
- `planner::plan(&PlanInput) -> Planning`: evidence is supplied independently by shell. Missing windows and first-mismatch audit reread return `NeedWindows` / `NeedAuditRecheck`, never a pretend no-op. `Ready` includes `refresh_stat` and `adopted` for local-only cache work. Full adopt digest comparison precedes append; no byte-zero replay for a valid rebuild. Protected rules changes invalidate epochs; writer version is not an input.
- All append table rows are represented, including birth identity differences, truncation, equal-size settled fast path, equal-size changed/racy window checks, growth checks and two-miss deletion. `Observation::DeletionRecorded` explicitly prevents repeated committed deletion work. The shell must supply it only after successful publication; missing counters/reset-on-presence remain shell state, not inferred I/O.
- Eligible, independently confirmed full-audit mismatches replay append **and snapshot** classes, even when snapshot physical fingerprint is unchanged. An initial audit mismatch requests a second stable read. `audit_eligible` checks identity, size and mtime across captured/before/after stats; `Ineligible` makes no convergence claim. The shell must compare transformed protected bytes and independently pin audit input. Bounded windows do **not** prove interior bytes; the property deliberately demonstrates escaped interior rewrites before eligible audit repair.
- `coalescer::{Window,Coalescer}`: strict seconds config, positive quiet and cap >= quiet, defaults 60/300. Caller-supplied monotonic `Duration`, per-owner regression errors, Stop debounced, SessionEnd immediately poll-eligible, one in-flight capture, later signals retained through complete. `wake_action` exposes >120s wall-minus-monotonic gap => reconcile and caller-selected jitter over 30s. Shell still owns 60s tick, actual clocks, randomness and scheduling; native P6 remains distinct. Cap liveness is subject to the explicit one-in-flight constraint, not a promise to run simultaneous captures.
- Closed domain error/outcome enums distinguish SourceChanged, BackupFailed, disk full, deterministic faults, source/owner parking, host auth pause, cancellation and guarded-commit outcomes. Retry/backoff/parking time policies and commit protocol are not implemented here.

## Source-family vocabulary (not locator acceptance)

All original 19 DESIGN variants remain. Known wire tags are explicitly dotted (e.g. `claude.transcript`, `claude.file_history`, `codex.rollout`, `pi.session`), not accidentally underscore-prefixed agent tags. Newly named families below expose acceptance-map omissions, rather than allowing an arbitrary file through a generic config kind. Classes/ownership here are **proposed matrix entries from map section 2**, not implemented path admission; parent/locator must finish finite subtype/path/owner fixtures before R1 acceptance.

| Known missing family | Domain vocabulary | Proposed strategy / ownership condition |
|---|---|---|
| Claude session-env | ClaudeSessionEnv | Snapshot; explicit unambiguous session UUID association |
| Claude plans/paste-cache/usage-data/memory | ClaudePlan / ClaudePaste / ClaudeUsage / ClaudeMemory | Snapshot; shared unless producer association supplied |
| Claude known nontranscript auxiliary | ClaudeAux | Snapshot; explicit session attribution |
| Codex memories/log | CodexMemory / CodexLog | Snapshot / append, shared |
| Pi known mcp/models-store/keybindings/instruction/asset families | PiConfig | Snapshot; finite subtype/path matrix still required, not raw fallback |
| Pi root logs | PiLog | Append, shared |
| Launch | No SourceKind added | Sanitized deduplicated launch records; hook-only durability is later work |
| Learned roots | No content kind added | Shell supplies stable namespace/ownership facts; roots remain preserved system of record |

Original kinds still need the mapper's explicit classifications: mutable Claude teams/todos/tasks/file history/debug, write-once tool results, exact config asset subtypes, Codex history vs session-index IDs, compressed logical-vs-physical representation, Pi child roles/snapshot/package provenance. No enum-count claim substitutes for the 14 grouped R1 source categories.

## Tests and applicable coverage guard

`core_mechanics.rs`: independent complete byte arrays, immutable per-case capture snapshots and iterator-only reassembly oracle. Cases cover append, head start/end, tail start/end, rename-replace, truncate-regrow, equal-size/equal-mtime racy rewrite, escaped interior rewrite plus eligible audit repair, and pure truncation. Every property invocation exercises every applicable edit category; coverage guard requires every category and at least one replay branch (pre-manifest only, **not** retained multi-epoch model coverage). Expected bytes never come from planner windows or resumable state. Binary/non-UTF8 and incomplete-line bytes are intentional.

The disabled-window negative control is a **test-only mutant algorithm**, not a production feature flag. It always appends the suffix after a changed window. The same independent prefix assertion must panic; `#[should_panic(expected = ...)]` verifies that failure in ordinary test/CI execution. No production mutation bypass exists. This control alone establishes oracle sensitivity, not the DESIGN production-source mutation gate or a general mutation score. `rust/scripts/check-window-mutation.py` additionally copies the workspace into private scratch, proves the unchanged baseline, uniquely disables the actual production window-comparison predicate, and requires the same property1 runtime byte assertion to fail; compile failure does not count. No mutation is written to the checkout. Execution evidence follows below.

Property 2 uses independent elapsed-time arithmetic across virtual schedules in both Hegel and proptest; dedicated tests cover quiet/cap boundaries, fresh SessionEnd, Stop, pending SessionEnd during flight, regression/config/wake boundaries and independent owners. Property 3 asserts zero **planned capture work** on unchanged append, snapshot and committed absence, including writer-version changes. There is no store and thus **no zero-PUT claim**.

Restart, crash-after-manifest, lost-reply and delete-index generator coverage is NOT applicable to this pure subset and NOT claimed complete. Property 4, actual retained manifest epoch persistence, store outcomes and reader reconstruction remain unimplemented. Slice 1 requires locator, manifests, privacy acceptance and other properties too.

## Final focused evidence — 2026-11-07

**Scoped implementation complete; owned paths released to parent. NOT slice 1 acceptance.**
All commands below ran from repository root on branch `rust/scaffold`, source base HEAD
`57d8400f632ab47a81e34d060f86d3687123153e` plus the uncommitted source identities below.
No commits, live hooks, credential/transcript reads, source-data changes, installs or remote operations.
Parent owns integration and the next aggregate gate; this worker did not run competing aggregates.

Pinned shell prefix (exact flags/limits):

```sh
nix develop --offline --max-jobs 2 --cores 2 -c env \
  CARGO_BUILD_JOBS=2 RUST_TEST_THREADS=2 \
  HEGEL_TEST_CASES=2000 HEGEL_SEED=730104 \
  PROPTEST_CASES=2000 PROPTEST_RNG_SEED=730104
```

Rust/cargo: `rustc 1.99.0 (b940084d7 2026-09-28)`, `cargo 1.99.0 (5f94df478 2026-08-27)`,
`/nix/store/0nmx02v6h7c792800l861dkj17df9ifm-rust-minimal-1.99.0/bin/{rustc,cargo}`.

### Final normal validation

Final combined invocation, prefix above followed by:

```sh
sh -c 'rustfmt --edition 2024 rust/crates/memory-core/src/domain.rs rust/crates/memory-core/src/planner.rs rust/crates/memory-core/src/coalescer.rs rust/crates/memory-core/tests/core_mechanics.rs rust/crates/memory-core/tests/core_planner.rs && cd rust && cargo clippy --locked -p memory-core --lib --test core_mechanics --test core_planner -- -D warnings && RUSTDOCFLAGS="-D warnings" cargo doc --locked -p memory-core --no-deps && cargo test --locked -p memory-core --test core_planner --test core_mechanics'
```

Exit **0**. Focused clippy (workspace lints and warnings denied) passed; rustdoc warnings denied passed;
`core_mechanics` **10/10**, `core_planner` **8/8** passed. Each Hegel and proptest property 1/2 used
**2,000 cases** with seed **730104**. Mechanics elapsed **133.89 s**, planner **0.00 s**.
The final hash parser uses the native array chunk API, without suppression. Test nested condition was
collapsed per clippy; neither production nor test lints were relaxed. The owner golden/schema correction
is in this final passing suite.

Final read-only owned-format/Python-syntax check, shell prefix without property variables (same limits):

```sh
sh -c 'rustfmt --edition 2024 --check rust/crates/memory-core/src/domain.rs rust/crates/memory-core/src/planner.rs rust/crates/memory-core/src/coalescer.rs rust/crates/memory-core/tests/core_mechanics.rs rust/crates/memory-core/tests/core_planner.rs && python3 -c "compile(open(\"rust/scripts/check-window-mutation.py\").read(), \"check-window-mutation.py\", \"exec\")"'
```

Exit **0**. No whole-workspace formatting write was used.

### Actual production-source mutation proof

Final invocation, prefix above followed by:

```sh
python3 rust/scripts/check-window-mutation.py
```

The final shell invocation redirected synthetic stdout/stderr to a unique `mktemp` log, printed selected
summary/assertion lines with `grep`, and propagated the exact script exit. Exit **0**.
The script copied the workspace/lock into private auto-removed scratch and used a private target directory.
It ran this identical command against normal then mutated source:

```sh
cargo test --locked -p memory-core --test core_mechanics \
  property1_independent_capture_and_eligible_audit -- --nocapture
```

- Normal baseline: exit **0**, **2 passed**, 2,000 cases per engine, **133.53 s**.
- Exact unique production predicate: `if windows.head != state.head || windows.tail != state.tail {`.
- Scratch-only replacement: `if { let _ = windows; false } {` (plus a test-only scratch comment).
- Mutant compiled successfully (**1.17 s**); then **both Hegel and proptest failed the independent
  runtime byte-prefix assertion**, `operation=1 seed=0`, at `core_mechanics.rs:156`.
  Mutant test exit **101**, **0 passed / 2 failed**, **0.22 s**. This was **not** a compiler failure.
- Script checked source/oracle/lock hashes did not drift, accepted only the runtime assertion kill,
  returned **0**, and removed scratch. Original checkout window predicate stayed enabled.
- Script inherits CI case counts with minimum **500** (DESIGN CI target); default/local evidence is
  **2,000**, not lowered. It passes `--locked` and reports planner/oracle/lock SHA-256 identities.
- Proptest emitted a failure-persistence source-root warning in private scratch; it still ran and
  shrank to seed 0. Its mutant regression file was inside that auto-removed scratch, not the checkout.

### Attempt history / stale evidence

Earlier mechanics **8/8** + planner **5/5** passes at 2,000 cases were **stale after later edits** and
are not the final evidence above. Initial compile failures (unsupported container schemars validation
attributes, Hegel return signature, ambiguous Strategy import) were fixed in owned files. A later focused
clippy attempt exited **101** on an active privacy `url` dependency gap; a subsequent invocation timed out
at **240 s** before cargo output during shell changes; another exited **101** on the active privacy
`RawValue`/missing `raw_value` feature. Shared owners resolved dependencies; no shared manifests were
edited here. Privacy owner reported the domain array-chunk lint blocker, which was corrected; own final
focused clippy initially exited **101** on nested-if guidance, corrected without suppression, then final
unchanged-strength checks passed as recorded. An earlier actual-source mutation
run also passed, but its oracle hash preceded the final clippy edit; the final rerun above supersedes it.

### Exact final source identities (SHA-256)

| Path | SHA-256 |
|---|---|
| `DESIGN.md` | `30a0ad68de1c9cbbeb9e0808b6e397958546c17eadd18b285ead1a1b9b8455df` |
| `AGENTS.md` | `ab4516e6f554f02dd06640c2bfa8e189c839fd45f07188b8d6111d54a0327889` |
| `flake.nix` (shared owner) | `48b67b22e317151981e14fc7cbd0bedc9e420efc15e9b52fb485a8d665f6bdde` |
| `flake.lock` (shared owner) | `ec6aabae0c2ec50a7f01eed74da33d914dab68ae08dbc21e76e323451ef085e1` |
| `rust/Cargo.toml` (shared owner) | `50b9ce468035e12a2b977ebd4b4f949d9c63d69618c22dbb9acf82e7f6817b45` |
| `rust/Cargo.lock` (shared owner) | `335e489dca6ce321d4c423c46b0580765f60e4ed76bac5f6a2e07a79801fa24f` |
| `rust/crates/memory-core/Cargo.toml` (shared owner) | `bbe0533000e51ce4ce6f99c0f3d34abf0d6498d87a0dfa7140ab9f67a8fb0149` |
| `rust/crates/memory-core/src/lib.rs` (parent) | `11dac4fe29919db813d695c7b405d5617d586731cef29c07c144b6f4fa36d3e9` |
| `rust/crates/memory-core/src/domain.rs` | `96b3e73abdf7da4a59c95939d42ac62decca3b1c5e4144eeb370a0d5a32c6896` |
| `rust/crates/memory-core/src/planner.rs` | `4a5f3d2933882f69e2fb6fc92bfc28b37feb66a75d8b1183caf5f74a32031e95` |
| `rust/crates/memory-core/src/coalescer.rs` | `5f6cd342e6b24de6dd0342401df0e0e21f62e44a901dc728fa69290df91d5228` |
| `rust/crates/memory-core/tests/core_mechanics.rs` | `c8df76c9be6354f7357aa89d05a452f60566dbb8c0e7408249aca4809f617505` |
| `rust/crates/memory-core/tests/core_planner.rs` | `936128b0c87dfc81e88d07877d6d90429e9d53835ecf29f7d11303a76327e6cc` |
| `rust/scripts/check-window-mutation.py` | `0809afcbd8e46ab28371d17dca24ae4332a9f8fe65744f68f6ff1eb88e4195e1` |

Shared source identities record what was inspected after final focused checks, not ownership or a claim
that parent/scaffold/privacy paths are frozen forever. Parent must rebind aggregate evidence after any changes.

## Remaining gates / explicit ownership release

Release all seven owned paths: three production modules, two `core_*.rs` tests, the mutation script and this report.
Parent may integrate and independently review them now. No further edits or running commands remain with this worker.

Not run here: full workspace nine-gate aggregate, nextest aggregate, cargo-deny, schema export/drift aggregate,
coverage floor, lint-check crate, cargo-mutants campaign, property 4/store simulation, locator/manifests,
reader/importer journey, native Mac/BC-01 integration, P5 prevalence, R2, sleep observation, deployment or cutover.
P1 remains synthetic; actual P5 prevalence is pending, never inferred from these generators. The scoped core
work is an independent safety prerequisite while slice 0/P5/remaining slice 1 requirements remain pending.

## Bounded coverage correction — reopened 2026-11-07

Parent's canonical integrated run at 2,000 cases / seed 730107 passed all gates except coverage:
**1,261 / 1,363 = 92.5165%**, below the unchanged **98%** scaffold floor. This supersedes any
interpretation of the focused evidence as aggregate acceptance. Acceptance remains pending.
Evidence inspected: `implementation/evidence/01-integrated/source-before.json` and `missing-lines.txt`;
`coverage.json` / `status.tsv` are parent-owned evidence, not a request for a second coverage run.

Reopened ownership is bounded to the three core modules, `tests/core_*.rs`, and this report.
Plan: external integration tests for branded-ID invalid boundaries / accessors / serialization,
ETag and digest schemas, valid nondefault window conversion and actual quiet/cap timing, duplicate
completion behavior, and remote protected-rules invalidation before adoption. No new production
functionality, floor changes, exclusions, assertion removal or line-count edits. The hex parser's
post-validation fallback is genuinely unreachable via safe public input; preserve and explain it.
Parent alone reruns full coverage/aggregate; focused new tests and clippy only here, max two workers.

### Coverage correction result / release

**Bounded test additions complete and released; aggregate acceptance still pending.** Inspected all four
parent evidence files. `coverage.json` confirms `{count:1363, covered:1261, percent:92.51650770359501}`;
`status.tsv` has exit 0 for fmt/clippy/deny/core-bans/nextest/doctests/Hegel/proptest/window-mutation/schema/
lint-check and exit **1** for coverage. That is parent-run evidence at seed **730107**, not a new worker
aggregate run. The **98%** floor is unchanged.

Only new executable code in this correction is external integration test file
`rust/crates/memory-core/tests/core_boundaries.rs` (five tests). **No production source changed.**
No bugs were exposed by these tests, no exclusions/suppressions were added, and no assertion was removed.

| Parent-reported missing production lines | New behavioral checks |
|---|---|
| coalescer 41–45, 49–54 | Positive nondefault `quiet=2,cap=5` conversion and serde roundtrip, exact output seconds, default 60/300 serialization conversion, permitted quiet=cap. Real scheduler emits at quiet 2s and, under repeated signals, cap 5s rather than quietly reverting to defaults. |
| coalescer 164 | Completion for a known owner with no in-flight capture returns NotInFlight; pending edge is preserved and invalid completion at a far-future time does not advance that owner's clock. Repeat completion after success is also rejected and later signals remain schedulable. |
| domain 41, 53–55, 73, 85–87, 105, 117–119, 138–139, 143–145, 149–151 | Every branded coordinate independently rejects empty/dot/dotdot/slash/control/non-ASCII and 1,025-byte input, accepts the 1,024-byte boundary, preserves case/punctuation through as_str, From, serialization and validated deserialization. |
| domain 168–170, 783–788, 792–797 | Opaque fence exact serialization/From and Unicode control rejection; distinct ETag schema title/string/nonempty/control pattern with no coordinate length cap; canonical lowercase digest schema title/pattern plus serialization and invalid lengths/alphabet. |
| planner 197 | Protected remote epoch with changed **or removed** rules replays RulesChanged *before* requesting adopt digest; equal rules requests Adopt, preserving the remote epoch. |
| domain 232 | **Preserved unreachable defensive fallback**, not artificially covered: the preceding validation proves length 64 and every byte in `0..9,a..f`; every two-byte pair therefore has two `to_digit(16)` results of Some. No safe public input can take the fallback. Boundary rejection tests cover invalid bytes before this point. Removing the fallback, using unsafe mutation or changing line layout solely to alter coverage would be incorrect. |

These are tests for actual externally visible behavior, not calls added merely to execute getters or
schema functions: accessor/From/serde spelling and distinct owned schema domains are asserted explicitly.
No property generators/oracles or existing production window mutation point changed. Do not infer a new
coverage percentage until parent reruns its unchanged canonical gate.

Exact focused command, repository root:

```sh
nix develop --offline --max-jobs 2 --cores 2 -c env CARGO_BUILD_JOBS=2 RUST_TEST_THREADS=2 \
  sh -c 'rustfmt --edition 2024 rust/crates/memory-core/tests/core_boundaries.rs && cd rust && cargo test --locked -p memory-core --test core_boundaries && cargo clippy --locked -p memory-core --lib --test core_boundaries -- -D warnings'
```

Exit **0**; new tests **5 passed / 0 failed**, test execution **0.00 s**; focused clippy with all warnings
denied passed. Same pinned Nix Rust 1.99 shell as preceding evidence; no property case-count override was
needed because this invocation executes only the five new unit-style integration tests. Expensive unchanged
2,000-case properties were intentionally **not rerun**; parent owns the final aggregate/coverage invocation.

Final read-only owned formatting check:

```sh
nix develop --offline --max-jobs 2 --cores 2 -c env CARGO_BUILD_JOBS=2 RUST_TEST_THREADS=2 \
  rustfmt --edition 2024 --check \
  rust/crates/memory-core/src/domain.rs rust/crates/memory-core/src/planner.rs \
  rust/crates/memory-core/src/coalescer.rs rust/crates/memory-core/tests/core_mechanics.rs \
  rust/crates/memory-core/tests/core_planner.rs rust/crates/memory-core/tests/core_boundaries.rs
```

Exit **0**. No shared/privacy/gate/manifest paths edited. No coverage/full aggregate run here.

Correction source identities (SHA-256):

| Path | SHA-256 |
|---|---|
| `rust/crates/memory-core/src/domain.rs` (unchanged) | `96b3e73abdf7da4a59c95939d42ac62decca3b1c5e4144eeb370a0d5a32c6896` |
| `rust/crates/memory-core/src/planner.rs` (unchanged) | `4a5f3d2933882f69e2fb6fc92bfc28b37feb66a75d8b1183caf5f74a32031e95` |
| `rust/crates/memory-core/src/coalescer.rs` (unchanged) | `5f6cd342e6b24de6dd0342401df0e0e21f62e44a901dc728fa69290df91d5228` |
| `rust/crates/memory-core/tests/core_mechanics.rs` (unchanged) | `c8df76c9be6354f7357aa89d05a452f60566dbb8c0e7408249aca4809f617505` |
| `rust/crates/memory-core/tests/core_planner.rs` (unchanged) | `936128b0c87dfc81e88d07877d6d90429e9d53835ecf29f7d11303a76327e6cc` |
| `rust/crates/memory-core/tests/core_boundaries.rs` (new) | `2e120ce2ccef2bf67f46a2ea735ac676b5c8fad1edfe56d8e9b699e1dba22a95` |
| `rust/Cargo.lock` (unchanged shared evidence) | `335e489dca6ce321d4c423c46b0580765f60e4ed76bac5f6a2e07a79801fa24f` |

**Explicit renewed release:** all reopened core module/test/report paths are frozen and released to parent.
The mutation script remains previously released and untouched. No commands remain running with this worker.
Next action belongs to parent: integrate disjoint privacy coverage tests and rerun unchanged canonical
coverage/aggregate; retain the unreachable hash guard and the 98% floor. Slice acceptance remains pending.
