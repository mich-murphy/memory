package sessioncapture

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

const (
	MaxEnrichmentMetadataBytes = 128 << 20
	MaxRetainedNativeBytes     = 64 << 20
	MaxEnrichmentAssets        = 24000
	MaxEnrichmentResultBytes   = 2 << 20
)

// Payload quota, prepared working reservations, native inbox, retained native
// records and durable metadata are separate explicit bounds. SQLite page/WAL
// overhead still needs filesystem reserve; this bounds logical serialized bytes.
// Triggers enforce reservations in the same FULL transaction as every mutation.
func (q *Queue) initMetadataBudgets(ctx context.Context) error {
	if _, err := q.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS metadata_budgets(name TEXT PRIMARY KEY,used INTEGER NOT NULL,limit_bytes INTEGER NOT NULL)`); err != nil {
		return err
	}
	for _, b := range []struct {
		name  string
		limit int
	}{{"enrichment", MaxEnrichmentMetadataBytes}, {"native", MaxRetainedNativeBytes}} {
		if _, err := q.db.ExecContext(ctx, `INSERT OR IGNORE INTO metadata_budgets VALUES(?,0,?)`, b.name, b.limit); err != nil {
			return err
		}
	}
	tables := []struct{ name, budget string }{
		{"native_evidence", "native"},
		{"enrichment_work", "enrichment"},
		{"enrichment_results", "enrichment"},
		{"enrichment_asset_lists", "enrichment"},
		{"optional_repairs", "enrichment"},
	}
	for _, table := range tables {
		if err := installBudgetTriggers(ctx, q.db, table.name, table.budget); err != nil {
			return err
		}
	}
	// Existing retained rows are charged too, including restart after upgrade.
	for name, query := range map[string]string{
		"native":     `SELECT coalesce(SUM(128+length(CAST(id AS BLOB))+length(body)),0) FROM native_evidence`,
		"enrichment": `SELECT coalesce((SELECT SUM(128+length(CAST(id AS BLOB))+length(CAST(session AS BLOB))+length(spec)+coalesce(length(result),0)+max(32,length(state))+max(64,length(last_error))+max(64,length(contract))+max(256,length(software))) FROM enrichment_work),0)+coalesce((SELECT SUM(128+length(id)+length(body)) FROM enrichment_results),0)+coalesce((SELECT SUM(128+length(id)+length(body)) FROM enrichment_asset_lists),0)+coalesce((SELECT SUM(` + budgetExpression("optional_repairs", "optional_repairs") + `) FROM optional_repairs),0)`,
	} {
		var used int64
		if err := q.db.QueryRowContext(ctx, query).Scan(&used); err != nil {
			return err
		}
		if _, err := q.db.ExecContext(ctx, `UPDATE metadata_budgets SET used=? WHERE name=?`, used, name); err != nil {
			return err
		}
	}

	return nil
}

func installBudgetTriggers(ctx context.Context, db *sql.DB, table, budget string) error {
	// SQL identifiers/expressions are package constants, never source metadata.
	for _, op := range []string{"INSERT", "UPDATE"} {
		next := budgetExpression(table, "NEW")
		old := "0"
		if op == "UPDATE" {
			old = budgetExpression(table, "OLD")
		}
		check := fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS budget_%s_%s BEFORE %s ON %s BEGIN SELECT CASE WHEN (SELECT used+(%s)-(%s)>limit_bytes FROM metadata_budgets WHERE name='%s') THEN RAISE(ABORT,'sessioncapture: metadata quota exceeded') END; END`, table, op, op, table, next, old, budget)
		charge := fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS charge_%s_%s AFTER %s ON %s BEGIN UPDATE metadata_budgets SET used=used+(%s)-(%s) WHERE name='%s'; END`, table, op, op, table, next, old, budget)
		for _, statement := range []string{fmt.Sprintf("DROP TRIGGER IF EXISTS budget_%s_%s", table, op), fmt.Sprintf("DROP TRIGGER IF EXISTS charge_%s_%s", table, op), check, charge} {
			if _, err := db.ExecContext(ctx, statement); err != nil {
				return err
			}
		}
	}
	return nil
}

func budgetExpression(table, prefix string) string {
	fields := map[string][]string{
		"native_evidence":        {"id", "body"},
		"enrichment_work":        {"id", "session", "spec", "result", "state", "last_error", "contract", "software"},
		"enrichment_results":     {"id", "body"},
		"enrichment_asset_lists": {"id", "body"},
		"optional_repairs":       {"id", "session", "source_hash", "epoch", "commitment", "source", "evidence", "identity", "state", "last_error", "stage", "contract", "software"},
	}
	var expression strings.Builder
	expression.WriteString("128")
	for _, field := range fields[table] {
		switch field {
		case "state":
			expression.WriteString("+max(32,length(" + prefix + ".state))")
		case "last_error":
			expression.WriteString("+max(64,length(" + prefix + ".last_error))")
		case "contract":
			expression.WriteString("+max(64,length(" + prefix + ".contract))")
		case "software":
			expression.WriteString("+max(256,length(" + prefix + ".software))")
		default:
			expression.WriteString("+coalesce(length(CAST(" + prefix + "." + field + " AS BLOB)),0)")
		}
	}
	return expression.String()
}
