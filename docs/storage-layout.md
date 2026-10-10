# Bucket layout

Everything one machine writes is under its machine id (`MEMSYNC_MACHINE_ID`, by default the hostname). A re-upload overwrites the same key, so the bucket always holds the latest copy of each file.

```
raw/agent=<agent>/machine=<machine>/<key>.gz       a local file, gzip-compressed, bytes unchanged
manifests/agent=<agent>/machine=<machine>/<session>.json
snapshots/blobs/<sha256>                           one harness file (redacted), stored once
snapshots/sets/<set-id>.json                       which files made up one harness
```

`<key>` mirrors where the file lives under the agent's directory:

| Agent | Keys |
|---|---|
| claude | `projects/<slug>/<session>.jsonl`, `projects/<slug>/<session>/subagents/…`, `…/tool-results/…`, `projects/<slug>/<session>.jsonl.superseded-…`, `projects/<slug>/memory/…`, `plans/<name>.md`, `history.jsonl` |
| codex | `rollouts/rollout-<ts>-<thread>[_<rollout>].jsonl` (no date folders, no `.zst`), `history.jsonl`, `session_index.jsonl`, `state/threads.jsonl` |
| pi | `sessions/--<cwd>--/<ts>_<session>.jsonl`; sessions from another directory go under `dirs/<hash of dir>/…` |

Each object carries `sha256` (of the uncompressed bytes), `agent`, `machine` and `role` as metadata.

## Manifests

There's one manifest per session: a Claude Code session, a Codex rollout, or a Pi session file. It's rewritten whenever one of the session's files changes.

```json
{
  "schema": 1,
  "agent": "claude",
  "machine": "laptop",
  "session_key": "11111111-…",
  "info": {"session_id": "…", "cwd": "/home/me/code/proj", "git_branch": "main", "agent_version": "2.1.296",
           "source_root": "/home/me/.claude"},
  "files": [{"key": "projects/…/11111111-….jsonl", "role": "main", "size": 48213, "sha256": "…",
             "object": "raw/agent=claude/machine=laptop/projects/…/11111111-….jsonl.gz"}],
  "snapshots": [{"id": "<set-id>", "git": {"remote": "…", "branch": "main", "commit": "…"},
                 "taken_at": 1791634556.9, "during_backfill": false}],
  "first_seen": 1791634556.9,
  "synced_at": 1791634560.1,
  "memsync_version": "0.1.0"
}
```

- **`role`** is one of:
  - `main`, the transcript
  - `subagent`
  - `tool_result`, output spilled to its own file
  - `superseded`, an earlier transcript Claude Code set aside
  - `aux`
- **`info`** comes from the transcript's first lines:
  - Codex adds `parent_session` for subagent threads and `forked_from` for forks.
  - Pi adds `parent_session` for `/fork` and `/clone`.
- **`snapshots`** get a new entry whenever the harness or the repo's commit changes, checked at most every 10 minutes while a session is active. A backfilled old session gets the harness as it was at backfill time, marked `during_backfill: true`.

## Snapshot sets

```json
{
  "agent": "claude",
  "user_base": "/home/me/.claude",
  "project_root": "/home/me/code/proj",
  "files": [{"scope": "user", "path": "CLAUDE.md", "sha256": "…", "size": 812},
            {"scope": "project", "path": "src/api/CLAUDE.md", "sha256": "…", "size": 230},
            {"scope": "ancestor", "path": "/home/me/code/CLAUDE.md", "sha256": "…", "size": 90},
            {"scope": "user", "path": ".claude.json#mcp", "sha256": "…", "size": 410}]
}
```

There are three scopes:

- **`user`**: the agent's config dir.
- **`project`**: the git repo containing the session's working directory. Outside a repo, only the directory itself is checked.
- **`ancestor`**: instruction files in the directories above the project, which every agent also loads.

Settings, MCP configs, TOML and `.env`-style files are redacted before upload: values under `env` and `headers` keys, keys that look like secrets, and well-known token formats.
