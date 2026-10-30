package state

import (
	"errors"
	"fmt"
)

const (
	archiveCurrentPageStreams = 64
	archiveCurrentPageBytes   = 16 << 20
)

// currentArchivePageQuery validates all committed descriptors, but fetches only
// each selected tip's body. The nonunique covering index deliberately permits
// corrupt branches to be stored and rejected, rather than hidden on insertion.
const currentArchivePageQuery = `WITH page AS MATERIALIZED (
 SELECT stream,COUNT(*) AS n,MAX(sequence) AS maximum,COUNT(DISTINCT sequence) AS distinct_n
 FROM archive_records INDEXED BY archive_current
 WHERE kind=? AND stream>=COALESCE(?,'') AND committed=1 AND (? IS NULL OR stream>?)
 GROUP BY stream ORDER BY stream LIMIT 64
), tips AS MATERIALIZED (
 SELECT page.n,page.maximum,page.distinct_n,
 (SELECT COUNT(*) FROM archive_records AS a
 WHERE a.kind=? AND a.stream=page.stream AND a.committed=1 AND
 ((a.sequence=1 AND a.parent<>'') OR (a.sequence>1 AND NOT EXISTS
 (SELECT 1 FROM archive_records AS b WHERE b.kind=a.kind AND b.stream=a.stream
 AND b.committed=1 AND b.sequence=a.sequence-1 AND b.id=a.parent)))) AS invalid,
 tip.key,length(tip.body) AS size,page.stream
 FROM page JOIN archive_records AS tip ON tip.key=(
 SELECT key FROM archive_records WHERE kind=? AND stream=page.stream AND committed=1
 ORDER BY sequence DESC LIMIT 1)
), bounded AS MATERIALIZED (
 SELECT tips.*,SUM(size) OVER (ORDER BY stream) AS total,
 ROW_NUMBER() OVER (ORDER BY stream) AS position FROM tips
)
SELECT bounded.n,bounded.maximum,bounded.distinct_n,bounded.invalid,
 tip.key,tip.kind,tip.stream,tip.id,tip.parent,tip.sequence,tip.committed,tip.body
 FROM bounded JOIN archive_records AS tip ON tip.key=bounded.key
 WHERE bounded.position=1 OR bounded.total<=?
 ORDER BY bounded.stream`

// CurrentArchivePage returns fresh validated tips in stream order, strictly after
// after when nonnil. A nil boundary includes the empty stream, which must not be
// silently skipped even if its publication is malformed. No cursor or transaction
// remains open on return. There is no persistent tip cache or global snapshot.
//
// Pages retain at most 64 tips and 16 MiB of bodies, except that the first body
// always makes progress. SQL selects the byte-bounded prefix using lengths before
// fetching bodies, so even a temporary sorter cannot retain 64 oversized bodies.
// Historical bodies are never loaded.
func (s *State) CurrentArchivePage(kind string, after *string) ([]Record, error) {
	return s.currentArchivePage(kind, after, archiveCurrentPageBytes)
}

func (s *State) currentArchivePage(kind string, after *string, budget int) ([]Record, error) {
	var boundary any
	if after != nil {
		boundary = *after
	}
	rows, err := s.conn.QueryContext(s.ctx, currentArchivePageQuery, kind, boundary, boundary, boundary, kind, kind, budget)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() // read-only; explicit close and rows.Err below
	var records []Record
	bytes := 0
	for rows.Next() {
		var r Record
		var count, maximum, distinct, invalid, sequence int64
		if err := rows.Scan(&count, &maximum, &distinct, &invalid, &r.Key, &r.Kind,
			&r.Stream, &r.ID, &r.Parent, &sequence, &r.Committed, &r.Body); err != nil {
			return nil, err
		}
		if count != maximum || distinct != count {
			return nil, fmt.Errorf("state archive: divergent or incomplete chain for %s", r.Stream)
		}
		if invalid != 0 {
			return nil, fmt.Errorf("state archive: missing or conflicting parent for %s", r.Stream)
		}
		if len(records) > 0 && len(r.Body) > budget-bytes {
			return nil, errors.New("state archive: current page exceeds body budget")
		}
		r.Sequence = uint64(sequence)
		records = append(records, r)
		bytes += len(r.Body)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return records, rows.Close()
}
