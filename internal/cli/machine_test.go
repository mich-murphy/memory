package cli

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mich-murphy/memory/internal/config"
	"github.com/mich-murphy/memory/internal/testenv"
)

func identityEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("AGENT_SESSIONS_CONFIG", filepath.Join(dir, "cfg", "config.env"))
	t.Setenv("AGENT_SESSIONS_STATE_DIR", filepath.Join(dir, "state"))
	t.Setenv("AGENT_SESSIONS_MACHINE_ID", "")
	t.Setenv("AGENT_SESSIONS_LOCAL_STORE", filepath.Join(dir, "bucket"))
	return filepath.Join(dir, "cfg", "machine-id")
}

func identityCommand(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Main(t.Context(), args, "test", IO{Stdout: &out, Stderr: &errOut})
	return code, out.String(), errOut.String()
}

func TestMachineIDRequiresExplicitNewChoice(t *testing.T) {
	path := identityEnv(t)
	for _, cmd := range []string{"machine-id", "sync", "backfill", "doctor"} {
		code, out, errOut := identityCommand(t, cmd)
		if code != exitFailed || out != "" || !strings.Contains(errOut, "machine-id --new") {
			t.Errorf("%s = %d, %q, %q, want actionable failure", cmd, code, out, errOut)
		}
		if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s created identity: %v", cmd, err)
		}
		if _, err := os.Stat(config.StateDir()); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s created state before identity choice: %v", cmd, err)
		}
	}
	code, out, errOut := identityCommand(t, "machine-id", "--new")
	if code != exitOK || len(strings.TrimSpace(out)) != 36 || errOut != "" {
		t.Fatalf("machine-id --new = %d, %q, %q", code, out, errOut)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != out {
		t.Fatalf("machine-id file = %q, %v, want %q", data, err, out)
	}
	for _, args := range [][]string{{"machine-id"}, {"machine-id", "--new"}} {
		code, again, errOut := identityCommand(t, args...)
		if code != exitOK || again != out || errOut != "" {
			t.Errorf("%v = %d, %q, %q, want unchanged %q", args, code, again, errOut, out)
		}
	}
}

func TestMachineIDRefusesEmptyExistingFile(t *testing.T) {
	path := identityEnv(t)
	testenv.Write(t, path, " \n")
	for _, args := range [][]string{{"machine-id"}, {"machine-id", "--new"}, {"doctor"}, {"sync"}} {
		code, out, errOut := identityCommand(t, args...)
		if code != exitFailed || out != "" || !strings.Contains(errOut, "restore") {
			t.Errorf("%v = %d, %q, %q, want restore instructions", args, code, out, errOut)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != " \n" {
		t.Errorf("machine-id file = %q, %v, want unchanged whitespace", data, err)
	}
}

func TestMachineIDCanRestoreKnownIdentityAfterCacheAndIdentityLoss(t *testing.T) {
	for _, method := range []string{"environment", "configFile", "originalFile"} {
		t.Run(method, func(t *testing.T) {
			path := identityEnv(t)
			code, original, errOut := identityCommand(t, "machine-id", "--new")
			if code != exitOK {
				t.Fatalf("create identity: %d %s", code, errOut)
			}
			// Losing both the disposable cache and identity cannot silently choose a fresh
			// namespace. Restoration of the operator's known ID is independent of R2.
			testenv.Write(t, filepath.Join(config.StateDir(), "state.sqlite"), "disposable cache")
			if err := os.RemoveAll(config.StateDir()); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if code, _, _ := identityCommand(t, "machine-id"); code != exitFailed {
				t.Fatal("lost cache and identity silently accepted")
			}
			switch method {
			case "environment":
				t.Setenv("AGENT_SESSIONS_MACHINE_ID", strings.TrimSpace(original))
			case "configFile":
				testenv.Write(t, config.FilePath(), "AGENT_SESSIONS_MACHINE_ID="+original)
			case "originalFile":
				testenv.Write(t, path, original)
			}
			for _, args := range [][]string{{"machine-id"}, {"machine-id", "--new"}} {
				code, out, errOut := identityCommand(t, args...)
				if code != exitOK || out != original || errOut != "" {
					t.Errorf("restored %v = %d, %q, %q, want %q", args, code, out, errOut, original)
				}
			}
			if method != "originalFile" {
				if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("override restore unexpectedly created identity file: %v", err)
				}
			}
		})
	}
}

func TestMachineIDUsageNeverCreatesIdentity(t *testing.T) {
	path := identityEnv(t)
	for _, tc := range []struct {
		args []string
		want int
	}{
		{[]string{"machine-id", "--help"}, exitOK},
		{[]string{"machine-id", "--unknown"}, exitUsage},
		{[]string{"machine-id", "--new", "unexpected"}, exitUsage},
	} {
		if code, _, _ := identityCommand(t, tc.args...); code != tc.want {
			t.Errorf("%v exit = %d, want %d", tc.args, code, tc.want)
		}
		if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%v created identity: %v", tc.args, err)
		}
	}
}

type identityBrokenWriter struct{}

func (identityBrokenWriter) Write([]byte) (int, error) { return 0, fs.ErrClosed }

func TestMachineIDReportsFailedOutputWithoutRotatingIdentity(t *testing.T) {
	path := identityEnv(t)
	var errOut bytes.Buffer
	code := Main(t.Context(), []string{"machine-id", "--new"}, "test", IO{Stdout: identityBrokenWriter{}, Stderr: &errOut})
	if code != exitFailed || !strings.Contains(errOut.String(), "print") {
		t.Errorf("broken stdout = %d, %q, want print failure", code, errOut.String())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	code, out, _ := identityCommand(t, "machine-id", "--new")
	if code != exitOK || out != string(data) {
		t.Errorf("retry = %d, %q, want persisted %q", code, out, data)
	}
}

func TestInstallAndReinstallDoNotCreateIdentity(t *testing.T) {
	path := identityEnv(t)
	for range 2 {
		code, out, errOut := identityCommand(t, "install")
		if code != exitOK {
			t.Fatalf("install = %d, %q, %q", code, out, errOut)
		}
		if !strings.Contains(out, "machine-id --new") {
			t.Errorf("install did not explain the required identity choice: %s", out)
		}
		if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("install created identity: %v", err)
		}
	}
}
