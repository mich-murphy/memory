# Challenge C: signals and the daemon process (2026-11-07)

Reviewer C. Labels: read (URL or file:line), measured (probe on this Mac, APFS data volume, scratchpad C), inferred, recalled, guess.

## Verdict

The signal architecture (edge hints, level reconcile) is sound, but four things are unspecified or wrong: there is no single-instance lock, hint-log rotation can lose a late writer's line, the Mac FSEvents path throws away the journal that would remove the downtime and sleep gap, and the "0 idle syscalls" target is not coherent or measurable as written. The O_APPEND hypothesis did not break on APFS (measured), but the design's reason for it is wrong and Linux is untested here. The coalescer has no flush-on-end rule and no wake-time stagger.

## Findings

### Blocker

**B1. No single-instance guarantee (sections 8, 11).** `grep -n 'flock\|pid\|lock' DESIGN.md` finds nothing relevant (measured, grep). Two daemons (launchd restart overlap, manual `memory daemon`) would both rename the hint log at start, both write the index snapshot (last rename wins, losing offsets), and both capture the same sessions. Pieces are idempotent, but the 412 on `current.json` retry path ("rebuild and retry once", 7.2) was sized for "rare, two hosts", not two live local writers racing continuously. The rotation in B1/M1 is also destructive: a second daemon's start-time rename would steal the first's file.
- Probe (measured): `flock(LOCK_EX|LOCK_NB)` on a file in a second open file description returns errno 35 (EWOULDBLOCK) while the first holds it. The lock dies with the process, so no stale pidfile.
- Industry: Postgres `postmaster.pid` plus shared memory check; Vector/Prometheus take a data-dir lock (Prometheus `lock` file, recalled); Docker/systemd daemons use flock on a runtime file (recalled).
- Change: take `flock` on `<state_dir>/daemon.lock` before anything else (including rotation); exit with a distinct code if held. Make launchd `ThrottleInterval` irrelevant to correctness. Add to slice 2 test: second instance exits non-zero without touching the index.

### Major

**M1. Hint-log rotation races (section 8, item 2).** "Read to end after 10 s and deleted" assumes no writer holds the old fd >10 s. Probe (measured, rot.py): a writer that opened before rename keeps writing into the renamed inode (good); after `unlink` its further writes succeed and vanish silently ("LOST-after-unlink" not recoverable). Hooks are short-lived, so the real window is a hook that opens, is descheduled (sleep, SIGSTOP of the agent process tree, a laptop lid close in the middle of the hook), then writes. Lid close is plausible: the Mac sleeps mid-hook and resumes hours later. Also, a hook opening by path between rename and the first new-file creation must `O_CREAT`; that works (probe: new file created, contains only "new") but the daemon must watch the directory (or re-open by path), not the old inode, and the design says "its own filesystem watch" without saying which.
- Because hints are only edges and the reconcile covers loss within 15 min, a lost hint is a latency bug, not data loss. State that explicitly; it makes the design safe, and then the whole rotation machinery is questionable.
- Industry: logrotate `create` (rename, new file, writers must reopen) versus `copytruncate` (race by design, documented to lose lines) (recalled, logrotate(8)); Vector and Fluent Bit tail by inode and keep reading the rotated inode until EOF plus a grace, and checkpoint by file fingerprint (reports/08, read).
- Change: either (a) drop rotation entirely: hints are consumed within seconds, so truncate-in-place by the daemon is wrong too; instead write hints into a directory of per-day files (`hints/2026-11-07.jsonl`) and delete files older than 2 days once the offset has passed them (no rename, no race, hooks compute the name); or (b) keep rename but delete after offset reached EOF AND file age >10 min, and say a lost late line is covered by reconcile. (a) removes the risk.

**M2. Mac FSEvents journal discarded (sections 9, 10, 7.2).** Design: "Events during downtime are lost; the start reconcile covers them" and a 15 min timer. Findings:
- notify 8.2.0 (the 8.x line) hard-codes `since_when: kFSEventStreamEventIdSinceNow` at construction (fsevent.rs:299) and passes it at stream creation (fsevent.rs:440) (read, raw.githubusercontent.com/notify-rs/notify/notify-8.2.0/notify/src/fsevent.rs). There is no public API to set a starting event ID or read IDs. The 8.x line also handles `HISTORY_DONE` (line 41/108), so the flag is understood but unreachable.
- notify `main` (9.0.0-rc.7) stores `since_when` only to survive an in-process stream restart, using `FSEventsGetCurrentEventId()` (fsevent.rs:686-705, read). Still no persisted ID across processes.
- MustScanSubDirs, UserDropped, KernelDropped ARE mapped: `Event::need_rescan()` / `Flag::Rescan` with info "rescan: user dropped"/"kernel dropped" (fsevent.rs:253-258, main, read). inotify `IN_Q_OVERFLOW` maps to the same flag (inotify.rs:212-213 in 8.2.0, read). So the "overflow sets the flag" claim is confirmed for both, provided the code checks `need_rescan()`.
- Apple: events persist across reboots; pass last event ID as `sinceWhen` to replay (reports/08:80, read via that report; Apple FSEvents Programming Guide, recalled). Watchman uses clockspecs built on FSEvents event ids, and Spotlight-like tools persist them (recalled).
- Consequence: after a crash, restart or wake, replay would tell the daemon exactly which directories changed in the gap, and a stat walk (0.4 s, measured in design) is already cheap. So replay does not remove the need for the start reconcile (journal can be purged, and Linux has no equivalent); it only gives a cheaper targeted catch-up. At 0.4 s per 30k stats a 15 min timer on Mac costs little. Verdict: sinceWhen is NOT worth bypassing notify for in v1. Using `fsevent-sys` directly costs a second watcher implementation (R8).
- Real issue instead: FSEvents is journaled, so across sleep the kernel delivers coalesced events on wake only to a *running* stream; the design says nothing about wake. Add a wake-triggered reconcile (macOS `NSWorkspaceDidWakeNotification` is not available to a plain daemon; use a clock-jump check: monotonic vs wall delta on the 15 min tick, and run the reconcile when the gap exceeds the interval). Guess: not needed for correctness, needed for the lag budget after long sleep.
- Recommend: document that Mac downtime/sleep lag is bounded by the start reconcile plus 15 min, and add the clock-jump check. Say plainly in section 9 that notify 8.x cannot replay.

**M3. Recursive inotify new-directory race (section 9).** notify 8.2.0 adds a watch on `IN_CREATE` of a directory (inotify.rs `add_watch_by_event`, line 61-70, 279-290) and `add_watch` walks the new tree with `WalkDir` (line 400-412) (read). So subdirectories already present at add time get watches, but the walk adds watches without emitting Create events for files that already exist (`add_single_watch` only registers; main's `report_created` parameter at inotify.rs:681-683 was added later to address this, which implies 8.2.0 has the gap; inferred from the existence of that parameter). A session directory created and a first transcript file written inside it before the watch lands is invisible until the next signal. Claude creates `projects/<slug>/` then immediately the JSONL, so this is the normal case for a new project. Mitigation exists in the design only through the 15 min reconcile, and the hook (SessionStart/Stop) names `transcript_path`, which covers Claude/Codex main files, but not Pi unless it has hooks. Reconcile covers it within 15 min, which exceeds the 60 s quiet and the stated lag budget for new sessions on non-hook paths.
- Not tested: no Linux box here (could not verify).
- Change: on any directory Create event, enqueue a reconcile of that directory immediately (cheap readdir plus stat). Pin notify to a version with `report_created` or implement the readdir yourself. Add a test: mkdir + write immediately, expect capture within quiet+cap, not 15 min.

**M4. "Idle syscalls per minute beyond the watcher = 0" is incoherent and not measurable (sections 8 table, 14).** The same design has a 15 min reconcile (30k stats in one burst, so not idle), a 60 s index snapshot when dirty, tokio timers (timer wheel plus kqueue/epoll_wait wake with timeout: `kevent`/`epoll_wait` returns each timer expiry), the retry timers, and the daily rehash and read-back sample. Also, the FSEvents watcher thread runs a CFRunLoop which makes `mach_msg` calls, not syscalls in the unix sense.
- A literal "0 syscalls in a minute" is false for any tokio process: the reactor blocks in `epoll_wait`/`kevent` with a timeout, and wakes at each timer deadline.
- Measurability on macOS: `strace` does not exist; `dtruss` requires SIP disabled or root with entitlements, and `fs_usage -w -f filesys <pid>` gives file-system calls only (recalled, man pages). Linux: `strace -c -f -p` works. So the counter-based test in section 14 ("asserted-counter tests") can only count calls the code makes through its own wrapper, which does not count the runtime's.
- Change: define the target as "wakeups per minute and bytes read per idle minute", measured with `powermetrics`/`ps`/`proc_pidinfo` context-switch counters on macOS and `/proc/<pid>/status` voluntary_ctxt_switches on Linux; target e.g. at most N wakeups/min (timers listed). Or define it as "zero stat/read/open calls per idle minute from our own code" asserted through the I/O wrapper trait. Say which.

**M5. Coalescer ignores session end and wake herd (section 7.1).** The coalescer has no flush-now rule. A `SessionEnd` hook (sync 1.5 s/3 s budget, reports/09:8-9, hooks.md:23) is exactly the signal that no more bytes follow, and the agent process may exit and be deleted along with the file on short-lived sessions; waiting 60 s to capture a session that has ended adds loss risk for expiring sources (design's own "loss window" case 10). Claude `Stop` fires every turn (hooks.md:27), so it must stay debounced; `SessionEnd`/Codex `session_end` should flush now.
- Sleep: the windows are processing time (design says so, 3.6/146). After a 3 h sleep, all deadlines are past on wake, so every session with a pending signal fires at once, and the start-of-wake reconcile adds the rest. 8 uploads in flight host-wide bounds network, but spawn_blocking hashing of up to 103 MB whole captures across hundreds of sessions will fill the CPU (two worker threads, but blocking pool defaults to 512 threads). tokio `Instant` on macOS uses `mach_absolute_time` which does not count sleep, so quiet windows effectively extend across sleep (inferred, recalled for `CLOCK_UPTIME_RAW`); on Linux `CLOCK_MONOTONIC` also excludes suspend. So deadlines do NOT fire all at once, they pause; but the FSEvents backlog and the hint backlog do arrive at once.
- Change: flush-now on SessionEnd; a semaphore for the blocking pool (cap at 2 hash jobs) rather than relying on the 8 upload cap; add jittered start on wake. Add a test with 500 pending sessions.

### Minor

**m1. O_APPEND atomicity (hypothesis 1).** Design (section 8) says lines under 4 KiB with O_APPEND; the right claim is not PIPE_BUF. POSIX says the seek-to-end+write is atomic with O_APPEND, but only pipes have the PIPE_BUF non-interleave guarantee for the data (recalled, POSIX write(2) text). Measured on APFS (macOS 27, 16 forked writers x 2000 writes, one `write(2)` each): 0 interleaved or torn lines for 200 B, 4 KB, 65 KB and 1 MB lines (the probe's first run reported "bad" for all lines because of a checker bug, then the corrected run gave 0 bad of 32,000 lines per size; measured). So APFS did not break at any tested size. ext4 not tested here; recalled that Linux serialises appends under the inode lock for local filesystems (`i_rwsem`), so ext4/xfs are also whole-write atomic for local use, but NFS and some FUSE are not (recalled). Verdict: confirmed in practice for local fs on both OSes, but it depends on a single `write(2)`. The design never states the hook does one write. Rust `File::write_all` loops on short writes, and `BufWriter` or `writeln!` on an unbuffered File makes two writes (line, then newline). Require: build the line in a `Vec<u8>`, one `write` call, check the return value, treat short write as failure. Keep the 4 KiB cap anyway; it limits the torn-line window when disk is full or a signal interrupts.

**m2. Hook failure paths (hypothesis 8; section 9).** Not specified: hint directory missing, disk full (`ENOSPC` partial write leaves a torn line, which the reader skips only if it ends at EOF; a torn line in the middle (followed by a good line) joins with the next line and corrupts both), read-only fs, permission. Requirements: `mkdir -p` the dir with `0700`, on any error exit 0 within budget, never print to stdout (hooks.md:22), write stderr only if cheap, bump nothing. On a torn mid-file line the reader must resync on newline and drop just that fragment: say so. Add a test with a full tmpfs (Linux) and `RLIMIT_FSIZE` (both OSes).

**m3. Supervision (section 12/launchd not covered in 4-11).** Nothing says: `KeepAlive` with `ThrottleInterval` (launchd default 10 s) versus crash loops; systemd `Restart=on-failure`, `RestartSec`, `StartLimitBurst`; where `tracing` logs go (launchd `StandardErrorPath` grows unbounded unless rotated; journald on BC-01). Crash loop with a poisoned index snapshot: loads, panics, restarts forever. Require: a corrupt snapshot is ignored (rebuild, section 8 says deleting loses nothing; say a parse failure does the same), and a graceful exit code convention (exit 0 only when asked to stop, so `KeepAlive.SuccessfulExit=false` works). No installed launchd/systemd were touched (rule).

**m4. Hint log growth.** "Unbounded until rotation" with daily rotation: a Stop hook per turn across many sessions, say 5k lines/day at ~300 B is 1.5 MB, fine; but if the daemon is down for weeks (the only downtime case), reconcile after restart dwarfs any hint use. Suggest ignoring hints older than a bound at start and just reconciling. Guess, low risk.

## Claims checked

| Claim | Result |
|---|---|
| notify "may fail to receive all events" under many files [vendor] | Could not locate the exact sentence; mapped overflow flags confirmed in source (read, fsevent.rs:253-258; inotify.rs:212-213). |
| Overflow or error sets the reconcile flag | Feasible: `Flag::Rescan`/`need_rescan()` exists on both backends. Design must check `Err` results too. Confirmed in source. |
| FSEvents sinceWhen replay available | Contradicted for notify 8.2.0 (SinceNow hard-coded, fsevent.rs:299). Confirmed as an OS feature (reports/08:80). |
| O_APPEND short lines atomic | Confirmed on APFS (measured, to 1 MB). Reason in design is incomplete (m1). Linux not measured. |
| Rename rotation safe at 10 s | Not safe against a late writer or unlink (measured, rot.py). Covered by reconcile only. |
| Idle syscalls = 0 | Contradicted by its own timers (M4). |
| Single instance | Absent. flock works (measured). |

## Open questions for the operator

1. Is a hook line lost after rotation acceptable because reconcile covers it (then drop rotation, M1a)?
2. Is the Mac lag budget after wake/crash "next reconcile, up to 15 min" or tighter?
3. Should SessionEnd flush immediately even though Codex gives it 1 s?
4. What does the idle target actually protect: battery wakeups or syscall noise?

## Things I could not verify

- Linux behaviour: ext4 O_APPEND concurrency, inotify new-directory race, `IN_Q_OVERFLOW` under load (no Linux host allowed). Source reading only.
- notify 8.2.1 specifically: read 8.2.0 tag and `main`; 8.2.1 not diffed.
- Monotonic clock behaviour across sleep for tokio on macOS (recalled).
- macOS `fs_usage`/`dtruss` capability on this host (not run).
- Watchman and Apple docs: not fetched (recalled only).
