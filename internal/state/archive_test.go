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

func TestPruningForgetsCopiedManifestsOnlyOfRepublishedSessions(t *testing.T) {
	db := open(t)
	republished := adapters.SessionRef{Agent: "claude", Key: "republished"}
	waiting := adapters.SessionRef{Agent: "claude", Key: "waiting"}
	copied := map[adapters.SessionRef]string{republished: "records/manifest/a/commits/1.json", waiting: "records/manifest/b/commits/1.json"}
	streams := map[adapters.SessionRef]string{republished: "a", waiting: "b"}
	for ref, key := range copied {
		check(t, db.CommitSession(Record{Key: key, Kind: "manifest", Stream: streams[ref], Sequence: 1, Committed: true, Body: []byte("copied")}, ref, &Session{FirstSeen: 1}, SessionManifestStale))
		check(t, db.MarkArchiveSeen(key, "scan"))
	}
	check(t, db.CommitSession(Record{Key: "records/session/a/commits/1.json", Kind: "session", Stream: "a", Sequence: 1, Committed: true, Body: []byte("references")}, republished, &Session{FirstSeen: 1}, SessionManifestCurrent))
	check(t, db.PruneCopiedManifests())

	if record, err := db.ArchiveRecord(copied[republished]); record != nil || err != nil {
		t.Fatalf("republished session's copied manifest = %v, %v; want forgotten", record, err)
	}
	if applied, err := db.ArchiveApplied(copied[republished]); applied || err != nil {
		t.Fatalf("republished session's copied manifest applied = %v, %v; want forgotten", applied, err)
	}
	if missing, err := db.MissingArchive("records/manifest/a/", "other"); missing != "" || err != nil {
		t.Fatalf("missing after prune = %q, %v", missing, err)
	}
	if record, err := db.ArchiveRecord(copied[waiting]); record == nil || err != nil {
		t.Fatalf("waiting session's copied manifest = %v, %v; want kept until it is republished", record, err)
	}
}

func TestFileAnchorMovesOnlyWhenSessionOrRoleChanges(t *testing.T) {
	db := open(t)
	gen := func(session string, role adapters.Role) *raw.Generation {
		return &raw.Generation{Identity: raw.Identity{Agent: "claude", Key: "f", SessionKey: session}, Role: string(role), Sources: []raw.Source{{Path: "/f"}}}
	}
	steps := []struct {
		record string
		g      *raw.Generation
		want   string
	}{
		{"capture", gen("s", adapters.RoleMain), "capture"},
		{"append", gen("s", adapters.RoleMain), "capture"},
		{"role", gen("s", adapters.RoleSuperseded), "role"},
		{"move", gen("t", adapters.RoleSuperseded), "move"},
		{"again", gen("t", adapters.RoleSuperseded), "move"},
	}
	var previous *raw.Generation
	for _, step := range steps {
		check(t, db.CommitFile(Record{Key: step.record, Kind: "file", Body: []byte(step.record)}, "f", step.g, previous))
		got, err := db.FileAnchor("f")
		check(t, err)
		if got != step.want {
			t.Fatalf("anchor after %s = %q, want %q", step.record, got, step.want)
		}
		previous = step.g
	}
}
