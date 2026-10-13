// Package shellwords quotes and splits POSIX shell words, like Python's shlex.join and
// shlex.split.
package shellwords

import (
	"errors"
	"regexp"
	"strings"
)

var safe = regexp.MustCompile(`^[\w@%+=:,./-]+$`)

func quote(s string) string {
	if s == "" {
		return "''"
	}
	if safe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

func Join(words []string) string {
	quoted := make([]string, len(words))
	for i, w := range words {
		quoted[i] = quote(w)
	}
	return strings.Join(quoted, " ")
}

var errUnterminated = errors.New("unterminated quote")

// Split does no expansion.
func Split(s string) ([]string, error) {
	var words []string
	var cur strings.Builder
	inWord := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		case c == '\'':
			end := strings.IndexByte(s[i+1:], '\'')
			if end < 0 {
				return nil, errUnterminated
			}
			cur.WriteString(s[i+1 : i+1+end])
			i += end + 1
			inWord = true
		case c == '"':
			n, err := readDoubleQuoted(s[i+1:], &cur)
			if err != nil {
				return nil, err
			}
			i += n // now on the closing quote
			inWord = true
		case c == '\\' && i+1 < len(s):
			i++
			cur.WriteByte(s[i])
			inWord = true
		default:
			cur.WriteByte(c)
			inWord = true
		}
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words, nil
}

func readDoubleQuoted(s string, cur *strings.Builder) (consumedWithClosingQuote int, err error) {
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"':
			return i + 1, nil
		case c == '\\' && i+1 < len(s) && strings.IndexByte("\\\"$`\n", s[i+1]) >= 0:
			i++
			cur.WriteByte(s[i])
		default:
			cur.WriteByte(c)
		}
	}
	return 0, errUnterminated
}
