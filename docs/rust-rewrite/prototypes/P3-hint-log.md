# P3. Hint log late writers

**Measured.** Per-day retained files lost 0/100 late writes. Rename followed by
consumer EOF and unlink lost 100/100, although every append write succeeded.

The deterministic schedule opens an O_APPEND descriptor to day0, writes early,
then pauses that writer logically while the consumer rotates/unlinks the inode.
It resumes the same descriptor and writes late. Reachability is checked through
all remaining directory entries after closing the descriptor, not its return
code. The per-day alternative creates day1 and leaves day0 reachable, so the
late append can be tailed from the old file's saved offset.

This uses a held descriptor and explicitly sequenced operations in one process.
It reproduces the kernel inode lifetime of a descheduled writer without relying
on probabilistic thread scheduling. It does not test concurrent append atomicity,
7-day retention enforcement, cursor persistence or launch commit acknowledgments.
Rename alone does not lose a line if the renamed file remains retained and read.
The defective alternative is rename plus premature unlink after observed EOF.
Per-day cleanup also requires DESIGN.md 9.1 retention and launch commitment rules.
No claim that an arbitrarily paused writer survives deletion after retention.

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

