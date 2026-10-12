package adapters

import (
	"testing"
)

// FuzzSessionInfo feeds arbitrary transcript heads to every agent's parser: a malformed or
// hostile first line must never stop a sync.
func FuzzSessionInfo(f *testing.F) {
	for _, seed := range []string{
		`{"type":"session","id":"x","cwd":"/w","version":3}` + "\n",
		`{"type":"session_meta","payload":{"id":"t","cwd":"/w","git":{"branch":"main"}}}` + "\n",
		`{"type":"user","sessionId":"s","cwd":"/w","gitBranch":"main","version":"2.1"}` + "\n" + `{"type":"assistant"`,
		`{"payload":null}` + "\n" + `[1,2]` + "\n" + `{"cwd":7}`,
		"\n\n  \n",
	} {
		f.Add([]byte(seed))
	}
	all := All(f.TempDir())
	f.Fuzz(func(t *testing.T, data []byte) {
		head := ReadHead(data, 5)
		if len(head) > 5 {
			t.Fatalf("ReadHead returned %d lines", len(head))
		}
		for _, name := range Names {
			_ = all[name].SessionInfo(head)
		}
	})
}
