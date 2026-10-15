# Capture and archive recovery

A user uploads old or changed sessions, retains every acknowledged historical generation, and resumes the same archive after losing local sources and the disposable cache.

## Sub-features

- `backfill-all` native-shaped Claude/Codex/Pi fixture files reach one Local archive.
- `sync-unchanged` unchanged sweep uploads zero files.
- `sync-target` each agent's explicit path uploads its append, byte-exactly.
- `history` rewrite and empty capture remain reconstructible beside earlier bytes.
- `recover-cli` source/cache loss, same identity/destination, real sync rebuilds state and adds deletion without deleting history.
- `restore-api` existing internal tests supplement user recovery; no public restore command.

## How to get to it (user POV)

- `agent-sessions backfill`, optionally repeated `--agent NAME`.
- `agent-sessions sync`, optionally repeated `--agent NAME`, `--path FILE`, `--delay SECONDS`, `--quiet`.
- Installed hooks request targeted sync and lifecycle sweeps; see hooks map.
- After SQLite loss, keep identity/destination and run real `sync` or `backfill`; restore a known identity before uploads if it was also lost.

## Driving it with verify.py

Preconditions:

- Fresh launched run, helper doctor passes, no capture attempt. `all` may run setup first; synthetic native fixtures are created automatically with cwd owned work.

- **All agents.** Run `"$H" drive "$RUN" capture`. It writes native-shaped fixtures then real `backfill`; committed generations reconstruct to exactly each source's bytes/size/SHA-256, including canonical Codex rollout key.
- **Changed/unchanged.** Same journey invokes sweep `sync` expecting zero uploads, appends each source, then `sync --agent NAME --path ABSOLUTE_SOURCE`. Encoded and decoded piece/whole hashes and exact bytes are asserted.
- **History.** Same journey rewrites Pi and then writes a real empty source, targeted syncing each. Capture evidence is retained before source/cache loss. Initial/appended/rewritten/empty history must exactly match independent fixture bodies. A self-hashed sequence-999 orphan is injected then removed in a finally block; unique-chain selection must reject it.
- **Recovery.** Same journey removes that source and entire state directory, then invokes real `sync`. Unique archived bytes are reconstructed independently from committed pieces; deletion tip retains content; history must exactly equal the known fixture/action bodies plus deletion-retained empty; all commit hashes acknowledged after each capture remain identical; rebuilt SQLite has committed projections.
- **Supplemental API.** Run `"$H" drive "$RUN" regression` (or `all`) for existing history-after-source/cache-loss and streamed Restore corruption tests. These are internal supplemental evidence, not a CLI restore journey.
- **Proof.** Inspect actions, pre-loss/final state snapshots, source and reconstructed hashes, sequence/kind observations and recovered row count.

## Gotchas

- Lost cache is not a reason to generate a new machine ID. There is no auto discovery of unknown identity by hostname.
- Helper independent decoding validates fixture bytes, not all fork/hostile-store behavior. Existing internal boundary tests and gates cover additional failure seams.
- Backfill's selected-agent entry, repeated paths/agents, quiet/delay manual flags, folds, live SQLite WAL exports, zstd source recompression, external roots and partial plaintext lines are not all automated here. Do not claim them passed from all-agent backfill.
- Local backend is the production boundary stand-in; this journey does not verify real R2. Separately authorised backend-plus-engine R2 coverage and its executed result are documented in [r2-acceptance.md](../../../../docs/r2-acceptance.md); it does not establish every network failure, lifecycle policy or full-scale throughput. Runtime on one OS does not prove another OS.

## New-design CLI acceptance

The regression drive also requires seven `TestAcceptance*` CLI journeys: append
kind/one-tail/idle invariance, 64-chain consolidation retaining the first base,
Pi child membership and late model metadata, decoded-archive literal guard and
noise/provenance controls, regular symlink capture, and live-WAL fixed-1-MiB
changed-chunk reuse with cold history recovery. These are unconditional assertions,
not skips when implementation is missing. See [acceptance-review.md](../../../../docs/acceptance-review.md)
for commands and explicit pending integration/gaps. FILE dependency omission is
accepted; any legacy explicit dependency array remains strictly checked against
pieces. No acceptance is established until the integrated journeys pass.
