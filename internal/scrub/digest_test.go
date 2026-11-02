package scrub

import "testing"

func TestRulesForReadsTheDigestedTable(t *testing.T) {
	for name, e := range ruleTable {
		rs, f := rulesFor(name)
		if len(rs) == 0 || len(rs) != len(e.rules) || f != e.format {
			t.Fatalf("%s: rulesFor disagrees with ruleTable", name)
		}
	}
	if rs, _ := rulesFor("notes.txt"); rs != nil {
		t.Fatal("unknown names must not be protected")
	}
}

func TestRulesDigestChangesWithRules(t *testing.T) {
	before := RulesDigest()
	if before != RulesDigest() {
		t.Fatal("digest must be deterministic")
	}
	saved := ruleTable["settings.json"]
	defer func() { ruleTable["settings.json"] = saved }()
	ruleTable["settings.json"] = ruleSet{append(append([]rule{}, saved.rules...), r("extra.*", keepNothing)), saved.format}
	if RulesDigest() == before {
		t.Fatal("adding a rule must change the digest")
	}
	changed := append([]rule{}, saved.rules...)
	changed[0].keep = keepNothing
	ruleTable["settings.json"] = ruleSet{changed, saved.format}
	if RulesDigest() == before {
		t.Fatal("changing a keep kind must change the digest")
	}
	ruleTable["new.json"] = ruleSet{saved.rules, formatJSON}
	defer delete(ruleTable, "new.json")
	ruleTable["settings.json"] = saved
	if RulesDigest() == before {
		t.Fatal("a new table entry must change the digest")
	}
}
