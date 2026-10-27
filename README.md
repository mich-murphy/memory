# memory

Capture and analyze Claude Code, Codex, and Pi session evidence across machines.

## Direction and implementation status

**Start with the [session capture contract](docs/session-capture-contract.md).** It is
the agreed replacement direction: independent sessions, durable local work, scoped
retries, complete readable publications, and analytics acceptance end to end.
For the next implementation session, use the **[implementation handoff](docs/session-capture-handoff.md)**:
it records the current checkout, phased build plan, acceptance gates, and pickup commands.

An **opt-in replacement candidate** is implemented under the explicit
experimental `agent-sessions capture-v1` command prefix. The last recorded Mac capture
installation is qualified-dirty binary `e69fb2ed…`, with staging8766; BC-01 remains on
`b23712bc762de77e14a4f096d27bf7b603abac1e`. Current service health is unknown. These
historical experimental installations are not final default cutover or release
acceptance. Newer checkout/private-candidate changes are undeployed. Its current publication,
reader and report contracts are **v2**; the command prefix and linked filenames do
not imply v1 wire compatibility. It includes independent durable capture, autonomous
retry/discovery, quarantine and progress, explicit Local/SDK R2 backend construction,
native-context capture, independent enrichment, declared-root aliases, and source/WAL
no-follow protections. The shared authoritative reader feeds the real analytics
importer, event index and report. Existing dashboard/query entry points are wired in
explicit `--source publications` mode; defaults remain legacy.

See the [implementation checkpoint](docs/session-capture-implementation.md),
[phase-3 candidate](docs/session-capture-phase3.md),
[backend boundary](docs/session-capture-backends.md), and
[offline evidence](docs/session-capture-offline-evidence.md).
The historical 2026-10-24 evidence records passing full development gates: `make check` **622.40s**,
`make test` **460.36s**, all **nine × 30s** fuzz targets (**285.31s** total), all
**73 analytics tests / 23.744s**, and all **11 actual-process journeys**.
These dated offline results are **not current-checkout release acceptance**. The later
b237 checkpoint passed415-input gates; newer426/427-input gates and the recorded
647-input `make check` attempt failed. ROOT analytics383 passed after reviewed outer/
inner caller cleanup; existing JS checks remain applicable. Subsequent reviewed Go
fixture changes reject invalid coverage configuration before side effects and add a
real final-exit error regression; their validation is tracked in the handoff, not
implied by the earlier383 result. The unsafe-directory fixture is also fixed/reviewed.
These corrections do **not** resolve the private queue race timeout or establish the
cause of historical CLI kills. See the handoff's current next action and source-bound
results before continuing; do not infer that all Go/release gates are green.

The [current scorecard](docs/session-capture-handoff.md#current-session-checkpoint)
separates repository code, private staged fixes and source-bound checks. The latest
V29 library Queue → Local → reader → importer → event index/report component passed
14 tests with one live-R2 skip; it does not establish installed CLI or autonomous
reliability. Two later Run deadline failures, three earlier covered attachment/timeline
failures and the original journey09 remain unresolved. The private V32V2 metadata/
predispatch corrections are uncompiled, unexecuted and unadopted; no complete current
full-development or release pass is established. Capture also retains a shared spool
metadata scan before new snapshots, a known independence/scaling limitation described
in the [phase-3 reference](docs/session-capture-phase3.md#implemented-capture-and-scheduling).

Unselected CLI/dashboard defaults remain legacy. Managed experimental capture is already
installed on both hosts; legacy PATH writers were fenced and managed hooks replaced in
the recorded rollout. The staging consumer remains separate from legacy8765. Installed
native negatives/restart, full reader-to-analytics acceptance, representative coverage/
freshness/resources and final cutover remain outstanding—not a request to reinstall or
repeat completed fencing. Legacy machine-wide recovery and historical publication chains
are not replacement requirements; preserve working safety coverage until replaced.

The operator accepts retiring the existing R2 archive and rebuilding from sources on
this Mac and BC-01. That does not authorize incidental deletion of original sessions,
identities, privacy exclusions, or local research. Follow the contract's inventory,
validation, two-machine cutover, and scoped deletion sequence.

### What we are building

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
