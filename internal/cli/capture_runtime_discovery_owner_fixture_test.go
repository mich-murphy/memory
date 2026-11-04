package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/mich-murphy/memory/internal/capturedaemon"
	"github.com/mich-murphy/memory/internal/capturesource"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

// externalPerOwner is the number of Claude external artifacts writeOwnerTree
// creates per owner (file-history, session-env, debug, todos, tasks).
const externalPerOwner = 8

func ownerTestUUID(i int) string { return fmt.Sprintf("%08x-0000-4000-8000-%012x", i+1, i+1) }

func ownerMainPath(c capturedaemon.Config, project string, i int) string {
	return filepath.Join(c.Home, ".claude", "projects", project, ownerTestUUID(i)+".jsonl")
}

// writeOwnerTree writes owners main transcripts plus externalPerOwner external
// artifacts each, with synthetic bodies, and returns the main paths.
func writeOwnerTree(tb testing.TB, c capturedaemon.Config, owners int) []string {
	tb.Helper()
	var mains []string
	for i := range owners {
		uuid := ownerTestUUID(i)
		root := filepath.Join(c.Home, ".claude")
		mains = append(mains, ownerMainPath(c, "p", i))
		mustWrite(tb, mains[i])
		for _, rel := range []string{
			"file-history/" + uuid + "/version", "file-history/" + uuid + "/v2", "file-history/" + uuid + "/v3",
			"session-env/" + uuid + "/env", "session-env/" + uuid + "/e2",
			"debug/" + uuid + ".txt", "todos/" + uuid + "-agent.json", "tasks/" + uuid + "/1.json",
		} {
			mustWrite(tb, filepath.Join(root, rel))
		}
	}
	return mains
}

// recordOwners records main transcripts as owners in the real queue, exactly as
// capture does. A conflicting owner is committed as ambiguous and tolerated.
func recordOwners(tb testing.TB, r runtimeComposition, config string, mains ...string) {
	tb.Helper()
	if err := os.MkdirAll(r.config.Spool, 0o700); err != nil {
		tb.Fatal(err)
	}
	q, err := sessioncapture.Open(tb.Context(), r.config.Spool, r.queueOptions())
	if err != nil {
		tb.Fatal(err)
	}
	opts := r.sourceOptions(config)
	for _, main := range mains {
		m, err := capturesource.Classify(tb.Context(), opts, "claude", main)
		if err != nil {
			tb.Fatal(err)
		}
		if err := q.RecordOwnership(tb.Context(), m); err != nil && !errors.Is(err, sessioncapture.ErrOwnershipAmbiguous) {
			tb.Fatal(err)
		}
	}
	if err := q.Close(); err != nil {
		tb.Fatal(err)
	}
}

func discoverProjection(t testing.TB, opts capturesource.Options) []string {
	t.Helper()
	res, err := capturesource.Discover(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.Cursor != nil {
		if err := res.Cursor.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return discoveryProjection(res)
}

func mustMkdir(tb testing.TB, dir string) {
	tb.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		tb.Fatal(err)
	}
}
