# Evidence 53: Go code and references removed, watcher test settled (brief 58)

All dates are UTC. Status: accepted locally on 2026-11-25 at `216c3d2` (top code branch) over the
brief `e8115b4` on `main` `ac918a5`. [Brief 58](../../58-go-references-and-watcher-test.md) records
the decisions.

## Commits

| Commit | By | Change |
|---|---|---|
| `0fee8e2` | parent | Real-watcher tests wait for a live stream (`live`), and run alone in nextest |
| `582ce2a` | parent | Denylist drops the Go installer suffix `.agent-sessions-backup-<n>` and normalises memory's own `.memory-backup-<stamp>[-n]`; Go fixtures renamed |
| `3fc2d0f` | parent | P11 Go probe deleted (in git history at `e8115b4`) |
| `b437fef` | parent | `Reason::{ScrubFailed, SecretInText}` doc comments name the content scan; a collision-suffix example |
| `97f99ff` | DOCS worker | 34 Go-only docs deleted (29 bannered docs, `docs/benchmarks/`, `docs/verification/`) |
| `3a1990a`, `ddb200c` | DOCS worker | Hooks, machine identity, security, export contract, analytics projection, capture contract, docs map, README and quality gates rewritten without Go |
| `cefcc74`, `a21fd2f`, `216c3d2` | DOCS worker | Live protections only (no config scrubbing); DESIGN body without Go (sections 0, 8, 17, 18; section numbers kept, D rows untouched); AGENTS.md; analytics comments; a `rust.yml` comment |
| record | parent | Handoff links to deleted docs unlinked, three lines over 100 columns wrapped, this record |

`publications.py` changed in comments and docstrings only (parent-checked diff).

## What is left on purpose

- `agent-sessions` as the R2 bucket name (production data): AGENTS.md, DESIGN.md, `docs/install.md`.
- Dated records: D rows, `docs/rust-rewrite/**`, `docs/research/**` and the handoff ledger keep
  their historical mentions; rewriting dated evidence would falsify it.
- `rust/crates/memory-core/data/gitleaks-v8.30.1/gitleaks.toml` (upstream rule data naming Go
  vendor paths and the word `golang`).
- `ScrubFailed`/`scrub_failed`: the live closed withhold reason of the content scan.
- No `.go`, `go.mod` or `go.sum` file is tracked.

## Watcher test: diagnosis

`fs_watcher_reports_directory_moved_in_with_children` failed twice late in full local runs
(evidence 52). A temporary probe (not committed) moved a directory with one child into a fresh
watched root and recorded what the real `FsWatcher` delivered within 30 s.

| Run | Result |
|---|---|
| Quiet host, 20 runs | 20 `DirCreated`, 15 to 29 ms |
| 8 CPU burners + 4 file churners, 40 runs | 20 `DirCreated` (at most 7,064 ms); 20 with no event at all: no `Rescan`, no error, nothing else |
| Same load, sentinel first (rename only after a sentinel file's event arrived), 40 runs | 32 streams went live (sentinel seen: median 5.1 s, p90 8.8 s, max 30.0 s) and all 32 then got `DirCreated` (at most 6.8 s); 8 streams delivered nothing in 30 s |

So a move is not lost once the stream delivers; under concurrent file churn a new FSEvents stream
(notify 8.2.0, latency 0, `SinceNow`) can stay silent for 30 s or more, and an action in that time
is never reported. That is not a lost-event `Rescan`, so the scheduler's rescan cover does not see
it either.

**Value.** Not tautological: the `translate_with` tests inject `is_dir`; this is the only test that
a real backend reports a moved-in directory as a rename that becomes `DirCreated` (W4 Q3, A10.5),
which the daemon relies on to read the children. The tooth below breaks that mapping and the test
catches it. Kept and fixed.

**Fix.** `live(w, dir)` writes sentinel files until the watcher reports one (bounded at 60 s, with
its own message), in all five real-watcher tests; the nextest override runs `fs_watcher_*` with
`threads-required = "num-test-threads"`, so no neighbouring test churns files. The 10 s wait for
the event under test is unchanged. Under the synthetic loads above (6 or 8 CPU burners plus file
churners at full speed) the liveness bound alone passed 3 of 10 runs each; the gates' own load is
the competing tests, which the override removes.

**Production.** The daemon creates the watcher before the start round (5 to 60 s later), and that
round walks every root, so changes made while the stream is not yet live are captured by it; the
15-minute reconcile is the backstop.

## Teeth

| Tooth | Result |
|---|---|
| `.memory-backup-` alternative removed from the suffix pattern | caught (privacy suites) |
| Its `t`/`z` upper-cased (`basename` lowercases first, so it never matches) | caught |
| The `-<n>` collision suffix dropped | caught |
| `translate_with` never maps a rename to `DirCreated` | caught (`fs_watcher_reports_directory_moved_in_with_children`) |

## Gates

- Full gates on `216c3d2`, 500 cases: 14/14 passed, seed 1136253774, line coverage 95.58 %
  (floor 80). `08-coverage` passed with the watcher tests in it.
- Focused: the three privacy suites (19 tests) and the renamed install test.
- Python: 121 OK, 8 skipped (unchanged).
- Not run: the Linux container (hosted CI runs the gates on `ubuntu-24.04` and
  `ubuntu-24.04-arm`).

## Cleanup outside the repository

| Where | Removed |
|---|---|
| This Mac | `~/.local/share/agent-sessions` (9.4 GB); 2 Go installer backups beside the Claude and Codex configs; 7 Go-era files in `~/.local/state/memory-cutover/backup`; 5 Docker images (about 3 GB; `memory-linux-test` kept). `~/go` kept: other projects here use Go |
| dev-01 | `~/.local/share/agent-sessions` (1.6 GB); 8 Go installer backups; 3 Go-era entries in the cutover backup dir; `~/go` (625 MB) and `~/.cache/go-build` (397 MB); no `go` on its path |
| GitHub | Releases v0.1.0, v0.3.0-rc.1, v0.3.0, v0.4.0, v0.5.0 and tags v0.1.0 to v0.5.0 (commits below); Dependabot PR #266 (Go modules) closed; 166 merged remote branches and 4 unmerged Go-era ones deleted |
| Local branches | 99 merged, 57 patch-equivalent (`git cherry`), 30 Go-era branches with unique commits (tips below) |

Both daemons kept running (launchd `running`, `memory.service` `active`).

Tag commits: v0.1.0 `a5eac7451509`, v0.2.0 `acf839d128a3`, v0.3.0 and v0.3.0-rc.1
`9ff2b42dfffd`, v0.4.0 `98f46f0d1894`, v0.5.0 `60a9c6a012ad`.

Unmerged remote branches deleted: `verify-hooks` (Go tool config), `fix/go-vulns-2026-10`,
`fix/large-transcript-sync` (`internal/syncer`), `security/emudhra-ca-distrust` (its `flake.lock`
nixpkgs revision equals `main`'s).

Go-era local branch tips (each touches only deleted Go paths, docs or tools; none touches `rust/`
or `DESIGN.md`):

| Branch | Tip |
|---|---|
| `test/model-optional-only` | `806359aa8e5b` |
| `perf/rebase-cost` | `d5ff23561ec3` |
| `fix/hang-mutants` | `096dc8036280` |
| `test/model-optional-repair` | `c3987483f0e1` |
| `fix/ci-margin` | `508c3dece09e` |
| `fix/sqlite-wal-backup` | `1421de3c2a0d` |
| `fix/discovery-timeout` | `acba80f0e493` |
| `backup/pre-cm-4-consumer` | `33603cc1e106` |
| `backup/pre-cm-5-docs` | `88ea0f4d903d` |
| `retained-worktrees/20261112-193744/030-dt-top` | `88ea0f4d903d` |
| `retained-worktrees/20261112-193744/077-wal-top` | `88ea0f4d903d` |
| `backup/pre-cm-3-coalesce` | `70a464ab872a` |
| `retained-worktrees/20261112-193744/073-sl-top` | `9220f28b52c5` |
| `gap/integration` | `1150db58f2e2` |
| `gap/journey` | `08de56763459` |
| `gap/perf` | `e2f0403e5b8c` |
| `gap/coalesce` | `5b39ebdd0dc0` |
| `gap/mutation` | `747b84380398` |
| `gap/discovery` | `a031a87c2c71` |
| `gap/test-infra` | `6e6f5ae0ac7a` |
| `gap/consumer` | `1638b313be0c` |
| `wt/asset-checks` | `8be261307376` |
| `wt/disc-measure` | `59645dfd35c5` |
| `preserve/pre-simplification-20261027` | `1db7d8a37f19` |
| `fix/log-failed-sync` | `65dd2fcfb38d` |
| `fix/cold-recovery-pruned-pieces` | `84a15c47821e` |
| `fix/large-transcript-sync` | `c33c6447ed82` |
| `test/linux-native-acceptance` | `466433463dac` |
| `fix/publication-performance` | `dabd5b5d9edf` |
| `fix/archive-pieces` | `3f8e097e8325` |

Kept: 16 Rust prototype and backup branches (`proto/ds-*`, `backup/*`, one worktree branch);
dated Rust evidence cites them.
