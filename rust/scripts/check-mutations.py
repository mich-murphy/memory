#!/usr/bin/env python3
"""Kill realistic production mutants of three invariants in one isolated workspace copy.

Run inside the pinned Nix shell. Never edits the source checkout. Mutants:
  if-match         the conditional current.json write goes blind (latest ETag, not the one we hold)
  advance-on-fail  the local index advances even when the commit did not land
  privacy-deny     the credential denylist is skipped (Deny becomes Allow)

One private copy and one private target dir serve the baseline and every mutant in turn;
the mutated file is restored between mutants. A predicate that does not occur exactly once,
a baseline failure or a mutant that does not compile is an invalid experiment (non-zero).
A mutant is killed only by a real test failure (exit 101 with a panic) of a listed test.
"""
from pathlib import Path
import os
import shutil
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[1]

MEMORY = ["cargo", "test", "--locked", "-p", "memory"]
CORE = ["cargo", "test", "--locked", "-p", "memory-core"]


def t(base: list[str], binary: str, *filters: str) -> list[str]:
    return [*base, "--test", binary, *filters, "--", "--test-threads=2", "--nocapture"]


MUTANTS = [
    {
        "name": "if-match",
        "file": "crates/memory/src/commit.rs",
        "predicate": "Some(etag) => store.put_if_match(&key, etag, Published::metadata(pointer.clone())),",
        "mutant": ("Some(etag) => { let latest = store.get(&key).map_or_else(|_| etag.clone(), |s| s.etag); "
                   "store.put_if_match(&key, &latest, Published::metadata(pointer.clone())) }"),
        "tests": [t(MEMORY, "mutation_daemon", "stale_if_match")],
    },
    {
        "name": "advance-on-fail",
        "file": "crates/memory/src/daemon.rs",
        "predicate": "        if let Ok(landed) = &result {\n            // Remote is authoritative",
        "mutant": "        self.apply(got.updates.clone());\n        if let Ok(landed) = &result {\n            // Remote is authoritative",
        "tests": [t(MEMORY, "mutation_daemon", "failed_commit_does_not_advance")],
    },
    {
        "name": "privacy-deny",
        "file": "crates/memory-core/src/privacy/mod.rs",
        "predicate": "        if is_credential(path) {\n            return Admission::Deny(Reason::Credential);",
        "mutant": "        if false && is_credential(path) {\n            return Admission::Deny(Reason::Credential);",
        "tests": [
            t(CORE, "privacy_property"),
            t(CORE, "privacy_port", "denied_identities"),
        ],
    },
]


def run(workspace: Path, env: dict[str, str], label: str, command: list[str]) -> subprocess.CompletedProcess[str]:
    print(f"{label}: {' '.join(command)}", flush=True)
    result = subprocess.run(command, cwd=workspace, env=env, text=True,
                            stdout=subprocess.PIPE, stderr=subprocess.STDOUT, check=False)
    tail = "\n".join(result.stdout.splitlines()[-25:])
    print(tail, flush=True)
    print(f"{label}: exit={result.returncode}", flush=True)
    return result


def main() -> int:
    env = os.environ.copy()
    cases = int(env.get("HEGEL_TEST_CASES", "2000"))
    cross = int(env.get("PROPTEST_CASES", str(cases)))
    if min(cases, cross) < 500:
        print("mutation gate requires at least 500 cases per engine (2000 locally)", file=sys.stderr)
        return 2
    env.update(CARGO_BUILD_JOBS="2", RUST_TEST_THREADS="2", HEGEL_TEST_CASES=str(cases),
               PROPTEST_CASES=str(cross), HEGEL_SEED=env.get("HEGEL_SEED", "730104"),
               PROPTEST_RNG_SEED=env.get("PROPTEST_RNG_SEED", "730104"))
    print(f"Hegel seed={env['HEGEL_SEED']} cases={cases}; proptest seed={env['PROPTEST_RNG_SEED']} cases={cross}",
          flush=True)
    originals = {}
    for m in MUTANTS:
        text = (ROOT / m["file"]).read_text()
        if text.count(m["predicate"]) != 1:
            print(f"FAIL {m['name']}: predicate not found exactly once in {m['file']}; refused", file=sys.stderr)
            return 2
        originals[m["file"]] = text
    results: list[tuple[str, str]] = []
    with tempfile.TemporaryDirectory(prefix="memory-mutations-") as directory:
        workspace = Path(directory) / "rust"
        shutil.copytree(ROOT, workspace, ignore=shutil.ignore_patterns("target", "artifacts", ".DS_Store"))
        env["CARGO_TARGET_DIR"] = str(Path(directory) / "target")
        seen: list[list[str]] = []
        for m in MUTANTS:
            for command in m["tests"]:
                if command not in seen:
                    seen.append(command)
        for command in seen:
            if run(workspace, env, "baseline", command).returncode != 0:
                print("baseline failed; mutation results not evaluated", file=sys.stderr)
                return 1
        for m in MUTANTS:
            target = workspace / m["file"]
            target.write_text(originals[m["file"]].replace(m["predicate"], m["mutant"], 1))
            try:
                verdict = "survived: no listed test failed"
                for command in m["tests"]:
                    out = run(workspace, env, f"mutant {m['name']}", command)
                    if "could not compile" in out.stdout or "error[E" in out.stdout:
                        verdict = "invalid: mutant did not compile"
                        break
                    if out.returncode == 101 and "panicked at" in out.stdout and "test result: FAILED" in out.stdout:
                        verdict = f"killed by {command[command.index('--test') + 1]}"
                        break
                results.append((m["name"], verdict))
            finally:
                target.write_text(originals[m["file"]])
    ok = True
    for name, verdict in results:
        good = verdict.startswith("killed")
        ok &= good
        print(f"{'PASS' if good else 'FAIL'} {name}: {verdict}", flush=True)
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
