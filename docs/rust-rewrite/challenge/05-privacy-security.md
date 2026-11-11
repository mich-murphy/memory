# E: privacy and security review of DESIGN.md (2026-11-07)

Reviewer E. Read-only on the repo except this file. No transcript content, credentials, live config or R2 touched. No web access was available to me in this session, so every vendor statement about R2 below is labelled "vendor, from memory, unverified". Labels: read (file:line), inferred, guess.

## Verdict

DESIGN.md states fail-closed privacy as requirement R2 but specifies none of the machinery that makes it true today. A grep of DESIGN.md for scrub, redact, encrypt, secret, credential, permission, 0600 and umask finds nothing (read, one hit only for "allowlisted" at line 357). The Go code has a 827-line scrubber, a credential denylist, symlink-identity checks, a monotonic exclusion registry, a launch argv allowlist and 0600 file modes. The design either drops each of these or leaves them as a one-word "policy" in `memory-core`. Hypothesis 1 is confirmed as a Blocker. The design also has no threat model, no credential scoping and one concrete regression (exclusions read only at daemon start). Fix the policy section before slice 1, because property 5 only tests the part that is easy.

## Blocker

### B1. Config scrubbing, credential denylist and self-exclusion are absent from the design

Claim in DESIGN.md. R2 (lines 51-52): no byte leaves without an `Authorized` token. Line 137: "The writer never parses transcript content. It moves bytes." Snapshot classes include `ClaudeConfig`, `CodexConfig`, `PiSettings`, `PiModels`, `PiSnapshot` (lines 198-203, 344-348). `policy` is listed as a `memory-core` module (line 493). Nothing says what the policy checks.

Problem. The contract says "Protected configuration still requires successful exclusion/scrubbing checks" (`docs/session-capture-contract.md:48-49`) and "Unknown exclusion state/unsafe scrub: fail closed" (`:109`). In Go the following protections exist and none is in the design.

1. Structural scrub of 11 known config basenames (`claude.json`, `.claude.json`, `settings.json`, `settings.local.json`, `managed-settings.json`, `.mcp.json`, `mcp.json`, `managed-mcp.json`, `models.json`, `config.toml`, `managed_config.toml`). Pi `models.json` carries provider `apiKey` and `headers`; `~/.claude.json` carries `primaryApiKey`. Rules at `internal/scrub/scrub.go:42-80,94-106`, as summarised in `reports/03-legacy-and-support.md:194-211`. The scrubber is a transform. A "moves bytes" writer would upload `models.json` with live provider keys. That is a direct secret leak in a captured class the design lists by name.
2. Malformed protected JSON or TOML is withheld, not copied raw (`scrub.go:128-139`, report 03:214). Duplicate JSON keys are rejected. Report 03:273 notes `serde_json` accepts duplicates silently with last wins, so a naive port reopens a bypass.
3. Selection is by filename of every identity in a symlink chain, not by collecting agent (`scrub.go:156-170`; `docs/security.md:21-30`). A Claude skill symlinked to Pi `models.json` is still scrubbed.
4. `scrub.Text` withholds small text assets (PiSnapshot skills, extensions, tools) that assign a literal to a secret-named variable (`internal/scrub/text.go:44-61`; callers `internal/snapshot/snapshot.go:151,155`, `options.go:167,171`).
5. Credential denylist with 20-plus names and directories (`.credentials.json`, `auth.json`, `.env*`, `*.key`, `*.pem`, `.ssh`, `.aws`, `.kube` and others) and noise denylist (report 01:262; `capturepolicy/policy.go:17-24`). The design says the agent roots are watched recursively and captures "all" 14 kinds. Nothing keeps `~/.codex/auth.json` or `~/.claude/.credentials.json` out. `docs/hooks.md:105` and `docs/storage-layout.md:52` state the Go exclusions.
6. Self-exclusion. Go denies collector config (`config.env`, including backups) and the state directory by filesystem identity (`docs/security.md:11-16`). The new config TOML must hold R2 credentials or point to them (not specified, see M3). The index, hint log and config need the same self-exclusion.
7. Symlink and path resolution. Go checks every intermediate symlink and final identity before reading, and the candidate reads no-follow (`docs/security.md:10-19,44-47`). DESIGN.md has no symlink rule at all. A symlink in an agent root, or a hint line whose `transcript_path` names a file outside the roots (line 371), has no stated check (see M4).

Second-order effects of adding a scrubber that the design has not absorbed.

- Manifest `sha256` is defined as "whole file at this capture" (line 266). For scrubbed classes the digest is of transformed bytes. The daily re-hash and read-back sample (lines 457-466) compare to "the local file". They would quarantine every scrubbed config. They need `transform(local)`.
- The planner's `Unchanged` decision is stat-based (size, mtime, lines 206-210). A scrub rule change would not recapture a previously mis-scrubbed file. Go binds `RulesDigest` into the fingerprint (`scrub/digest.go:17-27`; report 01:76). The design needs a transform version in `SourceState`.
- `Quarantine { reason: String }` (line 228) and `tracing` output are the operator's only failure view (line 474). Go notes parser diagnostics can echo a credential, so TOML errors return a fixed string (`scrub.go:249`). The same rule must hold for logs and marker objects.

Honest scope note. Go does not scrub transcripts either (`docs/security.md:5-8`, report 03:220). The scrubber is a known-file policy, not DLP. So this Blocker is about config and credential files, not transcript bodies. Transcripts are M1.

What industry does. Backup and sync tools exclude by pattern before reading (restic `--exclude`, `--exclude-if-present`; borg `--exclude`; kopia ignore rules). Secret scanners redact structurally for known formats (gitleaks, trufflehog operate on detectors, not on the "move bytes" model). I cite these from memory, not fetched.

Recommended change. Add section "Protected sources" to DESIGN.md. Specify a `Transform` stage between read and `Authorized`, and make the type chain enforce it, for example `Authorized<Redacted<Bytes>>` as the only body type accepted by the PUT function. That fits the design's own type rules (section 13) and makes "unscrubbed config reaches R2" a compile error. Carry forward the rule table, the credential denylist, the symlink-chain rule, `Text`, the argv allowlist (M5) and the rules digest. Add Hegel property 5b: for any file with a recognised basename and a planted literal in a known synthetic field, no PUT body contains the literal. Use synthetic canary values only. Port scrub in slice 1, not slice 3, because slice 2 uploads `all three agents` (line 579) and config classes are only in slice 3 (line 581). That ordering is a mitigation. State it, and add a slice 2 rule that unknown or config kinds are withheld until slice 3 lands.

## Major

### M1. No threat model, no client-side encryption (hypothesis 2)

Claim. None. DESIGN.md never states what R2 access control is trusted to do. Section 3.5 lists vendor facts only for correctness.

Problem. Transcripts contain pasted keys and tool output (env dumps, `cat .env`). They are uploaded byte for byte (line 137) and Go does the same (`security.md:5-8`). So the design is not a regression. It still makes privacy a headline requirement while the largest-surface content has one control, a bucket token. Provider-side encryption at rest (vendor, from memory: R2 encrypts all objects at rest with AES-256, unverified) protects against disk theft at Cloudflare, not against a token holder or a leaked token. Both hosts hold a token that can read everything (M2).

What industry does. restic, borg, kopia and Tarsnap encrypt client-side by default, because the storage provider is outside the trust boundary (restic design doc, borg docs, kopia docs, Tarsnap paper; from memory). Those tools have a reader that holds the key, as this project would.

Cost and interaction with content addressing.
- CPU. ChaCha20-Poly1305 runs at roughly 1 GB/s per core (guess from typical numbers, not measured here). Against zstd -3 at about 30 ms per 19 MB (design line 3.4) it is noise. Per-piece overhead of an age file is a header of a few hundred bytes (guess), against p50 pieces of 59 KB.
- Key management is the real cost. The reader (restore, DuckDB analytics) needs the private key. Key loss means total loss, which conflicts with R3 "A wrong or missing byte is a defect".
- Asymmetric recipient encryption (age with X25519 recipients) lets the hosts hold only a public key. A compromised host could then not decrypt other data. This partly answers M2.
- Convergent encryption (key derived from plaintext hash) leaks equality: anyone who can guess a plaintext can confirm it exists (confirmation-of-file attack, documented for Tahoe-LAFS and for convergent encryption in general). Avoid it. Use a random or deterministic-per-piece nonce.
- Content keys. Today the object key is the SHA-256 of the encoded bytes (line 238). If the body is encrypted with a random nonce, a retry after a lost reply produces a different ciphertext and key. Correctness survives because the manifest names the key actually written, and the stray object is an unreferenced orphan, which the design already tolerates (line 147). Idempotence of the PUT is then weaker than R4 claims. Alternative: key = HMAC-SHA256(idkey, plaintext piece) with an `idkey` the host holds. That keeps retry idempotence and removes the equality oracle for anyone without `idkey`. The reader's "digest matches key" check (line 439) then needs `idkey` or checks the plaintext digest in the manifest instead.
- Encryption also gives crypto-shredding for B3/M3. Per-session data keys wrapped by the recipient key make "delete the excluded session" a key deletion, not an object hunt across a global namespace.

Recommended change. Do not add encryption by default. Add a one-paragraph threat model to section 1 that names what is trusted (the R2 token holders, Cloudflare) and what is not. Put encryption to the operator as an explicit decision with the costs above (see Open questions). If the answer is "no", write that transcripts are stored in plaintext behind bucket access control, so R2 credential handling in M2 is the whole privacy boundary.

### M2. Credential scope: one token compromise reaches both hosts' data (hypothesis 4)

Claim. None. The design has no credentials section. Quarantine markers are "visible to both hosts" (line 245). Layout puts both machines in one bucket (`sessions/<archive>/<machine>/...`, line 240).

Problem.
- Fencing is not authorization. `If-Match` on `current.json` (line 336) stops accidental double writers. It does not stop a malicious holder of the same token from overwriting another host's `current.json`, repointing it to an older or forged manifest, or deleting objects. Any client that omits `If-None-Match: *` can also overwrite an "immutable" object.
- Vendor (from memory, unverified). R2 API tokens have the permission levels Admin Read and Write, Admin Read only, Object Read and Write, Object Read only, can be scoped to chosen buckets, and can carry a TTL and client IP filter. I know of no write-only object permission and no per-key-prefix scoping on API tokens. Object Read and Write includes delete. If true, a writer token cannot be made "no read, no delete".
- The design needs GET on the host for reconcile (one GET per session at start, line 376) and the daily read-back sample (lines 462-466). So the host token must read. Write-only is already ruled out by the design itself.
- Rewriting history is also covered by the free-delete fact (line 119): deletes cost nothing, so an attacker's erase is cheap and the design has no tombstone or retention lock.

What industry does. restic and kopia on S3 recommend a dedicated bucket or IAM policy per client and, for append-only, `rest-server --append-only` or S3 object lock. R2 Bucket Locks (retention rules, possibly by prefix) were announced in 2025 (from memory, unverified). Check whether a lock on `objects/` and `manifests/` can coexist with the post-hoc exclusion deletes in decision 2.

Recommended change. Use one bucket per host, or one token per host scoped to its own bucket, so a compromise of BC-01 cannot touch the Mac. Move the quarantine marker out of the shared bucket or accept read-only cross-host access that the token model cannot provide. Document that `current.json` can be rolled back by any holder of the host's token and that the reader must treat `previous` chain breaks as an alarm. Record where the token lives, its file mode, and that the config file is self-excluded (B1.6). Verify the R2 token facts before building on them.

### M3. Policy timing: exclusions are read at daemon start only, and the registry is dropped

Claim. Config TOML holds "privacy exclusions", is "read at start" (lines 364-366). Existing exclusions are "migrated into it once" (line 366).

Problem.
1. Read at start means an exclusion added while the daemon runs has no effect until restart. In Go the exclusion registry is a directory of per-record files, monotonic, 0600, loaded fail-closed (`internal/exclusion/exclusion.go:170-205`; report 03:226-228) and rechecked before publication (`docs/security.md:47`; contract `:113` "Recheck exclusions before delivering pending work"). The design deletes the spool, so the recheck window shrinks, but a session window can last 300 s (line 299) and an operator exclusion can land inside it. `Authorized<T>` (line 224) has no stated lifetime. A token minted before the 3 PUT sequence and used after is exactly the TOCTOU the contract rechecks.
2. Config managed by hand is not monotonic. The Go registry has no delete path (report 03:226). A hand edit that drops a line silently re-enables a session. AGENTS.md says reset must not "silently re-enable excluded sessions".
3. Remote exclusions. Legacy mirrors exclusions to `v3/machines/<m>/exclusions/<sha>.json` and restores the union before capture (`syncer/recovery.go:142-183`). Report 03:13 found no candidate code for this, and the design does not mention it. The contract requires preserving remote-only exclusions (`contract:182-184`).
4. Exclusion identity. Go compares paths, canonical paths, filesystem identity and case aliases (`security.md:15-17`; report 03:226). The design says only "privacy exclusions" in TOML.
5. Honesty of the guarantee. Earlier epochs stay in R2 after a later exclusion. The design does say post-hoc exclusion needs an object delete path (line 153, decision 2 at 595-598), which is honest. R2 still says "No byte leaves the host" without qualification. Say plainly that R2 covers bytes after the exclusion is effective, and that already uploaded bytes stay until the reader project deletes them.
6. Shared stores. Decision 1 says withhold the whole store if any session in it is excluded (592-594). Go does this through `SessionKey == ""` matching every record, which report 03:266 calls the same bug as the Claude test. The design's `SessionKey` has no `Shared` or `Unknown` variant (line 196). `Unknown` owner is indistinguishable from agent-wide. Without an `Owner` sum type the default risks "capture the shared store when the key is unknown".

Recommended change. Load exclusions per capture (cheap, a directory read, or a watched file), not at start. Make the registry append-only with fail-closed load, as Go does, and keep it separate from operator config. Define `Authorized<T>` as consumed at the first PUT with a stated lifetime, and recheck immediately before the `current.json` PUT, as the candidate does. Add `Owner { Session, Shared, Unknown }` and make Unknown withhold. Decide remote exclusions (Open questions).

### M4. Policy inputs: hint log is an unauthenticated, loosely permissioned input

Claim. Hooks append lines to `hints.jsonl` with `O_APPEND` (lines 367-372). The reconcile "never trusts the event's content" (line 182).

Problem.
- Permissions. Nothing says the file, its directory, the index snapshot (temp file then rename, line 373) or the rotated file have mode 0600/0700 or what umask applies. Go sets `0o700` directories and `0o600` files (`internal/hook/hook.go:104,109,256`). A hook that creates `hints.jsonl` under a default umask 022 makes it world-readable. Lines carry `cwd`, `transcript_path` and `launch` including argv (line 371), which can hold secrets. After rotation the next hook creates the file again, with the same risk.
- Injection. Any process running as the user can append a line. The design says reconcile uses `sources_of(session)` from the index plus readdir of the session dir (line 304). It does not say how a hint's `transcript_path` or `cwd` is validated against the configured roots and policy. If a hint can introduce a source outside a root, or a symlink target (`~/.aws/credentials`), policy is the only barrier, and B1.7 says the symlink check is missing. Validate every hint path against roots, resolve symlinks, and then apply policy, before it becomes a source.
- Shared machine. `memory hook` runs as the agent user and the daemon as the same user in the single-user case I can infer from the design. If a host is shared or the daemon runs under another user, the hint file is a cross-user channel. State that the design assumes one user per host, and make the daemon refuse a hint file not owned by itself or with group/world write.
- Torn lines. A hook line above `PIPE_BUF`-style atomic append size could interleave with another hook. The design caps lines at 4 KiB (line 372). Linux `O_APPEND` writes to local files are atomic with respect to the offset, but not guaranteed atomic in content across all filesystems (inferred). Fine on local disks. Say so.

### M5. Launch evidence and manifest metadata leakage (hypothesis 6)

Claim. Manifest carries `launch.argv`, `cwd`, `model`, `agent_version` and source `path` with the project slug (lines 257-259). Hint lines carry `launch` (line 371). Line 357 says only "allowlisted launch fields".

Problem.
- The allowlist is not specified. Go sanitizes argv with `scrub.Args` and then a strict flag allowlist: `--model`, `--provider`, `--permission-mode`, `--session-id`, `--session`, `--config`, `--config-dir`, `--cwd` and a few booleans, 32 args maximum, everything else `[redacted]` (`internal/launch/native.go:15-47`). Free-form prompt text is dropped. The design's example `"argv": ["claude", "--model", "…"]` suggests raw argv. If the Rust port stores argv before sanitizing, the hint log holds secrets (M4) and the manifest uploads them. Env allowlist is exact-name capped at 8 (report 03:147). Pi passes argv in the payload, so the hook gets it directly.
- Withheld manifests expose existence. An excluded source appears as `{"state": "withheld", "reason": "excluded"}` (line 270) with its id and kind, and `session` identifies the excluded session. The excluded session may be one the operator opted out of precisely to avoid any record. Decide whether withheld entries and launch evidence exist for excluded sessions at all. Go does not capture an excluded session's evidence.
- Access level. Manifests sit in the same bucket and under the same token as content (line 241), so there is no wider exposure today. It becomes one when the reader project exposes manifests to analytics or dashboards without content. Treat `cwd`, slug and argv as sensitive (they reveal client, repo and username).
- Quarantine markers are `Quarantine { reason: String }` objects (line 228). Reasons must be an enum, not free text, for the reason in B1.

Recommended change. Specify the launch allowlist in DESIGN.md exactly as Go does, and apply it in `memory hook` before the append, not at capture time. Make excluded sessions emit no manifest and no launch record. Make `reason` a closed enum.

### M6. Content-addressed global objects and deletion (hypothesis 3)

Claim. `objects/<sha256>.zst` is one global flat namespace (line 238). Deletion is deferred (lines 595-598).

Problem.
- Equality across sessions and hosts. Identical pieces share one key. Without encryption this reveals nothing the token holder cannot already read. It matters for the deletion path and for any later audience with list but not read access (inferred).
- Entanglement. Claude resume and fork copy earlier transcript content into a new file (inferred from agent behaviour I did not verify), small configs repeat across sessions and hosts, and the empty or one-line first piece repeats everywhere. Deleting the objects of an excluded session can remove bytes another session's manifest still references. Deletion must therefore be reference-aware, which needs a scan of every manifest, or a reference index the design says must not exist ("No local job table", "readers never list `objects/`", lines 132, 143). The reader project has to solve garbage collection with refcounts by walking `current.json` to manifest to `previous` chain for every session. That is feasible but not free.
- The window of exposure. Between capture and a later exclusion, an excluded session's objects are public to every token holder, and no design item bounds how long.
- The quarantine marker is keyed by session and visible to both hosts, so exclusion state for a session is in R2, but exclusion intent is not.

What industry does. restic, borg and kopia all use global content-addressed chunk stores and have to run a mark-and-sweep prune over all snapshots to delete (restic `forget` then `prune`; kopia maintenance). They accept equality leakage because the repository is encrypted (see M1). Crypto-shredding is the usual way to avoid object hunting in immutable stores.

Recommended change. Keep the layout. State in decision 2 that deletion of one session is a mark-and-sweep over all manifests, not a per-session prefix delete. If per-session deletion is likely, add a session prefix to piece keys (`objects/<archive>/<machine>/<session>/<sha>`) and lose cross-session deduplication, which the design never measured as a benefit (R8: no measured risk removed by global addressing). I cannot say how much dedup is lost. Ask for a measurement of cross-session piece overlap before keeping the global namespace.

## Minor

- m1. "Unknown policy state is an error, never a default allow" (lines 51-52). `PolicyDecision::Unknown` exists (line 225), but nothing defines what produces it or what Plan it maps to. `Plan::Withheld` takes a `WithholdReason` (line 219), and `Unknown` has no reason. Define the producers: config missing or invalid, exclusion registry unreadable, root unknown, path not resolvable. Define the effect as the Go behaviour, deny all capture for the affected scope until recovered (report 03:227, contract `:109`). Make the daemon refuse to start on a missing config rather than run with defaults. Add test property 5c for each.
- m2. Unknown source kinds (hypothesis 7). `SourceKind` is a closed enum of 19 variants (lines 200-203) and `wire enums non_exhaustive only in the reader` (line 231). That is sound for the writer's own wire, but discovery needs a rule for a file under an agent root that maps to no kind after an agent upgrade. Go treats a recognised name as protected and passes unrecognised names through byte-for-byte (`security.md:5-8`). That pass-through is the dangerous default for new agent files. For the Rust writer, unmapped paths should be skipped and counted, never uploaded. Say so. Property 5 (lines 553-554) tests excluded sources only. Add "unknown path contributes zero bytes".
- m3. Learned roots live in the index (line 374), yet line 375 says "Deleting it loses nothing". The two contradict. AGENTS.md says to preserve learned custom roots. If the roots are lost, sources under them stop being captured, and exclusion matching against root identity (`security.md:17`) also loses its anchor. Put learned roots in the operator config or a separate persistent file.
- m4. Rotated hint file is "read to end after 10 s and deleted" (line 369). Deleting does not scrub. Disk contents persist. Low value, mention only if the hint log ever carries argv before sanitising (see M5).
- m5. `memory status` and `tracing` run as the operator's view of failures (line 474). Log lines naming paths and reasons can leak project names into system logs (launchd, journald). Say what is logged at which level.
- m6. The daily read-back sample reassembles production transcripts in memory on the host (lines 462-466). Harmless in itself. Make sure it never writes them to logs or temp files, and never runs on an excluded session.
- m7. `rusqlite` bundled backs up Codex SQLite stores (line 511). Shared stores hold rows of every session, and Go reads them read-only with pinned main and WAL provenance (`security.md:44-47`). The design only says "SQLite backup API". Carry the read-only open flags and the sidecar and alias policy.

## Claims checked

| Claim | Result |
|---|---|
| "Fail closed on privacy" (R2) | Contradicted as specified. The policy module has no content (B1, M3). |
| "Writer never parses transcript content" (137) | True and fine for transcripts. Conflicts with config scrub for the five config classes (B1). |
| "Existing effective exclusions are migrated into config once" (366) | Not checked against data (I may not read `~/.config`). The design conflicts with the monotonic registry and remote mirroring (M3). |
| "Withhold the entire store if any session is excluded, which is today's behaviour" (592-594) | Confirmed in Go (report 03:229-230, `exclusion.go:211-232`). Needs `Owner` type (M3.6). |
| Hint lines allowlist launch fields (357) | Unconfirmed. No allowlist is written. Go's is at `launch/native.go:15-47` (M5). |
| Post-hoc exclusion needs object delete (153, 597) | Confirmed as honest. The cost is under-stated (M6). |
| Deleting the index loses nothing (375) | Contradicted for learned roots (m3). |
| R2 token permission model | Could not check (no web access). Stated from memory in M2. |
| Go has no encryption | Confirmed. `grep -rli encrypt\|chacha\|aes\.` over `internal` and `cmd` hits only tests and a workload helper. |

## Open questions for the operator

1. Is plaintext transcript storage behind one bucket token acceptable, or should the writer encrypt to an age recipient key held off the hosts? (M1)
2. Should each host get its own bucket and token, accepting that cross-host quarantine visibility goes away? (M2)
3. Are remote-only exclusions in legacy R2 to be imported once at cutover, mirrored by the new writer, or dropped? (M3.3)
4. Should an excluded session leave any trace in R2, including a withheld manifest entry? (M5)
5. Is the single-user-per-host assumption true for BC-01? (M4)
6. Is cross-session piece dedup valuable enough to keep a global object namespace, given the deletion cost? (M6)
7. Do you accept that the scrubber ports into slice 1 and that slice 2 withholds config and unknown kinds until then? (B1)

## Things I could not verify

- Every R2 vendor fact: API token permission levels, per-prefix scoping, Bucket Locks, encryption at rest. No web access.
- Whether Claude resume or fork copies prior transcript bytes into a new file (affects M6 overlap). I did not read transcripts.
- Encryption throughput and age header size. Numbers are typical, not measured.
- Whether the candidate Go code reproduces every legacy protection. Report 03:301 says it did not audit this either.
- Effective modes of existing Go state files on the hosts. I did not inspect live state.
