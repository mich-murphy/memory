#!/usr/bin/env python3
"""Explicit supervisor credential handoff; never evaluates a shell or logs values."""
import argparse
import os
from pathlib import Path
import re
import stat
import sys


REQUIRED = {'R2_ACCESS_KEY_ID', 'R2_SECRET_ACCESS_KEY'}


def credentials(path):
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(fd, 'rb') as stream:
        info = os.fstat(stream.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_uid != os.getuid() or info.st_mode & 0o077:
            raise ValueError('unsafe credential file')
        raw = stream.read(65537)
    if len(raw) > 65536:
        raise ValueError('credential file exceeds bound')
    values = {}
    for line in raw.decode('utf-8').splitlines():
        line = line.strip()
        if not line or line.startswith('#'):
            continue
        match = re.fullmatch(r'(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)', line)
        if not match or match[1] not in REQUIRED:
            continue
        key, value = match.groups()
        if key in values:
            raise ValueError('duplicate credential reference')
        if len(value) >= 2 and value[0] == value[-1] and value[0] in "\"'":
            value = value[1:-1]
        if not value or any(c.isspace() or c in '$`\\\"\'\x00' for c in value):
            raise ValueError('ambiguous credential syntax')
        values[key] = value
    if set(values) != REQUIRED:
        raise ValueError('missing explicit credential reference')
    return values


def arguments(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', required=True, type=Path)
    parser.add_argument('--credentials-env', required=True, type=Path)
    parser.add_argument('command', nargs=argparse.REMAINDER)
    args = parser.parse_args(argv)
    command = args.command
    if command[:1] == ['--']:
        command = command[1:]
    if not command or command[0] != 'capture-v1':
        raise ValueError('launcher only runs explicit candidate commands')
    if not args.binary.is_absolute() or not args.credentials_env.is_absolute():
        raise ValueError('launcher requires absolute explicit paths')
    info = args.binary.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_uid != os.getuid() or info.st_mode & 0o022 or not info.st_mode & stat.S_IXUSR:
        raise ValueError('unsafe candidate executable')
    env = os.environ.copy()
    # Hooks and local inspection need no destination credentials. In particular,
    # an isolated native producer must not need read access to the private file
    # merely to append a lightweight hint/evidence record.
    for name in REQUIRED:
        env.pop(name, None)
    if len(command) >= 2 and command[1] in ('init', 'run', 'export', 'current-heads'):
        try:
            env.update(credentials(args.credentials_env))
        except (OSError, ValueError, UnicodeError):
            if command[1] != 'run':
                raise
            # The coordinator owns durable capture independently of destination
            # readiness. No values from a partially parsed file or ambient env
            # survive this failure. Repair requires a deliberate process restart.
            print('candidate destination credentials unavailable; local capture only '
                  '(private details withheld; repair credentials and restart, then resume)',
                  file=sys.stderr)
    # A supervisor launch must not incidentally opt into any development live gate.
    for name in ('AGENT_SESSIONS_TEST_R2', 'AGENT_SESSIONS_LIVE', 'AGENT_SESSIONS_CAPTURE_NATIVE', 'CANDIDATE_R2_LIVE_ACCEPTANCE'):
        env.pop(name, None)
    return args.binary, command, env


def main(argv=None):
    try:
        binary, command, env = arguments(argv)
        os.execve(binary, [str(binary), *command], env)
    except (OSError, ValueError, UnicodeError):
        print('candidate launcher failed (private details withheld)', file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())
