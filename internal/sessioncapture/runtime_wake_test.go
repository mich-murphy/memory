package sessioncapture

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func wakePath(dir, agent string) string { return filepath.Join(dir, "discovery-hints", agent+".json") }

func TestRuntimeDiscoveryHintIndependentCoalescedAndPrivate(t *testing.T) {
	dir, o, req := setup(t)
	q := open(t, dir, o) // Writer must not acquire this lifetime Queue lock.
	for range 4 {
		for _, agent := range discoveryHintAgents {
			if err := WriteDiscoveryHint(t.Context(), dir, o.ArchiveID, req.Identity.MachineID, agent); err != nil {
				t.Fatal(err)
			}
		}
	}
	entries, err := os.ReadDir(filepath.Join(dir, "discovery-hints"))
	if err != nil || len(entries) != 3 {
		t.Fatalf("unbounded slots: %v %v", entries, err)
	}
	for _, entry := range entries {
		st, err := os.Stat(filepath.Join(dir, "discovery-hints", entry.Name()))
		if err != nil || st.Mode().Perm() != 0o600 || st.Size() > maxDiscoveryHintBytes {
			t.Fatalf("unsafe metadata: %v %v", st, err)
		}
	}
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := q.pollDiscoveryHints(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-q.runtimeLocal().discoveryWake:
	default:
		t.Fatal("missing wake")
	}
	if err := q.pollDiscoveryHints(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-q.runtimeLocal().discoveryWake:
		t.Fatal("same nonce busy-looped")
	default:
	}
	status, err := q.RuntimeStatus(t.Context())
	if err != nil || status.PendingDiscoveryHints != 3 || status.DiscoveryHintsScannedAtNS != o.Now().UnixNano() || status.Sources != 0 {
		t.Fatalf("fabricated progress: %+v %v", status, err)
	}
	for _, args := range [][3]string{{"", "machine", "codex"}, {o.ArchiveID, "", "codex"}, {o.ArchiveID, "machine", "../codex"}, {o.ArchiveID, "bad\x00machine", "codex"}} {
		if err := WriteDiscoveryHint(t.Context(), dir, args[0], args[1], args[2]); err == nil {
			t.Fatal("invalid wake accepted")
		}
	}
}

func TestRuntimeDiscoveryHintRestartAndNonceSweepFence(t *testing.T) {
	dir, o, req := setup(t)
	if err := WriteDiscoveryHint(t.Context(), dir, o.ArchiveID, req.Identity.MachineID, "codex"); err != nil {
		t.Fatal(err)
	}
	q, err := Open(t.Context(), dir, o)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	q = open(t, dir, o)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	entered := make(chan int, 4)
	releases := make(chan struct{})
	var calls atomic.Int32
	backend := newBackend(t)
	done := make(chan error, 1)
	go func() {
		done <- q.Run(ctx, backend, RuntimeOptions{PollInterval: time.Hour, DiscoverBatch: func(ctx context.Context) (DiscoveryBatch, error) {
			call := int(calls.Add(1))
			entered <- call
			select {
			case <-releases:
			case <-ctx.Done():
				return DiscoveryBatch{}, ctx.Err()
			}
			return DiscoveryBatch{Complete: call != 1}, nil
		}})
	}()
	waitCall := func(want int) {
		t.Helper()
		select {
		case got := <-entered:
			if got != want {
				t.Fatalf("call %d want %d", got, want)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("no autonomous discovery %d", want)
		}
	}
	waitCall(1) // Snapshot owns v1, even though the native event has no pathname.
	if err := WriteDiscoveryHint(t.Context(), dir, o.ArchiveID, req.Identity.MachineID, "codex"); err != nil {
		t.Fatal(err)
	}
	// v2 may concern a root already visited by this partial cursor.
	releases <- struct{}{}
	waitCall(2)
	releases <- struct{}{}
	waitCall(3) // v2 must force a fresh sweep without waiting for the hour timer.
	if _, err := os.Stat(wakePath(dir, "codex")); err != nil {
		t.Fatalf("new nonce acknowledged by old sweep: %v", err)
	}
	releases <- struct{}{}
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		_, err := os.Stat(wakePath(dir, "codex"))
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("complete fresh sweep did not acknowledge wake")
		case <-tick.C:
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("workers leaked")
	}
}

func TestRuntimeDiscoveryHintFailureNeverAcknowledges(t *testing.T) {
	for _, mode := range []string{"callback", "database"} {
		t.Run(mode, func(t *testing.T) {
			dir, o, req := setup(t)
			q := open(t, dir, o)
			if err := q.InitRuntime(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := WriteDiscoveryHint(t.Context(), dir, o.ArchiveID, req.Identity.MachineID, "codex"); err != nil {
				t.Fatal(err)
			}
			if mode == "database" {
				_, err := q.db.ExecContext(t.Context(), `CREATE TRIGGER reject_wake_commit BEFORE UPDATE ON runtime_destination BEGIN SELECT RAISE(ABORT,'test commit failure'); END`)
				if err != nil {
					t.Fatal(err)
				}
			}
			options := runtimeDefaults(RuntimeOptions{DiscoverBatch: func(context.Context) (DiscoveryBatch, error) {
				if mode == "callback" {
					return DiscoveryBatch{Complete: true}, errors.New("discovery failed")
				}
				return DiscoveryBatch{Complete: true}, nil
			}})
			err := q.discoverOnce(t.Context(), options)
			if mode == "database" && err == nil {
				t.Fatal("database failure suppressed")
			}
			if _, err := os.Stat(wakePath(dir, "codex")); err != nil {
				t.Fatalf("failed handoff removed wake: %v", err)
			}
		})
	}
}

func TestRuntimeDiscoveryHintPoisonDoesNotStarveKnownSlot(t *testing.T) {
	dir, o, req := setup(t)
	q := open(t, dir, o)
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := WriteDiscoveryHint(t.Context(), dir, o.ArchiveID, req.Identity.MachineID, "codex"); err != nil {
		t.Fatal(err)
	}
	for j := range maxDiscoveryHintEntries + 3 {
		if err := os.WriteFile(filepath.Join(dir, "discovery-hints", time.Unix(int64(j), 0).Format("150405")), []byte("private poison"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(wakePath(dir, "pi"), make([]byte, maxDiscoveryHintBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := q.pollDiscoveryHints(t.Context()); err != nil {
		t.Fatal(err)
	}
	status, err := q.RuntimeStatus(t.Context())
	if err != nil || status.PendingDiscoveryHints != 1 || status.RejectedDiscoveryHints == 0 || !status.DiscoveryHintsTruncated {
		t.Fatalf("poison blocked healthy wake: %+v %v", status, err)
	}
	options := runtimeDefaults(RuntimeOptions{DiscoverBatch: func(context.Context) (DiscoveryBatch, error) { return DiscoveryBatch{Complete: true}, nil }})
	if err := q.discoverOnce(t.Context(), options); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wakePath(dir, "codex")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("healthy hint not acknowledged: %v", err)
	}
	if _, err := os.Stat(wakePath(dir, "pi")); err != nil {
		t.Fatalf("invalid evidence discarded: %v", err)
	}
}

func TestRuntimeDiscoveryHintUnsafeStorageAndEncodedBound(t *testing.T) {
	dir, o, req := setup(t)
	// Escaped but otherwise valid identity strings must not create unreadable
	// oversized metadata; this is explicit backpressure before acceptance.
	long := strings.Repeat("\\", 256)
	if err := WriteDiscoveryHint(t.Context(), dir, long, long, "codex"); !errors.Is(err, ErrQuota) {
		t.Fatalf("oversized encoded identity: %v", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(dir, "discovery-hints")); err != nil {
		t.Fatal(err)
	}
	if err := WriteDiscoveryHint(t.Context(), dir, o.ArchiveID, req.Identity.MachineID, "codex"); err == nil {
		t.Fatal("unsafe wake directory accepted")
	}
	entries, err := os.ReadDir(target)
	if err != nil || len(entries) != 0 {
		t.Fatalf("wrote through symlink: %v %v", entries, err)
	}
}

func TestRuntimeHintOwnershipAfterRawAdmission(t *testing.T) {
	for _, mode := range []string{"classified", "legacy", "forged", "database", "ambiguous"} {
		t.Run(mode, func(t *testing.T) {
			dir, o, _ := setup(t)
			q := open(t, dir, o)
			root := filepath.Join(t.TempDir(), "root")
			req := ownerRequest(t, root, "first", false)
			if mode == "legacy" {
				req.Associations = nil
			}
			if mode == "forged" {
				req.Associations = []string{"root:" + root}
			}
			if mode == "database" {
				if _, err := q.db.ExecContext(t.Context(), ownershipDDL); err != nil {
					t.Fatal(err)
				}
				if _, err := q.db.ExecContext(t.Context(), `CREATE TRIGGER reject_hint_owner BEFORE INSERT ON source_ownership BEGIN SELECT RAISE(ABORT,'test owner failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "ambiguous" {
				if err := q.RecordOwnership(t.Context(), ownerRequest(t, root, "other", false)); err != nil {
					t.Fatal(err)
				}
			}
			err := q.Hint(t.Context(), req)
			if mode == "forged" || mode == "database" {
				if err == nil {
					t.Fatal("ownership failure hidden")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			sources, err := q.WorkStatus(t.Context(), 0, 64)
			if err != nil || len(sources) != 1 || sources[0].CapturedVersion != 0 {
				t.Fatalf("raw observation lost or marked captured: %+v %v", sources, err)
			}
			if mode == "classified" {
				session, _, err := q.LookupOwnership(t.Context(), "claude", root, testOwnerUUID)
				if err != nil || session != req.Identity.SessionID {
					t.Fatalf("Hint did not register owner: %s %v", session, err)
				}
			}
			if mode == "ambiguous" {
				if _, _, err := q.LookupOwnership(t.Context(), "claude", root, testOwnerUUID); !errors.Is(err, ErrOwnershipAmbiguous) {
					t.Fatalf("ambiguity not retained: %v", err)
				}
			}
		})
	}
}
