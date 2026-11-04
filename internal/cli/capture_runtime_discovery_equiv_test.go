package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mich-murphy/memory/internal/capturesource"
)

func discoveryProjection(res capturesource.Result) []string {
	var out []string
	for i := range res.Requests {
		q := &res.Requests[i]
		out = append(out, fmt.Sprintf("req|%s|%v|%v|%v", q.Path, q.Role, q.Transform, q.Fingerprint))
	}
	for i := range res.Issues {
		is := &res.Issues[i]
		out = append(out, fmt.Sprintf("issue|%s|%s|%s", is.Root.Path, is.Path, is.Code))
	}
	slices.Sort(out)
	return out
}

func mustWrite(t testing.TB, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("synthetic\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// snapshotPolicy (memoized) and policy(true) (live, unmemoized) give identical Discover output.
func TestDiscoveryEquivalenceSnapshotVersusLivePolicy(t *testing.T) {
	c, config, _ := snapshotFixture(t)
	root := filepath.Join(c.Home, ".claude")
	mustWrite(t, filepath.Join(c.Spool, "x.jsonl"))
	if err := os.Symlink(c.Spool, filepath.Join(root, "projects", "alias")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"p/auth.json", "p/.DS_Store"} {
		mustWrite(t, filepath.Join(root, "projects", name))
	}
	mustWrite(t, filepath.Join(root, ".credentials.json"))
	mustWrite(t, filepath.Join(root, ".claude.json"))
	r, err := newRuntimeComposition(c)
	if err != nil {
		t.Fatal(err)
	}
	run := func(snapshot bool) []string {
		opts := r.sourceOptions(config)
		if snapshot {
			opts.Policy = r.scopePolicy(r.snapshotPolicy(true))
		}
		res, err := capturesource.Discover(t.Context(), opts)
		if err != nil {
			t.Fatal(err)
		}
		if res.Cursor != nil {
			if err := res.Cursor.Close(); err != nil {
				t.Fatal(err)
			}
		}
		return discoveryProjection(res)
	}
	check := func(phase string, wantRequests int) {
		snap, live := run(true), run(false)
		if d := cmp.Diff(live, snap); d != "" {
			t.Fatalf("%s: snapshot output differs from live policy (-live +snapshot):\n%s", phase, d)
		}
		n := 0
		for _, line := range snap {
			if !strings.HasPrefix(line, "req|") {
				continue
			}
			n++
			for _, banned := range []string{"auth.json", ".DS_Store", "alias", ".credentials.json"} {
				if strings.Contains(line, banned) {
					t.Errorf("%s: excluded entry admitted (%s)", phase, banned)
				}
			}
		}
		if n < wantRequests || wantRequests == 0 && n != 0 {
			t.Errorf("%s: requests = %d, want at least %d (0 means exactly 0)", phase, n, wantRequests)
		}
	}
	check("before marker", snapshotFiles)
	if err := recordUnresolvedNativePolicy(t.Context(), c, "claude"); err != nil {
		t.Fatal(err)
	}
	check("after marker", 0)
}
