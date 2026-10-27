package sessioncapture

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// Synthetic before/after measurement of discovery re-hint churn through the real
// discoverOnce/captureNext path. "before" emits no fingerprint (legacy behavior).
func TestMeasureDiscoveryChurn(t *testing.T) {
	if os.Getenv("MEASURE_CHURN") == "" {
		t.Skip("set MEASURE_CHURN=1")
	}
	for _, withFP := range []bool{false, true} {
		dir, o, req := setup(t)
		o.MaxInventoryRecords = 4096
		q := open(t, dir, o)
		if err := q.InitRuntime(t.Context()); err != nil {
			t.Fatal(err)
		}
		root := t.TempDir()
		const n = 2000
		var reqs []Request
		for i := range n {
			p := filepath.Join(root, fmt.Sprintf("s%04d.jsonl", i))
			if err := os.WriteFile(p, []byte("hello\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			r := req
			r.SourceID, r.Path = fmt.Sprintf("src-%d", i), p
			reqs = append(reqs, r)
		}
		sweep := 0
		opts := runtimeDefaults(RuntimeOptions{Discover: func(context.Context) ([]Request, error) {
			sweep++
			out := make([]Request, len(reqs))
			for i, r := range reqs {
				r.ObservedAtNS = int64(1000 + sweep)
				if withFP {
					info, _ := os.Stat(r.Path)
					st := info.Sys().(*syscall.Stat_t)
					r.Fingerprint = fmt.Sprintf("%d:%d:%d:%v", st.Dev, st.Ino, st.Size, info.ModTime().UnixNano())
				}
				out[i] = r
			}
			return out, nil
		}})
		var prev int64
		for s := 1; s <= 4; s++ {
			start := time.Now()
			if err := q.discoverOnce(t.Context(), opts); err != nil {
				t.Fatal(err)
			}
			disc := time.Since(start)
			var sum int64
			_ = q.db.QueryRowContext(t.Context(), `SELECT SUM(version) FROM runtime_sources`).Scan(&sum)
			bumps := sum - prev
			if s == 1 {
				bumps = 0 // initial insert
			}
			prev = sum
			caps := 0
			cs := time.Now()
			for {
				ok, err := q.captureNext(t.Context(), runtimeDefaults(RuntimeOptions{}))
				if err != nil {
					t.Fatal(err)
				}
				if !ok {
					break
				}
				caps++
			}
			t.Logf("fingerprint=%v sweep=%d version_bumps=%d capture_attempts=%d discover=%v capture=%v", withFP, s, bumps, caps, disc, time.Since(cs))
		}
	}
}
