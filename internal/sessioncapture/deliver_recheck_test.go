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
