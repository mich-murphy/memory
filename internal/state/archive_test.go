package state

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/launch"
	"github.com/mich-murphy/memory/internal/raw"
)

func TestBindingRejectsIncompatibleReuse(t *testing.T) {
	db := open(t)
	binding := Binding{Schema: 3, Destination: "local:/bucket:v3", StoreID: "store", MachineID: "machine"}
	check(t, db.Bind(binding))
	check(t, db.Bind(binding))
	for _, change := range []func(*Binding){func(b *Binding) { b.Schema = 4 }, func(b *Binding) { b.Destination = "local:/other:v3" }, func(b *Binding) { b.StoreID = "replaced" }, func(b *Binding) { b.MachineID = "other" }} {
		other := binding
		change(&other)
		if err := db.Bind(other); err == nil {
			t.Fatal("Bind accepted incompatible reuse")
		}
	}
}

func TestAtomicArchiveRollbackAndIdempotence(t *testing.T) {
	db := open(t)
	record := Record{Key: "commit", Kind: "file", Stream: "s", ID: "a", Sequence: 1, Committed: true, Body: []byte("a")}
	injected := errors.New("injected")
	if err := db.Atomic(func() error {
		if err := db.RecordArchive(record); err != nil {
			return err
		}
		return injected
	}); !errors.Is(err, injected) {
		t.Fatalf("Atomic = %v", err)
	}
	current, err := db.CurrentArchive("file", "s")
	check(t, err)
	if current != nil {
		t.Fatal("failed domain operation committed")
	}
	check(t, db.Atomic(func() error { return db.RecordArchive(record) }))
	check(t, db.Atomic(func() error { return db.RecordArchive(record) }))
	record.Body = []byte("different")
	if err := db.Atomic(func() error { return db.RecordArchive(record) }); err == nil {
		t.Fatal("immutable body changed")
	}
	current, err = db.CurrentArchive("file", "s")
	check(t, err)
	if current == nil || string(current.Body) != "a" {
		t.Fatalf("CurrentArchive = %+v", current)
	}
}

func TestArchiveRejectsDivergentChains(t *testing.T) {
	for _, tc := range []struct {
		name    string
		records []Record
	}{
		{"missingRoot", []Record{{ID: "b", Sequence: 2, Parent: "a"}}},
		{"branches", []Record{{ID: "a", Sequence: 1}, {ID: "b", Sequence: 2, Parent: "a"}, {ID: "c", Sequence: 2, Parent: "a"}}},
		{"wrongParent", []Record{{ID: "a", Sequence: 1}, {ID: "b", Sequence: 2, Parent: "other"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := open(t)
			check(t, db.Atomic(func() error {
				for _, r := range tc.records {
					r.Key = r.ID
					r.Kind = "file"
					r.Stream = "s"
					r.Committed = true
					r.Body = []byte(r.ID)
					if err := db.RecordArchive(r); err != nil {
						return err
					}
				}
				return nil
			}))
			if _, err := db.CurrentArchive("file", "s"); err == nil {
				t.Fatal("CurrentArchive accepted broken chain")
			}
		})
	}
}

func TestCloseRollsBackUncommittedDomain(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(t.Context(), dir)
	check(t, err)
	check(t, db.RecordUpload("k", "sha", 3))
	check(t, db.Close())
	db, err = Open(t.Context(), dir)
	check(t, err)
	defer func() { check(t, db.Close()) }()
	upload, err := db.Uploaded("k")
	check(t, err)
	if upload != nil {
		t.Fatal("Close silently committed uncommitted domain")
	}
}

func TestSessionMetadataUsesCanonicalLaunchOrderAndMainFileEvidence(t *testing.T) {
	db := open(t)
	ref := adapters.SessionRef{Agent: "claude", Key: "session"}
	entries := []Launch{
		{Envelope: launch.Envelope{ID: "z", RecordedAt: 200, Payload: map[string]any{"hook_input": map[string]any{"cwd": "latest-event"}}}, Root: "latest-root"},
		{Envelope: launch.Envelope{ID: "a", RecordedAt: 200, Payload: map[string]any{"cwd": "earlier-tie"}}, Root: "earlier-root"},
		{Envelope: launch.Envelope{ID: "old", RecordedAt: 100, Payload: map[string]any{"cwd": "old-event"}}, Root: "old-root"},
		{Envelope: launch.Envelope{ID: "empty", RecordedAt: 300}},
	}
	sess := &Session{Launches: entries, FirstSeen: 100}
	check(t, db.CommitSession(Record{Key: "launch", Kind: "launch", Body: []byte("launch")}, ref, sess, SessionLaunch))
	got, err := db.Session(ref)
	check(t, err)
	want := map[string]any{"cwd": "latest-event", "source_root": "latest-root"}
	if diff := cmp.Diff(want, got.Info); diff != "" {
		t.Fatalf("launch metadata (-want +got): %s", diff)
	}
	g := &raw.Generation{Identity: raw.Identity{Agent: ref.Agent, SessionKey: ref.Key, Key: "main", Root: "main-root"}, Role: string(adapters.RoleMain), Head: map[string]any{"cwd": "current-main", "model": "observed-model"}, ObservedAt: 400, Sources: []raw.Source{{Path: "/main"}}}
	check(t, db.CommitFile(Record{Key: "main-commit", Kind: "file", Body: []byte("main")}, "main", g, nil))
	// An older manifest carries stale Info despite current independently committed evidence.
	stale := &Session{Info: map[string]any{"cwd": "stale-manifest"}, Launches: entries, FirstSeen: 100}
	check(t, db.CommitSession(Record{Key: "manifest", Kind: "session", Body: []byte("manifest")}, ref, stale, SessionManifestStale))
	got, err = db.Session(ref)
	check(t, err)
	if got.Info["cwd"] != "current-main" || got.Info["model"] != "observed-model" || got.Info["source_root"] != "main-root" {
		t.Fatalf("stale manifest metadata = %+v, want current main-file observation", got.Info)
	}
	// A late launch also cannot replace metadata observed in the current main file.
	got.Launches = append(got.Launches, Launch{Envelope: launch.Envelope{ID: "late", RecordedAt: 500, Payload: map[string]any{"cwd": "late-launch"}}})
	check(t, db.CommitSession(Record{Key: "late-launch", Kind: "launch", Body: []byte("late")}, ref, got, SessionLaunch))
	got, err = db.Session(ref)
	check(t, err)
	if got.Info["cwd"] != "current-main" {
		t.Fatalf("late launch metadata = %+v, want current main-file observation", got.Info)
	}
}

func TestOpeningForgetsRetiredManifestsAndRepublishesTheirSessions(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(t.Context(), dir)
	check(t, err)
	ref := adapters.SessionRef{Agent: "claude", Key: "session"}
	key := "v3/machines/m/records/manifest/stream/commits/00000000000000000001-x.json"
	check(t, db.CommitSession(Record{Key: key, Kind: "manifest", Body: []byte("copied generations")}, ref, &Session{FirstSeen: 1}, SessionManifestCurrent))
	check(t, db.MarkArchiveSeen(key, "scan"))
	check(t, db.Close())

	db, err = Open(t.Context(), dir)
	check(t, err)
	t.Cleanup(func() { _ = db.Close() })
	if record, err := db.ArchiveRecord(key); record != nil || err != nil {
		t.Fatalf("retired manifest = %v, %v; want forgotten", record, err)
	}
	if applied, err := db.ArchiveApplied(key); applied || err != nil {
		t.Fatalf("retired manifest applied = %v, %v; want forgotten", applied, err)
	}
	pending, err := db.PendingManifests()
	check(t, err)
	if diff := cmp.Diff([]adapters.SessionRef{ref}, pending); diff != "" {
		t.Fatalf("pending manifests (-want +got): %s", diff)
	}
	check(t, db.Atomic(func() error { return db.ManifestWritten(ref) }))
	check(t, db.Close())

	db, err = Open(t.Context(), dir)
	check(t, err)
	if pending, err := db.PendingManifests(); len(pending) != 0 || err != nil {
		t.Fatalf("reopening republished again: %v, %v", pending, err)
	}
	check(t, db.Close())
}
