"""Claude Code.

Layout (checked against https://code.claude.com/docs/en/claude-directory and a real ~/.claude,
Claude Code 2.1.x):
  projects/<cwd-slug>/<session-id>.jsonl                          main transcript
  projects/<cwd-slug>/<session-id>.orphaned-<ts>-<suffix>.jsonl   earlier transcript set aside
  projects/<cwd-slug>/<session-id>.jsonl.superseded-<ts>          earlier transcript set aside
  projects/<cwd-slug>/<session-id>/subagents/agent-<id>.jsonl     subagent transcript (+ .meta.json)
  projects/<cwd-slug>/<session-id>/tool-results/<file>            large tool output spilled to disk
  projects/<cwd-slug>/memory/                                     auto memory (Claude's own notes)
  file-history/<session-id>/<hash>@v<n>                           file contents before each edit
  todos/<session-id>-agent-<agent-id>.json, tasks/<list-id>/      todo and task lists
  teams/<team>/                                                   agent-team config and mailboxes
  session-env/<session-id>/, debug/<session-id>.txt               per-session env and debug log
  plans/, paste-cache/, usage-data/, agent-memory/, history.jsonl
  settings.json, CLAUDE.md, skills/, agents/, commands/, rules/, hooks/ ...   the harness
~/.claude.json (outside the dir unless CLAUDE_CONFIG_DIR is set) holds MCP servers, per-project
settings and UI state next to the API key, so it is exported without `primaryApiKey`.
CLAUDE_CONFIG_DIR moves the whole tree. Everything except auto memory is deleted after
cleanupPeriodDays (default 30), which `agent-sessions install` raises.
"""

from __future__ import annotations

import json
import os
import re
import time
from pathlib import Path

from .base import Adapter, SessionFile, SnapshotRules, files_under, root_tag

MAIN_RE = re.compile(r"^(?P<sid>[^./]+)\.jsonl$")
SET_ASIDE_RE = re.compile(r"^(?P<sid>[^./]+)\.(?:orphaned-.+\.jsonl|jsonl\.superseded-.+)$")
UUID_RE = re.compile(r"^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}")
SESSION_SUBDIR_ROLES = {"subagents": "subagent", "tool-results": "tool_result"}
# Top-level directories whose files belong to one session, found from the session id in the path.
PER_SESSION_DIRS = {"file-history": "file_history", "session-env": "session_env", "debug": "debug", "todos": "todo", "tasks": "task"}
TOP_LEVEL_ROLES = {
    "plans": "plan", "teams": "team", "paste-cache": "paste", "usage-data": "usage", "agent-memory": "memory",
    "skills": "config", "agents": "config", "commands": "config", "rules": "config", "output-styles": "config",
    "hooks": "config", "plugins": "config", "sessions": "liveness",
}
CONFIG_FILES = {"settings.json", "settings.local.json", "CLAUDE.md", "keybindings.json", "launcher-settings.json",
                "remote-settings.json", "policy-limits.json"}


class ClaudeAdapter(Adapter):
    name = "claude"
    # plugins/cache and plugins/marketplaces are git checkouts of plugin sources (snapshots record
    # the installed plugins); local/ and downloads/ hold Claude Code's own install; backups/ holds
    # copies of ~/.claude.json, API key included.
    skip_dirs = frozenset({"plugins/cache", "plugins/marketplaces", "local", "downloads", "backups", "statsig", "ide", "telemetry"})
    managed_paths = (
        "/etc/claude-code/managed-settings.json",
        "/etc/claude-code/managed-mcp.json",
        "/etc/claude-code/CLAUDE.md",
        "/Library/Application Support/ClaudeCode/managed-settings.json",
        "/Library/Application Support/ClaudeCode/managed-mcp.json",
        "/Library/Application Support/ClaudeCode/CLAUDE.md",
    )
    snapshot_rules = SnapshotRules(
        user=(
            "CLAUDE.md",
            "settings.json",
            "settings.local.json",
            "launcher-settings.json",
            "keybindings.json",
            "rules/**",
            "skills/**",
            "agents/**",
            "commands/**",
            "output-styles/**",
            "hooks/**",
            "plugins/installed_plugins.json",
            "plugins/known_marketplaces.json",
        ),
        project=(
            "CLAUDE.md",
            "CLAUDE.local.md",
            "AGENTS.md",
            "**/CLAUDE.md",
            "**/CLAUDE.local.md",
            "**/AGENTS.md",
            ".claude/settings.json",
            ".claude/settings.local.json",
            ".claude/rules/**",
            ".claude/skills/**",
            ".claude/agents/**",
            ".claude/commands/**",
            ".claude/output-styles/**",
            ".claude/hooks/**",
            ".mcp.json",
        ),
    )

    def default_roots(self) -> list[Path]:
        override = os.environ.get("CLAUDE_CONFIG_DIR")
        return [Path(override).expanduser() if override else self.canonical_root()]

    def canonical_root(self) -> Path:
        return self.home / ".claude"

    def infer_root(self, path: Path) -> Path | None:
        for parent in path.parents:
            if parent.name == "projects" and len(path.relative_to(parent).parts) >= 2:
                return parent.parent
        return None

    def classify(self, path: Path, root: Path) -> SessionFile | None:
        try:
            rel = path.relative_to(root)
        except ValueError:
            return None
        parts = rel.parts
        key = self.key_prefix(root) + rel.as_posix()

        def sf(session_key: str | None, role: str) -> SessionFile:
            return SessionFile(self.name, path, key, session_key, role)

        top = parts[0]
        if len(parts) == 1:
            if top == "history.jsonl":
                return sf(None, "history")
            return sf(None, "config" if top in CONFIG_FILES or top.endswith((".sh", ".py", ".js", ".ts")) else "unknown")
        if top == "projects":
            if len(parts) < 3:
                return sf(None, "unknown")
            slug, name = parts[1], parts[2]
            if len(parts) == 3:
                if m := MAIN_RE.match(name):
                    return sf(f"{slug}/{m['sid']}", "main")
                if m := SET_ASIDE_RE.match(name):
                    return sf(f"{slug}/{m['sid']}", "superseded")
                return sf(None, "unknown")
            if name == "memory":
                return sf(None, "memory")
            role = SESSION_SUBDIR_ROLES.get(parts[3], "aux") if len(parts) > 4 else "aux"
            return sf(f"{slug}/{name}", role)
        if top in PER_SESSION_DIRS:
            m = UUID_RE.match(parts[1])
            return sf(self._session_key(root, m.group(0)) if m else None, PER_SESSION_DIRS[top])
        return sf(None, TOP_LEVEL_ROLES.get(top, "unknown"))

    def _session_key(self, root: Path, sid: str) -> str | None:
        return _find_session(str(root), sid)

    def related(self, sf: SessionFile, root: Path) -> list[Path]:
        # A hook points at <session>.jsonl or at a subagent file inside <session>/: sync the lot,
        # plus the short-lived per-session files (file history, todos, tasks, team mailboxes)
        # that Claude Code deletes once the session or team is done.
        if not sf.session_key or "/" not in sf.session_key:
            return []
        slug, sid = sf.session_key.split("/", 1)
        project_dir = root / "projects" / slug
        main = project_dir / f"{sid}.jsonl"
        out = [main] if main.is_file() else []
        out.extend(files_under([project_dir / sid]))
        out.extend(p for p in project_dir.glob(f"{sid}.*") if p != main and p.is_file())
        out.extend(files_under([root / "file-history" / sid, root / "session-env" / sid, root / "tasks", root / "teams"]))
        out.extend(p for p in (root / "todos").glob(f"{sid}*") if p.is_file())
        debug = root / "debug" / f"{sid}.txt"
        if debug.is_file():
            out.append(debug)
        return out

    def exports(self, root: Path, work_dir: Path, db_interval: float) -> list[SessionFile]:
        out = super().exports(root, work_dir, db_interval)
        source = root / ".claude.json" if root != self.canonical_root() else self.home / ".claude.json"
        if not source.is_file():
            return out
        try:
            data = json.loads(source.read_bytes())
        except (OSError, ValueError):
            return out
        if isinstance(data, dict):
            data.pop("primaryApiKey", None)
        dest = work_dir / self.name / root_tag(root) / "claude.json"
        body = json.dumps(data, indent=2, sort_keys=True).encode() + b"\n"
        dest.parent.mkdir(parents=True, exist_ok=True)
        # Claude Code rewrites this file on every start (counters, tips, per-project state), so
        # like a database it is refreshed at most every db_interval to keep versions few.
        if not dest.exists() or (dest.read_bytes() != body and time.time() - dest.stat().st_mtime >= db_interval):
            dest.write_bytes(body)
        out.append(SessionFile(self.name, dest, self.key_prefix(root) + "claude.json", None, "config"))
        return out

    def extra_snapshot_entries(self, root: Path, project_root: Path | None) -> list[tuple[str, str, bytes]]:
        # User- and project-scoped MCP servers live in ~/.claude.json next to UI state that
        # changes every run, so the harness set records only the MCP parts.
        source = root / ".claude.json" if root != self.canonical_root() else self.home / ".claude.json"
        try:
            data = json.loads(source.read_text())
        except (OSError, ValueError):
            return []
        if not isinstance(data, dict):
            return []
        picked: dict = {"mcpServers": data.get("mcpServers") or {}}
        projects = data.get("projects") if isinstance(data.get("projects"), dict) else {}
        if project_root is not None and isinstance(projects.get(str(project_root)), dict):
            proj = projects[str(project_root)]
            picked["project"] = {
                k: proj[k] for k in ("mcpServers", "enabledMcpjsonServers", "disabledMcpjsonServers", "allowedTools") if k in proj
            }
        return [("user", ".claude.json#mcp", json.dumps(picked, indent=1, sort_keys=True).encode())]

    def memory_dirs(self, root: Path, project_root: Path | None, session_key: str | None) -> list[tuple[str, Path]]:
        out = [("user", root / "agent-memory")]
        if session_key and "/" in session_key:
            out.append(("auto", root / "projects" / session_key.split("/", 1)[0] / "memory"))
        if project_root is not None:
            out.append(("project", project_root / ".claude" / "agent-memory"))
        return out

    def plugin_dirs(self, root: Path) -> list[tuple[str, Path]]:
        try:
            data = json.loads((root / "plugins" / "installed_plugins.json").read_text())
        except (OSError, ValueError):
            return []
        plugins = data.get("plugins") if isinstance(data, dict) else None
        out = []
        for name, installs in (plugins or {}).items():
            for inst in installs if isinstance(installs, list) else [installs]:
                path = inst.get("installPath") if isinstance(inst, dict) else None
                if isinstance(path, str) and Path(path).is_dir():
                    out.append((name, Path(path)))
        return out

    def session_info(self, head_lines: list[dict]) -> dict:
        info: dict = {}
        fields = (("sessionId", "session_id"), ("agentId", "agent_id"), ("cwd", "cwd"), ("gitBranch", "git_branch"),
                  ("version", "agent_version"), ("entrypoint", "entrypoint"))
        for d in head_lines:
            for src, dst in fields:
                if dst not in info and d.get(src):
                    info[dst] = d[src]
        return info


_FOUND: dict[tuple[str, str], str] = {}


def _find_session(root: str, sid: str) -> str | None:
    """`<slug>/<sid>` for a session id, from where its transcript lives (hits are cached)."""
    if (root, sid) not in _FOUND:
        for p in Path(root, "projects").glob(f"*/{sid}.jsonl"):
            _FOUND[(root, sid)] = f"{p.parent.name}/{sid}"
            break
    return _FOUND.get((root, sid))
