package sessioncapture

import (
	"regexp"
	"strings"
	"testing"
)

func queryPlan(t *testing.T, q *Queue, query string) string {
	t.Helper()
	rows, err := q.db.QueryContext(t.Context(), `EXPLAIN QUERY PLAN `+query, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		out = append(out, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(out, "\n")
}

func TestRunnableClaimQueriesUsePartialIndexes(t *testing.T) {
	dir, o, _ := setup(t)
	q := open(t, dir, o)
	for _, c := range []struct{ name, query, index string }{
		{"enrichment", enrichmentClaimSQL, "enrichment_work_runnable"},
		{"optional repair", optionalRepairClaimSQL, "optional_repairs_runnable"},
	} {
		plan := queryPlan(t, q, c.query)
		if !regexp.MustCompile(`USING INDEX `+c.index+`\b`).MatchString(plan) || strings.Contains(plan, "TEMP B-TREE") {
			t.Fatalf("%s plan:\n%s", c.name, plan)
		}
	}
}
