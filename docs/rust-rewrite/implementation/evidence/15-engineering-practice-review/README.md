# Evidence 15: engineering-practice review and data-structure prototypes (2026-11-12)

Raw material behind [report 25](../../25-engineering-practice-review.md). Copied from the
session scratch directories; paths inside the reports that start with
`/private/tmp/claude-501/...` named that scratch space and no longer exist. Worker reports
are model output: report 25 says which claims the parent checked. No transcript, credential
or R2 data was read to produce any of it.
Trailing spaces and tabs were stripped from eight of the copies; nothing else was edited.

| Path | Content |
|---|---|
| `reports/COMMON.md`, `W1` to `W8` | The shared reviewer rules and the eight read-only Sonnet reviews of `41a07ff`: core mechanics; locator, hint and manifest; privacy; daemon and state; capture and commit; tests and gates; design conformance; delivery slicing |
| `prototypes/PROTO-COMMON.md` | The prototype protocol: measurements M1 to M7 and the decision rule, written before any measurement |
| `prototypes/P1-owner-state.md`, `P2-landed.md`, `P3-authorize.md` | The three worker reports, one per data structure, with every option built or rejected |
| `prototypes/p1-scratch`, `p2-scratch`, `p3-scratch` | The workers' scripts and raw outputs. `landed_bench.rs` and `authorize_bench.rs` are the benchmark sources, kept here and not in the tree. `p3-scratch/bench.txt` matches a `.gitignore` rule and was added by force |
| `parent-rerun/` | The parent's reruns on the workers' release binaries, interleaved with no build running: `bench.sh`, `p1-1k.txt`, `p1-30k.txt`, `p2.raw`, `p3.txt`, the misuse recompiles (`p1-m5.py`, `p2-m5.py`), the grouping measurement (`grouping.py`, `g.txt`, `benchpatch.py`) |
| `parent-rerun/integrated-idle-rounds.txt`, `integrated-idle-rounds-30k-bisect.txt` | Idle rounds on the integrated stack against the baseline with the grouping fix, 9 runs at 1,000 and 30,000 owners, then 15 runs at 30,000 with `rust/commit-end` added |
| `gates/` | `gates.sh` records: `run-08c37f0.log` (first run, seed 673264974) and `status`, `seeds`, `summary` for the final code tip `0f3cb7d` (seed 374449330) |

The benchmark binaries are not kept. To repeat a measurement, check out the branch named in
the worker report (`proto/ds-*`, local to the machine that ran this) or apply
`parent-rerun/benchpatch.py` to a stack branch, and build `--release` in the `.#rust` shell.
Timings are from one shared Mac; compare each option only with the baseline rows run beside it.
