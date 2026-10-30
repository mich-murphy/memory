package capturedaemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mich-murphy/memory/internal/capturecoordinates"
)

func TestDeclaredAliasConfigRoundtripAndExactAdmission(t *testing.T) {
	c := testConfig(t)
	logical := filepath.Join(c.Home, ".claude")
	physical := filepath.Join(filepath.Dir(c.Home), "physical")
	if err := os.MkdirAll(physical, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(c.Home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(physical, logical); err != nil {
		t.Fatal(err)
	}
	c.RootAliases = []RootAlias{{Agent: "claude", Root: logical, Path: physical}}
	leaf := filepath.Join(filepath.Dir(c.Home), "exact.jsonl")
	c.LeafAliases = []LeafAlias{{Agent: "claude", LogicalPath: filepath.Join(logical, "projects", "p", "a.jsonl"), Path: leaf}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(c.Home, "runtime.json")
	if err := Save(path, c); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.AllowsSource("claude", leaf) || got.AllowsSource("claude", leaf+"-sibling") || got.AllowsSource("codex", physical) {
		t.Fatal("alias admission broadened")
	}
	inventory, err := got.CoordinateInventory()
	if err != nil {
		t.Fatal(err)
	}
	coordinate, err := inventory.Coordinate("claude", filepath.Join(physical, "projects", "p", "a.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if coordinate.Root != logical || coordinate.LogicalPath != c.LeafAliases[0].LogicalPath {
		t.Fatal("logical namespace changed")
	}
	leaves := inventory.Leaves()
	leaves[0].Path = "/mutated"
	if inventory.Leaves()[0].Path != leaf {
		t.Fatal("mutable leaf declarations")
	}
	if err := SafePath(logical); err == nil {
		t.Fatal("source declaration weakened strict storage path validation")
	}
}

func TestDeclaredAliasesFailClosedWithoutChangingUnmappedRoots(t *testing.T) {
	for _, kind := range []string{"undeclared-resolution", "protected", "outside-logical", "invalid-agent", "combined-budget", "unavailable"} {
		t.Run(kind, func(t *testing.T) {
			c := testConfig(t)
			logical := filepath.Join(c.Home, ".claude")
			target := filepath.Join(filepath.Dir(c.Home), "physical")
			c.RootAliases = []RootAlias{{Agent: "claude", Root: logical, Path: target}}
			switch kind {
			case "undeclared-resolution":
				if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(filepath.Dir(target), "not-declared"), target); err != nil {
					t.Fatal(err)
				}
			case "protected":
				c.RootAliases[0].Path = c.Spool
			case "outside-logical":
				c.LeafAliases = []LeafAlias{{Agent: "claude", LogicalPath: filepath.Join(c.Home, "not-a-root", "a.jsonl"), Path: target + "-leaf"}}
			case "invalid-agent":
				c.RootAliases[0].Agent = "unknown"
			case "combined-budget":
				c.EnrichmentRoots = make([]string, capturecoordinates.MaxRoutes)
			case "unavailable":
				if err := os.Symlink(target, target); err != nil {
					t.Fatal(err)
				}
			}
			if err := c.Validate(); err == nil {
				t.Fatal("unsafe alias config admitted")
			}
		})
	}
	c := testConfig(t)
	if _, err := c.CoordinateInventory(); err != nil {
		t.Fatal(err)
	}
	c.RootAliases = []RootAlias{{Agent: "claude", Root: "relative", Path: "/other"}}
	if c.AllowsSource("claude", filepath.Join(c.Home, ".claude", "a.jsonl")) {
		t.Fatal("invalid mapping silently bypassed")
	}
}
