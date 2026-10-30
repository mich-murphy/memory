package sessioncapture

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestInventoryQuotaCrossesDefaultBarrier(t *testing.T) {
	dir, o, req := setup(t)
	o.MaxInventoryRecords = MaxRuntimeSources + 1
	q := open(t, dir, o)
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	// One transaction seeds valid pending observations, not thousands of fsyncs.
	_, err = q.db.ExecContext(t.Context(), `WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<?) INSERT INTO runtime_sources(session,source,request,observed_at) SELECT 'old',CAST(x AS TEXT),?,1 FROM n`, MaxRuntimeSources, body)
	if err != nil {
		t.Fatal(err)
	}
	_, err = q.db.ExecContext(t.Context(), `WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<?) INSERT INTO runtime_discovery_issues(agent,root,path,code,first_generation,seen_generation) SELECT 'pi','/old','/old/'||x,'unreadable',1,1 FROM n`, MaxDiscoveryRecords)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Hint(t.Context(), req); err != nil {
		t.Fatalf("4097th admission: %v", err)
	}
	issue, _ := discoveryFixture(t)
	recordDiscovery(t, q, DiscoveryBatch{Issues: []DiscoveryIssue{issue}, Complete: true}, nil)
	view, err := ReadRuntime(t.Context(), dir, o.ArchiveID, 0, 10000)
	if err != nil || view.Runtime.Sources != 4097 || view.Runtime.PendingSources != 4097 || view.Runtime.DiscoveryIssueCount != 4097 || len(view.Sources) != 256 || len(view.DiscoveryIssues) != 256 {
		t.Fatalf("truthful bounded view: %+v %v", view.Runtime, err)
	}
	req.SourceID = "overflow"
	if err := q.Hint(t.Context(), req); !errors.Is(err, ErrQuota) {
		t.Fatalf("configured ceiling: %v", err)
	}
}

func TestInventoryQuotaRestartRetainsBackpressureAndWork(t *testing.T) {
	dir, o, req := setup(t)
	o.MaxInventoryRecords = 2
	q, err := Open(t.Context(), dir, o)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if q != nil {
			if err := q.Close(); err != nil {
				t.Error(err)
			}
		}
	}()
	options := runtimeDefaults(RuntimeOptions{})
	for i := range 2 {
		r := req
		r.SourceID = fmt.Sprintf("source-%d", i)
		if err := q.Hint(t.Context(), r); err != nil {
			t.Fatal(err)
		}
	}
	if ok, err := q.captureNext(t.Context(), options); err != nil || !ok {
		t.Fatalf("capture: %v %v", ok, err)
	}
	before, err := q.Status(t.Context())
	if err != nil || len(before) != 1 {
		t.Fatalf("accepted work: %+v %v", before, err)
	}
	req.SourceID = "healthy-new"
	if err := WriteHint(t.Context(), dir, req); err != nil {
		t.Fatal(err)
	}
	if err := q.drainHints(t.Context()); err != nil {
		t.Fatal(err)
	}
	issue, scope := discoveryFixture(t)
	for i := range 3 {
		issue.Path = filepath.Join(issue.RootPath, strconv.Itoa(i))
		scope.Root = issue.Path
		recordDiscovery(t, q, DiscoveryBatch{Issues: []DiscoveryIssue{issue}, Coverage: []DiscoveryScope{scope}, Complete: true}, nil)
	}
	view, err := ReadRuntime(t.Context(), dir, o.ArchiveID, 0, 64)
	if err != nil || view.Runtime.InboxBackpressureHints != 1 || view.Runtime.DiscoveryError != "quota" || view.Runtime.DiscoveryComplete || view.Runtime.DiscoveryIssueCount != 2 || view.Runtime.DiscoveryScopeCount != 2 {
		t.Fatalf("pressure: %+v %v", view.Runtime, err)
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	q = nil
	// Restart at the same ceiling cannot acknowledge or delete blocked work.
	q, err = Open(t.Context(), dir, o)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.drainHints(t.Context()); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "hints"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("lost blocked hint: %d %v", len(entries), err)
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	q = nil
	o.MaxInventoryRecords = 3
	q, err = Open(t.Context(), dir, o)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.drainHints(t.Context()); err != nil {
		t.Fatal(err)
	}
	recordDiscovery(t, q, DiscoveryBatch{Issues: []DiscoveryIssue{issue}, Coverage: []DiscoveryScope{scope}, Complete: true}, nil)
	view, err = ReadRuntime(t.Context(), dir, o.ArchiveID, 0, 64)
	if err != nil || view.Runtime.Sources != 3 || view.Runtime.InboxBackpressureHints != 0 || view.Runtime.DiscoveryError != "" || !view.Runtime.DiscoveryComplete || view.Runtime.DiscoveryIssueCount != 3 || view.Runtime.DiscoveryScopeCount != 3 {
		t.Fatalf("resumed: %+v %v", view.Runtime, err)
	}
	after, err := q.Status(t.Context())
	if err != nil || len(after) != 1 || after[0].PublicationID != before[0].PublicationID {
		t.Fatalf("accepted bytes changed: %+v %v", after, err)
	}
	// Existing pending rows and healthy new source both capture and publish.
	for range 4 {
		if _, err := q.captureNext(t.Context(), options); err != nil {
			t.Fatal(err)
		}
	}
	backend := newBackend(t)
	for range 3 {
		if err := q.deliverDue(t.Context(), backend, req.Identity, options); err != nil {
			t.Fatal(err)
		}
	}
	view, err = ReadRuntime(t.Context(), dir, o.ArchiveID, 0, 64)
	if err != nil || view.Runtime.PendingSources != 0 || view.Runtime.PendingPublications != 0 || view.Runtime.Quarantined != 0 || view.Runtime.Paused {
		t.Fatalf("drain: %+v %v", view.Runtime, err)
	}
	found := false
	for _, s := range view.Sources {
		if s.Request.SourceID == req.SourceID {
			found = true
			if s.State != "captured" || s.CapturedVersion != s.ObservedVersion {
				t.Fatalf("healthy source: %+v", s)
			}
		}
	}
	if !found {
		t.Fatal("healthy source missing")
	}
}

func TestInventoryQuotaOptionsValidation(t *testing.T) {
	for _, limit := range []int{-1, MaxInventoryRecords + 1} {
		dir, o, _ := setup(t)
		o.MaxInventoryRecords = limit
		q, err := Open(t.Context(), dir, o)
		if err == nil {
			if e := q.Close(); e != nil {
				t.Fatal(e)
			}
			t.Fatalf("accepted %d", limit)
		}
	}
}
