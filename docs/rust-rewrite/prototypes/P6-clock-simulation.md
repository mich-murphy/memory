# P6. Clock cases

**Measured simulation only. Natural-sleep observation remains pending.** No
host suspension, service or clock adjustment was attempted.

The driver supplies independent wall, monotonic and sleep-inclusive continuous
elapsed values. A monotonic deadline is 300 s after its origin. At the next tick,
60 s monotonic elapsed leaves 240 s on both coalescer/backoff deadlines.

| Synthetic case | Wall elapsed | Mono elapsed | Continuous elapsed | Reconcile flag |
|---|---:|---:|---:|---|
| awake | 60 | 60 | 60 | no |
| 600 s sleep | 660 | 60 | 660 | yes |
| wall forward step | 660 | 60 | 60 | yes |
| wall backward step | -540 | 60 | 60 | no |
| exact 120 s gap | 180 | 60 | 180 | no |
| 121 s gap | 181 | 60 | 181 | yes |

Assertions enforce strict >120, not >=120. Sleep-inclusive deadline expires on
sleep but not on a wall-clock step. Initial review caught and corrected an early
model incorrectly deriving continuous time from wall time. Final raw results
record independent values. This is why final source hashes differ from run one.

**Inferred.** Wall forward steps are indistinguishable from sleep to the stated
wall-minus-monotonic detector. They trigger a conservative extra reconcile, not
incorrect clock ordering. A backward adjustment simultaneous with sleep can hide
a wake gap. These are system-model limitations, not new design defaults.

The simulation records the intended [0,30] s jitter bounds, but does not measure
actual jitter scheduling. It does not call Rust Instant, mach_continuous_time or
CLOCK_BOOTTIME. Natural sleep must independently record those clocks before/after
and observe reconcile plus staggered queue behavior with real implementation.

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

