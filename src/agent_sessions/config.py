"""Configuration from environment variables and an optional env file.

Hooks run without the user's shell profile, so credentials can also live in
``~/.config/agent-sessions/config.env`` (KEY=VALUE lines). Real environment variables
win over the file.
"""

from __future__ import annotations

import os
import re
import shlex
import socket
import uuid
from dataclasses import dataclass
from pathlib import Path

DEFAULT_CONFIG_FILE = Path("~/.config/agent-sessions/config.env").expanduser()
DEFAULT_STATE_DIR = Path("~/.local/state/agent-sessions").expanduser()


def config_file() -> Path:
    return Path(os.environ.get("AGENT_SESSIONS_CONFIG") or DEFAULT_CONFIG_FILE)


def _load_env_file(path: Path) -> dict[str, str]:
    values: dict[str, str] = {}
    if not path.is_file():
        return values
    for line in path.read_text().splitlines():
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, _, value = line.partition("=")
        key = key.strip().removeprefix("export ").strip()
        value = value.strip()
        if len(value) >= 2 and value[0] == value[-1] and value[0] in "\"'":
            value = value[1:-1]
        values[key] = value
    return values


def _sanitize(name: str) -> str:
    return re.sub(r"[^A-Za-z0-9._-]+", "-", name).strip("-") or "unknown"


def machine_uuid(config_dir: Path) -> str:
    """This machine's id in object keys: a UUID made on first use and kept next to config.env.

    Hostnames collide (two laptops called "macbook", VMs rebuilt from one image), so they are
    only a label in manifests.
    """
    path = config_dir / "machine-id"
    try:
        value = path.read_text().strip()
        if value:
            return _sanitize(value)
    except OSError:
        pass
    value = str(uuid.uuid4())
    config_dir.mkdir(parents=True, exist_ok=True)
    try:
        fd = os.open(path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o644)
    except FileExistsError:
        return _sanitize(path.read_text().strip()) or value  # another process won the race
    with os.fdopen(fd, "w") as fh:
        fh.write(value + "\n")
    return value


@dataclass
class Config:
    machine_id: str
    hostname: str
    state_dir: Path
    home: Path
    config_path: Path
    r2_endpoint: str | None = None
    r2_access_key_id: str | None = None
    r2_secret_access_key: str | None = None
    r2_bucket: str | None = None
    # Write to a local directory instead of R2 (testing, or a mounted share).
    local_store: Path | None = None
    # Minimum minutes between SQLite copies of one database (Codex rewrites them constantly).
    db_interval_minutes: int = 60
    # Shadow categoriser run on each transcript delta before upload (see categorise.py). Off when unset.
    categorise_cmd: list[str] | None = None
    # TYPESAFE_* settings from config.env, passed to the categoriser (hooks don't load the shell profile).
    categorise_env: dict[str, str] | None = None

    @classmethod
    def load(cls, path: Path | None = None) -> "Config":
        path = path or config_file()
        file_values = _load_env_file(path)

        def get(key: str) -> str | None:
            return os.environ.get(key) or file_values.get(key) or None

        state_dir = Path(get("AGENT_SESSIONS_STATE_DIR") or DEFAULT_STATE_DIR)
        local_store = get("AGENT_SESSIONS_LOCAL_STORE")
        override = get("AGENT_SESSIONS_MACHINE_ID")
        try:
            db_interval = int(get("AGENT_SESSIONS_DB_INTERVAL_MINUTES") or 60)
        except ValueError:
            db_interval = 60
        return cls(
            machine_id=_sanitize(override) if override else machine_uuid(path.parent),
            hostname=socket.gethostname(),
            state_dir=state_dir,
            home=Path.home(),
            config_path=path,
            r2_endpoint=get("R2_ENDPOINT"),
            r2_access_key_id=get("R2_ACCESS_KEY_ID"),
            r2_secret_access_key=get("R2_SECRET_ACCESS_KEY"),
            r2_bucket=get("R2_BUCKET"),
            local_store=Path(local_store) if local_store else None,
            db_interval_minutes=max(0, db_interval),
            categorise_cmd=shlex.split(get("AGENT_SESSIONS_CATEGORISE_CMD") or "") or None,
            categorise_env={k: v for k, v in file_values.items() if k.startswith(("TYPESAFE_", "AGENT_SESSIONS_JEV_"))},
        )

    def missing_r2_settings(self) -> list[str]:
        names = {
            "R2_ENDPOINT": self.r2_endpoint,
            "R2_ACCESS_KEY_ID": self.r2_access_key_id,
            "R2_SECRET_ACCESS_KEY": self.r2_secret_access_key,
            "R2_BUCKET": self.r2_bucket,
        }
        return [k for k, v in names.items() if not v]
