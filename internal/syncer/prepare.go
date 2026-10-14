package syncer

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/jsonx"
	"github.com/mich-murphy/memory/internal/raw"
	"github.com/mich-murphy/memory/internal/scrub"
	"github.com/mich-murphy/memory/internal/state"
)

// errVanished is a file that was rotated or recompressed between being read and uploaded; the
// next sync sees it under its new name.
var errVanished = errors.New("file vanished")

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
	strays      []string
	// leftover is what the upload couldn't delete of replaced and strays.
	leftover []string
	version  *versionPlan
	resume   *state.Resume
	tail     uint64
	// compress runs on the upload pool, so backfills use every core.
	compress func(u *upload) error
}

func (u *upload) cleanup() {
	if u.bodyPath != "" {
		_ = os.Remove(u.bodyPath) // the next run clears a leftover
	}
}

// held is about how many bytes of the file the upload holds in memory until it is sent.
func (u *upload) held() int64 {
	return min(u.piece.End-u.piece.Start, largeFileSize)
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
		last, err := s.db.LastVersionAt(key)
		if err != nil {
			return nil, err
		}
		if now-last < dbVersionEvery.Seconds() {
			return nil, nil
		}
	}
	index, err := s.db.Versions(key)
	if err != nil {
		return nil, err
	}
	if slices.ContainsFunc(index, func(v state.Version) bool { return v.SHA256 == prev.SHA256 }) {
		return nil, nil
	}
	old := pieces
	if len(old) == 0 {
		old = []raw.Piece{{Start: 0, End: prev.Size, SHA256: prev.SHA256, Object: key}}
	}
	v := &versionPlan{SHA256: prev.SHA256, Size: prev.Size, VersionedAt: now}
	for _, p := range old {
		blob := raw.BlobKey(p.SHA256)
		have, err := s.db.UploadedSHA(blob)
		if err != nil {
			return nil, err
		}
		if have == "" {
			v.newBlobs = append(v.newBlobs, p)
		}
		v.Pieces = append(v.Pieces, raw.Piece{Start: p.Start, End: p.End, SHA256: p.SHA256, Object: blob})
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
	age := s.nowSeconds() - fileMtime(st)
	idle := age >= foldAfter.Seconds()
	unchanged, npieces, err := s.db.FileUnchanged(sf.Path, key, st.Size(), st.ModTime().UnixNano())
	if err != nil {
		return nil, err
	}
	if unchanged && (!idle || npieces <= 1) {
		s.Summary.Unchanged++
		return nil, nil
	}
	pieces, err := s.db.Pieces(key)
	if err != nil {
		return nil, err
	}
	prev, err := s.db.Uploaded(key)
	if err != nil {
		return nil, err
	}
	saved, err := s.db.Resume(key)
	if err != nil {
		return nil, err
	}
	mayBeMidLine := filepath.Ext(sf.Path) == ".jsonl" && age < partialLineWindow.Seconds()
	var c *content
	// A file seen before is most likely an append, which needs only its new bytes in memory.
	plain := !sf.Zstd && !scrub.Applies(sf.Agent, filepath.Base(sf.Path))
	if plain && (prev != nil || st.Size() > largeFileSize) {
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
	return s.planUpload(sf, key, c, prev, pieces, idle)
}

type content struct {
	hashed
	mtimeNS  int64
	diskSize int64
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
		hashed: h, mtimeNS: mtimeNS, diskSize: st.Size(), first: data,
		compress: func(u *upload) error {
			part := data[u.piece.Start:]
			u.piece.SHA256 = h.sha
			if u.piece.Start > 0 {
				u.piece.SHA256 = raw.SHA256(part)
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
		hashed: h, mtimeNS: mtimeNS, diskSize: st.Size(), first: first,
		compress: func(u *upload) error {
			n := u.piece.End - u.piece.Start
			h := u.pieceHash()
			if n > largeFileSize {
				var err error
				if u.bodyPath, err = s.compressToTemp(path, u.piece.Start, n, h); err != nil {
					return err
				}
				return u.setPieceSHA(h)
			}
			part, err := readRange(path, u.piece.Start, n)
			if err != nil {
				return err
			}
			_, _ = h.Write(part) // hash writes never fail
			if err := u.setPieceSHA(h); err != nil {
				return err
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

func (s *Syncer) planUpload(sf adapters.SessionFile, key string, c *content, prev *state.Upload,
	pieces []raw.Piece, idle bool,
) (*upload, error) {
	u := &upload{key: key, resume: c.resume, tail: c.tail, file: state.File{
		SessionFile: sf, Size: c.size, DiskSize: c.diskSize, MtimeNS: c.mtimeNS, SHA256: c.sha, LineCount: c.lines, PrefixSHA256: prefixSHA(c.first),
	}}
	if transcriptRoles[sf.Role] && strings.HasSuffix(strings.TrimSuffix(sf.Path, ".zst"), ".jsonl") {
		u.file.Head = s.adapters[sf.Agent].SessionInfo(adapters.ReadHead(c.first, prefixLines))
	}
	start, ok := plan(prev, pieces, c, idle)
	if u.strays = s.strays[key]; len(u.strays) > 0 {
		start, ok = 0, true
	}
	if !ok {
		return u, nil
	}
	u.needsUpload = true
	u.piece = raw.Piece{Start: start, End: c.size, Object: key}
	if start > 0 {
		u.piece.Object = raw.SegmentKey(key, start, c.size)
	}
	u.compress = c.compress
	if start == 0 {
		if err := s.planReplace(u, prev, pieces, c.appended); err != nil {
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

func readLine(r *bufio.Reader, maxSize int) ([]byte, error) {
	var out []byte
	for len(out) < maxSize {
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

func (s *Syncer) compressToTemp(path string, start, length int64, h hash.Hash) (tmpPath string, err error) {
	if err := os.MkdirAll(s.tmpDir, 0o700); err != nil {
		return "", fmt.Errorf("temp dir: %w", err)
	}
	src, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("read: %w", err)
	}
	defer func() { _ = src.Close() }() // read-only
	out, err := os.CreateTemp(s.tmpDir, "*.zst")
	if err != nil {
		return "", fmt.Errorf("temp file: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.Remove(out.Name()) // best-effort cleanup of a failed temp file
		}
	}()
	if _, err = src.Seek(start, io.SeekStart); err != nil {
		_ = out.Close() // the seek error is the one to report
		return "", fmt.Errorf("seek: %w", err)
	}
	err = raw.CompressStream(src, out, length, h)
	if cerr := out.Close(); err == nil && cerr != nil {
		err = fmt.Errorf("temp file: %w", cerr)
	}
	if err != nil {
		return "", err
	}
	return out.Name(), nil
}

// upload runs on the upload pool, so it must not touch local state.
func (s *Syncer) upload(ctx context.Context, u *upload) error {
	if err := u.compress(u); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return errVanished
		}
		return err
	}
	if err := s.copyVersion(ctx, u); err != nil {
		return err
	}
	meta := map[string]string{
		"sha256": u.piece.SHA256, "agent": u.file.Agent, "machine": s.cfg.MachineID, "role": string(u.file.Role),
		"start": strconv.FormatInt(u.piece.Start, 10), "end": strconv.FormatInt(u.piece.End, 10),
	}
	var err error
	if u.bodyPath != "" {
		err = s.bucket.PutFile(ctx, u.piece.Object, u.bodyPath, meta)
	} else {
		err = s.bucket.Put(ctx, u.piece.Object, u.body, meta)
	}
	if err != nil {
		return err
	}
	if u.version != nil {
		if err := s.bucket.Put(ctx, s.versionsKey(u.file.SessionFile), u.version.index, map[string]string{}); err != nil {
			return err
		}
	}
	if u.piece.Start == 0 {
		s.deleteReplaced(ctx, u)
	}
	return nil
}

func (s *Syncer) copyVersion(ctx context.Context, u *upload) error {
	if u.version == nil {
		return nil
	}
	for _, p := range u.version.newBlobs {
		ok, err := s.bucket.Copy(ctx, p.Object, raw.BlobKey(p.SHA256))
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

// deleteReplaced leaves what it can't delete in u.leftover, for the next run to retry.
func (s *Syncer) deleteReplaced(ctx context.Context, u *upload) {
	objects := slices.Clone(u.strays)
	for _, p := range u.replaced {
		objects = append(objects, p.Object)
	}
	for _, o := range objects {
		if o == u.key {
			continue
		}
		if err := s.bucket.Delete(ctx, o); err != nil {
			s.log.Warn("could not delete a replaced segment", "object", o, "error", err)
			u.leftover = append(u.leftover, o)
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
	if err := s.db.RecordFile(&u.file); err != nil {
		return err
	}
	if u.resume != nil {
		if err := s.db.SetResume(u.key, u.resume); err != nil {
			return err
		}
	}
	return s.db.Flush()
}

func (s *Syncer) recordChanged(u *upload) error {
	if err := s.db.RecordUpload(u.key, u.file.SHA256, u.file.Size); err != nil {
		return err
	}
	if err := s.db.SetStrays(u.key, u.leftover); err != nil {
		return err
	}
	if u.piece.Start > 0 {
		if err := s.db.AddPiece(u.key, u.piece); err != nil {
			return err
		}
		s.Summary.Segments++
	} else {
		if err := s.db.SetPieces(u.key, []raw.Piece{u.piece}); err != nil {
			return err
		}
		if len(u.replaced) > 1 {
			s.Summary.Folded++
		}
	}
	if u.version != nil {
		for _, p := range u.version.newBlobs {
			if err := s.db.RecordUpload(raw.BlobKey(p.SHA256), p.SHA256, p.End-p.Start); err != nil {
				return err
			}
		}
		if err := s.db.RecordVersion(u.key, &u.version.Version); err != nil {
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
