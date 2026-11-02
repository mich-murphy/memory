package sessioncapture

// Privacy canary invariant. Only synthetic fake secrets. Every backend write
// (key, metadata, raw and zstd-decoded object bytes, and the exported body) is
// scanned for the canary in several encodings.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mich-murphy/memory/internal/publication"
)

const canaryRaw = "CANARY-7f3a\"9c\\é-synthetic-not-a-secret"

func canaryVariants() [][]byte {
	j, _ := json.Marshal(canaryRaw)
	inner := string(j[1 : len(j)-1])
	var asc strings.Builder
	for _, r := range inner {
		if r > 127 {
			fmt.Fprintf(&asc, `\u%04x`, r)
		} else {
			asc.WriteRune(r)
		}
	}
	return [][]byte{[]byte(canaryRaw), []byte(inner), []byte(asc.String()), []byte(url.QueryEscape(canaryRaw)), []byte(hex.EncodeToString([]byte(canaryRaw))), []byte(base64.StdEncoding.EncodeToString([]byte(canaryRaw)))}
}

type canaryEnv struct {
	t    *testing.T
	root string
	be   *modelBackend
	q    *Queue
	mu   sync.Mutex

	deny    map[string]bool
	onStage map[string]func()
}

func newCanaryEnv(t *testing.T) *canaryEnv {
	e := &canaryEnv{t: t, root: t.TempDir(), deny: map[string]bool{}, onStage: map[string]func(){}}
	e.be = newModelBackend(filepath.Join(e.root, "remote"))
	if err := publication.WriteArchive(t.Context(), e.be.inner, publication.Archive{Contract: publication.Contract, ArchiveID: "archive"}); err != nil {
		t.Fatal(err)
	}
	o := Options{
		ArchiveID:     "archive",
		MaxSpoolBytes: 1 << 20,
		Now:           func() time.Time { return time.Unix(100, 0) },
		CheckPolicy: func(_ context.Context, r Request) error {
			e.mu.Lock()
			defer e.mu.Unlock()
			if e.deny[r.Identity.SessionID] {
				return errors.Join(ErrPolicy, ErrSessionExcluded)
			}
			return nil
		},
		Fault: func(s string) error {
			e.mu.Lock()
			f := e.onStage[s]
			delete(e.onStage, s)
			e.mu.Unlock()
			if f != nil {
				f()
			}
			return nil
		},
	}
	q, err := Open(t.Context(), filepath.Join(e.root, "queue"), o)
	if err != nil {
		t.Fatal(err)
	}
	e.q = q
	t.Cleanup(func() { _ = q.Close() })
	return e
}

func (e *canaryEnv) setDeny(sess string) { e.mu.Lock(); e.deny[sess] = true; e.mu.Unlock() }

func (e *canaryEnv) req(sess, name, transform string) Request {
	return Request{Identity: publication.Identity{ArchiveID: "archive", MachineID: "machine", Agent: "pi", SessionID: sess}, SourceID: "src-" + sess, Role: "transcript", Path: filepath.Join(e.root, name), Transform: transform, ObservedAtNS: 1}
}

func (e *canaryEnv) put(name, content string) {
	e.t.Helper()
	if err := os.WriteFile(filepath.Join(e.root, name), []byte(content), 0o600); err != nil {
		e.t.Fatal(err)
	}
}

func canaryJSON() string { j, _ := json.Marshal(canaryRaw); return string(j[1 : len(j)-1]) }

// positive sibling: must capture, publish and export exactly.
func (e *canaryEnv) positive() {
	e.t.Helper()
	e.put("pos.jsonl", "positive-sibling\n")
	r := e.req("pos", "pos.jsonl", "")
	if _, err := e.q.Capture(e.t.Context(), r); err != nil {
		e.t.Fatalf("positive capture: %v", err)
	}
	if err := e.q.Deliver(e.t.Context(), e.be, r.Identity); err != nil {
		e.t.Fatalf("positive deliver: %v", err)
	}
	exp, err := publication.ExportSession(e.t.Context(), e.be.inner, r.Identity, filepath.Join(e.root, "posexp"))
	if err != nil || len(exp.Bodies) != 1 {
		e.t.Fatalf("positive export: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(e.root, "posexp", exp.Bodies[0].Path))
	if err != nil || string(b) != "positive-sibling\n" {
		e.t.Fatalf("positive body %q %v", b, err)
	}
}

func (e *canaryEnv) noLeak() {
	e.t.Helper()
	if l := e.be.leaks(canaryVariants()); len(l) != 0 {
		e.t.Fatalf("CANARY LEAK (%d): %v", len(l), l[:min(len(l), 5)])
	}
}

func (e *canaryEnv) notPublished(sess string) {
	e.t.Helper()
	_, err := publication.Read(e.t.Context(), e.be.inner, publication.Identity{ArchiveID: "archive", MachineID: "machine", Agent: "pi", SessionID: sess})
	if !errors.Is(err, publication.ErrNotFound) {
		e.t.Fatalf("session %s unexpectedly readable/other error: %v", sess, err)
	}
}

func TestPrivacyCanary(t *testing.T) {
	cfgJSON := `{"env":{"TOKEN":"` + canaryJSON() + `"}}`

	t.Run("scrubbed-config", func(t *testing.T) {
		e := newCanaryEnv(t)
		e.put("settings.json", cfgJSON)
		r := e.req("cfg", "settings.json", "scrubbed-config")
		if _, err := e.q.Capture(t.Context(), r); err != nil {
			t.Fatalf("scrubbed capture: %v", err)
		}
		if err := e.q.Deliver(t.Context(), e.be, r.Identity); err != nil {
			t.Fatal(err)
		}
		exp, err := publication.ExportSession(t.Context(), e.be.inner, r.Identity, filepath.Join(e.root, "cfgexp"))
		if err != nil || len(exp.Bodies) != 1 {
			t.Fatalf("export: %v", err)
		}
		b, _ := os.ReadFile(filepath.Join(e.root, "cfgexp", exp.Bodies[0].Path))
		if !bytes.Contains(b, []byte("[redacted]")) {
			t.Fatalf("published config not redacted: %q", b)
		}
		e.positive()
		e.noLeak()
	})
	t.Run("raw-config-denied", func(t *testing.T) {
		e := newCanaryEnv(t)
		e.put("settings.json", cfgJSON)
		if _, err := e.q.Capture(t.Context(), e.req("cfgraw", "settings.json", "")); !errors.Is(err, ErrPolicy) {
			t.Fatalf("raw protected config accepted: %v", err)
		}
		_ = e.q.Deliver(t.Context(), e.be, e.req("cfgraw", "settings.json", "").Identity)
		e.notPublished("cfgraw")
		e.positive()
		e.noLeak()
	})
	t.Run("malformed-config", func(t *testing.T) {
		e := newCanaryEnv(t)
		e.put("config.toml", "[mcp_servers.test.env]\nTOKEN = \""+canaryRaw+"\n")
		r := e.req("cfgbad", "config.toml", "scrubbed-config")
		if _, err := e.q.Capture(t.Context(), r); err == nil {
			t.Fatal("malformed protected config accepted")
		}
		_ = e.q.Deliver(t.Context(), e.be, r.Identity)
		e.notPublished("cfgbad")
		e.positive()
		e.noLeak()
	})
	t.Run("excluded-from-start", func(t *testing.T) {
		e := newCanaryEnv(t)
		e.setDeny("excl")
		name := "excl-" + canaryRaw + ".jsonl"
		e.put(name, `{"secret":"`+canaryJSON()+"\"}\n"+canaryRaw+"\n")
		r := e.req("excl", name, "")
		if _, err := e.q.Capture(t.Context(), r); !errors.Is(err, ErrPolicy) {
			t.Fatalf("excluded identity captured: %v", err)
		}
		_ = e.q.Deliver(t.Context(), e.be, r.Identity)
		e.notPublished("excl")
		e.positive()
		e.noLeak()
	})
	late := func(t *testing.T, mode string) {
		e := newCanaryEnv(t)
		name := "late-" + canaryRaw + ".jsonl"
		e.put(name, `{"secret":"`+canaryJSON()+"\"}\n"+canaryRaw+"\n")
		r := e.req("late", name, "")
		if _, err := e.q.Capture(t.Context(), r); err != nil {
			t.Fatalf("pre-optout capture: %v", err)
		}
		switch mode {
		case "before-delivery":
			e.setDeny("late")
		case "at-upload-before":
			e.mu.Lock()
			e.onStage["upload-before"] = func() { e.mu.Lock(); e.deny["late"] = true; e.mu.Unlock() }
			e.mu.Unlock()
		}
		if err := e.q.Deliver(t.Context(), e.be, r.Identity); err == nil {
			t.Fatal("deliver of opted-out session reported success")
		}
		e.notPublished("late")
		e.positive()
		e.noLeak()
	}
	t.Run("optout-after-capture-before-delivery", func(t *testing.T) { late(t, "before-delivery") })
	t.Run("optout-at-upload-before", func(t *testing.T) { late(t, "at-upload-before") })

	t.Run("scanner-control-unblocked-transcript", func(t *testing.T) {
		e := newCanaryEnv(t)
		e.put("ctl.jsonl", `{"m":"`+canaryJSON()+"\"}\n"+canaryRaw+"\n")
		r := e.req("ctl", "ctl.jsonl", "")
		if _, err := e.q.Capture(t.Context(), r); err != nil {
			t.Fatal(err)
		}
		if err := e.q.Deliver(t.Context(), e.be, r.Identity); err != nil {
			t.Fatal(err)
		}
		if len(e.be.leaks(canaryVariants())) == 0 {
			t.Fatal("scanner blind: canary in published transcript not found")
		}
		hidden := false
		for _, p := range e.be.puts {
			if bytes.Contains(p.Dec, []byte(canaryRaw)) && !bytes.Contains(p.Raw, []byte(canaryRaw)) {
				hidden = true
			}
		}
		if !hidden {
			t.Fatal("zstd decoding not exercised: canary visible only in compressed bytes or not at all")
		}
	})
}
