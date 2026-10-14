package shellwords

import (
	"errors"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

func TestJoinIsReadBackByAShell(t *testing.T) {
	words := []string{"", "/usr/bin/x", "My Tools", "it's", `a"b`, "$HOME", `back\slash`, "*", "~/x", "two\nlines", "a=b,c:d@e%f+g.h-i"}
	out, err := exec.CommandContext(t.Context(), "sh", "-c", `printf '%s\0' `+Join(words)).Output()
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
	if !slices.Equal(got, words) {
		t.Errorf("sh read %q back as %q", Join(words), got)
	}
}

func TestSplit(t *testing.T) {
	for in, want := range map[string][]string{
		`python3 ~/bin/x.py --flag`:    {"python3", "~/bin/x.py", "--flag"},
		`'a b' "c \"d\" \$e \x" f\ g`:  {"a b", `c "d" $e \x`, "f g"},
		"  spaced\tout\n":              {"spaced", "out"},
		`""`:                           {""},
		`bash -c 'echo "$HOME"; exit'`: {"bash", "-c", `echo "$HOME"; exit`},
		`mixed'quo'"tes"`:              {"mixedquotes"},
	} {
		got, err := Split(in)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("Split(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{`'open`, `"open`, `"esc\"`} {
		if _, err := Split(in); !errors.Is(err, errUnterminated) {
			t.Errorf("Split(%q) error = %v", in, err)
		}
	}
}

func FuzzJoinSplit(f *testing.F) {
	f.Add("a", "b c", "it's")
	f.Add("", "\"", "\\")
	f.Fuzz(func(t *testing.T, a, b, c string) {
		words := []string{a, b, c}
		got, err := Split(Join(words))
		if err != nil || !slices.Equal(got, words) {
			t.Fatalf("Split(Join(%q)) = %q, %v", words, got, err)
		}
	})
}
