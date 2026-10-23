package snapshot

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/testenv"
)

// The embedded legacy adapter must never be consulted on an already canceled build.
type legacyContextAdapter struct {
	adapters.Adapter
	t      *testing.T
	cancel context.CancelFunc
}

func (a legacyContextAdapter) ExtraSnapshotEntries(root, projectRoot string) adapters.Sources {
	if a.cancel == nil {
		a.t.Fatal("canceled build invoked legacy harness extraction")
	}
	out := a.Adapter.ExtraSnapshotEntries(root, projectRoot)
	a.cancel()
	return out
}

func (a legacyContextAdapter) MemoryDirs(string, string, string) []adapters.ScopedDir {
	a.t.Fatal("canceled build invoked legacy memory callback")
	return nil
}

func TestBuildContextLegacyCancellation(t *testing.T) {
	e := testenv.New(t)
	for _, canceledBefore := range []bool{true, false} {
		t.Run(map[bool]string{true: "before-harness", false: "after-harness"}[canceledBefore], func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			a := legacyContextAdapter{Adapter: adapters.NewClaude(e.Home), t: t}
			if canceledBefore {
				cancel()
			} else {
				a.cancel = cancel
			}
			snap, err := BuildContext(ctx, a, e.Claude, e.Project, "")
			if !errors.Is(err, context.Canceled) || snap != nil {
				t.Fatalf("canceled build returned snapshot=%v error=%v", snap != nil, err)
			}
		})
	}
}

func TestBuildContextPreservesLegacySelection(t *testing.T) {
	e := testenv.New(t)
	testenv.Write(t, filepath.Join(e.Project, ".gitignore"), "ignored/\n")
	testenv.Write(t, filepath.Join(e.Project, "ignored", "AGENTS.md"), "not selected\n")
	testenv.Write(t, filepath.Join(e.Project, "nested", "AGENTS.md"), "selected\n")
	plugin := filepath.Join(e.Claude, "plugins", "cache", "test", "tool", "1")
	testenv.Write(t, filepath.Join(plugin, "commands", "tool.md"), "plugin rules\n")
	testenv.Write(t, filepath.Join(e.Claude, "plugins", "installed_plugins.json"),
		`{"version":2,"plugins":{"tool@test":[{"installPath":"`+plugin+`"}]}}`)
	a := adapters.NewClaude(e.Home)
	legacy, err := Build(a, e.Claude, e.Project, "")
	if err != nil {
		t.Fatal(err)
	}
	active, err := BuildContext(t.Context(), a, e.Claude, e.Project, "")
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(legacy, active); diff != "" {
		t.Errorf("legacy/context snapshot mismatch (-legacy +context):\n%s", diff)
	}
	selected := map[string]bool{}
	for _, f := range active.Harness.Files {
		selected[f.Scope+":"+f.Path] = true
	}
	if !selected["project:nested/AGENTS.md"] || selected["project:ignored/AGENTS.md"] || !selected["plugin:tool@test/commands/tool.md"] {
		t.Fatal("legacy repository/plugin selectors were not preserved")
	}
}
