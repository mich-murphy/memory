package cli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mich-murphy/memory/internal/capturecoordinates"
	"github.com/mich-murphy/memory/internal/capturedaemon"
	"github.com/mich-murphy/memory/internal/capturesource"
	"github.com/mich-murphy/memory/internal/exclusion"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

func TestRuntimeCompositionCompilesOnceAcrossCallbacks(t *testing.T) {
	c, _ := runtimeFixture(t)
	root := filepath.Join(c.Home, ".claude")
	old := filepath.Join(filepath.Dir(c.Home), "old-root")
	moved := filepath.Join(filepath.Dir(c.Home), "moved-root")
	c.RootAliases = []capturedaemon.RootAlias{{Agent: "claude", Root: root, Path: old}, {Agent: "claude", Root: root, Path: moved}}
	path := filepath.Join(moved, "projects", "project", "a.jsonl")
	runtimeWrite(t, path)
	calls := 0
	r, err := composeRuntime(c, func() (*capturecoordinates.Inventory, error) { calls++; return c.CoordinateInventory() })
	if err != nil || calls != 1 {
		t.Fatalf("compile count %d: %v", calls, err)
	}
	// Replace the declarations in this test composition without mutating their
	// backing array. A callback recompiling them would forget the moved root;
	// the original compiled snapshot must remain the only coordinate authority.
	r.config.RootAliases = []capturedaemon.RootAlias{{Agent: "claude", Root: root, Path: old}, {Agent: "claude", Root: root, Path: moved + "-redeclared"}}
	queue := r.queueOptions()
	source := r.sourceOptions("")
	if queue.Coordinates != r.inventory || source.Coordinates != r.inventory {
		t.Fatal("callbacks did not share inventory")
	}
	req, err := capturesource.Classify(t.Context(), source, "claude", path)
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.CheckPolicy(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if err := r.policy(true)(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if !r.aliasMapped("claude", path) || r.aliasMapped("claude", moved+"-redeclared") {
		t.Fatal("alias membership did not use the original snapshot")
	}
	if calls != 1 {
		t.Fatalf("policy recompiled declarations: %d", calls)
	}
	// Effective opt-outs are fresh even while the declaration snapshot is reused.
	owner, err := r.privacyOwner(t.Context(), req, false)
	if err != nil || owner == nil {
		t.Fatalf("owner: %v", err)
	}
	if _, err := exclusion.RecordFile(c.PolicyDir, *owner); err != nil {
		t.Fatal(err)
	}
	if err := queue.CheckPolicy(t.Context(), req); !errors.Is(err, sessioncapture.ErrSessionExcluded) {
		t.Fatalf("opt-out not observed: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := queue.CheckPolicy(t.Context(), req); !errors.Is(err, sessioncapture.ErrSessionExcluded) {
		t.Fatalf("moved/missing alias lost opt-out: %v", err)
	}
	if calls != 1 {
		t.Fatalf("policy recompiled declarations: %d", calls)
	}
}
