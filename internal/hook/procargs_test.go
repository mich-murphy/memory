package hook

import (
	"encoding/binary"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func procArgs2(argc uint32, rest string) []byte {
	return append(binary.LittleEndian.AppendUint32(nil, argc), rest...)
}

func TestParseProcArgsStopsAtArgcSoTheEnvironmentStaysOut(t *testing.T) {
	for name, tc := range map[string]struct {
		buf    []byte
		want   []string
		wantOK bool
	}{
		"padded": {
			procArgs2(3, "/usr/local/bin/claude\x00\x00\x00\x00claude\x00--model\x00opus\x00HOME=/Users/m\x00TOKEN=x\x00"),
			[]string{"claude", "--model", "opus"},
			true,
		},
		"empty argument": {
			procArgs2(3, "/bin/codex\x00\x00codex\x00\x00exec\x00SECRET=x\x00"),
			[]string{"codex", "exec"},
			true,
		},
		"invalid utf-8": {procArgs2(1, "/bin/pi\x00pi\xff\x00"), []string{"pi�"}, true},
		"truncated":     {procArgs2(2, "/bin/pi\x00pi\x00"), nil, false},
		"no exec path":  {procArgs2(1, "/bin/pi"), nil, false},
		"too short":     {[]byte{1, 0}, nil, false},
	} {
		got, ok := parseProcArgs(tc.buf)
		if ok != tc.wantOK {
			t.Errorf("%s: ok = %v, want %v", name, ok, tc.wantOK)
		}
		if diff := cmp.Diff(tc.want, got); diff != "" {
			t.Errorf("%s: argv (-want +got):\n%s", name, diff)
		}
	}
}
