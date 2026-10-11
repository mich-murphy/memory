"""Upload new or changed files, then refresh each touched session's manifest.

Object layout (see docs/storage-layout.md):
  raw/agent=<agent>/machine=<machine>/<key>.zst                       file bytes, zstd, unmodified
  raw/agent=<agent>/machine=<machine>/<key>.zst.d/<start>-<end>.zst   bytes appended since, until folded
  blobs/<sha256>.zst                                                  bytes a rewrite replaced, stored once
  raw-versions/agent=<agent>/machine=<machine>/<key>.json             which blobs hold each earlier version
  manifests/agent=<agent>/machine=<machine>/<session>.json            files, launch, git, snapshots
  snapshots/blobs/<sha256>                                            harness and memory file bodies
  snapshots/sets/<set-id>.json                                        which files made up a set
  git/<repo-id>/...                                                   history of snapshotted repos
"""

from __future__ import annotations

import hashlib
import json
import logging
import os
import tempfile
import time
from collections.abc import Iterable
from concurrent.futures import FIRST_COMPLETED, ThreadPoolExecutor, as_completed, wait
from dataclasses import dataclass, field
from pathlib import Path

from . import __version__, gitinfo, raw, snapshot
from .adapters import Adapter, SessionFile
from .adapters.base import is_within, read_head
from .config import Config
from .state import State
from .store import Store

log = logging.getLogger("agent_sessions")

SNAPSHOT_RECHECK_SECONDS = 600
# A session whose transcript changed this recently counts as running when a sweep snapshots it.
ACTIVE_SESSION_SECONDS = 900
# A transcript written to in the last minute may end mid-line; hold back the partial line
# until the next sync instead of uploading half a JSON object.
ACTIVE_WRITE_SECONDS = 60
UPLOAD_WORKERS = 8
# Files bigger than this are hashed and compressed in a stream to a temp file and uploaded
# from disk (multipart on R2), instead of being held in memory.
LARGE_FILE_BYTES = 64 * 1024 * 1024
CHUNK = 1024 * 1024
PREFIX_LINES = 20
TRANSCRIPT_ROLES = {"main", "subagent", "superseded"}
# A file that grew by appending and was written to within this long is still in use: upload only
# its new bytes, as a segment. Once it has been quiet this long, the next sync folds it back
# into one object, which also compresses better.
FOLD_AFTER_SECONDS = ACTIVE_SESSION_SECONDS
MAX_SEGMENTS = 64
# Database copies are refreshed hourly (AGENT_SESSIONS_DB_INTERVAL_MINUTES); the copy a refresh
# replaces is kept as a version at most once a day.
DB_VERSION_SECONDS = 24 * 3600


@dataclass
class Summary:
    uploaded: int = 0
    unchanged: int = 0
    skipped: int = 0
    errors: int = 0
    bytes_uploaded: int = 0
    manifests: int = 0
    versions: int = 0
    segments: int = 0
    folded: int = 0
    deleted: int = 0
    unknown: int = 0
    error_messages: list[str] = field(default_factory=list)

    def as_dict(self) -> dict:
        d = self.__dict__.copy()
        d["error_messages"] = d["error_messages"][:10]
        return d


@dataclass
class _Upload:
    sf: SessionFile
    root: Path
    key: str
    sha: str  # sha256 of the whole file as uploaded
    size: int  # bytes uploaded (uncompressed), less than the file while a last line is held back
    mtime_ns: int
    line_count: int | None
    prefix_sha: str | None
    head: dict | None
    changed: bool  # False: the bucket already has these bytes, only local state needs updating
    start: int = 0  # above 0: a segment holding only bytes [start, size)
    piece_sha: str | None = None  # sha256 of the bytes in this object
    body: bytes | None = None
    body_path: Path | None = None
    old_pieces: list[dict] = field(default_factory=list)  # what a whole upload replaces
    version: dict | None = None  # the replaced bytes of a rewrite, kept as blobs


def _clean_version(v: dict) -> dict:
    return {"sha256": v["sha256"], "size": v["size"], "versioned_at": v["versioned_at"],
            "pieces": [{k: p[k] for k in ("start", "end", "sha256", "object")} for p in v["pieces"]]}


def read_file(sf: SessionFile) -> bytes:
    data = sf.path.read_bytes()
    if sf.compression == "zst":
        try:
            import zstandard
        except ImportError as e:
            raise RuntimeError("zstandard is needed for Codex .jsonl.zst rollouts (reinstall agent-sessions)") from e
        with zstandard.ZstdDecompressor().stream_reader(data) as reader:
            data = reader.read()
    return data


def _prefix_sha(data: bytes) -> str:
    end = 0
    for _ in range(PREFIX_LINES):
        nl = data.find(b"\n", end)
        if nl < 0:
            end = len(data)
            break
        end = nl + 1
    return hashlib.sha256(data[:end]).hexdigest()


def _last_newline(path: Path, size: int) -> int:
    """Offset just past the last newline in the first `size` bytes (0 if none)."""
    with path.open("rb") as fh:
        pos = size
        while pos > 0:
            start = max(0, pos - CHUNK)
            fh.seek(start)
            chunk = fh.read(pos - start)
            nl = chunk.rfind(b"\n")
            if nl >= 0:
                return start + nl + 1
            pos = start
    return 0


class Syncer:
    def __init__(self, cfg: Config, store: Store, state: State, adapters: dict[str, Adapter], *,
                 trigger: str = "manual", snapshots: bool = True):
        self.cfg = cfg
        self.store = store
        self.state = state
        self.adapters = adapters
        self.trigger = trigger
        self.snapshots = snapshots
        self.summary = Summary()
        self._roots: dict[tuple[str, str], Path] = {}
        self._touched: set[tuple[str, str]] = set()
        self._snapshot_cache: dict[tuple, tuple[str, str | None, dict]] = {}
        self.work_dir = cfg.state_dir / "exports"
        self.tmp_dir = cfg.state_dir / "tmp"

    # discovery --------------------------------------------------------
    def roots(self, agent: str) -> list[Path]:
        return list(dict.fromkeys([*self.adapters[agent].default_roots(), *self.state.roots(agent)]))

    def _excluded(self, path: Path) -> bool:
        return path == self.cfg.config_path or is_within(path, self.cfg.state_dir)

    def sweep_files(self, agents: Iterable[str], *, tombstones: bool = True) -> list[tuple[SessionFile, Path]]:
        """Every file under every root, plus database copies and other exports. Files that were
        uploaded before and are gone from disk now get a deletion date."""
        out: dict[Path, tuple[SessionFile, Path]] = {}
        for agent in agents:
            adapter = self.adapters[agent]
            for root in self.roots(agent):
                found: set[str] = set()
                for sf in adapter.discover(root):
                    if not self._excluded(sf.path) and sf.path not in out:
                        out[sf.path] = (sf, root)
                        found.add(str(sf.path))
                try:
                    for sf in adapter.exports(root, self.work_dir, self.cfg.db_interval_minutes * 60):
                        out[sf.path] = (sf, root)
                        found.add(str(sf.path))
                except Exception as e:
                    self._error(f"{agent} exports from {root}: {type(e).__name__}: {e}")
                if tombstones and root.is_dir():
                    self._tombstone(agent, root, found)
        return list(out.values())

    def _tombstone(self, agent: str, root: Path, found: set[str]) -> None:
        now = time.time()
        for path, session_key in self.state.live_files(agent, root):
            if path not in found and not os.path.exists(path):
                self.state.mark_deleted(path, now)
                self.summary.deleted += 1
                if session_key:
                    self._touched.add((agent, session_key))
                    self._roots.setdefault((agent, session_key), root)
        self.state.db.commit()

    def _resolve(self, agent: str, path: Path) -> tuple[SessionFile, Path] | None:
        adapter = self.adapters[agent]
        path = path.expanduser().absolute()
        root = next((r for r in self.roots(agent) if is_within(path, r) and adapter.classify(path, r)), None)
        if root is None:
            root = adapter.infer_root(path)
            if root is None or adapter.classify(path, root) is None:
                log.warning("%s: not a recognised %s session file", path, agent)
                self.summary.skipped += 1
                return None
        if root != adapter.canonical_root() and root not in self.state.roots(agent):
            # Found through the hook's environment (CLAUDE_CONFIG_DIR...) or the path itself:
            # the sweep, which runs without that environment, needs to know about it.
            self.state.add_root(agent, root)
            self.state.db.commit()
            log.info("remembering %s root %s", agent, root)
        return adapter.classify(path, root), root

    def hook_files(self, agent: str, paths: Iterable[Path]) -> list[tuple[SessionFile, Path]]:
        """Files for paths a hook reported, plus their related files (subagents, spilled output,
        file history, tasks...)."""
        adapter = self.adapters[agent]
        out: dict[Path, tuple[SessionFile, Path]] = {}
        for path in paths:
            resolved = self._resolve(agent, path)
            if resolved is None:
                continue
            sf, root = resolved
            for p in [sf.path, *adapter.related(sf, root)]:
                related = adapter.classify(p, root)
                if related and p not in out and not self._excluded(p):
                    out[p] = (related, root)
        return list(out.values())

    # launches ---------------------------------------------------------
    def record_launch(self, agent: str, path: Path, launch: dict) -> None:
        """A session started: keep how it was launched, its git state and the harness on disk
        before the first prompt (timing pre_session)."""
        resolved = self._resolve(agent, path)
        if resolved is None or not resolved[0].session_key:
            return
        sf, root = resolved
        session_key = sf.session_key
        cwd = launch.get("cwd") or (launch.get("hook_input") or {}).get("cwd")
        now = time.time()
        prev = self.state.get_session(agent, session_key)
        info = dict(prev["info"]) if prev else {}
        if cwd:
            info.setdefault("cwd", cwd)
        snapshots = list(prev["snapshots"]) if prev else []
        launches = list(prev["launches"]) if prev else []
        git = gitinfo.git_state(cwd, self.cfg.machine_id)
        entry = {**launch, "git": git}
        if self.snapshots:
            set_id, memory_id, mtimes = self.take_snapshot(agent, root, cwd, session_key)
            entry["snapshot"] = set_id
            self._add_snapshot(snapshots, set_id, memory_id, mtimes, git, "pre_session", now)
            self.export_git(git)
        launches.append(entry)
        self.state.put_session(agent, session_key, info, snapshots, prev["first_seen"] if prev else now, launches)
        self.state.db.commit()
        self._touched.add((agent, session_key))
        self._roots[(agent, session_key)] = root

    # syncing ----------------------------------------------------------
    def run(self, files: Iterable[tuple[SessionFile, Path]]) -> Summary:
        """Read and hash files in order; upload changed ones on a small thread pool (uploads are
        latency-bound, and a first backfill can be thousands of files). State is only written
        from this thread."""
        inflight: dict = {}

        def finish(done) -> None:
            for fut in done:
                upload = inflight.pop(fut)
                try:
                    fut.result()
                except Exception as e:
                    self._fail(upload.sf, e)
                else:
                    self._finish(upload)
                finally:
                    if upload.body_path is not None:
                        upload.body_path.unlink(missing_ok=True)

        with ThreadPoolExecutor(max_workers=UPLOAD_WORKERS) as pool:
            for sf, root in files:
                try:
                    upload = self._prepare(sf, root)
                except FileNotFoundError:
                    continue  # rotated or recompressed between discovery and read
                except Exception as e:  # keep going; one bad file must not block the rest
                    self._fail(sf, e)
                    continue
                if upload is None:
                    continue
                if not upload.changed:
                    self._finish(upload)
                    continue
                inflight[pool.submit(self._upload, upload)] = upload
                if len(inflight) >= UPLOAD_WORKERS * 2:
                    finish(wait(inflight, return_when=FIRST_COMPLETED).done)
            finish(wait(inflight).done if inflight else [])

        with ThreadPoolExecutor(max_workers=UPLOAD_WORKERS) as pool:
            puts = {}
            for agent, session_key in sorted(self._touched):
                try:
                    job = self.build_manifest(agent, session_key)
                except Exception as e:
                    self._manifest_failed(agent, session_key, e)
                    continue
                if job is not None:
                    key, body, sha = job
                    puts[pool.submit(self.store.put, key, body, {"sha256": sha})] = (agent, session_key, key, sha)
            for fut in as_completed(puts):
                agent, session_key, key, sha = puts[fut]
                try:
                    fut.result()
                except Exception as e:
                    self._manifest_failed(agent, session_key, e)
                else:
                    self.state.record_upload(key, sha)
                    self.summary.manifests += 1
        self.state.db.commit()
        return self.summary

    def _error(self, msg: str) -> None:
        self.summary.errors += 1
        self.summary.error_messages.append(msg)
        log.error("%s", msg)

    def _manifest_failed(self, agent: str, session_key: str, e: Exception) -> None:
        self._error(f"manifest {agent}/{session_key}: {type(e).__name__}: {e}")

    def _fail(self, sf: SessionFile, e: Exception) -> None:
        self._error(f"{sf.path}: {type(e).__name__}: {e}")

    def raw_key(self, sf: SessionFile) -> str:
        return f"raw/agent={sf.agent}/machine={self.cfg.machine_id}/{sf.key}.zst"

    def versions_key(self, sf: SessionFile) -> str:
        return f"raw-versions/agent={sf.agent}/machine={self.cfg.machine_id}/{sf.key}.json"

    def manifest_key(self, agent: str, session_key: str) -> str:
        return f"manifests/agent={agent}/machine={self.cfg.machine_id}/{session_key}.json"

    def _plan(self, prev: tuple[str, int | None] | None, pieces: list[dict], sha: str, size: int, append: bool,
              idle: bool) -> tuple[str | None, int]:
        """How to upload a file: None (the bucket has it), "segment" (only the bytes after
        `start`), or "whole" (one object, replacing any segments)."""
        if prev is not None and prev[0] == sha:
            return ("whole" if idle and len(pieces) > 1 else None), 0  # fold once the file is quiet
        if append and not idle and pieces and len(pieces) < MAX_SEGMENTS and size > prev[1]:
            return "segment", prev[1]
        return "whole", 0

    def _version_plan(self, sf: SessionFile, key: str, prev: tuple[str, int | None], pieces: list[dict]) -> dict | None:
        """Keep the bytes a rewrite replaces: each piece they were stored in is copied to
        blobs/<sha>.zst, unless a blob with that content is already there."""
        if sf.role == "db":
            last = self.state.last_version_at(key)
            if last is not None and time.time() - last < DB_VERSION_SECONDS:
                return None
        old = pieces or [{"start": 0, "end": prev[1], "sha256": prev[0], "object": key}]
        out = []
        for p in old:
            blob = raw.blob_key(p["sha256"])
            out.append({"start": p["start"], "end": p["end"], "sha256": p["sha256"], "object": blob,
                        "copy_from": None if self.state.uploaded(blob) else p["object"]})
        version = {"sha256": prev[0], "size": prev[1], "pieces": out, "versioned_at": time.time()}
        index = [*self.state.versions(key), _clean_version(version)]
        version["index"] = json.dumps({"key": sf.key, "versions": index}, indent=1, sort_keys=True).encode()
        return version

    def _prepare(self, sf: SessionFile, root: Path) -> _Upload | None:
        st = sf.path.stat()
        if sf.session_key:
            self._roots.setdefault((sf.agent, sf.session_key), root)
        key = self.raw_key(sf)
        pieces = self.state.pieces(key)
        idle = time.time() - st.st_mtime >= FOLD_AFTER_SECONDS
        if self.state.file_unchanged(sf.path, st.st_size, st.st_mtime_ns) and not (idle and len(pieces) > 1):
            self.summary.unchanged += 1
            return None
        prev = self.state.uploaded(key)
        active = sf.path.suffix == ".jsonl" and time.time() - st.st_mtime < ACTIVE_WRITE_SECONDS
        if st.st_size > LARGE_FILE_BYTES and sf.compression is None:
            return self._prepare_large(sf, root, st, key, prev, pieces, active, idle)

        data = read_file(sf)
        mtime_ns = st.st_mtime_ns
        if active and not data.endswith(b"\n"):
            data = data[: data.rfind(b"\n") + 1]
            mtime_ns = -1  # force a re-read next time, when the line is complete
        if not data:
            self.summary.skipped += 1
            return None
        sha = hashlib.sha256(data).hexdigest()
        head = None
        if sf.role in TRANSCRIPT_ROLES and sf.path.name.endswith((".jsonl", ".jsonl.zst")):
            head = self.adapters[sf.agent].session_info(read_head(data))
        append = prev is not None and prev[1] is not None and len(data) >= prev[1] \
            and hashlib.sha256(data[: prev[1]]).hexdigest() == prev[0]
        mode, start = self._plan(prev, pieces, sha, len(data), append, idle)
        upload = _Upload(sf, root, key, sha, len(data), mtime_ns, data.count(b"\n"), _prefix_sha(data), head,
                         changed=mode is not None, start=start)
        if mode == "segment":
            upload.piece_sha = hashlib.sha256(data[start:]).hexdigest()
            upload.body = raw.compress(data[start:])
        elif mode == "whole":
            upload.piece_sha = sha
            upload.body = raw.compress(data)
            upload.old_pieces = pieces
            if prev is not None and not append:
                upload.version = self._version_plan(sf, key, prev, pieces)
        return upload

    def _prepare_large(self, sf: SessionFile, root: Path, st: os.stat_result, key: str,
                       prev: tuple[str, int | None] | None, pieces: list[dict], active: bool, idle: bool) -> _Upload | None:
        """Hash the file in a stream, then compress only the bytes being uploaded to a temp file."""
        limit = _last_newline(sf.path, st.st_size) if active else st.st_size
        if limit == 0:
            self.summary.skipped += 1
            return None
        whole, old_prefix = hashlib.sha256(), hashlib.sha256()
        old_size = prev[1] if prev else None
        lines = 0
        read = 0
        with sf.path.open("rb") as src:
            while read < limit:
                chunk = src.read(min(CHUNK, limit - read))
                if not chunk:
                    break
                lines += chunk.count(b"\n")
                if old_size is not None and read < old_size:
                    old_prefix.update(chunk[: old_size - read])
                whole.update(chunk)
                read += len(chunk)
        sha = whole.hexdigest()
        with sf.path.open("rb") as fh:
            first = b"".join(fh.readline(16 * CHUNK) for _ in range(PREFIX_LINES))[:read]
        head = None
        if sf.role in TRANSCRIPT_ROLES and sf.path.suffix == ".jsonl":
            head = self.adapters[sf.agent].session_info(read_head(first))
        append = old_size is not None and read >= old_size and old_prefix.hexdigest() == prev[0]
        mode, start = self._plan(prev, pieces, sha, read, append, idle)
        upload = _Upload(sf, root, key, sha, read, st.st_mtime_ns if limit == st.st_size else -1, lines,
                         _prefix_sha(first), head, changed=mode is not None, start=start)
        if mode is None:
            return upload
        self.tmp_dir.mkdir(parents=True, exist_ok=True)
        fd, tmp = tempfile.mkstemp(dir=self.tmp_dir, suffix=".zst")
        try:
            with sf.path.open("rb") as src, os.fdopen(fd, "wb") as out:
                src.seek(start)
                upload.piece_sha = raw.compress_stream(src, out, read - start, CHUNK)
        except BaseException:
            Path(tmp).unlink(missing_ok=True)
            raise
        upload.body_path = Path(tmp)
        if mode == "whole":
            upload.old_pieces = pieces
            if prev is not None and not append:
                upload.version = self._version_plan(sf, key, prev, pieces)
        return upload

    def _upload(self, u: _Upload) -> None:
        """Runs on the pool. A rewrite first copies the bytes it replaces into blobs/."""
        if u.version is not None:
            for p in u.version["pieces"]:
                if p["copy_from"] and not self.store.copy(p["copy_from"], p["object"]):
                    log.warning("%s: %s is missing from the bucket; its earlier bytes can't be kept",
                                u.sf.path, p["copy_from"])
                    u.version = None
                    break
        target = raw.segment_key(u.key, u.start, u.size) if u.start else u.key
        meta = {"sha256": u.piece_sha, "agent": u.sf.agent, "machine": self.cfg.machine_id, "role": u.sf.role,
                "start": str(u.start), "end": str(u.size)}
        if u.body_path is not None:
            self.store.put_file(target, u.body_path, meta)
        else:
            self.store.put(target, u.body, meta)
        if u.version is not None:
            self.store.put(self.versions_key(u.sf), u.version["index"], {})
        if not u.start:
            for p in u.old_pieces:
                if p["start"] > 0:  # segments the new whole object replaces
                    try:
                        self.store.delete(p["object"])
                    except Exception as e:  # harmless: readers skip segments that start before the base ends
                        log.warning("could not delete %s: %s", p["object"], e)

    def _finish(self, u: _Upload) -> None:
        sf = u.sf
        if u.changed:
            self.state.record_upload(u.key, u.sha, u.size)
            if u.start:
                self.state.add_piece(u.key, {"start": u.start, "end": u.size, "sha256": u.piece_sha,
                                             "object": raw.segment_key(u.key, u.start, u.size)})
                self.summary.segments += 1
            else:
                self.state.set_pieces(u.key, [{"start": 0, "end": u.size, "sha256": u.sha, "object": u.key}])
                if len(u.old_pieces) > 1:
                    self.summary.folded += 1
            if u.version is not None:
                for p in u.version["pieces"]:
                    if p["copy_from"]:
                        self.state.record_upload(p["object"], p["sha256"], p["end"] - p["start"])
                v = _clean_version(u.version)
                self.state.record_version(u.key, v["sha256"], v["size"], v["pieces"])
                self.summary.versions += 1
            self.summary.uploaded += 1
            self.summary.bytes_uploaded += len(u.body) if u.body is not None else u.body_path.stat().st_size
            if sf.role == "unknown":
                self.summary.unknown += 1
        else:
            self.summary.unchanged += 1
        if sf.session_key and (u.changed or u.head is not None):
            self._touched.add((sf.agent, sf.session_key))
        self.state.record_file(sf.path, sf.agent, u.root, sf.key, sf.session_key, sf.role, u.size, u.mtime_ns,
                               u.sha, u.line_count, u.prefix_sha, u.head)
        self.state.db.commit()

    # manifests and snapshots -------------------------------------------
    def _timing(self, files: list[dict]) -> str:
        if self.trigger.startswith("hook:"):
            return "in_session"
        mtimes = [f.get("mtime_ns") or 0 for f in files if f["role"] == "main"]
        if -1 in mtimes:  # last line held back: being written right now
            return "in_session"
        return "in_session" if time.time() - max(mtimes, default=0) / 1e9 < ACTIVE_SESSION_SECONDS else "post_hoc"

    @staticmethod
    def _add_snapshot(snapshots: list, set_id: str, memory_id: str | None, mtimes: dict, git: dict,
                      timing: str, now: float) -> None:
        last = snapshots[-1] if snapshots else None
        same_git = last is not None and {k: last.get("git", {}).get(k) for k in ("head", "branch")} == \
            {k: git.get(k) for k in ("head", "branch")}
        if last is not None and last["id"] == set_id and last.get("memory_id") == memory_id and same_git:
            last["checked_at"] = now
            return
        snapshots.append({"id": set_id, "memory_id": memory_id, "git": git, "timing": timing, "taken_at": now,
                          "checked_at": now, "mtimes": mtimes})

    def build_manifest(self, agent: str, session_key: str) -> tuple[str, bytes, str] | None:
        """Update local session state and return (key, body, sha) if the manifest changed."""
        now = time.time()
        prev = self.state.get_session(agent, session_key)
        info = dict(prev["info"]) if prev else {}
        files = self.state.session_files(agent, session_key)
        for f in files:
            if f["role"] == "main" and f["head"]:
                info.update(f["head"])
        root = self._roots.get((agent, session_key))
        if root is not None:
            info["source_root"] = str(root)
        snapshots = list(prev["snapshots"]) if prev else []
        launches = list(prev["launches"]) if prev else []
        first_seen = prev["first_seen"] if prev else now

        if self.snapshots and root is not None and info.get("cwd"):
            last = snapshots[-1] if snapshots else None
            if last is None or now - last.get("checked_at", 0) >= SNAPSHOT_RECHECK_SECONDS:
                set_id, memory_id, mtimes = self.take_snapshot(agent, root, info["cwd"], session_key)
                git = gitinfo.git_state(info["cwd"], self.cfg.machine_id)
                self._add_snapshot(snapshots, set_id, memory_id, mtimes, git, self._timing(files), now)
                self.export_git(git)

        self.state.put_session(agent, session_key, info, snapshots, first_seen, launches)
        git = (launches[0].get("git") if launches else None) or (snapshots[0].get("git") if snapshots else None) or {}
        manifest = {
            "schema": 2,
            "agent": agent,
            "machine_id": self.cfg.machine_id,
            "hostname": self.cfg.hostname,
            "session_key": session_key,
            "info": info,
            "transcripts": [
                {
                    **{k: v for k, v in f.items() if k not in ("deleted_at", "mtime_ns")},
                    "object": self.raw_key(SessionFile(agent, Path(), f["key"], None, f["role"])),
                    "pieces": self.state.pieces(self.raw_key(SessionFile(agent, Path(), f["key"], None, f["role"]))),
                    "versions": self.state.versions(self.raw_key(SessionFile(agent, Path(), f["key"], None, f["role"]))),
                }
                for f in files
            ],
            "launch": launches[0] if launches else None,
            "launches": launches,
            "git": git,
            "snapshots": [{k: v for k, v in s.items() if k != "checked_at"} for s in snapshots],
            "deleted_locally": [{"key": f["key"], "at": f["deleted_at"]} for f in files if f["deleted_at"]],
            "first_seen": first_seen,
        }
        body = json.dumps(manifest, sort_keys=True).encode()
        sha = hashlib.sha256(body).hexdigest()
        key = self.manifest_key(agent, session_key)
        if self.state.uploaded_sha(key) == sha:
            return None
        manifest["synced_at"] = now
        manifest["collector_version"] = __version__
        return key, json.dumps(manifest, indent=1, sort_keys=True).encode(), sha

    def take_snapshot(self, agent: str, root: Path, cwd: str | None, session_key: str | None) -> tuple[str, str | None, dict]:
        cache_key = (agent, str(root), cwd, session_key if agent == "claude" else None)
        if cache_key in self._snapshot_cache:
            return self._snapshot_cache[cache_key]
        snap = snapshot.build(self.adapters[agent], root, cwd, session_key)
        missing = {f"snapshots/blobs/{sha}": (sha, body) for sha, body in snap.blobs.items()
                   if self.state.uploaded_sha(f"snapshots/blobs/{sha}") is None}
        with ThreadPoolExecutor(max_workers=UPLOAD_WORKERS) as pool:
            for key, _ in zip(missing, pool.map(lambda kv: self.store.put(kv[0], kv[1][1], {"sha256": kv[1][0]}), missing.items())):
                self.state.record_upload(key, missing[key][0])
        for set_id, doc in ((snap.set_id, snap.doc), (snap.memory_id, snap.memory_doc)):
            set_key = f"snapshots/sets/{set_id}.json"
            if set_id and self.state.uploaded_sha(set_key) is None:
                self.store.put(set_key, json.dumps(doc, indent=1, sort_keys=True).encode(), {})
                self.state.record_upload(set_key, set_id)
        result = (snap.set_id, snap.memory_id, snap.mtimes)
        self._snapshot_cache[cache_key] = result
        return result

    def export_git(self, git: dict) -> None:
        repo_id, head = git.get("repo_id"), git.get("head")
        if not repo_id or not head or self.state.git_exported(repo_id, head):
            return
        try:
            objects = gitinfo.export(git, self.state.git_heads(repo_id))
            if objects is None:
                return
            for key, body in objects:
                self.store.put(key, body, {})
        except Exception as e:
            self._error(f"git export {git.get('root')}: {type(e).__name__}: {e}")
            return
        self.state.record_git_export(repo_id, head)
        self.state.db.commit()
