# Evidence 31: short run with the current build, and cutover on this Mac (DESIGN 18)

All dates are UTC, 2026-11-19. Operator approval was given the same day ("1 approved, 2 go for it, i'll
approve"). Both steps used one release build of `main` `56c04aa` (`cargo build --release --locked -p
memory`, sha256 prefix `d0116373ebab1f94`). This record has counts only: no owner text, path under
a session root, credential or content.

## Short run `20261119-56c04aa`

Purpose (handoff item 8): measure peak memory on real data against the 128 MB target (D45). Then
list every owner, export each one and import each export into analytics.

- **Setup.** Binary `~/.local/libexec/memory-short/memory`, state root `~/.local/state/memory-short`
  with its own `config.toml`: prefix `rust-acceptance/20261119-56c04aa/`, archive id
  `acceptance-20261119-56c04aa`, the three default roots, and the token copied from the acceptance
  run without being read. The daemon ran under `nohup` with `MEMORY_STATE_DIR`, with no launchd
  agent and no hooks. A 10 s `footprint` sampler and `caffeinate` ran alongside.
- **Daemon.** Started 11:39:17Z. The first round (`cause=start`) ended at 12:23:04Z (44 minutes)
  with `landed=1076 failed=0 parked=0` and every failure class at 0. Then 6 rounds (`start` 1,
  `timer` 5), no `WARN` or `ERROR` line, and no round with `failed` above 0. Stopped with SIGTERM
  at 13:47:06Z; the daemon logged `stopped`.
- **Peak memory** (`phys_footprint_peak`): 72 MB at the end of the first full reconcile. It rose
  to 100 MB at 12:38:12Z and 103 MB at 12:38:22Z, during the first timer round's audit (6,026
  sources), and stayed at 103 MB to the end. **Under the 128 MB target.** The run on the
  `a28bfa4` build, before streaming, peaked at 1,254 MB (evidence 21).
- **`memory check`**:
  - 12:23:25Z to 12:59:23Z, exit 3: `match=6043` of 6,045 and `store_error=2`, with mismatch,
    uncaptured, withheld and parked all 0. `store_error` counts failed GETs while reading an
    owner. The check prints no further detail.
  - 13:27:06Z to 13:46:33Z, exit 0: 6,045 of 6,045 match, `store_error=0`, all else 0. So the
    two GET failures were transient. Open finding: `check` does not report which failure class
    a store error had.
- **`memory owners`** (12:59:41Z): exit 0, `owners=1074` (Claude 361, Codex 28, Pi 685). The
  bucket listing showed 1,076 `current.json` keys. The 2 extra are the shared owners, which
  `memory owners` does not list by design (brief 33).
- **Export and import** (13:00:45Z to 13:26:56Z). A driver script ran `memory export <owner> --out
  <dir>` for each listed owner, then `publications.import_export` into one private DuckDB file:
  - export: 1,074 of 1,074 exit 0, 6,041 sources, 0 `skipped_deleted`, 4,670,690,025 bytes,
    1,388 s;
  - import: 1,074 `imported`, 0 failures, 943,892 events, 21,380 issues, 135 s;
  - second import of the same exports: 1,074 `unchanged`, 26 s;
  - sessions by agent: Claude 361, Codex 28, Pi 685; normalization `complete` 845, `partial` 229.
- **Prefix.** The cleanup tool hard-codes one run prefix in `valid()`. It was changed to the new
  prefix, rebuilt, and then refused the old one. Listed 8,264 objects (`current` 1,076,
  `manifests` 1,080, `objects` 6,108) and deleted 8,264 (13:48:35Z to 14:03:23Z). A second
  listing returned 0, `remaining_after` 0 (A2).

## Cutover on this Mac (DESIGN 18)

- **Step 6 first (bucket census, 14:08Z).** A whole-bucket listing that prints only top-level
  folder names and counts found `delivery-artifacts` (2 objects) and no `rust-acceptance/` prefix.
  So the bucket held only what step 6 allows before the first production write.
- **Backups** (in `~/.local/state/memory-cutover/backup/`, 0600): `~/.claude/settings.json`,
  `~/.codex/hooks.json`, `~/.codex/config.toml`, the Go capture-v1 Pi extension and the Go
  `agent-sessions-capture-experimental` plist.
- **Production config** (`~/.local/state/memory/config.toml`, 0600, created with `O_EXCL`):
  - The archive id and machine id are copied from the Go `capture-runtime.json` (DESIGN 9.1). Both
    are valid Rust identifiers, and the machine id equals the Go `machine-id` file.
  - The three default roots, `bucket = "agent-sessions"`, `prefix = ""`, and token
    `~/.local/state/memory/token.toml` (0600).
  - The Go config has no custom source roots (`roots` is empty). Its one `enrichment_roots` entry
    is a snapshot selector for a folder with no agent session data, which is slice 3, so it is not
    carried over.
  - Binary `~/.local/libexec/memory/memory`, the same build as the short run.
- **Go hooks removed (step 2).**
  - `capture-v1 hooks --action uninstall --apply` exited 0. It rewrote `settings.json` and
    `hooks.json` (no `agent-sessions` handler is left in either) and deleted the capture-v1 Pi
    extension. It left a backup beside the extension, named `….ts.agent-sessions-backup-<n>`.
    Pi loads only `*.ts`, so that file was never loaded; it was moved into the cutover backup.
  - `capture-v1 service --action uninstall --apply` exited 1 (`experimental service uninstall
    command failed`). Its first step is `launchctl bootout`, which fails on a label that is
    disabled and not loaded, so the plist removal after it never ran. With the label confirmed
    not loaded and no Go capture process running, the parent did what Go's `removeServicePlan`
    does: check that the plist is a regular 0600 file, remove it, and fsync the directory. The
    Go publication-dashboard and publication-refresh agents were left as they were (refresh is
    disabled since 2026-11-09).
  - Codex `config.toml` was unchanged by the Go uninstall.
- **`memory install` (step 2, 14:12:05Z).** A `--dry-run` first validated the config and token.
  The real run wrote `~/Library/LaunchAgents/memory.plist` (`plutil -lint` OK), 8 Claude and 6
  Codex `memory hook` handlers (each file backed up as `*.memory-backup-20261119T141205Z`), and
  `~/.pi/agent/extensions/memory.ts`. Other handlers kept: Claude 11, Codex 5. Then `launchctl
  bootstrap gui/$(id -u) ~/Library/LaunchAgents/memory.plist` gave `state = running`, `runs = 1`,
  and the log shows `started` at 14:12:13Z.
- **Opt-out record (step 3).** The one file in `~/.local/state/agent-sessions/exclusions` was
  moved (not deleted) into the cutover backup. The Go path no longer runs, so nothing reads it.
- **First capture (step 4).**
  - First round, ended 14:55:06Z (43 minutes): `landed=1075 failed=1 offline=1 parked=0`.
  - The timer round at 15:10:29Z had `failed=0`, `audit_matched=6026`, `audit_mismatched=0`.
  - `status.json` after it: `parked` empty, failures `offline=1` only, audit
    `matched=6045 mismatched=0`.
- **Verification (step 5, this host).**
  - `memory check` from 15:10:52Z to 15:44:39Z, exit 0: `owners=1076 sources=6045 match=6045`;
    behind, unsettled, mismatch, uncaptured, withheld, remote_only, parked, duplicate, unreadable
    and store_error all 0. So the owner that was `offline` in the first round was retried and
    captured.
  - Restart: `launchctl kickstart -k gui/$(id -u)/memory` at 15:45:12Z. The old process exited 0,
    `runs = 2`, and the new process logged `started`. Its start round ended 15:49:53Z with
    `landed=1` (a session that changed meanwhile) and `failed=0`. So it resumed from its index
    and did not upload everything again.
  - Restore and analytics: `memory owners` listed 1,074, the same split as the short run. All
    1,074 were exported (6,041 sources, 4,670,827,693 bytes, 1,279 s), imported (944,086 events,
    0 failures) and imported again as `unchanged`.
  - Peak memory: 76 MB over both processes (733 samples to 16:15Z).
  - No `WARN` or `ERROR` line in the daemon log.
- **Hooks not yet verified.**
  - No hint day file existed by 16:16Z. No Claude or Codex session had started since the install.
    Pi loads extensions at session start, and the Pi sessions active at the install were older.
  - The Codex trust records in `config.toml` were unchanged (14). The operator still has to
    approve the six `memory hook codex` entries in Codex `/hooks`.
  - Until hints arrive, capture relies on filesystem events and the 15-minute timer, which is
    enough for correctness (DESIGN 10).
- **Cleanup.** The two export trees from the driver (12 GB each, plaintext copies of sessions)
  were deleted. The token copies under the finished acceptance and short-run state roots were
  removed. `caffeinate` was stopped; the footprint sampler (`~/.local/state/memory-cutover/
  sampler.pid`) keeps running until the next check.

## Not done here

- BC-01 (DESIGN 18 step 2, "then repeat on the other host"; slice 3).
- Hook delivery on the production install: Claude, Codex (needs the operator's `/hooks`
  approval) and Pi.
- The 2-day exit criterion on the production install (slice 2b, A13.1). The acceptance run met it
  on the `a28bfa4` build; this install has run for about 2 hours.
