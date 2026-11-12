# W8: delivery slicing review (read-only, HEAD 41a07ff)

Method: DESIGN 1/2/16/17/18, amendments A0..A13, reports 00..24, handoff lines 1-130 plus greps, prototypes, `git log`, and code (`main.rs`, `daemon.rs:349`, `hook.rs:308`, `memory-reader/src/lib.rs`, `rg` for HTTP/zstd/tracing). No builds run. Items marked [inferred] were not read directly.

## 1. Slice and prototype status

### Slices

| Slice | Planned exit (quoted) | Status | Evidence | Missing |
|---|---|---|---|---|
| 0 Scaffold | "every gate is green on an empty core" | Done | Report 00, 02; `d8fbc4f` (2026-11-09) | Nothing material. Hegel on BC-01 probed (report 00, evidence 03). |
| 1 Core | "properties green at 2,000 cases locally, the proptest cross-check green, and the table complete" | Done in substance, parent acceptance never formally recorded | Reports 01-11; F2 table in 09; 2,000-case integrated gate in 23 (13 subgates 0, 97 % cov); handoff:185 still says "Slice-1 exit: NOT yet accepted" | P5 waived (A1). Property 3 has no writer-version seam and no proptest twin (handoff "Open"). Audit (7.2 1b) not implemented: `capture.rs:327,430,732` hard-code `AuditEvidence::NotDue` (A12.2). |
| 2 Daemon on Mac | "a day of this Mac's sessions captured, with totals matching section 3" | Partial: library and simulation only. Exit not started | See slice-2 table below | S3 client, zstd, audit, retention, tracing, run loop, startup delay, footprint. |
| 2b Reader | "every captured session restores to its source bytes, and the consumer gate is green" | Not started | `memory-reader/src/lib.rs` is a 3-line doc comment stub. `main.rs` exits 78 for any other subcommand | Everything: restore, export, importer run, shadow run (now 2 days, A13.1). |
| 3 Linux and snapshot | "both hosts green, and the gap register updated" | Not started | Snapshot types exist in `capture.rs:76,95` (planner side); no SQLite/config snapshot capture. P9, P10 not run | BC-01 inotify use, snapshot classes, deletions, loss-window counter, shadow run. |

### Slice 2 "Builds:" items

| Item | Status | Evidence | Gap |
|---|---|---|---|
| The lock | Done | `state.rs` `DaemonLock` (report 14); `LockHeld` exit 75 | Used by `main_entry` on startup only |
| Hint files | Partial | Writer: `hook.rs` (report 12), real file, 0600, one write. Reader: daemon scans hints (`daemon.rs` `ScannedFile`) | Retention not implemented (A11.2, A13.2 prerequisite). No installed Rust hook (see 2) |
| Watcher | Partial | `watch.rs`: pure `Scheduler` + `FsWatcher` over `notify` (report 19, `watch.rs:5`) | Not wired to any loop; no FSEvents run on real agent dirs; A13.6 missing-root case found only 2026-11-11 |
| Index and status with durable writes | Done in library | `state.rs`, `daemon.rs:106,136`; A7 SyncPolicy | Exercised only by tests and the startup check |
| Commit protocol | Done against fakes | Report 13, `commit.rs`; A6; property 4 (16) | Never run against R2. Fake was rebuilt to R2's ETag model on 11-11 (`0ea10cc`) |
| Client chosen by P2 | Not started | P2 recommends rusty-s3 + ureq (`a6203c2`). `rg ureq\|rusty-s3` finds only `deny.toml:13` (a ban list) | The whole adapter; license decision for CDLA-Permissive-2.0 still open (handoff) |
| Runs against fake store and acceptance bucket | Half | Fake only | Acceptance bucket never written by Rust code |
| Captures append-class sources for all three agents | Partial | Locator (06), capture (15), `WalkSources` (20), RealFs (17), all synthetic or temp dirs | No piece encoding (identity, not zstd; `capture.rs:21`); audit absent |
| Property 4 and counter tests | Done | Reports 16, 23; `tests/capture_property.rs`, `counters.rs`, `mutation_daemon.rs` | Oracle excludes unaudited mid-file rewrites (A12.2) |
| Footprint measured | Not done | Handoff: "30k reconcile 1.24 s debug, not asserted"; P4 is a synthetic C benchmark, "NOT actual memory hook or daemon footprint" | RSS, p99 of the real binary |
| Exit: a day captured | Not started | | |

### Prototypes (numbering per A8)

| Id | Planned decision | Status | Evidence | Missing |
|---|---|---|---|---|
| P1 | head/tail windows vs full rehash | Done, synthetic. Interior rewrites escape (so audit needed) | `prototypes/P1-*.md` | Audit implementation |
| P2/P2b | S3 client; R2 checksum | Done. rusty-s3 + ureq | P2 doc, `p2-s3-client/` | Adapter, license |
| P3 | hint log rotation | Done: per-day files lose 0/100, rename loses 100/100 | P3 doc | None (A11.2 retention remains) |
| P4 | hook timing and RSS | Done, synthetic C only; p99 above 1 ms target in the final run | P4 doc header | Real binary measurement |
| P5 | prefix rewrite frequency | Waived (A1); also "blocked measurement" | P5 doc, A1 | Measure from daemon counters later |
| P6 | clock across sleep | Simulation only; "natural-sleep observation remains pending" | P6 doc | Real sleep |
| P7 | checksums | Done: SHA-256 7.98 ms at 20 MB | P7 doc | None |
| P8 | duplicate share | Blocked, informational | P8 doc | None needed |
| P9 | SQLite unchanged test | Not run (slice 3) | none | |
| P10 | Linux racy mtime | Not run (slice 3) | none | |
| P11 | commit retry exhaustion | Done on real R2 (A8: renumbered from P9) | P11 doc, `r2-raw.json`; A6 | |
| P12 | cheaper durable state writes | Done (A8: from P10); A7 | P12 doc | |

Prototype verdict: the ones that touched real R2 (P2, P11, A13.4 probes) changed the design every time. The ones that could only be simulated (P4, P6) are still unconfirmed.

## 2. What runs end to end today (ground truth)

- `memory hook <agent>`: reads stdin (bounded), parses, appends one owned JSONL hint line to a local hints file under the state root, always exits 0, prints nothing (`hook.rs:308`, report 12). It is real, tested, and has no consumer outside tests. Nothing in the documents says it is installed on a host. At the 2026-11-09 handover `~/.local/state/memory` was absent (handoff ~line 625); the installed hooks are the Go ones (A0) [inferred: no later install is recorded].
- `memory daemon`: `main_entry` (`daemon.rs:349`) resolves the state root, runs `open()` (lock, state dir, hints-dir vetting, config load, index and status reads), drops it, and exits `EXIT_NO_ADAPTERS`. Its doc comment says "Prints nothing and never captures". Anything else: exit 78 (`main.rs:19`).
- File on disk to object in R2: no code path. There is no HTTP client in any crate (the only matches are ban entries in `deny.toml`), no zstd, no `tracing`. The Rust writer has never written a production object, and per the documents it has never written to R2 at all; R2 was touched only by prototype and probe code in standalone Go/Rust projects under `docs/rust-rewrite/prototypes/` and by the A13.4 probes.
- Reader: none (`memory-reader` is a stub).
- User-visible value today: none from the binary. The delivered value is (a) a privacy transform, locator, manifest and commit-protocol library with a large test base, (b) measured facts about R2 and a client choice, (c) a design with explicit prerequisites. The Rust rewrite does not capture, store or restore a single byte.
- Is anything capturing sessions? From the documents: A0 and handoff ~584-592 record that on 2026-11-09 the Go capture daemons (Mac, BC-01) and the Mac refresh job were stopped and disabled; the Go hooks write only to the local Go spool (4.8 GiB of an 8 GiB cap on the Mac at handover; behaviour at the cap "unverified", handoff ~628). A2 deleted the 435,618 R2 objects the same day. So nothing publishes to R2 and nothing has since 2026-11-09, which is 3 days at today's date (2026-11-12). Local sources and the Go spool remain on the hosts. That the hosts were not re-inspected is stated at handoff:1095 (dated Oct 29) [no fresh check in the documents].
- Is the capture-off risk recorded and accepted? The stop is an operator instruction (A0; A2 "confirmed twice"), and DESIGN 18 step 4 records that old-archive-only sessions are accepted as lost. I found no place that states the duration of the gap, a bound on how long capture may stay off, or what happens when agent-side rotation or deletion removes local sources before the first Rust capture. A10.7 notes only that the Go cross-check can no longer run; A13.1 substitutes "local session files with no captured owner". The spool-cap behaviour is flagged as unverified. So: the stop is accepted; the cost of a long gap is not recorded.

## 3. Vertical or horizontal

Classification of reports 00-24 (a thin end-to-end increment, b horizontal component built to depth, c review/fix/test hardening, d prototype/decision):

| Class | Reports | Count |
|---|---|---|
| (a) thin end-to-end | 12 (hook: real stdin to real file, but no consumer) | 1 |
| (b) horizontal layer | 00, 03, 04, 06, 07, 08, 10, 11, 13, 14, 15, 17, 18, 19, 20, 21, 22 | 17 |
| (c) review/fix/hardening | 01, 02, 05, 09, 16, 23, 24 | 7 |
| (d) prototype/decision | none as implementation reports (P1-P12 are separate files) | 0 (12 prototype docs) |

Report 18 (daemon skeleton) and 15 (capture wiring) are the nearest to integration, but both state "synthetic in-memory filesystem and fake store only" (15 line 3) and 18 stops short of a loop. 21 and 22 are code driven by prototypes (A7, A6) and are classed (b).

Commits and days (git, `d8fbc4f^..HEAD`, paths rust/, docs/rust-rewrite, DESIGN, handoff; subject-prefix heuristic): 68 commits, all inside 3 calendar days (2026-11-09: 17, 11-10: 31, 11-11: 20). The work before `d8fbc4f` (reports 00-20, all of slices 0-1 and most of slice 2's components) is one commit on 11-09, from uncommitted work on 11-08, so per-report commit counts do not exist for classes (a) and (b). After that commit:

| Class | Commits | Dates |
|---|---|---|
| tests | 19 (21 by a looser regex) | 11-10 (9), 11-11 (10) |
| docs | 15 | all three days |
| ci | 5 | |
| "feature"-labelled | 29, of which most are review-driven fixes or tightening (`a8042c9`, `94bacad`, `97b0171`, `4eeb7db`, `45b43c9`) [inferred from subjects]; real new capability about 5 (`3e29ebf` A7, `c1ab3ec` A6, `295a092`, `9482e9f`, `a1920b3`) | |

So roughly 40 of 68 commits (about 59 %) are test, docs or CI, and most of the rest hardens existing components. Since report 23 (11-10), no commit adds a path toward R2.

Walking skeleton as a target: never. Report 23 says it in its "Why": the reviewers "found the code a well-tested library, not a running system". No step in 00-24 has "file to R2 to restore" as its goal. DESIGN 17 itself orders slices by component list, with the first real store contact at the slice-2 exit.

Earliest possible skeleton: 2026-11-09, right after `a6203c2` (P2 picks rusty-s3 + ureq) and the same-day commits that contain `commit.rs`, `capture.rs`, `realfs.rs` (all in `d8fbc4f`). A 150-line adapter plus a one-shot `capture + commit` on one synthetic file into `rust-acceptance/<run>/`, and a 50-line fold-and-concatenate reader, needed nothing the repo lacked. A2 explicitly authorises acceptance writes under that prefix.

What an early skeleton would have surfaced sooner (all found later, by other means):
- A13.4 (11-11): R2 ETag is content MD5, so `If-Match` cannot see an identical rewrite; the fake store modelled ETags otherwise until `0ea10cc`/`f63fa62`/`c5a2064` rebuilt it. Also, a write from a killed process still lands (8/10) and a restarted daemon may verify before it lands: the 5 s startup delay is still an open operator decision.
- A10.3 / A13.2: the manifest's `zstd-3` label and `.zst` key versus identity bytes (`capture.rs:21`, `schema.rs:316`): the writer cannot write a true piece. Found by review on 11-10, resolved 11-11.
- A12.2: the audit gap, found by the property generator, not by use. Writes are now blocked on it.
- A13.6: an uninstalled agent's root does not exist. One real run on this Mac would have hit it.
- A11.5/A12.4: the source-read race (changed file, parent-directory race). Review-found.
- P2 open items (license for `CDLA-Permissive-2.0`, `BadDigest` class, single checksum) have sat unresolved since 11-09.
- CI wall time: the 30-minute macOS limit forced a Nix-cache fix (#79-#81); depth in tests, not integration, drove it.
- P11/A6 (BaseUnchanged exhaustion) and P12/A7 (6-minute state-machine tests from `F_FULLFSYNC`) are real-system facts that the simulator had to learn from outside.

## 4. Is each step testable and valuable alone?

Testable alone: yes, almost every step, and heavily. Valuable alone: only a few.
- Value alone: 12 (hook, though unconsumed), 03/05 (privacy transform has Go fixtures and a canary property, so any later writer reuses it), 13 (commit protocol, proven against the fake with property 4), 17/20 (real filesystem and resolver, usable from a CLI immediately).
- Test depth ahead of integration risk reduction: 24 tests-to-source ratio below; report 24 itself spent a review round measuring test value (24 removed, 9 merged); mutation scripts and `cargo mutants` ran on `memory-core` before any S3 call existed (privacy 268 caught/22 missed, others 271/26 and 147/1, not triaged); daemon state machines with 12 shapes and negative controls (42e8826) for a daemon with no loop; 10 test-only commits on 11-11 while the adapter did not exist.
- Depth-first was right for: (1) the privacy pipeline, because storage is plaintext and permanent (DESIGN 19 default 1, "a scrubber bug is permanent"), and the Go fixtures give a cheap oracle; (2) commit protocol and `If-Match` semantics, because they are data-loss critical and the production mutation gate (`e2e3cd9`) proves the fence is load-bearing; (3) state-file durability (A7/P12). These are exactly the parts a skeleton would have reused unchanged.
- Credit also for prototypes that ran on real R2 early (P2, P11) and for the A13.4 probe, which was disciplined (39 keys, 0 left).

## 5. Process overhead (measured)

| Measure | Value |
|---|---|
| Rust source (`src/**`, 3 crates + xtask) | 11,906 lines |
| Rust tests (`tests/**`) | 24,046 lines (2.0 x source) |
| Rust-rewrite docs (top-level, implementation, prototypes md) | 4,511 lines |
| DESIGN.md / amendments / handoff | 1,305 / 442 / 3,205 lines |
| Total prose a maintainer has on the way in | about 9,000 lines vs about 11,900 lines of source |
| Implementation reports | 25 (00-24), 7,987 lines with DESIGN and amendments |
| Evidence directories | 15 under `implementation/evidence/` |
| Amendments | A0-A13, about 40 headings including subsections; A10-A13 are corrections and review outcomes |
| Review rounds | at least 12 named (`evidence` rounds 3, 4, 5, 7, 8, 10; reports 02, 05, 23, 24; parent reviews) |
| Feature vs review/fix/docs commits | about 5 new-capability vs about 63 other (3 days) |

Proportion judgement. For a privacy- and loss-sensitive system, a design amendment log and measured evidence are justified. What is disproportionate is the sequence: roughly 3,600 lines of review/evidence prose (reports 23 and 24, evidence 13 and 14, A10-A13) were produced about a code base that cannot yet connect to a store. The review rounds found real defects (symlink dir, O_NOFOLLOW, state sizes, launch-value shape), but also test-model faults and flaky guards (A10, report 24 close-out), which is the cost of verifying a simulator against itself.

Reading path for a maintainer: AGENTS.md, then DESIGN.md (1,305), amendments (442), handoff head (~130), report 24 (282) is about 2,200 lines, which is acceptable. The risk is the handoff: its first line calls it "the sole current ... brief", but the current brief is lines 1-130; lines 130-3205 are history (Go-era worker logs, 2026-10-29 installed-capture row at 1095, old checkpoints) and some of it contradicts current facts (handoff:185 "Slice-1 exit: NOT yet accepted", 1095 "collectors disabled/off" from Oct 29). Within the first 130 lines there are four overlapping "session" blocks and a "Resume here" that points to "the block directly below", itself followed by "history". It is usable only because of that pointer; it is not usable as a standalone brief. Remedy: move everything after line ~130 to an archive file, keep one 100-line brief (about 2 hours).

## 6. Remaining path to the first captured-and-restored session on the Mac

Smallest ordered list (S = small, M = medium):

1. Decide the P2 license item (`CDLA-Permissive-2.0` in `deny.toml`) and the `BadDigest` class. Operator decision, S.
2. S3 `Store` adapter (rusty-s3 + ureq): `put_if_absent`, `put_if_match`, `get`, throttle/Retry-After mapping, `Ambiguous` on lost reply, no retries on `current.json`. Test by the existing `commit_protocol` suite pointed at a prefix under `rust-acceptance/`. M.
3. Walking skeleton NOW, before the other prerequisites if the operator reads `rust-acceptance/` writes as outside A13.2 "production" (A13.2 and A2 are ambiguous; ask): `capture + commit` of one synthetic file, then a `memory-reader` fold that rebuilds the bytes and compares to `transform(local)`. About 100-200 lines. This tells whether manifests, keys and ETags behave as assumed. If the operator says A13.2 applies to every R2 write, do 3 right after step 7.
4. zstd encoding of pieces (A13.2); `encoded_len/sha` from real output. S-M.
5. Audit (A12.2): implement `AuditEvidence::Due` path and extend the property to cover the mid-file rewrite case. M.
6. Hint retention (A11.2). S.
7. `tracing` to stderr replacing the A11.3 counters (A13.2). S-M.
8. Startup delay (A13.4): operator picks the minimum (5 s suggested), then jitter per DESIGN 11.2. S.
9. Run loop in `main_entry`: wire `FsWatcher`, `Scheduler`, `WalkSources`, `SharedFs`, 60 s tick; replace `EXIT_NO_ADAPTERS`. M.
10. `memory restore <owner> --out DIR` over the reader library, with the daemon's read-back. M.
11. Install hook and daemon on the Mac (launchd), run one real session, then restore and compare. Operator action to install. S.
12. Day run (slice 2 exit), footprint measurement, then `memory export` and importer run (rest of 2b), then 2 consecutive clean days (A13.1).

On or off the critical path (items from report 24 close-out and A13.2):
- On: all A13.2 prerequisites (zstd, audit, retention, tracing); the startup delay (A13.4); the S3 adapter and license; the run loop.
- Not on it for the first captured session: triage of the 22+26+1 missed mutants; mutation runs for `state.rs` and `watch.rs`; rerun of `GET_ATTEMPTS` mutants; removing `FileSystem::stat_len`. Run these after step 9 so mutation targets include the wired code.
- The canonical-path type (A12.4): not needed for the first session (the race needs an attacker or a rename storm in an agent directory), but it belongs before cutover because the source read is a privacy boundary. Place after step 11.
- Not on it: slice 3, P9, P10, `LaunchRecord` unification, property-3 seam (do before cutover).

## 7. Verdict

- Alignment: the work follows the design's component list and keeps its safety rules, and the amendments record each divergence honestly. It has drifted from the design's intent of slices that end in observable value: slices 0 and 1 are done; slice 2 is a set of components with no wire, 2b is untouched, and the Go capture has been off since 2026-11-09.
- Shape: horizontal. 17 of 25 reports are components built to depth, 7 are review/test rounds, 1 is end to end (hook, no consumer). 12 of 12 prototypes that needed R2 and ran there changed the design; the code was not run against R2 at all.
- Proportion: depth was right for privacy, the commit protocol and state durability (permanent plaintext, data-loss risk). It was wrong to keep deepening (mutation triage, state-machine shapes, doc rounds) before one real object existed. The 3-day review ratio (about 59 % tests/docs/CI) reflects that.
- Three sequencing changes:
  1. Build the walking skeleton now: S3 adapter, one-file capture and commit under `rust-acceptance/`, plus a minimal fold-based restore. Get the operator to say whether A13.2 covers that prefix.
  2. Freeze review, mutation-triage and new-amendment work until the loop is wired; take only blockers that sit on the path in section 6. Then run the open mutation items on wired code.
  3. Define each remaining step as a vertical increment with a real-store test (adapter against R2, zstd round trip through restore, audit with a real rewrite, loop with one live session), put the capture-gap duration and the 5 s startup-delay decision in front of the operator now, and cut the handoff to a 100-line brief with the history archived.
