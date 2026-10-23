package sessioncapture

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/mich-murphy/memory/internal/publication"
)

func derivedBody(t *testing.T, q *Queue, m publication.Manifest) []byte {
	t.Helper()
	f, err := os.Open(q.payload(m.Sources[len(m.Sources)-1].Object))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	zr, err := zstd.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	body, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestDerivedZstdExactEpochsAndProvenance(t *testing.T) {
	for _, role := range []string{"main", "subagent"} {
		t.Run(role, func(t *testing.T) {
			dir, o, r := setup(t)
			r.Role = role
			r.Transform = "zstd"
			q := open(t, dir, o)
			encode := func(s string) {
				t.Helper()
				zw, err := zstd.NewWriter(nil)
				if err != nil {
					t.Fatal(err)
				}
				b := zw.EncodeAll([]byte(s), nil)
				if err := zw.Close(); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(r.Path, b, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			encode("exact\x00main\npartial")
			a := capture(t, q, r)
			if got := derivedBody(t, q, a); string(got) != "exact\x00main\npartial" {
				t.Fatalf("body mismatch: %q", got)
			}
			if a.Sources[0].Path != r.Path || !strings.Contains(strings.Join(a.Sources[0].Associations, ","), "transform:zstd") {
				t.Fatal("derived provenance lost")
			}
			b, err := q.CaptureIfChanged(t.Context(), r)
			if err != nil {
				t.Fatal(err)
			}
			if b.PublicationID != a.PublicationID {
				t.Fatal("derived no-op created publication")
			}
			encode("rewritten")
			c := capture(t, q, r)
			if len(c.Sources) != 2 {
				t.Fatal("rewrite not retained")
			}
			write(t, r.Path, "invalid zstd")
			if _, err := q.Capture(t.Context(), r); err == nil {
				t.Fatal("invalid compressed input accepted")
			}
			latest, _, err := q.latest(t.Context(), r.Identity)
			if err != nil || latest.PublicationID != c.PublicationID {
				t.Fatal("failure advanced root", err)
			}
		})
	}
}

func TestDerivedProtectedConfigFailClosed(t *testing.T) {
	for _, name := range []string{"settings.json", ".claude.json", "models.json", "config.toml"} {
		t.Run(name, func(t *testing.T) {
			dir, o, r := setup(t)
			r.Path = filepath.Join(filepath.Dir(r.Path), name)
			r.Transform = "scrubbed-config"
			q := open(t, dir, o)
			secret := "synthetic-private-secret"
			text := `{"env":{"TOKEN":"` + secret + `"}}`
			switch name {
			case ".claude.json":
				text = `{"primaryApiKey":"` + secret + `"}`
			case "models.json":
				text = `{"providers":{"local":{"apiKey":"` + secret + `"}}}`
			case "config.toml":
				text = `[mcp_servers.test.env]` + "\nTOKEN = \"" + secret + "\"\n"
			}
			write(t, r.Path, text)
			a := capture(t, q, r)
			if body := derivedBody(t, q, a); bytes.Contains(body, []byte(secret)) || !bytes.Contains(body, []byte("[redacted]")) {
				t.Fatal("config was not scrubbed")
			}
			b, err := q.CaptureIfChanged(t.Context(), r)
			if err != nil || b.PublicationID != a.PublicationID {
				t.Fatal("config no-op failed", err)
			}
			raw := r
			raw.Transform = ""
			if _, err := q.Capture(t.Context(), raw); !errors.Is(err, ErrPolicy) {
				t.Fatal("raw config accepted", err)
			}
			write(t, r.Path, "{malformed")
			if _, err := q.Capture(t.Context(), r); err == nil {
				t.Fatal("malformed config accepted")
			}
			latest, _, err := q.latest(t.Context(), r.Identity)
			if err != nil || latest.PublicationID != a.PublicationID {
				t.Fatal("malformed update advanced", err)
			}
		})
	}
}

func TestDerivedCredentialAliasAndUnknownScrubber(t *testing.T) {
	dir, o, r := setup(t)
	q := open(t, dir, o)
	r.Transform = "scrubbed-config"
	if _, err := q.Capture(t.Context(), r); !errors.Is(err, ErrPolicy) {
		t.Fatal("unknown scrubber accepted", err)
	}
	target := filepath.Join(filepath.Dir(r.Path), "auth.json")
	write(t, target, `{"secret":"synthetic"}`)
	r.Path = filepath.Join(filepath.Dir(r.Path), "settings.json")
	if err := os.Symlink(target, r.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Capture(t.Context(), r); !errors.Is(err, ErrPolicy) {
		t.Fatal("credential alias accepted", err)
	}
}

func TestDerivedSQLiteWALRestoresThroughReader(t *testing.T) {
	dir, o, r := setup(t)
	r.Transform = "sqlite-backup"
	r.Path = filepath.Join(filepath.Dir(r.Path), "session.sqlite")
	db, err := sql.Open("sqlite", r.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(t.Context(), "PRAGMA journal_mode=WAL; CREATE TABLE evidence(body TEXT); INSERT INTO evidence VALUES ('WAL row')"); err != nil {
		t.Fatal(err)
	}
	q := open(t, dir, o)
	m := capture(t, q, r)
	body := derivedBody(t, q, m)
	backend := newBackend(t)
	if err := q.Deliver(t.Context(), backend, r.Identity); err != nil {
		t.Fatal(err)
	}
	resolved, err := publication.Read(t.Context(), backend, r.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Sources[0].Path != r.Path {
		t.Fatal("SQLite provenance is not original")
	}
	exportDir := filepath.Join(t.TempDir(), "export")
	exported, err := publication.ExportSession(t.Context(), backend, r.Identity, exportDir)
	if err != nil {
		t.Fatal(err)
	}
	exact, err := os.ReadFile(filepath.Join(exportDir, exported.Bodies[0].Path))
	if err != nil || !bytes.Equal(exact, body) {
		t.Fatal("shared reader body mismatch", err)
	}
	if resolved.Sources[0].Object.SHA256 != digest(body) {
		t.Fatal("reader object mismatch")
	}
	restored := filepath.Join(t.TempDir(), "restored.sqlite")
	if err := os.WriteFile(restored, body, 0o600); err != nil {
		t.Fatal(err)
	}
	rd, err := sql.Open("sqlite", restored)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rd.Close() }()
	var got string
	if err := rd.QueryRowContext(t.Context(), "SELECT body FROM evidence").Scan(&got); err != nil || got != "WAL row" {
		t.Fatal("missing WAL data", err)
	}
}

func TestDerivedOriginalOptoutAndIndependentSessions(t *testing.T) {
	dir, o, r := setup(t)
	r.Transform = "scrubbed-config"
	r.Path = filepath.Join(filepath.Dir(r.Path), "settings.json")
	write(t, r.Path, `{"env":{"TOKEN":"synthetic"}}`)
	original := r.Path
	denied := false
	o.CheckPolicy = func(_ context.Context, req Request) error {
		if denied && req.Path == original {
			return ErrPolicy
		}
		return nil
	}
	q := open(t, dir, o)
	capture(t, q, r)
	denied = true
	if err := q.Deliver(t.Context(), newBackend(t), r.Identity); !errors.Is(err, ErrPolicy) {
		t.Fatal("original optout ignored", err)
	}
	// Exhausted prepared slots cannot block an unrelated raw capture.
	root := filepath.Join(dir, "prepared")
	for i := range 4 {
		if err := os.MkdirAll(filepath.Join(root, string(rune('0'+i))), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := q.Capture(t.Context(), r); !errors.Is(err, ErrPolicy) {
		t.Fatal(err)
	}
	r.Identity.SessionID = "independent"
	r.Path = filepath.Join(filepath.Dir(r.Path), "raw.jsonl")
	r.Transform = ""
	write(t, r.Path, "safe raw")
	capture(t, q, r)
	r.Transform = "zstd"
	if _, err := q.Capture(t.Context(), r); !errors.Is(err, ErrQuota) {
		t.Fatal("prepared orphan budget ignored", err)
	}
}

func TestDerivedSyncFailurePreservesPriorAndIndependentCapture(t *testing.T) {
	dir, o, r := setup(t)
	r.Transform = "scrubbed-config"
	r.Path = filepath.Join(filepath.Dir(r.Path), "settings.json")
	write(t, r.Path, `{"env":{"TOKEN":"synthetic-first"}}`)
	entered := make(chan struct{})
	release := make(chan struct{})
	enabled := false
	o.Fault = func(point string) error {
		if enabled && point == "prepared-before-sync" {
			close(entered)
			<-release
			return errors.New("synthetic sync failure")
		}
		return nil
	}
	q := open(t, dir, o)
	prior := capture(t, q, r)
	enabled = true
	write(t, r.Path, `{"env":{"TOKEN":"synthetic-second"}}`)
	finished := make(chan error, 1)
	go func() { _, err := q.Capture(t.Context(), r); finished <- err }()
	<-entered
	other := r
	other.Identity.SessionID = "unrelated"
	other.Transform = ""
	other.Path = filepath.Join(filepath.Dir(r.Path), "other.jsonl")
	write(t, other.Path, "independent raw")
	capture(t, q, other)
	close(release)
	if err := <-finished; err == nil {
		t.Fatal("sync failure accepted")
	}
	latest, _, err := q.latest(t.Context(), r.Identity)
	if err != nil || latest.PublicationID != prior.PublicationID {
		t.Fatal("failed prepared sync advanced prior", err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "prepared"))
	if err != nil || len(entries) != 0 {
		t.Fatal("prepared cleanup failed", err)
	}
}

func TestDerivedWorkingCapAndFreeSpace(t *testing.T) {
	dir, o, _ := setup(t)
	q := open(t, dir, o)
	var sink bytes.Buffer
	w := &preparedWriter{q: q, w: &sink, n: MaxSourceBytes - 1}
	if _, err := w.Write([]byte("xx")); !errors.Is(err, ErrQuota) || sink.Len() != 0 {
		t.Fatal("unbounded prepared write", err)
	}
	q.opts.MinFreeBytes = 1 << 62
	if _, _, err := q.preparedSlot(); !errors.Is(err, ErrQuota) {
		t.Fatal("working reservation ignored free-space gate", err)
	}
}

func TestDerivedPreparedOrphansChargedAfterRestart(t *testing.T) {
	dir, o, r := setup(t)
	q, err := Open(t.Context(), dir, o)
	if err != nil {
		t.Fatal(err)
	}
	for range 4 {
		if _, _, err := q.preparedSlot(); err != nil {
			t.Fatal(err)
		}
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	q = open(t, dir, o)
	r.Transform = "zstd"
	if _, err := q.Capture(t.Context(), r); !errors.Is(err, ErrQuota) {
		t.Fatal("restarted working orphan budget ignored", err)
	}
	r.Transform = ""
	capture(t, q, r)
}

func TestDerivedWorkingReservationsIncludeEncodedPeak(t *testing.T) {
	dir, o, _ := setup(t)
	o.MinFreeBytes = 100
	q := open(t, dir, o)
	state := q.derivedReservations()
	q.mu.Lock()
	state.active = 2
	q.used = 1000
	q.writing = 200
	n := int64(300)
	// Already-written prepared bytes are intentionally not subtracted from slots.
	required := o.MinFreeBytes + q.writing + 2*MaxSourceBytes + (o.MaxSpoolBytes - q.used)
	if err := q.checkWorkingReserveLocked(required, n); err != nil {
		t.Fatal("exact peak reservation rejected", err)
	}
	if err := q.checkWorkingReserveLocked(required-1, n); !errors.Is(err, ErrQuota) {
		t.Fatal("future encoded/working peak not reserved", err)
	}
	state.active = 0
	q.used = 0
	q.writing = 0
	q.mu.Unlock()
}

func TestDerivedWorkerGatePrecedesFilesystemInspection(t *testing.T) {
	dir, o, _ := setup(t)
	q := open(t, dir, o)
	state := q.derivedReservations()
	for range 4 {
		state.workers <- struct{}{}
	}
	// Even an unsafe prepared root is not walked once all worker tokens are held.
	if err := os.Symlink(t.TempDir(), filepath.Join(dir, "prepared")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := q.preparedSlot(); !errors.Is(err, ErrQuota) {
		t.Fatal("worker gate did filesystem work or blocked", err)
	}
	for range 4 {
		<-state.workers
	}
	if _, _, err := q.preparedSlot(); !errors.Is(err, ErrIntegrity) {
		t.Fatal("unsafe prepared root accepted", err)
	}
}

func TestDerivedCannotSubmitPreparedPathOrCredentialCopy(t *testing.T) {
	dir, o, r := setup(t)
	q := open(t, dir, o)
	path, cleanup, err := q.preparedSlot()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := cleanup(); err != nil {
			t.Error(err)
		}
	}()
	r.Path = filepath.Join(path, "body")
	write(t, r.Path, `{"env":{"TOKEN":"never raw"}}`)
	if _, err := q.Capture(t.Context(), r); !errors.Is(err, ErrPolicy) {
		t.Fatal("private prepared bytes accepted as public raw source", err)
	}
	r.Transform = "scrubbed-config"
	r.Path = filepath.Join(filepath.Dir(dir), ".claude.json.bak")
	write(t, r.Path, `{"primaryApiKey":"never raw"}`)
	if _, err := q.Capture(t.Context(), r); !errors.Is(err, ErrPolicy) {
		t.Fatal("credential backup incorrectly exempted", err)
	}
}

func TestDerivedCleanupFailureRetainsWorkingCharge(t *testing.T) {
	dir, o, r := setup(t)
	r.Transform = "scrubbed-config"
	r.Path = filepath.Join(filepath.Dir(r.Path), "settings.json")
	write(t, r.Path, `{"env":{"TOKEN":"synthetic"}}`)
	cleanupFailure := errors.New("cleanup refused")
	o.Fault = func(point string) error {
		if point == "prepared-before-cleanup" {
			return cleanupFailure
		}
		return nil
	}
	q := open(t, dir, o)
	for range 4 {
		if _, err := q.Capture(t.Context(), r); !errors.Is(err, cleanupFailure) {
			t.Fatal("cleanup failure hidden", err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(dir, "prepared"))
	if err != nil || len(entries) != 4 {
		t.Fatal("failed cleanup lost slot charge", err)
	}
	if _, err := q.Capture(t.Context(), r); !errors.Is(err, ErrQuota) {
		t.Fatal("cleanup failure released working budget", err)
	}
}
