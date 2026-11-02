//go:build darwin || linux

package capturesource

import (
	"os"
	"path/filepath"
	"testing"
)

func transformedFingerprints(t *testing.T, o Options) map[string]string {
	t.Helper()
	out := map[string]string{}
	reqs := discover(t, o).Requests
	for i := range reqs {
		q := &reqs[i]
		if q.Transform != "" {
			out[q.Transform+":"+filepath.Base(q.Path)] = q.Fingerprint
		}
	}
	return out
}

func appendFile(t *testing.T, p, s string) {
	t.Helper()
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestTransformedFingerprintsTrackEveryInput(t *testing.T) {
	o := options(t)
	o.Derived = true
	root := filepath.Join(o.Home, ".codex")
	db := fixture(t, filepath.Join(root, "state.sqlite"), "SQLite format 3\x00")
	fixture(t, filepath.Join(root, "config.toml"), "model = \"x\"\n")
	base := transformedFingerprints(t, o)
	for _, k := range []string{"sqlite-backup:state.sqlite", "scrubbed-config:config.toml"} {
		if base[k] == "" {
			t.Fatalf("%s has no fingerprint: %v", k, base)
		}
	}
	if again := transformedFingerprints(t, o); again["sqlite-backup:state.sqlite"] != base["sqlite-backup:state.sqlite"] || again["scrubbed-config:config.toml"] != base["scrubbed-config:config.toml"] {
		t.Fatal("unchanged sources must keep their fingerprints")
	}
	// Creating a -wal, growing it, creating a -shm: each must change the db fingerprint.
	seen := map[string]string{"": base["sqlite-backup:state.sqlite"]}
	for _, step := range []struct{ name, path, body string }{
		{"wal created", db + "-wal", "w"},
		{"wal grew", db + "-wal", "w"},
		{"shm created", db + "-shm", "s"},
		{"shm changed", db + "-shm", "s"},
	} {
		appendFile(t, step.path, step.body)
		got := transformedFingerprints(t, o)["sqlite-backup:state.sqlite"]
		for name, old := range seen {
			if got == old {
				t.Fatalf("%s: fingerprint equals the one after %q", step.name, name)
			}
		}
		seen[step.name] = got
	}
	appendFile(t, filepath.Join(root, "config.toml"), "extra = 1\n")
	if got := transformedFingerprints(t, o)["scrubbed-config:config.toml"]; got == base["scrubbed-config:config.toml"] || got == "" {
		t.Fatalf("config change not reflected: %q", got)
	}
}

func TestTransformFingerprintPolicyVersionAndUnknownInputs(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.sqlite")
	fixture(t, p, "SQLite format 3\x00")
	info, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	v1 := transformFingerprint("v1", "sqlite-backup", p, info)
	if v1 == "" || v1 == transformFingerprint("v2", "sqlite-backup", p, info) {
		t.Fatal("policy version must change the fingerprint")
	}
	if v1 == transformFingerprint("v1", "zstd", p, info) {
		t.Fatal("transform kind must change the fingerprint")
	}
	if got := transformFingerprint("v1", "sqlite-backup", p, fingerprintFileInfo{FileInfo: info}); got != "" {
		t.Fatalf("unknown stat must yield empty fingerprint, got %q", got)
	}
	// A sidecar that cannot be inspected makes the input unknown.
	if err := os.Mkdir(p+"-wal", 0o000); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(p), 0o000); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(filepath.Dir(p), 0o700) }()
	if got := transformFingerprint("v1", "sqlite-backup", p, info); got != "" && os.Getuid() != 0 {
		t.Fatalf("unreadable sidecar must yield empty fingerprint, got %q", got)
	}
}
