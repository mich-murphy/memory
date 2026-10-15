package capturepolicy

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestBackupSuffixesProtectCopiesRepeatedly(t *testing.T) {
	for _, suffix := range []string{".before-upgrade", "-20260412-235959", ".before-update.bak-20260412-235959.gz", "-20260412-235959.before-upgrade.old", ".before-version.2"} {
		for _, name := range []string{"auth.json", "config.env", "notes.key", ".env.local"} {
			if !IsCredential(filepath.Join(t.TempDir(), strings.ToUpper(name+suffix))) {
				t.Errorf("IsCredential(%q) = false", name+suffix)
			}
		}
		if got := BaseName("models.json" + suffix); got != "models.json" {
			t.Errorf("BaseName = %q, want models.json", got)
		}
	}
	for _, name := range []string{"keyboard.txt", "token-count.json", "auth.json.example", "models.json.before", "file-20260101-12345"} {
		if IsCredential(name) {
			t.Errorf("IsCredential(%q) = true", name)
		}
	}
}

func TestNoiseUsesExplicitNamesAndResolvedIdentities(t *testing.T) {
	p, err := New("", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".DS_Store", "stats_cache.json", "stats-cache.json.bak", "logs_1.sqlite", "logs_1.sqlite.before-upgrade", ".codex-global-state.json-20260412-235959", "App.app/source.txt", "migration-backups/old.json"} {
		if !p.Excludes([]string{"alias.txt", filepath.Join(string(filepath.Separator), "agent", name)}) {
			t.Errorf("Excludes(%q) = false", name)
		}
	}
	for _, name := range []string{"state_1.sqlite", "logs.db", "other.sqlite", "token-cache.txt", "keymap.json", "package-lock.json", "git/pkg/source.ts", "npm/pkg/source.ts"} {
		if IsNoise(name) {
			t.Errorf("IsNoise(%q) = true", name)
		}
	}
}

func FuzzBaseName(f *testing.F) {
	for _, seed := range []string{
		"auth.json.before-upgrade.bak", "models.json-20260412-235959.gz", ".bak", "file.before-before-before", "AUTH.JSON~",
		"auth.json.before-a.jsonl", "auth.json.before-a.sqlite", "auth.json.before-a.bak.before-b.old",
		"auth.json.agent-sessions-backup---", "auth.json.bz2", "auth.json.~123~.tar.gz-20261014-235959.old",
		"/dir.bak/AUTH.JSON.BEFORE-K", "AUTH.JSON.BEFORE-İ", "auth.json.before-é",
		"auth.json\x00.bak", "auth.json.before-a\x00", "auth.json\xff.bak", "auth.json.bak\xff", "auth.json.before-a\n",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, path string) {
		name := BaseName(path)
		if want := regexpBaseName(path); name != want {
			t.Fatalf("BaseName(%q) = %q, want regexp %q", path, name, want)
		}
		if name != "" && BaseName(name) != name {
			t.Fatal("BaseName is not idempotent")
		}
		if name != strings.ToLower(name) {
			t.Fatal("BaseName did not fold case")
		}
		if len(path) > 0 && len(name) > len(strings.ToLower(filepath.Base(path))) {
			t.Fatal("BaseName grew a filename")
		}
	})
}
