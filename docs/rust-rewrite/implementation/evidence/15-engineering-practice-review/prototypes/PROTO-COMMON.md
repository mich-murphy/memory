# Data-structure prototypes: common protocol (read first)

Main checkout: /Users/mm/dev/memory (branch main, 41a07ff). DO NOT edit, build or commit there.
You own exactly one git worktree (named in your assignment) and one report file. Nothing else.

## Purpose
An engineering review found three data structures that "do not earn their keep". The operator
asked: identify the best options for each, PROTOTYPE them, MEASURE them, and confirm the final
choice from evidence. You do one of the three cases. You recommend; the parent decides and
will rerun your decisive measurements.

## Rules
- Work only inside your worktree. One local branch per option, named `proto/ds-<case>-<letter>`,
  each branched from `proto/ds-<case>-base` (= main 41a07ff), one or more local commits each
  (commit messages in the repository's style, e.g. `memory: ...`). Never push. Never touch main.
- Every cargo command runs in the pinned shell, from your worktree:
  `nix develop /Users/mm/dev/memory#rust --command sh -c 'cd rust && <cargo ...>'`
  (`rust/.cargo/config.toml` already limits jobs to 2 and test threads to 2; keep that. Two
  other workers build at the same time.) Use the default `rust/target` inside YOUR worktree.
- Do not run `scripts/gates.sh`, coverage, or `cargo mutants`; the parent owns full gates.
  Run focused tests (named in your assignment), `cargo fmt --check`, and the clippy command
  exactly as `rust/scripts/gates.sh` runs it.
- Do not weaken anything: no test deleted or loosened to make an option pass, no privacy or
  security assertion removed, no lint `expect` added to hide a problem, no new dependency.
  If an option needs a test rewritten because the public shape changed, rewrite it to assert
  the same behaviour and count the edit.
- No network beyond what `nix develop` already has cached. No R2. Never read ~/.claude,
  ~/.codex, ~/.pi, `.env`, or ~/.local/state. Synthetic data only.
- Read the design text that governs your case before designing options (cited in your
  assignment): DESIGN.md and docs/rust-rewrite/design-amendments-20261109.md (amendments win).
- If an option turns out not to compile cleanly or to be clearly worse, stop work on it,
  keep the branch, and report why. A rejected option with a reason is a result.

## Options
Prototype the BASELINE (no change; measure it) plus the candidate options listed in your
assignment. You may add ONE option of your own if you see a better one; say why.
Keep each option minimal: change the type and its consumers, nothing else. No drive-by fixes.

## Measurements (same table for every option, baseline first)
M1 Correctness: focused tests pass (exact command, pass/fail counts). Property/state-machine
   tests at their default case count.
M2 State space: number of values the type can REPRESENT vs number that are VALID
   (enumerate; ignore payload values, count shapes). List each invalid-but-representable shape.
M3 Conditional flow at the consumers: for each named consumer function, count decision
   points that read the type (`if`, `match` arms, `is_some/is_none/matches!`, `?` excluded),
   and report clippy cognitive complexity for those functions (run clippy with
   `-W clippy::cognitive_complexity` and `CLIPPY_CONF_DIR` pointing at a scratch dir OUTSIDE
   the repo whose `clippy.toml` has `cognitive-complexity-threshold = 1`, so every function
   reports its number). Report wildcard `_ =>` arms and unreachable arms removed or added.
M4 Size: `git diff --shortstat proto/ds-<case>-base` split into `crates/*/src` and
   `crates/*/tests` (use `git diff --numstat` and sum).
M5 Misuse resistance: try to write the specific bug named in your assignment against each
   option. Does it compile? (compile error = prevented by type; compiles = relies on tests.)
   Put the attempt in a scratch file or stash, never commit it; quote the compiler error.
M6 Runtime and memory: the micro-benchmark named in your assignment, written in the existing
   repository pattern: an `#[ignore = "measurement: run with --release -- --ignored --nocapture"]`
   test (see `rust/crates/memory/tests/hook.rs:831`), `std::time::Instant` and
   `std::hint::black_box`, no new crates. The SAME benchmark source must run against every
   option (commit it on base first, then branch options from that commit; adapt only the
   lines the type change forces). At least 7 runs per option, interleaved across options
   (A,B,C,A,B,C,...), report median and min-max. State the noise floor (baseline vs baseline).
   Also `std::mem::size_of` of the type(s). A difference inside the noise floor is "no
   difference": say so, do not rank on it.
M7 Churn and risk: files touched, test call sites edited, public API changed (yes/no, what).

## Decision rule (fixed in advance)
An option is eligible only if M1 passes with no weakened test. Among eligible options prefer,
in order: fewer invalid representable states (M2) and the named bug prevented by type (M5);
then fewer decision points (M3); then fewer lines (M4); M6 only vetoes (a regression beyond
noise on a hot path) or breaks ties. If no option beats the baseline on M2/M3/M5 without
costing lines, say "keep the baseline": that is an acceptable answer.

## Report (your only file outside the worktree; at most ~150 lines)
1. Design constraints you read (section, one line each).
2. Options: for each, the type definition (verbatim, short) and branch name + commit hash.
3. The measurement table M1..M7, baseline first, with exact commands.
4. Recommendation under the decision rule, and what would change your mind.
5. Limits: what you did not run or could not measure.
Final chat reply: at most 30 lines: the table in brief, the recommendation, branch names.
Send a one-line startup acknowledgement of scope and approach before you begin work.
