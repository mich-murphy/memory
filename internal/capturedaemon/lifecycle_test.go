package capturedaemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeConfigurationMatchesDiscoveryBounds(t *testing.T) {
	c := testConfig(t)
	c.EntriesPerRoot = 4097
	if c.Validate() == nil {
		t.Fatal("configuration accepted an unsupported discovery budget")
	}
	c = testConfig(t)
	for range 129 {
		c.Roots = append(c.Roots, Root{Agent: "pi", Path: filepath.Join(c.Home, "root")})
	}
	if c.Validate() == nil {
		t.Fatal("unbounded root inventory accepted")
	}
	c = testConfig(t)
	c.MachineID = string([]byte{0xff})
	if c.Validate() == nil {
		t.Fatal("lossy identity accepted")
	}
}

func TestServiceStopStartRestartPreserveStateAndPlan(t *testing.T) {
	for _, platform := range []string{"darwin", "linux"} {
		t.Run(platform, func(t *testing.T) {
			home := sandbox(t)
			config := filepath.Join(home, "private", "configuration.json")
			if err := WritePrivate(config, []byte("synthetic private config")); err != nil {
				t.Fatal(err)
			}
			plan, err := ServicePlan(platform, home, filepath.Join(home, "candidate"), config, 42)
			if err != nil {
				t.Fatal(err)
			}
			var calls []Command
			runner := func(_ context.Context, name string, args ...string) error {
				calls = append(calls, Command{Name: name, Args: args})
				return nil
			}
			if err := Apply(t.Context(), plan, "install", true, runner); err != nil {
				t.Fatal(err)
			}
			for _, action := range []string{"stop", "start", "restart"} {
				before := len(calls)
				if err := Apply(t.Context(), plan, action, false, runner); err != nil || len(calls) != before {
					t.Fatal("dry-run lifecycle executed a host command")
				}
				if err := Apply(t.Context(), plan, action, true, runner); err != nil {
					t.Fatal(err)
				}
				if len(calls) == before {
					t.Fatal("lifecycle did not invoke the injected supervisor")
				}
				if _, err := os.Stat(plan.Path); err != nil {
					t.Fatal("lifecycle deleted the service plan")
				}
				body, err := os.ReadFile(config)
				if err != nil || string(body) != "synthetic private config" {
					t.Fatal("lifecycle changed preserved configuration")
				}
			}
		})
	}
}

func TestServicePlansBoundRestartStorms(t *testing.T) {
	for _, platform := range []string{"linux", "darwin"} {
		p, err := ServicePlan(platform, "/synthetic/home", "/synthetic/candidate", "/synthetic/config", 42)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"StartLimitIntervalSec=3600", "StartLimitBurst=5", "RestartSec=60", "RestartSteps=4", "RestartMaxDelaySec=900"}
		if platform == "darwin" {
			want = []string{"<key>ThrottleInterval</key><integer>300</integer>", "<key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>"}
		}
		for _, value := range want {
			if !strings.Contains(p.Unit, value) {
				t.Fatalf("%s missing restart guard %s", platform, value)
			}
		}
	}
}

func TestReadableSupervisorParent(t *testing.T) {
	home := sandbox(t)
	p, err := ServicePlan("darwin", home, filepath.Join(home, "candidate"), filepath.Join(home, "config"), 42)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p.Path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(p.Path), 0o755); err != nil {
		t.Fatal(err)
	}
	runner := func(context.Context, string, ...string) error { return nil }
	if err := Apply(t.Context(), p, "install", true, runner); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(p.Path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatal("supervisor file not private", err)
	}
	st, err = os.Stat(filepath.Dir(p.Path))
	if err != nil || st.Mode().Perm() != 0o755 {
		t.Fatal("parent changed", err)
	}
	if err := WritePrivate(filepath.Join(filepath.Dir(p.Path), "secret"), []byte("secret")); err == nil {
		t.Fatal("secret writer relaxed")
	}
	if err := os.Chmod(filepath.Dir(p.Path), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := Apply(t.Context(), p, "install", true, runner); err == nil {
		t.Fatal("writable parent accepted")
	}
}

func TestForeignServiceFileRejectedBeforeSupervisor(t *testing.T) {
	home := sandbox(t)
	p, err := ServicePlan("darwin", home, filepath.Join(home, "candidate"), filepath.Join(home, "config"), 42)
	if err != nil {
		t.Fatal(err)
	}
	if err := WritePrivate(p.Path, []byte("foreign private file")); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"stop", "uninstall", "install"} {
		calls := 0
		err := Apply(t.Context(), p, action, true, func(context.Context, string, ...string) error { calls++; return nil })
		if err == nil || calls != 0 {
			t.Fatalf("%s acted on foreign file", action)
		}
	}
}

func TestManagedServiceUpgradeAndDriftedTeardown(t *testing.T) {
	for _, platform := range []string{"darwin", "linux"} {
		t.Run(platform, func(t *testing.T) {
			home := sandbox(t)
			oldExe := filepath.Join(home, "old candidate $ %")
			oldConfig := filepath.Join(home, "old config")
			old, err := ServicePlan(platform, home, oldExe, oldConfig, 42)
			if err != nil {
				t.Fatal(err)
			}
			runner := func(context.Context, string, ...string) error { return nil }
			if err := Apply(t.Context(), old, "install", true, runner); err != nil {
				t.Fatal(err)
			}
			next, err := ServicePlan(platform, home, filepath.Join(home, "new candidate"), filepath.Join(home, "new config"), 42)
			if err != nil {
				t.Fatal(err)
			}
			// The old executable never existed. Changed desired argv and tuning must not
			// make a managed service foreign or prevent an operator from stopping it.
			old.Unit = strings.ReplaceAll(old.Unit, "300", "301")
			old.Unit = strings.ReplaceAll(old.Unit, "RestartSec=60", "RestartSec=61")
			if err := WritePrivate(old.Path, []byte(old.Unit)); err != nil {
				t.Fatal(err)
			}
			if err := Apply(t.Context(), next, "stop", true, runner); err != nil {
				t.Fatal(err)
			}
			if err := Apply(t.Context(), next, "install", true, runner); err != nil {
				t.Fatal("managed upgrade blocked", err)
			}
			if err := Apply(t.Context(), old, "uninstall", true, runner); err != nil {
				t.Fatal("drifted teardown blocked", err)
			}
			if _, err := os.Stat(old.Path); !os.IsNotExist(err) {
				t.Fatal("managed plan retained", err)
			}
		})
	}
}

func TestManagedServiceIdentityBoundary(t *testing.T) {
	for _, platform := range []string{"darwin", "linux"} {
		t.Run(platform, func(t *testing.T) {
			home := sandbox(t)
			p, err := ServicePlan(platform, home, filepath.Join(home, "candidate"), filepath.Join(home, "config"), 42)
			if err != nil {
				t.Fatal(err)
			}
			foreign := strings.ReplaceAll(p.Unit, ServiceName, "foreign-service")
			if platform == "linux" {
				foreign = strings.ReplaceAll(p.Unit, serviceDescription, "foreign description")
			}
			for _, body := range []string{
				foreign,
				strings.ReplaceAll(p.Unit, "capture-v1", "sync"),
				strings.ReplaceAll(p.Unit, filepath.Join(home, "config"), "/unsafe/../config"),
			} {
				if err := WritePrivate(p.Path, []byte(body)); err != nil {
					t.Fatal(err)
				}
				calls := 0
				err := Apply(t.Context(), p, "uninstall", true, func(context.Context, string, ...string) error { calls++; return nil })
				if err == nil || calls != 0 {
					t.Fatal("foreign or unsafe service identity accepted")
				}
				if _, err := os.Stat(p.Path); err != nil {
					t.Fatal("foreign file removed", err)
				}
			}
		})
	}
}
