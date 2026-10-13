package syncer

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/jsonx"
	"github.com/mich-murphy/memory/internal/raw"
	"github.com/mich-murphy/memory/internal/scrub"
	"github.com/mich-murphy/memory/internal/state"
)

type uploadPlan int

const (
	uploadNothing uploadPlan = iota
	uploadSegment
	uploadWhole
)

// mtimeLastLineHeldBack is recorded instead of a file's mtime while its partial last line is
// held back, so the next sync reads the file again.
const mtimeLastLineHeldBack int64 = -1

type versionPiece struct {
	raw.Piece
	copyFrom string
}

type versionPlan struct {
	sha256      string
	size        *int64
	versionedAt float64
	pieces      []versionPiece
	index       []byte
}

func (v *versionPlan) stateVersion() *state.Version {
	pieces := make([]raw.Piece, len(v.pieces))
	for i, p := range v.pieces {
		pieces[i] = p.Piece
	}
	return &state.Version{SHA256: v.sha256, Size: v.size, VersionedAt: v.versionedAt, Pieces: pieces}
}

type upload struct {
	sf             adapters.SessionFile
	root           string
	key            string
	sha            string
	size           int64
	mtimeNS        int64
	lineCount      int64
	prefixSHA      string
	head           map[string]any
	needsUpload    bool
	segmentStart   int64
	pieceSHA       string
	body           []byte
	bodyPath       string
	replacedPieces []raw.Piece
	version        *versionPlan
	resume         *state.Resume
}

func (u *upload) cleanup() {
	if u.bodyPath != "" {
		_ = os.Remove(u.bodyPath) // a leftover temp file is swept with the tmp dir
	}
}

func (u *upload) bodySize() int64 {
	if u.body != nil {
		return int64(len(u.body))
	}
	if st, err := os.Stat(u.bodyPath); err == nil {
		return st.Size()
	}
	return 0
}

func readFile(sf adapters.SessionFile) ([]byte, error) {
	data, err := os.ReadFile(sf.Path)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	if sf.Zstd {
		return raw.Decompress(data)
	}
	return scrub.File(sf.Agent, filepath.Base(sf.Path), data)
}

func readCompleteLines(sf adapters.SessionFile, st os.FileInfo, mayBeMidLine bool) (data []byte, mtimeNS int64, err error) {
	data, err = readFile(sf)
	if err != nil {
		return nil, 0, err
	}
	if mayBeMidLine && !bytes.HasSuffix(data, []byte("\n")) {
		return data[:bytes.LastIndexByte(data, '\n')+1], mtimeLastLineHeldBack, nil
	}
	return data, st.ModTime().UnixNano(), nil
}

func prefixSHA(data []byte) string {
	end, read := 0, 0
	for line := range bytes.Lines(data) {
		if read == prefixLines {
			break
		}
		end += len(line)
		read++
	}
	return raw.SHA256(data[:end])
}

func completeLinesEnd(path string, size int64) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("read: %w", err)
	}
	defer func() { _ = f.Close() }() // read-only
	buf := make([]byte, chunkSize)
	for pos := size; pos > 0; {
		start := max(0, pos-chunkSize)
		n, err := f.ReadAt(buf[:pos-start], start)
		if err != nil && !errors.Is(err, io.EOF) {
			return 0, fmt.Errorf("read: %w", err)
		}
		if nl := bytes.LastIndexByte(buf[:n], '\n'); nl >= 0 {
			return start + int64(nl) + 1, nil
		}
		pos = start
	}
	return 0, nil
}

func plan(prev *state.Upload, pieces []raw.Piece, sha string, size int64, appended, idle bool) (uploadPlan, int64) {
	if prev != nil && prev.SHA256 == sha {
		if idle && len(pieces) > 1 {
			return uploadWhole, 0
		}
		return uploadNothing, 0
	}
	if appended && !idle && len(pieces) > 0 && len(pieces) < maxSegments && size > *prev.Size {
		return uploadSegment, *prev.Size
	}
	return uploadWhole, 0
}

func (s *Syncer) planVersion(sf adapters.SessionFile, key string, prev *state.Upload, pieces []raw.Piece) (*versionPlan, error) {
	now := s.nowSeconds()
	if sf.Role == "db" {
		last, err := s.state.LastVersionAt(key)
		if err != nil {
			return nil, err
		}
		if last != nil && now-*last < dbVersionEvery.Seconds() {
			return nil, nil
		}
	}
	old := pieces
	if len(old) == 0 {
		var end int64
		if prev.Size != nil {
			end = *prev.Size
		}
		old = []raw.Piece{{Start: 0, End: end, SHA256: prev.SHA256, Object: key}}
	}
	v := &versionPlan{sha256: prev.SHA256, size: prev.Size, versionedAt: now}
	for _, p := range old {
		blob := raw.BlobKey(p.SHA256)
		have, err := s.state.Uploaded(blob)
		if err != nil {
			return nil, err
		}
		vp := versionPiece{Start: p.Start, End: p.End, SHA256: p.SHA256, Object: blob}
		if have == nil {
			vp.copyFrom = p.Object
		}
		v.pieces = append(v.pieces, vp)
	}
	index, err := s.state.Versions(key)
	if err != nil {
		return nil, err
	}
	index = append(index, *v.stateVersion())
	v.index, err = jsonx.SortedIndented(map[string]any{"key": sf.Key, "versions": index})
	if err != nil {
		return nil, err
	}
	return v, nil
}

func (s *Syncer) prepare(sf adapters.SessionFile, root string) (*upload, error) {
	st, err := os.Stat(sf.Path)
	if err != nil {
		return nil, fmt.Errorf("stat: %w", err)
	}
	if sf.SessionKey != "" {
		s.noteSessionRoot(sessionID{sf.Agent, sf.SessionKey}, root)
	}
	key := s.rawKey(sf.Agent, sf.Key)
	pieces, err := s.state.Pieces(key)
	if err != nil {
		return nil, err
	}
	age := s.nowSeconds() - fileMtime(st)
	idle := age >= foldAfter.Seconds()
	unchanged, err := s.state.FileUnchanged(sf.Path, st.Size(), st.ModTime().UnixNano())
	if err != nil {
		return nil, err
	}
	if unchanged && (!idle || len(pieces) <= 1) {
		s.Summary.Unchanged++
		return nil, nil
	}
	prev, err := s.state.Uploaded(key)
	if err != nil {
		return nil, err
	}
	saved, err := s.state.Resume(key)
	if err != nil {
		return nil, err
	}
	mayBeMidLine := filepath.Ext(sf.Path) == ".jsonl" && age < partialLineWindow.Seconds()
	var c *content
	// A file seen before is most likely an append, which needs only its new bytes in memory.
	plain := !sf.Zstd && !scrub.Applies(sf.Agent, filepath.Base(sf.Path))
	if plain && (prev != nil || st.Size() > largeFileBytes) {
		c, err = s.streamedContent(sf.Path, st, prev, saved, mayBeMidLine)
	} else {
		c, err = inMemoryContent(sf, st, prev, saved, mayBeMidLine)
	}
	if err != nil {
		return nil, err
	}
	if c == nil {
		s.Summary.Skipped++
		return nil, nil
	}
	return s.planUpload(sf, root, key, c, prev, pieces, idle)
}

type content struct {
	hashed
	mtimeNS  int64
	first    []byte
	compress func(u *upload) error
}

func inMemoryContent(sf adapters.SessionFile, st os.FileInfo, prev *state.Upload, saved *state.Resume,
	mayBeMidLine bool,
) (*content, error) {
	data, mtimeNS, err := readCompleteLines(sf, st, mayBeMidLine)
	if err != nil || len(data) == 0 {
		return nil, err
	}
	h, err := hashContent(bytes.NewReader(data), int64(len(data)), prev, saved)
	if err != nil {
		return nil, err
	}
	return &content{
		hashed: h, mtimeNS: mtimeNS, first: data,
		compress: func(u *upload) error {
			part := data[u.segmentStart:]
			u.pieceSHA = h.sha
			if u.segmentStart > 0 {
				u.pieceSHA = raw.SHA256(part)
			}
			u.body = raw.Compress(part)
			return nil
		},
	}, nil
}

func (s *Syncer) streamedContent(path string, st os.FileInfo, prev *state.Upload, saved *state.Resume,
	mayBeMidLine bool,
) (*content, error) {
	limit := st.Size()
	if mayBeMidLine {
		var err error
		if limit, err = completeLinesEnd(path, st.Size()); err != nil {
			return nil, err
		}
	}
	if limit == 0 {
		return nil, nil
	}
	h, err := hashFile(path, limit, prev, saved)
	if err != nil {
		return nil, err
	}
	first, err := headLines(path, h.size)
	if err != nil {
		return nil, err
	}
	mtimeNS := mtimeLastLineHeldBack
	if limit == st.Size() {
		mtimeNS = st.ModTime().UnixNano()
	}
	return &content{
		hashed: h, mtimeNS: mtimeNS, first: first,
		compress: func(u *upload) (err error) {
			n := u.size - u.segmentStart
			if n > largeFileBytes {
				u.bodyPath, u.pieceSHA, err = s.compressToTemp(path, u.segmentStart, n)
				return err
			}
			part, err := readRange(path, u.segmentStart, n)
			if err != nil {
				return err
			}
			u.pieceSHA = raw.SHA256(part)
			if u.segmentStart == 0 && u.pieceSHA != u.sha {
				return errors.New("changed while being read; the next sync uploads it")
			}
			u.body = raw.Compress(part)
			return nil
		},
	}, nil
}

func readRange(path string, start, n int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	defer func() { _ = f.Close() }() // read-only
	data := make([]byte, n)
	if _, err := f.ReadAt(data, start); err != nil {
		return nil, fmt.Errorf("read: %w (file shrank)", err)
	}
	return data, nil
}

func (s *Syncer) planUpload(sf adapters.SessionFile, root, key string, c *content, prev *state.Upload,
	pieces []raw.Piece, idle bool,
) (*upload, error) {
	var head map[string]any
	if transcriptRoles[sf.Role] && strings.HasSuffix(strings.TrimSuffix(sf.Path, ".zst"), ".jsonl") {
		head = s.adapters[sf.Agent].SessionInfo(adapters.ReadHead(c.first, prefixLines))
	}
	mode, start := plan(prev, pieces, c.sha, c.size, c.appended, idle)
	u := &upload{
		sf: sf, root: root, key: key, sha: c.sha, size: c.size, mtimeNS: c.mtimeNS, lineCount: c.lines,
		prefixSHA: prefixSHA(c.first), head: head, needsUpload: mode != uploadNothing, segmentStart: start,
		resume: c.resume,
	}
	if mode == uploadNothing {
		return u, nil
	}
	if err := c.compress(u); err != nil {
		return nil, err
	}
	if mode == uploadWhole {
		if err := s.planReplace(u, prev, pieces, c.appended); err != nil {
			u.cleanup()
			return nil, err
		}
	}
	return u, nil
}

func (s *Syncer) planReplace(u *upload, prev *state.Upload, pieces []raw.Piece, appended bool) error {
	u.replacedPieces = pieces
	if prev == nil || appended {
		return nil
	}
	v, err := s.planVersion(u.sf, u.key, prev, pieces)
	u.version = v
	return err
}

func hashFile(path string, limit int64, prev *state.Upload, saved *state.Resume) (hashed, error) {
	f, err := os.Open(path)
	if err != nil {
		return hashed{}, fmt.Errorf("read: %w", err)
	}
	defer func() { _ = f.Close() }() // read-only
	return hashContent(f, limit, prev, saved)
}

func headLines(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	defer func() { _ = f.Close() }() // read-only
	r := bufio.NewReader(io.LimitReader(f, limit))
	var out []byte
	for range prefixLines {
		line, err := readLine(r, int(16*chunkSize))
		out = append(out, line...)
		if err != nil {
			break
		}
	}
	return out, nil
}

func readLine(r *bufio.Reader, maxBytes int) ([]byte, error) {
	var out []byte
	for len(out) < maxBytes {
		b, err := r.ReadByte()
		if err != nil {
			return out, err
		}
		out = append(out, b)
		if b == '\n' {
			return out, nil
		}
	}
	return out, nil
}

func (s *Syncer) compressToTemp(path string, start, length int64) (tmpPath, sha string, err error) {
	if err := os.MkdirAll(s.tmpDir, 0o700); err != nil {
		return "", "", fmt.Errorf("temp dir: %w", err)
	}
	src, err := os.Open(path)
	if err != nil {
		return "", "", fmt.Errorf("read: %w", err)
	}
	defer func() { _ = src.Close() }() // read-only
	out, err := os.CreateTemp(s.tmpDir, "*.zst")
	if err != nil {
		return "", "", fmt.Errorf("temp file: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.Remove(out.Name()) // best-effort cleanup of a failed temp file
		}
	}()
	if _, err = src.Seek(start, io.SeekStart); err != nil {
		_ = out.Close() // the seek error is the one to report
		return "", "", fmt.Errorf("seek: %w", err)
	}
	sha, err = raw.CompressStream(src, out, length)
	if cerr := out.Close(); err == nil && cerr != nil {
		err = fmt.Errorf("temp file: %w", cerr)
	}
	if err != nil {
		return "", "", err
	}
	return out.Name(), sha, nil
}

// upload runs on the upload pool, so it must not touch local state.
func (s *Syncer) upload(ctx context.Context, u *upload) error {
	if err := s.copyVersion(ctx, u); err != nil {
		return err
	}
	target := u.key
	if u.segmentStart > 0 {
		target = raw.SegmentKey(u.key, u.segmentStart, u.size)
	}
	meta := map[string]string{
		"sha256": u.pieceSHA, "agent": u.sf.Agent, "machine": s.cfg.MachineID, "role": string(u.sf.Role),
		"start": strconv.FormatInt(u.segmentStart, 10), "end": strconv.FormatInt(u.size, 10),
	}
	var err error
	if u.bodyPath != "" {
		err = s.store.PutFile(ctx, target, u.bodyPath, meta)
	} else {
		err = s.store.Put(ctx, target, u.body, meta)
	}
	if err != nil {
		return err
	}
	if u.version != nil {
		if err := s.store.Put(ctx, s.versionsKey(u.sf), u.version.index, map[string]string{}); err != nil {
			return err
		}
	}
	if u.segmentStart == 0 {
		s.deleteReplacedSegments(ctx, u.replacedPieces)
	}
	return nil
}

func (s *Syncer) copyVersion(ctx context.Context, u *upload) error {
	if u.version == nil {
		return nil
	}
	for _, p := range u.version.pieces {
		if p.copyFrom == "" {
			continue
		}
		ok, err := s.store.Copy(ctx, p.copyFrom, p.Object)
		if err != nil {
			return err
		}
		if !ok {
			s.log.Warn("earlier bytes are missing from the bucket and can't be kept", "path", u.sf.Path,
				"object", p.copyFrom)
			u.version = nil
			return nil
		}
	}
	return nil
}

// deleteReplacedSegments only logs failures: readers skip a leftover segment, since it starts
// before the new base ends.
func (s *Syncer) deleteReplacedSegments(ctx context.Context, pieces []raw.Piece) {
	for _, p := range pieces {
		if p.Start == 0 {
			continue
		}
		if err := s.store.Delete(ctx, p.Object); err != nil {
			s.log.Warn("could not delete a replaced segment", "object", p.Object, "error", err)
		}
	}
}

func (s *Syncer) finish(u *upload) error {
	sf := u.sf
	if u.needsUpload {
		if err := s.recordChanged(u); err != nil {
			return err
		}
	} else {
		s.Summary.Unchanged++
	}
	if sf.SessionKey != "" && (u.needsUpload || u.head != nil) {
		if err := s.markManifestPending(sessionID{sf.Agent, sf.SessionKey}); err != nil {
			return err
		}
	}
	lines := u.lineCount
	file := &state.File{
		Path: sf.Path, Agent: sf.Agent, Root: u.root, Key: sf.Key, SessionKey: sf.SessionKey, Role: string(sf.Role),
		Size: u.size, MtimeNS: u.mtimeNS, SHA256: u.sha, LineCount: &lines, PrefixSHA256: u.prefixSHA, Head: u.head,
	}
	if err := s.state.RecordFile(file); err != nil {
		return err
	}
	if u.resume != nil {
		if err := s.state.SetResume(u.key, u.resume); err != nil {
			return err
		}
	}
	return s.state.Flush()
}

func (s *Syncer) recordChanged(u *upload) error {
	size := u.size
	if err := s.state.RecordUpload(u.key, u.sha, &size); err != nil {
		return err
	}
	if u.segmentStart > 0 {
		obj := raw.SegmentKey(u.key, u.segmentStart, u.size)
		if err := s.state.AddPiece(u.key, raw.Piece{Start: u.segmentStart, End: u.size, SHA256: u.pieceSHA, Object: obj}); err != nil {
			return err
		}
		s.Summary.Segments++
	} else {
		if err := s.state.SetPieces(u.key, []raw.Piece{{Start: 0, End: u.size, SHA256: u.sha, Object: u.key}}); err != nil {
			return err
		}
		if len(u.replacedPieces) > 1 {
			s.Summary.Folded++
		}
	}
	if u.version != nil {
		for _, p := range u.version.pieces {
			if p.copyFrom == "" {
				continue
			}
			n := p.End - p.Start
			if err := s.state.RecordUpload(p.Object, p.SHA256, &n); err != nil {
				return err
			}
		}
		if err := s.state.RecordVersion(u.key, u.version.stateVersion()); err != nil {
			return err
		}
		s.Summary.Versions++
	}
	s.Summary.Uploaded++
	s.Summary.BytesUploaded += u.bodySize()
	if u.sf.Role == adapters.RoleUnknown {
		s.Summary.Unknown++
	}
	return nil
}
