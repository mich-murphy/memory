package capturepolicy

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func setFixture(t *testing.T) (configs, states, ids []string) {
	t.Helper()
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	writeSynthetic(t, filepath.Join(real, "cfg", "config.json"))
	writeSynthetic(t, filepath.Join(real, "state", "spool", "x"))
	writeSynthetic(t, filepath.Join(real, "other", "session.jsonl"))
	writeSynthetic(t, filepath.Join(real, "other", ".claude.json"))
	writeSynthetic(t, filepath.Join(real, "other", "auth.json"))
	writeSynthetic(t, filepath.Join(real, "other", ".DS_Store"))
	linkSynthetic(t, filepath.Join(real, "cfg", "config.json"), filepath.Join(dir, "cfglink.json"))
	linkSynthetic(t, filepath.Join(real, "state"), filepath.Join(dir, "statelink"))
	linkSynthetic(t, real, filepath.Join(dir, "parentlink"))
	configs = []string{filepath.Join(dir, "cfglink.json"), filepath.Join(dir, "missing", "config.json"), ""}
	states = []string{filepath.Join(dir, "statelink"), filepath.Join(dir, "parentlink", "state", "spool"), filepath.Join(dir, "nostate")}
	for _, p := range []string{
		filepath.Join(real, "cfg", "config.json"), filepath.Join(dir, "cfglink.json"),
		filepath.Join(dir, "missing", "config.json"), filepath.Join(dir, "missing", "config.json.bak"),
		filepath.Join(real, "state", "spool", "x"), filepath.Join(dir, "statelink", "spool", "x"),
		filepath.Join(dir, "parentlink", "state", "spool", "x"), filepath.Join(dir, "nostate", "y"),
		filepath.Join(real, "other", "session.jsonl"), filepath.Join(real, "other", ".claude.json"),
		filepath.Join(real, "other", "auth.json"), filepath.Join(real, "other", ".DS_Store"),
		filepath.Join(dir, "parentlink", "other", "session.jsonl"),
	} {
		ids = append(ids, p)
		if r, _ := Resolve(p); r.Identities != nil {
			ids = append(ids, r.Identities...)
		}
	}
	return
}

func TestNewSetEqualsOrOverPairs(t *testing.T) {
	configs, states, ids := setFixture(t)
	set, err := NewSet(configs, states)
	if err != nil {
		t.Fatal(err)
	}
	var pairs []*Policy
	for _, c := range configs {
		for _, s := range states {
			p, err := New(c, s)
			if err != nil {
				t.Fatal(err)
			}
			pairs = append(pairs, p)
		}
	}
	any := func(f func(*Policy) bool) bool {
		for _, p := range pairs {
			if f(p) {
				return true
			}
		}
		return false
	}
	trueSeen, falseSeen := 0, 0
	for _, id := range ids {
		one := []string{id}
		w1 := any(func(p *Policy) bool { return p.Excludes(one) })
		w2 := any(func(p *Policy) bool { return p.ExcludesClaudeConfig(one) })
		if g := set.Excludes(one); g != w1 {
			t.Errorf("Excludes(%q)=%v want %v", id, g, w1)
		}
		if g := set.ExcludesClaudeConfig(one); g != w2 {
			t.Errorf("ExcludesClaudeConfig(%q)=%v want %v", id, g, w2)
		}
		if w1 {
			trueSeen++
		} else {
			falseSeen++
		}
	}
	if trueSeen == 0 || falseSeen == 0 {
		t.Fatalf("fixture not discriminating: %d/%d", trueSeen, falseSeen)
	}
	// Whole-slice calls must agree too.
	if set.Excludes(ids) != any(func(p *Policy) bool { return p.Excludes(ids) }) {
		t.Error("slice Excludes differs")
	}
	// States alone must exclude something configs alone do not.
	stateOnly, _ := NewSet(nil, states)
	if !stateOnly.Excludes([]string{filepath.Join(states[0], "spool", "x")}) {
		t.Error("state identity not excluded")
	}
}

func TestNewSetErrorEquivalence(t *testing.T) {
	configs, states, _ := setFixture(t)
	loop := t.TempDir()
	linkSynthetic(t, "b", filepath.Join(loop, "a"))
	linkSynthetic(t, "a", filepath.Join(loop, "b"))
	long := t.TempDir()
	for i := range maxSymlinks + 1 {
		linkSynthetic(t, "c"+strconv.Itoa(i+1), filepath.Join(long, "c"+strconv.Itoa(i)))
	}
	for _, bad := range []string{filepath.Join(loop, "a"), filepath.Join(long, "c0")} {
		for pos := range configs {
			cs := append([]string(nil), configs...)
			cs[pos] = bad
			if _, err := NewSet(cs, states); err == nil {
				t.Errorf("bad config %q at %d accepted", bad, pos)
			}
		}
		for pos := range states {
			ss := append([]string(nil), states...)
			ss[pos] = bad
			if _, err := NewSet(configs, ss); err == nil {
				t.Errorf("bad state %q at %d accepted", bad, pos)
			}
		}
	}
	if _, err := os.Stat(configs[0]); err != nil {
		t.Fatal(err)
	}
}

func TestNewSetResolvesEachDistinctLocationOnce(t *testing.T) {
	counts := map[string]int{}
	resolve := func(p string) (Resolution, error) {
		counts[p]++
		return Resolve(p)
	}
	dir := t.TempDir()
	cs := []string{filepath.Join(dir, "c1"), filepath.Join(dir, "c2"), ""}
	ss := []string{filepath.Join(dir, "s1"), filepath.Join(dir, "s2"), filepath.Join(dir, "s3")}
	if _, err := newSet(resolve, cs, ss); err != nil {
		t.Fatal(err)
	}
	if len(counts) != 5 {
		t.Errorf("resolved %d distinct paths, want 5: %v", len(counts), counts)
	}
	for p, n := range counts {
		if n != 1 {
			t.Errorf("%s resolved %d times", p, n)
		}
	}
}
