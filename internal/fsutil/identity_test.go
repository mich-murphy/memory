package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSameLocation(t *testing.T) {
	root := t.TempDir()
	original, alias, other := filepath.Join(root, "original"), filepath.Join(root, "alias"), filepath.Join(root, "other")
	for _, path := range []string{original, other} {
		if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(original, alias); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(root, "missing")
	for _, test := range []struct {
		left, right string
		want        bool
	}{
		{original, original, true},
		{original, alias, true},
		{original, other, false},
		{missing, missing, true},
		{missing, original, false},
		{original, missing, false},
		{"", "", false},
	} {
		if got := SameLocation(test.left, test.right); got != test.want {
			t.Errorf("SameLocation(%q, %q) = %t, want %t", test.left, test.right, got, test.want)
		}
	}
}
