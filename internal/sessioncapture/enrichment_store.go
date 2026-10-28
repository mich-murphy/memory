package sessioncapture

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/mich-murphy/memory/internal/launch"
)

const MaxEnrichmentWork = 4096

func (q *Queue) initEnrichment(ctx context.Context) error {
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS native_evidence (id TEXT PRIMARY KEY, body BLOB NOT NULL, adopted_at INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS enrichment_work (id TEXT PRIMARY KEY, session TEXT NOT NULL, spec BLOB NOT NULL, state TEXT NOT NULL DEFAULT 'pending', attempts INTEGER NOT NULL DEFAULT 0, next_retry INTEGER NOT NULL DEFAULT 0, last_error TEXT NOT NULL DEFAULT '', contract TEXT NOT NULL, software TEXT NOT NULL DEFAULT 'unknown/devel', result BLOB, attached_sequence INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE IF NOT EXISTS enrichment_results(id TEXT PRIMARY KEY,body BLOB NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS enrichment_asset_lists(id TEXT PRIMARY KEY,body BLOB NOT NULL)`,
		`UPDATE enrichment_work SET state='pending' WHERE state='working'`,
	} {
		if _, err := q.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	if err := q.dropRetiredHistory(ctx); err != nil {
		return err
	}
	if err := q.initOptionalRepair(ctx); err != nil {
		return err
	}
	if err := q.initEnrichmentAdmission(ctx); err != nil {
		return err
	}
	if err := q.initMetadataBudgets(ctx); err != nil {
		return err
	}
	// Triggers exist; shrink legacy inline rows, then recompute the charge.
	changed, err := q.compactEnrichmentResults(ctx)
	if err != nil {
		return err
	}
	if changed {
		return q.initMetadataBudgets(ctx)
	}
	return nil
}

func boundedText(s string) bool {
	return len(s) <= 4096 && utf8.ValidString(s) && !strings.ContainsAny(s, "\x00\r\n")
}

func validateNative(r NativeEvidence) ([]byte, error) {
	if r.Contract != EnrichmentContract || r.ID == "" || r.ObservedAtNS <= 0 || r.Identity != r.Request.Identity {
		return nil, ErrIntegrity
	}
	if err := validateHint(r.Request); err != nil {
		return nil, err
	}
	c := r.Context
	for _, s := range append([]string{r.ID, c.Event, c.Source, c.CWD, c.NativeSessionID, c.Model, c.Provider, c.PermissionMode, c.AgentVersion, c.ContextOrigin}, c.Missing...) {
		if !boundedText(s) {
			return nil, ErrPolicy
		}
	}
	if len(c.Missing) > 64 || len(c.Argv) > 32 || len(c.Environment) > 8 {
		return nil, ErrQuota
	}
	r.Context.Argv = launch.SanitizeNativeArgs(c.Argv)
	env := make([]string, 0, len(c.Environment))
	for k, v := range c.Environment {
		if !boundedText(k) || !boundedText(v) {
			return nil, ErrPolicy
		}
		env = append(env, k+"="+v)
	}
	r.Context.Environment = launch.SanitizeNativeEnvironment(env)
	b, err := json.Marshal(r)
	if len(b) > MaxNativeEvidenceBytes {
		return nil, ErrQuota
	}
	return b, err
}

func validateSpec(s EnrichmentSpec) ([]byte, error) {
	if !slices.Contains([]string{"snapshot", "launch"}, s.Kind) || s.Identity.ArchiveID == "" || s.Identity.MachineID == "" || s.Identity.Agent == "" || s.Identity.SessionID == "" || s.SelectionVersion == "" {
		return nil, ErrIntegrity
	}
	if s.Kind == "snapshot" && s.LaunchID == "" && (s.BasisPublication == "" || s.BasisSequence == 0 || s.Source.SourceID == "" || s.Source.EpochID == "" || s.Source.Revision == 0 || len(s.Source.SHA256) != 64) {
		return nil, ErrIntegrity
	}
	if s.Kind == "launch" && s.LaunchID == "" {
		return nil, ErrIntegrity
	}
	for _, v := range []string{s.Kind, s.BasisPublication, s.LaunchID, s.Root, s.AdapterSessionKey, s.CWD, s.SelectionVersion, s.Source.SourceID, s.Source.Path, s.Source.EpochID, s.Source.SHA256, s.Identity.ArchiveID, s.Identity.MachineID, s.Identity.Agent, s.Identity.SessionID} {
		if !boundedText(v) {
			return nil, ErrPolicy
		}
	}
	return json.Marshal(s)
}

func enqueueEnrichmentTx(ctx context.Context, tx *sql.Tx, spec EnrichmentSpec) error {
	body, err := validateSpec(spec)
	if err != nil {
		return err
	}
	id := digest(body)
	var existing int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM enrichment_work WHERE id=?`, id).Scan(&existing); err == nil {
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO enrichment_work(id,session,spec,contract) SELECT ?,?,?,? WHERE EXISTS(SELECT 1 FROM enrichment_work WHERE id=?) OR (SELECT COUNT(*) FROM enrichment_work)<?`, id, spec.Identity.BaseKey(), body, EnrichmentContract, id, MaxEnrichmentWork)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM enrichment_work WHERE id=?`, id).Scan(&exists); err != nil {
			return ErrQuota
		}
	}
	return nil
}

func (q *Queue) EnqueueEnrichment(ctx context.Context, spec EnrichmentSpec) (resultErr error) {
	defer func() { resultErr = metadataError(resultErr) }()
	if spec.Identity.ArchiveID != q.opts.ArchiveID {
		return ErrPolicy
	}
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := enqueueEnrichmentTx(ctx, tx, spec); err != nil {
		return err
	}
	return tx.Commit()
}

// AcceptNativeEvidence adopts immutable metadata without requiring a transcript
// file. Policy still runs against its declared source coordinate before adoption.
func (q *Queue) AcceptNativeEvidence(ctx context.Context, r NativeEvidence) (resultErr error) {
	defer func() { resultErr = metadataError(resultErr) }()
	body, err := validateNative(r)
	if err != nil {
		return err
	}
	if _, err = q.check(ctx, evidence{Request: r.Request}, true); err != nil {
		return err
	}
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var previous []byte
	err = tx.QueryRowContext(ctx, `SELECT body FROM native_evidence WHERE id=?`, r.ID).Scan(&previous)
	if err == nil {
		if !bytes.Equal(previous, body) {
			return ErrIntegrity
		}
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM native_evidence`).Scan(&count); err != nil {
		return err
	}
	if count >= MaxEnrichmentWork {
		return ErrQuota
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO native_evidence VALUES(?,?,?)`, r.ID, body, q.opts.Now().UnixNano()); err != nil {
		return err
	}
	for _, kind := range []string{"launch", "snapshot"} {
		// Native snapshots have a launch basis, not a fictitious accepted source epoch.
		root, adapter := requestCoordinates(r.Request)
		spec := EnrichmentSpec{Kind: kind, Identity: r.Identity, LaunchID: r.ID, Root: root, CWD: r.Context.CWD, AdapterSessionKey: adapter, SelectionVersion: "native-" + kind + "/v1"}
		if err := enqueueEnrichmentTx(ctx, tx, spec); err != nil {
			return err
		}
	}
	return tx.Commit()
}
