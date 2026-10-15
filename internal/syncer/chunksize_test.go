//go:build darwin || linux

package syncer

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"hash"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/mich-murphy/memory/internal/raw"
)

var (
	databaseChunkReference = flag.String("database-chunk-reference", "", "explicit absolute path to a quiescent SQLite backup copy")
	databaseChunkUpdate    = flag.String("database-chunk-update", "", "explicit absolute path to a second quiescent SQLite backup copy")
	errChunkSnapshot       = errors.New("chunk experiment: invalid or changed snapshot")
)

// chunkSink retains only the encoded digest and length, never compressed bodies.
// Every piece gets a fresh production streaming encoder, even if already seen.
type chunkSink struct {
	digest hash.Hash
	size   int64
}

func (s *chunkSink) Write(p []byte) (int, error) {
	n, err := s.digest.Write(p)
	s.size += int64(n)
	return n, err
}

type chunkMeasurement struct {
	size           int64
	sha            string
	pieces         []raw.Piece
	blobs          map[string]int64
	newBlobSize    int64
	newBlobCount   int64
	descriptorSize int64 // standard synthetic-identity plan+commit bytes
	descriptorID   string
}

func measureChunks(src io.Reader, size, length int64, prior map[string]int64, parent string) (chunkMeasurement, error) {
	m := chunkMeasurement{size: size, blobs: map[string]int64{}}
	whole := sha256.New()
	src = io.TeeReader(src, whole)
	if length == 0 {
		length = size // whole mode still streams, with bounded encoder memory
	}
	if size <= 0 || length <= 0 {
		return m, errChunkSnapshot
	}
	for start := int64(0); start < size; {
		end := start + min(length, size-start)
		decoded := sha256.New()
		encoded := &chunkSink{digest: sha256.New()}
		if err := raw.CompressStream(src, encoded, end-start, decoded); err != nil {
			return m, errChunkSnapshot // never propagate a reader's private path
		}
		sha := hex.EncodeToString(encoded.digest.Sum(nil))
		m.pieces = append(m.pieces, raw.Piece{
			Start: start, End: end, SHA256: hex.EncodeToString(decoded.Sum(nil)),
			EncodedSHA256: sha, EncodedSize: encoded.size, Object: raw.EncodedKey(sha),
		})
		if _, seen := m.blobs[sha]; !seen {
			m.blobs[sha] = encoded.size
			if _, exists := prior[sha]; !exists {
				m.newBlobSize += encoded.size
				m.newBlobCount++
			}
		}
		start = end
	}
	m.sha = hex.EncodeToString(whole.Sum(nil))
	_, body, id, err := chunkDescriptor(m, parent)
	if err != nil {
		return m, errChunkSnapshot
	}
	m.descriptorSize = 2 * int64(len(body)) // identical plan and commit bodies
	m.descriptorID = id
	return m, nil
}

// The identity and observation fields are deliberately synthetic and constant.
// Only the measured piece graph, sizes, digests and parent vary with the inputs.
func chunkDescriptor(m chunkMeasurement, parent string) (*publication, []byte, string, error) {
	sequence := uint64(1)
	if parent != "" {
		sequence++
	}
	identity := raw.Identity{Agent: "codex", MachineID: "chunk-experiment", Key: "db/synthetic.sqlite", Root: "/synthetic"}
	g := &raw.Generation{
		Schema: raw.Schema, Identity: identity, Sequence: sequence, Parent: parent,
		Kind: "capture", Sources: []raw.Source{{Path: "/synthetic/db.sqlite", Encoding: "plain"}},
		Role: "db", Size: m.size, SHA256: m.sha, Pieces: m.pieces, ObservedAt: 1,
	}
	p := &publication{
		Schema: raw.Schema, MachineID: identity.MachineID, Kind: "file", Agent: identity.Agent,
		Key: identity.Key, Sequence: sequence, Parent: parent, File: g,
	}
	if err := p.validate(identity.MachineID); err != nil {
		return nil, nil, "", errChunkSnapshot
	}
	body, id, err := canonicalPublication(p)
	return p, body, id, err
}

func validateChunkHeader(header []byte, size int64) error {
	if len(header) != 100 || !bytes.Equal(header[:16], []byte("SQLite format 3\x00")) {
		return errChunkSnapshot
	}
	pageSize := int64(binary.BigEndian.Uint16(header[16:18]))
	if pageSize == 1 {
		pageSize = 65536
	}
	if pageSize < 512 || pageSize > 65536 || pageSize&(pageSize-1) != 0 || size < pageSize || size%pageSize != 0 {
		return errChunkSnapshot
	}
	// Require a current in-header page count, as expected of a completed backup.
	if !bytes.Equal(header[24:28], header[92:96]) || int64(binary.BigEndian.Uint32(header[28:32])) != size/pageSize {
		return errChunkSnapshot
	}
	return nil
}

type chunkSnapshot struct {
	file *os.File
	info os.FileInfo
}

func openChunkSnapshot(name string) (*chunkSnapshot, error) {
	if !filepath.IsAbs(name) {
		return nil, errChunkSnapshot
	}
	st, err := os.Lstat(name)
	if err != nil || !st.Mode().IsRegular() {
		return nil, errChunkSnapshot
	}
	// These must be standalone backup copies, not databases with live sidecars.
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Lstat(name + suffix); !errors.Is(err, os.ErrNotExist) {
			return nil, errChunkSnapshot
		}
	}
	f, err := os.Open(name)
	if err != nil {
		return nil, errChunkSnapshot
	}
	snapshot := &chunkSnapshot{file: f, info: st}
	if err := snapshot.rewind(); err != nil {
		_ = f.Close() // read-only; the validation error is already returned
		return nil, errChunkSnapshot
	}
	return snapshot, nil
}

func (s *chunkSnapshot) unchanged() error {
	st, err := s.file.Stat()
	if err != nil || !os.SameFile(st, s.info) || st.Size() != s.info.Size() || !st.ModTime().Equal(s.info.ModTime()) {
		return errChunkSnapshot
	}
	return nil
}

func (s *chunkSnapshot) rewind() error {
	if err := s.unchanged(); err != nil {
		return err
	}
	if _, err := s.file.Seek(0, io.SeekStart); err != nil {
		return errChunkSnapshot
	}
	var header [100]byte
	if _, err := io.ReadFull(s.file, header[:]); err != nil {
		return errChunkSnapshot
	}
	if err := validateChunkHeader(header[:], s.info.Size()); err != nil {
		return err
	}
	if _, err := s.file.Seek(0, io.SeekStart); err != nil {
		return errChunkSnapshot
	}
	return nil
}

// Process CPU, not wall time or a simulation of remote PUT/HEAD latency.
func chunkCPU() (time.Duration, error) {
	var usage unix.Rusage
	if err := unix.Getrusage(unix.RUSAGE_SELF, &usage); err != nil {
		return 0, errChunkSnapshot
	}
	return time.Duration(usage.Utime.Nano() + usage.Stime.Nano()), nil
}

func runChunkMeasurement(b *testing.B, snapshot *chunkSnapshot, length int64, prior map[string]int64, parent string) (chunkMeasurement, time.Duration) {
	b.Helper()
	if err := snapshot.rewind(); err != nil {
		b.Fatal("experiment failure 1")
	}
	started, err := chunkCPU()
	if err != nil {
		b.Fatal("experiment failure 2")
	}
	m, err := measureChunks(snapshot.file, snapshot.info.Size(), length, prior, parent)
	if err != nil {
		b.Fatal("experiment failure 3")
	}
	ended, err := chunkCPU()
	if err != nil {
		b.Fatal("experiment failure 2")
	}
	if err := snapshot.unchanged(); err != nil {
		b.Fatal("experiment failure 1")
	}
	return m, ended - started
}

func BenchmarkDatabaseChunkSizing(b *testing.B) {
	if *databaseChunkReference == "" || *databaseChunkUpdate == "" {
		b.Skip("requires both explicit snapshot flags")
	}
	open := func(name string) *chunkSnapshot {
		snapshot, err := openChunkSnapshot(name)
		if err != nil {
			b.Fatal("experiment failure 1")
		}
		b.Cleanup(func() {
			if err := snapshot.file.Close(); err != nil {
				b.Error("experiment failure 4")
			}
		})
		return snapshot
	}
	reference, update := open(*databaseChunkReference), open(*databaseChunkUpdate)
	for _, scenario := range []struct {
		index  string
		length int64
	}{{"0", 0}, {"1", 1 << 20}, {"2", 4 << 20}} {
		b.Run(scenario.index, func(b *testing.B) {
			var cold, changed chunkMeasurement
			var coldCPU, updateCPU time.Duration
			b.ResetTimer()
			for range b.N {
				var elapsed time.Duration
				// Fresh reference and update maps each iteration: no compression cache.
				cold, elapsed = runChunkMeasurement(b, reference, scenario.length, nil, "")
				coldCPU += elapsed
				changed, elapsed = runChunkMeasurement(b, update, scenario.length, cold.blobs, cold.descriptorID)
				updateCPU += elapsed
			}
			b.StopTimer()
			b.ReportMetric(float64(coldCPU.Nanoseconds())/float64(b.N), "cold-cpu-ns/op")
			b.ReportMetric(float64(updateCPU.Nanoseconds())/float64(b.N), "update-cpu-ns/op")
			b.ReportMetric(float64(cold.size), "cold-raw-B")
			b.ReportMetric(float64(changed.size), "update-raw-B")
			b.ReportMetric(float64(cold.newBlobSize), "cold-blob-unique-B")
			b.ReportMetric(float64(changed.newBlobSize), "update-newblob-B")
			b.ReportMetric(float64(cold.newBlobCount), "cold-unique-blobs")
			b.ReportMetric(float64(changed.newBlobCount), "update-new-blobs")
			b.ReportMetric(float64(len(cold.pieces)), "cold-pieces")
			b.ReportMetric(float64(len(changed.pieces)), "update-pieces")
			b.ReportMetric(float64(cold.descriptorSize), "cold-synthetic-plan-commit-B")
			b.ReportMetric(float64(changed.descriptorSize), "update-synthetic-plan-commit-B")
		})
	}
}

func TestChunkMeasurementDeduplicatesRepeatedAndRelocatedPieces(t *testing.T) {
	const length = 512
	a, b, c := bytes.Repeat([]byte{1}, length), bytes.Repeat([]byte{2}, length), bytes.Repeat([]byte{3}, length)
	cold, err := measureChunks(bytes.NewReader(bytes.Join([][]byte{a, b, a}, nil)), 3*length, length, nil, "")
	if err != nil {
		t.Fatal("measureChunks failed")
	}
	changed, err := measureChunks(bytes.NewReader(bytes.Join([][]byte{b, c, c, a}, nil)), 4*length, length, cold.blobs, cold.descriptorID)
	if err != nil {
		t.Fatal("measureChunks failed")
	}
	if cold.newBlobCount != 2 || changed.newBlobCount != 1 || len(cold.pieces) != 3 || len(changed.pieces) != 4 {
		t.Fatalf("counts = %d %d %d %d, want 2 1 3 4", cold.newBlobCount, changed.newBlobCount, len(cold.pieces), len(changed.pieces))
	}
	if cold.newBlobSize != cold.pieces[0].EncodedSize+cold.pieces[1].EncodedSize || changed.newBlobSize != changed.pieces[1].EncodedSize {
		t.Fatal("unique byte totals differ from sealed piece sizes")
	}
	if changed.pieces[0].Object != cold.pieces[1].Object || changed.pieces[3].Object != cold.pieces[0].Object || changed.pieces[1].Object != changed.pieces[2].Object {
		t.Fatal("repeated or relocated piece identity differs")
	}
	unchanged, err := measureChunks(bytes.NewReader(bytes.Join([][]byte{a, b, a}, nil)), 3*length, length, cold.blobs, cold.descriptorID)
	if err != nil || unchanged.newBlobSize != 0 || unchanged.newBlobCount != 0 {
		t.Fatal("identical update must need no new blobs")
	}
}

func TestChunkDescriptorMeasuresCanonicalRecordWithOmittedDependencies(t *testing.T) {
	data := bytes.Repeat([]byte{7}, 1024)
	m, err := measureChunks(bytes.NewReader(data), int64(len(data)), 512, nil, "")
	if err != nil {
		t.Fatal("measureChunks failed")
	}
	p, body, id, err := chunkDescriptor(m, "")
	if err != nil {
		t.Fatal("chunkDescriptor failed")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatal("descriptor JSON invalid")
	}
	if _, present := fields["dependencies"]; present {
		t.Fatal("FILE dependencies must be omitted")
	}
	if m.descriptorSize != 2*int64(len(body)) || m.descriptorID != id || m.newBlobCount != 1 || len(p.dependencies()) != 2 {
		t.Fatal("descriptor size, identity or ordered repeated dependencies differ")
	}
	decoded, err := decodePublication(body)
	if err != nil || decoded.validate("chunk-experiment") != nil {
		t.Fatal("canonical descriptor does not validate")
	}
	canonical, canonicalID, err := canonicalPublication(decoded)
	if err != nil || !bytes.Equal(canonical, body) || canonicalID != id {
		t.Fatal("descriptor does not round trip canonically")
	}
	_, updateBody, _, err := chunkDescriptor(m, id)
	if err != nil || len(updateBody) <= len(body) {
		t.Fatal("update descriptor must include parent identity")
	}
}

func TestChunkMeasurementStreamsWholeAndFinalShortPiece(t *testing.T) {
	data := bytes.Repeat([]byte{11}, 1536)
	whole, err := measureChunks(bytes.NewReader(data), int64(len(data)), 0, nil, "")
	if err != nil || len(whole.pieces) != 1 || whole.sha != raw.SHA256(data) {
		t.Fatal("whole measurement differs")
	}
	fixed, err := measureChunks(bytes.NewReader(data), int64(len(data)), 1024, nil, "")
	if err != nil || len(fixed.pieces) != 2 || fixed.pieces[1].Start != 1024 || fixed.pieces[1].End != 1536 || fixed.sha != whole.sha {
		t.Fatal("final short piece or whole digest differs")
	}
	if _, err := measureChunks(bytes.NewReader(data[:512]), int64(len(data)), 0, nil, ""); err == nil {
		t.Fatal("short read accepted")
	}
}

func chunkHeader(pageSize uint16, pages uint32) []byte {
	header := make([]byte, 100)
	copy(header, "SQLite format 3\x00")
	binary.BigEndian.PutUint16(header[16:18], pageSize)
	binary.BigEndian.PutUint32(header[28:32], pages)
	return header
}

func TestChunkHeaderRequiresSQLitePageAlignmentAndCurrentPageCount(t *testing.T) {
	for _, tt := range []struct {
		name   string
		header []byte
		size   int64
		valid  bool
	}{
		{"aligned", chunkHeader(4096, 2), 8192, true},
		{"largePages", chunkHeader(1, 2), 131072, true},
		{"partialPage", chunkHeader(4096, 2), 8191, false},
		{"pageCount", chunkHeader(4096, 1), 8192, false},
		{"invalidPageSize", chunkHeader(513, 2), 1026, false},
		{"empty", chunkHeader(4096, 0), 0, false},
		{"notSQLite", make([]byte, 100), 8192, false},
		{"shortHeader", make([]byte, 99), 8192, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := validateChunkHeader(tt.header, tt.size) == nil; got != tt.valid {
				t.Fatalf("validateChunkHeader valid = %t, want %t", got, tt.valid)
			}
		})
	}
	header := chunkHeader(4096, 2)
	header[24] = 1
	if err := validateChunkHeader(header, 8192); err == nil {
		t.Fatal("stale in-header page count accepted")
	}
}

func TestChunkSnapshotRejectsSidecarsAndMutation(t *testing.T) {
	name := filepath.Join(t.TempDir(), "synthetic.sqlite")
	data := make([]byte, 4096)
	copy(data, chunkHeader(4096, 1))
	if err := os.WriteFile(name, data, 0o600); err != nil {
		t.Fatal("fixture write failed")
	}
	snapshot, err := openChunkSnapshot(name)
	if err != nil {
		t.Fatal("openChunkSnapshot failed")
	}
	t.Cleanup(func() {
		if err := snapshot.file.Close(); err != nil {
			t.Error("fixture close failed")
		}
	})
	if err := os.WriteFile(name+"-wal", nil, 0o600); err != nil {
		t.Fatal("fixture write failed")
	}
	if _, err := openChunkSnapshot(name); err == nil {
		t.Fatal("snapshot with sidecar accepted")
	}
	if err := os.Truncate(name, 8192); err != nil {
		t.Fatal("fixture truncate failed")
	}
	if err := snapshot.rewind(); err == nil {
		t.Fatal("changed snapshot accepted")
	}
	if _, err := openChunkSnapshot("relative.sqlite"); err == nil {
		t.Fatal("implicit relative location accepted")
	}
}
