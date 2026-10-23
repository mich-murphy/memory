package capturesource

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mich-murphy/memory/internal/sessioncapture"
)

const externalTestUUID = "12345678-1234-1234-1234-123456789abc"

func TestExternalIndexedOwnershipCoordinates(t *testing.T) {
	o := options(t)
	o.Now = func() time.Time { return time.Unix(100, 0) }
	root := Root{"claude", filepath.Join(o.Home, "custom")}
	o.Roots = []Root{root}
	o.OnlyRoots = true
	main := fixture(t, filepath.Join(root.Path, "projects", "slug", externalTestUUID+".jsonl"), "not JSON; do not parse")
	spool := filepath.Join(t.TempDir(), "spool")
	q, err := sessioncapture.Open(t.Context(), spool, sessioncapture.Options{ArchiveID: o.ArchiveID, MaxSpoolBytes: 1 << 20, CheckPolicy: func(context.Context, sessioncapture.Request) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := q.Close(); err != nil {
			t.Error(err)
		}
	}()
	m, err := Classify(t.Context(), o, "claude", main)
	if err != nil {
		t.Fatal(err)
	}
	if m.Optional {
		t.Fatal("primary transcript became optional")
	}
	if err = q.RecordOwnership(t.Context(), m); err != nil {
		t.Fatal(err)
	}
	calls := 0
	o.LookupOwner = func(ctx context.Context, r Root, uuid string) (string, string, error) {
		calls++
		if r != root || uuid != externalTestUUID {
			t.Fatal("bad lookup coordinate", r, uuid)
		}
		return sessioncapture.ReadOwnership(ctx, spool, o.ArchiveID, r.Agent, r.Path, uuid)
	}
	for _, rel := range []string{"file-history/" + externalTestUUID + "/version", "session-env/" + externalTestUUID + "/env", "debug/" + externalTestUUID + ".txt", "todos/" + externalTestUUID + "-agent.json", "tasks/" + externalTestUUID + "/1.json"} {
		path := fixture(t, filepath.Join(root.Path, rel), "malformed raw")
		got, err := Classify(t.Context(), o, "claude", path)
		if err != nil || got.Identity != m.Identity || got.SourceID != "roots/"+strings.Split(m.SourceID, "/")[1]+"/"+rel || got.Role != "artifact" || !got.Optional {
			t.Fatalf("%+v %v", got, err)
		}
		if !strings.Contains(strings.Join(got.Associations, "|"), "adapter-session:") {
			t.Fatal("lost adapter ownership")
		}
	}
	if calls != 5 {
		t.Fatal("not a single lookup per artifact", calls)
	}
	found := discover(t, o)
	for _, want := range found.Requests {
		got, err := Classify(t.Context(), o, "claude", want.Path)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("native/discover mismatch %+v %+v %v", got, want, err)
		}
	}
	// Owner-coordinate exclusion must withhold external bytes too.
	o.Policy = func(_ context.Context, s Scope) error {
		if s.Path == main {
			return errors.New("opt out")
		}
		return nil
	}
	_, err = Classify(t.Context(), o, "claude", filepath.Join(root.Path, "debug", externalTestUUID+".txt"))
	requireExternalCode(t, err, "policy_denied")
}

func requireExternalCode(t *testing.T, err error, code string) {
	t.Helper()
	var gap *ClassificationError
	if !errors.As(err, &gap) || gap.Issue.Code != code {
		t.Fatalf("wanted %s got %v", code, err)
	}
}

func TestExternalPendingAmbiguousDerivedAndPolicy(t *testing.T) {
	o := options(t)
	o.OnlyRoots = true
	root := Root{"claude", filepath.Join(o.Home, ".claude")}
	o.Roots = []Root{root}
	main := fixture(t, filepath.Join(root.Path, "projects", "slug", externalTestUUID+".jsonl"), "raw")
	m, err := Classify(t.Context(), o, "claude", main)
	if err != nil {
		t.Fatal(err)
	}
	legacy := "slug/" + externalTestUUID
	path := fixture(t, filepath.Join(root.Path, "debug", externalTestUUID+".txt"), "raw")
	_, err = Classify(t.Context(), o, "claude", path)
	requireExternalCode(t, err, "ownership_integration_required")
	for _, tc := range []struct {
		err  error
		code string
	}{{sessioncapture.ErrOwnershipPending, "ownership_pending"}, {sessioncapture.ErrOwnershipAmbiguous, "ownership_ambiguous"}, {errors.New("unavailable"), "ownership_pending"}} {
		o.LookupOwner = func(context.Context, Root, string) (string, string, error) { return "", "", tc.err }
		_, err = Classify(t.Context(), o, "claude", path)
		requireExternalCode(t, err, tc.code)
	}
	o.LookupOwner = func(context.Context, Root, string) (string, string, error) { return m.Identity.SessionID, legacy, nil }
	fake := fixture(t, filepath.Join(root.Path, "todos", "fakeguid.json"), "raw")
	_, err = Classify(t.Context(), o, "claude", fake)
	requireExternalCode(t, err, "ownership_pending")
	db := fixture(t, filepath.Join(root.Path, "tasks", externalTestUUID, "state.sqlite"), "not a DB; classify metadata only")
	_, err = Classify(t.Context(), o, "claude", db)
	requireExternalCode(t, err, "sqlite_backup_required")
	o.Derived = true
	got, err := Classify(t.Context(), o, "claude", db)
	if err != nil || got.Transform != "sqlite-backup" || got.Identity != m.Identity || !strings.HasPrefix(got.SourceID, "db/") {
		t.Fatalf("%+v %v", got, err)
	}
	config := fixture(t, filepath.Join(root.Path, "tasks", externalTestUUID, "settings.json"), "malformed sensitive")
	got, err = Classify(t.Context(), o, "claude", config)
	if err != nil || got.Transform != "scrubbed-config" || got.Identity != m.Identity {
		t.Fatalf("%+v %v", got, err)
	}
	o.Policy = func(_ context.Context, s Scope) error {
		if s.Path == path {
			return errors.New("excluded")
		}
		return nil
	}
	_, err = Classify(t.Context(), o, "claude", path)
	requireExternalCode(t, err, "policy_denied")
}
