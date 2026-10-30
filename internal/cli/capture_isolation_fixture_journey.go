package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/mich-murphy/memory/internal/publication"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

// First optional upload genuinely fails; subsequent optional operations wait on
// an unclosed channel. Required dependencies/current references stay healthy.
type isolationBackend struct {
	publication.Backend
	key     string
	calls   atomic.Int64
	entered chan struct{}
	release chan struct{}
}

func (b *isolationBackend) boundary(ctx context.Context, key string) error {
	if key != b.key {
		return nil
	}
	n := b.calls.Add(1)
	if n == 1 {
		return errors.New("synthetic optional transport unavailable")
	}
	if n == 2 {
		close(b.entered)
	}
	select {
	case <-b.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *isolationBackend) PutFile(ctx context.Context, key, path string) error {
	if err := b.boundary(ctx, key); err != nil {
		return err
	}
	return b.Backend.PutFile(ctx, key, path)
}

func (b *isolationBackend) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := b.boundary(ctx, key); err != nil {
		return nil, err
	}
	return b.Backend.Open(ctx, key)
}

func (b *isolationBackend) Sync(ctx context.Context, key string) error {
	if err := b.boundary(ctx, key); err != nil {
		return err
	}
	if d, ok := b.Backend.(publication.DurabilityFence); ok {
		return d.Sync(ctx, key)
	}
	return nil
}

func isolationRun(ctx context.Context, q *sessioncapture.Queue, b publication.Backend) (context.CancelFunc, <-chan error) {
	run, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- q.Run(run, b, sessioncapture.RuntimeOptions{PollInterval: 10 * time.Millisecond, CaptureWorkers: 1, DeliveryWorkers: 1, OperationTimeout: 10 * time.Second, MaxAttempts: 8})
	}()
	return cancel, done
}

func isolationCrash(ctx context.Context, root string, q *sessioncapture.Queue, local publication.Backend, id publication.Identity) error {
	if _, err := isolationCapture(ctx, root, q, id, false, isolationFirst); err != nil {
		return err
	}
	artifact, err := isolationCapture(ctx, root, q, id, true, isolationArtifact)
	if err != nil {
		return err
	}
	original := artifact.Sources[1]
	backend := &isolationBackend{Backend: local, key: original.Object.Key, entered: make(chan struct{}), release: make(chan struct{})}
	cancel, done := isolationRun(ctx, q, backend)
	// Normal errors always cancel/join. Only the explicit successful crash below
	// bypasses deferred cleanup while repair is still inside the optional backend.
	defer func() { _ = isolationStop(cancel, done) }()
	select {
	case <-backend.entered:
	case <-ctx.Done():
		return ctx.Err()
	}
	latest, err := isolationCapture(ctx, root, q, id, false, isolationLatest)
	if err != nil {
		return err
	}
	selected, err := isolationWait(ctx, q, local, id, latest.Sources[0].Object.SHA256, "unavailable")
	if err != nil {
		return err
	}
	if backend.calls.Load() < 2 {
		return errors.New("optional repair never entered")
	}
	for n := range selected.Sources {
		s := &selected.Sources[n]
		if s.Optional && (publication.SourceKeyHash(*s) != publication.SourceKeyHash(original) || s.Withheld == nil || s.Withheld.SnapshotCommitment != publication.ObjectCommitment(original.Object)) {
			return sessioncapture.ErrIntegrity
		}
	}
	if err := isolationExport(ctx, root, "withheld", local, id, selected); err != nil {
		return err
	}
	if err := isolationWriteExpectations(root, id, original, latest.Sources[0]); err != nil {
		return err
	}

	// Neither release nor queue Close runs. The new process must recover the
	// durable working repair claim and original compressed spool payload itself.
	return isolationExit()
}

func isolationContinue(ctx context.Context, root, stage string, q *sessioncapture.Queue, b publication.Backend, id publication.Identity) error {
	body, err := os.ReadFile(filepath.Join(root, "expectations.json"))
	if err != nil {
		return err
	}
	var e isolationExpectations
	if err := json.Unmarshal(body, &e); err != nil {
		return err
	}
	if e.Identity != id {
		return sessioncapture.ErrIntegrity
	}
	hash, reason := e.Main.Object.SHA256, ""
	if stage == "revoke" {
		hash, err = isolationRevokeCapture(ctx, root, q, id)
		if err != nil {
			return err
		}
		reason = "policy"
	}

	cancel, done := isolationRun(ctx, q, b)
	m, waitErr := isolationWait(ctx, q, b, id, hash, reason)
	cancel()
	runErr := <-done
	if err := errors.Join(waitErr, runErr); err != nil {
		return err
	}
	var artifact publication.Source
	for n := range m.Sources {
		s := &m.Sources[n]
		if s.Optional {
			artifact = *s
		}
	}
	if artifact.CapturedAtNS != e.Artifact.CapturedAtNS || publication.SourceKeyHash(artifact) != publication.SourceKeyHash(e.Artifact) {
		return errors.New("artifact identity/capture freshness changed")
	}
	if stage == "repair" && artifact.Object != e.Artifact.Object {
		return errors.New("repair did not select retained artifact")
	}
	name := "repaired"
	if stage == "revoke" {
		name = "revoked"
	}
	return isolationExport(ctx, root, name, b, id, m)
}

func isolationWait(ctx context.Context, q *sessioncapture.Queue, b publication.Backend, id publication.Identity, hash, reason string) (publication.Manifest, error) {
	timer := time.NewTicker(10 * time.Millisecond)
	defer timer.Stop()
	for {
		m, err := publication.Read(ctx, b, id)
		if err == nil && isolationSelected(m, hash, reason) {
			states, err := q.Status(ctx)
			if err != nil {
				return m, err
			}
			for n := range states {
				s := &states[n]
				if s.PublicationID == m.PublicationID && s.State == "published" {
					return m, nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return publication.Manifest{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func isolationSelected(m publication.Manifest, hash, reason string) bool {
	main, optional := false, false
	for n := range m.Sources {
		s := &m.Sources[n]
		if s.Role == "main" && s.Object.SHA256 == hash {
			main = true
		}
		if s.Optional {
			optional = (reason == "" && s.Withheld == nil) || (s.Withheld != nil && s.Withheld.Reason == reason && s.SourceID == "" && s.Path == "" && s.Object == (publication.Object{}))
		}
	}
	return main && optional
}

func isolationExport(ctx context.Context, root, name string, b publication.Backend, id publication.Identity, want publication.Manifest) error {
	out, err := publication.ExportSession(ctx, b, id, filepath.Join(root, "export-"+name))
	if err != nil {
		return err
	}
	if out.Manifest.PublicationID != want.PublicationID {
		return errors.New("export publication changed")
	}
	for n := range want.Sources {
		s := &want.Sources[n]
		if s.Withheld != nil && (s.Withheld.SourceIDHash == "" || s.Withheld.SnapshotCommitment == "" || want.Coverage.Enrichment != "partial") {
			return errors.New("invalid withholding coverage/commitment")
		}
	}
	return nil
}

// isolationExit is reachable only after durable checked fixture output.
func isolationExit() error {
	if err := flushFixtureCoverage(); err != nil {
		return err
	}
	os.Exit(0)
	return nil
}

func isolationStop(cancel context.CancelFunc, done <-chan error) error { cancel(); return <-done }

func isolationRevokeCapture(ctx context.Context, root string, q *sessioncapture.Queue, id publication.Identity) (string, error) {
	if err := isolationWrite(root, "policy/deny-optional", []byte("true")); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(root, "sources"), 0o700); err != nil {
		return "", err
	}
	m, err := isolationCapture(ctx, root, q, id, false, isolationRevoked)
	if err != nil {
		return "", err
	}
	for n := range m.Sources {
		s := &m.Sources[n]
		if s.Role == "main" && s.State == "active" {
			return s.Object.SHA256, nil
		}
	}
	return "", sessioncapture.ErrIntegrity
}

func isolationWriteExpectations(root string, id publication.Identity, original, main publication.Source) error {
	expect := isolationExpectations{Identity: id, MainBody: isolationLatest, RevokedBody: isolationRevoked, ArtifactBody: isolationArtifact, Artifact: original, Main: main}
	body, err := json.Marshal(expect)
	if err != nil {
		return err
	}
	return isolationWrite(root, "expectations.json", body)
}
