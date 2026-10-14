package install

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mich-murphy/memory/internal/jsonx"
	"github.com/mich-murphy/memory/internal/testenv"
)

func TestCodexTrustVerifiesNormalizedIdentity(t *testing.T) {
	e := testenv.New(t)
	if err := Codex(opts(e)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(filepath.Join(e.Home, ".codex"), "hooks.json")
	doc, err := loadJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	refs := OurHandlers(doc)
	for _, r := range refs {
		// Independently specified canonical identities from the pinned Rust schema:
		// None fields disappear through TomlValue; objects sort recursively.
		async, timeout := "true", 30
		if r.Event == "SessionEnd" {
			async, timeout = "false", 3
		}
		if r.Event == "Interrupt" {
			timeout = 3
		}
		canonical := fmt.Sprintf(`{"event_name":%q,"hooks":[{"async":%s,"command":"/opt/agent-sessions/bin/agent-sessions hook codex","timeout":%d,"type":"command"}]}`, codexEventKey(r.Event), async, timeout)
		sha := sha256.Sum256([]byte(canonical))
		want := fmt.Sprintf("sha256:%x", sha)
		if got := codexHookHash(r); got != want {
			t.Errorf("hash(%s) = %s, want %s", r.Event, got, want)
		}
	}
	r := refs[0]
	key := fmt.Sprintf("%s:%s:%d:%d", path, codexEventKey(r.Event), r.GroupIndex, r.HandlerIndex)
	configPath := filepath.Join(filepath.Join(e.Home, ".codex"), "config.toml")
	for _, hash := range []string{"sha256:x", "sha256:" + strings.Repeat("0", 64), codexHookHash(r)} {
		testenv.Write(t, configPath, fmt.Sprintf("[hooks.state.%q]\ntrusted_hash = %q\n", key, hash))
		want := 0
		if hash == codexHookHash(r) {
			want = 1
		}
		if _, got := CodexTrustState(filepath.Join(e.Home, ".codex")); got != want {
			t.Errorf("trust(%q) = %d, want %d", hash, got, want)
		}
	}
	testenv.Write(t, configPath, fmt.Sprintf("[hooks.state.%q]\ntrusted_hash = %q\nenabled = false\n", key, codexHookHash(r)))
	if _, got := CodexTrustState(filepath.Join(e.Home, ".codex")); got != 0 {
		t.Errorf("disabled trust = %d", got)
	}
	testenv.Write(t, configPath, fmt.Sprintf("[hooks.state.%q]\ntrusted_hash = %q\n", key, codexHookHash(r)))
	r.Handler.Set("command", "/new/agent-sessions hook codex")
	body, err := jsonx.Indent(doc, 2)
	if err != nil {
		t.Fatal(err)
	}
	testenv.Write(t, path, string(body))
	if _, got := CodexTrustState(filepath.Join(e.Home, ".codex")); got != 0 {
		t.Errorf("changed command trust = %d", got)
	}
}

func TestPiDisabledCaptureStillInvokesHook(t *testing.T) {
	body, err := PiExtension(testCmd)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "process.env.AGENT_SESSIONS_DISABLE") {
		t.Fatal("extension suppresses hook needed to persist exclusion")
	}
}

func TestCodexHashNormalization(t *testing.T) {
	for _, event := range []string{"SessionStart", "Stop", "Interrupt", "SessionEnd"} {
		h := jsonx.NewObject()
		h.Set("type", "command")
		h.Set("command", "agent-sessions hook codex")
		group := jsonx.NewObject()
		r := HandlerRef{Event: event, Handler: h, Group: group}
		original := codexHookHash(r)
		group.Set("matcher", "startup")
		if got := codexHookHash(r); (got == original) != (event == "Stop" || event == "Interrupt") {
			t.Errorf("matcher normalization for %s", event)
		}
		group.Delete("matcher")
		h.Set("timeout", 0)
		low := codexHookHash(r)
		h.Set("timeout", 1)
		if got := codexHookHash(r); got != low {
			t.Errorf("minimum timeout for %s", event)
		}
		h.Set("timeout", 999)
		high := codexHookHash(r)
		h.Set("timeout", 3)
		if got := codexHookHash(r); (got == high) != (event == "Interrupt" || event == "SessionEnd") {
			t.Errorf("timeout cap for %s", event)
		}
		h.Set("additionalContextLimit", 2500)
		defaultLimit := codexHookHash(r)
		h.Set("additionalContextLimit", 12)
		if got := codexHookHash(r); (got == defaultLimit) != (event != "SessionStart") {
			t.Errorf("context limit for %s", event)
		}
		h.Set("statusMessage", "hello <world>")
		if got := codexHookHash(r); got == "" || got == defaultLimit {
			t.Errorf("status message hash = %s", got)
		}
		h.Set("command", "agent-sessions\u2028 hook codex")
		if got := codexHookHash(r); got != "" {
			t.Errorf("unsupported separator hash = %s", got)
		}
	}
}
