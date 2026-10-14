package snapshot

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mich-murphy/memory/internal/adapters"
)

func TestSnapshotScrubsCrossAgentConfigAliases(t *testing.T) {
	for _, tc := range []struct {
		name, agent, config, body string
	}{
		{"claudePi", "claude", "models.json", `{"providers":{"test":{"apiKey":"synthetic-cross-secret"}}}`},
		{"claudeCodex", "claude", "config.toml", "[mcp_servers.test]\nbearer_token = \"synthetic-cross-secret\"\n"},
		{"piClaude", "pi", "settings.json", `{"env":{"API_KEY":"synthetic-cross-secret"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("AGENT_SESSIONS_CONFIG", filepath.Join(dir, "collector.conf"))
			t.Setenv("AGENT_SESSIONS_STATE_DIR", filepath.Join(dir, "state"))
			target := filepath.Join(dir, tc.config)
			alias := filepath.Join(dir, "SKILL.md")
			if err := os.WriteFile(target, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, alias); err != nil {
				t.Fatal(err)
			}
			e := newCollected(tc.agent).add("user", alias, "SKILL.md")
			if e == nil {
				t.Fatal("cross-agent config alias not captured")
			}
			if strings.Contains(string(e.body), "synthetic-cross-secret") || !strings.Contains(string(e.body), "[redacted]") {
				t.Error("snapshot.add retained the cross-agent config credential")
			}
		})
	}
}

func TestSnapshotBuildProtectsCrossAgentAliasesAtBothSizes(t *testing.T) {
	for _, tc := range []struct{ name, config, body string }{
		{"piModels", "models.json", `{"providers":{"s":{"apiKey":"synthetic-cross-secret"}},"copy":"\u0073ynthetic-cross-secret"}`},
		{"codexConfig", "config.toml", "[mcp_servers.s]\nbearer_token = 'synthetic-cross-secret'\n[copy]\nvalue = \"\\u0073ynthetic-cross-secret\"\n"},
	} {
		for _, size := range []struct {
			name    string
			padding int
		}{{"small", 0}, {"large", 1 << 20}} {
			t.Run(tc.name+"/"+size.name, func(t *testing.T) {
				dir := t.TempDir()
				t.Setenv("AGENT_SESSIONS_CONFIG", filepath.Join(dir, "collector.conf"))
				t.Setenv("AGENT_SESSIONS_STATE_DIR", filepath.Join(dir, "state"))
				root := filepath.Join(dir, ".claude")
				target := filepath.Join(dir, "foreign", tc.config+".bak.old~")
				alias := filepath.Join(root, "skills", "alias", "SKILL.md")
				for _, path := range []string{target, alias} {
					if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
						t.Fatal(err)
					}
				}
				body := tc.body + strings.Repeat(" ", size.padding)
				if err := os.WriteFile(target, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, alias); err != nil {
					t.Fatal(err)
				}
				snap, err := Build(adapters.NewClaude(dir), root, "", "")
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, file := range snap.Harness.Files {
					if file.Scope != "user" || file.Path != "skills/alias/SKILL.md" {
						continue
					}
					found = true
					body := snap.Blobs[file.SHA256]
					if bytes.Contains(body, []byte("synthetic-cross-secret")) || bytes.Count(body, []byte("[redacted]")) != 2 {
						t.Error("Build published cross-agent credentials or their escaped copies")
					}
					if want := realPath(target); file.Target != want {
						t.Errorf("Target = %q, want %q", file.Target, want)
					}
				}
				if !found {
					t.Fatal("Build did not include cross-agent skill alias")
				}
			})
		}
	}
}

func TestSnapshotRejectsMalformedCrossAgentIntermediateConfigs(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"models.json", `{"providers":{"s":{"apiKey":"synthetic-secret"}},"providers":{}}`},
		{"models.json", `[{"providers":{"s":{"apiKey":"synthetic-secret"}}}]`},
		{"config.toml", "[mcp_servers.s]\nbearer_token = synthetic-secret\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("AGENT_SESSIONS_CONFIG", filepath.Join(dir, "collector.conf"))
			t.Setenv("AGENT_SESSIONS_STATE_DIR", filepath.Join(dir, "state"))
			target := filepath.Join(dir, "innocent")
			config := filepath.Join(dir, tc.name+".bak~")
			alias := filepath.Join(dir, "SKILL.md")
			if err := os.WriteFile(target, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			for _, link := range []struct{ target, path string }{{target, config}, {config, alias}} {
				if err := os.Symlink(link.target, link.path); err != nil {
					t.Fatal(err)
				}
			}
			c := newCollected("claude")
			if got := c.add("user", alias, "SKILL.md"); got != nil {
				t.Error("snapshot.add captured malformed cross-agent protected config")
			}
			if len(c.skipped) != 1 || c.skipped[0].Reason != "unparseable" || len(c.entries) != 0 {
				t.Errorf("malformed alias entries = %d, skipped = %+v", len(c.entries), c.skipped)
			}
		})
	}
}
