package sessioncapture

import (
	"os"
	"path/filepath"
	"testing"
)

// An optional artifact is unique local bytes: selection supersession followed by
// reclamation must never remove its file, and delivery must still succeed.
func TestSelectionSupersessionKeepsOptionalArtifactFile(t *testing.T) {
	q, r, m := optionalFixture(t)
	art := filepath.Join(q.dir, "payloads", m.Sources[1].Object.EncodedSHA256+".zst")
	if _, err := q.supersedeOptional(t.Context(), r.Identity); err != nil {
		t.Fatal(err)
	}
	if err := q.Deliver(t.Context(), newBackend(t), r.Identity); err != nil {
		t.Fatalf("deliver after selection supersession: %v", err)
	}
	if _, err := os.Stat(art); err != nil {
		t.Fatalf("optional artifact payload removed: %v", err)
	}
}
