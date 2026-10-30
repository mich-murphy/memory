package publicationr2_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mich-murphy/memory/internal/capturebackend"
	"github.com/mich-murphy/memory/internal/publication"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

// acceptanceNamespace never accesses the bucket-global archive binding. Only
// that logical key is mapped, to a reserved current key inside this run's archive.
// This acceptance-only adapter is NOT evidence of installed runtime binding.
// Every operation is fenced to the unique prefix and consumes a fixed budget.
type acceptanceNamespace struct {
	publication.Backend
	mu              sync.Mutex
	prefix, binding string
	remaining       int
}

func (b *acceptanceNamespace) key(key string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if key == publication.ArchiveKey {
		key = b.binding
	}
	if !strings.HasPrefix(key, b.prefix) || b.remaining <= 0 {
		return "", errors.New("acceptance namespace or operation budget rejected")
	}
	b.remaining--
	return key, nil
}

// Listing is fenced to the same unique archive and counts as one backend
// operation. No global binding lookup, filtering, or nonpublication reinterpretation.
func (b *acceptanceNamespace) ListSessionNamespaces(ctx context.Context, scope publication.Identity, after string, limit int) ([]string, bool, error) {
	lister, ok := b.Backend.(publication.SessionNamespaceLister)
	if !ok {
		return nil, false, publication.ErrListingUnsupported
	}
	prefix, err := publication.SessionPrefix(scope)
	if err != nil {
		return nil, false, err
	}
	if _, err = b.key(prefix); err != nil {
		return nil, false, err
	}
	return lister.ListSessionNamespaces(ctx, scope, after, limit)
}

func (b *acceptanceNamespace) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	k, err := b.key(key)
	if err != nil {
		return nil, err
	}
	return b.Backend.Open(ctx, k)
}

func (b *acceptanceNamespace) Current(ctx context.Context, key string) ([]byte, string, error) {
	k, err := b.key(key)
	if err != nil {
		return nil, "", err
	}
	return b.Backend.Current(ctx, k)
}

func (b *acceptanceNamespace) CompareAndSwap(ctx context.Context, key, token string, body []byte) error {
	k, err := b.key(key)
	if err != nil {
		return err
	}
	return b.Backend.CompareAndSwap(ctx, k, token, body)
}

func (b *acceptanceNamespace) PutFile(ctx context.Context, key, path string) error {
	k, err := b.key(key)
	if err != nil {
		return err
	}
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if st.Size() > 64<<10 {
		return errors.New("acceptance payload budget rejected")
	}
	return b.Backend.PutFile(ctx, k, path)
}

func (b *acceptanceNamespace) Sync(ctx context.Context, key string) error {
	// Local offline peer needs its durability interface forwarded. R2 promises
	// durable visibility, so there is no additional network request here.
	if f, ok := b.Backend.(publication.DurabilityFence); ok {
		if key == publication.ArchiveKey {
			key = b.binding
		}
		if !strings.HasPrefix(key, b.prefix) {
			return errors.New("acceptance fence rejected")
		}
		return f.Sync(ctx, key)
	}
	return ctx.Err()
}

// faultACK drops only the caller's acknowledgement AFTER a real successful CAS;
// failKey instead fails BEFORE an update. Both are explicit injected faults, not
// claims of a naturally occurring R2 outage or transport-level ACK loss.
type faultACK struct {
	publication.Backend
	lostKey, failKey string
	lost             bool
}

func (b *faultACK) CompareAndSwap(ctx context.Context, key, token string, body []byte) error {
	if key == b.failKey {
		return errors.New("injected failed update")
	}
	if err := b.Backend.CompareAndSwap(ctx, key, token, body); err != nil {
		return err
	}
	if key == b.lostKey && !b.lost {
		b.lost = true
		return errors.New("injected lost acknowledgement")
	}
	return nil
}

func (b *faultACK) Sync(ctx context.Context, key string) error {
	if f, ok := b.Backend.(publication.DurabilityFence); ok {
		return f.Sync(ctx, key)
	}
	return ctx.Err()
}

func TestCandidateR2LiveAcceptance(t *testing.T) {
	if os.Getenv("CANDIDATE_R2_LIVE_ACCEPTANCE") != "new-synthetic-namespace-only" {
		t.Skip("explicit candidate live opt-in required")
	}
	if os.Getenv("CANDIDATE_R2_ANALYTICS_PYTHON") == "" {
		t.Fatal("analytics interpreter required before remote work")
	}
	spec := capturebackend.Spec{Kind: "r2", Endpoint: os.Getenv("R2_ENDPOINT"), Bucket: os.Getenv("R2_BUCKET"), AccessKeyEnv: "R2_ACCESS_KEY_ID", SecretKeyEnv: "R2_SECRET_ACCESS_KEY"}
	b, err := capturebackend.Open(t.Context(), spec, capturebackend.Dependencies{})
	if err != nil {
		t.Fatal("candidate backend construction failed (details withheld)")
	}
	runCandidateAcceptance(t, b)
}

func TestCandidateR2OfflineAcceptance(t *testing.T) {
	if os.Getenv("CANDIDATE_R2_ANALYTICS_PYTHON") == "" {
		t.Skip("set candidate analytics interpreter for complete offline rehearsal")
	}
	runCandidateAcceptance(t, publication.NewLocal(filepath.Join(t.TempDir(), "store")))
}

func runCandidateAcceptance(t *testing.T, backend publication.Backend) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	python := os.Getenv("CANDIDATE_R2_ANALYTICS_PYTHON")
	script := filepath.Join("..", "..", "tools", "candidate-live-r2", "verify.py")
	preflight := exec.CommandContext(ctx, python, script, "--preflight")
	preflight.Env = []string{"PYTHONDONTWRITEBYTECODE=1", "PATH=" + os.Getenv("PATH")}
	if err := preflight.Run(); err != nil {
		t.Fatal("analytics preflight failed before any remote request (diagnostics withheld)")
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal("namespace generation failed")
	}
	archive := "candidate-live-acceptance-" + hex.EncodeToString(nonce[:])
	id := publication.Identity{ArchiveID: archive, MachineID: "synthetic-machine", Agent: "pi", SessionID: "A"}
	binding := id
	binding.SessionID = "__archive_binding__"
	ns := &acceptanceNamespace{Backend: backend, prefix: publication.Prefix + "/archives/" + hex.EncodeToString([]byte(archive)) + "/", binding: binding.CurrentKey(), remaining: 160}
	t.Logf("NEW synthetic archive_id=%s namespace=%s (retained; no deletion)", archive, ns.prefix)
	// No request to the shared capture-v1/archive.json, and create-only binding.
	if err := publication.WriteArchive(ctx, ns, publication.Archive{Contract: publication.Contract, ArchiveID: archive}); err != nil {
		t.Fatal("isolated binding creation failed")
	}
	probe := id
	probe.SessionID = "__conditional_probe__"
	if err := ns.CompareAndSwap(ctx, probe.CurrentKey(), "", []byte("first")); err != nil {
		t.Fatal("conditional create failed")
	}
	if err := ns.CompareAndSwap(ctx, probe.CurrentKey(), "", []byte("duplicate")); !errors.Is(err, publication.ErrConflict) {
		t.Fatal("create conflict missing")
	}
	_, token, err := ns.Current(ctx, probe.CurrentKey())
	if err != nil {
		t.Fatal("probe read failed")
	}
	if err := ns.CompareAndSwap(ctx, probe.CurrentKey(), token, []byte("second")); err != nil {
		t.Fatal("conditional update failed")
	}
	if err := ns.CompareAndSwap(ctx, probe.CurrentKey(), token, []byte("stale")); !errors.Is(err, publication.ErrConflict) {
		t.Fatal("stale update conflict missing")
	}
	data, _, err := ns.Current(ctx, probe.CurrentKey())
	if err != nil || string(data) != "second" {
		t.Fatal("conflict changed successful bytes")
	}
	root := t.TempDir()
	qpath := filepath.Join(root, "queue")
	options := sessioncapture.Options{ArchiveID: archive, MaxSpoolBytes: 1 << 20, CheckPolicy: func(context.Context, sessioncapture.Request) error { return nil }}
	q, err := sessioncapture.Open(ctx, qpath, options)
	if err != nil {
		t.Fatal("queue open failed")
	}
	// Capture deterministic synthetic Pi messages only; no discovery or real source reads.
	a1 := []byte("{\"type\":\"message\",\"id\":\"synthetic-user\",\"timestamp\":\"2026-01-01T00:00:00Z\",\"message\":{\"role\":\"user\",\"content\":[{\"type\":\"text\",\"text\":\"synthetic acceptance A\"}]}}\n")
	a2 := append(bytes.Clone(a1), []byte("{\"type\":\"message\",\"id\":\"synthetic-assistant\",\"timestamp\":\"2026-01-01T00:00:01Z\",\"message\":{\"role\":\"assistant\",\"content\":[{\"type\":\"text\",\"text\":\"synthetic response\"}]}}\n")...)
	braw := bytes.ReplaceAll(a1, []byte("acceptance A"), []byte("acceptance B"))
	path := filepath.Join(root, "synthetic-A.jsonl")
	capture := func(identity publication.Identity, p string, raw []byte, observed int64) publication.Manifest {
		t.Helper()
		if err := os.WriteFile(p, raw, 0o600); err != nil {
			t.Fatal("synthetic source write failed")
		}
		m, err := q.Capture(ctx, sessioncapture.Request{Identity: identity, SourceID: "main", Role: "main", Path: p, ObservedAtNS: observed})
		if err != nil {
			t.Fatal("durable capture failed")
		}
		return m
	}
	first := capture(id, path, a1, 1)
	if err := q.Close(); err != nil {
		t.Fatal("queue close failed")
	}
	q, err = sessioncapture.Open(ctx, qpath, options)
	if err != nil {
		t.Fatal("queue restart failed")
	}
	defer func() {
		if err := q.Close(); err != nil {
			t.Error("queue close failed")
		}
	}()
	faults := &faultACK{Backend: ns, lostKey: id.CurrentKey()}
	if err := q.Deliver(ctx, faults, id); err != nil || !faults.lost {
		t.Fatal("lost-ACK reconciliation failed")
	}
	verifyExport := func(identity publication.Identity, m publication.Manifest, raw []byte, name string) string {
		t.Helper()
		dest := filepath.Join(root, name)
		out, err := publication.ExportSession(ctx, ns, identity, dest)
		if err != nil {
			t.Fatal("authoritative export failed")
		}
		if out.Manifest.Identity != identity || out.Manifest.PublicationID != m.PublicationID || len(out.Bodies) != 1 || len(out.Manifest.Sources) != 1 {
			t.Fatal("export identity or coverage mismatch")
		}
		s := out.Manifest.Sources[0]
		body := out.Bodies[0]
		if s.SourceID != "main" || s.Role != "main" || body.SourceID != s.SourceID || body.EpochID != s.EpochID || s.EpochID != m.Sources[0].EpochID || s.Object.SHA256 != body.SHA256 {
			t.Fatal("source/epoch/role identity changed")
		}
		got, err := os.ReadFile(filepath.Join(dest, body.Path))
		if err != nil || !bytes.Equal(got, raw) {
			t.Fatal("exact export bytes mismatch")
		}
		return dest
	}
	oldExport := verifyExport(id, first, a1, "A-first")
	second := capture(id, path, a2, 2)
	faults.failKey = id.CurrentKey()
	if err := q.Deliver(ctx, faults, id); err == nil {
		t.Fatal("injected update unexpectedly succeeded")
	}
	verifyExport(id, first, a1, "A-after-failure")
	bid := id
	bid.SessionID = "B"
	bm := capture(bid, filepath.Join(root, "synthetic-B.jsonl"), braw, 1)
	if err := q.Deliver(ctx, faults, bid); err != nil {
		t.Fatal("healthy B blocked by failed A")
	}
	bexport := verifyExport(bid, bm, braw, "B")
	verifyAcceptanceCurrentHeads(ctx, t, ns, first, bm, binding, probe)
	faults.failKey = ""
	if err := q.Deliver(ctx, faults, id); err != nil {
		t.Fatal("A update retry failed")
	}
	// Remove ONLY generated sources; read/export cannot fall back to originals.
	if err := os.Remove(path); err != nil {
		t.Fatal("synthetic source removal failed")
	}
	if err := os.Remove(filepath.Join(root, "synthetic-B.jsonl")); err != nil {
		t.Fatal("synthetic source removal failed")
	}
	aexport := verifyExport(id, second, a2, "A-final")
	cmd := exec.CommandContext(ctx, python, script, oldExport, aexport, bexport, filepath.Join(root, "analytics.duckdb"))
	cmd.Env = preflight.Env // Analytics does not receive destination credentials.
	if err := cmd.Run(); err != nil {
		t.Fatal("real analytics import/event index/report acceptance failed (diagnostics withheld)")
	}
	t.Logf("PASS conditional create/conflict/update, injected ACK loss and failed-update isolation, exact reader export, analytics; backend operations=%d/160", 160-ns.remaining)
}

// The Local rehearsal explicitly has no namespace listing capability. The SDK
// peer tests cover this journey offline; live R2 exercises actual StartAfter.
func verifyAcceptanceCurrentHeads(ctx context.Context, t *testing.T, ns *acceptanceNamespace, a, b publication.Manifest, binding, probe publication.Identity) {
	t.Helper()
	if _, ok := ns.Backend.(publication.SessionNamespaceLister); !ok {
		t.Log("current-root enumeration unsupported by Local rehearsal")
		return
	}
	want := map[publication.Identity]publication.Manifest{a.Identity: a, b.Identity: b}
	seen := map[publication.Identity]bool{}
	failures := map[publication.Identity]bool{}
	cursor := ""
	complete := false
	for range 8 {
		page, err := publication.ReadCurrentHeads(ctx, ns, a.Identity.ArchiveID, a.Identity.MachineID, cursor, 1)
		if err != nil {
			t.Fatal("bounded current-root enumeration failed (diagnostics withheld)")
		}
		if len(page.Heads)+len(page.Failures) > 1 {
			t.Fatal("head enumeration exceeded namespace budget")
		}
		for _, head := range page.Heads {
			m, ok := want[head.Identity]
			if !ok || seen[head.Identity] || head.Sequence != m.Sequence || head.PublicationID != m.PublicationID {
				t.Fatal("head enumeration differs from successful publication")
			}
			seen[head.Identity] = true
		}
		for _, failure := range page.Failures {
			if (failure.Identity != binding && failure.Identity != probe) || failures[failure.Identity] || failure.Reason != "current-unreadable" {
				t.Fatal("unexpected current-root failure metadata")
			}
			failures[failure.Identity] = true
		}
		if page.Complete {
			complete = true
			break
		}
		if page.NextCursor == cursor {
			t.Fatal("current-root enumeration did not advance")
		}
		cursor = page.NextCursor
	}
	if !complete || len(seen) != 2 || len(failures) != 2 || !failures[binding] || !failures[probe] {
		t.Fatal("incomplete current-root enumeration")
	}
}

func TestCandidateNamespaceRejectsOutsideKeys(t *testing.T) {
	ns := &acceptanceNamespace{prefix: "capture-v1/archives/unique/", binding: "capture-v1/archives/unique/binding", remaining: 2}
	if _, err := ns.key("capture-v1/archive.json"); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"capture-v1/archives/other/current.json", "legacy/key", ""} {
		if _, err := ns.key(key); err == nil {
			t.Fatal("outside key accepted")
		}
	}
	if _, err := ns.key(ns.binding); err != nil {
		t.Fatal(err)
	}
	if _, err := ns.key(ns.binding); err == nil {
		t.Fatal("budget not enforced")
	}
}
