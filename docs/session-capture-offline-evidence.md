# Replacement capture: final offline evidence

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
