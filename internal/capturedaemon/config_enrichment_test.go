package capturedaemon

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestEnrichmentInventoryIsExplicitAndDoesNotBroadenDiscovery(t *testing.T) {
	c := testConfig(t)
	original := c.SourceRoots()
	c.Enrichment = true
	c.EnrichmentRoots = []string{filepath.Join(filepath.Dir(c.Home), "managed")}
	path := filepath.Join(c.Home, "runtime.json")
	if err := Save(path, c); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Enrichment || !slices.Equal(loaded.EnrichmentRoots, c.EnrichmentRoots) || !slices.Equal(loaded.SourceRoots(), original) {
		t.Fatal("enrichment altered source inventory")
	}
	if loaded.AllowsSource("claude", filepath.Join(c.EnrichmentRoots[0], "CLAUDE.md")) {
		t.Fatal("snapshot roots became ordinary capture authority")
	}
}

func TestEnrichmentInventoryBoundsAndPathSafety(t *testing.T) {
	c := testConfig(t)
	for _, roots := range [][]string{{"relative"}, {string([]byte{'/', 255})}, {"/"}, {"/" + strings.Repeat("a", 4096)}, {c.Spool}} {
		trial := c
		trial.EnrichmentRoots = roots
		if err := trial.Validate(); err == nil {
			t.Fatal("invalid enrichment inventory accepted")
		}
	}
	target := filepath.Join(filepath.Dir(c.Home), "target")
	alias := filepath.Join(filepath.Dir(c.Home), "alias")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, alias); err != nil {
		t.Fatal(err)
	}
	c.EnrichmentRoots = []string{alias}
	if err := c.Validate(); err == nil {
		t.Fatal("symlinked enrichment inventory accepted")
	}
	c.EnrichmentRoots = make([]string, 129)
	if err := c.Validate(); err == nil {
		t.Fatal("combined root bound ignored")
	}
}
