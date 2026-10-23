package sessioncapture

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/mich-murphy/memory/internal/publication"
)

// The attacker changes the filesystem in the FINAL authorization callback, then
// restores it at the old payload boundary. Path-following double reads/stat would
// accept outside bytes and later delivery would see only the restored safe path.
func TestAuthorizedSourceRejectsCallbackSwap(t *testing.T) {
	for _, transform := range []string{"", "zstd", "scrubbed-config"} {
		for _, leaf := range []bool{false, true} {
			for _, unchanged := range []bool{false, true} {
				t.Run(transform+map[bool]string{false: "/parent", true: "/leaf"}[leaf]+map[bool]string{false: "/capture", true: "/unchanged"}[unchanged], func(t *testing.T) {
					dir, o, r := setup(t)
					parent := filepath.Join(filepath.Dir(r.Path), "allowed")
					outside := filepath.Join(filepath.Dir(r.Path), "outside")
					for _, p := range []string{parent, outside} {
						if err := os.Mkdir(p, 0o700); err != nil {
							t.Fatal(err)
						}
					}
					name := "body.jsonl"
					r.Transform = transform
					if transform == "scrubbed-config" {
						r.Identity.Agent = "claude"
						name = "settings.json"
					}
					r.Path = filepath.Join(parent, name)
					body := func(forbidden bool) []byte {
						text := "safe authorized bytes"
						if forbidden {
							text = "FORBIDDEN_CALLBACK_BYTES"
						}
						if transform == "scrubbed-config" {
							return []byte(`{"theme":"` + text + `"}`)
						}
						if transform == "zstd" {
							zw, err := zstd.NewWriter(nil)
							if err != nil {
								t.Fatal(err)
							}
							b := zw.EncodeAll([]byte(text), nil)
							if err := zw.Close(); err != nil {
								t.Fatal(err)
							}
							return b
						}
						return []byte(text)
					}
					if err := os.WriteFile(r.Path, body(false), 0o600); err != nil {
						t.Fatal(err)
					}
					forbidden := filepath.Join(outside, name)
					if err := os.WriteFile(forbidden, body(true), 0o600); err != nil {
						t.Fatal(err)
					}
					calls, target := 0, 0
					swapped := false
					restore := func() {
						if !swapped {
							return
						}
						p := parent
						if leaf {
							p = r.Path
						}
						if err := os.Remove(p); err != nil {
							t.Fatal(err)
						}
						if err := os.Rename(p+"-original", p); err != nil {
							t.Fatal(err)
						}
						swapped = false
					}
					defer restore()
					o.CheckPolicy = func(context.Context, Request) error {
						calls++
						if target > 0 && calls == target {
							p, to := parent, outside
							if leaf {
								p, to = r.Path, forbidden
							}
							if err := os.Rename(p, p+"-original"); err != nil {
								t.Fatal(err)
							}
							if err := os.Symlink(to, p); err != nil {
								t.Fatal(err)
							}
							swapped = true
						}
						return nil
					}
					o.Fault = func(point string) error {
						if point == "payload-before-sync" {
							restore()
						}
						return nil
					}
					q := open(t, dir, o)
					if unchanged {
						capture(t, q, r)
					}
					calls = 0
					if _, err := q.check(t.Context(), evidence{Request: r}, false); err != nil {
						t.Fatal(err)
					}
					group := calls
					calls = 0
					target = group
					if transform != "" {
						target = group * 2
					}
					if unchanged {
						target = group * 3
					}
					var err error
					if unchanged {
						_, err = q.CaptureIfChanged(t.Context(), r)
					} else {
						_, err = q.Capture(t.Context(), r)
					}
					if calls < target {
						t.Fatalf("attack callback not reached: %d < %d; err=%v", calls, target, err)
					}
					if err == nil {
						t.Fatal("accepted attacker pathname replacement")
					}
					restore()
					statuses, err := q.Status(t.Context())
					if err != nil {
						t.Fatal(err)
					}
					want := 0
					if unchanged {
						want = 1
					}
					if len(statuses) != want {
						t.Fatal("unsafe observation accepted a job")
					}
					if unchanged {
						backend := newBackend(t)
						if err := q.Deliver(t.Context(), backend, r.Identity); err != nil {
							t.Fatal(err)
						}
						m, _, err := q.latest(t.Context(), r.Identity)
						if err != nil {
							t.Fatal(err)
						}
						if string(derivedBody(t, q, m)) != "safe authorized bytes" && transform != "scrubbed-config" {
							t.Fatal("forbidden body retained")
						}
						dest := filepath.Join(t.TempDir(), "restored")
						exported, err := publication.ExportSession(t.Context(), backend, r.Identity, dest)
						if err != nil {
							t.Fatal(err)
						}
						if len(exported.Bodies) != 1 {
							t.Fatal("unexpected published bodies")
						}
						publicBody, err := os.ReadFile(filepath.Join(dest, exported.Bodies[0].Path))
						if err != nil {
							t.Fatal(err)
						}
						if bytes.Contains(publicBody, []byte("FORBIDDEN_CALLBACK_BYTES")) || !bytes.Contains(publicBody, []byte("safe authorized bytes")) {
							t.Fatal("unsafe published body")
						}

					}
				})
			}
		}
	}
}
