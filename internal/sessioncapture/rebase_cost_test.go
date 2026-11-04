package sessioncapture

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"slices"
	"testing"

	"github.com/mich-murphy/memory/internal/publication"
)

// costRemote builds n distinct sources; with mixed, every other one is sealed.
func costRemote(n int, mixed bool) publication.Manifest {
	m := publication.Manifest{Sequence: 10}
	for i := range n {
		s := rebaseSource(fmt.Sprintf("src-%d", i), "e1", 1, fmt.Sprintf("h%d", i))
		if mixed && i%2 == 1 {
			s.State = "sealed"
		}
		m.Sources = append(m.Sources, s)
	}
	return m
}

func BenchmarkRebaseManifest(b *testing.B) {
	for _, n := range []int{1, 8, 64, 256, 1024} {
		for _, mixed := range []bool{false, true} {
			name := fmt.Sprintf("sources=%d/active", n)
			if mixed {
				name = fmt.Sprintf("sources=%d/mixed", n)
			}
			remote := costRemote(n, mixed)
			// The local source advances an existing epoch: the merge and the seal pass run
			// without growing the manifest, so the 1024-source cap is not exceeded.
			local := publication.Manifest{Sequence: 1, Sources: []publication.Source{rebaseSource("src-0", "e1", 2, "l0")}}
			b.Run(name, func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					if _, _, _, err := rebaseManifest(remote, local); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// BenchmarkRebaseManifestRepeated rebases K consecutive CURRENT moves before an ACK.
func BenchmarkRebaseManifestRepeated(b *testing.B) {
	const k = 8
	for _, n := range []int{8, 64, 256} {
		remote := costRemote(n, false)
		b.Run(fmt.Sprintf("sources=%d/k=%d", n, k), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				cur := remote
				for step := range k {
					local := publication.Manifest{Sequence: 1, Sources: []publication.Source{rebaseSource("src-0", fmt.Sprintf("n%d", step), 1, "l")}}
					m, _, _, err := rebaseManifest(cur, local)
					if err != nil {
						b.Fatal(err)
					}
					cur = m
				}
			}
		})
	}
}

// sealSupersededOracle is the verbatim quadratic implementation the linear one replaced.
func sealSupersededOracle(out []publication.Source, remoteCount int) {
	for i := range remoteCount {
		if out[i].State != "active" {
			continue
		}
		for j := range out {
			if j != i && out[j].State == "active" && publication.SourceKeyHash(out[j]) == publication.SourceKeyHash(out[i]) && out[j].EpochID != out[i].EpochID {
				out[i].State = "sealed"
				break
			}
		}
	}
}

func randomSealSources(r *rand.Rand) []publication.Source {
	out := make([]publication.Source, r.IntN(24))
	for i := range out {
		s := rebaseSource(fmt.Sprintf("s%d", r.IntN(5)), fmt.Sprintf("e%d", r.IntN(4)), 1, "h")
		s.State = []string{"active", "active", "sealed", "other"}[r.IntN(4)]
		if r.IntN(6) == 0 {
			// Withheld sources key on the recorded hash, which may collide with a plain source's.
			s.Withheld = &publication.WithheldSource{SourceIDHash: publication.SourceKeyHash(rebaseSource(fmt.Sprintf("s%d", r.IntN(5)), "", 1, ""))}
		}
		out[i] = s
	}
	return out
}

func TestSealSupersededMatchesQuadraticOracle(t *testing.T) {
	t.Parallel()
	r := rand.New(rand.NewPCG(7, 11))
	for n := range 600 {
		want := randomSealSources(r)
		got := slices.Clone(want)
		remote := r.IntN(len(want) + 1)
		sealSupersededOracle(want, remote)
		sealSuperseded(got, remote)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("case %d remote=%d: got %+v want %+v", n, remote, got, want)
		}
	}
}

func TestSealSupersededEarlierSealDoesNotSealLater(t *testing.T) {
	t.Parallel()
	out := []publication.Source{rebaseSource("a", "e1", 1, "x"), rebaseSource("a", "e2", 1, "y")}
	sealSuperseded(out, 2)
	if out[0].State != "sealed" || out[1].State != "active" {
		t.Fatalf("states %s %s", out[0].State, out[1].State)
	}
}
