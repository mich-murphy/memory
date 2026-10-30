package capturedaemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mich-murphy/memory/internal/capturebackend"
)

func remoteSpec() *capturebackend.Spec {
	return &capturebackend.Spec{Kind: "r2", Endpoint: "https://" + strings.Repeat("a", 32) + ".r2.cloudflarestorage.com", Bucket: "synthetic-bucket", AccessKeyEnv: "SYNTHETIC_ACCESS", SecretKeyEnv: "SYNTHETIC_SECRET"}
}

func TestBackendConfigOfflineRoundTrip(t *testing.T) {
	for _, kind := range []string{"legacy", "local", "r2"} {
		t.Run(kind, func(t *testing.T) {
			c := testConfig(t)
			local := c.Store
			if kind != "legacy" {
				c.Store = ""
				if kind == "local" {
					c.Backend = &capturebackend.Spec{Kind: "local", LocalDir: local}
				} else {
					c.Backend = remoteSpec()
				}
			}
			if err := c.Validate(); err != nil {
				t.Fatal(err)
			}
			paths := c.LocalProtectedPaths()
			want := []string{c.Spool, c.PolicyDir}
			if kind != "r2" {
				want = append(want, local)
			}
			if !reflect.DeepEqual(paths, want) {
				t.Fatalf("protected paths: %v", paths)
			}
			path := filepath.Join(c.Home, "runtime.json")
			if err := Save(path, c); err != nil {
				t.Fatal(err)
			}
			got, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if got.BackendSpec() != c.BackendSpec() || got.ConfigPath() != path {
				t.Fatal("backend changed")
			}
			if _, err := os.Stat(c.Spool); !os.IsNotExist(err) {
				t.Fatal("configuration created spool")
			}
			if _, err := os.Stat(local); !os.IsNotExist(err) {
				t.Fatal("configuration created local store")
			}
		})
	}
}

func TestBackendConfigRejectsAmbiguityAndActualOverlaps(t *testing.T) {
	for _, name := range []string{"both", "missing", "unknown", "unused", "storage", "inventory", "enrichment", "config", "home", "empty-spool", "empty-policy"} {
		t.Run(name, func(t *testing.T) {
			c := testConfig(t)
			local := c.Store
			c.Store = ""
			c.Backend = remoteSpec()
			switch name {
			case "both":
				c.Store = local
			case "missing":
				c.Backend = nil
			case "unknown":
				c.Backend = &capturebackend.Spec{Kind: "unknown"}
			case "unused":
				c.Backend.LocalDir = local
			case "storage":
				c.Backend = &capturebackend.Spec{Kind: "local", LocalDir: c.Spool}
			case "inventory":
				c.Roots = []Root{{Agent: "pi", Path: c.Spool}}
			case "enrichment":
				c.EnrichmentRoots = []string{c.PolicyDir}
			case "config":
				if Save(filepath.Join(c.PolicyDir, "runtime.json"), c) == nil {
					t.Fatal("config overlap accepted")
				}
				return
			case "home":
				c.Home = ""
			case "empty-spool":
				c.Spool = ""
			case "empty-policy":
				c.PolicyDir = ""
			}
			if c.Validate() == nil {
				t.Fatal("invalid backend config accepted")
			}
		})
	}
	c := testConfig(t)
	c.Store = ""
	c.Backend = remoteSpec()
	body, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	body = []byte(strings.Replace(string(body), `"kind":"r2"`, `"kind":"r2","unknown":"private"`, 1))
	path := filepath.Join(c.Home, "bad.json")
	if err := WritePrivate(path, body); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("unknown backend JSON accepted")
	}
}
