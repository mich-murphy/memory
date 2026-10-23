package cli

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mich-murphy/memory/internal/capturedaemon"
	"github.com/mich-murphy/memory/internal/capturesource"
	"github.com/mich-murphy/memory/internal/publication"
	"github.com/mich-murphy/memory/internal/sessioncapture"
	"github.com/mich-murphy/memory/internal/snapshot"
)

func runtimeEnrichmentFixture(t *testing.T) (capturedaemon.Config, sessioncapture.EnrichmentSpec) {
	t.Helper()
	c, config := runtimeFixture(t)
	c.Enrichment = true
	if err := capturedaemon.Save(config, c); err != nil {
		t.Fatal(err)
	}
	c, err := capturedaemon.Load(config)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(c.Home, ".claude")
	path := filepath.Join(root, "projects", "slug", "12345678-1234-1234-1234-123456789abc.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("synthetic\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := capturesource.Classify(t.Context(), capturesource.Options{ArchiveID: c.ArchiveID, MachineID: c.MachineID, Home: c.Home, ConfigPath: config, StateDir: c.Spool, Policy: func(ctx context.Context, scope capturesource.Scope) error {
		return runtimeDiscoveryPolicy(c)(ctx, sessioncapture.Request{Identity: publication.Identity{ArchiveID: c.ArchiveID, MachineID: c.MachineID, Agent: scope.Root.Agent}, Path: scope.Path})
	}}, "claude", path)
	if err != nil {
		t.Fatal(err)
	}
	s := sessioncapture.EnrichmentSpec{Kind: "snapshot", Identity: r.Identity, Root: root, SelectionVersion: "snapshot/v1", WorkingDirectory: filepath.Join(c.Home, "working"), Source: sessioncapture.SourceCoordinate{SourceID: r.SourceID}}
	if err := os.MkdirAll(s.WorkingDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, association := range r.Associations {
		if value, ok := strings.CutPrefix(association, "adapter-session:"); ok {
			s.AdapterSessionKey = value
		}
	}
	return c, s
}

func TestRuntimeEnrichmentUsesActualPolicyAndSelfContainedBundle(t *testing.T) {
	c, spec := runtimeEnrichmentFixture(t)
	for name, body := range map[string]string{".claude/CLAUDE.md": "synthetic harness", ".claude/settings.json": `{"env":{"API_TOKEN":"synthetic-private-token"},"theme":"dark"}`} {
		path := filepath.Join(c.Home, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	artifact, err := runtimeEnrichment(c)(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := artifact.Body.Close(); err != nil {
			t.Error(err)
		}
	}()
	if artifact.Complete || !slices.Contains(artifact.Missing, "native-launch-context-missing") || !slices.Contains(artifact.Missing, "project-context-missing") {
		t.Fatal("missing native context incorrectly reported complete")
	}
	snap, err := snapshot.ReadBundle(t.Context(), artifact.Body)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, body := range snap.Blobs {
		found = found || strings.Contains(string(body), "synthetic harness")
		if strings.Contains(string(body), "synthetic-private-token") {
			t.Fatal("protected input escaped scrubbing")
		}
	}
	if !found || len(artifact.Assets) == 0 {
		t.Fatal("selected harness or original-input evidence missing")
	}
	for _, asset := range artifact.Assets {
		if err := runtimeEnrichmentAssetPolicy(c)(t.Context(), spec, asset, true); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRuntimeEnrichmentPolicyRejectsForgedScopeProtectedAndUndeclaredAssets(t *testing.T) {
	c, spec := runtimeEnrichmentFixture(t)
	if err := validateRuntimeEnrichment(c, spec); err != nil {
		t.Fatal(err)
	}
	bad := spec
	bad.AdapterSessionKey = "another-session"
	if err := validateRuntimeEnrichment(c, bad); err == nil {
		t.Fatal("forged owner accepted")
	}
	check := runtimeEnrichmentAssetPolicy(c)
	for index, path := range []string{c.ConfigPath(), filepath.Join(c.Spool, "payload"), filepath.Join(c.Home, ".ssh", "id_rsa"), filepath.Join(filepath.Dir(c.Home), "undeclared", "CLAUDE.md")} {
		asset := sessioncapture.EnrichmentAsset{Path: path, Identities: []string{path}, Scope: "project", Selector: "project:" + path, Kind: "file"}
		if err := check(t.Context(), spec, asset, true); err == nil {
			t.Fatalf("protected or undeclared input %d accepted", index)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := check(ctx, spec, sessioncapture.EnrichmentAsset{}, false); err == nil {
		t.Fatal("cancellation ignored")
	}
}
