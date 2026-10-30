package sessioncapture

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mich-murphy/memory/internal/publication"
)

func insertPublicationJob(t *testing.T, q *Queue, m publication.Manifest, state string) {
	t.Helper()
	if err := publication.ValidateManifest(m); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.db.ExecContext(t.Context(), `INSERT INTO jobs(session,sequence,manifest,evidence,state) VALUES(?,?,?,'[]',?)`, m.Identity.BaseKey(), m.Sequence, body, state); err != nil {
		t.Fatal(err)
	}
}

func TestPublicationHeadsSweepAndLatestSuccess(t *testing.T) {
	dir, o, req := setup(t)
	q := open(t, dir, o)
	// Actual accepted and published manual work establishes a valid manifest. SQL
	// cloning changes identities, not source/object closure, for cheap large pages.
	first := capture(t, q, req)
	if err := q.Deliver(t.Context(), newBackend(t), req.Identity); err != nil {
		t.Fatal(err)
	}
	// Use empty-source valid enrichment-only manifests for distinct session clones.
	for n := range 270 {
		m := first
		m.Identity.SessionID = fmt.Sprintf("session-%03d", n)
		m.Sources = nil
		m.Sequence = 3
		m.PublicationID = fmt.Sprintf("enrichment-%03d", n)
		insertPublicationJob(t, q, m, "published")
		m.Sequence = 4
		insertPublicationJob(t, q, m, "pending")
		m.Sequence = 5
		insertPublicationJob(t, q, m, "quarantine")
	}
	page, err := ReadPublicationHeads(t.Context(), dir, o.ArchiveID, "", 256)
	if err != nil || len(page.Heads) != 256 || page.Complete {
		t.Fatalf("page length=%d complete=%v err=%v", len(page.Heads), page.Complete, err)
	}
	second, err := ReadPublicationHeads(t.Context(), dir, o.ArchiveID, page.NextCursor, 256)
	if err != nil || len(second.Heads) != 15 || !second.Complete {
		t.Fatalf("second length=%d complete=%v err=%v", len(second.Heads), second.Complete, err)
	}
	all := append([]PublicationHead{}, page.Heads...)
	all = append(all, second.Heads...)
	for n, head := range all {
		if n > 0 && all[n-1].Identity.BaseKey() >= head.Identity.BaseKey() {
			t.Fatal("unordered heads")
		}
		if head.Identity == req.Identity {
			if head.Sequence != first.Sequence || head.PublicationID != first.PublicationID {
				t.Fatal("manual publication lost")
			}
		} else if head.Sequence != 3 {
			t.Fatal("pending/quarantine hid published head")
		}
	}
	empty, err := ReadPublicationHeads(t.Context(), dir, o.ArchiveID, second.NextCursor, 256)
	if err != nil || !empty.Complete || empty.Heads == nil || len(empty.Heads) != 0 {
		t.Fatal("empty page contract", err)
	}
	body, err := json.Marshal(empty)
	if err != nil || !strings.Contains(string(body), `"heads":[]`) {
		t.Fatal("null heads", err)
	}
	updated := first
	updated.Sequence++
	updated.PublicationID = "new-success"
	insertPublicationJob(t, q, updated, "published")
	repeat, err := ReadPublicationHeads(t.Context(), dir, o.ArchiveID, "", 1)
	if err != nil || repeat.Heads[0].PublicationID != updated.PublicationID {
		t.Fatal("repeated sweep missed earlier update", err)
	}
}

func TestPublicationHeadsReadOnlyBindingAndValidation(t *testing.T) {
	dir, o, req := setup(t)
	q := open(t, dir, o)
	m := capture(t, q, req)
	if err := q.Deliver(t.Context(), newBackend(t), req.Identity); err != nil {
		t.Fatal(err)
	}
	if err := q.Hint(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if _, err := q.claimSource(t.Context()); err != nil {
		t.Fatal(err)
	}
	before, err := ReadRuntime(t.Context(), dir, o.ArchiveID, 0, 256)
	if err != nil {
		t.Fatal(err)
	}
	var changesBefore, changesAfter int
	if err := q.db.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&changesBefore); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := ReadPublicationHeads(t.Context(), dir, o.ArchiveID, "", 1); err != nil {
			t.Fatal(err)
		}
	}
	after, err := ReadRuntime(t.Context(), dir, o.ArchiveID, 0, 256)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("read mutated runtime", err)
	}
	if err := q.db.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&changesAfter); err != nil || changesBefore != changesAfter {
		t.Fatal("read mutated jobs", err)
	}
	for _, tc := range []struct {
		archive, cursor string
		limit           int
	}{
		{"wrong", "", 1},
		{"", "", 1},
		{o.ArchiveID, "garbage", 1},
		{o.ArchiveID, strings.ToUpper(m.Identity.BaseKey()), 1},
		{o.ArchiveID, "", 0},
		{o.ArchiveID, "", 257},
		{o.ArchiveID, (publication.Identity{ArchiveID: "other", MachineID: "m", Agent: "pi", SessionID: "s"}).BaseKey(), 1},
	} {
		if _, err := ReadPublicationHeads(t.Context(), dir, tc.archive, tc.cursor, tc.limit); err == nil {
			t.Fatal("bad arguments accepted")
		}
	}
	for _, mutation := range []string{
		`UPDATE jobs SET manifest=x'7b'`,
		`UPDATE jobs SET manifest=zeroblob(8388609)`,
	} {
		if _, err := q.db.ExecContext(t.Context(), mutation); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadPublicationHeads(t.Context(), dir, o.ArchiveID, "", 1); err == nil {
			t.Fatal("bad manifest accepted")
		}
	}
}

func TestPublicationHeadsRejectManifestIdentityAndUnknownDatabase(t *testing.T) {
	dir, o, req := setup(t)
	q := open(t, dir, o)
	m := capture(t, q, req)
	if err := q.Deliver(t.Context(), newBackend(t), req.Identity); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*publication.Manifest){
		func(m *publication.Manifest) { m.Contract = "unknown" },
		func(m *publication.Manifest) { m.Identity.ArchiveID = "wrong" },
		func(m *publication.Manifest) { m.Identity.SessionID = "wrong" },
		func(m *publication.Manifest) { m.Sequence++ },
	} {
		changed := m
		change(&changed)
		b, err := json.Marshal(changed)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := q.db.ExecContext(t.Context(), "UPDATE jobs SET manifest=?", b); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadPublicationHeads(t.Context(), dir, o.ArchiveID, "", 1); err == nil {
			t.Fatal("invalid selected manifest accepted")
		}
	}
	missing := filepath.Join(t.TempDir(), "missing")
	if _, err := ReadPublicationHeads(t.Context(), missing, o.ArchiveID, "", 1); err == nil {
		t.Fatal("missing database accepted")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("read initialized spool")
	}
	if _, err := q.db.ExecContext(t.Context(), "DROP TABLE jobs"); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPublicationHeads(t.Context(), dir, o.ArchiveID, "", 1); err == nil {
		t.Fatal("unknown database accepted")
	}
}
