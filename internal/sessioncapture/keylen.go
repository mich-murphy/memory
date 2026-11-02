package sessioncapture

import (
	"fmt"

	"github.com/mich-murphy/memory/internal/publication"
)

// The limit is applied whatever the backend kind, local included: a conservative choice
// so an archive written locally stays portable to R2. capturebackend.ValidateIdentity
// states the same rule for r2 only; a parity test in internal/cli keeps them in step.
//
// maxObjectKeyBytes mirrors the 1024-byte R2 key limit enforced by publicationr2.
// It is restated here rather than imported so admission needs no backend dependency.
const maxObjectKeyBytes = 1024

// longestKeySuffix is the longest key component appended to an identity's base key:
// "/manifests/" + 64 hex digest + ".json".
const longestKeySuffix = len("/manifests/") + 64 + len(".json")

// ErrKeyLength marks an identity whose derived destination keys exceed the R2 limit.
// It is deterministic (the identity never shrinks), so it classifies as quota and is
// refused at admission instead of failing every upload attempt later.
var ErrKeyLength = fmt.Errorf("%w: destination key exceeds %d bytes", ErrQuota, maxObjectKeyBytes)

func checkKeyLength(id publication.Identity) error {
	if len(id.BaseKey())+longestKeySuffix > maxObjectKeyBytes {
		return ErrKeyLength
	}
	return nil
}
