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
	"github.com/mich-murphy/memory/internal/safefile"

	"github.com/mich-murphy/memory/internal/jsonx"
)

const snapshotPackageWalkLimit = 5000

type snapshotPackageRevision struct {
	Path   string `json:"path"`
	Commit string `json:"commit"`
	Dirty  *bool  `json:"dirty"`
}

// ExtraSnapshotEntries records locally checked-out revisions without archiving .git, fetching
// packages, or invoking their code. npm's lockfile is captured through the normal snapshot path.
func (p *Pi) SnapshotPackages(ctx context.Context, userBase string, policy *capturepolicy.Policy, workingDirectory string, authorize func(string) error) Sources {
	out := Sources{Complete: true}
	top := filepath.Join(userBase, "git")
	if _, err := os.Lstat(top); errors.Is(err, os.ErrNotExist) {
		return out
	}
	walk := &snapshotPackageWalk{pi: p, root: userBase, policy: policy, ctx: ctx, authorize: authorize, workingDirectory: workingDirectory}
	err := safefile.Walk(ctx, top, snapshotPackageWalkLimit, 8, walk.visit)
	revisions := walk.revisions
	if len(revisions) > 0 {
		out.issue(top, "package_dirty_unknown", errors.New("dirty state not observed without source-body authorization"))
	}
	if err != nil {
		// Git/config/read errors may contain private remote URLs; report no subprocess output.
		out.issue(top, "package_revision", errors.New("local package revisions could not be completely observed"))
	}
	if len(revisions) == 0 {
		return out
	}
	slices.SortFunc(revisions, func(a, b snapshotPackageRevision) int { return strings.Compare(a.Path, b.Path) })
	body, err := jsonx.SortedCompact(struct {
		Git []snapshotPackageRevision `json:"git"`
	}{Git: revisions})
	if err != nil {
		out.issue(top, "package_revision", err)
		return out
	}
	out.Entries = append(out.Entries, Entry{Scope: "user", Name: "pi-package-provenance.json", Body: body})
	return out
}

// snapshotPackageWalk stops at repository boundaries, so status observes revisions without walking
// package source trees, node_modules, or arbitrary symlinks inside installed packages.
type snapshotPackageWalk struct {
	ctx              context.Context
	authorize        func(string) error
	workingDirectory string
	pi               *Pi
	root             string
	policy           *capturepolicy.Policy
	walked           int
	revisions        []snapshotPackageRevision
}

func (w *snapshotPackageWalk) visit(path string, entry fs.DirEntry, walkErr error) error {
	if err := w.ctx.Err(); err != nil {
		return err
	}
	if walkErr != nil {
		return walkErr
	}
	w.walked++
	if w.walked > snapshotPackageWalkLimit {
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
	dir, err := snapshotRepository(w.ctx, path, w.authorize, w.workingDirectory, false)
	if err != nil {
		return err
	}
	// Cleanup failures are returned as incomplete provenance.
	revision, err := w.pi.snapshotPackageRevision(w.ctx, w.root, path, dir)
	err = errors.Join(err, os.RemoveAll(dir))
	if err != nil {
		return err
	}
	w.revisions = append(w.revisions, revision)
	return filepath.SkipDir
}

func (p *Pi) snapshotPackageRevision(ctx context.Context, root, repo, dir string) (snapshotPackageRevision, error) {
	commit, err := SnapshotRepositoryGit(ctx, repo, dir, 128, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return snapshotPackageRevision{}, err
	}
	id := strings.TrimSpace(string(commit))
	if _, err := hex.DecodeString(id); err != nil || len(id) != 40 && len(id) != 64 {
		return snapshotPackageRevision{}, errors.New("invalid package commit identity")
	}
	rel, err := filepath.Rel(root, repo)
	if err != nil {
		return snapshotPackageRevision{}, err
	}
	return snapshotPackageRevision{Path: filepath.ToSlash(rel), Commit: id, Dirty: nil}, nil
}

// Git status can print arbitrarily many filenames; only its nonemptiness is needed.
// A positive limit instead requires complete bounded output (used for object/index paths).
type snapshotPackageOutput struct {
	body  []byte
	limit int
}

func (o *snapshotPackageOutput) Write(p []byte) (int, error) {
	limit := 128
	if o.limit > 0 {
		limit = o.limit
		if len(p) > limit-len(o.body) {
			return 0, errors.New("package observation output limit exceeded")
		}
	}
	o.body = append(o.body, p[:min(len(p), limit-len(o.body))]...)
	return len(p), nil
}

// Inherited Git identity/config overrides must not redirect an observation (or its tests).
func snapshotPackageGitEnvironment(extra []string) []string {
	env := make([]string, 0, len(os.Environ())+len(extra)+4)
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "GIT_") {
			env = append(env, value)
		}
	}
	return append(append(env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_TERMINAL_PROMPT=0", "GIT_ATTR_NOSYSTEM=1"), extra...)
}

func snapshotRunPackageGit(parent context.Context, dir string, env []string, limit int, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	argv := append([]string{"--no-pager", "--no-optional-locks", "-c", "core.fsmonitor=false", "-c", "core.hooksPath=" + os.DevNull, "-C", dir}, args...)
	cmd := exec.CommandContext(ctx, "git", argv...)
	cmd.Env = snapshotPackageGitEnvironment(env)
	out := &snapshotPackageOutput{limit: limit}
	cmd.Stdout = out
	if err := cmd.Run(); err != nil {
		return nil, errors.New("package git observation failed")
	}
	return out.body, nil
}

func SnapshotLegacyGit(ctx context.Context, dir string, limit int, args ...string) ([]byte, error) {
	return snapshotRunPackageGit(ctx, dir, nil, limit, args...)
}
