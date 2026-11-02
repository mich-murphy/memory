package scrub

import "testing"

func TestRecognisedNamesCoverRulesFor(t *testing.T) {
	for _, name := range recognisedNames {
		if rs, _ := rulesFor(name); rs == nil {
			t.Fatalf("%s is listed but has no rules", name)
		}
	}
	if len(recognisedNames) != 11 {
		t.Fatalf("recognised names = %d; update the list to match rulesFor", len(recognisedNames))
	}
}

func TestRulesDigestChangesWithRules(t *testing.T) {
	before := RulesDigest()
	if before != RulesDigest() {
		t.Fatal("digest must be deterministic")
	}
	saved := settingsRules
	defer func() { settingsRules = saved }()
	settingsRules = append(append([]rule{}, saved...), r("extra.*", keepNothing))
	if RulesDigest() == before {
		t.Fatal("adding a rule must change the digest")
	}
	settingsRules = append([]rule{}, saved...)
	settingsRules[0].keep = keepNothing
	if RulesDigest() == before {
		t.Fatal("changing a keep kind must change the digest")
	}
}
