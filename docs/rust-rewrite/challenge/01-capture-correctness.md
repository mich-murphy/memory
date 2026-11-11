# 01 Capture correctness (reviewer A)

Labels: read (file:line), measured (my probe, `scratchpad/A/p.py`, synthetic data, Mac APFS), inferred, recalled (from memory of vendor docs or source, not fetched this session: I had no web tool, so every "industry" claim is recalled and should be re-checked before it is cited elsewhere).

## Verdict

The Append plan has no prefix proof. DESIGN.md 7.2 hashes `[captured_to, size)` into a stored state and compares "the state's digest at captured_to against the stored one". That comparison is a tautology and never touches bytes `[0, captured_to)`. An in-place rewrite with `size >= captured_to` yields a silently wrong Append, and nothing in the model catches it before the daily re-hash, which then mislabels it a writer bug and quarantines instead of starting an epoch. File identity is absent from `SourceState`, `(size, mtime)` is a weak change test on Linux, and `sources_of(session)` is undefined for shared and cross-directory stores. Property 1 as written cannot detect any of this. Three Blockers, four Majors.

## Findings

### B1 (Blocker). The prefix proof is not a proof (hypothesis 1: confirmed)

Claim: DESIGN.md 7.2 (lines ~"Prefix proof"): a healthy append costs the delta; the proof compares the resumed digest at `captured_to` with the stored one. Section 5 `Plan::Append` says "prefix proof required before upload". Section 10 lists "Source rewritten, truncated" as handled by "Prefix proof".

Problem. The state after `captured_to` bytes is stored in the index. Resuming it and hashing `[captured_to, size)` produces a digest of the *new tail appended to the old state*. The only digest "at captured_to" available is the stored one itself, so equality is trivially true. The bytes `[0, captured_to)` on disk are never read. The only things that can trigger `PrefixMismatch` are `size < captured_to` (Truncated) or an index miss. Any rewrite that keeps or grows size passes.

Evidence. Measured: stored state at n, then in-place overwrite of bytes 10..20 and an append; "resumed-state digest equals stored (proof passes): True"; re-reading `[0,n)` and hashing gives a mismatch ("bounded re-read matches stored: False"). Read: Section 10 daily re-hash would eventually see it, see B2.

Consequence: the manifest says epoch n, one whole-file `sha256` (the resumed-state digest of the *old* prefix plus new tail) and pieces whose concatenation is not the file. Pieces are immutable and `current.json` references them, so the reader sees a coherent, wrong object. Real cases: Claude resume/compaction rewriting a transcript prefix, Pi "snapshot" rewrites, any editor save that preserves size.

Industry (recalled). Filebeat `file_identity` + registry offset, and its default fingerprint of the first 1024 bytes (`file_identity.fingerprint`, offset 0 length 1024); Fluent Bit tail keeps inode + offset in its db and detects truncation by size < offset; Vector file source uses a `checksum` fingerprint of the first 256 bytes (`fingerprint.strategy = checksum`, `lines = 1`); Promtail/Alloy positions file is path + offset, rotation detected by inode change; OTel filelog receiver fingerprints first 1000 bytes and compares on each poll (`fingerprint_size`). None of them proves the whole prefix. All accept first-N-byte fingerprint + identity + size >= offset, and all are built for append-only logs where mid-file rewrite is out of contract. This design claims to prove "bytes, order, provenance recoverable" and is held to a stricter bar than a log shipper, because a Replay is cheap and a silent wrong object is the thing R3 forbids.

Cheapest sound check. Per capture, read and hash a bounded window ending at `captured_to`: `[max(0, captured_to - W), captured_to)` with W = 64 KiB, plus the first 4 KiB head, and store both window digests in `SourceState` (state hashes are not needed for them: store the 32-byte digest of the last-W bytes at the time of capture). Cost: one extra read of at most 68 KiB per capture, about 70 ms per 19 MB scales to under 1 ms; the design target "a capture of a 59 KB append reads at most 59 KB plus one block" (section 14) must be relaxed to "plus 68 KiB". This is still probabilistic for a mid-file rewrite far from both windows. For a sound check use the full re-hash that section 10 already budgets (70 ms / 19 MB, measured 3.4), but run it on the *Append decision* when `(inode, mtime)` changed in a way inconsistent with an append (B2/M2), not only daily. The two checks together give: tail window catches in-place edits near the end (the usual case for agent rewrites), full re-hash catches the rest within one daily period at worst.

Recommended change: replace the sentence in 7.2 with the window + identity check above, delete "state's digest at captured_to" language, and say plainly that the resumable state is a *cost optimisation for the whole-file digest*, not a proof.

### B2 (Blocker). Section 10 re-hash turns a source rewrite into a quarantine (new)

Claim: "A mismatch is a writer bug, not a source change. It quarantines the source" (section 10, measure 2). The comparison is fresh hash vs "the whole-file digest the resumable state produced for the manifest".

Problem. After a B1-style silent rewrite, the fresh full hash differs from the resumable digest. The check cannot tell writer bug from source rewrite, because the source *did* change after the last capture can also legitimately differ: a normal append between capture and re-hash makes the file longer, and a re-hash of the whole file at a later time compares against a manifest digest of the shorter prefix. The text does not say the re-hash is bounded to `captured_to`. If it is not bounded, every active source mismatches daily; if it is, a rewrite is correctly detected but wrongly classified as writer bug and the source is quarantined (stops uploading) rather than starting a new epoch. Either reading is wrong.

Evidence: read, section 10 measure 2; Property 4 and the journey test inject "corrupt index entry" only (section 14, last para), so the rewrite-vs-bug distinction is untested.

Recommended change. Bound the re-hash to `[0, captured_to)`; on mismatch re-read the same range once; if the second read is stable and differs, it is a source rewrite → `Replay{PrefixMismatch}` and count it; quarantine only when the hasher's own two fresh reads disagree with each other (that is what a software bug in the *resumable* path looks like: the independent path disagrees with the resumable path but the file is stable). Distinguish by comparing resumable digest vs fresh digest over a file whose `(identity, size, mtime)` is unchanged since the capture.

### B3 (Blocker). Property 1 cannot find B1 (hypothesis 7: confirmed)

Claim: section 14 property 1: "pieces emitted reassemble the file at every capture, and every epoch boundary corresponds to a failed prefix proof".

Problem. The second clause is defined in terms of the very predicate that is wrong; a model whose proof is the same tautology is consistent with it. The first clause would catch B1 only if the generator performs same-or-larger-size in-place rewrites and then checks reassembly; as written ("appends, rewrites and truncations") it may generate only size-shrinking rewrites if "rewrite" means replace-with-new-content of arbitrary length, and a rewrite that happens to keep the old prefix is indistinguishable from an append, which is legal.

Recommended property (oracle independent of the planner):
For any sequence of operations from `{append(k), overwrite(off, bytes) with off+len <= size, overwrite_then_append, truncate(n), truncate_then_regrow(n, m) with m >= old size, rename_replace(new content), same_size_rewrite}`, after each capture-and-flush the reassembly of the *current epoch's* pieces equals the file, **and** a naive oracle `needs_new_epoch = !file.starts_with(previous_captured_bytes)` (full byte compare, test-only) equals "an epoch boundary was emitted at this capture". Add a mutation target: with the window check disabled, the property must fail within 500 cases; run it under `cargo mutants` for the planner. Also add a time axis: a rewrite between the last stat and the capture read (fake filesystem hook after `stat`) to check the "file moved" abort.

### M1 (Major). SourceState has no file identity (hypothesis 2: confirmed)

Claim: section 5 `SourceState { size, mtime_ns, epoch, hasher, captured_to, pieces }`; `Observed { size, mtime_ns }`.

Problem. Atomic rename-replace changes the inode while leaving size possibly equal and mtime essentially fresh. Measured: after write-temp + rename over the target, inode changed True, size equal, mtime moved by 84 µs. With `size >= captured_to` this is another B1. Log rotation (`x.jsonl` → `x.jsonl.1`, new file) leaves the path with a smaller or growing new file; if it grows past `captured_to` before the next reconcile, identical failure. Codex `CodexCompressed` rollout vs `CodexRollout` is a rename plus recompression of the same session; unaddressed (index key is the source id, not identity). Config/todo JSON written by editors and CLIs (Claude `~/.claude.json` is the "206 epochs in 8 days" case, 05-history-lessons.md:206) are rename-replace writers.

Industry (recalled). Filebeat registry keys by `(inode, device)` (`file_identity.native`) or fingerprint; Fluent Bit `db` keys on inode+name; Vector `fingerprint` plus `ignore_older`; OTel filelog fingerprint. All treat "same path, different identity" as a new file starting at offset 0.

Recommended change. Add to `SourceState` and `Observed`: `dev`, `ino` (Unix), and on macOS `st_birthtime` as a tiebreaker for inode reuse (APFS does reuse inodes after unlink, recalled). Rule: identity change → never `Append`; goes to `Replay{PrefixMismatch}` for append class and `Snapshot` for snapshot class. Cost: zero extra syscalls, `stat` already returns them (section 8 "at most one stat per source" remains true).

### M2 (Major). `Unchanged` from `(size, mtime)` can miss changes (hypothesis 3: refined)

Measured: 0 of 2000 same-size rewrites in a tight loop left `(size, mtime_ns)` equal on APFS (ns resolution). So on this Mac the risk is low. On BC-01 ext4/xfs, Linux kernel timestamps come from the coarse clock (`current_time()` granularity is a jiffy, 1–4 ms at HZ=250/1000) (recalled; not measured, I cannot test Linux here). Two writes in one jiffy give equal mtime. Same-size rewrites in <4 ms of each other and of the previous capture's stat are rare, but the race is wider than that: a write immediately *before* the stat that produced the stored `mtime_ns` and a second write within the same tick *after* the capture read both look `Unchanged`.

Git's answer: "racily clean" entries. If a file's mtime is >= the index file's own timestamp, git cannot trust `(size, mtime)`, so it rehashes content (Documentation/technical/racy-git.txt, recalled). rsync's default quick check is `(size, mtime)` at 1 s granularity with `--modify-window`, and it knows that is weak, offering `-c` checksum (man rsync, recalled).

Recommended change. Store `observed_at` (host monotonic or wall clock) with the stat. If `mtime` is within a granularity margin (say 2 s) of `observed_at` at capture, mark the state `racy`, and treat the next reconcile as `Append{from=captured_to}` even if size is unchanged, verified by the B1 window. That costs a bounded read of 68 KiB once per racy source. Without this, the 15-minute reconcile will miss a size-equal change until the daily re-hash.

### M3 (Major). "Capture whole" and SQLite backup: livelock and determinism (hypothesis 5: refined)

Read: section 7.2 "If the second pass yields a different digest, the file moved; abort and let the coalescer retry."

Livelock. For a continuously appended 103 MB file, a two-pass read (~190 ms for hash+zstd, measured 3.4) races with writers; each abort re-queues after `quiet` or `cap`. A session that is active for hours can fail every attempt, and nothing bounds the retries or moves it to the Append path. Append-class files should never take "capture whole" on a growing file: if `Replay` is needed, capture a snapshot of `[0, size_at_stat)` only, by hashing the first `size_at_stat` bytes in both passes (file is append-only in the common case, so the first `size_at_stat` bytes are stable) and let the next capture Append the rest. For truly rewriting (snapshot-class) sources add a bounded retry count and a `source_unstable` state visible in `memory status` rather than silent retry.

Backup API. Measured with Python's `sqlite3.Connection.backup` on a WAL source: three backups of an unchanged source give identical digests (`d6b416f16e34` x3, 1.1 s apart); backup output has journal mode bytes `0202` (WAL flag retained in the header) and no `-wal` file is produced; after one insert the size is unchanged here (53,248) but content differs. So unchanged → byte-stable on this SQLite, which is good for content addressing and for `Unchanged`. Not measured: a writer concurrent with the copy. SQLite docs (recalled, sqlite.org/backup.html): if the source is modified by a *different connection/process* during the backup, the backup restarts from the beginning on the next step; with `pages = -1` (Python default for `backup()` is all pages in one step) the source is read-locked for the copy and writers wait, otherwise a hot writer can restart it indefinitely. The history lesson D8 (05-history-lessons.md:52) shows Go found "no torn snapshots" but needed a typed `ErrSQLiteSourceChanged`. The design has no equivalent type; `Disposition` has Retry and Quarantine only.

Also not addressed: the backup header's change counter / file-change counter and "schema cookie" can differ between backups of semantically equal content if the source checkpointed in between, so `Unchanged` for SQLite should be decided from a cheap source fingerprint (db + `-wal` size/mtime + identity) *before* backing up, not from the backup's hash, otherwise every reconcile runs a backup of a large store. 05-history-lessons.md:209 (item 5) calls the missing "cheap fingerprint that detects WAL/SHM change" unsolved; the design does not solve it.

### M4 (Major). Session attribution `sources_of(session)` is undefined (hypothesis 6: confirmed gap)

Claim: 7.2 `sources_of(session)  // from index plus a readdir of the session dir`; section 4 says events "name a session"; R1 says 13 of 14 kinds are visible to filesystem notifications.

Problem. The design never defines the function from an fs event path or a stray file to a `SessionKey`. Concretely:
- `ClaudeSubagent`, `ClaudeToolResults`, `ClaudeTodos`, `ClaudeTasks`, `ClaudeFileHistory`, `ClaudeDebug` live in directories keyed by session id but not under the transcript's directory: "readdir of the session dir" finds only the sibling subdirectory of `projects/<slug>/<id>/`, not `todos/`, `file-history/<id>/` and `debug/<id>.txt` (inferred from the Go classifier, `capturesource/classify.go`, reported in 02-runtime-architecture.md:402; I did not open the Go tree). The reconcile must therefore be driven by a per-kind path→session mapper, which is the thing the Go side got wrong (identity bugs I1, I2, 05-history-lessons.md:109–110).
- Shared stores (`CodexStore`, `CodexHistory`, `ClaudeHistory`, `ClaudeConfig`, `PiSettings`, `PiModels`) have no session. The manifest model puts every source under a session `current.json`; which session owns a shared store, and does each session re-capture it (N sessions × whole store) or is there a pseudo-session? Section 16.1 defers it as [open] but the "default adopted" does not say where the bytes live. 05-history-lessons.md:206 shows this exact choice produced 587 MiB per run.
- A stray file that appears in a root (new subagent directory, tool-results spill, a Pi `PiSnapshot`) before any hook arrives has no entry in the index: it is found only by the 15-minute reconcile's readdir, and only if its session is already known. A new subagent file in an *unknown* session produces no `SessionKey` unless the mapper can derive the id from the path. Path-only derivation is exactly what the contract's custom roots ("learned roots", section 8 index) complicate: if a root was learned from a hook `transcript_path` and the index is deleted ("Deleting it loses nothing", section 8), the learned custom roots are lost with it and the 13-of-14 notification coverage silently shrinks to the configured roots. Section 8 and AGENTS.md ("Preserve ... learned custom roots") contradict each other.
- Launch evidence (hook-only, 7.5) is held in the index "keyed by session until the next capture", so deleting the index or a crash before the capture drops it with no R2 copy: "Deleting it loses nothing" is false for launch evidence and learned roots.

Recommended change. Add a section 5 type `Locator { fn session_of(path: &Path, roots: &Roots) -> Option<(SessionKey, SourceKind)> }` per agent, pure, in `memory-core`, with a property test: for every path under any root, `session_of` is total, the mapping is stable under creation of unrelated siblings (the Go I1 lesson), and every path with `None` is counted and surfaced as `unattributed` in `memory status`, never dropped. Move learned roots and pending launch evidence out of the "rebuildable" index into the operator config file or a separate small file, or state explicitly that launch evidence is lossy.

### m1 (Minor). Mid-write pieces, torn lines (hypothesis 4: confirmed fine for bytes, with two caveats)

Capture is bytes-only and reassembly is byte concatenation, so a piece ending mid-line or mid-UTF-8 sequence is correct by construction as long as the next Append starts at exactly `captured_to`. The proof windows in B1 also work on raw bytes. Caveats: (a) any consumer, including the daily read-back sample and the downstream "importer", that splits by piece must not assume line boundaries; the design states the writer "never parses transcript content" (3.6) but nothing in the manifest says pieces are not line-aligned, so a reader author could assume so. State it in 6. (b) Mid-line capture means a partial record is durable in R2 for up to a window; if a reader acts on the latest manifest it sees a torn last record. Tail the file only to the last `\n` for the `to` offset when `class = append` and the kind is JSONL (cheap: look at the last read byte, back off at most one record). Cost is one extra byte compare; benefit is that every manifest ends at a record boundary and a reader may treat the manifest as a valid JSONL prefix. Note the hint log section 8 already uses this rule ("A torn last line is left until its newline arrives"), so the design uses it for its own file and not for the agents'.

### m2 (Minor). Epoch rule wording

Section 6: "A truncation, or a missing index entry for a file R2 already knows starts epoch n+1". "Truncation" is detected by `size < captured_to` only; truncate-then-regrow past `captured_to` between reconciles is B1/M1. `IndexMissing` forces a whole-object base piece for every source on every index loss (30k sources); R6 and 3.1 do not price this. After an index delete on a 2 GB host, the re-upload is the whole 1.9 GB again, not zero, because the design intentionally has no way to adopt R2's existing manifest as the baseline. Adopting the manifest from `current.json` (piece offsets and sha256s are there) plus the B1 window check (compare R2's last piece sha256 with the local window) would avoid the full re-upload; if the window check passes the state hash must be rebuilt by a full local re-hash (70 ms / 19 MB, cheap), no upload needed.

### m3 (Minor). `Deleted` and rename

Section 7.4 records `deleted` when a source "disappears". A rename-replace has a moment where the path is absent; a `Deleted` plan emitted in that window followed by a first-capture Replay looks like epoch churn. Debounce `Deleted` across one reconcile pass or require two consecutive misses.

## Claims checked

| Claim | Status |
|---|---|
| 7.2 prefix proof detects rewrite | Contradicted (measured, B1) |
| 3.4 SHA-256 19 MB 70 ms, 103 MB capture 190 ms | Not rerun; plausible for sha2 with SHA-NI, not confirmed |
| Backup output is deterministic | Confirmed for unchanged source, WAL mode, Python sqlite3 backup (measured) ; not for concurrent writer |
| Whole-object two-pass abort terminates | Not guaranteed, no retry bound (B3/M3, inferred) |
| 7.2 "mtime_ns" suffices | APFS ns granularity makes same-size collisions unobserved in 2000 tries (measured); ext4/xfs not tested |
| `sha2` `SerializableState` stability | Not checked (no network) |
| Hook 3 of 14 kinds, fs 13 | Not rechecked (reports/09 not read) |
| R2 conditional-PUT and Content-MD5 behaviour | Out of my area, not checked |

## Open questions for the operator

1. Is a silently wrong object acceptable for a rewritten transcript until the daily re-hash, or must the Append path read a window every time (my recommendation)?
2. Which agents rewrite transcripts in place (Claude resume/compact, Pi)? Needed to size B1 empirically; a one-day scan on this Mac for `captured_to`-prefix changes via `(inode, size, window-hash)` before slice 2 would quantify it without reading content.
3. Where do shared stores live in the manifest (a pseudo-session per host, or copied into each session)?
4. Are learned roots and pending launch evidence expendable on index loss?

## Things I could not verify

- Any industry claim above: no web tool in this session, all recalled from knowledge of Filebeat, Fluent Bit, Vector, Promtail/Alloy, OTel filelog, git racy-index, rsync and SQLite backup docs; the parent should re-fetch before quoting.
- Linux timestamp granularity and ext4/xfs behaviour on BC-01.
- SQLite backup restart behaviour under a concurrent writer; Python `backup()` was only run single-process.
- Go classifier details for subagents and tool-results (only read via 02-runtime-architecture.md:402).
- Probe caveat: my H1 "proof passes" line reproduces the tautology as I read the design; if the author intended a different comparison (for example re-hashing a window), 7.2 does not say so.
