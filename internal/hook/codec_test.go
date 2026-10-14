package hook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNonUTF8LaunchPathCannotBeSilentlyRenamedInQueue(t *testing.T) {
	dir := t.TempDir()
	path := "/unsupported/" + string([]byte{0xff})
	err := recordSessionStart(t.Context(), dir, "pi", path, map[string]any{}, "SessionStart", ExtensionArgs{Argv: `["pi"]`})
	if err == nil || !strings.Contains(err.Error(), "non-UTF-8") {
		t.Fatalf("recordSessionStart = %v, want unsupported path rejection", err)
	}
	files, err := filepath.Glob(filepath.Join(dir, "pending", "launch-*.json"))
	if err != nil || len(files) != 0 {
		t.Fatalf("unsupported launch queue = %v, %v, want empty", files, err)
	}
	if err := recordSessionStart(t.Context(), dir, "pi", "/supported/café", map[string]any{}, "SessionStart", ExtensionArgs{Argv: `["pi"]`}); err != nil {
		t.Fatal(err)
	}
	files, err = filepath.Glob(filepath.Join(dir, "pending", "launch-*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("supported launch queue = %v, %v, want one envelope", files, err)
	}
	body, err := os.ReadFile(files[0])
	if err != nil || !strings.Contains(string(body), "/supported/café") {
		t.Fatalf("supported Unicode path was not preserved: %v", err)
	}
}
