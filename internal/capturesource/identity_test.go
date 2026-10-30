package capturesource

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPiCustomChildrenAndCodexKnownRename(t *testing.T) {
	o := options(t)
	o.OnlyRoots = true
	parent := "2026-01-01T00-00-00Z_11111111-1111-1111-1111-111111111111"
	for _, root := range []string{filepath.Join(o.Home, ".pi", "agent"), filepath.Join(o.Home, "pi-custom")} {
		o.Roots = append(o.Roots, Root{"pi", root})
		fixture(t, filepath.Join(root, "sessions", "--project--", parent+".jsonl"), "partial{")
		fixture(t, filepath.Join(root, "sessions", "--project--", parent, "tasks", "child.jsonl"), "bad json")
	}
	r := discover(t, o)
	if len(r.Requests) != 4 {
		t.Fatal(r)
	}
	owners := map[string]int{}
	for _, q := range r.Requests {
		owners[q.Identity.SessionID]++
	}
	if len(owners) != 2 {
		t.Fatal(owners)
	}
	for _, n := range owners {
		if n != 2 {
			t.Fatal(owners)
		}
	}
	c := options(t)
	c.OnlyRoots = true
	root := filepath.Join(c.Home, ".codex")
	c.Roots = []Root{{"codex", root}}
	name := "rollout-2026-01-01T00-00-00-11111111-1111-1111-1111-111111111111.jsonl"
	source := fixture(t, filepath.Join(root, "sessions", "2026", name), "raw")
	first := discover(t, c)
	if len(first.Requests) != 1 {
		t.Fatal(first)
	}
	// Independent observations, not a content-based merge: the adapter's filename
	// identity is stable when the producer moves a rollout to archived_sessions.
	dest := filepath.Join(root, "archived_sessions", name)
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(source, dest); err != nil {
		t.Fatal(err)
	}
	second := discover(t, c)
	if len(second.Requests) != 1 {
		t.Fatal(second)
	}
	for _, q := range second.Requests {
		if q.Path != source && q.Path != dest {
			t.Fatal(q.Path)
		}
		if q.SourceID != first.Requests[0].SourceID || q.Identity != first.Requests[0].Identity {
			t.Fatal("known rename identity changed")
		}
	}
}
