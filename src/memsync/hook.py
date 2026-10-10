"""The command each agent runs from its hooks: hand off to a detached sync and exit at once.

Rules this follows, from each agent's docs and source (see docs/hooks.md):
- Always exit 0 and print nothing. Claude Code's Stop hook treats exit 2 as "keep working",
  and Codex's synchronous Stop hook fails on any stdout that isn't JSON.
- Return within milliseconds. Codex gives SessionEnd hooks 1 s by default (3 s max), Claude
  Code 1.5 s, and both kill a hook's process group when it times out.
- Hand the real work to a process in its own session with stdio detached, so it survives
  `claude -p` teardown, Codex's process-group kill and Pi exiting, and so the agent isn't
  left waiting for the hook's stdout to close.
- Debounce: Stop fires every turn. If a sync for the same file is already queued, don't queue
  another; the queued one reads the file after its delay, so it picks up this turn too.

This module imports only the standard library so it starts fast.
"""

from __future__ import annotations

import hashlib
import json
import os
import select
import subprocess
import sys
import time
from pathlib import Path

PENDING_STALE_SECONDS = 120
STDIN_LIMIT = 1 << 20
DEFAULT_DELAY = 3.0


def state_dir() -> Path:
    return Path(os.environ.get("MEMSYNC_STATE_DIR") or Path("~/.local/state/memsync").expanduser())


def read_hook_input(timeout: float = 2.0) -> dict:
    """Read the JSON object Claude Code and Codex write to a hook's stdin."""
    if sys.stdin is None or sys.stdin.closed:
        return {}
    try:
        if sys.stdin.isatty():
            return {}
        fd = sys.stdin.fileno()
        chunks: list[bytes] = []
        deadline = time.monotonic() + timeout
        size = 0
        while size < STDIN_LIMIT:
            remaining = deadline - time.monotonic()
            if remaining <= 0 or not select.select([fd], [], [], remaining)[0]:
                break
            chunk = os.read(fd, 65536)
            if not chunk:
                break
            chunks.append(chunk)
            size += len(chunk)
        data = json.loads(b"".join(chunks) or b"{}")
        return data if isinstance(data, dict) else {}
    except (OSError, ValueError):
        return {}


def paths_from_input(data: dict) -> list[str]:
    # transcript_path is the session; agent_transcript_path is a subagent's (SubagentStop).
    # Codex sends transcript_path: null for sessions it doesn't persist.
    out = []
    for key in ("transcript_path", "agent_transcript_path"):
        value = data.get(key)
        if isinstance(value, str) and value:
            out.append(value)
    return out


def claim_pending(path: str) -> Path | None:
    """Create a marker for a queued sync of this path; None if one is already queued."""
    pending = state_dir() / "pending"
    pending.mkdir(parents=True, exist_ok=True)
    marker = pending / hashlib.sha256(path.encode()).hexdigest()[:24]
    try:
        fd = os.open(marker, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    except FileExistsError:
        try:
            if time.time() - marker.stat().st_mtime < PENDING_STALE_SECONDS:
                return None
            marker.unlink()  # left behind by a sync that died; take it over
            fd = os.open(marker, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
        except OSError:
            return None
    os.write(fd, path.encode())
    os.close(fd)
    return marker


def spawn_sync(agent: str, path: str, marker: Path, event: str | None) -> None:
    args = [sys.executable, "-m", "memsync", "sync", "--agent", agent, "--path", path,
            "--delay", str(DEFAULT_DELAY), "--pending-marker", str(marker), "--trigger", f"hook:{event or agent}"]
    subprocess.Popen(
        args,
        stdin=subprocess.DEVNULL,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        close_fds=True,
        start_new_session=True,  # own session and process group: outlives the agent's kill
        cwd="/",
    )


def run(agent: str, explicit_paths: list[str]) -> int:
    try:
        if os.environ.get("MEMSYNC_DISABLE"):
            return 0
        data = {} if explicit_paths else read_hook_input()
        paths = explicit_paths or paths_from_input(data)
        event = data.get("hook_event_name") if isinstance(data.get("hook_event_name"), str) else None
        for path in dict.fromkeys(paths):
            marker = claim_pending(path)
            if marker is not None:
                spawn_sync(agent, path, marker, event)
    except Exception:
        pass  # never let capture break the agent
    return 0
