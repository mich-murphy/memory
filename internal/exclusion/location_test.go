package exclusion

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mich-murphy/memory/internal/adapters"
)

func TestOptOutCoversFilesystemCaseAliases(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	a := adapters.NewClaude(t.TempDir())
	root := a.CanonicalRoot()
	path := filepath.Join(root, "projects", "slug", "11111111-2222-3333-4444-555555555555.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("private fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	variantRoot := strings.ToUpper(root)
	original, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	variant, err := os.Stat(variantRoot)
	if errors.Is(err, fs.ErrNotExist) {
		t.Skip("filesystem has no case aliases")
	}
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(original, variant) {
		t.Skip("case variants are distinct directories")
	}
	record, err := RecordPath(t.TempDir(), a, path)
	if err != nil {
		t.Fatal(err)
	}
	registry := Registry{record}
	variantPath := filepath.Join(variantRoot, "projects", "slug", filepath.Base(path))
	source, ok := a.Classify(variantPath, variantRoot)
	if !ok || !registry.Excludes(source) {
		t.Fatal("opted-out source not excluded through filesystem root alias")
	}
	alias := filepath.Join(a.Home(), "alias.data")
	if err := os.Symlink(filepath.Join(strings.ToUpper(filepath.Dir(path)), filepath.Base(path)), alias); err != nil {
		t.Fatal(err)
	}
	if !registry.ExcludesPath(a, alias) || !registry.ExcludesPath(a, "~/alias.data") {
		t.Fatal("known opted-out object not excluded through an unclassified alias")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if !registry.Excludes(source) {
		t.Fatal("existing root identity must preserve opt-out after source removal")
	}
	source.SessionKey = ""
	source.Path = filepath.Join(variantRoot, "history.jsonl")
	if !registry.Excludes(source) {
		t.Fatal("shared source not excluded through filesystem root alias")
	}
	source.SessionKey = "other"
	source.Path = filepath.Join(variantRoot, "projects", "slug", "other.jsonl")
	if registry.Excludes(source) {
		t.Fatal("unrelated session excluded")
	}
}
