// Package scrub blanks secrets written inside agent config files before they leave the machine.
//
// Credential files are never copied at all (adapters.IsCredential). Config files are copied,
// because they are evidence of how the harness was set up, but several of them can hold a
// literal secret beside ordinary settings:
//
//	Claude Code   ~/.claude.json and .mcp.json: MCP server env, headers and URLs;
//	              settings.json: the env block; ~/.claude.json: the API key and its suffixes
//	Codex         config.toml: MCP server env, headers, URLs and bearer tokens, provider
//	              headers and bearer tokens, shell_environment_policy.set
//	Pi            models.json: provider API keys and headers; mcp.json: MCP server env,
//	              headers and URLs
//
// File replaces each such value with "[redacted]" and leaves every other byte as it was, so the
// file still parses, diffs and hashes like the original apart from those values. A value that
// only names a secret is kept, because it says how the harness is wired without revealing
// anything: a ${VAR} or $VAR reference, "Bearer ${TOKEN}", a Pi !command, a number or boolean.
// Keys are always kept. URLs keep their scheme, host and path; a password in them is dropped and
// each query value redacted.
package scrub

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/mich-murphy/memory/internal/jsonx"
)

// Redacted replaces a secret value.
const Redacted = "[redacted]"

// kind says which values at a rule's path are kept.
type kind int

const (
	kindValue  kind = iota // env and header values: kept only if they hold no literal secret
	kindAlways             // tokens and keys that are always literal: never kept
	kindAPIKey             // Pi apiKey: also kept when it names an environment variable
	kindURL                // URLs: password dropped, query values redacted
)

type rule struct {
	path []string // "*" matches any one key or array index
	kind kind
}

type format int

const (
	formatJSON format = iota
	formatTOML
)

func r(path string, k kind) rule {
	return rule{path: strings.Split(path, "."), kind: k}
}

func mcpRules(prefix string) []rule {
	return []rule{
		r(prefix+"mcpServers.*.env.*", kindValue),
		r(prefix+"mcpServers.*.headers.*", kindValue),
		r(prefix+"mcpServers.*.url", kindURL),
		r(prefix+"mcpServers.*.oauth.clientSecret", kindAlways),
	}
}

var (
	// claudeJSONRules cover ~/.claude.json and the MCP extract of it that snapshots record
	// (top-level "project" there).
	claudeJSONRules = slices.Concat([]rule{
		r("primaryApiKey", kindAlways),
		r("customApiKeyResponses.*", kindAlways), // the last 20 characters of each API key approved or rejected
	}, mcpRules(""), mcpRules("projects.*."), mcpRules("project."))
	settingsRules  = []rule{r("env.*", kindValue)}
	mcpFileRules   = mcpRules("")
	piModelsRules  = []rule{r("providers.*.apiKey", kindAPIKey), r("providers.*.headers.*", kindValue)}
	codexTOMLRules = []rule{
		r("mcp_servers.*.env.*", kindValue),
		r("mcp_servers.*.http_headers.*", kindValue),
		r("mcp_servers.*.url", kindURL),
		r("mcp_servers.*.bearer_token", kindAlways),
		r("model_providers.*.http_headers.*", kindValue),
		r("model_providers.*.experimental_bearer_token", kindAlways),
		r("model_providers.*.base_url", kindURL),
		r("shell_environment_policy.set.*", kindValue),
	}
)

// rulesFor picks the rules for a file by agent and base name; nil means the file is copied as
// it is.
func rulesFor(agent, name string) ([]rule, format) {
	switch name {
	case "claude.json", ".claude.json":
		return claudeJSONRules, formatJSON
	case "settings.json", "settings.local.json", "managed-settings.json":
		return settingsRules, formatJSON
	case ".mcp.json", "mcp.json", "managed-mcp.json":
		return mcpFileRules, formatJSON
	case "models.json":
		if agent == "pi" {
			return piModelsRules, formatJSON
		}
	case "config.toml", "managed_config.toml":
		if agent == "codex" {
			return codexTOMLRules, formatTOML
		}
	}
	return nil, formatJSON
}

// Applies reports whether File may change the file.
func Applies(agent, name string) bool {
	r, _ := rulesFor(agent, name)
	return r != nil
}

// File returns data with the secrets in it redacted, chosen by the agent and the file's base
// name. A file with no rules comes back unchanged. A file that has rules but doesn't parse is an
// error: it can't be checked, so it must not be uploaded.
func File(agent, name string, data []byte) ([]byte, error) {
	rs, f := rulesFor(agent, name)
	if rs == nil || len(bytes.TrimSpace(data)) == 0 {
		return data, nil
	}
	doc, err := decode(f, data)
	if err != nil {
		return nil, fmt.Errorf("scrub %s: %w", name, err)
	}
	changes := map[string]string{}
	doc = walk(doc, nil, rs, changes)
	if len(changes) == 0 {
		return data, nil
	}
	if out, ok := replaceLiterals(f, data, changes); ok && clean(f, out, rs) {
		return out, nil
	}
	// A value written in a form the literal search doesn't know (escapes, multi-line strings):
	// rewrite the whole file from the redacted document instead.
	out, err := encode(f, doc)
	if err != nil {
		return nil, fmt.Errorf("scrub %s: %w", name, err)
	}
	return out, nil
}

func decode(f format, data []byte) (any, error) {
	var doc any
	if f == formatTOML {
		var m map[string]any
		if _, err := toml.Decode(string(data), &m); err != nil {
			return nil, fmt.Errorf("toml: %w", err)
		}
		return m, nil
	}
	if err := jsonx.Decode(data, &doc); err != nil {
		return nil, err
	}
	return doc, nil
}

func encode(f format, doc any) ([]byte, error) {
	if f == formatTOML {
		var buf bytes.Buffer
		if err := toml.NewEncoder(&buf).Encode(doc); err != nil {
			return nil, fmt.Errorf("toml: %w", err)
		}
		return buf.Bytes(), nil
	}
	return jsonx.Indent(doc, 2)
}

// clean reports whether data parses and has nothing left to redact.
func clean(f format, data []byte, rs []rule) bool {
	doc, err := decode(f, data)
	if err != nil {
		return false
	}
	changes := map[string]string{}
	walk(doc, nil, rs, changes)
	return len(changes) == 0
}

func matches(path, pattern []string) bool {
	if len(path) != len(pattern) {
		return false
	}
	for i, p := range pattern {
		if p != "*" && p != path[i] {
			return false
		}
	}
	return true
}

// walk redacts the values under any rule's path, recording each changed string as old -> new,
// and returns the node with its redactions applied.
func walk(node any, path []string, rs []rule, changes map[string]string) any {
	for _, r := range rs {
		if matches(path, r.path) {
			return redactAll(node, r.kind, changes)
		}
	}
	switch n := node.(type) {
	case map[string]any:
		for k, v := range n {
			n[k] = walk(v, append(path, k), rs, changes)
		}
	case []any:
		for i, v := range n {
			n[i] = walk(v, append(path, strconv.Itoa(i)), rs, changes)
		}
	case []map[string]any: // a TOML array of tables
		for i, v := range n {
			n[i], _ = walk(v, append(path, strconv.Itoa(i)), rs, changes).(map[string]any)
		}
	}
	return node
}

// redactAll applies k to every string at or under node.
func redactAll(node any, k kind, changes map[string]string) any {
	switch n := node.(type) {
	case string:
		out := redact(n, k)
		if out != n {
			changes[n] = out
		}
		return out
	case map[string]any:
		for key, v := range n {
			n[key] = redactAll(v, k, changes)
		}
	case []any:
		for i, v := range n {
			n[i] = redactAll(v, k, changes)
		}
	case []map[string]any:
		for i, v := range n {
			n[i], _ = redactAll(v, k, changes).(map[string]any)
		}
	}
	return node
}

var (
	referenceRE = regexp.MustCompile(`\$\{[^}]*\}|\$[A-Za-z_][A-Za-z0-9_]*`)
	numberRE    = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`)
	envNameRE   = regexp.MustCompile(`^[A-Z][A-Z0-9]*(_[A-Z0-9]+)+$`)
	authWords   = map[string]bool{"bearer": true, "basic": true, "token": true}
)

// harmless reports whether a value holds no literal secret.
func harmless(s string) bool {
	switch strings.ToLower(s) {
	case "", Redacted, "true", "false", "yes", "no", "on", "off":
		return true
	}
	if numberRE.MatchString(s) {
		return true
	}
	if !referenceRE.MatchString(s) {
		return false
	}
	for word := range strings.FieldsSeq(referenceRE.ReplaceAllString(s, " ")) {
		if !authWords[strings.ToLower(word)] {
			return false
		}
	}
	return true
}

func redact(s string, k kind) string {
	switch k {
	case kindAlways:
		if s == "" {
			return s
		}
	case kindURL:
		return redactURL(s)
	case kindAPIKey:
		if harmless(s) || strings.HasPrefix(s, "!") || envNameRE.MatchString(s) {
			return s
		}
	case kindValue:
		if harmless(s) || strings.HasPrefix(s, "!") {
			return s
		}
	}
	return Redacted
}

func redactURL(s string) string {
	if harmless(s) {
		return s
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return Redacted
	}
	changed := false
	if _, hasPassword := u.User.Password(); hasPassword {
		u.User = url.User(u.User.Username())
		changed = true
	}
	if u.RawQuery != "" {
		parts := strings.Split(u.RawQuery, "&")
		for i, p := range parts {
			name, value, ok := strings.Cut(p, "=")
			if v, err := url.QueryUnescape(value); ok && (err != nil || !harmless(v)) {
				parts[i] = name + "=" + Redacted
				changed = true
			}
		}
		u.RawQuery = strings.Join(parts, "&")
	}
	if !changed {
		return s
	}
	return u.String()
}

// replaceLiterals swaps each old string's literal in data for the new one's, leaving keys alone.
// ok is false if some old string's literal wasn't found.
func replaceLiterals(f format, data []byte, changes map[string]string) ([]byte, bool) {
	out := data
	for old, repl := range changes {
		found := false
		for _, lit := range literals(f, old) {
			var n int
			out, n = replaceValues(f, out, lit, quote(f, repl))
			found = found || n > 0
		}
		if !found {
			return nil, false
		}
	}
	return out, true
}

// literals are the ways old is commonly written as a string value.
func literals(f format, s string) [][]byte {
	if f == formatTOML {
		out := [][]byte{quote(f, s)}
		if !strings.ContainsAny(s, "'\n\r") {
			out = append(out, []byte("'"+s+"'"))
		}
		return out
	}
	plain := quote(f, s)
	escaped, err := json.Marshal(s)
	if err != nil || bytes.Equal(escaped, plain) {
		return [][]byte{plain}
	}
	return [][]byte{plain, escaped}
}

func quote(f format, s string) []byte {
	if f == formatTOML {
		var b strings.Builder
		b.WriteByte('"')
		for _, r := range s {
			switch {
			case r == '"' || r == '\\':
				b.WriteByte('\\')
				b.WriteRune(r)
			case r < 0x20 || r == 0x7f:
				fmt.Fprintf(&b, `\u%04X`, r)
			default:
				b.WriteRune(r)
			}
		}
		b.WriteByte('"')
		return []byte(b.String())
	}
	out, err := jsonx.Compact(s)
	if err != nil {
		return []byte(strconv.Quote(s)) // unreachable: a string always marshals
	}
	return out
}

// replaceValues replaces each occurrence of lit that is a value rather than a key: JSON keys are
// followed by ':', TOML keys by '=' or '.'.
func replaceValues(f format, data, lit, repl []byte) ([]byte, int) {
	var out bytes.Buffer
	n := 0
	for {
		i := bytes.Index(data, lit)
		if i < 0 {
			out.Write(data)
			return out.Bytes(), n
		}
		rest := bytes.TrimLeft(data[i+len(lit):], " \t")
		isKey := len(rest) > 0 && (rest[0] == ':' && f == formatJSON || (rest[0] == '=' || rest[0] == '.') && f == formatTOML)
		out.Write(data[:i])
		if isKey {
			out.Write(lit)
		} else {
			out.Write(repl)
			n++
		}
		data = data[i+len(lit):]
	}
}
