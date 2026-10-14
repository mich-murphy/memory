package doctor

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mich-murphy/memory/internal/install"
	"github.com/mich-murphy/memory/internal/jsonx"
	"github.com/mich-murphy/memory/internal/testenv"
)

func TestDoctorRequiresDistinctUsableHookEvents(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		for _, defect := range []string{"correct", "duplicateStop", "wrongAgent", "foreignCommand", "wrongType", "disabled", "restrictedMatcher", "notExecutable", "disabledGlobally"} {
			t.Run(agent+"/"+defect, func(t *testing.T) {
				e := testenv.New(t)
				bin := filepath.Join(t.TempDir(), "agent-sessions")
				// Executing any fixture command would fail the test.
				marker := filepath.Join(t.TempDir(), "was-executed")
				testenv.Write(t, bin, "#!/bin/sh\ntouch '"+marker+"'\nexit 1\n")
				if err := os.Chmod(bin, 0o700); err != nil {
					t.Fatal(err)
				}
				o := &install.Options{Home: e.Home, Command: []string{bin}}
				installer, events := install.Claude, install.ClaudeEvents
				root, filename := e.Claude, "settings.json"
				if agent == "codex" {
					installer, events = install.Codex, install.CodexEvents
					root, filename = filepath.Join(e.Home, ".codex"), "hooks.json"
				}
				if err := installer(o); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(root, filename)
				settings := readJSON(path)
				hooksValue, _ := settings.Get("hooks")
				hooks := hooksValue.(*jsonx.Object)
				stop, _ := hooks.Get("Stop")
				group := stop.([]any)[0].(*jsonx.Object)
				handlers, _ := group.Get("hooks")
				handler := handlers.([]any)[0].(*jsonx.Object)
				switch defect {
				case "correct":
				case "duplicateStop":
					for _, event := range events {
						if event.Name != "Stop" {
							hooks.Delete(event.Name)
						}
					}
					var duplicates []any
					for range events {
						duplicates = append(duplicates, group)
					}
					hooks.Set("Stop", duplicates)
				case "wrongAgent":
					handler.Set("command", bin+" hook pi")
				case "foreignCommand":
					handler.Set("command", "sh -c '"+bin+" hook "+agent+"'")
				case "wrongType":
					handler.Set("type", "prompt")
				case "disabled":
					handler.Set("disabled", true)
				case "restrictedMatcher":
					group.Set("matcher", "startup")
				case "notExecutable":
					if err := os.Chmod(bin, 0o600); err != nil {
						t.Fatal(err)
					}
				case "disabledGlobally":
					if agent == "claude" {
						settings.Set("disableAllHooks", true)
					} else {
						testenv.Write(t, filepath.Join(root, "config.toml"), "[features]\nhooks = false\n")
					}
				}
				data, err := jsonx.Indent(settings, 2)
				if err != nil {
					t.Fatal(err)
				}
				testenv.Write(t, path, string(data))
				var out bytes.Buffer
				r := &reporter{w: &out}
				switch {
				case agent == "claude":
					checkClaude(e.Home, r)
				case defect == "disabledGlobally":
					checkCodex(e.Home, r)
				default:
					// Trust is independent of event coverage and is tested in install.
					checkHookCoverage(agent, install.OurHandlers(settings), events, path, r)
				}
				if r.failed != (defect != "correct") {
					t.Errorf("failed = %v, want %v:\n%s", r.failed, defect != "correct", &out)
				}
				if defect == "duplicateStop" && !strings.Contains(out.String(), "missing usable hooks for SessionStart") {
					t.Errorf("missing event report:\n%s", &out)
				}
				if _, err := os.Stat(marker); !os.IsNotExist(err) {
					t.Errorf("doctor executed a hook: %v", err)
				}
			})
		}
	}
}

func TestCodexDisabledFeatures(t *testing.T) {
	for _, setting := range []string{"[features]\nhooks = false", "[features]\ncodex_hooks = false", "[features]\nhooks = true", "malformed toml"} {
		path := filepath.Join(t.TempDir(), "config.toml")
		testenv.Write(t, path, setting)
		if got, want := codexHooksDisabled(path), strings.Contains(setting, "false"); got != want {
			t.Errorf("codexHooksDisabled(%q) = %v, want %v", setting, got, want)
		}
	}
}
