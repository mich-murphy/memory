# Pre-evaluation validation / frozen candidate

- Final synthetic suite: **16 tests passed**, 1.994 seconds locally.
- Interpreter: Python 3.14.7; SQLite 3.53.3; FTS5 creation verified.
- Final real build completed in 28.1523 seconds against HEAD
  `e4fbe8f3768b3348c349667f37f500b478950fed`.
- Indexed 30,788 files, 3,586 directories and 174,865 edges.
- Recorded 310 unresolved/excluded Markdown links; 2,109 total extraction
  diagnostics (also includes unsupported/missing sourceRefs). No target guesses
  were substituted for these diagnostics.
- Generic `repository navigation` hybrid smoke query succeeded: 3 results,
  1,401 UTF-8 bytes including newline, under a 9,600-byte cap.
- Actual lockfiles are excluded by known names/suffixes, not `lock` substring;
  tests retain legitimate `lock.rs`, `clock.ts`, `agents.ts` and `skills.md`.
- Instruction-file exclusions include AGENTS.md, CLAUDE.md, SKILL.md and disabled
  variants; forbidden contents are not opened during inventory.
- `FREEZE.json` records SHA-256 of final algorithm, routes, human map, README
  and tests. These candidate algorithms are now frozen before evaluation.
- No benchmark task file, relevance judgments or benchmark outcomes were read.
  No full benchmark or relevance-driven tuning was run by this worker.
- Runtime DB and temporary validation outputs remain in ignored `.cache/`.
  Only this prototype directory was written. Coordinator handles evaluation,
  independent review and any separately labelled production adaptations.
