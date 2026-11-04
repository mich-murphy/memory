package scrub

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// RulesDigest digests ruleTable (filename, format, patterns, keep kinds), the table
// rulesFor reads. Capture binds a version to transformed-source fingerprints and a guard
// test compares this digest with a golden, so changing the rules forces a version bump.
// Not covered: scrubDoc/IsSecretName and other scrubbing logic outside the table; a
// change there still needs a manual bump of the version.
func RulesDigest() string {
	h := sha256.New()
	for _, name := range slices.Sorted(maps.Keys(ruleTable)) {
		e := ruleTable[name]
		_, _ = fmt.Fprintf(h, "%s|%d|%d\n", name, e.format, len(e.rules))
		for _, r := range e.rules {
			_, _ = fmt.Fprintf(h, "%s|%d\n", strings.Join(r.pattern, "."), r.keep)
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}
