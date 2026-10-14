package scrub

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

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
			name:  "a TOML secret that is also a quoted key only changes the value",
			agent: "codex", file: "config.toml",
			in:   "\"abc\" = 1\n\n[mcp_servers.s.env]\nK = \"abc\"\n",
			want: "\"abc\" = 1\n\n[mcp_servers.s.env]\nK = \"[redacted]\"\n",
		},
		{
			name:  "a TOML file without a final newline",
			agent: "codex", file: "config.toml",
			in:   "[mcp_servers.s]\nbearer_token = \"bt\"",
			want: "[mcp_servers.s]\nbearer_token = \"[redacted]\"",
		},
		{
			name:  "a secret escaped as Go's encoder writes it is replaced in place",
			agent: "claude", file: "settings.json",
			in:   `{"env": {"DB": "a=1\u0026b=2"},  "model": "opus"}`,
			want: `{"env": {"DB": "[redacted]"},  "model": "opus"}`,
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
			name:  "a config.toml collected by Claude still has Codex rules",
			agent: "claude", file: "config.toml",
			in:   "[mcp_servers.s]\nbearer_token = 'synthetic-token'\n",
			want: "[mcp_servers.s]\nbearer_token = \"[redacted]\"\n",
		},
		{
			name:  "MCP server args in claude.json at both levels",
			agent: "claude", file: "claude.json",
			in: `{"mcpServers": {"c7": {"command": "npx", "args": ["-y", "@upstash/context7-mcp", "--api-key", "ctx7sk-1"]}},
 "projects": {"/p": {"mcpServers": {"r": {"command": "mcp-remote", "args": ["https://h/mcp", "--header", "Authorization: Bearer t0k", "--token=t1", "--max-tokens", "9"]}}}}}`,
			want: `{"mcpServers": {"c7": {"command": "npx", "args": ["-y", "@upstash/context7-mcp", "--api-key", "[redacted]"]}},
 "projects": {"/p": {"mcpServers": {"r": {"command": "mcp-remote", "args": ["https://h/mcp", "--header", "Authorization: [redacted]", "--token=[redacted]", "--max-tokens", "9"]}}}}}`,
		},
		{
			name:  "pi mcp.json args keep references",
			agent: "pi", file: "mcp.json",
			in:   `{"mcpServers": {"s": {"args": ["--api-key", "${CTX7_KEY}", "-H", "X-Key: k1"]}}}`,
			want: `{"mcpServers": {"s": {"args": ["--api-key", "${CTX7_KEY}", "-H", "X-Key: [redacted]"]}}}`,
		},
		{
			name:  "claude settings http hook headers",
			agent: "claude", file: "settings.json",
			in:   `{"hooks": {"Stop": [{"hooks": [{"type": "http", "url": "https://h/x", "headers": {"Authorization": "Bearer ghp_1", "X-Ref": "$MY_TOKEN"}}]}]}}`,
			want: `{"hooks": {"Stop": [{"hooks": [{"type": "http", "url": "https://h/x", "headers": {"Authorization": "[redacted]", "X-Ref": "$MY_TOKEN"}}]}]}}`,
		},
		{
			name:  "codex MCP args and otel exporter headers",
			agent: "codex", file: "config.toml",
			in: `[mcp_servers.c7]
command = "npx"
args = ["-y", "@upstash/context7-mcp", "--api-key", "ctx7sk-1"]

[otel.exporter.otlp-http]
endpoint = "https://otel.example/v1"
headers = { "x-otlp-api-key" = "ok1" }

[otel.trace_exporter.otlp-grpc.headers]
authorization = "Bearer ${OTEL_TOKEN}"
`,
			want: `[mcp_servers.c7]
command = "npx"
args = ["-y", "@upstash/context7-mcp", "--api-key", "[redacted]"]

[otel.exporter.otlp-http]
endpoint = "https://otel.example/v1"
headers = { "x-otlp-api-key" = "[redacted]" }

[otel.trace_exporter.otlp-grpc.headers]
authorization = "Bearer ${OTEL_TOKEN}"
`,
		},
		{
			name:  "an empty unprotected file stays empty",
			agent: "pi", file: "SKILL.md",
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

func TestBackupCopiesAreScrubbedLikeTheirOriginal(t *testing.T) {
	for _, name := range []string{
		"settings.json.agent-sessions-backup-20261014-101010", "settings.json~", "settings.json.bak",
		"settings.local.json.orig", "managed-settings.json.old",
	} {
		got, err := File("claude", name, []byte(`{"env": {"KEY": "sk-1"}}`))
		if err != nil {
			t.Fatal(err)
		}
		if want := `{"env": {"KEY": "[redacted]"}}`; string(got) != want {
			t.Errorf("File(%s) = %s, want %s", name, got, want)
		}
		if got, err := File("claude", name, []byte(`{"env": {"KEY": "sk-`)); err == nil {
			t.Errorf("File(%s) of a broken copy = %q, want an error", name, got)
		}
	}
	if got, err := File("claude", "settings.json.bak2", []byte(`{"env": {"KEY": "sk-1"}`)); err != nil || !strings.Contains(string(got), "sk-1") {
		t.Errorf("an unrelated name was scrubbed: %q, %v", got, err)
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
		{"claude", "models.json", true},
		{"pi", "config.toml", true},
		{"codex", "history.jsonl", false},
		{"codex", "config.toml.agent-sessions-backup-20261014-101010", true},
		{"pi", "models.json~", true},
	} {
		if got := Applies(c.agent, c.file); got != c.want {
			t.Errorf("Applies(%s, %s) = %v", c.agent, c.file, got)
		}
	}
}

func TestArgsRedactsSecretFlagValues(t *testing.T) {
	for _, c := range []struct {
		name     string
		in, want []string
	}{
		{"flag and value", []string{"pi", "--api-key", "sk-1", "--model", "m"}, []string{"pi", "--api-key", Redacted, "--model", "m"}},
		{"flag=value", []string{"x", "--auth-token=t", "--client-secret=s", "--password", "p"}, []string{"x", "--auth-token=" + Redacted, "--client-secret=" + Redacted, "--password", Redacted}},
		{"whole words only", []string{"x", "--max-tokens", "9", "--keyboard", "us", "--monkey", "y"}, []string{"x", "--max-tokens", "9", "--keyboard", "us", "--monkey", "y"}},
		{"no value", []string{"codex", "login", "--with-api-key", "--verbose"}, []string{"codex", "login", "--with-api-key", "--verbose"}},
		{"references", []string{"x", "--token", "$GH_TOKEN", "--api-key=${K}"}, []string{"x", "--token", "$GH_TOKEN", "--api-key=${K}"}},
		{"headers", []string{"curl", "-H", "Authorization: Bearer t", "--header=X-Key: k", "-H", "Accept: ${A}"}, []string{"curl", "-H", "Authorization: " + Redacted, "--header=X-Key: " + Redacted, "-H", "Accept: ${A}"}},
		{"name=value", []string{"codex", "-c", "mcp_servers.gh.env.GITHUB_TOKEN=ghp", "-c", "model=o3"}, []string{"codex", "-c", "mcp_servers.gh.env.GITHUB_TOKEN=" + Redacted, "-c", "model=o3"}},
		{
			"inline JSON",
			[]string{"claude", "--mcp-config", `{"mcpServers":{"s":{"headers":{"Authorization":"Bearer t"}}}}`, "--settings", `{"env":{"API_KEY":"k"},"model":"opus"}`, "{not json"},
			[]string{"claude", "--mcp-config", `{"mcpServers":{"s":{"headers":{"Authorization":"[redacted]"}}}}`, "--settings", `{"env":{"API_KEY":"[redacted]"},"model":"opus"}`, Redacted},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			if diff := cmp.Diff(c.want, Args(c.in)); diff != "" {
				t.Errorf("Args(%q) (-want +got):\n%s", c.in, diff)
			}
		})
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
	f.Add(uint8(0), `{"env":{"K":"synthetic-secret"},"env":{}}`)
	f.Add(uint8(0), `{"env":{"K":"synthetic-secret"},"copy":"\u0073ynthetic-secret"}`)
	f.Add(uint8(0), `{"env":{"K":"${VAR:-synthetic-secret}"}}`)
	f.Add(uint8(4), "[mcp_servers.s.env]\nK = 'synthetic-secret'\n[copy]\nvalue = \"\\u0073ynthetic-secret\"\n")
	f.Add(uint8(2), `{"mcpServers": {"s": {"headers": {"A": "Bearer x"}, "url": "https://h/?k=v"}}}`)
	f.Add(uint8(2), `{"mcpServers": {"s": {"args": ["--token", "t", "-H", "A: b", "{\"env\": {\"K\": \"v\"}}"]}}}`)
	f.Add(uint8(3), `{"providers": {"p": {"apiKey": "k"}}}`)
	f.Add(uint8(4), "[mcp_servers.s.env]\nK = 'v'\n")
	f.Fuzz(func(t *testing.T, which uint8, in string) {
		file := files[int(which)%len(files)]
		out, err := File(file.agent, file.name, []byte(in))
		if err != nil || strings.TrimSpace(in) == "" {
			return
		}
		rs, form := rulesFor(file.name)
		if !nothingLeftToRedact(form, out, rs) {
			t.Errorf("File(%s, %q) = %q, which still has secrets or doesn't parse", file.name, in, out)
		}
	})
}

func TestProtectedJSONRejectsDuplicatesAtEveryDepth(t *testing.T) {
	for _, input := range []string{
		`{"env":{"TOKEN":"synthetic-secret"},"env":{}}`,
		`{"env":{"TOKEN":"synthetic-secret","TOKEN":"$REF"}}`,
		`{"env":{"TOKEN":"synthetic-secret","\u0054OKEN":"$REF"}}`,
		`{"other":[{"env":{"TOKEN":"synthetic-secret"},"env":{}}]}`,
		`{"other":{"duplicate":1,"duplicate":2}}`,
		`{"env":{"TOKEN":"synthetic-secret"},"\u0065nv":{}}`,
		`{"env":{"TOKEN":"synthetic-secret"}} {}`,
		`{"env":{"TOKEN":"\ud800"}}`,
		"", " \n\t",
	} {
		out, err := File("claude", "settings.json.bak.old~", []byte(input))
		if err == nil || out != nil {
			t.Errorf("File(%q) = %q, %v, want no output and error", input, out, err)
		}
	}
	out, err := File("codex", "config.toml", []byte("[mcp_servers.s.env]\nTOKEN = 'synthetic-secret'\nTOKEN = '$REF'\n"))
	if err == nil || out != nil {
		t.Errorf("duplicate TOML = %q, %v, want no output and error", out, err)
	}
}

// The assertions use encoding/json directly, not the scrubber's validator/decoder or rules.
// All copies are explicit, independent expectations; a lossy duplicate parser is not an oracle.
func assertNoJSONSecret(t *testing.T, output []byte, secret string) {
	t.Helper()
	if bytes.Contains(output, []byte(secret)) {
		t.Fatalf("output contains literal synthetic secret: %q", output)
	}
	var doc any
	if err := json.Unmarshal(output, &doc); err != nil {
		t.Fatal(err)
	}
	var check func(any)
	check = func(node any) {
		switch n := node.(type) {
		case string:
			if n == secret {
				t.Fatalf("output contains decoded synthetic secret: %q", output)
			}
		case map[string]any:
			for _, value := range n {
				check(value)
			}
		case []any:
			for _, value := range n {
				check(value)
			}
		}
	}
	check(doc)
}

func TestEquivalentEscapedCopiesAreAllRedacted(t *testing.T) {
	for _, input := range []string{
		`{"env":{"KEY":"synthetic-secret"},"copies":["\u0073ynthetic-secret",{"value":"synthetic\u002dsecret"}]}`,
		`{"env":{"KEY":"\u0073ynthetic-secret"},"copies":["synthetic-secret","synthetic\u002dsecret"]}`,
	} {
		out, err := File("claude", "settings.json", []byte(input))
		if err != nil {
			t.Fatal(err)
		}
		assertNoJSONSecret(t, out, "synthetic-secret")
		if got := strings.Count(string(out), Redacted); got != 3 {
			t.Errorf("redactions = %d, want 3: %q", got, out)
		}
	}
	input := "[mcp_servers.s.env]\nKEY = 'synthetic-secret'\n[other]\ncopy = \"\\u0073ynthetic-secret\"\nmultiline = \"\"\"\nsynthetic-secret\"\"\"\n"
	out, err := File("codex", "config.toml", []byte(input))
	if err != nil {
		t.Fatal(err)
	}
	// Independent expected output count also checks escaped and multiline decoded copies, rather
	// than trusting the scrubber's own TOML decoder to identify its missed occurrences.
	if strings.Contains(string(out), "synthetic-secret") || strings.Count(string(out), Redacted) != 3 {
		t.Errorf("TOML copies not all redacted: %q", out)
	}
}

func TestReferencesAndCommandsAreLimitedToSupportedSyntaxAndFields(t *testing.T) {
	for _, value := range []string{"${VAR:-synthetic-secret}", "${VAR:=synthetic-secret}", "${VAR:+synthetic-secret}", "${VAR?synthetic-secret}", "${}", "!printf synthetic-secret", "$VAR-synthetic-secret"} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		input := append([]byte(`{"env":{"KEY":`), encoded...)
		input = append(input, []byte(`}}`)...)
		out, err := File("pi", "settings.json", input)
		if err != nil {
			t.Fatal(err)
		}
		if string(out) != `{"env":{"KEY":"[redacted]"}}` {
			t.Errorf("File(%q) = %q, want redacted value", value, out)
		}
	}
	for _, value := range []string{"${VAR}", "$VAR", "Bearer ${TOKEN}", "Basic $TOKEN", "Token ${TOKEN}"} {
		if !holdsNoSecret(value) {
			t.Errorf("holdsNoSecret(%q) = false", value)
		}
	}
	input := `{"providers":{"p":{"apiKey":"!pass show p","headers":{"X-Key":"!pass show h"}}}}`
	out, err := File("pi", "models.json", []byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != input {
		t.Errorf("Pi supported commands = %q, want %q", out, input)
	}
}

func TestPathsProtectsEitherRecognisedIdentity(t *testing.T) {
	for _, paths := range [][2]string{{"/skills/innocent.md", "/elsewhere/settings.json.bak.old~"}, {"/skills/settings.json~", "/elsewhere/innocent.md"}} {
		out, err := Paths("claude", paths[0], paths[1], []byte(`{"env":{"KEY":"synthetic-secret"}}`))
		if err != nil {
			t.Fatal(err)
		}
		assertNoJSONSecret(t, out, "synthetic-secret")
	}
	if out, err := Paths("claude", "settings.json", "innocent.md", []byte("bad")); err == nil || out != nil {
		t.Errorf("Paths malformed logical = %q, %v", out, err)
	}
	if out, err := Paths("claude", "innocent.md", "settings.json", []byte("bad")); err == nil || out != nil {
		t.Errorf("Paths malformed target = %q, %v", out, err)
	}
}

func FuzzEquivalentSecretCopies(f *testing.F) {
	for _, seed := range []string{"token", "quotes\"\\", "Unicode ☃😀", "line\nbreak", "<>&"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, suffix string) {
		if !utf8.ValidString(suffix) {
			t.Skip()
		}
		secret := "synthetic-secret-" + suffix
		literal, err := json.Marshal(secret)
		if err != nil {
			t.Fatal(err)
		}
		// Escape the leading ASCII s only: the copy always decodes to exactly the same value.
		escaped := `"\u0073` + string(literal[2:])
		input := `{"env":{"KEY":` + string(literal) + `},"copy":` + escaped + `}`
		out, err := File("claude", "settings.json", []byte(input))
		if err != nil {
			t.Fatal(err)
		}
		assertNoJSONSecret(t, out, secret)
		var doc struct {
			Copy string            `json:"copy"`
			Env  map[string]string `json:"env"`
		}
		if err := json.Unmarshal(out, &doc); err != nil {
			t.Fatal(err)
		}
		if doc.Copy != Redacted || doc.Env["KEY"] != Redacted {
			t.Errorf("decoded values = %+v, want both redacted", doc)
		}
	})
}

func TestJSONSecretNamedKeysKeepEveryWhitespaceSeparator(t *testing.T) {
	for _, space := range []string{"", " ", "\t", "\n", "\r", "\r\n", " \t\r\n"} {
		input := `{"env":{"KEY":"synthetic-secret"},"synthetic-secret"` + space + `:1,"nested":[{"synthetic-secret"` + space + `:"synthetic-secret"}]}`
		output, err := File("claude", "settings.json", []byte(input))
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		if err := json.Unmarshal(output, &doc); err != nil {
			t.Fatal(err)
		}
		if got, ok := doc["synthetic-secret"]; !ok || got != float64(1) {
			t.Errorf("key whitespace %q: output key/value = %v, %v", space, got, ok)
		}
		nested := doc["nested"].([]any)[0].(map[string]any)
		if got, ok := nested["synthetic-secret"]; !ok || got != Redacted {
			t.Errorf("nested key whitespace %q: output key/value = %v, %v", space, got, ok)
		}
		if got := doc["env"].(map[string]any)["KEY"]; got != Redacted {
			t.Errorf("env value = %v", got)
		}
	}
}

func TestIdentitiesAppliesAllIntermediateConfigSchemas(t *testing.T) {
	input := []byte(`{"env":{"KEY":"synthetic-secret"},"mcpServers":{"s":{"headers":{"X-Key":"synthetic-header"}}}}`)
	output, err := Identities("claude", []string{"skill.md", "settings.json.old~", "mcp.json.bak", "innocent"}, input)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Env map[string]string `json:"env"`
		MCP map[string]struct {
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(output, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Env["KEY"] != Redacted || doc.MCP["s"].Headers["X-Key"] != Redacted {
		t.Errorf("decoded identities = %+v", doc)
	}
	if strings.Contains(string(output), "synthetic-secret") || strings.Contains(string(output), "synthetic-header") {
		t.Errorf("literal secret retained: %q", output)
	}
}
