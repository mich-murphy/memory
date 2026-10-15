package adapters

import (
	"context"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mich-murphy/memory/internal/capturepolicy"
	"github.com/mich-murphy/memory/internal/config"
	"github.com/mich-murphy/memory/internal/jsonx"
)

const packageWalkLimit = 5000

// Rules includes npm's exact resolved versions, not just the ranges in settings.json.
func (p *Pi) Rules() SnapshotRules {
	rules := p.layout.Rules()
	rules.User = slices.Concat(rules.User, []string{"npm/package.json", "npm/package-lock.json"})
	return rules
}

type packageRevision struct {
	Path   string `json:"path"`
	Commit string `json:"commit"`
	Dirty  bool   `json:"dirty"`
}

// ExtraSnapshotEntries records locally checked-out revisions without archiving .git, fetching
// packages, or invoking their code. npm's lockfile is captured through the normal snapshot path.
func (p *Pi) ExtraSnapshotEntries(root, _ string) Sources {
	out := Sources{Complete: true}
	top := filepath.Join(p.UserBase(root), "git")
	if _, err := os.Lstat(top); errors.Is(err, os.ErrNotExist) {
		return out
	}
	policy, err := capturepolicy.New(config.FilePath(), config.StateDir())
	if err != nil {
		out.issue(top, "package_policy", err)
		return out
	}
	walk := &packageWalk{pi: p, root: root, policy: policy}
	err = filepath.WalkDir(top, walk.visit)
	revisions := walk.revisions
	if err != nil {
		// Git/config/read errors may contain private remote URLs; report no subprocess output.
		out.issue(top, "package_revision", errors.New("local package revisions could not be completely observed"))
	}
	if len(revisions) == 0 {
		return out
	}
	slices.SortFunc(revisions, func(a, b packageRevision) int { return strings.Compare(a.Path, b.Path) })
	body, err := jsonx.SortedCompact(struct {
		Git []packageRevision `json:"git"`
	}{Git: revisions})
	if err != nil {
		out.issue(top, "package_revision", err)
		return out
	}
	out.Entries = append(out.Entries, Entry{Scope: "user", Name: "pi-package-provenance.json", Body: body})
	return out
}

// packageWalk stops at repository boundaries, so status observes revisions without walking
// package source trees, node_modules, or arbitrary symlinks inside installed packages.
type packageWalk struct {
	pi        *Pi
	root      string
	policy    *capturepolicy.Policy
	walked    int
	revisions []packageRevision
}

func (w *packageWalk) visit(path string, entry fs.DirEntry, walkErr error) error {
	if walkErr != nil {
		return walkErr
	}
	w.walked++
	if w.walked > packageWalkLimit {
		return errors.New("package walk limit exceeded")
	}
	resolution, err := capturepolicy.Resolve(path)
	if w.policy.Excludes(resolution.Identities) {
		if entry.IsDir() {
			return filepath.SkipDir
		}
		return nil
	}
	if err != nil {
		return errors.New("package path could not be resolved")
	}
	if entry.Type()&os.ModeSymlink != 0 {
		return errors.New("package alias not traversed; revision observation incomplete")
	}
	if !entry.IsDir() {
		return nil
	}
	if skipDirNames[entry.Name()] {
		return filepath.SkipDir
	}
	if _, err := os.Lstat(filepath.Join(path, ".git")); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	revision, err := w.pi.packageRevision(w.root, path)
	if err != nil {
		return err
	}
	w.revisions = append(w.revisions, revision)
	return filepath.SkipDir
}

func (p *Pi) packageRevision(root, repo string) (packageRevision, error) {
	commit, err := packageGit(repo, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return packageRevision{}, err
	}
	id := strings.TrimSpace(string(commit))
	if _, err := hex.DecodeString(id); err != nil || len(id) != 40 && len(id) != 64 {
		return packageRevision{}, errors.New("invalid package commit identity")
	}
	status, err := packageGit(repo, "status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		return packageRevision{}, err
	}
	rel, err := filepath.Rel(p.UserBase(root), repo)
	if err != nil {
		return packageRevision{}, err
	}
	return packageRevision{Path: filepath.ToSlash(rel), Commit: id, Dirty: len(status) > 0}, nil
}

// Git status can print arbitrarily many filenames; only its nonemptiness is needed.
type packageOutput struct{ body []byte }

func (o *packageOutput) Write(p []byte) (int, error) {
	o.body = append(o.body, p[:min(len(p), 128-len(o.body))]...)
	return len(p), nil
}

func packageGit(dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// In particular, disable fsmonitor: status must not execute a package-supplied command.
	argv := append([]string{"--no-optional-locks", "-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null", "-C", dir}, args...)
	cmd := exec.CommandContext(ctx, "git", argv...)
	out := &packageOutput{}
	cmd.Stdout = out
	if err := cmd.Run(); err != nil {
		return nil, errors.New("package git observation failed")
	}
	return out.body, nil
}
