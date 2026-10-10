"""`memsync doctor`: check everything a silent hook can't report."""

from __future__ import annotations

import json
import shutil
import time
import uuid
from pathlib import Path

from . import install
from .adapters import make_adapters
from .config import Config

OK, WARN, FAIL = "ok  ", "warn", "FAIL"


def run() -> int:
    cfg = Config.load()
    results: list[tuple[str, str]] = []

    def report(level: str, msg: str) -> None:
        results.append((level, msg))
        print(f"[{level}] {msg}")

    report(OK, f"machine id: {cfg.machine_id}")

    # Store
    if cfg.local_store:
        report(WARN, f"writing to a local directory, not R2: {cfg.local_store}")
    elif missing := cfg.missing_r2_settings():
        report(FAIL, f"missing {', '.join(missing)} (set them in {install.DEFAULT_CONFIG_FILE})")
    else:
        try:
            from .store import open_store

            store = open_store(cfg)
            key = f"_memsync/doctor/{cfg.machine_id}/{uuid.uuid4().hex}"
            store.put(key, b"ok")
            if not store.exists(key):
                raise RuntimeError("test object not found after writing it")
            store.delete(key)
            report(OK, f"bucket {cfg.r2_bucket}: write, read and delete work")
        except Exception as e:
            report(FAIL, f"bucket {cfg.r2_bucket}: {type(e).__name__}: {e}")

    try:
        import zstandard  # noqa: F401

        report(OK, "zstandard available for Codex .jsonl.zst rollouts")
    except ImportError:
        report(WARN, "zstandard missing: Codex rollouts older than 7 days can't be read")

    adapters = make_adapters(Path.home())

    # Claude Code
    claude_root = adapters["claude"].default_roots()[0]
    if claude_root.exists():
        settings = _json(claude_root / "settings.json")
        n = _count_ours(settings)
        report(OK if n >= len(install.CLAUDE_EVENTS) else FAIL,
               f"claude: {n}/{len(install.CLAUDE_EVENTS)} memsync hooks in {claude_root / 'settings.json'}")
        if settings.get("disableAllHooks"):
            report(FAIL, "claude: disableAllHooks is set, so no hooks run")
        days = settings.get("cleanupPeriodDays", 30)
        report(OK if isinstance(days, int) and days >= 365 else WARN,
               f"claude: cleanupPeriodDays = {days} (Claude Code deletes older transcripts)")
    else:
        report(OK, f"claude: not used here ({claude_root} missing)")

    # Codex
    codex_root = adapters["codex"].default_roots()[0]
    if codex_root.exists():
        configured, trusted = install.codex_trust_state(codex_root)
        report(OK if configured >= len(install.CODEX_EVENTS) else FAIL,
               f"codex: {configured}/{len(install.CODEX_EVENTS)} memsync hooks in {codex_root / 'hooks.json'}")
        if configured:
            report(OK if trusted == configured else FAIL,
                   f"codex: {trusted}/{configured} memsync hooks trusted"
                   + ("" if trusted == configured else " (open Codex and review them in /hooks)"))
        if _codex_hooks_disabled(codex_root / "config.toml"):
            report(FAIL, "codex: [features] hooks = false in config.toml, so no hooks run")
    else:
        report(OK, f"codex: not used here ({codex_root} missing)")

    # Pi
    pi_dir = adapters["pi"].agent_dir()
    if pi_dir.exists():
        ext = pi_dir / "extensions" / "memsync.ts"
        ok = ext.exists() and install.PI_MARKER in ext.read_text()
        report(OK if ok else FAIL, f"pi: extension {'installed' if ok else 'missing'} at {ext}")
    else:
        report(OK, f"pi: not used here ({pi_dir} missing)")

    # Hook command still points at an executable
    cmd = install.memsync_command()
    report(OK if shutil.which(cmd[0]) or Path(cmd[0]).exists() else FAIL, f"memsync command: {' '.join(cmd)}")

    # Sweep timer and recent runs
    timer = install.timer_installed()
    report(OK if timer else FAIL, f"sweep timer: {timer or 'not installed (run memsync install)'}")
    if timer == "systemd" and not install.linger_enabled():
        report(WARN, "systemd user timers stop when you log out; `sudo loginctl enable-linger $USER` keeps them running")
    try:
        from .state import State

        state = State(cfg.state_dir)
        runs = state.last_runs(20)
        state.close()
    except Exception:
        runs = []
    sweeps = [r for r in runs if r["trigger"] == "sweep"]
    if sweeps:
        age = time.time() - sweeps[0]["finished_at"]
        report(OK if age < 3600 else WARN, f"last sweep {age / 60:.0f} min ago, {sweeps[0]['errors']} errors")
    elif timer:
        report(WARN, "no sweep has run yet")
    errors = [m for r in runs for m in r.get("error_messages", [])]
    if errors:
        report(WARN, f"recent errors (see {cfg.state_dir / 'memsync.log'}): {errors[0]}")

    return 1 if any(level == FAIL for level, _ in results) else 0


def _json(path: Path) -> dict:
    try:
        data = json.loads(path.read_text())
        return data if isinstance(data, dict) else {}
    except (OSError, ValueError):
        return {}


def _count_ours(settings: dict) -> int:
    n = 0
    for groups in (settings.get("hooks") or {}).values():
        for group in groups if isinstance(groups, list) else []:
            for h in group.get("hooks", []) if isinstance(group, dict) else []:
                if isinstance(h, dict) and install.OURS.search(str(h.get("command", ""))):
                    n += 1
    return n


def _codex_hooks_disabled(path: Path) -> bool:
    try:
        import tomllib

        features = tomllib.loads(path.read_text()).get("features", {})
    except Exception:
        return False
    return features.get("hooks") is False or features.get("codex_hooks") is False
