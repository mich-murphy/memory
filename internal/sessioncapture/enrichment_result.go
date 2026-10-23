package sessioncapture

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/mich-murphy/memory/internal/publication"
)

type enrichmentResult struct {
	Spec         EnrichmentSpec
	Object       publication.Object
	Assets       []EnrichmentAsset
	Complete     bool
	Missing      []string
	ObservedAtNS int64
	CapturedAtNS int64
}

func (q *Queue) storeEnrichmentResult(ctx context.Context, r enrichmentResult) (string, error) {
	body, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	if len(body) > MaxEnrichmentResultBytes || len(r.Assets) > MaxEnrichmentAssets {
		return "", ErrQuota
	}
	id := digest(body)
	var existing int
	if err := q.db.QueryRowContext(ctx, `SELECT 1 FROM enrichment_results WHERE id=?`, id).Scan(&existing); err == nil {
		return id, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	_, err = q.db.ExecContext(ctx, `INSERT OR IGNORE INTO enrichment_results(id,body) VALUES(?,?)`, id, body)
	return id, metadataError(err)
}

func (q *Queue) loadEnrichmentResult(ctx context.Context, id string) (enrichmentResult, error) {
	var r enrichmentResult
	var body []byte
	if err := q.db.QueryRowContext(ctx, `SELECT body FROM enrichment_results WHERE id=?`, id).Scan(&body); err != nil {
		if IsCallerCancellation(ctx, err) {
			return r, err
		}
		return r, errors.Join(ErrIntegrity, err)
	}
	if len(body) > MaxEnrichmentResultBytes || digest(body) != id || json.Unmarshal(body, &r) != nil {
		return r, ErrIntegrity
	}
	return r, nil
}

func metadataError(err error) error {
	if err != nil && strings.Contains(err.Error(), "sessioncapture: metadata quota exceeded") {
		return ErrQuota
	}
	return err
}

// Stable identity excludes mutable primary revision/publication and observation
// time. Different launches and selectors are distinct streams. Different safe
// content starts a retained epoch within that stream.
func stableEnrichmentSource(s EnrichmentSpec) string {
	b, _ := json.Marshal([]string{s.Kind, s.LaunchID, s.Source.SourceID, s.SelectionVersion})
	return "enrichment:" + digest(b)
}

func (q *Queue) enrichmentSourceRequest(ctx context.Context, s EnrichmentSpec) (Request, error) {
	if s.LaunchID == "" {
		if s.Source.Path == "" {
			return Request{}, ErrIntegrity
		}
		return Request{Path: s.Source.Path}, nil
	}
	var body []byte
	if err := q.db.QueryRowContext(ctx, `SELECT body FROM native_evidence WHERE id=?`, s.LaunchID).Scan(&body); err != nil {
		return Request{}, err
	}
	var native NativeEvidence
	if json.Unmarshal(body, &native) != nil || native.Identity != s.Identity {
		return Request{}, ErrIntegrity
	}
	return native.Request, nil
}
