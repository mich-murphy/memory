package snapshot

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/jsonx"
	"github.com/mich-murphy/memory/internal/shellwords"
)

var (
	importRE     = regexp.MustCompile(`(?:^|\s)@((?:~/|\.{1,2}/|/)?[\p{L}\p{N}_.~-][\p{L}\p{N}_./~-]*)`)
	projectDirRE = regexp.MustCompile(`\$\{?CLAUDE_PROJECT_DIR\}?`)
	homeVarRE    = regexp.MustCompile(`\$\{?HOME\}?`)
)

func parseSettings(e *Entry) any {
	var v any
	if strings.HasSuffix(e.Path, ".toml") {
		if _, err := toml.Decode(string(e.Body), &v); err != nil {
			return nil
		}
		return v
	}
	if err := jsonx.Decode(e.Body, &v); err != nil {
		return nil
	}
	return v
}

func commands(obj any, inHooks bool) []string {
	var out []string
	switch v := obj.(type) {
	case map[string]any:
		for _, k := range sortedKeys(v) {
			val := v[k]
			if k == "notify" || (k == "command" && inHooks) {
				if c, ok := commandString(val); ok {
					out = append(out, c)
				}
				continue
			}
			out = append(out, commands(val, inHooks || k == "hooks" || k == "statusLine")...)
		}
	case []any:
		for _, x := range v {
			out = append(out, commands(x, inHooks)...)
		}
	case []map[string]any: // TOML arrays of tables
		for _, x := range v {
			out = append(out, commands(x, inHooks)...)
		}
	}
	return out
}

func commandString(v any) (string, bool) {
	switch c := v.(type) {
	case string:
		return c, true
	case []any:
		words := make([]string, 0, len(c))
		for _, x := range c {
			s, ok := x.(string)
			if !ok {
				return "", false
			}
			words = append(words, s)
		}
		return shellwords.Join(words), true
	}
	return "", false
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func scriptPaths(command, home, projectRoot string) []string {
	expanded := command
	if projectRoot != "" {
		expanded = projectDirRE.ReplaceAllLiteralString(expanded, projectRoot)
	}
	expanded = homeVarRE.ReplaceAllLiteralString(expanded, home)
	tokens, err := shellwords.Split(expanded)
	if err != nil {
		tokens = strings.Fields(expanded)
	}
	var out []string
	for _, tok := range tokens {
		if !strings.Contains(tok, "/") || strings.HasPrefix(tok, "-") ||
			strings.HasPrefix(tok, "http://") || strings.HasPrefix(tok, "https://") {
			continue
		}
		p := tok
		if strings.HasPrefix(p, "~") {
			p = home + p[1:]
		}
		if !filepath.IsAbs(p) {
			if projectRoot == "" {
				continue
			}
			p = filepath.Join(projectRoot, p)
		}
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() && !adapters.IsExecutableBinary(p, st.Mode()) {
			out = append(out, p)
		}
	}
	return out
}

func imports(e *Entry, home string) []string {
	if e.RealPath == "" || !strings.HasSuffix(e.Path, ".md") {
		return nil
	}
	var out []string
	fenced := false
	for line := range strings.Lines(string(e.Body)) {
		if strings.HasPrefix(strings.TrimLeft(line, " \t"), "```") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		for _, m := range importRE.FindAllStringSubmatch(line, -1) {
			ref := strings.TrimRight(m[1], ".,;:")
			p := ref
			if strings.HasPrefix(p, "~") {
				p = home + p[1:]
			}
			if !filepath.IsAbs(p) {
				p = filepath.Join(filepath.Dir(e.RealPath), p)
			}
			if isFile(p) {
				out = append(out, p)
			}
		}
	}
	return out
}
