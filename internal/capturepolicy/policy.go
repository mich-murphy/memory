// Package capturepolicy applies the same known credential and config boundaries to raw files,
// snapshots and external exports. It is not transcript DLP: unrecognised files pass unchanged.
package capturepolicy

import (
	"path/filepath"
	"regexp"
	"strings"
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
		filepath.Dir(path) == filepath.Dir(configPath) && BaseName(path) == BaseName(configPath)
}

func isWithin(path, dir string) bool {
	if dir == "" {
		return false
	}
	rel, err := filepath.Rel(dir, path)
	return err == nil && (rel == "." || filepath.IsLocal(rel))
}

// backupSuffix matches copies left by editors and the installer.
var backupSuffix = regexp.MustCompile(`(~|\.bak|\.orig|\.old|\.agent-sessions-backup-[0-9-]+)$`)

// BaseName strips known, possibly repeated backup suffixes from a file's basename.
func BaseName(path string) string {
	name := filepath.Base(path)
	for {
		base := backupSuffix.ReplaceAllString(name, "")
		if base == name {
			return name
		}
		name = base
	}
}
