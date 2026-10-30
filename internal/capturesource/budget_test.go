package capturesource

import (
	"fmt"
	"path/filepath"
	"testing"
)

func TestGlobalEntryBudgetAllocation(t *testing.T) {
	for _, tc := range []struct{ requested, roots, want int }{
		{4096, 0, 4096},
		{4096, 1, 4096},
		{4096, 2, 2048},
		{4096, 6, 682},
		{4096, 128, 32},
		{256, 128, 32},
		{3, 6, 3},
	} {
		got := effectiveEntryBudget(tc.requested, tc.roots)
		if got != tc.want || got > tc.requested || got*tc.roots > 4096 {
			t.Fatalf("%+v: got %d", tc, got)
		}
	}
	// Exhaustively check every supported inventory size without thousands of
	// filesystem fixtures: each walker consumes at most its allocated entry slice.
	for roots := 1; roots <= 128; roots++ {
		for _, requested := range []int{1, 3, 256, 4096} {
			got := effectiveEntryBudget(requested, roots)
			if got < 1 || got > requested || got*roots > 4096 {
				t.Fatalf("roots=%d requested=%d budget=%d", roots, requested, got)
			}
		}
	}
}

func TestFairEntrySlicesResumeAllRoots(t *testing.T) {
	o := options(t)
	o.OnlyRoots = true
	o.EntriesPerRoot = 2
	for i := range 6 {
		root := filepath.Join(o.Home, fmt.Sprintf("root-%d", i))
		o.Roots = append(o.Roots, Root{"codex", root})
		for j := range 3 {
			fixture(t, filepath.Join(root, fmt.Sprintf("file-%d.txt", j)), "raw")
		}
	}
	first := discover(t, o)
	if len(first.Requests) != 12 || len(first.Coverage) != 6 {
		t.Fatal(first)
	}
	for _, c := range first.Coverage {
		if c.Examined != effectiveEntryBudget(o.EntriesPerRoot, len(o.Roots)) || c.State != "bounded" {
			t.Fatal(c)
		}
	}
	o.Cursor = first.Cursor
	second := discover(t, o)
	if second.Cursor != first.Cursor || len(second.Requests) != 6 {
		t.Fatal(second)
	}
	for _, c := range second.Coverage {
		if c.Examined != 1 || c.State != "observed_complete" {
			t.Fatal(c)
		}
	}
	seen := map[string]bool{}
	for _, batch := range []Result{first, second} {
		for _, q := range batch.Requests {
			if seen[q.Path] {
				t.Fatal("duplicate after resume", q.Path)
			}
			seen[q.Path] = true
		}
	}
	if len(seen) != 18 {
		t.Fatal(len(seen))
	}
}
