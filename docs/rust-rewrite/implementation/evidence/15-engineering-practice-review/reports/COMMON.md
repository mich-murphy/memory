# Common rules for every reviewer (read first)

Repository: /Users/mm/dev/memory, branch main, HEAD 41a07ff, clean worktree.

## Hard rules
- READ-ONLY on the repository. Do not create, edit or delete any file in it. No git command
  that changes state. Your only output file is the report path given in your assignment.
- No `cargo build/test/clippy/nextest/mutants`, no `make`, no gates scripts. Seven other
  reviewers run in parallel and builds contend. Use read, `rg`, `wc`, `awk`, `git log/show/blame` only.
- Stay inside the repository. Do not read agent session transcripts (~/.claude, ~/.codex,
  ~/.pi), `.env` files, credentials or anything under ~/.local/state. Never print secrets.
- No network.

## Context
- A Go implementation of "session capture" (agent session files -> Cloudflare R2) is being
  replaced by a Rust rewrite under `rust/`. `DESIGN.md` is the authoritative design.
  `docs/rust-rewrite/design-amendments-20261109.md` (A0 to A13) amends it and WINS where they differ.
- `docs/rust-rewrite/implementation/00..24-*.md` are per-step implementation reports.
  Report 24 was an earlier review of test value, complexity numbers and evidence labels. Do
  not re-report items it closed. You may cite an item it left open if it still exists in code.
- Crates: `rust/crates/memory-core` (pure, no I/O), `rust/crates/memory` (binary: daemon, hook),
  `rust/crates/memory-reader` (stub), `rust/crates/xtask`.

## What the operator asked
Does the work follow sound engineering practice:
 C1 clear boundaries (modules, crates, traits, who owns what)
 C2 ease of extensibility (what does adding the next planned thing cost: a new agent, a new
    source kind, the real S3 store, the reader, Linux)
 C3 human maintainable and easy to reason about (function size, control flow, state, invariants
    carried by types rather than by comments)
 C4 names: variable, type, function and module names clearly and succinctly tell a reader the job
 C5 data structures well chosen and PROVEN beneficial: they reduce surrounding conditional
    flow, reduce code length and are easy to reason about. Flag types that add ceremony
    without removing branches, and flag branchy code that a better type would remove.
 C6 DRY (real duplication of knowledge, not incidental similarity)
 C7 YAGNI (code, abstraction, configurability or generality with no current caller or
    no planned slice that needs it)

## Standard of evidence
- Every finding: read the code at the cited lines yourself. Cite `path:line`. Quote at most
  3 lines. Mark anything you inferred rather than read as [inferred].
- Calibrated, not adversarial. If something is sound, say so with one concrete example. Do
  not manufacture findings and do not pad. Style nits that rustfmt/clippy own are out of scope.
- Prefer a few findings with a concrete, sized remedy over many vague ones.
- Count where counting is cheap (callers of a function, implementors of a trait, variants
  never constructed outside tests, duplicated blocks, longest functions, parameters per function).

## Report format (Markdown, at most ~200 lines)
1. Verdict table: C1..C7 (those in your scope) -> sound / mostly sound / weak, one line of why.
2. What is good (3 to 6 bullets, each with a path:line example).
3. Findings, ranked. Each: id, severity (high/medium/low), criterion, `path:line`, observation
   (fact), why it matters, remedy and rough size (lines / hours).
4. Things you checked and found fine (short list), and what you did NOT read.

Your final chat reply: at most 40 lines. Verdict table plus your top 5 findings with path:line.
The full detail lives in the report file.
