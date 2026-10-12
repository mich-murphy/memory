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
- Session start is different: record how the agent was launched (its command line, found by
  walking up the process tree, and allowlisted environment variables) right here, while the
  agent is still our ancestor, and hand it to the sync, which snapshots the harness and git
  state before the first prompt.

This module imports only the standard library so it starts fast.
"""

from __future__ import annotations

import hashlib
import json
import os
import re
import select
import subprocess
import sys
import time
import uuid
from pathlib import Path

PENDING_STALE_SECONDS = 120
STDIN_LIMIT = 1 << 20
DEFAULT_DELAY = 3.0
SESSION_START_EVENTS = ("SessionStart", "session_start")
ENV_PREFIXES = ("CLAUDE_CODE_", "CODEX_", "PI_", "ANTHROPIC_DEFAULT_")
ENV_NAMES = ("ANTHROPIC_MODEL", "ANTHROPIC_SMALL_FAST_MODEL", "MAX_THINKING_TOKENS", "CLAUDE_CONFIG_DIR",
             "MAX_MCP_OUTPUT_TOKENS", "DISABLE_PROMPT_CACHING")
SECRET_ENV = re.compile(r"TOKEN|KEY|SECRET|PASSWORD|AUTH|CREDENTIAL|COOKIE", re.I)
AGENT_MARKERS = {
    "claude": ("claude", "@anthropic-ai/claude-code"),
    "codex": ("codex", "@openai/codex"),
    "pi": ("pi", "pi-coding-agent"),
}


def state_dir() -> Path:
    return Path(os.environ.get("AGENT_SESSIONS_STATE_DIR") or Path("~/.local/state/agent-sessions").expanduser())


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


def _proc(pid: int) -> tuple[list[str], int] | None:
    """(argv, parent pid) of a process."""
    try:
        if sys.platform.startswith("linux"):
            with open(f"/proc/{pid}/cmdline", "rb") as fh:
                argv = [a.decode(errors="replace") for a in fh.read().split(b"\0")]
            with open(f"/proc/{pid}/stat") as fh:
                ppid = int(fh.read().rsplit(")", 1)[1].split()[1])
            return [a for a in argv if a], ppid
        r = subprocess.run(["ps", "-o", "ppid=,args=", "-p", str(pid)], capture_output=True, text=True, timeout=2)
        ppid_s, _, args = r.stdout.strip().partition(" ")
        return [args.strip()], int(ppid_s)  # ps can't split argv reliably; keep the line whole
    except (OSError, ValueError, IndexError, subprocess.SubprocessError):
        return None


def _is_agent(agent: str, argv: list[str]) -> bool:
    # On macOS argv is one `ps` line; split it roughly, for matching only.
    tokens = argv[:3] if len(argv) > 1 else (argv[0].split()[:3] if argv else [])
    text = " ".join(tokens)
    if "agent-sessions" in text or "agent_sessions" in text:
        return False
    markers = AGENT_MARKERS[agent]
    for tok in tokens:
        base = tok.split(" ", 1)[0].rsplit("/", 1)[-1]
        if base == markers[0] or base.startswith(markers[0] + "-") or any(m in tok for m in markers[1:]):
            return True
    return False


def launch_process(agent: str, limit: int = 10) -> dict:
    """The agent's own command line, found among this hook's ancestors."""
    chain = []
    pid = os.getppid()
    for _ in range(limit):
        if pid <= 1:
            break
        info = _proc(pid)
        if info is None:
            break
        argv, ppid = info
        chain.append(argv[0].rsplit("/", 1)[-1] if argv else "?")
        if _is_agent(agent, argv):
            return {"argv": argv, "pid": pid, "ancestry": chain}
        pid = ppid
    return {"argv": None, "ancestry": chain}


def _given_process(argv_json: str | None) -> dict | None:
    """argv passed in by the Pi extension (process.argv), which the process table can't show."""
    try:
        argv = json.loads(argv_json) if argv_json else None
    except ValueError:
        return None
    if isinstance(argv, list) and all(isinstance(a, str) for a in argv):
        return {"argv": argv, "pid": os.getppid(), "via": "extension"}
    return None


def launch_env() -> dict:
    return {k: v for k, v in sorted(os.environ.items())
            if (k.startswith(ENV_PREFIXES) or k in ENV_NAMES) and not SECRET_ENV.search(k)}


def record_session_start(agent: str, path: str, data: dict, event: str, extra: dict) -> None:
    launch = {
        "event": event,
        "recorded_at": time.time(),
        "source": data.get("source") or extra.get("source"),
        "cwd": data.get("cwd") or extra.get("cwd"),
        "permission_mode": data.get("permission_mode"),
        "hook_input": data or None,
        "process": _given_process(extra.get("argv")) or launch_process(agent),
        "env": launch_env(),
    }
    pending = state_dir() / "pending"
    pending.mkdir(parents=True, exist_ok=True)
    launch_file = pending / f"launch-{uuid.uuid4().hex}.json"
    fd = os.open(launch_file, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    with os.fdopen(fd, "w") as fh:
        json.dump(launch, fh)
    _spawn([sys.executable, "-m", "agent_sessions", "sync", "--agent", agent, "--path", path,
            "--launch", str(launch_file), "--trigger", f"hook:{event}"])


def spawn_sync(agent: str, path: str, marker: Path, event: str | None) -> None:
    _spawn([sys.executable, "-m", "agent_sessions", "sync", "--agent", agent, "--path", path,
            "--delay", str(DEFAULT_DELAY), "--pending-marker", str(marker), "--trigger", f"hook:{event or agent}"])


def _spawn(args: list[str]) -> None:
    subprocess.Popen(
        args,
        stdin=subprocess.DEVNULL,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        close_fds=True,
        start_new_session=True,  # own session and process group: outlives the agent's kill
        cwd="/",
    )


def run(agent: str, explicit_paths: list[str], event: str | None = None, extra: dict | None = None) -> int:
    try:
        if os.environ.get("AGENT_SESSIONS_DISABLE"):
            return 0
        data = {} if explicit_paths else read_hook_input()
        paths = explicit_paths or paths_from_input(data)
        if isinstance(data.get("hook_event_name"), str):
            event = data["hook_event_name"]
        if event in SESSION_START_EVENTS:
            for path in paths[:1]:
                record_session_start(agent, path, data, event, extra or {})
            return 0
        for path in dict.fromkeys(paths):
            marker = claim_pending(path)
            if marker is not None:
                spawn_sync(agent, path, marker, event)
    except Exception:
        pass  # never let capture break the agent
    return 0
