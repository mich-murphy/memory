package gitinfo

import (
	"strings"
	"testing"
)

func TestNormalizeRemote(t *testing.T) {
	for _, url := range []string{
		"https://github.com/Me/proj.git", "git@github.com:Me/proj.git", "ssh://git@github.com:22/Me/proj",
		"https://x:tok@GitHub.com/Me/proj/",
	} {
		if got := normalizeRemote(url); got != "github.com/Me/proj" {
			t.Errorf("normalizeRemote(%q) = %q", url, got)
		}
	}
	for url, want := range map[string]string{
		"https://x:tok@github.com/a/b":   "https://github.com/a/b",
		"https://user:p@ss@github.com/a": "https://github.com/a",
		"https://github.com/a@b":         "https://github.com/a@b",
		"https://github.com?q=a@b":       "https://github.com?q=a@b",
	} {
		if got := stripUserinfo(url); got != want {
			t.Errorf("stripUserinfo(%q) = %q, want %q", url, got, want)
		}
	}
}

func FuzzStripUserinfo(f *testing.F) {
	for _, seed := range []string{
		"https://x:tok@github.com/a/b", "https://u:p@ss@h/a", "ssh://git@h:22/p", "https://h/a@b", "git@h:p", "",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, url string) {
		got := stripUserinfo(url)
		if stripUserinfo(got) != got {
			t.Errorf("not idempotent: %q -> %q -> %q", url, got, stripUserinfo(got))
		}
		if strings.Contains(authority(got), "@") {
			t.Errorf("stripUserinfo(%q) = %q keeps userinfo", url, got)
		}
	})
}

func authority(url string) string {
	loc := schemeRE.FindStringIndex(url)
	if loc == nil {
		return ""
	}
	rest := url[loc[1]:]
	if end := strings.IndexAny(rest, "/?#"); end >= 0 {
		return rest[:end]
	}
	return rest
}

func TestStateOutsideARepoIsZero(t *testing.T) {
	if g := Read(t.Context(), t.TempDir(), "m"); !g.IsZero() {
		t.Errorf("State = %+v", g)
	}
	if g := Read(t.Context(), "", "m"); !g.IsZero() {
		t.Errorf("State = %+v", g)
	}
}
