// Package snapshot records which instructions, skills, settings and scripts an agent had for a
// project, as content-addressed sets. The set format and its scopes are in
// docs/storage-layout.md.
package snapshot

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/capturepolicy"
	"github.com/mich-murphy/memory/internal/config"
	"github.com/mich-murphy/memory/internal/exclusion"
	"github.com/mich-murphy/memory/internal/fsutil"
	"github.com/mich-murphy/memory/internal/jsonx"
	"github.com/mich-murphy/memory/internal/raw"
	"github.com/mich-murphy/memory/internal/scrub"
)

const (
	maxFileSize      = 5 << 20
	maxFilesPerScope = 3000
	maxWalkDirs      = 5000
	maxImportDepth   = 5
	maxSnapshotSize  = 64 << 20
	maxWalkFiles     = 12000
	maxSkippedFiles  = 3000
)

var (
	skipDirs      = map[string]bool{".git": true, "node_modules": true, ".venv": true, "venv": true, "__pycache__": true, "dist": true, "build": true, ".next": true, "target": true, ".cache": true}
	ancestorNames = []string{"CLAUDE.md", "CLAUDE.local.md", "AGENTS.md", "AGENTS.override.md"}
	settingsNames = map[string]bool{"settings.json": true, "settings.local.json": true, "hooks.json": true, "config.toml": true, "managed-settings.json": true}
)

type entry struct {
	scope string
	path  string
	body  []byte
	// mtime is zero for entries that are not files on disk.
	mtime    float64
	target   string
	realPath string
}

type SkippedFile struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
	Scope  string `json:"scope"`
	Size   int64  `json:"size"`
	Count  int    `json:"count,omitempty"`
}

type scopedName struct{ scope, name string }

type budget struct {
	held   int64
	walked int
	listed int
}

type collected struct {
	budget    *budget
	agent     string
	entries   []*entry
	skipped   []SkippedFile
	counts    map[string]int
	seen      map[scopedName]bool
	policy    *capturepolicy.Policy
	policyErr error
}

func newCollected(agent string) *collected {
	policy, err := capturepolicy.New(config.FilePath(), config.StateDir())
	return &collected{
		budget: &budget{}, agent: agent, skipped: []SkippedFile{}, counts: map[string]int{}, seen: map[scopedName]bool{},
		policy: policy, policyErr: err,
	}
}

func (c *collected) add(scope, path, name string) *entry {
	if c.seen[scopedName{scope, name}] {
		return nil
	}
	if c.policyErr != nil {
		c.skip(scope, name, "policy_error", 0)
		return nil
	}
	resolution, err := capturepolicy.Resolve(path)
	if c.policy.Excludes(resolution.Identities) {
		return nil
	}
	if err != nil {
		c.skip(scope, name, "resolve_error", 0)
		return nil
	}
	resolved := resolution.Path
	abs, err := filepath.Abs(path)
	if err != nil {
		c.skip(scope, name, "resolve_error", 0)
		return nil
	}
	st, err := os.Stat(resolved)
	if err != nil {
		c.skip(scope, name, "stat_error", 0)
		return nil
	}
	if !st.Mode().IsRegular() {
		c.skip(scope, name, "non_regular", st.Size())
		return nil
	}
	if c.counts[scope] >= maxFilesPerScope {
		c.skip(scope, name, "file_limit", st.Size())
		return nil
	}
	if st.Size() > maxFileSize {
		c.skip(scope, name, "too_large", st.Size())
		return nil
	}
	if st.Size() > maxSnapshotSize-c.budget.held {
		c.skip(scope, name, "memory_limit", st.Size())
		return nil
	}
	body, err := readBounded(resolved, min(int64(maxFileSize), int64(maxSnapshotSize)-c.budget.held))
	if err != nil {
		c.skip(scope, name, "read_error", st.Size())
		return nil
	}
	if len(body) > maxFileSize {
		c.skip(scope, name, "too_large", int64(len(body)))
		return nil
	}
	if int64(len(body)) > maxSnapshotSize-c.budget.held {
		c.skip(scope, name, "memory_limit", int64(len(body)))
		return nil
	}
	if body, err = scrub.Identities(c.agent, resolution.Identities, body); err != nil {
		c.skip(scope, name, "unparseable", st.Size())
		return nil
	}
	if int64(len(body)) > maxSnapshotSize-c.budget.held {
		c.skip(scope, name, "memory_limit", int64(len(body)))
		return nil
	}
	c.budget.held += int64(len(body))
	c.seen[scopedName{scope, name}] = true
	c.counts[scope]++
	e := &entry{scope: scope, path: name, body: body, mtime: float64(st.ModTime().UnixNano()) / 1e9, realPath: resolved}
	if resolved != abs {
		e.target = resolved
	}
	c.entries = append(c.entries, e)
	return e
}

func (c *collected) skip(scope, name, reason string, size int64) {
	// Diagnostics must not consume unbounded memory after a scope reaches its file budget.
	if len(c.skipped) >= maxSkippedFiles {
		if len(c.skipped) == maxSkippedFiles {
			c.skipped = append(c.skipped, SkippedFile{Reason: "report_limit"})
		}
		c.skipped[maxSkippedFiles].Count++
		return
	}
	c.skipped = append(c.skipped, SkippedFile{Scope: scope, Path: name, Reason: reason, Size: size})
}

// Reads cannot exceed the remaining aggregate budget even if a file grows after stat.
func readBounded(path string, limit int64) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // read-only
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, errors.New("snapshot: source is not a regular file")
	}
	return io.ReadAll(io.LimitReader(f, limit+1))
}

func (c *collected) addOptional(scope, path, name string) {
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return
	}
	c.add(scope, path, name)
}

func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		if a, err := filepath.Abs(r); err == nil {
			return a
		}
		return r
	}
	a, _ := filepath.Abs(p)
	return a
}

func findProjectRoot(cwd string) (root string, isRepo bool) {
	for d := cwd; ; d = filepath.Dir(d) {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return d, true
		}
		if d == filepath.Dir(d) {
			return cwd, false
		}
	}
}

func (c *collected) walkFiles(scope, top string, follow bool) []string {
	// Optional roots are normal when absent; disappearance after traversal starts is an issue.
	if _, err := os.Lstat(top); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	var out []string
	visited := map[string]bool{}
	queue := []string{top}
	for len(queue) > 0 {
		if c.budget.walked >= maxWalkDirs || c.budget.listed >= maxWalkFiles {
			c.skip(scope, top, "walk_limit", 0)
			break
		}
		dir := queue[0]
		queue = queue[1:]
		resolved := realPath(dir)
		if visited[resolved] {
			continue
		}
		visited[resolved] = true
		c.budget.walked++
		subdirs, files := c.listDir(scope, dir, follow, maxWalkFiles-c.budget.listed)
		out = append(out, files...)
		if len(queue)+len(subdirs) > maxWalkDirs {
			c.skip(scope, top, "walk_limit", 0)
			subdirs = subdirs[:max(0, maxWalkDirs-len(queue))]
		}
		queue = append(queue, subdirs...)
	}
	return out
}

func (c *collected) listDir(scope, dir string, follow bool, limit int) (subdirs, files []string) {
	f, err := os.Open(dir)
	if err != nil {
		c.skip(scope, dir, "walk_error", 0)
		return nil, nil
	}
	defer func() { _ = f.Close() }() // read-only directory
	for {
		entries, err := f.ReadDir(128)
		if err != nil && !errors.Is(err, io.EOF) {
			c.skip(scope, dir, "walk_error", 0)
			return subdirs, files
		}
		for _, d := range entries {
			c.budget.listed++
			p := filepath.Join(dir, d.Name())
			isDir, include := c.walkEntry(scope, p, d, follow)
			if !include {
				continue
			}
			if isDir {
				subdirs = append(subdirs, p)
			} else {
				files = append(files, p)
			}
			if len(files) >= limit || len(subdirs) >= maxWalkDirs || c.budget.listed >= maxWalkFiles {
				c.skip(scope, dir, "walk_limit", 0)
				return subdirs, files
			}
		}
		if errors.Is(err, io.EOF) {
			return subdirs, files
		}
	}
}

// walkEntry follows directory symlinks only in scopes that permit it, but preserves
// other entries for add's regular-file check and explicit failure reporting.
func (c *collected) walkEntry(scope, path string, d os.DirEntry, follow bool) (isDir, include bool) {
	isDir = d.IsDir()
	if d.Type()&os.ModeSymlink != 0 {
		st, err := os.Stat(path)
		if err != nil {
			c.skip(scope, path, "stat_error", 0)
			return false, false
		}
		isDir = st.IsDir()
		if isDir && !follow {
			return true, false
		}
	}
	return isDir, !isDir || !skipDirs[d.Name()]
}

func (c *collected) match(scope, base, pattern string, recursiveOK bool) []string {
	if top, ok := strings.CutSuffix(pattern, "/**"); ok {
		return c.walkFiles(scope, filepath.Join(base, top), true)
	}
	if name, ok := strings.CutPrefix(pattern, "**/"); ok && !strings.Contains(name, "/") {
		if !recursiveOK {
			return nil
		}
		var out []string
		for _, p := range c.walkFiles(scope, base, false) {
			if m, _ := filepath.Match(name, filepath.Base(p)); m {
				out = append(out, p)
			}
		}
		return out
	}
	matches, err := filepath.Glob(filepath.Join(base, filepath.FromSlash(pattern)))
	if err != nil {
		c.skip(scope, pattern, "walk_error", 0)
	}
	slices.Sort(matches)
	return slices.DeleteFunc(matches, func(p string) bool {
		rel, err := filepath.Rel(base, p)
		return err != nil || slices.ContainsFunc(strings.Split(filepath.ToSlash(rel), "/"), func(part string) bool { return skipDirs[part] })
	})
}

func relSlash(path, base string) string {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}

type scopeBase struct {
	scope       string
	base        string
	patterns    []string
	recursiveOK bool
}

func collect(a adapters.Adapter, root, projectRoot string, isRepo bool, shared *budget, withheld bool) *collected {
	c := newCollected(a.Name())
	c.budget = shared
	home := a.Home()
	if fsutil.IsDir(home) {
		home = realPath(home)
	}
	rules := a.Rules()
	scopes := []scopeBase{
		{"user", a.UserBase(root), rules.User, true},
		{"home", a.Home(), rules.Home, false},
	}
	if projectRoot != "" {
		resolved := realPath(projectRoot)
		searchSubdirs := isRepo && resolved != home && resolved != "/"
		scopes = append(scopes, scopeBase{"project", projectRoot, rules.Project, searchSubdirs})
	}
	for _, s := range scopes {
		if withheld && s.scope == "user" {
			c.skip(s.scope, s.base, "session_exclusion", 0)
			continue
		}
		c.addPatterns(s)
	}
	if projectRoot != "" && projectRoot != filepath.Dir(projectRoot) {
		c.addAncestors(projectRoot)
	}
	for _, p := range a.ManagedSettingsFiles() {
		c.addOptional("managed", p, p)
	}
	plugins := a.Plugins(root)
	c.sourceIssues("plugin", plugins.Issues)
	for _, plugin := range plugins.Plugins {
		for _, p := range c.walkFiles("plugin", plugin.Dir, true) {
			c.add("plugin", p, plugin.Name+"/"+relSlash(p, plugin.Dir))
		}
	}
	addScripts(c, home, projectRoot)
	addImports(c, home)
	extras := []adapters.Entry(nil)
	if !withheld {
		sources := a.ExtraSnapshotEntries(root, projectRoot)
		c.sourceIssues("user", sources.Issues)
		extras = sources.Entries
	}
	for _, e := range extras {
		if int64(len(e.Body)) > maxSnapshotSize-c.budget.held {
			c.skip(e.Scope, e.Name, "memory_limit", int64(len(e.Body)))
			continue
		}
		c.budget.held += int64(len(e.Body))
		c.entries = append(c.entries, &entry{scope: e.Scope, path: e.Name, body: e.Body})
	}
	sortEntries(c.entries)
	return c
}

func (c *collected) sourceIssues(scope string, issues []adapters.Issue) {
	for _, issue := range issues {
		c.skip(scope, issue.Path, issue.Kind+"_error", 0)
	}
}

func (c *collected) addPatterns(s scopeBase) {
	for _, pattern := range s.patterns {
		for _, p := range c.match(s.scope, s.base, pattern, s.recursiveOK) {
			c.add(s.scope, p, relSlash(p, s.base))
		}
	}
}

func (c *collected) addAncestors(projectRoot string) {
	for d := filepath.Dir(projectRoot); ; d = filepath.Dir(d) {
		for _, name := range ancestorNames {
			p := filepath.Join(d, name)
			c.addOptional("ancestor", p, p)
		}
		if d == filepath.Dir(d) {
			return
		}
	}
}

func addScripts(c *collected, home, projectRoot string) {
	for _, e := range slices.Clone(c.entries) {
		if !settingsNames[filepath.Base(e.path)] {
			continue
		}
		for _, command := range commands(parseSettings(e), false) {
			for _, p := range scriptPaths(command, home, projectRoot) {
				c.add("script", p, realPath(p))
			}
		}
	}
}

func addImports(c *collected, home string) {
	var frontier []*entry
	for _, e := range c.entries {
		switch e.scope {
		case "user", "project", "ancestor", "managed":
			frontier = append(frontier, e)
		}
	}
	for range maxImportDepth {
		var added []*entry
		for _, e := range frontier {
			for _, p := range imports(e, home) {
				if ne := c.add("import", p, realPath(p)); ne != nil {
					added = append(added, ne)
				}
			}
		}
		frontier = added
	}
}

func sortEntries(entries []*entry) {
	slices.SortStableFunc(entries, func(a, b *entry) int {
		if c := strings.Compare(a.scope, b.scope); c != 0 {
			return c
		}
		return strings.Compare(a.path, b.path)
	})
}

func collectMemory(a adapters.Adapter, root, projectRoot, sessionKey string, shared *budget) *collected {
	c := newCollected(a.Name())
	c.budget = shared
	for _, d := range a.MemoryDirs(root, projectRoot, sessionKey) {
		for _, p := range c.walkFiles(d.Scope, d.Dir, true) {
			c.add(d.Scope, p, p)
		}
	}
	sortEntries(c.entries)
	return c
}

// Set is the document stored at snapshots/sets/<ID>.json; ID is the sha256 of its compact form.
type Set struct {
	ID          string        `json:"-"`
	Complete    bool          `json:"complete"`
	Kind        string        `json:"kind"`
	Agent       string        `json:"agent"`
	UserBase    string        `json:"user_base"`
	ProjectRoot *string       `json:"project_root"`
	Files       []File        `json:"files"`
	Skipped     []SkippedFile `json:"skipped"`
}

type File struct {
	Scope  string `json:"scope"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int    `json:"size"`
	Target string `json:"target,omitempty"`
}

type Snapshot struct {
	Complete bool
	Harness  *Set
	// Memory is nil when the agent kept no memory for the session.
	Memory *Set
	Blobs  map[string][]byte
	Mtimes map[string]float64
}

func (s *Snapshot) set(kind string, a adapters.Adapter, root, projectRoot string, c *collected) (*Set, error) {
	set := &Set{
		Kind: kind, Agent: a.Name(), UserBase: a.UserBase(root), Files: make([]File, 0, len(c.entries)),
		Skipped: c.skipped, Complete: len(c.skipped) == 0,
	}
	if projectRoot != "" {
		set.ProjectRoot = &projectRoot
	}
	for _, e := range c.entries {
		sha := raw.SHA256(e.body)
		s.Blobs[sha] = e.body
		set.Files = append(set.Files, File{Scope: e.scope, Path: e.path, SHA256: sha, Size: len(e.body), Target: e.target})
		if e.mtime != 0 {
			s.Mtimes[kind+":"+e.scope+":"+e.path] = e.mtime
		}
	}
	body, err := jsonx.SortedCompact(set)
	if err != nil {
		return nil, err
	}
	set.ID = raw.SHA256(body)
	return set, nil
}

func Build(a adapters.Adapter, root, cwd, sessionKey string) (*Snapshot, error) {
	excluded, err := exclusion.Load(config.StateDir())
	if err != nil {
		return nil, err
	}
	withheld := excluded.Excludes(adapters.SessionFile{Agent: a.Name(), Root: root})
	shared := &budget{}
	s := &Snapshot{Complete: true, Blobs: map[string][]byte{}, Mtimes: map[string]float64{}}
	projectRoot, isRepo := "", false
	if cwd != "" && fsutil.IsDir(cwd) {
		projectRoot, isRepo = findProjectRoot(cwd)
	}
	if s.Harness, err = s.set("harness", a, root, projectRoot, collect(a, root, projectRoot, isRepo, shared, withheld)); err != nil {
		return nil, err
	}
	memory := newCollected(a.Name())
	if withheld {
		memory.skip("user", root, "session_exclusion", 0)
	} else {
		memory = collectMemory(a, root, projectRoot, sessionKey, shared)
	}
	if len(memory.entries) > 0 || len(memory.skipped) > 0 {
		if s.Memory, err = s.set("memory", a, root, projectRoot, memory); err != nil {
			return nil, err
		}
	}
	s.Complete = s.Harness.Complete && (s.Memory == nil || s.Memory.Complete)
	return s, nil
}
