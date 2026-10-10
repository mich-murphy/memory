"""Pi coding agent (earendil-works/pi; npm @earendil-works/pi-coding-agent).

Layout (checked against packages/coding-agent/docs/session-format.md and session-manager.ts,
2026-10-10):
  <agent-dir>/sessions/--<cwd encoded>--/<timestamp>_<session-id>.jsonl
  <sessionDir>/<timestamp>_<session-id>.jsonl      when --session-dir, PI_CODING_AGENT_SESSION_DIR
                                                   or the `sessionDir` setting picks another place
Format v3: the first line is {"type":"session","version":3,"id",...,"cwd","parentSession"?} and
entries form a tree via id/parentId. Append-only; /tree branches stay in the same file, /fork and
/clone write new files. The file is created only once the session has a user or assistant
message, and --no-session runs never write one. PI_CODING_AGENT_DIR moves the agent dir
(default ~/.pi/agent).
"""

from __future__ import annotations

import json
import os
from pathlib import Path

from .base import Adapter, SessionFile, SnapshotRules, root_tag


class PiAdapter(Adapter):
    name = "pi"
    snapshot_rules = SnapshotRules(
        user=(
            "AGENTS.override.md",
            "AGENTS.md",
            "CLAUDE.md",
            "SYSTEM.md",
            "APPEND_SYSTEM.md",
            "settings.json",
            "mcp.json",
            "models.json",
            "skills/**/SKILL.md",
            "prompts/*.md",
            "extensions/*.ts",
            "extensions/*.js",
            "extensions/*/index.ts",
            "extensions/*/index.js",
        ),
        project=(
            "AGENTS.override.md",
            "AGENTS.md",
            "CLAUDE.md",
            "**/AGENTS.md",
            "**/CLAUDE.md",
            ".pi/settings.json",
            ".pi/mcp.json",
            ".pi/SYSTEM.md",
            ".pi/APPEND_SYSTEM.md",
            ".pi/skills/**/SKILL.md",
            ".pi/prompts/*.md",
            ".pi/extensions/*.ts",
            ".pi/extensions/*/index.ts",
        ),
    )

    def agent_dir(self) -> Path:
        override = os.environ.get("PI_CODING_AGENT_DIR")
        return Path(override).expanduser() if override else self.home / ".pi" / "agent"

    def default_roots(self) -> list[Path]:
        roots = [self.agent_dir() / "sessions"]
        override = os.environ.get("PI_CODING_AGENT_SESSION_DIR")
        if override:
            roots.append(Path(override).expanduser())
        try:
            setting = json.loads((self.agent_dir() / "settings.json").read_text()).get("sessionDir")
        except (OSError, ValueError, AttributeError):
            setting = None
        if isinstance(setting, str) and setting:
            p = Path(setting).expanduser()
            if p.is_absolute():  # relative values resolve per working directory; hooks report those
                roots.append(p)
        return list(dict.fromkeys(roots))

    def infer_root(self, path: Path) -> Path | None:
        if path.suffix != ".jsonl":
            return None
        parent = path.parent
        if parent.name.startswith("--") and parent.name.endswith("--"):
            return parent.parent
        return parent

    def classify(self, path: Path, root: Path) -> SessionFile | None:
        try:
            rel = path.relative_to(root)
        except ValueError:
            return None
        if path.suffix != ".jsonl" or len(rel.parts) > 2:
            return None
        prefix = "sessions" if root == self.agent_dir() / "sessions" else f"dirs/{root_tag(root)}"
        return SessionFile(self.name, path, f"{prefix}/{rel.as_posix()}", path.stem, "main")

    def user_base(self, root: Path) -> Path:
        return self.agent_dir()

    def session_info(self, head_lines: list[dict]) -> dict:
        for d in head_lines:
            if d.get("type") == "session":
                info = {
                    "session_id": d.get("id"),
                    "cwd": d.get("cwd"),
                    "format_version": d.get("version"),
                    "parent_session": d.get("parentSession"),
                }
                return {k: v for k, v in info.items() if v}
        return {}
