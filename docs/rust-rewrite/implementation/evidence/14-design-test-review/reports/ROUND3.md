# Round 3 shared brief (read fully, then your task)

You are one of several implementers working in parallel on the Rust workspace of the
`memory` repo. Each of you has your OWN git worktree (your current directory is inside it)
and a DISJOINT set of owned paths. The coordinator integrates your commits afterwards.

## Read first
- AGENTS.md, DESIGN.md sections named in your task, docs/quality-gates.md.
- docs/rust-rewrite/implementation/24-design-and-test-review.md (coordinator brief; do not edit).
- The review reports named in your task. They live in
  /private/tmp/claude-501/-Users-mm-dev-memory/83693829-eddb-4e4e-a499-380d68c821f5/scratchpad/review/
  (read-only for you; reports 01-08, W2-W5).

## Rules
- Edit only your owned paths. If the right fix needs a file you do not own, do not touch it:
  describe the needed change in your final report.
- Work only inside your worktree. Never edit /Users/mm/dev/memory directly or any other worktree.
- Use the pinned Nix dev environment for cargo (docs/quality-gates.md). CARGO_BUILD_JOBS=2,
  one cargo invocation at a time. Other workers share the machine, which is heavily loaded:
  do not add timing-sensitive tests, and never use pkill/killall.
- No new dependencies. No `unsafe`. Do not relax lints, gates, timeouts, case counts or the
  coverage floor. Do not delete privacy/security assertions. No network, no real session data.
- Behaviour changes must match DESIGN.md. Where DESIGN.md is silent or wrong, stop and report
  rather than invent behaviour.
- Simplification standard (operator request): remove accidental complexity where you touch
  code, but only with evidence. Acceptable: splitting a function whose cyclomatic complexity
  is far above 10 into named steps, removing duplication, replacing a bool parameter with an
  enum, deleting dead or single-use indirection, propagating instead of swallowing error
  detail. Not acceptable: speculative abstractions, new traits or generics with one user,
  renames for taste, drive-by rewrites outside your task. Every simplification must leave
  the public behaviour and the tests' meaning unchanged, and you must say how you know
  (tests that pin it, mutants still killed).
- Tests you add must be able to fail: for each new test, name the mutant (a concrete one-line
  change to production code) it kills, and verify by applying that mutant in your worktree,
  seeing the test fail, and reverting. Prefer asserting on observable outputs with literal
  expected values over re-deriving the answer with production logic or constants.

## Verification before you finish
- `cargo fmt --check`; `cargo clippy --workspace --all-targets --locked -- -Dwarnings`;
- the test binaries for the crate(s) you touched, with HEGEL_TEST_CASES=500 PROPTEST_CASES=500;
- do NOT run the full gates.sh (the coordinator runs it once after integration).
- Commit your work in your worktree on its branch (one or a few focused commits, message
  ending with the line `Co-Authored-By: Claude <noreply@anthropic.com>`). Do not push.

## Final message (at most 350 words)
Worktree path and branch, commit hashes, what changed per file, each new test with the mutant
it kills (verified yes/no), each simplification with before/after complexity or line counts,
exact commands run with outcomes, changes needed in files you do not own, and anything
unfinished. No file dumps.
