package sessioncapture

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mich-murphy/memory/internal/publication"
)

// The crash matrix re-executes the test binary and kills it with os.Exit at a
// named fault stage: no Close, no deferred cleanup, SQLite WAL left as-is. The
// parent then reopens the queue and checks what the contract requires: no torn
// job, exact bytes, no sequence regression, and no unreclaimed temporaries.

const (
	crashEnvDir   = "SESSIONCAPTURE_CRASH_DIR"
	crashEnvStage = "SESSIONCAPTURE_CRASH_STAGE"
	crashEnvPhase = "SESSIONCAPTURE_CRASH_PHASE"
	crashFirst    = "first\n"
	crashSecond   = "first\nsecond\n"
	crashExitCode = 137
)

var (
	crashCaptureStages = map[string]bool{"payload-before-sync": true, "payload-synced": true, "payload-accept-before": true, "job-before-commit": true, "job-committed": true}
	crashPreCommit     = map[string]bool{"payload-before-sync": true, "payload-synced": true, "payload-accept-before": true, "job-before-commit": true}
	// crashReclaimStages only fire when del2 retires the first published payload.
	crashReclaimStages = map[string]bool{"reclaim-before-unlink": true, "reclaim-after-unlink": true}
	crashStages        = []string{
		"payload-before-sync", "payload-synced", "payload-accept-before", "job-before-commit", "job-committed",
		"upload-before", "upload-after", "manifest-before", "manifest-upload-before",
		"manifest-upload-after", "current-before", "current-after", "current-published",
		"ack-before-commit", "ack-committed", "reclaim-before-unlink", "reclaim-after-unlink",
	}
)

func crashOptions() Options {
	return Options{ArchiveID: "archive", MaxSpoolBytes: 1 << 20, CheckPolicy: func(context.Context, Request) error { return nil }, Now: func() time.Time { return time.Unix(100, 0) }}
}

func crashRequest(src string) Request {
	return Request{Identity: publication.Identity{ArchiveID: "archive", MachineID: "machine", Agent: "pi", SessionID: "session"}, SourceID: "source", Role: "transcript", Path: src, ObservedAtNS: 1}
}

// TestCrashProcessChild is the crashing helper process; it does nothing unless
// re-executed by TestCrashProcess.
func TestCrashProcessChild(t *testing.T) {
	root := os.Getenv(crashEnvDir)
	if root == "" {
		t.Skip("helper process only")
	}
	stage, target := os.Getenv(crashEnvStage), os.Getenv(crashEnvPhase)
	src := filepath.Join(root, "transcript.jsonl")
	current := ""
	fail := func(code int, args ...any) {
		fmt.Fprintln(os.Stderr, args...)
		os.Exit(code)
	}
	o := crashOptions()
	o.Fault = func(s string) error {
		if stage != "none" && s == stage && current == target {
			os.Exit(crashExitCode)
		}
		return nil
	}
	ctx := context.Background()
	q, err := Open(ctx, filepath.Join(root, "queue"), o)
	if err != nil {
		fail(3, err)
	}
	backend := publication.NewLocal(filepath.Join(root, "remote"))
	r := crashRequest(src)
	step := func(name string, f func() error) {
		current = name
		if err := os.WriteFile(filepath.Join(root, "phase"), []byte(name), 0o600); err != nil {
			fail(2, err)
		}
		if err := f(); err != nil {
			fail(4, name, err)
		}
	}
	step("cap1", func() error { _, err := q.Capture(ctx, r); return err })
	step("del1", func() error { return q.Deliver(ctx, backend, r.Identity) })
	step("cap2", func() error {
		if err := os.WriteFile(src, []byte(crashSecond), 0o600); err != nil {
			return err
		}
		_, err := q.Capture(ctx, r)
		return err
	})
	step("del2", func() error { return q.Deliver(ctx, backend, r.Identity) })
	if err := q.Close(); err != nil {
		fail(5, err)
	}
	os.Exit(0)
}

func crashRun(t *testing.T, root, stage, target string) int {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestCrashProcessChild$", "-test.timeout=120s")
	cmd.Env = append(os.Environ(), crashEnvDir+"="+root, crashEnvStage+"="+stage, crashEnvPhase+"="+target)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return 0
	}
	exitErr, ok := errors.AsType[*exec.ExitError](err)
	if !ok {
		t.Fatalf("cannot run crash child: %v", err)
	}
	if exitErr.ExitCode() != crashExitCode {
		t.Logf("child output: %s", strings.TrimSpace(string(out)))
	}
	return exitErr.ExitCode()
}

func crashCurrent(ctx context.Context, b publication.Backend, i publication.Identity) (uint64, error) {
	m, err := publication.Read(ctx, b, i)
	if errors.Is(err, publication.ErrNotFound) {
		return 0, nil
	}
	return m.Sequence, err
}

// crashExpectation derives, from the contract alone, what must exist after the
// child died in the given phase: stages before the job commit leave the new
// sequence uncommitted, all others have it durable.
type crashExpectation struct {
	committed uint64
	final     uint64
	body      string
	pre       bool
}

func expectAfterCrash(stage, phase string) crashExpectation {
	if stage == "none" {
		return crashExpectation{committed: 2, final: 2, body: crashSecond}
	}
	e := crashExpectation{committed: map[string]uint64{"cap1": 1, "del1": 1, "cap2": 2, "del2": 2}[phase], body: crashFirst}
	e.final = e.committed
	if phase == "cap2" || phase == "del2" {
		e.body = crashSecond
	}
	if crashPreCommit[stage] {
		e.pre = true
		e.committed--
	}
	return e
}

func assertSpoolReclaimed(t *testing.T, q *Queue, label string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(q.dir, "payloads"))
	if err != nil {
		t.Fatal(err)
	}
	var physical int64
	for _, de := range entries {
		if !strings.HasSuffix(de.Name(), ".zst") {
			t.Fatalf("%s: unreclaimed temporary %s after reopen", label, de.Name())
		}
		fi, err := de.Info()
		if err != nil {
			t.Fatal(err)
		}
		physical += fi.Size()
	}
	used, err := q.usage()
	if err != nil {
		t.Fatalf("%s: usage: %v", label, err)
	}
	if used != physical {
		t.Fatalf("%s: usage %d != physical %d", label, used, physical)
	}
	if q.used != used {
		t.Fatalf("%s: accounted %d != physical %d", label, q.used, used)
	}
}

func crashCase(t *testing.T, stage string, n int) {
	t.Helper()
	root := t.TempDir()
	src := filepath.Join(root, "transcript.jsonl")
	if err := os.WriteFile(src, []byte(crashFirst), 0o600); err != nil {
		t.Fatal(err)
	}
	b := publication.NewLocal(filepath.Join(root, "remote"))
	if err := publication.WriteArchive(t.Context(), b, publication.Archive{Contract: publication.Contract, ArchiveID: "archive"}); err != nil {
		t.Fatal(err)
	}
	kind := "del"
	if crashCaptureStages[stage] {
		kind = "cap"
	}
	target := fmt.Sprintf("%s%d", kind, n)
	label := stage + "/" + target
	code := crashRun(t, root, stage, target)
	switch {
	case stage == "none" && code != 0:
		t.Fatalf("control: uncrashed child exit %d", code)
	case stage != "none" && code != crashExitCode:
		t.Fatalf("%s: crash stage not reached by child (exit %d)", label, code)
	}
	ph, err := os.ReadFile(filepath.Join(root, "phase"))
	if err != nil {
		t.Fatal(err)
	}
	want := expectAfterCrash(stage, string(ph))
	id := crashRequest(src).Identity
	seqBefore, err := crashCurrent(t.Context(), b, id)
	if err != nil {
		t.Fatalf("%s: remote unreadable after crash: %v", label, err)
	}
	q, err := Open(t.Context(), filepath.Join(root, "queue"), crashOptions())
	if err != nil {
		t.Fatalf("%s: reopen after process crash failed: %v", label, err)
	}
	t.Cleanup(func() { _ = q.Close() })
	assertSpoolReclaimed(t, q, label+" (reopen)")
	if crashReclaimStages[stage] {
		// The ack committed before the crash, whichever side of the unlink it hit:
		// reopen must leave only the second payload.
		if n := payloadFileCount(t, q); n != 1 {
			t.Fatalf("%s: %d payload files after reopen, want 1", label, n)
		}
	}
	st, err := q.Status(t.Context())
	if err != nil {
		t.Fatalf("%s: status: %v", label, err)
	}
	var maxSeq uint64
	for i := range st {
		s := &st[i]
		if s.PublicationID == "" {
			t.Fatalf("%s: torn job row %+v", label, s)
		}
		maxSeq = max(maxSeq, s.Sequence)
	}
	if maxSeq != want.committed || uint64(len(st)) != want.committed {
		t.Fatalf("%s: phase=%s jobs=%d max=%d want %d", label, ph, len(st), maxSeq, want.committed)
	}
	if want.pre {
		if _, err := q.Capture(t.Context(), crashRequest(src)); err != nil {
			t.Fatalf("%s: recapture while source exists: %v", label, err)
		}
	} else if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}
	if err := q.Deliver(t.Context(), b, id); err != nil {
		t.Fatalf("%s: drain Deliver: %v", label, err)
	}
	m, err := publication.Read(t.Context(), b, id)
	if err != nil {
		t.Fatalf("%s: read current: %v", label, err)
	}
	if m.Sequence < seqBefore {
		t.Fatalf("%s: current sequence regressed %d -> %d", label, seqBefore, m.Sequence)
	}
	if m.Sequence != want.final {
		t.Fatalf("%s: final sequence %d want %d", label, m.Sequence, want.final)
	}
	if len(m.Sources) != 1 {
		t.Fatalf("%s: %d epochs want 1", label, len(m.Sources))
	}
	dest := filepath.Join(t.TempDir(), "restored")
	exp, err := publication.ExportSession(t.Context(), b, id, dest)
	if err != nil || len(exp.Bodies) != 1 {
		t.Fatalf("%s: export: %v bodies=%d", label, err, len(exp.Bodies))
	}
	raw, err := os.ReadFile(filepath.Join(dest, exp.Bodies[0].Path))
	if err != nil || string(raw) != want.body {
		t.Fatalf("%s: exported bytes %q want %q (%v)", label, raw, want.body, err)
	}
	if st, err = q.Status(t.Context()); err != nil {
		t.Fatal(err)
	}
	for i := range st {
		s := &st[i]
		if s.State != "published" {
			t.Fatalf("%s: job %d state %s after drain", label, s.Sequence, s.State)
		}
	}
	assertSpoolReclaimed(t, q, label+" (drained)")
}

func TestCrashProcess(t *testing.T) {
	t.Run("control-no-crash", func(t *testing.T) { crashCase(t, "none", 2) })
	for _, stage := range crashStages {
		ns := []int{1, 2}
		if crashReclaimStages[stage] {
			ns = []int{2}
		}
		for _, n := range ns {
			t.Run(fmt.Sprintf("%s/seq%d", stage, n), func(t *testing.T) { crashCase(t, stage, n) })
		}
	}
}
