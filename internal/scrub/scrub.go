// Package scrub replaces literal secrets in agent config files with "[redacted]" and keeps every
// other byte, so a scrubbed file still parses and diffs like the original. Values that only point
// at a secret, such as ${VAR} or a Pi !command, are kept: they show how the harness is wired.
package scrub

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/mich-murphy/memory/internal/capturepolicy"
	"github.com/mich-murphy/memory/internal/jsonx"
)

const Redacted = "[redacted]"

type keep int

const (
	keepReferences keep = iota
	keepNothing
	keepReferencesAndEnvNames
	keepReferencesAndCommands
	keepURLWithoutCredentials
	keepArgs // a command line: redacts what Args does
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
		r(prefix+"mcpServers.*.args", keepArgs),
	}
}

var (
	claudeJSONRules = slices.Concat([]rule{
		r("primaryApiKey", keepNothing),
		r("customApiKeyResponses.*", keepNothing), // suffixes of approved and rejected API keys
	}, mcpRules(""), mcpRules("projects.*."), mcpRules("project.")) // "project." is the snapshot's MCP extract
	settingsRules  = []rule{r("env.*", keepReferences), r("hooks.*.*.hooks.*.headers.*", keepReferences)}
	mcpFileRules   = mcpRules("")
	piModelsRules  = []rule{r("providers.*.apiKey", keepReferencesAndEnvNames), r("providers.*.headers.*", keepReferencesAndCommands)}
	codexTOMLRules = []rule{
		r("mcp_servers.*.env.*", keepReferences),
		r("mcp_servers.*.http_headers.*", keepReferences),
		r("mcp_servers.*.url", keepURLWithoutCredentials),
		r("mcp_servers.*.bearer_token", keepNothing),
		r("mcp_servers.*.args", keepArgs),
		r("model_providers.*.http_headers.*", keepReferences),
		r("model_providers.*.experimental_bearer_token", keepNothing),
		r("model_providers.*.base_url", keepURLWithoutCredentials),
		r("shell_environment_policy.set.*", keepReferences),
		r("otel.*.*.headers.*", keepReferences), // otel.<exporter>.<otlp-http|otlp-grpc>.headers
	}
	// Claude Code's --settings and --mcp-config take a file or inline JSON.
	jsonArgRules = slices.Concat(settingsRules, mcpFileRules)
)

// rulesFor selects the union of supported schemas for this filename, not the collecting agent.
// Shared settings filenames retain their supported dialect rules; unknown filenames are not DLP.
func rulesFor(name string) ([]rule, format) {
	switch capturepolicy.BaseName(name) {
	case "claude.json", ".claude.json":
		return claudeJSONRules, formatJSON
	case "settings.json", "settings.local.json", "managed-settings.json":
		return settingsRules, formatJSON
	case ".mcp.json", "mcp.json", "managed-mcp.json":
		return mcpFileRules, formatJSON
	case "models.json":
		return piModelsRules, formatJSON
	case "config.toml", "managed_config.toml":
		return codexTOMLRules, formatTOML
	}
	return nil, formatJSON
}

// Recognizes reports whether a path names a supported protected config, including backups.
// Streaming capture must check every resolved identity: a config must never take a raw-byte path,
// even when it belongs to a different agent or has an unrecognised logical alias.
func Recognizes(path string) bool {
	rs, _ := rulesFor(path)
	return rs != nil
}

// Applies is the compatibility form of Recognizes. The collecting agent does not limit protection.
func Applies(_, name string) bool {
	return Recognizes(name)
}

// File applies the supported schemas for name regardless of the collecting agent. It fails when
// a protected config doesn't parse: such a file can't be checked, so it mustn't be uploaded.
// The agent argument is retained for compatibility.
func File(_, name string, data []byte) ([]byte, error) {
	rs, f := rulesFor(name)
	if rs == nil {
		return data, nil
	}
	out, err := scrubDoc(f, data, rs)
	if err != nil {
		return nil, fmt.Errorf("scrub %s: %w", name, err)
	}
	return out, nil
}

// Paths scrubs both logical and resolved recognised filenames. An unrecognised alias cannot
// bypass the target's schema, and a recognised alias stays protected even if its target is unknown.
// This compatibility helper only knows two identities. New capture paths should use
// capturepolicy.Resolve, Policy.Excludes before reading, and Identities after reading.
func Paths(agent, logical, resolved string, data []byte) ([]byte, error) {
	return Identities(agent, []string{logical, resolved}, data)
}

// Identities applies all recognised schemas encountered by capturepolicy.Resolve, including
// intermediate config symlinks and other agents' configs. Schemas are selected by each identity's
// filename, never by the collecting agent. Check a capturepolicy.Policy before reading the data.
func Identities(agent string, identities []string, data []byte) ([]byte, error) {
	seen := map[string]bool{}
	for _, identity := range identities {
		name := capturepolicy.BaseName(identity)
		if seen[name] || !Recognizes(name) {
			continue
		}
		seen[name] = true
		out, err := File(agent, name, data)
		if err != nil {
			return nil, err
		}
		data = out
	}
	return data, nil
}

func scrubDoc(f format, data []byte, rs []rule) ([]byte, error) {
	doc, err := decode(f, data)
	if err != nil {
		return nil, err
	}
	redactions := map[string]string{}
	doc = walk(doc, nil, rs, redactions)
	if len(redactions) == 0 {
		return data, nil
	}
	if out, ok := replaceInPlace(f, data, redactions); ok && noSecretCopies(f, out, redactions) {
		return out, nil
	}
	// Escaped and multi-line strings aren't found in place; re-encoding loses layout and comments.
	// Replace all equivalent decoded values, not only schema-selected values.
	return encode(f, replaceCopies(doc, redactions))
}

var (
	nameRE = regexp.MustCompile(`^-{0,2}[A-Za-z0-9][\w.-]*$`)
	// Matched as whole words, so MAX_TOKENS and --max-tokens hold no secret.
	secretWords = []string{"token", "key", "auth"}
	// Matched anywhere in a name: --client-secret, SECRETKEY.
	secretParts = []string{"secret", "passw", "passphrase", "credential", "cookie", "apikey", "authorization"}
)

// IsSecretName reports whether a flag, environment variable or dotted setting name says it
// holds a secret, judging a dotted name by its last part.
func IsSecretName(name string) bool {
	name = strings.ToLower(name[strings.LastIndexByte(name, '.')+1:])
	words := strings.FieldsFunc(name, func(r rune) bool { return r == '-' || r == '_' })
	return slices.ContainsFunc(words, func(w string) bool { return slices.Contains(secretWords, w) }) ||
		slices.ContainsFunc(secretParts, func(p string) bool { return strings.Contains(name, p) })
}

// Args returns argv with the values of secret-looking flags and NAME=value words redacted, the
// value of each -H or --header after its name, and secrets in inline JSON scrubbed as a
// settings or MCP file is. A value that only names a secret, such as $VAR, is kept.
func Args(argv []string) []string {
	out := slices.Clone(argv)
	for i, a := range out {
		name, v, hasValue := strings.Cut(a, "=")
		switch {
		case strings.HasPrefix(strings.TrimSpace(a), "{"):
			if scrubbed, err := scrubDoc(formatJSON, []byte(a), jsonArgRules); err == nil {
				out[i] = string(scrubbed)
			} else {
				out[i] = Redacted
			}
		case hasValue && nameRE.MatchString(name):
			out[i] = name + "=" + redactFlagValue(name, v)
		case strings.HasPrefix(a, "-") && nameRE.MatchString(a) && i+1 < len(out) && !strings.HasPrefix(out[i+1], "-"):
			out[i+1] = redactFlagValue(a, out[i+1])
		}
	}
	return out
}

func redactFlagValue(flag, v string) string {
	if flag == "-H" || flag == "--header" {
		name, value, ok := strings.Cut(v, ":")
		if !ok || holdsNoSecret(strings.TrimSpace(value)) {
			return v
		}
		return name + ": " + Redacted
	}
	if IsSecretName(flag) {
		return redact(v, keepReferences)
	}
	return v
}

func decode(f format, data []byte) (any, error) {
	var doc any
	if f == formatTOML {
		var m map[string]any
		if _, err := toml.Decode(string(data), &m); err != nil {
			// Parser diagnostics can echo an unquoted credential value. Callers log this error.
			return nil, errors.New("toml: invalid document")
		}
		return m, nil
	}
	// encoding/json v1 silently replaces duplicate keys, losing earlier secrets. Go 1.27's
	// jsontext rejects duplicate decoded names (including escapes), invalid Unicode and trailing
	// data before the compatibility decoder sees the document.
	if !jsontext.Value(data).IsValid() {
		return nil, errors.New("json: invalid document or duplicate object names")
	}
	if err := jsonx.Decode(data, &doc); err != nil {
		return nil, err
	}
	if _, ok := doc.(map[string]any); !ok {
		return nil, errors.New("json: protected config must be an object")
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
	if args, ok := node.([]any); ok && k == keepArgs {
		return redactArgs(args, redactions)
	}
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

func redactArgs(args []any, redactions map[string]string) []any {
	argv := make([]string, len(args))
	for i, a := range args {
		argv[i], _ = a.(string)
	}
	for i, a := range Args(argv) {
		if a != argv[i] {
			redactions[argv[i]] = a
			args[i] = a
		}
	}
	return args
}

var (
	referenceRE = regexp.MustCompile(`\$\{[A-Za-z_][A-Za-z0-9_]*\}|\$[A-Za-z_][A-Za-z0-9_]*`)
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
	case keepReferencesAndCommands:
		if holdsNoSecret(s) || strings.HasPrefix(s, "!") {
			return s
		}
	case keepReferences:
		if holdsNoSecret(s) {
			return s
		}
	case keepArgs:
		return Args([]string{s})[0]
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
		rest := bytes.TrimLeft(data[i+len(lit):], " \t\r\n")
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
