# memory

Capture and analyze Claude Code, Codex, and Pi session evidence across machines.

## Direction and status

Production is the Rust `memory` binary under [`rust/`](rust/README.md): `memory daemon`
captures Claude Code, Codex and Pi sessions as append pieces and delta manifests in an
S3-compatible bucket, `memory hook <agent>` writes hint lines, and `memory restore`,
`memory export`, `memory check`, `memory status`, `memory unpark` and `memory reconcile` read
and operate the archive. [DESIGN.md](DESIGN.md) is authoritative; the
[capture contract](docs/session-capture-contract.md) retains the goals, and the
[handoff](docs/session-capture-handoff.md#current-session-checkpoint) says where the work is.
Dated briefs and evidence are under [docs/rust-rewrite/](docs/rust-rewrite/).

Install and operate `memory` with [install](docs/install.md). The optional
[analytics tools](tools/analytics/README.md) import `memory export` output into DuckDB. See
the [documentation map](docs/README.md) and the
[design rationale](docs/research/session-capture-contract-review.md).

### Safety boundaries

- Keep buckets, credentials, logs, transcript bodies, and analytics caches private.
  Scrubbing known configuration secrets is not universal transcript DLP.
- Preserve machine IDs and learned custom source roots through rebuilding. Do not
  rotate identity or select an old namespace by hostname to work around recovery.
- Session opt-out is retired by operator decision (2026-11-07). Secret protections
  (credential denylist, configuration scrubbing, launch argv allowlist, withholding unknown
  kinds) are not opt-outs and stay.
- Incomplete discovery must not imply source deletion. FIFOs/devices and unsafe or
  unreadable configuration must not be treated as successfully captured.
- Never delete a live lock file to bypass the single-writer protections.

See [security](docs/security.md) and the replacement contract for the full boundaries.

## Development

Read [AGENTS.md](AGENTS.md) before editing. Use the pinned Nix development environment:

```sh
nix develop --max-jobs 2 --cores 2   # Rust toolchain, nextest, deny, llvm-cov, mutants
```

The shell supplies the pinned Rust toolchain and matching LLVM tools, Python and the gate
tools. Rust dependencies are in `rust/Cargo.lock`. Agent CLIs and live credentials are not
provisioned. See the [Rust workspace guide](rust/README.md) for the gate command, and
[quality gates](docs/quality-gates.md). CI runs the Rust gates, the analytics unit suite and
workflow lint (zizmor, actionlint). Do not relax gates.
