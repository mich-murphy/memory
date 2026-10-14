// Package scrub replaces literal secrets in agent config files with "[redacted]" and keeps every
// other byte, so a scrubbed file still parses and diffs like the original. Values that only point
// at a secret, such as ${VAR} or a Pi !command, are kept: they show how the harness is wired.
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

const Redacted = "[redacted]"

type keep int

const (
	keepReferences keep = iota
	keepNothing
	keepReferencesAndEnvNames
	keepURLWithoutCredentials
)

type rule struct {
	pattern []string
	keep    keep
}

type format int

const (
	formatJSON format = iota
	formatTOML
)

func r(pattern string, k keep) rule {
	return rule{pattern: strings.Split(pattern, "."), keep: k}
}

func mcpRules(prefix string) []rule {
	return []rule{
		r(prefix+"mcpServers.*.env.*", keepReferences),
		r(prefix+"mcpServers.*.headers.*", keepReferences),
		r(prefix+"mcpServers.*.url", keepURLWithoutCredentials),
		r(prefix+"mcpServers.*.oauth.clientSecret", keepNothing),
	}
}

var (
	claudeJSONRules = slices.Concat([]rule{
		r("primaryApiKey", keepNothing),
		r("customApiKeyResponses.*", keepNothing), // suffixes of approved and rejected API keys
	}, mcpRules(""), mcpRules("projects.*."), mcpRules("project.")) // "project." is the snapshot's MCP extract
	settingsRules  = []rule{r("env.*", keepReferences)}
	mcpFileRules   = mcpRules("")
	piModelsRules  = []rule{r("providers.*.apiKey", keepReferencesAndEnvNames), r("providers.*.headers.*", keepReferences)}
	codexTOMLRules = []rule{
		r("mcp_servers.*.env.*", keepReferences),
		r("mcp_servers.*.http_headers.*", keepReferences),
		r("mcp_servers.*.url", keepURLWithoutCredentials),
		r("mcp_servers.*.bearer_token", keepNothing),
		r("model_providers.*.http_headers.*", keepReferences),
		r("model_providers.*.experimental_bearer_token", keepNothing),
		r("model_providers.*.base_url", keepURLWithoutCredentials),
		r("shell_environment_policy.set.*", keepReferences),
	}
)

// backupSuffix matches the copies editors and our installer leave beside a config file, which
// hold the same secrets.
var backupSuffix = regexp.MustCompile(`(~|\.bak|\.orig|\.old|\.agent-sessions-backup-[0-9-]+)$`)

func rulesFor(agent, name string) ([]rule, format) {
	switch backupSuffix.ReplaceAllString(name, "") {
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

func Applies(agent, name string) bool {
	r, _ := rulesFor(agent, name)
	return r != nil
}

// File fails when a file it has rules for doesn't parse: such a file can't be checked, so it
// mustn't be uploaded.
func File(agent, name string, data []byte) ([]byte, error) {
	rs, f := rulesFor(agent, name)
	if rs == nil || len(bytes.TrimSpace(data)) == 0 {
		return data, nil
	}
	doc, err := decode(f, data)
	if err != nil {
		return nil, fmt.Errorf("scrub %s: %w", name, err)
	}
	redactions := map[string]string{}
	doc = walk(doc, nil, rs, redactions)
	if len(redactions) == 0 {
		return data, nil
	}
	if out, ok := replaceInPlace(f, data, redactions); ok && nothingLeftToRedact(f, out, rs) {
		return out, nil
	}
	// Escaped and multi-line strings aren't found in place; re-encoding loses layout and comments.
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

func nothingLeftToRedact(f format, data []byte, rs []rule) bool {
	doc, err := decode(f, data)
	if err != nil {
		return false
	}
	redactions := map[string]string{}
	walk(doc, nil, rs, redactions)
	return len(redactions) == 0
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

func walk(node any, path []string, rs []rule, redactions map[string]string) any {
	for _, r := range rs {
		if matches(path, r.pattern) {
			return redactAll(node, r.keep, redactions)
		}
	}
	switch n := node.(type) {
	case map[string]any:
		for k, v := range n {
			n[k] = walk(v, append(path, k), rs, redactions)
		}
	case []any:
		for i, v := range n {
			n[i] = walk(v, append(path, strconv.Itoa(i)), rs, redactions)
		}
	case []map[string]any: // a TOML array of tables
		for i, v := range n {
			n[i], _ = walk(v, append(path, strconv.Itoa(i)), rs, redactions).(map[string]any)
		}
	}
	return node
}

func redactAll(node any, k keep, redactions map[string]string) any {
	switch n := node.(type) {
	case string:
		out := redact(n, k)
		if out != n {
			redactions[n] = out
		}
		return out
	case map[string]any:
		for key, v := range n {
			n[key] = redactAll(v, k, redactions)
		}
	case []any:
		for i, v := range n {
			n[i] = redactAll(v, k, redactions)
		}
	case []map[string]any:
		for i, v := range n {
			n[i], _ = redactAll(v, k, redactions).(map[string]any)
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

func holdsNoSecret(s string) bool {
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

func redact(s string, k keep) string {
	switch k {
	case keepNothing:
		if s == "" {
			return s
		}
	case keepURLWithoutCredentials:
		return redactURL(s)
	case keepReferencesAndEnvNames:
		if holdsNoSecret(s) || strings.HasPrefix(s, "!") || envNameRE.MatchString(s) {
			return s
		}
	case keepReferences:
		if holdsNoSecret(s) || strings.HasPrefix(s, "!") {
			return s
		}
	}
	return Redacted
}

func redactURL(s string) string {
	if holdsNoSecret(s) {
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
			if v, err := url.QueryUnescape(value); ok && (err != nil || !holdsNoSecret(v)) {
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

func replaceInPlace(f format, data []byte, redactions map[string]string) ([]byte, bool) {
	out := data
	for old, repl := range redactions {
		found := false
		for _, lit := range spellings(f, old) {
			var n int
			out, n = replaceNonKeys(f, out, lit, quote(f, repl))
			found = found || n > 0
		}
		if !found {
			return nil, false
		}
	}
	return out, true
}

func spellings(f format, s string) [][]byte {
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

func replaceNonKeys(f format, data, lit, repl []byte) ([]byte, int) {
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
