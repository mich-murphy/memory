package adapters

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func writePlugins(t *testing.T, root, body string) {
	t.Helper()
	dir := filepath.Join(root, "plugins")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "installed_plugins.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestClaudePluginsReadsBothFileVersions(t *testing.T) {
	root := t.TempDir()
	lsp, docs, v1 := filepath.Join(root, "cache", "lsp"), filepath.Join(root, "cache", "docs"), filepath.Join(root, "cache", "v1")
	for _, d := range []string{lsp, docs, v1} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	uninstalled := filepath.Join(root, "cache", "uninstalled")
	writePlugins(t, root, `{"version": 2, "plugins": {
		"lsp@official": [{"scope": "user", "installPath": "`+lsp+`"}, {"scope": "project", "installPath": "`+uninstalled+`"}],
		"docs@official": [{"scope": "user", "installPath": "`+docs+`"}, "not an object", {"scope": "user"}],
		"v1-format": {"installPath": "`+v1+`"}
	}}`)
	want := []Plugin{{Name: "docs@official", Dir: docs}, {Name: "lsp@official", Dir: lsp}, {Name: "v1-format", Dir: v1}}
	if diff := cmp.Diff(want, NewClaude(t.TempDir()).Plugins(root)); diff != "" {
		t.Errorf("plugins (-want +got):\n%s", diff)
	}
}

func TestClaudePluginsWithoutAUsableFile(t *testing.T) {
	c := NewClaude(t.TempDir())
	root := t.TempDir()
	if got := c.Plugins(root); got != nil {
		t.Errorf("no file: %v", got)
	}
	for _, body := range []string{`{"plugins": `, `[1, 2]`, `{"plugins": []}`} {
		writePlugins(t, root, body)
		if got := c.Plugins(root); got != nil {
			t.Errorf("%s: %v", body, got)
		}
	}
}
