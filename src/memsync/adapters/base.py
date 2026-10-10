from __future__ import annotations

import hashlib
import json
from collections.abc import Iterator
from dataclasses import dataclass
from pathlib import Path


@dataclass(frozen=True)
class SessionFile:
    agent: str
    path: Path
    # Object key relative to raw/agent=<agent>/machine=<machine>/ (without .gz).
    key: str
    # Groups files that belong to one session; None for agent-wide files like history.
    session_key: str | None
    role: str  # main | subagent | tool_result | superseded | aux
    compression: str | None = None  # "zst" for Codex's recompressed rollouts


@dataclass(frozen=True)
class SnapshotRules:
    """Harness files worth recording, as globs relative to the user config dir and the project root."""

    user: tuple[str, ...]
    project: tuple[str, ...]


class Adapter:
    """One coding agent's on-disk layout.

    A *root* is the directory an agent writes sessions under (``~/.claude``, ``$CODEX_HOME``,
    Pi's sessions dir). Agents can be pointed elsewhere with environment variables or settings
    that a cron job never sees, so a hook that reports a path outside the default roots makes
    memsync remember that root and sweep it from then on.
    """

    name: str
    snapshot_rules: SnapshotRules

    def __init__(self, home: Path):
        self.home = home

    def default_roots(self) -> list[Path]:
        raise NotImplementedError

    def infer_root(self, path: Path) -> Path | None:
        """Work out the root from the shape of a path a hook reported."""
        raise NotImplementedError

    def discover(self, root: Path) -> Iterator[SessionFile]:
        for p in files_under(self.scan_dirs(root)):
            sf = self.classify(p, root)
            if sf:
                yield sf

    def scan_dirs(self, root: Path) -> list[Path]:
        return [root]

    def classify(self, path: Path, root: Path) -> SessionFile | None:
        """Map a local path to a SessionFile, or None if it isn't one of ours."""
        raise NotImplementedError

    def related(self, sf: SessionFile, root: Path) -> list[Path]:
        """Other files to sync alongside a file a hook pointed at."""
        return []

    def user_base(self, root: Path) -> Path:
        """Directory the user-level snapshot globs are relative to."""
        return root

    def extra_snapshot_entries(self, root: Path, project_root: Path | None) -> list[tuple[str, str, bytes]]:
        """Harness state that doesn't live in a plain file of its own (scope, name, redacted bytes)."""
        return []

    def exports(self, root: Path) -> list[tuple[str, bytes]]:
        """Session metadata kept outside the transcripts, as (key, bytes); refreshed by sweeps."""
        return []

    def session_info(self, head_lines: list[dict]) -> dict:
        """Extract session_id / cwd / version from the first parsed lines of a main file."""
        raise NotImplementedError


def root_tag(root: Path) -> str:
    """Short stable prefix for keys under a non-default root, so two roots can't collide."""
    return hashlib.sha256(str(root).encode()).hexdigest()[:10]


def read_head(data: bytes, max_lines: int = 50) -> list[dict]:
    out: list[dict] = []
    for raw in data.split(b"\n", max_lines)[:max_lines]:
        raw = raw.strip()
        if not raw:
            continue
        try:
            obj = json.loads(raw)
        except ValueError:
            continue
        if isinstance(obj, dict):
            out.append(obj)
    return out


def files_under(roots: list[Path]) -> Iterator[Path]:
    for root in roots:
        if not root.is_dir():
            continue
        for p in sorted(root.rglob("*")):
            if p.is_file() and not p.is_symlink() and not p.name.endswith((".tmp", ".lock")):
                yield p


def is_within(path: Path, root: Path) -> bool:
    try:
        path.relative_to(root)
        return True
    except ValueError:
        return False
