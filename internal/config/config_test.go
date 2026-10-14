package config

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/mich-murphy/memory/internal/testenv"
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

func TestRunsStartingTogetherShareOneMachineID(t *testing.T) {
	for range 20 {
		dir := t.TempDir()
		ids := make([]string, 8)
		var wg sync.WaitGroup
		for i := range ids {
			wg.Go(func() {
				var err error
				if ids[i], err = MachineUUID(dir); err != nil {
					t.Error(err)
				}
			})
		}
		wg.Wait()
		if len(slices.Compact(slices.Clone(ids))) != 1 {
			t.Fatalf("machine ids %v", ids)
		}
	}
}

func TestAnEmptyMachineIDFileGetsAnIDThatLasts(t *testing.T) {
	dir := t.TempDir()
	testenv.Write(t, filepath.Join(dir, "machine-id"), "")
	first, err := MachineUUID(dir)
	if err != nil {
		t.Fatal(err)
	}
	again, err := MachineUUID(dir)
	if err != nil || first != again || !uuidRE.MatchString(first) {
		t.Errorf("machine ids %q then %q, %v", first, again, err)
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
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.R2Endpoint != "https://x.r2.example" || cfg.R2Bucket != "sessions" || cfg.R2AccessKeyID != "from-env" {
		t.Errorf("cfg = %+v", cfg)
	}
	if want := filepath.Join(dir, ".local", "state", "agent-sessions"); cfg.StateDir != want || cfg.Home != dir {
		t.Errorf("StateDir = %s, Home = %s, want %s under %s", cfg.StateDir, cfg.Home, want, dir)
	}
	if want, err := os.Hostname(); err != nil || cfg.Hostname != want {
		t.Errorf("Hostname = %q, want %q (%v)", cfg.Hostname, want, err)
	}
	if got := cfg.MissingR2Settings(); !slices.Equal(got, []string{"R2_SECRET_ACCESS_KEY"}) {
		t.Errorf("missing = %v", got)
	}
}

func TestMachineIDOverrideIsSafeInAnObjectKey(t *testing.T) {
	clearEnv(t)
	for in, want := range map[string]string{"My Laptop!": "My-Laptop", "a b/c": "a-b-c", "---": "unknown", "ok.name_1": "ok.name_1"} {
		t.Setenv("AGENT_SESSIONS_MACHINE_ID", in)
		cfg, err := Load("")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.MachineID != want {
			t.Errorf("machine id %q = %q, want %q", in, cfg.MachineID, want)
		}
	}
}

func TestEnvFileValues(t *testing.T) {
	for _, c := range []struct{ name, body, want string }{
		{"plain", "R2_BUCKET=sessions\n", "sessions"},
		{"padded", "  R2_BUCKET = sessions  \n", "sessions"},
		{"doubleQuoted", `R2_BUCKET="sessions"`, "sessions"},
		{"singleQuotedExport", "export R2_BUCKET='sessions'\n", "sessions"},
		{"empty", "R2_BUCKET=\n", ""},
		{"emptyQuotes", `R2_BUCKET=""`, ""},
		{"unmatchedQuote", `R2_BUCKET="sessions`, `"sessions`},
		{"afterJunk", "# settings\nnot a setting\n\nR2_BUCKET=sessions\n", "sessions"},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := clearEnv(t)
			path := filepath.Join(dir, "config.env")
			testenv.Write(t, path, c.body)
			cfg, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.R2Bucket != c.want {
				t.Errorf("R2Bucket = %q, want %q", cfg.R2Bucket, c.want)
			}
		})
	}
}
