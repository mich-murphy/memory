package sessioncapture

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	ErrOwnershipPending   = errors.New("sessioncapture: ownership pending")
	ErrOwnershipAmbiguous = errors.New("sessioncapture: ownership ambiguous")
	ErrOwnershipCapacity  = fmt.Errorf("sessioncapture: ownership index capacity: %w", ErrQuota)
	ownerUUIDPattern      = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

const (
	ownershipLimit = 4096
	ownershipDDL   = `CREATE TABLE IF NOT EXISTS source_ownership (
 agent TEXT NOT NULL, root TEXT NOT NULL, uuid TEXT NOT NULL,
 session TEXT NOT NULL, legacy TEXT NOT NULL, main_path TEXT NOT NULL,
 ambiguous INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(agent,root,uuid,session,legacy,main_path))`
)

// OwnerUUID recognizes only a complete UUID main filename, not transcript content.
func OwnerUUID(path string) string {
	name := strings.TrimSuffix(filepath.Base(path), ".zst")
	name, ok := strings.CutSuffix(name, ".jsonl")
	if !ok || !ownerUUIDPattern.MatchString(name) {
		return ""
	}
	return name
}

// OwnershipSession validates the full-root namespace and original adapter coordinate.
// Claims are structural metadata, never authority to bypass capture/delivery policy.
func OwnershipSession(root, session, legacy string) bool {
	sum := sha256.Sum256([]byte(root))
	return filepath.IsAbs(root) && filepath.Clean(root) == root && !strings.Contains(root, "\x00") && legacy != "" && session == "session/"+hex.EncodeToString(sum[:])+"/"+legacy
}

// Duplicate or empty coordinates are invalid, even if the repeated claim agrees.
func singleOwnershipAssociation(associations []string, prefix string) string {
	value := ""
	seen := false
	for _, association := range associations {
		if v, ok := strings.CutPrefix(association, prefix); ok {
			if seen {
				return ""
			}
			value, seen = v, true
		}
	}
	return value
}

func ownershipClaim(r Request) (root, uuid, legacy string, err error) {
	if r.Identity.Agent != "claude" || r.Role != "main" {
		return "", "", "", ErrPolicy
	}
	root = singleOwnershipAssociation(r.Associations, "root:")
	legacy = singleOwnershipAssociation(r.Associations, "adapter-session:")
	uuid = OwnerUUID(r.Path)
	if uuid == "" || !OwnershipSession(root, r.Identity.SessionID, legacy) || !filepath.IsAbs(r.Path) || filepath.Clean(r.Path) != r.Path {
		return "", "", "", ErrPolicy
	}
	rel, e := filepath.Rel(root, r.Path)
	if e != nil {
		return "", "", "", ErrPolicy
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) != 3 || parts[0] != "projects" || parts[1] == "" || parts[1] == "." || parts[1] == ".." {
		return "", "", "", ErrPolicy
	}
	local := parts[1] + "/" + uuid
	sum := sha256.Sum256([]byte(root))
	prefix := "roots/" + hex.EncodeToString(sum[:])[:10] + "/"
	if legacy != local && legacy != prefix+local {
		return "", "", "", ErrPolicy
	}
	expected := filepath.ToSlash(rel)
	if strings.HasPrefix(legacy, prefix) {
		expected = prefix + expected
	}
	if r.SourceID != expected {
		return "", "", "", ErrPolicy
	}
	return root, uuid, legacy, nil
}

// RecordOwnership retains observed main-file coordinates independently of byte capture.
// It never parses source bodies, marks progress Captured, or garbage-collects evidence.
// A conflicting observation is committed before returning ErrOwnershipAmbiguous.
func (q *Queue) RecordOwnership(ctx context.Context, r Request) error {
	if r.Identity.Agent != "claude" || r.Role != "main" {
		return nil
	}
	var err error
	r, err = q.prepareRequest(r)
	if err != nil {
		return err
	}
	logical, err := q.ownershipLogicalPath(r)
	if err != nil {
		return err
	}
	if OwnerUUID(logical) == "" {
		return nil
	}
	if _, err = q.check(ctx, evidence{Request: r}, false); err != nil {
		return err
	}
	// Only the inventory-derived logical pathname enters structural ownership.
	// Observed/resolved identities were checked above and remain capture evidence.
	r.Path = logical
	root, uuid, legacy, err := ownershipClaim(r)
	if err != nil {
		return err
	}
	if _, err = q.db.ExecContext(ctx, ownershipDDL); err != nil {
		return err
	}
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var exists int
	err = tx.QueryRowContext(ctx, `SELECT count(*) FROM source_ownership WHERE agent=? AND root=? AND uuid=? AND session=? AND legacy=? AND main_path=?`, r.Identity.Agent, root, uuid, r.Identity.SessionID, legacy, r.Path).Scan(&exists)
	if err != nil {
		return err
	}
	if exists == 0 {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM source_ownership`).Scan(&count); err != nil {
			return err
		}
		if count >= ownershipLimit {
			return invalidateOwnershipAtCapacity(ctx, tx, r.Identity.Agent, root, uuid)
		}

		if _, err = tx.ExecContext(ctx, `INSERT INTO source_ownership (agent,root,uuid,session,legacy,main_path) VALUES (?,?,?,?,?,?)`, r.Identity.Agent, root, uuid, r.Identity.SessionID, legacy, r.Path); err != nil {
			return err
		}
	}
	_, _, lookupErr := lookupOwnership(ctx, tx, r.Identity.Agent, root, uuid)
	if err := tx.Commit(); err != nil {
		return err
	}
	return lookupErr
}

// No rows are removed. A rejected conflicting owner must not leave a unique
// lookup usable; commit the invalidation before returning explicit backpressure.
func invalidateOwnershipAtCapacity(ctx context.Context, tx *sql.Tx, agent, root, uuid string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE source_ownership SET ambiguous=1 WHERE agent=? AND root=? AND uuid=?`, agent, root, uuid); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return ErrOwnershipCapacity
}

type ownershipReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func lookupOwnership(ctx context.Context, db ownershipReader, agent, root, uuid string) (string, string, error) {
	if agent != "claude" || !filepath.IsAbs(root) || filepath.Clean(root) != root || !ownerUUIDPattern.MatchString(uuid) {
		return "", "", ErrPolicy
	}
	// The primary key bounds this to one indexed root/UUID range; LIMIT 2 detects
	// ambiguity without choosing a winner or enumerating the entire source tree.
	var session, legacy string
	var count, ambiguous int
	err := db.QueryRowContext(ctx, `SELECT coalesce(min(session),''),coalesce(min(legacy),''),count(*),coalesce(max(ambiguous),0) FROM (SELECT DISTINCT session,legacy,ambiguous FROM source_ownership WHERE agent=? AND root=? AND uuid=? LIMIT 2)`, agent, root, uuid).Scan(&session, &legacy, &count, &ambiguous)
	if err != nil {
		if strings.Contains(err.Error(), "no such table: source_ownership") {
			return "", "", ErrOwnershipPending
		}
		return "", "", err
	}
	if count == 0 {
		return "", "", ErrOwnershipPending
	}
	if count > 1 || ambiguous != 0 {
		return "", "", ErrOwnershipAmbiguous
	}
	if !OwnershipSession(root, session, legacy) {
		return "", "", ErrIntegrity
	}
	return session, legacy, nil
}

func (q *Queue) LookupOwnership(ctx context.Context, agent, root, uuid string) (string, string, error) {
	return lookupOwnership(ctx, q.db, agent, root, uuid)
}

// ReadOwnership opens a read-only single lookup while Run holds the queue flock.
// It neither creates a spool/index nor changes journal settings or source metadata.
func ReadOwnership(ctx context.Context, spool, archive, agent, root, uuid string) (string, string, error) {
	path := filepath.Join(spool, "queue.sqlite")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", "", ErrOwnershipPending
	}
	if err != nil {
		return "", "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return "", "", ErrPolicy
	}
	u := url.URL{Scheme: "file", Path: path}
	db, err := sql.Open("sqlite", u.String()+"?mode=ro")
	if err != nil {
		return "", "", err
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	var bound string
	if err := db.QueryRowContext(ctx, `SELECT archive FROM binding LIMIT 1`).Scan(&bound); err != nil {
		return "", "", err
	}
	if archive == "" || bound != archive {
		return "", "", ErrPolicy
	}
	return lookupOwnership(ctx, db, agent, root, uuid)
}
