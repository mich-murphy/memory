package snapshot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSnapshotChecksLogicalAndResolvedSecurityIdentities(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "collector", "custom.conf")
	stateDir := filepath.Join(dir, "state")
	t.Setenv("AGENT_SESSIONS_CONFIG", configPath)
	t.Setenv("AGENT_SESSIONS_STATE_DIR", stateDir)
	for _, path := range []string{configPath + ".bak~", filepath.Join(stateDir, "launch.json"), filepath.Join(dir, "auth.json.bak.old~"), filepath.Join(dir, "secret.key.bak"), filepath.Join(dir, "settings.json"), filepath.Join(dir, "innocent.md"), filepath.Join(dir, "malformed", "settings.json")} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		body := `{"env":{"KEY":"synthetic-secret"}}`
		if strings.Contains(path, "malformed") {
			body = `{"env":{"KEY":"synthetic-secret"},"env":{}}`
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	c := newCollected("claude")
	for i, target := range []string{configPath + ".bak~", filepath.Join(stateDir, "launch.json"), filepath.Join(dir, "auth.json.bak.old~"), filepath.Join(dir, "secret.key.bak")} {
		alias := filepath.Join(dir, "alias"+string(rune('a'+i)))
		if err := os.Symlink(target, alias); err != nil {
			t.Fatal(err)
		}
		if got := c.add("user", alias, alias); got != nil {
			t.Errorf("credential alias %q captured", alias)
		}
		if got := c.add("user", target, target); got != nil {
			t.Errorf("credential backup %q captured", target)
		}
	}
	alias := filepath.Join(dir, "harmless.md")
	if err := os.Symlink(filepath.Join(dir, "settings.json"), alias); err != nil {
		t.Fatal(err)
	}
	recognised := filepath.Join(dir, "settings.local.json.bak~")
	if err := os.Symlink(filepath.Join(dir, "innocent.md"), recognised); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{alias, recognised} {
		e := c.add("user", path, path)
		if e == nil || strings.Contains(string(e.body), "synthetic-secret") || !strings.Contains(string(e.body), "[redacted]") {
			t.Errorf("config alias %q = %+v, want redacted entry", path, e)
		}
	}
	badAlias := filepath.Join(dir, "bad.md")
	if err := os.Symlink(filepath.Join(dir, "malformed", "settings.json"), badAlias); err != nil {
		t.Fatal(err)
	}
	if got := c.add("user", badAlias, badAlias); got != nil || len(c.skipped) != 1 || c.skipped[0].Reason != "unparseable" {
		t.Errorf("malformed alias = %+v, skipped %+v", got, c.skipped)
	}
}

func TestSnapshotExcludesResolvedCustomCollectorConfig(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "innocent.txt")
	custom := filepath.Join(dir, "custom.conf")
	alias := filepath.Join(dir, "alias.md")
	t.Setenv("AGENT_SESSIONS_CONFIG", custom)
	if err := os.WriteFile(target, []byte("synthetic-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{custom, alias} {
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(custom+".bak.old", []byte("synthetic-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := newCollected("claude")
	for _, path := range []string{target, alias, custom + ".bak.old"} {
		if e := c.add("user", path, path); e != nil {
			t.Errorf("collector config identity %q captured", path)
		}
	}
}

func TestSnapshotExcludesLogicalStateDirectoryWithExternalTarget(t *testing.T) {
	dir := t.TempDir()
	stateTarget := filepath.Join(dir, "state-target")
	stateAlias := filepath.Join(dir, "state-alias")
	outside := filepath.Join(dir, "notes.txt")
	if err := os.Mkdir(stateTarget, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(stateTarget, stateAlias); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("synthetic-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(stateAlias, "launch.json")
	if err := os.Symlink(outside, inside); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_SESSIONS_STATE_DIR", stateAlias)
	c := newCollected("claude")
	if e := c.add("user", inside, inside); e != nil {
		t.Errorf("logical collector state entry captured: %+v", e)
	}
}
