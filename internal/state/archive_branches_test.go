package state

import (
	"errors"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/raw"
)

// Fail writes at different points in one domain, including after earlier projections
// have succeeded. A retry must see the old evidence, not a partially advanced cache.
func TestArchiveBranchesCaptureFailureRollsBackAllProjections(t *testing.T) {
	for _, table := range []string{"archive_records", "uploads", "files", "sessions", "pending_manifests", "archive_applied"} {
		t.Run(table, func(t *testing.T) {
			db := open(t)
			ref := adapters.SessionRef{Agent: "claude", Key: "session"}
			old := &raw.Generation{Identity: raw.Identity{Agent: ref.Agent, SessionKey: ref.Key, Key: "file", Root: "/root"}, Role: string(adapters.RoleMain), SHA256: raw.SHA256([]byte("old")), Size: 3, ObservedAt: 10, Head: map[string]any{"cwd": "/old"}, Sources: []raw.Source{{Path: "/source", Encoding: "plain"}}}
			first := Record{Key: "first", Kind: "file", Stream: "stream", ID: "first", Sequence: 1, Committed: true, Body: []byte("first")}
			check(t, db.CommitFile(first, "file", old, nil))
			check(t, db.Atomic(func() error { return db.ManifestWritten(ref) }))
			before, err := db.Session(ref)
			check(t, err)
			files, err := db.SessionFiles(ref)
			check(t, err)
			// Triggers model a local disk/constraint failure without altering production code.
			check(t, db.Atomic(func() error {
				return db.exec(fmt.Sprintf("CREATE TRIGGER fail_projection BEFORE INSERT ON %s BEGIN SELECT RAISE(ABORT, 'injected projection failure'); END", table))
			}))
			next := *old
			next.SHA256, next.Size, next.ObservedAt = raw.SHA256([]byte("new content")), 11, 20
			next.Head = map[string]any{"cwd": "/new"}
			record := Record{Key: "second", Kind: "file", Stream: "stream", ID: "second", Sequence: 2, Parent: first.ID, Committed: true, Body: []byte("second")}
			capture := &File{Path: "/source", Agent: ref.Agent, Root: "/root", Key: "file", SessionKey: ref.Key, Role: adapters.RoleMain, SHA256: next.SHA256, Size: next.Size, DiskSize: 11, MtimeNS: 123}
			if err := db.CommitCapture(record, "file", &next, old, capture); err == nil {
				t.Fatal("CommitCapture accepted injected projection failure")
			}
			gotRecord, err := db.ArchiveRecord(record.Key)
			check(t, err)
			applied, err := db.ArchiveApplied(record.Key)
			check(t, err)
			if gotRecord != nil || applied {
				t.Fatalf("failed capture evidence = %+v, applied %v, want neither", gotRecord, applied)
			}
			current, err := db.CurrentArchive("file", "stream")
			check(t, err)
			if diff := cmp.Diff(&first, current); diff != "" {
				t.Fatalf("current evidence changed (-want +got): %s", diff)
			}
			upload, err := db.Uploaded("file")
			check(t, err)
			if upload == nil || upload.SHA256 != old.SHA256 || upload.Size != old.Size {
				t.Fatalf("Uploaded = %+v, want old generation", upload)
			}
			gotFiles, err := db.SessionFiles(ref)
			check(t, err)
			if diff := cmp.Diff(files, gotFiles); diff != "" {
				t.Fatalf("files changed (-want +got): %s", diff)
			}
			got, err := db.Session(ref)
			check(t, err)
			if diff := cmp.Diff(before, got); diff != "" {
				t.Fatalf("session changed (-want +got): %s", diff)
			}
			pending, err := db.PendingManifests()
			check(t, err)
			if len(pending) != 0 {
				t.Fatalf("PendingManifests = %+v, want unchanged empty set", pending)
			}
			check(t, db.Atomic(func() error { return db.exec("DROP TRIGGER fail_projection") }))
			check(t, db.CommitCapture(record, "file", &next, old, capture))
			applied, err = db.ArchiveApplied(record.Key)
			check(t, err)
			unchanged, _, err := db.FileUnchanged(capture.Path, "file", capture.DiskSize, capture.MtimeNS)
			check(t, err)
			if !applied || !unchanged {
				t.Fatalf("retry applied = %v, observation unchanged = %v, want both true", applied, unchanged)
			}
		})
	}
}

func TestArchiveBranchesFailedBindingKeepsLegacyEvidence(t *testing.T) {
	db := open(t)
	check(t, db.Atomic(func() error {
		if err := db.RecordUpload("legacy", "old", 3); err != nil {
			return err
		}
		return db.exec("CREATE TRIGGER fail_transition BEFORE DELETE ON uploads BEGIN SELECT RAISE(ABORT, 'injected transition failure'); END")
	}))
	binding := Binding{Schema: 3, Destination: "local:/archive:v3", StoreID: "store", MachineID: "machine"}
	invalid := binding
	invalid.StoreID = ""
	if err := db.Bind(invalid); err == nil {
		t.Fatal("Bind accepted empty store identity")
	}
	if err := db.Bind(binding); err == nil {
		t.Fatal("Bind accepted failed format transition")
	}
	got, err := db.Binding()
	check(t, err)
	if got != nil {
		t.Fatalf("Binding = %+v, want unbound after failure", got)
	}
	upload, err := db.Uploaded("legacy")
	check(t, err)
	if upload == nil || upload.SHA256 != "old" {
		t.Fatalf("Uploaded = %+v, want retained legacy evidence", upload)
	}
	check(t, db.Atomic(func() error { return db.exec("DROP TRIGGER fail_transition") }))
	check(t, db.Bind(binding))
	other := binding
	other.StoreID = "replacement"
	if err := db.Bind(other); err == nil {
		t.Fatal("Bind accepted replacement store")
	}
	got, err = db.Binding()
	check(t, err)
	if diff := cmp.Diff(&binding, got); diff != "" {
		t.Fatalf("binding changed (-want +got): %s", diff)
	}
}

func TestArchiveBranchesNestedAtomicDoesNotCommitOuterWrites(t *testing.T) {
	db := open(t)
	check(t, db.RecordUpload("uncommitted", "sha", 3))
	called := false
	if err := db.Atomic(func() error { called = true; return nil }); err == nil {
		t.Fatal("Atomic accepted unfinished domain")
	}
	if called {
		t.Fatal("Atomic called nested callback")
	}
	check(t, db.Rollback())
	upload, err := db.Uploaded("uncommitted")
	check(t, err)
	if upload != nil {
		t.Fatalf("Uploaded = %+v, want rolled-back outer writes", upload)
	}
}

func TestArchiveBranchesSessionFailureDoesNotImportEvidence(t *testing.T) {
	for _, failure := range []string{"unknownOutcome", "corruptCache", "unserializableInfo", "appliedWrite", "pendingWrite"} {
		t.Run(failure, func(t *testing.T) {
			db := open(t)
			ref := adapters.SessionRef{Agent: "claude", Key: "session"}
			baseline := &Session{Info: map[string]any{"cwd": "/old"}, Snapshots: []SnapshotEntry{}, Launches: []Launch{}, FirstSeen: 10}
			check(t, db.CacheSession(ref, baseline))
			status := SessionManifestCurrent
			next := &Session{Info: map[string]any{"cwd": "/new"}, FirstSeen: 20}
			switch failure {
			case "unknownOutcome":
				status = "unsupported"
			case "corruptCache":
				check(t, db.Atomic(func() error {
					return db.exec("UPDATE sessions SET info='not-json' WHERE agent=? AND session_key=?", ref.Agent, ref.Key)
				}))
			case "unserializableInfo":
				next.Info["invalid"] = make(chan int)
			case "appliedWrite":
				check(t, db.Atomic(func() error {
					return db.exec("CREATE TRIGGER fail_session BEFORE INSERT ON archive_applied BEGIN SELECT RAISE(ABORT, 'injected session failure'); END")
				}))
			case "pendingWrite":
				status = SessionLaunch
				check(t, db.Atomic(func() error {
					return db.exec("CREATE TRIGGER fail_session BEFORE INSERT ON pending_manifests BEGIN SELECT RAISE(ABORT, 'injected session failure'); END")
				}))
			}
			record := Record{Key: "failed-session", Kind: "session", Body: []byte("evidence")}
			if err := db.CommitSession(record, ref, next, status); err == nil {
				t.Fatal("CommitSession accepted invalid or failed projection")
			}
			gotRecord, err := db.ArchiveRecord(record.Key)
			check(t, err)
			applied, err := db.ArchiveApplied(record.Key)
			check(t, err)
			if gotRecord != nil || applied {
				t.Fatalf("failed session record = %+v, applied %v, want neither", gotRecord, applied)
			}
			got, err := db.Session(ref)
			if failure == "corruptCache" {
				if err == nil || got != nil {
					t.Fatalf("Session = %+v, %v, want corrupt cache preserved, not overwritten", got, err)
				}
				return
			}
			check(t, err)
			if diff := cmp.Diff(baseline, got); diff != "" {
				t.Fatalf("failed session projection changed cache (-want +got): %s", diff)
			}
		})
	}
}

func TestArchiveBranchesWalkStopsOnVisitorFailure(t *testing.T) {
	db := open(t)
	for _, key := range []string{"a", "b"} {
		check(t, db.ImportPlan(Record{Key: key, Kind: "file", Stream: "stream", Body: []byte(key)}))
	}
	injected := errors.New("visitor failed")
	calls := 0
	err := db.WalkArchive("file", "stream", func(Record) error { calls++; return injected })
	if !errors.Is(err, injected) || calls != 1 {
		t.Fatalf("WalkArchive = %v, calls %d, want visitor error and one call", err, calls)
	}
	count := 0
	check(t, db.WalkArchive("file", "stream", func(Record) error { count++; return nil }))
	if count != 2 {
		t.Fatalf("WalkArchive retry visited %d, want both immutable records", count)
	}
}
