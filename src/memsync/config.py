"""Configuration from environment variables and an optional env file.

Hooks run without the user's shell profile, so credentials can also live in
``~/.config/memsync/config.env`` (KEY=VALUE lines). Real environment variables
win over the file.
"""

from __future__ import annotations

import os
import re
import socket
from dataclasses import dataclass
from pathlib import Path

DEFAULT_CONFIG_FILE = Path("~/.config/memsync/config.env").expanduser()


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


@dataclass
class Config:
    machine_id: str
    state_dir: Path
    home: Path
    r2_endpoint: str | None = None
    r2_access_key_id: str | None = None
    r2_secret_access_key: str | None = None
    r2_bucket: str | None = None
    # Write to a local directory instead of R2 (testing, or a mounted share).
    local_store: Path | None = None

    @classmethod
    def load(cls, config_file: Path | None = None) -> "Config":
        file_values = _load_env_file(config_file or Path(os.environ.get("MEMSYNC_CONFIG", DEFAULT_CONFIG_FILE)))

        def get(key: str) -> str | None:
            return os.environ.get(key) or file_values.get(key) or None

        home = Path.home()
        state_dir = Path(get("MEMSYNC_STATE_DIR") or Path("~/.local/state/memsync").expanduser())
        local_store = get("MEMSYNC_LOCAL_STORE")
        return cls(
            machine_id=_sanitize(get("MEMSYNC_MACHINE_ID") or socket.gethostname()),
            state_dir=state_dir,
            home=home,
            r2_endpoint=get("R2_ENDPOINT"),
            r2_access_key_id=get("R2_ACCESS_KEY_ID"),
            r2_secret_access_key=get("R2_SECRET_ACCESS_KEY"),
            r2_bucket=get("R2_BUCKET"),
            local_store=Path(local_store) if local_store else None,
        )

    def missing_r2_settings(self) -> list[str]:
        names = {
            "R2_ENDPOINT": self.r2_endpoint,
            "R2_ACCESS_KEY_ID": self.r2_access_key_id,
            "R2_SECRET_ACCESS_KEY": self.r2_secret_access_key,
            "R2_BUCKET": self.r2_bucket,
        }
        return [k for k, v in names.items() if not v]
