"""Harness snapshots: which instructions, skills, settings and scripts an agent had for a project.

A snapshot is a content-addressed set of (scope, path, sha256) entries. File bodies are stored
once under snapshots/blobs/<sha256>, unredacted, and sets under snapshots/sets/<set-id>.json, so
identical files across sessions and machines cost nothing extra. Two kinds of set are taken:

  harness  what shapes the agent's behaviour
  memory   memory the agent writes for itself (Claude auto memory and agent-memory, Codex
           memories/), kept apart because it changes during sessions on its own

Scopes:
  user      the agent's user config dir (~/.claude, ~/.codex, ~/.pi/agent)
  home      files under $HOME that agents also read (~/.agents/skills)
  project   the git repo containing the session's cwd (or just the cwd outside a repo)
  ancestor  instruction files in the directories above the project
  managed   administrator settings (/etc/claude-code, /etc/codex, ...)
  plugin    every file of each installed Claude Code plugin, as <plugin>/<path>
  script    hook and status-line scripts named in settings, by real path
  import    files pulled into CLAUDE.md/AGENTS.md with @path, by real path

Directories in a pattern ending in /** are taken whole (a skill's scripts and references, not
just its SKILL.md), following symlinks; a symlinked file records its target. Files left out for
size or count are listed in the set's `skipped`. Each file's mtime is returned separately so it
doesn't change the set id.
"""

from __future__ import annotations

import fnmatch
import hashlib
import json
import os
import re
import shlex
from dataclasses import dataclass, field
from pathlib import Path

from .adapters.base import Adapter, is_credential, is_executable_binary

MAX_FILE_BYTES = 5 * 1024 * 1024
MAX_FILES_PER_SCOPE = 3000
MAX_WALK_DIRS = 5000
SKIP_DIRS = {".git", "node_modules", ".venv", "venv", "__pycache__", "dist", "build", ".next", "target", ".cache"}
ANCESTOR_NAMES = ("CLAUDE.md", "CLAUDE.local.md", "AGENTS.md", "AGENTS.override.md")
SETTINGS_NAMES = ("settings.json", "settings.local.json", "hooks.json", "config.toml", "managed-settings.json")
IMPORT_RE = re.compile(r"(?:^|\s)@((?:~/|\.{1,2}/|/)?[\w.~-][\w./~-]*)")
MAX_IMPORT_DEPTH = 5


@dataclass
class Entry:
    scope: str
    path: str
    body: bytes
    mtime: float | None = None
    target: str | None = None
    source: Path | None = None  # where the bytes came from, for scripts and imports


@dataclass
class Collected:
    entries: list[Entry] = field(default_factory=list)
    skipped: list[dict] = field(default_factory=list)
    counts: dict[str, int] = field(default_factory=dict)
    seen: set[tuple[str, str]] = field(default_factory=set)

    def add(self, scope: str, path: Path, name: str) -> Entry | None:
        """Read one file into the set, or note why it was left out."""
        real = os.path.realpath(path)
        if (scope, name) in self.seen or is_credential(path.name):
            return None
        try:
            st = path.stat()
        except OSError:
            return None
        if self.counts.get(scope, 0) >= MAX_FILES_PER_SCOPE:
            self.skipped.append({"scope": scope, "path": name, "reason": "file_limit", "size": st.st_size})
            return None
        if st.st_size > MAX_FILE_BYTES:
            self.skipped.append({"scope": scope, "path": name, "reason": "too_large", "size": st.st_size})
            return None
        try:
            body = path.read_bytes()
        except OSError:
            return None
        self.seen.add((scope, name))
        self.counts[scope] = self.counts.get(scope, 0) + 1
        entry = Entry(scope, name, body, st.st_mtime, real if real != os.path.abspath(path) else None, Path(real))
        self.entries.append(entry)
        return entry


def find_project_root(cwd: Path) -> tuple[Path, bool]:
    """Return (root, is_git_repo)."""
    for d in (cwd, *cwd.parents):
        if (d / ".git").exists():
            return d, True
    return cwd, False


def _walk_files(top: Path, follow: bool) -> list[Path]:
    """Every file under top, pruning SKIP_DIRS, following directory symlinks when asked."""
    out: list[Path] = []
    visited: set[str] = set()
    for n, (dirpath, dirnames, filenames) in enumerate(os.walk(top, followlinks=follow)):
        if n >= MAX_WALK_DIRS:
            break
        real = os.path.realpath(dirpath)
        if real in visited:  # a symlink loop
            dirnames[:] = []
            continue
        visited.add(real)
        dirnames[:] = sorted(d for d in dirnames if d not in SKIP_DIRS)
        out.extend(Path(dirpath) / f for f in sorted(filenames))
    return out


def _match(base: Path, pattern: str, recursive_ok: bool) -> list[Path]:
    if pattern.endswith("/**"):
        top = base / pattern[:-3]
        return [p for p in _walk_files(top, follow=True) if p.is_file()] if top.is_dir() else []
    if pattern.startswith("**/") and "/" not in pattern[3:]:
        if not recursive_ok:
            return []
        name = pattern[3:]
        return [p for p in _walk_files(base, follow=False) if fnmatch.fnmatch(p.name, name) and p.is_file()]
    return [p for p in sorted(base.glob(pattern)) if p.is_file() and not any(part in SKIP_DIRS for part in p.relative_to(base).parts)]


def _commands(obj, in_hooks: bool = False):
    """Command strings in a settings tree: hooks, statusLine, Codex notify."""
    if isinstance(obj, dict):
        for k, v in obj.items():
            if k in ("command", "notify") and (in_hooks or k == "notify"):
                if isinstance(v, str):
                    yield v
                elif isinstance(v, list) and all(isinstance(x, str) for x in v):
                    yield shlex.join(v)
            else:
                yield from _commands(v, in_hooks or k in ("hooks", "statusLine"))
    elif isinstance(obj, list):
        for v in obj:
            yield from _commands(v, in_hooks)


def _script_paths(command: str, home: Path, project_root: Path | None) -> list[Path]:
    expanded = command
    if project_root is not None:
        expanded = re.sub(r"\$\{?CLAUDE_PROJECT_DIR\}?", str(project_root), expanded)
    expanded = re.sub(r"\$\{?HOME\}?", str(home), expanded)
    try:
        tokens = shlex.split(expanded)
    except ValueError:
        tokens = expanded.split()
    out = []
    for tok in tokens:
        if "/" not in tok or tok.startswith(("-", "http://", "https://")):
            continue
        p = Path(tok.replace("~", str(home), 1) if tok.startswith("~") else tok)
        if not p.is_absolute():
            if project_root is None:
                continue
            p = project_root / p
        if p.is_file() and not is_executable_binary(p):
            out.append(p)
    return out


def _parse_settings(entry: Entry):
    try:
        if entry.path.endswith(".toml"):
            import tomllib

            return tomllib.loads(entry.body.decode())
        return json.loads(entry.body)
    except Exception:
        return None


def _imports(entry: Entry, home: Path) -> list[Path]:
    if entry.source is None or not entry.path.endswith(".md"):
        return []
    try:
        text = entry.body.decode()
    except UnicodeDecodeError:
        return []
    out, fenced = [], False
    for line in text.splitlines():
        if line.lstrip().startswith("```"):
            fenced = not fenced
            continue
        if fenced:
            continue
        for m in IMPORT_RE.finditer(line):
            ref = m.group(1).rstrip(".,;:")
            p = Path(ref.replace("~", str(home), 1)) if ref.startswith("~") else Path(ref)
            if not p.is_absolute():
                p = entry.source.parent / p
            if p.is_file():
                out.append(p)
    return out


def collect(adapter: Adapter, root: Path, cwd: str | None) -> Collected:
    """Every harness file for a session in this cwd."""
    c = Collected()
    project_root: Path | None = None
    is_repo = False
    if cwd and Path(cwd).is_dir():
        project_root, is_repo = find_project_root(Path(cwd))
    home = adapter.home.resolve() if adapter.home.exists() else adapter.home

    rules = adapter.snapshot_rules
    scopes: list[tuple[str, Path, tuple[str, ...], bool]] = [
        ("user", adapter.user_base(root), rules.user, True),
        ("home", adapter.home, rules.home, False),
    ]
    if project_root is not None:
        # Outside a git repo (say, a session started in ~), only look at the cwd itself.
        recursive_ok = is_repo and project_root.resolve() not in (home, Path("/"))
        scopes.append(("project", project_root, rules.project, recursive_ok))
    for scope, base, patterns, recursive_ok in scopes:
        if not base.is_dir():
            continue
        for pattern in patterns:
            for p in _match(base, pattern, recursive_ok):
                c.add(scope, p, p.relative_to(base).as_posix())

    if project_root is not None:
        for d in project_root.parents:
            for name in ANCESTOR_NAMES:
                if (d / name).is_file():
                    c.add("ancestor", d / name, str(d / name))
    for path in adapter.managed_paths:
        if Path(path).is_file():
            c.add("managed", Path(path), path)
    for name, top in adapter.plugin_dirs(root):
        for p in _walk_files(top, follow=True):
            if p.is_file():
                c.add("plugin", p, f"{name}/{p.relative_to(top).as_posix()}")

    # Hook and status-line scripts named in any settings file collected above.
    for entry in list(c.entries):
        if Path(entry.path).name in SETTINGS_NAMES:
            for command in _commands(_parse_settings(entry)):
                for p in _script_paths(command, home, project_root):
                    c.add("script", p, os.path.realpath(p))
    # @imports, followed a few levels deep.
    frontier = [e for e in c.entries if e.scope in ("user", "project", "ancestor", "managed")]
    for _ in range(MAX_IMPORT_DEPTH):
        added = []
        for entry in frontier:
            for p in _imports(entry, home):
                e = c.add("import", p, os.path.realpath(p))
                if e:
                    added.append(e)
        frontier = added

    for scope, name, body in adapter.extra_snapshot_entries(root, project_root):
        c.entries.append(Entry(scope, name, body))
    c.entries.sort(key=lambda e: (e.scope, e.path))
    return c


def collect_memory(adapter: Adapter, root: Path, cwd: str | None, session_key: str | None) -> Collected:
    c = Collected()
    project_root = find_project_root(Path(cwd))[0] if cwd and Path(cwd).is_dir() else None
    for scope, top in adapter.memory_dirs(root, project_root, session_key):
        if top.is_dir():
            for p in _walk_files(top, follow=True):
                if p.is_file():
                    c.add(scope, p, str(p))
    c.entries.sort(key=lambda e: (e.scope, e.path))
    return c


@dataclass
class Snapshot:
    set_id: str
    doc: dict
    memory_id: str | None
    memory_doc: dict | None
    blobs: dict[str, bytes]
    mtimes: dict[str, float]


def _set_doc(kind: str, adapter: Adapter, root: Path, project_root: Path | None, c: Collected,
             blobs: dict[str, bytes], mtimes: dict[str, float]) -> tuple[str, dict]:
    files = []
    for e in c.entries:
        sha = hashlib.sha256(e.body).hexdigest()
        blobs[sha] = e.body
        f = {"scope": e.scope, "path": e.path, "sha256": sha, "size": len(e.body)}
        if e.target:
            f["target"] = e.target
        files.append(f)
        if e.mtime is not None:
            mtimes[f"{kind}:{e.scope}:{e.path}"] = e.mtime
    doc = {
        "kind": kind,
        "agent": adapter.name,
        "user_base": str(adapter.user_base(root)),
        "project_root": str(project_root) if project_root else None,
        "files": files,
        "skipped": c.skipped,
    }
    return hashlib.sha256(json.dumps(doc, sort_keys=True).encode()).hexdigest(), doc


def build(adapter: Adapter, root: Path, cwd: str | None, session_key: str | None = None) -> Snapshot:
    blobs: dict[str, bytes] = {}
    mtimes: dict[str, float] = {}
    project_root = find_project_root(Path(cwd))[0] if cwd and Path(cwd).is_dir() else None
    set_id, doc = _set_doc("harness", adapter, root, project_root, collect(adapter, root, cwd), blobs, mtimes)
    memory = collect_memory(adapter, root, cwd, session_key)
    memory_id = memory_doc = None
    if memory.entries or memory.skipped:
        memory_id, memory_doc = _set_doc("memory", adapter, root, project_root, memory, blobs, mtimes)
    return Snapshot(set_id, doc, memory_id, memory_doc, blobs, mtimes)
