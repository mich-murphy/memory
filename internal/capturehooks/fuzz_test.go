package capturehooks

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func FuzzEditJSON(f *testing.F) {
	legacy := `{"type":"command","command":"memory old"}`
	seeds := []string{
		``, `{}`, `{"hooks":{}}`, `{"hooks":null}`, `[]`, `{"secret":"S",`, `{"x":1,"x":2}`,
		`{"secret":"SETTINGSECRET","cleanupPeriodDays":7,"large":9007199254740993,"hooks":{"Stop":[{"matcher":"foreign","unknown":true,"hooks":[` + legacy + `,{"type":"command","command":"foreign"}]}]}}`,
		`{"hooks":{"Stop":[{}]}}`, `{"hooks":{"Stop":[{"hooks":[null]}]}}`, `{"hooks":{"Stop":{}}}`,
		`{"hooks":{},"unknown":{"token":"CODEXSECRET"}}`, strings.Repeat("[", 3000),
	}
	for _, s := range seeds {
		f.Add([]byte(s), true, false, "memory hook")
		f.Add([]byte(s), false, true, "memory hook")
	}
	f.Fuzz(func(t *testing.T, before []byte, claude, exists bool, command string) {
		agent := "codex"
		if claude {
			agent = "claude"
		}
		command = commandPrefix + command
		out, err := editJSON(before, exists, agent, command, false)
		if err != nil {
			return
		}
		if !json.Valid(out) {
			t.Fatalf("invalid JSON output %q", out)
		}
		again, err := editJSON(out, true, agent, command, false)
		if err != nil || !bytes.Equal(again, out) {
			t.Fatalf("not idempotent: err=%v\n%q\n%q", err, out, again)
		}
		if !exists {
			return
		}
		var in, o map[string]json.RawMessage
		if json.Unmarshal(before, &in) != nil || json.Unmarshal(out, &o) != nil {
			return
		}
		for k, v := range in {
			if k == "hooks" {
				continue
			}
			var a, b bytes.Buffer
			if json.Compact(&a, v) != nil || json.Compact(&b, o[k]) != nil || !bytes.Equal(a.Bytes(), b.Bytes()) {
				t.Fatalf("foreign key %q changed: %s -> %s", k, v, o[k])
			}
		}
		if strings.Contains(string(before), command) && command != "" {
			return
		}
		rm, err := editJSON(out, true, agent, command, true)
		if err != nil {
			t.Fatalf("remove: %v", err)
		}
		var x, y any
		if json.Unmarshal(before, &x) != nil || json.Unmarshal(rm, &y) != nil {
			t.Fatal("unmarshal")
		}
		norm := func(v any) {
			if m, ok := v.(map[string]any); ok {
				if h, ok := m["hooks"].(map[string]any); ok && len(h) == 0 {
					delete(m, "hooks")
				}
			}
		}
		norm(x)
		norm(y)
		if !reflect.DeepEqual(x, y) {
			t.Fatalf("remove(edit(x)) != x:\n%s\n%s", before, rm)
		}
	})
}
