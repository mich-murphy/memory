# Evidence 38: slice 3 snapshot scope checked against evidence (D54, D55)

Dates 2026-11-20 and 2026-11-21 (UTC). Two research subagents (Sonnet), each checked by the parent.
Only metadata, counts and in-memory byte or hash comparisons were used. No content of a transcript,
paste, image, attachment or database row was printed, and no file name, id or hash is recorded
here. The working reports stayed in session scratchpads; the results that matter are below.

## Claude `file-history` (D54)

Operator: "research recommended practice and determine whether this is worth capturing".

- Documented (Anthropic, `code.claude.com/docs/en/claude-directory.md` and `checkpointing.md`):
  `file-history/<session>/` holds pre-edit snapshots of files Claude changed, used for checkpoint
  restore (`/rewind`); only edits made by Claude's file-editing tools are tracked; snapshots are
  swept after `cleanupPeriodDays` (default 30); everything under `~/.claude` is plaintext.
- Measured: this Mac 80 session directories (all with a transcript), 946 files, 10.9 MB, every name
  `<16 hex>@vN`, files never rewritten; dev-01 30 directories, 514 files, 9.5 MB. Both hosts set
  `cleanupPeriodDays` to 3650.
- Conclusion: the transcript is the record of what a session did; file-history adds the exact
  pre-edit file state. It is a raw copy of project files named by hash, so the path-based
  credential denylist cannot see it. Decision (D54): capture only behind a fail-closed content
  scan that withholds the whole file, with a recorded reason, on a secret match, binary or
  non-UTF-8 content, an oversize file or a scanner error.
- Not verified: whole file rather than diff (inferred from the docs and sizes); that the
  hash-to-path map is in the transcript.

## Codex `generated_images`, Codex `attachments`, Claude `paste-cache` (D55)

Operator: challenge the D54 decision to capture these, with best practice and verified evidence.
The first report rested on two inferences and incomplete checks; it was sent back. The second pass
re-fetched every source, matched every Codex source file by blob hash at tag `rust-v0.170.0`, and
measured redundancy by byte and hash comparison. The parent reran the measurements with its own
script; the counts below are the parent's and agree with the second pass.

| Item | Files (this Mac) | Found elsewhere | Where |
|---|---|---|---|
| Claude `paste-cache` | 59, 0.47 MB | 59 | Claude project transcripts (raw, CRLF-normalised or JSON-escaped bytes) |
| Codex `generated_images` | 4 PNG, 7.5 MB | 4 | base64 inside `thread_history_1.sqlite` `item_json` |
| Codex `attachments` screenshots | 5 PNG | 0 | no rollout, no `thread_history_1` copy (by hash or base64) |
| Codex `attachments` text | 2 | 1 | `pasted-text.txt` in `thread_history_1`; the JSON index nowhere |

- Documented: Claude docs, `paste-cache/` is "Contents of large pastes", and a collapsed paste is
  still sent in full on submit (`terminal-config.md`). All 59 cache file stems equal a
  `contentHash` in `history.jsonl`, so the cache backs up-arrow recall only. Codex source:
  `artifact.rs` writes `generated_images/<session>/<call_id>.<png|gif>` as a decoded copy of the
  `ImageGenerationCall` result, which `tool.rs` also emits inline as a base64 data URL;
  `attachment_files.rs` creates `attachments/<uuid4>/`.
- Best practice (re-fetched): GDPR Art. 5(1)(c) "limited to what is necessary" and 5(1)(e) "for no
  longer than is necessary"; NIST SP 800-122 "Reduce PII holdings to the minimum necessary"; NIST
  Privacy Framework CT.DM-P (data management, names data minimisation); OWASP Logging Cheat Sheet
  on excluding or masking tokens. Comparable tools (claude-code-log, SpecStory, Claude `/export`)
  work from the transcript and do not copy these side folders.
- Verification of the second pass: 34 claims, 28 yes, 3 partial, 3 no. Weak points: ISO 27002 8.13
  has no usable quote and 8.10 only a secondary summary; Codex history exporters not checked; the
  generated-image base64 in a live rollout is inferred from source (the two image sessions here
  are among the 873 whose rollouts are gone, handoff item 18); "nowhere" means no byte-identical
  copy, not that no re-encoded copy exists.
- Decision (D55, operator 2026-11-21): none of the three is captured. `paste-cache` and
  `generated_images` are duplicates of captured or to-be-captured data; the attachment screenshots
  and JSON index are given up (option d), accepting their loss on restore. Consequence:
  `generated_images` depends on slice 3 capturing Codex `thread_history_1.sqlite`.
