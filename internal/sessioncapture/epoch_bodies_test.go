package sessioncapture

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mich-murphy/memory/internal/publication"
)

// TestEpochBodiesAreExact checks the bytes, state and revision of every
// retained epoch after append, same-size rewrite and truncation. Counts and
// states alone cannot tell a stale object from the right one.
func TestEpochBodiesAreExact(t *testing.T) {
	dir, o, r := setup(t)
	q := open(t, dir, o)
	capture(t, q, r)
	write(t, r.Path, "first\nsecond\n")
	capture(t, q, r)
	write(t, r.Path, "other\nsecond\n") // same size, different prefix
	capture(t, q, r)
	write(t, r.Path, "x")
	capture(t, q, r)
	backend := newBackend(t)
	if err := q.Deliver(t.Context(), backend, r.Identity); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "export")
	ex, err := publication.ExportSession(t.Context(), backend, r.Identity, dest)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		body, state string
		rev         uint64
	}{{"first\nsecond\n", "sealed", 2}, {"other\nsecond\n", "sealed", 1}, {"x", "active", 1}}
	if len(ex.Bodies) != len(want) || len(ex.Manifest.Sources) != len(want) {
		t.Fatalf("bodies %d sources %d, want %d", len(ex.Bodies), len(ex.Manifest.Sources), len(want))
	}
	seen := map[string]bool{}
	for i, w := range want {
		b, err := os.ReadFile(filepath.Join(dest, ex.Bodies[i].Path))
		if err != nil {
			t.Fatal(err)
		}
		s := ex.Manifest.Sources[i]
		if string(b) != w.body || s.State != w.state || s.Revision != w.rev || ex.Bodies[i].EpochID != s.EpochID {
			t.Errorf("epoch %d: body=%q state=%s rev=%d; want body=%q state=%s rev=%d", i, b, s.State, s.Revision, w.body, w.state, w.rev)
		}
		if seen[s.EpochID] {
			t.Errorf("duplicate epoch id at %d", i)
		}
		seen[s.EpochID] = true
	}
}
