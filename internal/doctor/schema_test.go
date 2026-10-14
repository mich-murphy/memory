package doctor

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mich-murphy/memory/internal/install"
	"github.com/mich-murphy/memory/internal/jsonx"
	"github.com/mich-murphy/memory/internal/testenv"
)

func TestCodexRejectedDocumentCannotHaveUsableHooks(t *testing.T) {
	e := testenv.New(t)
	binary := filepath.Join(t.TempDir(), "agent-sessions")
	testenv.Write(t, binary, "#!/bin/sh\nexit 1\n")
	if err := os.Chmod(binary, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := install.Codex(&install.Options{Home: e.Home, Command: []string{binary}}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(e.Home, ".codex", "hooks.json")
	doc := readJSON(path)
	doc.Set("unexpected", true) // Codex's HooksFile rejects unknown top-level fields.
	body, err := jsonx.Compact(doc)
	if err != nil {
		t.Fatal(err)
	}
	testenv.Write(t, path, string(body))
	var output bytes.Buffer
	r := &reporter{w: &output}
	checkCodex(e.Home, r)
	if !r.failed || strings.Contains(output.String(), "usable required events") || strings.Contains(output.String(), "hooks trusted") {
		t.Errorf("checkCodex = %s, want whole-document failure without usable/trusted counts", &output)
	}
}
