package capturedaemon

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedServiceRejectsExecutionOverrides(t *testing.T) {
	for _, platform := range []string{"darwin", "linux"} {
		t.Run(platform, func(t *testing.T) {
			home := sandbox(t)
			p, err := ServicePlan(platform, home, filepath.Join(home, "missing-old-executable"), filepath.Join(home, "old-config"), 42)
			if err != nil {
				t.Fatal(err)
			}
			variants := map[string]string{}
			if platform == "darwin" {
				for name, extra := range map[string]string{
					"program":             "<key>Program</key><string>/foreign/executable</string>",
					"environment":         "<key>EnvironmentVariables</key><dict><key>DYLD_INSERT_LIBRARIES</key><string>/foreign/library</string></dict>",
					"duplicate-arguments": "<key>ProgramArguments</key><array><string>/foreign/executable</string></array>",
					"unknown":             "<key>RootDirectory</key><string>/foreign</string>",
				} {
					variants[name] = strings.Replace(p.Unit, "<key>RunAtLoad</key>", extra+"<key>RunAtLoad</key>", 1)
				}
				variants["duplicate-label"] = strings.Replace(p.Unit, "<key>RunAtLoad</key>", "<key>Label</key><string>"+ServiceName+"</string><key>RunAtLoad</key>", 1)
				variants["wrong-value-type"] = strings.Replace(p.Unit, "<key>ProgramArguments</key><array>", "<key>ProgramArguments</key><string>", 1)
				variants["nested-string"] = strings.Replace(p.Unit, "<string>capture-v1</string>", "<string><string>capture-v1</string></string>", 1)
				variants["extra-argument"] = strings.Replace(p.Unit, "</array>", "<string>extra</string></array>", 1)
				variants["duplicate-dictionary"] = strings.Replace(p.Unit, "</plist>", "<dict><key>Program</key><string>/foreign/executable</string></dict></plist>", 1)
			} else {
				for name, extra := range map[string]string{
					"space-reset":     " ExecStart=\n ExecStart=/foreign/executable\n",
					"tab-reset":       "\tExecStart=\n\tExecStart=/foreign/executable\n",
					"pre":             "ExecStartPre=/foreign/executable\n",
					"post":            "ExecStartPost=/foreign/executable\n",
					"stop":            "ExecStop=/foreign/executable\n",
					"reload":          "ExecReload=/foreign/executable\n",
					"duplicate":       "ExecStart=/foreign/executable\n",
					"reset":           "ExecStart=\n",
					"environment":     "Environment=LD_PRELOAD=/foreign/library\n",
					"unknown-section": "[Foreign]\nCommand=/foreign/executable\n[Service]\n",
				} {
					variants[name] = strings.Replace(p.Unit, "Restart=on-failure\n", extra+"Restart=on-failure\n", 1)
				}
				variants["around-equals"] = strings.Replace(p.Unit, "Restart=on-failure\n", "ExecStart =\n ExecStart = /foreign/executable\nRestart=on-failure\n", 1)
				variants["continuation"] = strings.Replace(p.Unit, "Restart=on-failure\n", "ExecStartPre=/foreign/\\\n executable\nRestart=on-failure\n", 1)
				variants["comment"] = strings.Replace(p.Unit, "Type=simple", "# unsupported layout\nType=simple", 1)
				variants["repeated-section"] = strings.Replace(p.Unit, "Restart=on-failure", "[Service]\nRestart=on-failure", 1)
				variants["duplicate-description"] = strings.Replace(p.Unit, "[Service]", "Description="+serviceDescription+"\n[Service]", 1)
				variants["extra-argument"] = strings.Replace(p.Unit, "Restart=on-failure\n", "ExecStart=/foreign/executable extra\nRestart=on-failure\n", 1)
				variants["whitespace"] = strings.Replace(p.Unit, "Type=simple", " Type=simple", 1)
				variants["duplicate-tuning"] = strings.Replace(p.Unit, "RestartSec=60", "RestartSec=60\nRestartSec=61", 1)
			}
			for name, body := range variants {
				t.Run(name, func(t *testing.T) {
					for _, action := range []string{"start", "restart", "stop", "uninstall", "install"} {
						t.Run(action, func(t *testing.T) {
							if err := WritePrivate(p.Path, []byte(body)); err != nil {
								t.Fatal(err)
							}
							calls := 0
							err := Apply(t.Context(), p, action, true, func(context.Context, string, ...string) error { calls++; return nil })
							if err == nil || calls != 0 {
								t.Errorf("override accepted: error=%v runner calls=%d", err, calls)
							}
							after, readErr := os.ReadFile(p.Path)
							if readErr != nil || !bytes.Equal(after, []byte(body)) {
								t.Error("override file deleted or replaced", readErr)
							}
						})
					}
				})
			}
		})
	}
}

func TestManagedServiceSupportedGeneratedTuning(t *testing.T) {
	for _, platform := range []string{"darwin", "linux"} {
		t.Run(platform, func(t *testing.T) {
			home := sandbox(t)
			old, err := ServicePlan(platform, home, filepath.Join(home, "missing old $ % executable"), filepath.Join(home, "old $ % config"), 42)
			if err != nil {
				t.Fatal(err)
			}
			body := strings.ReplaceAll(old.Unit, "<integer>300</integer>", "<integer>301</integer>")
			for _, pair := range [][2]string{{"StartLimitIntervalSec=3600", "StartLimitIntervalSec=3601"}, {"StartLimitBurst=5", "StartLimitBurst=6"}, {"RestartSec=60", "RestartSec=61"}, {"RestartSteps=4", "RestartSteps=5"}, {"RestartMaxDelaySec=900", "RestartMaxDelaySec=901"}} {
				body = strings.ReplaceAll(body, pair[0], pair[1])
			}
			if err := WritePrivate(old.Path, []byte(body)); err != nil {
				t.Fatal(err)
			}
			next, err := ServicePlan(platform, home, filepath.Join(home, "new executable"), filepath.Join(home, "new config"), 42)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			runner := func(context.Context, string, ...string) error { calls++; return nil }
			if err := Apply(t.Context(), next, "stop", true, runner); err != nil {
				t.Fatal("tuned teardown rejected", err)
			}
			if err := Apply(t.Context(), next, "install", true, runner); err != nil {
				t.Fatal("managed upgrade rejected", err)
			}
			if err := Apply(t.Context(), old, "uninstall", true, runner); err != nil {
				t.Fatal("drifted removal rejected", err)
			}
			if calls == 0 {
				t.Fatal("fake supervisor not called")
			}
		})
	}
}

func TestManagedServiceActivationRejectsExecutionDrift(t *testing.T) {
	for _, platform := range []string{"darwin", "linux"} {
		t.Run(platform, func(t *testing.T) {
			for _, state := range []string{"missing-executable", "writable-executable", "config-only"} {
				t.Run(state, func(t *testing.T) {
					c := testConfig(t)
					desiredConfig := filepath.Join(c.Home, "desired-config.json")
					if err := Save(desiredConfig, c); err != nil {
						t.Fatal(err)
					}
					desiredExe := filepath.Join(c.Home, "desired-executable")
					if err := os.WriteFile(desiredExe, []byte("synthetic executable"), 0o700); err != nil {
						t.Fatal(err)
					}
					oldExe := filepath.Join(c.Home, "old-executable")
					if state == "writable-executable" {
						if err := os.WriteFile(oldExe, []byte("unsafe old executable"), 0o777); err != nil {
							t.Fatal(err)
						}
						if err := os.Chmod(oldExe, 0o777); err != nil {
							t.Fatal(err)
						}
					}
					if state == "config-only" {
						oldExe = desiredExe
					}
					oldConfig := filepath.Join(c.Home, "old-config.json")
					oldC := c
					oldC.MachineID = "different-installed-machine"
					if err := Save(oldConfig, oldC); err != nil {
						t.Fatal(err)
					}
					old, err := ServicePlan(platform, c.Home, oldExe, oldConfig, 42)
					if err != nil {
						t.Fatal(err)
					}
					next, err := ServicePlan(platform, c.Home, desiredExe, desiredConfig, 42)
					if err != nil {
						t.Fatal(err)
					}
					if err := WritePrivate(old.Path, []byte(old.Unit)); err != nil {
						t.Fatal(err)
					}
					for _, action := range []string{"start", "restart"} {
						calls := 0
						err := Apply(t.Context(), next, action, true, func(context.Context, string, ...string) error { calls++; return nil })
						if err == nil || calls != 0 {
							t.Errorf("%s activated drifted old execution coordinates: err=%v calls=%d", action, err, calls)
						}
						after, err := os.ReadFile(old.Path)
						if err != nil || !bytes.Equal(after, []byte(old.Unit)) {
							t.Fatal("activation changed installed file", err)
						}
					}
					runner := func(context.Context, string, ...string) error { return nil }
					if err := Apply(t.Context(), next, "stop", true, runner); err != nil {
						t.Fatal("trusted drifted stop blocked", err)
					}
					if err := Apply(t.Context(), next, "install", true, runner); err != nil {
						t.Fatal("safe replacement blocked", err)
					}
					if err := Apply(t.Context(), next, "start", true, runner); err != nil {
						t.Fatal("replaced activation blocked", err)
					}
					if err := Apply(t.Context(), old, "uninstall", true, runner); err != nil {
						t.Fatal("drifted trusted uninstall blocked", err)
					}
				})
			}
		})
	}
}
