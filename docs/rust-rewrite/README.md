# Rust rewrite evidence

Companion to [DESIGN.md](../../DESIGN.md). Everything here was produced on 2026-11-06 and
2026-11-07 on this Mac. Aggregates only; no transcript content, identities or credentials.

## Reviewer reports

Nine read-only reviews of the Go project by Sonnet subagents, briefed by
[reviewer-brief.md](reviewer-brief.md). Each labels its claims as read, measured, looked up,
inferred or guessed.

| File | Covers |
|---|---|
| [01-requirements.md](reports/01-requirements.md) | Requirements extracted from the contract, docs and code |
| [02-runtime-architecture.md](reports/02-runtime-architecture.md) | The Go candidate's runtime, call stacks, state tables |
| [03-legacy-and-support.md](reports/03-legacy-and-support.md) | Legacy paths, scrubbing rules, hooks, backends |
| [04-tests.md](reports/04-tests.md) | What the Go tests prove and what they cost |
| [05-history-lessons.md](reports/05-history-lessons.md) | What went wrong, ranked lessons, open decisions |
| [06-rust-standards.md](reports/06-rust-standards.md) | BeautifulRust assessment, lint table, crate picks, gates |
| [07-consumer-reader.md](reports/07-consumer-reader.md) | The Python analytics consumer and reader contract |
| [08-event-driven-research.md](reports/08-event-driven-research.md) | Shippers, OS notification APIs, R2 conditionals, dedup, four architectures |
| [09-repo-hooks-vs-scan.md](reports/09-repo-hooks-vs-scan.md) | Hooks as installed, 14 source kinds versus signals, Codex trust episodes |

## Challenge review (2026-11-07)

Six Sonnet reviewers challenged the approved design. The parent synthesised their findings
and cross-checked them against DDIA 2e. DESIGN.md was then revised to final, and its
section 20 maps each finding to a section.

| File | Covers |
|---|---|
| [challenge/README.md](challenge/README.md) | Brief, assignments, ledger |
| [challenge/00-synthesis.md](challenge/00-synthesis.md) | Verdict, blockers, majors, operator decisions |
| [challenge/01-capture-correctness.md](challenge/01-capture-correctness.md) | Prefix proof, identity, epochs, snapshots |
| [challenge/02-storage-protocol.md](challenge/02-storage-protocol.md) | R2 layout, commit protocol, `object_store` |
| [challenge/03-signals-daemon.md](challenge/03-signals-daemon.md) | Hooks, hint log, notify, sleep, single instance |
| [challenge/04-failure-ops.md](challenge/04-failure-ops.md) | Failure state, durability, upgrades, memory |
| [challenge/05-privacy-security.md](challenge/05-privacy-security.md) | Scrubbing, credentials, encryption, threat model |
| [challenge/06-strategy-engineering.md](challenge/06-strategy-engineering.md) | Reader slice, shadow run, Hegel, async, targets |
| [challenge/07-ddia-crosscheck.md](challenge/07-ddia-crosscheck.md) | Blockers against DDIA 2e ch. 4, 6, 8, 9, 13, 14 |

Prototype results go in `prototypes/<id>-<name>.md` (DESIGN.md section 16):
P1, P3, P4 (synthetic), P5 (blocked, then waived), P6 (simulation), P7, P8, and the
2026-11-09 research prototypes P11 (commit retry, real R2 probe and simulation) and P12
(durable state writes). [design-amendments-20261109.md](design-amendments-20261109.md)
records the operator decisions and the direction P11 and P12 settled; it wins over
DESIGN.md where they conflict. Implementation reports and gate evidence are in
`implementation/`.

## Experiments

Run with `uv run --no-project python3 -I <script>`. Each reads only timestamps and byte
offsets from local session files and prints aggregates.

| Script | Produces | DESIGN.md section |
|---|---|---|
| `experiments/coalesce_sim.py` | Captures and whole-object amplification per agent at three windows | 3.1 |
| `experiments/policy_sim.py` | Upload bytes, PUTs and max pieces per policy | 3.1 |
| `experiments/batch_sweep.py` | Window sweep: captures, PUTs, pieces, lag, piece sizes | 3.2 |
| `experiments/piece_compress.py` | Per-piece zstd penalty versus whole-file | 3.3 |
| `experiments/walk.py` | Stat-walk cost of the agent roots and map lookup cost | 3.4 |
| `experiments/manifest_cost.py` | Manifest bytes uploaded: full rewrite against delta plus checkpoint | 3.7 |
| `experiments/hegel-probe/` | Minimal `hegeltest` project: one passing and one failing property | 3.4, 13 |

To rerun the Hegel probe: `cargo new --lib probe`, copy `Cargo.toml`'s dev-dependency and
`tests/coalesce.rs` in, then `cargo test --test coalesce`. The failing test is deliberate and
should shrink to `[[0]]`.
