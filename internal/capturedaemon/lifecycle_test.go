package capturedaemon

import (
	"context"
	"os"
	"path/filepath"
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
