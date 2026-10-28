package sessioncapture

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func assetFixture(n int) []EnrichmentAsset {
	out := make([]EnrichmentAsset, n)
	for i := range out {
		out[i] = EnrichmentAsset{Path: fmt.Sprintf("/synthetic/asset-%d", i), Identities: []string{"a", "b"}, Scope: "s", Selector: "x", Kind: "k"}
	}
	return out
}

func assetResult(n int, assets []EnrichmentAsset) enrichmentResult {
	return enrichmentResult{Spec: EnrichmentSpec{Kind: "snapshot", SelectionVersion: fmt.Sprintf("v%d", n)}, Assets: assets, Complete: true, Missing: []string{"m"}, ObservedAtNS: int64(n)}
}

func assetBudget(t *testing.T, q *Queue) (used int64) {
	t.Helper()
	if err := q.db.QueryRowContext(t.Context(), `SELECT used FROM metadata_budgets WHERE name='enrichment'`).Scan(&used); err != nil {
		t.Fatal(err)
	}
	return used
}

func assetWant(t *testing.T, q *Queue) int64 {
	t.Helper()
	var want int64
	if err := q.db.QueryRowContext(t.Context(), `SELECT coalesce((SELECT SUM(128+length(id)+length(body)) FROM enrichment_results),0)+coalesce((SELECT SUM(128+length(id)+length(body)) FROM enrichment_asset_lists),0)+coalesce((SELECT SUM(`+budgetExpression("enrichment_work", "enrichment_work")+`) FROM enrichment_work),0)+coalesce((SELECT SUM(`+budgetExpression("optional_repairs", "optional_repairs")+`) FROM optional_repairs),0)`).Scan(&want); err != nil {
		t.Fatal(err)
	}
	return want
}

func assetQueue(t *testing.T) *Queue {
	dir, o, _ := setup(t)
	return open(t, dir, o)
}

// legacy inserts an inline row exactly as the old code stored it.
func legacy(t *testing.T, q *Queue, body []byte) string {
	t.Helper()
	id := digest(body)
	if _, err := q.db.ExecContext(t.Context(), `INSERT INTO enrichment_results(id,body) VALUES(?,?)`, id, body); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestAssetListsChargedOnce(t *testing.T) {
	q := assetQueue(t)
	assets := assetFixture(40)
	list, _ := json.Marshal(assets)
	var ids []string
	for i := range 20 {
		id, err := q.storeEnrichmentResult(t.Context(), assetResult(i, assets))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	var lists int
	var rows int64
	_ = q.db.QueryRowContext(t.Context(), `SELECT count(*) FROM enrichment_asset_lists`).Scan(&lists)
	_ = q.db.QueryRowContext(t.Context(), `SELECT sum(length(body)) FROM enrichment_results`).Scan(&rows)
	if lists != 1 || rows*4 > 20*int64(len(list)) {
		t.Fatal("not compact", lists, rows, len(list))
	}
	if used := assetBudget(t, q); used != assetWant(t, q) {
		t.Fatal("budget drift")
	}
	for i, id := range ids {
		r, err := q.loadEnrichmentResult(t.Context(), id)
		if err != nil || len(r.Assets) != 40 || r.ObservedAtNS != int64(i) {
			t.Fatal("load", err)
		}
	}
}

func TestAssetListTamperIsIntegrity(t *testing.T) {
	for _, mode := range []string{"body", "list", "missing"} {
		t.Run(mode, func(t *testing.T) {
			q := assetQueue(t)
			id, err := q.storeEnrichmentResult(t.Context(), assetResult(1, assetFixture(30)))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := q.loadEnrichmentResult(t.Context(), id); err != nil {
				t.Fatal(err)
			}
			var stmt string
			switch mode {
			case "body":
				stmt = `UPDATE enrichment_results SET body=replace(body,'"Complete":true','"Complete":fals_')`
			case "list":
				stmt = `UPDATE enrichment_asset_lists SET body=replace(body,'asset-1','asset-9')`
			default:
				stmt = `DELETE FROM enrichment_asset_lists`
			}
			if _, err := q.db.ExecContext(t.Context(), stmt); err != nil {
				t.Fatal(err)
			}
			if _, err := q.loadEnrichmentResult(t.Context(), id); !errors.Is(err, ErrIntegrity) {
				t.Fatal("want integrity", err)
			}
		})
	}
}

func TestLegacyRowsCompactAtInit(t *testing.T) {
	dir, o, _ := setup(t)
	q := open(t, dir, o)
	assets := assetFixture(60)
	var ids []string
	for i := range 70 {
		b, _ := json.Marshal(assetResult(i, assets))
		ids = append(ids, legacy(t, q, b))
	}
	before := assetBudget(t, q)
	if err := q.initEnrichment(t.Context()); err != nil {
		t.Fatal(err)
	}
	after := assetBudget(t, q)
	if after >= before/5 || after != assetWant(t, q) {
		t.Fatal("not shrunk", before, after)
	}
	for _, id := range ids {
		if _, err := q.loadEnrichmentResult(t.Context(), id); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	_ = q.db.QueryRowContext(t.Context(), `SELECT count(*) FROM enrichment_results WHERE instr(body,'assetlist:')>0`).Scan(&n)
	if n != 70 {
		t.Fatal("compacted", n)
	}
	var snap string
	_ = q.db.QueryRowContext(t.Context(), `SELECT group_concat(id||body) FROM enrichment_results`).Scan(&snap)
	if err := q.initEnrichment(t.Context()); err != nil {
		t.Fatal(err)
	}
	var again string
	_ = q.db.QueryRowContext(t.Context(), `SELECT group_concat(id||body) FROM enrichment_results`).Scan(&again)
	if again != snap {
		t.Fatal("second init changed rows")
	}
	if u := assetBudget(t, q); u != after {
		t.Fatal("budget moved")
	}
}

func TestNearFullBudgetCompacts(t *testing.T) {
	dir, o, _ := setup(t)
	q := open(t, dir, o)
	assets := assetFixture(60)
	var ids []string
	for i := range 5 {
		b, _ := json.Marshal(assetResult(i, assets))
		ids = append(ids, legacy(t, q, b))
	}
	used := assetBudget(t, q)
	if _, err := q.db.ExecContext(t.Context(), `UPDATE metadata_budgets SET limit_bytes=? WHERE name='enrichment'`, used+3); err != nil {
		t.Fatal(err)
	}
	if err := q.initEnrichment(t.Context()); err != nil {
		t.Fatal(err)
	}
	if after := assetBudget(t, q); after >= used/2 || after != assetWant(t, q) {
		t.Fatal("not compacted", used, after)
	}
	for _, id := range ids {
		if _, err := q.loadEnrichmentResult(t.Context(), id); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNonCanonicalRowStaysInline(t *testing.T) {
	q := assetQueue(t)
	canon, _ := json.Marshal(assetResult(1, assetFixture(30)))
	variants := map[string][]byte{
		"extra":   []byte(strings.Replace(string(canon), `{"Spec"`, `{"Extra":1,"Spec"`, 1)),
		"spaced":  []byte(strings.Replace(string(canon), `"Assets":`, `"Assets": `, 1)),
		"reorder": []byte(`{"Complete":true,` + string(canon[1:])),
	}
	var ids []string
	for _, b := range variants {
		ids = append(ids, legacy(t, q, b))
	}
	if err := q.initEnrichment(t.Context()); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = q.db.QueryRowContext(t.Context(), `SELECT count(*) FROM enrichment_results WHERE instr(body,'assetlist:')>0`).Scan(&n)
	if n != 0 {
		t.Fatal("non-canonical row compacted")
	}
	for _, id := range ids {
		if _, err := q.loadEnrichmentResult(t.Context(), id); err != nil {
			t.Fatal(err)
		}
	}
}

func TestStatusReportsBothForms(t *testing.T) {
	dir, o, _ := setup(t)
	q := open(t, dir, o)
	assets := assetFixture(30)
	compactID, err := q.storeEnrichmentResult(t.Context(), assetResult(1, assets))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(assetResult(2, assets))
	inlineID := legacy(t, q, b)
	for i, rid := range []string{compactID, inlineID} {
		spec, _ := json.Marshal(EnrichmentSpec{Kind: "snapshot", Identity: assetResult(0, nil).Spec.Identity})
		if _, err := q.db.ExecContext(t.Context(), `INSERT INTO enrichment_work(id,session,spec,contract,result) VALUES(?,?,?,?,?)`, fmt.Sprint("w", i), "s", spec, "c", []byte(rid)); err != nil {
			t.Fatal(err)
		}
	}
	var complete, missing int
	_ = q.db.QueryRowContext(t.Context(), `SELECT sum(coalesce(json_extract(r.body,'$.Complete'),0)),sum(json_array_length(json_extract(r.body,'$.Missing'))) FROM enrichment_work w JOIN enrichment_results r ON r.id=CAST(w.result AS TEXT)`).Scan(&complete, &missing)
	if complete != 2 || missing != 2 {
		t.Fatal("status", complete, missing)
	}
}
