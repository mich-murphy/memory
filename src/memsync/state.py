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
    key TEXT NOT NULL,
    session_key TEXT,
    role TEXT NOT NULL,
    size INTEGER NOT NULL,
    mtime_ns INTEGER NOT NULL,
    sha256 TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS files_session ON files(agent, session_key);
CREATE TABLE IF NOT EXISTS uploads (
    key TEXT PRIMARY KEY,
    sha256 TEXT NOT NULL,
    uploaded_at REAL NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
    agent TEXT NOT NULL,
    session_key TEXT NOT NULL,
    info TEXT NOT NULL,
    snapshots TEXT NOT NULL,
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
        row = self.db.execute("SELECT size, mtime_ns FROM files WHERE path = ?", (str(path),)).fetchone()
        return row is not None and row[0] == size and row[1] == mtime_ns

    def record_file(self, path: Path, agent: str, key: str, session_key: str | None, role: str,
                    size: int, mtime_ns: int, sha256: str) -> None:
        self.db.execute(
            "INSERT OR REPLACE INTO files VALUES (?,?,?,?,?,?,?,?)",
            (str(path), agent, key, session_key, role, size, mtime_ns, sha256),
        )

    def session_files(self, agent: str, session_key: str) -> list[dict]:
        rows = self.db.execute(
            "SELECT key, role, size, sha256 FROM files WHERE agent = ? AND session_key = ? ORDER BY key",
            (agent, session_key),
        ).fetchall()
        # Codex can hold the same key under two paths (.jsonl then .jsonl.zst); keep one row per key.
        seen: dict[str, dict] = {}
        for key, role, size, sha in rows:
            seen[key] = {"key": key, "role": role, "size": size, "sha256": sha}
        return list(seen.values())

    # uploads ----------------------------------------------------------
    def uploaded_sha(self, key: str) -> str | None:
        row = self.db.execute("SELECT sha256 FROM uploads WHERE key = ?", (key,)).fetchone()
        return row[0] if row else None

    def record_upload(self, key: str, sha256: str) -> None:
        self.db.execute("INSERT OR REPLACE INTO uploads VALUES (?,?,?)", (key, sha256, time.time()))

    # sessions ---------------------------------------------------------
    def get_session(self, agent: str, session_key: str) -> dict | None:
        row = self.db.execute(
            "SELECT info, snapshots, first_seen FROM sessions WHERE agent = ? AND session_key = ?",
            (agent, session_key),
        ).fetchone()
        if not row:
            return None
        return {"info": json.loads(row[0]), "snapshots": json.loads(row[1]), "first_seen": row[2]}

    def put_session(self, agent: str, session_key: str, info: dict, snapshots: list, first_seen: float) -> None:
        self.db.execute(
            "INSERT OR REPLACE INTO sessions VALUES (?,?,?,?,?)",
            (agent, session_key, json.dumps(info, sort_keys=True), json.dumps(snapshots), first_seen),
        )

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
            "SELECT agent, role, COUNT(*), SUM(size) FROM files GROUP BY agent, role ORDER BY agent, role"
        ):
            out[f"{agent}/{role}"] = {"files": n, "bytes": total or 0}
        out["uploaded_objects"] = self.db.execute("SELECT COUNT(*) FROM uploads").fetchone()[0]
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
                    raise TimeoutError("another memsync run is holding the lock")
                time.sleep(0.25)
        try:
            yield
        finally:
            fcntl.flock(fh, fcntl.LOCK_UN)
