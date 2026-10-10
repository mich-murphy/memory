"""memsync command line.

  memsync install      hooks for Claude Code and Codex, the Pi extension, the sweep timer
  memsync backfill     upload every session already on this machine
  memsync sync         upload what changed (the timer runs this; hooks run it per session)
  memsync hook AGENT   what the agents' hooks call; returns at once
  memsync doctor       check settings, bucket access, hooks and timer
  memsync status       what has been synced from this machine
  memsync uninstall    remove hooks, extension and timer
"""

from __future__ import annotations

import argparse
import sys

AGENTS = ("claude", "codex", "pi")


def main(argv: list[str] | None = None) -> int:
    argv = sys.argv[1:] if argv is None else argv
    # Fast path: hooks run on every turn, so don't import anything heavy for them.
    if argv[:1] == ["hook"]:
        return _hook(argv[1:])

    parser = argparse.ArgumentParser(prog="memsync", description="Collect coding-agent sessions into one bucket.")
    parser.add_argument("--version", action="store_true")
    sub = parser.add_subparsers(dest="command")

    p = sub.add_parser("sync", help="upload new or changed session files")
    p.add_argument("--agent", action="append", choices=AGENTS, help="limit to these agents (repeatable)")
    p.add_argument("--path", action="append", default=[], help="sync this session file and its related files")
    p.add_argument("--delay", type=float, default=0.0, help="wait this many seconds first (lets transcripts flush)")
    p.add_argument("--pending-marker", help=argparse.SUPPRESS)
    p.add_argument("--trigger", default="manual", help=argparse.SUPPRESS)
    p.add_argument("--no-snapshots", action="store_true", help="skip harness snapshots")
    p.add_argument("--quiet", action="store_true")

    p = sub.add_parser("backfill", help="upload every session already on this machine")
    p.add_argument("--agent", action="append", choices=AGENTS)
    p.add_argument("--no-snapshots", action="store_true")

    p = sub.add_parser("install", help="install hooks, the Pi extension and the sweep timer")
    p.add_argument("--agent", action="append", choices=AGENTS, help="only these agents (default: all)")
    p.add_argument("--no-timer", action="store_true", help="don't install the periodic sweep")
    p.add_argument("--interval", type=int, default=10, help="sweep interval in minutes (default 10)")
    p.add_argument("--keep-days", type=int, default=3650,
                   help="raise Claude Code's cleanupPeriodDays to at least this (default 3650; 0 leaves it)")
    p.add_argument("--dry-run", action="store_true")

    p = sub.add_parser("uninstall", help="remove hooks, the Pi extension and the sweep timer")
    p.add_argument("--agent", action="append", choices=AGENTS)
    p.add_argument("--keep-timer", action="store_true")
    p.add_argument("--dry-run", action="store_true")

    sub.add_parser("doctor", help="check settings, bucket access, hooks and timer")
    sub.add_parser("status", help="what has been synced from this machine")

    args = parser.parse_args(argv)
    if args.version:
        from . import __version__

        print(__version__)
        return 0
    if args.command is None:
        parser.print_help()
        return 1
    return {
        "sync": _sync,
        "backfill": _sync,
        "install": _install,
        "uninstall": _uninstall,
        "doctor": _doctor,
        "status": _status,
    }[args.command](args)


def _hook(argv: list[str]) -> int:
    from . import hook

    agent = argv[0] if argv and argv[0] in AGENTS else None
    if agent is None:
        return 0  # misconfigured hook: still never fail the agent
    paths = [argv[i + 1] for i, a in enumerate(argv[:-1]) if a == "--path"]
    return hook.run(agent, paths)


def _setup_logging(cfg, quiet: bool) -> None:
    import logging
    import logging.handlers

    cfg.state_dir.mkdir(parents=True, exist_ok=True)
    root = logging.getLogger("memsync")
    root.setLevel(logging.INFO)
    fh = logging.handlers.RotatingFileHandler(cfg.state_dir / "memsync.log", maxBytes=1_000_000, backupCount=3)
    fh.setFormatter(logging.Formatter("%(asctime)s %(process)d %(levelname)s %(message)s"))
    root.addHandler(fh)
    if not quiet and sys.stderr is not None and sys.stderr.isatty():
        sh = logging.StreamHandler()
        sh.setLevel(logging.WARNING)
        root.addHandler(sh)


def _sync(args) -> int:
    import json
    import logging
    import os
    import time
    from pathlib import Path

    from .adapters import make_adapters
    from .config import Config
    from .state import State, sync_lock
    from .store import open_store
    from .sync import Syncer

    cfg = Config.load()
    backfill = args.command == "backfill"
    quiet = getattr(args, "quiet", False)
    _setup_logging(cfg, quiet)
    log = logging.getLogger("memsync")
    started = time.time()

    if getattr(args, "delay", 0):
        time.sleep(args.delay)
    marker = getattr(args, "pending_marker", None)
    paths = getattr(args, "path", [])
    trigger = "backfill" if backfill else getattr(args, "trigger", "manual")
    # A hook's sync gives up quickly if a long backfill holds the lock (the sweep catches up), so
    # waiting syncs can't pile up; a sweep skips its turn; a manual run waits.
    lock_timeout = 30 if paths else 60 if trigger == "sweep" else 600

    agents = args.agent or list(AGENTS)
    try:
        store = open_store(cfg)
    except Exception as e:
        log.error("cannot open store: %s", e)
        if not quiet:
            print(f"memsync: {e}", file=sys.stderr)
        return 2
    try:
        with sync_lock(cfg.state_dir, timeout=lock_timeout):
            if marker:
                # From here on a new hook should queue another sync: this one is about to read the file.
                try:
                    os.unlink(marker)
                except OSError:
                    pass
            state = State(cfg.state_dir)
            try:
                syncer = Syncer(cfg, store, state, make_adapters(cfg.home), backfill=backfill,
                                snapshots=not args.no_snapshots)
                if paths:
                    files = []
                    for agent in agents:
                        files.extend(syncer.hook_files(agent, [Path(p) for p in paths]))
                else:
                    files = syncer.sweep_files(agents)
                summary = syncer.run(files)
                if not paths:
                    syncer.export_metadata(agents)
                state.record_run(started, trigger, summary.as_dict())
            finally:
                state.close()
    except TimeoutError as e:
        log.warning("%s; the next sweep will catch up", e)
        return 0 if quiet else 3
    result = summary.as_dict()
    log.info("sync (%s): %s", ",".join(agents), json.dumps(result))
    if not quiet:
        print(
            f"uploaded {result['uploaded']} files ({result['bytes_uploaded'] / 1e6:.1f} MB compressed), "
            f"{result['unchanged']} unchanged, {result['manifests']} manifests, {result['errors']} errors"
        )
        for msg in result["error_messages"]:
            print(f"  error: {msg}", file=sys.stderr)
    return 1 if result["errors"] else 0


def _install(args) -> int:
    from .config import Config
    from . import install

    cfg = Config.load()
    agents = args.agent or list(AGENTS)
    cmd = install.memsync_command()
    log: list[str] = []
    steps = {
        "claude": lambda: install.install_claude(cmd, args.keep_days or None, args.dry_run, log),
        "codex": lambda: install.install_codex(cmd, args.dry_run, log),
        "pi": lambda: install.install_pi(cmd, args.dry_run, log),
    }
    failed = False
    install.write_config_template(cfg.machine_id, args.dry_run, log)
    present = install.agents_present()
    for agent in agents:
        if not args.agent and agent not in present:
            log.append(f"{agent}: not used on this machine, skipped (rerun `memsync install` after installing it)")
            continue
        try:
            steps[agent]()
        except Exception as e:
            failed = True
            log.append(f"{agent}: not changed: {type(e).__name__}: {e}")
    if not args.no_timer:
        try:
            install.install_timer(cmd, args.interval, args.dry_run, log)
        except Exception as e:
            failed = True
            log.append(f"timer: not installed: {type(e).__name__}: {e}")
    print(f"memsync command: {' '.join(cmd)}")
    for line in log:
        print(f"  {line}")
    if not args.dry_run:
        print("\nNext: `memsync doctor` to check everything, then `memsync backfill` to upload existing sessions.")
    return 1 if failed else 0


def _uninstall(args) -> int:
    from . import install

    agents = args.agent or list(AGENTS)
    cmd = install.memsync_command()
    log: list[str] = []
    for agent in agents:
        try:
            {
                "claude": lambda: install.install_claude(cmd, None, args.dry_run, log, remove=True),
                "codex": lambda: install.install_codex(cmd, args.dry_run, log, remove=True),
                "pi": lambda: install.install_pi(cmd, args.dry_run, log, remove=True),
            }[agent]()
        except Exception as e:
            log.append(f"{agent}: not changed: {type(e).__name__}: {e}")
    if not args.keep_timer:
        install.install_timer(cmd, 10, args.dry_run, log, remove=True)
    for line in log:
        print(f"  {line}")
    return 0


def _doctor(args) -> int:
    from .doctor import run

    return run()


def _status(args) -> int:
    import json
    import time

    from .config import Config
    from .state import State

    cfg = Config.load()
    state = State(cfg.state_dir)
    try:
        print(f"machine: {cfg.machine_id}   state: {cfg.state_dir}")
        for name, c in state.counts().items():
            if isinstance(c, dict):
                print(f"  {name:22} {c['files']:7} files {c['bytes'] / 1e6:10.1f} MB")
            else:
                print(f"  {name:22} {c:7}")
        for agent in AGENTS:
            for root in state.roots(agent):
                print(f"  extra {agent} root: {root}")
        print("recent runs:")
        for run in state.last_runs(8):
            when = time.strftime("%Y-%m-%d %H:%M:%S", time.localtime(run["finished_at"]))
            print(f"  {when} {run['trigger']:24} up {run['uploaded']:4}  errors {run['errors']}")
            for msg in run.get("error_messages", [])[:3]:
                print(f"      {msg}")
    finally:
        state.close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
