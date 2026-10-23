package sessioncapture

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func discoveryFixture(t *testing.T) (DiscoveryIssue, DiscoveryScope) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "root")
	return DiscoveryIssue{RootAgent: "pi", RootPath: root, Path: filepath.Join(root, "A"), Code: "source_unreadable"}, DiscoveryScope{Agent: "pi", Root: root, State: "bounded", Examined: 1, Enrichment: "not_integrated"}
}

func recordDiscovery(t *testing.T, q *Queue, batch DiscoveryBatch, cause error) {
	t.Helper()
	if err := q.prepareDiscoverySweep(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := q.saveDiscoveryBatch(t.Context(), batch, cause); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeDiscoveryIncrementalSweepAndResolution(t *testing.T) {
	dir, o, req := setup(t)
	q := open(t, dir, o)
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	issue, scope := discoveryFixture(t)
	recordDiscovery(t, q, DiscoveryBatch{Issues: []DiscoveryIssue{issue}, Coverage: []DiscoveryScope{scope}}, nil)
	// B's healthy slice is not evidence that A's earlier error disappeared.
	options := runtimeDefaults(RuntimeOptions{DiscoverBatch: func(context.Context) (DiscoveryBatch, error) { return DiscoveryBatch{Requests: []Request{req}}, nil }})
	if err := q.discoverOnce(t.Context(), options); err != nil {
		t.Fatal(err)
	}
	status, err := q.RuntimeStatus(t.Context())
	if err != nil || status.ActiveDiscoveryIssues != 1 || status.DiscoveryComplete || status.CurrentSweep != 1 {
		t.Fatalf("%+v %v", status, err)
	}
	sources, err := q.WorkStatus(t.Context(), 0, 64)
	if err != nil || len(sources) != 1 {
		t.Fatalf("healthy B not admitted: %+v %v", sources, err)
	}
	// Completing this sweep still retains A, which WAS seen in this sweep.
	scope.State = "partial"
	recordDiscovery(t, q, DiscoveryBatch{Complete: true, Coverage: []DiscoveryScope{scope}}, nil)
	status, err = q.RuntimeStatus(t.Context())
	if err != nil || !status.DiscoveryComplete || status.CompletedSweep != 1 || status.ActiveDiscoveryIssues != 1 || status.CompletedAtNS == 0 || status.Coverage != "partial" {
		t.Fatalf("%+v %v", status, err)
	}
	// Only a later FULL sweep can prove A's issue no longer occurs.
	scope.State = "bounded"
	recordDiscovery(t, q, DiscoveryBatch{Coverage: []DiscoveryScope{scope}}, nil)
	status, err = q.RuntimeStatus(t.Context())
	if err != nil || status.CurrentSweep != 2 || status.DiscoveryComplete || status.ActiveDiscoveryIssues != 1 {
		t.Fatalf("%+v %v", status, err)
	}
	scope.State = "observed_complete"
	recordDiscovery(t, q, DiscoveryBatch{Complete: true, Coverage: []DiscoveryScope{scope}}, nil)
	issues, err := q.DiscoveryIssues(t.Context(), 0, 64)
	if err != nil || len(issues) != 1 || issues[0].Active || issues[0].SeenGeneration != 1 || issues[0].ResolvedGeneration != 2 {
		t.Fatalf("%+v %v", issues, err)
	}
	// Reopening the same coordinate reuses its ID and retains resolution history.
	recordDiscovery(t, q, DiscoveryBatch{Issues: []DiscoveryIssue{issue}}, nil)
	reopened, err := q.DiscoveryIssues(t.Context(), 0, 64)
	if err != nil || len(reopened) != 1 || !reopened[0].Active || reopened[0].ID != issues[0].ID || reopened[0].ResolvedGeneration != 2 || reopened[0].FirstGeneration != 1 || reopened[0].SeenGeneration != 3 {
		t.Fatalf("%+v %v", reopened, err)
	}
}

func TestRuntimeDiscoveryRestartPreservesGapAndReadOnlyView(t *testing.T) {
	dir, o, _ := setup(t)
	q, err := Open(t.Context(), dir, o)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	issue, scope := discoveryFixture(t)
	recordDiscovery(t, q, DiscoveryBatch{Issues: []DiscoveryIssue{issue}, Coverage: []DiscoveryScope{scope}}, nil)
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	q = open(t, dir, o)
	// Run's startup operation never resolves the interrupted sweep.
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := q.startDiscoverySweep(t.Context()); err != nil {
		t.Fatal(err)
	}
	view, err := ReadRuntime(t.Context(), dir, o.ArchiveID, 0, 1)
	if err != nil || view.Runtime.CurrentSweep != 2 || view.Runtime.DiscoveryComplete || view.Runtime.ActiveDiscoveryIssues != 1 || len(view.DiscoveryIssues) != 1 || !view.DiscoveryIssues[0].Active || len(view.DiscoveryScopes) != 1 || view.DiscoveryScopes[0].SeenGeneration != 1 {
		t.Fatalf("%+v %v", view, err)
	}
	empty, err := ReadRuntime(t.Context(), dir, o.ArchiveID, view.DiscoveryIssues[0].ID, 1)
	if err != nil || len(empty.DiscoveryIssues) != 0 {
		t.Fatalf("%+v %v", empty, err)
	}
	recordDiscovery(t, q, DiscoveryBatch{Complete: true}, nil)
	view, err = ReadRuntime(t.Context(), dir, o.ArchiveID, 0, 64)
	if err != nil || view.Runtime.ActiveDiscoveryIssues != 0 || view.DiscoveryScopes[0].Active || view.DiscoveryIssues[0].Active || view.Runtime.CompletedSweep != 2 {
		t.Fatalf("%+v %v", view, err)
	}
}

func TestRuntimeDiscoveryInvalidMetadataCannotResolveOrBlockHealthyRequests(t *testing.T) {
	dir, o, req := setup(t)
	q := open(t, dir, o)
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	issue, _ := discoveryFixture(t)
	recordDiscovery(t, q, DiscoveryBatch{Issues: []DiscoveryIssue{issue}, Complete: true}, nil)
	bad := issue
	bad.Code = "private raw error must not persist"
	options := runtimeDefaults(RuntimeOptions{DiscoverBatch: func(context.Context) (DiscoveryBatch, error) {
		return DiscoveryBatch{Requests: []Request{{}, req}, Issues: []DiscoveryIssue{bad}, Complete: true}, nil
	}})
	if err := q.discoverOnce(t.Context(), options); err != nil {
		t.Fatal(err)
	}
	status, err := q.RuntimeStatus(t.Context())
	if err != nil || status.DiscoveryError != "invalid-metadata" || status.DiscoveryComplete || status.ActiveDiscoveryIssues != 1 || status.CompletedSweep != 1 {
		t.Fatalf("%+v %v", status, err)
	}
	issues, err := q.DiscoveryIssues(t.Context(), 0, 64)
	if err != nil || len(issues) != 1 || issues[0].Code != issue.Code {
		t.Fatalf("%+v %v", issues, err)
	}
	sources, err := q.WorkStatus(t.Context(), 0, 64)
	if err != nil || len(sources) != 1 {
		t.Fatalf("healthy request lost %+v %v", sources, err)
	}
	// A clean partial batch cannot clear an earlier error in its generation.
	recordDiscovery(t, q, DiscoveryBatch{}, errors.New("private backend text"))
	recordDiscovery(t, q, DiscoveryBatch{Complete: true}, nil)
	status, err = q.RuntimeStatus(t.Context())
	if err != nil || status.DiscoveryError != "transient" || status.ActiveDiscoveryIssues != 1 || status.DiscoveryComplete {
		t.Fatalf("%+v %v", status, err)
	}
	// Subsequent fully healthy traversal is the only resolution boundary.
	recordDiscovery(t, q, DiscoveryBatch{Complete: true}, nil)
	status, err = q.RuntimeStatus(t.Context())
	if err != nil || status.DiscoveryError != "" || status.ActiveDiscoveryIssues != 0 || !status.DiscoveryComplete {
		t.Fatalf("%+v %v", status, err)
	}
}

func TestRuntimeDiscoveryLimitsCoalesceWithoutGC(t *testing.T) {
	dir, o, req := setup(t)
	q := open(t, dir, o)
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	issue, scope := discoveryFixture(t)
	recordDiscovery(t, q, DiscoveryBatch{Issues: []DiscoveryIssue{issue}, Coverage: []DiscoveryScope{scope}}, nil)
	_, err := q.db.ExecContext(t.Context(), `WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<?) INSERT INTO runtime_discovery_issues(agent,root,path,code,first_generation,seen_generation,active) SELECT 'pi','/old','/old/'||x,'unreadable',1,1,0 FROM n`, MaxDiscoveryRecords-1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = q.db.ExecContext(t.Context(), `WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<?) INSERT INTO runtime_discovery_scopes(agent,root,state,examined,enrichment,seen_generation,active) SELECT 'pi','/old/'||x,'partial',0,'not_integrated',1,0 FROM n`, MaxDiscoveryRecords-1)
	if err != nil {
		t.Fatal(err)
	}
	recordDiscovery(t, q, DiscoveryBatch{Issues: []DiscoveryIssue{issue}, Coverage: []DiscoveryScope{scope}}, nil)
	status, err := q.RuntimeStatus(t.Context())
	if err != nil || status.DiscoveryError != "" || status.DiscoveryIssueCount != MaxDiscoveryRecords || status.DiscoveryScopeCount != MaxDiscoveryRecords {
		t.Fatalf("%+v %v", status, err)
	}
	otherIssue, otherScope := discoveryFixture(t)
	options := runtimeDefaults(RuntimeOptions{DiscoverBatch: func(context.Context) (DiscoveryBatch, error) {
		return DiscoveryBatch{Requests: []Request{req}, Issues: []DiscoveryIssue{otherIssue}, Coverage: []DiscoveryScope{otherScope}, Complete: true}, nil
	}})
	if err := q.discoverOnce(t.Context(), options); err != nil {
		t.Fatal(err)
	}
	status, err = q.RuntimeStatus(t.Context())
	if err != nil || status.DiscoveryError != "quota" || status.DiscoveryComplete || status.DiscoveryIssueCount != MaxDiscoveryRecords || status.DiscoveryScopeCount != MaxDiscoveryRecords {
		t.Fatalf("%+v %v", status, err)
	}
	sources, err := q.WorkStatus(t.Context(), 0, 64)
	if err != nil || len(sources) != 1 {
		t.Fatalf("%+v %v", sources, err)
	}
	// Capacity is still consumed by resolved history: completion never performs GC.
	recordDiscovery(t, q, DiscoveryBatch{Complete: true}, nil)
	status, err = q.RuntimeStatus(t.Context())
	if err != nil || status.DiscoveryIssueCount != MaxDiscoveryRecords || status.DiscoveryScopeCount != MaxDiscoveryRecords || status.ActiveDiscoveryIssues != 0 {
		t.Fatalf("%+v %v", status, err)
	}
}

func TestRuntimeDiscoveryLegacyNeverResolvesAndCallbacksExclusive(t *testing.T) {
	dir, o, _ := setup(t)
	q := open(t, dir, o)
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	issue, _ := discoveryFixture(t)
	recordDiscovery(t, q, DiscoveryBatch{Issues: []DiscoveryIssue{issue}}, nil)
	options := runtimeDefaults(RuntimeOptions{Discover: func(context.Context) ([]Request, error) { return nil, nil }})
	if err := q.discoverOnce(t.Context(), options); err != nil {
		t.Fatal(err)
	}
	status, err := q.RuntimeStatus(t.Context())
	if err != nil || status.ActiveDiscoveryIssues != 1 || status.DiscoveryComplete {
		t.Fatalf("%+v %v", status, err)
	}
	options.DiscoverBatch = func(context.Context) (DiscoveryBatch, error) { return DiscoveryBatch{Complete: true}, nil }
	if err := q.Run(t.Context(), nil, options); err == nil {
		t.Fatal("ambiguous discovery callbacks accepted")
	}
}

func TestRuntimeRunStartsNewDiscoveryGeneration(t *testing.T) {
	dir, o, _ := setup(t)
	q := open(t, dir, o)
	backend := newBackend(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- q.Run(ctx, backend, RuntimeOptions{PollInterval: time.Hour, DiscoverBatch: func(context.Context) (DiscoveryBatch, error) { return DiscoveryBatch{Complete: true}, nil }})
	}()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		status, err := q.RuntimeStatus(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if status.DiscoveryComplete && status.CompletedSweep == 1 {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("batch callback not persisted")
		case <-tick.C:
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeDiscoveryMalformedInboxStatusIsBoundedAndReadOnly(t *testing.T) {
	dir, o, req := setup(t)
	q := open(t, dir, o)
	if err := WriteHint(t.Context(), dir, req); err != nil {
		t.Fatal(err)
	}
	malformed := filepath.Join(dir, "hints", fmt.Sprintf("%064d.json", 42))
	unknown := filepath.Join(dir, "hints", "unknown-entry")
	for _, path := range []string{malformed, unknown} {
		if err := os.WriteFile(path, []byte("private invalid data"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := q.drainHints(t.Context()); err != nil {
		t.Fatal(err)
	}
	view, err := ReadRuntime(t.Context(), dir, o.ArchiveID, 0, 64)
	if err != nil || view.Runtime.RejectedInboxHints != 2 || view.Runtime.InboxTruncated || view.Runtime.InboxBackpressureHints != 0 || view.Runtime.Coverage != "partial" || len(view.Sources) != 1 {
		t.Fatalf("%+v %v", view, err)
	}
	// Inspection never removes, acknowledges, or copies the unsafe body into DB.
	for _, path := range []string{malformed, unknown} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	if err := q.drainHints(t.Context()); err != nil {
		t.Fatal(err)
	}
	status, err := q.RuntimeStatus(t.Context())
	if err != nil || status.RejectedInboxHints != 0 {
		t.Fatalf("%+v %v", status, err)
	}
	// A hostile overfull directory cannot cause an unbounded allocation or a green
	// count: numbers are explicitly flagged as a lower bound for this pass.
	for n := range MaxInboxHints + 2 {
		if err := os.WriteFile(filepath.Join(dir, "hints", fmt.Sprintf("junk-%d", n)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := q.drainHints(t.Context()); err != nil {
		t.Fatal(err)
	}
	status, err = q.RuntimeStatus(t.Context())
	if err != nil || status.RejectedInboxHints != MaxInboxHints+1 || !status.InboxTruncated {
		t.Fatalf("%+v %v", status, err)
	}
}

func TestRuntimeDiscoveryMetadataValidationAndBatchBound(t *testing.T) {
	for _, sample := range []string{"", "not safe", "newline\n", "π", "too-long-" + string(make([]byte, 65))} {
		if discoveryToken(sample, false) {
			t.Fatalf("unsafe code %q", sample)
		}
	}
	if !discoveryToken("", true) || discoveryPath("relative") || discoveryPath("/unsafe\n") || discoveryPath("/invalid\xff") {
		t.Fatal("unsafe coordinates accepted")
	}
	dir, o, _ := setup(t)
	q := open(t, dir, o)
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	issue, scope := discoveryFixture(t)
	recordDiscovery(t, q, DiscoveryBatch{Issues: []DiscoveryIssue{issue}, Complete: true}, nil)
	scope.Examined = -1
	recordDiscovery(t, q, DiscoveryBatch{Coverage: []DiscoveryScope{scope}, Complete: true}, nil)
	status, err := q.RuntimeStatus(t.Context())
	if err != nil || status.DiscoveryError != "invalid-metadata" || status.ActiveDiscoveryIssues != 1 || status.DiscoveryComplete {
		t.Fatalf("%+v %v", status, err)
	}
	issues := make([]DiscoveryIssue, MaxDiscoveryRecords+1)
	for j := range issues {
		issues[j] = issue
	}
	recordDiscovery(t, q, DiscoveryBatch{Issues: issues, Complete: true}, nil)
	status, err = q.RuntimeStatus(t.Context())
	if err != nil || status.DiscoveryError != "quota" || status.DiscoveryIssueCount != 1 || status.DiscoveryComplete {
		t.Fatalf("%+v %v", status, err)
	}
}
