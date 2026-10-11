"""Install and remove the hooks, the Pi extension and the sweep timer.

Every edit is idempotent: agent-sessions' own entries are recognised by their command, removed and
re-added, and anything else in the file is left alone. A file is backed up before it changes
and never rewritten if it can't be parsed.
"""

from __future__ import annotations

import json
import os
import platform
import re
import shlex
import shutil
import subprocess
import sys
import time
from pathlib import Path

from .adapters import make_adapters
from .config import config_file

# Also matches entries written by memsync, this tool's earlier name, so a reinstall replaces them.
OURS = re.compile(r"(?:agent-sessions|agent_sessions|memsync).*\bhook (claude|codex|pi)\b")
PI_MARKER = "// agent-sessions: managed by `agent-sessions install`"

# Claude Code (https://code.claude.com/docs/en/hooks):
#   SessionStart (startup, resume, clear, compact) records the launch and the harness before the
#   first prompt. Stop fires after every turn but not on a user interrupt; StopFailure covers turns
#   that end on an API error; SubagentStop carries agent_transcript_path; PreCompact catches the
#   transcript before compaction; TaskCompleted and TeammateIdle catch task lists and agent-team
#   mailboxes that Claude Code deletes when the team is done; SessionEnd covers /clear, /resume,
#   exit and logout. `async` keeps even a slow interpreter start off Claude's path. SessionEnd
#   stays synchronous (it runs during teardown) with a timeout above our ~0.1 s.
_ASYNC = {"async": True, "timeout": 30}
CLAUDE_EVENTS = {
    "SessionStart": _ASYNC,
    "Stop": _ASYNC,
    "StopFailure": _ASYNC,
    "SubagentStop": _ASYNC,
    "PreCompact": _ASYNC,
    "TaskCompleted": _ASYNC,
    "TeammateIdle": _ASYNC,
    "SessionEnd": {"timeout": 5},
}
# Codex (codex-rs/hooks, codex-rs/config/src/hook_config.rs):
#   SessionStart on startup and resume, Stop after every turn, SubagentStop for subagent threads,
#   PreCompact before compaction, Interrupt when the user cancels a turn, SessionEnd on exit (sync
#   only, 1 s default, clamped to 3 s). Async hooks ignore stdout, which keeps a chatty login shell
#   (Codex runs hooks with `$SHELL -lc`) from failing Stop.
CODEX_EVENTS = {
    "SessionStart": _ASYNC,
    "Stop": _ASYNC,
    "SubagentStop": _ASYNC,
    "PreCompact": _ASYNC,
    "Interrupt": {"async": True, "timeout": 3},
    "SessionEnd": {"timeout": 3},
}

PI_EXTENSION = """{marker}; edits are overwritten.
// Ships this Pi session to the agent-sessions store. agent-sessions hands the upload to a detached process,
// so these handlers return immediately. Docs: https://github.com/mich-murphy/memory
import {{ spawn }} from "node:child_process";
import type {{ ExtensionAPI }} from "@earendil-works/pi-coding-agent";

const AGENT_SESSIONS: string[] = {command};

function kick(file: string | undefined, extra: string[] = []): void {{
\t// No file for --no-session runs; Pi also creates the file only after the first message.
\tif (!file || process.env.AGENT_SESSIONS_DISABLE) return;
\ttry {{
\t\tconst child = spawn(AGENT_SESSIONS[0], [...AGENT_SESSIONS.slice(1), "hook", "pi", "--path", file, ...extra], {{
\t\t\tdetached: true,
\t\t\tstdio: "ignore",
\t\t}});
\t\tchild.on("error", () => {{}});
\t\tchild.unref();
\t}} catch {{
\t\t// Capture must never break the session.
\t}}
}}

export default function (pi: ExtensionAPI) {{
\t// Startup, /new, /resume, /fork, /reload: record the launch and the harness before the first prompt.
\t// Pi renames its process, which hides its arguments from the process table, so pass them along.
\tpi.on("session_start", (event, ctx) =>
\t\tkick(ctx.sessionManager.getSessionFile(), [
\t\t\t"--event", "session_start", "--source", event.reason, "--cwd", ctx.cwd, "--argv", JSON.stringify(process.argv),
\t\t]),
\t);
\t// A prompt has fully finished: no retries, compaction or queued follow-ups left.
\tpi.on("agent_settled", (_event, ctx) => kick(ctx.sessionManager.getSessionFile()));
\t// Quit (including SIGTERM/SIGHUP), /new, /resume, /fork and /reload: flush the session being left.
\tpi.on("session_shutdown", (_event, ctx) => kick(ctx.sessionManager.getSessionFile()));
}}
"""

SYSTEMD_SERVICE = """[Unit]
Description=agent-sessions sweep: upload new coding-agent session files

[Service]
Type=oneshot
ExecStart={command} sync --quiet --trigger sweep
Nice=10
IOSchedulingClass=idle
"""

SYSTEMD_TIMER = """[Unit]
Description=Run the agent-sessions sweep every {minutes} minutes

[Timer]
OnBootSec=2min
OnUnitActiveSec={minutes}min
Persistent=true

[Install]
WantedBy=timers.target
"""

LAUNCHD_LABEL = "tech.agent-sessions.sweep"
LAUNCHD_PLIST = """<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>{label}</string>
  <key>ProgramArguments</key>
  <array>
{args}
  </array>
  <key>StartInterval</key><integer>{seconds}</integer>
  <key>RunAtLoad</key><true/>
  <key>LowPriorityIO</key><true/>
  <key>Nice</key><integer>10</integer>
  <key>StandardOutPath</key><string>/dev/null</string>
  <key>StandardErrorPath</key><string>/dev/null</string>
</dict>
</plist>
"""
CRON_MARKER = "# agent-sessions sweep"


def agent_sessions_command() -> list[str]:
    """The command hooks should run. Prefer the installed `agent-sessions` shim: its path survives
    upgrades, which keeps Codex's trust hash for the hook valid."""
    found = shutil.which("agent-sessions")
    if found:
        return [str(Path(found).absolute())]
    return [sys.executable, "-m", "agent_sessions"]


def _backup(path: Path) -> Path | None:
    if not path.exists():
        return None
    backup = path.with_name(f"{path.name}.agent-sessions-backup-{time.strftime('%Y%m%d-%H%M%S')}")
    shutil.copy2(path, backup)
    return backup


def _write(path: Path, text: str, dry_run: bool, log: list[str]) -> None:
    old = path.read_text() if path.exists() else None
    if old == text:
        log.append(f"unchanged: {path}")
        return
    if dry_run:
        log.append(f"would write: {path}")
        return
    path.parent.mkdir(parents=True, exist_ok=True)
    backup = _backup(path)
    tmp = path.with_name(path.name + ".agent-sessions-tmp")
    tmp.write_text(text)
    if path.exists():
        shutil.copymode(path, tmp)
    tmp.replace(path)
    log.append(f"wrote: {path}" + (f" (backup: {backup.name})" if backup else ""))


def _load_json(path: Path) -> dict:
    if not path.exists():
        return {}
    text = path.read_text()
    if not text.strip():
        return {}
    data = json.loads(text)  # raises: never overwrite a file we can't parse
    if not isinstance(data, dict):
        raise ValueError(f"{path} does not contain a JSON object")
    return data


def merge_hooks(config: dict, events: dict[str, dict], command: str | None) -> dict:
    """Remove agent-sessions' handlers from every event, then add one group per event (unless command is None)."""
    hooks = config.get("hooks")
    if not isinstance(hooks, dict):
        hooks = {}
    for event in list(hooks):
        groups = hooks[event] if isinstance(hooks[event], list) else []
        kept = []
        for group in groups:
            handlers = group.get("hooks", []) if isinstance(group, dict) else []
            others = [h for h in handlers if not (isinstance(h, dict) and OURS.search(str(h.get("command", ""))))]
            if others or not handlers:
                kept.append({**group, "hooks": others} if handlers else group)
        if kept:
            hooks[event] = kept
        else:
            del hooks[event]
    if command is not None:
        for event, extra in events.items():
            hooks.setdefault(event, []).append({"hooks": [{"type": "command", "command": command, **extra}]})
    if hooks:
        config["hooks"] = hooks
    else:
        config.pop("hooks", None)
    return config


def agents_present() -> set[str]:
    """Agents with a config dir or a command on PATH."""
    adapters = make_adapters(Path.home())
    dirs = {
        "claude": adapters["claude"].default_roots()[0],
        "codex": adapters["codex"].default_roots()[0],
        "pi": adapters["pi"].agent_dir(),
    }
    return {name for name, d in dirs.items() if d.exists() or shutil.which(name)}


# Claude Code ----------------------------------------------------------------
def install_claude(cmd: list[str], keep_days: int | None, dry_run: bool, log: list[str], remove: bool = False) -> None:
    root = make_adapters(Path.home())["claude"].default_roots()[0]
    settings_path = root / "settings.json"
    settings = _load_json(settings_path)
    merge_hooks(settings, CLAUDE_EVENTS, None if remove else shlex.join([*cmd, "hook", "claude"]))
    if not remove and keep_days:
        current = settings.get("cleanupPeriodDays")
        if not isinstance(current, int) or current < keep_days:
            settings["cleanupPeriodDays"] = keep_days
            log.append(f"claude: cleanupPeriodDays {current or 30} -> {keep_days} (transcripts were being deleted after it)")
    _write(settings_path, json.dumps(settings, indent=2) + "\n", dry_run, log)


# Codex ------------------------------------------------------------------------
def install_codex(cmd: list[str], dry_run: bool, log: list[str], remove: bool = False) -> None:
    root = make_adapters(Path.home())["codex"].default_roots()[0]
    hooks_path = root / "hooks.json"
    config = _load_json(hooks_path)
    merge_hooks(config, CODEX_EVENTS, None if remove else shlex.join([*cmd, "hook", "codex"]))
    if remove and not config:
        if hooks_path.exists() and not dry_run:
            _backup(hooks_path)
            hooks_path.unlink()
        log.append(f"removed: {hooks_path}")
        return
    _write(hooks_path, json.dumps(config, indent=2) + "\n", dry_run, log)
    if not remove:
        log.append("codex: open Codex and run /hooks once to trust the agent-sessions hooks. Codex skips new hooks until they're trusted.")
        if _toml_has_hook_events(root / "config.toml"):
            log.append("codex: config.toml also defines [hooks]; Codex runs both but warns. Moving them into hooks.json silences it.")


def codex_trust_state(root: Path) -> tuple[int, int]:
    """(agent-sessions hooks configured, of those with a trusted hash recorded in config.toml)."""
    hooks_path = root / "hooks.json"
    try:
        config = _load_json(hooks_path)
    except (OSError, ValueError):
        return 0, 0
    keys = []
    for event, groups in (config.get("hooks") or {}).items():
        for gi, group in enumerate(groups if isinstance(groups, list) else []):
            for hi, handler in enumerate(group.get("hooks", []) if isinstance(group, dict) else []):
                if isinstance(handler, dict) and OURS.search(str(handler.get("command", ""))):
                    keys.append(f"{hooks_path}:{_codex_event_key(event)}:{gi}:{hi}")
    try:
        toml_text = (root / "config.toml").read_text()
    except OSError:
        return len(keys), 0
    try:
        import tomllib

        state = tomllib.loads(toml_text).get("hooks", {}).get("state", {})
    except Exception:
        return len(keys), 0
    trusted = sum(1 for k in keys if isinstance(state.get(k), dict) and state[k].get("trusted_hash"))
    return len(keys), trusted


def _toml_has_hook_events(path: Path) -> bool:
    try:
        import tomllib

        hooks = tomllib.loads(path.read_text()).get("hooks", {})
    except Exception:
        return False
    return isinstance(hooks, dict) and any(k != "state" for k in hooks)


def _codex_event_key(event: str) -> str:
    # codex-rs/hooks/src/lib.rs hook_event_key_label: snake_case event names.
    return re.sub(r"(?<!^)(?=[A-Z])", "_", event).lower()


# Pi ---------------------------------------------------------------------------
def install_pi(cmd: list[str], dry_run: bool, log: list[str], remove: bool = False) -> None:
    agent_dir = make_adapters(Path.home())["pi"].agent_dir()
    path = agent_dir / "extensions" / "agent-sessions.ts"
    if path.exists() and PI_MARKER not in path.read_text():
        log.append(f"pi: {path} exists and isn't agent-sessions'; leaving it alone")
        return
    if remove:
        if path.exists() and not dry_run:
            path.unlink()
        log.append(f"removed: {path}")
        return
    _write(path, PI_EXTENSION.format(marker=PI_MARKER, command=json.dumps(cmd)), dry_run, log)
    log.append("pi: new Pi sessions load the extension; run /reload in open ones.")


# Sweep timer --------------------------------------------------------------------
def _run(args: list[str]) -> subprocess.CompletedProcess:
    return subprocess.run(args, capture_output=True, text=True, timeout=30)


def _systemd_user_available() -> bool:
    if platform.system() != "Linux" or not shutil.which("systemctl"):
        return False
    try:
        return _run(["systemctl", "--user", "show-environment"]).returncode == 0
    except (OSError, subprocess.SubprocessError):
        return False


def install_timer(cmd: list[str], minutes: int, dry_run: bool, log: list[str], remove: bool = False) -> None:
    minutes = max(1, min(minutes, 59))
    system = platform.system()
    if system == "Darwin":
        plist = Path.home() / "Library" / "LaunchAgents" / f"{LAUNCHD_LABEL}.plist"
        domain = f"gui/{os.getuid()}"
        if not dry_run:
            _run(["launchctl", "bootout", domain, str(plist)])
        if remove:
            if plist.exists() and not dry_run:
                plist.unlink()
            log.append(f"removed: {plist}")
            return
        args = "\n".join(f"    <string>{a}</string>" for a in [*cmd, "sync", "--quiet", "--trigger", "sweep"])
        _write(plist, LAUNCHD_PLIST.format(label=LAUNCHD_LABEL, args=args, seconds=minutes * 60), dry_run, log)
        if not dry_run:
            r = _run(["launchctl", "bootstrap", domain, str(plist)])
            log.append("launchd: sweep loaded" if r.returncode == 0 else f"launchd: bootstrap failed: {r.stderr.strip()}")
        return

    if _systemd_user_available():
        unit_dir = Path(os.environ.get("XDG_CONFIG_HOME", Path.home() / ".config")) / "systemd" / "user"
        service, timer = unit_dir / "agent-sessions.service", unit_dir / "agent-sessions.timer"
        if remove:
            if not dry_run:
                _run(["systemctl", "--user", "disable", "--now", "agent-sessions.timer"])
                for p in (service, timer):
                    if p.exists():
                        p.unlink()
                _run(["systemctl", "--user", "daemon-reload"])
            log.append(f"removed: {service}, {timer}")
            return
        _write(service, SYSTEMD_SERVICE.format(command=shlex.join(cmd)), dry_run, log)
        _write(timer, SYSTEMD_TIMER.format(minutes=minutes), dry_run, log)
        if not dry_run:
            _run(["systemctl", "--user", "daemon-reload"])
            r = _run(["systemctl", "--user", "enable", "--now", "agent-sessions.timer"])
            log.append("systemd: agent-sessions.timer enabled" if r.returncode == 0 else f"systemd: enable failed: {r.stderr.strip()}")
        if not linger_enabled():
            log.append(
                "systemd: user timers stop when you log out. To keep sweeping while logged out "
                f"(e.g. agents left running in tmux on a VM), run: sudo loginctl enable-linger {os.environ.get('USER', '$USER')}"
            )
        return

    if not shutil.which("crontab"):
        log.append("timer: no systemd user session, launchd or crontab found; run `agent-sessions sync` periodically yourself")
        return
    current = _run(["crontab", "-l"])
    lines = [l for l in (current.stdout.splitlines() if current.returncode == 0 else []) if CRON_MARKER not in l]
    if not remove:
        lines.append(f"*/{minutes} * * * * {shlex.join([*cmd, 'sync', '--quiet', '--trigger', 'sweep'])} >/dev/null 2>&1 {CRON_MARKER}")
    if dry_run:
        log.append("would update crontab")
        return
    r = subprocess.run(["crontab", "-"], input="\n".join(lines) + "\n", capture_output=True, text=True, timeout=30)
    log.append(("cron: sweep removed" if remove else "cron: sweep installed") if r.returncode == 0 else f"cron: failed: {r.stderr.strip()}")


def linger_enabled() -> bool:
    user = os.environ.get("USER")
    if not user or not shutil.which("loginctl"):
        return True  # can't tell; don't nag
    try:
        r = _run(["loginctl", "show-user", user, "-p", "Linger"])
    except (OSError, subprocess.SubprocessError):
        return True
    return r.returncode != 0 or "Linger=yes" in r.stdout


def timer_installed() -> str | None:
    if platform.system() == "Darwin":
        return "launchd" if (Path.home() / "Library" / "LaunchAgents" / f"{LAUNCHD_LABEL}.plist").exists() else None
    if _systemd_user_available():
        r = _run(["systemctl", "--user", "is-enabled", "agent-sessions.timer"])
        if r.returncode == 0:
            return "systemd"
    if shutil.which("crontab"):
        r = _run(["crontab", "-l"])
        if r.returncode == 0 and CRON_MARKER in r.stdout:
            return "cron"
    return None


# Config -------------------------------------------------------------------------
CONFIG_TEMPLATE = """# agent-sessions settings. Hooks and timers don't load your shell profile, so they read this file.
# Real environment variables override it.
R2_ENDPOINT={R2_ENDPOINT}
R2_BUCKET={R2_BUCKET}
R2_ACCESS_KEY_ID={R2_ACCESS_KEY_ID}
R2_SECRET_ACCESS_KEY={R2_SECRET_ACCESS_KEY}
# This machine's id in object keys is a UUID kept in machine-id next to this file.
# Minimum minutes between uploads of a changed SQLite database (Codex's state and logs).
# AGENT_SESSIONS_DB_INTERVAL_MINUTES=60
# Shadow categoriser: labels each transcript delta before upload and drops nothing. Off unless set.
# `agent-sessions labels` shows what it would have marked as waste. Jev needs the [jev] extra.
# AGENT_SESSIONS_CATEGORISE_CMD=agent-sessions jev
# TYPESAFE_API_KEY=
# AGENT_SESSIONS_JEV_QUESTIONS=~/.config/agent-sessions/jev-questions.json
"""


def write_config_template(dry_run: bool, log: list[str]) -> Path:
    path = config_file()
    if path.exists():
        log.append(f"config: keeping {path}")
        return path
    values = {k: os.environ.get(k, "") for k in ("R2_ENDPOINT", "R2_BUCKET", "R2_ACCESS_KEY_ID", "R2_SECRET_ACCESS_KEY")}
    if dry_run:
        log.append(f"would write: {path}")
        return path
    path.parent.mkdir(parents=True, exist_ok=True)
    fd = os.open(path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    with os.fdopen(fd, "w") as fh:
        fh.write(CONFIG_TEMPLATE.format(**values))
    missing = [k for k, v in values.items() if not v]
    log.append(f"config: wrote {path}" + (f"; fill in {', '.join(missing)}" if missing else " from your environment"))
    return path
