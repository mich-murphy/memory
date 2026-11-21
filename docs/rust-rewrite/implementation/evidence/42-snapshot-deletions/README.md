# Evidence 42: deletions of snapshot sources (brief 48)

All dates are UTC, 2026-11-21. Status: increment SNAPDEL accepted locally at `3e1a51a`. That is
4 worker commits and 1 parent commit over `a29f44d`. During the work nothing was installed,
nothing was sent to R2, and nothing read a production state root or real agent data. Both
production daemons kept running.

[Brief 48](../../48-snapshot-deletions.md) sits on `a29f44d`, on top of `main` `9353b4c`. Reason:
since briefs 45 and 47 the archive holds live snapshot entries (Codex stores, Claude
file-history), but D52 recorded deletions for append entries only, so a removed snapshot file
stayed live in the manifest forever.

## Commits

| Commit | By | Change |
|---|---|---|
| `27c8bc0` | worker | `candidates()` no longer skips snapshot entries; `all_gone` counts any live entry, so an owner whose only live entries are snapshots stays in the owner set. Five `Gate`-rig tests: second full round only, back before the second round, incomplete walk / missing root / stat error delete nothing, owner set, `capture_owner` never counts absence |
| `1ffa4ad` | worker | A recreated snapshot lands as a new live epoch (`Replace`, next epoch number, `deleted_at` cleared). `reconcile_once` drops a local record that holds only a withheld judgement after two consecutive complete walks that did not yield it; no manifest change is built |
| `71a4dd1` | worker | Real binary against the fake S3 store: a session with a transcript and a clean file-history file, plus a synthetic Codex store. One miss gives `deleted=0`, the second `deleted=2`; `check` exits 0 with `remote_only=0`; `restore` reports `written=1 skipped_deleted=1`; `export` lists the deleted source `sealed`; the recreated store lands (`landed=1 deleted=0`) and `restore` of the shared owner writes it with `integrity_check` `ok` |
| `98af0e9` | worker | Module docs |
| `3e1a51a` | parent finding | Test `a_walked_withheld_file_with_a_stat_error_keeps_its_record`: the `seen` guard of the withheld-only drop survived every test, because a walked file normally resets its count through `Present`. On a stat error the step returns before `Present`, so without the guard a present, withheld file's record would be dropped. D52 says a stat error is not an absence |

No existing test expectation changed. `deleted_and_epochs` in `tests/daemon.rs` accepts one more
byte string (`SNAP`) so it can observe snapshot owners. `memory-core` is unchanged.

A recreated snapshot shows one live epoch afterwards, not two: `Replace` clears the earlier
epochs. Its bytes are still in `objects/` (nothing is purged).

## Parent acceptance

- Whole diff read. The withheld-only drop does not consult the missing-root guard. The effect is
  local only: the cached judgement is lost and the file is scanned once more when its root comes
  back; nothing changes in R2. Accepted as is.
- Teeth, 10 of 10 caught (`snapdel_teeth.py`, focused filter with a 151-test baseline):

  | Tooth | Result |
  |---|---|
  | Snapshot entries skipped as candidates | caught (5 tests) |
  | One miss deletes | caught |
  | Truncated-walk guard dropped | caught (2) |
  | Missing-root guard dropped | caught (1) |
  | `all_gone` append-only | caught (5) |
  | A recreated entry stays deleted | caught (5) |
  | Withheld-only record kept forever | caught (1) |
  | Withheld-only record dropped after one miss | caught (2) |
  | Withheld-only drop ignores the walk | survived at first; caught (1) after `3e1a51a` |
  | `capture_owner` counts absence | caught (1) |

- One listed tooth could not be built as a meaningful mutant: "a missing `-wal` counted as
  absence". The absence probe runs only for ids the walk did not yield, and a store whose
  database file exists is always yielded; the probe `stat`s the database file alone.
- Full gates on `3e1a51a`: 14 of 14, seed 1340466347, nextest 879 passed, consumer 3, line
  coverage 95.58 %.
- Linux container (Ubuntu 24.04): `memory` `capture` 35 and `daemon` 95, all passed.

## Merge, install on both hosts, two full rounds

- Stack #235 (PRs #232 to #234) merged as `87795f3`. Rust gates passed on all three PRs. CI failed
  only in the two accepted `govulncheck` steps.
- This Mac: installed in place at 11:06:04Z (sha256 prefix `58ccbc7209aea599`, `runs = 12`). The
  `36adb8c` binary is kept as `backup/memory-36adb8c`. Start round at 11:09:26Z: `landed=1
  failed=0 local=0 deleted=0 withheld=0`, `tmp/` empty, no WARN or ERROR line, peak footprint
  76 MB. Timer round at 11:24:31Z: `landed=0 deleted=0`.
- dev-01: `87795f3` built on the host (sha256 prefix `7a3998b1cbeee5a4`). Both Nix store
  dependencies are the pinned GC roots. Installed in place at 11:14:33Z, with the `36adb8c` binary
  kept as `backup/memory-36adb8c`. Start round at 11:15:35Z: `landed=2 failed=0 deleted=0`,
  `VmHWM` 103 MB, `NRestarts=0`, no WARN or ERROR line. Timer round at 11:30:39Z: `landed=3
  deleted=0`.
- Two consecutive full rounds with `deleted=0` on each host, as expected: every captured snapshot
  file still exists.
- `memory check`, exit 0 on both hosts, `remote_only=0`, 0 mismatch, 0 uncaptured, 0
  `store_error`. This Mac (12:13Z to 12:36Z): 1,080 owners, 7,012 sources, 6,871 `match`, 141
  `withheld`. dev-01 (11:31Z to 11:35Z): 450 owners, 4,838 sources, 4,739 `match`, 4 `behind`
  (sessions active during the check), 95 `withheld`. Every `withheld` line reads
  `kind=claude.file_history reason=secret_in_text`.
- A first Mac check started in the background was killed with its process group when an unrelated
  SSH call timed out; it was rerun in the foreground. No result was lost or substituted.

## Limits

- Bytes of deleted sources are never purged (DESIGN 19, default 4).
- Watcher events still route append sources only; a deletion is observed by full rounds only, as
  for append sources.
- Withheld versions are still not recorded in R2.
