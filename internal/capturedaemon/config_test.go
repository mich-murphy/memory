package capturedaemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func sandbox(t *testing.T) string {
	t.Helper()
	p, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func testConfig(t *testing.T) Config {
	t.Helper()
	dir := sandbox(t)
	c := Config{Version: ConfigVersion, ArchiveID: "archive", MachineID: "machine", Store: filepath.Join(dir, "store"), Spool: filepath.Join(dir, "spool"), PolicyDir: filepath.Join(dir, "policy"), Home: filepath.Join(dir, "home"), DefaultRoots: true, CaptureWorkers: 2, DeliveryWorkers: 2, PollSeconds: 1, OperationSeconds: 2, MaxAttempts: 3, MaxSpoolBytes: 1 << 20, MinFreeBytes: 1, EntriesPerRoot: 32}
	if err := os.MkdirAll(filepath.Join(c.PolicyDir, "exclusions"), 0o700); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestConfigRoundtripAndInventory(t *testing.T) {
	c := testConfig(t)
	path := filepath.Join(c.Home, "runtime.json")
	if err := Save(path, c); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.ArchiveID != c.ArchiveID {
		t.Fatal("identity changed")
	}
	if !got.AllowsSource("claude", filepath.Join(c.Home, ".claude", "projects", "a.jsonl")) || got.AllowsSource("pi", c.Store) {
		t.Fatal("inventory boundary")
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatal("config not private")
	}
}

func TestConfigRejectsUnsafe(t *testing.T) {
	for _, name := range []string{"version", "identity", "overlap", "root", "relative", "workers", "quota", "policy", "newline"} {
		t.Run(name, func(t *testing.T) {
			c := testConfig(t)
			switch name {
			case "version":
				c.Version = "v2"
			case "identity":
				c.ArchiveID = ""
			case "overlap":
				c.Store = c.Spool
			case "root":
				c.Roots = []Root{{"pi", c.Store}}
			case "relative":
				c.Home = "relative"
			case "workers":
				c.CaptureWorkers = 100
			case "quota":
				c.MaxSpoolBytes = 0
			case "policy":
				c.PolicyDir = filepath.Join(c.Home, "missing")
			case "newline":
				c.Spool += "\n"
			}
			if c.Validate() == nil {
				t.Fatal("accepted unsafe config")
			}
		})
	}
	c := testConfig(t)
	link := filepath.Join(c.Home, "link")
	if err := os.MkdirAll(c.Home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(c.Store, link); err != nil {
		t.Fatal(err)
	}
	c.Store = link
	if c.Validate() == nil {
		t.Fatal("accepted symlink")
	}
}

func TestConfigStrictJSONAndPrivateWrites(t *testing.T) {
	c := testConfig(t)
	body, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(c.Home, "runtime.json")
	for _, b := range []string{string(body) + " {}", strings.TrimSuffix(string(body), "}") + `,"credential":"DO-NOT-ECHO"}`, "{", "{}", strings.Repeat(" ", maxConfigBytes+1)} {
		if err = WritePrivate(path, []byte(b)); err != nil {
			t.Fatal(err)
		}
		_, err = Load(path)
		if err == nil || strings.Contains(err.Error(), "DO-NOT-ECHO") {
			t.Fatal("unsafe JSON accepted or echoed")
		}
	}
	if err = os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err = Load(path); err == nil {
		t.Fatal("public config accepted")
	}
	if err = WritePrivate(path, nil); err == nil {
		t.Fatal("public destination overwritten")
	}
	link := filepath.Join(c.Home, "link")
	if err = os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if err = WritePrivate(link, nil); err == nil {
		t.Fatal("symlink overwritten")
	}
}

func TestServicePlansEscapeAndApply(t *testing.T) {
	home := sandbox(t)
	quote := string('"')
	exe := filepath.Join(home, "a space % $ & "+quote+" binary")
	config := filepath.Join(home, "config % & "+quote+".json")
	for _, platform := range []string{"linux", "darwin"} {
		t.Run(platform, func(t *testing.T) {
			p, err := ServicePlan(platform, home, exe, config, 42)
			if err != nil {
				t.Fatal(err)
			}
			if platform == "linux" && (!strings.Contains(p.Unit, "%%") || !strings.Contains(p.Unit, "$$") || !strings.Contains(p.Unit, `\"`)) {
				t.Fatal("systemd escaping missing")
			}
			if platform == "darwin" && (!strings.Contains(p.Unit, "&amp;") || !strings.Contains(p.Unit, "&#34;")) {
				t.Fatal("XML escaping missing")
			}
			var calls []Command
			runner := func(_ context.Context, name string, args ...string) error {
				calls = append(calls, Command{Name: name, Args: args})
				return nil
			}
			if err = Apply(t.Context(), p, "install", false, runner); err != nil || len(calls) != 0 {
				t.Fatal("dry run executed")
			}
			if _, err = os.Stat(p.Path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("dry run wrote")
			}
			if err = Apply(t.Context(), p, "install", true, runner); err != nil {
				t.Fatal(err)
			}
			if want := expectedServiceCommands(platform, home, "install"); !reflect.DeepEqual(calls, want) {
				t.Fatalf("install commands = %#v, want %#v", calls, want)
			}
			if err = Apply(t.Context(), p, "uninstall", true, func(context.Context, string, ...string) error { return errors.New("host secret") }); err == nil || strings.Contains(err.Error(), "host secret") {
				t.Fatal("stop failure leaked")
			}
			if _, err = os.Stat(p.Path); err != nil {
				t.Fatal("deleted before stop")
			}
			before := len(calls)
			if err = Apply(t.Context(), p, "uninstall", true, runner); err != nil {
				t.Fatal(err)
			}
			if got, want := calls[before:], expectedServiceCommands(platform, home, "uninstall"); !reflect.DeepEqual(got, want) {
				t.Fatalf("uninstall commands = %#v, want %#v", got, want)
			}
			if _, err = os.Stat(p.Path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("unit retained")
			}
		})
	}
}

func TestServiceRejectsAndRunnerRequired(t *testing.T) {
	home := sandbox(t)
	for _, platform := range []string{"windows", "linux"} {
		exe := filepath.Join(home, "binary")
		if platform == "linux" {
			exe += "\n"
		}
		if _, err := ServicePlan(platform, home, exe, filepath.Join(home, "config"), 1); err == nil {
			t.Fatal("invalid plan")
		}
	}
	p, err := ServicePlan("linux", home, filepath.Join(home, "binary"), filepath.Join(home, "config"), 1)
	if err != nil {
		t.Fatal(err)
	}
	if Apply(t.Context(), p, "invalid", false, nil) == nil || Apply(t.Context(), p, "install", true, nil) == nil {
		t.Fatal("invalid apply accepted")
	}
}

func TestConfigInventoryQuotaOptionalRoundtrip(t *testing.T) {
	for _, limit := range []int{0, 1, 32768, 65536} {
		c := testConfig(t)
		c.MaxInventoryRecords = limit
		path := filepath.Join(c.Home, "runtime.json")
		if err := Save(path, c); err != nil {
			t.Fatal(err)
		}
		got, err := Load(path)
		if err != nil || got.MaxInventoryRecords != limit {
			t.Fatalf("quota %d: %d %v", limit, got.MaxInventoryRecords, err)
		}
	}
	for _, limit := range []int{-1, 65537} {
		c := testConfig(t)
		c.MaxInventoryRecords = limit
		if err := c.Validate(); err == nil {
			t.Fatalf("accepted quota %d", limit)
		}
	}
}
