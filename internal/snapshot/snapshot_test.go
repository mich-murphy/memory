package snapshot

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/testenv"
)

func TestSnapshotDoesNotWalkHomeOutsideARepo(t *testing.T) {
	e := testenv.New(t)
	testenv.Write(t, filepath.Join(e.Home, "deep", "x", "CLAUDE.md"), "should not be found")
	c := collect(adapters.NewClaude(e.Home), filepath.Join(e.Home, ".claude"), e.Home)
	for _, entry := range c.entries {
		if strings.Contains(entry.Path, "deep") {
			t.Errorf("walked into %s", entry.Path)
		}
	}
}
