# 47. Slice 3, part 3: Claude file-history behind a fail-closed content scan (2026-11-21)

Coordinator brief. The parent session owns this file and acceptance. It follows D54 (operator,
2026-11-21), briefs 45 and 46 and handoff item 22. Evidence goes to `evidence/41-file-history/`.
Text that states a plan stays as written; later changes are marked.

Production daemons run on this Mac (launchd) and on dev-01 (systemd), both on the `89b155e` build,
while this brief is worked on. Nothing here touches `~/.local/state/memory*`, `~/.codex`,
`~/.claude`, `~/.pi`, `~/Library`, the installed binaries, agent settings, `.env` or R2.

## Why

D54: Claude `file-history` is captured, but only behind a content scan that withholds the whole
file, with a recorded reason, on a secret match, binary or non-UTF-8 content, an oversize file or a
scanner error. The local file is never touched. File-history holds whole pre-edit copies of
project files Claude edited (`~/.claude/file-history/<session>/<16 hex>@v<N>`). Its names are
hashes, so no path denylist can see a `.env` or a key file inside it. The research report
(2026-11-21, metadata only) measured 946 files / 10.9 MB on this Mac (p50 6 KB, max 152 KB, no
empty files, 100 % `<16 hex>@v<N>` names, 80 of 80 session folders with a transcript) and 514
files / 9.5 MB on dev-01. Files are immutable: a new version gets a new `@vN`.

## Facts checked on `6f5139f`

| Fact | Where |
|---|---|
| `file-history/<sub>/**` is `ClaudeFileHistory`, class `Snapshot`, owner the session found by `claude_sessions.lookup(uuid prefix of sub)`, else `UnknownOwner` | `memory-core/src/locator.rs` (`claude`, `claude_external`) |
| The walk yields `CodexStore` snapshots and counts every other snapshot kind as `deferred_snapshot` | `memory/src/resolve.rs` (`enumerate`, the `Located::Source { class: Snapshot, .. }` arm) |
| `profile_of(ClaudeFileHistory)` is `OtherKnown`: no text check, `prefix_stable` true, so it would be streamed and published unscanned | `memory/src/resolve.rs` (`profile_of`), `memory-core/src/privacy/mod.rs` (`prefix_stable`, `transform_owned`) |
| `text::check` is a shell-assignment guard for config and text assets. It returns `Ok` for input over 1 MiB, with a NUL byte or not UTF-8: the opposite of fail-closed | `memory-core/src/privacy/text.rs` (`check`) |
| `publishable_owned` turns a transform `Reason` into `SourceOutcome::Withheld(WithholdReason)` before any piece is built; the whole-input path (`write_whole`) reads, authorizes, then slices | `memory/src/capture.rs` |
| The daemon counts `Parked` and `BackupFailed` outcomes; a `Withheld` outcome is counted nowhere and the index has no slot for it, so a withheld snapshot would be read and rescanned every round without a trace | `memory/src/daemon.rs` (outcome loop, `SourceIndex`) |
| `memory status` counts as `withheld` only sources that admission denies; `memory check` reports `withheld owner=… kind=… reason=<code>` from the transform and does not fail on it | `memory/src/status.rs` (`classify`), `memory/src/check.rs` |
| The snapshot path from brief 45: plain fingerprint, planner `Strategy::Snapshot`, `Mode::Replace`, piece reuse by digest; the 6 h interval applies only to `SnapshotFingerprint::Sqlite` | `memory/src/capture.rs` (`snapshot`) |
| The D52 absence probe covers append sources only (`Candidate`); a deleted snapshot source is never recorded as deleted | `memory/src/daemon.rs` (`candidates`) |
| `rules_digest()` hashes `TRANSFORM_VERSION` and the document rules; admission requires it for `Config`/`TextAsset` and for recognised documents | `memory-core/src/privacy/mod.rs` |

## Decisions

Each is reversible before merge. A worker that finds one wrong reports it and does not decide.

| Topic | Decision |
|---|---|
| Scope | Only `ClaudeFileHistory` leaves `deferred_snapshot` and is yielded. Every other snapshot kind (config, Claude aux and memory, Pi aux, todos, tasks, session-env) stays deferred |
| Profile | A new `Profile::Scanned`, used for `ClaudeFileHistory` only. It is never prefix-stable (the whole file is judged at once), it is bound in `binding()` with its own byte, and admission requires the current rules digest as for `Config` |
| Scanner | A new module `memory-core/src/privacy/scan.rs`, run by `transform_owned` for `Profile::Scanned` on the whole file, and nothing else. Order, first hit wins: (1) longer than 1 MiB: `SourceLimit`; (2) a NUL byte or not valid UTF-8: a new `Reason::Binary`; (3) a secret pattern below: `SecretInText`; (4) any pattern that fails to compile or any internal error: `ScrubFailed`. A clean file passes through byte for byte (no redaction: the whole file is published or withheld) |
| Secret patterns | A fixed table, each pattern with a one-line comment: PEM private-key headers (`-----BEGIN [A-Z ]*PRIVATE KEY-----`, OpenSSH, PGP private block); AWS access key ids (`AKIA`/`ASIA` + 16 upper alphanumerics); GitHub tokens (`ghp_`, `gho_`, `ghu_`, `ghs_`, `ghr_`, `github_pat_`); Slack (`xox[abprs]-`); Anthropic and OpenAI keys (`sk-ant-`, `sk-proj-`, `sk-` + at least 32 key characters); Google API keys (`AIza` + 35); Stripe live keys (`sk_live_`, `rk_live_`); JWTs (`eyJ…\.eyJ…\.`); credentials in a URL (`://user:password@`); the existing shell-assignment guard (`text::check`'s line rule, reused, not copied); and a generic assignment: a secret name (`names::is_secret_name`) followed by `=` or `:` (optionally quoted, YAML, JSON, TOML, env and source-code forms) and a literal value of 8 or more characters that `names::reference` does not accept. False positives are accepted: the cost of a wrong withhold is one missing pre-edit copy, which the transcript and git usually hold |
| Rules digest | The scanner table and the 1 MiB cap are part of `rules_digest()`; a changed table changes the digest, which re-judges every scanned file at the next round. Report every other effect of the digest change (stored epochs, `rules` fields, tests) |
| Oversize read | The size check uses the stat before any read: a file larger than the cap is withheld without reading it. Memory per file is therefore at most 1 MiB plus one piece |
| Withheld record | `SourceIndex` gets `#[serde(default)] withheld: Option<(Fingerprint, Option<RulesDigest>, WithholdReason)>`. The same fingerprint and rules give `Unchanged` without a read; a new fingerprint or rules judge again. A file that later passes clears the record. An index written before this field loads with `None` |
| Counting | The round line gets `withheld=<n>`: sources judged and withheld in that round (a cached withhold is not counted again). `memory status` counts a source with a withheld record as `withheld`. `memory check` keeps printing `withheld … reason=<code>` and its exit codes do not change. The reason codes are the closed `WithholdReason` spellings; no path, file name, match or content is ever printed or logged |
| Manifest | Unchanged: a withheld version is absent from the manifest. Recording withheld entries in R2 needs a schema change and is not part of this brief |
| Interval | None. File-history files are immutable; the 6 h interval stays SQLite-only |
| Deletions | Unchanged (append only). A file-history file that Claude's cleanup removes stays live in the manifest; `memory check` counts it as `remote_only`, which does not fail. Snapshot deletions remain the open finding |
| Export and restore | `restore` writes `file-history/<session>/<name>` back. `export` keeps a file-history source as role `artifact` (as `export_role` already says); the consumer gate must stay green with file-history present in an exported session |

## Increment

| # | Increment | Branch | Owner |
|---|---|---|---|
| 1 | FILEHISTORY | `rust/file-history` | worker (Sonnet, medium) |

Owns `memory-core/src/privacy/{mod,scan,text,names}.rs` (text and names only to share a rule, not
to change its result), `memory-core/src/domain.rs` (only `WithholdReason`),
`memory/src/{resolve,capture,daemon,status,check,log}.rs`, `memory-core/tests/**`,
`memory/tests/**`, `memory-reader/tests/**` (only for the export check), `rust/schemas/**` (only if
the schema gate requires it; report it), `docs/install.md` (only if an operator-visible line
changes). Read-only everywhere else.

Ordered steps, one commit each; `cargo fmt`, clippy `-Dwarnings` and the focused tests green before
each commit:

1. The scanner: `Profile::Scanned`, `scan.rs`, `Reason::Binary`, `WithholdReason::Binary`, the
   rules digest. Table tests, one row per pattern (a hit) and a near miss each (a placeholder such
   as `API_KEY=${API_KEY}`, `token: <redacted>`, a 7-character value, `sk-` with too few
   characters); a file of exactly 1 MiB passes and 1 MiB + 1 is `SourceLimit`; a NUL byte, an
   invalid UTF-8 sequence and a lone surrogate encoding are `Binary`; a clean file passes byte for
   byte. A property: for generated clean text with one secret inserted at a random position, the
   scan withholds; for generated text from an alphabet that cannot form any pattern, it passes and
   the output equals the input.
2. The walk yields `ClaudeFileHistory` with profile `Scanned`; every other snapshot kind stays
   deferred. Capture: a scanned source goes through the whole-input path; a withheld file PUTs
   nothing (count PUTs on the fake store) and builds no change; the oversize check happens before
   the read (count reads on the seam).
3. The withheld record, the round line `withheld=`, `memory status`. Tests: a withheld file is read
   once over three rounds; a changed fingerprint is judged again; a file that becomes clean lands
   and clears the record; an index without the field loads.
4. Real binary against the fake S3 store: one session with a transcript and three file-history
   files (clean, secret, binary). `memory reconcile` lands the clean one only; the round line shows
   `withheld=2`; `memory status` shows `withheld=2`; `memory check` exits 0 with two `withheld`
   lines and reason codes `secret_in_text` and `binary`; `memory restore` of the session writes the
   transcript and the clean file only; `memory export` of the session keeps the consumer gate green
   (`MEMORY_REQUIRE_CONSUMER=1`). Neither the daemon log nor any output contains a file name,
   the secret or a content byte (assert on the captured output).
5. Module docs.

## Rules for the worker

- Cargo only via `nix develop /Users/mm/dev/memory#rust --command sh -c 'cd <worktree>/rust && …'`,
  one cargo job at a time. Combine nextest `-E` conditions into one expression.
- No `unsafe`, no new crate (`regex` is already in `memory-core`).
- Never touch `~/.local/state/memory*`, `~/.codex`, `~/.claude`, `~/.pi`, `~/Library`, the
  installed binary, `.env` or R2; do not list or read real agent directories. Use temporary
  directories. Test secrets are synthetic and obviously fake (for example `AKIAEXAMPLEEXAMPLE00`),
  built in the test from parts where a scanner of this repository would otherwise flag the source
  file. A production daemon runs on this Mac: do not inspect or signal it. No `git stash`. Never
  commit `.proptest-regressions`.
- No gate is loosened: no timeout raised, no test deleted or weakened, no coverage floor lowered.
  An existing test whose expectation changes because of a decision above is updated with a comment
  naming D54 or this brief; list each one in the report.
- If a decision looks wrong or cannot be implemented cleanly, stop on that point, report it with the
  reason, and do not decide it yourself. Do not edit the brief, DESIGN.md or the handoff.
- Do not run `scripts/gates.sh` (the parent runs the full gates). Do not push.
- Report: commits, deviations with reasons, changed existing test expectations, exact commands with
  test counts, every effect of the rules-digest change, `git status --short`.

## Acceptance (parent)

Read the whole diff. Teeth: the scanner passes binary; passes non-UTF-8; reads an oversize file;
misses a PEM header; misses an AWS key id; misses the generic assignment; passes on a regex compile
error; `Scanned` prefix-stable; the scanner table left out of the rules digest; a withheld file
PUTs a piece; the withheld record ignored (rescanned each round); `withheld=` not counted; the walk
still defers file-history; another snapshot kind yielded; a file name or match in a log line. Then
the full gates once, the Linux container run of the privacy and resolve tests, the hosted gates,
evidence 41, DESIGN (8, 9, 19 default 3, D58), the handoff; merge; install on this Mac and dev-01;
record the first round (files landed, withheld by reason, bytes uploaded, peak memory), `memory
check` on both hosts, and a restore of one session with file-history into a temporary directory
compared by size and SHA-256 with the local files (counts only).

## Ledger

- 2026-11-21 05:20Z: brief written on `6f5139f`.
- 2026-11-21: FILEHISTORY accepted locally at `68043e3` (5 worker commits, 3 parent commits). The
  parent found three gaps: a cached withhold skipped the absence-count reset (`1359290`), a secret
  assignment nested in another value was never judged (`6e70050`), and no test pinned the reused
  shell guard (`68043e3`). Teeth 23 of 23; gates 14/14, seed 3887993034, nextest 869, coverage
  95.58 %; Linux container privacy, capture, daemon and resolve tests passed.
  [Evidence 41](evidence/41-file-history/). Next: hosted gates, merge, install on both hosts, the
  first round measured, `memory check`, restore of one session compared with the local files.
- 2026-11-21 06:53Z to 07:28Z: merged as `36adb8c` (stack #230) and installed on this Mac and
  dev-01. First round: 805 of 946 and 419 of 514 file-history files landed; 141 and 95 withheld,
  all `secret_in_text`; `deleted=0`. `memory check` exit 0 on both hosts. Restore of three
  sessions: 57 of 58 files equal by size and SHA-256, the missing one withheld. Exit criteria met.
  Finding: no withhold came from a fixed pattern, and more than half look like code-shaped false
  positives (evidence 41); narrowing the rule is an operator decision.
