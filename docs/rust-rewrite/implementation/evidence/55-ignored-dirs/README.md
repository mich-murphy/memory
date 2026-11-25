# Evidence 55: the walk skips ignored directories; snapshot routing closed (brief 60)

All dates are UTC. Status: accepted locally on 2026-11-26 at `fac4846` over the brief `4948e66` on
`main` `9b97cb8`. [Brief 60](../../60-ignored-dir-pruning.md) records the measurements and
decisions.

## Commits

| Commit | By | Change |
|---|---|---|
| `0e24cbe` | RUST worker | `locator::dir_ignored(dir, roots)`: true only in `AgentDir` layouts, for whole-directory rules where `locate` is `Ignored` for every file below; table, Hegel and proptest proofs |
| `fac4846` | RUST worker | `walk_dir` does not enter a pruned directory and counts it in `Walk::ignored_dirs` (its entries are not read or counted); `walk_under` on a pruned directory or one below it reads nothing; `ignored_dirs=` in `Counts` and the `memory status` `sources` line |
| record | parent | This record, D72, handoff item 39, DESIGN 8.2 text |

## Pruned rules

- Claude: top-level `CLAUDE_IGNORED_DIRS` except `plugins`, and `plugins/<x>/...` (the two asset
  files sit directly in `plugins/`).
- Codex: top-level `CODEX_IGNORED_DIRS`, `app-server-*`, `*-locks`, `skills/.system`,
  `plugins/cache` and `plugins/.<dot>`.
- Pi: top-level `PI_IGNORED_DIRS` and the `pi-extensions-pre-` and `extensions-update-backup-`
  prefixes.
- Declined: Codex `plugins/<x>/cache` and `plugins/<x>/.<dot>`. `codex_plugin_ignored` keys on
  `plugins/<sub>`, so a `.json`, `.toml` or `.md` file under `plugins/<x>/cache` is not ignored;
  the brief's wording was wrong and the worker followed the code.
- No captured kind can sit under a pruned directory: Claude transcripts, subagents and tool
  results live under `projects/`, Codex rollouts under `sessions/` and `archived_sessions/`, Pi
  sessions and run records under `sessions/`.

## Tests

- memory-core `core_locator.rs`: `dir_ignored_names_each_pruned_rule_and_near_misses`,
  `hegel_dir_ignored_implies_all_files_ignored`, `proptest_dir_ignored_implies_all_files_ignored`,
  `proptest_dir_ignored_table_directories_are_pruned_exhaustively`.
- memory `realfs.rs`: `walk_does_not_enter_ignored_dirs_and_walk_under_reads_nothing_there`.
- Updated: `resolve.rs` `ignored_files_are_counted_not_yielded_and_never_hints` (files under
  `skills/` and `cache/` are now pruned directories), `s3_store/ops.rs`
  `status_counts_an_ignored_file_apart_from_unknown_kind`.

## Teeth

| Tooth | Result |
|---|---|
| Claude `plugins` itself pruned | caught (`core_locator`) |
| Codex `plugins/<any>` pruned | caught |
| Pi `sessions` pruned | caught |
| `walk_dir` never prunes | caught (`realfs`, `resolve`) |
| `walk_under` reads a pruned directory | caught |

## Gates

- Worker run, 500 cases, seed 1740788385: 13 of 14 passed, line coverage 95.77 %. `04-nextest`
  failed on one `fs_watcher_*` test (620 of 621 passed).
- Watcher tests alone, 5 runs each: this branch 1 of 5 passed, `main` `9b97cb8` 2 of 5 passed,
  failing on the same tests (`moved_in_with_children`, `new_directory`, `file_write`). Through
  both runs `fseventsd` sat at 100 % CPU with no test or probe of ours running, so this host's
  FSEvents delivery is saturated; the change does not touch the watcher. No wait or timeout was
  changed. The hosted gates (clean macOS and Linux runners) decide the watcher tests.
- Not run: the Linux container (hosted CI runs the gates on `ubuntu-24.04` and
  `ubuntu-24.04-arm`).

## Snapshot routing: closed without code

The watcher's `note` routes append sources only, so a Pi run record (the only snapshot kind left,
D66) is captured by the next 15-minute reconcile, not by its event (evidence 41, Limits). On
2026-11-26 this Mac had 0 run records and dev-01 59, the newest 56 days old; Pi neither creates
them often nor deletes them, so the reconcile loses nothing.
