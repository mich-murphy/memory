package syncer

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/exclusion"
	"github.com/mich-murphy/memory/internal/launch"
	"github.com/mich-murphy/memory/internal/state"
)

func contractLaunch(t *testing.T, sy *Syncer) launch.Envelope {
	t.Helper()
	path := filepath.Join(sy.cfg.Home, ".claude", "projects", "project", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return launch.Envelope{Schema: 1, ID: "stable-event", Agent: "claude", Path: path, RecordedAt: 100, Payload: map[string]any{"hook_input": map[string]any{"cwd": sy.cfg.Home}}}
}

func TestDurableLaunchRetryIsIdempotentAndConflictingPayloadFailsClosed(t *testing.T) {
	sy, _, _, bucket := contractArchive(t)
	e := contractLaunch(t, sy)
	if err := sy.RecordEnvelope(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	_, first, err := sy.current("launch", e.Agent, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	bucket.takeOps()
	if err := sy.RecordEnvelope(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	if ops, _ := bucket.takeOps(); len(ops) != 0 {
		t.Fatalf("durable launch retry performed remote operations: %v", ops)
	}
	changed := e
	changed.Payload = map[string]any{"cwd": "changed"}
	if err := sy.RecordEnvelope(t.Context(), changed); err == nil || !strings.Contains(err.Error(), "conflicting payload") {
		t.Fatalf("conflicting stable ID = %v, want explicit conflict", err)
	}
	changed.Payload = map[string]any{"unsupported": make(chan int)}
	if err := sy.RecordEnvelope(t.Context(), changed); err == nil {
		t.Fatal("non-JSON retry payload was accepted")
	}
	_, after, err := sy.current("launch", e.Agent, e.ID)
	if err != nil || !bytes.Equal(first.Body, after.Body) {
		t.Fatalf("durable launch changed after conflicting retries: %v", err)
	}
}

func TestLaunchRejectionsDoNotPublishOrProjectSession(t *testing.T) {
	for _, kind := range []string{"invalidEnvelope", "unknownAgent", "noSessionIdentity", "unclassifiedPath", "credential", "optOut", "recoveryFailure"} {
		t.Run(kind, func(t *testing.T) {
			sy, cfg, db, bucket := contractArchive(t)
			e := contractLaunch(t, sy)
			wantSuppressed := false
			switch kind {
			case "invalidEnvelope":
				e.Schema = 2
			case "unknownAgent":
				e.Agent = "unknown"
			case "noSessionIdentity":
				e.Path = filepath.Join(cfg.Home, ".claude", "settings.json")
			case "unclassifiedPath":
				e.Path = filepath.Join(cfg.Home, "unrelated.txt")
			case "credential":
				e.Path = filepath.Join(cfg.Home, ".claude", "auth.json.zip")
				wantSuppressed = true
			case "optOut":
				if _, err := exclusion.RecordPath(cfg.StateDir, sy.adapters[e.Agent], e.Path); err != nil {
					t.Fatal(err)
				}
				wantSuppressed = true
			case "recoveryFailure":
				sy.policyErr = errors.New("invalid capture policy")
			}
			err := sy.RecordEnvelope(t.Context(), e)
			if wantSuppressed && err != nil || !wantSuppressed && err == nil {
				t.Fatalf("RecordEnvelope(%s) = %v, suppressed = %v", kind, err, wantSuppressed)
			}
			for key := range bucket.objects {
				if strings.Contains(key, "/records/launch/") || strings.Contains(key, "/records/session/") {
					t.Fatalf("rejected/suppressed launch published %s", key)
				}
			}
			sessions, err := db.PendingManifests()
			if err != nil || len(sessions) != 0 {
				t.Fatalf("rejected/suppressed launch pending sessions = %+v, %v", sessions, err)
			}
		})
	}
}

func TestLaunchSnapshotFailureRemainsRetryableWithoutAcknowledgement(t *testing.T) {
	sy, cfg, db, _ := contractArchive(t)
	t.Setenv("AGENT_SESSIONS_STATE_DIR", cfg.StateDir)
	e := contractLaunch(t, sy)
	if err := sy.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	// An exclusion file becomes unreadable as a document between recovery and snapshot capture.
	// Snapshot failure must not acknowledge a launch or project an incomplete session.
	path := filepath.Join(cfg.StateDir, "exclusions", "broken.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not JSON"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := sy.RecordEnvelope(t.Context(), e); err == nil {
		t.Fatal("launch acknowledged despite snapshot failure")
	}
	assertNoLaunchProjection(t, sy, db, e.ID)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := sy.RecordEnvelope(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	p, _, err := sy.current("launch", e.Agent, e.ID)
	if err != nil || p == nil || p.Launch.Snapshot.Timing != "captured" {
		t.Fatalf("retry launch = %+v, %v", p, err)
	}
}

func TestLaunchPublicationFailureDoesNotAcknowledgeAndStableRetrySucceeds(t *testing.T) {
	sy, cfg, db, bucket := contractArchive(t)
	e := contractLaunch(t, sy)
	if err := sy.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	failed := &reviewCrashStore{Store: bucket, phase: "launchPlan"}
	sy.bucket = failed
	if err := sy.RecordEnvelope(t.Context(), e); err == nil || !failed.fired {
		t.Fatalf("RecordEnvelope failure = %v, fired = %v", err, failed.fired)
	}
	assertNoLaunchProjection(t, sy, db, e.ID)
	sy = New(cfg, bucket, db, reviewAdapters(cfg.Home), Options{})
	if err := sy.RecordEnvelope(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	p, _, err := sy.current("launch", e.Agent, e.ID)
	if err != nil || p == nil || p.Launch.Envelope.ID != e.ID {
		t.Fatalf("retried launch = %+v, %v", p, err)
	}
}

func assertNoLaunchProjection(t *testing.T, sy *Syncer, db *state.State, id string) {
	t.Helper()
	p, _, err := sy.current("launch", "claude", id)
	if err != nil || p != nil {
		t.Fatalf("failed launch current = %+v, %v", p, err)
	}
	pending, err := db.PendingManifests()
	if err != nil || len(pending) != 0 {
		t.Fatalf("failed launch projected sessions = %+v, %v", pending, err)
	}
	ref := adapters.SessionRef{Agent: "claude", Key: "project/session"}
	sess, err := db.Session(ref)
	if err != nil || sess != nil {
		t.Fatalf("failed launch session = %+v, %v", sess, err)
	}
}
