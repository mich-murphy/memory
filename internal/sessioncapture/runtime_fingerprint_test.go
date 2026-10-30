package sessioncapture

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
)

type srcRow struct {
	version int64
	state   string
	observe string
}

func rowOf(t *testing.T, q *Queue, r Request) srcRow {
	t.Helper()
	var s srcRow
	err := q.db.QueryRowContext(t.Context(), `SELECT version,state,observed_state FROM runtime_sources WHERE session=? AND source=?`, r.Identity.BaseKey(), r.SourceID).Scan(&s.version, &s.state, &s.observe)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func capturedOnce(t *testing.T, q *Queue, r Request) {
	t.Helper()
	if err := q.Hint(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	if ok, err := q.captureNext(t.Context(), runtimeDefaults(RuntimeOptions{})); err != nil || !ok {
		t.Fatalf("capture %v %v", ok, err)
	}
	if s := rowOf(t, q, r); s.state != "captured" {
		t.Fatalf("%+v", s)
	}
}

func TestHintFingerprintUnchangedIsNoop(t *testing.T) {
	dir, o, req := setup(t)
	q := open(t, dir, o)
	req.Fingerprint = "fp1"
	capturedOnce(t, q, req)
	before := rowOf(t, q, req)
	for i := range 3 {
		req.ObservedAtNS = int64(100 + i)
		if err := q.Hint(t.Context(), req); err != nil {
			t.Fatal(err)
		}
	}
	if after := rowOf(t, q, req); after != before {
		t.Fatalf("%+v != %+v", after, before)
	}
	if ok, err := q.captureNext(t.Context(), runtimeDefaults(RuntimeOptions{})); err != nil || ok {
		t.Fatalf("extra capture %v %v", ok, err)
	}
	// Changed fingerprint, changed request field bump.
	req.Fingerprint = "fp2"
	if err := q.Hint(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if s := rowOf(t, q, req); s.version != before.version+1 || s.state != "pending" {
		t.Fatalf("%+v", s)
	}
	capturedOnce(t, q, req)
	v := rowOf(t, q, req).version
	req.Role = "main"
	if err := q.Hint(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if s := rowOf(t, q, req); s.version != v+1 {
		t.Fatalf("role change must bump: %+v", s)
	}
}

func TestHintWithoutFingerprintAlwaysBumps(t *testing.T) {
	dir, o, req := setup(t)
	q := open(t, dir, o)
	capturedOnce(t, q, req) // empty fingerprint (hooks, manual, transform/WAL sources)
	v := rowOf(t, q, req).version
	for i := range 2 {
		if err := q.Hint(t.Context(), req); err != nil {
			t.Fatal(err)
		}
		if s := rowOf(t, q, req); s.version != v+int64(i)+1 || s.state != "pending" {
			t.Fatalf("%+v", s)
		}
	}
}

func TestHintFingerprintAbsentThenPresentBumps(t *testing.T) {
	dir, o, req := setup(t)
	q := open(t, dir, o)
	req.Fingerprint = "fp1"
	capturedOnce(t, q, req)
	v := rowOf(t, q, req).version
	if err := q.HintAbsent(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if s := rowOf(t, q, req); s.observe != "absent" || s.state != "absent" {
		t.Fatalf("%+v", s)
	}
	if err := q.Hint(t.Context(), req); err != nil { // same fingerprint, now present
		t.Fatal(err)
	}
	if s := rowOf(t, q, req); s.observe != "present" || s.state != "pending" || s.version <= v {
		t.Fatalf("%+v", s)
	}
}

func TestHintFingerprintDuringInflightClaimStillCaptured(t *testing.T) {
	dir, o, req := setup(t)
	req.Fingerprint = "fp1"
	var q *Queue
	armed := false
	fired := false
	o.CheckPolicy = func(ctx context.Context, _ Request) error {
		if armed && !fired {
			fired = true
			r := req
			r.ObservedAtNS = 999
			if err := q.Hint(ctx, r); err != nil {
				return err
			}
		}
		return nil
	}
	q = open(t, dir, o)
	if err := q.Hint(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	armed = true
	if ok, err := q.captureNext(t.Context(), runtimeDefaults(RuntimeOptions{})); err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
	if !fired {
		t.Fatal("sweep did not land during capture")
	}
	if s := rowOf(t, q, req); s.state != "captured" || s.version != 1 {
		t.Fatalf("%+v", s)
	}
}

func TestHintFingerprintInventoryCap(t *testing.T) {
	dir, o, req := setup(t)
	o.MaxInventoryRecords = 1
	q := open(t, dir, o)
	req.Fingerprint = "fp1"
	if err := q.Hint(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if err := q.Hint(t.Context(), req); err != nil {
		t.Fatalf("unchanged existing source at cap: %v", err)
	}
	other := req
	other.SourceID = "other"
	if err := q.Hint(t.Context(), other); !errors.Is(err, ErrQuota) {
		t.Fatalf("new source at cap: %v", err)
	}
	_ = fmt.Sprint
	_ = os.Stat
}
