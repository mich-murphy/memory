package cli

import (
	"context"
	"net/http"
	"path/filepath"

	"github.com/mich-murphy/memory/internal/capturebackend"
	"github.com/mich-murphy/memory/internal/capturefixture"
	"github.com/mich-murphy/memory/internal/publication"
)

// captureFixtureBackend never consults the process environment for SDK fixtures.
// The SDK still performs real request serialization and signing against a peer
// that cannot dial, resolve DNS, or consult host credentials.
func captureFixtureBackend(ctx context.Context, root, kind string) (publication.Backend, error) {
	if kind == "local" {
		return publication.NewLocal(filepath.Join(root, "store")), nil
	}
	spec := capturebackend.Spec{Kind: "r2", Endpoint: "https://" + capturefixture.Host, Bucket: capturefixture.Bucket, AccessKeyEnv: "FIXTURE_ACCESS", SecretKeyEnv: "FIXTURE_SECRET"}
	return capturebackend.Open(ctx, spec, capturebackend.Dependencies{
		LookupEnv: func(name string) (string, bool) {
			switch name {
			case "FIXTURE_ACCESS":
				return "synthetic-access", true
			case "FIXTURE_SECRET":
				return "synthetic-secret", true
			default:
				return "", false
			}
		},
		HTTPClient: &http.Client{Transport: &capturefixture.Peer{LostACK: true}},
	})
}
