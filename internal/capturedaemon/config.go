// Package capturedaemon implements opt-in experimental service configuration with explicit local or R2 destinations.
// It never discovers credentials or changes the installed legacy hooks.
package capturedaemon

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/mich-murphy/memory/internal/capturebackend"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

const (
	ConfigVersion  = "capture-runtime/v1"
	maxConfigBytes = 64 << 10
)

type Root struct {
	Agent string `json:"agent"`
	Path  string `json:"path"`
}
type Config struct {
	Version             string               `json:"version"`
	ArchiveID           string               `json:"archive_id"`
	MachineID           string               `json:"machine_id"`
	Store               string               `json:"store,omitempty"`
	Backend             *capturebackend.Spec `json:"backend,omitempty"`
	Spool               string               `json:"spool"`
	PolicyDir           string               `json:"policy_dir"`
	Home                string               `json:"home"`
	DefaultRoots        bool                 `json:"default_roots"`
	Enrichment          bool                 `json:"enrichment,omitempty"`
	EnrichmentRoots     []string             `json:"enrichment_roots,omitempty"`
	Roots               []Root               `json:"roots"`
	RootAliases         []RootAlias          `json:"root_aliases,omitempty"`
	LeafAliases         []LeafAlias          `json:"leaf_aliases,omitempty"`
	CaptureWorkers      int                  `json:"capture_workers"`
	DeliveryWorkers     int                  `json:"delivery_workers"`
	PollSeconds         int                  `json:"poll_seconds"`
	OperationSeconds    int                  `json:"operation_seconds"`
	MaxAttempts         int                  `json:"max_attempts"`
	MaxInventoryRecords int                  `json:"max_inventory_records,omitempty"`
	MaxSpoolBytes       int64                `json:"max_spool_bytes"`
	MinFreeBytes        int64                `json:"min_free_bytes"`
	EntriesPerRoot      int                  `json:"entries_per_root"`
	sourcePath          string
}

// Load rejects unknown fields, trailing values, oversized input and unsafe paths.
// Diagnostics deliberately never include configuration contents.
func Load(path string) (Config, error) {
	var c Config
	if err := SafePath(path); err != nil {
		return c, err
	}
	st, err := os.Lstat(path)
	if err != nil {
		return c, errors.New("capture config unavailable")
	}
	if !st.Mode().IsRegular() || st.Mode().Perm()&0o077 != 0 {
		return c, errors.New("capture config must be a private regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return c, errors.New("capture config unavailable")
	}
	body, err := io.ReadAll(io.LimitReader(f, maxConfigBytes+1))
	err = errors.Join(err, f.Close())
	if err != nil || len(body) > maxConfigBytes {
		return c, errors.New("capture config unreadable or oversized")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&c); err != nil {
		return Config{}, errors.New("invalid capture config JSON")
	}
	if err = dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return Config{}, errors.New("capture config has trailing JSON")
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	for _, p := range c.LocalProtectedPaths() {
		if Overlap(path, p) {
			return Config{}, errors.New("capture config overlaps storage or policy")
		}
	}
	c.sourcePath = path
	return c, nil
}

// ConfigPath is the protected source of a loaded configuration. It is never
// serialized or accepted from JSON; programmatically constructed configs have none.
func (c Config) ConfigPath() string { return c.sourcePath }

func (c Config) Validate() error {
	if c.Version != ConfigVersion || !validID(c.ArchiveID) || !validID(c.MachineID) {
		return errors.New("capture config version or identity invalid")
	}
	if err := c.validateStorage(); err != nil {
		return err
	}
	if err := c.validateInventory(); err != nil {
		return err
	}
	if err := c.validateEnrichmentRoots(); err != nil {
		return err
	}
	if err := c.validateBudgets(); err != nil {
		return err
	}
	return c.validatePolicy()
}

func (c Config) validateStorage() error {
	if err := c.validateBackend(); err != nil {
		return err
	}
	for _, p := range append(c.LocalProtectedPaths(), c.Home, c.Spool, c.PolicyDir) {
		if err := SafePath(p); err != nil {
			return err
		}
	}
	stores := c.LocalProtectedPaths()
	for i, p := range stores {
		for _, q := range stores[i+1:] {
			if Overlap(p, q) {
				return errors.New("capture storage and policy paths overlap")
			}
		}
	}
	return nil
}

// SourceRoots returns the configured inventory, without environment root discovery.
// The returned slice is independent of Config and includes explicit default roots.
func (c Config) SourceRoots() []Root {
	roots := append([]Root(nil), c.Roots...)
	if c.DefaultRoots {
		for _, agent := range []string{"claude", "codex", "pi"} {
			p := filepath.Join(c.Home, "."+agent)
			if agent == "pi" {
				p = filepath.Join(p, "agent")
			}
			roots = append(roots, Root{agent, p})
		}
	}
	return roots
}

func (c Config) validateInventory() error {
	if len(c.RootAliases) != 0 || len(c.LeafAliases) != 0 {
		return c.validateAliases()
	}
	roots := c.SourceRoots()
	if len(roots) > 128 {
		return errors.New("capture source inventory exceeds root limit")
	}
	for _, r := range roots {
		if r.Agent != "claude" && r.Agent != "codex" && r.Agent != "pi" {
			return errors.New("capture root agent invalid")
		}
		if err := SafePath(r.Path); err != nil {
			return err
		}
		for _, p := range c.LocalProtectedPaths() {
			if Overlap(r.Path, p) {
				return errors.New("capture root overlaps storage or policy")
			}
		}
	}
	return nil
}

func (c Config) validateBudgets() error {
	bounds := [][3]int64{
		{int64(c.CaptureWorkers), 1, 32},
		{int64(c.DeliveryWorkers), 1, 32},
		{int64(c.PollSeconds), 1, 3600},
		{int64(c.OperationSeconds), 1, 3600},
		{int64(c.MaxAttempts), 1, 100},
		{c.MaxSpoolBytes, 1, 1 << 50},
		{c.MinFreeBytes, 0, 1 << 50},
		{int64(c.EntriesPerRoot), 1, 4096},
		{int64(c.MaxInventoryRecords), 0, sessioncapture.MaxInventoryRecords},
	}
	for _, b := range bounds {
		if b[0] < b[1] || b[0] > b[2] {
			return errors.New("capture worker, budget or quota out of bounds")
		}
	}
	return nil
}

func (c Config) validatePolicy() error {
	for _, p := range []string{c.PolicyDir, filepath.Join(c.PolicyDir, "exclusions")} {
		st, err := os.Stat(p)
		if err != nil || !st.IsDir() {
			return errors.New("effective exclusions unavailable")
		}
		if err := SafePath(p); err != nil {
			return err
		}
	}
	return nil
}

// AllowsSource requires the configured source inventory, including defaults.
func (c Config) AllowsSource(agent, path string) bool {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return false
	}
	if len(c.RootAliases) != 0 || len(c.LeafAliases) != 0 {
		inventory, err := c.CoordinateInventory()
		if err != nil {
			return false
		}
		if _, err := inventory.Coordinate(agent, path); err == nil {
			return true
		}
	}
	for _, r := range c.SourceRoots() {
		if r.Agent == agent && (path == r.Path || strings.HasPrefix(path, r.Path+string(filepath.Separator))) {
			return true
		}
	}
	return c.DefaultRoots && agent == "claude" && path == filepath.Join(c.Home, ".claude.json")
}

func validID(s string) bool {
	return s != "" && len(s) <= 256 && utf8.ValidString(s) && !strings.ContainsAny(s, "\x00\r\n")
}

func Overlap(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+string(filepath.Separator)) || strings.HasPrefix(b, a+string(filepath.Separator))
}

// SafePath rejects symlinks at every existing component. Ancestors must remain
// trusted; this is not a defense against hostile concurrent ancestor replacement.
func SafePath(p string) error {
	if !filepath.IsAbs(p) || filepath.Clean(p) != p || strings.ContainsAny(p, "\x00\r\n") || p == string(filepath.Separator) {
		return errors.New("capture paths must be clean absolute non-root paths")
	}
	for cur := p; cur != filepath.Dir(cur); cur = filepath.Dir(cur) {
		st, err := os.Lstat(cur)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || st.Mode()&os.ModeSymlink != 0 {
			return errors.New("capture path unavailable or symlinked")
		}
	}
	return nil
}

// WritePrivate atomically replaces a private regular file and checks both data
// and directory fsync. It refuses existing symlinks and nonprivate destinations.
func WritePrivate(path string, body []byte) error {
	if err := SafePath(path); err != nil {
		return err
	}
	if st, e := os.Lstat(path); e == nil {
		if !st.Mode().IsRegular() || st.Mode().Perm()&0o077 != 0 {
			return errors.New("unsafe private file destination")
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	st, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if st.Mode().Perm()&0o077 != 0 {
		return errors.New("private file directory must be private")
	}
	f, err := os.CreateTemp(dir, ".capture-")
	if err != nil {
		return err
	}
	defer func() { _ = f.Close(); _ = os.Remove(f.Name()) }()
	if _, err := f.Write(body); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	return syncParents(dir)
}

// Fence every directory entry MkdirAll may have created, including visible
// ancestors left by a previous attempt whose directory sync failed.
func syncParents(dir string) error {
	for {
		d, err := os.Open(dir)
		if err != nil {
			return err
		}
		if err := errors.Join(d.Sync(), d.Close()); err != nil {
			return err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil
		}
		dir = parent
	}
}

func Save(path string, c Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	for _, p := range c.LocalProtectedPaths() {
		if Overlap(path, p) {
			return errors.New("capture config overlaps storage or policy")
		}
	}
	body, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return WritePrivate(path, append(body, '\n'))
}
