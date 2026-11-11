# P1. Change detection

**Measured synthetic evidence, not real-agent prevalence or Rust acceptance.**
The 4 KiB head / 64 KiB tail strategy satisfies its scoped safety oracle and
its disabled-window mutation is killed. Interior rewrites still escape capture.
Full rehash catches all generated rewrites, at greater bytes read.

Seed 1601 generates 420 cases, 360 actual rewrites, at old-prefix sizes 2 KiB,
32 KiB, 128 KiB, 1 MiB and 4 MiB. Each has 12 repetitions of append, head/tail/
interior edit, random same-size edit, truncate-regrow and identity replacement.
All but same-size edits grow by 257 B. Generator-owned complete bytes are the
oracle. Reassembly must be a prefix of those bytes. The oracle never calls the
window predicate. Identity replacements always replay. The audit uses a fresh
full digest and replaces mismatching bytes with a new epoch in this model.

| Head+tail KiB | Rewrites detected | Wrong reassemblies | Wrong within DESIGN 4+64 safety scope | Check bytes at 4 MiB | Warm p50 ms |
|---|---:|---:|---:|---:|---:|
| 16+16 | 293/360 | 67 | 16 | 32,768 | 0.0309 |
| 256+256 | 316/360 | 44 | 0 | 524,288 | 0.2249 |
| 4+16 | 285/360 | 75 | 22 | 20,480 | 0.0280 |
| 4+256 | 314/360 | 46 | 0 | 266,240 | 0.1235 |
| 4+64 | 307/360 | 53 | 0 | 69,632 | 0.0476 |
| disabled | 60/360 | 300 | 247 | 0 | 0.0181 |
| full | 360/360 | 0 | 0 | 4,194,304 | 1.7463 |

All model post-audit latest epochs equal generator truth (0 audit failures).
The audit branch literally substitutes generator current bytes on mismatch. This
is a model-oracle illustration, NOT proof of actual implementation audit
correctness, epoch persistence, input pinning or liveness.
The disabled-window control has 247 scoped wrong reassemblies; the suite asserts
this is positive, rather than falsely demanding the whole suite exit nonzero.
The smaller alternatives need not meet the default window's safety scope.
Both fixed-4-KiB-head tail sweeps and symmetric 16/256 KiB interpretations are
included to remove ambiguity about alternative window sizes.

**Limitations.** Independent Python model, not production planner property tests.
Warm timing is 15 ordered samples on a 4 MiB scratch file, includes open/seek/read/
hash, not cold I/O. p99 in raw results is the maximum of these small samples.
Window overlap on small files is intentionally read twice. Timings exclude append
bytes and network writes. Rates reflect this arbitrary generator, never agents.
Pure truncation below captured position and multistep crash histories belong in
Rust properties, not this limited comparison. No design number changes.

## Reproduction and provenance

Repository source HEAD `57d8400f632ab47a81e34d060f86d3687123153e`.
DESIGN.md defaults are unchanged. See [00-scope.md](00-scope.md) for ownership
and [results.json](results.json) for full measured output and script SHA-256.
Final suite command, exit **0**:

```sh
env OMP_NUM_THREADS=2 OPENBLAS_NUM_THREADS=2 python3 -I docs/rust-rewrite/prototypes/run.py > docs/rust-rewrite/prototypes/results.json
```

This uses the already-active original pinned Nix closure, not the concurrently
edited flake. Exact executables:

- Python `/nix/store/0jb1qn7qnpbz8774mq5k2c1p84gp0x76-python3-3.14.8/bin/python3`.
- cc `/nix/store/8z7gv889g2cxbvmwcibdzvmikllc51qv-clang-wrapper-21.1.8/bin/cc`.
- macOS 27.0.1 arm64; Python 3.14.8; clang 21.1.8.

The driver compiles `native_probe.c` sequentially using `cc -O2 -Wall -Wextra
-Werror ... -o native` and `cc -O2 -shared -fPIC ... -o native.dylib` in its
private auto-removed scratch directory. No more than two worker threads, no
network or private source access. Initial old-flake `nix develop --offline
--max-jobs 2 --cores 2 -c env OMP_NUM_THREADS=2 OPENBLAS_NUM_THREADS=2 python3 -I
docs/rust-rewrite/prototypes/run.py` also exited 0. A later identical wrapper
hit the 240 s tool timeout while building dependencies after concurrent flake
edits; it did not finish the probes. Final results are from the command above.

Script SHA-256:
- `native_probe.c` `b2aa8ff965806af9d3e1d7758dcad3eea123fbf64288254f1bd8ef09a06c4e5b`.
- `run.py` `fb11a8cb34976568737c8215195648bd457d549bcc6c768169df687ec87e593c`.

