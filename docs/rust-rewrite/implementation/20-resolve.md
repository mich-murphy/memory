# 20 - Resolver (slice 2)

Implemented in `rust/crates/memory/src/resolve.rs`; tests in `rust/crates/memory/tests/resolve.rs`.

## Design (DESIGN.md 7.2, 8.1-8.3, D20)
- `resolve_facts(path, root, located)` builds `ResolvedSourceFacts`. Every spelling in the symlink chain of the last component is recorded (via `symlink_metadata` and `read_link`), then the canonical final path. `privacy::admit` therefore denies a link to a credential-named file through the identity union.
- Identity packing: `(dev << 64) | ino` as `u128`, for the final file and for every ancestor directory of the canonical path, plus the root.
- Hop bound: `MAX_HOPS` = 40 links on the last component. A loop, overflow, dangling link, directory target or any I/O failure clears `path_resolved` (and `file_identity`). A missing root clears `root_resolved`. Nothing panics.
- Owner comes from `Located`, as a string. Profile comes from the kind (transcripts, config, text assets, other known, and Unknown for unlocated rows).
- `to_source` registers the canonical final path with `RealFs` (the seam never follows links). It falls back to the logical path when unresolved, so capture blocks the source on its facts. A handle is stable per final path.
- `WalkSources` implements `daemon::Sources`. It yields only located sources. Unknown-kind and unknown-owner rows have no SourceId, kind or owner, so capture cannot account for them; they are counted in `Counts`. Unresolved sources are yielded, so capture reports Blocked.
- `SharedFs` shares one `RealFs` between the sources and the capture seam. A busy `RefCell` yields an error or an empty enumeration with `busy`, not a panic.

## Tests
Ten tests in `tests/resolve.rs`: plain file, ancestors, symlink chain, credential link, loop, dangling and hop overflow, missing root and directory target, file removed after walk, stable handles and counts, busy shared fs, and an end-to-end capture and commit with a byte search for the credential.

## Limitations
Only the last component is hop-bounded; directory links are resolved by `canonicalize`. The walk skips symlinks, so links are resolved only for files `walk` returns; symlinked files are not enumerated by `walk`.

## Status and what remains
Clippy (`-p memory --all-targets -Dwarnings`) exit 0 on the last retry; an earlier failure was in the other worker's `watch.rs`. `cargo test --test resolve` exit 0, 10 passed. Coverage run (`cargo llvm-cov -p memory --ignore-run-fail`, 2,000 cases) exit 0: `resolve.rs` lines 181/193 = 93.8%, which is BELOW the 98% floor.

Uncovered lines in `src/resolve.rs` (by line number at that measurement): 55, 59 (`chain`: `read_link` failure, `parent()` None fallback), 75-78 (`owner_string`: `Owner::Shared` arm), 93, 95, 97 (`profile_of`: Config, TextAsset arms), 126, 131 (`resolve`: directory-metadata failure and canonicalize-error paths), 304 (`WalkSources::enumerate`: UnknownOwner count).

Next action: add tests for a shared-owner source (e.g. `projects/-p/memory/x.md` with a TextAsset profile), a Config-kind file (e.g. `settings.json`), an UnknownOwner row, and a path whose parent directory is unreadable (chmod 0). Then rerun the coverage command and confirm at least 98%.

## Parent completion (2026-11-09)

The worker released its paths with `resolve.rs` at 93.8%. The parent added two tests to
`tests/resolve.rs`: `shared_owner_and_profiles_are_literal` (plan gives owner `a/m/Claude`
and `TextAsset`; `settings.json` gives `Config`; `UnknownKind` gives no owner and `Unknown`)
and `unknown_owner_rows_are_counted_not_yielded` (an unattributed todos UUID gives
`(yielded, unknown_owner, unknown_kind) = (1, 1, 0)`). Clippy 0, 12/12 pass. In gate 8
`resolve.rs` is 188/193 (97.41%). The workspace total is above the floor. The remaining
lines are `read_link`/canonicalize/ancestor-metadata I/O failures and the `parent()` fallback.

Open: `realfs::walk` skips symlinks, so `WalkSources` never yields a symlinked file. The
symlink and credential-union handling is tested only through `resolve_facts`/`to_source`
called directly. Before wiring this into the daemon, decide whether walk should report
symlinks for resolution (DESIGN 7.2 says resolve every link).

## Resolved 2026-11-09

Symlinks stay skipped by `walk` (operator decision, [amendment A4](../design-amendments-20261109.md)). The resolver's link handling stays as defence for paths reached directly.
