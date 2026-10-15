package scrub

import (
	"bytes"
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/mich-murphy/memory/internal/shellwords"
)

// MaxTextSize bounds the assignment guard, not protected-config parsing. Larger files and
// binary/encoded documents are outside this guard; it is not general-purpose secret DLP.
const MaxTextSize = 1 << 20

// ErrLiteralSecret contains no source bytes or secret names and denotes intentional withholding,
// not a transient parse/upload failure. Callers must report it as a skipped capture.
var ErrLiteralSecret = errors.New("literal_secret_assignment")

var (
	assignmentRE = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)=(.*)$`)
	exportRE     = regexp.MustCompile(`^export[ \t]+`)
	fishRE       = regexp.MustCompile(`^set[ \t]+-gx[ \t]+`)
)

// Text withholds small UTF-8 text containing a shell assignment or fish set -gx command
// whose name says it holds a secret and whose value is not a supported pure reference.
// Only command-like lines are checked: comments, prose and inline documentation examples
// are not assignments. Executable examples (including fenced examples) are checked too.
// Callers MUST NOT pass main, subagent or superseded transcript contents.
func Text(data []byte) error {
	if len(data) > MaxTextSize || bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		return nil
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if literalAssignment(strings.TrimSpace(line)) {
			return ErrLiteralSecret
		}
	}
	return nil
}

func literalAssignment(line string) bool {
	line = exportRE.ReplaceAllString(line, "")
	fish := fishRE.MatchString(line)
	if fish {
		line = fishRE.ReplaceAllString(line, "")
	} else if !assignmentRE.MatchString(line) {
		return false
	}
	words, err := shellwords.Split(withoutComment(line))
	if err != nil {
		// An incomplete quoted literal cannot be proven to be a reference. Never log it.
		name, _, _ := strings.Cut(line, "=")
		if fish {
			name, _, _ = strings.Cut(line, " ")
		}
		return IsSecretName(name)
	}
	for i, word := range words {
		if strings.HasPrefix(word, "#") {
			words = words[:i]
			break
		}
	}
	if fish {
		return len(words) > 1 && IsSecretName(words[0]) && !holdsNoSecret(strings.Join(words[1:], " "))
	}
	for _, word := range words {
		match := assignmentRE.FindStringSubmatch(word)
		if match == nil {
			break // the command or a comment, not another environment assignment
		}
		if IsSecretName(match[1]) && !holdsNoSecret(strings.TrimSuffix(match[2], ";")) {
			return true
		}
	}
	return false
}

// withoutComment keeps quote characters in shell comments from making an otherwise valid
// reference assignment look unterminated. It does not expand or execute shell syntax.
func withoutComment(line string) string {
	var quote byte
	escaped := false
	for i := range len(line) {
		c := line[i]
		switch {
		case escaped:
			escaped = false
		case c == '\\' && quote != '\'':
			escaped = true
		case c == quote:
			quote = 0
		case quote != 0:
			continue
		case c == '\'' || c == '"':
			quote = c
		case c == '#' && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t'):
			return line[:i]
		}
	}
	return line
}
