package snapshot

import (
	"path/filepath"
	"testing"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/raw"
	"github.com/mich-murphy/memory/internal/testenv"
)

func TestPiSnapshotPreservesResolvedNpmVersions(t *testing.T) {
	e := testenv.New(t)
	root := filepath.Join(e.Home, ".pi", "agent")
	lock := `{"lockfileVersion":3,"packages":{"node_modules/example":{"version":"1.2.3","integrity":"sha512-example"}}}`
	packageFile := `{"dependencies":{"example":"^1.0.0"}}`
	testenv.Write(t, filepath.Join(root, "npm", "package.json"), packageFile)
	testenv.Write(t, filepath.Join(root, "npm", "package-lock.json"), lock)
	testenv.Write(t, filepath.Join(root, "npm", "node_modules", "example", "index.js"), "do not snapshot installed dependency trees")
	first, err := Build(adapters.NewPi(e.Home), root, e.Project, "")
	if err != nil {
		t.Fatal(err)
	}
	wanted := map[string]string{"npm/package.json": packageFile, "npm/package-lock.json": lock}
	for _, file := range first.Harness.Files {
		if body, ok := wanted[file.Path]; ok {
			if file.SHA256 != raw.SHA256([]byte(body)) || string(first.Blobs[file.SHA256]) != body {
				t.Fatalf("exact resolved package metadata not preserved: %s", file.Path)
			}
			delete(wanted, file.Path)
		}
		if file.Path == "npm/node_modules/example/index.js" {
			t.Fatal("installed dependency tree captured")
		}
	}
	if len(wanted) != 0 {
		t.Fatalf("package metadata absent from snapshot: %v", wanted)
	}
	testenv.Write(t, filepath.Join(root, "npm", "package-lock.json"), `{"lockfileVersion":3,"packages":{"node_modules/example":{"version":"1.2.4"}}}`)
	second, err := Build(adapters.NewPi(e.Home), root, e.Project, "")
	if err != nil {
		t.Fatal(err)
	}
	if first.Harness.ID == second.Harness.ID {
		t.Fatal("changed installed version did not change the harness snapshot")
	}
}
