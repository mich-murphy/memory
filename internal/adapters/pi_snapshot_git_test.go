package adapters

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mich-murphy/memory/internal/capturepolicy"
	"github.com/mich-murphy/memory/internal/safefile"
)

func TestSnapshotGitReadsOnlyMetadata(t *testing.T) {
	repo, slot := t.TempDir(), t.TempDir()
	put := func(p, body string) {
		t.Helper()
		p = filepath.Join(repo, p)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	put(".git/HEAD", "ref: refs/heads/main\n")
	put(".git/refs/heads/main", strings.Repeat("a", 40)+"\n")
	put(".git/objects/aa/historical", "historical private body")
	put(".gitignore", "ignored/\n")
	for _, p := range []string{"tracked.env", "untracked.env", "ignored/private.env", "nested/AGENTS.md"} {
		put(p, "must not read")
	}
	opened := map[string]bool{}
	v := &snapshotGitView{ctx: t.Context(), repo: repo, dir: slot, authorize: func(string) error { return nil }, readFile: func(ctx context.Context, p string, f *os.File) ([]byte, error) {
		opened[relGit(repo, p)] = true
		st, err := f.Stat()
		if err != nil {
			return nil, err
		}
		return snapshotReadGitDescriptor(ctx, f, st)
	}}
	if err := v.prepare(true); err != nil {
		t.Fatal(err)
	}
	if err := filepath.WalkDir(repo, v.visit); err != nil {
		t.Fatal(err)
	}
	for p := range opened {
		if p != ".git/HEAD" && p != ".git/refs/heads/main" && p != ".gitignore" {
			t.Fatalf("unselected body opened: %s", p)
		}
	}
	if len(opened) != 3 {
		t.Fatal(opened)
	}
	b, err := os.ReadFile(filepath.Join(slot, "worktree", "nested", "AGENTS.md"))
	if err != nil || len(b) != 0 {
		t.Fatal("body was copied", err)
	}
	if _, err := os.Stat(filepath.Join(slot, "objects", "aa")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("historical objects copied")
	}
}
func relGit(repo, p string) string { rel, _ := filepath.Rel(repo, p); return filepath.ToSlash(rel) }

func TestSnapshotGitExplicitScratchAndBounds(t *testing.T) {
	repo, slot := t.TempDir(), t.TempDir()
	allow := func(string) error { return nil }
	if _, err := SnapshotRepository(t.Context(), repo, allow, ""); err == nil {
		t.Fatal("missing slot accepted")
	}
	if err := os.Chmod(slot, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte(strings.Repeat("a", 40)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := SnapshotRepository(ctx, repo, allow, slot); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(slot)
	if err != nil || len(entries) != 0 {
		t.Fatal("failed observation leaked scratch", err)
	}
	v := &snapshotGitView{size: 64 << 20}
	if err := v.charge(1); err == nil {
		t.Fatal("Git quota ignored")
	}
	v = &snapshotGitView{ctx: t.Context(), repo: repo, count: 12000}
	d, err := os.ReadDir(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.visit(filepath.Join(repo, d[0].Name()), d[0], nil); err == nil {
		t.Fatal("entry limit ignored")
	}
	v = &snapshotGitView{dirs: 5000}
	if err := v.directory(repo, slot); err == nil {
		t.Fatal("directory limit ignored")
	}
}

func TestSnapshotGitRejectsUnsafeHeadAndMetadata(t *testing.T) {
	for _, head := range []string{"ref: refs/../outside\n", "ref: refs/heads/main\n", strings.Repeat("b", 64) + "\n"} {
		t.Run(strings.TrimSpace(head), func(t *testing.T) {
			repo, slot := t.TempDir(), t.TempDir()
			git := filepath.Join(repo, ".git")
			if err := os.MkdirAll(git, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(git, "HEAD"), []byte(head), 0o600); err != nil {
				t.Fatal(err)
			}
			v := &snapshotGitView{ctx: t.Context(), repo: repo, dir: slot, authorize: func(string) error { return nil }}
			err := v.prepare(false)
			if strings.Contains(head, "..") {
				if err == nil {
					t.Fatal("unsafe ref accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(strings.TrimSpace(head)) == 64 && !v.sha256 {
				t.Fatal("SHA256 metadata not recognized")
			}
		})
	}
}

func TestPackageRevisionWalkFailureClassification(t *testing.T) {
	policy, err := capturepolicy.New("", "")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	w := &snapshotPackageWalk{ctx: t.Context(), policy: policy, root: root, authorize: func(string) error { return nil }}
	if err := w.visit(root, nil, errors.New("listing failed")); err == nil {
		t.Fatal("walk failure hidden")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	w.ctx = ctx
	if err := w.visit(root, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	w.ctx = t.Context()
	w.walked = snapshotPackageWalkLimit
	if err := w.visit(root, nil, nil); err == nil {
		t.Fatal("walk budget ignored")
	}
	w.walked = 0
	if err := os.WriteFile(filepath.Join(root, "file"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "node_modules"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "ordinary"), 0o700); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		err := w.visit(filepath.Join(root, entry.Name()), entry, nil)
		switch entry.Name() {
		case "alias":
			if err == nil {
				t.Fatal("package alias accepted")
			}
		case "node_modules":
			if !errors.Is(err, filepath.SkipDir) {
				t.Fatal(err)
			}
		default:
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestSnapshotIgnoreAuthorizationAncestorSwapNeverReadsOutside(t *testing.T) {
	repo, outside, slot := t.TempDir(), t.TempDir(), t.TempDir()
	sub := filepath.Join(repo, "sub")
	for _, dir := range []string{filepath.Join(repo, ".git"), sub} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for p, body := range map[string]string{filepath.Join(repo, ".git", "HEAD"): strings.Repeat("a", 40), filepath.Join(sub, ".gitignore"): "safe/\n", filepath.Join(outside, ".gitignore"): "OUTSIDE-UNIQUE-BODY\n"} {
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	readOutside := false
	swapped := false
	v := &snapshotGitView{ctx: t.Context(), repo: repo, dir: slot, authorize: func(p string) error {
		if p == filepath.Join(sub, ".gitignore") && !swapped {
			swapped = true
			if err := os.Rename(sub, sub+"-old"); err != nil {
				return err
			}
			return os.Symlink(outside, sub)
		}
		return nil
	}, readFile: func(ctx context.Context, _ string, f *os.File) ([]byte, error) {
		st, err := f.Stat()
		if err != nil {
			return nil, err
		}
		b, err := snapshotReadGitDescriptor(ctx, f, st)
		if strings.Contains(string(b), "OUTSIDE-UNIQUE-BODY") {
			readOutside = true
		}
		return b, err
	}}
	if err := v.prepare(true); err != nil {
		t.Fatal(err)
	}
	if err := safefile.Walk(t.Context(), repo, 12000, 8, v.visit); err != nil {
		t.Fatal(err)
	}
	if !swapped || readOutside || len(v.pruned) != 1 {
		t.Fatal("ancestor swap was not safely pruned")
	}
	if _, err := os.Stat(filepath.Join(slot, "worktree", "sub", ".gitignore")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("outside ignore copied", err)
	}
}
