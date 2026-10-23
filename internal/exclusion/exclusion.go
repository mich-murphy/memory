// Package exclusion persists session opt-outs independently of the disposable upload cache.
// An opt-out lasts until deliberately cleared; an enabled hook never removes it. Shared
// agent-wide files in the same root are withheld conservatively because they can contain
// the opted-out session. Previously archived bytes are never removed.
package exclusion

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/fsutil"
	"github.com/mich-murphy/memory/internal/jsonx"
)

const schema = 1

type Record struct {
	Schema       int      `json:"schema"`
	ID           string   `json:"id"`
	Agent        string   `json:"agent"`
	Root         string   `json:"root"`
	ResolvedRoot string   `json:"resolved_root"`
	SessionKey   string   `json:"session_key"`
	Paths        []string `json:"paths"`
	RecordedAt   float64  `json:"recorded_at"`
}

type Registry []Record

// canonical resolves existing ancestors as well, since early hooks can name a file not yet
// created. Root symlinks must not let the same session evade a durable opt-out.
func canonical(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	var suffix []string
	for p := abs; ; p = filepath.Dir(p) {
		if resolved, err := filepath.EvalSymlinks(p); err == nil {
			for _, part := range slices.Backward(suffix) {
				resolved = filepath.Join(resolved, part)
			}
			return resolved
		}
		if p == filepath.Dir(p) {
			return abs
		}
		suffix = append(suffix, filepath.Base(p))
	}
}

func sessionName(key string) string {
	if strings.HasPrefix(key, "roots/") || strings.HasPrefix(key, "dirs/") {
		parts := strings.SplitN(key, "/", 3)
		if len(parts) == 3 {
			return parts[2]
		}
	}
	return key
}

// classify prefers known logical roots even when a hook reports their physical alias.
func classify(a adapters.Adapter, path string) (adapters.SessionFile, bool) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		path = a.Home() + strings.TrimPrefix(path, "~")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return adapters.SessionFile{}, false
	}
	roots := append([]string{a.CanonicalRoot()}, a.DefaultRoots()...)
	for _, root := range roots {
		logical := abs
		if !fsutil.Within(logical, root) {
			resolved := canonical(root)
			if !fsutil.Within(canonical(abs), resolved) {
				continue
			}
			rel, err := filepath.Rel(resolved, canonical(abs))
			if err != nil {
				continue
			}
			logical = filepath.Join(root, rel)
		}
		if sf, ok := a.Classify(logical, root); ok {
			return sf, true
		}
	}
	if root := a.InferRoot(abs); root != "" {
		return a.Classify(abs, root)
	}
	return adapters.SessionFile{}, false
}

func recordID(agent, root, key string) string {
	sum := sha256.Sum256([]byte(agent + "\x00" + root + "\x00" + sessionName(key)))
	return hex.EncodeToString(sum[:])
}

func RecordPath(stateDir string, a adapters.Adapter, path string) (Record, error) {
	sf, ok := classify(a, path)
	if !ok || sf.SessionKey == "" {
		return Record{}, errors.New("exclusion: no session identity for path")
	}
	return recordFile(stateDir, sf, canonical(path))
}

// RecordFile records independently classified ownership without root inference or
// source body reads. Repeated observations retain the earlier identity and aliases.
func RecordFile(stateDir string, sf adapters.SessionFile) (Record, error) {
	return recordFile(stateDir, sf)
}

func recordFile(stateDir string, sf adapters.SessionFile, aliases ...string) (Record, error) {
	r := Record{
		Schema: schema, Agent: sf.Agent, Root: sf.Root, ResolvedRoot: canonical(sf.Root), SessionKey: sf.SessionKey,
		Paths: append([]string{sf.Path, canonical(sf.Path)}, aliases...), RecordedAt: float64(time.Now().UnixNano()) / 1e9,
	}
	r.ID = recordID(r.Agent, r.ResolvedRoot, r.SessionKey)
	if err := r.validate(); err != nil {
		return Record{}, err
	}
	old, err := Load(stateDir)
	if err != nil {
		return Record{}, err
	}
	for _, previous := range old {
		if previous.ID == r.ID {
			paths := r.Paths
			r = previous
			r.Paths = append(r.Paths, paths...)
			break
		}
	}
	slices.Sort(r.Paths)
	r.Paths = slices.Compact(r.Paths)
	return r, Save(stateDir, r)
}

func (r Record) validate() error {
	if filepath.IsAbs(r.SessionKey) || filepath.ToSlash(filepath.Clean(r.SessionKey)) != r.SessionKey || r.SessionKey == "." || r.SessionKey == ".." || strings.HasPrefix(r.SessionKey, "../") {
		return errors.New("exclusion: invalid session identity")
	}
	for _, path := range r.Paths {
		if !filepath.IsAbs(path) {
			return errors.New("exclusion: invalid path")
		}
	}
	for _, identity := range append([]string{r.Root, r.ResolvedRoot, r.SessionKey}, r.Paths...) {
		if !utf8.ValidString(identity) {
			return errors.New("exclusion: unsupported non-UTF-8 identity or path")
		}
	}
	if r.Schema != schema || !slices.Contains(adapters.Names, r.Agent) || (!filepath.IsAbs(r.Root) || !filepath.IsAbs(r.ResolvedRoot)) || r.SessionKey == "" || len(r.Paths) == 0 || r.ID != recordID(r.Agent, r.ResolvedRoot, r.SessionKey) {
		return errors.New("exclusion: invalid record")
	}
	return nil
}

// Save also ingests recovered remote opt-outs. It never clears an earlier opt-out.
func Save(stateDir string, r Record) error {
	if err := r.validate(); err != nil {
		return err
	}
	body, err := jsonx.Compact(r)
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(filepath.Join(stateDir, "exclusions", r.ID+".json"), body, 0o600)
}

func Load(stateDir string) (Registry, error) {
	dir := filepath.Join(stateDir, "exclusions")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("exclusion: %w", err)
	}
	var records Registry
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("exclusion: %w", err)
		}
		var r Record
		if err := jsonx.Decode(body, &r); err != nil {
			return nil, fmt.Errorf("exclusion: %w", err)
		}
		if err := r.validate(); err != nil {
			return nil, err
		}
		records = append(records, r)
	}
	return records, nil
}

func (r Registry) Excludes(sf adapters.SessionFile) bool {
	for _, record := range r {
		if record.Agent != sf.Agent {
			continue
		}
		sameRoot := record.Root == sf.Root || fsutil.SameLocation(record.ResolvedRoot, canonical(sf.Root))
		if sameRoot && (sf.SessionKey == "" || sessionName(sf.SessionKey) == sessionName(record.SessionKey)) {
			return true
		}
		path := sf.Path
		if sf.SourcePath != "" {
			path = sf.SourcePath
		}
		if matchesPath(record, path) {
			return true
		}
	}
	return false
}

func matchesPath(record Record, path string) bool {
	resolved := canonical(path)
	for _, alias := range record.Paths {
		if fsutil.SameLocation(canonical(alias), resolved) {
			return true
		}
	}
	return false
}

func (r Registry) ExcludesPath(a adapters.Adapter, path string) bool {
	if sf, ok := classify(a, path); ok {
		return r.Excludes(sf)
	}
	// A filesystem alias may not retain the agent's logical directory spelling.
	// Known opted-out objects remain excluded even when classification is unavailable.
	if path == "~" || strings.HasPrefix(path, "~/") {
		path = a.Home() + strings.TrimPrefix(path, "~")
	}
	for _, record := range r {
		if record.Agent == a.Name() && matchesPath(record, path) {
			return true
		}
	}
	return false
}
