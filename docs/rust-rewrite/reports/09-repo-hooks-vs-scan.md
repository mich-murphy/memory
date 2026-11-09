# Review 09: repo evidence on hooks versus periodic scanning

Scope: read-only. Repo /Users/mm/dev/memory at main 57d8400 plus uncommitted doc edits. Labels: **measured** = I read the code or doc text at the cited line. **doc says** = a claim in a doc that I did not independently verify. **inferred** = my reasoning.

## Section 1. Candidate hooks as installed

**Events registered (measured).** internal/capturehooks/json.go:18-23 is the whole list.
- Claude (settings.json): SessionStart, Stop, StopFailure, SubagentStop, PreCompact, TaskCompleted, TeammateIdle (all async, 30 s) and SessionEnd (sync, 5 s).
- Codex (hooks.json): SessionStart, Stop, SubagentStop, PreCompact (async 30 s), Interrupt (async 3 s), SessionEnd (sync 3 s).
- Pi (extension file agent-sessions-capture-v1.ts): session_start, agent_settled, session_shutdown (internal/capturehooks/pi.go:233-237). Pi has no timeout setting. The spawn is killed at 2500 ms (pi.go:188-192, 219-223).
- Target files: Claude settings.json, Codex hooks.json, Pi extensions/<marker>.ts (plan.go:167-178).

**Command string (measured).** plan.go:19 and 131-144. Command is `: agent-sessions-capture-v1; exec <exe> capture-v1 hook --config <cfg> --agent <agent>`, shell-quoted. The `: marker;` prefix is the ownership tag. Executable must be non-group/world-writable (plan.go:197-199). Config must be mode 0600 (plan.go:191).

**Time budget (measured).** runNativeCaptureHook wraps everything in a 2 s context (internal/cli/capture_runtime_hook.go:26). Stdin read is bounded to 64 KiB and 2 s (lines 79, 104). Outcomes are exit codes only. Hook prints nothing.

**Stdin fields read (measured).**
- Paths, first non-empty of: transcript_path, agent_transcript_path, session_path, session_file, rollout_path (capture_runtime_hook.go:119). No path means a pathless "discovery hint" (lines 36-41, 131-133).
- SessionStart-class events only (capture_runtime_launch.go:56-60): hook_event_name or event, source, cwd, session_id, model, provider, agent_version, permission_mode. Each is regexp-allowlisted (launch.go:36-53, 64-76).
- Not from stdin but read by the hook process: its own ancestor argv via hook.InspectNativeArgs and a sanitized os.Environ() (launch.go:85, 89). Pi passes argv in the payload instead (launch.go:78-83).

**Hint file format (measured).** sessioncapture.WriteHint (internal/sessioncapture/runtime_inbox.go:29-80). The file is the JSON of a Request (identity, source id, path, observed-at ns), at most 64 KiB (line 22). It is named sha256(BaseKey + 0x00 + SourceID).json, so repeated events for one source overwrite one slot (lines 54-56, 76). It is written with a temp file, fsync, rename, then directory fsync (62-79) under a short inbox flock (45-49). It never opens the SQLite queue or queue.lock (comment lines 26-28). Cap is 1024 files. When full, a hint for a new source returns ErrQuota; a hint for an existing slot still overwrites (57-61). Pathless discovery hints go through a separate WriteDiscoveryHint (hook.go:40) that I did not read.

**What the hook deliberately does not do (measured).**
- Does not read or copy transcript bytes. Success means "durable pathname observation only, not captured bytes" (runtime_inbox.go:27-28; capturehooks/README.md:33-35).
- Does not open the queue/DB, does not start a detached sync, no shell in Pi (pi.go:172-173).
- Does not guarantee recovery. Code comment at capture_runtime_hook.go:20-24 says "Periodic discovery can recover missed hints while eligible sources remain; it cannot recover vanished bytes or missed native launch context." The same sentence is in pi.go:196-197.
- Applies classifier plus policy (privacy) before writing the hint (capture_runtime_hook.go:58-67). So a hint is never written for an excluded source.
- Does not change Codex trust (plan.go:109-111). Codex needs the operator to approve in /hooks.

**Registered but unverified (doc says).** .agents/skills/verify-agent-sessions/features/hooks.md:36 says all specialized events (Interrupt, TaskCompleted, TeammateIdle, StopFailure, PreCompact, Pi /new /fork /reload) are "registration only". Live tests only exercise start, turn, end (features/README.md:31).

**Events that exist but are not registered (measured / inferred).** I found no comment or doc giving a reason. Codex's schema list (internal/install/codexschema.go:35) names PreToolUse, PermissionRequest, PostToolUse, PostCompact, UserPromptSubmit, SubagentStart. None are registered by either the legacy installer (install.go:~35-40) or the candidate. Claude events not registered include PostToolUse, UserPromptSubmit, FileChanged, CwdChanged, SubagentStart. The repo never discusses a per-tool or per-prompt hook design. **Inferred** reason: per-event process spawn cost (each is a Go process start plus a 2 s budget plus a fsynced inbox write). Treat as a guess. An incident shows UserPromptSubmit does fire on Codex (handoff.md:1731, 1850), but that was another tool's hook and a diagnostic probe.

## Section 2. What hooks cannot see

**Mechanism (measured).** A hook can only name paths found in five payload keys (internal/cli/capture_runtime_hook.go:119). The candidate hook classifies exactly those paths with capturesource.Classify and writes one hint per path (hook.go:53-71). Classify "never reads bodies or scans siblings" (internal/capturesource/classify.go:24-26). So sibling files are not fanned out by a hook. The legacy fan-out, adapter.Related (internal/adapters/claude.go:178-211), is called only from the legacy syncer (internal/syncer/syncer.go:395). I found no caller in the candidate path. Everything else reaches the candidate only through the discovery walk (internal/capturesource/discover.go, README.md:37-52).

Claude Code also sends a hook `transcript_path` for the main transcript. The Codex payload `transcript_path` is null for sessions Codex does not persist (docs/hooks.md, Codex section, doc says).

| Source kind | Classifier / adapter (measured) | Any hook event announces it? | Found by |
|---|---|---|---|
| Claude main transcript projects/<slug>/<uuid>.jsonl | claude.go:124-126 RoleMain | Yes: transcript_path on Stop, SubagentStop, PreCompact, SessionStart, SessionEnd | hook or sweep |
| Claude subagent file projects/<slug>/<uuid>/subagents/... | claude.go:29, 133-139 RoleSubagent | Partly. SubagentStop carries agent_transcript_path (docs/hooks.md:~73, doc says). A subagent that dies without SubagentStop is not announced. | hook or sweep |
| Claude tool-results projects/<slug>/<uuid>/tool-results/... | claude.go:29 RoleToolResult | No payload key names it | sweep only (inferred from key list) |
| Claude per-session artifacts file-history, session-env, debug, todos, tasks | claude.go:30-33, 112-114 (classify.go); ownership.go:19-34 | No | sweep only. Needs an ownership lookup (LookupOwner) from the session; without it the code is "ownership_integration_required" (ownership.go:~57) |
| Claude teams/, plans/, paste-cache, usage-data, agent-memory, skills, settings, CLAUDE.md | claude.go:34-41 | TaskCompleted and TeammateIdle are registered, but the candidate hook reads only the five path keys. Legacy used them to sync tasks/teams (docs/hooks.md:73-75). In the candidate no mapping from those events to teams/tasks paths exists (inferred from hook.go:119). | sweep only |
| Claude ~/.claude.json | primary.go:15-19, 78-96, scrubbed-config transform | No | sweep only, once per cursor sweep (primary.go:78-86) |
| Claude history.jsonl | claude.go:35 RoleHistory | No | sweep only |
| Codex rollout sessions/YYYY/MM/DD/rollout-*.jsonl and archived_sessions | codex.go:13, 79 RoleMain | Yes via transcript_path or rollout_path | hook or sweep |
| Codex recompressed .jsonl.zst | derived.go:137-152 transform zstd | No (recompression happens after 7 days, doc says docs/hooks.md Codex section) | sweep only |
| Codex SQLite state_*.sqlite, memories_*, goals_*, thread_history_*, queue_* | derived.go:117-128 sqlite-backup transform | No | sweep only. docs/hooks.md:37 says databases were copied "at most once an hour" in legacy |
| Codex history.jsonl, session_index.jsonl, config.toml | codex.go:15 | No | sweep only |
| Pi session .jsonl | pi.go:143-153 | Yes via session_file in agent_settled and session_shutdown (pi.go:233-237) | hook or sweep |
| Pi settings, models, extensions, snapshots, git package revisions | pi.go:15-19; pi_snapshot.go | No | sweep only |
| Launch context (argv, env, cwd, model, permission mode) | capture_runtime_launch.go:55-100 | Only SessionStart-class events. The hook reads its ancestor process argv and its own env | **hook only**. The sweep cannot recover it. Comment at capture_runtime_hook.go:22-24 and pi.go:196-197 says so |
| Learned custom roots (CLAUDE_CONFIG_DIR, CODEX_HOME, PI session dirs) | docs/hooks.md "Config dirs set only in your shell" (doc says) | Hook environment reveals them | hook teaches roots, sweep covers them (legacy). Candidate: roots are explicit config; README says learned roots come from "a trusted policy/root recovery layer" (capturesource/README.md:6-7) |

**Takeaway (inferred from the table).** Of 14 source kinds, the hook names only main transcripts, the subagent transcript on SubagentStop, and launch context. Every other kind is discovered only by walking the roots. Conversely launch context is the one input that only a hook can supply.

**Pathless events.** When a payload has no path key (for example Codex with no persisted rollout, or a registered event like Interrupt that lacks one), the hook writes a metadata-only "discovery hint" per agent that wakes the walker (hook.go:36-41; runtime_wake.go:49-98). That is the repo's own admission that a hook cannot always name the work, and the fallback is a walk.

## Section 3. Operational evidence, hook-driven versus sweep-driven

All figures below are "doc says" from docs/session-capture-handoff.md (call it HO) and its archives unless marked measured. I ran nothing live.

**Direct answers to the quantities asked.**
- Sources found only by sweep: **not recorded anywhere I could find**. No doc or test counts hint-originated versus walk-originated sources. Searches for hookless, hint-driven, "only by sweep" found only the items below. Treat the ratio as unmeasured.
- Hints per session or turn: **not measured**. HO:1088-1095 and the D report state amplification as "4.5-10x (hook per turn) up to 228-1294x (every append)" (HO:505-506, doc says). That is capture amplification, not hint count. Native-evidence rows rose 267 to 270 across three one-turn probes (HO:1846, doc says), about 1 per probe.
- Hints dropped by the 1024 inbox cap: **zero evidence of real drops in production**. The only production backpressure number is "30 inbox-backpressure hints" on the Mac before the inventory cap was raised from 4096 to 32768 (archive-20261027:2984, doc says). That count is `backpressure_hints` from drainHints, which increments on ErrQuota from the SQLite queue's inventory cap (runtime_inbox.go:271-274), not on the 1024 file cap. After the raise: "no discovery error/backpressure" (archive-20261027:2985, 3363). The 1024 cap is exercised only by tests: 16 goroutines x 1000 hints (production-gap-register-20261102.md:309, doc says; test name c-c2-job2 "Inbox capped at 1024 with typed refusals").
- Cap behavior (measured, runtime_inbox.go:57-61): a new source is refused when 1024 files are queued. The hook then exits non-zero, which the agent ignores. The only backstop is the walk. Note drainHints reads at most 1025 entries and sets `inbox_truncated` (runtime_inbox.go:249, 278).
- Sweep duration, Mac: the poll runs every 10 s (HO:1707, 1732, doc says). A cold walk over a roughly 30k-source inventory took 27.9 s then 13.4 s after the policy-snapshot fix, max call 2.1 s (HO:1470-1481, "DT accepted" entry, doc says, private harness). A settled plain sweep at N=4000 is about 95 ms discovery plus 270 ms hint (production-gap-register-20261102.md:310, doc says). Profile of the live Mac coordinator: discovery about 60 percent of 0.277 cores, "per-entry syscalls from re-walking unchanged trees every 10 s poll" (HO:1703-1707). The authors call change detection or cadence a structural follow-up (HO:97).
- Sweep duration, BC-01: "A BC-01 discovery generation takes about 5 minutes (the Pi root needs many 1,365-entry slices; Codex/Claude walks finish early and wait)" (HO:1854-1856). HO:84 repeats it as "the wait for a hookless session".
- How long a hookless session waits: one observed case on BC-01. Rollout written 18:00:11Z, observed 18:03:32Z, captured 18:04:03Z, published (HO:1853-1855). That is 3 min 21 s to observe, 31 s more to capture (the 30 s quiet period, cadence.go:21). Doc says up to one generation, about 5 minutes. Total for that probe about 4 min.

**BC-01 and Mac Codex stale-trust episode (doc says).**
- Mac: `config.toml` trust predates the last `hooks.json` rewrite (07:41 vs 08:38 Oct 30). A normal `codex exec` ran only another tool's UserPromptSubmit hook. "none of the 6 agent-sessions hooks ran and the hints inbox was untouched (the session was still captured by the 10 s discovery poll)" (HO:1722-1733). A `--dangerously-bypass-hook-trust` run fired SessionStart and Stop, wrote both inboxes, and was drained (HO:1733-1737). After operator re-approval a normal probe worked (HO:1742-1745).
- BC-01 (Nov 5 18:00Z): Codex 0.163.0 trust again stale. A normal `codex exec` ran no hook and the inboxes did not change. Capture still happened, by discovery only (HO:1846-1856). Still open as of the status table (HO:29, 74-79).
- Cause (doc says, HO:1736-1739): the persisted trust hash covers the command and cannot be recomputed outside Codex. Code agrees. plan.go:109-111 says trust "was not changed", and README.md:26 of capturehooks keeps trust untouched. Any change to the hook command invalidates trust until a human approves in `/hooks`.
- Reading (inferred): on a hook-only design this episode would have meant silent total loss of Codex capture on BC-01 for as long as the trust stayed stale. The system recorded 5 Codex captures in the prior 24 h on the Mac while the hooks were unapproved (HO:1722-1723), all via discovery.

**Other operational evidence about hooks versus walking (doc says).**
- Source absence (HO:1752-1758, 1857-1860): with the rollout moved aside "With no trigger nothing changed; one explicit `capture-v1 hint` for the missing path moved the source to `absent`". So an absent file is noticed by a path hint, not by the walk. Inferred: a deletion that no hook names is invisible to the walk as well, because the walk only enumerates present entries (capturesource/README.md:49-50 "Directory changes during traversal require a later fresh sweep, not an absence inference").
- Outage retry without another hook (HO:1759-1771, 1862-1868): retries after R2 outage ran from the spool with no new hook on both hosts. That is the persistent-state case, not discovery.
- Privacy: a Codex `AGENT_SESSIONS_DISABLE=1` probe made hooks skip hints (capture_runtime_hook.go:28-30, disabled variant) while the file still existed. The walk would have captured it if not for the exclusion record. So privacy exclusion has to hold in both paths, and for a shared store like thread_history the fail-closed result was 7,909 `policy_denied` issues on the Mac (HO:1788-1795).
- Hook cost is not measured separately. Native-evidence inbox was 10 percent of coordinator CPU in the profile (HO:1704-1706).

## Section 4. The earlier hook-driven design and why it was reversed

**What the earlier design actually was (measured, gh pr view 9, merged 2026-10-13).** PR 9 "Capture only on session triggers; drop the periodic sweep timer". Before it, `install` set up a systemd, launchd or cron job running a full sync every 10 minutes. After it, "nothing runs on a schedule". Every SessionStart and SessionEnd queued one sweep of every agent's files. So the earlier design was **hooks plus a sweep triggered by session boundaries**, never hooks alone. Legacy hook.go confirms:
- hook spawns a detached `sync --delay 3` in a new session, `Setsid: true` (internal/hook/hook.go:262-290, delay constant at line 34).
- debounce marker per transcript, stale after 120 s (hook.go:33, 99-126).
- sweep marker key "sweep" (hook.go:38, 270-277).
- lock timeouts: hook sync 30 s, sweep 60 s, manual 10 min (internal/cli/commands.go:28-34, 69-78). A comment says the sync "gives up soon when a backfill holds the lock ... the next hook or sweep catches up".
- partial-line window 60 s and in-session window 900 s (internal/syncer/syncer.go:36-41).

**Reasons given for adding a coordinator, in the authors' words, and which kind each is.**

| # | Quote and location | About hooks themselves, or about v3 archive recovery? |
|---|---|---|
| 1 | hook.go:99-101: the marker "is not a durable capture queue or acknowledgement; retries still depend on later hooks/sweeps, not an autonomous delivery worker." | Hooks themselves. A retry needs another event. |
| 2 | docs/hooks.md "No session, no sweep" (read already): what only a sweep finds "stay[s] on disk until" the next session starts or ends. | Hooks themselves. |
| 3 | Contract section 3, session-capture-contract.md:84: "Hooks enqueue lightweight discovery hints. Work retries without requiring another agent event." Same text in review:93 and acceptance test list review:199 ("Restart with prior errors intact, no new hooks, and successful autonomous retry"). | Hooks themselves, stated as a requirement. |
| 4 | Progress-history:333-334: "autonomous periodic discovery/retry ... Do not assume detached per-hook syncs or stale marker expiry provide scheduling guarantees." | Hooks themselves. No incident is cited for it. |
| 5 | Review:34: "debounce markers and detached processes, rather than a general durable retry scheduler." | Hooks themselves. |
| 6 | Review:63 and progress-history:327-328: "hooks must not compete for a lock held throughout remote recovery" and "hooks must not queue behind a historical network scan". | Both. The hook's sync waits on the single global lock, and that lock was held by the v3 recovery. The lock coupling is a v3 recovery design defect. |
| 7 | session-capture-progress-history.md:154-158 "Operational diagnosis": installed v0.5.0 had "a dirty checkpoint, last recorded completion October 21 ... with an R2 HEAD 403, repeated slow recovery holding the global lock, and hundreds of pending launches ... 819 pruned historical-only pieces ... Those findings motivated replacement." | Mostly v3 recovery design (dirty run-wide checkpoint, global lock, pruned pieces). "Hundreds of pending launches" is hook-related only because a launch recorded by SessionStart is consumed by a later sync that could not get the lock. |
| 8 | PR 14 (merged 2026-10-14) body: "A session-start launch is no longer lost when its sync can't get the lock, for example during a backfill." | Hook design weakness with a fix at the time. Launch context depends on a later sync running. |
| 9 | Review:16-36 and storage-layout.md:128-131: the failure is the pruned-piece recovery and the run-wide clean/dirty checkpoint (`internal/syncer/checkpoint.go`, `recovery.go`, `syncer.go`). | v3 archive recovery design. Not about hooks. |
| 10 | Review:50 and contract.md:44-45: "Polling cannot preserve changes that disappear before capture". | General, applies to both designs. |

**Plain verdict (inferred from the table).** The strongest incident (item 7, pruned pieces plus a dirty global checkpoint plus a held lock) was a v3 archive recovery failure. It is not evidence that hook-driven discovery is unreliable. The hook-specific reasons (items 1-5, 8) are design-level statements that a hook-triggered retry cannot happen without another event. They are not backed by a measured loss. I found no document recording a session or source that was permanently lost because a hook failed to fire. The one measured hook-failure episode in the repo is Codex trust staleness (Section 3), and there the walk saved the data.

**Other hook-side reasons visible in the hook docs (doc says).** Claude Stop does not fire on interrupt. StopFailure covers API errors. Codex SessionEnd is synchronous with a 1 s default, 3 s max. `claude -p` kills async hooks at teardown. Transcript can lag the in-memory conversation when a hook fires. Codex trust hash invalidates on a command change. All in docs/hooks.md as read. These are the reasons "hooks only" is a lossy trigger.

**Holistic and adversarial reviews (measured).** holistic-review-20261029.md:63 sums up the settled position: "Hooks are hints, not durable capture." architecture-review-20261030.md:21: "Hooks correctly carry hints, not ownership of bytes." Neither argues the hint path should go away. Adversarial R1 (adversarial-review-20261029.md:51-52) records that when the credentialed coordinator cannot start, "Hooks can still accept hints, but a pathname hint is not captured evidence", which is the hook-without-a-queue failure mode in a sentence.

## Section 5. Why the spool and SQLite exist, and whether R2 state could replace them

**Authors' stated basis (measured text).**
- Contract section 2, session-capture-contract.md:68-72: "The durable pending store is authoritative for accepted unpublished bytes. It cannot also be an unexplained disposable cache. If SQLite is an index over spool records, prove index rebuilding; if it is authoritative, document and test durability settings."
- Contract section 1, lines 44-46: "Queuing a pathname is not capturing bytes. Polling cannot preserve changes that disappear before capture, and staging cannot guarantee an atomic source snapshot without producer cooperation."
- Phase 3 (session-capture-phase3.md:26-28): "SQLite WAL with synchronous=FULL remains authoritative for accepted work. Private encoded payloads are synced before acceptance."
- Review:67: SQLite and filesystem payloads "do not form one automatic transaction", so a crash-reconciliation protocol is needed.
- Review:107 and contract:84: "Accepted publications are resumed after restart without another hook or their original source" (phase3:48-49, the "or their original source" clause is the authors' spool argument).
- Review:63 (item 8): short SQLite transactions for claim/update, no network under them.

**Separating the six reasons, with evidence the case occurs.**

(a) Sources change or vanish before upload.
- What the spool does here: it does NOT help if the source vanishes before capture. It helps only after local acceptance. Contract 44-45 concedes that.
- Evidence the loss exists (doc says): Claude deletes transcripts, subagents, tool results, plans, file history after 30 days unless `cleanupPeriodDays` is raised (docs/hooks.md Claude section; the installer raises it to 3650). Claude deletes per-session file-history, todos, tasks and team mailboxes when a session or team ends (adapters/claude.go:178-179 comment; hooks.md:73-75). Codex recompresses rollouts untouched for 7 days to .zst (hooks.md Codex section) and the candidate maps that to the same source key (derived.go:148-151). Codex in paginated mode truncates command output before writing (hooks.md).
- Measured rewrite rates: the only repository quantification is a comment. model_history_test.go:13-19: shapes "measured on real Claude/Codex/Pi use (see the delivery workload report)", "Rewrite probabilities are scaled up from the measured 0.01-5% per session". The report (reports/d-workload.md) is private and outside the repo, so I could not check it. Inferred: for transcripts rewrites are rare. For artifacts they are common: Mac `~/.claude.json` 206 epochs in 8 days, Claude model-catalog cache 20 epochs, `thread_history_1.sqlite` gaining about 587 MiB per Codex run, 1,064 MiB of sealed-epoch payload across 65 sessions on the Mac (HO:88-91, 1802-1808, doc says).
- No document counts a transcript that was lost because it vanished before capture. D's replay is "append-only, one source per session" by its own stated limitation (HO:1096-1097).
- Could R2 state serve? No for the vanishing case. R2 can only hold what was already uploaded.

(b) Offline operation.
- Evidence it occurs: the hooks doc says an offline failed upload retries on the next hook/sweep and "truncation, rewrite or source expiry can lose uncaptured content" (hooks.md Offline machines). Mac clamshell sleep on battery is documented as real (archive-20261029:85, 557, 858-859) and BC-01 coordinator stayed off for about a week (HO:1828-1829). The documented outage drills held captured jobs through 9-10 failed attempts and published later with no new hook, on both hosts (HO:1759-1771, 1862-1868).
- Caveat (inferred): those drills would also pass if the delivery step re-read the still-present source. They do not isolate the spool as the cause of recovery. A synthetic test exists for the "original source gone" case (phase3.md:48-49), but I did not read it.
- Could R2 state serve? Not while offline. The remote cannot be written.

(c) Crash safety.
- Evidence: crash_process_test.go, fault hooks (`q.fault("payload-before-sync")` capture.go:~140, `runtime-hint-commit` runtime_store.go:99) and the model history test cover crash points (contract.md:~155 required scenarios). Live evidence of crash recovery is thin: "0 restarts by systemd" (HO:27, 1840-1841) and an intentional stop/start exercise (HO:1840). I found no recorded real crash.
- Could R2 state serve? Partly. Idempotent content-addressed keys (publication.go:244, key = objects/<encoded sha256>.zst) plus guarded current.json (CAS) make re-upload safe. A crash mid-stream just repeats the PUT. What is lost without local state is the "which sources were already published at what size/version" cursor, which R2 can answer by reading current.json, as the rebase code already does (phase3.md:66-68, "A rebuilt or lagging spool converges instead of conflicting forever").

(d) Privacy recheck before delivery.
- Contract.md:107 "Recheck exclusions before delivering pending work, not only when reading sources." Review:184 "An archive reset must not silently re-enable excluded sessions; recheck policy before delivery of already queued work."
- Evidence it occurs: the Mac Codex exclusion probe (HO:1745-1752, 1786-1800). A rebase could republish a remote-only excluded source until `recheckScopes` was added (HO:683-686, 1306-1308, GAP-06 review finding). That is a real defect found in review, not a field incident.
- Could R2 state serve? Not directly. The policy lives locally (`exclusion` registry, `capturepolicy`). A streaming design must apply the same check before each PUT. The spool matters only when bytes sit locally between capture and delivery. With no spool the check moves to "just before streaming", which is the same code path. Inferred: privacy recheck does not by itself require a spool. It requires the policy store, which is local state.

(e) Operator inspectability (status, quarantine, replay).
- Evidence it is used: status aggregates drive every dated entry in the handoff (jobs published/superseded/pending, quarantined, retry classes, destination pause) and the 14-15 quarantined delivery jobs the Mac accumulated (archive-20261029:322, 405) were diagnosed and "can be replayed". Commands: `capture-v1 work-status`, `quarantine`, `replay`, `resume`, `optional-replay` (internal/cli/capture_runtime.go:25-36, phase3.md:221).
- Could R2 state serve? No for pending, retry, quarantine and attempt counts, which describe unpublished work. R2 gives only published sessions (reader/current-heads).

(f) Fairness and backpressure.
- Evidence it occurs: a Nov 5 BC-01 catch-up after about 7 days off found 17,903 new sources and 6,148 changed versions, pending jobs rose 10,433 to 19,596, drained over about 11 h with 1.9-2 cores used, and 36 sessions formed the slow tail (HO:1813-1838). Earlier defect: every sweep reset captured sources to pending and re-read and re-hashed them, 295 percent CPU (archive-20261029:318-326). The Mac hit the 4096 inventory cap with 30 inbox-backpressure hints (archive-20261027:2984). A 215 MB encoded object needs about 235 s at the 1 MiB/s budget (phase3.md:414-415).
- Could R2 state serve? No. Fairness and per-session ordering, retries with jitter, and a bounded number of concurrent big uploads are scheduling concerns in the sender. Local state is what makes them restartable.

**Design fact that constrains streaming (measured).** The object key is `objects/<EncodedSHA256>.zst` (internal/publication/publication.go:244). The encoded hash and size are known only after the whole source is compressed. Capture compresses into a temp payload file while hashing raw bytes, then does a second full read of the source plus stat checks to prove the source did not change (internal/sessioncapture/capture.go:111-135, 426-460). The R2 backend uploads that file with a known ContentLength (internal/publicationr2/backend.go:255). So under the current key scheme a straight stream to a final key is not possible. A rewrite would need a staging key plus server-side copy, a different key scheme (for example keyed by raw hash or by source epoch and offset), or an in-memory buffer (up to 215 MB observed max, phase3.md:414). That is inferred from the code, not something the authors discuss as an alternative.

## Section 6. Source change characteristics

**Per source kind (code measured, behavior doc says).**

| Source | Change pattern | Evidence |
|---|---|---|
| Claude, Codex, Pi main transcripts (jsonl) | Append-only in the normal case. Rewrites rare. | The model test scales rewrite probability up "from the measured 0.01-5% per session" (model_history_test.go:18-19). Contract treats proven append as advancing an epoch, "anything else" as a new epoch (contract.md:34-38, model_history_test.go:5-9). Claude has set-aside variants `.orphaned-*.jsonl` and `.jsonl.superseded-*` (adapters/claude.go:25, 127-129), which means Claude itself renames transcripts away. |
| Claude subagent and tool-result files | Mostly write-once per subagent or tool call. Count per session is large: max 379 sources per session in D (HO:505-506). | adapters/claude.go:29, 130-139 |
| Claude per-session artifacts (file-history, todos, tasks, session-env, debug) and teams | Appear during the session, **deleted by Claude when the session or team ends** (claude.go:178-179 comment, hooks.md:73-75). | Sweep-found only in the candidate. In legacy, hooks synced them while they existed. |
| Claude `~/.claude.json` | Rewritten in place often. 206 epochs in 8 days on the Mac (HO:91, 1804). Scrubbed on read (primary.go:74-75). | doc says |
| Codex rollout | Append. Recompressed to .jsonl.zst after 7 days, decompressed on resume (hooks.md Codex section, doc says). Candidate keys it without the date folder or .zst (derived.go:144-151). | |
| Codex SQLite (state_*, thread_history_*, memories, goals, queue) | Rewritten in place. `thread_history_1.sqlite` is 442 MiB on BC-01 and each Codex run adds a full epoch (HO:1874-1876, doc says). Captured via a read-only backup API copy (derived.go:117-128, sqlite_authorized*.go). | |
| Codex/Claude history.jsonl, session_index.jsonl | Append or rewrite, agent-wide. Mac now withheld when a Codex exclusion exists (HO:1788-1795). | |
| Pi session jsonl | Append. File appears only once the session has a message. `--no-session` writes nothing. Branch via /tree stays in one file, /fork and /clone write new files (hooks.md Pi section, doc says). | |
| Pi snapshots, git package revisions | Computed from local state at capture time (adapters/pi_snapshot*.go) | |

**Sizes (doc says, phase3.md:414-415).** Encoded objects p50 about 2 KB, p99 about 1.1 MB, max 215 MB (Mac) and 35 MB (BC-01). The review quotes the largest investigated transcript at about 79 MB (review:140). D: max cumulative decoded 615 MB, one Codex artifact (HO:506). Mac inventory about 19k sources, BC-01 about 29k (phase3 table, HO:506-507).

**Measured workload D (doc says, HO:490-520, 1088-1100).**
- Amplification under a full-object model: 4.5-10x if captured once per hook (turn), 228-1294x if captured on every append (HO:505-506). This is the number that explains the quiet period and the epoch/append design. It says nothing about how often hooks fire.
- Replay of 128 anonymised timelines (78 Mac, 50 BC-01). With Q=30 s and M=300 s, p99 captures per session fall from 509 to 142 (-72 percent), uploaded bytes -77 percent, PUTs -82 percent. Lag p50 61 s, p99 277 s, max 301 s (HO:1088-1093). Limitations are stated: downsampled traces, append-only, one source per session, no throughput data (HO:1096-1097). Derived from real traces but a simulation, so "simulated evidence, not live measurement" (phase3.md:435).
- Cadence code: cadence.go:16-23, runtime.go:80-84, hints set `dirty_since` and `debounce_until = min(now+Q, dirty_since+M)` (HO:1044-1049). Debounce state lives in the queue DB. Observations are durable so Run "flushes the debounce at start" (HO:1051-1052).
- The p99 of 509 captures per session (immediate capture) means some sessions append at least 509 times. A hook fires at most once per turn, so hook-triggered capture would be far below that. The quiet period matters most to the walk-driven path.

**Evidence transcripts were still being written when a hook fired.**
- Docs say "The transcript can lag the in-memory conversation when a hook fires" and that is why the legacy sync waited 3 s (hooks.md:26). No measurement of the lag exists in the repo.
- Legacy `partialLineWindow = 60 s`: a transcript written in the last minute that ends mid-line is uploaded only to its last complete line (syncer.go:40, hooks.md:143).
- Candidate takes literal bytes. "a partial trailing line is part of the bytes" (model_history_test.go:6-8). Contract:98 requires "Wait or expose partial state, not arbitrary byte skipping" for an incomplete active tail. The double-hash check (capture.go:426-460) detects a write that lands during capture and fails the capture as "source changed during observation". The retry then happens on the next observation.
- Stop does not fire on user interrupt, and Codex has a separate Interrupt event with a 3 s cap (hooks.md, json.go:22).
- I found no recorded count of "source changed during observation" failures. Their rate is unknown.
- Practical consequence (inferred): a hook-only design reads at most once per turn, and may read before the tail is complete. The candidate handles it by debounce plus re-observation, which needs a trigger after the debounce. A later hook or a walk supplies it.

## Section 7. Conclusion

### Supported by the repository's evidence

1. **Hints are cheap and useful for latency (measured and doc says).** The hook is small and bounded: 2 s budget, 64 KiB stdin, no DB open, fsynced one-file-per-source inbox write that coalesces repeated events (capture_runtime_hook.go:26, runtime_inbox.go:29-80). On live probes a hook-driven capture followed within about 30 s (the quiet period) while a hookless one waited 3 min 21 s to be observed on BC-01 (HO:1853-1855). Pathname hints also pick up short-lived files that a walk might miss.
2. **Hooks are the only source of launch context (measured).** argv, env, cwd, model and permission mode come from the hook process and its ancestors (capture_runtime_launch.go:55-100). The code comments say the walk "cannot recover ... missed native launch context" (capture_runtime_hook.go:22-24). That is an argument for keeping hooks, not for removing the sweep.
3. **Dedup by content addressing is real (measured).** Object key is `objects/<encoded sha256>.zst` (publication.go:244). Unchanged sources produce no new compressed copy (phase3.md:36-38). So "dedup" already exists. The question is only whether it needs a local cache to be cheap. Today an unchanged settled walk costs about 95 ms discovery plus 270 ms hint at N=4000 and is write-free (production-gap-register-20261102.md:310).
4. **Remote state can replace some local bookkeeping (measured and doc says).** Rebase reads remote current and merges, so "a rebuilt or lagging spool converges" (phase3.md:66-68; rebase.go). R2 current plus manifests can answer "what is published" and "what epoch am I on", which suggests the per-source published cursor need not be authoritative locally.
5. **A hook-driven trigger is sufficient for the main-transcript case when hooks run.** The legacy design captured byte-exact sessions that way with live agents (hooks.md opening, docs/production-readiness.md:~280-282).

### Contradicted or unsupported

1. **"No periodic scanning" is contradicted for most source kinds.** Of 14 source kinds, the hook names only main transcripts, the subagent transcript on SubagentStop, and launch context (Section 2). tool-results, per-session artifacts, teams/plans, SQLite stores, history.jsonl, ~/.claude.json, config files, Pi snapshots and recompressed Codex files have no announcing event. Registered events TaskCompleted and TeammateIdle exist in the installer but the candidate hook ignores them for path selection, because it reads only five path keys (capture_runtime_hook.go:119). Under a hooks-only rewrite these kinds would need new hook semantics or be dropped. The contract forbids silent narrowing: "must not silently drop subagents, tool results, custom roots, launches, harness snapshots, database exports" (contract.md:47-49).
2. **Hooks do not always fire (doc says, repeated).** Codex persisted hook trust went stale on the Mac for days and again on BC-01 (HO:1722-1739, 1846-1856, 29, 74-79). Production-readiness shows Codex trust 0/6 after the first install on both machines (production-readiness.md:70-73). Trust invalidates on any command or hooks.json change and only a human can re-approve. Claude Stop does not fire on interrupt, Codex SessionEnd is synchronous with a 3 s cap, `claude -p` kills async hooks (hooks.md). Pi `--no-session` has no source. In each Codex episode the data was captured anyway, by the walk.
3. **Hook budgets are tight and the failure is silent.** The hook exits non-zero on timeout or inbox-full and the agent ignores it (capture_runtime_hook.go:26, runtime_inbox.go:57-61). The only backstop is the walk.
4. **Absence is not announced by anything but a path hint (doc says).** With the rollout file moved away and no trigger, nothing changed in the queue. Only an explicit hint moved it to `absent` (HO:1752-1758, 1857-1860). That says deletes are not reliably noticed by the walk either, which weakens "scan finds everything" but does not help a hook-only design, since no hook fires on external deletion.
5. **The reversal evidence does not isolate hooks (see Section 4).** The documented incidents that motivated replacement were v3 recovery defects (pruned pieces, dirty run-wide checkpoint, global lock). I found no recorded loss attributable to hook non-delivery. So the repo cannot prove that a hook-only design is unsafe in practice. It can only show that hook-only design relies on assumptions (trust approved, event fired, tail flushed) that were each seen to fail at least once, with the walk as the safety net.
6. **"Bytes streamed straight to R2" conflicts with the key scheme (inferred, Section 5).** `objects/<encoded sha256>.zst` needs the whole compressed body first. The double read for stability (capture.go:426-460) also needs the source file to be re-readable. A streaming design needs a different key or a staging-and-copy step. It also gives up the 3-tier safety the authors list: stable observation, durable acceptance, then delivery.
7. **"No SQLite" has a cost the authors measured (doc says).** Debounce (Q=30 s, M=300 s) needs durable per-source observation times. Without a local table the debounce would be in-memory and lost on restart. HO:1051-1052 notes the queue "flushes the debounce at start, because observations are durable". Inferred: an in-memory debounce plus a post-restart full walk is a workable replacement, but that is a walk.

### Minimum local state the evidence says cannot be removed

Each item names why a remote read cannot replace it.
1. **The effective privacy exclusion registry and policy** (capturepolicy, exclusion). Contract.md:107; HO:1745-1752. It lives only locally by design and must be consulted before every PUT.
2. **Identity and archive configuration** (archive id, machine id, roots including learned custom roots). Contract: "Preserve identities ... learned custom roots" (AGENTS.md:28; contract.md:47-49). Learned roots come from the hook environment and are not on R2.
3. **Native launch evidence** from hooks until it is attached to the session. It cannot be re-derived from files (capture_runtime_hook.go:22-24).
4. **Some record of what happened to each source that is not in R2**: attempts, last error, backoff time, quarantine reason with coordinates, and destination pause. Review:124 and contract.md:105-107 make quarantine "durable, inspectable, and replayable". R2 holds only successes.
5. **Bytes that were observed but could not yet be delivered**, if offline operation must survive source expiry or rewrite. Contract.md:44-45 and 68-72 make this the spool's reason. If the operator accepts that a source can be re-read at retry time (and that rewritten sources lose the earlier epoch), this item can shrink to a pending-work index. The repo has not measured how often that choice would lose data. It has measured that in-place-rewritten artifacts create new epochs often (206 epochs in 8 days for `~/.claude.json`).
6. **A mechanism that runs without agent events**: the walk (10 s poll on the Mac, about 5 min generation on BC-01) or something equivalent, because sources, trust, hints and the coordinator each fail independently.

Items that the evidence says could be REMOVED or moved to R2 in a rewrite (inferred): the per-source published cursor (reconstructible from current.json), the `runtime_discovery` inventory beyond a small fingerprint cache (HO:1707 says rewalking costs CPU; a cache is a performance aid, not truth), and the retention of every old job row with a full manifest (Mac manifests 578 MiB versus about 8 MiB latest-only, HO:1808-1810).

### Open questions for the operator

1. Is a loss of uncaptured source bytes during an R2 outage acceptable if the source is later deleted, rewritten or recompressed? This decides whether item 5 above is a spool or just an index. The repo has no loss rate.
2. Which of these may be dropped silently or captured on a coarse schedule rather than by event: tool-results, todos, tasks, teams, file-history, SQLite stores, history.jsonl, `~/.claude.json`, harness snapshots? Contract.md:49-51 forbids silent narrowing, so this needs an explicit statement.
3. What freshness target is required for a hookless session? Today it is up to one generation (about 5 min on BC-01, roughly the poll interval on the Mac) plus the 30 s quiet period.
4. Is a long-lived coordinator process acceptable at all, given "no periodic scanning"? A purely event-driven design still needs something to retry and to debounce. Is a short timer-based retry inside a hook-launched worker acceptable?
5. Who approves Codex hook trust on each machine after each hook-command change, and is a silent total loss of Codex events acceptable between change and approval?
6. Should launch context stay a hook-only artifact with explicit "missing" coverage when the hook does not fire?

### Things I could not verify

- Any count of sources found only by the walk versus named by a hook. No doc or test records it. I could compute it only from a live queue, which the brief forbids. The nearest figure is BC-01's week-long outage where the inbox held 534 hints while discovery found 17,903 new sources and 6,148 changed versions (HO:1816, 1829-1830). Both numbers were taken from the handoff and not independently checked. They also mix transcripts with artifacts (16,737 of the new sources were Pi, mostly artifact role), so no transcript-only hook-to-walk ratio can be derived.
- The delivery workload report (`reports/d-workload.md`, `reports/g3-cadence.md`) lives in a private path outside the repo. I used only what the handoff and phase-3 say about it, including the 0.01-5 percent rewrite rate and the 4.5-1294x amplification.
- Real hook-failure rates (timeouts, nonzero exit, inbox-full) are not logged anywhere I could find. Hook stderr is discarded by design.
- How often `source changed during observation` fires in production. Not in docs.
- The `WriteDiscoveryHint` consumer (`pollDiscoveryHints`, runtime_wake.go:183) and whether the wake shortens the 10 s poll. I read the writer only.
- Why Claude events PostToolUse, UserPromptSubmit, SubagentStart, FileChanged, CwdChanged and Codex PostToolUse and UserPromptSubmit are not registered. No document says. Whether any of them would announce tool-results or per-session artifacts was not tested.
- Current hook health on both machines. The handoff snapshot is 2026-11-05 20:57Z. I ran nothing live.
- Whether the Mac poll is 10 s or the 30 s in the phase-3 table (phase3.md "Discovery timer 30 seconds", HO:1707 and 1732 say 10 s). Config default is in internal/capturedaemon/config.go and I did not trace it to a literal.
