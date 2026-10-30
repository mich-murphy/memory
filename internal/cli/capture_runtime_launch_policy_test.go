package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnresolvedNativeBarrierPersistsAndScopesAgent(t *testing.T) {
	for _, body := range []string{`{}`, `null`, `{invalid`, `{"session_path":"/outside/unproven.jsonl"}`} {
		t.Run(body, func(t *testing.T) {
			c, _ := runtimeFixture(t)
			t.Setenv("AGENT_SESSIONS_DISABLE", "1")
			code, err := runNativeCaptureHook(t.Context(), c, runtimeFlags{agent: "claude"}, strings.NewReader(body))
			if code == exitOK || err == nil {
				t.Fatal("unresolved disabled request acknowledged")
			}
			if err := runtimeUnresolvedNativePolicy(c, "claude"); err == nil {
				t.Fatal("barrier absent")
			}
			if err := runtimeUnresolvedNativePolicy(c, "pi"); err != nil {
				t.Fatal("other agent paused", err)
			}
			if _, err := os.Stat(c.Spool); !os.IsNotExist(err) {
				t.Fatal("disabled hook touched spool", err)
			}
			path, err := unresolvedNativePath(c, "claude")
			if err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("AGENT_SESSIONS_DISABLE", "")
			// Enabling another hook must never repair unknown prior ownership.
			_, _ = runNativeCaptureHook(t.Context(), c, runtimeFlags{agent: "pi"}, strings.NewReader(`{}`))
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("barrier changed", err)
			}
			if err := recordUnresolvedNativePolicy(t.Context(), c, "claude"); err != nil {
				t.Fatal(err)
			}
			marker, err := readUnresolvedNativeMarker(path, "claude")
			var old unresolvedNativeMarker
			if err := json.Unmarshal(before, &old); err != nil {
				t.Fatal(err)
			}
			if err != nil || marker.Count != 2 || marker.Nonce == old.Nonce {
				t.Fatal(marker, err)
			}
		})
	}
}

func TestKnownDisabledNativeSessionDoesNotPauseHealthyB(t *testing.T) {
	c, _ := runtimeFixture(t)
	path := filepath.Join(c.Home, ".claude", "projects", "p", "a.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_SESSIONS_DISABLE", "1")
	if code, err := runNativeCaptureHook(t.Context(), c, runtimeFlags{agent: "claude", path: path}, strings.NewReader("")); err != nil || code != exitOK {
		t.Fatal(code, err)
	}
	if err := runtimeUnresolvedNativePolicy(c, "claude"); err != nil {
		t.Fatal("known A paused B", err)
	}
	if _, err := os.Stat(c.Spool); !os.IsNotExist(err) {
		t.Fatal("disabled hook touched spool", err)
	}
}

func TestUnresolvedNativeBarrierFailsClosed(t *testing.T) {
	c, _ := runtimeFixture(t)
	path, err := unresolvedNativePath(c, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`null`, `{}`, `{"version":"unknown"}`, strings.Repeat("x", 4097)} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := runtimeUnresolvedNativePolicy(c, "codex"); err == nil {
			t.Fatal("malformed accepted")
		}
		if err := recordUnresolvedNativePolicy(t.Context(), c, "codex"); err == nil {
			t.Fatal("malformed replaced")
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := recordUnresolvedNativePolicy(ctx, c, "codex"); err == nil {
		t.Fatal("canceled write")
	}
	if err := recordUnresolvedNativePolicy(t.Context(), c, "unknown"); err == nil {
		t.Fatal("unknown agent slot")
	}
	if err := os.Symlink(c.PolicyDir, path); err != nil {
		t.Fatal(err)
	}
	if err := runtimeUnresolvedNativePolicy(c, "codex"); err == nil {
		t.Fatal("alias accepted")
	}
}
