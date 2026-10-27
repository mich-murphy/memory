package sessioncapture

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mich-murphy/memory/internal/publication"
)

// growSession captures and delivers source k=1..n (one new source per sequence)
// and returns the CheckPolicy invocations spent delivering each publication.
func growSession(t *testing.T, n int, hook func(k int)) []int64 {
	t.Helper()
	dir, o, r := setup(t)
	var calls atomic.Int64
	o.CheckPolicy = func(context.Context, Request) error { calls.Add(1); return nil }
	q := open(t, dir, o)
	b := newBackend(t)
	root := filepath.Dir(r.Path)
	out := make([]int64, 0, n)
	for k := 1; k <= n; k++ {
		req := r
		req.SourceID = fmt.Sprintf("source-%d", k)
		req.Path = filepath.Join(root, fmt.Sprintf("s%d.jsonl", k))
		write(t, req.Path, fmt.Sprintf("line %d payload\n", k))
		capture(t, q, req)
		if hook != nil {
			hook(k)
		}
		calls.Store(0)
		if e := q.Deliver(t.Context(), b, r.Identity); e != nil {
			t.Fatal(e)
		}
		out = append(out, calls.Load())
	}
	return out
}

// Policy checks per publication must be linear in session size K for a
// one-source delta: a constant number of full passes, not K passes.
func TestDeliveryPolicyChecksAreLinearInSessionSize(t *testing.T) {
	const n = 150
	counts := growSession(t, n, nil)
	for _, k := range []int{10, 50, 150} {
		t.Logf("K=%d policy checks=%d", k, counts[k-1])
	}
	for k := 1; k <= n; k++ {
		// Each full pass over K sources costs 2 checks per source (declared +
		// retained identity). Measured: 16 passes = 32K. Bound 40K leaves slack
		// but fails any O(K^2) regression (baseline 4K^2: 1270 at K=10 > 400).
		if c := counts[k-1]; c > int64(40*k) {
			t.Fatalf("publication %d used %d policy checks; want <= %d", k, c, 40*k)
		}
	}
}

// A policy exclusion appearing between captures, at upload-before for an
// uploaded object, or on a trusted source before the root commit blocks publication.
func TestDeliveryExclusionBlocksWithTrustedObjects(t *testing.T) {
	for _, stage := range []string{"between", "upload-before", "manifest-before"} {
		t.Run(stage, func(t *testing.T) {
			dir, o, r := setup(t)
			root := filepath.Dir(r.Path)
			var denied atomic.Value
			denied.Store("")
			o.CheckPolicy = func(_ context.Context, req Request) error {
				if d := denied.Load().(string); d != "" && req.Path == d {
					return errors.Join(ErrPolicy, ErrSessionExcluded)
				}
				return nil
			}
			armed := false
			seen := 0
			o.Fault = func(s string) error {
				if armed && s == stage {
					// upload-before: fire for the third (uploaded) object, not the trusted ones.
					if seen++; stage == "upload-before" && seen < 3 {
						return nil
					}
					armed = false
					denied.Store(filepath.Join(root, "s1.jsonl")) // trusted source
				}
				return nil
			}
			q := open(t, dir, o)
			b := newBackend(t)
			for k := 1; k <= 3; k++ {
				req := r
				req.SourceID = fmt.Sprintf("source-%d", k)
				req.Path = filepath.Join(root, fmt.Sprintf("s%d.jsonl", k))
				write(t, req.Path, strings.Repeat("x\n", k))
				capture(t, q, req)
				if k < 3 {
					if e := q.Deliver(t.Context(), b, r.Identity); e != nil {
						t.Fatal(e)
					}
				}
			}
			if stage == "between" {
				denied.Store(filepath.Join(root, "s1.jsonl"))
			} else {
				armed = true
			}
			_ = q.Deliver(t.Context(), b, r.Identity)
			m, err := publication.Read(t.Context(), b, r.Identity)
			if err != nil {
				t.Fatal(err)
			}
			if len(m.Sources) != 2 {
				t.Fatalf("excluded session published %d sources", len(m.Sources))
			}
		})
	}
}

// firstPub captures k sources without delivering and supersedes every job but
// the last, so one Deliver must upload all k objects (D=K, nothing trusted).
type firstPub struct {
	q     *Queue
	b     *publication.Local
	id    publication.Identity
	paths []string
	m     publication.Manifest
	calls atomic.Int64
}

func newFirstPublication(t *testing.T, k int, deny func(Request) error, fault func(string) error) *firstPub {
	t.Helper()
	dir, o, r := setup(t)
	f := &firstPub{id: r.Identity}
	o.CheckPolicy = func(_ context.Context, req Request) error {
		f.calls.Add(1)
		if deny != nil {
			return deny(req)
		}
		return nil
	}
	o.Fault = fault
	f.q = open(t, dir, o)
	f.b = newBackend(t)
	root := filepath.Dir(r.Path)
	for i := 1; i <= k; i++ {
		req := r
		req.SourceID = fmt.Sprintf("source-%d", i)
		req.Path = filepath.Join(root, fmt.Sprintf("s%d.jsonl", i))
		write(t, req.Path, fmt.Sprintf("line %d payload\n", i))
		f.m = capture(t, f.q, req)
		f.paths = append(f.paths, req.Path)
	}
	if _, err := f.q.db.ExecContext(t.Context(), `UPDATE jobs SET state='superseded' WHERE session=? AND sequence<?`, f.id.BaseKey(), f.m.Sequence); err != nil {
		t.Fatal(err)
	}
	f.calls.Store(0)
	return f
}

func (f *firstPub) keyAbsent(t *testing.T, key string) bool {
	t.Helper()
	rc, err := f.b.Open(t.Context(), key)
	if err == nil {
		_ = rc.Close()
		return false
	}
	if !errors.Is(err, publication.ErrNotFound) {
		t.Fatalf("unexpected open error: %v", err)
	}
	return true
}

func (f *firstPub) current(t *testing.T) bool {
	t.Helper()
	_, _, err := f.b.Current(t.Context(), f.id.CurrentKey())
	return err == nil
}

// One publication uploading all K objects costs a constant number of full
// passes plus one own-evidence check per upload: linear in K.
func TestFirstPublicationPolicyChecksAreLinear(t *testing.T) {
	for _, k := range []int{10, 50, 150, 378} {
		f := newFirstPublication(t, k, nil, nil)
		if e := f.q.Deliver(t.Context(), f.b, f.id); e != nil {
			t.Fatal(e)
		}
		got := f.calls.Load()
		t.Logf("K=%d D=%d policy checks=%d", k, k, got)
		// Each full pass costs 2K checks; at most ~16 fixed passes (measured
		// 32K for one-source deltas) plus a constant number per uploaded object.
		if limit := int64(40 * k); got > limit {
			t.Errorf("K=%d used %d policy checks; want <= %d", k, got, limit)
		}
	}
}

func denyPath(v *atomic.Value) func(Request) error {
	return func(req Request) error {
		if d := v.Load().(string); d != "" && req.Path == d {
			return errors.Join(ErrPolicy, ErrSessionExcluded)
		}
		return nil
	}
}

// An exclusion of the object about to upload stops its PutFile.
func TestUploadExclusionOfOwnPathPreventsPut(t *testing.T) {
	var denied atomic.Value
	denied.Store("")
	var f *firstPub
	seen := 0
	f = newFirstPublication(t, 4, denyPath(&denied), func(s string) error {
		if s == "upload-before" {
			if seen++; seen == 3 {
				denied.Store(f.paths[2])
			}
		}
		return nil
	})
	if e := f.q.Deliver(t.Context(), f.b, f.id); e == nil {
		t.Fatal("expected blocked delivery")
	}
	if !f.keyAbsent(t, f.m.Sources[2].Object.Key) {
		t.Fatal("excluded object was uploaded")
	}
	if f.current(t) {
		t.Fatal("excluded session became current")
	}
}

// An exclusion matching only a later, not-yet-uploaded source is caught by that
// source's own check before its PutFile; nothing becomes current.
func TestUploadExclusionOfLaterSourceBlocksPublication(t *testing.T) {
	var denied atomic.Value
	denied.Store("")
	var f *firstPub
	seen := 0
	f = newFirstPublication(t, 4, denyPath(&denied), func(s string) error {
		if s == "upload-before" {
			if seen++; seen == 1 {
				denied.Store(f.paths[3])
			}
		}
		return nil
	})
	if e := f.q.Deliver(t.Context(), f.b, f.id); e == nil {
		t.Fatal("expected blocked delivery")
	}
	if !f.keyAbsent(t, f.m.Sources[3].Object.Key) {
		t.Fatal("later excluded object was uploaded")
	}
	if f.current(t) {
		t.Fatal("excluded session became current")
	}
}

// A session-scope exclusion landing mid-batch blocks publication at the next
// uploaded object's own scopes check, before its PutFile.
func TestUploadSessionExclusionMidBatchBlocksPublication(t *testing.T) {
	var on atomic.Bool
	seen := 0
	f := newFirstPublication(t, 4, func(Request) error {
		if on.Load() {
			return ErrSessionExcluded
		}
		return nil
	}, func(s string) error {
		if s == "upload-before" {
			if seen++; seen == 2 {
				on.Store(true)
			}
		}
		return nil
	})
	if e := f.q.Deliver(t.Context(), f.b, f.id); e == nil {
		t.Fatal("expected blocked delivery")
	}
	if !f.keyAbsent(t, f.m.Sources[1].Object.Key) {
		t.Fatal("object uploaded after session exclusion")
	}
	if f.current(t) {
		t.Fatal("excluded session became current")
	}
}
