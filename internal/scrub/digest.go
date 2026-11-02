package scrub

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// recognisedNames lists every filename rulesFor maps to a rule table. A name added to
// rulesFor but not here is caught by TestRecognisedNamesCoverRulesFor.
var recognisedNames = []string{
	"claude.json", ".claude.json", "settings.json", "settings.local.json", "managed-settings.json",
	".mcp.json", "mcp.json", "managed-mcp.json", "models.json", "config.toml", "managed_config.toml",
}

// RulesDigest digests the rule tables (filename, format, patterns, keep kinds) that decide
// how config bytes are scrubbed. Capture binds a version to transformed-source
// fingerprints; a guard test compares this digest with a golden so that changing the
// rules forces that version to be bumped.
func RulesDigest() string {
	h := sha256.New()
	for _, name := range recognisedNames {
		rs, f := rulesFor(name)
		_, _ = fmt.Fprintf(h, "%s|%d|%d\n", name, f, len(rs))
		for _, r := range rs {
			_, _ = fmt.Fprintf(h, "%s|%d\n", strings.Join(r.pattern, "."), r.keep)
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}
