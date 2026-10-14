package capturepolicy

import (
	"path/filepath"
	"testing"
)

func TestClaudeConfigExceptionKeepsEveryOtherProtection(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "collector", ".claude.json")
	state := filepath.Join(dir, "state")
	policy, err := New(config, state)
	if err != nil {
		t.Fatal(err)
	}
	primary := filepath.Join(dir, ".claude.json")
	cases := []struct {
		name       string
		identities []string
		excluded   bool
	}{
		{"primary", []string{primary}, false},
		{"aliasTarget", []string{primary, filepath.Join(dir, "ordinary")}, false},
		{"credentialHop", []string{primary, filepath.Join(dir, "auth.json"), filepath.Join(dir, "ordinary")}, true},
		{"backup", []string{primary, primary + ".bak.old"}, true},
		{"collectorConfig", []string{config}, true},
		{"collectorConfigBackup", []string{config + ".bak"}, true},
		{"collectorState", []string{primary, filepath.Join(state, ".claude.json")}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := policy.ExcludesClaudeConfig(tc.identities); got != tc.excluded {
				t.Errorf("ExcludesClaudeConfig = %t, want %t", got, tc.excluded)
			}
		})
	}
	if !policy.Excludes([]string{primary}) {
		t.Error("ordinary raw capture accepted .claude.json without explicit export handling")
	}
}
