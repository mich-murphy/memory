# P4. Synthetic hook and buffer alternatives

**NOT actual memory hook or daemon footprint acceptance.** This benchmark does
not justify a change to the 8 MiB / 8 uploads defaults. Production Rust gates
remain required. Synthetic success-hook p99 varied across runs and exceeded the
1 ms target in the final run.

C standalone hand-parser results, 300 sequential process launches per path:

| Path | In-process p50 ms | In-process p99 ms | Spawn p99 ms |
|---|---:|---:|---:|
| append succeeds | 0.03 | 1.548 | 5.874667 |
| missing parent | 0.004 | 0.008 | 4.423292 |

Timer starts inside main and ends after parse/open/single append/close. Diagnostic
stdout is outside this region. Real hook never prints stdout. This fixed JSON
probe omits stdin parsing, allowlists, policy, Rust startup and actual hint schema.
There is no Tokio/clap. It tests only success and missing-parent paths, not ENOSPC
or all failures. The initial run's success p99 was 0.421 ms; final was 1.548 ms.
Do not infer stable tail latency from two small samples under shared host load.

Python SHA-256 / zstd-3 scratch process. Each alternative runs in a fresh process,
two compression workers maximum; four/eight retained encoded buffers model pending
uploads, not real I/O or additional threads. Two raw buffers are explicitly held.

| Piece MiB | Encoded buffers | Input | Retained payload MB | Peak RSS MB |
|---|---:|---|---:|---:|
| 4 | 4 | random | 25.17 | 64.93 |
| 4 | 4 | compressible | 8.39 | 44.40 |
| 4 | 8 | random | 41.94 | 82.23 |
| 4 | 8 | compressible | 8.39 | 44.38 |
| 8 | 4 | random | 50.33 | 100.34 |
| 8 | 4 | compressible | 16.78 | 57.05 |
| 8 | 8 | random | 83.89 | 135.12 |
| 8 | 8 | compressible | 16.78 | 57.03 |

MB above means decimal bytes / 1e6. RSS includes Python interpreter, transient
copies, allocator and compression state. Fresh processes report getrusage peak;
there is no subtraction of interpreter baseline. Random input is synthetic and
compressible input is repeated x. These results illustrate memory sensitivity,
not a prediction of Rust client behavior. At 8 MiB / 8 random buffers, payload
alone is 83.89 MB and process peak is 135.12 MB.

**Target, not measured acceptance.** Real daemon peak <128 MB, idle <40 MB at
30k sources; real hook main-to-exit p99 <1 ms. None are passed here. Request-client
copies, eight actual network PUTs, 30k index entries, fairness and native idle
wakeups still need real implementation measurement. No runtime/client selected.

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

