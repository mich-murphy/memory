# P5. Prefix rewrite frequency

**Blocked measurement.** There is no usable historical same-offset hash metadata
in the inspected session-capture research. No prevalence, zero-frequency claim,
or denominator can honestly be produced within the authorized privacy boundary.
P1 generated rewrite counts are deliberately not substituted for agents.

**Read evidence.** `policy_sim.py` events() reads JSONL source bytes and extracts
timestamps/offsets in memory; its output is policy aggregates. `batch_sweep.py`
and `manifest_cost.py` likewise read JSONL from home-root agent directories and
retain only in-memory event tuples. They do not persist before/after prefix
hashes. `piece_compress.py` reads whole private files to compare compression.
None is safe to execute under this assignment's no-private-content-read rule.
No collector was run or imported. `challenge/01-capture-correctness.md` B1 and
its open questions propose observing prefix changes, not measured prevalence.

**Names-only inventory.** docs/rust-rewrite has 28 frozen source/report files,
no JSON/JSONL/CSV/Parquet/DB candidate. docs/research has 69 artifacts in an
unrelated navigation experiment. Its README says that corpus is source files,
and real archive-derived task judgments are sanitized navigation benchmarks.
Those are not repeated transcript-prefix observations. Candidate JSON/SQLite/
query artifacts were not opened. `research/` does not exist. Scope is repository
research only, not an assertion that no metadata exists anywhere on the host.

**Required evidence to unblock.** Already-authorized metadata needs pseudonymous
source/identity, observation order, captured_to N, length, and both prior and
later hash of the identical [0,N) prefix, plus independent whole/audit outcomes
for interior rewrites. Define transcript/source denominator and observation
interval and distinguish identity replacement/truncation. A growing file's full
hash changing does not establish prefix rewrite. A single current digest or
record timestamps cannot reconstruct historical bytes. Collection would need
separate authorization, not a reason to read private sources here.

This remains a genuine slice-1 prerequisite acceptance gap for the parent.
No design windows or rates changed.

## Inspection commands and source identity

Repository HEAD `57d8400f632ab47a81e34d060f86d3687123153e`.
Safe metadata inventory command, exit **0**:

```sh
/nix/store/0jb1qn7qnpbz8774mq5k2c1p84gp0x76-python3-3.14.8/bin/python3 -I docs/rust-rewrite/prototypes/metadata_inventory.py > docs/rust-rewrite/prototypes/metadata-inventory.json
```

Inventory reads names only under repository docs/rust-rewrite, docs/research,
and research. It excludes prototypes and implementation output. It does not
execute frozen collectors or inspect candidate JSON/SQLite contents. One thread,
no network, no private roots. Inventory script SHA-256
`d22abc43bd32d29c62ecfa28e258db2412fea50b81b644e9f10c9e5d50c0dee4`.

Source/doc inspection used `sed -n` on the files below and `find docs -type f`
with hash/offset/corpus/duplicate/timeline filename filters. These commands
exited 0. `find research ...` separately exited 1 because that directory does
not exist. No private transcript contents were read. Frozen source SHA-256:

- `docs/rust-rewrite/experiments/policy_sim.py` `c6b93a08ec02282a4e8419f1d8f261706ab082c02a42a38fbdca7268deb31104`.
- `docs/rust-rewrite/experiments/batch_sweep.py` `5557826fb7537dabf1c03e5215e5a175766fd159b791c60128df4eea3dcd29de`.
- `docs/rust-rewrite/experiments/manifest_cost.py` `d1cc2310f2048aa9b6837a374e57b887d7b117e8b68884fd0fb4cb21838ce638`.
- `docs/rust-rewrite/experiments/piece_compress.py` `86b45aed21924c9c3df068d43042e171b98570e78a8beb662b5ab5764d42c1f9`.
- `docs/rust-rewrite/README.md` `f962145630b519f79d4f67cd48fbff5eee9ade40dc6a4e055ff4e626a1b5597d`.
- `docs/research/agent-navigation-context-graphs/README.md` `b5c0385f17fc5964d2652d89bfb7d9d4f0e28ad4c568542fb01ddf54c8d97332`.

**Waived 2026-11-09 (operator decision; [DESIGN amendment A1](../design-amendments-20261109.md)).** Slice 1 is not held on P5. The rewrite frequency is measured from the running daemon's replay counters.
