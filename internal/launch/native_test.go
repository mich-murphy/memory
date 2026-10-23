package launch

import (
	"strings"
	"testing"
)

func TestNativeMetadataSanitizers(t *testing.T) {
	args := SanitizeNativeArgs([]string{"/bin/claude", "--model", "sonnet-4", "--prompt", "private prompt", "positional", "--system-prompt=secret", "--token", "sk-secret"})
	text := strings.Join(args, " ")
	if !strings.Contains(text, "sonnet-4") || strings.Contains(text, "private") || strings.Contains(text, "secret") || strings.Contains(text, "positional") {
		t.Fatal(args)
	}
	env := SanitizeNativeEnvironment([]string{"ANTHROPIC_MODEL=sonnet-4", "CLAUDE_CODE_PROMPT=private", "PI_API_KEY=secret", "UNKNOWN=value"})
	if len(env) != 1 || env["ANTHROPIC_MODEL"] != "sonnet-4" {
		t.Fatal(env)
	}
}
