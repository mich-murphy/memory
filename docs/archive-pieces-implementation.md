# Pieces/delta integration notes

Parent owns storage-layout/golden updates (neither edited here).

## Wire and semantics

- File generation `kind` now includes `append`. Only a cryptographically proven extension
  qualifies. Every immutable parent piece is inherited verbatim with one new tail range.
  JSONL parents must end in newline; arbitrary binary files have no line guarantee.
  Existing live-plaintext holdback/closed-zstd trailing-fragment policies remain intact.
- No age-based folds. At 64 pieces an unchanged append chain retains its first base and
  consolidates the rest into one piece. If the file keeps growing, preparation publishes
  that fold of the acknowledged parent's bytes BEFORE capturing the separate append child.
  The growing capture is never mislabeled a fold, acknowledged history remains immutable,
  and continuous growth cannot produce >64-piece append descriptors. The 130-turn test
  exercises two folds, parent links, base retention, and historical fold restoration.
- Database exports privately stage the consistent backup. Below 8 MiB: one piece. At/above:
  fixed 1 MiB pieces. Whole SHA/length and piece identities come from staged bytes with
  bounded hashing/compression memory. HEAD-validated content-addressed chunks are reused
  across offsets/generations/files; repeated blocks upload once. The 7-to-8 MiB threshold
  intentionally changes representation. Database pieces never trigger append-chain folding.
- New file publication bodies omit `dependencies`, deriving ordered identities from
  `file.pieces`. Nonfile records still serialize explicit dependencies. Existing supplied
  file lists are byte-stable and validated to exactly match pieces. Explicit null/empty
  lists are invalid. Recovery, pending completion, deletion publication and testenv Audit
  use the derived list; restore verifies encoded, decoded-piece and whole-file bytes.
- Recovery checks inherited-piece equality/strict growth for append and unchanged whole
  SHA/size/base retention for fold against the parent, including previously applied records.
  Existing v3 explicit-dependency folds retain their older whole-file representation support.
- Identical bytes with changed role/session/root/head emit a capture reusing pieces without
  blob PUTs. Session moves mark both manifests pending atomically in the state projection.

## Metadata integration

Keep the metadata agent's bounded `content.tail` addition and `sessionHead(sf,c,previous)`
callsite. This commit intentionally retains the original planCaptured SessionInfo callsite.
The two stat skips use sameClassification plus the helper's metadata_version=1 marker.
There is no duplicate session parser. equalHeads uses canonical JSON, treating empty/nil
heads as equivalent because the wire omits empty heads.

## Fixtures and validation

BenchmarkFold now measures a real 64-piece unchanged append chain and retains the base;
its old idle two-piece baseline is not equivalent work. Segmented fixtures save 63 turns
as well as the existing 1/16/62 cases. Failure fixtures build real 64-piece lineage rather
than reducing the production limit. A Fold smoke benchmark passed; no full/comparative
benchmark ran here, since parent owns serial comparisons.

Focused regressions, race-enabled pieces tests, golangci-lint and worktree-local gopls CLI
checks were run via nix develop /Users/mm/dev/memory. The gopls MCP workspace was unavailable;
CLI checks use this worktree. An earlier full make check passed format/lint/fix/tidy/deadcode/
vulnerability checks and reached race coverage testing before the operation timed out.
Golden is intentionally stale. Parent will run complete integrated gates after metadata,
performance and privacy merges. No configs were relaxed and no credentials/production
captures/remote deletion/other-worktree modifications were used.
