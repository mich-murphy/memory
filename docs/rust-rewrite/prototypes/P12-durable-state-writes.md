# P12: cheaper durable state writes for the daemon

> **Renamed 2026-11-09** from P10 to P12: DESIGN.md section 16 already assigns P10 to Linux racy mtime. Scripts, patch env vars, scratch names and the R2 prefix used (`rust-acceptance/p9-<ts>/`) keep their original labels, so recorded hashes and commands stay valid.

Status: research prototype, 2026-11-09. Nothing in rust/, DESIGN.md or the handoff was changed. Raw data: `p12-durable-writes/`.

## Question
`hegel_daemon_state_machine` and `proptest_daemon_state_machine` take about 6 minutes at 2,000 cases each, because every simulated round does real `state::write_durable` calls (temp, `F_FULLFSYNC`, rename, directory fsync). What is cheaper, and what does the real daemon pay?

## Method
- Microbenchmark `p12-durable-writes/bench.py` (Python 3 stdlib, `python3 -I`). Python was chosen because the syscall sequence is identical and it needs no build. n=200 per cell. Results: `results-macos.json`.
- End to end: scratch copy of rust/ (`rust-p10`, `rust-p10v`), patched only in the copy (`e2e-variants.patch`, selected by env vars `P10_SYNC`, `P10_SKIP`). Command in `e2e.sh`: `HEGEL_TEST_CASES=2000 PROPTEST_CASES=2000 cargo test --locked -p memory --test daemon -- --test-threads=2` inside `nix develop`. Results: `results-e2e.jsonl`, logs in `logs/`.
- Host: APFS, internal Apple Fabric SSD, `/dev/disk3s1s1` sealed root (`env-macos.txt`). The bench ran in a scratchpad temp dir.
- Each e2e variant was a single run, so run-to-run noise is unknown. The differences below are large enough (more than 10x) that this should not matter.

## Measured: microbenchmark (macOS APFS, one durable write = temp + sync + rename + dir sync)
| sync | 1 KiB p50/p99 ms | 10 KiB | 100 KiB | 500 KiB p50 |
|---|---|---|---|---|
| F_FULLFSYNC (current) | 5.1 / 10.3 | 6.1 | 6.3 | 7.1 |
| F_BARRIERFSYNC | 1.3 / 9.9 | 0.57 | 0.62 | 0.76 |
| fsync() | 0.27 / 0.49 | 0.19 | 0.21 | 0.34 |
| none | 0.14 / 0.25 | 0.16 | 0.16 | 0.20 |

Candidate 5 (1 KiB append record, fsync per append, snapshot of 100 KiB every 100 appends): FULLFSYNC p50 2.9 ms, barrier 0.19 ms, fsync 0.03 ms. Append only, FULLFSYNC: 2.8 ms p50 (about 390 per second), fsync() 0.05 ms.
Candidate 3: hashing a 100 KiB body to skip an unchanged one costs 0.09 ms p50. The skip only helps when bodies repeat.
Candidate 4: coalescing K updates into one FULLFSYNC write divides the cost per logical update by K (K=10: p50 about 0, throughput 1,442 per second against 152 uncoalesced). That is arithmetic about call count, not a new finding.
Size barely matters under FULLFSYNC: the cost is the flush, not the bytes.

## Measured: end to end (2,000 cases each, both properties, 30 tests)
| variant | seconds | result |
|---|---|---|
| baseline | 421 | pass |
| fsync() everywhere | 16 | pass |
| F_BARRIERFSYNC everywhere | 30 | pass |
| no sync at all (rename ordering kept) | 14 | pass |
| FULLFSYNC + skip unchanged body | 345 | FAIL `shutdown_table` |

The skip variant is no faster, because status carries `rounds` and `updated_at`, so almost every body differs. It also fails `shutdown_table` (it expects `PermissionDenied` from `shutdown()` on a read-only dir, and the skip wrote nothing). Skipping therefore changes what that test checks. It is rejected as a test-time fix.

## DDIA ties
- ch08 Durability: "many databases therefore use the fsync() system call" (line 250); "even fsync isn't guaranteed to work correctly" (280); "fsync is hard to use; even PostgreSQL used it incorrectly for over 20 years" (287). Lesson: a flush is a hardware-dependent promise. Fewer, deliberate flushes are better than many assumed ones.
- ch04 write-ahead log (line 462): an append-only file for crash recovery. Candidate 5 is this pattern. Its checkpoints and compaction add recovery code the daemon does not otherwise need.
- ch04 line 1043: log-structured stores "perform writes in batches". This is candidate 4, group commit.

## Production footprint
The daemon writes status at most every 60 s (immediately only on parked or host-pause change) and the index at most every 60 s when dirty (DESIGN 9.2). That is at most 2 durable writes per minute, about 12 ms of flush time per minute (under 0.02% of wall time). The test is slow because the simulation compresses time (61 s per round), not because production writes are heavy. Candidates 3, 4 and 5 would save almost nothing in production and add complexity (cache, dirty flags, log recovery). Do not adopt them for production.

## Recommendation
(a) Production: keep candidate 1 unchanged, `F_FULLFSYNC` via `File::sync_all`. The cost is negligible at this write rate, and weakening the barrier trades away power-loss safety for nothing measurable. Do not add a WAL.
(b) Tests: candidate 6, a sync policy on `StateDir`. In the simulation properties, build the `StateDir` with `Sync::None` (keep temp, write, rename and directory open, drop only the flush calls). Measured: 421 s down to 14 s (30x). `Sync::Barrier` gives 30 s if some device-level flush is wanted. Rationale: the properties never cut power. An fsync has no effect on what a live process reads back, so none of the properties' assertions can observe it. Rename and publish ordering stay exercised. state.rs's own unit tests keep the real `Full` path.
What is lost: nothing observable by the current properties. What is not covered either way, before or after: a power-loss crash model. If wanted, add a deterministic crash-point property (drop the daemon between `stage` and `publish`, which `Staged` already supports, and reload). That is cheap under `None`.
Proposed text:
- DESIGN 9.2, new sentence: "The flush calls (steps 2 and 4) are governed by a `SyncPolicy` (`Full` by default). Only tests may select a weaker policy; the daemon binary always uses `Full`."
- state.rs: `pub enum SyncPolicy { Full, None }`, a `sync: SyncPolicy` field on `StateDir` (a `#[cfg(test)]`-free constructor, `StateDir::with_sync`, `#[doc(hidden)]`), and `write_synced` and `fsync_dir` take the policy and skip `sync_all` for `None`. Default stays `Full`. Add one state.rs test that the default is `Full`.
- tests/daemon.rs: the `boot` helper used by `Sut` selects `SyncPolicy::None`. Keep a small non-property test with `Full` (the existing example tests already use it).
- Do not apply the skip-unchanged variant. Candidate 7 (clock step) was not run; it would not remove the 60 s rules from coverage but would make the properties exercise fewer write paths per case. Prefer 6.

## Not measured or unverified
- BC-01 (Linux): ssh failed (`sign_and_send_pubkey: signing failed ... communication with agent failed`, then `Permission denied (publickey)`; status 255). No remote work was done, so nothing was created there or needs cleanup. The Linux numbers are missing, and the fsync-versus-fdatasync cost on ext4/xfs is unknown.
- Candidates 4, 5 and 7 were measured only in the microbenchmark (4 and 5) or not at all (7), not end to end. Candidate 3's combined status plus index file was not patched.
- The exact crash guarantee of `fsync()` and `F_BARRIERFSYNC` on APFS: I did not read `man fsync` or `man fcntl`, so this is unverified. The widely stated claim (fsync does not force the drive cache flush on macOS) is not confirmed here, and the fast fsync() timing is consistent with it but not proof.
- The patched copy used `unsafe` and `unsafe_code = "allow"` in the copy only. The repo forbids unsafe, and the real change needs no unsafe (the policy just skips `sync_all`).

## Parent verification (2026-11-09)

- **Repo untouched.** All 77 files of the gate-8 source snapshot
  (`implementation/evidence/12-slice2-round10/source-before.json`) still match;
  `~/.local/state/memory` is absent.
- **Logs agree with the table.** `logs/*.log`: baseline, barrier, none and plain each
  `30 passed`; skip-unchanged `29 passed; 1 failed` (`shutdown_table`, daemon.rs:1869).
  The cargo-internal times (381.6, 29.1, 11.8, 12.7, 343.7 s) are below the wall times in
  `results-e2e.jsonl` by build time, as expected.
- **Barrier semantics, from the pinned macOS 14.4 SDK** (`usr/include/sys/fcntl.h`; the man
  pages are not installed here): `F_FULLFSYNC 51 /* fsync + ask the drive to flush to the
  media */`, `F_BARRIERFSYNC 85 /* fsync + issue barrier to drive */`. A barrier orders writes
  but does not ask for the media flush. That supports keeping `F_FULLFSYNC` in production.
  The SDK comments do not settle what plain `fsync()` guarantees on APFS.
- **BC-01 still unmeasured.** The parent's own rerun of `bc01-script.sh` also exited 255:
  the local SSH agent holds no identities (`ssh-add -l`: "The agent has no identities"). It
  is an environment condition, not a host refusal. Nothing ran on BC-01.
