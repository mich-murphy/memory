#!/usr/bin/env python3
"""Explicit supervisor credential handoff; never evaluates a shell or logs values."""
import argparse
import json
import os
from pathlib import Path
import re
import stat
import sys


REQUIRED = {'R2_ACCESS_KEY_ID', 'R2_SECRET_ACCESS_KEY'}


def private_bytes(path):
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(fd, 'rb') as stream:
        info = os.fstat(stream.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_uid != os.getuid() or info.st_mode & 0o077:
            raise ValueError('unsafe credential file')
        raw = stream.read(65537)
    if len(raw) > 65536:
        raise ValueError('credential file exceeds bound')
    return raw


def credentials(path):
    raw = private_bytes(path)
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


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        # Go matches JSON struct field names case-insensitively. Normalize this
        # projection too, and reject case aliases instead of choosing a winner.
        key = key.casefold()
        if key in result:
            raise ValueError('duplicate runtime config field')
        result[key] = value
    return result


def credential_references(command):
    # This is only a bounded credential-reference projection. The binary remains
    # authoritative for full runtime validation; no source/config interpretation
    # or generic CLI parser is duplicated here.
    paths = []
    for n, word in enumerate(command[2:], 2):
        if word in ('--config', '-config'):
            if n + 1 >= len(command):
                raise ValueError('missing explicit runtime config')
            paths.append(command[n + 1])
        elif word.startswith(('--config=', '-config=')):
            paths.append(word.split('=', 1)[1])
    if len(paths) > 1:
        raise ValueError('ambiguous explicit runtime config')
    default = ('R2_ACCESS_KEY_ID', 'R2_SECRET_ACCESS_KEY')
    if not paths:
        return default
    path = Path(paths[0])
    if not path.is_absolute():
        raise ValueError('runtime config requires absolute path')
    try:
        config = json.loads(private_bytes(path), object_pairs_hook=unique_object)
    except RecursionError:
        raise ValueError('invalid runtime config nesting') from None
    if not isinstance(config, dict):
        raise ValueError('invalid runtime config object')
    backend = config.get('backend')
    if backend is None:
        return default
    if not isinstance(backend, dict):
        raise ValueError('invalid runtime backend object')
    if backend.get('kind') != 'r2':
        return default
    refs = (backend.get('access_key_env'), backend.get('secret_key_env'))
    if any(not isinstance(ref, str) or not re.fullmatch(r'[A-Za-z_][A-Za-z0-9_]*', ref) for ref in refs) or refs[0] == refs[1]:
        raise ValueError('invalid explicit credential references')
    return refs


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
        refs = credential_references(command)
        for ref in refs:
            env.pop(ref, None)
        try:
            explicit = credentials(args.credentials_env)
            env.update({refs[0]: explicit['R2_ACCESS_KEY_ID'], refs[1]: explicit['R2_SECRET_ACCESS_KEY']})
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
