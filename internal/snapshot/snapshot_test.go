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
	projectRoot, isRepo := FindProjectRoot(e.Home)
	c := collect(adapters.NewClaude(e.Home), filepath.Join(e.Home, ".claude"), projectRoot, isRepo)
	for _, entry := range c.entries {
		if strings.Contains(entry.Path, "deep") {
			t.Errorf("walked into %s", entry.Path)
		}
	}
}

func TestSnapshotSkipsAConfigFileThatCannotBeCheckedForSecrets(t *testing.T) {
	e := testenv.New(t)
	testenv.Write(t, filepath.Join(e.Project, ".mcp.json"), `{"mcpServers": {"db": {"env": {"PW": "hunter2"`)
	projectRoot, isRepo := FindProjectRoot(e.Project)
	c := collect(adapters.NewClaude(e.Home), filepath.Join(e.Home, ".claude"), projectRoot, isRepo)
	for _, entry := range c.entries {
		if entry.Path == ".mcp.json" {
			t.Errorf("kept %s: %s", entry.Path, entry.Body)
		}
	}
	want := Skipped{Scope: "project", Path: ".mcp.json", Reason: "unparseable", Size: 46}
	if !slices.Contains(c.skipped, want) {
		t.Errorf("skipped = %v, want %v", c.skipped, want)
	}
}
