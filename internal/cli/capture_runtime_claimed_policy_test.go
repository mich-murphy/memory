package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/capturedaemon"
	"github.com/mich-murphy/memory/internal/exclusion"
	"github.com/mich-murphy/memory/internal/publication"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

func claimedTestSession(root, key string) string {
	sum := sha256.Sum256([]byte(root))
	return "session/" + hex.EncodeToString(sum[:]) + "/" + key
}

func claimedTestOwner(t *testing.T, c capturedaemon.Config, path, root string) adapters.SessionFile {
	t.Helper()
	sf, ok := adapters.ClassifyLogical(adapters.NewClaude(c.Home), path, root)
	if !ok || sf.SessionKey == "" {
		t.Fatal("unclassified synthetic owner")
	}
	return sf
}

func claimedTestRequest(c capturedaemon.Config, path, session string) sessioncapture.Request {
	return sessioncapture.Request{Identity: publication.Identity{ArchiveID: c.ArchiveID, MachineID: c.MachineID, Agent: "claude", SessionID: session}, Path: path, SourceID: "manual-arbitrary", Role: "main"}
}

func TestRuntimeClaimedSessionRestrictsManualHintAndCapture(t *testing.T) {
	c, config := runtimeFixture(t)
	root := filepath.Join(c.Home, ".claude")
	a := filepath.Join(root, "projects", "p", "A.jsonl")
	b := filepath.Join(root, "projects", "p", "B.jsonl")
	runtimeWrite(t, a)
	owner := claimedTestOwner(t, c, b, root)
	if _, err := exclusion.RecordFile(c.PolicyDir, owner); err != nil {
		t.Fatal(err)
	}
	session := claimedTestSession(root, owner.SessionKey)
	req := claimedTestRequest(c, a, session)
	if err := runtimePolicy(c)(t.Context(), req); !errors.Is(err, sessioncapture.ErrSessionExcluded) {
		t.Fatalf("claimed opt-out admitted: %v", err)
	}
	q, err := sessioncapture.Open(t.Context(), c.Spool, sessioncapture.Options{ArchiveID: c.ArchiveID, MaxSpoolBytes: c.MaxSpoolBytes, CheckPolicy: runtimePolicy(c)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := q.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := q.Capture(t.Context(), req); !errors.Is(err, sessioncapture.ErrSessionExcluded) {
		t.Fatalf("capture admitted claimed opt-out: %v", err)
	}
	var out bytes.Buffer
	_, err = runCaptureRuntime(t.Context(), "hint", []string{"--config", config, "--agent", "claude", "--session", session, "--source", "arbitrary", "--path", a}, IO{Stdout: &out, Stderr: &out})
	if !errors.Is(err, sessioncapture.ErrSessionExcluded) {
		t.Fatalf("hint admitted claimed opt-out: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(c.Spool, "hints"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatal("excluded hint reached durable inbox")
	}
	// Unknown raw labels and a same-name session in an unrelated namespace remain compatible.
	for _, id := range []string{"arbitrary-label", claimedTestSession(filepath.Join(c.Home, "unrelated"), owner.SessionKey)} {
		req.Identity.SessionID = id
		if _, err := q.Capture(t.Context(), req); err != nil {
			t.Fatalf("compatible raw hint rejected: %v", err)
		}
	}
	// Directory traversal is not an aggregate publication or session claim.
	req = claimedTestRequest(c, root, "")
	req.Role = ""
	if err := runtimeDiscoveryPolicy(c)(t.Context(), req); err != nil {
		t.Fatalf("opt-out suppressed directory traversal: %v", err)
	}
}

func TestRuntimeClaimedSessionAliasNamespaces(t *testing.T) {
	c, _ := runtimeFixture(t)
	logical := filepath.Join(c.Home, ".claude")
	old := filepath.Join(filepath.Dir(c.Home), "old-agent")
	current := filepath.Join(filepath.Dir(c.Home), "current-agent")
	b := filepath.Join(old, "projects", "p", "B.jsonl")
	owner := claimedTestOwner(t, c, b, old)
	record, err := exclusion.RecordFile(c.PolicyDir, owner)
	if err != nil {
		t.Fatal(err)
	}
	c.RootAliases = []capturedaemon.RootAlias{{Agent: "claude", Root: logical, Path: old}, {Agent: "claude", Root: logical, Path: current}}
	a := filepath.Join(current, "projects", "p", "A.jsonl")
	runtimeWrite(t, a)
	for _, root := range []string{logical, old, current} {
		sf := claimedTestOwner(t, c, filepath.Join(root, "projects", "p", "B.jsonl"), root)
		req := claimedTestRequest(c, a, claimedTestSession(root, sf.SessionKey))
		if err := runtimePolicy(c)(t.Context(), req); !errors.Is(err, sessioncapture.ErrSessionExcluded) {
			t.Fatalf("declared namespace %s bypassed opt-out: %v", filepath.Base(root), err)
		}
		// Root metadata alone can retain the declared correspondence.
		record.Paths = nil
		matched, err := runtimeClaimedSessionExcluded(c, req, exclusion.Registry{record})
		if err != nil || !matched {
			t.Fatalf("root-only correspondence lost: %v", err)
		}
	}
	unrelated := filepath.Join(filepath.Dir(c.Home), "unrelated")
	sf := claimedTestOwner(t, c, filepath.Join(unrelated, "projects", "p", "B.jsonl"), unrelated)
	req := claimedTestRequest(c, a, claimedTestSession(unrelated, sf.SessionKey))
	if err := runtimePolicy(c)(t.Context(), req); err != nil {
		t.Fatalf("unrelated root collided: %v", err)
	}
	// All original aliases are considered even when the first spelling is unmapped.
	record.Paths = []string{filepath.Join(unrelated, "unmapped"), b}
	req.Identity.SessionID = claimedTestSession(logical, "p/B")
	matched, err := runtimeClaimedSessionExcluded(c, req, exclusion.Registry{record})
	if err != nil || !matched {
		t.Fatalf("later original alias ignored: %v", err)
	}
}

func TestRuntimeClaimedSessionDeliveryRechecksBeforeCAS(t *testing.T) {
	for _, boundary := range []string{"before-delivery", "current-before"} {
		t.Run(boundary, func(t *testing.T) {
			c, _ := runtimeFixture(t)
			root := filepath.Join(c.Home, ".claude")
			a := filepath.Join(root, "projects", "p", "A.jsonl")
			runtimeWrite(t, a)
			owner := claimedTestOwner(t, c, filepath.Join(root, "projects", "p", "B.jsonl"), root)
			req := claimedTestRequest(c, a, claimedTestSession(root, owner.SessionKey))
			optedOut := false
			exclude := func() error {
				if optedOut {
					return nil
				}
				_, err := exclusion.RecordFile(c.PolicyDir, owner)
				optedOut = err == nil
				return err
			}
			q, err := sessioncapture.Open(t.Context(), c.Spool, sessioncapture.Options{ArchiveID: c.ArchiveID, MaxSpoolBytes: c.MaxSpoolBytes, CheckPolicy: runtimePolicy(c), Fault: func(stage string) error {
				if boundary == "current-before" && stage == boundary {
					return exclude()
				}
				return nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := q.Close(); err != nil {
					t.Error(err)
				}
			}()
			if _, err := q.Capture(t.Context(), req); err != nil {
				t.Fatal(err)
			}
			backend := publication.NewLocal(c.Store)
			if err := publication.WriteArchive(t.Context(), backend, publication.Archive{Contract: publication.Contract, ArchiveID: c.ArchiveID}); err != nil {
				t.Fatal(err)
			}
			if boundary == "before-delivery" {
				if err := exclude(); err != nil {
					t.Fatal(err)
				}
			}
			err = q.Deliver(t.Context(), backend, req.Identity)
			if !errors.Is(err, sessioncapture.ErrSessionExcluded) {
				t.Fatalf("delivery did not preserve opt-out cause: %v", err)
			}
			if _, _, err := backend.Current(context.WithoutCancel(t.Context()), req.Identity.CurrentKey()); !errors.Is(err, publication.ErrNotFound) {
				t.Fatalf("opted-out current was published: %v", err)
			}
		})
	}
}

// Inspect only generated public manifest metadata, never source bodies.
type claimedManifestBackend struct {
	publication.Backend
	withheld bool
}

func (b *claimedManifestBackend) PutFile(ctx context.Context, key, path string) error {
	if strings.Contains(key, "/manifests/") {
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var m publication.Manifest
		if err := json.Unmarshal(body, &m); err != nil {
			return err
		}
		b.withheld = len(m.Sources) > 0
		for i := range m.Sources {
			b.withheld = b.withheld && m.Sources[i].Withheld != nil
		}
	}
	return b.Backend.PutFile(ctx, key, path)
}

func TestRuntimeClaimedSessionWithheldPrivateHistoryBeforeCAS(t *testing.T) {
	c, _ := runtimeFixture(t)
	root := filepath.Join(c.Home, ".claude")
	a := filepath.Join(root, "projects", "p", "A.jsonl")
	runtimeWrite(t, a)
	owner := claimedTestOwner(t, c, filepath.Join(root, "projects", "p", "B.jsonl"), root)
	req := claimedTestRequest(c, a, claimedTestSession(root, owner.SessionKey))
	req.Optional, req.Role = true, "artifact"
	redact := false
	runtimeCheck := runtimePolicy(c)
	backend := &claimedManifestBackend{Backend: publication.NewLocal(c.Store)}
	q, err := sessioncapture.Open(t.Context(), c.Spool, sessioncapture.Options{ArchiveID: c.ArchiveID, MaxSpoolBytes: c.MaxSpoolBytes, CheckPolicy: func(ctx context.Context, r sessioncapture.Request) error {
		if err := runtimeCheck(ctx, r); err != nil {
			return err
		}
		if redact {
			return errors.New("synthetic optional selection exclusion")
		}
		return nil
	}, Fault: func(stage string) error {
		if stage == "current-before" {
			if !backend.withheld {
				return errors.New("expected withheld-only public manifest")
			}
			_, err := exclusion.RecordFile(c.PolicyDir, owner)
			return err
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := q.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := q.Capture(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	// Redact the public source, leaving its original request identity only in
	// retained private evidence. Its healthy A path still must not authorize B.
	redact = true
	if err := publication.WriteArchive(t.Context(), backend, publication.Archive{Contract: publication.Contract, ArchiveID: c.ArchiveID}); err != nil {
		t.Fatal(err)
	}
	// The first finite delivery pass supersedes the selected view; the next
	// pass publishes that higher-sequence withheld view.
	if err := q.Deliver(t.Context(), backend, req.Identity); err != nil {
		t.Fatal(err)
	}
	if err := q.Deliver(t.Context(), backend, req.Identity); !errors.Is(err, sessioncapture.ErrSessionExcluded) {
		t.Fatalf("withheld history bypassed claimed opt-out: %v", err)
	}
	if !backend.withheld {
		t.Fatal("did not exercise withheld-only publication")
	}
	if _, _, err := backend.Current(t.Context(), req.Identity.CurrentKey()); !errors.Is(err, publication.ErrNotFound) {
		t.Fatalf("withheld-only opted-out current published: %v", err)
	}
}
