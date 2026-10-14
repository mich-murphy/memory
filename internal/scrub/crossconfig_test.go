package scrub

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestRecognizesEverySupportedSchemaWithoutAnAgent(t *testing.T) {
	for _, name := range []string{
		"claude.json", ".claude.json", "settings.json", "settings.local.json", "managed-settings.json",
		".mcp.json", "mcp.json", "managed-mcp.json", "models.json", "config.toml", "managed_config.toml",
	} {
		for _, suffix := range []string{"", ".bak.old~", ".agent-sessions-backup-20261014-101010"} {
			path := "/other-agent/" + name + suffix
			if !Recognizes(path) {
				t.Errorf("Recognizes(%q) = false, want true", path)
			}
		}
	}
	for _, name := range []string{"SKILL.md", "transcript.jsonl", "models-store.json", "settings.json.bak2", "config.toml.txt"} {
		if Recognizes(name) {
			t.Errorf("Recognizes(%q) = true, want false", name)
		}
	}
}

func TestEveryAgentProtectsEveryConfigIdentity(t *testing.T) {
	for _, agent := range []string{"claude", "codex", "pi"} {
		t.Run(agent, func(t *testing.T) {
			for _, tc := range []struct{ name, input, want string }{
				{"models.json", `{"providers":{"s":{"apiKey":"synthetic-secret","headers":{"X-Key":"synthetic-header"}},"ref":{"apiKey":"OPENAI_API_KEY"}}}`, `{"providers":{"s":{"apiKey":"[redacted]","headers":{"X-Key":"[redacted]"}},"ref":{"apiKey":"OPENAI_API_KEY"}}}`},
				{"config.toml", "[mcp_servers.s]\nbearer_token = 'synthetic-secret'\n", "[mcp_servers.s]\nbearer_token = \"[redacted]\"\n"},
				{"settings.json", `{"env":{"API_KEY":"synthetic-secret"},"hooks":{"Stop":[{"hooks":[{"headers":{"Authorization":"synthetic-header"}}]}]},"model":"opus"}`, `{"env":{"API_KEY":"[redacted]"},"hooks":{"Stop":[{"hooks":[{"headers":{"Authorization":"[redacted]"}}]}]},"model":"opus"}`},
			} {
				t.Run(tc.name, func(t *testing.T) {
					// Cover direct file capture and logical, intermediate and resolved aliases.
					out, err := File(agent, tc.name, []byte(tc.input))
					if err != nil {
						t.Fatal(err)
					}
					if diff := cmp.Diff(tc.want, string(out)); diff != "" {
						t.Errorf("File (-want +got):\n%s", diff)
					}
					for _, identities := range [][]string{
						{tc.name + ".bak.old~", "innocent.md"},
						{"SKILL.md", "/other-agent/" + tc.name},
						{"SKILL.md", tc.name + ".orig", "innocent", tc.name + ".orig"},
					} {
						out, err = Identities(agent, identities, []byte(tc.input))
						if err != nil {
							t.Fatal(err)
						}
						if diff := cmp.Diff(tc.want, string(out)); diff != "" {
							t.Errorf("Identities(%v) (-want +got):\n%s", identities, diff)
						}
					}
				})
			}
		})
	}
}

func TestCrossAgentIdentitiesUnionOnlyEncounteredSchemas(t *testing.T) {
	input := []byte(`{"env":{"KEY":"synthetic-env"},"providers":{"s":{"apiKey":"synthetic-api"}},"mcpServers":{"s":{"headers":{"X-Key":"synthetic-header"}}}}`)
	out, err := Identities("codex", []string{"SKILL.md", "settings.json", "models.json.bak~", "mcp.json", "innocent"}, input)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "synthetic-") || strings.Count(string(out), Redacted) != 3 {
		t.Error("Identities failed to combine encountered JSON schemas")
	}
	// Do not turn every known JSON filename into a catch-all for every JSON schema.
	out, err = Identities("claude", []string{"SKILL.md", "models.json"}, input)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte("synthetic-env")) || !bytes.Contains(out, []byte("synthetic-header")) || bytes.Contains(out, []byte("synthetic-api")) {
		t.Error("Identities applied schemas not associated with the encountered filenames")
	}
}

func TestCrossAgentProtectedConfigsFailClosed(t *testing.T) {
	for _, tc := range []struct{ name, input string }{
		{"models.json", `{"providers":{"s":{"apiKey":"synthetic-secret"}},"providers":{}}`},
		{"models.json", `{"providers":{"s":{"apiKey":"synthetic-secret","\u0061piKey":"$REF"}}}`},
		{"models.json", `{"providers":{"s":{"apiKey":"\ud800"}}}`},
		{"models.json", "{\"providers\":{\"s\":{\"apiKey\":\"bad\xff\"}}}"},
		{"models.json", `[{"providers":{"s":{"apiKey":"synthetic-secret"}}}]`},
		{"models.json", `null`},
		{"models.json", `{} {}`},
		{"config.toml", "[mcp_servers.s]\nbearer_token = synthetic-secret\n"},
		{"config.toml", "[mcp_servers.s]\nbearer_token = \"synthetic-secret\\z\"\n"},
		{"config.toml", "[mcp_servers.s]\nbearer_token = 'synthetic-secret'\nbearer_token = '$REF'\n"},
	} {
		for _, agent := range []string{"claude", "codex", "pi"} {
			out, err := Identities(agent, []string{"SKILL.md", tc.name + ".bak.old~", "innocent"}, []byte(tc.input))
			if err == nil || out != nil {
				t.Errorf("Identities(%s, %s) returned publishable malformed config", agent, tc.name)
			} else if strings.Contains(err.Error(), "synthetic-secret") {
				t.Error("protected config parser error exposed a credential")
			}
		}
	}
}

func TestCrossAgentJSONPreservesKeysAndScrubsUnicodeAndNewlines(t *testing.T) {
	input := []byte("{\"providers\":{\"s\":{\"apiKey\":\"synthetic-\\u2603\\nsecret\"}},\"copy\":\"synthetic-☃\\nsecret\",\"synthetic-☃\\nsecret\"\r\n:1}")
	out, err := Identities("claude", []string{"SKILL.md", "models.json~", "innocent"}, input)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["copy"] != Redacted || doc["providers"].(map[string]any)["s"].(map[string]any)["apiKey"] != Redacted || doc["synthetic-☃\nsecret"] != float64(1) {
		t.Error("cross-agent JSON did not redact decoded values while retaining keys")
	}
}

func TestUnknownCrossAgentIdentitiesRemainByteExact(t *testing.T) {
	input := []byte("transcript literal synthetic-secret\n{\"providers\":{\"s\":{\"apiKey\":\"synthetic-secret\"}}}\nnot = [toml\xff")
	for _, agent := range []string{"claude", "codex", "pi"} {
		out, err := Identities(agent, []string{"SKILL.md", "models.json.bak2", "config.toml.txt", "transcript.jsonl"}, input)
		if err != nil || !bytes.Equal(out, input) {
			t.Errorf("Identities(%s) altered unknown-file bytes or parsed them: %v", agent, err)
		}
	}
}
