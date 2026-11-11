#!/usr/bin/env python3
"""Offline synthetic prerequisite probes. No host-root discovery or network I/O."""
import argparse
import concurrent.futures
import ctypes
import hashlib
import json
import os
from pathlib import Path
import platform
import random
import resource
import statistics
import subprocess
import sys
import tempfile
import time
from compression import zstd

HERE = Path(__file__).resolve().parent


def sha(b):
    return hashlib.sha256(b).hexdigest()


def timed(fn, count=15):
    samples = []
    for _ in range(count):
        start = time.perf_counter_ns()
        fn()
        samples.append((time.perf_counter_ns() - start) / 1e6)
    samples.sort()
    return {"p50_ms": statistics.median(samples), "p99_ms": samples[-1], "n": count}


def p1(tmp):
    rng = random.Random(1601)
    alternatives = [("4+16", 4096, 16384), ("4+64", 4096, 65536),
                    ("4+256", 4096, 262144), ("16+16", 16384, 16384),
                    ("256+256", 262144, 262144), ("full", None, None),
                    ("disabled", 0, 0)]
    results = {name: {"rewrites": 0, "detected": 0, "unsafe_commits": 0,
                      "scoped_unsafe_commits": 0, "audit_failures": 0,
                      "by_kind": {}} for name, _, _ in alternatives}
    total = 0
    for size in [2048, 32768, 131072, 1048576, 4194304]:
        old = rng.randbytes(size)
        for kind in ["append", "head", "tail", "middle", "same_size", "truncate_regrow", "rename"]:
            for _ in range(12):
                # Generator owns the complete source bytes, not a planner digest.
                current = bytearray(old)
                pos = None
                if kind in ("head", "tail", "middle", "same_size"):
                    pos = {"head": 0, "tail": size - 1, "middle": size // 2,
                           "same_size": rng.randrange(size)}[kind]
                    current[pos] ^= 1
                elif kind == "truncate_regrow":
                    cut = rng.randrange(size)
                    current[cut:] = rng.randbytes(size - cut)
                elif kind == "rename":
                    current = bytearray(rng.randbytes(size))
                if kind != "same_size":
                    current += rng.randbytes(257)
                current = bytes(current)
                rewritten = current[:size] != old
                # Independent safety scope derives from raw generator bytes.
                required = (kind == "rename" or current[:min(4096, size)] != old[:min(4096, size)]
                            or current[max(0, size-65536):size] != old[max(0, size-65536):size])
                for name, head, tail in alternatives:
                    r = results[name]
                    changed = kind == "rename"
                    if head is None:
                        changed |= sha(current[:size]) != sha(old)
                    else:
                        changed |= sha(current[:min(head, size)]) != sha(old[:min(head, size)])
                        changed |= sha(current[max(0, size-tail):size]) != sha(old[max(0, size-tail):size])
                    assembled = current if changed else old + current[size:]
                    # Oracle never calls detection logic. It compares full bytes.
                    unsafe = not current.startswith(assembled)
                    r["rewrites"] += rewritten
                    r["detected"] += changed and rewritten
                    r["unsafe_commits"] += unsafe
                    r["scoped_unsafe_commits"] += unsafe and required
                    # Fresh audit replaces any mismatching epoch from generator truth.
                    audited = current if sha(assembled) != sha(current) else assembled
                    r["audit_failures"] += audited != current
                    d = r["by_kind"].setdefault(kind, [0, 0, 0])
                    d[0] += 1
                    d[1] += changed
                    d[2] += unsafe
                total += 1
    assert results["4+64"]["scoped_unsafe_commits"] == 0
    assert results["disabled"]["scoped_unsafe_commits"] > 0
    assert results["full"]["unsafe_commits"] == 0
    assert all(r["audit_failures"] == 0 for r in results.values())
    data = rng.randbytes(4 * 1024 * 1024)
    path = tmp / "p1-source"
    path.write_bytes(data)
    costs = {}
    for name, head, tail in alternatives:
        ranges = [(0, len(data))] if head is None else [(0, min(head, len(data))),
                  (max(0, len(data)-tail), min(tail, len(data)))]
        def check():
            with path.open("rb", buffering=0) as f:
                for off, length in ranges:
                    f.seek(off)
                    sha(f.read(length))
        costs[name] = {"bytes_read": sum(n for _, n in ranges), **timed(check)}
    return {"seed": 1601, "cases": total, "results": results, "warm_file_check": costs,
            "by_kind_columns": ["cases", "detected", "unsafe"]}


def p3(tmp):
    results = {}
    # Held descriptors are the exact state of a writer descheduled after open.
    for mode in ["per_day", "rename_unlink"]:
        lost = 0
        for i in range(100):
            directory = tmp / f"{mode}-{i}"
            directory.mkdir(mode=0o700)
            old = directory / "day0"
            fd = os.open(old, os.O_CREAT | os.O_APPEND | os.O_WRONLY, 0o600)
            os.write(fd, b"early\n")
            if mode == "rename_unlink":
                rotated = directory / "rotated"
                old.rename(rotated)
                # Consumer reached EOF before late writer resumes, then unlinks.
                assert rotated.read_bytes() == b"early\n"
                rotated.unlink()
                old.write_bytes(b"new\n")
            else:
                (directory / "day1").write_bytes(b"new\n")
            assert os.write(fd, b"late\n") == 5
            os.close(fd)
            reachable = b"".join(p.read_bytes() for p in directory.iterdir())
            lost += b"late\n" not in reachable
        results[mode] = {"late_writes": 100, "successful_but_unreachable": lost}
    assert results["per_day"]["successful_but_unreachable"] == 0
    assert results["rename_unlink"]["successful_but_unreachable"] == 100
    return results


def memory_child(cap, uploads, pattern):
    rng = random.Random(4)
    # Exactly two compression workers; 'uploads' are retained encoded buffers,
    # not network operations or additional worker threads.
    def build(_):
        raw = rng.randbytes(cap) if pattern == "random" else b"x" * cap
        sha(raw)
        encoded = zstd.compress(raw, level=3)
        sha(encoded)
        return encoded
    with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
        encoded = list(pool.map(build, range(uploads)))
        # Force both raw working buffers to remain resident with encoded backlog.
        raw = [bytearray(b"y" * cap), bytearray(b"z" * cap)]
        total = sum(map(len, encoded)) + sum(map(len, raw))
        rss = resource.getrusage(resource.RUSAGE_SELF).ru_maxrss
    return {"rss_peak_bytes": rss if sys.platform == "darwin" else rss * 1024,
            "retained_payload_bytes": total}


def p4(tmp, exe):
    hooks = {}
    for case in ["success", "missing_parent"]:
        path = tmp / "hint" if case == "success" else tmp / "missing" / "hint"
        samples = []
        wall = []
        for _ in range(300):
            t = time.perf_counter_ns()
            r = subprocess.run([str(exe), "--hint", str(path)], check=True, capture_output=True, text=True)
            wall.append((time.perf_counter_ns()-t)/1e6)
            ns, ok = map(int, r.stdout.split())
            assert ok == (case == "success")
            samples.append(ns/1e6)
        samples.sort()
        wall.sort()
        hooks[case] = {"in_process_p50_ms": statistics.median(samples),
                       "in_process_p99_ms": samples[296], "spawn_p99_ms": wall[296], "n": 300}
    memory = []
    for cap in [4, 8]:
        for uploads in [4, 8]:
            for pattern in ["random", "compressible"]:
                r = subprocess.run([sys.executable, "-I", str(HERE / "run.py"), "--memory-child",
                                    str(cap*1024*1024), str(uploads), pattern], check=True,
                                   capture_output=True, text=True)
                memory.append({"piece_MiB": cap, "encoded_buffers": uploads, "pattern": pattern,
                               **json.loads(r.stdout)})
    return {"synthetic_C_hook": hooks, "synthetic_Python_memory": memory}


def p6():
    cases = []
    # 60s tick after 10s active followed by a wall-only gap or clock adjustment.
    for name, wall, mono, continuous in [("awake", 60, 60, 60), ("sleep_600", 660, 60, 660),
                             ("forward_step", 660, 60, 60), ("backward_step", -540, 60, 60),
                             ("threshold_120", 180, 60, 180), ("threshold_121", 181, 60, 181)]:
        wake = wall - mono > 120
        # Deadline at monotonic 300 remains 240s away regardless of wall time.
        cases.append({"case": name, "wall_elapsed_s": wall, "monotonic_elapsed_s": mono,
                      "reconcile": wake, "deadline_remaining_s": 300-mono,
                      "continuous_elapsed_s": continuous,
                      "continuous_deadline_remaining_s": max(0, 300-continuous),
                      "jitter_bounds_s": [0, 30] if wake else None})
    assert [c["reconcile"] for c in cases] == [False, True, True, False, False, True]
    return {"simulated": cases, "natural_sleep": "pending; host never suspended"}


def p7(tmp, lib):
    native = ctypes.CDLL(str(lib))
    native.crc32c.argtypes = [ctypes.c_void_p, ctypes.c_size_t]
    native.crc32c.restype = ctypes.c_uint32
    assert native.crc32c(b"123456789", 9) == 0xe3069283
    body = random.Random(7).randbytes(20_000_000)
    output = {}
    for name, digest in [("sha256", sha), ("crc32c_scalar_table", lambda b: native.crc32c(b, len(b)))]:
        expected = digest(body)
        header = {"schema": "synthetic.index/1", "writer_version": 1,
                  "body_len": len(body), "digest": expected}
        wire = json.dumps(header).encode() + b"\n" + body
        def valid(w):
            try:
                h, b = w.split(b"\n", 1)
                h = json.loads(h)
                return (h["schema"] == "synthetic.index/1" and h["writer_version"] == 1
                        and h["body_len"] == len(b) and h["digest"] == digest(b))
            except (ValueError, KeyError):
                return False
        assert valid(wire)
        bad = [wire[:n] for n in [0, 1, 30, len(wire)//2, len(wire)-1]]
        changed = bytearray(wire)
        changed[-100] ^= 1
        bad += [bytes(changed), wire+b"x", wire.replace(b'"writer_version": 1', b'"writer_version": 2', 1)]
        detected = sum(not valid(w) for w in bad)
        assert detected == len(bad)
        path = tmp / name
        path.write_bytes(wire)
        output[name] = {"checksum_only": timed(lambda: digest(body)),
                        "read_validate": timed(lambda: valid(path.read_bytes())),
                        "injected_invalid": len(bad), "rejected": detected}
    return {"body_bytes": len(body), "results": output,
            "durability": "not tested: no power loss, F_FULLFSYNC or directory fsync acceptance"}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--memory-child", nargs=3)
    args = parser.parse_args()
    if args.memory_child:
        cap, uploads, pattern = args.memory_child
        print(json.dumps(memory_child(int(cap), int(uploads), pattern)))
        return
    with tempfile.TemporaryDirectory(prefix="memory-prereq-") as directory:
        tmp = Path(directory)
        os.chmod(tmp, 0o700)
        exe, lib = tmp / "native", tmp / "native.dylib"
        for command in [["cc", "-O2", "-Wall", "-Wextra", "-Werror", str(HERE/"native_probe.c"), "-o", str(exe)],
                        ["cc", "-O2", "-shared", "-fPIC", str(HERE/"native_probe.c"), "-o", str(lib)]]:
            subprocess.run(command, check=True)
        output = {"platform": platform.platform(), "python": sys.version,
                  "source_sha256": {p.name: sha(p.read_bytes()) for p in [HERE/"run.py", HERE/"native_probe.c"]},
                  "P1": p1(tmp), "P3": p3(tmp), "P4": p4(tmp, exe), "P6": p6(), "P7": p7(tmp, lib)}
        print(json.dumps(output, indent=2, sort_keys=True))


if __name__ == "__main__":
    main()
