package capturepolicy

import (
	"path/filepath"
	"testing"
)

func TestCredentialsIncludeRepeatedBackupSuffixes(t *testing.T) {
	for _, name := range []string{"auth.json", "oauth.json", "mcp-auth.json", ".credentials.json", "secret.key", "cert.pem", ".env", ".env.local", "config.env", ".claude.json"} {
		for _, suffix := range []string{"", ".bak", "~", ".old", ".orig", ".agent-sessions-backup-20261014-101010", ".bak.old~", ".agent-sessions-backup-20261014-101010.bak~"} {
			path := filepath.Join(string(filepath.Separator), "synthetic", name+suffix)
			if !IsCredential(path) {
				t.Errorf("IsCredential(%q) = false, want true", path)
			}
			if got := BaseName(path); got != name {
				t.Errorf("BaseName(%q) = %q, want %q", path, got, name)
			}
		}
	}
	for _, name := range []string{"settings.json", "auth.json.example", "notes.key.txt", "transcript.jsonl", "config.env.example", "models.json", "auth.json.agent-sessions-backup-not-a-time"} {
		if IsCredential(name) {
			t.Errorf("IsCredential(%q) = true, want false", name)
		}
	}
}

func TestExclusionsCheckBothIdentitiesAndCollectorLocations(t *testing.T) {
	for _, c := range []struct {
		logical, resolved, config, state string
		want                             bool
	}{
		{"/project/notes", "/home/auth.json.bak.old", "", "", true},
		{"/project/secret.key~", "/home/notes", "", "", true},
		{"/collector/custom.conf.bak~", "/collector/custom.conf.bak~", "/collector/custom.conf", "", true},
		{"/project/notes", "/collector/custom.conf.old", "/collector/custom.conf", "", true},
		{"/project/notes", "/collector/state/db.sqlite", "", "/collector/state", true},
		{"/collector/state", "/collector/state", "", "/collector/state", true},
		{"/collector/state/db.sqlite", "/project/notes", "", "/collector/state", true},
		{"/project/custom.conf", "/project/custom.conf", "/collector/custom.conf", "/collector/state", false},
		{"/collector/state-other/notes", "/collector/state-other/notes", "", "/collector/state", false},
		{"/project/notes", "/project/notes", "", "", false},
	} {
		if got := Excludes(c.logical, c.resolved, c.config, c.state); got != c.want {
			t.Errorf("Excludes(%q, %q, %q, %q) = %v, want %v", c.logical, c.resolved, c.config, c.state, got, c.want)
		}
	}
}
