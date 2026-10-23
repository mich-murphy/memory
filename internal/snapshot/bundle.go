package snapshot

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/mich-murphy/memory/internal/jsonx"
	"github.com/mich-murphy/memory/internal/raw"
)

const (
	maxBundleSize     = 128 << 20
	maxBundleMetadata = 8 << 20
)

type BundleResult struct {
	Reader   io.ReadCloser
	SHA256   string
	Size     int64
	Evidence []AssetEvidence
}
type bundleAsset struct {
	Member string `json:"member"`
	SHA256 string `json:"sha256"`
	Size   int    `json:"size"`
}
type bundleManifest struct {
	Version  int           `json:"version"`
	Complete bool          `json:"complete"`
	Assets   []bundleAsset `json:"assets"`
}
type bundleReader struct {
	ctx    context.Context
	body   *bytes.Reader
	closed bool
}

func (r *bundleReader) Read(p []byte) (int, error) {
	if r.closed {
		return 0, io.ErrClosedPipe
	}
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.body.Read(p)
}
func (r *bundleReader) Close() error { r.closed = true; r.body = nil; return nil }

// Bundle produces a self-contained canonical TAR, with no Mtimes or local policy
// evidence in its content identity. Every selected blob must be present and valid.
func Bundle(ctx context.Context, s *Snapshot) (*BundleResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	assets, err := bundleClosure(s)
	if err != nil {
		return nil, err
	}
	manifest := bundleManifest{Version: 1, Complete: s.Complete, Assets: assets}
	members := []struct {
		name  string
		value any
	}{{"snapshot.json", manifest}, {"harness.json", s.Harness}}
	if s.Memory != nil {
		members = append(members, struct {
			name  string
			value any
		}{"memory.json", s.Memory})
	}
	var body bytes.Buffer
	tw := tar.NewWriter(&bundleCapacityWriter{writer: &body, remaining: maxBundleSize})
	metadata := 0
	for _, member := range members {
		data, err := jsonx.SortedCompact(member.value)
		if err != nil {
			return nil, err
		}
		metadata += len(data)
		if metadata > maxBundleMetadata {
			return nil, errors.New("snapshot: metadata capacity exceeded")
		}
		if err := writeBundleMember(ctx, tw, member.name, data); err != nil {
			return nil, err
		}
	}
	for _, asset := range assets {
		if err := writeBundleMember(ctx, tw, asset.Member, s.Blobs[asset.SHA256]); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if body.Len() > maxBundleSize {
		return nil, errors.New("snapshot: bundle capacity exceeded")
	}
	data := body.Bytes()
	return &BundleResult{Reader: &bundleReader{ctx: ctx, body: bytes.NewReader(data)}, SHA256: raw.SHA256(data), Size: int64(len(data)), Evidence: cloneEvidenceList(s.Evidence)}, nil
}

func writeBundleMember(ctx context.Context, tw *tar.Writer, name string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(data)), Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}); err != nil {
		return err
	}
	_, err := io.Copy(tw, contextReader{ctx, bytes.NewReader(data)})
	return err
}

func bundleClosure(s *Snapshot) ([]bundleAsset, error) {
	if s == nil || s.Harness == nil {
		return nil, errors.New("snapshot: missing harness")
	}
	if err := validateSnapshotCompleteness(s); err != nil {
		return nil, err
	}
	selected, err := selectedAssets(s)
	if err != nil {
		return nil, err
	}
	hashes := make([]string, 0, len(selected))
	for sha := range selected {
		hashes = append(hashes, sha)
	}
	slices.Sort(hashes)
	assets := make([]bundleAsset, 0, len(hashes))
	held := 0
	for _, sha := range hashes {
		data, ok := s.Blobs[sha]
		if !ok || len(data) != selected[sha] || raw.SHA256(data) != sha {
			return nil, errors.New("snapshot: invalid asset closure")
		}
		held += len(data)
		if held > maxSnapshotSize {
			return nil, errors.New("snapshot: content capacity exceeded")
		}
		assets = append(assets, bundleAsset{Member: fmt.Sprintf("assets/%06d.bin", len(assets)), SHA256: sha, Size: len(data)})
	}
	return assets, nil
}

// ReadBundle validates generated regular names, bounded metadata/content and exact
// internal closure. It never opens original source paths or external blob stores.
func ReadBundle(ctx context.Context, r io.Reader) (*Snapshot, error) {
	data, err := io.ReadAll(io.LimitReader(contextReader{ctx, r}, maxBundleSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxBundleSize {
		return nil, errors.New("snapshot: bundle capacity exceeded")
	}
	members, err := readBundleMembers(ctx, data)
	if err != nil {
		return nil, err
	}
	s, err := decodeBundleMembers(members)
	if err != nil {
		return nil, err
	}
	canonical, err := Bundle(ctx, s)
	if err != nil {
		return nil, err
	}
	defer func() { _ = canonical.Reader.Close() }() // in-memory reader
	encoded, err := io.ReadAll(canonical.Reader)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(data, encoded) {
		return nil, errors.New("snapshot: noncanonical TAR")
	}
	return s, nil
}

func decodeBundleMembers(members map[string][]byte) (*Snapshot, error) {
	var manifest bundleManifest
	if err := jsonx.Decode(members["snapshot.json"], &manifest); err != nil {
		return nil, err
	}
	if manifest.Version != 1 {
		return nil, errors.New("snapshot: unsupported bundle")
	}
	s := &Snapshot{Complete: manifest.Complete, Harness: &Set{}, Blobs: map[string][]byte{}, Mtimes: map[string]float64{}}
	if err := jsonx.Decode(members["harness.json"], s.Harness); err != nil {
		return nil, err
	}
	if b, ok := members["memory.json"]; ok {
		s.Memory = &Set{}
		if err := jsonx.Decode(b, s.Memory); err != nil {
			return nil, err
		}
	}
	if err := decodeBundleAssets(s, manifest.Assets, members); err != nil {
		return nil, err
	}
	expected, err := bundleClosure(s)
	if err != nil {
		return nil, err
	}
	if len(expected) != len(manifest.Assets) {
		return nil, errors.New("snapshot: invalid closure")
	}
	for i, a := range expected {
		if a != manifest.Assets[i] {
			return nil, errors.New("snapshot: noncanonical closure")
		}
	}
	n := 2 + len(expected)
	if s.Memory != nil {
		n++
	}
	if len(members) != n {
		return nil, errors.New("snapshot: extra members")
	}
	return s, nil
}

func selectedAssets(s *Snapshot) (map[string]int, error) {
	if err := checkSnapshotMetadata(s); err != nil {
		return nil, err
	}
	selected := map[string]int{}
	count := 0
	for _, set := range []*Set{s.Harness, s.Memory} {
		if set == nil {
			continue
		}
		for _, f := range set.Files {
			count++
			if count > maxWalkFiles*2 {
				return nil, errors.New("snapshot: metadata capacity exceeded")
			}
			if f.Size < 0 || f.Size > maxFileSize {
				return nil, errors.New("snapshot: invalid file size")
			}
			if old, ok := selected[f.SHA256]; ok && old != f.Size {
				return nil, errors.New("snapshot: inconsistent asset length")
			}
			selected[f.SHA256] = f.Size
		}
	}
	return selected, nil
}

func decodeBundleAssets(s *Snapshot, assets []bundleAsset, members map[string][]byte) error {
	for i, a := range assets {
		if a.Member != fmt.Sprintf("assets/%06d.bin", i) {
			return errors.New("snapshot: invalid asset member")
		}
		b, ok := members[a.Member]
		if !ok || len(b) != a.Size || raw.SHA256(b) != a.SHA256 {
			return errors.New("snapshot: invalid asset")
		}
		s.Blobs[a.SHA256] = b
	}
	return nil
}

func readBundleMembers(ctx context.Context, data []byte) (map[string][]byte, error) {
	tr := tar.NewReader(bytes.NewReader(data))
	members := map[string][]byte{}
	metadata := int64(0)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag != tar.TypeReg || h.Size < 0 || h.Size > maxSnapshotSize || len(members) > maxWalkFiles*2 {
			return nil, errors.New("snapshot: invalid member")
		}
		if h.Name != "snapshot.json" && h.Name != "harness.json" && h.Name != "memory.json" && !strings.HasPrefix(h.Name, "assets/") {
			return nil, errors.New("snapshot: invalid member name")
		}
		if _, ok := members[h.Name]; ok {
			return nil, errors.New("snapshot: duplicate member")
		}
		if strings.HasSuffix(h.Name, ".json") {
			metadata += h.Size
		}
		if metadata > maxBundleMetadata {
			return nil, errors.New("snapshot: metadata capacity exceeded")
		}
		b, err := io.ReadAll(contextReader{ctx, tr})
		if err != nil {
			return nil, err
		}
		members[h.Name] = b
	}
	return members, nil
}

type bundleCapacityWriter struct {
	writer    io.Writer
	remaining int
}

func (w *bundleCapacityWriter) Write(p []byte) (int, error) {
	if len(p) > w.remaining {
		return 0, errors.New("snapshot: bundle capacity exceeded")
	}
	n, err := w.writer.Write(p)
	w.remaining -= n
	return n, err
}

func checkSnapshotMetadata(s *Snapshot) error {
	size := 0
	for _, set := range []*Set{s.Harness, s.Memory} {
		if set == nil {
			continue
		}
		size += len(set.Agent) + len(set.UserBase)
		if set.ProjectRoot != nil {
			size += len(*set.ProjectRoot)
		}
		if len(set.Skipped) > maxSkippedFiles+1 || len(set.Files) > maxWalkFiles*2 {
			return errors.New("snapshot: metadata capacity exceeded")
		}
		for _, f := range set.Files {
			size += 256 + 6*(len(f.Path)+len(f.Target)+len(f.Scope))
		}
		for _, f := range set.Skipped {
			size += 128 + 6*(len(f.Path)+len(f.Reason)+len(f.Scope))
		}
		if size > maxBundleMetadata {
			return errors.New("snapshot: metadata capacity exceeded")
		}
	}
	return nil
}

func validateSnapshotCompleteness(s *Snapshot) error {
	if s.Harness.Kind != "harness" || s.Memory != nil && s.Memory.Kind != "memory" {
		return errors.New("snapshot: invalid set kind")
	}
	complete := s.Harness.Complete && (s.Memory == nil || s.Memory.Complete)
	if s.Complete != complete {
		return errors.New("snapshot: inconsistent completeness")
	}
	for _, set := range []*Set{s.Harness, s.Memory} {
		if set != nil && set.Complete != (len(set.Skipped) == 0) {
			return errors.New("snapshot: inconsistent set completeness")
		}
	}
	return nil
}

func cloneEvidenceList(input []AssetEvidence) []AssetEvidence {
	out := make([]AssetEvidence, len(input))
	for i, e := range input {
		out[i] = cloneEvidence(e)
	}
	return out
}
