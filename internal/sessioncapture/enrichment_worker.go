package sessioncapture

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/mich-murphy/memory/internal/publication"
)

func (q *Queue) enrichmentNext(ctx context.Context, o RuntimeOptions) (did bool, resultErr error) {
	if err := q.retryEnrichmentAdmission(ctx); err != nil {
		return false, err
	}
	var id string
	var body, saved []byte
	var attempts int
	err := q.db.QueryRowContext(ctx, `SELECT id,spec,attempts,result FROM enrichment_work WHERE state IN ('pending','retry','ready') AND next_retry<=? ORDER BY attempts,next_retry,rowid LIMIT 1`, q.opts.Now().UnixNano()).Scan(&id, &body, &attempts, &saved)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var spec EnrichmentSpec
	if json.Unmarshal(body, &spec) != nil {
		// An undecodable authoritative spec cannot become valid by retrying.
		// Isolate it durably without invoking callbacks or blocking later work.
		save, cancel := context.WithTimeout(context.WithoutCancel(ctx), o.OperationTimeout)
		defer cancel()
		_, err := q.db.ExecContext(save, `UPDATE enrichment_work SET state='quarantined',attempts=attempts+1,last_error='integrity-error',next_retry=0,software=? WHERE id=? AND state IN ('pending','retry','ready')`, o.SoftwareVersion, id)
		return true, metadataError(err)
	}
	claim, err := q.db.ExecContext(ctx, `UPDATE enrichment_work SET state='working',attempts=attempts+1,software=? WHERE id=? AND state IN ('pending','retry','ready') AND attempts=?`, o.SoftwareVersion, id, attempts)
	if err != nil {
		return true, err
	}
	claimed, err := claim.RowsAffected()
	if err != nil || claimed == 0 {
		return false, err
	}
	defer q.releaseEnrichment(ctx, id, attempts+1)
	defer func() {
		if !shutdownCancellation(ctx, resultErr) {
			return
		}
		save, cancel := context.WithTimeout(context.WithoutCancel(ctx), o.OperationTimeout)
		defer cancel()
		// Keep a durable ready result (or completed attachment). Only an
		// interrupted working claim needs releasing back to retry.
		released := "retry"
		if len(saved) > 0 {
			released = "ready"
		}
		_, err := q.db.ExecContext(save, `UPDATE enrichment_work SET state=CASE WHEN state='working' THEN ? ELSE state END,attempts=?,last_error='shutdown',next_retry=0 WHERE id=? AND attempts=? AND state IN ('working','ready')`, released, attempts, id, attempts+1)
		resultErr = errors.Join(resultErr, metadataError(err))
	}()
	var result enrichmentResult
	if len(saved) > 0 {
		result, err = q.loadEnrichmentResult(ctx, string(saved))
	} else {
		result, err = q.buildEnrichment(ctx, spec, o)
	}
	if err != nil {
		return true, q.finishEnrichment(ctx, id, attempts+1, err, o)
	}
	resultID, err := q.storeEnrichmentResult(ctx, result)
	if err != nil {
		return true, q.finishEnrichment(ctx, id, attempts+1, err, o)
	}
	if err := q.fault("enrichment-result-stored"); err != nil {
		return true, err
	}
	// Once the result is stored, retain its pointer even if shutdown arrives
	// before attachment. Restart must not need the optional callback again.
	save, cancel := context.WithTimeout(context.WithoutCancel(ctx), o.OperationTimeout)
	defer cancel()
	if _, err := q.db.ExecContext(save, `UPDATE enrichment_work SET result=?,state='ready' WHERE id=?`, []byte(resultID), id); err != nil {
		return true, metadataError(err)
	}

	seq, err := q.attachEnrichment(ctx, spec, result, resultID, o)
	if errors.Is(err, errAttachmentBusy) {
		_, saveErr := q.db.ExecContext(ctx, `UPDATE enrichment_work SET state='ready',attempts=MAX(attempts-1,0),next_retry=? WHERE id=?`, q.opts.Now().Add(time.Second).UnixNano(), id)
		return true, saveErr
	}
	if err != nil {
		return true, q.finishEnrichment(ctx, id, attempts+1, err, o)
	}
	_, err = q.db.ExecContext(ctx, `UPDATE enrichment_work SET state='attached',attached_sequence=?,last_error='',next_retry=0 WHERE id=?`, seq, id)
	return true, err
}

func (q *Queue) finishEnrichment(ctx context.Context, id string, attempts int, cause error, o RuntimeOptions) error {
	if shutdownCancellation(ctx, cause) {
		// The claim defer refunds/releases with the original operation context.
		return cause
	}
	state := "retry"
	if attempts >= o.MaxAttempts {
		state = "quarantined"
	}
	code := string(classify(cause, o.Classify))
	save, cancel := context.WithTimeout(context.WithoutCancel(ctx), o.OperationTimeout)
	defer cancel()
	_, err := q.db.ExecContext(save, `UPDATE enrichment_work SET state=?,last_error=?,next_retry=? WHERE id=?`, state, code, q.opts.Now().Add(retryDelay(attempts, o.Jitter)).UnixNano(), id)
	// Optional source/callback outcomes retain their existing isolation. A mixed
	// cancellation tree, however, must survive bookkeeping for the caller's
	// all-leaf shutdown check rather than being silently treated as a clean stop.
	if errors.Is(cause, context.Canceled) {
		return errors.Join(runtimeFatalCause(cause), metadataError(err))
	}
	return metadataError(err)
}

// Reader-to-private-preparation is deliberately not a public trusted pathname API.
func (q *Queue) buildEnrichment(ctx context.Context, s EnrichmentSpec, o RuntimeOptions) (result enrichmentResult, resultErr error) {
	slot, cleanup, err := q.preparedSlot()
	if err != nil {
		return result, err
	}
	defer func() { resultErr = errors.Join(resultErr, cleanup()) }()
	var artifact EnrichmentArtifact
	if s.Kind == "launch" {
		var body []byte
		if err := q.db.QueryRowContext(ctx, `SELECT body FROM native_evidence WHERE id=?`, s.LaunchID).Scan(&body); err != nil {
			return result, err
		}
		artifact = EnrichmentArtifact{Body: io.NopCloser(bytes.NewReader(body)), Complete: true, ContentHash: digest(body)}
	} else {
		if o.Enrich == nil {
			return result, errors.New("enrichment unavailable")
		}
		callbackSpec := s
		callbackSpec.WorkingDirectory = slot
		artifact, err = o.Enrich(ctx, callbackSpec)
		if err != nil {
			return result, err
		}
	}
	if artifact.Body == nil {
		return result, ErrIntegrity
	}
	defer func() { resultErr = errors.Join(resultErr, artifact.Body.Close()) }()
	if len(artifact.Assets) > MaxEnrichmentAssets || len(artifact.Missing) > 128 {
		return result, ErrQuota
	}
	if err := checkAssets(ctx, s, artifact.Assets, false, o); err != nil {
		return result, err
	}
	path := filepath.Join(slot, "enrichment")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return result, err
	}
	n, copyErr := io.Copy(f, &contextReader{ctx: ctx, r: io.LimitReader(artifact.Body, MaxSourceBytes+1)})
	syncErr := f.Sync()
	closeErr := f.Close()
	if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
		return result, err
	}
	if n > MaxSourceBytes {
		return result, ErrQuota
	}
	request := Request{Identity: s.Identity, SourceID: stableEnrichmentSource(s), Role: "artifact", Path: path, Optional: true}
	obj, _, err := q.snapshot(ctx, request, path, publication.Source{})
	if err != nil {
		return result, err
	}
	if artifact.ContentHash != "" && artifact.ContentHash != obj.SHA256 {
		return result, ErrIntegrity
	}
	now := q.opts.Now().UnixNano()
	return enrichmentResult{Spec: s, Object: obj, Assets: artifact.Assets, Complete: artifact.Complete, Missing: artifact.Missing, ObservedAtNS: now, CapturedAtNS: now}, nil
}

func digestSpec(s EnrichmentSpec) string { b, _ := json.Marshal(s); return digest(b) }
func checkAssets(ctx context.Context, s EnrichmentSpec, assets []EnrichmentAsset, delivery bool, o RuntimeOptions) error {
	if len(assets) > MaxEnrichmentAssets {
		return ErrQuota
	}
	total := 0
	for _, a := range assets {
		encoded, err := json.Marshal(a)
		if err != nil {
			return ErrIntegrity
		}
		total += len(encoded)
		if total > MaxEnrichmentResultBytes-(64<<10) {
			return ErrQuota
		}
		if err := validateEnrichmentAsset(a); err != nil {
			return err
		}

		if o.CheckEnrichmentAsset == nil {
			return ErrPolicy
		}
		if err := o.CheckEnrichmentAsset(ctx, s, a, delivery); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return ErrPolicy
		}
	}
	return nil
}

func (q *Queue) attachEnrichment(ctx context.Context, s EnrichmentSpec, r enrichmentResult, resultID string, o RuntimeOptions) (uint64, error) {
	if err := checkAssets(ctx, s, r.Assets, true, o); err != nil {
		return 0, err
	}
	lock := q.session(s.Identity)
	if !lock.TryLock() {
		return 0, errAttachmentBusy
	}
	defer lock.Unlock()
	m, es, err := q.latestPolicyChecked(ctx, s.Identity)
	if err != nil {
		return 0, err
	}
	if err := q.verify(ctx, r.Object); err != nil {
		return 0, err
	}
	sourceID := stableEnrichmentSource(s)
	previous, idx := activeSource(m, sourceID)
	// Equivalent safe bytes add a durable per-basis proof/result, not a new
	// capture/publication/epoch or timestamp. The result row remains available
	// for current-basis coverage and future policy checks.
	if idx >= 0 && previous.Object == r.Object {
		return m.Sequence, nil
	}
	// The same short-cut across source ids: the main transcript and each subagent
	// attach their own artifact, often byte-identical. If an ACTIVE, SELECTED
	// artifact already holds exactly this object, attach nothing (it only ever
	// publishes less; existing copies are never sealed, withheld or changed).
	// latestPolicyChecked rehydrates withheld optional sources to their originals,
	// so selection must be read from SelectionReason, not Source.Withheld alone.
	// The asset list/result body need not match: the bytes are identical and the
	// bundle is self-contained; they were authorized by that source's own evidence,
	// and this result's assets were just checked above (delivery=true), so nothing
	// new is exposed. Residual: if the matched artifact is later sealed/replaced,
	// this work row still reports attached at this sequence, but nothing claims
	// completeness: reduceEnrichmentCoverage keeps coverage partial and
	// BasisCurrent tracks only the transcript source coordinate.
	for i := range m.Sources {
		c := m.Sources[i]
		if c.Role == "artifact" && c.State == "active" && publication.IsSelected(c) && i < len(es) && es[i].SelectionReason == "" && c.Object == r.Object {
			return m.Sequence, nil
		}
	}
	provenance, err := q.enrichmentSourceRequest(ctx, s)
	if err != nil {
		return 0, err
	}
	request := Request{Identity: s.Identity, SourceID: sourceID, Role: "artifact", Path: provenance.Path, LogicalPath: provenance.LogicalPath, Identities: slices.Clone(provenance.Identities), Optional: true, ObservedAtNS: r.ObservedAtNS}
	src := publication.Source{SourceID: sourceID, EpochID: digest([]byte(sourceID + ":" + r.Object.SHA256 + ":" + digestSpec(s))), Role: "artifact", Path: publicationSourcePath(request), Optional: true, State: "active", Revision: 1, ObservedAtNS: r.ObservedAtNS, CapturedAtNS: r.CapturedAtNS, Object: r.Object, Associations: []string{"enrichment:" + s.Kind, "basis:" + s.BasisPublication}}
	if len(m.Sources) >= publication.MaxSources-128 {
		return 0, ErrQuota
	}
	if idx >= 0 {
		m.Sources[idx].State = "sealed"
	}
	m.Sources = append(m.Sources, src)
	es = append(es, evidence{EpochID: src.EpochID, Request: request, ResultID: resultID})
	m.Identity = s.Identity
	m.Contract = publication.Contract
	m.Sequence++
	m.PublishedAtNS = 0
	// Attachment time is not byte capture time. A first native-only publication
	// uses the actual later observation time from its owned artifact result.
	if m.CapturedAtNS == 0 {
		m.CapturedAtNS = r.CapturedAtNS
		m.ObservedAtNS = r.ObservedAtNS
	}
	m.Coverage = reduceEnrichmentCoverage(m, es)
	if b, _ := json.Marshal(m); len(b) > publication.MaxMetadata-(256<<10) {
		return 0, ErrQuota
	}
	accepted, err := q.accept(ctx, m, es)
	return accepted.Sequence, err
}

var errAttachmentBusy = errors.New("sessioncapture: enrichment attachment busy")

// Complete means only the recorded artifact basis, never a newer raw epoch.
// Public aggregate stays partial until all discovery/native scopes are proven;
// private per-work status carries exact completeness/current-basis proof.
func reduceEnrichmentCoverage(m publication.Manifest, _ []evidence) publication.Coverage {
	c := m.Coverage
	if c.Discovery == "" {
		c.Discovery = "partial"
	}
	c.Enrichment = "partial"
	c.Missing = appendUniqueMissing(c.Missing, "enrichment-basis-scoped")
	return c
}

func appendUniqueMissing(missing []string, code string) []string {
	if slices.Contains(missing, code) {
		return missing
	}
	return append(missing, code)
}

func validateEnrichmentAsset(a EnrichmentAsset) error {
	if !boundedText(a.Path) || !boundedText(a.Scope) || !boundedText(a.Selector) || !boundedText(a.Kind) || len(a.Identities) > 64 {
		return ErrPolicy
	}
	for _, id := range a.Identities {
		if !boundedText(id) {
			return ErrPolicy
		}
	}
	return nil
}
