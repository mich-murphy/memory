// Package snapshot records which instructions, skills, settings and scripts an agent had for a
// project, as content-addressed sets. The set format and its scopes are in
// docs/storage-layout.md.
package snapshot

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/capturepolicy"
	"github.com/mich-murphy/memory/internal/config"
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
}

type scopedName struct{ scope, name string }

type collected struct {
	agent        string
	entries      []*entry
	skipped      []SkippedFile
	counts       map[string]int
	seen         map[scopedName]bool
	configPath   string
	configTarget string
	stateDir     string
	statePath    string
}

func newCollected(agent string) *collected {
	statePath, err := filepath.Abs(config.StateDir())
	if err != nil {
		statePath = config.StateDir()
	}
	return &collected{
		agent: agent, skipped: []SkippedFile{}, counts: map[string]int{}, seen: map[scopedName]bool{},
		configPath:   filepath.Join(realPath(filepath.Dir(config.FilePath())), filepath.Base(config.FilePath())),
		configTarget: realPath(config.FilePath()), stateDir: realPath(config.StateDir()), statePath: statePath,
	}
}

func (c *collected) add(scope, path, name string) *entry {
	if c.seen[scopedName{scope, name}] {
		return nil
	}
	resolved := realPath(path)
	abs, err := filepath.Abs(path)
	if err != nil || capturepolicy.Excludes(abs, resolved, c.configPath, c.stateDir) || capturepolicy.Excludes(abs, resolved, c.configTarget, c.statePath) {
		return nil
	}
	st, err := os.Stat(resolved)
	if err != nil {
		return nil
	}
	if c.counts[scope] >= maxFilesPerScope {
		c.skipped = append(c.skipped, SkippedFile{Scope: scope, Path: name, Reason: "file_limit", Size: st.Size()})
		return nil
	}
	if st.Size() > maxFileSize {
		c.skipped = append(c.skipped, SkippedFile{Scope: scope, Path: name, Reason: "too_large", Size: st.Size()})
		return nil
	}
	body, err := os.ReadFile(resolved)
	if err != nil {
		return nil
	}
	if body, err = scrub.Paths(c.agent, path, resolved, body); err != nil {
		c.skipped = append(c.skipped, SkippedFile{Scope: scope, Path: name, Reason: "unparseable", Size: st.Size()})
		return nil
	}
	c.seen[scopedName{scope, name}] = true
	c.counts[scope]++
	e := &entry{scope: scope, path: name, body: body, mtime: float64(st.ModTime().UnixNano()) / 1e9, realPath: resolved}
	if resolved != abs {
		e.target = resolved
	}
	c.entries = append(c.entries, e)
	return e
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

func walkFiles(top string, follow bool) []string {
	var out []string
	visited := map[string]bool{}
	queue := []string{top}
	for n := 0; len(queue) > 0 && n < maxWalkDirs; n++ {
		dir := queue[0]
		queue = queue[1:]
		resolved := realPath(dir)
		if visited[resolved] {
			continue
		}
		visited[resolved] = true
		subdirs, files := listDir(dir, follow)
		out = append(out, files...)
		queue = append(subdirs, queue...)
	}
	return out
}

func listDir(dir string, follow bool) (subdirs, files []string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil
	}
	for _, d := range entries {
		p := filepath.Join(dir, d.Name())
		linkedDir := d.Type()&os.ModeSymlink != 0 && fsutil.IsDir(p)
		switch {
		case linkedDir && !follow:
			continue
		case d.IsDir() || linkedDir:
			if !skipDirs[d.Name()] {
				subdirs = append(subdirs, p)
			}
		default:
			files = append(files, p)
		}
	}
	return subdirs, files
}

func match(base, pattern string, recursiveOK bool) []string {
	if top, ok := strings.CutSuffix(pattern, "/**"); ok {
		top = filepath.Join(base, top)
		if !fsutil.IsDir(top) {
			return nil
		}
		return filterFiles(walkFiles(top, true))
	}
	if name, ok := strings.CutPrefix(pattern, "**/"); ok && !strings.Contains(name, "/") {
		if !recursiveOK {
			return nil
		}
		var out []string
		for _, p := range walkFiles(base, false) {
			if m, _ := filepath.Match(name, filepath.Base(p)); m && fsutil.IsFile(p) {
				out = append(out, p)
			}
		}
		return out
	}
	matches, _ := filepath.Glob(filepath.Join(base, filepath.FromSlash(pattern)))
	slices.Sort(matches)
	var out []string
	for _, p := range matches {
		rel, err := filepath.Rel(base, p)
		if err != nil || !fsutil.IsFile(p) {
			continue
		}
		if !slices.ContainsFunc(strings.Split(filepath.ToSlash(rel), "/"), func(part string) bool { return skipDirs[part] }) {
			out = append(out, p)
		}
	}
	return out
}

func filterFiles(paths []string) []string {
	var out []string
	for _, p := range paths {
		if fsutil.IsFile(p) {
			out = append(out, p)
		}
	}
	return out
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

func collect(a adapters.Adapter, root, projectRoot string, isRepo bool) *collected {
	c := newCollected(a.Name())
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
		c.addPatterns(s)
	}
	if projectRoot != "" && projectRoot != filepath.Dir(projectRoot) {
		c.addAncestors(projectRoot)
	}
	for _, p := range a.ManagedSettingsFiles() {
		if fsutil.IsFile(p) {
			c.add("managed", p, p)
		}
	}
	for _, plugin := range a.Plugins(root) {
		for _, p := range filterFiles(walkFiles(plugin.Dir, true)) {
			c.add("plugin", p, plugin.Name+"/"+relSlash(p, plugin.Dir))
		}
	}
	addScripts(c, home, projectRoot)
	addImports(c, home)
	for _, e := range a.ExtraSnapshotEntries(root, projectRoot) {
		c.entries = append(c.entries, &entry{scope: e.Scope, path: e.Name, body: e.Body})
	}
	sortEntries(c.entries)
	return c
}

func (c *collected) addPatterns(s scopeBase) {
	if !fsutil.IsDir(s.base) {
		return
	}
	for _, pattern := range s.patterns {
		for _, p := range match(s.base, pattern, s.recursiveOK) {
			c.add(s.scope, p, relSlash(p, s.base))
		}
	}
}

func (c *collected) addAncestors(projectRoot string) {
	for d := filepath.Dir(projectRoot); ; d = filepath.Dir(d) {
		for _, name := range ancestorNames {
			if p := filepath.Join(d, name); fsutil.IsFile(p) {
				c.add("ancestor", p, p)
			}
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

func collectMemory(a adapters.Adapter, root, projectRoot, sessionKey string) *collected {
	c := newCollected(a.Name())
	for _, d := range a.MemoryDirs(root, projectRoot, sessionKey) {
		if !fsutil.IsDir(d.Dir) {
			continue
		}
		for _, p := range filterFiles(walkFiles(d.Dir, true)) {
			c.add(d.Scope, p, p)
		}
	}
	sortEntries(c.entries)
	return c
}

// Set is the document stored at snapshots/sets/<ID>.json; ID is the sha256 of its compact form.
type Set struct {
	ID          string        `json:"-"`
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
	Harness *Set
	// Memory is nil when the agent kept no memory for the session.
	Memory *Set
	Blobs  map[string][]byte
	Mtimes map[string]float64
}

func (s *Snapshot) set(kind string, a adapters.Adapter, root, projectRoot string, c *collected) (*Set, error) {
	set := &Set{
		Kind: kind, Agent: a.Name(), UserBase: a.UserBase(root), Files: make([]File, 0, len(c.entries)),
		Skipped: c.skipped,
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
	s := &Snapshot{Blobs: map[string][]byte{}, Mtimes: map[string]float64{}}
	projectRoot, isRepo := "", false
	if cwd != "" && fsutil.IsDir(cwd) {
		projectRoot, isRepo = findProjectRoot(cwd)
	}
	var err error
	if s.Harness, err = s.set("harness", a, root, projectRoot, collect(a, root, projectRoot, isRepo)); err != nil {
		return nil, err
	}
	memory := collectMemory(a, root, projectRoot, sessionKey)
	if len(memory.entries) > 0 || len(memory.skipped) > 0 {
		if s.Memory, err = s.set("memory", a, root, projectRoot, memory); err != nil {
			return nil, err
		}
	}
	return s, nil
}
