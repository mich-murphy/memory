# 17. RealFs (slice 2)

Implemented in `rust/crates/memory/src/realfs.rs`; tests in `rust/crates/memory/tests/realfs.rs`.

## Design
- `RealFs` implements `environment::FileSystem` over absolute paths. `register(path) -> u64` hands out handles. An unknown handle is `RealFsError::UnknownHandle`.
- `stat` uses `symlink_metadata` (DESIGN.md 7.1, 7.2). NotFound gives `Ok(None)`. Any other error gives `Err(Io(kind))`, so a transient failure never counts as absence. A symlink is reported as itself: its own dev, ino, link-text length and mtime. It is never followed.
- `FileId` is dev + ino + birth. Birth is `created()` in ns since the epoch (`Birth::Known`). It is `Birth::Unavailable` when the platform or filesystem lacks it, or when the time predates the epoch or overflows i64.
- `read(range)` opens, seeks to `start` and takes `end - start` bytes, so it never reads past the range. A short result means EOF or truncation, which capture detects as `Raced`. A reversed range gives `InvalidRange`.
- `Counters`: `stats`, `reads`, `bytes_read`.

## Walk (DESIGN.md 10.2, 10.3: the safety reconcile, not the driver)
- `walk(&[(dir, Roots)], &WalkLimits) -> Walk` uses std `read_dir`. It calls `locate` per regular file. Symlinks are counted and neither followed nor returned.
- Order is deterministic: names are sorted per directory, and results are sorted by relative components per root, roots in the given order.
- Bounds: `max_depth = 32`, `max_entries = 500_000`. DESIGN.md 3.4 measures only 24,127 files and 14,947 directories (0.4 s of stats) and states no limit. These are safety bounds above that measurement.
- Closed counts: `unreadable` (including a missing root), `symlinks`, `too_deep`, `invalid_names`, `entries`, `truncated`.

## Limitations
No notify watcher, no SQLite backup, no zstd decompression. The non-UTF-8 name test only creates the file on filesystems that allow it (APFS does not).

## Coverage round

Test-only (tests/realfs.rs). No file under src/ changed.

New tests and literal expectations:
- `read_errors_carry_the_io_kind`: missing file -> `Err(Io(NotFound))`; directory read -> `Err(Io(_))`; counters unchanged after failures; handle 99 -> `UnknownHandle`; range 5..2 -> `InvalidRange`.
- `unreadable_file_is_permission_denied_on_read`: mode 000 -> `Io(PermissionDenied)` (or the bytes when run as root).
- `walk_entry_bound_stops_partway_through_a_directory`: max_entries 1 over three files -> truncated, entries 1, files `["a.txt"]`; max_entries 3 -> not truncated, 3 files.
- `walk_skips_sockets_and_fifos_without_counting_them_as_files`: a Unix socket plus a file -> 1 file, entries 2, symlinks 0, invalid_names 0.

Uncovered lines (realfs.rs), before: 135, 137, 211, 224, 225, 228, 229, 247, 249. After: 135, 211, 224, 225, 228, 229, 247.

Not reached:
- 135: `seek` on a regular file opened for read does not fail; needs a seam.
- 211: `read_dir` iterator errors need fault injection.
- 224, 225: non-UTF-8 names are rejected by APFS on this Mac (reachable on Linux).
- 228, 229: `file_type()` failure after a successful `read_dir`.
- 247: `RootRelativePath::parse` fails only for over-long components or paths, which the OS rejects first.
Requested change: take a `read_dir`/`FileSystem` seam in `walk`, so these can be injected.

Statuses: see the capture-wiring section.
