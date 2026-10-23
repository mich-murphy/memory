package capturepolicy

import (
	"path/filepath"
	"slices"
	"strings"
)

// Snapshot imports and explicit custom roots can leave the usual agent tree.
// These credential locations must remain forbidden even through path aliases;
// callers apply this predicate to every resolved identity, not just the leaf.
func additionalCredentialPath(path string) bool {
	clean := filepath.ToSlash(filepath.Clean(path))
	name := BaseName(clean)
	if slices.Contains([]string{".git-credentials", ".netrc", ".npmrc", ".pypirc", "id_rsa", "id_dsa", "id_ecdsa", "id_ed25519", "id_ecdsa_sk", "id_ed25519_sk"}, name) {
		return true
	}
	for _, directory := range []string{"/.ssh", "/.gnupg", "/.aws", "/.azure", "/.kube", "/.config/gcloud"} {
		if strings.HasSuffix(clean, directory) || strings.Contains(clean, directory+"/") {
			return true
		}
	}
	return false
}
