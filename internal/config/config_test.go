package config

import (
	"errors"
	"io/fs"
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
	id, err := CreateMachineID("")
	if err != nil {
		t.Fatal(err)
	}
	first, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if first.MachineID != id {
		t.Errorf("Load identity = %q, want created %q", first.MachineID, id)
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

func TestExplicitCreationsStartingTogetherShareOneMachineID(t *testing.T) {
	clearEnv(t)
	for range 20 {
		dir := t.TempDir()
		ids := make([]string, 8)
		var wg sync.WaitGroup
		for i := range ids {
			wg.Go(func() {
				var err error
				if ids[i], err = CreateMachineID(filepath.Join(dir, "config.env")); err != nil {
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

func TestMissingAndEmptyIdentityRequireOperatorChoice(t *testing.T) {
	for _, tc := range []struct {
		name   string
		body   string
		exists bool
	}{
		{name: "missing"},
		{name: "empty", exists: true},
		{name: "whitespace", body: " \n\t", exists: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := clearEnv(t)
			path := filepath.Join(dir, "cfg", "machine-id")
			if tc.exists {
				testenv.Write(t, path, tc.body)
			}
			for range 2 {
				cfg, err := Load("")
				var missing *MissingMachineIDError
				if cfg != nil || !errors.As(err, &missing) {
					t.Fatalf("Load = %v, %v, want typed missing identity error", cfg, err)
				}
				if missing.Path != path || missing.Empty != tc.exists {
					t.Errorf("missing error = %+v, want path %q, empty %v", missing, path, tc.exists)
				}
				if !strings.Contains(err.Error(), "AGENT_SESSIONS_MACHINE_ID") || !strings.Contains(err.Error(), "machine-id --new") {
					t.Errorf("missing identity error is not actionable: %v", err)
				}
			}
			if tc.exists {
				if _, err := CreateMachineID(""); err == nil {
					t.Fatal("CreateMachineID replaced an empty existing identity")
				}
				data, err := os.ReadFile(path)
				if err != nil || string(data) != tc.body {
					t.Errorf("identity file = %q, %v, want unchanged %q", data, err, tc.body)
				}
			} else if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("Load created config directory: %v", err)
			}
		})
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
	if _, err := CreateMachineID(""); err != nil {
		t.Fatal(err)
	}
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
			if _, err := CreateMachineID(path); err != nil {
				t.Fatal(err)
			}
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

func TestTheStateDirIsWhereTheHookLooks(t *testing.T) {
	dir := clearEnv(t)
	testenv.Write(t, filepath.Join(dir, "cfg", "config.env"), "AGENT_SESSIONS_STATE_DIR="+filepath.Join(dir, "elsewhere")+"\n")
	if _, err := CreateMachineID(""); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StateDir != StateDir() {
		t.Errorf("sync state dir %s, hook state dir %s", cfg.StateDir, StateDir())
	}
}

func TestExplicitCreationIsIdempotentAndPrivate(t *testing.T) {
	dir := clearEnv(t)
	first, err := CreateMachineID("")
	if err != nil {
		t.Fatal(err)
	}
	again, err := CreateMachineID("")
	if err != nil || again != first || !uuidRE.MatchString(first) {
		t.Fatalf("CreateMachineID = %q then %q, %v", first, again, err)
	}
	for path, want := range map[string]fs.FileMode{
		filepath.Join(dir, "cfg"):               0o700,
		filepath.Join(dir, "cfg", "machine-id"): 0o600,
	} {
		st, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != want {
			t.Errorf("%s permissions = %o, want %o", path, st.Mode().Perm(), want)
		}
	}
	entries, err := os.ReadDir(filepath.Join(dir, "cfg"))
	if err != nil || len(entries) != 1 || entries[0].Name() != "machine-id" {
		t.Errorf("config directory = %v, %v, want only machine-id", entries, err)
	}
}

func TestIdentityOverridesPreservePersistedIdentity(t *testing.T) {
	dir := clearEnv(t)
	path := filepath.Join(dir, "cfg", "config.env")
	original := "c295cbba-2677-4421-b67e-1193ef6144fa"
	testenv.Write(t, filepath.Join(dir, "cfg", "machine-id"), original+"\n")
	testenv.Write(t, path, "AGENT_SESSIONS_MACHINE_ID=from-file\n")
	for _, tc := range []struct{ name, env, want string }{
		{"configFile", "", "from-file"},
		{"environment", "from-env", "from-env"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AGENT_SESSIONS_MACHINE_ID", tc.env)
			cfg, err := Load("")
			if err != nil || cfg.MachineID != tc.want {
				t.Fatalf("Load identity = %v, %v, want %q", cfg, err, tc.want)
			}
			id, err := CreateMachineID("")
			if err != nil || id != tc.want {
				t.Errorf("CreateMachineID = %q, %v, want override %q", id, err, tc.want)
			}
		})
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	id, err := CreateMachineID("")
	if err != nil || id != original {
		t.Errorf("CreateMachineID = %q, %v, want persisted %q", id, err, original)
	}
	data, err := os.ReadFile(filepath.Join(dir, "cfg", "machine-id"))
	if err != nil || string(data) != original+"\n" {
		t.Errorf("machine-id = %q, %v, want unchanged original", data, err)
	}
}

func TestIdentityReadAndCreationErrorsDoNotReplaceFiles(t *testing.T) {
	for _, name := range []string{"configIsDirectory", "identityIsDirectory", "parentIsFile"} {
		t.Run(name, func(t *testing.T) {
			dir := clearEnv(t)
			path := filepath.Join(dir, "cfg", "config.env")
			switch name {
			case "configIsDirectory":
				if err := os.MkdirAll(path, 0o700); err != nil {
					t.Fatal(err)
				}
			case "identityIsDirectory":
				if err := os.MkdirAll(filepath.Join(dir, "cfg", "machine-id"), 0o700); err != nil {
					t.Fatal(err)
				}
			case "parentIsFile":
				testenv.Write(t, filepath.Join(dir, "cfg"), "not a directory")
			}
			if cfg, err := Load(path); cfg != nil || err == nil {
				t.Errorf("Load = %v, %v, want read failure", cfg, err)
			}
			if id, err := CreateMachineID(path); id != "" || err == nil {
				t.Errorf("CreateMachineID = %q, %v, want failure", id, err)
			}
		})
	}
}

func TestOverrideDoesNotCreateMissingMachineID(t *testing.T) {
	for _, name := range []string{"environment", "configFile"} {
		t.Run(name, func(t *testing.T) {
			dir := clearEnv(t)
			path := filepath.Join(dir, "cfg", "config.env")
			if name == "environment" {
				t.Setenv("AGENT_SESSIONS_MACHINE_ID", "known-id")
			} else {
				testenv.Write(t, path, "AGENT_SESSIONS_MACHINE_ID=known-id\n")
			}
			id, err := CreateMachineID(path)
			if err != nil || id != "known-id" {
				t.Errorf("CreateMachineID = %q, %v, want known-id", id, err)
			}
			if _, err := os.Stat(filepath.Join(dir, "cfg", "machine-id")); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("override created machine-id: %v", err)
			}
		})
	}
}
