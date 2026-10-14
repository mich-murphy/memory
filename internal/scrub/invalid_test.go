package scrub

import (
	"strings"
	"testing"
)

func TestInvalidProtectedConfigCannotLeakThroughErrorsOrRootShape(t *testing.T) {
	const secret = "sensitivepassword"
	for _, source := range []string{
		"[mcp_servers.x.env]\nAPI_KEY = " + secret + "\n",
		"[mcp_servers.x.env]\nAPI_KEY = \"" + secret + "\\z\"\n",
	} {
		output, err := File("codex", "config.toml", []byte(source))
		if err == nil || len(output) != 0 {
			t.Fatalf("malformed TOML returned publishable bytes: %q, %v", output, err)
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatal("parser error exposed a credential value")
		}
	}
	for _, source := range []string{
		`[{"env":{"API_KEY":"sensitivepassword"}}]`,
		`"sensitivepassword"`,
		`null`,
	} {
		output, err := File("claude", "settings.json", []byte(source))
		if err == nil || len(output) != 0 {
			t.Fatalf("non-object config bypassed schema redaction: %q, %v", output, err)
		}
	}
}
