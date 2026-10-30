package launch

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mich-murphy/memory/internal/scrub"
)

var nativeIdentifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@+-]{0,255}$`)

// SanitizeNativeArgs retains metadata, never free-form prompt or positional text.
// It deliberately applies a stricter allowlist after the legacy secret scrubber.
func SanitizeNativeArgs(argv []string) []string {
	argv = scrub.Args(argv[:min(len(argv), 32)])
	out := make([]string, 0, min(len(argv), 32))
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		if i == 0 {
			switch filepath.Base(arg) {
			case "claude", "codex", "pi", "node", "nodejs", "bun":
				out = append(out, filepath.Base(arg))
			default:
				out = append(out, "[redacted]")
			}
			continue
		}
		key, value, eq := strings.Cut(arg, "=")
		switch key {
		case "--resume", "--continue", "--no-session", "--dangerously-skip-permissions", "--full-auto", "--version":
			if !eq {
				out = append(out, key)
			} else {
				out = append(out, "[redacted]")
			}
		case "--model", "-m", "--provider", "--permission-mode", "--session-id", "--session", "--config", "--config-dir", "--cwd":
			if takeNativeValue(eq, i, argv) {
				i++
				value = argv[i]
			}
			if safeNativeValue(value) {
				out = append(out, key, value)
			} else {
				out = append(out, key, "[redacted]")
			}
		default:
			out = append(out, "[redacted]")
		}
	}
	return out[:min(len(out), 32)]
}

// SanitizeNativeEnvironment accepts exact metadata names, not agent prefixes.
func SanitizeNativeEnvironment(environ []string) map[string]string {
	out := map[string]string{}
	for _, entry := range environ {
		if len(out) == 8 {
			break
		}
		key, value, _ := strings.Cut(entry, "=")
		switch key {
		case "ANTHROPIC_MODEL", "ANTHROPIC_SMALL_FAST_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "CLAUDE_CONFIG_DIR", "MAX_THINKING_TOKENS", "MAX_MCP_OUTPUT_TOKENS", "DISABLE_PROMPT_CACHING":
			if !scrub.IsSecretName(key) && nativeIdentifier.MatchString(value) && !scrub.IsSecretName(value) {
				out[key] = value
			}
		}
	}
	return out
}

func safeNativeValue(value string) bool {
	return nativeIdentifier.MatchString(value) && !scrub.IsSecretName(value)
}

func takeNativeValue(eq bool, i int, argv []string) bool { return !eq && i+1 < len(argv) }
