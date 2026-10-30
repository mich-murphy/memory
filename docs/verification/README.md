# Bounded operator verification fixtures

## Acceptance scope: current collector versus replacement

The installed/default collector and its legacy tests retain the **v3 contract**.
The opt-in session-local candidate is also implemented and has its own actual
writer/reader/consumer gate. Requirements remain
[session-capture-contract.md](../session-capture-contract.md); see the
[current handoff](../session-capture-handoff.md) and
[dated offline evidence](../session-capture-offline-evidence.md). Passing offline
checks is not native/live/two-machine release acceptance.

| Verification area | Current evidence and boundary |
| --- | --- |
| `internal/syncer` tests | v3 capture, append/piece integrity, immutable histories, machine-checkpoint recovery, and failure handling. Historical-generation assertions remain required for the running collector. |
| `internal/testenv/archive.go` | Independent v3 physical-record oracle; deliberately audits historical generations/dependencies. Not the replacement's shared authoritative reader. |
| `cmd/agent-sessions/acceptance_test.go` and journeys | Compiled v3 CLI against a private local destination, with independent byte/record checks. Not an actual reader/importer compatibility test. |
| `cmd/agent-sessions/live_test.go` | Opt-in sandboxed agent/hook acceptance for the current collector. Not evidence of a durable spool, autonomous retries, or replacement publication. |
| `internal/store` live tests | Opt-in backend behavior and legacy recovery acceptance. Not a production cutover authorization. |
| `internal/sessioncapture`, `internal/publication` and `make capture-acceptance` | Independent durable candidate, authoritative reader and eleven actual-process writer → importer/event-index/report journeys, including SDK peer, aliases and optional crash/restart/repair/revocation. Offline, not actual R2/native acceptance. |
| Verification skill inventory | Its recorded inventory has not been re-audited while access is disabled. Legacy checks remain applicable to legacy behavior; the implemented replacement gate is separately documented here. No check is waived or weakened. |

**Audit limitation:** the verification skill was disabled by the operator's skill toggle
and could not be read or updated during this cleanup. Its contents were not bypassed.
Review that inventory against the agreed contract if the operator enables it; this
page, not an unaudited skill entry, records the replacement acceptance boundary.

The implemented mandatory offline replacement gate exercises writer-generated full epoch snapshots,
complete current roots, and the shared authoritative reader through the actual
metadata/body importer, event index, and report. Assert exact session/source/epoch
coverage and body hashes, idempotent repeat import, visible incoming updates, and
explicit unsupported-format failures (never a falsely successful empty refresh).
It also exercises durable local acceptance, restart/retry without another hook, and
broken A with independent healthy B. These offline checks and the full unchanged
development gates pass as recorded in the dated evidence. Actual conditional R2,
actual native agents, operating-resource/freshness and both-machine installed-path
acceptance remain separate requirements. See the contract for the full release gate.

Legacy cold recovery and preservation of every collector generation are **not**
future replacement requirements. Keep their working algorithms and tests until
that implementation is retired; do not turn cleanup into an old-stack repair or
relax its present safety checks. The clean rebuild does not authorize deleting
local sources, pending bytes, exclusions, or unrelated artifacts. No fixture or
passing test here authorizes a live reset, rollout, or remote deletion.

## Frozen operator fixture

`linux-native-2026-10-15.patch` reproduces test-only fixture `4664334` from source
`a95976a`. It is evidence/reproduction material, not a shipped or supported Linux
sandbox feature. It contains no actual CLI distributions, user data or credentials.

In a separate worktree at that base, apply its zero-context hunks with:

```sh
git apply --unidiff-zero /path/to/linux-native-2026-10-15.patch
```

Read the resulting `private/linux-native/README.md` before supplying public assets
or executing anything. Never run the real agents outside the required outer OS
boundary. The frozen fixture retains its **pre-execution UNVERIFIED** label;
actual executed results, failure history and scope are recorded separately in
[production-readiness.md](../production-readiness.md). No full production gate was
substituted by this operator fixture.
