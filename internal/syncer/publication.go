package syncer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/klauspost/compress/zstd"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/gitinfo"
	"github.com/mich-murphy/memory/internal/jsonx"
	"github.com/mich-murphy/memory/internal/raw"
	"github.com/mich-murphy/memory/internal/state"
	"github.com/mich-murphy/memory/internal/store"
)

const maxRecordSize = 16 << 20

// manifestSchema 5 references launch records and each snapshot's mtimes blob, which schema 4
// copied into every manifest. Both reference file streams under records/session/. Schema 3
// manifests, under records/manifest/, copied every generation; the collector still reads schemas
// 3 and 4, so a session that has only a schema 3 manifest is republished, but never writes them.
const (
	manifestSchema       = 5
	copiedLaunchSchema   = 4
	copiedManifestSchema = 3
)

type dependency struct {
	Object    string `json:"object"`
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size"`
	Encoding  string `json:"encoding"`
	RawSHA256 string `json:"raw_sha256,omitempty"`
	RawSize   int64  `json:"raw_size,omitempty"`
}

// transcript names a file stream; readers follow History to its current generation, so an
// append to the file leaves the manifest unchanged.
type transcript struct {
	Key     string        `json:"key"`
	Role    adapters.Role `json:"role"`
	History string        `json:"history"`
	*CopiedGeneration
}

// CopiedGeneration is what a schema 3 manifest repeated beside each history.
type CopiedGeneration struct {
	Size         int64          `json:"size"`
	SHA256       string         `json:"sha256"`
	LineCount    int64          `json:"line_count"`
	PrefixSHA256 string         `json:"prefix_sha256,omitempty"`
	Head         map[string]any `json:"head,omitempty"`
	Object       string         `json:"object"`
	Pieces       []raw.Piece    `json:"pieces"`
	Versions     []version      `json:"versions"`
	Commit       string         `json:"commit"`
}

type version struct {
	SHA256      string      `json:"sha256"`
	Size        int64       `json:"size"`
	VersionedAt float64     `json:"versioned_at"`
	Pieces      []raw.Piece `json:"pieces"`
}

type deletion struct {
	Key string  `json:"key"`
	At  float64 `json:"at"`
}

type launchRecord = state.Launch

// launchRef names the launch record of one launch of the session.
type launchRef struct {
	ID     string `json:"id"`
	Commit string `json:"commit"`
}

type manifest struct {
	Schema           int                   `json:"schema"`
	Agent            string                `json:"agent"`
	MachineID        string                `json:"machine_id"`
	Hostname         string                `json:"hostname"`
	SessionKey       string                `json:"session_key"`
	Info             map[string]any        `json:"info"`
	Transcripts      []transcript          `json:"transcripts"`
	Launch           *launchRecord         `json:"launch"`
	Launches         []launchRecord        `json:"launches"`
	LaunchRecords    []launchRef           `json:"launch_records,omitempty"`
	Git              gitinfo.State         `json:"git"`
	Snapshots        []state.SnapshotEntry `json:"snapshots"`
	DeletedLocally   []deletion            `json:"deleted_locally"`
	FirstSeen        float64               `json:"first_seen"`
	SyncedAt         float64               `json:"synced_at"`
	CollectorVersion string                `json:"collector_version"`
}

// MarshalJSON leaves the launch copies out of schema 5, which lists launch records instead.
func (m manifest) MarshalJSON() ([]byte, error) {
	type wire manifest
	if m.Schema < manifestSchema {
		return json.Marshal(wire(m))
	}
	return json.Marshal(struct {
		wire
		Launch        *launchRecord  `json:"launch,omitempty"`
		Launches      []launchRecord `json:"launches,omitempty"`
		LaunchRecords []launchRef    `json:"launch_records"`
	}{wire: wire(m), LaunchRecords: orEmpty(m.LaunchRecords)})
}

type rootRecord struct {
	Agent string `json:"agent"`
	Path  string `json:"path"`
}

// publication has exactly one typed payload. Its identical canonical bytes are published
// first as a plan, then as a commit after all dependencies are durable. No mutable head is
// necessary: the unique parent chain selects current, and branches fail closed.
type publication struct {
	Schema       int             `json:"schema"`
	MachineID    string          `json:"machine_id"`
	Kind         string          `json:"kind"`
	Agent        string          `json:"agent"`
	Key          string          `json:"key"`
	Sequence     uint64          `json:"sequence"`
	Parent       string          `json:"parent,omitempty"`
	File         *raw.Generation `json:"file,omitempty"`
	Manifest     *manifest       `json:"manifest,omitempty"`
	Launch       *launchRecord   `json:"launch,omitempty"`
	Root         *rootRecord     `json:"root,omitempty"`
	Dependencies []dependency    `json:"dependencies"`
}

func (p publication) MarshalJSON() ([]byte, error) {
	type wire publication
	if p.File == nil || p.Dependencies != nil {
		return json.Marshal(wire(p))
	}
	return json.Marshal(struct {
		wire
		Dependencies []dependency `json:"dependencies,omitempty"`
	}{wire: wire(p)})
}

// dependencies is the single source of dependency identities for all consumers.
// Legacy explicit file lists are validated against pieces, never trusted in their place.
func (p *publication) dependencies() []dependency {
	if p.File == nil {
		return p.Dependencies
	}
	deps := make([]dependency, 0, len(p.File.Pieces))
	for _, piece := range p.File.Pieces {
		deps = append(deps, pieceDependency(piece))
	}
	return deps
}

func streamID(agent, key string) string { return raw.SHA256([]byte(agent + "\x00" + key)) }
func (s *Syncer) machinePrefix() string { return "v3/machines/" + s.cfg.MachineID + "/" }
func (s *Syncer) publicationPrefix(kind, agent, key string) string {
	return s.machinePrefix() + "records/" + kind + "/" + streamID(agent, key) + "/"
}

func (s *Syncer) publicationKey(p *publication, committed bool, id string) string {
	stage := "plans"
	if committed {
		stage = "commits"
	}
	return fmt.Sprintf("%s%s/%020d-%s.json", s.publicationPrefix(p.Kind, p.Agent, p.Key), stage, p.Sequence, id)
}

func (p *publication) validate(machine string) error {
	if p.Schema != 3 || p.MachineID != machine || !raw.ValidKey(p.MachineID) || strings.Contains(p.MachineID, "/") || !raw.ValidKey(p.Key) || !slices.Contains(adapters.Names, p.Agent) || p.Sequence == 0 || (p.Sequence == 1) != (p.Parent == "") || p.Parent != "" && !raw.ValidHash(p.Parent) {
		return errors.New("archive: invalid publication identity or parent; non-UTF-8 identities are unsupported")
	}
	count := 0
	for _, present := range []bool{p.File != nil, p.Manifest != nil, p.Launch != nil, p.Root != nil} {
		if present {
			count++
		}
	}
	if count != 1 {
		return errors.New("archive: publication must have exactly one payload")
	}
	if err := p.validatePayload(machine); err != nil {
		return err
	}

	for _, d := range p.dependencies() {
		if !raw.ValidKey(d.Object) || !strings.HasPrefix(d.Object, "v3/") || !raw.ValidHash(d.SHA256) || d.Size < 0 {
			return errors.New("archive: invalid dependency")
		}
		if d.Encoding != "plain" && d.Encoding != "zstd" {
			return errors.New("archive: invalid dependency encoding")
		}
		if d.Encoding == "zstd" && (!raw.ValidHash(d.RawSHA256) || d.RawSize < 0 || d.Object != raw.EncodedKey(d.SHA256)) {
			return errors.New("archive: invalid compressed dependency")
		}
	}
	return nil
}

func (p *publication) validatePayload(machine string) error {
	switch p.Kind {
	case "file":
		if p.File == nil {
			return errors.New("archive: missing file payload")
		}
		return p.validateFile(machine)
	case "session", "manifest":
		return p.validateManifest()
	case "launch":
		if p.Launch != nil && !utf8.ValidString(p.Launch.Root) {
			return errors.New("archive launch: unsupported non-UTF-8 root")
		}
		if p.Launch == nil || p.Sequence != 1 || p.Launch.Envelope.Agent != p.Agent || p.Launch.Envelope.ID != p.Key || !raw.ValidKey(p.Launch.SessionKey) || !raw.ValidPath(p.Launch.Root) || !validTime(p.Launch.Snapshot.TakenAt) {
			return errors.New("archive: invalid launch payload")
		}
		return validateLaunch(p.Launch)
	case "root":
		if p.Root != nil && !utf8.ValidString(p.Root.Path) {
			return errors.New("archive root: unsupported non-UTF-8 path")
		}
		if p.Root == nil || p.Sequence != 1 || p.Root.Agent != p.Agent || streamID(p.Root.Agent, p.Root.Path) != p.Key || !raw.ValidPath(p.Root.Path) {
			return errors.New("archive: invalid root payload")
		}
	default:
		return errors.New("archive: unknown publication kind")
	}
	return nil
}

func decodePublication(body []byte) (*publication, error) {
	var p publication
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&p); err != nil {
		return nil, fmt.Errorf("archive record: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, errors.New("archive record: trailing data")
	}
	// An explicitly supplied legacy file dependency list must cover the pieces, even
	// when encoded as null or an empty array. Absence alone selects derivation.
	if p.File != nil && p.Dependencies == nil {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(body, &fields); err != nil {
			return nil, err
		}
		if _, present := fields["dependencies"]; present {
			p.Dependencies = []dependency{}
		}
	}
	return &p, nil
}

func recordFor(key, id string, p *publication, body []byte, committed bool) state.Record {
	return state.Record{Key: key, Kind: p.Kind, Stream: streamID(p.Agent, p.Key), ID: id, Parent: p.Parent, Sequence: p.Sequence, Committed: committed, Body: body}
}

func (s *Syncer) current(kind, agent, key string) (*publication, *state.Record, error) {
	r, err := s.db.CurrentArchive(kind, streamID(agent, key))
	if r == nil || err != nil {
		return nil, r, err
	}
	p, err := decodePublication(r.Body)
	if err == nil {
		s.confirmBaseline(p, r)
	}
	return p, r, err
}

func (s *Syncer) readRecord(ctx context.Context, key string) ([]byte, error) {
	r, err := s.bucket.OpenReader(ctx, key)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }() // read-only; read errors are reported below
	body, err := io.ReadAll(io.LimitReader(r, maxRecordSize+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxRecordSize {
		return nil, errors.New("archive record: exceeds metadata limit")
	}
	return body, nil
}

// putRecord reconciles unknown acknowledgements by exact read-back. It never guesses that
// an error means absence, and immutable keys make delayed completion harmless.
func (s *Syncer) putRecord(ctx context.Context, key string, body []byte) error {
	err := s.bucket.Put(ctx, key, body, map[string]string{"sha256": raw.SHA256(body)})
	if err == nil {
		s.confirmRecord(key, body)
		return nil
	}
	held, rerr := s.readRecord(ctx, key)
	if rerr == nil && bytes.Equal(held, body) {
		s.confirmRecord(key, body)
		return nil
	}
	return errors.Join(err, rerr)
}

func verifyDependency(ctx context.Context, bucket store.Store, d dependency) error {
	object, err := bucket.Head(ctx, d.Object)
	if err != nil {
		return err
	}
	if object.Size != d.Size {
		return fmt.Errorf("archive dependency %s: encoded size mismatch", d.Object)
	}
	reader, err := bucket.OpenReader(ctx, d.Object)
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close() }() // read-only; io.Copy reports transport errors
	return verifyDependencyContent(reader, d)
}

func verifyDependencyContent(reader io.Reader, d dependency) error {
	encoded := sha256.New()
	src := io.TeeReader(io.LimitReader(reader, d.Size+1), encoded)
	var count int64
	var err error
	if d.Encoding == "zstd" {
		decoder, err := zstd.NewReader(src, zstd.WithDecoderConcurrency(1), zstd.WithDecoderLowmem(true), zstd.WithDecoderMaxMemory(128<<20))
		if err != nil {
			return err
		}
		defer decoder.Close()
		decoded := sha256.New()
		count, err = io.Copy(decoded, io.LimitReader(decoder, d.RawSize+1))
		if err != nil {
			return err
		}
		if count != d.RawSize || hex.EncodeToString(decoded.Sum(nil)) != d.RawSHA256 {
			return fmt.Errorf("archive dependency %s: raw checksum or size mismatch", d.Object)
		}
		// Drain the compressed reader too: concatenated/trailing data must affect its checksum.
		if _, err := io.Copy(io.Discard, src); err != nil {
			return err
		}
	} else {
		count, err = io.Copy(io.Discard, src)
		if err != nil {
			return err
		}
		if count != d.Size {
			return fmt.Errorf("archive dependency %s: size mismatch", d.Object)
		}
	}
	if hex.EncodeToString(encoded.Sum(nil)) != d.SHA256 {
		return fmt.Errorf("archive dependency %s: encoded checksum mismatch", d.Object)
	}
	return nil
}

func canonicalPublication(p *publication) ([]byte, string, error) {
	body, err := jsonx.SortedCompact(p)
	if err != nil {
		return nil, "", err
	}
	if len(body) > maxRecordSize {
		return nil, "", errors.New("archive: publication exceeds metadata limit")
	}
	return body, raw.SHA256(body), nil
}

// Index reconstruction validates immutable dependency identity via HEAD. Transcript bodies
// are streamed only by explicit audit/restore or ambiguous write reconciliation.
func verifyDependencyMetadata(ctx context.Context, bucket store.Store, d dependency) error {
	object, err := bucket.Head(ctx, d.Object)
	if err != nil {
		return err
	}
	return validateDependencyMetadata(d, object)
}

func validateDependencyMetadata(d dependency, object store.Object) error {
	if object.Size != d.Size {
		return fmt.Errorf("archive dependency %s: size mismatch", d.Object)
	}
	encoded := object.Metadata["encoded_sha256"]
	if d.Encoding == "plain" {
		encoded = object.Metadata["sha256"]
	}
	if local := object.Metadata["content_sha256"]; local != "" {
		encoded = local
	}
	if encoded == "" || encoded != d.SHA256 {
		return fmt.Errorf("archive dependency %s: checksum identity mismatch", d.Object)
	}
	if d.Encoding == "zstd" && object.Metadata["sha256"] != "" && object.Metadata["sha256"] != d.RawSHA256 {
		return fmt.Errorf("archive dependency %s: raw checksum identity mismatch", d.Object)
	}
	return nil
}

func (p *publication) validateFile(machine string) error {
	g := p.File
	if err := g.Validate(machine); err != nil {
		return err
	}
	if p.Dependencies != nil && len(p.Dependencies) != len(g.Pieces) {
		return errors.New("archive: file dependencies do not cover pieces")
	}
	for i, piece := range g.Pieces {
		if len(p.Dependencies) > i && p.Dependencies[i] != pieceDependency(piece) {
			return errors.New("archive: file dependency mismatch")
		}
	}
	if g.Identity.Agent != p.Agent || g.Identity.Key != p.Key || g.Sequence != p.Sequence || g.Parent != p.Parent {
		return errors.New("archive: file identity mismatch")
	}
	return nil
}

func validTime(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 }
