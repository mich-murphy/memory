package snapshot

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/capturepolicy"
)

// Keep this filesystem-policy regression independent of host managed settings.
type locationAliasClaude struct{ *adapters.Claude }

func (locationAliasClaude) ManagedSettingsFiles() []string { return nil }

func TestSnapshotExcludesFilesystemCaseAliases(t *testing.T) {
	for _, scenario := range []string{"custom-config", "state-file", "state-external-leaf"} {
		t.Run(scenario, func(t *testing.T) {
			dir, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			home := filepath.Join(dir, "home")
			configPath, stateDir := filepath.Join(home, ".config", "private-settings.data"), filepath.Join(home, "collector-state")
			root := filepath.Join(home, ".claude")
			skill := filepath.Join(root, "skills", "casealias", "SKILL.md")
			for _, path := range []string{filepath.Dir(configPath), stateDir, filepath.Dir(skill)} {
				if err := os.MkdirAll(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			marker := []byte("private fixture " + scenario)
			original := configPath
			if scenario != "custom-config" {
				original = filepath.Join(stateDir, "launch-record.data")
			}
			if scenario == "state-external-leaf" {
				outside := filepath.Join(dir, "outside.data")
				if err := os.WriteFile(outside, marker, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, original); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(original, marker, 0o600); err != nil {
				t.Fatal(err)
			}
			variant := filepath.Join(strings.ToUpper(filepath.Dir(original)), filepath.Base(original))
			first, err := os.Stat(original)
			if err != nil {
				t.Fatal(err)
			}
			second, err := os.Stat(variant)
			if errors.Is(err, fs.ErrNotExist) {
				t.Skip("filesystem has no case aliases")
			}
			if err != nil {
				t.Fatal(err)
			}
			if !os.SameFile(first, second) {
				t.Skip("case variants are distinct objects")
			}
			if err := os.Symlink(variant, skill); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HOME", home)
			t.Setenv("AGENT_SESSIONS_CONFIG", configPath)
			t.Setenv("AGENT_SESSIONS_STATE_DIR", stateDir)
			t.Setenv("CLAUDE_CONFIG_DIR", "")
			t.Setenv("XDG_CONFIG_HOME", "")
			policy, err := capturepolicy.New(configPath, stateDir)
			if err != nil {
				t.Fatal(err)
			}
			resolution, err := capturepolicy.Resolve(skill)
			if err != nil {
				t.Fatal(err)
			}
			if !policy.Excludes(resolution.Identities) {
				t.Fatal("configured location was not excluded")
			}
			result, err := Build(locationAliasClaude{adapters.NewClaude(home)}, root, "", "")
			if err != nil {
				t.Fatal(err)
			}
			for _, file := range result.Harness.Files {
				if file.Path == "skills/casealias/SKILL.md" {
					t.Error("private location included in snapshot files")
				}
			}
			for _, body := range result.Blobs {
				if bytes.Contains(body, marker) {
					t.Error("private location included in snapshot blobs")
				}
			}
		})
	}
}
