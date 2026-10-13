package syncer

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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

// mtimeLastLineHeldBack is recorded instead of a file's mtime while its partial last line is
// held back, so the next sync reads the file again.
const mtimeLastLineHeldBack int64 = -1

type versionPlan struct {
	state.Version
	newBlobs []raw.Piece
	index    []byte
}

type upload struct {
	file        state.File
	key         string
	needsUpload bool
	piece       raw.Piece
	body        []byte
	bodyPath    string
	replaced    []raw.Piece
	version     *versionPlan
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

// plan returns the offset to upload from, or false when the bucket already holds the bytes.
func plan(prev *state.Upload, pieces []raw.Piece, c *content, idle bool) (start int64, ok bool) {
	if prev != nil && prev.SHA256 == c.sha {
		return 0, idle && len(pieces) > 1
	}
	if c.appended && !idle && len(pieces) > 0 && len(pieces) < maxSegments && c.size > prev.Size {
		return prev.Size, true
	}
	return 0, true
}

func (s *Syncer) planVersion(sf adapters.SessionFile, key string, prev *state.Upload, pieces []raw.Piece) (*versionPlan, error) {
	now := s.nowSeconds()
	if sf.Role == adapters.RoleDB {
		last, err := s.state.LastVersionAt(key)
		if err != nil {
			return nil, err
		}
		if now-last < dbVersionEvery.Seconds() {
			return nil, nil
		}
	}
	old := pieces
	if len(old) == 0 {
		old = []raw.Piece{{Start: 0, End: prev.Size, SHA256: prev.SHA256, Object: key}}
	}
	v := &versionPlan{SHA256: prev.SHA256, Size: prev.Size, VersionedAt: now}
	for _, p := range old {
		blob := raw.BlobKey(p.SHA256)
		have, err := s.state.UploadedSHA(blob)
		if err != nil {
			return nil, err
		}
		if have == "" {
			v.newBlobs = append(v.newBlobs, p)
		}
		v.Pieces = append(v.Pieces, raw.Piece{Start: p.Start, End: p.End, SHA256: p.SHA256, Object: blob})
	}
	index, err := s.state.Versions(key)
	if err != nil {
		return nil, err
	}
	v.index, err = jsonx.SortedIndented(map[string]any{"key": sf.Key, "versions": append(index, v.Version)})
	if err != nil {
		return nil, err
	}
	return v, nil
}

func (s *Syncer) prepare(sf adapters.SessionFile) (*upload, error) {
	st, err := os.Stat(sf.Path)
	if err != nil {
		return nil, fmt.Errorf("stat: %w", err)
	}
	if sf.SessionKey != "" {
		s.noteSessionRoot(sf.Session(), sf.Root)
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
	mayBeMidLine := filepath.Ext(sf.Path) == ".jsonl" && age < partialLineWindow.Seconds()
	var c *content
	if st.Size() > largeFileBytes && !sf.Zstd && !scrub.Applies(sf.Agent, filepath.Base(sf.Path)) {
		c, err = s.streamedContent(sf.Path, st, prev, mayBeMidLine)
	} else {
		c, err = inMemoryContent(sf, st, prev, mayBeMidLine)
	}
	if err != nil {
		return nil, err
	}
	if c == nil {
		s.Summary.Skipped++
		return nil, nil
	}
	return s.planUpload(sf, key, c, prev, pieces, idle)
}

type content struct {
	sha      string
	size     int64
	mtimeNS  int64
	lines    int64
	first    []byte
	appended bool
	compress func(u *upload) error
}

func inMemoryContent(sf adapters.SessionFile, st os.FileInfo, prev *state.Upload, mayBeMidLine bool) (*content, error) {
	data, mtimeNS, err := readCompleteLines(sf, st, mayBeMidLine)
	if err != nil || len(data) == 0 {
		return nil, err
	}
	sha := raw.SHA256(data)
	size := int64(len(data))
	return &content{
		sha: sha, size: size, mtimeNS: mtimeNS, lines: int64(bytes.Count(data, []byte("\n"))), first: data,
		appended: prev != nil && size >= prev.Size && raw.SHA256(data[:prev.Size]) == prev.SHA256,
		compress: func(u *upload) error {
			part := data[u.piece.Start:]
			u.piece.SHA256 = sha
			if u.piece.Start > 0 {
				u.piece.SHA256 = raw.SHA256(part)
			}
			u.body = raw.Compress(part)
			return nil
		},
	}, nil
}

func (s *Syncer) streamedContent(path string, st os.FileInfo, prev *state.Upload, mayBeMidLine bool) (*content, error) {
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
	oldSize := int64(-1)
	if prev != nil {
		oldSize = prev.Size
	}
	sha, oldPrefix, read, lines, err := hashStreamed(path, limit, oldSize)
	if err != nil {
		return nil, err
	}
	first, err := headLines(path, read)
	if err != nil {
		return nil, err
	}
	mtimeNS := mtimeLastLineHeldBack
	if limit == st.Size() {
		mtimeNS = st.ModTime().UnixNano()
	}
	return &content{
		sha: sha, size: read, mtimeNS: mtimeNS, lines: lines, first: first,
		appended: oldSize >= 0 && read >= oldSize && oldPrefix == prev.SHA256,
		compress: func(u *upload) (err error) {
			u.bodyPath, u.piece.SHA256, err = s.compressToTemp(path, u.piece.Start, u.piece.End-u.piece.Start)
			return err
		},
	}, nil
}

func (s *Syncer) planUpload(sf adapters.SessionFile, key string, c *content, prev *state.Upload,
	pieces []raw.Piece, idle bool,
) (*upload, error) {
	u := &upload{key: key, file: state.File{
		SessionFile: sf, Size: c.size, MtimeNS: c.mtimeNS, SHA256: c.sha, LineCount: c.lines, PrefixSHA256: prefixSHA(c.first),
	}}
	if transcriptRoles[sf.Role] && strings.HasSuffix(strings.TrimSuffix(sf.Path, ".zst"), ".jsonl") {
		u.file.Head = s.adapters[sf.Agent].SessionInfo(adapters.ReadHead(c.first, prefixLines))
	}
	start, ok := plan(prev, pieces, c, idle)
	if !ok {
		return u, nil
	}
	u.needsUpload = true
	u.piece = raw.Piece{Start: start, End: c.size, Object: key}
	if start > 0 {
		u.piece.Object = raw.SegmentKey(key, start, c.size)
	}
	if err := c.compress(u); err != nil {
		return nil, err
	}
	if start == 0 {
		if err := s.planReplace(u, prev, pieces, c.appended); err != nil {
			u.cleanup()
			return nil, err
		}
	}
	return u, nil
}

func (s *Syncer) planReplace(u *upload, prev *state.Upload, pieces []raw.Piece, appended bool) error {
	u.replaced = pieces
	if prev == nil || appended {
		return nil
	}
	v, err := s.planVersion(u.file.SessionFile, u.key, prev, pieces)
	u.version = v
	return err
}

func hashStreamed(path string, limit, oldSize int64) (whole, oldPrefix string, read, lines int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", 0, 0, fmt.Errorf("read: %w", err)
	}
	defer func() { _ = f.Close() }() // read-only
	wh, oh := sha256.New(), sha256.New()
	buf := make([]byte, chunkSize)
	for read < limit {
		n, rerr := f.Read(buf[:min(chunkSize, limit-read)])
		chunk := buf[:n]
		lines += int64(bytes.Count(chunk, []byte("\n")))
		if oldSize >= 0 && read < oldSize {
			oh.Write(chunk[:min(int64(n), oldSize-read)])
		}
		wh.Write(chunk)
		read += int64(n)
		if errors.Is(rerr, io.EOF) || n == 0 {
			break
		}
		if rerr != nil {
			return "", "", 0, 0, fmt.Errorf("read: %w", rerr)
		}
	}
	return hex.EncodeToString(wh.Sum(nil)), hex.EncodeToString(oh.Sum(nil)), read, lines, nil
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
	meta := map[string]string{
		"sha256": u.piece.SHA256, "agent": u.file.Agent, "machine": s.cfg.MachineID, "role": string(u.file.Role),
		"start": strconv.FormatInt(u.piece.Start, 10), "end": strconv.FormatInt(u.piece.End, 10),
	}
	var err error
	if u.bodyPath != "" {
		err = s.store.PutFile(ctx, u.piece.Object, u.bodyPath, meta)
	} else {
		err = s.store.Put(ctx, u.piece.Object, u.body, meta)
	}
	if err != nil {
		return err
	}
	if u.version != nil {
		if err := s.store.Put(ctx, s.versionsKey(u.file.SessionFile), u.version.index, map[string]string{}); err != nil {
			return err
		}
	}
	if u.piece.Start == 0 {
		s.deleteReplacedSegments(ctx, u.replaced)
	}
	return nil
}

func (s *Syncer) copyVersion(ctx context.Context, u *upload) error {
	if u.version == nil {
		return nil
	}
	for _, p := range u.version.newBlobs {
		ok, err := s.store.Copy(ctx, p.Object, raw.BlobKey(p.SHA256))
		if err != nil {
			return err
		}
		if !ok {
			s.log.Warn("earlier bytes are missing from the bucket and can't be kept", "path", u.file.Path,
				"object", p.Object)
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
	if u.needsUpload {
		if err := s.recordChanged(u); err != nil {
			return err
		}
	} else {
		s.Summary.Unchanged++
	}
	if u.file.SessionKey != "" && (u.needsUpload || u.file.Head != nil) {
		if err := s.markManifestPending(u.file.Session()); err != nil {
			return err
		}
	}
	if err := s.state.RecordFile(&u.file); err != nil {
		return err
	}
	return s.state.Flush()
}

func (s *Syncer) recordChanged(u *upload) error {
	if err := s.state.RecordUpload(u.key, u.file.SHA256, u.file.Size); err != nil {
		return err
	}
	if u.piece.Start > 0 {
		if err := s.state.AddPiece(u.key, u.piece); err != nil {
			return err
		}
		s.Summary.Segments++
	} else {
		if err := s.state.SetPieces(u.key, []raw.Piece{u.piece}); err != nil {
			return err
		}
		if len(u.replaced) > 1 {
			s.Summary.Folded++
		}
	}
	if u.version != nil {
		for _, p := range u.version.newBlobs {
			if err := s.state.RecordUpload(raw.BlobKey(p.SHA256), p.SHA256, p.End-p.Start); err != nil {
				return err
			}
		}
		if err := s.state.RecordVersion(u.key, &u.version.Version); err != nil {
			return err
		}
		s.Summary.Versions++
	}
	s.Summary.Uploaded++
	s.Summary.BytesUploaded += u.bodySize()
	if u.file.Role == adapters.RoleUnknown {
		s.Summary.Unknown++
	}
	return nil
}
