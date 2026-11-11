# Design challenge of DESIGN.md (2026-11-07)

Parent: the coordinating Opus session. Workers: six Sonnet reviewers, read-only on the
repository except for their own report file below. Acceptance of findings stays with the
parent. Purpose: challenge DESIGN.md against industry practice and its own evidence before
slice 0 starts. Find gaps, wrong premises, and places where established systems do it
differently and why.

## Shared rules for every reviewer

- Read `DESIGN.md` in full first, then `docs/rust-rewrite/reviewer-brief.md` (report style
  rules apply), then the parts of `docs/rust-rewrite/reports/` and
  `docs/session-capture-contract.md` relevant to your area.
- Your job is adversarial. Assume the design has defects and look for them. "The design is
  fine here" is an acceptable finding only with evidence that you tried to break it.
- Compare with how established systems solve the same problem. Name the system, the
  mechanism, and a source (vendor docs, source code, paper, RFC, man page). Use websearch
  and webfetch for current vendor docs and crate docs. Cite URLs.
- Check the design's own [vendor] and [measured] claims where your area depends on them.
  Say whether you confirmed, contradicted, or could not check each one.
- Label every claim: read (file:line or URL), measured (you ran it), inferred, guess.
- You may write small probes or scripts only under the scratchpad directory named in your
  own environment, in a subdirectory named by your reviewer id, using synthetic data. Do not read transcript content. Do not touch R2, BC-01, `~/.config`,
  `~/.local`, launchd/systemd, installed hooks, or any live service. No global installs.
  Do not edit any repository file other than your own report.
- Report structure: Verdict (3 to 6 lines), Findings ranked by severity (Blocker, Major,
  Minor), each with: claim in DESIGN.md (section), the problem, evidence, what industry
  does, recommended change. Then "Claims checked", "Open questions for the operator",
  "Things I could not verify". Keep it under about 300 lines.
- Reports go in `docs/rust-rewrite/challenge/<report>`. Write a skeleton report within your
  first few actions (this is the parent's startup evidence), then keep it updated as
  findings land so the parent can inspect progress and steer.
- Prioritise depth over breadth: three well-evidenced Blockers or Majors beat twenty
  generic suggestions. Do not recommend adding complexity without saying which measured
  risk it removes (DESIGN.md R8).

## Assignments

| Id | Area | Report | Worker |
|---|---|---|---|
| A | Change-capture correctness: prefix proof, epochs, offsets, torn lines, file identity, snapshot classes | `01-capture-correctness.md` | 6fd9a705-1cf1-462 |
| B | R2 layout, content addressing, manifest/current.json protocol, object_store, Content-MD5, cost | `02-storage-protocol.md` | 36bab9ca-a788-436 |
| C | Signals and daemon: hooks, hint log, notify/FSEvents/inotify, sleep, supervision, reconcile | `03-signals-daemon.md` | 3cf24141-5ca3-43c |
| D | Failure handling, durability, observability, operations, upgrades | `04-failure-ops.md` | 07f7eacd-62b1-4b3 (queued) |
| E | Privacy and security: fail-closed policy, secrets, encryption, deletion, threat model | `05-privacy-security.md` | 200d363e-eb4a-40a (queued) |
| F | Rewrite strategy, scope, Rust engineering, testing, delivery slices | `06-strategy-engineering.md` | 6f298959-b495-4a4 (queued) |

## Ledger

- 2026-11-07: brief written, six Sonnet workers dispatched at medium thinking. The tool runs
  at most 3 concurrently; D, E, F queued. Live status and steering are available through
  get_subagent_result and steer_subagent. Synthesis goes to `00-synthesis.md` (parent-owned).
- A completed. Parent verified B1 against DESIGN.md 7.2 text: the resumed digest is compared
  with the stored state, so the check cannot fail on in-place rewrites. Accepted. A had no web
  tool; its industry comparisons are recalled and need re-fetching before citation.
- Workers lack websearch/webfetch; curl reaches public docs. B, C, D steered to use curl.
- B completed. Parent verified B1 (no Content-MD5 in object_store 0.14.2 `with_attributes`,
  src/aws/client.rs) and B2 (Update not idempotent, retry_on_conflict true, status retry
  branch ignores idempotence, src/client/retry.rs ~405-412, src/aws/mod.rs ~274-300). Accepted.
- C, D, E, F completed. Parent verified E-B1 against internal/scrub/scrub.go:94-106 and
  internal/launch/native.go; C-M2 against notify 8.2.0 fsevent.rs:299 (SinceNow hard-coded).
  Parent corrected E-M2: R2 temporary credentials do support prefix and action scoping
  (fetched docs). Probes by A and F ran in stale scratchpad paths; F also wrote and removed
  /tmp/__f_hint.jsonl. No repository file other than reports was touched.
- Scope breach: reviewer C wrote its synthetic rotation probe `rot.py` into the repository
  root (session transcript confirms). Parent removed it; it was not present at session start
  and left no other files.
- Synthesis written to `00-synthesis.md`.
- Operator answers (2026-11-07): one bucket shared by both hosts, for a single view of all
  sessions. Individual sessions will never be deleted. A minimal reader and `memory restore`
  are in scope. Session exclusion is not required, which is pending the operator's
  confirmation of what dropping it removes. Encryption and the manifest shape are waiting on
  the parent's explanation. Blockers are to be checked against DDIA 2nd edition chapters the
  operator will supply. After the design is final, Sonnet workers prototype the alternatives
  for each Major and new callout, and the evidence decides between them.
- Parent asked the operator for DDIA 2e chapters 9, 8, 13, 6, 4 and 14, with 5, 2 and 10
  optional. Blockers 2 and 10 have no DDIA basis: one is a testing-oracle defect and the
  other is an AGENTS.md acceptance rule. (Corrected later: both have a basis in ch. 9 and
  ch. 13.)
- Operator supplied DDIA ch. 9, 8, 13, 6, 4 and 14. The parent did the cross-check directly
  rather than delegating, to avoid copying book chapters into worker files. Result is in
  `07-ddia-crosscheck.md`: 9 blockers supported (5 refined), blocker 5 (Content-MD5)
  downgraded to Major by the end-to-end argument, and 8 new callouts, including
  deterministic simulation testing and the choice of clock across sleep. Still waiting on
  the operator: encryption, manifest shape, session exclusion. Next: the DESIGN.md revision
  once those land. Chapters 11 and 12 are already quoted in DESIGN.md. Measured the manifest cost
  (`experiments/manifest_cost.py`). Explained encryption, manifest shape and session
  exclusion. Next: operator answers, the DDIA cross-check, then the DESIGN.md revision. Next: operator decisions, then DESIGN.md revision.
- Scratchpad paths: the brief names none because each session's path differs; workers use
  their own environment's scratchpad.
- Operator decisions, 2026-11-07: manifest shape B (delta plus checkpoint every 64);
  session opt-out retired (README.md, AGENTS.md, contract section 6 step 2 and
  docs/security.md edited; the one existing record is removed at cutover); encryption (a)
  plaintext; none of the existing R2 data kept or migrated.
- Parent reran `experiments/manifest_cost.py`: 4,650 sessions, 27,683 captures, 4.01 GB;
  full rewrite 457 MB (11.4%), delta 29 MB (0.7%).
- DESIGN.md rewritten to final. Section 16 lists the prototypes (P1 to P10) for Sonnet
  workers; section 17 the slices; section 20 maps every finding to a section. Handoff,
  docs/README.md and AGENTS.md point at it. Next: slice 0 and the prototypes that need no
  R2 (P1, P3 to P8). External prerequisite: an operator-created acceptance bucket and
  token for P2, P2b and slice 2.
