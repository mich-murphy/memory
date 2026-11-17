# Evidence 21: commit pauses, the slice-2 walk, `memory check` (brief 30)

Dates are UTC; all work here is from 2026-11-17. Status: increments PAUSE, WALK and CHECK
accepted locally as `09e763d`; not yet pushed when this was written. The RUN increment (the
2-day acceptance run) has not started; no R2 request was made and nothing was installed.

[Brief 30](../../30-acceptance-run.md) sits on `3d46e18` (on top of `origin/main` `90cdbdf`). The
code stack `3d46e18..09e763d` is 8 commits on three branches. Seven checks and parent mutants ran
per increment, then on the combined tips; the full gates ran once on `09e763d`.

## Commits

`git log --reverse 3d46e18..09e763d`. "Worker" is a delegated Sonnet session (medium), "parent"
the coordinating session.

| Branch | Commit | By | Change |
|---|---|---|---|
| `docs/brief-30` | `3d46e18` | parent | brief 30, operator answers 4 to 10, D43 (no wake stagger), D44 (pause lengths) |
| `rust/commit-pauses` | `2ce506c` | worker | `memory_core::pause`: `Wait`, `ceiling`, `pause_for` (uniform 0 to ceiling) |
| `rust/commit-pauses` | `8e022bf` | worker | commit callbacks take `Wait`; daemon sleep seam, real sleep by default |
| `rust/commit-pauses` | `b5c41c4` | parent | pin that the default sleep is a real sleep |
| `rust/commit-pauses` | `f0cb744` | parent | pin that pauses use the draw (fraction of each ceiling) |
| `rust/slice2-walk` | `1a8feec` | worker | walk yields append sources only; `deferred_snapshot`; per-walk Claude session index |
| `rust/slice2-walk` | `80e2943` | parent | pin a symlinked `projects/`; drop an unreachable snapshot check in `of` |
| `rust/acceptance-check` | `263f394` | worker | `memory check`; `capture::publishable` and `assemble::policy_context` extracted |
| `rust/acceptance-check` | `09e763d` | parent | pin `unsettled` (unreadable file, file moved during read); rename the debug-log test |

## What exists now

- Commit pauses (D44): verification pauses after GET attempts 0, 1, 2 with ceilings 1 s, 2 s,
  4 s; rebase pauses 1 to 4 with 1 s, 2 s, 4 s, 4 s; `uniform(0, ceiling)` over the daemon's
  splitmix draw; the daemon sleeps for real unless a test installs `set_sleep`. A stop does not
  cut a pause.
- The walk yields only append-class sources (DESIGN 17 slice 2). Snapshot-class files are counted
  as `deferred_snapshot` and are not yielded by `enumerate`, `of` or `note`. The walk of each root
  first lists `projects/<slug>/<uuid>.jsonl` (no symlink followed) and fills the
  `ClaudeSessionIndex`, so Claude `debug/` logs get their session owner.
- `memory check` (read-only, GET only, no lock, no write under the state root): walks the
  configured roots, folds each owner's chain, restores each local source's latest epoch and
  compares it with `publishable(local)`. Results `match`, `behind`, `unsettled`, `mismatch`,
  `uncaptured`, `withheld`, `remote_only`, plus `parked` from `status.json` and the walk counts.
  Exit 0 clean, 1 a mismatch/uncaptured/parked, 2 config refusal, 3 result unknown, 64 usage.
  Output carries owner prefixes, kinds and closed reason codes only.

## Checks

Seven checks (fmt, clippy `-Dwarnings`, rustdoc `-Dwarnings`, nextest, doctests, schemas,
`cargo deny`) all 0 on `80e2943` (688 passed, 5 skipped) and on `09e763d` (704 passed, 5 skipped).
The four mutation predicates each occur exactly once on `80e2943` and `09e763d`.

Full gates on `09e763d`: `gates/` (500 cases, seed 3302469678, all 13 sub-gates 0, 704 tests under
coverage, coverage 8001/8251 = 96.97 %, floor 80).

## Parent mutants

Each applied alone, the focused tests run, the file reverted with `git checkout`.

| Increment | Mutant | Result |
|---|---|---|
| PAUSE | `Wait::Verify` passed as `Wait::Rebase` | killed (two commit-protocol tests) |
| PAUSE | `rebases - 1` | killed (two commit-protocol tests) |
| PAUSE | Rebase(4) ceiling 2 s | killed (`the_ceiling_table_is_exact`) |
| PAUSE | daemon ignores the draw | survived the worker's test; killed after `f0cb744` |
| PAUSE | default sleep a no-op | killed by the parent test `b5c41c4` |
| PAUSE | draw 0 not zero | killed (`the_extreme_draws_give_zero_and_exactly_the_ceiling`) |
| WALK | snapshot rows yielded | killed |
| WALK | session index not used | killed |
| WALK | `of` yields a snapshot | equivalent: nothing inserts a snapshot path into `known`; the check was removed in `80e2943` |
| WALK | deferred counted as `unknown_kind` | killed |
| WALK | `note` returns a snapshot owner | killed |
| WALK | index follows a symlinked `projects/` | survived; killed after `80e2943` |
| CHECK | starts-with counted as match | killed |
| CHECK | `unsettled` counted as `match` | survived; killed after `09e763d` |
| CHECK | `behind` without the prefix check | killed |
| CHECK | a path printed | killed |
| CHECK | a PUT sent | killed (11 tests) |
| CHECK | withheld as uncaptured | killed |
| CHECK | exit 0 with parked > 0 | killed |
| CHECK | `remote_only` counts local sources | killed |
| CHECK | `read_settled` ignores a move | survived; killed after `09e763d` |
| CHECK | `publishable` skips authorize | not run: first form did not compile. For every slice-2 source authorize allows the transformed bytes, and capture and check share the function, so a change shows in both alike; recorded as not testable by the check tests |

## Limits

- `unsettled` from a non-prefix-stable growing source cannot arise in slice 2: every append-class
  source the walk yields is prefix-stable. It arises from a file that moved during the read or
  could not be read.
- `memory check` fetches only owners with a local row (no LIST), so an owner whose every file is
  gone is invisible. Deleted files are still never recorded as deleted (slice 3).
- A capture still holds its whole range in memory; the run measures peak memory (largest local
  file 98 MB, target 128 MB).
- Not run: 2,000 property cases, `cargo mutants`, a Linux stress run, anything against R2.
