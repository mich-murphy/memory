from __future__ import annotations

import hashlib
import json
import os
import sqlite3
import time
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
    # main | subagent | tool_result | superseded | memory | history | plan | file_history | task |
    # todo | team | session_env | debug | paste | usage | config | log | db | unknown ...
    role: str
    compression: str | None = None  # "zst" for Codex's recompressed rollouts


@dataclass(frozen=True)
class SnapshotRules:
    """Harness files worth recording, as globs relative to a base directory.

    A pattern ending in ``/**`` takes every file under that directory (whole skill directories
    with their scripts and references, not just SKILL.md). ``**/NAME`` matches NAME at any depth.
    """

    user: tuple[str, ...]
    project: tuple[str, ...]
    home: tuple[str, ...] = ()  # relative to $HOME, e.g. ~/.agents/skills


# Never copied, from any agent: credentials. Contract v2, "Skipped".
CREDENTIAL_NAMES = frozenset({".credentials.json", "auth.json", "oauth.json", "mcp-auth.json"})
CREDENTIAL_SUFFIXES = (".key", ".pem")
# Never copied either: churn and things that aren't the agent's own data.
SKIP_DIR_NAMES = frozenset({
    "node_modules", ".git", "__pycache__", ".venv", "venv", "cache", ".cache", "tmp", ".tmp", "temp",
    "shell-snapshots", "shell_snapshots",
})
SKIP_SUFFIXES = (".lock", ".tmp", ".pid", ".sock", ".swp", "-wal", "-shm", "-journal")
SQLITE_SUFFIXES = (".sqlite", ".sqlite3", ".db")
BINARY_MAGIC = (b"\x7fELF", b"\xcf\xfa\xed\xfe", b"\xce\xfa\xed\xfe", b"\xca\xfe\xba\xbe", b"MZ")


def is_credential(name: str) -> bool:
    return name in CREDENTIAL_NAMES or name.endswith(CREDENTIAL_SUFFIXES) or name.startswith(".claude.json")


def is_sqlite(path: Path) -> bool:
    return path.name.endswith(SQLITE_SUFFIXES)


def is_executable_binary(path: Path) -> bool:
    try:
        st = path.stat()
        if not st.st_mode & 0o111:
            return False
        with path.open("rb") as fh:
            return fh.read(4).startswith(BINARY_MAGIC)
    except OSError:
        return False


class Adapter:
    """One coding agent's on-disk layout.

    A *root* is a directory an agent writes under (``~/.claude``, ``$CODEX_HOME``, ``~/.pi/agent``,
    or a Pi session dir). Agents can be pointed elsewhere with environment variables or settings
    that a cron job never sees, so a hook that reports a path outside the known roots makes
    agent-sessions remember that root and sweep it from then on.

    Capture is a denylist walk: every file under a root is copied unless it is a credential, a
    lock, cache, temp file or binary, or on the adapter's own skip list. Files the adapter doesn't
    recognise get role ``unknown`` rather than being dropped.
    """

    name: str
    snapshot_rules: SnapshotRules
    # Directories (relative to a root) never copied.
    skip_dirs: frozenset[str] = frozenset()
    # Settings files outside the user's dirs that an administrator controls.
    managed_paths: tuple[str, ...] = ()

    def __init__(self, home: Path):
        self.home = home

    def default_roots(self) -> list[Path]:
        raise NotImplementedError

    def canonical_root(self) -> Path:
        """The root keyed without a prefix. Fixed from $HOME, not the environment, so a hook
        (which sees CLAUDE_CONFIG_DIR and friends) and a sweep (which doesn't) agree on keys."""
        raise NotImplementedError

    def key_prefix(self, root: Path) -> str:
        return "" if root == self.canonical_root() else f"roots/{root_tag(root)}/"

    def infer_root(self, path: Path) -> Path | None:
        """Work out the root from the shape of a path a hook reported."""
        raise NotImplementedError

    def walk(self, root: Path) -> Iterator[Path]:
        if not root.is_dir():
            return
        for dirpath, dirnames, filenames in os.walk(root):
            base = Path(dirpath)
            rel = base.relative_to(root)
            dirnames[:] = sorted(
                d for d in dirnames
                if d not in SKIP_DIR_NAMES and (rel / d).as_posix() not in self.skip_dirs
            )
            for name in sorted(filenames):
                if is_credential(name) or name.endswith(SKIP_SUFFIXES):
                    continue
                p = base / name
                if p.is_symlink() or not p.is_file():
                    continue
                yield p

    def discover(self, root: Path) -> Iterator[SessionFile]:
        for p in self.walk(root):
            if is_sqlite(p) or is_executable_binary(p):
                continue
            sf = self.classify(p, root)
            if sf:
                yield sf

    def classify(self, path: Path, root: Path) -> SessionFile | None:
        """Map a local path to a SessionFile, or None if it isn't captured."""
        raise NotImplementedError

    def related(self, sf: SessionFile, root: Path) -> list[Path]:
        """Other files to sync alongside a file a hook pointed at."""
        return []

    def user_base(self, root: Path) -> Path:
        """Directory the user-level snapshot globs are relative to."""
        return root

    def extra_snapshot_entries(self, root: Path, project_root: Path | None) -> list[tuple[str, str, bytes]]:
        """Harness state that doesn't live in a plain file of its own: (scope, name, bytes)."""
        return []

    def memory_dirs(self, root: Path, project_root: Path | None, session_key: str | None) -> list[tuple[str, Path]]:
        """Directories of memory the agent writes for itself, as (scope, dir)."""
        return []

    def plugin_dirs(self, root: Path) -> list[tuple[str, Path]]:
        """Installed plugins, as (name, install dir)."""
        return []

    def exports(self, root: Path, work_dir: Path, db_interval: float) -> list[SessionFile]:
        """Data that can't be copied as-is (live SQLite databases, files holding a credential
        field), materialised under work_dir and synced like any other file. Run by sweeps."""
        return self.sqlite_exports(root, work_dir, db_interval)

    def sqlite_exports(self, root: Path, work_dir: Path, db_interval: float) -> list[SessionFile]:
        """A consistent copy of each SQLite database under the root, via SQLite's backup API.

        Copying the file itself would miss whatever still sits in the -wal file and can catch
        the database mid-write. A copy is refreshed when the database changed and the last copy
        is older than db_interval seconds, so a busy database isn't re-uploaded every sweep.
        """
        out = []
        for db in self.walk(root):
            if not is_sqlite(db):
                continue
            rel = db.relative_to(root).as_posix()
            dest = work_dir / self.name / root_tag(root) / rel
            sig_path = dest.with_name(dest.name + ".sig")
            sig = _db_signature(db)
            try:
                old_sig = sig_path.read_text()
            except OSError:
                old_sig = None
            due = not dest.exists() or (old_sig != sig and time.time() - dest.stat().st_mtime >= db_interval)
            if due:
                try:
                    backup_sqlite(db, dest)
                    sig_path.write_text(sig)
                except (sqlite3.Error, OSError):
                    continue  # locked or not really a database; try again next sweep
            if dest.exists():
                out.append(SessionFile(self.name, dest, f"{self.key_prefix(root)}db/{rel}", None, "db"))
        return out

    def session_info(self, head_lines: list[dict]) -> dict:
        """Extract session_id / cwd / version from the first parsed lines of a transcript."""
        return {}


def _db_signature(db: Path) -> str:
    parts = []
    for p in (db, db.with_name(db.name + "-wal")):
        try:
            st = p.stat()
            parts.append(f"{st.st_size}:{st.st_mtime_ns}")
        except OSError:
            parts.append("-")
    return " ".join(parts)


def backup_sqlite(src: Path, dest: Path) -> None:
    dest.parent.mkdir(parents=True, exist_ok=True)
    tmp = dest.with_name(dest.name + ".partial")
    tmp.unlink(missing_ok=True)
    source = sqlite3.connect(f"file:{src}?mode=ro", uri=True, timeout=10)
    try:
        target = sqlite3.connect(tmp)
        try:
            source.backup(target)
        finally:
            target.close()
    finally:
        source.close()
    tmp.replace(dest)


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
            if p.is_file() and not p.is_symlink() and not is_credential(p.name) and not p.name.endswith(SKIP_SUFFIXES):
                yield p


def is_within(path: Path, root: Path) -> bool:
    try:
        path.relative_to(root)
        return True
    except ValueError:
        return False
