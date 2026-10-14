# Executed verification — 2026-10-14

Use the maintained [verification skill](../.agents/skills/verify-agent-sessions/SKILL.md) and its five-feature map to reproduce user-path checks. This report records an executed run, not a claim that every production environment is verified.

## Final end-to-end run

From the checkout root:

```sh
.agents/skills/verify-agent-sessions/verify.py launch
# Save the printed evidence path as RUN after checking launch exited successfully.
.agents/skills/verify-agent-sessions/verify.py doctor "$RUN"
.agents/skills/verify-agent-sessions/verify.py drive "$RUN" all
.agents/skills/verify-agent-sessions/verify.py live "$RUN"
.agents/skills/verify-agent-sessions/verify.py cleanup "$RUN"
```

All commands passed. The run executed **36 command actions**, covering:

- Explicit machine identity, installation/removal, dry-run invariance, foreign settings and retained state.
- Real CLI backfill/sync, byte-exact captures, append/rewrite/empty/deletion history, source/cache loss and rebuilt projections.
- Known-secret redaction, credential exclusion, observable partial capture, persistent opt-outs and later enabled-hook exclusion.
- Synthetic supplied-argv hook dispatch plus existing CLI and supplemental internal recovery regressions.
- Actual Claude Code **2.1.296**, Codex **0.162.1** and Pi **1.1.0**, each executing start/turn/end hooks under the existing macOS sandbox. Final v3 source sizes were respectively **234,854**, **40,397** and **3,362 bytes**, reconstructed byte-for-byte. Only synthetic loopback models and a private local store were used.

After cleanup, the owned instance directory was absent. All **1,515 indexed evidence artifacts** were independently rehashed; permissions remained private and the proof contained no symlinks. Native evidence symlinks were recorded as literal metadata without following their targets.

Local evidence location: `/private/tmp/agent-sessions-proof-6vanocyx`. This is private development evidence, not a portable repository fixture; regenerate it with the commands above and retain it before temporary-storage eviction.

The helper executed had SHA-256 `23513228c56ef3118f5a4b9f703178e0913f05e537948b1334defe08062e6d2f`. Production source was unchanged from `ff67e75`. Sol 6.1/high agents implemented the skill and independently reviewed its safety, archive oracles, feature map and final evidence importer. Earlier failed fixture/import attempts were cleaned up and their failure evidence retained; they were not counted as passes.

## Other validation

- Fresh full `make check` passed, including race/shuffle, coverage **88.3% (4789/5421)**, CRAP, static checks and benchmark smoke. Gate configurations were not weakened.
- Fresh `make fuzz FUZZTIME=30s` passed all **seven** targets.
- The same production source previously passed 200 simulated histories, independent recovery overlays, nine sandboxed native-agent sessions and Linux amd64/arm64 cross-builds.
- Ten-sample performance results and baseline qualifications are in [benchmarks.md](benchmarks.md); backfill costs increased, not a general speedup.

## Boundaries still unverified

No live R2 credentials were supplied for this original local/native-agent run. Subsequently, the operator supplied credentials and the [isolated live R2 acceptance](r2-acceptance.md#executed-live-result) passed, including multipart transfer, archive recovery, durable exclusions and verified cleanup. That is separate backend-plus-engine evidence, not native CLI/hook-to-R2 acceptance or proof of lifecycle policy, full-scale throughput or every network failure mode. Release downloads/upgrades, real-provider authentication/model behavior, actual Codex approval, specialised lifecycle variants and other-OS runtime acceptance remain outside this run. Cross-building is not runtime acceptance. No fresh full mutation campaign was run. See the feature map for entry-point-specific gaps; do not promote unverified entries to passes.
