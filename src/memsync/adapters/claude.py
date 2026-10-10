"""Claude Code.

Layout (checked against https://code.claude.com/docs/en/claude-directory and a real ~/.claude,
Claude Code 2.1.x):
  projects/<cwd-slug>/<session-id>.jsonl                          main transcript
  projects/<cwd-slug>/<session-id>.orphaned-<ts>-<suffix>.jsonl   earlier transcript set aside
  projects/<cwd-slug>/<session-id>.jsonl.superseded-<ts>          earlier transcript set aside
  projects/<cwd-slug>/<session-id>/subagents/agent-<id>.jsonl     subagent transcript (+ .meta.json)
  projects/<cwd-slug>/<session-id>/tool-results/<file>            large tool output spilled to disk
  projects/<cwd-slug>/memory/                                     auto memory (Claude's own notes)
  plans/<name>.md                                                 plan-mode plans
  history.jsonl                                                   prompt history
CLAUDE_CONFIG_DIR moves the whole tree. Everything except auto memory is deleted after
cleanupPeriodDays (default 30), which `memsync install` raises.
"""

from __future__ import annotations

import json
import os
import re
from collections.abc import Iterator
from pathlib import Path

from ..redact import redact_json_bytes
from .base import Adapter, SessionFile, SnapshotRules, files_under

MAIN_RE = re.compile(r"^(?P<sid>[^./]+)\.jsonl$")
SET_ASIDE_RE = re.compile(r"^(?P<sid>[^./]+)\.(?:orphaned-.+\.jsonl|jsonl\.superseded-.+)$")
SESSION_SUBDIR_ROLES = {"subagents": "subagent", "tool-results": "tool_result"}


class ClaudeAdapter(Adapter):
    name = "claude"
    snapshot_rules = SnapshotRules(
        user=(
            "CLAUDE.md",
            "settings.json",
            "rules/**/*.md",
            "skills/*/SKILL.md",
            "agents/*.md",
            "commands/**/*.md",
            "output-styles/*.md",
            "plugins/installed_plugins.json",
            "agent-memory/**/*.md",
        ),
        project=(
            "CLAUDE.md",
            "CLAUDE.local.md",
            "AGENTS.md",
            "**/CLAUDE.md",
            "**/AGENTS.md",
            ".claude/settings.json",
            ".claude/settings.local.json",
            ".claude/rules/**/*.md",
            ".claude/skills/*/SKILL.md",
            ".claude/agents/*.md",
            ".claude/commands/**/*.md",
            ".claude/agent-memory/**/*.md",
            ".mcp.json",
        ),
    )

    def default_roots(self) -> list[Path]:
        override = os.environ.get("CLAUDE_CONFIG_DIR")
        return [Path(override).expanduser() if override else self.home / ".claude"]

    def infer_root(self, path: Path) -> Path | None:
        for parent in path.parents:
            if parent.name == "projects" and len(path.relative_to(parent).parts) >= 2:
                return parent.parent
        return None

    def discover(self, root: Path) -> Iterator[SessionFile]:
        yield from super().discover(root)
        hist = root / "history.jsonl"
        if hist.is_file():
            yield SessionFile(self.name, hist, "history.jsonl", None, "aux")

    def scan_dirs(self, root: Path) -> list[Path]:
        return [root / "projects", root / "plans"]

    def classify(self, path: Path, root: Path) -> SessionFile | None:
        if path == root / "history.jsonl":
            return SessionFile(self.name, path, "history.jsonl", None, "aux")
        try:
            rel = path.relative_to(root)
        except ValueError:
            return None
        parts = rel.parts
        key = rel.as_posix()
        if parts[0] == "plans" and len(parts) == 2 and path.suffix == ".md":
            return SessionFile(self.name, path, key, None, "aux")
        if parts[0] != "projects" or len(parts) < 3:
            return None
        name = parts[2]
        if len(parts) == 3:
            if m := MAIN_RE.match(name):
                return SessionFile(self.name, path, key, m["sid"], "main")
            if m := SET_ASIDE_RE.match(name):
                return SessionFile(self.name, path, key, m["sid"], "superseded")
            return None
        if name == "memory":
            return SessionFile(self.name, path, key, None, "aux")
        role = SESSION_SUBDIR_ROLES.get(parts[3], "aux") if len(parts) > 4 else "aux"
        return SessionFile(self.name, path, key, name, role)

    def related(self, sf: SessionFile, root: Path) -> list[Path]:
        # A hook points at <session>.jsonl or at a subagent file inside <session>/: sync the lot.
        if not sf.session_key:
            return []
        project_dir = root / Path(sf.key).parts[0] / Path(sf.key).parts[1]
        main = project_dir / f"{sf.session_key}.jsonl"
        out = [main] if main.is_file() else []
        out.extend(files_under([project_dir / sf.session_key]))
        out.extend(p for p in project_dir.glob(f"{sf.session_key}.*") if p != main and p.is_file())
        return out

    def extra_snapshot_entries(self, root: Path, project_root: Path | None) -> list[tuple[str, str, bytes]]:
        # User- and project-scoped MCP servers live in ~/.claude.json next to OAuth and UI state,
        # so record only the MCP parts, redacted.
        for candidate in (root / ".claude.json", self.home / ".claude.json"):
            if candidate.is_file():
                break
        else:
            return []
        try:
            data = json.loads(candidate.read_text())
        except (OSError, ValueError):
            return []
        if not isinstance(data, dict):
            return []
        picked: dict = {"mcpServers": data.get("mcpServers") or {}}
        projects = data.get("projects") if isinstance(data.get("projects"), dict) else {}
        if project_root is not None and isinstance(projects.get(str(project_root)), dict):
            proj = projects[str(project_root)]
            picked["project"] = {
                k: proj[k] for k in ("mcpServers", "enabledMcpjsonServers", "disabledMcpjsonServers") if k in proj
            }
        return [("user", ".claude.json#mcp", redact_json_bytes(picked))]

    def session_info(self, head_lines: list[dict]) -> dict:
        info: dict = {}
        for d in head_lines:
            for src, dst in (("sessionId", "session_id"), ("cwd", "cwd"), ("gitBranch", "git_branch"), ("version", "agent_version")):
                if dst not in info and d.get(src):
                    info[dst] = d[src]
        return info
