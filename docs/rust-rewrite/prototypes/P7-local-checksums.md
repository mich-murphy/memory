# P7. Local checksums

**Measured.** At exactly 20,000,000 body bytes, SHA-256 costs p50
7.976 ms; table-based scalar CRC32C costs
49.268 ms. Both rejected 8/8
injected invalid envelopes and accepted the intact envelope.

| Algorithm | Digest p50 / max ms | Warm file read + validation p50 / max ms |
|---|---|---|
| SHA-256 | 7.976 / 8.314 | 11.392 / 12.610 |
| Scalar CRC32C | 49.268 / 49.604 | 50.602 / 51.738 |

15 samples each. CRC32C is Castagnoli, not zlib CRC32, verified against the
123456789 vector = e3069283. It uses a native C byte table through ctypes and
includes table setup. SHA-256 uses the active Nix Python hashlib backend.
This is not an optimized hardware CRC32C-vs-SHA shootout. No broad algorithm
performance ranking follows; no dependency was installed for hardware CRC32C.

The synthetic envelope includes schema, writer_version, body length and digest.
Injected cases truncate at 0, 1, 30, midpoint and final-byte-minus-one; flip a
body bit; append trailing bytes; and change writer version. Validation checks
length/version/schema before digest. This demonstrates short/corrupt-body
rejection, not power-loss atomicity. Same-length malformed header JSON is not a
cryptographic authenticity guarantee. CRC32C is not collision-resistant.

**Remaining.** No F_FULLFSYNC/fsync/rename/directory-fsync power-failure test,
actual index serialization, persisted hash-state compatibility or root/status
recovery acceptance. DESIGN.md SHA-256 default remains unchanged.

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

