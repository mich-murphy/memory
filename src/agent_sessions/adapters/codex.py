"""OpenAI Codex CLI.

Layout (checked against openai/codex codex-rs/rollout, 2026-10-10):
  sessions/YYYY/MM/DD/rollout-<ts>-<thread-id>.jsonl               one file per thread
  sessions/.../rollout-<ts>-<thread-id>_<rollout-id>.jsonl         after a thread revert
  *.jsonl.zst                                                       rollouts untouched for 7 days are
                                                                    recompressed; resuming one
                                                                    decompresses it back to .jsonl
  archived_sessions/...                                             archived threads, same names
  history.jsonl, session_index.jsonl                                prompt history, thread names
  memories/, log/                                                   Codex's own memories, TUI log
  state_<n>.sqlite, logs_<n>.sqlite, ...                            thread metadata, logs, goals...
  config.toml, hooks.json, AGENTS.md, rules/, skills/, prompts/     the harness
Subagents and forks get their own rollout files; session_meta records parent_thread_id and
forked_from_id. CODEX_HOME moves the tree (default ~/.codex). auth.json and .credentials.json
(MCP OAuth tokens when there's no keyring) are never copied.

Codex keeps per-thread metadata (titles, token totals, sandbox and approval mode) in its SQLite
databases, and its "paginated" history mode keeps some of it only there, so sweeps upload a
backup-API copy of every database under db/. Rollouts stay the source of truth for the
conversation.

Keys drop the date folders and the .zst suffix, so a rollout keeps one key when Codex
recompresses or archives it, and an unchanged body is not uploaded twice.
"""

from __future__ import annotations

import os
import re
from pathlib import Path

from .base import Adapter, SessionFile, SnapshotRules

ROLLOUT_RE = re.compile(
    r"^rollout-\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}-(?P<thread>[0-9a-fA-F-]{36})(?:_[0-9a-fA-F-]{36})?\.jsonl(?:\.zst)?$"
)
HISTORY_FILES = ("history.jsonl", "session_index.jsonl")
SESSION_DIRS = ("sessions", "archived_sessions")
TOP_LEVEL_ROLES = {"memories": "memory", "log": "log", "rules": "config", "skills": "config", "prompts": "config",
                   "plugins": "config"}
CONFIG_FILES = {"config.toml", "hooks.json", "AGENTS.md", "AGENTS.override.md", "requirements.toml", "managed_config.toml"}


class CodexAdapter(Adapter):
    name = "codex"
    skip_dirs = frozenset({"plugins/cache", "bin", "packages"})
    managed_paths = ("/etc/codex/config.toml", "/etc/codex/requirements.toml", "/etc/codex/managed_config.toml")
    snapshot_rules = SnapshotRules(
        user=("AGENTS.md", "AGENTS.override.md", "config.toml", "hooks.json", "rules/**", "skills/**", "prompts/**"),
        project=(
            "AGENTS.md",
            "AGENTS.override.md",
            "**/AGENTS.md",
            "**/AGENTS.override.md",
            ".codex/config.toml",
            ".codex/hooks.json",
            ".codex/rules/**",
            ".codex/skills/**",
            ".agents/skills/**",
        ),
        home=(".agents/skills/**",),
    )

    def default_roots(self) -> list[Path]:
        override = os.environ.get("CODEX_HOME")
        return [Path(override).expanduser() if override else self.canonical_root()]

    def canonical_root(self) -> Path:
        return self.home / ".codex"

    def infer_root(self, path: Path) -> Path | None:
        if not ROLLOUT_RE.match(path.name):
            return None
        for parent in path.parents:
            if parent.name in SESSION_DIRS:
                return parent.parent
        return None

    def classify(self, path: Path, root: Path) -> SessionFile | None:
        try:
            rel = path.relative_to(root)
        except ValueError:
            return None
        prefix = self.key_prefix(root)
        m = ROLLOUT_RE.match(path.name)
        if m and rel.parts[0] in SESSION_DIRS:
            compression = "zst" if path.name.endswith(".zst") else None
            name = path.name.removesuffix(".zst")
            return SessionFile(self.name, path, f"{prefix}rollouts/{name}", name.removesuffix(".jsonl"), "main", compression)
        top = rel.parts[0]
        if len(rel.parts) == 1:
            role = "history" if top in HISTORY_FILES else "config" if top in CONFIG_FILES else "unknown"
        elif top in SESSION_DIRS:
            role = "unknown"
        else:
            role = TOP_LEVEL_ROLES.get(top, "unknown")
        return SessionFile(self.name, path, prefix + rel.as_posix(), None, role)

    def memory_dirs(self, root: Path, project_root: Path | None, session_key: str | None) -> list[tuple[str, Path]]:
        return [("user", root / "memories")]

    def session_info(self, head_lines: list[dict]) -> dict:
        info: dict = {}
        for d in head_lines:
            payload = d.get("payload")
            if not isinstance(payload, dict):
                continue
            if d.get("type") == "session_meta":
                info.setdefault("session_id", payload.get("id"))
                info.setdefault("root_session_id", payload.get("session_id"))
                info.setdefault("cwd", payload.get("cwd"))
                info.setdefault("agent_version", payload.get("cli_version"))
                info.setdefault("originator", payload.get("originator"))
                info.setdefault("forked_from", payload.get("forked_from_id"))
                info.setdefault("parent_session", payload.get("parent_thread_id"))
                info.setdefault("agent_role", payload.get("agent_role"))
                git = payload.get("git") or d.get("git") or {}
                if isinstance(git, dict):
                    info.setdefault("git_branch", git.get("branch"))
                    info.setdefault("git_commit", git.get("commit_hash"))
                    info.setdefault("git_remote", git.get("repository_url"))
            elif d.get("type") == "turn_context":
                info.setdefault("cwd", payload.get("cwd"))
                info.setdefault("model", payload.get("model"))
        return {k: v for k, v in info.items() if v}
