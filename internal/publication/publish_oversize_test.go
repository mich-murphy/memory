package publication

import (
	"strings"
	"testing"
)

// Publish measures the manifest once, through MarshalValidManifest. A manifest
// whose own JSON exceeds MaxMetadata must be rejected before any backend access.
func TestPublishRejectsOversizeManifestBeforeBackendAccess(t *testing.T) {
	b, m, _ := fixture(t)
	m.Coverage.Missing = append(append([]string(nil), m.Coverage.Missing...), strings.Repeat("x", MaxMetadata))
	counter := &metadataIOCounter{Backend: b}
	if _, err := Publish(t.Context(), counter, m, ""); err == nil || err.Error() != "metadata exceeds limit" {
		t.Fatalf("Publish error %v, want %q", err, "metadata exceeds limit")
	}
	if counter.calls != 0 {
		t.Fatalf("oversize manifest accessed backend %d times", counter.calls)
	}
}
