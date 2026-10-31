package sessioncapture

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mich-murphy/memory/internal/publication"
)

func setup(t *testing.T) (string, Options, Request) {
	t.Helper()
	root := t.TempDir()
	p := filepath.Join(root, "transcript.jsonl")
	if e := os.WriteFile(p, []byte("first\n"), 0o600); e != nil {
		t.Fatal(e)
	}
	return filepath.Join(root, "queue"), Options{ArchiveID: "archive", MaxSpoolBytes: 1 << 20, CheckPolicy: func(context.Context, Request) error { return nil }, Now: func() time.Time { return time.Unix(100, 0) }}, Request{Identity: publication.Identity{ArchiveID: "archive", MachineID: "machine", Agent: "pi", SessionID: "session"}, SourceID: "source", Role: "transcript", Path: p, ObservedAtNS: 1}
}

func open(t *testing.T, dir string, o Options) *Queue {
	t.Helper()
	q, e := Open(t.Context(), dir, o)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := q.Close(); e != nil {
			t.Error(e)
		}
	})
	return q
}

func capture(t *testing.T, q *Queue, r Request) publication.Manifest {
	t.Helper()
	m, e := q.Capture(t.Context(), r)
	if e != nil {
		t.Fatal(e)
	}
	return m
}

func write(t *testing.T, p, s string) {
	t.Helper()
	if e := os.WriteFile(p, []byte(s), 0o600); e != nil {
		t.Fatal(e)
	}
}

func TestEpochsAndRetainedPublication(t *testing.T) {
	dir, o, r := setup(t)
	q := open(t, dir, o)
	a := capture(t, q, r)
	write(t, r.Path, "first\nsecond\n")
	b := capture(t, q, r)
	if b.Sources[0].EpochID != a.Sources[0].EpochID || b.Sources[0].Revision != 2 {
		t.Fatal("append not proven")
	}
	write(t, r.Path, "other\nsecond\n")
	c := capture(t, q, r)
	if len(c.Sources) != 2 || c.Sources[0].State != "sealed" {
		t.Fatal("rewrite evidence lost")
	}
	write(t, r.Path, "x")
	d := capture(t, q, r)
	if len(d.Sources) != 3 {
		t.Fatal("truncation lost epoch")
	}
	backend := newBackend(t)
	if e := q.Deliver(t.Context(), backend, r.Identity); e != nil {
		t.Fatal(e)
	}
	got, e := publication.Read(t.Context(), backend, r.Identity)
	if e != nil {
		t.Fatal(e)
	}
	if got.PublicationID != d.PublicationID || len(got.Sources) != 3 {
		t.Fatal("wrong root")
	}
	st, e := q.Status(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	for _, s := range st {
		if s.State != "published" {
			t.Fatal(s)
		}
	}
}

func TestRestartAndLostAck(t *testing.T) {
	for _, boundary := range []string{"payload-before-sync", "payload-synced", "job-before-commit", "job-committed", "upload-before", "upload-after", "manifest-before", "manifest-upload-before", "manifest-upload-after", "current-before", "current-after", "current-published", "ack-before-commit", "ack-committed"} {
		t.Run(boundary, func(t *testing.T) {
			dir, o, r := setup(t)
			triggered := false
			o.Fault = func(s string) error {
				if s == boundary && !triggered {
					triggered = true
					return errors.New("crash")
				}
				return nil
			}
			q, e := Open(t.Context(), dir, o)
			if e != nil {
				t.Fatal(e)
			}
			m, capErr := q.Capture(t.Context(), r)
			backend := newBackend(t)
			if capErr == nil {
				_ = q.Deliver(t.Context(), backend, r.Identity)
			}
			if !triggered {
				t.Fatal("boundary not reached")
			}
			// Only these three boundaries precede the durable job commit. In
			// particular, job-committed returns an error after accepting bytes.
			committed := boundary != "payload-before-sync" && boundary != "payload-synced" && boundary != "job-before-commit"
			var accepted publication.Manifest
			if committed {
				accepted, _, e = q.latest(t.Context(), r.Identity)
				if e != nil || accepted.PublicationID == "" || len(accepted.Sources) != 1 {
					t.Fatalf("committed capture missing before restart: %v", e)
				}
				// Recovery cannot repair lost accepted work from the original.
				if e = os.Remove(r.Path); e != nil {
					t.Fatal(e)
				}
			}
			if e = q.Close(); e != nil {
				t.Fatal(e)
			}
			o.Fault = nil
			q = open(t, dir, o)
			st, e := q.Status(t.Context())
			if e != nil {
				t.Fatal(e)
			}
			if committed && (len(st) != 1 || st[0].Identity != r.Identity || st[0].Sequence != 1 || st[0].PublicationID != accepted.PublicationID) {
				t.Fatal("committed job lost or changed on reopen")
			}
			if len(st) == 0 {
				if capErr == nil {
					t.Fatal("acknowledged job vanished")
				}
				capture(t, q, r)
			}
			if !committed {
				if e = os.Remove(r.Path); e != nil {
					t.Fatal(e)
				}
			}
			if e = q.Deliver(t.Context(), backend, r.Identity); e != nil {
				t.Fatal(e)
			}
			got, e := publication.Read(t.Context(), backend, r.Identity)
			if e != nil {
				t.Fatal(e)
			}
			if m.PublicationID != "" && got.PublicationID != m.PublicationID {
				t.Fatal("unstable retry")
			}
			if got.Identity != r.Identity || got.Sequence != 1 || len(got.Sources) != 1 {
				t.Fatal("recovered publication identity or coverage changed")
			}
			source := got.Sources[0]
			if source.SourceID != r.SourceID || source.Role != r.Role || source.Path != r.Path || source.State != "active" || source.Revision != 1 || source.EpochID == "" {
				t.Fatal("recovered source identity changed")
			}
			if committed && (got.PublicationID != accepted.PublicationID || source.EpochID != accepted.Sources[0].EpochID) {
				t.Fatal("committed publication or epoch changed on restart")
			}
			dest := filepath.Join(t.TempDir(), "restored")
			exported, e := publication.ExportSession(t.Context(), backend, r.Identity, dest)
			if e != nil || len(exported.Bodies) != 1 {
				t.Fatalf("recovered body missing: %v", e)
			}
			body := exported.Bodies[0]
			if body.SourceID != r.SourceID || body.EpochID != source.EpochID {
				t.Fatal("recovered body coordinates changed")
			}
			raw, e := os.ReadFile(filepath.Join(dest, body.Path))
			if e != nil || string(raw) != "first\n" {
				t.Fatalf("recovered bytes differ from accepted input: %v", e)
			}
			if e = q.Deliver(t.Context(), backend, r.Identity); e != nil {
				t.Fatal(e)
			}
			st, e = q.Status(t.Context())
			if e != nil || len(st) != 1 || st[0].State != "published" || st[0].Identity != r.Identity || st[0].Sequence != 1 || st[0].PublicationID != got.PublicationID {
				t.Fatalf("recovered publication not durably acknowledged: %v", e)
			}
		})
	}
}

func TestQuotaPolicyAndIntegrityIsolation(t *testing.T) {
	for _, mode := range []string{"missing", "corrupt", "optout"} {
		t.Run(mode, func(t *testing.T) {
			dir, o, r := setup(t)
			denied := false
			o.CheckPolicy = func(_ context.Context, req Request) error {
				if denied && req.Identity.SessionID == r.Identity.SessionID {
					return errors.New("excluded")
				}
				return nil
			}
			q := open(t, dir, o)
			m := capture(t, q, r)
			switch mode {
			case "missing":
				if e := os.Remove(q.payload(m.Sources[0].Object)); e != nil {
					t.Fatal(e)
				}
			case "corrupt":
				write(t, q.payload(m.Sources[0].Object), "broken")
			case "optout":
				denied = true
			}
			backend := newBackend(t)
			e := q.Deliver(t.Context(), backend, r.Identity)
			want := ErrIntegrity
			if mode == "optout" {
				want = ErrPolicy
			}
			if !errors.Is(e, want) {
				t.Fatalf("want %v got %v", want, e)
			}
			healthy := r
			healthy.Identity.SessionID = "healthy"
			write(t, healthy.Path, "healthy distinct bytes")
			capture(t, q, healthy)
			if e = q.Deliver(t.Context(), backend, healthy.Identity); e != nil {
				t.Fatal(e)
			}
			st, e := q.Status(t.Context())
			if e != nil {
				t.Fatal(e)
			}
			if st[0].State == "published" && st[1].State == "published" {
				t.Fatal("failure hidden")
			}
		})
	}
	dir, o, r := setup(t)
	o.MaxSpoolBytes = 1
	q := open(t, dir, o)
	if _, e := q.Capture(t.Context(), r); !errors.Is(e, ErrQuota) {
		t.Fatal(e)
	}
	st, e := q.Status(t.Context())
	if e != nil || len(st) != 0 {
		t.Fatal("quota advanced acceptance", e)
	}
}

func TestFailClosedAndScrub(t *testing.T) {
	dir, o, r := setup(t)
	noPolicy := o
	noPolicy.CheckPolicy = nil
	if q, e := Open(t.Context(), dir, noPolicy); e == nil {
		_ = q.Close()
		t.Fatal("nil policy accepted")
	}
	q := open(t, dir, o)
	if q2, e := Open(t.Context(), dir, o); e == nil {
		_ = q2.Close()
		t.Fatal("second owner accepted")
	}
	for _, name := range []string{"auth.json", "settings.json", "config.toml", ".env"} {
		r.Path = filepath.Join(filepath.Dir(r.Path), name)
		write(t, r.Path, "private")
		if _, e := q.Capture(t.Context(), r); !errors.Is(e, ErrPolicy) {
			t.Fatalf("%s: %v", name, e)
		}
	}
}

func newBackend(t *testing.T) *publication.Local {
	t.Helper()
	b := publication.NewLocal(filepath.Join(t.TempDir(), "remote"))
	if e := publication.WriteArchive(t.Context(), b, publication.Archive{Contract: publication.Contract, ArchiveID: "archive"}); e != nil {
		t.Fatal(e)
	}
	return b
}

func TestFailedUpdateKeepsRootAndHistoricalPolicyIdentities(t *testing.T) {
	dir, o, r := setup(t)
	deniedPath := ""
	o.CheckPolicy = func(_ context.Context, req Request) error {
		if req.Path == deniedPath {
			return errors.New("excluded identity")
		}
		return nil
	}
	q := open(t, dir, o)
	a := capture(t, q, r)
	b := newBackend(t)
	if e := q.Deliver(t.Context(), b, r.Identity); e != nil {
		t.Fatal(e)
	}
	write(t, r.Path, "rewrite")
	capture(t, q, r)
	q.opts.Fault = func(s string) error {
		if s == "current-before" {
			return errors.New("outage")
		}
		return nil
	}
	if e := q.Deliver(t.Context(), b, r.Identity); e == nil {
		t.Fatal("update unexpectedly succeeded")
	}
	got, e := publication.Read(t.Context(), b, r.Identity)
	if e != nil {
		t.Fatal(e)
	}
	if got.PublicationID != a.PublicationID {
		t.Fatal("failed update replaced root")
	}
	q.opts.Fault = nil
	// Captured target identities survive removal of both target and alias.
	target := filepath.Join(filepath.Dir(r.Path), "target.jsonl")
	write(t, target, "alias bytes")
	alias := filepath.Join(filepath.Dir(r.Path), "alias.jsonl")
	if e = os.Symlink(target, alias); e != nil {
		t.Fatal(e)
	}
	r.Path = alias
	r.Identity.SessionID = "alias"
	capture(t, q, r)
	if e = os.Remove(alias); e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(target); e != nil {
		t.Fatal(e)
	}
	deniedPath = target
	if e = q.Deliver(t.Context(), b, r.Identity); !errors.Is(e, ErrPolicy) {
		t.Fatalf("historical alias bypassed policy: %v", e)
	}
}

// countingBackend records write attempts, including immutable-object reuse.
type countingBackend struct {
	publication.Backend
	uploads, swaps int
	syncs          int
	fenceError     error
}

func (b *countingBackend) Sync(ctx context.Context, key string) error {
	b.syncs++
	if b.fenceError != nil {
		return b.fenceError
	}
	if durable, ok := b.Backend.(publication.DurabilityFence); ok {
		return durable.Sync(ctx, key)
	}
	return nil
}

func (b *countingBackend) PutFile(ctx context.Context, key, path string) error {
	b.uploads++
	return b.Backend.PutFile(ctx, key, path)
}

func (b *countingBackend) CompareAndSwap(ctx context.Context, key, token string, body []byte) error {
	b.swaps++
	return b.Backend.CompareAndSwap(ctx, key, token, body)
}

func TestAppendRetainsHistoricalAliasPolicyAfterRestart(t *testing.T) {
	dir, o, r := setup(t)
	targetA := r.Path
	targetB := filepath.Join(filepath.Dir(targetA), "other.jsonl")
	alias := filepath.Join(filepath.Dir(targetA), "alias.jsonl")
	if err := os.Symlink(targetA, alias); err != nil {
		t.Fatal(err)
	}
	r.Path = alias
	denied := false
	o.CheckPolicy = func(_ context.Context, req Request) error {
		if denied && req.Path == targetA {
			return errors.New("excluded historical identity")
		}
		return nil
	}
	q, err := Open(t.Context(), dir, o)
	if err != nil {
		t.Fatal(err)
	}
	a := capture(t, q, r)
	b := &countingBackend{Backend: newBackend(t)}
	if err := q.Deliver(t.Context(), b, r.Identity); err != nil {
		t.Fatal(err)
	}
	write(t, targetB, "first\nsecond\n")
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(targetB, alias); err != nil {
		t.Fatal(err)
	}
	appended := capture(t, q, r)
	if len(appended.Sources) != 1 || appended.Sources[0].EpochID != a.Sources[0].EpochID || appended.Sources[0].Revision != 2 {
		t.Fatal("append changed epoch/order")
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	denied = true
	q = open(t, dir, o)
	uploads, swaps := b.uploads, b.swaps
	if err := q.Deliver(t.Context(), b, r.Identity); !errors.Is(err, ErrPolicy) {
		t.Fatalf("want policy denial: %v", err)
	}
	if b.uploads != uploads || b.swaps != swaps {
		t.Fatal("excluded historical bytes uploaded")
	}
	got, err := publication.Read(t.Context(), b, r.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if got.PublicationID != a.PublicationID {
		t.Fatal("root changed")
	}
	st, err := q.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(st) != 2 || st[0].State != "published" || st[1].State != "policy-blocked" {
		t.Fatalf("wrong progress: %+v", st)
	}
}

func TestPolicyChangeDuringManifestUploadPreventsRootCAS(t *testing.T) {
	dir, o, r := setup(t)
	denied := false
	o.CheckPolicy = func(context.Context, Request) error {
		if denied {
			return errors.New("excluded")
		}
		return nil
	}
	q := open(t, dir, o)
	a := capture(t, q, r)
	b := &countingBackend{Backend: newBackend(t)}
	if err := q.Deliver(t.Context(), b, r.Identity); err != nil {
		t.Fatal(err)
	}
	write(t, r.Path, "first\nsecond\n")
	capture(t, q, r)
	q.opts.Fault = func(boundary string) error {
		if boundary == "manifest-upload-after" {
			denied = true
		}
		return nil
	}
	swaps := b.swaps
	if err := q.Deliver(t.Context(), b, r.Identity); !errors.Is(err, ErrPolicy) {
		t.Fatalf("want policy denial: %v", err)
	}
	if b.swaps != swaps {
		t.Fatal("CAS called after exclusion")
	}
	got, err := publication.Read(t.Context(), b, r.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if got.PublicationID != a.PublicationID {
		t.Fatal("root changed")
	}
	st, err := q.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(st) != 2 || st[1].State != "policy-blocked" {
		t.Fatalf("wrong progress: %+v", st)
	}
}

func TestNegativeCaptureTimestampsDoNotAdvanceJobs(t *testing.T) {
	for _, mode := range []string{"observed", "now", "default-observed"} {
		t.Run(mode, func(t *testing.T) {
			dir, o, r := setup(t)
			q := open(t, dir, o)
			a := capture(t, q, r)
			write(t, r.Path, "first\nsecond\n")
			switch mode {
			case "observed":
				r.ObservedAtNS = -1
			case "now":
				q.opts.Now = func() time.Time { return time.Unix(0, -1) }
			case "default-observed":
				r.ObservedAtNS = 0
				q.opts.Now = func() time.Time { return time.Unix(0, -1) }
			}
			if _, err := q.Capture(t.Context(), r); err == nil {
				t.Fatal("negative timestamp accepted")
			}
			st, err := q.Status(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if len(st) != 1 || st[0].PublicationID != a.PublicationID {
				t.Fatalf("invalid capture advanced jobs: %+v", st)
			}
			r.ObservedAtNS = 2
			q.opts.Now = o.Now
			valid := capture(t, q, r)
			if valid.Sequence != a.Sequence+1 || valid.Sources[0].Revision != 2 {
				t.Fatal("invalid capture advanced source order")
			}
			// Delivery of the valid follow-up is identical across modes; check it once.
			if mode != "observed" {
				return
			}
			b := newBackend(t)
			if err := q.Deliver(t.Context(), b, r.Identity); err != nil {
				t.Fatal(err)
			}
			got, err := publication.Read(t.Context(), b, r.Identity)
			if err != nil {
				t.Fatal(err)
			}
			if got.PublicationID != valid.PublicationID {
				t.Fatal("valid followup not delivered")
			}
		})
	}
}

func TestRelativeCapturePathRemainsAbsoluteAfterRestart(t *testing.T) {
	dir, o, r := setup(t)
	absolute := r.Path
	t.Chdir(filepath.Dir(absolute))
	r.Path = filepath.Base(absolute)
	q, err := Open(t.Context(), dir, o)
	if err != nil {
		t.Fatal(err)
	}
	m := capture(t, q, r)
	if m.Sources[0].Path != absolute {
		t.Fatal("manifest retained ambiguous relative path")
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	o.CheckPolicy = func(_ context.Context, req Request) error {
		if !filepath.IsAbs(req.Path) {
			return errors.New("ambiguous provenance")
		}
		return nil
	}
	q = open(t, dir, o)
	b := newBackend(t)
	if err := q.Deliver(t.Context(), b, r.Identity); err != nil {
		t.Fatal(err)
	}
	got, err := publication.Read(t.Context(), b, r.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if got.PublicationID != m.PublicationID || got.Sources[0].Path != absolute {
		t.Fatal("restart changed provenance")
	}
}

func TestLostAckRetryForwardsDurabilityFence(t *testing.T) {
	dir, o, r := setup(t)
	q := open(t, dir, o)
	m := capture(t, q, r)
	b := &countingBackend{Backend: newBackend(t)}
	q.opts.Fault = func(boundary string) error {
		if boundary == "ack-before-commit" {
			return errors.New("lost ack")
		}
		return nil
	}
	if err := q.Deliver(t.Context(), b, r.Identity); err == nil {
		t.Fatal("missing lost ack")
	}
	q.opts.Fault = nil
	before := b.syncs
	b.fenceError = publication.ErrDurability
	if err := q.Deliver(t.Context(), b, r.Identity); !errors.Is(err, publication.ErrDurability) {
		t.Fatalf("fence failure hidden: %v", err)
	}
	if b.syncs <= before {
		t.Fatal("already-current retry bypassed durability fence")
	}
	st, err := q.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(st) != 1 || st[0].State != "retry" {
		t.Fatalf("failed fence acknowledged: %+v", st)
	}
	b.fenceError = nil
	if err := q.Deliver(t.Context(), b, r.Identity); err != nil {
		t.Fatal(err)
	}
	got, err := publication.Read(t.Context(), b, r.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if got.PublicationID != m.PublicationID {
		t.Fatal("retry changed publication")
	}
	st, err = q.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if st[0].State != "published" {
		t.Fatal("durable retry not acknowledged")
	}
}
