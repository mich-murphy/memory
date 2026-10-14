package gitinfo

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mich-murphy/memory/internal/testenv"
)

func TestNormalizeRemote(t *testing.T) {
	for _, url := range []string{
		"https://github.com/Me/proj.git", "git@github.com:Me/proj.git", "ssh://git@github.com:22/Me/proj",
		"https://x:tok@GitHub.com/Me/proj/",
	} {
		if got := normalizeRemote(url); got != "github.com/Me/proj" {
			t.Errorf("normalizeRemote(%q) = %q", url, got)
		}
	}
	for url, want := range map[string]string{
		"https://x:tok@github.com/a/b":   "https://github.com/a/b",
		"https://user:p@ss@github.com/a": "https://github.com/a",
		"https://github.com/a@b":         "https://github.com/a@b",
		"https://github.com?q=a@b":       "https://github.com?q=a@b",
	} {
		if got := stripUserinfo(url); got != want {
			t.Errorf("stripUserinfo(%q) = %q, want %q", url, got, want)
		}
	}
}

func FuzzStripUserinfo(f *testing.F) {
	for _, seed := range []string{
		"https://x:tok@github.com/a/b", "https://u:p@ss@h/a", "ssh://git@h:22/p", "https://h/a@b", "git@h:p", "",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, url string) {
		got := stripUserinfo(url)
		if stripUserinfo(got) != got {
			t.Errorf("not idempotent: %q -> %q -> %q", url, got, stripUserinfo(got))
		}
		if strings.Contains(authority(got), "@") {
			t.Errorf("stripUserinfo(%q) = %q keeps userinfo", url, got)
		}
	})
}

func authority(url string) string {
	loc := schemeRE.FindStringIndex(url)
	if loc == nil {
		return ""
	}
	rest := url[loc[1]:]
	if end := strings.IndexAny(rest, "/?#"); end >= 0 {
		return rest[:end]
	}
	return rest
}

func TestStateOutsideARepoIsZero(t *testing.T) {
	if g := Read(t.Context(), t.TempDir(), "m"); !g.IsZero() {
		t.Errorf("State = %+v", g)
	}
	if g := Read(t.Context(), "", "m"); !g.IsZero() {
		t.Errorf("State = %+v", g)
	}
}

func TestStateOfARepo(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "proj")
	testenv.Write(t, filepath.Join(dir, "src", "main.go"), "package main\n")
	testenv.Git(t, dir, "init", "-q", "-b", "main")
	testenv.Git(t, dir, "remote", "add", "origin", "https://x:ghp_tok@github.com/Me/proj.git")
	testenv.Git(t, dir, "add", "-A")
	testenv.Git(t, dir, "commit", "-q", "-m", "first")
	head, err := exec.CommandContext(t.Context(), "git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("github.com/Me/proj"))
	clean, dirty := false, true
	want := State{
		Root: dir, CommonDir: filepath.Join(dir, ".git"), Remote: "https://github.com/Me/proj.git", Branch: "main",
		Head: strings.TrimSpace(string(head)), Dirty: &clean, RepoID: hex.EncodeToString(sum[:])[:16],
	}
	if diff := cmp.Diff(want, Read(t.Context(), filepath.Join(dir, "src"), "m")); diff != "" {
		t.Errorf("Read (-want +got):\n%s", diff)
	}

	testenv.Write(t, filepath.Join(dir, "src", "main.go"), "package main // edited\n")
	want.Dirty = &dirty
	if diff := cmp.Diff(want, Read(t.Context(), dir, "m")); diff != "" {
		t.Errorf("Read after an edit (-want +got):\n%s", diff)
	}

	// Without a remote, the id is the machine's and the git directory's, which worktrees share.
	testenv.Git(t, dir, "remote", "remove", "origin")
	wt := filepath.Join(tmp, "wt")
	testenv.Git(t, dir, "worktree", "add", "-q", wt)
	main, linked := Read(t.Context(), dir, "m"), Read(t.Context(), wt, "m")
	if main.RepoID != linked.RepoID || main.RepoID == want.RepoID || main.RepoID == Read(t.Context(), dir, "other").RepoID {
		t.Errorf("RepoID of the repo %s, of its worktree %s, with remote %s", main.RepoID, linked.RepoID, want.RepoID)
	}
}
