package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/capturedaemon"
	"github.com/mich-murphy/memory/internal/exclusion"
	"github.com/mich-murphy/memory/internal/publication"
	"github.com/mich-murphy/memory/internal/sessioncapture"
)

func TestDisabledNativePersistsAcrossDiscoveryAndDelivery(t *testing.T) {
	for _, custom := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "custom"}[custom], func(t *testing.T) {
			c, _ := runtimeFixture(t)
			root := filepath.Join(c.Home, ".claude")
			if custom {
				root = filepath.Join(c.Home, "projects")
				c.DefaultRoots = false
				c.Roots = []capturedaemon.Root{{Agent: "claude", Path: root}}
			}
			a := filepath.Join(root, "projects", "p", "A.jsonl")
			as := filepath.Join(root, "projects", "p", "A", "subagents", "agent-x.jsonl")
			b := filepath.Join(root, "projects", "p", "B.jsonl")
			bs := filepath.Join(root, "projects", "p", "B", "subagents", "agent-y.jsonl")
			for _, p := range []string{a, as, b, bs} {
				runtimeWrite(t, p)
			}
			discover, closeDiscovery := runtimeDiscovery(c, "")
			defer func() {
				if err := closeDiscovery(); err != nil {
					t.Error(err)
				}
			}()
			batch, err := discover(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			q, err := sessioncapture.Open(t.Context(), c.Spool, sessioncapture.Options{ArchiveID: c.ArchiveID, MaxSpoolBytes: c.MaxSpoolBytes, CheckPolicy: runtimePolicy(c)})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := q.Close(); err != nil {
					t.Error(err)
				}
			}()
			if err := publication.WriteArchive(t.Context(), publication.NewLocal(c.Store), publication.Archive{Contract: publication.Contract, ArchiveID: c.ArchiveID}); err != nil {
				t.Fatal(err)
			}
			requests := map[string]sessioncapture.Request{}
			for _, r := range batch.Requests {
				requests[r.Path] = r
			}
			for _, p := range []string{a, as, b, bs} {
				r, ok := requests[p]
				if !ok {
					t.Fatal("missing initial discovery")
				}
				if _, err := q.Capture(t.Context(), r); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("AGENT_SESSIONS_DISABLE", "1")
			code, err := runNativeCaptureHook(t.Context(), c, runtimeFlags{agent: "claude", path: a}, nil)
			if err != nil || code != exitOK {
				t.Fatalf("disabled exclusion: %v", err)
			}
			t.Setenv("AGENT_SESSIONS_DISABLE", "")

			for _, p := range []string{a, as} {
				if err := runtimePolicy(c)(t.Context(), requests[p]); err == nil {
					t.Fatal("opted-out session admitted")
				}
			}
			check, closeCheck := runtimeDiscovery(c, "")
			defer func() {
				if err := closeCheck(); err != nil {
					t.Error(err)
				}
			}()
			foundBefore := map[string]bool{}
			for range 20 {
				batch, err := check(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				for _, r := range batch.Requests {
					foundBefore[r.Path] = true
				}
				if batch.Complete {
					break
				}
			}
			if foundBefore[a] || foundBefore[as] || !foundBefore[b] || !foundBefore[bs] {
				t.Fatal("disabled policy did not isolate periodic discovery")
			}
			if err := os.Remove(a); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(as); err != nil {
				t.Fatal(err)
			}
			if err := q.Deliver(t.Context(), publication.NewLocal(c.Store), requests[a].Identity); err == nil {
				t.Fatal("deleted excluded original delivered")
			}
			for _, p := range []string{b, bs} {
				if err := runtimePolicy(c)(t.Context(), requests[p]); err != nil {
					t.Fatal(err)
				}
				if _, err := q.Capture(t.Context(), requests[p]); err != nil {
					t.Fatal("healthy session capture blocked after opt-out:", err)
				}
			}
			if err := q.Deliver(t.Context(), publication.NewLocal(c.Store), requests[b].Identity); err != nil {
				t.Fatal(err)
			}
			next, closeNext := runtimeDiscovery(c, "")
			defer func() {
				if err := closeNext(); err != nil {
					t.Error(err)
				}
			}()
			found := map[string]bool{}
			// Discovery is intentionally budgeted: continue until coverage completes.
			for range 20 {
				batch, err := next(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				for _, r := range batch.Requests {
					found[r.Path] = true
				}
				if batch.Complete {
					break
				}
			}
			if !found[b] || !found[bs] {
				t.Fatal("one opt-out blocked healthy session discovery")
			}
			if found[a] || found[as] {
				t.Fatal("excluded session discovered")
			}
		})
	}
}

func TestDisabledNativeMetadataOnlyAndFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name, path, input string
		broken, want      bool
	}{
		{name: "missing-main", path: "projects/p/A.jsonl", want: true},
		{name: "subagent", path: "projects/p/A/subagents/agent-x.jsonl", want: true},
		{name: "stdin", input: `{"transcript_path":"PATH"}`, want: true},
		{name: "pathless", input: `{"session_id":"DO-NOT-ECHO"}`},
		{name: "malformed", input: `{"transcript_path":3}`},
		{name: "protected", path: "settings.json"},
		{name: "external-unresolved", path: "file-history/12345678-1234-1234-1234-123456789abc/x"},
		{name: "broken-policy", path: "projects/p/A.jsonl", broken: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := runtimeFixture(t)
			t.Setenv("AGENT_SESSIONS_DISABLE", "1")
			root := filepath.Join(c.Home, ".claude")
			path := ""
			if tc.path != "" {
				path = filepath.Join(root, tc.path)
			}
			input := strings.ReplaceAll(tc.input, "PATH", filepath.Join(root, "projects", "p", "A.jsonl"))
			if tc.broken {
				if err := os.WriteFile(filepath.Join(c.PolicyDir, "exclusions", "bad.json"), []byte("DO-NOT-ECHO"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			code, err := runNativeCaptureHook(t.Context(), c, runtimeFlags{agent: "claude", path: path}, strings.NewReader(input))
			if (err == nil && code == exitOK) != tc.want {
				t.Fatalf("unexpected disabled result: %d %v", code, err)
			}
			if err != nil && (strings.Contains(err.Error(), "DO-NOT-ECHO") || strings.Contains(err.Error(), c.Home)) {
				t.Fatal("private error")
			}
			if _, err := os.Stat(c.Spool); !os.IsNotExist(err) {
				t.Fatal("disabled hook created queue/hints/wake state")
			}
			if tc.want {
				r, err := exclusion.Load(c.PolicyDir)
				if err != nil || len(r) != 1 || r[0].SessionKey == "" {
					t.Fatal("no durable session opt-out")
				}
			}
		})
	}
}

func TestDisabledNativePreservesOldAliases(t *testing.T) {
	c, _ := runtimeFixture(t)
	root := filepath.Join(c.Home, ".claude")
	path := filepath.Join(root, "projects", "p", "A.jsonl")
	old, err := exclusion.RecordPath(c.PolicyDir, adapters.NewClaude(c.Home), path)
	if err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(c.Home, "old-alias.jsonl")
	old.Paths = append(old.Paths, alias)
	if err := exclusion.Save(c.PolicyDir, old); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_SESSIONS_DISABLE", "1")
	if code, err := runNativeCaptureHook(t.Context(), c, runtimeFlags{agent: "claude", path: path}, nil); code != exitOK || err != nil {
		t.Fatal(err)
	}
	records, err := exclusion.Load(c.PolicyDir)
	if err != nil || len(records) != 1 {
		t.Fatal(err)
	}
	r := records[0]
	if r.ID != old.ID || r.Root != old.Root || r.SessionKey != old.SessionKey || r.RecordedAt != old.RecordedAt {
		t.Fatal("old identity changed")
	}
	found := false
	for _, p := range r.Paths {
		if p == alias {
			found = true
		}
	}
	if !found {
		t.Fatal("old alias lost")
	}
}
