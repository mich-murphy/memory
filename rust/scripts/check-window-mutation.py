#!/usr/bin/env python3
"""Kill an actual production window-check mutant in an isolated workspace copy.

Run inside the pinned Nix shell. Never edits the source checkout or enables a
production bypass. A compile failure is an invalid experiment, not a killed mutant.
"""
from pathlib import Path
import os
import shutil
import subprocess
import tempfile
import sys

ROOT = Path(__file__).resolve().parents[1]
PREDICATE = "if windows.head != state.head || windows.tail != state.tail {"
MUTANT = "if { let _ = windows; false } { // scratch-only disabled window comparison"


def run(workspace: Path, env: dict[str, str], label: str) -> subprocess.CompletedProcess[str]:
    command = ["cargo", "test", "--locked", "-p", "memory-core", "--test", "core_mechanics",
               "property1_independent_capture_and_eligible_audit", "--", "--nocapture"]
    print(f"{label}: {' '.join(command)}", flush=True)
    result = subprocess.run(command, cwd=workspace, env=env, text=True,
                            stdout=subprocess.PIPE, stderr=subprocess.STDOUT, check=False)
    print(result.stdout, end="", flush=True)
    print(f"{label}: exit={result.returncode}", flush=True)
    return result


def main() -> int:
    env = os.environ.copy()
    cases = int(env.get("HEGEL_TEST_CASES", "2000"))
    cross_cases = int(env.get("PROPTEST_CASES", str(cases)))
    if min(cases, cross_cases) < 500:
        print("mutation gate requires at least 500 baseline cases per engine (2000 locally)", file=sys.stderr)
        return 2
    env.update(CARGO_BUILD_JOBS="2", RUST_TEST_THREADS="2", HEGEL_TEST_CASES=str(cases),
               PROPTEST_CASES=str(cross_cases), HEGEL_SEED=env.get("HEGEL_SEED", "730104"),
               PROPTEST_RNG_SEED=env.get("PROPTEST_RNG_SEED", "730104"))
    print(f"Hegel seed={env['HEGEL_SEED']} cases={cases}; "
          f"proptest seed={env['PROPTEST_RNG_SEED']} cases={cross_cases}", flush=True)
    source = ROOT / "crates/memory-core/src/planner.rs"
    original = source.read_bytes()
    snapshot_paths = [source, ROOT / "crates/memory-core/tests/core_mechanics.rs", ROOT / "Cargo.lock"]
    snapshots = {path: path.read_bytes() for path in snapshot_paths}
    import hashlib
    for path, content in snapshots.items():
        print(f"source sha256 {path.relative_to(ROOT)} {hashlib.sha256(content).hexdigest()}", flush=True)
    if original.decode().count(PREDICATE) != 1:
        print("unique mutation point drifted; experiment refused", file=sys.stderr)
        return 2
    with tempfile.TemporaryDirectory(prefix="memory-window-mutation-") as directory:
        workspace = Path(directory) / "rust"
        shutil.copytree(ROOT, workspace,
                        ignore=shutil.ignore_patterns("target", "artifacts", ".DS_Store"))
        # Both baseline and mutant use one private target; no concurrent source-target writes.
        # gates.sh may pass MEMORY_MUTATION_TARGET, a temp target shared only by the mutation
        # scripts it runs one after another, so registry dependencies compile once.
        env["CARGO_TARGET_DIR"] = env.get("MEMORY_MUTATION_TARGET") or str(Path(directory) / "target")
        normal = run(workspace, env, "baseline")
        if normal.returncode != 0:
            print("baseline failed; mutation result not evaluated", file=sys.stderr)
            return 1
        copied = workspace / "crates/memory-core/src/planner.rs"
        copied.write_text(original.decode().replace(PREDICATE, MUTANT, 1))
        mutant = run(workspace, env, "disabled-window mutant")
        output = mutant.stdout
        assertion_failed = ("operation=" in output and "seed=" in output
                            and "panicked at" in output and "test result: FAILED" in output)
        compiled = "could not compile" not in output and "error[E" not in output
        if mutant.returncode != 101 or not compiled or not assertion_failed:
            print("mutant NOT killed by the independent runtime safety assertion", file=sys.stderr)
            return 1
        if any(path.read_bytes() != content for path, content in snapshots.items()):
            print("source/oracle/lock changed concurrently; evidence no longer source-bound", file=sys.stderr)
            return 2
        print("PASS: unchanged baseline passed; actual window-check mutant compiled and "
              "failed the independent property1 runtime byte oracle", flush=True)
    return 0


if __name__ == "__main__":
    sys.exit(main())
