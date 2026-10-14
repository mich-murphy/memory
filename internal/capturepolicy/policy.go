// Package capturepolicy applies the same known credential and config boundaries to raw files,
// snapshots and external exports. It is not transcript DLP: unrecognised files pass unchanged.
package capturepolicy

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mich-murphy/memory/internal/fsutil"
)

// IsCredential reports whether a path names a known credential file, including backup copies.
// The collector's config.env is excluded regardless of where it is found.
func IsCredential(path string) bool {
	name := BaseName(path)
	return name == ".credentials.json" || name == "auth.json" || name == "oauth.json" || name == "mcp-auth.json" ||
		name == "config.env" || strings.HasSuffix(name, ".key") || strings.HasSuffix(name, ".pem") ||
		strings.HasPrefix(name, ".claude.json") || name == ".env" || strings.HasPrefix(name, ".env.")
}

// Excludes checks both the logical path and its resolved target before the caller reads bytes.
// configPath and stateDir are the active collector locations; pass empty strings if unavailable.
// This two-identity compatibility helper cannot inspect intermediate symlink names. New capture
// paths should use Resolve and Policy.Excludes to check every ancestor and leaf identity.
func Excludes(logical, resolved, configPath, stateDir string) bool {
	for _, path := range []string{logical, resolved} {
		if IsCredential(path) || isConfig(path, configPath) || isWithin(path, stateDir) {
			return true
		}
	}
	return false
}

func isConfig(path, configPath string) bool {
	if configPath == "" {
		return false
	}
	return filepath.Clean(path) == filepath.Clean(configPath) ||
		BaseName(path) == BaseName(configPath) && fsutil.SameLocation(filepath.Dir(path), filepath.Dir(configPath))
}

func isWithin(path, dir string) bool {
	if dir == "" {
		return false
	}
	rel, err := filepath.Rel(dir, path)
	if err == nil && (rel == "." || filepath.IsLocal(rel)) {
		return true
	}
	// Resolved path spellings can still differ on case-insensitive filesystems. Walk
	// ancestors by filesystem identity, including intermediate paths supplied by Resolve.
	// Do not lowercase whole paths: distinct directories on case-sensitive volumes
	// must remain distinct. Metadata checks do not read transcript contents.
	root, err := os.Stat(dir)
	if err != nil {
		return false // An unavailable location cannot establish an additional alias.
	}
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		info, statErr := os.Stat(current)
		if statErr == nil && os.SameFile(info, root) {
			return true
		}
		if filepath.Dir(current) == current {
			return false
		}
	}
}

// backupSuffix recognises editor, dated and compressed copies without decoding their contents.
// Do not treat every suffix as a backup: auth.json.example and notes.key.txt are unrelated names.
var backupSuffix = regexp.MustCompile(`(\.?~[0-9]+~|~|\.bak|\.backup|\.orig|\.old|-[0-9]{8}|\.(gz|bz2|xz|zst|zstd|zip|7z|rar|tar|tgz)|\.agent-sessions-backup-[0-9-]+)$`)

// BaseName folds filename case and strips known, possibly repeated backup suffixes.
// Conservative case folding also protects aliases on case-insensitive filesystems.
func BaseName(path string) string {
	name := strings.ToLower(filepath.Base(path))
	for {
		base := backupSuffix.ReplaceAllString(name, "")
		if base == name {
			return name
		}
		name = base
	}
}
