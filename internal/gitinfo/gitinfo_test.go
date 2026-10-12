package gitinfo

import "testing"

func TestRemoteNormalisation(t *testing.T) {
	for _, url := range []string{
		"https://github.com/Me/proj.git", "git@github.com:Me/proj.git", "ssh://git@github.com:22/Me/proj",
		"https://x:tok@GitHub.com/Me/proj/",
	} {
		if got := NormaliseRemote(url); got != "github.com/Me/proj" {
			t.Errorf("NormaliseRemote(%q) = %q", url, got)
		}
	}
	if got := StripUserinfo("https://x:tok@github.com/a/b"); got != "https://github.com/a/b" {
		t.Errorf("StripUserinfo = %q", got)
	}
}

func TestStateOutsideARepoIsZero(t *testing.T) {
	if g := State(t.Context(), t.TempDir(), "m"); !g.IsZero() {
		t.Errorf("State = %+v", g)
	}
	if g := State(t.Context(), "", "m"); !g.IsZero() {
		t.Errorf("State = %+v", g)
	}
}
