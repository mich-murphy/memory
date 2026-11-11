# 14. Daemon state (slice 2)

Implemented: `rust/crates/memory/src/{config,state}.rs`, tests in `rust/crates/memory/tests/state.rs`.
Not implemented: the daemon loop, reconcile, index/status/roots schemas (bodies are opaque bytes here).

## Design decisions

- State root: `MEMORY_STATE_DIR`, else `$HOME/.local/state/memory`, identical to `hook.rs` (`root_from`, `default_root`). hook.rs is unchanged. DESIGN 9.1.
- Lock first (DESIGN 12): `DaemonLock::acquire` vets/creates the root (0700), opens `daemon.lock` (0600, no truncate) and `File::try_lock`. `WouldBlock` is `StateError::LockHeld`, exit code 75 (`EXIT_LOCK_HELD`), all other errors exit 1. `StateDir::prepare` requires a `&DaemonLock`, so `tmp/` is never swept without the lock.
- Envelope (DESIGN 9.2): one header line `memory-state/1 <schema>/<version> <writer_version> <len> <sha256hex>\n` plus body. Write: temp file (`create_new`, 0600) → `sync_all` → rename → fsync dir. Read never errors; every failure is `Loaded::Missing(reason)`: NotFound, Unreadable, TooLarge, BadHeader, UnknownVersion (envelope version), SchemaMismatch, NewerSchema, ShortBody, ExtraBytes, ChecksumMismatch. Older schema versions are readable.
- `state/tmp/` is emptied at prepare, and stale `.*.tmp-*` files in the root are removed.
- Machine id (DESIGN 9.1, O_EXCL): the envelope is staged, then published with `hard_link` (fails with EEXIST, never partial, never overwrites). The loser reads the winner's id. A damaged existing file is `MachineIdCorrupt` and is left untouched.
- Hint dir: `vet_hints_dir` refuses symlink, non-directory, group/world-writable (`mode & 0o022`, same as hook.rs) and not-owned.
- Owner check without geteuid: `prepare` creates a probe file with `create_new`; its uid is the daemon's effective uid. Sound on local filesystems. Unsound where the filesystem remaps owners (root-squashed NFS), or where a directory has a setgid/inherited-owner scheme that changes file owners; one user per host excludes these. Only the final path component is vetted; a symlinked parent is allowed.
- Config: serde `deny_unknown_fields` on every table, TOML. `ArchiveId`/`MachineId` from memory-core. Bucket 3..63 chars `[a-z0-9-]` with alnum edges. Window defaults 60/300 s; quiet > 0, cap ≥ quiet, cap ≤ 86400. Roots and token path must be absolute. `check_token` is metadata only: regular file, `mode & 0o077 == 0`. Errors are closed and never echo values. The 1 MiB read cap is mine, not DESIGN's.

## Std facts verified (toolchain library source, stable-aarch64-apple-darwin 1.98.0, `library/std/src`)

- `fs.rs:980`: `File::try_lock` is `#[stable(since = "1.89.0")]`; `TryLockError::{Error, WouldBlock}` at `fs.rs:144`.
- `sys/fs/unix.rs:1516-1540`: `try_lock` calls `libc::flock(fd, LOCK_EX | LOCK_NB)` on Apple and Linux targets, and maps `WouldBlock` to `TryLockError::WouldBlock`.
- `sys/fs/unix.rs:1411-1423`: `fsync` uses `fcntl(fd, F_FULLFSYNC)` under `target_vendor = "apple"`, else `fsync`. `File::sync_all` (`fs.rs:779`) calls it. `datasync` also uses F_FULLFSYNC on Apple.
- The pinned Nix rustc is 1.99.0 and has no rust-src. The source cited is the rustup 1.98.0 toolchain's. The 1.99.0 behavior is not directly verified.
- flock locks belong to the open file description, so a second open in the same process conflicts. Tested in-process only. No child process was spawned.

## Tests (22, `tests/state.rs`)

round_trip_and_modes, older_schema_version_is_readable, every_corruption_reads_as_missing_with_its_reason (18 cases, each through decode and read_durable), read_failures_are_missing, invalid_names_and_sizes_are_refused, crash_before_rename_leaves_old_file_readable_and_start_sweeps, publish_failures_are_closed_errors_and_clean_up, second_lock_is_held_in_process_and_on_a_second_description, lock_refusals, hint_dir_refusals, prepare_refuses_bad_tmp, state_root_rule_matches_the_hook, error_text_is_closed, machine_id_is_created_once_and_never_overwritten, machine_id_race_has_one_winner, config_accept_reject_table, config_errors_never_echo_values, config_load_and_token_checks, and Hegel + proptest pairs for write→read identity and single-byte-mutation-never-yields-a-different-Ok-body.

## Commands

`cargo clippy -p memory --all-targets --locked -- -Dwarnings` → exit 0.
`HEGEL_TEST_CASES=2000 PROPTEST_CASES=2000 cargo test --locked -p memory --test state -- --test-threads=2` → 22 passed, exit 0.
Not run: coverage (llvm-cov), gates.sh, the full workspace tests, any real-process lock test.

## Limitations

No daemon loop or reconcile. Body schemas are opaque. Coverage was not measured. I believe most branches are exercised, but a few remain: the fsync_dir failure, the `LockUnavailable` path and the `write_synced` failure.
