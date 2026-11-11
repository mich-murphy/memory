# P8. Cross-session duplicate share

**Blocked real-corpus measurement, informational only.** Existing safe research
metadata does not provide session-associated comparable piece hashes and lengths.
No duplicate prevalence was invented. The global raw-hash namespace was already
decided by DESIGN.md 6.2 and is not reopened.

**Read evidence.** Existing policy/batch/manifest experiments calculate offsets
and request/byte aggregates from private JSONL. They do not emit per-piece raw
hashes grouped by source/session. Compression experiment emits compressed sizes,
not safe cross-session hash metadata. The names-only inventory found no metadata
artifact in docs/rust-rewrite; the separate navigation proof README identifies
its corpus as code/source retrieval. Even source-file hashes from that corpus
would not estimate transcript duplication. Its JSON/SQLite/query artifacts were
not opened and private roots were not traversed.

**Required evidence to unblock.** Already-authorized metadata with pseudonymous
session/source identity, piece offset, raw length, raw SHA-256 and segmentation
policy. Count duplicate content only when shared by distinct sessions. Report
both duplicate byte savings and duplicate object count with explicit denominators;
separate within-session repeats, repeated epoch prefixes and shared-store capture.
Whole-file hashes alone can measure only whole-session exact duplicates, not
cross-session piece savings. Piece-boundary dependence and the 8 MiB maximum
must be recorded. No synthetic duplicate fixture establishes actual prevalence.

P8 does not block the already-decided namespace. Missing information is reported
separately from the production acceptance gates.

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
