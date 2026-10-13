package config

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var uuidRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func clearEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, k := range []string{
		"AGENT_SESSIONS_MACHINE_ID", "AGENT_SESSIONS_STATE_DIR", "AGENT_SESSIONS_LOCAL_STORE",
		"R2_ENDPOINT", "R2_BUCKET", "R2_ACCESS_KEY_ID", "R2_SECRET_ACCESS_KEY",
	} {
		t.Setenv(k, "")
	}
	t.Setenv("HOME", dir)
	t.Setenv("AGENT_SESSIONS_CONFIG", filepath.Join(dir, "cfg", "config.env"))
	return dir
}

func TestMachineIDIsAPersistentUUID(t *testing.T) {
	dir := clearEnv(t)
	first, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if !uuidRE.MatchString(first.MachineID) {
		t.Errorf("machine id %q is not a v4 UUID", first.MachineID)
	}
	again, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if again.MachineID != first.MachineID {
		t.Errorf("machine id changed: %s then %s", first.MachineID, again.MachineID)
	}
	data, err := os.ReadFile(filepath.Join(dir, "cfg", "machine-id"))
	if err != nil || strings.TrimSpace(string(data)) != first.MachineID {
		t.Errorf("machine-id file = %q, %v", data, err)
	}
}

func TestEnvFileAndEnvironment(t *testing.T) {
	dir := clearEnv(t)
	path := filepath.Join(dir, "cfg", "config.env")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	body := "# comment\nexport R2_ENDPOINT=\"https://x.r2.example\"\nR2_BUCKET='sessions'\nnot a setting\n" +
		"R2_ACCESS_KEY_ID = from-file\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("R2_ACCESS_KEY_ID", "from-env")
	t.Setenv("AGENT_SESSIONS_MACHINE_ID", "My Laptop!")
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.R2Endpoint != "https://x.r2.example" || cfg.R2Bucket != "sessions" || cfg.R2AccessKeyID != "from-env" {
		t.Errorf("cfg = %+v", cfg)
	}
	if cfg.MachineID != "My-Laptop" || cfg.StateDir != DefaultStateDir() {
		t.Errorf("cfg = %+v", cfg)
	}
	if got := cfg.MissingR2Settings(); !slices.Equal(got, []string{"R2_SECRET_ACCESS_KEY"}) {
		t.Errorf("missing = %v", got)
	}
}

func TestSanitize(t *testing.T) {
	for in, want := range map[string]string{"a b/c": "a-b-c", "---": "unknown", "ok.name_1": "ok.name_1"} {
		if got := sanitize(in); got != want {
			t.Errorf("sanitize(%q) = %q", in, got)
		}
	}
}
