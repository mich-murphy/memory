"""Local sync state (SQLite) plus a process lock shared by hooks and the sweep."""

from __future__ import annotations

import fcntl
import json
import sqlite3
import time
from contextlib import contextmanager
from pathlib import Path

SCHEMA = """
CREATE TABLE IF NOT EXISTS files (
    path TEXT PRIMARY KEY,
    agent TEXT NOT NULL,
    root TEXT NOT NULL,
    key TEXT NOT NULL,
    session_key TEXT,
    role TEXT NOT NULL,
    size INTEGER NOT NULL,
    mtime_ns INTEGER NOT NULL,
    sha256 TEXT NOT NULL,
    line_count INTEGER,
    prefix_sha256 TEXT,
    head TEXT,
    deleted_at REAL
);
CREATE INDEX IF NOT EXISTS files_session ON files(agent, session_key);
CREATE INDEX IF NOT EXISTS files_key ON files(key);
-- What the bucket holds under each key: sha256 and length of the uncompressed bytes.
CREATE TABLE IF NOT EXISTS uploads (
    key TEXT PRIMARY KEY,
    sha256 TEXT NOT NULL,
    size INTEGER,
    uploaded_at REAL NOT NULL
);
-- Earlier contents of a raw key, copied aside before a non-append rewrite replaced them.
CREATE TABLE IF NOT EXISTS versions (
    version_key TEXT PRIMARY KEY,
    key TEXT NOT NULL,
    sha256 TEXT NOT NULL,
    size INTEGER,
    versioned_at REAL NOT NULL
);
CREATE INDEX IF NOT EXISTS versions_key ON versions(key);
CREATE TABLE IF NOT EXISTS sessions (
    agent TEXT NOT NULL,
    session_key TEXT NOT NULL,
    info TEXT NOT NULL,
    snapshots TEXT NOT NULL,
    launches TEXT NOT NULL DEFAULT '[]',
    first_seen REAL NOT NULL,
    PRIMARY KEY (agent, session_key)
);
-- Session directories learned from hooks (CLAUDE_CONFIG_DIR, CODEX_HOME, Pi's sessionDir...)
-- so the sweep covers them even though cron never sees those settings.
CREATE TABLE IF NOT EXISTS roots (
    agent TEXT NOT NULL,
    path TEXT NOT NULL,
    added_at REAL NOT NULL,
    PRIMARY KEY (agent, path)
);
CREATE TABLE IF NOT EXISTS git_exports (
    repo_id TEXT NOT NULL,
    head TEXT NOT NULL,
    exported_at REAL NOT NULL,
    PRIMARY KEY (repo_id, head)
);
CREATE TABLE IF NOT EXISTS runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    started_at REAL NOT NULL,
    finished_at REAL NOT NULL,
    trigger TEXT NOT NULL,
    summary TEXT NOT NULL
);
"""


class State:
    def __init__(self, state_dir: Path):
        state_dir.mkdir(parents=True, exist_ok=True)
        self.db = sqlite3.connect(state_dir / "state.sqlite3", timeout=30)
        self.db.execute("PRAGMA journal_mode=WAL")
        self.db.executescript(SCHEMA)

    def close(self) -> None:
        self.db.commit()
        self.db.close()

    # files ------------------------------------------------------------
    def file_unchanged(self, path: Path, size: int, mtime_ns: int) -> bool:
        row = self.db.execute("SELECT size, mtime_ns, deleted_at FROM files WHERE path = ?", (str(path),)).fetchone()
        return row is not None and row[0] == size and row[1] == mtime_ns and row[2] is None

    def record_file(self, path: Path, agent: str, root: Path, key: str, session_key: str | None, role: str,
                    size: int, mtime_ns: int, sha256: str, line_count: int | None, prefix_sha256: str | None,
                    head: dict | None) -> None:
        if head is None:
            row = self.db.execute("SELECT head FROM files WHERE path = ?", (str(path),)).fetchone()
            head = json.loads(row[0]) if row and row[0] else None
        self.db.execute(
            "INSERT OR REPLACE INTO files VALUES (?,?,?,?,?,?,?,?,?,?,?,?,NULL)",
            (str(path), agent, str(root), key, session_key, role, size, mtime_ns, sha256, line_count, prefix_sha256,
             json.dumps(head, sort_keys=True) if head is not None else None),
        )

    def session_files(self, agent: str, session_key: str) -> list[dict]:
        rows = self.db.execute(
            "SELECT key, role, size, sha256, line_count, prefix_sha256, head, deleted_at, mtime_ns FROM files "
            "WHERE agent = ? AND session_key = ? ORDER BY key, deleted_at IS NULL",
            (agent, session_key),
        ).fetchall()
        # Codex can hold the same key under two paths (.jsonl then .jsonl.zst); keep one row per
        # key, preferring the one still on disk (sorted last).
        seen: dict[str, dict] = {}
        for key, role, size, sha, lines, prefix, head, deleted_at, mtime_ns in rows:
            seen[key] = {"key": key, "role": role, "size": size, "sha256": sha, "line_count": lines,
                         "prefix_sha256": prefix, "head": json.loads(head) if head else None, "deleted_at": deleted_at,
                         "mtime_ns": mtime_ns}
        return list(seen.values())

    def live_files(self, agent: str, root: Path) -> list[tuple[str, str | None]]:
        return self.db.execute(
            "SELECT path, session_key FROM files WHERE agent = ? AND root = ? AND deleted_at IS NULL", (agent, str(root))
        ).fetchall()

    def mark_deleted(self, path: str, at: float) -> None:
        self.db.execute("UPDATE files SET deleted_at = ? WHERE path = ?", (at, path))

    # uploads ----------------------------------------------------------
    def uploaded(self, key: str) -> tuple[str, int | None] | None:
        row = self.db.execute("SELECT sha256, size FROM uploads WHERE key = ?", (key,)).fetchone()
        return (row[0], row[1]) if row else None

    def uploaded_sha(self, key: str) -> str | None:
        row = self.uploaded(key)
        return row[0] if row else None

    def record_upload(self, key: str, sha256: str, size: int | None = None) -> None:
        self.db.execute("INSERT OR REPLACE INTO uploads VALUES (?,?,?,?)", (key, sha256, size, time.time()))

    def record_version(self, key: str, version_key: str, sha256: str, size: int | None) -> None:
        self.db.execute("INSERT OR REPLACE INTO versions VALUES (?,?,?,?,?)", (version_key, key, sha256, size, time.time()))

    def versions(self, key: str) -> list[dict]:
        rows = self.db.execute(
            "SELECT version_key, sha256, size, versioned_at FROM versions WHERE key = ? ORDER BY versioned_at", (key,)
        ).fetchall()
        return [{"object": r[0], "sha256": r[1], "size": r[2], "versioned_at": r[3]} for r in rows]

    # sessions ---------------------------------------------------------
    def get_session(self, agent: str, session_key: str) -> dict | None:
        row = self.db.execute(
            "SELECT info, snapshots, launches, first_seen FROM sessions WHERE agent = ? AND session_key = ?",
            (agent, session_key),
        ).fetchone()
        if not row:
            return None
        return {"info": json.loads(row[0]), "snapshots": json.loads(row[1]), "launches": json.loads(row[2]),
                "first_seen": row[3]}

    def put_session(self, agent: str, session_key: str, info: dict, snapshots: list, first_seen: float,
                    launches: list | None = None) -> None:
        if launches is None:
            prev = self.get_session(agent, session_key)
            launches = prev["launches"] if prev else []
        self.db.execute(
            "INSERT OR REPLACE INTO sessions VALUES (?,?,?,?,?,?)",
            (agent, session_key, json.dumps(info, sort_keys=True), json.dumps(snapshots), json.dumps(launches), first_seen),
        )

    # git --------------------------------------------------------------
    def git_exported(self, repo_id: str, head: str) -> bool:
        return self.db.execute("SELECT 1 FROM git_exports WHERE repo_id = ? AND head = ?", (repo_id, head)).fetchone() is not None

    def git_heads(self, repo_id: str) -> list[str]:
        return [r[0] for r in self.db.execute("SELECT head FROM git_exports WHERE repo_id = ? ORDER BY exported_at", (repo_id,))]

    def record_git_export(self, repo_id: str, head: str) -> None:
        self.db.execute("INSERT OR IGNORE INTO git_exports VALUES (?,?,?)", (repo_id, head, time.time()))

    # roots ------------------------------------------------------------
    def roots(self, agent: str) -> list[Path]:
        return [Path(r[0]) for r in self.db.execute("SELECT path FROM roots WHERE agent = ? ORDER BY path", (agent,))]

    def add_root(self, agent: str, path: Path) -> None:
        self.db.execute("INSERT OR IGNORE INTO roots VALUES (?,?,?)", (agent, str(path), time.time()))

    # runs -------------------------------------------------------------
    def record_run(self, started_at: float, trigger: str, summary: dict) -> None:
        self.db.execute(
            "INSERT INTO runs (started_at, finished_at, trigger, summary) VALUES (?,?,?,?)",
            (started_at, time.time(), trigger, json.dumps(summary)),
        )
        self.db.execute("DELETE FROM runs WHERE id <= (SELECT MAX(id) - 500 FROM runs)")
        self.db.commit()

    def last_runs(self, limit: int = 5) -> list[dict]:
        rows = self.db.execute(
            "SELECT started_at, finished_at, trigger, summary FROM runs ORDER BY id DESC LIMIT ?", (limit,)
        ).fetchall()
        return [{"started_at": r[0], "finished_at": r[1], "trigger": r[2], **json.loads(r[3])} for r in rows]

    def counts(self) -> dict:
        out = {}
        for agent, role, n, total in self.db.execute(
            "SELECT agent, role, COUNT(*), SUM(size) FROM files WHERE deleted_at IS NULL GROUP BY agent, role ORDER BY agent, role"
        ):
            out[f"{agent}/{role}"] = {"files": n, "bytes": total or 0}
        out["uploaded_objects"] = self.db.execute("SELECT COUNT(*) FROM uploads").fetchone()[0]
        out["earlier_versions"] = self.db.execute("SELECT COUNT(*) FROM versions").fetchone()[0]
        out["deleted_locally"] = self.db.execute("SELECT COUNT(*) FROM files WHERE deleted_at IS NOT NULL").fetchone()[0]
        return out


@contextmanager
def sync_lock(state_dir: Path, timeout: float = 300.0):
    """Serialize hook-triggered and sweep runs so the same file isn't uploaded twice at once."""
    state_dir.mkdir(parents=True, exist_ok=True)
    with open(state_dir / "sync.lock", "w") as fh:
        deadline = time.monotonic() + timeout
        while True:
            try:
                fcntl.flock(fh, fcntl.LOCK_EX | fcntl.LOCK_NB)
                break
            except BlockingIOError:
                if time.monotonic() > deadline:
                    raise TimeoutError("another agent-sessions run is holding the lock")
                time.sleep(0.25)
        try:
            yield
        finally:
            fcntl.flock(fh, fcntl.LOCK_UN)
