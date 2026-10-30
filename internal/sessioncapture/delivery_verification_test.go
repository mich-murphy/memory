package sessioncapture

import (
	"context"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/mich-murphy/memory/internal/publication"
)

// Count only contextReader calls inside the local immutable verifier, not the
// publisher's independent remote dependency reads.
type verificationCountContext struct {
	context.Context
	reads atomic.Int64
}

func (c *verificationCountContext) Err() error {
	pcs := make([]uintptr, 24)
	n := runtime.Callers(2, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	reader, verifier := false, false
	for {
		f, more := frames.Next()
		reader = reader || strings.Contains(f.Function, "contextReader")
		verifier = verifier || strings.HasSuffix(f.Function, ".(*Queue).verify")
		if !more {
			break
		}
	}
	if reader && verifier {
		c.reads.Add(1)
	}
	return c.Context.Err()
}

func TestAttemptVerifiesPayloadsOnce(t *testing.T) {
	dir, o, r := setup(t)
	q := open(t, dir, o)
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	capture(t, q, r)
	write(t, r.Path, "rewritten\n")
	capture(t, q, r) // Includes a retained sealed epoch.
	r.SourceID = "artifact"
	r.Role = "artifact"
	r.Optional = true
	write(t, r.Path, "optional bytes\n")
	m := capture(t, q, r)
	_, es, err := q.latest(t.Context(), r.Identity)
	if err != nil {
		t.Fatal(err)
	}
	counter := &verificationCountContext{Context: t.Context()}
	var expected, encoded int64
	for _, s := range m.Sources {
		if publication.IsSelected(s) {
			before := counter.reads.Load()
			if err := q.verify(counter, s.Object); err != nil {
				t.Fatal(err)
			}
			expected += counter.reads.Load() - before
			encoded += s.Object.EncodedSize
		}
	}
	if expected <= 0 {
		t.Fatal("local verifier read calibration did not observe any reads")
	}
	counter.reads.Store(0)
	if err := q.attemptManifest(counter, newBackend(t), r.Identity, m.Sequence, m, es); err != nil {
		t.Fatal(err)
	}
	actual := counter.reads.Load()
	t.Logf("selected=%d encoded_bytes=%d verify_read_checks=%d single_pass_checks=%d", len(m.Sources), encoded, actual, expected)
	if actual != expected {
		t.Fatalf("duplicate immutable verification: reads=%d want=%d", actual, expected)
	}
}

// Bounded synthetic cost probe. Not a production capacity/latency benchmark.
func TestAttemptSyntheticVerificationCost(t *testing.T) {
	dir, o, r := setup(t)
	o.MaxSpoolBytes = 32 << 20
	q := open(t, dir, o)
	if err := q.InitRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	// Deterministic incompressible payload; three selected objects including sealed history.
	data := make([]byte, 1<<20)
	var seed uint64 = 0x12345678
	for j := range data {
		seed ^= seed << 13
		seed ^= seed >> 7
		seed ^= seed << 17
		data[j] = byte(seed)
	}
	var m publication.Manifest
	for j := range 3 {
		data[0] = byte(j)
		if err := os.WriteFile(r.Path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if j == 2 {
			r.SourceID = "optional"
			r.Role = "artifact"
			r.Optional = true
		}
		m = capture(t, q, r)
	}
	_, es, err := q.latest(t.Context(), r.Identity)
	if err != nil {
		t.Fatal(err)
	}
	var encoded int64
	for _, s := range m.Sources {
		encoded += s.Object.EncodedSize
	}
	// Warm the fixed fixture once, then measure 5 identical actual attempts using a
	// fresh local destination each time. Includes local backend/publisher overhead.
	run := func() {
		if err := q.attemptManifest(t.Context(), newBackend(t), r.Identity, m.Sequence, m, es); err != nil {
			t.Fatal(err)
		}
	}
	run()
	var before, after syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &before); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	for range 5 {
		run()
	}
	elapsed := time.Since(started)
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &after); err != nil {
		t.Fatal(err)
	}
	cpu := func(r syscall.Rusage) time.Duration {
		return time.Duration(r.Utime.Sec+r.Stime.Sec)*time.Second + time.Duration(r.Utime.Usec+r.Stime.Usec)*time.Microsecond
	}
	t.Logf("objects=%d raw_bytes=%d encoded_bytes=%d attempts=5 wall=%s process_cpu=%s", len(m.Sources), len(data)*3, encoded, elapsed, cpu(after)-cpu(before))
}

func TestAttemptCorruptionAfterLocalVerificationCannotPublish(t *testing.T) {
	dir, o, r := setup(t)
	q := open(t, dir, o)
	first := capture(t, q, r)
	backend := newBackend(t)
	if err := q.Deliver(t.Context(), backend, r.Identity); err != nil {
		t.Fatal(err)
	}
	write(t, r.Path, "first\nnew bytes\n")
	m := capture(t, q, r)
	_, es, err := q.latest(t.Context(), r.Identity)
	if err != nil {
		t.Fatal(err)
	}
	corrupted := false
	q.opts.Fault = func(point string) error {
		if point == "upload-before" && !corrupted {
			corrupted = true
			return os.WriteFile(q.payload(m.Sources[0].Object), []byte("corrupt after local verification"), 0o600)
		}
		return nil
	}
	if err := q.attemptManifest(t.Context(), backend, r.Identity, m.Sequence, m, es); err == nil {
		t.Fatal("corrupt dependency published")
	}
	if !corrupted {
		t.Fatal("corruption boundary not reached")
	}
	current, err := publication.Read(t.Context(), backend, r.Identity)
	if err != nil || current.PublicationID != first.PublicationID {
		t.Fatalf("previous publication not retained: %v", err)
	}
}
