package scrub

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestFileRedactsSecretsAndKeepsEverythingElse(t *testing.T) {
	for _, c := range []struct {
		name, agent, file, in, want string
	}{
		{
			name:  "claude settings env",
			agent: "claude", file: "settings.json",
			in: `{
  "model": "opus",
  "env": {"ANTHROPIC_API_KEY": "sk-ant-abc123", "MAX_THINKING_TOKENS": "8000", "DEBUG": true, "TOKEN": "${GH_TOKEN}"}
}
`,
			want: `{
  "model": "opus",
  "env": {"ANTHROPIC_API_KEY": "[redacted]", "MAX_THINKING_TOKENS": "8000", "DEBUG": true, "TOKEN": "${GH_TOKEN}"}
}
`,
		},
		{
			name:  "claude.json api key, its suffixes and MCP servers at both levels",
			agent: "claude", file: "claude.json",
			in: `{"primaryApiKey": "sk-ant-api", "customApiKeyResponses": {"approved": ["abcdefghij0123456789"], "rejected": []},
 "mcpServers": {"gh": {"command": "npx", "env": {"GITHUB_TOKEN": "ghp_aaaa"}}},
 "projects": {"/home/me/proj": {"history": ["ghp_aaaa"], "mcpServers": {"api": {"url": "https://me:pw@api.example.com/mcp?key=k1&v=2",
   "headers": {"Authorization": "Bearer abc", "X-Ref": "Bearer ${API_TOKEN}"}}}}}}`,
			want: `{"primaryApiKey": "[redacted]", "customApiKeyResponses": {"approved": ["[redacted]"], "rejected": []},
 "mcpServers": {"gh": {"command": "npx", "env": {"GITHUB_TOKEN": "[redacted]"}}},
 "projects": {"/home/me/proj": {"history": ["[redacted]"], "mcpServers": {"api": {"url": "https://me@api.example.com/mcp?key=[redacted]&v=2",
   "headers": {"Authorization": "[redacted]", "X-Ref": "Bearer ${API_TOKEN}"}}}}}}`,
		},
		{
			name:  "project .mcp.json",
			agent: "claude", file: ".mcp.json",
			in:   `{"mcpServers": {"db": {"command": "db-mcp", "env": {"DB_URL": "postgres://u:hunter2@db/x"}, "oauth": {"clientId": "cid", "clientSecret": "cs"}}}}`,
			want: `{"mcpServers": {"db": {"command": "db-mcp", "env": {"DB_URL": "[redacted]"}, "oauth": {"clientId": "cid", "clientSecret": "[redacted]"}}}}`,
		},
		{
			name:  "pi models.json",
			agent: "pi", file: "models.json",
			in: `{"providers": {
  "a": {"apiKey": "sk-literal", "headers": {"X-Key": "!pass show x"}},
  "b": {"apiKey": "$OPENAI_KEY"}, "c": {"apiKey": "OPENAI_API_KEY"}, "d": {"apiKey": "!op read x"}}}`,
			want: `{"providers": {
  "a": {"apiKey": "[redacted]", "headers": {"X-Key": "!pass show x"}},
  "b": {"apiKey": "$OPENAI_KEY"}, "c": {"apiKey": "OPENAI_API_KEY"}, "d": {"apiKey": "!op read x"}}}`,
		},
		{
			name:  "codex config.toml keeps comments and layout",
			agent: "codex", file: "config.toml",
			in: `model = "gpt-5" # the default

[mcp_servers.gh]
command = "npx"
url = 'https://mcp.example.com/sse?token=t0k'
env = { GITHUB_TOKEN = "ghp_secret", LOG = "1" }
bearer_token = "bt"

[model_providers.corp]
base_url = "https://llm.corp/v1"
experimental_bearer_token = "corp-token"
http_headers = { "X-Api-Key" = "hk" }

[shell_environment_policy.set]
AWS_SECRET_ACCESS_KEY = "aws"
`,
			want: `model = "gpt-5" # the default

[mcp_servers.gh]
command = "npx"
url = "https://mcp.example.com/sse?token=[redacted]"
env = { GITHUB_TOKEN = "[redacted]", LOG = "1" }
bearer_token = "[redacted]"

[model_providers.corp]
base_url = "https://llm.corp/v1"
experimental_bearer_token = "[redacted]"
http_headers = { "X-Api-Key" = "[redacted]" }

[shell_environment_policy.set]
AWS_SECRET_ACCESS_KEY = "[redacted]"
`,
		},
		{
			name:  "a secret that is also a key name only changes the value",
			agent: "claude", file: "settings.json",
			in:   `{"env": {"X": "abc"}, "abc": 1}`,
			want: `{"env": {"X": "[redacted]"}, "abc": 1}`,
		},
		{
			name:  "nothing to redact leaves the bytes alone",
			agent: "claude", file: "settings.json",
			in:   "{\n\t\"model\": \"opus\"\n}",
			want: "{\n\t\"model\": \"opus\"\n}",
		},
		{
			name:  "files without rules are not parsed",
			agent: "claude", file: "CLAUDE.md",
			in:   "env: sk-secret {",
			want: "env: sk-secret {",
		},
		{
			name:  "a config.toml outside Codex has no rules",
			agent: "claude", file: "config.toml",
			in:   "not = [toml",
			want: "not = [toml",
		},
		{
			name:  "an empty file stays empty",
			agent: "pi", file: "mcp.json",
			in:   "",
			want: "",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := File(c.agent, c.file, []byte(c.in))
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(c.want, string(got)); diff != "" {
				t.Errorf("(-want +got):\n%s", diff)
			}
		})
	}
}

func TestFileRewritesWhenASecretIsWrittenWithEscapes(t *testing.T) {
	got, err := File("claude", "settings.json", []byte(`{"env": {"KEY": "\u0073ecret"}, "model": "opus"}`))
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"env\": {\n    \"KEY\": \"[redacted]\"\n  },\n  \"model\": \"opus\"\n}\n"
	if diff := cmp.Diff(want, string(got)); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}

	got, err = File("codex", "config.toml", []byte("[mcp_servers.x.env]\nK = \"\"\"\nsecret\"\"\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "secret") || !strings.Contains(string(got), Redacted) {
		t.Errorf("multi-line TOML secret: %q", got)
	}
}

func TestFileRefusesAConfigFileItCannotParse(t *testing.T) {
	for _, c := range []struct{ agent, file, in string }{
		{"claude", "settings.json", `{"env": {"KEY": "sk-`},
		{"codex", "config.toml", "[mcp_servers.x.env\nK = \"v\""},
	} {
		if got, err := File(c.agent, c.file, []byte(c.in)); err == nil {
			t.Errorf("%s: got %q, want an error", c.file, got)
		}
	}
}

func TestApplies(t *testing.T) {
	for _, c := range []struct {
		agent, file string
		want        bool
	}{
		{"claude", "settings.local.json", true},
		{"claude", "managed-mcp.json", true},
		{"codex", "managed_config.toml", true},
		{"pi", "models.json", true},
		{"claude", "models.json", false},
		{"pi", "config.toml", false},
		{"codex", "history.jsonl", false},
	} {
		if got := Applies(c.agent, c.file); got != c.want {
			t.Errorf("Applies(%s, %s) = %v", c.agent, c.file, got)
		}
	}
}

func TestRedactURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://api.example.com/mcp":                "https://api.example.com/mcp",
		"https://u@host/x":                           "https://u@host/x",
		"https://u:p@host/x":                         "https://u@host/x",
		"https://host/x?a=1&key=s3cr3t&ref=${TOKEN}": "https://host/x?a=1&key=[redacted]&ref=${TOKEN}",
		"https://host/x?flag":                        "https://host/x?flag",
		"${MCP_URL}":                                 "${MCP_URL}",
		"not a url":                                  Redacted,
		"http://[::1":                                Redacted,
	} {
		if got := redactURL(in); got != want {
			t.Errorf("redactURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func FuzzFile(f *testing.F) {
	files := []struct{ agent, name string }{
		{"claude", "settings.json"},
		{"claude", "claude.json"},
		{"claude", ".mcp.json"},
		{"pi", "models.json"},
		{"codex", "config.toml"},
	}
	f.Add(uint8(0), `{"env": {"K": "v", "N": "1"}}`)
	f.Add(uint8(2), `{"mcpServers": {"s": {"headers": {"A": "Bearer x"}, "url": "https://h/?k=v"}}}`)
	f.Add(uint8(3), `{"providers": {"p": {"apiKey": "k"}}}`)
	f.Add(uint8(4), "[mcp_servers.s.env]\nK = 'v'\n")
	f.Fuzz(func(t *testing.T, which uint8, in string) {
		file := files[int(which)%len(files)]
		out, err := File(file.agent, file.name, []byte(in))
		if err != nil || strings.TrimSpace(in) == "" {
			return
		}
		rs, form := rulesFor(file.agent, file.name)
		if !nothingLeftToRedact(form, out, rs) {
			t.Errorf("File(%s, %q) = %q, which still has secrets or doesn't parse", file.name, in, out)
		}
	})
}
