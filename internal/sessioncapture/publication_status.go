package sessioncapture

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/mich-murphy/memory/internal/publication"
)

const PublicationHeadsContract = "session-publication-heads/v1"

// PublicationHead identifies the last locally acknowledged publication, not the
// latest capture attempt. It deliberately contains no source paths or bodies.
type PublicationHead struct {
	Identity      publication.Identity `json:"identity"`
	Sequence      uint64               `json:"sequence"`
	PublicationID string               `json:"publication_id"`
}

// PublicationHeads is one lexical page of acknowledged heads. A cursor is only a
// position within a sweep, never a permanent publication watermark: updates to
// earlier sessions are found by starting another sweep with an empty cursor.
// Separate pages need not represent a globally atomic archive snapshot.
type PublicationHeads struct {
	Contract   string            `json:"contract"`
	ArchiveID  string            `json:"archive_id"`
	Heads      []PublicationHead `json:"heads"`
	NextCursor string            `json:"next_cursor"`
	Complete   bool              `json:"complete"`
}

// ReadPublicationHeads reads the authoritative jobs table while its coordinator
// owns the queue. Like ReadRuntime, it uses a private readonly SQLite binding:
// no flock, schema initialization, claims reset, backend or credentials.
func ReadPublicationHeads(ctx context.Context, spool, archive, afterSession string, limit int) (PublicationHeads, error) {
	page := PublicationHeads{Contract: PublicationHeadsContract, ArchiveID: archive, Heads: []PublicationHead{}}
	if !validPublicationCoordinate(archive) || limit < 1 || limit > 256 || !validPublicationCursor(afterSession, archive) {
		return page, errors.New("sessioncapture: invalid publication status arguments")
	}
	q, err := readDiagnosticQueue(ctx, spool, archive)
	if err != nil {
		return page, errors.New("sessioncapture: publication status database unavailable or binding mismatch")
	}
	defer func() { _ = q.db.Close() }()
	// Bound bytes before transferring a manifest out of SQLite. Oversized metadata
	// remains an error, rather than becoming an absent head. Fetch one lookahead
	// coordinate so complete is exact even when the page is full.
	rows, err := q.db.QueryContext(ctx, `SELECT j.session,j.sequence,length(CAST(j.manifest AS BLOB)),CASE WHEN length(CAST(j.manifest AS BLOB))<=? THEN j.manifest ELSE NULL END
 FROM jobs j JOIN (SELECT session,MAX(sequence) AS sequence FROM jobs WHERE state='published' AND session>? GROUP BY session ORDER BY session LIMIT ?) h
 ON j.session=h.session AND j.sequence=h.sequence ORDER BY j.session`, publication.MaxMetadata, afterSession, limit+1)
	if err != nil {
		return page, errors.New("sessioncapture: publication status query failed")
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var session string
		var sequence uint64
		var size int64
		var body []byte
		if err := rows.Scan(&session, &sequence, &size, &body); err != nil {
			return page, ErrIntegrity
		}
		if session == "" || !validPublicationCursor(session, archive) {
			return page, ErrIntegrity
		}
		if len(page.Heads) == limit {
			return page, nil
		}
		var m publication.Manifest
		if size <= 0 || size > publication.MaxMetadata || json.Unmarshal(body, &m) != nil || publication.ValidateManifest(m) != nil || m.Identity.ArchiveID != archive || m.Identity.BaseKey() != session || m.Sequence != sequence {
			return page, ErrIntegrity
		}
		page.Heads = append(page.Heads, PublicationHead{Identity: m.Identity, Sequence: m.Sequence, PublicationID: m.PublicationID})
		page.NextCursor = session
	}
	if err := rows.Err(); err != nil {
		return page, errors.New("sessioncapture: publication status read failed")
	}
	page.Complete = true
	return page, nil
}

func validPublicationCoordinate(s string) bool {
	return s != "" && len(s) <= publication.MaxMetadata && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}

func validPublicationCursor(cursor, archive string) bool {
	if cursor == "" {
		return true
	}
	// BaseKey is canonical lowercase hex, with fixed labels and four nonempty
	// UTF-8 coordinates. Reject other archives and arbitrary SQL/key cursors.
	parts := strings.Split(cursor, "/")
	if len(parts) != 9 || parts[0] != publication.Prefix || parts[1] != "archives" || parts[3] != "machines" || parts[5] != "agents" || parts[7] != "sessions" {
		return false
	}
	var values [4]string
	for n, p := range []string{parts[2], parts[4], parts[6], parts[8]} {
		if len(p) > 2*publication.MaxMetadata {
			return false
		}
		b, err := hex.DecodeString(p)
		if err != nil || hex.EncodeToString(b) != p || !validPublicationCoordinate(string(b)) {
			return false
		}
		values[n] = string(b)
	}
	return values[0] == archive
}
