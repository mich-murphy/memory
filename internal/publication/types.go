// Package publication implements the session-publication/v2 writer and authoritative reader.
package publication

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
)

const (
	Contract       = "session-publication/v2"
	ReaderContract = "session-reader/v2"
	Prefix         = "capture-v1"
	ArchiveKey     = Prefix + "/archive.json"
	MaxMetadata    = 8 << 20
	MaxSources     = 1024
)

var (
	// ErrDurability cannot be reconciled by visible read-back. Retry must sync again.
	ErrDurability        = errors.New("publication durability failure")
	ErrDestinationPaused = errors.New("publication destination paused")
	ErrConflict          = errors.New("publication conflict")
	ErrNotFound          = errors.New("publication object not found")
)

type Backend interface {
	Open(context.Context, string) (io.ReadCloser, error)
	PutFile(context.Context, string, string) error
	Current(context.Context, string) ([]byte, string, error)
	CompareAndSwap(context.Context, string, string, []byte) error
}

// DurabilityFence is implemented by backends where visibility alone does not
// confirm durability. Wrappers around such backends must forward this interface.
// Sync must durably flush the object and all required directory entries.
// Backends without this interface promise durable visibility after successful writes.
type DurabilityFence interface {
	Sync(context.Context, string) error
}

type Identity struct {
	ArchiveID string `json:"archive_id"`
	MachineID string `json:"machine_id"`
	Agent     string `json:"agent"`
	SessionID string `json:"session_id"`
}

// Hex encoding is injective for UTF-8 identities and never creates path separators.
func (i Identity) BaseKey() string {
	enc := func(s string) string { return hex.EncodeToString([]byte(s)) }
	return Prefix + "/archives/" + enc(i.ArchiveID) + "/machines/" + enc(i.MachineID) + "/agents/" + enc(i.Agent) + "/sessions/" + enc(i.SessionID)
}
func (i Identity) CurrentKey() string { return i.BaseKey() + "/current.json" }

type Archive struct {
	Contract  string `json:"contract"`
	ArchiveID string `json:"archive_id"`
}
type Manifest struct {
	Contract      string   `json:"contract"`
	Identity      Identity `json:"identity"`
	PublicationID string   `json:"publication_id"`
	Sequence      uint64   `json:"sequence"`
	ObservedAtNS  int64    `json:"observed_at_ns"`
	CapturedAtNS  int64    `json:"captured_at_ns"`
	PublishedAtNS int64    `json:"published_at_ns"`
	Coverage      Coverage `json:"coverage"`
	Sources       []Source `json:"sources"`
}
type Coverage struct {
	Discovery  string   `json:"discovery"`
	Enrichment string   `json:"enrichment"`
	Missing    []string `json:"missing"`
}
type Source struct {
	SourceID     string          `json:"source_id"`
	EpochID      string          `json:"epoch_id"`
	Role         string          `json:"role"`
	Path         string          `json:"path"`
	State        string          `json:"state"`
	Revision     uint64          `json:"revision"`
	ObservedAtNS int64           `json:"observed_at_ns"`
	CapturedAtNS int64           `json:"captured_at_ns"`
	Object       Object          `json:"object,omitzero"`
	Optional     bool            `json:"optional,omitempty"`
	Withheld     *WithheldSource `json:"withheld,omitempty"`
	Associations []string        `json:"associations"`
}

// WithheldSource retains content-free identity and commitment, never a descriptor.
type WithheldSource struct {
	SourceIDHash       string `json:"source_id_hash"`
	SnapshotCommitment string `json:"snapshot_commitment"`
	Reason             string `json:"reason"`
}

// MissingSourceWithheld marks explicitly incomplete enrichment coverage.
const MissingSourceWithheld = "source-withheld"

type Object struct {
	Key           string `json:"key"`
	Encoding      string `json:"encoding"`
	SHA256        string `json:"sha256"`
	EncodedSHA256 string `json:"encoded_sha256"`
	Size          int64  `json:"size"`
	EncodedSize   int64  `json:"encoded_size"`
}
type Current struct {
	Contract       string   `json:"contract"`
	Identity       Identity `json:"identity"`
	PublicationID  string   `json:"publication_id"`
	ManifestKey    string   `json:"manifest_key"`
	ManifestSHA256 string   `json:"manifest_sha256"`
	Sequence       uint64   `json:"sequence"`
}
type Export struct {
	Contract string   `json:"contract"`
	Manifest Manifest `json:"manifest"`
	Bodies   []Body   `json:"bodies"`
}
type Body struct {
	SourceID string `json:"source_id"`
	EpochID  string `json:"epoch_id"`
	Path     string `json:"path"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
}
