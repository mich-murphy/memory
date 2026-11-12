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

**Current Rust state (2026-11-10):** under [`rust/`](rust/README.md), slice 1 (pure core:
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

The work is committed as draft gh stack #56 (PRs #54, #55, #57, #58, #59, #60, top branch
`rust/coverage-floor-80`), green on hosted CI. No Rust deployment or cutover occurred.
On 2026-11-09, by operator instruction, the Go capture daemons (Mac and BC-01) and the Mac
refresh job were stopped and disabled, and the old capture prefixes in the R2 bucket were
deleted. The Go hooks are still installed and write only local spool files. Preserve existing
identities, sources and protections.

### Go candidate reference — not the Rust roadmap

**Historical October 30 status: source integration is complete; live operating acceptance is not.**
[PR30](https://github.com/mich-murphy/memory/pull/30) is merged, with local development
qualification and all seven PR and merged-main CI jobs passing. This does **not** prove
memory is now working live end to end. Earlier experimental installations and selected
live journeys are historical evidence; current installed health was not freshly checked.
The next stage is separately authorized **Mac → BC-01 → combined operating acceptance**,
not more PR cleanup or activation inferred from a green build.

An **opt-in replacement candidate** is implemented under the experimental
`agent-sessions capture-v1` command prefix. Its publication, reader and report
contracts are **v2**; the prefix does not imply v1 wire compatibility. It includes
durable capture, autonomous work, quarantine, explicit Local/R2 backend construction,
independent enrichment and an authoritative reader shared with the opt-in analytics
consumer. Unselected collector and analytics defaults remain legacy.

For current checkout qualification, deployment, service health, blockers and next action,
use the handoff's [current-session checkpoint](docs/session-capture-handoff.md#current-session-checkpoint).
The [implementation reference](docs/session-capture-implementation.md),
[phase-3 candidate](docs/session-capture-phase3.md),
[backend boundary](docs/session-capture-backends.md) and
[dated offline evidence](docs/session-capture-offline-evidence.md) describe interfaces and
historical results, not current release acceptance. No experimental installation alone
establishes final cutover.

The old R2 archive was retired on 2026-11-09 (inventory, then deletion of the
`capture-v1/` and `v3/` prefixes; see the handoff brief and amendment A2). The Rust writer
rebuilds from sources on this Mac and BC-01. That does not authorize deleting original
sessions, identities, secret protections, or local research.

### Go candidate flow and retained acceptance goals

```text
agent hooks + periodic discovery
    -> durable per-source capture/work queue
    -> bounded fair publication workers
    -> complete session publications in R2
    -> one authoritative reader
    -> analytics metadata/bodies -> events -> reports
```

- **Session isolation:** a failed session does not block unrelated capture or reads.
- **Honest progress:** observed, locally captured, remotely published, enriched, and
  imported/served are separate states. Previous success survives failed updates.
- **Durable recovery:** pending work retries without another hook, with backoff,
  explicit quarantine, bounded resources, and visible coverage/freshness.
- **Conversation preservation:** retain captured source epochs across unproven rewrites;
  do not require every collector prefix, fold, or compression layout to survive forever.
- **Simple storage first:** full compressed epoch objects and complete session manifests.
  Delta chunking is an optimization to measure, not the logical data contract.
- **One reader:** restore and analytics use the same resolved publication, not independent
  interpretations of storage JSON.
- **Consumer acceptance:** actual writer output must reach the real importer and report.
  Upload success or separately handwritten fixtures are insufficient.

See the [documentation map](docs/README.md) for current implementation references,
historical evidence, and the [design rationale](docs/research/session-capture-contract-review.md).

## Current tooling: legacy, not replacement acceptance

`agent-sessions` is the existing Go collector. It installs Claude/Codex hooks and a Pi
extension, captures session files and associated launch/harness data, applies credential
exclusions and configuration scrubbing, and writes to R2. Its implemented detached syncs
and sweeps are **not** the Rust implementation or evidence of its acceptance.

| Existing command | Current purpose |
| --- | --- |
| `agent-sessions machine-id` | Print the effective stable machine identity. |
| `agent-sessions machine-id --new` | Explicit new-machine setup, not identity recovery. |
| `agent-sessions install` | Install current hooks/extension and configuration template. |
| `agent-sessions doctor` | Inspect current setup; includes remote store probes. |
| `agent-sessions backfill` | Capture available sources using the legacy archive engine. |
| `agent-sessions sync` | Run the legacy sync path. |
| `agent-sessions uninstall` | Remove managed hooks/extension; retain data and configuration. |

These are references to implemented commands, not instructions to install the old
collector for the replacement rollout. Follow [install](docs/install.md),
[hooks](docs/hooks.md), and [machine identity](docs/machine-identity.md) when maintaining
an existing deployment. They do not prove deployment health or new-contract acceptance.

The optional [analytics tools](tools/analytics/README.md) preserve existing private
local research access and provide a separate v2 publication consumer. The legacy R2
importer targets retired manifest payloads, not current legacy session references or
the replacement contract; it is not a current archive refresh path. Use the explicit
publication-backed mode only with its separate reader-generated projection. Neither
historical reports nor offline candidate results establish live archive coverage.

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
nix develop --max-jobs 2 --cores 2
# Or use direnv with the existing .envrc after reviewing it.
```

The shell supplies pinned Go and Rust toolchains, matching Rust LLVM tools,
golangci-lint, C compiler for race tests, Python, and supporting verification tools.
Go tool dependencies are pinned in `tools/`; Rust dependencies are in `rust/Cargo.lock`.
Agent CLIs and live credentials are not provisioned. See the
[Rust workspace guide](rust/README.md) for its canonical gate command; the `make`
commands below remain Go checks, not Rust replacement acceptance.

```sh
make build        # builds the collector currently in this checkout
make test         # current race-enabled Go suite
make check        # existing formatting, lint, static, coverage/CRAP and other gates
make hooks        # install repository Git hooks
make gopls        # install the pinned language server
```

[Quality gates](docs/quality-gates.md) describes the current development checks,
including mandatory actual-writer/shared-reader/importer/report journeys in `make test`
and `make check`, plus the separate CI analytics job. Legacy safety suites remain.
The [verification record](docs/verification.md) and linked verification skill describe
separate legacy/native work, not authorization to operate hosts during Rust development.
No live verification was run as part of this documentation handoff. Passing offline
gates does not prove native-agent/live-R2 or installed replacement acceptance. Do not relax gates to ship the redesign.

Existing dated benchmark and production records remain under `docs/` as historical
evidence. They are not active release instructions or promises about the replacement.
