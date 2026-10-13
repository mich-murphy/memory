// Package snapshot records which instructions, skills, settings and scripts an agent had for a
// project.
//
// A snapshot is a content-addressed set of (scope, path, sha256) entries. File bodies are stored
// once under snapshots/blobs/<sha256>, and sets under snapshots/sets/<set-id>.json,
// so identical files across sessions and machines cost nothing extra. Two kinds of set are
// taken:
//
//	harness  what shapes the agent's behaviour
//	memory   memory the agent writes for itself (Claude auto memory and agent-memory, Codex
//	         memories/), kept apart because it changes during sessions on its own
//
// Scopes:
//
//	user      the agent's user config dir (~/.claude, ~/.codex, ~/.pi/agent)
//	home      files under $HOME that agents also read (~/.agents/skills)
//	project   the git repo containing the session's cwd (or just the cwd outside a repo)
//	ancestor  instruction files in the directories above the project
//	managed   administrator settings (/etc/claude-code, /etc/codex, ...)
//	plugin    every file of each installed Claude Code plugin, as <plugin>/<path>
//	script    hook and status-line scripts named in settings, by real path
//	import    files pulled into CLAUDE.md/AGENTS.md with @path, by real path
//
// Secrets written inside config files (MCP env and headers, settings env, API keys) are
// redacted by package scrub before a body is hashed; everything else is stored as it is.
//
// Directories in a pattern ending in /** are taken whole (a skill's scripts and references, not
// just its SKILL.md), following symlinks; a symlinked file records its target. Files left out
// for size or count are listed in the set's skipped. Each file's mtime is returned separately so
// it doesn't change the set id.
package snapshot

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/jsonx"
	"github.com/mich-murphy/memory/internal/raw"
	"github.com/mich-murphy/memory/internal/scrub"
)

const (
	maxFileBytes     = 5 << 20
	maxFilesPerScope = 3000
	maxWalkDirs      = 5000
	maxImportDepth   = 5
)

var (
	skipDirs      = map[string]bool{".git": true, "node_modules": true, ".venv": true, "venv": true, "__pycache__": true, "dist": true, "build": true, ".next": true, "target": true, ".cache": true}
	ancestorNames = []string{"CLAUDE.md", "CLAUDE.local.md", "AGENTS.md", "AGENTS.override.md"}
	settingsNames = map[string]bool{"settings.json": true, "settings.local.json": true, "hooks.json": true, "config.toml": true, "managed-settings.json": true}
)

// Entry is one file in a set.
type Entry struct {
	Scope  string
	Path   string
	Body   []byte
	Mtime  *float64
	Target string
	// Source is where the bytes came from, for following imports.
	Source string
}

// Skipped is a file left out of a set, and why.
// Fields are in key order so the set doc marshals with sorted keys throughout.
type Skipped struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
	Scope  string `json:"scope"`
	Size   int64  `json:"size"`
}

type collected struct {
	agent   string
	entries []*Entry
	skipped []Skipped
	counts  map[string]int
	seen    map[string]bool
}

func newCollected(agent string) *collected {
	return &collected{agent: agent, counts: map[string]int{}, seen: map[string]bool{}}
}

// add reads one file into the set, or notes why it was left out.
func (c *collected) add(scope, path, name string) *Entry {
	if c.seen[scope+"\x00"+name] || adapters.IsCredential(filepath.Base(path)) {
		return nil
	}
	st, err := os.Stat(path)
	if err != nil {
		return nil
	}
	if c.counts[scope] >= maxFilesPerScope {
		c.skipped = append(c.skipped, Skipped{Scope: scope, Path: name, Reason: "file_limit", Size: st.Size()})
		return nil
	}
	if st.Size() > maxFileBytes {
		c.skipped = append(c.skipped, Skipped{Scope: scope, Path: name, Reason: "too_large", Size: st.Size()})
		return nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	if body, err = scrub.File(c.agent, filepath.Base(path), body); err != nil {
		// A config file that may hold secrets but doesn't parse can't be checked, so it isn't kept.
		c.skipped = append(c.skipped, Skipped{Scope: scope, Path: name, Reason: "unparseable", Size: st.Size()})
		return nil
	}
	c.seen[scope+"\x00"+name] = true
	c.counts[scope]++
	resolved := realPath(path)
	abs, _ := filepath.Abs(path)
	mtime := float64(st.ModTime().UnixNano()) / 1e9
	e := &Entry{Scope: scope, Path: name, Body: body, Mtime: &mtime, Source: resolved}
	if resolved != abs {
		e.Target = resolved
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

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// FindProjectRoot returns the nearest directory at or above cwd holding .git, and whether one
// was found; otherwise cwd itself.
func FindProjectRoot(cwd string) (string, bool) {
	for d := cwd; ; d = filepath.Dir(d) {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return d, true
		}
		if d == filepath.Dir(d) {
			return cwd, false
		}
	}
}

// walkFiles lists every file under top (following directory symlinks when asked), pruning
// skipDirs and stopping after maxWalkDirs directories. The order is os.walk's: depth first,
// each directory's files before its subdirectories'.
func walkFiles(top string, follow bool) []string {
	var out []string
	visited := map[string]bool{}
	queue := []string{top}
	for n := 0; len(queue) > 0 && n < maxWalkDirs; n++ {
		dir := queue[0]
		queue = queue[1:]
		resolved := realPath(dir)
		if visited[resolved] { // a symlink loop
			continue
		}
		visited[resolved] = true
		subdirs, files := listDir(dir, follow)
		out = append(out, files...)
		queue = append(subdirs, queue...)
	}
	return out
}

// listDir splits a directory's entries into subdirectories to walk and everything else. A
// symlinked directory is walked only when following links; otherwise, like os.walk, it is
// neither walked nor listed as a file.
func listDir(dir string, follow bool) (subdirs, files []string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil
	}
	for _, d := range entries {
		p := filepath.Join(dir, d.Name())
		linkedDir := d.Type()&os.ModeSymlink != 0 && isDir(p)
		switch {
		case d.IsDir() || (linkedDir && follow):
			if !skipDirs[d.Name()] {
				subdirs = append(subdirs, p)
			}
		case linkedDir:
		default:
			files = append(files, p)
		}
	}
	return subdirs, files
}

func match(base, pattern string, recursiveOK bool) []string {
	if top, ok := strings.CutSuffix(pattern, "/**"); ok {
		top = filepath.Join(base, top)
		if !isDir(top) {
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
			if m, _ := filepath.Match(name, filepath.Base(p)); m && isFile(p) {
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
		if err != nil || !isFile(p) {
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
		if isFile(p) {
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

// collect gathers every harness file for a session in cwd.
func collect(a adapters.Adapter, root, cwd string) *collected {
	c := newCollected(a.Name())
	projectRoot, isRepo := "", false
	if cwd != "" && isDir(cwd) {
		projectRoot, isRepo = FindProjectRoot(cwd)
	}
	home := a.Home()
	if isDir(home) {
		home = realPath(home)
	}
	rules := a.Rules()
	scopes := []scopeBase{
		{"user", a.UserBase(root), rules.User, true},
		{"home", a.Home(), rules.Home, false},
	}
	if projectRoot != "" {
		// Outside a git repo (say, a session started in ~), only look at the cwd itself.
		resolved := realPath(projectRoot)
		scopes = append(scopes, scopeBase{"project", projectRoot, rules.Project, isRepo && resolved != home && resolved != "/"})
	}
	for _, s := range scopes {
		c.addPatterns(s)
	}
	if projectRoot != "" && projectRoot != filepath.Dir(projectRoot) {
		c.addAncestors(projectRoot)
	}
	for _, p := range a.ManagedPaths() {
		if isFile(p) {
			c.add("managed", p, p)
		}
	}
	for _, plugin := range a.PluginDirs(root) {
		for _, p := range filterFiles(walkFiles(plugin.Dir, true)) {
			c.add("plugin", p, plugin.Name+"/"+relSlash(p, plugin.Dir))
		}
	}
	addScripts(c, home, projectRoot)
	addImports(c, home)
	for _, e := range a.ExtraSnapshotEntries(root, projectRoot) {
		c.entries = append(c.entries, &Entry{Scope: e.Scope, Path: e.Name, Body: e.Body})
	}
	sortEntries(c.entries)
	return c
}

// addPatterns adds the files a scope's patterns match, named relative to its base.
func (c *collected) addPatterns(s scopeBase) {
	if !isDir(s.base) {
		return
	}
	for _, pattern := range s.patterns {
		for _, p := range match(s.base, pattern, s.recursiveOK) {
			c.add(s.scope, p, relSlash(p, s.base))
		}
	}
}

// addAncestors adds the instruction files in every directory above the project.
func (c *collected) addAncestors(projectRoot string) {
	for d := filepath.Dir(projectRoot); ; d = filepath.Dir(d) {
		for _, name := range ancestorNames {
			if p := filepath.Join(d, name); isFile(p) {
				c.add("ancestor", p, p)
			}
		}
		if d == filepath.Dir(d) {
			return
		}
	}
}

// addScripts adds hook and status-line scripts named in any settings file collected so far.
func addScripts(c *collected, home, projectRoot string) {
	for _, e := range slices.Clone(c.entries) {
		if !settingsNames[filepath.Base(e.Path)] {
			continue
		}
		for _, command := range commands(parseSettings(e), false) {
			for _, p := range scriptPaths(command, home, projectRoot) {
				c.add("script", p, realPath(p))
			}
		}
	}
}

// addImports follows @imports a few levels deep.
func addImports(c *collected, home string) {
	var frontier []*Entry
	for _, e := range c.entries {
		switch e.Scope {
		case "user", "project", "ancestor", "managed":
			frontier = append(frontier, e)
		}
	}
	for range maxImportDepth {
		var added []*Entry
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

func sortEntries(entries []*Entry) {
	slices.SortStableFunc(entries, func(a, b *Entry) int {
		if c := strings.Compare(a.Scope, b.Scope); c != 0 {
			return c
		}
		return strings.Compare(a.Path, b.Path)
	})
}

// collectMemory gathers the memory the agent writes for itself.
func collectMemory(a adapters.Adapter, root, cwd, sessionKey string) *collected {
	c := newCollected(a.Name())
	projectRoot := ""
	if cwd != "" && isDir(cwd) {
		projectRoot, _ = FindProjectRoot(cwd)
	}
	for _, d := range a.MemoryDirs(root, projectRoot, sessionKey) {
		if !isDir(d.Dir) {
			continue
		}
		for _, p := range filterFiles(walkFiles(d.Dir, true)) {
			c.add(d.Name, p, p)
		}
	}
	sortEntries(c.entries)
	return c
}

// Snapshot is a harness set and, if the agent keeps any, a memory set.
type Snapshot struct {
	SetID     string
	Doc       map[string]any
	MemoryID  string // "" when there is no memory set
	MemoryDoc map[string]any
	Blobs     map[string][]byte
	Mtimes    map[string]float64
}

func setDoc(kind string, a adapters.Adapter, root, projectRoot string, c *collected, blobs map[string][]byte,
	mtimes map[string]float64,
) (string, map[string]any, error) {
	files := make([]map[string]any, 0, len(c.entries))
	for _, e := range c.entries {
		sha := raw.SHA256(e.Body)
		blobs[sha] = e.Body
		f := map[string]any{"scope": e.Scope, "path": e.Path, "sha256": sha, "size": len(e.Body)}
		if e.Target != "" {
			f["target"] = e.Target
		}
		files = append(files, f)
		if e.Mtime != nil {
			mtimes[kind+":"+e.Scope+":"+e.Path] = *e.Mtime
		}
	}
	skipped := c.skipped
	if skipped == nil {
		skipped = []Skipped{}
	}
	var pr any
	if projectRoot != "" {
		pr = projectRoot
	}
	doc := map[string]any{
		"kind": kind, "agent": a.Name(), "user_base": a.UserBase(root), "project_root": pr,
		"files": files, "skipped": skipped,
	}
	body, err := jsonx.Sorted(doc, 0)
	if err != nil {
		return "", nil, err
	}
	return raw.SHA256(body), doc, nil
}

// Build takes the harness and memory sets for a session in cwd.
func Build(a adapters.Adapter, root, cwd, sessionKey string) (*Snapshot, error) {
	s := &Snapshot{Blobs: map[string][]byte{}, Mtimes: map[string]float64{}}
	projectRoot := ""
	if cwd != "" && isDir(cwd) {
		projectRoot, _ = FindProjectRoot(cwd)
	}
	var err error
	s.SetID, s.Doc, err = setDoc("harness", a, root, projectRoot, collect(a, root, cwd), s.Blobs, s.Mtimes)
	if err != nil {
		return nil, err
	}
	memory := collectMemory(a, root, cwd, sessionKey)
	if len(memory.entries) > 0 || len(memory.skipped) > 0 {
		s.MemoryID, s.MemoryDoc, err = setDoc("memory", a, root, projectRoot, memory, s.Blobs, s.Mtimes)
		if err != nil {
			return nil, err
		}
	}
	return s, nil
}
