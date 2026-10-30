package adapters

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mich-murphy/memory/internal/capturepolicy"
	"github.com/mich-murphy/memory/internal/safefile"
)

// SnapshotRepository creates an index/ignore/filename-only selection view. It
// never reads working-tree bodies (except ignore rules), or historical objects.
// workingDirectory is the caller's private, quota-reserved preparation slot.
func SnapshotRepository(ctx context.Context, repo string, authorize func(string) error, workingDirectory string) (string, error) {
	dir, pruned, err := SnapshotRepositoryPartial(ctx, repo, authorize, workingDirectory)
	if err == nil && len(pruned) > 0 {
		err = errors.Join(errors.New("snapshot: pruned Git selection"), os.RemoveAll(dir))
		dir = ""
	}
	return dir, err
}

// SnapshotRepositoryPartial retains unrelated selections when ignore rules are
// unavailable. Callers MUST filter all index results under pruned directories and
// report partial coverage. No rules from a denied subtree are ignored as if empty.
func SnapshotRepositoryPartial(ctx context.Context, repo string, authorize func(string) error, workingDirectory string) (dir string, pruned []string, err error) {
	return snapshotRepositoryView(ctx, repo, authorize, workingDirectory, true)
}

func snapshotRepository(ctx context.Context, repo string, authorize func(string) error, workingDirectory string, selection bool) (string, error) {
	dir, _, err := snapshotRepositoryView(ctx, repo, authorize, workingDirectory, selection)
	return dir, err
}

func snapshotRepositoryView(ctx context.Context, repo string, authorize func(string) error, workingDirectory string, selection bool) (dir string, pruned []string, err error) {
	if workingDirectory == "" {
		return "", nil, errors.New("snapshot: Git requires WorkingDirectory")
	}
	st, err := os.Lstat(workingDirectory)
	if err != nil {
		return "", nil, err
	}
	if !st.IsDir() || st.Mode().Perm() != 0o700 {
		return "", nil, errors.New("snapshot: working directory must be private")
	}
	dir, err = os.MkdirTemp(workingDirectory, "git-")
	if err != nil {
		return "", nil, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, os.RemoveAll(dir))
		}
	}()
	v := &snapshotGitView{ctx: ctx, repo: repo, dir: dir, authorize: authorize}
	if err := validateSnapshotGit(repo); err != nil {
		return dir, v.pruned, err
	}
	err = v.prepare(selection)
	if err == nil && selection {
		err = safefile.Walk(ctx, repo, 12000, 8, v.visit)
	}
	return dir, v.pruned, err
}

func validateSnapshotGit(repo string) error {
	git := filepath.Join(repo, ".git")
	st, err := os.Lstat(git)
	if err != nil {
		return err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return errors.New("snapshot: external git directory unsupported")
	}
	for _, sub := range []string{"objects", "objects/info", "refs", "info"} {
		info, e := os.Lstat(filepath.Join(git, filepath.FromSlash(sub)))
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			return e
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("snapshot: external git store unsupported")
		}
	}
	for _, name := range []string{"commondir", "gitdir", "objects/info/alternates", "objects/info/http-alternates"} {
		if _, e := os.Lstat(filepath.Join(git, filepath.FromSlash(name))); !errors.Is(e, os.ErrNotExist) {
			return errors.New("snapshot: external git store unsupported")
		}
	}
	return nil
}

func (v *snapshotGitView) prepare(selection bool) error {
	dir := v.dir

	if err := os.MkdirAll(filepath.Join(dir, "objects"), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, "refs"), 0o700); err != nil {
		return err
	}
	head, err := v.metadata("HEAD", true)
	if err != nil {
		return err
	}
	if ref, ok := strings.CutPrefix(strings.TrimSpace(string(head)), "ref: "); ok {
		if !strings.HasPrefix(ref, "refs/") || strings.Contains(ref, "..") || strings.ContainsAny(ref, "\\\x00\n\r") {
			return errors.New("snapshot: invalid HEAD ref")
		}
		if _, err = v.metadata(ref, false); err != nil {
			return err
		}
	}
	if _, err = v.metadata("packed-refs", false); err != nil {
		return err
	}
	if selection {
		if _, err = v.metadata("index", false); err != nil {
			return err
		}
		if _, err = v.metadata("info/exclude", false); err != nil {
			return err
		}
	}
	configuration := "[core]\nrepositoryformatversion = 0\nbare = false\n"
	if v.sha256 {
		configuration = "[core]\nrepositoryformatversion = 1\nbare = false\n[extensions]\nobjectformat = sha256\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte(configuration), 0o600); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, "worktree"), 0o700); err != nil {
		return err
	}
	return nil
}

// SnapshotRepositoryGit runs only against the private metadata/filename view.
func SnapshotRepositoryGit(ctx context.Context, _, dir string, limit int, args ...string) ([]byte, error) {
	return snapshotRunPackageGit(ctx, filepath.Join(dir, "worktree"), []string{"GIT_DIR=" + dir, "GIT_WORK_TREE=" + filepath.Join(dir, "worktree")}, limit, args...)
}

type snapshotGitView struct {
	ctx         context.Context
	repo, dir   string
	authorize   func(string) error
	count, dirs int
	size        int64
	sha256      bool
	pruned      []string
	readFile    func(context.Context, string, *os.File) ([]byte, error)
}

func (v *snapshotGitView) metadata(name string, required bool) ([]byte, error) {
	p := filepath.Join(v.repo, ".git", filepath.FromSlash(name))
	st, err := os.Lstat(p)
	if errors.Is(err, os.ErrNotExist) && !required {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, errors.New("snapshot: nonregular Git metadata")
	}
	// Check every intermediate component without following aliases.
	for parent := filepath.Dir(p); parent != v.repo; parent = filepath.Dir(parent) {
		info, e := os.Lstat(parent)
		if e != nil {
			return nil, e
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("snapshot: git metadata alias")
		}
	}
	body, err := v.authorizedRead(p, st)
	if err != nil {
		return nil, err
	}
	if err := v.charge(int64(len(body))); err != nil {
		return nil, err
	}
	v.observeObjectFormat(name, body)
	dest := filepath.Join(v.dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return nil, err
	}
	return body, os.WriteFile(dest, body, 0o600)
}

func (v *snapshotGitView) charge(n int64) error {
	v.size += n
	if v.size > 64<<20 {
		return errors.New("snapshot: git input byte capacity exceeded")
	}
	return nil
}

func (v *snapshotGitView) visit(p string, d fs.DirEntry, walkErr error) error {
	if walkErr != nil {
		return walkErr
	}
	if err := v.ctx.Err(); err != nil {
		return err
	}
	v.count++
	if v.count > 12000 {
		return errors.New("snapshot: git input entry capacity exceeded")
	}
	rel, err := filepath.Rel(v.repo, p)
	if err != nil {
		return err
	}
	if rel == ".git" {
		return filepath.SkipDir
	}
	if err := v.charge(int64(len(rel))); err != nil {
		return err
	}
	dest := filepath.Join(v.dir, "worktree", rel)
	if d.IsDir() {
		return v.directory(p, dest)
	}
	return v.filename(p, d, dest)
}

func (v *snapshotGitView) directory(p, dest string) error {
	v.dirs++
	if v.dirs > 5000 {
		return errors.New("snapshot: git directory capacity exceeded")
	}
	// Denied subtrees do not erase unrelated safe selections.
	if err := v.authorize(p); err != nil {
		if p == v.repo {
			return err
		}
		v.pruned = append(v.pruned, p)
		return filepath.SkipDir
	}
	// Preflight ignore rules before enumerating any children: a denied rule
	// prunes its whole scope, including indexed files filtered by the consumer.
	body, exists, err := v.ignoreRule(p)
	if err != nil {
		if p == v.repo {
			return err
		}
		v.pruned = append(v.pruned, p)
		return filepath.SkipDir
	}
	if err := v.charge(int64(len(body))); err != nil {
		return err
	}
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return err
	}
	if exists {
		return os.WriteFile(filepath.Join(dest, ".gitignore"), body, 0o600)
	}
	return nil
}

func (v *snapshotGitView) ignoreRule(dir string) ([]byte, bool, error) {
	p := filepath.Join(dir, ".gitignore")
	st, err := os.Lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	body, err := v.authorizedRead(p, st)
	return body, true, err
}

func (v *snapshotGitView) filename(_ string, d fs.DirEntry, dest string) error {
	if d.Name() == ".gitignore" {
		return nil
	} // already authorized by directory preflight
	var body []byte
	// Filename placeholders only. Actual declared bodies are independently
	// authorized and read by the snapshot collector after Git selection.
	return os.WriteFile(dest, body, 0o600)
}

func (v *snapshotGitView) authorizedRead(p string, expected fs.FileInfo) ([]byte, error) {
	r, err := capturepolicy.Resolve(p)
	if err != nil {
		return nil, err
	}
	// Identity is sampled before the callback, never via DirEntry.Info afterward.
	st, err := os.Stat(r.Path)
	if err != nil {
		return nil, err
	}
	if expected.Mode()&os.ModeSymlink == 0 && !os.SameFile(st, expected) {
		return nil, errors.New("snapshot: input replaced")
	}
	if err := v.authorize(p); err != nil {
		return nil, err
	}
	// The callback's own resolution may differ if an alias moves during
	// authorization. Independently authorize every identity from OUR resolution,
	// including the canonical coordinate subsequently bound by OpenResolved.
	seen := map[string]bool{p: true}
	for _, identity := range r.Identities {
		if seen[identity] {
			continue
		}
		seen[identity] = true
		if err := v.authorize(identity); err != nil {
			return nil, err
		}
	}
	f, err := safefile.OpenResolved(v.ctx, r.Path, st)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // read-only
	if v.readFile != nil {
		return v.readFile(v.ctx, p, f)
	}
	return snapshotReadGitDescriptor(v.ctx, f, st)
}

func snapshotReadGitDescriptor(ctx context.Context, f *os.File, expected fs.FileInfo) ([]byte, error) {
	if expected.Size() > 5<<20 {
		return nil, errors.New("snapshot: git input file capacity exceeded")
	}
	body := make([]byte, 0, min(expected.Size(), 5<<20))
	buffer := make([]byte, 32<<10)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, err := f.Read(buffer)
		if len(body)+n > 5<<20 {
			return nil, errors.New("snapshot: git file capacity exceeded")
		}
		body = append(body, buffer[:n]...)
		if errors.Is(err, io.EOF) {
			return body, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

func (v *snapshotGitView) observeObjectFormat(sub string, body []byte) {
	if sub != "HEAD" && sub != "packed-refs" && !strings.HasPrefix(sub, "refs"+string(filepath.Separator)) {
		return
	}
	for line := range strings.SplitSeq(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || len(fields[0]) != 64 {
			continue
		}
		if _, err := hex.DecodeString(fields[0]); err == nil {
			v.sha256 = true
		}
	}
}
