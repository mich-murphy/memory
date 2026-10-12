package shellwords

import (
	"errors"
	"slices"
	"testing"
)

func TestQuote(t *testing.T) {
	for in, want := range map[string]string{
		"":                  "''",
		"/usr/bin/x":        "/usr/bin/x",
		"My Tools":          "'My Tools'",
		"it's":              `'it'"'"'s'`,
		"a=b,c:d@e%f+g.h-i": "a=b,c:d@e%f+g.h-i",
	} {
		if got := Quote(in); got != want {
			t.Errorf("Quote(%q) = %s, want %s", in, got, want)
		}
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
		if _, err := Split(in); !errors.Is(err, ErrUnterminated) {
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
