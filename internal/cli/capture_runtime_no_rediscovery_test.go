package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/capturedaemon"
	"github.com/mich-murphy/memory/internal/exclusion"
)

// This is a work-mechanism assertion, not a filesystem-size/timing heuristic.
// These policy translations may not re-enter either ownership classifier: Claude's
// classifier enumerates all projects for UUID-addressed external artifacts.
func TestAliasPolicyNeverReclassifiesOwnership(t *testing.T) {
	for file, name := range map[string]string{"capture_runtime_alias_policy.go": "aliasExcluded", "capture_runtime_claimed_policy.go": "runtimeClaimedRecordAliases"} {
		tree, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, decl := range tree.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name.Name != name {
				continue
			}
			found = true
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if ok && (sel.Sel.Name == "Classify" || sel.Sel.Name == "ClassifyLogical") {
					t.Errorf("%s re-enters ownership classifier %s", name, sel.Sel.Name)
				}
				return true
			})
		}
		if !found {
			t.Fatalf("missing policy function %s", name)
		}
	}
}

type noRediscoveryAdapter struct {
	adapters.Adapter
	calls int
}

func (a *noRediscoveryAdapter) CanonicalRoot() string { return a.Adapter.CanonicalRoot() }
func (a *noRediscoveryAdapter) Classify(string, string) (adapters.SessionFile, bool) {
	a.calls++
	return adapters.SessionFile{}, false
}

func TestClaimedArtifactAliasesUsePreservedTupleWithoutEnumeration(t *testing.T) {
	c, _ := runtimeFixture(t)
	logical := filepath.Join(c.Home, ".claude")
	old := filepath.Join(c.Home, "absent-old")
	current := filepath.Join(c.Home, "new")
	runtimeWrite(t, filepath.Join(current, "projects", "aaa", "11111111-1111-1111-1111-111111111111.jsonl"))
	c.RootAliases = []capturedaemon.RootAlias{{Agent: "claude", Root: logical, Path: old}, {Agent: "claude", Root: logical, Path: current}}
	r, err := newRuntimeComposition(c)
	if err != nil {
		t.Fatal(err)
	}
	adapter := &noRediscoveryAdapter{Adapter: adapters.NewClaude(c.Home)}
	const uuid = "11111111-1111-1111-1111-111111111111"
	key := runtimeClaimedLegacyKey(adapter, logical, old, "zzz/"+uuid)
	record := exclusion.Record{Agent: "claude", Root: old, SessionKey: key, Paths: []string{filepath.Join(old, "tasks", uuid, "result.txt")}}
	for _, root := range []string{logical, old, current} {
		session := claimedTestSession(root, runtimeClaimedLegacyKey(adapter, old, root, key))
		matched, err := runtimeClaimedRecordAliases(r.inventory, adapter, session, record)
		if err != nil || !matched {
			t.Fatalf("preserved full tuple lost for %s: %v", root, err)
		}
	}
	if adapter.calls != 0 {
		t.Fatalf("policy invoked legacy ownership classifier %d times; external artifacts trigger global enumeration", adapter.calls)
	}
	// A duplicate UUID does not collapse the full project/session tuple.
	matched, err := runtimeClaimedRecordAliases(r.inventory, adapter, claimedTestSession(logical, "aaa/"+uuid), record)
	if err != nil || matched {
		t.Fatalf("duplicate UUID became opted-out owner: %v", err)
	}
}
