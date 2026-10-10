"""OpenAI Codex CLI.

Layout (checked against openai/codex codex-rs/rollout, 2026-10-10):
  sessions/YYYY/MM/DD/rollout-<ts>-<thread-id>.jsonl               one file per thread
  sessions/.../rollout-<ts>-<thread-id>_<rollout-id>.jsonl         after a thread revert
  *.jsonl.zst                                                       rollouts untouched for 7 days are
                                                                    recompressed; resuming one
                                                                    decompresses it back to .jsonl
  archived_sessions/...                                             archived threads, same names
  history.jsonl, session_index.jsonl                                prompt history, thread names
Subagents and forks get their own rollout files; session_meta records parent_thread_id and
forked_from_id. CODEX_HOME moves the tree (default ~/.codex).

Codex also keeps per-thread metadata in state_<n>.sqlite (titles, token totals, sandbox and
approval mode). Its "paginated" history mode keeps some of it only there, so sweeps export the
threads table as state/threads.jsonl. Rollouts stay the source of truth for the conversation.

Keys drop the date folders and the .zst suffix, so a rollout keeps one key when Codex
recompresses or archives it, and an unchanged body is not uploaded twice.
"""

from __future__ import annotations

import json
import os
import re
import sqlite3
from collections.abc import Iterator
from pathlib import Path

from .base import Adapter, SessionFile, SnapshotRules

ROLLOUT_RE = re.compile(
    r"^rollout-\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}-(?P<thread>[0-9a-fA-F-]{36})(?:_[0-9a-fA-F-]{36})?\.jsonl(?:\.zst)?$"
)
AUX_FILES = ("history.jsonl", "session_index.jsonl")
SESSION_DIRS = ("sessions", "archived_sessions")


class CodexAdapter(Adapter):
    name = "codex"
    snapshot_rules = SnapshotRules(
        user=("AGENTS.md", "AGENTS.override.md", "config.toml", "hooks.json", "rules/*.rules", "skills/**/SKILL.md", "prompts/*.md"),
        project=(
            "AGENTS.md",
            "AGENTS.override.md",
            "**/AGENTS.md",
            "**/AGENTS.override.md",
            ".codex/config.toml",
            ".codex/hooks.json",
            ".codex/skills/**/SKILL.md",
            ".agents/skills/**/SKILL.md",
        ),
    )

    def default_roots(self) -> list[Path]:
        override = os.environ.get("CODEX_HOME")
        return [Path(override).expanduser() if override else self.home / ".codex"]

    def infer_root(self, path: Path) -> Path | None:
        if not ROLLOUT_RE.match(path.name):
            return None
        for parent in path.parents:
            if parent.name in SESSION_DIRS:
                return parent.parent
        return None

    def scan_dirs(self, root: Path) -> list[Path]:
        return [root / d for d in SESSION_DIRS]

    def discover(self, root: Path) -> Iterator[SessionFile]:
        yield from super().discover(root)
        for name in AUX_FILES:
            if (root / name).is_file():
                yield SessionFile(self.name, root / name, name, None, "aux")

    def classify(self, path: Path, root: Path) -> SessionFile | None:
        if path.parent == root and path.name in AUX_FILES:
            return SessionFile(self.name, path, path.name, None, "aux")
        m = ROLLOUT_RE.match(path.name)
        if not m:
            return None
        compression = "zst" if path.name.endswith(".zst") else None
        name = path.name.removesuffix(".zst")
        return SessionFile(self.name, path, f"rollouts/{name}", name.removesuffix(".jsonl"), "main", compression)

    def exports(self, root: Path) -> list[tuple[str, bytes]]:
        dbs = sorted(root.glob("state_*.sqlite"), key=lambda p: int(re.sub(r"\D", "", p.stem) or 0))
        if not dbs:
            return []
        try:
            con = sqlite3.connect(f"file:{dbs[-1]}?mode=ro", uri=True, timeout=5)
            try:
                con.row_factory = sqlite3.Row
                rows = con.execute("SELECT * FROM threads ORDER BY id").fetchall()
            finally:
                con.close()
        except sqlite3.Error:
            return []
        body = "".join(json.dumps(dict(r), sort_keys=True, default=str) + "\n" for r in rows)
        return [("state/threads.jsonl", body.encode())]

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
