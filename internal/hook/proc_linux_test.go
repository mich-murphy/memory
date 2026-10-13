package hook

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestStatPPIDCountsFromTheLastParenthesis(t *testing.T) {
	for stat, want := range map[string]int{
		"42 (claude) S 7 42 42 0":     7,
		"42 (a) b (c)) R 9 42 42 0":   9,
		"42 (claude) S":               -1,
		"42 claude S 7 42":            -1,
		"42 (claude) S seven 42 42 0": -1,
	} {
		ppid, ok := statPPID([]byte(stat))
		if !ok {
			ppid = -1
		}
		if ppid != want {
			t.Errorf("statPPID(%q) = %d, %v; want %d", stat, ppid, ok, want)
		}
	}
}

func TestSplitCmdlineDropsEmptyArgsAndRepairsUTF8(t *testing.T) {
	got := splitCmdline([]byte("node\x00/usr/bin/pi\x00\x00--model\x00x\xff\x00"))
	if diff := cmp.Diff([]string{"node", "/usr/bin/pi", "--model", "x\uFFFD"}, got); diff != "" {
		t.Errorf("splitCmdline (-want +got):\n%s", diff)
	}
}
