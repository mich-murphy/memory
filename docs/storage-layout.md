# Bucket layout

This is layers 0 and 1 of the session data contract (v2): raw files, and the collector's metadata about them. Everything one machine writes is under its machine id, a UUID that `agent-sessions install` creates and keeps in `~/.config/agent-sessions/machine-id`. Hostnames collide, so the hostname is only a label in manifests.

```
raw/agent=<agent>/machine=<machine>/<key>.zst                       a local file, zstd, bytes unchanged
raw/agent=<agent>/machine=<machine>/<key>.zst.d/<start>-<end>.zst   bytes appended since, until folded
blobs/<sha256>.zst                                                  bytes a rewrite replaced, stored once
raw-versions/agent=<agent>/machine=<machine>/<key>.json             which blobs hold each earlier version
manifests/agent=<agent>/machine=<machine>/<session>.json            one per session
snapshots/blobs/<sha256>                                            a harness or memory file, stored once
snapshots/sets/<set-id>.json                                        which files made up one set
```

## Raw files

The collector walks each agent's whole directory and copies every file except:

- **Credentials**, never copied: `.credentials.json` (Claude Code's login, Codex's MCP OAuth tokens), `auth.json`, `oauth.json`, `mcp-auth.json`, any `*.key` or `*.pem`, `.env` and `.env.*` (Codex loads API keys from `$CODEX_HOME/.env`), `~/.claude.json` backups, and the collector's own `config.env`. `~/.claude.json` itself is uploaded as `claude.json`, with the secrets below redacted and its layout kept.
- **Noise**: locks, temp files, caches, `node_modules`, shell snapshots, executables, Claude Code's own install (`local/`, `downloads/`), plugin source checkouts, and Pi's `bin/` (fd and rg).
- **Live SQLite files**, which are uploaded as consistent copies instead (below).

**Secrets inside config files** are redacted before upload, by `internal/scrub`. Credential files are left out whole, but some config files hold a secret beside settings worth keeping, so only those values are replaced with `"[redacted]"`. Every other byte of the file, keys included, is kept as it was:

| File | Values redacted |
|---|---|
| `~/.claude.json` (uploaded as `claude.json`) | `primaryApiKey`, `customApiKeyResponses`, and each MCP server's `env`, `headers`, `url`, `oauth.clientSecret` and `args`, at the top level and under `projects` |
| `settings.json`, `settings.local.json`, `managed-settings.json` | `env`, and the `headers` of each `http` hook |
| `.mcp.json`, `mcp.json` (Claude Code's, and Pi's `~/.pi/agent/mcp.json`), `managed-mcp.json` | each MCP server's `env`, `headers`, `url`, `oauth.clientSecret` and `args` |
| Pi `models.json` | each provider's `apiKey` and `headers` |
| Codex `config.toml`, `managed_config.toml` | `mcp_servers.*` `env`, `http_headers`, `url`, `bearer_token` and `args`; the `headers` of each `otel` exporter (`otel.<exporter>.<otlp-http or otlp-grpc>.headers`); `model_providers.*` `http_headers`, `experimental_bearer_token` and `base_url`; `shell_environment_policy.set` |

In a command line (`args`), what is redacted is the value of a flag whose name says it holds a secret, as the next word or after `=` (`--api-key`, `--token`, `--auth-token`, `--client-secret`, `--password`; whole words, so `--max-tokens` is kept, and a next word starting with `-` isn't a value), the value in a `NAME=value` word with such a name (`GITHUB_TOKEN=…`, Codex's `-c mcp_servers.x.env.API_KEY=…`), the part of a `--header` or `-H` value after the header's name, and, in an argument that is inline JSON, what these rules redact in a settings or MCP file.

A copy of any of these files with a backup suffix (`~`, `.bak`, `.orig`, `.old`, or the `.agent-sessions-backup-<time>` that `agent-sessions install` leaves) is redacted the same way.

A value that only names a secret is kept: `${VAR}`, `$VAR`, `Bearer ${TOKEN}`, a Pi `!command`, a number or a boolean, and for Pi's `apiKey` an environment variable name. A URL keeps its scheme, host, user and path; its password is dropped and each query value redacted. The same string anywhere else in the file is redacted too. A value written in a form the collector can't find in place (a JSON `\u` escape, a TOML multi-line string) makes it rewrite the whole file from the parsed document, which loses comments and layout. A file that has these rules but doesn't parse isn't uploaded; the run counts it as an error and tries again on the next sync.

Nothing else is redacted: transcripts, history, logs and every other file are copied as they are. Files the collector doesn't recognise are uploaded with role `unknown` and counted in each run's summary.

`<key>` mirrors where the file lives under the agent's directory:

| Agent | Keys |
|---|---|
| claude | `projects/<slug>/<session>.jsonl`, `projects/<slug>/<session>/subagents/…`, `…/tool-results/…`, `…/<session>.jsonl.superseded-…`, `projects/<slug>/memory/…`, `file-history/<session>/…`, `todos/…`, `tasks/…`, `teams/…`, `session-env/…`, `debug/…`, `plans/…`, `paste-cache/…`, `usage-data/…`, `agent-memory/…`, `history.jsonl`, `settings.json`, `skills/…` and the rest of the directory, `claude.json` |
| codex | `rollouts/rollout-<ts>-<thread>[_<rollout>].jsonl` (no date folders, no `.zst`), `history.jsonl`, `session_index.jsonl`, `memories/…`, `log/…`, `config.toml`, `skills/…` …, `db/<name>.sqlite` |
| pi | `sessions/--<cwd>--/<ts>_<session>.jsonl`, `settings.json`, `models.json`, `extensions/…`, `experimental/…`, `pi-debug.log` …; sessions from a directory outside the agent dir go under `dirs/<hash of dir>/…` |

A second Claude Code or Codex directory (`CLAUDE_CONFIG_DIR`, `CODEX_HOME`) or Pi agent dir (`PI_CODING_AGENT_DIR`) is keyed under `roots/<hash of dir>/…`, so it can't overwrite the default one.

**Databases.** Each sweep (at every session start and end, and in `backfill`) takes a SQLite backup-API copy of every database under an agent's directory (Codex's `state_*`, `logs_*`, `memories_*`, `goals_*`, `thread_history_*`, `queue_*`). That includes rows still in the `-wal` file, which a plain file copy would miss. A database that changed is copied again once its last copy is an hour old. The copies live in the state directory between sweeps.

**Appends.** Transcripts and logs only grow. While a file is in use (written to in the last 15 minutes), a sync uploads only the bytes added since the last upload, as a segment `<key>.zst.d/<start>-<end>.zst` holding bytes `[start, end)`; start and end are zero-padded so the segments list in order. Once the file has been quiet for 15 minutes, or is in 64 pieces (its base object and 63 segments), the next sync uploads it whole as `<key>.zst` and deletes the segments. That fold happens once per session and compresses better than many small pieces.

A file is its base object followed by its segments. The manifest lists them as `pieces`, each with `start`, `end`, `sha256` and `object`. Without a manifest, `<key>.zst` plus each segment that starts where the bytes so far end gives the same result; segments a fold failed to delete start earlier and are skipped. A segment that reached the bucket without its upload being recorded (the answer was lost, or the sync was killed) is deleted at the start of the next sync, before that file uploads again, so it can't stand in for the bytes after its start; if it can't be deleted, the file is uploaded whole instead.

**Versions.** When a file changes in any way other than an append (its first `old size` bytes no longer hash to what was uploaded), each piece of its old contents is copied to `blobs/<sha256>.zst` before the new bytes are uploaded. A blob that is already there isn't copied again, so identical old contents from different files, sessions or machines are stored once. `raw-versions/…/<key>.json` lists the file's earlier versions, and the manifest lists them under `versions`; each has `sha256`, `size`, `versioned_at` and the `pieces` (in `blobs/`) that rebuild it. Database copies keep at most one version a day: an hourly refresh within a day of the last kept version replaces the copy without keeping it.

Every object is zstd (level 10) with these metadata fields: `sha256` (of the uncompressed bytes the object holds), `start`, `end`, `agent`, `machine` and `role`.

**Deletions.** When a sweep finds that an uploaded file is gone from disk, the file gets a `deleted_locally` date in its session's manifest. The object stays in the bucket.

**Large files.** Files over 64 MB are hashed and compressed in a stream and uploaded from a temp file (multipart on R2).

## Manifests

There's one manifest per session: a Claude Code session (`<slug>/<session id>`), a Codex rollout, or a Pi session file. It's rewritten whenever one of the session's files changes.

```json
{
  "schema": 2,
  "agent": "claude",
  "machine_id": "48b3b69a-a5a9-4d31-9a9c-25564d5ae786",
  "hostname": "laptop",
  "session_key": "-home-me-code-proj/11111111-…",
  "info": {"session_id": "…", "cwd": "/home/me/code/proj", "git_branch": "main", "agent_version": "2.1.296",
           "entrypoint": "cli", "source_root": "/home/me/.claude"},
  "transcripts": [{"key": "projects/…/11111111-….jsonl", "role": "main", "size": 48213, "sha256": "…",
                   "line_count": 212, "prefix_sha256": "…", "head": {"session_id": "…", "cwd": "…"},
                   "object": "raw/agent=claude/machine=…/projects/…/11111111-….jsonl.zst",
                   "pieces": [{"start": 0, "end": 40110, "sha256": "…", "object": "raw/…/11111111-….jsonl.zst"},
                              {"start": 40110, "end": 48213, "sha256": "…",
                               "object": "raw/…/11111111-….jsonl.zst.d/000000000040110-000000000048213.zst"}],
                   "versions": []}],
  "launch": {"event": "SessionStart", "source": "startup", "cwd": "/home/me/code/proj", "permission_mode": "default",
             "process": {"argv": ["claude", "--model", "opus"], "pid": 4242, "ancestry": ["sh", "claude"]},
             "env": {"CLAUDE_CODE_ENTRYPOINT": "cli"}, "hook_input": {"…": "…"}, "recorded_at": 1791634550.2,
             "git": {"…": "…"}, "snapshot": "<set-id>"},
  "launches": ["…every SessionStart, including resumes…"],
  "git": {"root": "/home/me/code/proj", "common_dir": "/home/me/code/proj/.git", "remote": "https://github.com/me/proj.git",
          "branch": "main", "head": "…", "dirty": false, "repo_id": "401e2f7a41fe430b"},
  "snapshots": [{"id": "<set-id>", "memory_id": "<set-id>", "timing": "pre_session", "taken_at": 1791634550.4,
                 "git": {"…": "…"}, "mtimes": {"harness:user:CLAUDE.md": 1791000000.0}}],
  "deleted_locally": [{"key": "…", "at": 1791700000.0}],
  "first_seen": 1791634550.4,
  "synced_at": 1791634560.1,
  "collector_version": "0.3.0"
}
```

- **`transcripts`** lists every file of the session, with the `pieces` that make up its current bytes and its earlier `versions`. `role` is `main`, `subagent`, `tool_result`, `superseded` (an earlier transcript Claude Code set aside), `file_history`, `todo`, `task`, `session_env`, `debug` or `aux`. `line_count` and `prefix_sha256` (the hash of the first 20 lines) let the normaliser pin an input and spot a rewritten file. `head` is what the file's first lines say about it, each field taking the first non-empty value in those lines, so a Claude subagent file carries its own `agent_id`.
- **`launch`** is recorded by the session-start hook:
  - `process.argv` is the agent's command line, found by walking up from the hook to the agent's process. Pi hides its arguments from the process table, so its extension passes `process.argv` (`via: "extension"`). Secrets in it are redacted as in MCP server `args` ([Raw files](#raw-files)).
  - `env` keeps `CLAUDE_CODE_*`, `CODEX_*`, `PI_*`, `ANTHROPIC_MODEL`, `MAX_THINKING_TOKENS` and a few other variables, minus any whose name has the word `TOKEN`, `KEY` or `AUTH`, or `SECRET`, `PASSWORD`, `CREDENTIAL` or `COOKIE` anywhere (`MAX_THINKING_TOKENS` is kept).
  - `hook_input` is the hook's whole payload.
  - Sessions from before agent-sessions was installed have no launch.
- **`git`** is the repo state when the session started, or at its first snapshot if there was no launch. `common_dir` points worktrees at their main repo. A token in the remote URL is dropped. `repo_id` is a hash of the normalised remote URL (`github.com/owner/repo`), so clones on different machines share it; a repo with no remote is keyed by machine and git directory instead. The repository's history isn't exported: for a repo with a remote, fetch it from there when analysing.
- **`snapshots`** get a new entry when the harness set, the memory set, the branch or HEAD changes, checked at most every 10 minutes. `timing` says how far to trust it: `pre_session` (taken at session start), `in_session` (taken while the session was running) or `post_hoc` (a sweep or backfill of a session that had already finished, so the harness may have changed since).

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
