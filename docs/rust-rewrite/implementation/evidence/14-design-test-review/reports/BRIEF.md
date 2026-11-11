# Shared brief for review agents (read fully before starting)

Repo: /Users/mm/dev/memory. Session-capture tool being rewritten from Go to Rust.
- Authoritative design: DESIGN.md (approved 2026-11-07; section 0 says what to read next). Amendments: docs/rust-rewrite/design-amendments-20261109.md.
- Design evidence: docs/rust-rewrite/ (README.md, experiments/, prototypes/, reports/, implementation/, challenge/).
- Rust implementation: rust/ (crates memory-core, memory, memory-reader, xtask; rust/scripts/gates.sh; rust/README.md).
- Project rules: AGENTS.md. Quality gates: docs/quality-gates.md (use the pinned Nix dev environment for any cargo command, e.g. `nix develop -c ...`, as that doc describes).
- The Go code (cmd/, internal/) is frozen and OUT of scope except as context.

## Hard rules
- This phase is READ-ONLY on the repo: do not edit, delete, commit, stash or create any tracked or untracked file inside /Users/mm/dev/memory (cargo build output in existing target dirs is fine). No git state changes. No live ingestion, no network writes to R2/S3, no installs, no host changes.
- Never print credentials or private transcript content. Do not read ~/.claude transcripts or real session data.
- Put all scratch files and your report under /private/tmp/claude-501/-Users-mm-dev-memory/83693829-eddb-4e4e-a499-380d68c821f5/scratchpad/review/ only.
- Do not spawn subagents. Run at most one cargo invocation at a time; other agents share the machine, so prefer targeted commands (-p <crate>, --test <name>) over whole-workspace runs.

## Standard of evidence
- The operator wants critique from every angle, not confirmation. Assume prior reviews in the repo may be wrong or self-congratulatory; verify independently. Equally, do not invent problems: every finding needs a concrete file:line (or doc section) and a concrete failure scenario or cost.
- External claims ("industry best practice", textbook guidance) must cite an authoritative source you actually fetched in this session (WebSearch/WebFetch): papers, official docs, well-known textbooks, vendor docs. Give URL + the specific statement relied on. If you could not verify, label it "unverified, from memory".
- Separate clearly: (a) verified fact, (b) inference, (c) open question. For each open question, propose a concrete experiment (what to run, what result would settle it), and run it yourself if it is cheap, local and read-only.
- Rate each finding: severity (critical/high/medium/low) and confidence (high/medium/low).

## Deliverable
- Write a full markdown report to the path named in your task. Structure: summary verdict, findings ranked most severe first (each: claim, evidence, source, severity, confidence, recommended action), what is sound and why (briefly, with sources), open questions + experiments, what you did not cover.
- Your final message back: at most 350 words: the report path, top 5 findings in one line each, and open questions needing experiments. No file dumps.
