package sessioncapture

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/mich-murphy/memory/internal/publication"
)

func (q *Queue) verify(ctx context.Context, o publication.Object) error {
	f, e := os.Open(q.payload(o))
	if e != nil {
		return fmt.Errorf("%w: unavailable encoded object: %w", ErrIntegrity, e)
	}
	defer func() { _ = f.Close() }() // Read-only descriptor; no writes to flush.
	st, e := f.Stat()
	if e != nil {
		return fmt.Errorf("%w: stat", ErrIntegrity)
	}
	if !st.Mode().IsRegular() || st.Size() != o.EncodedSize {
		return fmt.Errorf("%w: encoded size", ErrIntegrity)
	}
	h := sha256.New()
	if _, e = io.Copy(h, &contextReader{ctx: ctx, r: f}); e != nil {
		return e
	}
	if sum(h) != o.EncodedSHA256 {
		return fmt.Errorf("%w: encoded hash", ErrIntegrity)
	}
	if _, e = f.Seek(0, io.SeekStart); e != nil {
		return e
	}
	z, e := zstd.NewReader(f, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(64<<20))
	if e != nil {
		return fmt.Errorf("%w: decoder", ErrIntegrity)
	}
	defer z.Close()
	h = sha256.New()
	n, e := io.Copy(h, &contextReader{ctx: ctx, r: io.LimitReader(z, o.Size+1)})
	if e != nil {
		return fmt.Errorf("%w: decode", ErrIntegrity)
	}
	if n != o.Size || sum(h) != o.SHA256 {
		return fmt.Errorf("%w: decoded size/hash", ErrIntegrity)
	}
	return nil
}

// Deliver retries each accepted publication in sequence, including rewrites.
// A delivery-only lock orders immutable jobs. Raw capture and local acceptance
// never wait for remote operations. Errors retain the previous root and evidence.
func (q *Queue) Deliver(ctx context.Context, b publication.Backend, i publication.Identity) error {
	if err := q.InitRuntime(ctx); err != nil {
		return err
	}
	paused, err := q.destinationPaused(ctx)
	if err != nil {
		return err
	}
	if paused {
		return ErrDestination
	}
	if i.ArchiveID != q.opts.ArchiveID {
		return errors.New("sessioncapture: archive mismatch")
	}
	// Fix a finite pass boundary while capture continues independently.
	var maximum uint64
	if err := q.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(sequence),0) FROM jobs WHERE session=?", i.BaseKey()).Scan(&maximum); err != nil {
		return err
	}
	for {
		var count int
		if err := q.db.QueryRowContext(ctx, `SELECT count(*) FROM jobs WHERE session=? AND sequence<=? AND state NOT IN ('published','superseded')`, i.BaseKey(), maximum).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			return nil
		}
		optional, err := q.optionalDeliveryHead(ctx, i)
		if err != nil {
			return err
		}
		unlock, err := lockContext(ctx, q.deliveryLane(i, optional))
		if err != nil {
			return err
		}
		err = q.deliverAccepted(ctx, b, i, maximum, optional)
		unlock()
		if err != nil {
			return err
		}
	}
}

func (q *Queue) deliverAccepted(ctx context.Context, b publication.Backend, i publication.Identity, maximum uint64, optional bool) error {
	for {
		var body, ev []byte
		var seq uint64
		var attempts int
		var state string
		err := q.db.QueryRowContext(ctx, "SELECT sequence,manifest,evidence,attempts,state FROM jobs WHERE session=? AND state NOT IN ('published','superseded') AND sequence<=? ORDER BY sequence LIMIT 1", i.BaseKey(), maximum).Scan(&seq, &body, &ev, &attempts, &state)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var head publication.Manifest
		if json.Unmarshal(body, &head) != nil {
			return ErrIntegrity
		}
		if hasSelectedOptional(head) != optional {
			return nil
		}
		if state == "quarantined" {
			return ErrQuarantined
		}
		err = q.attempt(ctx, b, i, seq, body, ev)
		if errors.Is(err, errSelectionSuperseded) {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return nil
		}
		if err != nil {
			return q.recordFailure(ctx, i, seq, attempts, err)
		}
		if err := q.acknowledge(ctx, i, seq); err != nil {
			return err
		}
	}
}

func (q *Queue) deliverOne(ctx context.Context, b publication.Backend, m publication.Manifest, es []evidence) error {
	if e := q.verifyDependencies(ctx, m, es); e != nil {
		return e
	}
	body, token, e := b.Current(ctx, m.Identity.CurrentKey())
	if e != nil && !errors.Is(e, publication.ErrNotFound) {
		return e
	}
	if e == nil {
		var c publication.Current
		if e := json.Unmarshal(body, &c); e != nil {
			return e
		}
		if c.Identity != m.Identity || c.Contract != publication.Contract {
			return publication.ErrConflict
		}
		if c.PublicationID == m.PublicationID && c.Sequence == m.Sequence {
			_, err := publication.Publish(ctx, faultBackend{Backend: b, q: q, evidence: selectedEvidence(m, es), scopeEvidence: es, manifest: m}, m, token)
			return err
		}
		if c.Sequence >= m.Sequence {
			return publication.ErrConflict
		}
	}
	// Objects already in the remote current's verified manifest need no upload or
	// remote re-verification. Invariant: an object referenced by a published manifest
	// stays present until the reviewed cutover deletion of contract section 6 (section 4:
	// "Begin without automatic GC"; section 6 step 6: delete only "the reviewed retired
	// destination/key set after acceptance"). Any unreadable or mismatching prior yields
	// nil and the full path. Local payload verification still covers every object.
	if e := q.uploadDependencies(ctx, b, m, es, publication.TrustedObjects(ctx, b, m)); e != nil {
		return e
	}
	if e := q.fault("manifest-before"); e != nil {
		return e
	}
	_, e = publication.Publish(ctx, faultBackend{Backend: b, q: q, evidence: selectedEvidence(m, es), scopeEvidence: es, manifest: m}, m, token)
	if e != nil {
		return e
	}
	return q.fault("current-published")
}

// faultBackend exposes the immutable-manifest and guarded-root boundaries inside
// the shared publisher, without moving publication logic into the queue.
type faultBackend struct {
	publication.Backend
	q             *Queue
	evidence      []evidence
	scopeEvidence []evidence
	manifest      publication.Manifest
}

// Sync preserves optional backend durability fencing through this wrapper.
func (b faultBackend) Sync(ctx context.Context, key string) error {
	if durable, ok := b.Backend.(publication.DurabilityFence); ok {
		return durable.Sync(ctx, key)
	}
	return nil
}

func (b faultBackend) PutFile(ctx context.Context, key, path string) error {
	if err := b.q.recheckScopes(ctx, b.scopeEvidence); err != nil {
		return err
	}
	if e := b.q.fault("manifest-upload-before"); e != nil {
		return e
	}
	if err := b.q.recheckScopes(ctx, b.scopeEvidence); err != nil {
		return err
	}
	if e := b.q.recheck(ctx, b.evidence); e != nil {
		return e
	}
	if e := b.Backend.PutFile(ctx, key, path); e != nil {
		return e
	}
	return b.q.fault("manifest-upload-after")
}

func (b faultBackend) CompareAndSwap(ctx context.Context, key, token string, body []byte) error {
	var state string
	if err := b.q.db.QueryRowContext(ctx, `SELECT state FROM jobs WHERE session=? AND sequence=?`, b.manifest.Identity.BaseKey(), b.manifest.Sequence).Scan(&state); err != nil {
		return err
	}
	if state == "superseded" {
		return errSelectionSuperseded
	}
	if e := b.q.fault("current-before"); e != nil {
		return e
	}
	if err := b.q.recheckScopes(ctx, b.scopeEvidence); err != nil {
		return err
	}
	if e := b.q.recheck(ctx, b.evidence); e != nil {
		return e
	}
	if e := b.Backend.CompareAndSwap(ctx, key, token, body); e != nil {
		return e
	}
	return b.q.fault("current-after")
}

func (q *Queue) recordFailure(ctx context.Context, i publication.Identity, seq uint64, attempts int, err error) error {
	if errors.Is(err, ErrDestination) {
		return errors.Join(err, q.runtimeDeliveryFailure(ctx, i, seq, attempts+1, err, runtimeDefaults(RuntimeOptions{})))
	}
	state := "retry"
	if errors.Is(err, ErrIntegrity) {
		state = "integrity-error"
	}
	if errors.Is(err, ErrPolicy) {
		state = "policy-blocked"
	}
	// Store safe classifications only: backend errors may contain sensitive URLs.
	delay := time.Second * time.Duration(1<<min(attempts, 8))
	_, saveErr := q.db.ExecContext(ctx, "UPDATE jobs SET state=?,last_error=?,next_retry=? WHERE session=? AND sequence=?", state, state, q.opts.Now().Add(delay).UnixNano(), i.BaseKey(), seq)
	return errors.Join(err, saveErr)
}

func (q *Queue) verifyDependencies(ctx context.Context, m publication.Manifest, es []evidence) error {
	if err := q.recheckScopes(ctx, es); err != nil {
		return err
	}
	if len(es) != len(m.Sources) {
		return fmt.Errorf("%w: evidence missing", ErrIntegrity)
	}
	// Validate every dependency and exclusion before the first upload. A missing
	// original is fine; retained policy identities and owned spool bytes suffice.
	for j := range m.Sources {
		s := &m.Sources[j]
		if !publication.IsSelected(*s) {
			continue
		}
		if s.Optional {
			blocked, err := q.unavailableLatch(ctx, m.Identity, *s)
			if err != nil {
				return err
			}
			if blocked {
				return errOptionalUnavailable
			}
		}
		if _, e := q.check(ctx, es[j], true); e != nil {
			return e
		}
		if e := q.verify(ctx, s.Object); e != nil {
			return e
		}
	}

	return nil
}

func (q *Queue) uploadDependencies(ctx context.Context, b publication.Backend, m publication.Manifest, es []evidence, trusted map[publication.Object]bool) error {
	for j := range m.Sources {
		s := &m.Sources[j]
		if !publication.IsSelected(*s) {
			continue
		}
		if err := q.uploadOne(ctx, b, m, es, s, trusted[s.Object]); err != nil {
			return err
		}
	}
	if err := q.recheckScopes(ctx, es); err != nil {
		return err
	}
	return q.recheck(ctx, selectedEvidence(m, es))
}

// uploadOne rechecks policy around one selected object. A trusted object (already
// in the remote current's verified manifest) skips only the remote PutFile.
func (q *Queue) uploadOne(ctx context.Context, b publication.Backend, m publication.Manifest, es []evidence, s *publication.Source, trusted bool) error {
	// Recheck policy immediately before each upload as well as before root commit.
	if err := q.recheckScopes(ctx, es); err != nil {
		return err
	}
	if e := q.recheck(ctx, selectedEvidence(m, es)); e != nil {
		return e
	}
	if e := q.fault("upload-before"); e != nil {
		return e
	}
	if err := q.recheckScopes(ctx, es); err != nil {
		return err
	}
	if e := q.recheck(ctx, selectedEvidence(m, es)); e != nil {
		return e
	}
	if !trusted {
		if e := b.PutFile(ctx, s.Object.Key, q.payload(s.Object)); e != nil {
			return &publication.SourceDependencyError{SourceKeyHash: publication.SourceKeyHash(*s), EpochID: s.EpochID, Optional: s.Optional, Stage: "upload", Err: e}
		}
	}
	return q.fault("upload-after")
}

func (q *Queue) recheck(ctx context.Context, es []evidence) error {
	for j := range es {
		if _, e := q.check(ctx, es[j], true); e != nil {
			return e
		}
	}
	return nil
}

func (q *Queue) attempt(ctx context.Context, b publication.Backend, i publication.Identity, seq uint64, body, ev []byte) error {
	var err error
	var m publication.Manifest
	var es []evidence
	_, err = q.db.ExecContext(ctx, "UPDATE jobs SET attempts=attempts+1 WHERE session=? AND sequence=?", i.BaseKey(), seq)
	if err == nil {
		if json.Unmarshal(body, &m) != nil || publication.ValidateManifest(m) != nil || m.Identity != i || m.Sequence != seq {
			err = fmt.Errorf("%w: invalid manifest", ErrIntegrity)
		}
	}
	if err == nil {
		if json.Unmarshal(ev, &es) != nil {
			err = fmt.Errorf("%w: invalid evidence", ErrIntegrity)
		}
	}
	if err != nil {
		return err
	}
	return q.attemptManifest(ctx, b, i, seq, m, es)
}

func (q *Queue) attemptManifest(ctx context.Context, b publication.Backend, i publication.Identity, seq uint64, m publication.Manifest, es []evidence) error {
	if checkErr := q.verifyDependencies(ctx, m, es); checkErr != nil {
		if handled, handleErr := q.isolateOptionalFailure(ctx, i, m, es, checkErr); handled || handleErr != nil {
			return handleErr
		}
		return checkErr
	}
	finish, err := q.beginOptionalDelivery(ctx, m, es)
	if err != nil {
		if errors.Is(metadataError(err), ErrQuota) {
			unlock, lockErr := q.lockSession(ctx, i)
			if lockErr != nil {
				return lockErr
			}
			defer unlock()
			_, quotaErr := q.terminalOptionalQuota(ctx, i)
			return quotaErr
		}
		return err
	}
	err = errors.Join(q.deliverOne(ctx, b, m, es), finish())
	if err == nil || errors.Is(err, publication.ErrConflict) {
		var state string
		if stateErr := q.db.QueryRowContext(ctx, `SELECT state FROM jobs WHERE session=? AND sequence=?`, i.BaseKey(), seq).Scan(&state); stateErr != nil {
			return stateErr
		}
		if state == "superseded" {
			return errSelectionSuperseded
		}
	}
	if handled, handleErr := q.isolateOptionalFailure(ctx, i, m, es, err); handled || handleErr != nil {
		return handleErr
	}
	return err
}

func (q *Queue) acknowledge(ctx context.Context, i publication.Identity, seq uint64) error {
	if err := q.fault("ack-before-commit"); err != nil {
		return err
	}
	if _, err := q.db.ExecContext(ctx, "UPDATE jobs SET state='published',last_error='',next_retry=0 WHERE session=? AND sequence=? AND state!='superseded'", i.BaseKey(), seq); err != nil {
		return err
	}
	return q.fault("ack-committed")
}
