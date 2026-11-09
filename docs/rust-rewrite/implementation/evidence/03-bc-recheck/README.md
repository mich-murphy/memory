# BC-01 official-toolchain synthetic recheck — passed

Measured 2026-11-07 UTC. Bounded verification only; parent owns acceptance.
This supersedes the final-toolchain SSH blocker in `00-scaffold.md`, not the
historical research evidence in `../00-scaffold/README.md`.

## Scope and result

Fresh remote scratch: `/tmp/memory-slice0-official-bc.RcafTr`.
Host: **Linux x86_64**, not ARM. No old scratch flake or previous compiler reused.
Byte-exact current repository `flake.nix`, `flake.lock`, and required
`.golangci-lint-version` were transferred, together with unchanged frozen
`experiments/hegel-probe/Cargo.toml` and `tests/coalesce.rs`.

The frozen research manifest says `version = "0.52"`; it was not edited. Scratch
`Cargo.lock` was generated and `cargo update -p hegeltest --precise 0.52.0`
completed successfully before the locked test. Thus this check pins Hegel to
**0.52.0**, static-engine (`hegeltest-c 0.50.0`), while recording its newly resolved
transitive dependencies; it does not claim the original run's full dependency graph.
`cargo tree --locked -e features` records the actual enabled static engine.

**Native gate exit 0:** `pieces_concatenate` passed, 1 passed / 0 failed /
0 ignored / 1 filtered out. The filtered property is the frozen, deliberately
wrong research `coalescer_bug_is_found`; only the requested positive property was
selected, exactly as in the prior synthetic gate. Compile 19.53 s, test 0.39 s.
Hegel configuration: 2,000 cases, seed 730052. No assertion, timeout, or gate changed.

Official compiler probe (`rustc -Vv`, exit 0):

```text
rustc 1.99.0 (b940084d7 2026-09-28)
commit-hash: b940084d7eb6a299eb4bfeb8e34901bc051e7ac4
host: x86_64-unknown-linux-gnu
LLVM version: 23.1.1
```

Cargo: `1.99.0 (5f94df478 2026-08-27)`. Bundled `llvm-cov` and `llvm-profdata`
report `23.1.1-rust-1.99.0-stable`, both exit 0. Their exact Nix store paths are
in `native-nix-hegel.log`. Both derive from `rust-minimal-1.99.0`, not the
old nixpkgs compiler. Nix input pins are recorded in the transferred current lock:
nixpkgs `d3b13ad75cc492690728f9307e443dec9ef9a20e`, rust-overlay
`d3fe74269f2a6652437e21e78eb7e83be81f6982`.

## Exact commands and statuses

The initial access probe, now authenticated successfully, still exits **127**
only because cargo is absent on the default remote PATH:

```sh
ssh -o BatchMode=yes -o ConnectTimeout=10 bc-01 'uname -sm; command -v nix; command -v cargo'
```

Scratch creation used `mktemp -d /tmp/memory-slice0-official-bc.XXXXXX` under
explicit `sh` (remote login shell is not assumed POSIX). Transfer uses `scp`.
`run.sh` preserves the full local orchestration, commands, and statuses.
The decisive command is:

```sh
ssh -o BatchMode=yes -o ConnectTimeout=10 bc-01 "sh -c 'cd /tmp/memory-slice0-official-bc.RcafTr && HEGEL_TEST_CASES=2000 HEGEL_SEED=730052 CARGO_BUILD_JOBS=2 RUST_TEST_THREADS=2 nix develop --no-update-lock-file --max-jobs 2 --cores 2 --command sh ./verify.sh'"
```

Inside this exact current flake's default shell, `input/verify.sh` probes tools,
generates/pins the scratch lock, prints features, and invokes:

```sh
cargo test --locked --test coalesce pieces_concatenate -- --test-threads=2
```

`status.tsv`: access probe 127; scratch-create, transfer, remote-input-hashes,
native-nix-hegel, and collect all **0**. `remote-output/remote-status.tsv`:
uname, rustc, cargo, llvm-cov, llvm-profdata, lock-generate, lock-pin-hegel,
feature-tree, hegel, and post-hashes all **0**. Runner exit **0**.
Nix maximum jobs/cores 2, Cargo jobs 2, test threads 2 throughout. The default
shell was realized successfully; no separate coverage or local full suite run.

## Source binding and retained evidence

- `source-before.json`: original paths, byte counts, SHA-256 and dirty HEAD context;
  this was not a committed-source run.
- `input/`: exact transferred files plus verification script; `input.sha256`
  contains the five initial input hashes.
- `remote-before.log`: native host/Nix and pre-build remote input hashes.
- `native-nix-hegel.log`: full Nix/bootstrap/compiler/features/build/test output,
  commands and individual statuses (no credentials or transcripts).
- `remote-output/`: returned exact inputs, scratch `Cargo.lock` and remote status.
- `remote-output.tar`: transport copy of those same synthetic files, not production
  source/archive data. No binaries or build output copied into evidence.
- `source-after.json`: programmatically verified all five original inputs still
  match both local current files and returned remote files. Verification script
  also matches its returned copy.
- `SHA256SUMS`: all evidence files except this checksum manifest itself.

This closes only the formerly blocked **final pinned toolchain native BC Hegel
synthetic check**. No integrated-core native suite, local full suite, Go suite,
ARM, hosted CI, production capture, R2, live ingestion, hooks/services/installation,
shadow run, or end-to-end writer/reader acceptance is claimed. Remote writes were
limited to unique synthetic `/tmp` scratch and explicitly authorized sandboxed Nix
development builds; scratch remains for reproducibility. No old evidence, runtime
configuration, source data, credentials, private files or transcripts were accessed.

Ownership released on completion: `implementation/00-scaffold.md` and this
`implementation/evidence/03-bc-recheck/` tree; no retained path reservation.
