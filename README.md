# memory

Capture and analyze Claude Code, Codex, and Pi session evidence across machines.

## Direction and implementation status

**Start with the [session capture contract](docs/session-capture-contract.md).** It is
the agreed replacement direction: independent sessions, durable local work, scoped
retries, complete readable publications, and analytics acceptance end to end.
Use the **[delivery handoff](docs/session-capture-handoff.md)** for the accepted source,
last-recorded installed state, operating authorization boundary and next actions.

**October 30 status: source integration is complete; live operating acceptance is not.**
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

The operator accepts retiring the existing R2 archive and rebuilding from sources on
this Mac and BC-01. That does not authorize incidental deletion of original sessions,
identities, privacy exclusions, or local research. Follow the contract's inventory,
validation, two-machine cutover, and scoped deletion sequence.

### Replacement flow and acceptance goals

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
and sweeps do **not** constitute the replacement's autonomous durable queue.

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
- `AGENT_SESSIONS_DISABLE=1` records a persistent session opt-out where a session path
  is available. Preserve effective local and remote exclusions through reset; do not
  silently re-enable capture or confuse opt-out with deletion of archived bytes.
- Incomplete discovery must not imply source deletion. FIFOs/devices and unsafe or
  unreadable configuration must not be treated as successfully captured.
- Never delete a live lock file to bypass the single-writer protections.

See [security](docs/security.md) and the replacement contract for the full boundaries.

## Development

Read [AGENTS.md](AGENTS.md) before editing. Use the pinned Nix development environment:

```sh
nix develop
# Or use direnv with the existing .envrc after reviewing it.
```

The shell supplies the pinned Go toolchain, golangci-lint, C compiler for race tests,
Python, and supporting verification tools. Tool dependencies are pinned in `tools/`;
agent CLIs and live credentials are not provisioned.

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
The [verification record](docs/verification.md) is dated legacy/native evidence;
the linked verification skill is operator-blocked for this audit and was not reviewed
or executed. Passing offline gates does not prove native-agent/live-R2 or installed
replacement acceptance. Do not relax gates to ship the redesign.

Existing dated benchmark and production records remain under `docs/` as historical
evidence. They are not active release instructions or promises about the replacement.
