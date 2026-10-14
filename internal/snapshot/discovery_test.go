package snapshot

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/exclusion"
	"github.com/mich-murphy/memory/internal/testenv"
)

func TestSnapshotReportsUnreadableNonregularAndBudgetLimitedSources(t *testing.T) {
	dir := t.TempDir()
	c := newCollected("pi")
	path := filepath.Join(dir, "source.md")
	if err := os.WriteFile(path, []byte("contents"), 0o600); err != nil {
		t.Fatal(err)
	}
	c.budget.held = maxSnapshotSize - 1
	if c.add("user", path, "source.md") != nil || c.skipped[0].Reason != "memory_limit" {
		t.Fatalf("budget = %+v", c.skipped)
	}
	c = newCollected("pi")
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	if c.add("user", path, "source.md") != nil || c.skipped[0].Reason != "read_error" {
		t.Fatalf("read = %+v", c.skipped)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(dir, "fifo.md")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	if c.add("user", fifo, "fifo.md") != nil || c.skipped[len(c.skipped)-1].Reason != "non_regular" {
		t.Fatalf("fifo = %+v", c.skipped)
	}
	c.add("user", filepath.Join(dir, "missing"), "missing")
	if c.skipped[len(c.skipped)-1].Reason != "resolve_error" {
		t.Fatalf("stat = %+v", c.skipped)
	}
	body, err := readBounded(path, 2)
	if err != nil || len(body) != 3 {
		t.Fatalf("bounded = %q %v", body, err)
	}
	if _, err := readBounded(fifo, 2); err == nil {
		t.Fatal("FIFO read accepted")
	}
	if _, err := readBounded(filepath.Join(dir, "missing"), 2); err == nil {
		t.Fatal("missing read accepted")
	}
}

func TestSnapshotDiscoveryReportsWalkLimitsAndPartialCompleteness(t *testing.T) {
	dir := t.TempDir()
	c := newCollected("pi")
	for i := range 3 {
		if err := os.WriteFile(filepath.Join(dir, strings.Repeat("x", i+1)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	c.listDir("user", dir, true, 1)
	if len(c.skipped) != 1 || c.skipped[0].Reason != "walk_limit" {
		t.Fatalf("walk = %+v", c.skipped)
	}
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	c.walkFiles("user", dir, true)
	if c.skipped[len(c.skipped)-1].Reason != "walk_error" {
		t.Fatalf("walk = %+v", c.skipped)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	a := adapters.NewPi(t.TempDir())
	root := a.CanonicalRoot()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing", filepath.Join(root, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	snap, err := Build(a, root, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if snap.Complete || snap.Harness.Complete || len(snap.Harness.Skipped) == 0 {
		t.Fatalf("snapshot complete = %+v", snap.Harness)
	}
}

func TestAdapterSourceIssuesMakeSnapshotPartial(t *testing.T) {
	e := testenv.New(t)
	a := adapters.NewClaude(e.Home)
	testenv.Write(t, filepath.Join(e.Home, ".claude.json"), `{"projects":{"/p":{"mcpServers":{},"\u006dcpServers":{}}}}`)
	testenv.Write(t, filepath.Join(e.Claude, "plugins", "installed_plugins.json"), `{"plugins":[]}`)
	snap, err := Build(a, e.Claude, e.Project, testenv.ClaudeKey)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Complete || snap.Harness.Complete {
		t.Fatal("adapter source failure disappeared as complete")
	}
	var user, plugin bool
	for _, skipped := range snap.Harness.Skipped {
		user = user || skipped.Scope == "user" && strings.Contains(skipped.Path, ".claude.json")
		plugin = plugin || skipped.Scope == "plugin"
	}
	if !user || !plugin {
		t.Fatalf("source issues = %+v", snap.Harness.Skipped)
	}
	for _, file := range snap.Harness.Files {
		if file.Path == ".claude.json#mcp" {
			t.Fatal("duplicate source entry retained")
		}
	}
}

func TestOptedOutRootWithholdsSharedGeneratedSnapshotMemory(t *testing.T) {
	e := testenv.New(t)
	a := adapters.NewClaude(e.Home)
	path := filepath.Join(e.ClaudeSlug, testenv.ClaudeSID+".jsonl")
	if _, err := exclusion.RecordPath(e.State, a, path); err != nil {
		t.Fatal(err)
	}
	testenv.Write(t, filepath.Join(e.Claude, "agent-memory", "private.md"), "excluded session content")
	snap, err := Build(a, e.Claude, e.Project, testenv.ClaudeKey)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Complete || snap.Memory == nil || snap.Memory.Complete || len(snap.Memory.Files) != 0 {
		t.Fatalf("shared memory = %+v", snap.Memory)
	}
	for _, file := range snap.Harness.Files {
		if file.Scope == "user" {
			t.Fatalf("shared user entry retained: %+v", file)
		}
	}
	for _, body := range snap.Blobs {
		if strings.Contains(string(body), "excluded session content") {
			t.Fatal("private shared memory retained")
		}
	}
	// A different allowed session under the same root must not export its sibling's shared
	// generated memory either.
	allowed, err := Build(a, e.Claude, e.Project, "other/allowed")
	if err != nil {
		t.Fatal(err)
	}
	if allowed.Memory == nil || len(allowed.Memory.Files) != 0 {
		t.Fatal("allowed sibling exported shared memory")
	}
}

func TestHarnessAndMemoryShareAggregateRetainedByteBudget(t *testing.T) {
	e := testenv.New(t)
	a := adapters.NewClaude(e.Home)
	chunk := strings.Repeat("x", 4<<20)
	for i := range 17 {
		testenv.Write(t, filepath.Join(e.Claude, "skills", "budget", fmt.Sprintf("file%d.md", i)), chunk)
	}
	testenv.Write(t, filepath.Join(e.Claude, "agent-memory", "budget.md"), chunk)
	snap, err := Build(a, e.Claude, e.Project, testenv.ClaudeKey)
	if err != nil {
		t.Fatal(err)
	}
	held := 0
	for _, body := range snap.Blobs {
		held += len(body)
	}
	if held > maxSnapshotSize || snap.Complete {
		t.Fatalf("retained = %d, complete = %v", held, snap.Complete)
	}
	found := false
	for _, set := range []*Set{snap.Harness, snap.Memory} {
		if set == nil {
			continue
		}
		for _, skipped := range set.Skipped {
			found = found || skipped.Reason == "memory_limit"
		}
	}
	if !found {
		t.Fatal("aggregate budget did not report skipped entries")
	}
}

func TestSnapshotBoundsDiagnosticsAndWalksAcrossScopes(t *testing.T) {
	c := newCollected("pi")
	for range maxSkippedFiles + 5 {
		c.skip("user", "missing", "stat_error", 0)
	}
	if len(c.skipped) != maxSkippedFiles+1 || c.skipped[maxSkippedFiles].Reason != "report_limit" || c.skipped[maxSkippedFiles].Count != 5 {
		t.Fatalf("bounded skipped count = %d, last = %+v", len(c.skipped), c.skipped[len(c.skipped)-1])
	}
	c = newCollected("pi")
	c.budget.walked = maxWalkDirs
	if got := c.walkFiles("user", t.TempDir(), true); len(got) != 0 || len(c.skipped) != 1 || c.skipped[0].Reason != "walk_limit" {
		t.Fatalf("aggregate walk = %v %+v", got, c.skipped)
	}
}
