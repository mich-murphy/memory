"""Upload new or changed session files, then refresh each touched session's manifest.

Object layout (see docs/storage-layout.md):
  raw/agent=<agent>/machine=<machine>/<adapter key>.gz      transcript bytes, gzip, unmodified
  manifests/agent=<agent>/machine=<machine>/<session>.json  files, session info, snapshot ids
  snapshots/blobs/<sha256>                                  redacted harness file bodies
  snapshots/sets/<set-id>.json                              which files made up a harness
"""

from __future__ import annotations

import gzip
import hashlib
import json
import logging
import time
from collections.abc import Iterable
from concurrent.futures import FIRST_COMPLETED, ThreadPoolExecutor, as_completed, wait
from dataclasses import dataclass, field
from pathlib import Path

from . import __version__, snapshot
from .adapters import Adapter, SessionFile
from .adapters.base import is_within, read_head
from .config import Config
from .state import State
from .store import Store

log = logging.getLogger("memsync")

SNAPSHOT_RECHECK_SECONDS = 600
# A transcript written to in the last minute may end mid-line; hold back the partial line
# until the next sync instead of uploading half a JSON object.
ACTIVE_WRITE_SECONDS = 60
UPLOAD_WORKERS = 8


@dataclass
class Summary:
    uploaded: int = 0
    unchanged: int = 0
    skipped: int = 0
    errors: int = 0
    bytes_uploaded: int = 0
    manifests: int = 0
    error_messages: list[str] = field(default_factory=list)

    def as_dict(self) -> dict:
        d = self.__dict__.copy()
        d["error_messages"] = d["error_messages"][:10]
        return d


@dataclass
class _Upload:
    sf: SessionFile
    key: str
    sha: str
    body: bytes | None  # None: content already in the bucket, only local state needs updating
    size: int
    mtime_ns: int
    head: list[dict] | None


def read_file(sf: SessionFile) -> bytes:
    data = sf.path.read_bytes()
    if sf.compression == "zst":
        try:
            import zstandard
        except ImportError as e:
            raise RuntimeError("zstandard is needed for Codex .jsonl.zst rollouts (reinstall memsync)") from e
        with zstandard.ZstdDecompressor().stream_reader(data) as reader:
            data = reader.read()
    return data


class Syncer:
    def __init__(self, cfg: Config, store: Store, state: State, adapters: dict[str, Adapter], *,
                 backfill: bool = False, snapshots: bool = True):
        self.cfg = cfg
        self.store = store
        self.state = state
        self.adapters = adapters
        self.backfill = backfill
        self.snapshots = snapshots
        self.summary = Summary()
        self._heads: dict[tuple[str, str], list[dict]] = {}
        self._roots: dict[tuple[str, str], Path] = {}
        self._touched: set[tuple[str, str]] = set()
        self._snapshot_cache: dict[tuple[str, str, str | None], str] = {}

    # discovery --------------------------------------------------------
    def roots(self, agent: str) -> list[Path]:
        return list(dict.fromkeys([*self.adapters[agent].default_roots(), *self.state.roots(agent)]))

    def sweep_files(self, agents: Iterable[str]) -> list[tuple[SessionFile, Path]]:
        out = []
        for agent in agents:
            for root in self.roots(agent):
                out.extend((sf, root) for sf in self.adapters[agent].discover(root))
        return out

    def hook_files(self, agent: str, paths: Iterable[Path]) -> list[tuple[SessionFile, Path]]:
        """Files for paths a hook reported, plus their related files (subagents, spilled output)."""
        adapter = self.adapters[agent]
        out: dict[Path, tuple[SessionFile, Path]] = {}
        for path in paths:
            path = path.expanduser().absolute()
            root = next((r for r in self.roots(agent) if is_within(path, r) and adapter.classify(path, r)), None)
            if root is None:
                root = adapter.infer_root(path)
                if root is None or adapter.classify(path, root) is None:
                    log.warning("%s: not a recognised %s session file", path, agent)
                    self.summary.skipped += 1
                    continue
                self.state.add_root(agent, root)
                log.info("remembering %s session root %s", agent, root)
            sf = adapter.classify(path, root)
            for p in [path, *adapter.related(sf, root)]:
                related = adapter.classify(p, root)
                if related and p not in out:
                    out[p] = (related, root)
        return list(out.values())

    def export_metadata(self, agents: Iterable[str]) -> None:
        for agent in agents:
            for root in self.roots(agent):
                try:
                    exports = self.adapters[agent].exports(root)
                except Exception as e:
                    self.summary.errors += 1
                    self.summary.error_messages.append(f"{agent} export from {root}: {type(e).__name__}: {e}")
                    continue
                for key, data in exports:
                    sf = SessionFile(agent, root / key, key, None, "aux")
                    sha = hashlib.sha256(data).hexdigest()
                    if self.state.uploaded_sha(self.raw_key(sf)) != sha:
                        self.store.put(self.raw_key(sf), gzip.compress(data, mtime=0), {"sha256": sha})
                        self.state.record_upload(self.raw_key(sf), sha)
                        self.summary.uploaded += 1
        self.state.db.commit()

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
                if upload.body is None:
                    self._finish(upload)
                    continue
                meta = {"sha256": upload.sha, "agent": sf.agent, "machine": self.cfg.machine_id, "role": sf.role}
                inflight[pool.submit(self.store.put, upload.key, upload.body, meta)] = upload
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

    def _manifest_failed(self, agent: str, session_key: str, e: Exception) -> None:
        self.summary.errors += 1
        self.summary.error_messages.append(f"manifest {agent}/{session_key}: {type(e).__name__}: {e}")
        log.error("failed to write manifest for %s/%s: %s: %s", agent, session_key, type(e).__name__, e)

    def _fail(self, sf: SessionFile, e: Exception) -> None:
        self.summary.errors += 1
        self.summary.error_messages.append(f"{sf.path}: {type(e).__name__}: {e}")
        log.error("failed to sync %s: %s: %s", sf.path, type(e).__name__, e)

    def raw_key(self, sf: SessionFile) -> str:
        return f"raw/agent={sf.agent}/machine={self.cfg.machine_id}/{sf.key}.gz"

    def manifest_key(self, agent: str, session_key: str) -> str:
        return f"manifests/agent={agent}/machine={self.cfg.machine_id}/{session_key}.json"

    def _prepare(self, sf: SessionFile, root: Path) -> "_Upload | None":
        st = sf.path.stat()
        if sf.session_key:
            self._roots.setdefault((sf.agent, sf.session_key), root)
        if self.state.file_unchanged(sf.path, st.st_size, st.st_mtime_ns):
            self.summary.unchanged += 1
            return None
        data = read_file(sf)
        size, mtime_ns = st.st_size, st.st_mtime_ns
        if sf.path.suffix == ".jsonl" and not data.endswith(b"\n") and time.time() - st.st_mtime < ACTIVE_WRITE_SECONDS:
            data = data[: data.rfind(b"\n") + 1]
            size, mtime_ns = len(data), -1  # force a re-read next time, when the line is complete
        if not data:
            self.summary.skipped += 1
            return None
        sha = hashlib.sha256(data).hexdigest()
        key = self.raw_key(sf)
        head = read_head(data) if sf.role == "main" and sf.session_key else None
        body = gzip.compress(data, compresslevel=6, mtime=0) if self.state.uploaded_sha(key) != sha else None
        return _Upload(sf, key, sha, body, size, mtime_ns, head)

    def _finish(self, u: "_Upload") -> None:
        sf = u.sf
        if u.body is not None:
            self.state.record_upload(u.key, u.sha)
            self.summary.uploaded += 1
            self.summary.bytes_uploaded += len(u.body)
            if sf.session_key:
                self._touched.add((sf.agent, sf.session_key))
        else:
            self.summary.unchanged += 1
        if u.head is not None:
            self._heads[(sf.agent, sf.session_key)] = u.head
        self.state.record_file(sf.path, sf.agent, sf.key, sf.session_key, sf.role, u.size, u.mtime_ns, u.sha)
        self.state.db.commit()

    # manifests and snapshots -------------------------------------------
    def build_manifest(self, agent: str, session_key: str) -> tuple[str, bytes, str] | None:
        """Update local session state and return (key, body, sha) if the manifest changed."""
        adapter = self.adapters[agent]
        now = time.time()
        prev = self.state.get_session(agent, session_key)
        info = dict(prev["info"]) if prev else {}
        head = self._heads.get((agent, session_key))
        if head:
            info.update(adapter.session_info(head))
        root = self._roots.get((agent, session_key))
        if root is not None:
            info["source_root"] = str(root)
        snapshots = list(prev["snapshots"]) if prev else []
        first_seen = prev["first_seen"] if prev else now

        if self.snapshots and root is not None:
            last = snapshots[-1] if snapshots else None
            if last is None or now - last.get("checked_at", 0) >= SNAPSHOT_RECHECK_SECONDS:
                set_id = self.take_snapshot(agent, root, info.get("cwd"))
                git = snapshot.git_state(snapshot.find_project_root(Path(info["cwd"]))[0]) if info.get("cwd") and Path(info["cwd"]).is_dir() else {}
                if last is None or last["id"] != set_id or last.get("git") != git:
                    snapshots.append({"id": set_id, "git": git, "taken_at": now, "checked_at": now, "during_backfill": self.backfill})
                else:
                    last["checked_at"] = now

        self.state.put_session(agent, session_key, info, snapshots, first_seen)
        manifest = {
            "schema": 1,
            "agent": agent,
            "machine": self.cfg.machine_id,
            "session_key": session_key,
            "info": info,
            "files": [
                {**f, "object": f"raw/agent={agent}/machine={self.cfg.machine_id}/{f['key']}.gz"}
                for f in self.state.session_files(agent, session_key)
            ],
            "snapshots": [{k: v for k, v in s.items() if k != "checked_at"} for s in snapshots],
            "first_seen": first_seen,
        }
        body = json.dumps(manifest, sort_keys=True).encode()
        sha = hashlib.sha256(body).hexdigest()
        key = self.manifest_key(agent, session_key)
        if self.state.uploaded_sha(key) == sha:
            return None
        manifest["synced_at"] = now
        manifest["memsync_version"] = __version__
        return key, json.dumps(manifest, indent=1, sort_keys=True).encode(), sha

    def take_snapshot(self, agent: str, root: Path, cwd: str | None) -> str:
        cache_key = (agent, str(root), cwd)
        if cache_key in self._snapshot_cache:
            return self._snapshot_cache[cache_key]
        set_id, doc, blobs = snapshot.build(self.adapters[agent], root, cwd)
        missing = {f"snapshots/blobs/{sha}": (sha, body) for sha, body in blobs.items()
                   if self.state.uploaded_sha(f"snapshots/blobs/{sha}") is None}
        with ThreadPoolExecutor(max_workers=UPLOAD_WORKERS) as pool:
            for key, _ in zip(missing, pool.map(lambda kv: self.store.put(kv[0], kv[1][1], {"sha256": kv[1][0]}), missing.items())):
                self.state.record_upload(key, missing[key][0])
        set_key = f"snapshots/sets/{set_id}.json"
        if self.state.uploaded_sha(set_key) is None:
            self.store.put(set_key, json.dumps(doc, indent=1, sort_keys=True).encode(), {})
            self.state.record_upload(set_key, set_id)
        self._snapshot_cache[cache_key] = set_id
        return set_id
