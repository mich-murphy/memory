package snapshot

import (
	"encoding/json"
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

func TestSnapshotStateSubtreeDirectoryAliasWithExternalLeaf(t *testing.T) {
	d := t.TempDir()
	state := filepath.Join(d, "state")
	alias := filepath.Join(d, "alias")
	outside := filepath.Join(d, "outside.md")
	if err := os.Mkdir(state, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("synthetic-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(state, "launch.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(state, alias); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_SESSIONS_STATE_DIR", state)
	c := newCollected("claude")
	if e := c.add("user", filepath.Join(alias, "launch.md"), "launch.md"); e != nil {
		t.Fatalf("state-subtree alias captured: %q", e.body)
	}
}

func TestSnapshotCredentialIdentityInSymlinkChain(t *testing.T) {
	d := t.TempDir()
	target := filepath.Join(d, "vault-entry")
	credential := filepath.Join(d, "auth.json")
	alias := filepath.Join(d, "skill.md")
	if err := os.WriteFile(target, []byte("synthetic-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, credential); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(credential, alias); err != nil {
		t.Fatal(err)
	}
	if e := newCollected("claude").add("user", alias, "skill.md"); e != nil {
		t.Fatalf("credential symlink chain captured: %q", e.body)
	}
}

func TestSnapshotScrubsIntermediateConfigAndRejectsCycles(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "innocent")
	settings := filepath.Join(dir, "settings.json.bak~")
	alias := filepath.Join(dir, "skill.md")
	if err := os.WriteFile(target, []byte(`{"env":{"KEY":"synthetic-secret"},"copy":"\u0073ynthetic-secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("innocent", settings); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(settings, alias); err != nil {
		t.Fatal(err)
	}
	e := newCollected("claude").add("user", alias, "skill.md")
	if e == nil {
		t.Fatal("config chain not captured")
	}
	var decoded struct {
		Env  map[string]string `json:"env"`
		Copy string            `json:"copy"`
	}
	if err := json.Unmarshal(e.body, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Env["KEY"] != "[redacted]" || decoded.Copy != "[redacted]" {
		t.Errorf("decoded chain output = %+v", decoded)
	}
	if strings.Contains(string(e.body), "synthetic-secret") {
		t.Errorf("chain output has literal secret: %q", e.body)
	}
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	if err := os.Symlink(b, a); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(a, b); err != nil {
		t.Fatal(err)
	}
	if got := newCollected("claude").add("user", a, "a"); got != nil {
		t.Error("cyclic input captured")
	}
	t.Setenv("AGENT_SESSIONS_CONFIG", a)
	c := newCollected("claude")
	if c.policyErr == nil || c.add("user", target, "innocent") != nil {
		t.Error("cyclic protected location did not fail closed")
	}
}
