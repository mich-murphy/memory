package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mich-murphy/memory/internal/publication"
)

func TestCaptureSDKFixtureJourney(t *testing.T) {
	root := filepath.Join(t.TempDir(), "fixture")
	var output bytes.Buffer
	code, err := runCaptureFixture(t.Context(), []string{"--output", root, "--backend", "sdk-fixture"}, IO{Stdout: &output, Stderr: &output})
	if err != nil || code != exitOK {
		t.Fatalf("fixture code=%d err=%v", code, err)
	}
	for _, name := range []string{"sources", "spool", "store"} {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("%s exists or unexpected error", name)
		}
	}
	wantBytes, err := os.ReadFile(filepath.Join(root, "expectations.json"))
	if err != nil {
		t.Fatal(err)
	}
	var expected struct {
		Identity publication.Identity `json:"identity"`
		Bodies   []string             `json:"retained_bodies"`
	}
	if err := json.Unmarshal(wantBytes, &expected); err != nil {
		t.Fatal(err)
	}
	if expected.Identity.ArchiveID != fixtureArchive || len(expected.Bodies) != 3 {
		t.Fatal("fixture expectations changed")
	}
	// Verify every expected source body appears exactly once among exported files.
	found := make(map[string]int)
	err = filepath.WalkDir(filepath.Join(root, "export"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		found[string(body)]++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range expected.Bodies {
		if found[body] != 1 {
			t.Fatal("export body coverage mismatch")
		}
	}
}

func TestCaptureSDKFixtureFlags(t *testing.T) {
	var output bytes.Buffer
	for _, args := range [][]string{{"--output", "out", "--backend", "remote"}, {"--backend", "sdk-fixture"}} {
		if _, _, err := fixtureFlags(args, IO{Stdout: &output, Stderr: &output}); err == nil {
			t.Fatal("invalid flags accepted")
		}
	}
	_, kind, err := fixtureFlags([]string{"--output", "out"}, IO{Stdout: &output, Stderr: &output})
	if err != nil || kind != "local" {
		t.Fatal("default backend changed")
	}
}
