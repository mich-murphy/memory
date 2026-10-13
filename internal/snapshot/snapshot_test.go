package snapshot

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/testenv"
)

func TestSnapshotDoesNotWalkHomeOutsideARepo(t *testing.T) {
	e := testenv.New(t)
	testenv.Write(t, filepath.Join(e.Home, "deep", "x", "CLAUDE.md"), "should not be found")
	projectRoot, isRepo := findProjectRoot(e.Home)
	c := collect(adapters.NewClaude(e.Home), filepath.Join(e.Home, ".claude"), projectRoot, isRepo)
	for _, entry := range c.entries {
		if strings.Contains(entry.path, "deep") {
			t.Errorf("walked into %s", entry.path)
		}
	}
}

func TestSnapshotSkipsAConfigFileThatCannotBeCheckedForSecrets(t *testing.T) {
	e := testenv.New(t)
	testenv.Write(t, filepath.Join(e.Project, ".mcp.json"), `{"mcpServers": {"db": {"env": {"PW": "hunter2"`)
	projectRoot, isRepo := findProjectRoot(e.Project)
	c := collect(adapters.NewClaude(e.Home), filepath.Join(e.Home, ".claude"), projectRoot, isRepo)
	for _, entry := range c.entries {
		if entry.path == ".mcp.json" {
			t.Errorf("kept %s: %s", entry.path, entry.body)
		}
	}
	want := SkippedFile{Scope: "project", Path: ".mcp.json", Reason: "unparseable", Size: 46}
	if !slices.Contains(c.skipped, want) {
		t.Errorf("skipped = %v, want %v", c.skipped, want)
	}
}
