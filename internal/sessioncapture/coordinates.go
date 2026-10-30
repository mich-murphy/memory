package sessioncapture

import (
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/mich-murphy/memory/internal/capturepolicy"
)

const (
	MaxRequestIdentities    = 128
	MaxRequestIdentityBytes = 64 << 10
	MaxPolicyIdentities     = 32768
	MaxPolicyIdentityBytes  = 2 << 20
)

func cleanIdentity(path string) bool {
	return len(path) <= 4096 && utf8.ValidString(path) && filepath.IsAbs(path) && filepath.Clean(path) == path && !strings.ContainsAny(path, "\x00\r\n")
}

// Additional identities can only restrict privacy. They never choose bytes,
// classification roots, source IDs, epochs or ownership claims.
func validateRequestProvenance(r Request) error {
	if r.LogicalPath != "" && !cleanIdentity(r.LogicalPath) {
		return ErrPolicy
	}
	if len(r.Identities) > MaxRequestIdentities {
		return ErrQuota
	}
	size := len(r.LogicalPath)
	for _, path := range r.Identities {
		if !cleanIdentity(path) {
			return ErrPolicy
		}
		size += len(path)
	}
	if size > MaxRequestIdentityBytes {
		return ErrQuota
	}
	return nil
}

func boundPolicyIdentities(ids []string) error {
	if len(ids) > MaxPolicyIdentities {
		return ErrQuota
	}
	size := 0
	for _, path := range ids {
		size += len(path)
		if size > MaxPolicyIdentityBytes {
			return ErrQuota
		}
	}
	return nil
}

// Projection is pure inventory authority. Filesystem resolution contributes only
// restrictive identities, including partial resolutions of absent older routes.
func (q *Queue) requestCoordinates(r Request) (string, []string, error) {
	if err := validateRequestProvenance(r); err != nil {
		return "", nil, err
	}
	logical := r.Path
	ids := unionIdentities([]string{r.Path}, r.Identities)
	if q.opts.Coordinates != nil {
		c, err := q.opts.Coordinates.Coordinate(r.Identity.Agent, r.Path)
		if err == nil {
			logical = c.LogicalPath
			for _, path := range q.opts.Coordinates.EquivalentPaths(c) {
				ids = unionIdentities(ids, []string{path})
				resolved, _ := capturepolicy.Resolve(path) // Never used as read/ownership authority.
				ids = unionIdentities(ids, resolved.Identities)
				if err := boundPolicyIdentities(ids); err != nil {
					return "", nil, err
				}
			}
		}
	}
	if r.LogicalPath != "" && r.LogicalPath != logical {
		return "", nil, ErrPolicy
	}
	if err := boundPolicyIdentities(ids); err != nil {
		return "", nil, err
	}
	return logical, ids, nil
}

func publicationSourcePath(r Request) string {
	if r.LogicalPath != "" {
		return r.LogicalPath
	}
	return r.Path
}

// Structural claims must belong to a declared inventory root when configured.
// Private/unmapped enrichment requests never enter the main ownership index.
func (q *Queue) ownershipLogicalPath(r Request) (string, error) {
	if q.opts.Coordinates != nil {
		coordinate, err := q.opts.Coordinates.Coordinate(r.Identity.Agent, r.Path)
		if err != nil || coordinate.Root != singleOwnershipAssociation(r.Associations, "root:") {
			return "", ErrPolicy
		}
	}
	return publicationSourcePath(r), nil
}
