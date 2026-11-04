package capturesource

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
)

// allowed asks Policy twice per evaluation: once for the bare path, once for resolved identities.
const policyCallsPerEvaluation = 2

// Artifacts sharing an owner evaluate the owner's main coordinates once per call,
// and a second call re-evaluates them (the memo never crosses calls).
func TestDiscoverEvaluatesSharedOwnerCoordinatesOncePerCall(t *testing.T) {
	o := options(t)
	root := Root{"claude", filepath.Join(o.Home, ".claude")}
	o.Roots, o.OnlyRoots, o.Derived = []Root{root}, true, true
	main := fixture(t, filepath.Join(root.Path, "projects", "slug", externalTestUUID+".jsonl"), "raw")
	m, err := Classify(t.Context(), o, "claude", main)
	if err != nil {
		t.Fatal(err)
	}
	const artifacts = 6
	for i := range artifacts {
		fixture(t, filepath.Join(root.Path, "file-history", externalTestUUID, fmt.Sprintf("v%d", i)), "raw")
	}
	o.LookupOwner = func(context.Context, Root, string) (string, string, error) {
		return m.Identity.SessionID, "slug/" + externalTestUUID, nil
	}
	counts := map[string]int{}
	o.Policy = func(_ context.Context, s Scope) error {
		if len(s.Identities) == 1 && s.Identities[0] == s.Path {
			counts[s.Path]++
		}
		return nil
	}
	for call := 1; call <= 2; call++ {
		clear(counts)
		res := discover(t, o)
		if len(res.Requests) != artifacts+1 {
			t.Fatalf("call %d: requests = %d, want %d", call, len(res.Requests), artifacts+1)
		}
		for _, p := range []string{main, main + ".zst"} {
			if counts[p] != policyCallsPerEvaluation {
				t.Errorf("call %d: %s Policy calls = %d, want %d (one evaluation)", call, filepath.Base(p), counts[p], policyCallsPerEvaluation)
			}
		}
	}
}
