package syncer

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/raw"
	"github.com/mich-murphy/memory/internal/state"
	"github.com/mich-murphy/memory/internal/store"
)

func seedTombstoneTip(t *testing.T, sy *Syncer, p *publication, stream string) state.Record {
	t.Helper()
	body, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	r := state.Record{Key: "fixture-" + stream, Kind: "file", Stream: stream, ID: raw.SHA256(body), Sequence: 1, Committed: true, Body: body}
	if err := sy.db.Atomic(func() error { return sy.db.RecordArchive(r) }); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestTombstonePagesRejectIrrelevantCorruption(t *testing.T) {
	for _, mode := range []string{"agent", "root", "decode", "emptyStream"} {
		t.Run(mode, func(t *testing.T) {
			sy, _ := branchSyncer(t)
			// Put the failure beyond the first page; none of these streams match
			// this invocation, but every chain and publication must be checked.
			for i := range 65 {
				p := branchPublication("file")
				p.Agent = "pi"
				seedTombstoneTip(t, sy, p, fmt.Sprintf("a-%03d", i))
			}
			p := branchPublication("file")
			p.File.Identity.Root = "/irrelevant"
			if mode == "agent" {
				p.Agent = "pi"
			}
			stream := "z-corrupt"
			if mode == "emptyStream" {
				stream = ""
			}
			r := seedTombstoneTip(t, sy, p, stream)
			want := "missing or conflicting parent"
			if mode == "decode" || mode == "emptyStream" {
				r.Key += "-bad"
				r.ID = "next"
				r.Sequence = 2
				r.Parent = raw.SHA256(r.Body)
				r.Body = []byte(`{"unknown":true}`)
				want = "unknown field"
			} else {
				r.Key += "-bad"
				r.Sequence = 2
				r.Parent = "wrong"
			}
			if err := sy.db.Atomic(func() error { return sy.db.RecordArchive(r) }); err != nil {
				t.Fatal(err)
			}
			err := sy.tombstone(t.Context(), "claude", "/root", nil, nil)
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("tombstone = %v, want %s", err, want)
			}
		})
	}
}

type tombstoneWriteProbe struct {
	store.Store
	probe func()
}

func (b *tombstoneWriteProbe) Put(ctx context.Context, key string, body []byte, meta map[string]string) error {
	b.probe()
	return b.Store.Put(ctx, key, body, meta)
}

func TestTombstonePageClosesBeforeRemoteAndKeepsSourceFences(t *testing.T) {
	sy, bucket := branchSyncer(t)
	if err := sy.beginCheckpoint(t.Context()); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for _, key := range []string{"fenced", "present", "delete"} {
		p := branchPublication("file")
		p.Key = key
		p.File.Identity.Key = key
		p.File.Identity.Root = root
		p.File.Sources = []raw.Source{{Path: filepath.Join(root, key), Encoding: "plain"}}
		seedTombstoneTip(t, sy, p, streamID(p.Agent, key))
	}
	// All file descriptors share this synthetic immutable dependency.
	p := branchPublication("file")
	piece := p.File.Pieces[0]
	if err := bucket.Put(t.Context(), piece.Object, raw.Compress([]byte("transcript\n")), map[string]string{"encoded_sha256": piece.EncodedSHA256, "sha256": piece.SHA256}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	sy.bucket = &tombstoneWriteProbe{Store: bucket, probe: func() {
		calls++
		// Same-connection writes and a separate reopened writer both must work
		// while the remote callback runs: no page cursor/transaction survives.
		if err := sy.db.SetCheckpoint("remote-probe", false); err != nil {
			t.Fatal(err)
		}
		other, err := state.Open(t.Context(), sy.cfg.StateDir)
		if err != nil {
			t.Fatal(err)
		}
		if err := other.SetCheckpoint("reopen-probe", false); err != nil {
			t.Fatal(err)
		}
		if err := other.Close(); err != nil {
			t.Fatal(err)
		}
	}}
	err := sy.tombstone(t.Context(), "claude", root,
		[]adapters.SessionFile{{Key: "present"}},
		[]adapters.SkippedFile{{Path: filepath.Join(root, "fenced"), Reason: "symlink_unresolved"}})
	if err != nil {
		t.Fatal(err)
	}
	if sy.Summary.Deleted != 1 || calls == 0 {
		t.Fatalf("deleted=%d, remote calls=%d", sy.Summary.Deleted, calls)
	}
	for _, key := range []string{"fenced", "present", "delete"} {
		tip, err := sy.db.CurrentArchive("file", streamID("claude", key))
		if err != nil {
			t.Fatal(err)
		}
		want := uint64(1)
		if key == "delete" {
			want = 2
		}
		if tip.Sequence != want {
			t.Fatalf("%s: sequence %d, want %d", key, tip.Sequence, want)
		}
	}
	// A new invocation must see the deletion just published, not re-delete
	// a cached first-generation tip.
	if err := sy.tombstone(t.Context(), "claude", root, []adapters.SessionFile{{Key: "present"}}, []adapters.SkippedFile{{Path: filepath.Join(root, "fenced"), Reason: "symlink_unresolved"}}); err != nil {
		t.Fatal(err)
	}
	if sy.Summary.Deleted != 1 {
		t.Fatal("fresh invocation re-deleted old tip")
	}
}
