package exclusion

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mich-murphy/memory/internal/adapters"
)

func TestNonUTF8ExclusionIdentityCannotReplacePersistedOptOut(t *testing.T) {
	dir := t.TempDir()
	a := adapters.NewClaude(t.TempDir())
	baseline, err := RecordPath(dir, a, a.CanonicalRoot()+"/projects/slug/session.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	bad := string([]byte{0xff})
	for _, mutate := range []func(*Record){
		func(r *Record) { r.Root += bad },
		func(r *Record) { r.ResolvedRoot += bad },
		func(r *Record) { r.SessionKey += bad },
		func(r *Record) { r.Paths = []string{"/unsupported/" + bad} },
	} {
		record := baseline
		mutate(&record)
		if err := Save(dir, record); err == nil || !strings.Contains(err.Error(), "non-UTF-8") {
			t.Fatalf("Save = %v, want unsupported identity rejection", err)
		}
		registry, err := Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff(Registry{baseline}, registry); diff != "" {
			t.Fatalf("persisted exclusion changed (-want +got): %s", diff)
		}
	}
}
