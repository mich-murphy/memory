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

The whole agent dir is captured (settings, models, mcp.json, extensions, prompts, skills, themes,
experimental sessions, the debug and MCP logs) except auth.json, oauth.json, mcp-auth.json and
the fd/rg binaries in bin/. A session dir outside the agent dir only contributes .jsonl files.
"""

from __future__ import annotations

import json
import os
from pathlib import Path

from .base import Adapter, SessionFile, SnapshotRules, is_within, root_tag

TOP_LEVEL_ROLES = {"extensions": "config", "prompts": "config", "skills": "config", "themes": "config",
                   "tools": "config", "experimental": "experimental"}
CONFIG_FILES = {"settings.json", "models.json", "models-store.json", "mcp.json", "keybindings.json", "AGENTS.md",
                "AGENTS.override.md", "CLAUDE.md", "SYSTEM.md", "APPEND_SYSTEM.md"}


class PiAdapter(Adapter):
    name = "pi"
    skip_dirs = frozenset({"bin"})
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
            "skills/**",
            "prompts/**",
            "extensions/**",
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
            ".pi/skills/**",
            ".pi/prompts/**",
            ".pi/extensions/**",
            ".agents/skills/**",
        ),
        home=(".agents/skills/**",),
    )

    def agent_dir(self) -> Path:
        override = os.environ.get("PI_CODING_AGENT_DIR")
        return Path(override).expanduser() if override else self.home / ".pi" / "agent"

    def canonical_root(self) -> Path:
        return self.home / ".pi" / "agent"

    def default_roots(self) -> list[Path]:
        agent_dir = self.agent_dir()
        roots = [agent_dir]
        override = os.environ.get("PI_CODING_AGENT_SESSION_DIR")
        if override:
            roots.append(Path(override).expanduser())
        try:
            setting = json.loads((agent_dir / "settings.json").read_text()).get("sessionDir")
        except (OSError, ValueError, AttributeError):
            setting = None
        if isinstance(setting, str) and setting:
            p = Path(setting).expanduser()
            if p.is_absolute():  # relative values resolve per working directory; hooks report those
                roots.append(p)
        # A session dir inside the agent dir is already covered by walking the agent dir.
        return list(dict.fromkeys(r for r in roots if r == agent_dir or not is_within(r, agent_dir)))

    def is_agent_dir(self, root: Path) -> bool:
        return root in (self.canonical_root(), self.agent_dir()) or (root / "sessions").is_dir()

    def infer_root(self, path: Path) -> Path | None:
        if path.suffix != ".jsonl":
            return None
        parent = path.parent
        if parent.name.startswith("--") and parent.name.endswith("--"):
            # <agent dir>/sessions/--cwd--/file.jsonl: the root is the agent dir, so a custom
            # PI_CODING_AGENT_DIR gets the same keys from hooks and sweeps.
            return parent.parent.parent if parent.parent.name == "sessions" else parent.parent
        return parent

    def classify(self, path: Path, root: Path) -> SessionFile | None:
        try:
            rel = path.relative_to(root)
        except ValueError:
            return None
        if self.is_agent_dir(root):
            prefix = self.key_prefix(root)
            parts = rel.parts
            if parts[0] == "sessions" and path.suffix == ".jsonl" and len(parts) <= 3:
                return SessionFile(self.name, path, prefix + rel.as_posix(), path.stem, "main")
            if len(parts) == 1:
                role = "config" if parts[0] in CONFIG_FILES else "log" if parts[0].endswith(".log") else "unknown"
            else:
                role = TOP_LEVEL_ROLES.get(parts[0], "unknown")
            return SessionFile(self.name, path, prefix + rel.as_posix(), None, role)
        if path.suffix != ".jsonl" or len(rel.parts) > 2:
            return None
        return SessionFile(self.name, path, f"dirs/{root_tag(root)}/{rel.as_posix()}", path.stem, "main")

    def user_base(self, root: Path) -> Path:
        return root if self.is_agent_dir(root) else self.agent_dir()

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
