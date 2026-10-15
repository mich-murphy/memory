---
name: verify-agent-sessions
description: "Verify agent-sessions through its real CLI, isolated local-store archive, existing end-to-end scripts, and optional sandboxed Claude Code/Codex/Pi acceptance. Use before claiming collection, installation, recovery, or privacy behavior works."
---

# Verify agent-sessions

Read [features/README.md](features/README.md) first. The primary surface is the short-lived Go CLI, not a server. Its installed hooks and Pi extension are secondary entry points. There is **no public restore CLI**; `Syncer.Restore` is an internal API. This skill does not introduce one.

## Launch

From the repository root, use Python **3.14+ with `compression.zstd` and `sqlite3`**, an installed Go toolchain satisfying `go.mod`, and a populated trusted Go module cache. No Python packages or downloaded temporary tools are needed. The helper selects the exact already-cached `toolchain` from `go.mod`, otherwise the installed PATH Go; `--go /absolute/path/to/go` selects one explicitly. `--module-cache /absolute/cache` overrides the default `~/go/pkg/mod`. Builds set `GOPROXY=off`, `GOSUMDB=off`, `GOTOOLCHAIN=local`, `GOENV=off`, and `-mod=readonly`: missing tools/dependencies **fail**, never silently download. Provision tools/cache separately if needed; record that unmet precondition, then launch a fresh run.

```sh
.agents/skills/verify-agent-sessions/verify.py launch
```

The first stdout line is the canonical absolute **evidence directory**, for example `/private/tmp/agent-sessions-proof-<random>`. Save that exact line as `RUN`; do not guess a previous run's path. You may instead specify a new absolute evidence directory with `launch --artifacts /absolute/new-private-proof`. Existing directories are refused. Both default proof and scratch are independent private directories under the system `/tmp`, outside the checkout. Do not use command substitution around launch followed by doctor: launch can print a recovery path then fail. Check its exit status first.

Launch creates an empty scratch HOME/workdir/config/state/bucket, compiles the real CLI once with `CGO_ENABLED=0`, private output and a unique build version, explicitly chooses a new machine ID with `machine-id --new`, then runs real `sync` against the production `store.Local` boundary to initialize the v3 bucket/SQLite binding. Ready means exit 0, `run.json` has `ready: true`, and helper doctor passes; mere compilation is not readiness. No agent or persistent server is launched. Existing test harnesses later build their own binaries as designed.

The subprocess environment is constructed explicitly, not inherited: independent HOME, XDG roots, agent roots, collector config/state and local store, no R2/provider/OAuth credentials, shell profiles, or real user Git configuration. Only the explicit installed toolchain and prepopulated module cache are reused. Config contains no R2 settings; `AGENT_SESSIONS_STATE_DIR` is environment-only in this application. Local mode refuses host managed Claude/Codex directories and ancestor instruction files rather than accidentally snapshotting host settings. This is an ordinary local-store boundary test, **not** the live harness's OS sandbox. Do not point it at real source roots or insert real credentials. Parallel runs get distinct machine IDs, locks, HOME, state and buckets; a run-level advisory lock rejects simultaneous mutating drives of the same run.

## Doctor

```sh
.agents/skills/verify-agent-sessions/verify.py doctor "$RUN"
```

This is the **read-only helper doctor**, not `agent-sessions doctor`. It checks private ownership/token/path separation, current source digest, exact binary SHA-256/version, selected config/machine identity, local bucket store ID, and SQLite's format/destination/store/machine binding using a read-only immutable SQLite connection. It runs only read-only `--version` and `machine-id`, comparing runtime file inventories before/after. It prints JSON to stdout, writes no artifact or runtime state, and cannot repair a wrong instance. Capture its stdout outside scratch if desired. Run it whenever anything looks off. A changed source/build/destination requires a fresh launch, not a bypass.

**Production doctor is not read-only.** `internal/doctor.checkRuns` opens state and may initialize/migrate SQLite. On R2, `probeStore` PUTs, GETs, then DELETEs a random probe. The setup drive invokes production doctor only against this private local store, asserts the expected unapproved-Codex failure, and verifies bucket hashes did not change. Never use production doctor with real R2/config/credentials in this skill.

## Drive

```sh
# One mapped feature, enough for an initial skill proof:
.agents/skills/verify-agent-sessions/verify.py drive "$RUN" setup

# Fresh run: ALL mapped automated local journeys, strict assertions, not command echoes:
.agents/skills/verify-agent-sessions/verify.py drive "$RUN" all

# Optional, same ready run before cleanup; no unsandboxed fallback:
.agents/skills/verify-agent-sessions/verify.py live "$RUN"
```

`all` runs `setup`, `capture`, `privacy`, `hooks`, `regression`, in that order. Individual names are accepted by `drive`. Each mutating feature is one-shot; launch a new run after an attempt rather than retrying until it passes. `setup` and `diagnostics` map to the same setup journey; there is no separate `diagnostics` argument. Collector commands run from owned scratch `work`; only Go builds/tests run from the checkout. All collector commands are executed through the real compiled CLI; fixtures imitate native transcript formats and use synthetic content. Helper doctor precedes every drive. Failures are not weakened to passes.

- **Setup:** explicit identity creation/printing/nonrotation, missing-ID failure, known-ID override, all-present installation, repeatability, agent-selective install/uninstall, dry-run file invariance, foreign-settings preservation, eight Claude/six Codex/Pi lifecycle registrations, production doctor's local warning/unapproved trust, and uninstall preservation.
- **Capture:** all-agent `backfill`, all-agent `sync`, unchanged run, each agent's `sync --agent NAME --path PATH`, byte-exact committed reconstruction, appends, Pi rewrites/empty capture, source deletion plus total cache loss, real CLI remote-only recovery, retained immutable history and rebuilt SQLite projection.
- **Privacy:** known config redaction, credential/backup exclusion, explicit non-DLP unknown file, malformed protected config error/previous generation retained, self-referential symlink-loop partial discovery (ELOOP, not ordinary missing-target absence) without deletion, disabled-hook silence/no uploads, later enabled hook still excluded with no new commits/pending launch, durable exclusion restored after cache loss, allowed sibling and conservative shared-history withholding.
- **Hooks:** synthetic direct Claude `SessionStart` stdin with explicit `--argv '["claude","synthetic-verification"]'` through the real hook command; wait for detached work, committed launch and exact source; malformed hook remains silent and succeeds. This is **not** a claim of real agent ancestry or all lifecycle events. The explicit nonempty JSON argv uses the supported extension-provenance path and bypasses ancestor scanning. Assertions require that exact argv, `via: extension`, owned cwd and source root; this is synthetic extension-supplied provenance, NOT native ancestor proof. Native ancestry is verified only by the sandboxed live harness.
- **Regression:** existing `TestScripts` (installation, sync, Pi detached hook and shutdown sweep), plus the seven isolated `TestAcceptance*` new-design CLI journeys (see [acceptance review](../../../docs/acceptance-review.md)), then named internal API/history/restore/exclusion tests. Missing implementation fails these journeys rather than being skipped. `TestMain` builds an instrumented binary itself; this is not the coverage gate, `make check`, a benchmark, or a replacement for them. Internal restore checks are **supplemental**, never represented as a user restore command.

Optional live requires macOS, `/usr/bin/sandbox-exec`, `/usr/bin/script`, C toolchain for race tests, and **exactly** `/opt/homebrew/bin/claude`, `/opt/homebrew/bin/codex`, `/opt/homebrew/bin/pi`. It invokes existing `AGENT_SESSIONS_LIVE=1 AGENT_SESSIONS_LIVE_KEEP=1 go test -race ./cmd/agent-sessions -run '^TestLiveAgents$' -count=1 -v`. The harness creates its own isolated homes, synthetic loopback Anthropic/OpenAI model server, OS network/read/write deny profile, scratch trust/config and observer wrapper. It proves its sandbox before driving agents and fails if unavailable; never substitute PATH agents or an unsandboxed invocation. See [docs/live-acceptance.md](../../../docs/live-acceptance.md) for lifecycle/PTY details and tested versions (Claude 2.1.296, Codex 0.162.1, Pi 1.1.0). Agent version drift is not silently equivalent. This proves one no-tool synthetic reply and orderly start/turn/end per real agent; not real providers, auth/keychain, tools, interruption, compaction, resume, TLS, arbitrary argv redaction, or empty argv words. Recorded ordered argv assertions specifically exclude empty words and secret/redaction cases.

## Evidence

`$RUN/run.json` is the action/status manifest: argv, cwd, explicit safe environment, input, timestamps, expected and actual exit code, stdout/stderr artifact paths, observations and per-feature `passed`/`failed`/`blocked`/`unverified`. Command actions also record their owned PID/PGID. `passed` means that named journey's assertions completed, not that every entry point or production environment is verified. Unattempted optional live remains unverified. Missing preconditions and timeouts must be reported, never relabeled skipped/pass.

`actions/*.stdout` and `*.stderr` preserve command output separately. `states/*` holds original fixture/config/state/bucket/work files plus per-file sizes/SHA-256, including an installed view and pre-loss history. Cleanup captures a final view. The helper checks committed record-name hashes, encoded zstd piece hashes/lengths, decoded hashes/ranges, whole-file bytes/hashes, history retention and side effects. The history oracle comes from explicit fixture/action bodies (initial, appended, rewritten, empty, deletion-retained empty), not the archive under test. Every successful capture retains all acknowledged commit hashes; selection validates physical stream identity and a unique contiguous parent chain. A self-hashed sequence-999 orphan negative fixture must be rejected. It is not a replacement for the archive's hostile-store/fork/failure-boundary test suite. `evidence-hashes.json` indexes final retained artifacts (excluding itself). A final screen/summary alone is insufficient; inspect actions **and** resulting state.

Live stdout names each `/private/tmp/memory-live-*` evidence root, preserved by `KEEP=1` (also retained on failure). Successful runs import those trees into `$RUN/live/` with `.verification-hashes.json` inventories. Native agent symlinks (such as Claude debug `latest`) are recorded as literal relative-path → `os.readlink` target strings in `.verification-symlinks.json`; they are never dereferenced, recreated, or used to read target data. Descriptor-relative `O_NOFOLLOW` reads copy only owned regular files/directories, preserving strict private, link-free proof trees. These two reserved manifest names are collision-checked against native root entries. An owned scratch fixture proves external file/directory links do not import or modify their sentinel, and that manifest collisions fail closed. The original harness roots remain independently retained; see Cleanup. If live fails before importing, its stdout/stderr still identify available originals. If import fails midway, any private link-free partial tree remains as evidence with `complete: false` in its symlink manifest; the live feature fails rather than claiming a complete import. Cleanup can still inventory the proof without following links. No import path ever modifies/deletes native harness sources or link targets. Never claim success from an `ok` without required subtest PASS lines; helper rejects skips.

**Unverified boundaries:** local backend is a production-supported stand-in for R2, not proof of R2 access/durability/multipart behavior/lifecycle configuration. Scratch installation does not prove release download/checksum/update, real login profiles or real Codex `/hooks` approval. A Linux cross-build is compilation only, not Linux runtime acceptance; this helper does not cross-build. Synthetic model acceptance is not real-provider acceptance. Global archive failure injection, resource/performance budgets and every hook event are not exhaustively established by these local journeys; use the repo's separately owned gates/harnesses. See the map's explicit entry-point gaps.

## Cleanup

Always clean up after success **and failed attempts** using the evidence path printed by launch:

```sh
.agents/skills/verify-agent-sessions/verify.py cleanup "$RUN"
python3 -c 'import json,pathlib,sys; p=pathlib.Path(sys.argv[1]); m=json.loads((p/"run.json").read_text()); assert m["cleaned"] and not pathlib.Path(m["scratch"]).exists(); assert (p/"evidence-hashes.json").is_file(); print(p)' "$RUN"
```

Cleanup validates private owner, canonical nonoverlapping paths and matching private ownership token, refuses symlinks/special files/foreign ownership, waits for exact owned binary processes to exit, captures final state, then removes **only** that run's scratch tree. Cleanup never kills by name or kills any process, and never touches real agent configs, and never deletes proof. Foreground commands run in their own new process session/group; actions record PID/PGID. On timeout the helper sends bounded TERM then KILL only to that owned group and retains output/status. Cleanup checks all recorded groups and complete owned scratch/live-profile command footprints, not only the main binary. Exited zombies cannot write and are ignored. Detached hooks deliberately create their own session: if any owned process remains active, cleanup blocks rather than deleting its files. Inspect recorded argv/PID/PGID and exact live scratch paths, let those processes finish, then rerun cleanup; never kill by name or weaken ownership checks. After a timeout or any failure always attempt cleanup and report a blocked cleanup explicitly, not as complete. Missing/tampered scratch is an error, not permission to remove an arbitrary path. A second cleanup is an evidence-preserving no-op.

Existing live harness roots are **evidence**, not part of the helper-owned instance, and intentionally remain after helper cleanup. They contain sandbox-confined detached children; do not kill agents by name or delete a root merely matching `memory-live-*`. Keep the originals for review, especially on failure. When explicitly retiring live evidence, individually verify the exact logged path, current-user ownership/private permissions, absence of symlinks and active commands referencing that root, and presence of its `sandbox.sb`, `config.env`, `events`, and bucket. Delete only that explicitly reviewed root after its proof has been copied/hash-checked; retain `$RUN`. The helper does not broadly sweep `/tmp`. Default proof roots are on temporary storage: copy private proof to durable storage before OS temp eviction if long-term evidence is required.

## Helpers

Only `verify.py` is shipped; it is executable and uses stdlib Python. Full invocation reference:

```sh
.agents/skills/verify-agent-sessions/verify.py --help
.agents/skills/verify-agent-sessions/verify.py launch --help
.agents/skills/verify-agent-sessions/verify.py launch [--artifacts /absolute/new-proof] [--module-cache /absolute/cache] [--go /absolute/go]
.agents/skills/verify-agent-sessions/verify.py doctor "$RUN"
.agents/skills/verify-agent-sessions/verify.py drive "$RUN" {setup,capture,privacy,hooks,regression,all}
.agents/skills/verify-agent-sessions/verify.py live "$RUN"
.agents/skills/verify-agent-sessions/verify.py cleanup "$RUN"
```

Braces/brackets in the invocation reference describe alternatives/options, not literal shell arguments. See the literal working invocations above. Exit 0 means completed; exit 2 with `BLOCKED` means an unmet precondition/timeout; other failures exit 1. Never repair a failed journey by editing assertions. Inspect artifacts, fix the skill or report a real product problem before changing production code. Do not run `make check`, coverage or benchmarks through this helper; those remain separately owned project verification.
