// Package adapters describes each coding agent's on-disk layout: which files to capture, how
// to key them in the bucket, and which belong to one session.
package adapters

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mich-murphy/memory/internal/capturepolicy"
	"github.com/mich-murphy/memory/internal/jsonx"
)

var Names = []string{"claude", "codex", "pi"}

type Role string

const (
	RoleMain         Role = "main"
	RoleSubagent     Role = "subagent"
	RoleToolResult   Role = "tool_result"
	RoleSuperseded   Role = "superseded"
	RoleAux          Role = "aux"
	RoleFileHistory  Role = "file_history"
	RoleSessionEnv   Role = "session_env"
	RoleDebug        Role = "debug"
	RoleTodo         Role = "todo"
	RoleTask         Role = "task"
	RoleTeam         Role = "team"
	RolePlan         Role = "plan"
	RolePaste        Role = "paste"
	RoleUsage        Role = "usage"
	RoleMemory       Role = "memory"
	RoleHistory      Role = "history"
	RoleLiveness     Role = "liveness"
	RoleExperimental Role = "experimental"
	RoleConfig       Role = "config"
	RoleLog          Role = "log"
	RoleDB           Role = "db"
	RoleUnknown      Role = "unknown"
)

type SessionFile struct {
	Agent string
	Root  string
	Path  string
	// SourcePath is the original source when Path is a generated export.
	SourcePath string
	// Key is relative to raw/agent=<agent>/machine=<machine>/, without .zst.
	Key string
	// SessionKey is empty for agent-wide files like history.
	SessionKey string
	Role       Role
	Zstd       bool
}

type SessionRef struct {
	Agent string
	Key   string
}

func (r SessionRef) String() string { return r.Agent + "/" + r.Key }

func (f SessionFile) Session() SessionRef { return SessionRef{f.Agent, f.SessionKey} }

// SnapshotRules are patterns relative to UserBase, the project root and $HOME. A pattern
// ending in "/**" takes every file under that directory; "**/NAME" matches NAME at any depth.
type SnapshotRules struct {
	User    []string
	Project []string
	Home    []string
}

type Entry struct {
	Scope string
	Name  string
	Body  []byte
}

type ScopedDir struct {
	Scope string
	Dir   string
}

type Plugin struct {
	Name string
	Dir  string
}

// Sources describes adapter-generated snapshot entries and installed plugin roots,
// including failures that must not become a false claim of snapshot completeness.
type Sources struct {
	Entries  []Entry
	Plugins  []Plugin
	Complete bool
	Issues   []Issue
}

func (s *Sources) issue(path, kind string, err error) {
	s.Complete = false
	s.Issues = append(s.Issues, Issue{Path: path, Kind: kind, Err: err})
}

type Adapter interface {
	Name() string
	Home() string
	Rules() SnapshotRules
	SkipDirs() []string
	ManagedSettingsFiles() []string
	// Dir is where the agent keeps its settings and hooks.
	Dir() string
	DefaultRoots() []string
	// CanonicalRoot is the root keyed without a prefix. It is fixed from $HOME, not the
	// environment, so a hook (which sees CLAUDE_CONFIG_DIR and friends) and a sweep (which
	// doesn't) agree on keys.
	CanonicalRoot() string
	InferRoot(path string) string
	Classify(path, root string) (SessionFile, bool)
	Related(sf SessionFile) Discovery
	UserBase(root string) string
	ExtraSnapshotEntries(root, projectRoot string) Sources
	MemoryDirs(root, projectRoot, sessionKey string) []ScopedDir
	Plugins(root string) Sources
	// Exports materialises under workDir what the walk of root can't copy as it is, apart from
	// databases (SQLiteExports).
	Exports(root, workDir string, interval time.Duration) Discovery
	SessionInfo(head []map[string]any) map[string]any
}

func All(home string) map[string]Adapter {
	return map[string]Adapter{
		"claude": NewClaude(home),
		"codex":  NewCodex(home),
		"pi":     NewPi(home),
	}
}

var (
	skipDirNames = map[string]bool{
		"node_modules": true, ".git": true, "__pycache__": true, ".venv": true, "venv": true, "cache": true,
		".cache": true, "tmp": true, ".tmp": true, "temp": true, "shell-snapshots": true, "shell_snapshots": true,
	}
	skipSuffixes   = []string{".lock", ".tmp", ".pid", ".sock", ".swp", "-wal", "-shm", "-journal"}
	sqliteSuffixes = []string{".sqlite", ".sqlite3", ".db"}
	binaryMagic    = [][]byte{{0x7f, 'E', 'L', 'F'}, {0xcf, 0xfa, 0xed, 0xfe}, {0xce, 0xfa, 0xed, 0xfe}, {0xca, 0xfe, 0xba, 0xbe}, []byte("MZ")}
)

// topRole looks up the first element of rel in roles, where a directory's entry ends in "/".
func topRole(roles map[string]Role, rel string) Role {
	top, _, isDir := strings.Cut(rel, "/")
	if isDir {
		top += "/"
	}
	return cmp.Or(roles[top], RoleUnknown)
}

func hasAnySuffix(name string, suffixes []string) bool {
	return slices.ContainsFunc(suffixes, func(s string) bool { return strings.HasSuffix(name, s) })
}

func IsCredential(name string) bool { return capturepolicy.IsCredential(name) }

func skipped(name string) bool {
	// Dependency locks describe exact installed source versions, unlike live process locks.
	switch strings.ToLower(name) {
	case "yarn.lock", "bun.lock", "cargo.lock", "poetry.lock", "uv.lock", "composer.lock", "gemfile.lock", "flake.lock", "pdm.lock", "pipfile.lock":
		return false
	}
	return IsCredential(name) || capturepolicy.IsNoise(name) || hasAnySuffix(name, skipSuffixes)
}

func isSQLite(name string) bool {
	return hasAnySuffix(name, sqliteSuffixes)
}

func IsExecutableBinary(path string, mode fs.FileMode) bool {
	if mode&0o111 == 0 {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }() // read-only
	var head [4]byte
	n, _ := f.Read(head[:])
	return slices.ContainsFunc(binaryMagic, func(m []byte) bool { return bytes.HasPrefix(head[:n], m) })
}

func rootTag(root string) string {
	sum := sha256.Sum256([]byte(root))
	return hex.EncodeToString(sum[:])[:10]
}

func keyPrefix(a Adapter, root string) string {
	if root == a.CanonicalRoot() {
		return ""
	}
	return "roots/" + rootTag(root) + "/"
}

func prefixedSession(a Adapter, root, key string) string {
	if key == "" {
		return ""
	}
	return keyPrefix(a, root) + key
}

// layout is what an adapter knows about its agent before reading a file.
type layout struct {
	name string
	home string
	// dir is the agent's directory under home; dirEnv, when set, overrides it.
	dir             string
	dirEnv          string
	skipDirs        []string
	managedSettings []string
	rules           SnapshotRules
}

func (l *layout) Name() string { return l.name }

func (l *layout) Home() string { return l.home }

func (l *layout) Rules() SnapshotRules { return l.rules }

func (l *layout) SkipDirs() []string { return l.skipDirs }

func (l *layout) ManagedSettingsFiles() []string { return l.managedSettings }

func (l *layout) CanonicalRoot() string { return filepath.Join(l.home, l.dir) }

func (l *layout) Dir() string { return cmp.Or(envPath(l.dirEnv, l.home), l.CanonicalRoot()) }

func (l *layout) DefaultRoots() []string { return []string{l.Dir()} }

func (l *layout) UserBase(root string) string { return root }

type noExtras struct{}

func (noExtras) Related(SessionFile) Discovery { return Discovery{Complete: true} }

func (noExtras) ExtraSnapshotEntries(string, string) Sources { return Sources{Complete: true} }

func (noExtras) Plugins(string) Sources { return Sources{Complete: true} }

func (noExtras) Exports(string, string, time.Duration) Discovery { return Discovery{Complete: true} }

// Issue describes a source failure; absent optional roots are not issues.
type Issue struct {
	Path string
	Kind string
	Err  error
}

func (i Issue) Error() string { return fmt.Sprintf("%s %s: %v", i.Kind, i.Path, i.Err) }

// SkippedFile is a deliberate omission, not a retryable source failure.
type SkippedFile struct {
	Path   string
	Reason string
}

// Discovery preserves useful partial results without asserting authoritative absence.
type Discovery struct {
	Files     []SessionFile
	Databases []string
	Skipped   []SkippedFile
	Complete  bool
	Issues    []Issue
}

func (d *Discovery) issue(path, kind string, err error) {
	d.Complete = false
	d.Issues = append(d.Issues, Issue{Path: path, Kind: kind, Err: err})
}

func Discover(a Adapter, root string) Discovery { return discoverUnder(a, root, root) }

// discoverUnder shares the sweep's policy and failure reporting, while allowing hook syncs
// to visit only their session's sources. Namespace root and traversal top are distinct.
func discoverUnder(a Adapter, root, top string) Discovery {
	out := Discovery{Complete: true}
	// WalkDir does not follow its root symlink. Resolve it for traversal only: keys and
	// recorded paths must still use the caller's logical namespace.
	resolved, err := filepath.EvalSymlinks(top)
	if err != nil {
		// A genuinely absent optional source is normal; a dangling root alias is not.
		if _, lerr := os.Lstat(top); !errors.Is(lerr, os.ErrNotExist) {
			out.issue(top, "root", err)
		}
		return out
	}
	st, err := os.Stat(resolved)
	if err != nil {
		out.issue(top, "stat", err)
		return out
	}
	if top == root && !st.IsDir() {
		out.issue(root, "root", errors.New("not a directory"))
		return out
	}
	skip := map[string]bool{}
	for _, dir := range a.SkipDirs() {
		skip[dir] = true
	}
	err = filepath.WalkDir(resolved, func(p string, d fs.DirEntry, err error) error {
		return out.visit(a, root, top, resolved, p, d, err, skip)
	})
	if err != nil {
		out.issue(top, "walk", err)
	}
	return out
}

// visit applies the layout's directory and file policy to one logical walk entry.
func (d *Discovery) visit(a Adapter, root, top, resolved, p string, entry fs.DirEntry, err error, skip map[string]bool) error {
	rel, rerr := filepath.Rel(resolved, p)
	if rerr != nil {
		return rerr
	}
	logical := filepath.Join(top, rel)
	if err != nil {
		d.issue(logical, "walk", err)
		return nil
	}
	if entry.IsDir() {
		namespaceRel, err := filepath.Rel(root, logical)
		if err != nil {
			return err
		}
		if logical != root && (skipDirNames[entry.Name()] || capturepolicy.IsNoise(entry.Name()) || skip[filepath.ToSlash(namespaceRel)]) {
			return filepath.SkipDir
		}
		return nil
	}
	if skipped(entry.Name()) {
		return nil
	}
	info, err := d.fileInfo(logical, entry)
	if err != nil {
		d.issue(logical, "stat", err)
		return nil
	}
	if info == nil || !info.Mode().IsRegular() {
		return nil
	}
	switch {
	case isSQLite(entry.Name()):
		d.Databases = append(d.Databases, logical)
	case IsExecutableBinary(logical, info.Mode()):
	default:
		if sf, ok := a.Classify(logical, root); ok {
			d.Files = append(d.Files, sf)
		}
	}
	return nil
}

func ReadHead(data []byte, maxLines int) []map[string]any {
	var out []map[string]any
	read := 0
	for line := range bytes.Lines(data) {
		if read == maxLines {
			break
		}
		read++
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		if m, ok := jsonx.Map(line); ok {
			out = append(out, m)
		}
	}
	return out
}

func relative(path, root string) (string, bool) {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || !filepath.IsLocal(rel) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

func expandUser(p, home string) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}

func envPath(name, home string) string {
	v := os.Getenv(name)
	if v == "" {
		return ""
	}
	return filepath.Clean(expandUser(v, home))
}

// fill sets each field of info from d[fields[field]], keeping the first non-empty value.
func fill(info, d map[string]any, fields map[string]string) {
	for field, from := range fields {
		if _, done := info[field]; !done && truthy(d[from]) {
			info[field] = d[from]
		}
	}
}

func str(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

func truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != ""
	case json.Number:
		f, err := t.Float64()
		return err != nil || f != 0
	case float64:
		return t != 0
	case []any:
		return len(t) > 0
	case map[string]any:
		return len(t) > 0
	default:
		return true
	}
}

// fileInfo resolves regular-file aliases without traversing directory aliases. A nil
// result is a deliberate skip; errors remain discovery failures at the caller.
func (d *Discovery) fileInfo(logical string, entry fs.DirEntry) (os.FileInfo, error) {
	info, err := entry.Info()
	if entry.Type()&os.ModeSymlink != 0 {
		resolution, resolveErr := capturepolicy.Resolve(logical)
		if resolveErr != nil {
			// A dangling target might have been a directory. Never infer deletions from it.
			if errors.Is(resolveErr, os.ErrNotExist) {
				d.Complete = false
				d.Skipped = append(d.Skipped, SkippedFile{Path: logical, Reason: "symlink_unresolved"})
			} else {
				d.issue(logical, "symlink_resolve", resolveErr)
			}
			return nil, nil
		}
		if slices.ContainsFunc(resolution.Identities, func(path string) bool { return IsCredential(path) || capturepolicy.IsNoise(path) }) {
			return nil, nil
		}
		info, err = os.Stat(resolution.Path)
		if err == nil && info.IsDir() {
			d.Complete = false
			d.Skipped = append(d.Skipped, SkippedFile{Path: logical, Reason: "symlink_directory"})
			return nil, nil
		}
	}
	return info, err
}
