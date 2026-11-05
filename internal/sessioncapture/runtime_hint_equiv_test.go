package sessioncapture

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/mich-murphy/memory/internal/publication"
)

// hintModel is the reference semantics of batch admission: an existing source
// coalesces (a changed fingerprint bumps its version, an unchanged one is a no-op),
// a new source needs a free live slot, and refusals never block other requests.
type hintModel struct {
	cap  int
	rows map[string]*modelRow
}

type modelRow struct {
	version int
	fp      string
}

// apply returns the expected admitted sessions and error classes for one chunk.
type hintSpec struct {
	kind    int // 0 normal, 1 invalid, 2 oversized
	session int
	fp      string
}

func (m *hintModel) apply(specs []hintSpec) (admitted []string, quota, other bool) {
	for _, s := range specs {
		switch s.kind {
		case 1:
			other = true
		case 2:
			quota = true
		default:
			key := fmt.Sprintf("s%d", s.session)
			if r, ok := m.rows[key]; ok {
				if r.fp != s.fp {
					r.fp = s.fp
					r.version++
				}
				admitted = append(admitted, key)
			} else if len(m.rows) < m.cap {
				m.rows[key] = &modelRow{version: 1, fp: s.fp}
				admitted = append(admitted, key)
			} else {
				quota = true
			}
		}
	}
	return admitted, quota, other
}

func hintRequest(base Request, s hintSpec) Request {
	r := base
	r.Identity.SessionID = fmt.Sprintf("s%d", s.session)
	r.SourceID = "src"
	r.Fingerprint = s.fp
	switch s.kind {
	case 1:
		r.Path = ""
	case 2:
		r.Path = "/" + strings.Repeat("p", MaxHintBytes)
	}
	return r
}

func TestAdmitChunkMatchesReferenceModel(t *testing.T) {
	for seed := uint64(1); seed <= 6; seed++ {
		t.Run(fmt.Sprint("seed", seed), func(t *testing.T) {
			dir, o, base := setup(t)
			o.MaxInventoryRecords = 4
			q := open(t, dir, o)
			if err := q.InitRuntime(t.Context()); err != nil {
				t.Fatal(err)
			}
			rng := rand.New(rand.NewPCG(seed, 7))
			model := &hintModel{cap: 4, rows: map[string]*modelRow{}}
			for batch := range 40 {
				specs := make([]hintSpec, 1+rng.IntN(7))
				for i := range specs {
					specs[i] = hintSpec{session: rng.IntN(9), fp: fmt.Sprint("f", rng.IntN(3))}
					if k := rng.IntN(12); k == 0 {
						specs[i].kind = 1
					} else if k == 1 {
						specs[i].kind = 2
					}
				}
				reqs := make([]Request, len(specs))
				for i, s := range specs {
					reqs[i] = hintRequest(base, s)
				}
				wantAdmitted, wantQuota, wantOther := model.apply(specs)
				admitted, err := q.admitChunk(t.Context(), reqs)
				if (err != nil) != (wantQuota || wantOther) || errors.Is(err, ErrQuota) != wantQuota {
					t.Fatalf("batch %d: err %v, want quota=%v other=%v", batch, err, wantQuota, wantOther)
				}
				var gotAdmitted []string
				for _, h := range admitted {
					gotAdmitted = append(gotAdmitted, h.req.Identity.SessionID)
				}
				if diff := cmp.Diff(wantAdmitted, gotAdmitted); diff != "" {
					t.Fatalf("batch %d admitted (-want +got):\n%s", batch, diff)
				}
				want := map[string]int{}
				for k, r := range model.rows {
					want[k] = r.version
				}
				if diff := cmp.Diff(want, runtimeSourceVersions(t, q)); diff != "" {
					t.Fatalf("batch %d rows (-want +got):\n%s", batch, diff)
				}
			}
		})
	}
}

func runtimeSourceVersions(t *testing.T, q *Queue) map[string]int {
	t.Helper()
	rows, err := q.db.QueryContext(t.Context(), `SELECT session,version FROM runtime_sources`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	got := map[string]int{}
	for rows.Next() {
		var key string
		var version int
		if err := rows.Scan(&key, &version); err != nil {
			t.Fatal(err)
		}
		// Session keys are BaseKey values; the trailing component is the hex session id.
		id, err := hex.DecodeString(key[strings.LastIndex(key, "/")+1:])
		if err != nil {
			t.Fatal(err)
		}
		got[string(id)] = version
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return got
}

// The single-Hint path checks the live cap per statement; a refused new source
// reports ErrQuota while an existing one still coalesces at capacity.
func TestHintSingleLiveCapRefusesNewButCoalescesExisting(t *testing.T) {
	dir, o, base := setup(t)
	o.MaxInventoryRecords = 2
	q := open(t, dir, o)
	for i := range 2 {
		if err := q.Hint(t.Context(), hintRequest(base, hintSpec{session: i, fp: "a"})); err != nil {
			t.Fatal(err)
		}
	}
	if err := q.Hint(t.Context(), hintRequest(base, hintSpec{session: 2, fp: "a"})); !errors.Is(err, ErrQuota) {
		t.Fatalf("new source at cap: %v, want ErrQuota", err)
	}
	if err := q.Hint(t.Context(), hintRequest(base, hintSpec{session: 0, fp: "b"})); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(map[string]int{"s0": 2, "s1": 1}, runtimeSourceVersions(t, q)); diff != "" {
		t.Fatalf("rows (-want +got):\n%s", diff)
	}
}

// BenchmarkHintBatchUnchanged is one settled discovery sweep: every request is
// already admitted with an unchanged fingerprint, so nothing is written.
func BenchmarkHintBatchUnchanged(b *testing.B) {
	root := b.TempDir()
	o := Options{ArchiveID: "archive", MaxSpoolBytes: 1 << 20, CheckPolicy: func(context.Context, Request) error { return nil }, Now: func() time.Time { return time.Unix(100, 0) }}
	q, err := Open(b.Context(), root+"/queue", o)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := q.Close(); err != nil {
			b.Error(err)
		}
	})
	base := Request{Identity: publication.Identity{ArchiveID: "archive", MachineID: "machine", Agent: "pi"}, Role: "transcript", Path: root + "/t.jsonl", ObservedAtNS: 1}
	reqs := make([]Request, 4000)
	for i := range reqs {
		reqs[i] = base
		reqs[i].Identity.SessionID = fmt.Sprint("s", i)
		reqs[i].SourceID = "src"
		reqs[i].Fingerprint = "fp"
	}
	if err := q.HintBatch(b.Context(), reqs); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := q.HintBatch(b.Context(), reqs); err != nil {
			b.Fatal(err)
		}
	}
}
