package syncer

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/klauspost/compress/zstd"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/capturepolicy"
	"github.com/mich-murphy/memory/internal/raw"
	"github.com/mich-murphy/memory/internal/scrub"
	"github.com/mich-murphy/memory/internal/state"
)

var errVanished = errors.New("file vanished")

const mtimeLastLineHeldBack int64 = -1

type upload struct {
	file        state.File
	key         string
	outcome     uploadOutcome
	piece       raw.Piece
	body        []byte
	bodyPath    string
	stagedPath  string
	replaced    []raw.Piece
	resume      *state.Resume
	tail        uint64
	expectedSHA string
	compress    func(u *upload) error
	publication *publication
	previous    *raw.Generation
}

func (u *upload) cleanup() {
	for _, path := range []string{u.bodyPath, u.stagedPath} {
		if path != "" {
			_ = os.Remove(path)
		}
	} // disposable private staging; retry recaptures
}
func (u *upload) held() int64 { return min(u.piece.End-u.piece.Start, largeFileSize) }
func (u *upload) bodySize() int64 {
	if u.body != nil {
		return int64(len(u.body))
	}
	if st, err := os.Stat(u.bodyPath); err == nil {
		return st.Size()
	}
	return 0
}

func resolvedPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return filepath.Clean(path)
}

func (s *Syncer) excludedFile(sf adapters.SessionFile) bool {
	if s.opts.Exclusions.Excludes(sf) || s.policyErr != nil {
		return true
	}
	export := sf.SourcePath != "" && fsWithin(sf.Path, s.workDir) && (sf.Role == adapters.RoleDB && (strings.HasPrefix(sf.Key, "db/") || strings.Contains(sf.Key, "/db/")) || sf.Agent == "claude" && sf.Role == adapters.RoleConfig && strings.HasSuffix(sf.Key, "claude.json") && filepath.Base(sf.SourcePath) == ".claude.json")
	path := sf.Path
	if export {
		staging, err := capturepolicy.Resolve(sf.Path)
		if err != nil || !fsWithin(staging.Path, resolvedPath(s.workDir)) {
			return true
		}
		if slices.ContainsFunc(staging.Identities, capturepolicy.IsCredential) {
			return true
		}
		path = sf.SourcePath
	}
	resolution, err := capturepolicy.Resolve(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return true
	}
	identities := slices.Clone(resolution.Identities)
	if export && sf.Agent == "claude" && sf.Role == adapters.RoleConfig {
		return s.policy.ExcludesClaudeConfig(identities)
	}

	if s.policy != nil {
		return s.policy.Excludes(identities)
	}
	return s.protected(path, resolution.Path)
}

func fsWithin(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && (rel == "." || filepath.IsLocal(rel))
}

func (s *Syncer) protected(logical, resolved string) bool {
	if s.policyErr != nil {
		return true
	}
	resolution, err := capturepolicy.Resolve(logical)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return true
	}
	if s.policy != nil {
		return s.policy.Excludes(resolution.Identities)
	}
	return capturepolicy.Excludes(logical, resolved, s.cfg.Path, s.cfg.StateDir)
}

func readFile(sf adapters.SessionFile) ([]byte, error) {
	resolution, err := capturepolicy.Resolve(sf.Path)
	if err != nil {
		return nil, err
	}
	protected := slices.ContainsFunc(resolution.Identities, scrub.Recognizes)
	f, err := os.Open(sf.Path)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	defer func() { _ = f.Close() }() // read-only
	var reader io.Reader = f
	if sf.Zstd {
		decoder, err := zstd.NewReader(f, zstd.WithDecoderConcurrency(1), zstd.WithDecoderLowmem(true), zstd.WithDecoderMaxMemory(128<<20))
		if err != nil {
			return nil, err
		}
		defer decoder.Close()
		reader = decoder
	}
	if protected {
		reader = io.LimitReader(reader, maxRecordSize+1)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	if protected && len(data) > maxRecordSize {
		return nil, errors.New("archive config: exceeds bounded parse limit")
	}
	return scrub.Identities(sf.Agent, resolution.Identities, data)
}

func readCompleteLines(sf adapters.SessionFile, st os.FileInfo, mayBeMidLine bool) (data []byte, mtimeNS int64, err error) {
	data, err = readFile(sf)
	if err != nil {
		return nil, 0, err
	}
	if mayBeMidLine && len(data) > 0 && !bytes.HasSuffix(data, []byte("\n")) {
		return data[:bytes.LastIndexByte(data, '\n')+1], mtimeLastLineHeldBack, nil
	}
	return data, st.ModTime().UnixNano(), nil
}

func prefixSHA(data []byte) string {
	data = data[:min(len(data), 1<<20)]
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
		return 0, err
	}
	defer func() { _ = f.Close() }() // read-only
	buf := make([]byte, chunkSize)
	for pos := size; pos > 0; {
		start := max(0, pos-chunkSize)
		n, err := f.ReadAt(buf[:pos-start], start)
		if err != nil && !errors.Is(err, io.EOF) {
			return 0, err
		}
		if nl := bytes.LastIndexByte(buf[:n], '\n'); nl >= 0 {
			return start + int64(nl) + 1, nil
		}
		pos = start
	}
	return 0, nil
}

type content struct {
	hashed
	mtimeNS    int64
	diskSize   int64
	first      []byte
	compress   func(u *upload) error
	stagedPath string
}

func inMemoryContent(sf adapters.SessionFile, st os.FileInfo, prev *state.Upload, saved *state.Resume, partial bool) (*content, error) {
	data, mtimeNS, err := readCompleteLines(sf, st, partial)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 && mtimeNS == mtimeLastLineHeldBack {
		return nil, nil
	}
	h, err := hashContent(bytes.NewReader(data), int64(len(data)), prev, saved)
	if err != nil {
		return nil, err
	}
	return &content{hashed: h, mtimeNS: mtimeNS, diskSize: st.Size(), first: data, compress: func(u *upload) error {
		part := data[u.piece.Start:]
		u.piece.SHA256 = raw.SHA256(part)
		u.body = raw.Compress(part)
		return nil
	}}, nil
}

// stageDecoded bounds memory for both plain and compressed sources. Hashing and compression
// operate on this private captured stream, not a later re-read of the mutable source.
func (s *Syncer) stageDecoded(ctx context.Context, sf adapters.SessionFile) (path string, err error) {
	if err := os.MkdirAll(s.tmpDir, 0o700); err != nil {
		return "", err
	}
	source, err := os.Open(sf.Path)
	if err != nil {
		return "", err
	}
	defer func() { _ = source.Close() }() // read-only
	var reader io.Reader = source
	if sf.Zstd {
		decoder, err := zstd.NewReader(source, zstd.WithDecoderConcurrency(1), zstd.WithDecoderLowmem(true), zstd.WithDecoderMaxMemory(128<<20))
		if err != nil {
			return "", err
		}
		defer decoder.Close()
		reader = decoder
	}
	out, err := os.CreateTemp(s.tmpDir, "capture-*")
	if err != nil {
		return "", err
	}
	_, err = io.Copy(out, contextReader{ctx: ctx, reader: reader})
	err = errors.Join(err, out.Close())
	if err != nil {
		_ = os.Remove(out.Name())
		return "", err
	} // failed staging is never referenced remotely
	return out.Name(), nil
}

func (s *Syncer) streamedContent(path string, st os.FileInfo, prev *state.Upload, saved *state.Resume, partial bool) (*content, error) {
	limit := st.Size()
	if partial {
		var err error
		limit, err = completeLinesEnd(path, limit)
		if err != nil {
			return nil, err
		}
	}
	if limit == 0 && st.Size() > 0 && partial {
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
	mtimeNS := st.ModTime().UnixNano()
	if limit != st.Size() {
		mtimeNS = mtimeLastLineHeldBack
	}
	// A SHA-256 of the actual captured tail guards compression against any mutation between
	// preparation and compression. Prefix classification is cryptographic too.
	tailSHA := ""
	if prev != nil && prev.Size <= limit {
		tailSHA, err = hashRange(path, prev.Size, limit-prev.Size)
		if err != nil {
			return nil, err
		}
	}
	return &content{hashed: h, mtimeNS: mtimeNS, diskSize: st.Size(), first: first, compress: s.capturedCompressor(path, h.sha, tailSHA)}, nil
}

func hashRange(path string, start, size int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }() // read-only
	h := sha256.New()
	n, err := io.Copy(h, io.NewSectionReader(f, start, size))
	if err != nil {
		return "", err
	}
	if n != size {
		return "", errRewritten
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func hashFile(path string, limit int64, prev *state.Upload, saved *state.Resume) (hashed, error) {
	f, err := os.Open(path)
	if err != nil {
		return hashed{}, err
	}
	defer func() { _ = f.Close() }() // read-only
	return hashContent(f, limit, prev, saved)
}

func readRange(path string, start, n int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // read-only
	data := make([]byte, n)
	_, err = f.ReadAt(data, start)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func headLines(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // read-only
	reader := bufio.NewReader(io.LimitReader(f, limit))
	var out []byte
	for range prefixLines {
		line, err := readLine(reader, min(1<<20-len(out), int(16*chunkSize)))
		out = append(out, line...)
		if err != nil || len(out) >= 1<<20 {
			break
		}
	}
	return out, nil
}

func readLine(reader *bufio.Reader, maxSize int) ([]byte, error) {
	var out []byte
	for len(out) < maxSize {
		b, err := reader.ReadByte()
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
		return "", err
	}
	src, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = src.Close() }() // read-only
	out, err := os.CreateTemp(s.tmpDir, "*.zst")
	if err != nil {
		return "", err
	}
	if _, err = src.Seek(start, io.SeekStart); err == nil {
		err = raw.CompressStream(src, out, length, h)
	}
	err = errors.Join(err, out.Close())
	if err != nil {
		_ = os.Remove(out.Name())
		return "", err
	} // failed staging cleanup
	return out.Name(), nil
}

func (s *Syncer) captureContent(ctx context.Context, sf adapters.SessionFile, st os.FileInfo, prev *state.Upload, partial bool) (*content, error) {
	resolution, err := capturepolicy.Resolve(sf.Path)
	if err != nil {
		return nil, err
	}
	protected := slices.ContainsFunc(resolution.Identities, scrub.Recognizes)
	if protected && !sf.Zstd && st.Size() > maxRecordSize {
		return nil, errors.New("archive config: exceeds bounded parse limit")
	}
	if protected || !sf.Zstd && st.Size() <= largeFileSize {
		return inMemoryContent(sf, st, prev, nil, partial)
	}
	staged, err := s.stageDecoded(ctx, sf)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(staged)
	if err != nil {
		_ = os.Remove(staged)
		return nil, err
	} // disposable capture
	c, err := s.streamedContent(staged, info, prev, nil, partial)
	if err != nil || c == nil {
		_ = os.Remove(staged)
		return c, err
	} // not published
	c.stagedPath = staged
	c.diskSize = st.Size()
	if c.mtimeNS != mtimeLastLineHeldBack {
		c.mtimeNS = st.ModTime().UnixNano()
	}
	return c, nil
}

func (s *Syncer) prepare(ctx context.Context, sf adapters.SessionFile) (*upload, error) {
	for _, identity := range []string{sf.Key, sf.SessionKey, sf.Root, sf.Path, sf.SourcePath} {
		if !utf8.ValidString(identity) {
			return nil, errors.New("archive capture: unsupported non-UTF-8 identity or path")
		}
	}
	if s.excludedFile(sf) {
		s.Summary.Skipped++
		return nil, nil
	}
	st, err := os.Stat(sf.Path)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, errors.New("archive capture: source is not a regular file")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	baseline, err := s.fileBaseline(ctx, sf, st)
	if err != nil {
		return nil, err
	}
	if baseline.skip {
		return nil, nil
	}
	prev := baseline.prev
	// A complete zstd frame is a closed artifact, including a final decoded fragment.
	// Only a live plaintext JSONL source needs an incomplete-line holdback.
	partial := !sf.Zstd && strings.HasSuffix(sf.Path, ".jsonl") && s.nowSeconds()-fileMtime(st) < partialLineWindow.Seconds()
	c, err := s.captureContent(ctx, sf, st, prev, partial)
	if err != nil {
		return nil, err
	}
	if c == nil {
		s.Summary.Skipped++
		return nil, nil
	}
	return s.planCaptured(sf, c, baseline), nil
}

type fileBaseline struct {
	key      string
	idle     bool
	previous *raw.Generation
	prev     *state.Upload
	record   *state.Record
	skip     bool
}

func (s *Syncer) fileBaseline(ctx context.Context, sf adapters.SessionFile, st os.FileInfo) (*fileBaseline, error) {
	key := s.rawKey(sf.Agent, sf.Key)
	idle := s.nowSeconds()-fileMtime(st) >= foldAfter.Seconds()
	unchanged, _, err := s.db.FileUnchanged(sf.Path, key, st.Size(), st.ModTime().UnixNano())
	if err != nil {
		return nil, err
	}
	p, _, err := s.current("file", sf.Agent, sf.Key)
	if err != nil {
		return nil, err
	}
	if s.warm && unchanged && p != nil && p.File.DeletedAt == 0 && (!idle || len(p.File.Pieces) <= 1) {
		s.Summary.Unchanged++
		return &fileBaseline{skip: true}, nil
	}
	if err := s.refreshStream(ctx, "file", sf.Agent, sf.Key); err != nil {
		return nil, err
	}
	p, r, err := s.current("file", sf.Agent, sf.Key)
	if err != nil {
		return nil, err
	}
	var previous *raw.Generation
	var prev *state.Upload
	if p != nil {
		if err := s.verifyPublication(ctx, p); err != nil {
			return nil, err
		}
		previous = p.File
		prev = &state.Upload{SHA256: previous.SHA256, Size: previous.Size}
	}
	if unchanged && previous != nil && previous.DeletedAt == 0 && (!idle || len(previous.Pieces) <= 1) {
		s.Summary.Unchanged++
		return &fileBaseline{skip: true}, nil
	}

	return &fileBaseline{key: key, idle: idle, previous: previous, prev: prev, record: r}, nil
}

func (s *Syncer) planCaptured(sf adapters.SessionFile, c *content, baseline *fileBaseline) *upload {
	key, previous, prev, idle, r := baseline.key, baseline.previous, baseline.prev, baseline.idle, baseline.record
	u := &upload{outcome: uploadOutcome{Status: captureUnchanged}, key: key, previous: previous, resume: c.resume, tail: c.tail, compress: c.compress, stagedPath: c.stagedPath, file: state.File{SessionFile: sf, Size: c.size, DiskSize: c.diskSize, MtimeNS: c.mtimeNS, SHA256: c.sha, LineCount: c.lines, PrefixSHA256: prefixSHA(c.first)}}
	if transcriptRoles[sf.Role] {
		u.file.Head = s.adapters[sf.Agent].SessionInfo(adapters.ReadHead(c.first, prefixLines))
	}
	if previous != nil && previous.SHA256 == c.sha && previous.DeletedAt == 0 && (!idle || len(previous.Pieces) <= 1) {
		return u
	}
	u.outcome.Status = captureStaged
	kind := "capture"
	var pieces []raw.Piece
	start := int64(0)
	if previous != nil {
		u.replaced = previous.Pieces
		if previous.SHA256 == c.sha {
			kind = "fold"
		} else if c.appended && prev.Size > 0 && !idle && len(previous.Pieces) < maxSegments && c.size > prev.Size {
			start = prev.Size
			pieces = slices.Clone(previous.Pieces)
		}
	}
	u.piece = raw.Piece{Start: start, End: c.size}
	g := &raw.Generation{Schema: 3, Identity: raw.Identity{Agent: sf.Agent, MachineID: s.cfg.MachineID, Key: sf.Key, SessionKey: sf.SessionKey, Root: sf.Root}, Sequence: 1, Kind: kind, Sources: []raw.Source{{Path: sf.Path, SourcePath: sf.SourcePath, Encoding: "plain"}}, Role: string(sf.Role), Size: c.size, SHA256: c.sha, LineCount: c.lines, PrefixSHA256: u.file.PrefixSHA256, Head: u.file.Head, Pieces: pieces, ObservedAt: s.nowSeconds()}
	if sf.Zstd {
		g.Sources[0].Encoding = "zstd"
	}
	if r != nil {
		g.Sequence = r.Sequence + 1
		g.Parent = r.ID
	}
	if aliases := s.aliases[key]; len(aliases) > 0 {
		g.Sources = aliases
	}
	u.publication = &publication{Schema: 3, MachineID: s.cfg.MachineID, Kind: "file", Agent: sf.Agent, Key: sf.Key, Sequence: g.Sequence, Parent: g.Parent, File: g}
	return u
}

func pieceDependency(piece raw.Piece) dependency {
	return dependency{Object: piece.Object, SHA256: piece.EncodedSHA256, Size: piece.EncodedSize, Encoding: "zstd", RawSHA256: piece.SHA256, RawSize: piece.End - piece.Start}
}

func (s *Syncer) upload(ctx context.Context, u *upload) error {
	if err := u.compress(u); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return errVanished
		}
		return err
	}
	if err := sealPiece(u); err != nil {
		return err
	}
	p := u.publication
	p.File.Pieces = append(p.File.Pieces, u.piece)
	for _, piece := range p.File.Pieces {
		p.Dependencies = append(p.Dependencies, pieceDependency(piece))
	}
	for _, piece := range p.File.Pieces[:len(p.File.Pieces)-1] {
		if err := verifyDependencyMetadata(ctx, s.bucket, pieceDependency(piece)); err != nil {
			return err
		}
	}
	if err := p.validate(s.cfg.MachineID); err != nil {
		return err
	}
	body, id, err := canonicalPublication(p)
	if err != nil {
		return err
	}
	if err := s.putRecord(ctx, s.publicationKey(p, false, id), body); err != nil {
		return err
	}
	u.outcome = uploadOutcome{Status: captureUnknown, GenerationID: id}
	meta := map[string]string{"sha256": u.piece.SHA256, "encoded_sha256": u.piece.EncodedSHA256}
	if u.bodyPath != "" {
		err = s.bucket.PutFile(ctx, u.piece.Object, u.bodyPath, meta)
	} else {
		err = s.bucket.Put(ctx, u.piece.Object, u.body, meta)
	}
	if err != nil {
		if verify := verifyDependency(ctx, s.bucket, pieceDependency(u.piece)); verify != nil {
			return errors.Join(err, verify)
		}
	}
	if err := s.verifyWriter(ctx); err != nil {
		return err
	}
	if err := s.putRecord(ctx, s.publicationKey(p, true, id), body); err != nil {
		return err
	}
	u.outcome.Status = captureCommitted
	return nil
}

func (s *Syncer) finish(u *upload) error {
	defer u.cleanup()
	if u.outcome.Status == captureUnchanged {
		s.Summary.Unchanged++
		return s.db.ObserveFile(u.key, &u.file)
	}
	p := u.publication
	body, id, err := canonicalPublication(p)
	if err != nil {
		return err
	}
	r := recordFor(s.publicationKey(p, true, id), id, p, body, true)
	if err := s.db.CommitCapture(r, u.key, p.File, u.previous, &u.file); err != nil {
		return err
	}
	s.Summary.Uploaded++
	s.Summary.BytesUploaded += u.bodySize()
	if u.piece.Start > 0 {
		s.Summary.Segments++
	}
	if p.File.Kind == "fold" {
		s.Summary.Folded++
	}
	if u.previous != nil && u.previous.SHA256 != p.File.SHA256 {
		s.Summary.Versions++
	}
	if u.file.Role == adapters.RoleUnknown {
		s.Summary.Unknown++
	}
	if u.file.SessionKey != "" {
		s.touched[u.file.Session()] = true
		s.noteSessionRoot(u.file.Session(), u.file.Root)
	}
	return nil
}

func (s *Syncer) capturedCompressor(path, wholeSHA, tailSHA string) func(*upload) error {
	return func(u *upload) error {
		n := u.piece.End - u.piece.Start
		u.expectedSHA = wholeSHA
		if u.piece.Start > 0 {
			u.expectedSHA = tailSHA
		}
		digest := sha256.New()
		if n > largeFileSize {
			var err error
			u.bodyPath, err = s.compressToTemp(path, u.piece.Start, n, digest)
			if err != nil {
				return err
			}
			return u.setPieceSHA(digest)
		}
		part, err := readRange(path, u.piece.Start, n)
		if err != nil {
			return err
		}
		_, _ = digest.Write(part) // hash writes never fail
		if err := u.setPieceSHA(digest); err != nil {
			return err
		}
		u.body = raw.Compress(part)
		return nil
	}
}

type captureStatus string

const (
	captureUnchanged captureStatus = "unchanged"
	captureStaged    captureStatus = "staged"
	captureUnknown   captureStatus = "unknown"
	captureCommitted captureStatus = "committed"
)

type uploadOutcome struct {
	Status       captureStatus
	GenerationID string
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}

// sealPiece fixes immutable encoded identity from the exact staged compressed bytes.
func sealPiece(u *upload) error {
	if u.bodyPath != "" {
		sha, err := hashRange(u.bodyPath, 0, u.bodySize())
		if err != nil {
			return err
		}
		u.piece.EncodedSHA256 = sha
	} else {
		u.piece.EncodedSHA256 = raw.SHA256(u.body)
	}
	u.piece.EncodedSize = u.bodySize()
	u.piece.Object = raw.EncodedKey(u.piece.EncodedSHA256)
	return nil
}
