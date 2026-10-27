package sessioncapture

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"unicode/utf8"

	"github.com/klauspost/compress/zstd"

	"github.com/mich-murphy/memory/internal/capturepolicy"
	"github.com/mich-murphy/memory/internal/publication"
)

const MaxSourceBytes int64 = 1 << 30

type prefixWriter struct {
	all, prefix hash.Hash
	left        int64
}

func (w *prefixWriter) Write(b []byte) (int, error) {
	_, _ = w.all.Write(b)
	n := min(int64(len(b)), w.left)
	_, _ = w.prefix.Write(b[:n])
	w.left -= n
	return len(b), nil
}
func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func sum(h hash.Hash) string { return hex.EncodeToString(h.Sum(nil)) }
func (q *Queue) usage() (int64, error) {
	// This shared accounting/integrity scan stats all retained payload entries;
	// snapshot admission therefore still depends on unrelated spool metadata.
	// Only directory metadata is fenced: compression, source I/O, payload sync
	// and fault callbacks remain independent. A missing entry is external
	// interference, not a benign internal temporary rename/removal.
	q.payloadMu.RLock()
	defer q.payloadMu.RUnlock()
	if err := privateDirs(q.dir); err != nil {
		return 0, err
	}
	es, e := os.ReadDir(filepath.Join(q.dir, "payloads"))
	if e != nil {
		return 0, e
	}
	return payloadUsage(es)
}

func payloadUsage(es []os.DirEntry) (int64, error) {
	var n int64
	for _, v := range es {
		st, e := v.Info()
		if e != nil {
			return 0, e
		}
		if !st.Mode().IsRegular() || st.Mode().Perm()&0o077 != 0 || st.Size() < 0 || st.Size() > math.MaxInt64-n {
			return 0, ErrIntegrity
		}
		n += st.Size()
	}
	return n, nil
}

func (q *Queue) payload(o publication.Object) string {
	return filepath.Join(q.dir, "payloads", o.EncodedSHA256+".zst")
}

func (q *Queue) snapshot(ctx context.Context, r Request, path string, previous publication.Source) (publication.Object, string, error) {
	source, err := observePrivate(path)
	if err != nil {
		return publication.Object{}, "", err
	}
	return q.snapshotAuthorized(ctx, r, source, previous)
}

func (q *Queue) snapshotAuthorized(ctx context.Context, r Request, source authorizedSource, previous publication.Source) (publication.Object, string, error) {
	var obj publication.Object
	f, e := source.open(ctx)
	if e != nil {
		return obj, "", e
	}
	defer func() { _ = f.Close() }() // Read-only descriptor; no writes to flush.
	before, e := f.Stat()
	if e != nil {
		return obj, "", e
	}
	if !before.Mode().IsRegular() {
		return obj, "", errors.New("sessioncapture: regular source required")
	}
	if before.Size() > MaxSourceBytes {
		return obj, "", ErrQuota
	}
	_, e = q.usage()
	if e != nil {
		return obj, "", e
	}
	tmp, e := os.CreateTemp(filepath.Join(q.dir, "payloads"), "capture-")
	if e != nil {
		return obj, "", e
	}
	qw := &quotaWriter{q: q, w: tmp}
	defer func() {
		_ = tmp.Close() // Checked explicitly on the successful write path.
		q.cleanupTemporary(tmp.Name(), qw)
	}()
	encoded := sha256.New()
	raw := sha256.New()
	prefix := sha256.New()
	zw, e := zstd.NewWriter(io.MultiWriter(qw, encoded), zstd.WithEncoderConcurrency(1))
	if e != nil {
		return obj, "", e
	}
	pw := &prefixWriter{all: raw, prefix: prefix, left: previous.Object.Size}
	n, e := io.Copy(io.MultiWriter(zw, pw), &contextReader{ctx: ctx, r: io.LimitReader(f, MaxSourceBytes+1)})
	closeErr := zw.Close()
	if e != nil {
		return obj, "", e
	}
	if closeErr != nil {
		return obj, "", closeErr
	}
	if n > MaxSourceBytes {
		return obj, "", ErrQuota
	}
	if e := stableObservation(ctx, f, source.Path, before, n, sum(raw)); e != nil {
		return obj, "", e
	}
	if e := q.fault("payload-before-sync"); e != nil {
		return obj, "", e
	}
	if e := tmp.Sync(); e != nil {
		return obj, "", e
	}
	if e := tmp.Close(); e != nil {
		return obj, "", e
	}
	obj = publication.Object{Encoding: "zstd", SHA256: sum(raw), EncodedSHA256: sum(encoded), Size: n}
	st, e := os.Stat(tmp.Name())
	if e != nil {
		return obj, "", e
	}
	obj.EncodedSize = st.Size()
	obj.Key = r.Identity.BaseKey() + "/objects/" + obj.EncodedSHA256 + ".zst"
	installed, e := q.installPayload(ctx, tmp.Name(), obj)
	if installed {
		qw.retained = true
	}
	if e != nil {
		return obj, "", e
	}

	return obj, sum(prefix), nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *contextReader) Read(b []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.r.Read(b)
}

func unionIdentities(old, current []string) []string {
	ids := slices.Clone(old)
	for _, id := range current {
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	return ids
}

func (q *Queue) prepareRequest(r Request) (Request, error) {
	for _, value := range append([]string{r.Identity.ArchiveID, r.Identity.MachineID, r.Identity.Agent, r.Identity.SessionID, r.SourceID, r.Role, r.Path, r.Transform}, r.Associations...) {
		if !utf8.ValidString(value) {
			return r, errors.New("sessioncapture: invalid UTF-8 request")
		}
	}
	if r.ObservedAtNS < 0 {
		return r, errors.New("sessioncapture: negative observation time")
	}
	// Persist path provenance independently of the caller's working directory.
	// SourceID remains an explicit caller identity, not a pathname-derived ID.
	if r.Path != "" {
		path, err := filepath.Abs(r.Path)
		if err != nil {
			return r, err
		}
		r.Path = path
	}
	logical, _, err := q.requestCoordinates(r)
	if err != nil {
		return r, err
	}
	if logical != "" {
		r.LogicalPath = logical
	}
	r.Identities = slices.Clone(r.Identities)
	if r.Associations == nil {
		r.Associations = []string{}
	}
	if r.Transform != "" && !slices.Contains(r.Associations, "transform:"+r.Transform) {
		r.Associations = append(slices.Clone(r.Associations), "transform:"+r.Transform)
	}
	if r.ObservedAtNS == 0 {
		r.ObservedAtNS = q.opts.Now().UnixNano()
	}
	return r, nil
}

func (q *Queue) Capture(ctx context.Context, r Request) (publication.Manifest, error) {
	return q.capture(ctx, r, false)
}

// CaptureIfChanged verifies stable exact source bytes and avoids a new publication
// only when the active source and its provenance are unchanged.
func (q *Queue) CaptureIfChanged(ctx context.Context, r Request) (publication.Manifest, error) {
	return q.capture(ctx, r, true)
}

func (q *Queue) capture(ctx context.Context, r Request, ifChanged bool) (result publication.Manifest, resultErr error) {
	var zero publication.Manifest
	var err error
	r, err = q.prepareRequest(r)
	if err != nil {
		return zero, err
	}
	unlock, err := q.lockSession(ctx, r.Identity)
	if err != nil {
		return zero, err
	}
	defer unlock()
	resolution, e := q.checkObserved(ctx, evidence{Request: r}, false)
	if e != nil {
		return zero, e
	}
	m, es, e := q.latestPolicyChecked(ctx, r.Identity)
	if e != nil {
		return zero, e
	}
	preparedPath, cleanup, e := q.materialize(ctx, r, &resolution)
	if e != nil {
		return zero, e
	}
	defer func() { resultErr = errors.Join(resultErr, cleanup()) }()
	previous, idx := activeSource(m, r.SourceID)
	if idx >= 0 && !previous.Optional && r.Optional {
		return zero, ErrPolicy
	}
	if idx >= 0 {
		if e := q.verify(ctx, previous.Object); e != nil {
			return zero, e
		}
	}
	if ifChanged && idx >= 0 {
		unchanged, err := q.unchanged(ctx, r, preparedPath, resolution.Identities, previous, es[idx])
		if err != nil {
			return zero, err
		}
		if unchanged {
			if err := q.validateUnchangedManifest(m); err != nil {
				return zero, err
			}
			public, _, err := q.latest(ctx, r.Identity)
			return public, err
		}
	}
	obj, prefix, e := q.snapshotAuthorized(ctx, r, preparedPath, previous)
	if e != nil {
		return zero, e
	}
	return q.acceptSnapshot(ctx, r, resolution.Identities, m, es, obj, prefix)
}

func activeSource(m publication.Manifest, sourceID string) (publication.Source, int) {
	for i := range m.Sources {
		if m.Sources[i].SourceID == sourceID && m.Sources[i].State == "active" {
			return m.Sources[i], i
		}
	}
	return publication.Source{}, -1
}

// acceptSnapshot advances the complete view only after durable payload ownership.
func (q *Queue) acceptSnapshot(ctx context.Context, r Request, identities []string, m publication.Manifest, es []evidence, obj publication.Object, prefix string) (publication.Manifest, error) {
	previous, idx := activeSource(m, r.SourceID)
	sequence := m.Sequence + 1
	now := q.opts.Now().UnixNano()
	src := publication.Source{Optional: r.Optional, SourceID: r.SourceID, EpochID: digest([]byte(fmt.Sprintf("%s:%s:%d:%s", r.Identity.BaseKey(), r.SourceID, sequence, obj.SHA256))), Role: r.Role, Path: publicationSourcePath(r), State: "active", Revision: 1, ObservedAtNS: r.ObservedAtNS, CapturedAtNS: now, Object: obj, Associations: r.Associations}
	if idx >= 0 && obj.Size >= previous.Object.Size && prefix == previous.Object.SHA256 {
		src.EpochID = previous.EpochID
		src.Revision = previous.Revision + 1
		m.Sources[idx] = src
		// A proven append still contains the prior epoch's bytes. Keep all
		// historical policy identities even when an alias now resolves elsewhere.
		merged := unionIdentities(es[idx].Identities, identities)
		if err := boundPolicyIdentities(merged); err != nil {
			return publication.Manifest{}, err
		}
		es[idx] = evidence{Request: r, Identities: merged}
	} else {
		if idx >= 0 {
			m.Sources[idx].State = "sealed"
		}
		m.Sources = append(m.Sources, src)
		es = append(es, evidence{Request: r, Identities: identities})
	}
	m.Contract = publication.Contract
	m.Identity = r.Identity
	m.Sequence = sequence
	m.ObservedAtNS = r.ObservedAtNS
	m.CapturedAtNS = now
	// The immutable candidate is persisted before network I/O. Successful remote
	// commit time is not known at acceptance; zero is explicitly unknown, not a
	// fabricated publication-success timestamp copied from capture time.
	m.PublishedAtNS = 0
	if q.opts.EnableEnrichment {
		m.Coverage = reduceEnrichmentCoverage(m, es)
		m.Coverage.Missing = appendUniqueMissing(m.Coverage.Missing, "discovery-not-enumerated")
	} else {
		m.Coverage = publication.Coverage{Discovery: "partial", Enrichment: "partial", Missing: []string{"discovery-not-enumerated", "enrichment-not-implemented"}}
	}
	return q.acceptView(ctx, m, es, r.SourceID, false)
}

func (q *Queue) accept(ctx context.Context, m publication.Manifest, es []evidence) (publication.Manifest, error) {
	return q.acceptView(ctx, m, es, "", false)
}

func (q *Queue) acceptView(ctx context.Context, m publication.Manifest, es []evidence, changedSource string, supersede bool) (publication.Manifest, error) {
	var zero publication.Manifest
	var err error
	if changedSource != "" {
		detached, detachErr := q.detachActiveOptional(ctx, m, es)
		if detachErr != nil {
			return zero, detachErr
		}
		supersede = supersede || detached
	}
	m, err = carryOptionalSelection(m, es)
	if err != nil {
		return zero, err
	}
	m, b, ev, e := encodeAcceptedView(m, es)
	if e != nil {
		return zero, e
	}
	if e := q.fault("job-before-commit"); e != nil {
		return zero, e
	}
	tx, e := q.db.BeginTx(ctx, nil)
	if e != nil {
		return zero, e
	}
	defer func() { _ = tx.Rollback() }()
	_, e = tx.ExecContext(ctx, "INSERT INTO jobs(session,sequence,manifest,evidence) VALUES(?,?,?,?)", m.Identity.BaseKey(), m.Sequence, b, ev)
	if e == nil && changedSource != "" && q.opts.EnableEnrichment {
		if spec, ok := capturedSpec(m, es, changedSource); ok {
			e = q.admitCapturedEnrichment(ctx, tx, m, spec)
		}
	}
	if e == nil && supersede {
		_, e = tx.ExecContext(ctx, `UPDATE jobs SET state='superseded' WHERE session=? AND sequence<? AND state NOT IN ('published','superseded')`, m.Identity.BaseKey(), m.Sequence)
	}
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		return zero, e
	}
	if e := q.fault("job-committed"); e != nil {
		return zero, e
	}
	return m, nil
}

func stableObservation(ctx context.Context, f *os.File, path string, before os.FileInfo, n int64, rawHash string) error {
	// A second complete read plus descriptor/path stats detects ordinary concurrent
	// edits. It cannot establish a producer transaction or defeat adversarial races.
	if _, e := f.Seek(0, io.SeekStart); e != nil {
		return e
	}
	second := sha256.New()
	n2, e := io.Copy(second, &contextReader{ctx: ctx, r: io.LimitReader(f, MaxSourceBytes+1)})
	if e != nil {
		return e
	}
	after, e := f.Stat()
	if e != nil {
		return e
	}
	canonical, e := capturepolicy.Resolve(path)
	if e != nil {
		return fmt.Errorf("sessioncapture: source changed during observation: %w", e)
	}
	atFile, e := (authorizedSource{Resolution: canonical, before: before}).open(ctx)
	if e != nil {
		return fmt.Errorf("sessioncapture: source changed during observation: %w", e)
	}
	defer func() { _ = atFile.Close() }() // Read-only identity check.
	atPath, e := atFile.Stat()
	if e != nil {
		return e
	}
	if n != n2 || rawHash != sum(second) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || !os.SameFile(before, atPath) {
		return errors.New("sessioncapture: source changed during observation")
	}

	return nil
}

// Serialize only identical encoded hashes during install, not independent source
// I/O. Rename transfers the existing temporary charge to the retained payload;
// duplicates remain charged until their separate temporary is removed.
func (q *Queue) installPayload(ctx context.Context, tmpPath string, obj publication.Object) (bool, error) {
	unlock, err := lockContext(ctx, keyedMutex(&q.installs, obj.EncodedSHA256))
	if err != nil {
		return false, err
	}
	defer unlock()
	target := q.payload(obj)
	installed := false
	if _, e := os.Lstat(target); e == nil {
		if e := q.verify(ctx, obj); e != nil {
			return false, e
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return false, e
	} else if e := q.renamePayload(tmpPath, target); e != nil {
		return false, e
	} else {
		installed = true
	}
	if e := syncDir(filepath.Dir(target)); e != nil {
		return installed, e
	}
	return installed, q.fault("payload-synced")
}

func (q *Queue) renamePayload(from, to string) error {
	q.payloadMu.Lock()
	defer q.payloadMu.Unlock()
	return os.Rename(from, to)
}

func (q *Queue) unchanged(ctx context.Context, r Request, source authorizedSource, originalIdentities []string, previous publication.Source, ev evidence) (bool, error) {
	if r.Optional != previous.Optional || r.Transform != ev.Request.Transform || publicationSourcePath(r) != previous.Path || r.Role != previous.Role || !slices.Equal(r.Associations, previous.Associations) {
		return false, nil
	}
	// Prepared output has private byte authority, not original provenance. Include
	// all observed inputs (including a present SQLite WAL) when checking whether
	// the previous acceptance already owns every restrictive policy identity.
	ids := unionIdentities(ev.Identities, originalIdentities)
	resolution, err := q.check(ctx, evidence{Request: r, Identities: ids}, false)
	if err != nil {
		return false, err
	}
	// A newly learned spelling is durable policy evidence, so it requires a new
	// acceptance rather than being lost in a no-op.
	if !slices.Equal(unionIdentities(ev.Identities, resolution.Identities), ev.Identities) {
		return false, nil
	}
	f, err := source.open(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }() // Read-only descriptor.
	before, err := f.Stat()
	if err != nil {
		return false, err
	}
	if !before.Mode().IsRegular() {
		return false, errors.New("sessioncapture: regular source required")
	}
	if before.Size() > MaxSourceBytes {
		return false, ErrQuota
	}
	h := sha256.New()
	n, err := io.Copy(h, &contextReader{ctx: ctx, r: io.LimitReader(f, MaxSourceBytes+1)})
	if err != nil {
		return false, err
	}
	if err := stableObservation(ctx, f, source.Path, before, n, sum(h)); err != nil {
		return false, err
	}
	return n == previous.Object.Size && sum(h) == previous.Object.SHA256, nil
}

// encodeAcceptedView establishes the stable ID and validates the exact bytes
// before the acceptance transaction can mutate the durable queue.
func encodeAcceptedView(m publication.Manifest, es []evidence) (publication.Manifest, []byte, []byte, error) {
	m.PublicationID = ""
	b, err := json.Marshal(m)
	if err != nil {
		return m, nil, nil, err
	}
	m.PublicationID = digest(b)
	if err := publication.ValidateManifest(m); err != nil {
		return m, nil, nil, err
	}
	b, err = json.Marshal(m)
	if err != nil {
		return m, nil, nil, err
	}
	ev, err := json.Marshal(es)
	if err != nil {
		return m, nil, nil, err
	}
	if len(b) > publication.MaxMetadata || len(m.Sources) > publication.MaxSources {
		return m, nil, nil, ErrQuota
	}
	return m, b, ev, nil
}
