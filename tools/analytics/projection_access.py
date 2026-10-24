"""Cooperative bounded cross-process projection access, separate from DuckDB locks.

All participating readers hold SH through connection close; local import holds EX.
Never hold this lock during reader exports, network calls or retry sleeps.
"""
import contextlib
import fcntl
import os
from pathlib import Path
import stat
import time


class ProjectionBusy(OSError):
    def __init__(self):
        super().__init__('projection_lock_unavailable')


@contextlib.contextmanager
def access(database, *, write=False, timeout=10):
    path = Path(database).with_name(Path(database).name + '.projection.lock')
    descriptor = None
    try:
        descriptor = os.open(path, os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW | os.O_NONBLOCK, 0o600)
        info = os.fstat(descriptor)
        if not (stat.S_ISREG(info.st_mode) and info.st_uid == os.getuid() and
                info.st_mode & 0o077 == 0 and info.st_nlink == 1):
            raise ProjectionBusy()
        deadline = time.monotonic() + timeout
        while True:
            try:
                fcntl.flock(descriptor, (fcntl.LOCK_EX if write else fcntl.LOCK_SH) | fcntl.LOCK_NB)
                break
            except BlockingIOError:
                if time.monotonic() >= deadline:
                    raise ProjectionBusy() from None
                time.sleep(.025)
    except OSError:
        if descriptor is not None:
            os.close(descriptor)
        raise ProjectionBusy() from None
    try:
        yield
    finally:
        os.close(descriptor)


@contextlib.contextmanager
def connect(database, *, timeout=10):
    import duckdb
    with access(database, timeout=timeout):
        with duckdb.connect(str(database), read_only=True) as connection:
            yield connection
