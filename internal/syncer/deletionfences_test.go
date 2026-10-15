package syncer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/raw"
)

func TestDeletionFenceMetadataUsesLinkIdentityAndCachesComparisons(t *testing.T) {
	root := t.TempDir()
	for _, dangling := range []bool{false, true} {
		target := t.TempDir()
		if dangling {
			target = filepath.Join(target, "missing")
		}
		link := filepath.Join(root, "link")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		f := newDeletionFences([]adapters.SkippedFile{{Path: link, Reason: "symlink_directory"}})
		if !f.sameBoundary(link, link) {
			t.Fatal("sameBoundary did not recognise the link itself")
		}
		missing := filepath.Join(root, "missing")
		if f.sameBoundary(link, missing) {
			t.Fatal("sameBoundary conflated a missing distinct path with a stable link")
		}
		if err := os.Remove(link); err != nil {
			t.Fatal(err)
		}
		if f.sameBoundary(link, missing) || len(f.comparisons) != 2 {
			t.Fatal("comparison was not cached for the sweep")
		}
		if !newDeletionFences(nil).sameBoundary(link, missing) {
			t.Fatal("disappeared boundary did not conservatively fence")
		}
	}
}

func TestDeletionFencesKeepUnrelatedPathsLexicalAndChangedBoundariesConservative(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(root, "archive")
	f := newDeletionFences([]adapters.SkippedFile{{Path: link, Reason: "symlink_unresolved"}})
	if f.hasSource([]raw.Source{{Path: link + "-sibling/source.ts"}}) || len(f.comparisons) != 0 {
		t.Fatal("unrelated source performed a metadata comparison or was fenced")
	}
	if !f.hasSource([]raw.Source{{Path: filepath.Join(root, "Archive", "source.ts")}}) {
		t.Fatal("missing skipped boundary did not conservatively fence")
	}
	if err := os.Mkdir(link, 0o700); err != nil {
		t.Fatal(err)
	}
	if !newDeletionFences(nil).sameBoundary(link, filepath.Join(root, "Archive")) {
		t.Fatal("boundary replaced by directory did not conservatively fence")
	}
}

func TestBoundaryAliasCandidatesRejectUnrelatedASCIIComponents(t *testing.T) {
	for name, tc := range map[string]struct {
		left, right string
		want        bool
	}{
		"identical":                {"/home/skills/archive", "/home/skills/archive", true},
		"case":                     {"/home/skills/Archive", "/home/skills/archive", true},
		"asciiDistinct":            {"/home/skills/archive", "/home/skills/other", false},
		"normalization":            {"/home/skills/Café", "/home/skills/Cafe\u0301", true},
		"unicodeHome":              {"/Café/skills/archive", "/Cafe\u0301/skills/Archive", true},
		"unicodeHomeDifferentTree": {"/Café/skills/archive", "/Cafe\u0301/plugins/archive", false},
		"unicodeHomeDifferentName": {"/Café/skills/archive", "/Café/skills/other", false},
		"differentDepth":           {"/home/skills/Café", "/home/skills/Cafe\u0301/child", false},
		"distinctUnicodeCandidate": {"/home/skills/Café", "/home/skills/Cafè", true},
		"mixedUnicodeASCII":        {"/home/skills/\u212a", "/home/skills/K", true},
	} {
		t.Run(name, func(t *testing.T) {
			if got := mayAliasBoundary(tc.left, tc.right); got != tc.want {
				t.Fatalf("mayAliasBoundary = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestUnicodeBoundaryCandidatesStillRequireSameLinkIdentity(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Café")
	ancestor := filepath.Join(root, "skills", "Café")
	if err := os.MkdirAll(ancestor, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "skills", "Cafè")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Fatal(err)
	}
	f := newDeletionFences([]adapters.SkippedFile{{Path: link, Reason: "symlink_directory"}})
	if f.hasSource([]raw.Source{{Path: filepath.Join(ancestor, "source.ts")}}) || len(f.comparisons) != 1 {
		t.Fatal("distinct Unicode entries were fenced instead of compared by identity")
	}
	if f.hasSource([]raw.Source{{Path: filepath.Join(root, "plugins", "Café", "source.ts")}}) || len(f.comparisons) != 1 {
		t.Fatal("Unicode home caused metadata comparison for unrelated ASCII subtree")
	}
}
