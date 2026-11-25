# Evidence 54: `restore`, `check` and the read-back removed (brief 59)

All dates are UTC. Status: accepted locally on 2026-11-25 at `31519cc` (top code branch) over the
brief `d2055b6` on `main` `b358ebc`. [Brief 59](../../59-no-restore.md) records the decision and
what guarantees the stored data.

## Commits

| Commit | By | Change |
|---|---|---|
| `93e8af0` | RUST worker | `memory restore`, `memory check`, `Fault::ReadbackMismatch`, `ParkReason::RepeatedReadbackMismatch` and the reader's `read_owner`, `restore_source`, `restore_epoch`, `RestoreError` deleted; shared exit codes and store-failure output moved to `exit.rs`; `read_state`, `read_status`, `parked_line`, `Refusal`, `code_of` moved to `status.rs`; tests re-oracled |
| `b99bcf8` | RUST worker | `rust/README.md` without `restore` and `check` |
| `733d252` | parent | A broken `status.rs` doc line; the snapshot-deletion test's session export asserts the deleted source (`deleted=1`), replacing the dropped restore assertion |
| `5fb84c4` | parent | `memory-reader/tests/decode.rs`: the six `decode_piece` tests that the worker deleted with `restore.rs`; export relies on that function |
| `31519cc` | DOCS worker | DESIGN body (sections 1, 6, 7.3, 8, 11.1 to 11.5, 12, 13, 17, 18; numbers kept, D rows untouched), AGENTS.md, README, install guide, capture contract, quality gates |
| record | parent | This record, D71, handoff item 38 |

Diff over the brief: 59 files, 511 insertions, 3,721 deletions.

## Tests

- A test-only oracle `tests/common/epoch.rs` (`epoch_bytes`: `decode_piece` per piece,
  contiguity, `size`, `sha256`) replaces `restore_source` in `audit.rs`, `capture.rs`,
  `daemon.rs`, `capture_property.rs`, `s3_store/skeleton.rs` and `s3_store/assemble.rs`.
- `memory export` bodies compared with disk replace `memory restore` in `daemon_setup.rs`
  (`exported`, formerly `daemon_restore.rs`), `event_loop.rs`, `ops.rs`, `snapshot_kinds.rs`,
  `snapshot_deletions.rs`, `file_history.rs` and `assemble.rs`. The consumer gate's restore
  block went; its `check_bodies` already compares export bodies with disk.
- Every comparison of stored bytes with disk survives. Assertions about `check`'s own output
  went with it (`mismatch=0`, `uncaptured=0`, `remote_only=0`, per-file `withheld ... reason=`
  lines); the `status` and round `withheld=` counts in those tests are unchanged, and the
  undecodable `.zst` test now asserts `current=0` and nonzero `unreadable`.
- Deleted with the code they tested: `memory-reader/tests/restore.rs` (its decode tests kept in
  `decode.rs`), `check_property.rs`, `s3_store/check.rs`, `s3_store/restore.rs`.

## Teeth (the checks that keep stored data good)

| Tooth | Result |
|---|---|
| `decode_piece` skips the raw digest | caught (`memory-reader`) |
| `decode_piece` skips the stored (encoded) digest | survived, then caught once `decode.rs` restored `wrong_encoded_digest_is_rejected` |
| export skips the epoch digest | caught |
| export skips the contiguity check | caught |
| PUT sent without the `content-md5` header | caught (`s3_store`); the first aim removed only the signed header and left the wire header, so it was re-aimed |

## Gates

- Worker run on `b99bcf8`: 14/14, seed 2200733068, line coverage 95.77 %.
- Parent run on `31519cc`, 500 cases: 14/14 passed, seed 1881541311, line coverage 95.77 %
  (floor 80), including `10-consumer`.
- Not run: the Linux container (hosted CI runs the gates on `ubuntu-24.04` and
  `ubuntu-24.04-arm`).

## Hosts

Nothing scheduled `memory check` on either host (no launchd, systemd timer or cron entry).

## Install of `126ac48` (2026-11-25)

Stack #298 merged as `126ac48`. The capture binary changed (subcommands and closed enums).

| | This Mac | dev-01 |
|---|---|---|
| sha256 prefix | `4d3bb3e2327164a4` | `1dee9f0eb02f65d3` (built there; `ldd` store paths equal the gcroots) |
| Backup | `memory-befdd0a` | `memory-befdd0a` |
| Start round | 13:51Z, `landed=1 failed=0 withheld=0`, no WARN/ERROR, `tmp/` empty | 13:56Z, `landed=3 failed=0 withheld=0`, no WARN/ERROR, `tmp/` empty, `NRestarts=0` |
| `memory status` | exit 0, `state=loaded index=loaded`, 6,088 sources, 1 behind (live), `audit_mismatched=0` | `state=loaded index=loaded`, 4,707 sources, 3 behind and 1 unindexed (live), `audit_mismatched=0` |
| `memory check`, `memory restore` | exit 78 (unknown subcommand) | exit 78 |
| `memory export` of one owner | exit 0, `sources=1 deleted=0`; output deleted | exit 0, `sources=1 deleted=0`; output deleted |
| Daemon | pid 9471, RSS 50 MiB after the start round | `VmHWM` 100 MiB |
