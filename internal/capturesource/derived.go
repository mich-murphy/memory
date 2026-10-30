package capturesource

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/scrub"
)

// classifySource only selects metadata. Bytes are prepared exclusively by Queue.
func classifySource(derived bool, root Root, a adapters.Adapter, path string) (adapters.SessionFile, string, string) {
	// Retired unrecognized installer backups may contain protected configuration.
	// Never admit them as raw artifacts, even when derived preparation is off.
	if strings.Contains(filepath.Base(path), ".agent-sessions-capture-v1-backup-") {
		return adapters.SessionFile{}, "", "protected_source_requires_scrub"
	}
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".db" || ext == ".sqlite" || ext == ".sqlite3" {
		if !derived {
			return adapters.SessionFile{}, "", "sqlite_backup_required"
		}
		// Adapters deliberately omit live DBs. Match their SQLiteExports db/<rel>
		// identity while using a metadata-only probe to retain known directory ownership.
		sf, ok := a.Classify(path+".capture-db-owner", root.Path)
		if !ok {
			return adapters.SessionFile{}, "", "database_classification_required"
		}
		sf.Path, sf.Key, sf.Role = path, derivedKey(a, root.Path, "db/"+relativeKey(root.Path, path)), adapters.RoleDB
		return sf, "sqlite-backup", ""
	}
	sf, ok := a.Classify(path, root.Path)
	if scrub.Recognizes(path) {
		return classifyConfig(derived, root, a, path, sf, ok)
	}
	if !ok {
		return sf, "", "adapter_omission"
	}
	if compressed(sf, ext) {
		if !derived {
			return sf, "", "decoded_source_required"
		}
		if !strings.HasSuffix(path, ".jsonl.zst") {
			return sf, "", "unsupported_compressed_source"
		}
		logical, classified := a.Classify(strings.TrimSuffix(path, ".zst"), root.Path)
		if !classified || (logical.Role != adapters.RoleMain && logical.Role != adapters.RoleSubagent) {
			return sf, "", "unsupported_compressed_source"
		}
		// Only Codex explicitly maps physical recompression to the same adapter key.
		// For other adapters retain the physical source coordinate, not an inferred
		// append/epoch relationship. Session ownership still comes from the layout.
		logical.Path, logical.Key = path, sf.Key
		return logical, "zstd", ""
	}
	if sf.Role == adapters.RoleConfig {
		return sf, "", "protected_source_requires_scrub"
	}
	return sf, "", ""
}

func relativeKey(root, path string) string {
	rel, _ := filepath.Rel(root, path)
	return filepath.ToSlash(rel)
}

// Match the existing adapter-derived key convention without exporting adapter
// internals or using its 40-bit tag as session identity (sessions use full SHA256).
func derivedKey(a adapters.Adapter, root, rel string) string {
	if root == a.CanonicalRoot() {
		return rel
	}
	sum := sha256.Sum256([]byte(root))
	return "roots/" + hex.EncodeToString(sum[:])[:10] + "/" + rel
}

func classifyConfig(derived bool, root Root, a adapters.Adapter, path string, sf adapters.SessionFile, ok bool) (adapters.SessionFile, string, string) {
	ext := strings.ToLower(filepath.Ext(path))
	if !derived {
		return adapters.SessionFile{}, "", "protected_source_requires_scrub"
	}
	// The primary .claude.json is intentionally excluded by raw adapters. Only
	// this exact recognized name may use their scrubbed-export exception.
	if !ok && filepath.Base(path) == ".claude.json" {
		sf = adapters.SessionFile{Agent: root.Agent, Root: root.Path, Path: path, Key: derivedKey(a, root.Path, relativeKey(root.Path, path))}
		ok = true
	}
	if !ok {
		return adapters.SessionFile{}, "", "protected_source_requires_scrub"
	}
	// Recognizes includes compressed backups, which are not JSON/TOML input.
	if compressed(sf, ext) {
		return adapters.SessionFile{}, "", "protected_compressed_source_requires_decode_scrub"
	}
	sf.Role = adapters.RoleConfig
	return sf, "scrubbed-config", ""
}
