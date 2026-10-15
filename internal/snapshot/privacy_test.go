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

func TestSnapshotWithholdsQuotedAndMalformedAssignments(t *testing.T) {
	e := testenv.New(t)
	for name, body := range map[string]string{
		"fishQuotedName": "set -gx 'API_KEY' fixture-secret",
		"fishReordered":  "set -xg API_KEY fixture-secret",
		"fishSplit":      "set -g -x API_KEY fixture-secret",
		"fishLong":       "set --global --export API_KEY fixture-secret",
		"fishAndExport":  "true;and export API_KEY=fixture-secret",
		"fishOrExport":   "false;or export API_KEY=fixture-secret",
		"fishHash":       "set -gx PASSWORD '#fixture-secret'",
		"separator":      "OTHER=ok;API_KEY=fixture-secret",
		"fishSeparator":  "set -gx OTHER ok;set -gx API_KEY fixture-secret",
		"malformed":      "OTHER=ok API_KEY=fixture-secret CMD='",
		"singleQuoted":   "API_KEY='${TOKEN}'",
		"escaped":        `API_KEY=\$TOKEN`,
		"fishQuoted":     "set -gx API_KEY '${TOKEN}'",
		"fishEscaped":    `set -gx API_KEY \$TOKEN`,
	} {
		path := "skills/" + name + ".sh"
		testenv.Write(t, filepath.Join(e.Claude, filepath.FromSlash(path)), body)
	}
	set := build(t, e, e.Project)
	if len(set.Skipped) != 14 {
		t.Fatalf("skips = %+v, want 14 withheld assignments", set.Skipped)
	}
	for _, skipped := range set.Skipped {
		if skipped.Reason != "literal_secret_assignment" {
			t.Fatalf("skip reason = %s, want literal_secret_assignment", skipped.Reason)
		}
		if slices.ContainsFunc(set.Files, func(f File) bool { return f.Path == skipped.Path }) {
			t.Fatal("withheld assignment retained in snapshot")
		}
	}
}

func TestSnapshotKeepsFishReferencesAndUnrelatedCommands(t *testing.T) {
	e := testenv.New(t)
	for name, body := range map[string]string{
		"quotedName":  "set -gx 'API_KEY' $TOKEN",
		"reordered":   "set -xg API_KEY \"${TOKEN}\"",
		"split":       "set -g -x API_KEY $TOKEN",
		"long":        "set --global --export API_KEY $TOKEN",
		"andExport":   "true;and export API_KEY=$TOKEN",
		"orExport":    "false;or export API_KEY=$TOKEN",
		"query":       "set -q API_KEY fixture-value",
		"prose":       "For example, set -xg API_KEY fixture-value",
		"invalidName": "set -gx 'PASSWORD NAME' fixture-value",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(e.Claude, "skills", name+".sh")
			testenv.Write(t, path, body)
			c := newCollected("claude")
			got := c.add("user", path, name+".sh")
			if got == nil || string(got.body) != body || len(c.skipped) != 0 {
				t.Fatal("snapshot did not retain allowed text unchanged")
			}
		})
	}
}
