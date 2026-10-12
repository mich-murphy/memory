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

func TestClaudePluginDirsReadsBothFileVersions(t *testing.T) {
	root := t.TempDir()
	lsp, docs, old := filepath.Join(root, "cache", "lsp"), filepath.Join(root, "cache", "docs"), filepath.Join(root, "cache", "old")
	for _, d := range []string{lsp, docs, old} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	gone := filepath.Join(root, "cache", "uninstalled")
	// Version 2 lists installs per plugin; version 1 had one object per plugin. A plugin whose
	// install dir is gone, or an entry without one, is skipped.
	writePlugins(t, root, `{"version": 2, "plugins": {
		"lsp@official": [{"scope": "user", "installPath": "`+lsp+`"}, {"scope": "project", "installPath": "`+gone+`"}],
		"docs@official": [{"scope": "user", "installPath": "`+docs+`"}, "not an object", {"scope": "user"}],
		"old": {"installPath": "`+old+`"}
	}}`)
	want := []ScopedDir{{Name: "docs@official", Dir: docs}, {Name: "lsp@official", Dir: lsp}, {Name: "old", Dir: old}}
	if diff := cmp.Diff(want, NewClaude(t.TempDir()).PluginDirs(root)); diff != "" {
		t.Errorf("plugin dirs (-want +got):\n%s", diff)
	}
}

func TestClaudePluginDirsWithoutAUsableFile(t *testing.T) {
	c := NewClaude(t.TempDir())
	root := t.TempDir()
	if got := c.PluginDirs(root); got != nil {
		t.Errorf("no file: %v", got)
	}
	for _, body := range []string{`{"plugins": `, `[1, 2]`, `{"plugins": []}`} {
		writePlugins(t, root, body)
		if got := c.PluginDirs(root); got != nil {
			t.Errorf("%s: %v", body, got)
		}
	}
}
