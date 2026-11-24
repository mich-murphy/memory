# memory

Capture and analyze Claude Code, Codex, and Pi session evidence across machines.

## Direction and implementation status

**For the Rust rewrite, start with [DESIGN.md](DESIGN.md) (amendments folded in;
[why each change was made](docs/rust-rewrite/design-amendments-20261109.md)), then the
[active delivery handoff](docs/session-capture-handoff.md#current-session-checkpoint).**
DESIGN.md is authoritative: append pieces, delta/checkpoint manifests, no spool or
job tables, hooks/notifications with safety reconciliation, and one shared reader.
The [capture contract](docs/session-capture-contract.md) retains the goals and Go
maintenance contract; its replaced mechanisms are not the Rust implementation plan.

**Current Rust state (2026-11-17):** everything through brief 29 is merged into `main`. Under
[`rust/`](rust/README.md), `memory daemon` runs an event loop (per-owner capture after the
coalescer window, hook hints, a full reconcile at start and every 15 minutes, retries, a stop
honoured inside a round) and writes zstd pieces and manifests through the S3 adapter;
`memory restore <owner> --out DIR` reads an owner back; `memory hook <agent>` writes hint
lines. Tests run the real binary against a local S3 server on macOS and Linux. It is **not
yet a production collector**: the daemon refuses an empty key prefix, no acceptance run on
real session data has happened, deletions, snapshot kinds and the BC-01 host are slice 3,
and `memory status`, `unpark`, `reconcile` and `export` do not exist. The operator approved
the 2-day acceptance run under `rust-acceptance/<run-id>/` on 2026-11-17; it has not started.
Where the work is and what comes next:
[the handoff](docs/session-capture-handoff.md#current-session-checkpoint),
[brief 29](docs/rust-rewrite/implementation/29-event-loop.md) and
[evidence 20](docs/rust-rewrite/implementation/evidence/20-event-loop/). The dated paragraphs
below are the history of how it got here.

**Rust state on 2026-11-10 (history):** under [`rust/`](rust/README.md), slice 1 (pure core:
domain, locator, manifests, hints, planner, coalescer, privacy) meets its exit criteria, with
P5 waived by the operator. Slice 2 is partly built against fakes and temp directories: the
`memory hook` binary, the commit protocol, daemon state, capture wiring (delta-only append
reads), the real filesystem adapter, the source resolver, the notify watcher/scheduler, a
daemon skeleton, property 4 and the counter tests. The latest integrated local gate passed on
`d8e80a6` at 2,000 cases: all 13 subgates, 391/391 tests, production mutation checks killed,
97.03% line coverage against the fixed 80% backstop (amendment A9). This is **not a
functioning Rust collector or release acceptance**: `memory daemon` exits 78 after startup,
and there is no R2 client, zstd, run loop or reader yet. See
[report 23](docs/rust-rewrite/implementation/23-review-fix-round.md) for progress against
DESIGN sections 15 and 17.

**Update 2026-11-12 (run-loop stack, brief 27):** the S3
adapter, zstd piece encoding and the reader exist. `memory daemon` starts from `config.toml`,
runs a safety reconcile every 15 minutes and stops on SIGTERM or SIGINT; `memory restore
<owner> --out DIR` reads an owner back. A test runs both on the real binary against a local
S3 server and compares bytes. It is still not a collector: the daemon refuses to write
outside an acceptance key prefix until `tracing`, hint retention and the audit exist, and the
file watcher is not connected. See
[brief 27](docs/rust-rewrite/implementation/27-run-loop.md) and
[evidence 18](docs/rust-rewrite/implementation/evidence/18-run-loop/).
**Update 2026-11-16 (brief 28):** the daemon now logs to stderr, removes old hint files and audits
append sources, accepted locally; see
[brief 28](docs/rust-rewrite/implementation/28-production-prerequisites.md) and
[evidence 19](docs/rust-rewrite/implementation/evidence/19-production-prerequisites/). The
empty-prefix refusal and the unconnected watcher remain.
**Update 2026-11-16 (brief 29):** the daemon now runs an event loop: per-owner capture after the
coalescer window, hints, a safety reconcile every 15 minutes and a stop honoured inside a
round; merged into `main` on 2026-11-16 as `f15f651`. The empty-prefix refusal remains. See
[brief 29](docs/rust-rewrite/implementation/29-event-loop.md) and
[evidence 20](docs/rust-rewrite/implementation/evidence/20-event-loop/).

Every Rust stack so far (#56, #95, #103, #111 and #124) is merged into `main`. No Rust
deployment or cutover occurred.
On 2026-11-09, by operator instruction, the Go capture daemons (Mac and BC-01) and the Mac
refresh job were stopped and disabled, and the old capture prefixes in the R2 bucket were
deleted. The Go hooks are still installed and write only local spool files. Preserve existing
identities, sources and protections.

### Retired Go implementation

The Go collector (`agent-sessions`, its `capture-v1` candidate and the legacy analytics) was
deleted on 2026-11-25 (brief 57); it is in git history, and its docs carry a historical
banner. The old R2 archive was deleted on 2026-11-09. The Rust writer rebuilds from sources.
That does not authorize deleting original sessions, identities, secret protections or local
research. Install and operate `memory` with [install](docs/install.md). The optional
[analytics tools](tools/analytics/README.md) import `memory export` output into DuckDB.
See the [documentation map](docs/README.md) and the
[design rationale](docs/research/session-capture-contract-review.md).

### Safety boundaries

- Keep buckets, credentials, logs, transcript bodies, and analytics caches private.
  Scrubbing known configuration secrets is not universal transcript DLP.
- Preserve machine IDs and learned custom source roots through rebuilding. Do not
  rotate identity or select an old namespace by hostname to work around recovery.
- Session opt-out (`AGENT_SESSIONS_DISABLE=1`) is retired by operator decision
  (2026-11-07). The frozen Go path still honors existing records until cutover. The Rust
  rewrite does not implement opt-out, and the one existing record is removed at cutover.
  Secret protections (credential denylist, configuration scrubbing, launch argv
  allowlist, withholding unknown kinds) are not opt-outs and stay.
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

Existing dated benchmark and production records remain under `docs/` as historical
evidence, not active release instructions.
