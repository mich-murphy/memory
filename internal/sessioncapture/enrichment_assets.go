package sessioncapture

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// Asset lists are stored once in enrichment_asset_lists, keyed by the digest of
// their exact JSON bytes. A result row may hold a compact body in which the
// top-level "Assets" array is replaced by the string "assetlist:<id>". The
// result id remains digest(original full body); loading rebuilds the original
// bytes and verifies that digest. Legacy inline rows load unchanged.
//
// Results and asset lists are never deleted (no code path removes
// enrichment_results rows), so a list cannot be orphaned. Any future result
// removal must delete a list in the same transaction only when no remaining
// compact row references it.
const (
	assetListPrefix  = "assetlist:"
	minCompactAssets = 256
	compactBatchRows = 32
)

// locateAssets returns the byte range of the top-level "Assets" value.
func locateAssets(b []byte) (int, int, bool) {
	dec := json.NewDecoder(bytes.NewReader(b))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return 0, 0, false
	}
	for dec.More() {
		kt, err := dec.Token()
		key, isString := kt.(string)
		if err != nil || !isString {
			return 0, 0, false
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return 0, 0, false
		}
		if key == "Assets" {
			end := int(dec.InputOffset())
			return end - len(raw), end, true
		}
	}
	return 0, 0, false
}

// compactEnrichmentBody compacts only canonical bodies whose rebuild is
// byte-identical. Anything else stays inline.
func compactEnrichmentBody(body []byte) (compact, list []byte, listID string, ok bool) {
	if len(body) > MaxEnrichmentResultBytes {
		return nil, nil, "", false
	}
	var r enrichmentResult
	if json.Unmarshal(body, &r) != nil {
		return nil, nil, "", false
	}
	if again, err := json.Marshal(r); err != nil || !bytes.Equal(again, body) {
		return nil, nil, "", false
	}
	start, end, found := locateAssets(body)
	if !found || end-start <= minCompactAssets || body[start] != '[' {
		return nil, nil, "", false
	}
	list = body[start:end]
	listID = digest(list)
	compact = append(append(append(append([]byte{}, body[:start]...), '"'), assetListPrefix+listID+`"`...), body[end:]...)
	rebuilt, ok2 := expandCompact(compact, func(id string) ([]byte, bool) { return list, id == listID })
	if !ok2 || !bytes.Equal(rebuilt, body) {
		return nil, nil, "", false
	}
	return compact, list, listID, true
}

// compactReference returns the asset-list id when body is in compact form.
func compactReference(body []byte) (start, end int, id string, ok bool) {
	start, end, found := locateAssets(body)
	if !found || body[start] != '"' {
		return 0, 0, "", false
	}
	var s string
	if json.Unmarshal(body[start:end], &s) != nil || !strings.HasPrefix(s, assetListPrefix) {
		return 0, 0, "", false
	}
	return start, end, strings.TrimPrefix(s, assetListPrefix), true
}

func expandCompact(compact []byte, fetch func(string) ([]byte, bool)) ([]byte, bool) {
	start, end, id, ok := compactReference(compact)
	if !ok {
		return nil, false
	}
	list, ok := fetch(id)
	if !ok || digest(list) != id {
		return nil, false
	}
	out := make([]byte, 0, len(compact)+len(list))
	out = append(append(append(out, compact[:start]...), list...), compact[end:]...)
	return out, len(out) <= MaxEnrichmentResultBytes
}

type compactItem struct {
	id           string
	old, compact []byte
}

// scanCompactBatch reads one bounded batch of inline rows after rowid last.
func (q *Queue) scanCompactBatch(ctx context.Context, last int64) (items []compactItem, lists map[string][]byte, next int64, n int, err error) {
	marker := `"Assets":"` + assetListPrefix
	rows, err := q.db.QueryContext(ctx, `SELECT rowid,id,body FROM enrichment_results WHERE rowid>? AND instr(body,CAST(? AS BLOB))=0 ORDER BY rowid LIMIT ?`, last, marker, compactBatchRows)
	if err != nil {
		return nil, nil, last, 0, err
	}
	defer func() { _ = rows.Close() }()
	lists = map[string][]byte{}
	next = last
	for rows.Next() {
		var rowid int64
		var id string
		var body []byte
		if err := rows.Scan(&rowid, &id, &body); err != nil {
			return nil, nil, last, 0, err
		}
		n++
		next = rowid
		if digest(body) != id {
			continue
		}
		if c, l, lid, ok := compactEnrichmentBody(body); ok {
			items = append(items, compactItem{id: id, old: body, compact: c})
			lists[lid] = l
		}
	}
	return items, lists, next, n, rows.Err()
}

// compactEnrichmentResults rewrites legacy inline rows in bounded transactions.
// Each batch shrinks rows before inserting their lists, so a budget at its
// limit does not block compaction. A batch that still cannot fit is left inline.
func (q *Queue) compactEnrichmentResults(ctx context.Context) (bool, error) {
	changed := false
	last := int64(0)
	for {
		items, lists, next, n, err := q.scanCompactBatch(ctx, last)
		if err != nil {
			return changed, err
		}
		if n == 0 {
			return changed, nil
		}
		last = next
		if len(items) == 0 {
			continue
		}
		err = q.applyCompaction(ctx, func(yield func(string, []byte, []byte)) {
			for _, i := range items {
				yield(i.id, i.old, i.compact)
			}
		}, lists)
		if err != nil {
			if errors.Is(metadataError(err), ErrQuota) {
				continue
			}
			return changed, err
		}
		changed = true
	}
}

func (q *Queue) applyCompaction(ctx context.Context, each func(func(string, []byte, []byte)), lists map[string][]byte) error {
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var failure error
	each(func(id string, old, compact []byte) {
		if failure != nil {
			return
		}
		res, err := tx.ExecContext(ctx, `UPDATE enrichment_results SET body=? WHERE id=? AND body=?`, compact, id, old)
		if err == nil {
			var n int64
			if n, err = res.RowsAffected(); err == nil && n != 1 {
				err = ErrIntegrity
			}
		}
		failure = err
	})
	if failure != nil {
		return failure
	}
	for id, l := range lists {
		if err := insertAssetList(ctx, tx, id, l); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// insertAssetList skips existing lists so the BEFORE INSERT budget trigger
// cannot reject a no-op insert on a full budget.
func insertAssetList(ctx context.Context, tx *sql.Tx, id string, list []byte) error {
	var one int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM enrichment_asset_lists WHERE id=?`, id).Scan(&one)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO enrichment_asset_lists(id,body) VALUES(?,?)`, id, list)
	return err
}
