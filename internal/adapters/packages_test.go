package adapters

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func packageFixture(t *testing.T) (*Pi, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AGENT_SESSIONS_CONFIG", filepath.Join(home, "collector", "config.env"))
	t.Setenv("AGENT_SESSIONS_STATE_DIR", filepath.Join(home, "collector-state"))
	return NewPi(home), home
}

func packageCommand(t *testing.T, repo string, args ...string) string {
	t.Helper()
	argv := append([]string{"-c", "commit.gpgsign=false", "-c", "user.email=test@example.invalid", "-c", "user.name=Test", "-C", repo}, args...)
	body, err := exec.CommandContext(t.Context(), "git", argv...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, body)
	}
	return strings.TrimSpace(string(body))
}

func packageWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPiPackageRevision(t *testing.T) {
	p, home := packageFixture(t)
	root := p.CanonicalRoot()
	if got := p.ExtraSnapshotEntries(root, ""); !got.Complete || len(got.Entries) != 0 {
		t.Fatalf("absent packages: %+v", got)
	}
	for _, path := range []string{"npm/package.json", "npm/package-lock.json"} {
		if !slices.Contains(p.Rules().User, path) {
			t.Errorf("exact npm version source %s omitted", path)
		}
	}
	repo := filepath.Join(root, "git", "example.invalid", "owner", "package")
	packageWrite(t, filepath.Join(repo, "extension.ts"), "// version one\n")
	packageCommand(t, repo, "init", "-q")
	packageCommand(t, repo, "add", "extension.ts")
	packageCommand(t, repo, "commit", "-qm", "one")
	head := packageCommand(t, repo, "rev-parse", "HEAD")
	marker := filepath.Join(home, "fsmonitor-was-run")
	// A repository-supplied fsmonitor command must never execute during provenance capture.
	packageCommand(t, repo, "config", "core.fsmonitor", "touch "+marker)
	first := p.ExtraSnapshotEntries(root, "")
	if !first.Complete || len(first.Issues) != 0 || len(first.Entries) != 1 {
		t.Fatalf("package revision: %+v", first)
	}
	entry := first.Entries[0]
	if entry.Scope != "user" || entry.Name != "pi-package-provenance.json" {
		t.Fatalf("unexpected entry identity: %+v", entry)
	}
	var decoded struct {
		Git []packageRevision `json:"git"`
	}
	if err := json.Unmarshal(entry.Body, &decoded); err != nil {
		t.Fatal(err)
	}
	want := []packageRevision{{Path: "git/example.invalid/owner/package", Commit: head}}
	if diff := cmp.Diff(want, decoded.Git); diff != "" {
		t.Fatal(diff)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("repository fsmonitor executed: %v", err)
	}
	if diff := cmp.Diff(first, p.ExtraSnapshotEntries(root, "")); diff != "" {
		t.Fatalf("unchanged revisions must have stable bytes: %s", diff)
	}
	packageWrite(t, filepath.Join(repo, "extension.ts"), "// modified locally\n")
	second := p.ExtraSnapshotEntries(root, "")
	if !second.Complete || !strings.Contains(string(second.Entries[0].Body), `"dirty":true`) {
		t.Fatalf("dirty source not recorded: %+v", second)
	}
	packageWrite(t, filepath.Join(repo, "extension.ts"), "// version one\n")
	packageWrite(t, filepath.Join(repo, "untracked.ts"), "// local extension\n")
	if got := p.ExtraSnapshotEntries(root, ""); !strings.Contains(string(got.Entries[0].Body), `"dirty":true`) {
		t.Fatal("untracked source not recorded as dirty")
	}
}

func TestPiPackageWorktreeAndSorting(t *testing.T) {
	p, home := packageFixture(t)
	repo := filepath.Join(home, "original")
	packageWrite(t, filepath.Join(repo, "file"), "a")
	packageCommand(t, repo, "init", "-q")
	packageCommand(t, repo, "add", "file")
	packageCommand(t, repo, "commit", "-qm", "one")
	for _, name := range []string{"z", "a"} {
		path := filepath.Join(p.CanonicalRoot(), "git", name)
		packageCommand(t, repo, "worktree", "add", "--detach", path)
	}
	got := p.ExtraSnapshotEntries(p.CanonicalRoot(), "")
	if !got.Complete || len(got.Entries) != 1 {
		t.Fatalf("worktree provenance: %+v", got)
	}
	var decoded struct {
		Git []packageRevision `json:"git"`
	}
	if err := json.Unmarshal(got.Entries[0].Body, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Git) != 2 || decoded.Git[0].Path != "git/a" || decoded.Git[1].Path != "git/z" {
		t.Fatalf("unsorted/missing worktrees: %+v", decoded.Git)
	}
}

func TestPiPackagePartialObservations(t *testing.T) {
	for _, kind := range []string{"alias", "invalid_repository", "walk_limit", "protected"} {
		t.Run(kind, func(t *testing.T) {
			p, home := packageFixture(t)
			top := filepath.Join(p.CanonicalRoot(), "git")
			switch kind {
			case "alias":
				if err := os.MkdirAll(filepath.Dir(top), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(home, top); err != nil {
					t.Fatal(err)
				}
			case "invalid_repository":
				packageWrite(t, filepath.Join(top, "bad", ".git"), "unreadable repository")
			case "walk_limit":
				for i := range packageWalkLimit + 1 {
					packageWrite(t, filepath.Join(top, "files", strconv.Itoa(i)), "")
				}
			case "protected":
				packageWrite(t, filepath.Join(top, "notes"), "ordinary")
				t.Setenv("AGENT_SESSIONS_STATE_DIR", top)
			}
			got := p.ExtraSnapshotEntries(p.CanonicalRoot(), "")
			if kind == "protected" {
				if !got.Complete || len(got.Entries) != 0 {
					t.Fatalf("excluded source was observed: %+v", got)
				}
			} else if got.Complete || len(got.Issues) != 1 || len(got.Entries) != 0 {
				t.Fatalf("failed observation claimed completeness: %+v", got)
			}
		})
	}
}

func TestPackageOutputBounded(t *testing.T) {
	out := &packageOutput{}
	for range 3 {
		data := []byte(strings.Repeat("x", 10000))
		n, err := out.Write(data)
		if n != len(data) || err != nil || len(out.body) != 128 {
			t.Fatalf("output not bounded: %d %v %d", n, err, len(out.body))
		}
	}
}
