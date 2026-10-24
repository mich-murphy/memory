package sessioncapture

import (
	"encoding/json"
	"testing"

	"github.com/mich-murphy/memory/internal/publication"
)

func TestPublicationHeadsFullIdentityAndSelectedSuccess(t *testing.T) {
	dir, o, req := setup(t)
	q := open(t, dir, o)
	base := capture(t, q, req)
	// Different machines and agents can use the same root-qualified session ID.
	// They must never collapse into one head or use a newer unsuccessful update.
	for n, id := range []publication.Identity{
		{ArchiveID: o.ArchiveID, MachineID: "a", Agent: "pi", SessionID: "same"},
		{ArchiveID: o.ArchiveID, MachineID: "b", Agent: "pi", SessionID: "same"},
		{ArchiveID: o.ArchiveID, MachineID: "b", Agent: "claude", SessionID: "same"},
	} {
		m := base
		m.Identity = id
		m.Sources = nil
		m.Sequence = uint64(n + 1)
		m.PublicationID = "old"
		insertPublicationJob(t, q, m, "published")
		m.Sequence++
		m.PublicationID = "selected"
		insertPublicationJob(t, q, m, "published")
		m.Sequence++
		m.PublicationID = "failed"
		insertPublicationJob(t, q, m, "quarantine")
	}
	page, err := ReadPublicationHeads(t.Context(), dir, o.ArchiveID, "", 256)
	if err != nil || len(page.Heads) != 3 || !page.Complete {
		t.Fatal("full identities collapsed", err)
	}
	for _, h := range page.Heads {
		if h.PublicationID != "selected" {
			t.Fatal("wrong successful sequence selected")
		}
	}
	// Status must be an exact content-free wire shape, not a manifest serializer.
	b, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire) != 5 {
		t.Fatal("unexpected wire fields")
	}
	for _, key := range []string{"contract", "archive_id", "heads", "next_cursor", "complete"} {
		if _, ok := wire[key]; !ok {
			t.Fatal("missing wire field", key)
		}
	}
}
