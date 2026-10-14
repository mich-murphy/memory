# Bucket layout

The authoritative archive contract is **v3**. The collector never deletes or modifies legacy
v2 archive objects. A configured machine ID is an explicit identity, not a hostname guess.

```
v3/store.json
v3/machines/<machine>/checkpoint.json
v3/machines/<machine>/exclusions/<record-sha256>.json
v3/machines/<machine>/records/<kind>/<stream-id>/plans/<sequence>-<descriptor-sha256>.json
v3/machines/<machine>/records/<kind>/<stream-id>/commits/<sequence>-<descriptor-sha256>.json
v3/blobs/zstd/<encoded-sha256>.zst
v3/blobs/plain/<sha256>
v3/snapshots/sets/<set-id>.json
```

`kind` is `file`, `manifest`, `launch` or `root`. `stream-id` is SHA-256 of the agent name,
a NUL byte, and the logical key. Sequences have 20 decimal digits. A descriptor hash is of its
exact sorted compact JSON bytes; the plan and commit contain **identical bytes**. Nonfile
dependency sets are sorted by object key and deduplicated before publication. File dependencies
retain decoded piece order. Generic JSON numbers in launch payloads, file heads and manifest
info retain their original numeric spelling through decoding (including integers above
2^53 and scaled decimals), so canonical record bytes and descriptor hashes remain stable.

A file key mirrors its logical agent-relative location (the table below). Session keys include
custom-root and Pi session-directory namespaces. Launch streams use the durable envelope ID;
root streams use SHA-256 of agent, NUL, and the absolute root path as their logical key.

A plan is written before any new dependency. A commit is written only after all dependencies
are confirmed durable. Immutable commits, not SQLite or checkpoint timestamps, are authoritative.
A stream's unique complete parent chain selects current; conflicting children fail closed.

`store.json` contains schema 3 and a random store ID. SQLite is bound to format, canonical
nonsecret destination, store ID and machine ID; incompatible reuse requires a separate state
directory. On the first v3 binding, unbound legacy local upload/session projections are cleared
and the file projection is recreated with its agent/key/path identity. Roots and run summaries
remain; legacy remote data is neither migrated nor deleted. `checkpoint.json` is only a warm-cache hint: a random token and `complete` flag.
The local and remote tokens become dirty before publication; only an entirely successful run
marks both clean. Lost acknowledgements, crashes and stale restored SQLite require full remote
reconciliation. Checkpoints never replace immutable records or elect a chain winner.

See [archive-design.md](archive-design.md) for durability boundaries and failure behaviour.

## Raw files

The collector walks each agent's whole directory and copies every file except:

- **Credentials**, never copied: `.credentials.json` (Claude Code's login, Codex's MCP OAuth tokens), `auth.json`, `oauth.json`, `mcp-auth.json`, any `*.key` or `*.pem`, `.env` and `.env.*` (Codex loads API keys from `$CODEX_HOME/.env`), `~/.claude.json` backups, and the collector's own `config.env` (including its configured path) and state directory. Known backup suffixes are stripped repeatedly before credential checks. These exclusions apply to snapshots and external exports too, checking both the logical path and resolved symlink target before reading. `~/.claude.json` itself is uploaded as `claude.json`, with the secrets below redacted and its layout kept.
- **Noise**: locks, temp files, caches, `node_modules`, shell snapshots, executables, Claude Code's own install (`local/`, `downloads/`), plugin source checkouts, and Pi's `bin/` (fd and rg).
- **Live SQLite files**, which are uploaded as consistent copies instead (below).

**Secrets inside config files** are redacted before upload, by `internal/scrub`. Credential files are left out whole, but some config files hold a secret beside settings worth keeping, so only those values are replaced with `"[redacted]"`. Where possible, every other byte of the file, keys included, is kept as it was; fallback reserialization is described below:

| File | Values redacted |
|---|---|
| `~/.claude.json` (uploaded as `claude.json`) | `primaryApiKey`, `customApiKeyResponses`, and each MCP server's `env`, `headers`, `url`, `oauth.clientSecret` and `args`, at the top level and under `projects` |
| `settings.json`, `settings.local.json`, `managed-settings.json` | `env`, and the `headers` of each `http` hook |
| `.mcp.json`, `mcp.json` (Claude Code's, and Pi's `~/.pi/agent/mcp.json`), `managed-mcp.json` | each MCP server's `env`, `headers`, `url`, `oauth.clientSecret` and `args` |
| Pi `models.json` | each provider's `apiKey` and `headers` |
| Codex `config.toml`, `managed_config.toml` | `mcp_servers.*` `env`, `http_headers`, `url`, `bearer_token` and `args`; the `headers` of each `otel` exporter (`otel.<exporter>.<otlp-http or otlp-grpc>.headers`); `model_providers.*` `http_headers`, `experimental_bearer_token` and `base_url`; `shell_environment_policy.set` |

In a command line (`args`), what is redacted is the value of a flag whose name says it holds a secret, as the next word or after `=` (`--api-key`, `--token`, `--auth-token`, `--client-secret`, `--password`; whole words, so `--max-tokens` is kept, and a next word starting with `-` isn't a value), the value in a `NAME=value` word with such a name (`GITHUB_TOKEN=…`, Codex's `-c mcp_servers.x.env.API_KEY=…`), the part of a `--header` or `-H` value after the header's name, and, in an argument that is inline JSON, what these rules redact in a settings or MCP file.

Filename checks fold case and repeatedly strip explicit known backup suffixes: editor (`~`, `.~<number>~`), backup (`.bak`, `.backup`, `.orig`, `.old`), dated (`-<YYYYMMDD>`), installer (`.agent-sessions-backup-<time>`) and compressed/archive (`.gz`, `.bz2`, `.xz`, `.zst`, `.zstd`, `.zip`, `.7z`, `.rar`, `.tar`, `.tgz`). Credential copies are excluded without decoding. Recognised config copies must still parse through the protected scrub path; an unsupported compressed encoding is withheld, never sent as raw bytes. Unrelated names such as `auth.json.example` and `notes.key.txt` are not credential-copy matches. See [security.md](security.md).

A value that only names a secret is kept: `${VAR}`, `$VAR`, `Bearer ${TOKEN}`, a Pi `!command` only in `models.json` provider `apiKey` and `headers`, a number or a boolean, and for Pi's `apiKey` an environment variable name. Only a simple environment variable name is supported inside `${…}`: shell defaults such as `${VAR:-literal-secret}` are not pure references and are redacted. A URL keeps its scheme, host, user and path; its password is dropped and each query value redacted. The same decoded string value anywhere else in the file is redacted too, including equivalent Unicode escapes; object keys are kept. A value written in a form the collector can't find in place (a JSON `\u` escape, a TOML multi-line string) makes it rewrite the whole file from the parsed document, which loses comments and layout. JSON config documents with duplicate object names (including equivalent escaped names), invalid Unicode, empty contents or trailing data are rejected before decoding; TOML duplicate definitions are rejected too. All recognised filenames encountered along the symlink chain—logical, intermediate and final resolved identities—contribute scrubbing rules. Malformed inline JSON arguments are replaced wholesale. A file that has these rules but doesn't parse isn't uploaded; the run counts it as an error and tries again on the next sync.

Nothing else is redacted: transcripts, history, logs and every other file are copied as they are. Files the collector doesn't recognise are uploaded with role `unknown` and counted in each run's summary.

`<key>` mirrors where the file lives under the agent's directory:

| Agent | Keys |
|---|---|
| claude | `projects/<slug>/<session>.jsonl`, `projects/<slug>/<session>/subagents/…`, `…/tool-results/…`, `…/<session>.jsonl.superseded-…`, `projects/<slug>/memory/…`, `file-history/<session>/…`, `todos/…`, `tasks/…`, `teams/…`, `session-env/…`, `debug/…`, `plans/…`, `paste-cache/…`, `usage-data/…`, `agent-memory/…`, `history.jsonl`, `settings.json`, `skills/…` and the rest of the directory, `claude.json` |
| codex | `rollouts/rollout-<ts>-<thread>[_<rollout>].jsonl` (no date folders, no `.zst`), `history.jsonl`, `session_index.jsonl`, `memories/…`, `log/…`, `config.toml`, `skills/…` …, `db/<name>.sqlite` |
| pi | `sessions/--<cwd>--/<ts>_<session>.jsonl`, `settings.json`, `models.json`, `extensions/…`, `experimental/…`, `pi-debug.log` …; sessions from a directory outside the agent dir go under `dirs/<hash of dir>/…` |

A second Claude Code or Codex directory (`CLAUDE_CONFIG_DIR`, `CODEX_HOME`) or Pi agent dir (`PI_CODING_AGENT_DIR`) is keyed under `roots/<hash of dir>/…`, so it can't overwrite the default one.

**Databases.** Each sweep (at every session start and end, and in `backfill`) takes a SQLite backup-API copy of every database under an agent's directory (Codex's `state_*`, `logs_*`, `memories_*`, `goals_*`, `thread_history_*`, `queue_*`). That includes rows still in the `-wal` file, which a plain file copy would miss. A database that changed is copied again once its last copy is an hour old. The copies live in the state directory between sweeps.

**Capture and appends.** Captured bytes are hashed cryptographically. Plain and zstd source
files stream through private staging for large/encoded captures; compression hashes the staged
bytes, not an earlier mutable-source scan. Appends inherit immutable pieces and upload only the
new tail. A fold captures one consolidated piece after 15 minutes idle or at 64 pieces. The
previous pieces and commits remain. A rewrite creates new pieces without copying or overwriting
the old ones. Every captured database generation is retained; capture frequency may be throttled,
not acknowledged history. The collector performs no remote garbage collection.

A file generation records schema, identity (`agent`, `machine_id`, `key`, `session_key`, `root`),
sequence, parent descriptor hash, kind (`capture`, `fold`, `delete`), source aliases, role, decoded
size/SHA-256, line count, prefix hash, head, pieces and observation/deletion times. Each piece
records `[start,end)`, decoded SHA-256, encoded SHA-256, encoded size and its immutable object key.
Authoritative keys, roots, launch paths, source aliases, exclusion identities and snapshot filesystem labels must be valid UTF-8. Unsupported Unix byte names are reported before a plan is uploaded, not silently renamed by JSON encoding.
The prefix covers at most the first 20 lines and 1 MiB. A real empty file has a committed empty
piece; a deferred first partial plaintext transcript line does not create an empty capture.
Complete zstd frames are closed artifacts: all decoded bytes, including a final fragment
without a newline, are captured rather than held back as a live plaintext write.

**Aliases.** Candidates are grouped by agent and logical file key before preparation. Equal
decoded bytes are aliases; a cryptographically proven prefix may choose the longer candidate.
Divergent candidates report a conflict without selecting an arbitrary source or response order.

**Deletions.** Only complete discovery can publish a delete generation. All aliases must be
absent; generated exports use their original source paths. The generation retains its content
pieces and observation time. Incomplete discovery never implies deletion.

**Integrity and bounds.** Dependency existence, encoded lengths and checksum identities are
validated before reconstruction/publication. Cold recovery downloads metadata, not transcript
bodies. Streamed restore separately verifies encoded, decoded-piece and whole-file checksums.
A metadata record is limited to 16 MiB; recognised config parsing is similarly bounded.
LIST pages contain at most 1000 objects; historical descriptors are replayed one at a time
from SQLite cursors, not retained as an all-history array of JSON bodies. Recovery disk,
requests and CPU grow with historical metadata. Stream-ID lists, per-session launch/snapshot
projections and manifest version references also grow with their cardinalities: the record
limit is not a fixed total-machine memory guarantee. Manifests exceeding the record limit
fail explicitly without deleting their underlying history. Staging,
state and export files are private (0600, directories 0700). Unknown acknowledgements are
reconciled by exact record read-back or streamed dependency verification, never destructive cleanup.

## Manifests and launches

Every publication has schema, machine ID, kind, agent, logical key, sequence, parent, exactly
one typed payload (`file`, `manifest`, `launch` or `root`) and explicit dependencies. Dependencies
include immutable object key, encoded/body SHA-256, length and encoding; compressed dependencies
also name decoded SHA-256 and length. References must match their typed committed payloads.

A manifest payload has schema 3, agent, machine ID, hostname, namespaced session key, info,
transcripts, launch/launches, git, snapshots, local deletions, first-seen/sync times and collector
version. Each transcript contains its file key/role, size/SHA-256, line count/prefix/head, current
commit key, immutable pieces, deduplicated previous captured-content versions and history prefix.
The manifest is a valid captured view, not an atomic snapshot of a live filesystem. A failed file
capture may retain its previous committed entry beside unrelated successful captures.

A launch payload contains a typed durable envelope (`schema`, `id`, `agent`, `path`, `recorded_at`,
`payload`), namespaced session key, source root, git state and snapshot entry. The envelope ID is
assigned before queue persistence and survives retries. The queue is removed only after remote
launch durability is confirmed, or when a persistent exclusion intentionally suppresses capture.
Malformed/unreadable queued launches are retained and reported, not silently discarded.

Launch snapshots use `timing: "captured"` with the actual capture time, **not** an unconditional
`pre_session` claim. Later captures use `in_session` or `post_hoc`. Snapshot entries include set ID,
optional memory set ID, git, taken-at and mtimes. `checked_at` is a disposable local recheck hint
and is zero/omitted in immutable manifest views.

`git` records root/common directory, redacted remote, branch, HEAD, dirty flag and repository ID;
a repo without a remote is keyed by machine/git directory. Repository history is not exported.
Launch process/env/hook payload sanitisation is described in [hooks.md](hooks.md); transcript
contents are not general-purpose secret-scanned.

Session `info` uses the current main-file head where present. Otherwise launch cwd/root
metadata comes from canonical event order (`recorded_at`, then envelope ID), never catalog
replay order; the latest event carrying a cwd supplies that fallback. Main-file observations
also override an older manifest replay. Launch snapshot transitions instead use actual
`taken_at` order with stable ID ties, before adjacent deduplication; these two orders are
intentionally independent.

## Persistent exclusions

Exclusions are immutable typed records under the machine, content-addressed by exact JSON body.
They contain schema, stable exclusion ID, agent, logical/resolved root, namespaced session key,
source aliases and recorded-at. Recovery restores their monotonic union before launches,
discovery, exports, snapshots or pending-plan completion. Excluded pending plans are never
promoted; existing archived history is retained. Shared root-wide files and shared snapshot
scopes are conservatively withheld for opted-out roots. There is no automatic reactivation or
remote deletion. See [hooks.md](hooks.md) for local opt-out persistence.

## Snapshot sets

```json
{
  "kind": "harness",
  "agent": "claude",
  "user_base": "/home/me/.claude",
  "project_root": "/home/me/code/proj",
  "files": [{"scope": "user", "path": "skills/deploy/scripts/go.sh", "sha256": "…", "size": 812},
            {"scope": "user", "path": "skills/review/SKILL.md", "sha256": "…", "size": 230,
             "target": "/home/me/dotfiles/skills/review/SKILL.md"},
            {"scope": "project", "path": "src/api/CLAUDE.md", "sha256": "…", "size": 230},
            {"scope": "script", "path": "/home/me/.claude/hooks/guard.sh", "sha256": "…", "size": 90},
            {"scope": "import", "path": "/home/me/code/proj/docs/conventions.md", "sha256": "…", "size": 410}],
  "skipped": [{"scope": "plugin", "path": "big/asset.bin", "reason": "too_large", "size": 9000000}]
}
```

File bodies are stored as they are on disk, except for the secret values in config files listed under [Raw files](#raw-files); a config file that doesn't parse is listed in `skipped` with reason `unparseable`. The scopes are:

- **`user`**: the agent's config dir. Skill, agent, command, rule and hook directories are taken whole.
- **`home`**: `~/.agents/skills` for Codex and Pi.
- **`project`**: the git repo containing the session's working directory, including nested CLAUDE.md and AGENTS.md files. Outside a repo, only the directory itself is checked.
- **`ancestor`**: instruction files in the directories above the project.
- **`managed`**: administrator settings in `/etc/claude-code`, `/Library/Application Support/ClaudeCode` and `/etc/codex`.
- **`plugin`**: every file of each installed Claude Code plugin, as `<plugin>/<path>`.
- **`script`**: hook, status-line and `notify` scripts named in any settings file in the set.
- **`import`**: files a CLAUDE.md or AGENTS.md pulls in with `@path`, followed five levels deep.

Symlinks are followed, and `target` records where a file really lives. Files over 5 MB, or past 3000 in one scope, are listed in `skipped` with the reason. Each file's mtime goes in the manifest's snapshot entry, not the set, so it doesn't change the set id.

A second set of `kind: "memory"` holds memory the agent writes for itself: Claude Code's auto memory for the project (scope `auto`) and `agent-memory/` (scopes `user` and `project`), and Codex's `memories/`.
