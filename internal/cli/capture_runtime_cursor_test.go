package cli

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/mich-murphy/memory/internal/capturedaemon"
)

// A root with more entries than EntriesPerRoot must reach a complete sweep by cursor
// resumption across calls, and every intermediate batch must report itself incomplete.
func TestDiscoveryResumesBoundedRootToCompletion(t *testing.T) {
	c, _ := runtimeFixture(t)
	c.EntriesPerRoot = 4
	root := filepath.Join(c.Home, "projects")
	c.DefaultRoots = false
	c.Roots = []capturedaemon.Root{{Agent: "claude", Path: root}}
	const files = 12
	for i := range files {
		runtimeWrite(t, filepath.Join(root, "projects", "p", fmt.Sprintf("%08d-0000-4000-8000-000000000000.jsonl", i)))
	}
	discover, closeDiscovery := runtimeDiscovery(c, "")
	defer func() {
		if err := closeDiscovery(); err != nil {
			t.Error(err)
		}
	}()
	seen := map[string]bool{}
	var complete []bool
	for range 20 {
		batch, err := discover(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for i := range batch.Requests {
			seen[batch.Requests[i].Path] = true
		}
		complete = append(complete, batch.Complete)
		if batch.Complete {
			break
		}
		if len(batch.Coverage) != 1 || batch.Coverage[0].State != "bounded" || batch.Coverage[0].Examined != 4 {
			t.Fatalf("incomplete batch must be visibly bounded: %+v", batch.Coverage)
		}
	}
	if last := complete[len(complete)-1]; !last || len(complete) < 3 {
		t.Fatalf("complete sequence %v", complete)
	}
	for _, ok := range complete[:len(complete)-1] {
		if ok {
			t.Fatalf("complete before the end: %v", complete)
		}
	}
	if len(seen) != files {
		t.Fatalf("discovered %d of %d files over %d calls", len(seen), files, len(complete))
	}
}
