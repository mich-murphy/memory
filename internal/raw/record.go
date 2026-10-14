package raw

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const Schema = 3

type Identity struct {
	Agent      string `json:"agent"`
	MachineID  string `json:"machine_id"`
	Key        string `json:"key"`
	SessionKey string `json:"session_key,omitempty"`
	Root       string `json:"root"`
}

type Source struct {
	Path       string `json:"path"`
	SourcePath string `json:"source_path,omitempty"`
	Encoding   string `json:"encoding"`
}

// Generation is the same canonical body in a discoverable plan and its immutable commit.
// Parent is a descriptor hash, not a timestamp election. Deletion retains content pieces.
type Generation struct {
	Schema       int            `json:"schema"`
	Identity     Identity       `json:"identity"`
	Sequence     uint64         `json:"sequence"`
	Parent       string         `json:"parent,omitempty"`
	Kind         string         `json:"kind"`
	Sources      []Source       `json:"sources"`
	Role         string         `json:"role"`
	Size         int64          `json:"size"`
	SHA256       string         `json:"sha256"`
	LineCount    int64          `json:"line_count"`
	PrefixSHA256 string         `json:"prefix_sha256,omitempty"`
	Head         map[string]any `json:"head,omitempty"`
	Pieces       []Piece        `json:"pieces"`
	ObservedAt   float64        `json:"observed_at"`
	DeletedAt    float64        `json:"deleted_at,omitempty"`
}

func EncodedKey(sha string) string { return "v3/blobs/zstd/" + sha + ".zst" }

func ValidHash(sha string) bool {
	if len(sha) != 64 {
		return false
	}
	b, err := hex.DecodeString(sha)
	return err == nil && len(b) == 32 && strings.ToLower(sha) == sha
}

func ValidKey(key string) bool {
	return utf8.ValidString(key) && key != "" && key != "." && key != ".." && !strings.HasPrefix(key, "/") && path.Clean(key) == key && !strings.HasPrefix(key, "../") && !strings.ContainsAny(key, "\\\x00")
}

// ValidPath reports whether an absolute filesystem identity can be preserved in JSON.
// Unix byte names outside UTF-8 are unsupported rather than silently replaced on encoding.
func ValidPath(name string) bool {
	return utf8.ValidString(name) && filepath.IsAbs(name) && !strings.ContainsRune(name, '\x00')
}

func (g *Generation) Validate(machine string) error {
	if err := g.validateIdentity(machine); err != nil {
		return err
	}
	return g.validateContent()
}

func (g *Generation) validateIdentity(machine string) error {
	for _, identity := range []string{g.Identity.MachineID, g.Identity.Key, g.Identity.SessionKey, g.Identity.Root} {
		if !utf8.ValidString(identity) {
			return errors.New("raw generation: unsupported non-UTF-8 identity or path")
		}
	}
	if g.Schema != Schema || g.Identity.MachineID != machine || !ValidKey(g.Identity.Key) || !knownAgent(g.Identity.Agent) || !ValidPath(g.Identity.Root) || !finiteTime(g.ObservedAt) || !finiteTime(g.DeletedAt) || g.Sequence == 0 || g.Size < 0 || !ValidHash(g.SHA256) {
		return errors.New("raw generation: invalid identity or content")
	}
	if g.Identity.SessionKey != "" && !ValidKey(g.Identity.SessionKey) {
		return errors.New("raw generation: invalid session key")
	}
	if (g.Sequence == 1) != (g.Parent == "") || g.Parent != "" && !ValidHash(g.Parent) {
		return errors.New("raw generation: invalid parent")
	}
	switch g.Kind {
	case "capture", "fold", "delete":
	default:
		return errors.New("raw generation: invalid kind")
	}
	if g.Kind == "delete" && g.DeletedAt <= 0 {
		return errors.New("raw generation: deletion has no observation time")
	}
	return nil
}

func (g *Generation) validateContent() error {
	if len(g.Sources) == 0 || len(g.Pieces) == 0 {
		return errors.New("raw generation: missing sources or pieces")
	}
	for _, source := range g.Sources {
		if !utf8.ValidString(source.Path) || !utf8.ValidString(source.SourcePath) {
			return errors.New("raw generation: unsupported non-UTF-8 source path")
		}
		if !ValidPath(source.Path) || source.SourcePath != "" && !ValidPath(source.SourcePath) || source.Encoding != "plain" && source.Encoding != "zstd" {
			return errors.New("raw generation: invalid source")
		}
	}
	var end int64
	for _, p := range g.Pieces {
		if err := p.validate(end, g.Size); err != nil {
			return fmt.Errorf("raw generation: invalid piece at %d", end)
		}
		end = p.End
	}
	if end != g.Size {
		return errors.New("raw generation: piece coverage differs from size")
	}
	if g.Size == 0 && g.SHA256 != SHA256(nil) {
		return errors.New("raw generation: invalid empty content hash")
	}
	return nil
}

func knownAgent(agent string) bool { return agent == "claude" || agent == "codex" || agent == "pi" }

func finiteTime(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 }

func (p Piece) validate(start, size int64) error {
	if p.Start != start || p.End < p.Start || p.End == p.Start && size != 0 || !ValidHash(p.SHA256) || !ValidHash(p.EncodedSHA256) || p.EncodedSize <= 0 || p.Object != EncodedKey(p.EncodedSHA256) {
		return errors.New("raw piece: invalid coverage or checksum")
	}
	return nil
}
