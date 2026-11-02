package capturesource

import (
	"testing"

	"github.com/mich-murphy/memory/internal/scrub"
)

func TestScrubRulesMatchTransformPolicyVersion(t *testing.T) {
	if got := scrub.RulesDigest(); got != scrubRulesGolden {
		t.Fatalf("scrub rules changed (digest %s, golden %s): bump transformPolicyVersion (now %s) and update scrubRulesGolden",
			got, scrubRulesGolden, transformPolicyVersion)
	}
}
