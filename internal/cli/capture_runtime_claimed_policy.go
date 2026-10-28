package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/capturecoordinates"
	"github.com/mich-murphy/memory/internal/exclusion"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

// A claimed identity cannot authorize bytes, but naming a known opted-out session
// must restrict publication even when the hint's independently owned path is healthy.
// Only preserved records and compiled declarations supply namespace equivalence.
func (r runtimeComposition) claimedSessionExcluded(req sessioncapture.Request, registry exclusion.Registry) (bool, error) {
	c := r.config
	if !strings.HasPrefix(req.Identity.SessionID, "session/") {
		return false, nil
	}
	inventory := r.inventory
	adapter := adapters.All(c.Home)[req.Identity.Agent]
	for _, record := range registry {
		if record.Agent != req.Identity.Agent {
			continue
		}
		if sessioncapture.OwnershipSession(record.Root, req.Identity.SessionID, record.SessionKey) {
			return true, nil
		}
		if adapter == nil {
			continue
		}
		matched, err := runtimeClaimedRecordAliases(inventory, adapter, req.Identity.SessionID, record)
		if err != nil || matched {
			return matched, err
		}
	}
	return false, nil
}

func runtimeClaimedRecordAliases(inventory *capturecoordinates.Inventory, adapter adapters.Adapter, session string, record exclusion.Record) (bool, error) {
	// Include every original spelling, not just the first currently available path.
	for _, path := range record.Paths {
		coordinate, err := inventory.Coordinate(record.Agent, path)
		if err != nil {
			continue
		}
		positions, err := inventory.PolicyCoordinates(coordinate)
		if err != nil {
			return false, err
		}
		for _, position := range positions {
			sf, ok := adapters.ClassifyLogical(adapter, position.LogicalPath, position.Root)
			if ok && sessioncapture.OwnershipSession(sf.Root, session, sf.SessionKey) {
				return true, nil
			}
		}
	}
	// A preserved root declaration remains useful even without a classifiable path
	// (or when its original physical directory has disappeared).
	coordinate, err := inventory.Coordinate(record.Agent, record.Root)
	if err != nil {
		// An unrelated preserved root has no declared alias correspondence.
		return false, nil //nolint:nilerr // An inventory miss is expected, not a policy failure.
	}
	positions, err := inventory.PolicyCoordinates(coordinate)
	if err != nil {
		return false, err
	}
	for _, position := range positions {
		key := runtimeClaimedLegacyKey(adapter, record.Root, position.Root, record.SessionKey)
		if sessioncapture.OwnershipSession(position.Root, session, key) {
			return true, nil
		}
	}
	return false, nil
}

// Translate only the legacy root prefix; keep the complete session name, not a
// UUID suffix. The destination root is already proven equivalent by inventory.
func runtimeClaimedLegacyKey(adapter adapters.Adapter, oldRoot, root, key string) string {
	if oldRoot == root {
		return key
	}
	oldSum := sha256.Sum256([]byte(oldRoot))
	flavor := "roots/"
	for _, prefix := range []string{"roots/", "dirs/"} {
		if local, ok := strings.CutPrefix(key, prefix+hex.EncodeToString(oldSum[:])[:10]+"/"); ok {
			key, flavor = local, prefix
			break
		}
	}
	if root == adapter.CanonicalRoot() {
		return key
	}
	sum := sha256.Sum256([]byte(root))
	return flavor + hex.EncodeToString(sum[:])[:10] + "/" + key
}
