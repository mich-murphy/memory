// Package adapters describes each coding agent's on-disk layout: which files to capture, how
// to key them in the bucket, and which belong to one session.
package adapters

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mich-murphy/memory/internal/fsutil"
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
	Related(sf SessionFile) []string
	UserBase(root string) string
	ExtraSnapshotEntries(root, projectRoot string) []Entry
	MemoryDirs(root, projectRoot, sessionKey string) []ScopedDir
	Plugins(root string) []Plugin
	// Exports materialises under workDir what the walk of root can't copy as it is, apart from
	// databases (SQLiteExports).
	Exports(root, workDir string, interval time.Duration) []SessionFile
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
	credentialNames = map[string]bool{".credentials.json": true, "auth.json": true, "oauth.json": true, "mcp-auth.json": true}
	skipDirNames    = map[string]bool{
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

func IsCredential(name string) bool {
	return credentialNames[name] || strings.HasSuffix(name, ".key") || strings.HasSuffix(name, ".pem") ||
		strings.HasPrefix(name, ".claude.json")
}

func Skipped(name string) bool {
	return IsCredential(name) || hasAnySuffix(name, skipSuffixes)
}

func IsSQLite(name string) bool {
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

func RootTag(root string) string {
	sum := sha256.Sum256([]byte(root))
	return hex.EncodeToString(sum[:])[:10]
}

func KeyPrefix(a Adapter, root string) string {
	if root == a.CanonicalRoot() {
		return ""
	}
	return "roots/" + RootTag(root) + "/"
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

func (noExtras) Related(SessionFile) []string { return nil }

func (noExtras) ExtraSnapshotEntries(string, string) []Entry { return nil }

func (noExtras) Plugins(string) []Plugin { return nil }

func (noExtras) Exports(string, string, time.Duration) []SessionFile { return nil }

type walkedFile struct {
	path string
	mode fs.FileMode
}

func walk(a Adapter, root string) []walkedFile {
	if !fsutil.IsDir(root) {
		return nil
	}
	skip := map[string]bool{}
	for _, d := range a.SkipDirs() {
		skip[d] = true
	}
	var out []walkedFile
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable directory is skipped, not fatal
		}
		if d.IsDir() {
			if p == root {
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			if skipDirNames[d.Name()] || skip[filepath.ToSlash(rel)] {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || Skipped(d.Name()) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil //nolint:nilerr // vanished between listing and stat
		}
		out = append(out, walkedFile{path: p, mode: info.Mode()})
		return nil
	})
	return out
}

func Discover(a Adapter, root string) (files []SessionFile, databases []string) {
	for _, f := range walk(a, root) {
		switch {
		case IsSQLite(filepath.Base(f.path)):
			databases = append(databases, f.path)
		case IsExecutableBinary(f.path, f.mode):
		default:
			if sf, ok := a.Classify(f.path, root); ok {
				files = append(files, sf)
			}
		}
	}
	return files, databases
}

func FilesUnder(roots ...string) []string {
	var out []string
	for _, root := range roots {
		if !fsutil.IsDir(root) {
			continue
		}
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err == nil && d.Type().IsRegular() && !Skipped(d.Name()) {
				out = append(out, p)
			}
			return nil
		})
	}
	return out
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

func Rel(path, root string) (string, bool) {
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
