package scrub

import (
	"bytes"
	"errors"
	"iter"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/mich-murphy/memory/internal/capturepolicy"
)

// MaxTextSize bounds the assignment guard, not protected-config parsing. Larger files and
// binary/encoded documents are outside this guard; it is not general-purpose secret DLP.
const MaxTextSize = 1 << 20

// ErrLiteralSecret contains no source bytes or secret names and denotes intentional withholding,
// not a transient parse/upload failure. Callers must report it as a skipped capture.
var ErrLiteralSecret = errors.New("literal_secret_assignment")

var (
	assignmentRE = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)=(.*)$`)
	shellNameRE  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	// In these languages a keyword argument on its own line (`key=lambda r: r[0],`) reads as
	// a shell assignment, and none of them has shell assignment syntax to guard.
	sourceExtensions = map[string]bool{
		".py": true, ".pyi": true, ".rb": true, ".js": true, ".mjs": true, ".cjs": true, ".jsx": true,
		".ts": true, ".mts": true, ".cts": true, ".tsx": true, ".go": true, ".rs": true, ".java": true,
		".kt": true, ".kts": true, ".scala": true, ".swift": true, ".c": true, ".h": true, ".cc": true,
		".cpp": true, ".hpp": true, ".cs": true, ".php": true, ".lua": true, ".r": true, ".jl": true,
		".ex": true, ".exs": true, ".dart": true,
	}
)

// Text withholds small UTF-8 text containing a shell assignment or fish global/export set command
// whose name says it holds a secret and whose value is not a supported pure reference.
// Only command-like lines are checked: comments, prose and inline documentation examples
// are not assignments. Executable examples (including fenced examples) are checked too.
// Callers MUST NOT pass main, subagent or superseded transcript contents. A file is
// skipped only when every path it was reached by names source code in such a language.
func Text(identities []string, data []byte) error {
	if len(data) > MaxTextSize || bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		return nil
	}
	if len(identities) > 0 && !slices.ContainsFunc(identities, isGuarded) {
		return nil
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if literalAssignment(strings.TrimSpace(line)) {
			return ErrLiteralSecret
		}
	}
	return nil
}

func isGuarded(identity string) bool {
	return !sourceExtensions[strings.ToLower(filepath.Ext(capturepolicy.BaseName(identity)))]
}

func literalAssignment(line string) bool {
	var command []shellWord
	for word := range shellWords(line) {
		if word.separator {
			if literalCommand(command) {
				return true
			}
			command = command[:0]
		} else {
			command = append(command, word)
		}
	}
	return literalCommand(command)
}

func literalCommand(words []shellWord) bool {
	// Fish permits `and` and `or` at the start of a command after a separator.
	if len(words) > 0 && (words[0].raw == "and" || words[0].raw == "or") {
		words = words[1:]
	}
	// Builtin names and flags are static words, so complete quoted spellings
	// have the same meaning. Keywords above and shell assignment positions below
	// deliberately retain raw syntax; quoted `and` and quoted names aren't those forms.
	if len(words) > 0 && words[0].complete && words[0].value == "set" {
		return literalFish(fishSetArgs(words[1:]))
	}
	if len(words) > 0 && words[0].complete && words[0].value == "export" {
		words = words[1:]
	}
	for _, word := range words {
		match := assignmentRE.FindStringSubmatch(word.raw)
		if match == nil {
			break // command arguments and prose aren't environment assignments
		}
		_, value, _ := strings.Cut(word.value, "=")
		if IsSecretName(match[1]) && (!word.complete || !holdsNoSecret(value)) {
			return true
		}
	}
	return false
}

// fishSetArgs recognises only global/export assignment flags, not queries, erases,
// other set modes or prose. Equivalent flag orderings share the same value policy.
func fishSetArgs(words []shellWord) []shellWord {
	var global, export bool
	for i, word := range words {
		if !word.complete {
			return nil
		}
		switch word.value {
		case "-g", "--global":
			global = true
		case "-x", "--export":
			export = true
		case "-gx", "-xg":
			global, export = true, true
		case "--":
			if global && export {
				return words[i+1:]
			}
			return nil
		default:
			if global && export && !strings.HasPrefix(word.value, "-") {
				return words[i:]
			}
			return nil
		}
	}
	return nil
}

// literalFish checks the decoded name and all values of a global/export set command.
func literalFish(words []shellWord) bool {
	if len(words) < 2 || !words[0].complete || !shellNameRE.MatchString(words[0].value) || !IsSecretName(words[0].value) {
		return false
	}
	var values []string
	for _, word := range words[1:] {
		if !word.complete {
			return true
		}
		values = append(values, word.value)
	}
	return !holdsNoSecret(strings.Join(values, " "))
}

// shellWord retains syntax as well as meaning. In value, a literal dollar sign is
// represented by NUL (excluded by Text), so it cannot masquerade as an expansion.
// This is a bounded assignment guard, not a shell parser or an expansion evaluator.
type shellWord struct {
	raw, value string
	complete   bool
	separator  bool
}

// shellWords observes comments and command separators before removing quotes. It yields
// completed words even when a later word is malformed, so an unrelated parse error cannot
// hide a detectable assignment. Single quotes and escaped dollars never form references.
func shellWords(line string) iter.Seq[shellWord] {
	return func(yield func(shellWord) bool) {
		for line != "" {
			line = strings.TrimLeft(line, " \t")
			if line == "" || line[0] == '#' {
				return
			}
			var word shellWord
			if strings.ContainsRune(";|&", rune(line[0])) {
				word.separator = true
				line = line[1:]
			} else {
				word = readShellWord(line)
				line = line[len(word.raw):]
			}
			if !yield(word) {
				return
			}
		}
	}
}

func readShellWord(line string) shellWord {
	var value strings.Builder
	quoted := false
	i := 0
	for i < len(line) {
		c := line[i]
		if !quoted && strings.ContainsRune(" \t;|&", rune(c)) {
			break
		}
		i++
		switch {
		case c == '\\':
			escaped, n := shellEscape(line[i:], quoted)
			value.WriteString(escaped)
			i += n
			if n == 0 && i == len(line) {
				return shellWord{raw: line[:i], value: value.String()}
			}
		case c == '"':
			quoted = !quoted
		case c == '\'' && !quoted:
			end := strings.IndexByte(line[i:], '\'')
			if end < 0 {
				return shellWord{raw: line}
			}
			value.WriteString(strings.ReplaceAll(line[i:i+end], "$", "\x00"))
			i += end + 1
		default:
			value.WriteByte(c)
		}
	}
	return shellWord{raw: line[:i], value: value.String(), complete: !quoted}
}

// shellEscape consumes the byte following a backslash, if that byte is escapable.
// Within double quotes only shell-special escapes lose their slash.
func shellEscape(rest string, quoted bool) (string, int) {
	if rest == "" || quoted && !strings.ContainsRune("\\\"$`", rune(rest[0])) {
		return "\\", 0
	}
	if rest[0] == '$' {
		return "\x00", 1
	}
	return rest[:1], 1
}
