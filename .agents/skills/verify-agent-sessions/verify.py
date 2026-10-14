#!/usr/bin/env python3
"""Owned, offline CLI verification. See SKILL.md; no third-party Python packages."""
import argparse
import contextlib
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import signal
import sqlite3
import stat
import subprocess
import sys
import tempfile
import time
import uuid

REPO = Path(__file__).resolve().parents[3]
FEATURES = ("setup", "capture", "privacy", "hooks", "regression", "live")
MANAGED = (Path("/etc/claude-code"), Path("/etc/codex"),
           Path("/Library/Application Support/ClaudeCode"))


class Blocked(RuntimeError):
    pass


def require(ok, message):
    if not ok:
        raise RuntimeError(message)


def sha(body):
    return hashlib.sha256(body).hexdigest()


def write(path, body):
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    path.write_bytes(body.encode() if isinstance(body, str) else body)
    path.chmod(0o600)


def save(path, data):
    write(path, json.dumps(data, indent=2, sort_keys=True) + "\n")


def private_tree(root):
    for path in [root, *root.rglob("*")]:
        require(not path.is_symlink(), f"refusing symlink: {path}")
        require(path.stat().st_uid == os.getuid(), f"not owned: {path}")
        path.chmod(0o700 if path.is_dir() or os.access(path, os.X_OK) else 0o600)


def owned(path, directory=False):
    require(path.is_absolute() and path.resolve() == path, f"not canonical: {path}")
    st = path.lstat()
    require(st.st_uid == os.getuid() and not stat.S_ISLNK(st.st_mode), f"not owned: {path}")
    require(stat.S_ISDIR(st.st_mode) if directory else stat.S_ISREG(st.st_mode), f"wrong type: {path}")
    require(st.st_mode & 0o077 == 0, f"not private: {path}")


def inventory(root):
    result = {}
    for path in sorted(root.rglob("*")):
        require(not path.is_symlink(), f"unexpected symlink: {path}")
        if path.is_file():
            result[str(path.relative_to(root))] = {"size": path.stat().st_size,
                                                       "sha256": sha(path.read_bytes())}
    return result


LINK_MANIFEST = ".verification-symlinks.json"
IMPORT_HASHES = ".verification-hashes.json"


def import_evidence(source, destination):
    """Copy owned native evidence without following links, even if entries change type."""
    owned(source, True)
    destination.mkdir(parents=True, mode=0o700, exist_ok=False)
    links = {}
    complete = False
    descriptor = None

    def copy_directory(fd, output, relative):
        require(os.fstat(fd).st_uid == os.getuid(), "foreign evidence directory owner")
        for name in sorted(os.listdir(fd)):
            if not relative.parts:
                require(name not in (LINK_MANIFEST, IMPORT_HASHES), "reserved evidence manifest name collision")
            identity = os.stat(name, dir_fd=fd, follow_symlinks=False)
            require(identity.st_uid == os.getuid(), "foreign evidence entry owner")
            path = relative / name
            if stat.S_ISLNK(identity.st_mode):
                links[str(path)] = os.readlink(name, dir_fd=fd)  # Literal metadata; target never opened.
            elif stat.S_ISDIR(identity.st_mode):
                child = os.open(name, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=fd)
                try:
                    (output / name).mkdir(mode=0o700)
                    copy_directory(child, output / name, path)
                finally:
                    os.close(child)
            else:
                require(stat.S_ISREG(identity.st_mode), "nonregular native evidence entry")
                child = os.open(name, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=fd)
                with os.fdopen(child, "rb") as reader:
                    actual = os.fstat(reader.fileno())
                    require(stat.S_ISREG(actual.st_mode) and actual.st_uid == os.getuid(), "native evidence changed type/owner")
                    with (output / name).open("xb") as writer:
                        shutil.copyfileobj(reader, writer, length=1024 * 1024)
                    (output / name).chmod(0o700 if actual.st_mode & 0o111 else 0o600)

    try:
        descriptor = os.open(source, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
        copy_directory(descriptor, destination, Path())
        complete = True
    finally:
        if descriptor is not None:
            os.close(descriptor)
        # Partial imports remain safe, link-free evidence, never a claim of completed import.
        save(destination / LINK_MANIFEST, {"schema": 1, "source": str(source),
                                           "complete": complete, "symlinks": links})
        private_tree(destination)
    save(destination / IMPORT_HASHES, inventory(destination))
    return links


def evidence_import_fixture(run):
    source = run.root / "work/import-fixture/source"
    outside = run.root / "work/import-fixture/outside"
    source.mkdir(parents=True, mode=0o700)
    outside.mkdir(mode=0o700)
    sentinel = outside / "sentinel.txt"
    content = b"synthetic outside link target must never be imported\n"
    write(sentinel, content)
    write(source / "ordinary.txt", "ordinary owned evidence\n")
    write(source / "nested/ordinary.txt", "nested owned evidence\n")
    file_link = source / "latest"
    directory_link = source / "outside-directory"
    file_link.symlink_to(sentinel)
    directory_link.symlink_to(outside, target_is_directory=True)
    before = sentinel.stat()
    destination = run.proof / "negative-fixtures/native-symlink-import"
    try:
        links = import_evidence(source, destination)
        require(links == {"latest": str(sentinel), "outside-directory": str(outside)}, "literal native link metadata differs")
        require(not (destination / "latest").exists() and not (destination / "outside-directory").exists(), "native link target copied")
        require(all(not p.is_symlink() for p in destination.rglob("*")), "copied native evidence retained links")
        require(all(content not in p.read_bytes() for p in destination.rglob("*") if p.is_file()), "outside sentinel bytes imported")
        after = sentinel.stat()
        require((before.st_ino, before.st_size, before.st_mtime_ns, before.st_atime_ns) ==
                (after.st_ino, after.st_size, after.st_mtime_ns, after.st_atime_ns), "outside target read or modified by import")
        require(sentinel.read_bytes() == content, "outside sentinel changed")
        # A native file using our reserved metadata name must fail, not be overwritten.
        write(source / LINK_MANIFEST, "native collision fixture\n")
        rejected = run.proof / "negative-fixtures/native-manifest-collision"
        try:
            import_evidence(source, rejected)
        except RuntimeError as error:
            require("manifest name collision" in str(error), "collision rejected for wrong reason")
        else:
            raise RuntimeError("native evidence manifest collision accepted")
        require(not json.loads((rejected / LINK_MANIFEST).read_text())["complete"], "partial collision import marked complete")
        require((source / LINK_MANIFEST).read_text() == "native collision fixture\n", "native collision source modified")
        private_tree(rejected)
        run.note("live", "negative native evidence links recorded without target reads/copies; manifest collision rejected",
                 path=str(destination), symlinks=links)
    finally:
        file_link.unlink()
        directory_link.unlink()  # Only our owned fixture links; native harness originals are untouched.


def source_digest():
    files = [REPO / "go.mod", REPO / "go.sum"]
    for directory in ("cmd", "internal"):
        files.extend((REPO / directory).rglob("*.go"))
    return sha(json.dumps({str(p.relative_to(REPO)): sha(p.read_bytes())
                           for p in sorted(files)}, sort_keys=True).encode())


class Run:
    def __init__(self, path):
        self.proof = Path(path)
        owned(self.proof, True)
        owned(self.proof / "run.json")
        self.meta = json.loads((self.proof / "run.json").read_text())
        require(self.meta["repo"] == str(REPO), "run belongs to another checkout")
        self.root = Path(self.meta["scratch"])
        require(self.root.is_absolute() and self.root.resolve() == self.root
                and self.root.parent == Path("/tmp").resolve()
                and self.root.name.startswith("agent-sessions-verify-"),
                "scratch must be a canonical direct child of system /tmp")
        require(self.proof != self.root and not self.proof.is_relative_to(self.root)
                and not self.root.is_relative_to(self.proof), "evidence/scratch overlap")
        if self.root.exists():
            owned(self.root, True)
            owned(self.root / ".owner.json")
            marker = json.loads((self.root / ".owner.json").read_text())
            require(marker == {"token": self.meta["token"], "proof": str(self.proof)}, "ownership mismatch")
        else:
            require(self.meta.get("cleaned"), "scratch missing without cleanup record")
        self.binary = self.root / "bin/agent-sessions"

    def persist(self):
        save(self.proof / "run.json", self.meta)

    def env(self):
        root = self.root
        return {
            "PATH": str(root / "bin") + ":" + str(Path(self.meta["go"]).parent)
                    + ":/usr/bin:/bin:/usr/sbin:/sbin:/opt/homebrew/bin",
            "HOME": str(root / "home"), "SHELL": "/bin/sh", "LANG": "C", "TERM": "dumb",
            "TMPDIR": str(root / "tmp"), "XDG_CONFIG_HOME": str(root / "home/.config"),
            "XDG_CACHE_HOME": str(root / "home/.cache"), "XDG_STATE_HOME": str(root / "home/.local/state"),
            "AGENT_SESSIONS_CONFIG": str(root / "config/config.env"),
            "AGENT_SESSIONS_STATE_DIR": str(root / "state"),
            "AGENT_SESSIONS_LOCAL_STORE": str(root / "bucket"),
            "CLAUDE_CONFIG_DIR": str(root / "home/.claude"),
            "CODEX_HOME": str(root / "home/.codex"),
            "PI_CODING_AGENT_DIR": str(root / "home/.pi/agent"),
            "GOENV": "off", "GOTOOLCHAIN": "local", "GOWORK": "off",
            "GOFLAGS": "-mod=readonly", "GOPROXY": "off", "GOSUMDB": "off",
            "GOCACHE": str(root / "go-cache"), "GOPATH": str(root / "go"),
            "GOMODCACHE": self.meta["module_cache"], "CGO_ENABLED": "0",
            "GIT_CONFIG_GLOBAL": "/dev/null", "GIT_CONFIG_NOSYSTEM": "1",
        }

    def command(self, feature, argv, expected=0, extra=None, stdin="", timeout=180, cwd=None):
        env = self.env()
        env.update(extra or {})
        number = len(self.meta["actions"]) + 1
        base = f"actions/{number:03d}-{feature}"
        action = {"feature": feature, "argv": list(map(str, argv)), "cwd": str(cwd or self.root / "work"),
                  "stdin": stdin, "environment": env, "started": time.time(),
                  "stdout": base + ".stdout", "stderr": base + ".stderr", "status": "unverified"}
        self.meta["actions"].append(action)
        self.persist()
        # Every foreground command owns a new process group, including Go's test children.
        # Detached collector hooks deliberately form another session; quiescent checks their
        # exact owned paths too, and refuses cleanup rather than killing by executable name.
        with subprocess.Popen(action["argv"], cwd=action["cwd"], env=env, stdin=subprocess.PIPE,
                              stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True) as proc:
            action.update(pid=proc.pid, pgid=proc.pid)
            self.persist()
            try:
                stdout, stderr = proc.communicate(stdin.encode(), timeout=timeout)
            except subprocess.TimeoutExpired as error:
                action.update(status="blocked", timeout=True,
                              reason="timeout; owned foreground group terminated; cleanup requires quiescence")
                # The PGID belongs to this newly created session for this bounded command.
                # Terminate its children even if the immediate Go/CLI leader just exited.
                try:
                    os.killpg(action["pgid"], signal.SIGTERM)
                except ProcessLookupError:
                    pass  # Entire owned process group already exited.
                try:
                    stdout, stderr = proc.communicate(timeout=5)
                except subprocess.TimeoutExpired:
                    try:
                        os.killpg(action["pgid"], signal.SIGKILL)
                    except ProcessLookupError:
                        pass  # Entire owned process group exited after TERM.
                    try:
                        stdout, stderr = proc.communicate(timeout=5)
                    except subprocess.TimeoutExpired:
                        # Do not wait indefinitely on pipe-owning detached descendants.
                        stdout, stderr = error.stdout or b"", error.stderr or b""
                        proc.stdout.close()
                        proc.stderr.close()
                        proc.wait(timeout=5)
                write(self.proof / action["stdout"], stdout)
                write(self.proof / action["stderr"], stderr)
                action.update(exit=proc.returncode, finished=time.time())
                self.persist()
                raise Blocked(action["reason"]) from error
            write(self.proof / action["stdout"], stdout)
            write(self.proof / action["stderr"], stderr)
            action.update(exit=proc.returncode, finished=time.time(),
                          status="passed" if proc.returncode == expected else "failed", expected_exit=expected)
            self.persist()
            require(proc.returncode == expected,
                    f"{argv}: exit {proc.returncode}, expected {expected}; see {self.proof / base}")
            return stdout.decode(errors="replace"), stderr.decode(errors="replace")

    def cli(self, feature, *args, **kwargs):
        return self.command(feature, [self.binary, *args], **kwargs)

    def note(self, feature, action, **state):
        self.meta["observations"].append({"feature": feature, "action": action, "time": time.time(), **state})
        self.persist()

    def snapshot(self, name):
        dest = self.proof / "states" / f"{len(self.meta['observations']):03d}-{name}"
        dest.mkdir(parents=True, mode=0o700)
        for directory in ("home", "config", "state", "bucket", "work"):
            if (self.root / directory).exists():
                shutil.copytree(self.root / directory, dest / directory, symlinks=True)
        private_tree(dest)
        save(dest / "hashes.json", inventory(dest))
        self.note(name, "side-effect snapshot", path=str(dest))

    def records(self, kind="file"):
        records = []
        streams = {}
        base = self.root / "bucket/v3/machines" / self.meta["machine_id"] / "records" / kind
        for path in sorted(base.glob("*/commits/*.json")):
            body = path.read_bytes()
            record_id = sha(body)
            record = json.loads(body)
            require(record["schema"] == 3 and record["machine_id"] == self.meta["machine_id"]
                    and record["kind"] == kind, "record identity")
            stream = sha((record["agent"] + "\0" + record["key"]).encode())
            require(path.parent.parent.name == stream, "physical stream identity differs")
            require(path.name == f"{record['sequence']:020d}-{record_id}.json", f"record name hash/sequence: {path}")
            if kind == "file":
                generation = record["file"]
                identity = generation["identity"]
                require(generation["schema"] == 3 and identity["agent"] == record["agent"]
                        and identity["key"] == record["key"] and identity["machine_id"] == record["machine_id"]
                        and generation["sequence"] == record["sequence"]
                        and generation.get("parent", "") == record.get("parent", ""), "typed file identity differs")
            streams.setdefault(stream, []).append((record, record_id))
            records.append(record)
        for stream, chain in streams.items():
            parent = ""
            for sequence, (record, record_id) in enumerate(sorted(chain, key=lambda item: item[0]["sequence"]), 1):
                require(record["sequence"] == sequence and record.get("parent", "") == parent,
                        f"fork/orphan/noncontiguous committed chain: {kind}/{stream}")
                parent = record_id
        return records

    def generations(self, agent, key):
        # Sorting is permitted only after validating the unique contiguous physical chain.
        return sorted([p["file"] for p in self.records() if p["agent"] == agent and p["key"] == key],
                      key=lambda g: g["sequence"])

    def decoded(self, generation):
        try:
            from compression import zstd
        except ImportError as error:
            raise Blocked("Python 3.14+ with stdlib compression.zstd required") from error
        result = bytearray()
        for piece in generation["pieces"]:
            key = Path(piece["object"])
            require(not key.is_absolute() and ".." not in key.parts, "unsafe piece key")
            encoded = (self.root / "bucket" / key).read_bytes()
            require(len(encoded) == piece["encoded_size"] and sha(encoded) == piece["encoded_sha256"], "encoded piece integrity")
            decoded = zstd.decompress(encoded)
            require(piece["start"] == len(result) and len(decoded) == piece["end"] - piece["start"]
                    and sha(decoded) == piece["sha256"], "decoded piece integrity")
            result.extend(decoded)
        require(len(result) == generation["size"] and sha(result) == generation["sha256"], "whole content integrity")
        return bytes(result)

    def capture(self, feature, agent, key, body):
        generations = self.generations(agent, key)
        require(bool(generations), f"missing committed {agent}:{key}")
        require(self.decoded(generations[-1]) == body, f"committed bytes differ: {key}")
        self.note(feature, "read committed bytes", agent=agent, key=key, sequence=generations[-1]["sequence"],
                  kind=generations[-1]["kind"], size=len(body), sha256=sha(body))
        return generations[-1]

    def quiescent(self, timeout=30):
        # Check complete owned scratch footprints, retained live scratch profiles, and all
        # command groups, not only the primary collector. Never kill anything in this check.
        roots = [str(self.root) + "/"]
        groups = {action["pgid"] for action in self.meta["actions"] if "pgid" in action}
        for action in self.meta["actions"]:
            output = self.proof / action["stdout"]
            if action["feature"] == "live" and output.is_file():
                roots.extend(path + "/" for path in re.findall(r"scratch evidence: (/\S+)", output.read_text(errors="replace")))
        deadline = time.monotonic() + timeout
        while True:
            proc = subprocess.run(["/bin/ps", "-axo", "pid=,pgid=,stat=,command="], env=self.env(),
                                  stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False)
            require(proc.returncode == 0, "cannot inspect owned processes; cleanup refused")
            active = []
            for line in proc.stdout.decode().splitlines():
                fields = line.split(None, 3)
                if len(fields) == 4 and not fields[2].startswith("Z") and (int(fields[1]) in groups or any(root in fields[3] for root in roots)):
                    active.append(line)
            if not active:
                return
            if time.monotonic() >= deadline:
                raise Blocked(f"owned group/scratch processes still active (not killed): {active}")
            time.sleep(0.1)


def safe_sources():
    # Real collector snapshots include managed and ancestor settings even under a fake HOME.
    # Refuse rather than read a host's managed config. Live mode supplies its own deny sandbox.
    for directory in MANAGED:
        if directory.exists():
            raise Blocked(f"local mode requires absent host managed directory: {directory}")
    temporary = Path("/tmp").resolve()
    for directory in (temporary, *temporary.parents):
        for name in ("CLAUDE.md", "CLAUDE.local.md", "AGENTS.md", "AGENTS.override.md"):
            if (directory / name).exists():
                raise Blocked(f"host ancestor instruction exists: {directory / name}")


def doctor(run):
    require(not run.meta.get("cleaned"), "instance already cleaned; launch a new run")
    safe_sources()
    require(source_digest() == run.meta["source_digest"], "source drift: launch a new build/run")
    for name in ("home", "config", "bucket", "state", "work", "tmp", "bin"):
        owned(run.root / name, True)
    owned(run.binary)
    for name in ("config/config.env", "config/machine-id", "bucket/v3/store.json", "state/state.sqlite3"):
        owned(run.root / name)
    require(sha(run.binary.read_bytes()) == run.meta["binary_sha256"], "binary identity changed")
    require((run.root / "config/config.env").read_text() == "# Offline verification; local destination is environment-only.\n", "config changed")
    require((run.root / "config/machine-id").read_text().strip() == run.meta["machine_id"], "machine identity changed")
    identity = json.loads((run.root / "bucket/v3/store.json").read_text())
    require(identity == run.meta["store_identity"], "bucket identity changed")
    db_path = run.root / "state/state.sqlite3"
    with contextlib.closing(sqlite3.connect(db_path.as_uri() + "?mode=ro&immutable=1", uri=True)) as db:
        binding = db.execute("SELECT schema,destination,store_id,machine_id FROM binding").fetchone()
    require(binding == (3, "local:" + str(run.root / "bucket") + ":v3", identity["id"], run.meta["machine_id"]), "state binding changed")
    before = {name: inventory(run.root / name) for name in ("home", "config", "state", "bucket", "work")}
    # These two CLI surfaces are read-only; unlike production doctor (SQLite init / R2 probe).
    for args, expected in ((["--version"], run.meta["version"]), (["machine-id"], run.meta["machine_id"])):
        proc = subprocess.run([str(run.binary), *args], env=run.env(), cwd=run.root / "work",
                              stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False, timeout=15)
        require(proc.returncode == 0 and not proc.stderr and proc.stdout.decode().strip() == expected,
                f"read-only CLI identity check failed: {args}")
    require(before == {name: inventory(run.root / name) for name in before}, "doctor mutated runtime files")
    print(json.dumps({"status": "passed", "read_only": True, "scratch": str(run.root),
                      "binary_sha256": run.meta["binary_sha256"], "machine_id": run.meta["machine_id"],
                      "store_identity": identity, "backend": "local stand-in, NOT R2"}, indent=2))


def launch(args):
    safe_sources()
    try:
        from compression import zstd  # noqa: F401 -- dependency preflight, no external codec
    except ImportError as error:
        raise Blocked("Python 3.14+ with compression.zstd required") from error
    module_cache = Path(args.module_cache or Path.home() / "go/pkg/mod").resolve()
    require(module_cache.is_dir(), "module cache missing; populate go.mod dependencies separately or pass --module-cache")
    go = args.go or shutil.which("go")
    if not go:
        raise Blocked("Go toolchain in go.mod is required on PATH")
    # A PATH Go can be an older launcher whose auto toolchain was already cached.
    # Select the exact cached toolchain offline instead of enabling auto-download.
    version = re.search(r"^toolchain (go\S+)$", (REPO / "go.mod").read_text(), re.M).group(1)
    import platform
    arch = {"arm64": "arm64", "aarch64": "arm64", "x86_64": "amd64"}.get(platform.machine())
    cached = module_cache / ("golang.org/toolchain@v0.0.1-" + version + "." + platform.system().lower() + "-" + str(arch)) / "bin/go"
    if not args.go and cached.is_file():
        go = str(cached)
    if args.artifacts:
        proof = Path(args.artifacts)
        require(proof.is_absolute() and proof.resolve() == proof, "--artifacts must be canonical absolute NEW directory")
        proof.mkdir(mode=0o700)
    else:
        proof = Path(tempfile.mkdtemp(prefix="agent-sessions-proof-", dir="/tmp")).resolve()
    root = Path(tempfile.mkdtemp(prefix="agent-sessions-verify-", dir="/tmp")).resolve()
    token = uuid.uuid4().hex
    meta = {"schema": 1, "repo": str(REPO), "scratch": str(root), "token": token,
            "go": str(Path(go).resolve()), "module_cache": str(module_cache), "source_digest": source_digest(),
            "version": "verify-" + token[:12], "actions": [], "observations": [],
            "features": {name: {"status": "unverified", "reason": "not attempted"} for name in FEATURES},
            "limits": ["local store only: R2 credentials, network durability and lifecycle policy unverified",
                       "synthetic native transcript fixtures; real agents only in opt-in live mode",
                       "no public restore command; API restore checks are supplemental",
                       "release download/upgrade and real Codex approval remain unverified",
                       "Linux runtime not established by cross-build or macOS execution"]}
    save(proof / "run.json", meta)
    write(proof / "control.lock", "")
    save(root / ".owner.json", {"token": token, "proof": str(proof)})
    for name in ("home", "config", "state", "bucket", "work", "tmp", "bin"):
        (root / name).mkdir(mode=0o700)
    write(root / "config/config.env", "# Offline verification; local destination is environment-only.\n")
    run = Run(proof)
    print(str(proof), flush=True)  # Always available even if build fails; use cleanup on failures.
    try:
        run.command("launch", [meta["go"], "build", "-trimpath", "-ldflags", "-X main.version=" + meta["version"],
                               "-o", run.binary, "./cmd/agent-sessions"], timeout=300, cwd=REPO)
        run.binary.chmod(0o700)
        run.meta["binary_sha256"] = sha(run.binary.read_bytes())
        out, _ = run.cli("launch", "machine-id", "--new")
        run.meta["machine_id"] = out.strip()
        run.cli("launch", "sync")  # real command initializes SQLite and a fresh production Local store
        run.meta["store_identity"] = json.loads((root / "bucket/v3/store.json").read_text())
        run.meta["ready"] = True
        run.persist()
        private_tree(root)
    except Exception as error:
        run.meta["launch_error"] = str(error)
        run.persist()
        private_tree(root)
        raise


def setup(run):
    # A forged matching token cannot authorize cleanup outside the direct temp root.
    fake_proof = run.root / "ownership-negative-proof"
    fake_root = run.root / "work/agent-sessions-verify-forged"
    fake_proof.mkdir(mode=0o700)
    fake_root.mkdir(mode=0o700)
    forged = dict(run.meta, scratch=str(fake_root))
    save(fake_proof / "run.json", forged)
    save(fake_root / ".owner.json", {"token": run.meta["token"], "proof": str(fake_proof)})
    try:
        try:
            Run(fake_proof)
        except RuntimeError as error:
            require("canonical direct child" in str(error), "ownership fixture rejected for wrong reason")
        else:
            raise RuntimeError("forged cleanup ownership accepted")
        require(fake_root.exists(), "ownership check deleted a foreign path")
        run.note("setup", "negative forged scratch ownership rejected without deletion")
    finally:
        shutil.rmtree(fake_proof)
        shutil.rmtree(fake_root)
    original = run.meta["machine_id"]
    for args in (("machine-id",), ("machine-id", "--new")):
        out, _ = run.cli("setup", *args)
        require(out.strip() == original, "identity rotated")
    missing = run.root / "missing/config.env"
    run.cli("setup", "machine-id", expected=1, extra={"AGENT_SESSIONS_CONFIG": str(missing)})
    require(not missing.parent.exists(), "read-only missing identity created a directory")
    out, _ = run.cli("setup", "machine-id", extra={"AGENT_SESSIONS_CONFIG": str(missing),
                                                   "AGENT_SESSIONS_MACHINE_ID": original})
    require(out.strip() == original and not missing.parent.exists(), "known identity override failed")
    settings = run.root / "home/.claude/settings.json"
    codex = run.root / "home/.codex/config.toml"
    pi = run.root / "home/.pi/agent/settings.json"
    write(settings, '{"model":"verification-model","hooks":{"Stop":[{"hooks":[{"type":"command","command":"/bin/true"}]}]}}\n')
    write(codex, 'model = "verification-model"\n')
    write(pi, '{}\n')
    baseline = inventory(run.root)
    run.cli("setup", "install", "--dry-run")
    require(baseline == inventory(run.root), "install dry-run mutated scratch")
    run.cli("setup", "install")
    installed = settings.read_bytes()
    cfg = json.loads(installed)
    require(cfg["model"] == "verification-model" and cfg["cleanupPeriodDays"] >= 3650, "settings/retention not preserved")
    require(set(cfg["hooks"]) == {"SessionStart", "Stop", "StopFailure", "SubagentStop", "PreCompact", "TaskCompleted", "TeammateIdle", "SessionEnd"}
            and "/bin/true" in installed.decode(), "hook coverage or foreign hook lost")
    hooks = run.root / "home/.codex/hooks.json"
    extension = run.root / "home/.pi/agent/extensions/agent-sessions.ts"
    require(set(json.loads(hooks.read_text())["hooks"]) == {"SessionStart", "Stop", "SubagentStop", "PreCompact", "Interrupt", "SessionEnd"}, "Codex event coverage")
    require(all(event in extension.read_text() for event in ("session_start", "agent_settled", "session_shutdown")), "Pi event coverage")
    run.cli("setup", "install")
    require(settings.read_bytes() == installed, "reinstall changed settings")
    baseline = inventory(run.root)
    run.cli("setup", "uninstall", "--dry-run")
    require(baseline == inventory(run.root), "uninstall dry-run mutated scratch")
    # Production doctor is deliberately NOT our read-only doctor: checkRuns opens SQLite;
    # on R2 it also PUTs/GETs/DELETEs a random probe. This instance uses only Local.
    bucket_before = inventory(run.root / "bucket")
    out, _ = run.cli("setup", "doctor", expected=1)
    require("local directory, not R2" in out and "0/6 agent-sessions hooks trusted" in out, "doctor did not expose unapproved Codex hooks")
    require(bucket_before == inventory(run.root / "bucket"), "local doctor unexpectedly changed bucket")
    run.snapshot("installed")
    preserved = {name: inventory(run.root / name) for name in ("config", "state", "bucket")}
    run.cli("setup", "uninstall")
    require(preserved == {name: inventory(run.root / name) for name in preserved}, "uninstall changed config/state/bucket")
    require("hook claude" not in settings.read_text() and "/bin/true" in settings.read_text(), "uninstall foreign settings damaged")
    require(not hooks.exists() and not extension.exists(), "managed hooks remain")
    run.cli("setup", "install", "--agent", "pi")
    run.cli("setup", "uninstall", "--agent", "pi")
    require((run.root / "config/machine-id").read_text().strip() == original, "uninstall removed identity")


def fixtures(run):
    cwd = str(run.root / "work")
    return {
        "claude": ("projects/-verify/11111111-2222-3333-4444-555555555555.jsonl",
                   json.dumps({"type": "user", "sessionId": "11111111-2222-3333-4444-555555555555", "cwd": cwd,
                               "message": {"role": "user", "content": "local fixture"}}).encode() + b"\n"),
        "codex": ("sessions/2026/10/10/rollout-2026-10-10T10-00-00-0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b.jsonl",
                  json.dumps({"type": "session_meta", "payload": {"id": "0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b", "cwd": cwd}}).encode() + b"\n"),
        "pi": ("sessions/--verify--/2026-10-10T10-00-00-000Z_s1.jsonl",
               json.dumps({"type": "session", "version": 3, "id": "s1", "cwd": cwd}).encode() + b"\n")}


def agent_root(run, agent):
    return run.root / "home" / {"claude": ".claude", "codex": ".codex", "pi": ".pi/agent"}[agent]


def capture(run):
    write(run.root / "work/AGENTS.md", "# Synthetic verification instructions\n")
    items = fixtures(run)
    for agent, (relative, body) in items.items():
        write(agent_root(run, agent) / relative, body)
    run.cli("capture", "backfill")
    # The production adapters canonicalize Codex rollout keys, preserving other agent paths.
    keys = {agent: relative if agent != "codex" else "rollouts/" + Path(relative).name
            for agent, (relative, _) in items.items()}
    for agent, (_, body) in items.items():
        run.capture("capture", agent, keys[agent], body)
    acknowledged = {}

    def retain_commits():
        for key, digest in acknowledged.items():
            require(sha((run.root / "bucket" / key).read_bytes()) == digest, "acknowledged immutable commit changed or lost")
        acknowledged.update({str(p.relative_to(run.root / "bucket")): sha(p.read_bytes())
                             for p in (run.root / "bucket").rglob("*.json") if "/commits/" in str(p)})
        artifact = run.proof / "commit-inventories" / f"{len(run.meta['observations']):03d}.json"
        save(artifact, acknowledged)
        run.note("capture", "retain all acknowledged immutable commit hashes", path=str(artifact), count=len(acknowledged))

    retain_commits()
    expected_history = [items["pi"][1]]
    out, _ = run.cli("capture", "sync")
    require("uploaded 0 files" in out, "unchanged run uploaded files")
    for agent, (relative, body) in items.items():
        path = agent_root(run, agent) / relative
        appended = body + b'{"type":"message","message":{"role":"user","content":"appended"}}\n'
        write(path, appended)
        run.cli("capture", "sync", "--agent", agent, "--path", str(path))
        run.capture("capture", agent, keys[agent], appended)
        retain_commits()
        if agent == "pi":
            expected_history.append(appended)
    agent = "pi"
    path = agent_root(run, agent) / items[agent][0]
    for body in (b'{"type":"session","version":3,"id":"rewritten"}\n', b""):
        write(path, body)
        run.cli("capture", "sync", "--agent", agent, "--path", str(path))
        run.capture("capture", agent, keys[agent], body)
        expected_history.append(body)
        retain_commits()
    require([run.decoded(g) for g in run.generations(agent, keys[agent])] == expected_history,
            "archive history differs from explicit acknowledged fixture bodies before loss")
    # Negative oracle: a self-hashed, typed sequence-999 orphan must not become current.
    stream = sha((agent + "\0" + keys[agent]).encode())
    directory = run.root / "bucket/v3/machines" / run.meta["machine_id"] / "records/file" / stream / "commits"
    tip = sorted(directory.glob("*.json"))[-1]
    forged = json.loads(tip.read_bytes())
    forged["sequence"] = forged["file"]["sequence"] = 999
    forged["parent"] = forged["file"]["parent"] = sha(tip.read_bytes())
    body = json.dumps(forged, sort_keys=True, separators=(",", ":")).encode()
    orphan = directory / (f"{999:020d}-" + sha(body) + ".json")
    write(orphan, body)
    write(run.proof / "negative-fixtures/orphan-commit.json", body)
    try:
        try:
            run.generations(agent, keys[agent])
        except RuntimeError as error:
            require("noncontiguous committed chain" in str(error), "orphan rejected for wrong reason")
        else:
            raise RuntimeError("self-hashed orphan commit accepted as current")
        run.note("capture", "negative self-hashed orphan commit rejected")
    finally:
        orphan.unlink()
    run.snapshot("before-source-cache-loss")
    path.unlink()
    run.note("capture", "delete source and disposable cache", path=str(path), state=str(run.root / "state"))
    shutil.rmtree(run.root / "state")
    run.cli("capture", "sync")  # real CLI recovery, never an invented restore subcommand
    current = run.capture("capture", agent, keys[agent], b"")
    require(current["kind"] == "delete", "complete sweep failed to publish deletion")
    recovered_history = [run.decoded(g) for g in run.generations(agent, keys[agent])]
    require(recovered_history == expected_history + [b""], "history changed after cache/source loss")
    retain_commits()
    db_path = run.root / "state/state.sqlite3"
    with contextlib.closing(sqlite3.connect(db_path.as_uri() + "?mode=ro", uri=True)) as db:
        count = db.execute("SELECT count(*) FROM archive_records WHERE committed=1").fetchone()[0]
    require(count > 0, "CLI did not rebuild SQLite archive projection")
    run.note("capture", "cold CLI recovery retained historical bytes and rebuilt projection", committed_rows=count,
             history_sha256=[sha(body) for body in recovered_history])


def privacy(run):
    root = agent_root(run, "pi")
    config = root / "models.json"
    literal = "synthetic-secret-do-not-upload-0001"
    write(config, json.dumps({"providers": {"fixture": {"apiKey": literal}}}))
    write(root / "auth.json", literal)
    write(root / "auth.json.bak", literal)
    write(root / "unknown.txt", "NOT_DLP_" + literal)
    run.cli("privacy", "sync", "--agent", "pi")
    protected = run.decoded(run.generations("pi", "models.json")[-1])
    require(b"[redacted]" in protected and literal.encode() not in protected, "config secret not redacted")
    require(not run.generations("pi", "auth.json") and not run.generations("pi", "auth.json.bak"), "credential file captured")
    run.capture("privacy", "pi", "unknown.txt", ("NOT_DLP_" + literal).encode())
    previous = len(run.generations("pi", "models.json"))
    write(config, '{"providers": malformed')
    out, err = run.cli("privacy", "sync", "--agent", "pi", expected=1)
    require("error" in out + err and len(run.generations("pi", "models.json")) == previous, "malformed protected config not withheld")
    run.note("privacy", "malformed protected config reported, previous committed generation retained", stdout=out, stderr=err)
    write(config, '{}')
    # A self-referential link yields ELOOP: genuine unresolved observation, even as root.
    # A dangling target is ordinary absence (fs.ErrNotExist), not partial discovery.
    target = root / "sessions/--partial--/partial.jsonl"
    write(target, '{"type":"session","version":3,"id":"partial"}\n')
    run.cli("privacy", "sync", "--agent", "pi")
    key = "sessions/--partial--/partial.jsonl"
    count = len(run.generations("pi", key))
    target.unlink()
    target.symlink_to(target.name)
    try:
        out, err = run.cli("privacy", "sync", "--agent", "pi", expected=1)
        require(len(run.generations("pi", key)) == count, "partial discovery published deletion")
        run.note("privacy", "partial discovery reported without deletion", stdout=out, stderr=err)
    finally:
        target.unlink()  # never leave symlinks for evidence or cleanup
    denied = root / "sessions/--optout--/denied.jsonl"
    allowed = root / "sessions/--optout--/allowed.jsonl"
    write(denied, '{"type":"session","version":3,"id":"denied"}\n')
    write(allowed, '{"type":"session","version":3,"id":"allowed"}\n')
    write(root / "history.jsonl", '{"synthetic":"shared-history"}\n')
    before = inventory(run.root / "bucket")
    out, err = run.cli("privacy", "hook", "pi", "--path", str(denied), extra={"AGENT_SESSIONS_DISABLE": "1"})
    require(not out and not err and before == inventory(run.root / "bucket"), "disabled hook uploaded or printed")
    require(list((run.root / "state").rglob("*exclu*")), "disabled hook did not persist exclusion")
    run.cli("privacy", "sync", "--agent", "pi")
    require(not run.generations("pi", "sessions/--optout--/denied.jsonl"), "opted-out session captured")
    run.capture("privacy", "pi", "sessions/--optout--/allowed.jsonl", allowed.read_bytes())
    require(not run.generations("pi", "history.jsonl"), "shared root history captured after opt-out")
    commits_before = {str(p.relative_to(run.root / "bucket")): sha(p.read_bytes())
                      for p in (run.root / "bucket").rglob("*.json") if "/commits/" in str(p)}
    out, err = run.cli("privacy", "hook", "pi", "--path", str(denied))
    require(not out and not err, "later enabled hook printed output")
    run.quiescent()
    require(not run.generations("pi", "sessions/--optout--/denied.jsonl"), "later enabled hook re-enabled excluded session")
    commits_after = {str(p.relative_to(run.root / "bucket")): sha(p.read_bytes())
                     for p in (run.root / "bucket").rglob("*.json") if "/commits/" in str(p)}
    require(commits_before == commits_after, "later enabled excluded hook published unexpected commits")
    require(not list((run.root / "state/pending").glob("launch-*.json"))
            and not list((run.root / "state/pending").glob("[0-9a-f]*")), "later excluded hook left pending launch/capture")
    run.note("privacy", "later enabled real hook remained excluded; no new commits or pending work")
    shutil.rmtree(run.root / "state")
    run.cli("privacy", "backfill")
    require(denied.is_file() and allowed.is_file(), "opt-out evidence sources unexpectedly missing")
    require(not run.generations("pi", "sessions/--optout--/denied.jsonl"), "cold recovery lost persistent opt-out")
    require(not run.generations("pi", "history.jsonl"), "cold recovery lost conservative root withholding")
    run.note("privacy", "opt-out survived cache loss; sibling captured, shared history withheld")


def hooks(run):
    # Synthetic direct hook path is not a real-agent lifecycle claim. E2E and live are separate.
    root = agent_root(run, "claude")
    path = root / "projects/-hooks/22222222-2222-3333-4444-555555555555.jsonl"
    body = json.dumps({"type": "user", "sessionId": "22222222-2222-3333-4444-555555555555",
                       "cwd": str(run.root / "work"), "message": {"role": "user", "content": "hook fixture"}}).encode() + b"\n"
    write(path, body)
    event = json.dumps({"hook_event_name": "SessionStart", "transcript_path": str(path), "cwd": str(run.root / "work")})
    fixture_argv = ["claude", "synthetic-verification"]
    out, err = run.cli("hooks", "hook", "claude", "--argv", json.dumps(fixture_argv),
                       "--cwd", str(run.root / "work"), stdin=event)
    require(not out and not err, "hook printed output")
    deadline = time.monotonic() + 40
    while time.monotonic() < deadline:
        if run.generations("claude", "projects/-hooks/" + path.name) and run.records("launch"):
            break
        time.sleep(0.1)
    run.quiescent()
    run.capture("hooks", "claude", "projects/-hooks/" + path.name, body)
    launches = [p["launch"] for p in run.records("launch")
                if p["launch"]["envelope"]["path"] == str(path)]
    require(len(launches) == 1, "synthetic launch missing or duplicated")
    payload = launches[0]["envelope"]["payload"]
    require(payload["process"]["argv"] == fixture_argv
            and payload["process"]["via"] == "extension"
            and payload["cwd"] == str(run.root / "work")
            and launches[0]["root"] == str(root), "synthetic extension provenance differs")
    run.note("hooks", "synthetic extension-supplied provenance committed, NOT native ancestor proof",
             argv=fixture_argv, via="extension", cwd=payload["cwd"], source_root=launches[0]["root"])
    out, err = run.cli("hooks", "hook", "invalid-agent", "--bad")
    require(not out and not err, "malformed hook failed open contract")


def regression(run):
    # Existing TestMain builds its own instrumented binary; this is NOT make cover/gates.
    out, _ = run.command("regression", [run.meta["go"], "test", "./cmd/agent-sessions", "-run", "^TestScripts$", "-count=1", "-v"], timeout=300, cwd=REPO)
    require("--- PASS: TestScripts" in out and "--- SKIP:" not in out, "existing CLI e2e incomplete")
    pattern = "^(TestImmutableFileHistoryRecoversAfterCacheAndSourceLoss|TestStreamedRestoreValidatesWholeContentAndRejectsCorruption|TestArchiveRemoteExclusionRestoresBeforeRawExportsAndSnapshots)$"
    out, _ = run.command("regression", [run.meta["go"], "test", "./internal/syncer", "-run", pattern, "-count=1", "-v"], timeout=300, cwd=REPO)
    for name in pattern[2:-2].split("|"):
        require("--- PASS: " + name in out, "missing supplemental test " + name)
    require("--- SKIP:" not in out, "supplemental restore tests skipped")
    run.note("regression", "existing CLI e2e + INTERNAL supplemental API/history/restore tests; no restore CLI exists")


def live(run):
    if sys.platform != "darwin" or not os.access("/usr/bin/sandbox-exec", os.X_OK):
        raise Blocked("live requires macOS /usr/bin/sandbox-exec; NO fallback")
    for agent in ("claude", "codex", "pi"):
        if not os.access("/opt/homebrew/bin/" + agent, os.X_OK):
            raise Blocked("missing exact live agent: /opt/homebrew/bin/" + agent)
    evidence_import_fixture(run)
    out, _ = run.command("live", [run.meta["go"], "test", "-race", "./cmd/agent-sessions", "-run", "^TestLiveAgents$", "-count=1", "-v"],
                         extra={"CGO_ENABLED": "1", "AGENT_SESSIONS_LIVE": "1", "AGENT_SESSIONS_LIVE_KEEP": "1"}, timeout=360, cwd=REPO)
    # Existing harness owns separate /tmp/memory-live-* roots. Keep originals: their detached
    # sandboxed hooks are not our process children. Copy evidence; do not kill/delete those roots.
    paths = sorted(set(re.findall(r"scratch evidence: (/[^\s]+)", out)))
    require(len(paths) == 3, "live did not report all three scratch evidence roots")
    for path in paths:
        source = Path(path)
        require(source.parent == Path("/tmp").resolve() and source.name.startswith("memory-live-"), "unexpected live evidence path")
        owned(source, True)
        dest = run.proof / "live" / source.name
        links = import_evidence(source, dest)
        run.note("live", "native harness evidence imported; links are metadata only", path=str(dest), symlink_count=len(links))
    for agent in ("claude", "codex", "pi"):
        require("--- PASS: TestLiveAgents/" + agent in out, "missing live pass: " + agent)
    require("--- SKIP:" not in out, "live skipped (not a pass)")
    run.note("live", "existing sandbox acceptance; synthetic loopback models/local store only", retained_harness_roots=paths)


def drive(run, feature):
    doctor(run)
    names = FEATURES[:-1] if feature == "all" else (feature,)
    for name in names:
        require(run.meta["features"][name]["status"] == "unverified", f"{name} already attempted; launch a fresh run")
        run.meta["features"][name] = {"status": "unverified", "reason": "in progress"}
        run.persist()
        try:
            globals()[name](run)
            run.quiescent()
            run.snapshot(name)
            run.meta["features"][name] = {"status": "passed", "reason": "strict assertions completed; see actions and states"}
        except Exception as error:
            run.meta["features"][name] = {"status": "blocked" if isinstance(error, Blocked) else "failed", "reason": str(error)}
            raise
        finally:
            private_tree(run.root)
            run.persist()


def cleanup(run):
    if run.meta.get("cleaned"):
        print("Already cleaned; evidence retained: " + str(run.proof))
        return
    run.quiescent()
    # Validate the entire tree before deleting anything. Symlinks/special files/foreign owners
    # are refused. An arbitrary path supplied in JSON is insufficient without its private token.
    for path in [run.root, *run.root.rglob("*")]:
        owned(path, path.is_dir())
    run.snapshot("cleanup")
    shutil.rmtree(run.root)
    run.meta["cleaned"] = True
    run.meta["cleanup_time"] = time.time()
    run.persist()
    require((run.proof / "run.json").is_file() and (run.proof / "states").is_dir(), "evidence lost")
    save(run.proof / "evidence-hashes.json", inventory(run.proof))
    print("Cleaned owned scratch; evidence retained: " + str(run.proof))


def main():
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    launch_parser = commands.add_parser("launch")
    launch_parser.add_argument("--artifacts", help="canonical absolute NEW evidence directory (default private /tmp/agent-sessions-proof-*)")
    launch_parser.add_argument("--module-cache", help="prepopulated trusted Go module cache; default ~/go/pkg/mod; network disabled")
    launch_parser.add_argument("--go", help="explicit installed Go executable matching go.mod (no downloads)")
    for name in ("doctor", "cleanup", "live", "drive"):
        sub = commands.add_parser(name)
        sub.add_argument("run", help="absolute evidence directory printed by launch")
        if name == "drive":
            sub.add_argument("feature", choices=(*FEATURES[:-1], "all"))
    args = parser.parse_args()
    if args.command == "launch":
        launch(args)
        return
    run = Run(args.run)
    # Lock created by launch, never by read-only doctor. Concurrent drivers fail closed.
    with (run.proof / "control.lock").open("rb") as lock:
        fcntl.flock(lock, (fcntl.LOCK_SH if args.command == "doctor" else fcntl.LOCK_EX) | fcntl.LOCK_NB)
        if args.command == "doctor":
            doctor(run)
        elif args.command == "cleanup":
            cleanup(run)
        else:
            drive(run, "live" if args.command == "live" else args.feature)


if __name__ == "__main__":
    try:
        main()
    except (RuntimeError, OSError, ValueError, KeyError, sqlite3.Error) as error:
        print(("BLOCKED: " if isinstance(error, Blocked) else "FAILED: ") + str(error), file=sys.stderr)
        sys.exit(2 if isinstance(error, Blocked) else 1)
