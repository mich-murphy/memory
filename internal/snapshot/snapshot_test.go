package snapshot

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/testenv"
)

func build(t *testing.T, e *testenv.Env, cwd string) *Set {
	t.Helper()
	snap, err := Build(adapters.NewClaude(e.Home), e.Claude, cwd, "")
	if err != nil {
		t.Fatal(err)
	}
	return snap.Harness
}

func TestSnapshotDoesNotWalkHomeOutsideARepo(t *testing.T) {
	e := testenv.New(t)
	testenv.Write(t, filepath.Join(e.Home, "deep", "x", "CLAUDE.md"), "should not be found")
	for _, f := range build(t, e, e.Home).Files {
		if strings.Contains(f.Path, "deep") {
			t.Errorf("walked into %s", f.Path)
		}
	}
}

func TestSnapshotSkipsAConfigFileThatCannotBeCheckedForSecrets(t *testing.T) {
	e := testenv.New(t)
	testenv.Write(t, filepath.Join(e.Project, ".mcp.json"), `{"mcpServers": {"db": {"env": {"PW": "hunter2"`)
	set := build(t, e, e.Project)
	for _, f := range set.Files {
		if f.Path == ".mcp.json" {
			t.Errorf("kept %+v", f)
		}
	}
	want := SkippedFile{Scope: "project", Path: ".mcp.json", Reason: "unparseable", Size: 46}
	if !slices.Contains(set.Skipped, want) {
		t.Errorf("skipped = %v, want %v", set.Skipped, want)
	}
}
