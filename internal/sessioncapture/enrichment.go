package sessioncapture

import (
	"context"
	"io"

	"github.com/mich-murphy/memory/internal/publication"
)

const EnrichmentContract = "session-enrichment/v1"

// SourceCoordinate pins work to accepted bytes, never a mutable pathname.
type SourceCoordinate struct {
	SourceID string
	Path     string
	EpochID  string
	Revision uint64
	SHA256   string
}

type EnrichmentSpec struct {
	WorkingDirectory  string `json:"-"`
	Kind              string
	Identity          publication.Identity
	BasisPublication  string
	BasisSequence     uint64
	Source            SourceCoordinate
	LaunchID          string
	Root              string
	AdapterSessionKey string
	CWD               string
	SelectionVersion  string
}

type NativeContext struct {
	Event           string
	Source          string
	CWD             string
	NativeSessionID string
	Model           string
	Provider        string
	PermissionMode  string
	AgentVersion    string
	Argv            []string
	Environment     map[string]string
	ContextOrigin   string
	Missing         []string
}

type NativeEvidence struct {
	Contract     string
	ID           string
	Identity     publication.Identity
	Request      Request
	ObservedAtNS int64
	Context      NativeContext
}

type EnrichmentAsset struct {
	Path       string
	Identities []string
	Scope      string
	Selector   string
	Kind       string
}

type EnrichmentArtifact struct {
	Body        io.ReadCloser
	Assets      []EnrichmentAsset
	Complete    bool
	Missing     []string
	ContentHash string
}

// Enrichment callbacks run outside raw source claims and session locks.
type (
	EnrichFunc               func(context.Context, EnrichmentSpec) (EnrichmentArtifact, error)
	CheckEnrichmentAssetFunc func(context.Context, EnrichmentSpec, EnrichmentAsset, bool) error
)
