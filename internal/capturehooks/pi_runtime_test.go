package capturehooks

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Execute the generated producer, not a reimplementation or an installed SDK.
// Node's built-in type stripper erases only TypeScript types; spawn is mocked
// before ESM import so no collector command, model, or network is invoked.
func TestPiGeneratedHandlersRespectNoSession(t *testing.T) {
	body, err := piExtension([]string{"/synthetic/collector", "capture-v1", "hook", "--config", "/synthetic/config", "--agent", "pi"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "generated.ts")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("generated Pi handler regression requires Node with stripTypeScriptTypes:", err)
	}
	cmd := exec.CommandContext(t.Context(), node, "testdata/pi-runtime.mjs", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated Pi handlers: %v\n%s", err, out)
	}
}
