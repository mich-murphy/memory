# Evidence 14: design, test and complexity review (2026-11-10 to 2026-11-11)

Raw material behind [report 24](../../24-design-and-test-review.md) and amendments A10 to A13.
Copied from the session scratch directory at close-out; paths inside the reports that start
with `/private/tmp/claude-501/...` or `.claude/worktrees/` named that scratch space and no
longer exist. Worker reports are model output: report 24 says which claims the parent checked.

| Path | Content |
|---|---|
| `reports/01` to `08` | Phase 1 read-only reviews: design (storage and capture; privacy, state and failure; process and evidence), tests per crate, complexity and architecture, Hegel property testing |
| `reports/W1` to `W5` | Phase 2: test cut result, property flake rates, first `cargo mutants` baseline, OS experiments (fsync, symlink swap, FSEvents), evidence reconciliation |
| `reports/R5-B-stack-review.md` | Independent read-only review of rounds 3 and 4; found the coverage guards that could not fail |
| `reports/R5-E-r2-probes.md` | Live R2 probes (A13.4): ETag is the MD5 of the body; writes from a killed process land |
| `reports/BRIEF.md`, `ROUND3.md` | The shared worker briefs |
| `r2-probes/` | Raw probe results (`e1.jsonl`, `e2.jsonl`) and the three cleanup records. No URLs, keys or signatures; object bodies were synthetic |
| `seed-campaign/` | `results.tsv`: 40 random-seed runs at library-default case counts of each of the four daemon and commit state machines on `8532325`, 0 failures in 160; `run.sh` is the script |
| `mutants/` | `cargo mutants` outcome lists (see below) |

## Mutation lists

Tree `da01300` (before round 5 landed), example tests only (`-E 'not (test(/hegel_/) |
test(/proptest_/))'`), so every score is a floor. The machine was shared with gate runs, and
the minimum test timeout was 60 s, so some timeouts are load, not hangs. Not triaged.

| Directory | Scope | Caught | Missed | Timeout | Unviable |
|---|---|---|---|---|---|
| `mut1b` | crate `memory` (interrupted: `state.rs` and `watch.rs` incomplete) | 374 | 43 | 19 | 92 |
| `mut2` | `memory-core` coalescer, coordinates, disposition, domain (interrupted) | 142 | 5 | 0 | 16 |
| `mut3a` | `memory-core/src/privacy/*.rs` | 268 | 22 | 5 | 26 |
| `mut3b` | `memory-core` hint, planner, limits, `manifest/*.rs` | 271 | 26 | 23 | 90 |
| `mut3c` | `memory-core/src/locator.rs` | 147 | 1 | 0 | 23 |

`missed.txt` in each directory is the input for the triage that report 24 lists as not done.
