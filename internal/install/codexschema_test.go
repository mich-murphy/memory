package install

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/mich-murphy/memory/internal/jsonx"
	"github.com/mich-murphy/memory/internal/testenv"
)

func TestCodexDocumentAdmissionMatchesSupportedLoaderSchema(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		valid bool
	}{
		{"empty", `{}`, true},
		{"description", `{"description":"hi","hooks":{}}`, true},
		{"unknownTop", `{"unexpected":true,"hooks":{}}`, false},
		{"descriptionNumber", `{"description":1}`, false},
		{"nullDescription", `{"description":null}`, true},
		{"nullHooks", `{"hooks":null}`, false},
		{"nullEvent", `{"hooks":{"Stop":null}}`, false},
		{"unknownEventIgnored", `{"hooks":{"FutureEvent":null}}`, true},
		{"nullGroup", `{"hooks":{"Stop":[null]}}`, false},
		{"groupWithoutHandlers", `{"hooks":{"Stop":[{}]}}`, true},
		{"badMatcher", `{"hooks":{"Stop":[{"matcher":false}]}}`, false},
		{"nullHandlers", `{"hooks":{"Stop":[{"hooks":null}]}}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checkCodexDocument(t, tc.body, tc.valid)
		})
	}
}

func TestCodexDocumentAdmissionChecksForeignHandlersToo(t *testing.T) {
	valid := []string{
		`{"type":"command","command":"echo hi","command_windows":"windows","timeout":0,"async":false,"statusMessage":null,"additionalContextLimit":null,"future":true}`,
		`{"type":"command","command":"","commandWindows":null,"timeout":null,"additionalContextLimit":0}`,
		`{"type":"prompt","future":true}`, `{"type":"agent"}`,
		`{"type":"mcp_tool","server":"s","tool":"t"}`,
		`{"type":"mcp_tool","server":"s","tool":"t","input":{"int":1,"float":1.5,"array":[true,"s",{"nested":2}]}}`,
	}
	invalid := []string{
		`null`, `{"command":"missing type"}`, `{"type":"future"}`, `{"type":"command"}`,
		`{"type":"command","command":null}`, `{"type":"command","command":"x","async":null}`,
		`{"type":"command","command":"x","commandWindows":1}`,
		`{"type":"command","command":"x","commandWindows":null,"command_windows":null}`,
		`{"type":"command","command":"x","statusMessage":false}`,
		`{"type":"command","command":"x","timeout":-1}`,
		`{"type":"command","command":"x","timeout":"1"}`,
		`{"type":"command","command":"x","additionalContextLimit":1.5}`,
		`{"type":"mcp_tool","server":"s"}`,
		`{"type":"mcp_tool","server":"s","tool":"t","input":null}`,
		`{"type":"mcp_tool","server":"s","tool":"t","input":{"nested":[null]}}`,
		`{"type":"mcp_tool","server":"s","tool":"t","input":{"overflow":1e999}}`,
		`{"type":"mcp_tool","server":"s","tool":"t","input":{"unsupported":9223372036854775808}}`,
	}
	for _, group := range []struct {
		handlers []string
		valid    bool
	}{{valid, true}, {invalid, false}} {
		for _, handler := range group.handlers {
			body := fmt.Sprintf(`{"hooks":{"PreToolUse":[{"hooks":[%s]}]}}`, handler)
			checkCodexDocument(t, body, group.valid)
		}
	}
}

func checkCodexDocument(t *testing.T, body string, valid bool) {
	t.Helper()
	doc, err := jsonx.ParseObject([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateCodexHooks(doc); (err == nil) != valid {
		t.Errorf("ValidateCodexHooks(%s) = %v, want valid %t", body, err, valid)
	}
}

func TestCodexRefusesToRewriteAnUnusableHooksDocument(t *testing.T) {
	e := testenv.New(t)
	root := filepath.Join(e.Home, ".codex")
	path := filepath.Join(root, "hooks.json")
	body := `{"description":"keep me","unexpected":true,"hooks":{}}`
	testenv.Write(t, path, body)
	if err := Codex(opts(e)); err == nil {
		t.Fatal("Codex accepted a document its loader rejects")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Errorf("hooks.json = %q, want unchanged %q", got, body)
	}
	if configured, trusted := CodexTrustState(root); configured != 0 || trusted != 0 {
		t.Errorf("CodexTrustState = %d/%d, want 0/0", trusted, configured)
	}
}
