// Package adapters describes each coding agent's on-disk layout: which files to capture, how
// to key them in the bucket, and which belong to one session.
//
// A root is a directory an agent writes under (~/.claude, $CODEX_HOME, ~/.pi/agent, or a Pi
// session dir). Agents can be pointed elsewhere with environment variables or settings that a
// cron job never sees, so a hook that reports a path outside the known roots makes the
// collector remember that root and sweep it from then on.
//
// Capture is a denylist walk: every file under a root is copied unless it is a credential, a
// lock, cache, temp file or binary, or on the adapter's own skip list. Files the adapter
// doesn't recognise get role "unknown" rather than being dropped.
package adapters

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mich-murphy/memory/internal/jsonx"
)

// Names lists the supported agents.
var Names = []string{"claude", "codex", "pi"}

// SessionFile is one local file and where it goes in the bucket.
type SessionFile struct {
	Agent string
	Path  string
	// Key is the object key relative to raw/agent=<agent>/machine=<machine>/, without .zst.
	Key string
	// SessionKey groups the files of one session; empty for agent-wide files like history.
	SessionKey string
	// Role is main, subagent, tool_result, superseded, memory, history, plan, file_history,
	// task, todo, team, session_env, debug, paste, usage, config, log, db or unknown...
	Role string
	// Compression is "zst" for Codex's recompressed rollouts.
	Compression string
}

// SnapshotRules are harness files worth recording, as patterns relative to a base directory.
// A pattern ending in "/**" takes every file under that directory; "**/NAME" matches NAME at
// any depth.
type SnapshotRules struct {
	User    []string
	Project []string
	Home    []string // relative to $HOME, e.g. .agents/skills/**
}

// Entry is harness state that doesn't live in a plain file of its own.
type Entry struct {
	Scope string
	Name  string
	Body  []byte
}

// ScopedDir is a directory with the snapshot scope or plugin name it belongs to.
type ScopedDir struct {
	Name string
	Dir  string
}

// Adapter is one agent's layout.
type Adapter interface {
	Name() string
	Home() string
	Rules() SnapshotRules
	// SkipDirs are directories (relative to a root) never copied.
	SkipDirs() []string
	// ManagedPaths are settings files outside the user's dirs that an administrator controls.
	ManagedPaths() []string
	DefaultRoots() []string
	// CanonicalRoot is the root keyed without a prefix. It is fixed from $HOME, not the
	// environment, so a hook (which sees CLAUDE_CONFIG_DIR and friends) and a sweep (which
	// doesn't) agree on keys.
	CanonicalRoot() string
	// InferRoot works out the root from the shape of a path a hook reported ("" if it can't).
	InferRoot(path string) string
	// Classify maps a local path to a SessionFile; false if it isn't captured.
	Classify(path, root string) (SessionFile, bool)
	// Related lists other files to sync alongside a file a hook pointed at.
	Related(sf SessionFile, root string) []string
	// UserBase is the directory the user-level snapshot patterns are relative to.
	UserBase(root string) string
	ExtraSnapshotEntries(root, projectRoot string) []Entry
	// MemoryDirs are directories of memory the agent writes for itself, as (scope, dir).
	MemoryDirs(root, projectRoot, sessionKey string) []ScopedDir
	// PluginDirs are installed plugins, as (name, install dir).
	PluginDirs(root string) []ScopedDir
	// Exports materialises data that can't be copied as-is (live SQLite databases, files
	// holding a credential field) under workDir. Run by sweeps.
	Exports(root, workDir string, dbInterval time.Duration) []SessionFile
	// SessionInfo extracts session_id, cwd, version... from a transcript's first lines.
	SessionInfo(head []map[string]any) map[string]any
}

// All returns every adapter for a home directory, keyed by name.
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

func hasAnySuffix(name string, suffixes []string) bool {
	return slices.ContainsFunc(suffixes, func(s string) bool { return strings.HasSuffix(name, s) })
}

// IsCredential reports whether a file name is a credential, never copied.
func IsCredential(name string) bool {
	return credentialNames[name] || strings.HasSuffix(name, ".key") || strings.HasSuffix(name, ".pem") ||
		strings.HasPrefix(name, ".claude.json")
}

// Skipped reports whether a file name is a credential or noise (locks, temp files, journals).
func Skipped(name string) bool {
	return IsCredential(name) || hasAnySuffix(name, skipSuffixes)
}

// IsSQLite reports whether a file name looks like a SQLite database.
func IsSQLite(name string) bool {
	return hasAnySuffix(name, sqliteSuffixes)
}

// IsExecutableBinary reports whether path is an executable with a native binary header.
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

// RootTag is a short stable prefix for keys under a non-default root, so two roots can't
// collide.
func RootTag(root string) string {
	sum := sha256.Sum256([]byte(root))
	return hex.EncodeToString(sum[:])[:10]
}

// KeyPrefix is "" for the canonical root and roots/<tag>/ for any other.
func KeyPrefix(a Adapter, root string) string {
	if root == a.CanonicalRoot() {
		return ""
	}
	return "roots/" + RootTag(root) + "/"
}

// WalkedFile is a regular file found under a root.
type WalkedFile struct {
	Path string
	Mode fs.FileMode
}

// Walk lists every capturable regular file under root in lexical order, skipping credentials,
// noise, symlinks and the adapter's skip list.
func Walk(a Adapter, root string) []WalkedFile {
	if !isDir(root) {
		return nil
	}
	skip := map[string]bool{}
	for _, d := range a.SkipDirs() {
		skip[d] = true
	}
	var out []WalkedFile
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
		out = append(out, WalkedFile{Path: p, Mode: info.Mode()})
		return nil
	})
	return out
}

// Discover lists the captured files under root.
func Discover(a Adapter, root string) []SessionFile {
	var out []SessionFile
	for _, f := range Walk(a, root) {
		if IsSQLite(filepath.Base(f.Path)) || IsExecutableBinary(f.Path, f.Mode) {
			continue
		}
		if sf, ok := a.Classify(f.Path, root); ok {
			out = append(out, sf)
		}
	}
	return out
}

// FilesUnder lists capturable regular files under each root, recursively and sorted.
func FilesUnder(roots ...string) []string {
	var out []string
	for _, root := range roots {
		if !isDir(root) {
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

// ReadHead parses the JSON objects among the first maxLines lines of data.
func ReadHead(data []byte, maxLines int) []map[string]any {
	var out []map[string]any
	for i := 0; i < maxLines && len(data) > 0; i++ {
		line := data
		if nl := bytes.IndexByte(data, '\n'); nl >= 0 {
			line, data = data[:nl], data[nl+1:]
		} else {
			data = nil
		}
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

// Rel returns path relative to root in slash form, or false if path isn't under root.
func Rel(path, root string) (string, bool) {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// expandUser expands a leading ~ to the home directory.
func expandUser(p, home string) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}

// envPath is an environment variable as a cleaned, ~-expanded path, or "".
func envPath(name, home string) string {
	v := os.Getenv(name)
	if v == "" {
		return ""
	}
	return filepath.Clean(expandUser(v, home))
}

func str(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// truthy is Python's truth test for a decoded JSON value, which the adapters' field picking
// follows.
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
