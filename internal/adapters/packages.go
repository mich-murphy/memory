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
	status, err := passivePackageStatus(repo, id)
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
// A positive limit instead requires complete bounded output (used for object/index paths).
type packageOutput struct {
	body  []byte
	limit int
}

func (o *packageOutput) Write(p []byte) (int, error) {
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

func packageGit(dir string, args ...string) ([]byte, error) {
	return runPackageGit(dir, nil, 0, args...)
}

// Inherited Git identity/config overrides must not redirect an observation (or its tests).
func packageGitEnvironment(extra []string) []string {
	env := make([]string, 0, len(os.Environ())+len(extra)+4)
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "GIT_") {
			env = append(env, value)
		}
	}
	return append(append(env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_TERMINAL_PROMPT=0", "GIT_ATTR_NOSYSTEM=1"), extra...)
}

func runPackageGit(dir string, env []string, limit int, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	argv := append([]string{"--no-pager", "--no-optional-locks", "-c", "core.fsmonitor=false", "-c", "core.hooksPath=" + os.DevNull, "-C", dir}, args...)
	cmd := exec.CommandContext(ctx, "git", argv...)
	cmd.Env = packageGitEnvironment(env)
	out := &packageOutput{limit: limit}
	cmd.Stdout = out
	if err := cmd.Run(); err != nil {
		return nil, errors.New("package git observation failed")
	}
	return out.body, nil
}

// Status can execute clean/process filters even with hooks and fsmonitor disabled.
// Observe the original index/objects through a disposable, configuration-free repository:
// neither .git/config nor .git/info/attributes can select executable package code. Working
// tree attributes without configured filters are inert. Dirty describes raw bytes, not a
// package-defined clean-filter transformation. No original index/ref/config is written.
func passivePackageStatus(repo, commit string) (body []byte, err error) {
	objects, err := runPackageGit(repo, nil, 64<<10, "rev-parse", "--path-format=absolute", "--git-path", "objects")
	if err != nil {
		return nil, err
	}
	index, err := runPackageGit(repo, nil, 64<<10, "rev-parse", "--path-format=absolute", "--git-path", "index")
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "agent-sessions-git-")
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(dir)) }()
	for _, name := range []string{"objects", "refs"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0o700); err != nil {
			return nil, err
		}
	}
	configuration := "[core]\nrepositoryformatversion = 0\nbare = false\n"
	if len(commit) == 64 {
		configuration = "[core]\nrepositoryformatversion = 1\nbare = false\n[extensions]\nobjectformat = sha256\n"
	}
	for name, content := range map[string]string{"HEAD": commit + "\n", "config": configuration} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			return nil, err
		}
	}
	env := []string{
		"GIT_DIR=" + dir, "GIT_WORK_TREE=" + repo,
		"GIT_OBJECT_DIRECTORY=" + strings.TrimSuffix(string(objects), "\n"),
		"GIT_INDEX_FILE=" + strings.TrimSuffix(string(index), "\n"),
	}
	return runPackageGit(repo, env, 0, "status", "--porcelain", "--untracked-files=normal", "--ignore-submodules=all", "--no-renames")
}
