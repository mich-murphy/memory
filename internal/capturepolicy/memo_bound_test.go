package capturepolicy

import (
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"
)

func memoFixture(t *testing.T, states int) (dir string, p *Policy) {
	t.Helper()
	dir = t.TempDir()
	var dirs []string
	for i := range states {
		d := filepath.Join(dir, "state"+string(rune('a'+i)))
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
		dirs = append(dirs, d)
	}
	if err := os.Symlink(dirs[0], filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "other"), 0o700); err != nil {
		t.Fatal(err)
	}
	p, err := NewSet(nil, dirs)
	if err != nil {
		t.Fatal(err)
	}
	return dir, p
}

// Stats per evaluation are bounded by the distinct paths touched, not by
// identities x state dirs; the alias ancestor is stat'ed through the injected lookup.
func TestMemoizedStatBoundIsDistinctPaths(t *testing.T) {
	dir, p := memoFixture(t, 3)
	counts := map[string]int{}
	m := p.memoizedOver(func(s string) (os.FileInfo, error) { counts[s]++; return os.Stat(s) })
	identities := []string{filepath.Join(dir, "link", "f")}
	distinct := map[string]bool{}
	for i := range 30 {
		identities = append(identities, filepath.Join(dir, "other", "sub", string(rune('a'+i%26))+string(rune('a'+i/26))))
	}
	for _, id := range identities {
		for cur := id; ; cur = filepath.Dir(cur) {
			distinct[cur] = true
			if filepath.Dir(cur) == cur {
				break
			}
		}
	}
	for _, s := range p.states {
		distinct[s] = true
	}
	for _, id := range identities {
		want := id == identities[0]
		if got := m.Excludes([]string{id}); got != want {
			t.Fatalf("Excludes(%s) = %t, want %t", id, got, want)
		}
	}
	total := 0
	for _, n := range counts {
		total += n
	}
	if total > len(distinct) {
		t.Errorf("stats = %d, want <= %d distinct paths", total, len(distinct))
	}
	if counts[filepath.Join(dir, "link")] != 1 || counts[filepath.Join(dir, "statea")] != 1 {
		t.Errorf("alias ancestor/state stats = %d/%d, want 1/1", counts[filepath.Join(dir, "link")], counts[filepath.Join(dir, "statea")])
	}
}

func TestPolicyMemoPropertyMatchesUnmemoized(t *testing.T) {
	dir, p := memoFixture(t, 3)
	m := p.Memoized()
	bases := []string{dir, filepath.Join(dir, "statea"), filepath.Join(dir, "stateb", "x"), filepath.Join(dir, "link"), filepath.Join(dir, "link", "sub"), filepath.Join(dir, "other"), filepath.Join(dir, "missing", "d")}
	leaves := []string{"session.jsonl", "auth.json", ".DS_Store", "x.bak", ".claude.json", "STATEA", "f"}
	rng := rand.New(rand.NewPCG(1, 2))
	for range 400 {
		var ids []string
		for range 1 + rng.IntN(4) {
			ids = append(ids, filepath.Join(bases[rng.IntN(len(bases))], leaves[rng.IntN(len(leaves))]))
		}
		if m.Excludes(ids) != p.Excludes(ids) || m.ExcludesClaudeConfig(ids) != p.ExcludesClaudeConfig(ids) {
			t.Fatalf("memoized decision differs for %q", ids)
		}
	}
}
