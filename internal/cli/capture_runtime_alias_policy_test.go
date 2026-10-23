package cli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/capturedaemon"
	"github.com/mich-murphy/memory/internal/capturesource"
	"github.com/mich-murphy/memory/internal/exclusion"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

func TestRuntimeDeclaredAliasOptOutSurvivesOldRootDeletion(t *testing.T) {
	c, _ := runtimeFixture(t)
	logical := filepath.Join(c.Home, ".claude")
	old := filepath.Join(filepath.Dir(c.Home), "old-agent")
	current := filepath.Join(filepath.Dir(c.Home), "current-agent")
	aPath := filepath.Join(old, "projects", "project", "a.jsonl")
	runtimeWrite(t, aPath)
	adapter := adapters.All(c.Home)["claude"]
	owner, ok := adapter.Classify(aPath, old)
	if !ok {
		t.Fatal("fixture owner unclassified")
	}
	if _, err := exclusion.RecordFile(c.PolicyDir, owner); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(old); err != nil {
		t.Fatal(err)
	}
	c.RootAliases = []capturedaemon.RootAlias{{Agent: "claude", Root: logical, Path: old}, {Agent: "claude", Root: logical, Path: current}}
	aPath = filepath.Join(current, "projects", "project", "a.jsonl")
	bPath := filepath.Join(current, "projects", "project", "b.jsonl")
	runtimeWrite(t, aPath)
	runtimeWrite(t, bPath)
	opts, err := runtimeSourceOptions(c, "")
	if err != nil {
		t.Fatal(err)
	}
	// The classifier policy already denies A. Identity cannot be supplied by a hint.
	if _, err := capturesource.Classify(t.Context(), opts, "claude", aPath); err == nil {
		t.Fatal("old absent-root opt-out bypassed through new physical route")
	}
	b, err := capturesource.Classify(t.Context(), opts, "claude", bPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtimePolicy(c)(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	// Independently derived ownership must still reject a forged healthy-session identity.
	a := b
	a.Path = aPath
	if err := runtimePolicy(c)(t.Context(), a); !errors.Is(err, sessioncapture.ErrSessionExcluded) {
		t.Fatal("supplied identity bypassed alias opt-out")
	}
	if err := os.Remove(aPath); err != nil {
		t.Fatal(err)
	}
	if err := runtimePolicy(c)(t.Context(), a); !errors.Is(err, sessioncapture.ErrSessionExcluded) {
		t.Fatal("missing source erased alias opt-out")
	}
	// No alias declaration weakens storage/config policy.
	alias := filepath.Join(current, "projects", "project", "config.jsonl")
	if err := os.Symlink(filepath.Join(c.PolicyDir, "exclusions"), alias); err != nil {
		t.Fatal(err)
	}
	if err := runtimeDiscoveryPolicy(c)(t.Context(), sessioncapture.Request{Identity: b.Identity, Path: alias}); err == nil {
		t.Fatal("undeclared protected alias admitted")
	}
}

func TestRuntimeDeclaredLeafAliasHasNoParentAuthority(t *testing.T) {
	c, _ := runtimeFixture(t)
	logical := filepath.Join(c.Home, ".claude", "projects", "project", "a.jsonl")
	physical := filepath.Join(filepath.Dir(c.Home), "one-exact.jsonl")
	runtimeWrite(t, physical)
	c.LeafAliases = []capturedaemon.LeafAlias{{Agent: "claude", LogicalPath: logical, Path: physical}}
	opts, err := runtimeSourceOptions(c, "")
	if err != nil {
		t.Fatal(err)
	}
	request, err := capturesource.Classify(t.Context(), opts, "claude", physical)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtimePolicy(c)(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	request.Path = physical + "-sibling"
	if err := runtimePolicy(c)(t.Context(), request); err == nil {
		t.Fatal("exact leaf authorized sibling")
	}
	if c.AllowsSource("claude", filepath.Dir(physical)) {
		t.Fatal("exact leaf authorized its parent")
	}
}
