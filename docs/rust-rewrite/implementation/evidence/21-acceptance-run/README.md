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

## Stack #132 and hosted CI

Pushed as stack #132 (PRs #127 to #131) and merged into `main` as `a28bfa4` on 2026-11-17
(`gh stack merge 132 --yes --merge`). Before the push, the whole range was checked for the
values in `.env` (0 hits). "Rust rewrite gates" succeeded on all six jobs of the latest attempt
on every branch. "CI" failed only in the two accepted `govulncheck` steps on every branch.
Post-merge on `a28bfa4`: "Rust rewrite gates" run 44021465203 success; "CI" run 44021465320
failed only in the two accepted steps.

Finding: on `docs/brief-30` (run 44014160296, attempt 1) the job "Rust gates
(ubuntu-24.04-arm)" was cancelled at the 30-minute limit inside "Run all Rust gates without
live access". That branch's `rust/` tree equals `main`'s, and the same job took about 14 minutes
on `main` and on the four other branches. The job log returned 404 and the job saved no
artifact, so the cause is unknown. `rust/.config/nextest.toml` sets no `slow-timeout`, so a hung
test runs until the job limit. Attempt 2 passed in 14 minutes. Not fixed; recorded as an open
finding.

## RUN: the 2-day acceptance run (started)

(Later records follow under "Day 0" and "Day 0, continued" below.)

Run id `20261117-a28bfa4`, prefix `rust-acceptance/20261117-a28bfa4/`, bucket `agent-sessions`.
Release build of `main` `a28bfa4` (sha256 prefix `421a1564d71b8e34`), installed at
`~/.local/libexec/memory-acceptance/memory`. State root `~/.local/state/memory-acceptance`
(config 0600 with archive id `acceptance-20261117-a28bfa4`, machine id `mac-acceptance`, roots
`~/.claude`, `~/.codex`, `~/.pi/agent`; token 0600 written from `.env` in a subshell, never
printed). Run files, backups, teardown steps and a list/delete tool restricted to this prefix:
`~/.local/state/memory-acceptance-run/` (README there).

Host changes (operator answers 4 to 6), all to be reverted at the end:

- launchd user agent `memory-acceptance` (`KeepAlive` on unsuccessful exit, throttle 60 s,
  stderr to `~/Library/Logs/memory-acceptance/daemon.log`); started 2026-11-17T05:30:59Z.
- Rust hooks: 8 groups appended to Claude `settings.json`, 6 to Codex `hooks.json`, each a new
  last group of its event so existing groups keep their positions (Codex trust keys). Every other
  key and group was verified unchanged against the backups; Codex `config.toml` unchanged. Codex
  skips a new hook until it is trusted in `/hooks`, so Codex hints need the operator's approval
  there. Pi: a new extension `memory-acceptance.ts` that writes a JSON payload (`event`,
  `session_id`, `session_file`, `cwd`) to the hook's stdin. The release hook was first run on
  synthetic payloads for all three agents against a throwaway state root: exit 0, one line each.
- `caffeinate -i -m -s`, a footprint sampler (`footprint -p`, `phys_footprint_peak`, every 10 s)
  and a loop that runs `memory check` every 12 h after the first `round` line.

First observations (2026-11-17, 50 minutes in):

1. **Peak memory 431 MB** (`phys_footprint_peak`), against the 128 MB target (A10.4). Steady
   state between captures 45 to 140 MB. The local files reach 98 MB, and a capture holds its whole
   range plus its encoded pieces. This is the measurement decision 2 waits for; the operator
   decides whether streaming is required before the first production write.
2. **The first reconcile is slow and silent.** After 50 minutes the prefix held 569 owners
   (`current.json`), 569 manifests and 2,614 objects. ("Of about 4,400 local owners" was written
   here at first; that counted `.jsonl` files, and most Pi and Claude files are child files of a
   shared owner. The first round landed 1,074 owners; see below.) The host's measured upload rate was 180 to
   380 KB/s. The daemon logs nothing until a round ends (no `capture` line inside a full round),
   so from outside a long first round looks like a stall; it took a stack sample, `nettop` and a
   prefix listing to tell. Each PUT opened a new TLS connection (the local port changed about
   every 10 s).

### Day 0 (recorded 2026-11-17 at 09:50Z)

Commands: `cat` of the daemon log, `checks.log`, `footprint.tsv` and `status.json` (counts and
closed codes only); the prefix list tool (counts per key family); `launchctl print`; a Python
walk of the session roots that reads names, sizes and times only.

- **First round.** `round cause=start` ended 07:07:10Z after 1 h 36 min: `landed=1074 failed=1
  offline=1`, everything else 0. The failed owner landed in a later round. Prefix at 09:42Z:
  1,075 `current.json`, 1,077 manifests, 6,087 objects.
- **First `memory check`** (07:07:40Z to 07:46:11Z, 38.5 minutes, exit 0): `owners=1075
  sources=6026 match=6026 behind=0 unsettled=0 mismatch=0 uncaptured=0 withheld=0 remote_only=0
  parked=0 unknown_kind=19015 unknown_owner=0 deferred_snapshot=3123 unreadable=0 store_error=0
  status=loaded`. The check loop runs again 12 h after each check ends (next about 19:46Z).
- **`status.json`** at 09:45Z: 66 rounds, `parked` empty, failures `offline=1` only, audit
  `matched=6029 mismatched=0 ineligible=2`.
- **The 19,015 `unknown_kind` files hold no transcript.** A census built on the daemon's own
  `walk`, `index_claude_sessions` and locator, printing counts by root, first path component,
  extension and verdict (19,070 at its time, `truncated=false`, `unknown_owner` 0): Pi `git/`
  (about 9,700 package files), Codex `.tmp/` and `plugins/`, Claude `plugins/`. The only
  `.jsonl` files among them: 40 under Pi `pi-terminology/`, Codex `session_index.jsonl`, one
  file under Codex `.tmp/`, and a Pi top-level cache file. The census source is kept in the run
  directory (`unknown-census/`). Local `.jsonl` counts behind the corrected owner estimate: Claude
  253 main transcripts and 837 subagent files; Pi 4,076 under `sessions/` (684 at depth 1, 3,289
  at depth 3, 103 at depth 4); Codex 27.
- **Peak memory 1,254 MB** (`phys_footprint_peak` at 07:07:46Z). Footprint went 46 MB, 112 MB,
  790 MB, 1,156 MB, 1,210 MB between 07:07:05Z and 07:07:46Z, the seconds after the start round
  ended, while the first `cause=dir` round audited 6,021 sources (`audit_matched=6021`); it
  stayed at 1,157 MB for some minutes, then fell (hourly maxima of the current footprint: 08h
  128 MB, 09h 239 MB). An audit read holds a source's whole `[0, size)` plus its transformed
  copy; the process was not profiled, so the split between live buffers and allocator retention
  is not known. **Decision 2 taken by the operator on 2026-11-17: streaming capture is required
  before the first production write** (DESIGN D45, sections 7.4 and 18).
- **Bursts of directory-event reconciles.** 59 `round cause=dir` rounds so far, 52 of them
  between 09:00Z and 09:42Z, at 09:40Z one about every 3 s; each walks every root (about 25,000
  files) and landed 0. Which directories fired was not looked at (that needs paths). Open
  finding: a directory-creation storm runs back-to-back full reconciles.
- **Hooks not yet exercised.** Codex trusts all six new hooks (each has a `trusted_hash` in
  Codex `config.toml` and is enabled; checked 09:42Z). `hints/` is still empty, which fits the
  file times: no Claude, Codex or Pi session file was created after 05:30:59Z, and Claude Code
  and Pi load hooks and extensions at session start; only one Claude and one Pi session that
  started earlier wrote since. Hint delivery in this run is therefore unverified until a new
  session starts.
- The sampler, `caffeinate` and the check loop have parent pid 1, so they outlive the session
  that started them.

### Day 0, continued (recorded 2026-11-17 at 12:01Z)

Commands: `launchctl print`, `ps`, the daemon log, `status.json` and the hint day file (keys,
agent, event and closed state values only), `footprint.tsv`, and a throwaway watcher built on the
same `notify` 8.2.0 that printed event kinds and paths with every component under `sessions/`,
`projects/` and `backups/` masked. The watcher was first checked against a scratch directory: a
`mkdir x.lock; rmdir x.lock` shows as `Create(Folder)` then `Remove(Folder)`.

- **Health at 12:01Z.** Agent running since the start (one run, never exited); sampler,
  `caffeinate` and check loop alive. 211 rounds (`start` 1, `timer` 15, `dir` 195); no round since
  the first has `failed`, `parked` or `audit_mismatched` above 0. `status.json`: `parked` empty,
  failures `offline=1` only (the first round), audit `matched=6031 mismatched=0 ineligible=2`.
  Current footprint 94 to 248 MB, peak unchanged at 1,254 MB. Daemon CPU time 11 min 56 s over
  6 h 30 min. Next check due about 19:46Z.
- **Pi hint delivery works.** A new Pi session started at 11:41:21Z. The hint day file got one
  record (`memory.hint/1`, agent `pi`, event `session_start`, transcript state `present`) whose
  transcript path is that session's file. The daemon captured that session's owner at 11:51:20Z
  and 11:56:38Z (`capture ... result=landed`). Claude and Codex hint delivery are still
  unverified: no Claude or Codex transcript was created since the start (0 of 253 and 0 of 27).
  The `claude` processes spawned by this Pi session run with `--setting-sources=` and load no
  hooks.
- **Cause of the `cause=dir` storms: short-lived lock directories in a non-source part of
  `~/.claude`.** In 4 minutes of watching (11:53:49Z to 11:57:49Z) every `Create(Folder)` event
  under the three roots, 12 in all, was `~/.claude/plugins/store/cc-plugin-agents-md_builtin-<hash>.json.lock`,
  a lock directory made and removed around a write of that `.json` (Claude Code, run once per turn
  of the Pi session's Claude provider). They came in groups of three at three moments; each group
  was followed about 5 s later by two `round cause=dir` lines (six rounds in that window, none
  for anything else). Each such round walks every root (about 25,000 files) and lands nothing
  unless a capture was due anyway. Code path: `watch::translate_with` turns every
  `Create(Folder)` into `Input::DirCreated`, even when the directory is gone when it is looked
  at; `Scheduler::handle` turns that into `Action::ReadDir { path }`; `Driver::apply` ignores the
  path and sets the reconcile flag (`Cause::Dir`). DESIGN 10.2 says only that directory is read
  and its sources reconciled, so this is a gap between the design and the code, not a design
  choice. Wanted (brief 31): read only the new directory, and do nothing for a directory that is
  gone or that no locator layout can hold a source in.

### Check 2 (recorded 2026-11-17 at 20:10Z)

Commands: `checks.log`, `launchctl print`, `ps`, the daemon log (counts of `round` causes and
non-zero failure keys), `status.json` and the hint day file (agent and event only), hourly maxima
of `footprint.tsv`, and the names and birth times of transcript files.

- **Second `memory check`** (19:46:11Z to 20:08:57Z, 22.8 minutes, exit 0): `owners=1076
  sources=6033 match=6033 behind=0 unsettled=0 mismatch=0 uncaptured=0 withheld=0 remote_only=0
  parked=0 unknown_kind=19073 unknown_owner=0 deferred_snapshot=3123 unreadable=0 store_error=0
  status=loaded`. Two consecutive clean checks.
- **Daemon.** Same process since the start (one launchd run, never exited); 702 rounds (`start` 1,
  `timer` 30, `dir` 671); no round after the first has `parked` or `audit_mismatched` above 0.
  `status.json` at 20:02:55Z: `parked` empty, failures `offline=9` only, audit `matched=6046
  mismatched=0 ineligible=25`. The `offline` failures came in three bursts (the first round,
  12:31Z, 15:31Z); at 15:31Z this host also could not resolve `api.github.com` for about a minute.
  Each failed owner landed within two minutes.
- **Hints.** The hint day file holds Pi records only: `session_start` 7, `turn_end` 7,
  `session_end` 6. Since the start no Claude or Codex transcript was created (0 of each), so Claude
  and Codex hint delivery is still unverified.
- **Footprint.** Peak unchanged at 1,254 MB (07:07Z). Hourly maxima of the current footprint since
  08h: 128 to 341 MB.
- The brief 31 build (streaming, `2e5f193`) is on `main`; this run keeps the `a28bfa4` binary to
  its end.
- Next checks: the loop sleeps 12 h after each check ends, so about 08:09Z and 20:35Z on
  2026-11-18 and about 09:00Z on 2026-11-19. The run can end after the first clean check that ends
  after 2026-11-19T07:46Z.

### Check 3 (recorded 2026-11-18 at 08:40Z)

Commands as for check 2.

- **Third `memory check`** (2026-11-18 08:08:57Z to 08:34:13Z, 25.3 minutes, exit 0):
  `owners=1076 sources=6039 match=6039 behind=0 unsettled=0 mismatch=0 uncaptured=0 withheld=0
  remote_only=0 parked=0 unknown_kind=19069 unknown_owner=0 deferred_snapshot=3123 unreadable=0
  store_error=0 status=loaded`. Three consecutive clean checks.
- **Daemon.** Same process since the start (one launchd run, never exited); 1,489 rounds (`start`
  1, `timer` 56, `dir` 1,432); no `WARN` or `ERROR` line since check 2, and no round since check 2
  has `parked`, `audit_mismatched` or `failed` above 0. `status.json` at 08:14:42Z: `parked` empty,
  failures still `offline=9` only (no new failure since check 2), audit `matched=6055 mismatched=0
  ineligible=49`.
- **Hints.** The 2026-11-17 day file holds one Claude `session_end` record beside the Pi records,
  so the Rust Claude hook delivered a hint (Claude hook delivery verified). The daemon log shows
  captures of Claude owners in each hour from 18h to 22h on 2026-11-17. The 2026-11-18 day file
  holds Pi records only. No Codex rollout was created since the run started, so Codex hint
  delivery is still unverified.
- **Footprint.** Peak unchanged at 1,254 MB (07:07Z on 2026-11-17). Hourly maxima of the current
  footprint since check 2: 165 to 328 MB.
- Merged into `main` since check 2, none of it installed for this run (which keeps its `a28bfa4`
  binary): briefs 32 (`memory export`, consumer gate), 33 (`memory owners`) and 34 (streamed
  `memory check`), and the Go `FuzzEditJSON` fix (PR #156).
- Next checks: about 20:35Z on 2026-11-18, then about 09:00Z on 2026-11-19; the run can end after
  the latter if both are clean.

### Check 4 (recorded 2026-11-18 at 21:26Z)

Commands as for check 2.

- **Fourth `memory check`** (2026-11-18 20:34:13Z to 20:55:16Z, 21.1 minutes, exit 0):
  `owners=1076 sources=6042 match=6042 behind=0 unsettled=0 mismatch=0 uncaptured=0 withheld=0
  remote_only=0 parked=0 unknown_kind=19069 unknown_owner=0 deferred_snapshot=3123 unreadable=0
  store_error=0 status=loaded`. Four consecutive clean checks.
- **Daemon.** Same process since the start (one launchd run, never exited); 2,023 rounds (`start`
  1, `timer` 93, `dir` 1,929); no `WARN` or `ERROR` line in the whole log. Of the 538 rounds since
  check 3, none has `failed`, `parked` or `audit_mismatched` above 0 and none has a non-zero failure
  key. `status.json` at 21:25:48Z: `parked` empty, failures still `offline=9` only (no new failure
  since check 2), audit `matched=6065 mismatched=0 ineligible=59`.
- **Hints.** The 2026-11-18 day file holds Pi records only (`session_start` 5, `turn_end` 11,
  `session_end` 6). One Claude transcript was created after check 3. No Codex rollout was created
  since the run started, so Codex hint delivery is still unverified.
- **Footprint.** Peak unchanged at 1,254 MB (07:07Z on 2026-11-17). Hourly maxima of the current
  footprint since check 3: 124 to 269 MB.
- Merged into `main` since check 3, none of it installed for this run (which keeps its `a28bfa4`
  binary): briefs 35 (prefix guard), 36 (`memory install`) and 37 (`memory export` decompresses
  recompressed Codex rollouts), and the Go `internal/syncer` timeout finding (PR #170).
- Next check: about 09:00Z on 2026-11-19; the run can end after it if it is clean.

### Check 5 and end of run (recorded 2026-11-19 at 09:45Z)

Commands as for check 2, then the teardown of the run README.

- **Fifth `memory check`** (2026-11-19 08:55:17Z to 09:19:35Z, 24.3 minutes, exit 0):
  `owners=1076 sources=6045 match=6045 behind=0 unsettled=0 mismatch=0 uncaptured=0 withheld=0
  remote_only=0 parked=0 unknown_kind=19069 unknown_owner=0 deferred_snapshot=3123 unreadable=0
  store_error=0 status=loaded`. Five consecutive clean checks; the first clean check ending after
  2026-11-19T07:46Z, so the end condition (two consecutive clean days) is met. No abort trigger
  fired during the run.
- **Daemon.** Same process for the whole run (one launchd run, pid unchanged, never exited);
  2,533 rounds (`start` 1, `timer` 126, `dir` 2,406); no `WARN` or `ERROR` line in the whole log.
  Of the 530 rounds since check 4, none has `failed`, `parked` or `audit_mismatched` above 0.
  `status.json` at 09:19:52Z: `parked` empty, `host_paused` false, failures `offline=9` only (all
  before check 2), audit `matched=6076 mismatched=0 ineligible=67`.
- **Hints.** Day files for 2026-11-17 to 2026-11-19. Pi and Claude hook delivery verified; no
  Codex rollout was created during the run, so Codex hint delivery stays unverified.
- **Footprint.** 18,487 samples from 2026-11-17T05:36:24Z to 2026-11-19T09:20:17Z. Peak
  (`phys_footprint_peak`) 1,254 MB, reached during the first full reconcile on 2026-11-17
  (largest current sample 1,210 MB at 07:07:46Z); hourly maxima of the current footprint since
  check 4: 95 to 312 MB. The run's `a28bfa4` binary predates streaming capture (brief 31), so
  this peak is not the current build's; DESIGN D45 already records it.
- **Teardown** (2026-11-19 09:20Z to 09:44Z):
  - `launchctl bootout` of `memory-acceptance` (the daemon logged `stopped` at 09:20:15Z); plist
    removed; sampler, `caffeinate` and check loop stopped; no `memory-acceptance` process left.
  - Hooks: `~/.claude/settings.json` and `~/.codex/hooks.json` each equalled the backup plus
    exactly the acceptance handlers (8 and 6; compared structurally after removing them), so the
    backups were copied back; both now byte-equal the backups and hold no `memory-acceptance`
    entry. The Pi extension `memory-acceptance.ts` was removed.
  - `~/.codex/config.toml` differed from its backup, which the README said should not happen.
    Compared by key path only: 5 trust records added and 1 changed, one per event of the six
    acceptance handlers (`hooks.state[...].trusted_hash` for `session_start`, `stop`,
    `interrupt`, `pre_compact`, `session_end`, `subagent_stop`), written when the operator
    approved the hooks on day 0; nothing else. The backup was restored together with
    `hooks.json` (the pair before the run); the teardown-time file is kept privately as
    `backup/codex-config.toml.at-teardown-20261119` (mode 0600) in the run directory.
  - Prefix `rust-acceptance/20261117-a28bfa4/`: listed 11,591 objects (`current` 1,076,
    `manifests` 2,710, `objects` 7,805); deleted 11,591 in 11,591 delete requests; listed again:
    0, `remaining_after` 0 (A2). The listing tool only accepts one run prefix, so other
    `rust-acceptance/` prefixes were not listed here.
  - Kept for evidence: `footprint.tsv`, `checks.log`, the daemon log (no paths or content).
- **Result.** Run `20261117-a28bfa4` passed: two consecutive days with zero restore mismatches,
  zero parked owners and zero uncaptured local sessions (DESIGN 17 slice 2b exit criterion), on
  the `a28bfa4` build. Merged into `main` since check 4 and not part of this run: briefs 38 and
  39 (Codex rollout identity, DESIGN D47 and D48; brief 37 reverted).
