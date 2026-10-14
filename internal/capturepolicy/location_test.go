package capturepolicy

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfiguredLocationCaseAliases(t *testing.T) {
	root := t.TempDir()
	configDir, stateDir := filepath.Join(root, "private-config"), filepath.Join(root, "private-state")
	for _, dir := range []string{configDir, stateDir} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	config := filepath.Join(configDir, "private-settings.data")
	if err := os.WriteFile(config, []byte("private fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	aliasDir := strings.ToUpper(configDir)
	original, err := os.Stat(configDir)
	if err != nil {
		t.Fatal(err)
	}
	alias, err := os.Stat(aliasDir)
	if errors.Is(err, fs.ErrNotExist) {
		t.Skip("filesystem has no case aliases")
	}
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(original, alias) {
		t.Skip("case variants are distinct directories")
	}
	policy, err := New(config, stateDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(aliasDir, filepath.Base(config)),
		filepath.Join(aliasDir, filepath.Base(config)+".bak"),
		filepath.Join(strings.ToUpper(stateDir), "pending.json"),
		filepath.Join(strings.ToUpper(stateDir), ".claude.json"),
	} {
		if !policy.Excludes([]string{path}) || !policy.ExcludesClaudeConfig([]string{path}) {
			t.Errorf("configured location not excluded: %s", path)
		}
	}

	outside := filepath.Join(root, "outside.data")
	if err := os.WriteFile(outside, []byte("outside fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(stateDir, "bridge")); err != nil {
		t.Fatal(err)
	}
	resolution, err := Resolve(filepath.Join(strings.ToUpper(stateDir), "bridge"))
	if err != nil {
		t.Fatal(err)
	}
	if !policy.Excludes(resolution.Identities) {
		t.Fatal("intermediate configured state location not excluded")
	}
}

func TestConfiguredLocationsUseDirectoryIdentity(t *testing.T) {
	root := t.TempDir()
	protected, unrelated, alias := filepath.Join(root, "protected"), filepath.Join(root, "unrelated"), filepath.Join(root, "alias")
	for _, dir := range []string{protected, unrelated} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(protected, alias); err != nil {
		t.Fatal(err)
	}
	policy, err := New(filepath.Join(protected, "private.data"), protected)
	if err != nil {
		t.Fatal(err)
	}
	if !policy.Excludes([]string{filepath.Join(alias, "private.data.bak")}) {
		t.Fatal("same directory identity not excluded")
	}
	if !policy.Excludes([]string{filepath.Join(alias, "pending.json")}) {
		t.Fatal("same state directory identity not excluded")
	}
	if policy.Excludes([]string{filepath.Join(unrelated, "private.data")}) {
		t.Fatal("unrelated directory excluded")
	}
}

func TestCaseSensitiveConfiguredLocationsRemainDistinct(t *testing.T) {
	root := t.TempDir()
	protected, distinct := filepath.Join(root, "protected"), filepath.Join(root, "PROTECTED")
	if err := os.Mkdir(protected, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(distinct, 0o700); errors.Is(err, fs.ErrExist) {
		t.Skip("filesystem has case aliases")
	} else if err != nil {
		t.Fatal(err)
	}
	policy, err := New(filepath.Join(protected, "private.data"), protected)
	if err != nil {
		t.Fatal(err)
	}
	if policy.Excludes([]string{filepath.Join(distinct, "private.data")}) {
		t.Fatal("distinct case-sensitive directory excluded")
	}
}
