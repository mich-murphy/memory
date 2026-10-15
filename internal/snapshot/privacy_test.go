package snapshot

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/mich-murphy/memory/internal/testenv"
)

func TestSnapshotWithholdsAssignmentsIncludingFileAliases(t *testing.T) {
	e := testenv.New(t)
	outside := filepath.Join(t.TempDir(), "script.txt")
	testenv.Write(t, outside, "export API_KEY=fixture-private-value\n")
	symlink(t, outside, filepath.Join(e.Claude, "skills", "x", "run.sh"))
	testenv.Write(t, filepath.Join(e.Claude, "skills", "x", "refs.sh"), "export API_KEY=$TOKEN\n")
	testenv.Write(t, filepath.Join(e.Claude, "skills", "x", "README.md"), "For example, API_KEY=example\n# API_KEY=example\n")
	testenv.Write(t, filepath.Join(e.Claude, "plugins", "known_marketplaces.json"), "{\"fixture\":true}")
	set := build(t, e, e.Project)
	if !slices.ContainsFunc(set.Skipped, func(s SkippedFile) bool {
		return s.Path == "skills/x/run.sh" && s.Reason == "literal_secret_assignment"
	}) {
		t.Fatalf("skips = %+v", set.Skipped)
	}
	for _, name := range []string{"skills/x/refs.sh", "skills/x/README.md"} {
		if !slices.ContainsFunc(set.Files, func(f File) bool { return f.Path == name }) {
			t.Errorf("missing allowed file %s", name)
		}
	}
	for _, f := range set.Files {
		if f.Path == "skills/x/run.sh" || f.Path == "plugins/known_marketplaces.json" {
			t.Errorf("unexpected snapshot file %s", f.Path)
		}
	}
}
