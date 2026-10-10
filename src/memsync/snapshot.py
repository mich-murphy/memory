"""Harness snapshots: which instructions, skills and settings an agent had for a project.

A snapshot is a content-addressed set of (scope, path, sha256) entries. File bodies are
stored once under snapshots/blobs/<sha256>, sets under snapshots/sets/<set-id>.json, so
identical CLAUDE.md files across sessions and machines cost nothing extra.

Scopes:
  user      the agent's user config dir (~/.claude, ~/.codex, ~/.pi/agent)
  project   the git repo containing the session's cwd (or just the cwd outside a repo)
  ancestor  instruction files in the directories above the project, which all three
            agents also load

Snapshots are taken when memsync syncs a session, which for hook-driven syncs is during
the session. For backfilled old sessions it reflects the harness at backfill time, and the
manifest records that (`taken_at`, `during_backfill`).
"""

from __future__ import annotations

import fnmatch
import hashlib
import json
import os
import subprocess
from pathlib import Path

from .adapters.base import Adapter
from .redact import redact_file, scrub_text

MAX_FILE_BYTES = 512 * 1024
MAX_FILES_PER_SCOPE = 300
MAX_WALK_DIRS = 5000
SKIP_DIRS = {".git", "node_modules", ".venv", "venv", "__pycache__", "dist", "build", ".next", "target", ".cache"}
ANCESTOR_NAMES = ("CLAUDE.md", "CLAUDE.local.md", "AGENTS.md", "AGENTS.override.md")


def find_project_root(cwd: Path) -> tuple[Path, bool]:
    """Return (root, is_git_repo)."""
    for d in (cwd, *cwd.parents):
        if (d / ".git").exists():
            return d, True
    return cwd, False


def _glob(base: Path, pattern: str, recursive_ok: bool) -> list[Path]:
    if pattern.startswith("**/") and "/" not in pattern[3:]:
        if not recursive_ok:
            return []
        # Recursive name match: walk ourselves so node_modules and friends are pruned, not scanned.
        name = pattern[3:]
        candidates = []
        for n, (dirpath, dirnames, filenames) in enumerate(os.walk(base)):
            if n >= MAX_WALK_DIRS:
                break
            dirnames[:] = sorted(d for d in dirnames if d not in SKIP_DIRS)
            candidates.extend(Path(dirpath) / f for f in filenames if fnmatch.fnmatch(f, name))
    else:
        candidates = [p for p in base.glob(pattern) if not any(part in SKIP_DIRS for part in p.relative_to(base).parts)]
    out = []
    for p in candidates:
        try:
            if p.is_file() and not p.is_symlink() and p.stat().st_size <= MAX_FILE_BYTES:
                out.append(p)
        except OSError:
            continue
    return out


def _read_redacted(p: Path) -> bytes:
    try:
        return redact_file(p.name, p.read_bytes())
    except OSError:
        return b""


def collect(adapter: Adapter, root: Path, cwd: str | None) -> list[tuple[str, str, bytes]]:
    """Return (scope, relative path, redacted bytes) for every harness file found."""
    entries: list[tuple[str, str, bytes]] = []
    project_root: Path | None = None
    is_repo = False
    if cwd and Path(cwd).is_dir():
        project_root, is_repo = find_project_root(Path(cwd))
    home = adapter.home.resolve() if adapter.home.exists() else adapter.home

    scopes: list[tuple[str, Path, tuple[str, ...], bool]] = [("user", adapter.user_base(root), adapter.snapshot_rules.user, True)]
    if project_root is not None:
        # Outside a git repo (say, a session started in ~), only look at the cwd itself.
        recursive_ok = is_repo and project_root.resolve() not in (home, Path("/"))
        scopes.append(("project", project_root, adapter.snapshot_rules.project, recursive_ok))
    for scope, base, patterns, recursive_ok in scopes:
        if not base.is_dir():
            continue
        seen: set[Path] = set()
        for pattern in patterns:
            for p in _glob(base, pattern, recursive_ok):
                if p in seen or len(seen) >= MAX_FILES_PER_SCOPE:
                    continue
                seen.add(p)
                body = _read_redacted(p)
                if body:
                    entries.append((scope, p.relative_to(base).as_posix(), body))

    if project_root is not None:
        for d in project_root.parents:
            for name in ANCESTOR_NAMES:
                p = d / name
                if p.is_file() and not p.is_symlink() and p.stat().st_size <= MAX_FILE_BYTES:
                    body = _read_redacted(p)
                    if body:
                        entries.append(("ancestor", str(p), body))

    entries.extend(adapter.extra_snapshot_entries(root, project_root))
    return sorted(entries, key=lambda e: (e[0], e[1]))


def git_state(project_root: Path | None) -> dict:
    if project_root is None or not (project_root / ".git").exists():
        return {}

    def git(*args: str) -> str | None:
        try:
            r = subprocess.run(["git", "-C", str(project_root), *args], capture_output=True, text=True, timeout=5)
        except (OSError, subprocess.SubprocessError):
            return None
        return r.stdout.strip() or None if r.returncode == 0 else None

    state = {
        "remote": git("remote", "get-url", "origin"),
        "branch": git("rev-parse", "--abbrev-ref", "HEAD"),
        "commit": git("rev-parse", "HEAD"),
    }
    if state["remote"]:
        state["remote"] = scrub_text(state["remote"])
    return {k: v for k, v in state.items() if v}


def build(adapter: Adapter, root: Path, cwd: str | None) -> tuple[str, dict, dict[str, bytes]]:
    """Return (set_id, set_document, blobs_by_sha)."""
    blobs: dict[str, bytes] = {}
    files = []
    for scope, rel, body in collect(adapter, root, cwd):
        sha = hashlib.sha256(body).hexdigest()
        blobs[sha] = body
        files.append({"scope": scope, "path": rel, "sha256": sha, "size": len(body)})
    project_root = find_project_root(Path(cwd))[0] if cwd and Path(cwd).is_dir() else None
    doc = {
        "agent": adapter.name,
        "user_base": str(adapter.user_base(root)),
        "project_root": str(project_root) if project_root else None,
        "files": files,
    }
    set_id = hashlib.sha256(json.dumps(doc, sort_keys=True).encode()).hexdigest()
    return set_id, doc, blobs
