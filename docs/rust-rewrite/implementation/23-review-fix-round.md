# 23. Review and fix round (2026-11-09/10)

Status: committed on `rust/coverage-floor-80` (draft PR #60, top of stack #56). Integrated
gate green on `d8e80a6`; hosted CI green on `d8e80a6`. No slice or release acceptance is
claimed. No secrets or transcript content.

## Why

A read-only review (four Sonnet reviewers, parent-verified) judged the direction right but
found the code a well-tested library, not a running system, with privacy at the store sink
held by convention, test-only settings reachable from production, representable illegal
states and gaps in the DESIGN 15 evidence. The operator then asked for an 80 % coverage
backstop, removal of tautological tests, fixes for every finding and source-bound evidence.

## Commits

| Commit | Change |
|---|---|
| 9206235 | A9: fixed 80 % line-coverage backstop, not a ratchet |
| 136cf0d | 7 tautological tests removed, ~17 tightened to literal oracles; `core_coordinates` proof-only guard fixed by constructing the case (guard not lowered) |
| 2a18883 | `Published` upload type with crate-private constructors, `Store::document` removed (compile-fail doctests); `SyncPolicy::None`/`start_with` behind the `sim` feature; serialization failures returned instead of empty writes; per-owner `not_before` retry scheduling and per-class failure counts; pure `verify::classify` in core; `thiserror` shell errors |
| 6c8846e | Core types: derived trust (no duplicate field), `AppendBasis`, `Observation::MissingOnce/MissingConfirmed`, unit `PauseHost`, private `RulesDigest`, public `transition` removed, coalescer eviction and `next_due` |
| a1920b3 | `manifest` split into submodules; `Piece::from_authorized` (fields private); private `Delta`/`Checkpoint` fields; fail-closed credential regex; explicit deny on absent owner |
| 295a092 | `environment::Counting` wrapper; counter tests through `Daemon`/capture; property 3 (Hegel) |
| 9482e9f | Delta-only Extend reads with a resumable `Sha256State` (bug found by the counter test) |
| e2e3cd9 | `scripts/check-mutations.py`, gate `06-mutations` |
| 42e8826 | Daemon state machine: rewrite, truncate, remove-source, revoke/restore, crash, late write |
| e94a09b | Daemon 1a oracle: per-byte exemption only at positions of unaudited mid-file rewrites |
| 8794e19 | Mutation scripts share one temp target under `gates.sh` (macOS CI wall time) |

## Findings fixed with evidence

- **Read amplification (R5, DESIGN 15 counter bound).** An Extend capture read `[0, to)`.
  A 59 KB append to a 200 kB source read 328,632 bytes; it now reads 128,632 (bound
  59,000 + 68 KiB = 128,632, asserted). The delta path runs only when
  `AdmittedSource::prefix_stable()` holds (no protected document, not Config/TextAsset), the
  index's `captured_to` equals the planned `from`, and the stored state digests to the
  committed `whole`; otherwise the full read is kept. The state travels in the same index
  update that is applied only after a landed, non-follow-up commit. Delta bytes still go
  through `transform` and `authorize`; piece offsets take an explicit base. A Hegel property
  compares the index digest with an independent `sha2` digest of the file, including deleted
  and corrupted index cases.
- **Daemon 1a oracle.** The first 2,000-case gate failed (seed 730212): bulk > 68 KiB,
  mid-file rewrite, settle, mid-file rewrite, late write. Cause: the model assumed the first
  capture preceded the first hidden rewrite. DESIGN 15 1a covers changes that alter identity
  or touch a window; 7.2 says only an audit finds a same-size rewrite between the windows.
  The oracle now exempts only those byte positions, and only for values the source held
  there; every other byte must match a held version exactly. Liveness is checked outside the
  masked positions too (previously skipped). Not a daemon bug.

## DESIGN 15 status after this round

| Item | State |
|---|---|
| 1a safety, 1b liveness | 1a: core property 1 and the capture and daemon state machines, mutation-checked (window). 1b "after one audit run": **untested**, audit evidence is always `NotDue` |
| 2 coalescer | Hegel and proptest |
| 3 idempotence | Hegel through `Daemon`: converged round, restart, index deletion make zero PUTs. **No writer-version seam** (`WRITER_VERSION` is compile-time), no proptest twin |
| 4 state machine | Capture level (`capture_property.rs`): append, rewrite, truncate, remove-source, crash before PUT / after piece / after manifest, committed-reply-lost, committed-then-5xx, restart, delete-index, go-offline/online, revoke/restore token, foreign writer. Daemon level (`daemon.rs`): append, bulk, rewrite (head, tail, between windows), truncate, remove-source, revoke/restore, crash after piece or manifest before `current.json`, late write (A6), lost reply, offline/online, restart, delete-index; 12-shape guard with per-shape negative controls. Both in Hegel and proptest. **`rename-replace` is modelled at neither level** |
| 5 privacy | Pure canary properties; daemon canary test on PUT bodies; credential-deny mutant killed; sink typed (`Published`) |
| 6 schemas | Round-trip, unknown schema rejected, delta-from-checkpoint property |
| 7 locator | Hegel and proptest |
| Counter tests | Idle round, 30k sources (≤ 1 stat each, 0 reads, 0 PUTs), 59 KB append bound, all through production code. **Wakeups not counted**; 1 s/30k measured (1.24 s debug), not asserted |
| Journey test | **Not started** (needs reader) |
| Footprint | **Not measured** (slices 2 and 3) |

Mutation checks: window comparison (planner), blind/latest-ETag `If-Match` (commit),
index advanced after a commit that did not land (daemon), skipped credential denylist
(privacy). All killed by independent-oracle tests.

## DESIGN 17 slices

- Slice 0, 1: met (P5 waived, A1).
- Slice 2 (daemon on the Mac): lock, hint files, watcher, index/status with durable writes,
  commit protocol, capture wiring, property 4 and counter tests exist against fakes. **Not
  built:** the S3 client (`rusty-s3` + `ureq` chosen, P2; `cargo deny` license decision on
  `CDLA-Permissive-2.0` open), zstd, the run loop (`memory daemon` exits 78 after startup).
  Exit (a day of this Mac's sessions captured) not attempted.
- Slice 2b (reader), slice 3, cutover: not started.

## Integrated gate

`nix develop --max-jobs 2 --cores 2 --command sh -c 'cd rust && PROPERTY_CASES=2000
PROPERTY_SEED=730213 bash scripts/gates.sh'` on `d8e80a6`: exit 0, all 13 subgates 0,
nextest 391/391 (2 ignored benchmarks), 12 compile-fail doctests, coverage 5,595/5,766 =
97.03 % (floor 80), 87 source files unchanged during the run. Evidence:
[evidence/13-review-fix-round/](evidence/13-review-fix-round/). An earlier run at
`e94a09b` (seed 730212) was also green.

Hosted CI (500 cases, random seed): the first Rust run on PR #60 was cancelled at the macOS
30-minute job limit 8 s after all 13 subgates returned 0. 8794e19 removed a duplicate
dependency build; the timeout was not raised. Runs 42627740003 (8794e19) and 42631230006
(d8e80a6): all three Rust runners success; CI workflow success.

## Open

- `LaunchRecord` is defined twice (P3 unification not done).
- Damaged `status.json` clears parks without a logged event (DESIGN 9.2 tension).
- No `rename-replace` step at either state-machine level.
- The earlier macOS llvm-cov "unconfirmed failure" of `hegel_daemon_state_machine` (run
  42565602976, seed 938868371) was not root-caused; it has not recurred.
- P2 decisions: `cargo deny` license for `CDLA-Permissive-2.0`, R2 `BadDigest` class,
  single checksum.
- Slice 2 delivery: S3 adapter, zstd, run loop; then the slice-2 exit.
- Native, footprint, live R2 and journey gates were not run.
