# Replacement capture: dated qualification evidence

Current deployment, authority and next action live in the
[sole delivery handoff](session-capture-handoff.md#current-session-checkpoint).
The records below are source-bound evidence, not a declaration that memory is working
live on the Mac and BC-01. Earlier failed or staged aggregates retain their exact scope.

## 2026-10-30: merged-source integration qualification

**Accepted source:** `ef0f3d31aa76d9ba7c1370431006e1a3c2bb1f62`.
[PR30](https://github.com/mich-murphy/memory/pull/30) merged as
`b0f949c6a8de76b4a8cd3f0bdf48ae29d44b1026`; parent independently verified identical
source trees. This integration adds the useful maintenance updates and failed-sync CLI
leaf to the earlier repaired candidate. It includes all 139 commits / 458 changed paths,
not merely the original stack-relative PR delta. No installed-state changes, capture
activation, original-source/R2/SSH inventory, refresh, release publication, archive reset/
rebind/deletion or legacy retirement occurred during this integration qualification.
Earlier experimental live work is separately recorded in the handoff archive.

| Check | Source-bound result |
| --- | --- |
| Actual canonical `make check` aggregate | **PASS, exit 0**, literal `ef0f3d3`; not a staged prerequisite union |
| Configured format/lint, fix/tidy, deadcode, collector vulnerabilities, benchmark smoke and actionlint | PASS; lint zero issues; all three modules tidy/verify |
| Full CGO/race/shuffle, atomic plus actual-subprocess coverage and CRAP | PASS; **87.5%, 13,963/15,963 statements**; maximum CRAP **29.40**, unchanged limit 30; syncer **509.493s** within native 600s/package |
| New failed-sync regressions and complete CLI/cmd race/shuffle | PASS; six new focused groups; old-source regression contrast genuinely RED |
| Actual writer → shared reader → importer → event index → report / launchers / delivery | PASS: **12 + 6 + 12**; one existing optional explicit SDK-repair-peer skip |
| Complete analytics discovery with actual candidate | PASS: **117 tests, no skips**; nonfatal HTTPError ResourceWarning retained |
| Real nested gopls dependency graph | PASS: actual pinned scanner in `tools/gopls`, **24 modules plus Go 1.27.3**, no vulnerabilities; collector-only scan was not substituted |
| New-tool offline pedantic workflow-security scan | PASS; normal online audit also passed in hosted CI |
| [Exact-head PR CI](https://github.com/mich-murphy/memory/actions/runs/41017288817) | **All seven jobs SUCCESS**, head `ef0f3d3`, base `main` |
| [Exact merged-main push CI](https://github.com/mich-murphy/memory/actions/runs/41018839999) | **All seven jobs SUCCESS**, push `main` at `b0f949c` |

Each hosted run includes Linux amd64/arm64, native macOS coverage/CRAP plus Darwin
vulnerability checks, static checks, actual consumer/full analytics, all nine unchanged
30s fuzz campaigns and workflow security. Fresh local timed fuzz campaigns were not
rerun for this integration; their actual hosted execution is recorded above. Neither
hosted native macOS nor Linux CI proves installed native-agent hooks or real R2 health.

The new lock was actually realized: nixpkgs `d3b13ad` and cacert 3.131.1, not a relabeled
older shell or CA. Recorded tools include Go 1.27.3, golangci-lint 2.15.0, Node 24.21.0,
Python 3.14.8 and Nix 2.28.5. Accepted local jobs used shared worker-side admission,
network-none validation, read-only source/private Git/Nix/tools/analytics/vulnerability
inputs, and **both module-cache aliases plus prepared binaries enforced read-only**.
Both `TMPDIR` and `GOTMPDIR` were executable VM-native EXT4. A bounded 1800s canonical
aggregate allowance accommodates the measured combined check; Go 600s/package,
race/shuffle, eight simulation histories × 40 steps, fixtures, privacy and quality gates
remain unchanged. All five accepted local jobs exited 0, OOM false, with exact owned
containers removed and pre-exit accounting retained.

Earlier writable-module runs are **functional diagnostics, not accepted canonical
validation**. Initial environment/TLS preparation, OCI mountpoint, missing/empty Git
metadata and genuine fixture-lint failures remain recorded failures. Valid read-only
source-bound Git metadata and the narrow same-byte invalid-UTF8 test-name correction
resolved them; no `-buildvcs=false`, linter suppression or weakened gate was used.

Private evidence:
`~/.local/state/machines-validation-pilot/memory-pr-integration-20261030/`:
`parent/FINAL-ACCEPTANCE.json`, `ci-runtime/LEDGER.md`, accepted `ro-*` job argv/log/
created/final/cleanup/counters, and separate local/CI manifests. Parent matched **737
source files and executable modes** to Git, verified **817 local evidence files** and
**19 CI evidence files**, independently checked both exact-head events/all seven job
conclusions, and verified merge-tree equality. The completed public disposition is
https://github.com/mich-murphy/memory/pull/30#issuecomment-6516354560

**Artifact and operating limits:** the earlier private snapshot
`0.5.0-SNAPSHOT-74c00f4` is **not source-aligned with this merged integration**. It is
not a published/installed release. No current-source artifact or source/CI pass proves
fresh installed health, actual automatic capture/report freshness, restart/outage
recovery, full two-machine coverage or settled/background CPU budgets. Those remain
separately gated in the current handoff; no new activation or reset is authorized here.

## 2026-10-30: earlier repaired-candidate release qualification

This dated Linux record concerns source **`74c00f4a985d9b2ad3ebe0028458e324f14c6b20`**:
keep the independently reviewed per-lane scheduling repair `881b04e`, plus portable
Unix stat-field adapters. The adapters preserve the exact previous `stat-v1` bytes
(including negative Darwin device wrapping) and quota arithmetic/error behavior.
Golden/extreme regression tests were added; no linter suppression, privacy check,
coverage threshold, fixture size, simulation history or native Go test deadline was
relaxed. This is **offline qualification, not deployment or operating acceptance**.
Current state and authorization remain in the [sole handoff](session-capture-handoff.md).

| Check | Source-bound result |
| --- | --- |
| Configured `make fmt-check` and `make lint` | PASS; lint zero issues |
| Complete `capturesource` and `sessioncapture` race/shuffle packages | PASS |
| Full `CGO_ENABLED=1 go test -race -shuffle=on ./...` | PASS; 30 tested packages, two with no tests; syncer365.971s |
| `make capture-acceptance` with actual writer and RO pinned analytics environment | PASS: 12 writer/analytics journeys, 6 candidate-launcher tests, 12 delivery tests; one existing optional explicit SDK-repair-peer test skipped |
| Complete analytics discovery with actual candidate binary supplied | PASS: **117 tests, no skips** |
| Unchanged merged atomic/race/subprocess coverage and CRAP | PASS: final **87.4%, 13,942/15,958 statements**; package/total/high-risk thresholds unchanged; maximum CRAP **29.40**, zero functions above30 |
| All `make check` prerequisites, completed in staged workers | PASS: fmt, lint, fix, all three tidy checks, deadcode, vulnerability scan, coverage/CRAP, benchmark smoke, actionlint and actual consumer gate |
| `make fuzz` | PASS: all **nine targets ×30s**, unchanged campaigns |
| `zizmor --offline --persona pedantic .github` | PASS, no findings; online audits remain hosted-CI scope |

Pinned environment: Nix2.28.5 / Go1.27.3 / Node24.21.0 / Python3.14.8, unchanged
flake/tool/module identities, separately qualified public node6f7 runtime and prepared
read-only analytics environment. All actual jobs used the shared worker-side Docker
admission and complete approved resource profiles. Source/dependencies/runtime were
read-only; test homes and **both `TMPDIR` and `GOTMPDIR`** were native executable EXT4.
The full race suite completed under a900s aggregate worker allowance after preserved
300s and600s aggregate timeouts. Go's native600s per-package limit, race/shuffle flags,
all eight simulation histories and40 steps remained unchanged. No timeout is a pass.

**Staged-check scope:** the full `make check` replay passed through benchmark smoke,
then exited2 because actionlint could not discover a project in the deliberately
Git-metadata-free source mount. The same unchanged `make actionlint` passed after
read-only private Git metadata was supplied; the earlier source-identical consumer
pass completes the prerequisite set. This is not a claim that the failed aggregate
command exited0. The pinned vulnerability client uses its supported
`-db=file:///vulndb` flag through a transparent `TOOL` adapter; all other pinned tool
argv and exits are forwarded unchanged. The mirrored public database includes all
**4,712 OSV IDs and three indexes**, with response/hash provenance, not a sparse
advisory subset. The adapter SHA is
`436b30c9e43017ce16c31b19154c64dece20c91e2373b39b25d741b6a4f452a5`.
The earlier standalone87.3% coverage run's raw log remains; its profile was overwritten
by the full replay before it was frozen. Do not reconstruct or claim that original
profile was retained. The final87.4% profile is separately frozen.

Private evidence: `~/.local/state/machines-validation-pilot/memory-release-20261030/`.
Parent verified all615 frozen source files and all tracked gate-input coverage.
Manifest `85ffde157f833c776b960a783c693fbe73ff6f6939df28b576ca1325f9755ffa`
starts at881 plus reviewed patch13ea4169; all Go/tests/scripts/modules/workflows/gate
configs match74c. Only two later, nonconsumed performance markdown files differ.
Initial lint failures, incomplete preparation attempts and false Docker0/trap-status
claims remain explicitly corrected in the ledger, not relabeled successful runs.

No local native-Darwin admission was invented. The existing hosted macOS workflow is
a separate platform route, not this Mac's installed paths, sandbox hooks, actual R2,
Mac analytics runtime or combined two-machine acceptance. Neither this record nor an
artifact build authorizes activation, archive reset/rebind, deletion or legacy
retirement. Prior881 performance evidence still leaves settled/background CPU and
operating-budget acceptance open; these release checks do not remeasure or solve it.

## 2026-10-24: historical offline candidate

Dated **2026-10-24 (AEDT)**. This records the opt-in, uncommitted candidate in the dirty
`9078a3e` worktree, not a deployed release. Requirements remain
[session-capture-contract.md](session-capture-contract.md) and
[session-enrichment-contract.md](session-enrichment-contract.md). See the
[current handoff](session-capture-handoff.md) for repairs and independent reviews.
A subsequent [current-state alignment audit](current-state-audit.md) changes documentation,
comments/help and its regression coverage; that page distinguishes its fresh scoped
checks from the full engine-candidate gates recorded here.

## Unchanged development gates

Run serially in pinned Nix, with `AGENT_SESSIONS_TEST_R2` and
`AGENT_SESSIONS_LIVE` unset, after implementation settled:

| Check | Result | Evidence |
| --- | --- | --- |
| `make check` | PASS, **622.40s** | `/tmp/memory-replacement-final.pIMS8w/make-check-current.log` |
| Merged atomic unit + actual subprocess coverage | **86.8%, 13,059 / 15,051 statements**; unchanged package/total gates PASS | `current-merged.cover.out` in the same directory |
| Root pessimistic CRAP scan | PASS; maximum **29.40**, unchanged fail-above **30** | Same check log |
| Format, lint, fix, module tidy, deadcode, vulnerabilities, actionlint | PASS; lint zero; no vulnerabilities found | Same check log |
| Benchmark smoke | PASS; syncer **113.092s** | Same check log |
| Actual writer → authoritative reader → importer → event index → report | All **11 process journeys**, **12.727s** | Same check log |
| Fresh `make test` | PASS, **460.36s**; full CGO/race/shuffle | `make-test-current.log` in the same directory |
| `make fuzz` | All **nine** targets PASS at unchanged **30s** each, **285.31s** overall | `make-fuzz.log` in the same directory |
| Fresh complete analytics discovery | **73 tests / 23.744s**, PASS, no skips/warnings | `analytics-current.log` in the same directory |

Full atomic check package times include syncer **433.512s**, queue **112.897s**,
CLI **74.599s**. Fresh test includes syncer **414.472s**, queue **117.885s** and
snapshot **67.416s**; its eleven journeys take **11.247s**. These are comparable
serial observations, not a throughput/freshness service-level guarantee.

The two intentional-crash fixture functions originally had falsely zero merged
coverage: the Go test helper exits before `M.Run` flush and cannot write runtime
metadata itself. In the explicit coverage-gate mode, the test now builds/runs the
**actual atomic/race-instrumented command** for that crash stage, and returning
children receive the gate's coverage-directory flag. Checked metadata/counter flush
occurs after durable output and before the real `os.Exit(0)`. It does **not** close
the queue or release the optional channel. Flush failure is a failed fixture, not
success. Normal uninstrumented fixture behavior is unchanged. Actual merged CRAP is
**28.48** for `isolationCrash` and **6.40** for the optional network boundary. No
coverage exclusion, threshold, race flag, simulation history, fixture size, assertion
or timeout was relaxed.

Original `cover.out` was backed up, restored even on failure, and hash-checked:
`70386d5160553c62f5b9cef9c38af0f7273336aebd491000ab9259fd94f117b9`.
Generated profiles remain separately under `/tmp`.

## Bounded synthetic resource observations

These are **small / single-source diagnostics**, not production capacity acceptance.
No actual native agent, original session, real credential lookup or remote endpoint was
used. A freshly built CGO-disabled candidate ran serially on this Mac (Apple M2,
Darwin arm64); `/usr/bin/time -l` reports per-command maximum RSS, not simultaneous
whole-service peak. Logs and owned fixtures remain at
`/tmp/memory-capture-resource.4KI0Oq/`.

| Synthetic operation | Wall | Maximum RSS | Scope |
| --- | --- | --- | --- |
| Local four-step fixture, repeat | 0.37s | 30,212,096 bytes | Complete append/subagent/rewrite fixture and reader export |
| Actual SDK factory four-step fixture, repeat | 0.13s | 32,063,488 bytes | Serializer/signer against in-memory HTTPS peer; **no DNS/TCP/R2** |
| Large source acceptance | 0.29s | 42,909,696 bytes | 134,218,178 bytes, 24,216 deterministic synthetic JSONL rows |
| Large source local publication | 0.58s | 52,543,488 bytes | Queue closes between CLI commands; local storage durability path |
| Large source authoritative export | 0.20s | 29,933,568 bytes | Complete selected body verified by SHA256 |

`diagnostic-final.log` and `large-diagnostic-passed.log` contain raw timing metadata.
The initial small fixture runs are slower (Local 0.98s, SDK 0.14s); cache effects
and tiny data prohibit an optimization or backend-speed claim. The SDK peer's
storage is in memory and is not a persistent-disk measurement. Repeated Local small
fixture allocated 40 KiB store / 184 KiB spool / 16 KiB export; SDK export 16 KiB.

For the large source, allocated sizes are 131,232 KiB spool, 131,088 KiB store and
131,080 KiB export, in addition to the original synthetic input. These are retained
copies, not temporary-overhead or worst-case rewrite/amplification bounds. Original
and spool paths were then **moved aside, not deleted**, and a new authoritative export
again restored the exact size and SHA256. Both bodies were hashed in 64-KiB reads.
A separate **1-MiB encoded quota** rejects that source with `ErrQuota`; read-only
SQLite inspection confirms **zero accepted jobs**. `large-verification.log` records
those assertions. Initial harness attempts correctly fail closed with uninitialized
policy; a mistaken diagnostic `du` path / SQLite filename was corrected without
changing capture code or security checks.

The implemented encoded quota, separate four × 1-GiB prepared-file bound, reserve
checks, decoder/snapshot/discovery/repair caps and cancellation checks remain as
specified in the contracts and implementation documents. These measurements do not
prove their combined RSS, worst-case filesystem usage, real-inventory sizing,
request cost, producer atomicity, kernel-I/O cancellation or discovery freshness.
Those remain explicit operating-resource acceptance work.

## Acceptance boundary and next action

Original-contract independent review `5d6e429c-b629-419` gives a **conditional PASS
for the offline candidate within its spot-check scope**, with no new confirmed
code/wiring blocker. Earlier independent privacy, source-read, WAL-provenance,
optional-progress and fatal-cause findings were reproduced, fixed and re-reviewed;
passing legacy tests were not substituted for consumer-contract proof.

Development gates are now clean. **Release acceptance is not complete.** Proceed to
controlled acceptance planning/measurement for actual native Claude/Codex/Pi context
and hook behavior, isolated real conditional R2 failure/readback, representative
resource/freshness loads, preserved inventories/policies on both machines, installed
paths/restart, writer fencing and cutover. Follow the original controlled sequence;
permission to replace R2 is not evidence of complete sources or authorization for
incidental live operations. Do not activate defaults, delete sources/retired keys,
reset an archive, install services or claim deployment from these offline results.
