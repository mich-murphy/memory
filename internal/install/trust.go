package install

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/mich-murphy/memory/internal/jsonx"
)

// codexHookHash mirrors Unix command-hook normalization and version_for_toml
// at openai/codex@806d973. See docs/install.md for primary sources and limits.
// Unsupported identities fail closed rather than treating any stored hash as trust.
func codexHookHash(r HandlerRef) string {
	if !slices.ContainsFunc(CodexEvents, func(e HookEvent) bool { return e.Name == r.Event }) {
		return ""
	}
	data, err := jsonx.Compact(r.Handler)
	if err != nil {
		return ""
	}
	var h struct {
		Type                   string  `json:"type"`
		Command                string  `json:"command"`
		CommandWindows         *string `json:"commandWindows"`
		Timeout                *uint64 `json:"timeout"`
		Async                  bool    `json:"async"`
		StatusMessage          *string `json:"statusMessage"`
		AdditionalContextLimit *uint64 `json:"additionalContextLimit"`
	}
	if jsonx.Decode(data, &h) != nil || h.Type != "command" || strings.TrimSpace(h.Command) == "" {
		return ""
	}
	timeout := uint64(600)
	capped := r.Event == "SessionEnd" || r.Event == "Interrupt"
	if capped {
		timeout = 1
	}
	if h.Timeout != nil {
		timeout = *h.Timeout
	}
	timeout = max(1, timeout)
	if capped {
		timeout = min(timeout, 3)
	}
	handler := map[string]any{"type": "command", "command": h.Command, "timeout": timeout, "async": h.Async}
	if h.StatusMessage != nil {
		handler["statusMessage"] = *h.StatusMessage
	}
	if r.Event == "SessionStart" && h.AdditionalContextLimit != nil && *h.AdditionalContextLimit != 2500 {
		handler["additionalContextLimit"] = *h.AdditionalContextLimit
	}
	identity := map[string]any{"event_name": codexEventKey(r.Event), "hooks": []any{handler}}
	matcher, _ := r.Group.Get("matcher")
	if matcher != nil {
		value, ok := matcher.(string)
		if !ok {
			return ""
		}
		if r.Event != "Stop" && r.Event != "Interrupt" {
			identity["matcher"] = value
		}
	}
	return codexFingerprint(identity)
}

// codexFingerprint hashes recursively sorted compact JSON, as version_for_toml
// does. Go's encoding differs only for the two Unicode separators rejected here.
func codexFingerprint(identity map[string]any) string {
	handlers, _ := identity["hooks"].([]any)
	handler, _ := handlers[0].(map[string]any)
	for _, value := range handler {
		if text, ok := value.(string); ok && strings.ContainsAny(text, "\u2028\u2029") {
			return ""
		}
	}
	if text, ok := identity["matcher"].(string); ok && strings.ContainsAny(text, "\u2028\u2029") {
		return ""
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if enc.Encode(identity) != nil {
		return ""
	}
	sha := sha256.Sum256(bytes.TrimSuffix(out.Bytes(), []byte("\n")))
	return fmt.Sprintf("sha256:%x", sha)
}
