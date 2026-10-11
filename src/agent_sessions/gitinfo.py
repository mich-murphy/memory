"""Git state at session start, and the repository history the normaliser needs.

git/<repo_id>/repo.json           the remote (checkouts are in each session's manifest)
git/<repo_id>/log/<head>.txt.gz   `git log --name-status` for commits not in an earlier export
git/<repo_id>/tree/<head>.txt.gz  `git ls-tree -r -l <head>`: every file and blob at that commit

Log records are separated by \\x1e; each starts with NUL-separated fields
(sha, parents, author name, author email, author date, committer date, full message) followed by
the --name-status lines. repo_id is a hash of the normalised remote URL, or of the machine and
the common git dir when there is no remote, so clones of one repo on many machines share it.
"""

from __future__ import annotations

import gzip
import hashlib
import json
import re
import subprocess
from pathlib import Path

LOG_FORMAT = "%x1e%H%x00%P%x00%an%x00%ae%x00%aI%x00%cI%x00%B%x00"


def _git(root: Path | str, *args: str, timeout: float = 10, text: bool = True):
    try:
        r = subprocess.run(["git", "-C", str(root), "-c", "core.quotepath=off", *args],
                           capture_output=True, text=text, timeout=timeout)
    except (OSError, subprocess.SubprocessError):
        return None
    return r.stdout if r.returncode == 0 else None


def strip_userinfo(url: str) -> str:
    """Drop a token or password embedded in a remote URL (a credential, never stored)."""
    return re.sub(r"^([a-z][a-z0-9+.-]*://)[^/@]*@", r"\1", url, flags=re.I)


def normalise_remote(url: str) -> str:
    """github.com/owner/repo for https, ssh and scp-style remotes alike."""
    u = strip_userinfo(url.strip())
    u = re.sub(r"^[a-z][a-z0-9+.-]*://", "", u, flags=re.I)
    u = re.sub(r"^[^@/]+@", "", u)  # git@host:owner/repo
    u = re.sub(r"^([^/:]+):(?!\d+/)", r"\1/", u)  # scp-style host:path
    u = re.sub(r":\d+/", "/", u)  # explicit port
    u = u.removesuffix("/").removesuffix(".git")
    host, _, path = u.partition("/")
    return f"{host.lower()}/{path}"


def git_state(cwd: str | None, machine_id: str) -> dict:
    if not cwd or not Path(cwd).is_dir():
        return {}
    top = _git(cwd, "rev-parse", "--show-toplevel")
    if not top:
        return {}
    root = top.strip()
    common = (_git(cwd, "rev-parse", "--path-format=absolute", "--git-common-dir") or "").strip()
    remote = (_git(cwd, "remote", "get-url", "origin") or "").strip()
    head = (_git(cwd, "rev-parse", "HEAD") or "").strip()
    branch = (_git(cwd, "rev-parse", "--abbrev-ref", "HEAD") or "").strip()
    status = _git(cwd, "status", "--porcelain", "--untracked-files=no", timeout=20)
    state = {
        "root": root,
        "common_dir": common or None,
        "remote": strip_userinfo(remote) if remote else None,
        "branch": branch or None,
        "head": head or None,
        "dirty": bool(status.strip()) if status is not None else None,
    }
    key = normalise_remote(remote) if remote else f"{machine_id}:{common or root}"
    state["repo_id"] = hashlib.sha256(key.encode()).hexdigest()[:16]
    return {k: v for k, v in state.items() if v is not None}


def export(git: dict, exported_heads: list[str]) -> list[tuple[str, bytes]] | None:
    """Objects to upload for this HEAD: (key, body). None if git failed."""
    root, head, repo_id = git.get("root"), git.get("head"), git.get("repo_id")
    if not (root and head and repo_id):
        return None
    # Only exclude earlier heads this clone still has; an unknown sha would fail the log.
    known: list[str] = []
    if exported_heads:
        try:
            r = subprocess.run(["git", "-C", root, "cat-file", "--batch-check"], input="\n".join(exported_heads) + "\n",
                               capture_output=True, text=True, timeout=30)
            known = [line.split()[0] for line in r.stdout.splitlines() if line.split()[1:2] == ["commit"]]
        except (OSError, subprocess.SubprocessError):
            known = []
    log = _git(root, "log", "--name-status", f"--format={LOG_FORMAT}", head, *(f"^{h}" for h in known), timeout=300, text=False)
    tree = _git(root, "ls-tree", "-r", "-l", head, timeout=120, text=False)
    if log is None or tree is None:
        return None
    repo = {"repo_id": repo_id, "remote": git.get("remote")}
    return [
        (f"git/{repo_id}/log/{head}.txt.gz", gzip.compress(log, mtime=0)),
        (f"git/{repo_id}/tree/{head}.txt.gz", gzip.compress(tree, mtime=0)),
        (f"git/{repo_id}/repo.json", json.dumps(repo, indent=1, sort_keys=True).encode()),
    ]
