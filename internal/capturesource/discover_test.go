package capturesource

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T, path, body string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func options(t *testing.T) Options {
	t.Helper()
	return Options{ArchiveID: "archive", MachineID: "machine", Home: t.TempDir(), Policy: func(context.Context, Scope) error { return nil }}
}

func discover(t *testing.T, o Options) Result {
	t.Helper()
	r, err := Discover(t.Context(), o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Cursor.Close(); err != nil {
			t.Error(err)
		}
	})
	return r
}

func hasCode(r Result, code string) bool {
	for _, i := range r.Issues {
		if i.Code == code {
			return true
		}
	}
	return false
}

func TestDefaultCustomOwnershipAndRaw(t *testing.T) {
	o := options(t)
	custom := filepath.Join(o.Home, "learned")
	o.Roots = []Root{{"claude", custom}}
	var paths []string
	for _, root := range []string{filepath.Join(o.Home, ".claude"), custom} {
		for _, rel := range []string{"projects/project/s.jsonl", "projects/project/s/subagents/child.jsonl", "projects/project/s/tool-results/result.txt"} {
			paths = append(paths, fixture(t, filepath.Join(root, rel), "not-json\n{\"partial\":"))
		}
	}
	pi := filepath.Join(o.Home, ".pi", "agent", "sessions", "--project--", "session.jsonl")
	fixture(t, pi, "malformed raw")
	codex := filepath.Join(o.Home, ".codex", "sessions", "2026", "rollout-2026-01-01T00-00-00-11111111-1111-1111-1111-111111111111.jsonl")
	fixture(t, codex, "{unfinished")
	r := discover(t, o)
	if len(r.Requests) != 8 {
		t.Fatalf("requests=%d issues=%v", len(r.Requests), r.Issues)
	}
	owners := map[string]string{}
	roles := map[string]int{}
	for _, q := range r.Requests {
		roles[q.Role]++
		if q.Identity.Agent == "claude" {
			root := filepath.Join(o.Home, ".claude")
			if strings.HasPrefix(q.Path, custom) {
				root = custom
			}
			if old := owners[root]; old != "" && old != q.Identity.SessionID {
				t.Fatal("child ownership mismatch")
			}
			owners[root] = q.Identity.SessionID
		}
		if q.SourceID == "" || len(q.Associations) < 3 {
			t.Fatal("lost provenance")
		}
	}
	if owners[custom] == owners[filepath.Join(o.Home, ".claude")] {
		t.Fatal("root identity collision")
	}
	if roles["subagent"] != 2 || roles["tool-result"] != 2 || roles["main"] != 4 {
		t.Fatal(roles)
	}
	for _, p := range paths {
		body, err := os.ReadFile(p)
		if err != nil || string(body) != "not-json\n{\"partial\":" {
			t.Fatal("source modified")
		}
	}
}

func TestPartialDoesNotBlockHealthyResource(t *testing.T) {
	o := options(t)
	bad := fixture(t, filepath.Join(o.Home, "bad"), "not a directory")
	healthy := filepath.Join(o.Home, "healthy")
	o.OnlyRoots = true
	o.Roots = []Root{{"claude", bad}, {"codex", healthy}}
	fixture(t, filepath.Join(healthy, "history.jsonl"), "raw")
	r := discover(t, o)
	if len(r.Requests) != 1 || !strings.HasPrefix(r.Requests[0].Identity.SessionID, "resource/") || !hasCode(r, "root_not_directory_or_alias") {
		t.Fatalf("%+v", r)
	}
}

func TestPolicyProtectedDatabaseCompressionAliases(t *testing.T) {
	o := options(t)
	root := filepath.Join(o.Home, ".codex")
	for _, name := range []string{"auth.json", "config.toml", "state.sqlite", "other.zst", "history.jsonl", "denied.jsonl"} {
		fixture(t, filepath.Join(root, name), "SYNTHETIC-NOT-PRIVATE")
	}
	if err := os.Symlink(filepath.Join(root, "auth.json"), filepath.Join(root, "alias.jsonl")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "history.jsonl"), filepath.Join(root, "safealias.jsonl")); err != nil {
		t.Fatal(err)
	}
	o.Policy = func(_ context.Context, s Scope) error {
		if strings.HasSuffix(s.Path, "denied.jsonl") {
			return errors.New("do not print this error")
		}
		return nil
	}
	r := discover(t, o)
	if len(r.Requests) != 1 || filepath.Base(r.Requests[0].Path) != "history.jsonl" {
		t.Fatalf("unsafe requests: %+v", r.Requests)
	}
	for _, code := range []string{"protected_source_requires_scrub", "sqlite_backup_required", "decoded_source_required", "policy_denied", "alias_integration_required"} {
		if !hasCode(r, code) {
			t.Errorf("missing %s", code)
		}
	}
	for _, issue := range r.Issues {
		if strings.Contains(issue.Code, "SYNTHETIC") || strings.Contains(issue.Code, "print") {
			t.Fatal("unsafe diagnostic")
		}
	}
}

func TestBoundedResumeFairnessAndCancel(t *testing.T) {
	o := options(t)
	o.OnlyRoots = true
	o.EntriesPerRoot = 3
	for _, name := range []string{"a", "b"} {
		root := filepath.Join(o.Home, name)
		o.Roots = append(o.Roots, Root{"codex", root})
		for i := range 20 {
			fixture(t, filepath.Join(root, fmt.Sprintf("file-%02d.txt", i)), "raw")
		}
	}
	seen := map[string]bool{}
	calls := 0
	for {
		r := discover(t, o)
		calls++
		for _, c := range r.Coverage {
			if c.Examined > 3 {
				t.Fatal("budget exceeded")
			}
		}
		for _, q := range r.Requests {
			if seen[q.Path] {
				t.Fatal("cursor duplicate")
			}
			seen[q.Path] = true
		}
		o.Cursor = r.Cursor
		if calls == 1 && len(seen) != 6 {
			t.Fatal("early root starved later root")
		}
		done := true
		for _, w := range r.Cursor.walks {
			done = done && w.done
		}
		if done {
			break
		}
		if calls > 10 {
			t.Fatal("cursor not progressing")
		}
	}
	if len(seen) != 40 {
		t.Fatal(len(seen))
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Discover(ctx, o); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestAbsenceEmptyAndPolicyUnavailable(t *testing.T) {
	o := options(t)
	o.OnlyRoots = true
	missing := filepath.Join(o.Home, "missing")
	empty := filepath.Join(o.Home, "empty")
	if err := os.Mkdir(empty, 0o700); err != nil {
		t.Fatal(err)
	}
	o.Roots = []Root{{"pi", missing}, {"pi", empty}}
	r := discover(t, o)
	if r.Coverage[0].State != "observed_absent" || r.Coverage[1].State != "observed_empty" {
		t.Fatal(r.Coverage)
	}
	o.Policy = nil
	r = discover(t, o)
	if !hasCode(r, "policy_unavailable") || len(r.Requests) != 0 {
		t.Fatal(r)
	}
}

func TestClaudeExternalArtifactsHonest(t *testing.T) {
	o := options(t)
	fixture(t, filepath.Join(o.Home, ".claude", "tasks", "11111111-1111-1111-1111-111111111111", "1.json"), "raw")
	r := discover(t, o)
	if !hasCode(r, "ownership_integration_required") || len(r.Requests) != 0 {
		t.Fatal(r)
	}
}

func fingerprintOf(t *testing.T, o Options, path string) string {
	t.Helper()
	requests := discover(t, o).Requests
	for i := range requests {
		if requests[i].Path == path {
			return requests[i].Fingerprint
		}
	}
	t.Fatalf("not discovered: %s", path)
	return ""
}

func TestDiscoveryFingerprintTracksStatNotSweepTime(t *testing.T) {
	o := options(t)
	p := fixture(t, filepath.Join(o.Home, ".pi", "agent", "sessions", "--project--", "session.jsonl"), "aaaa\n")
	first := fingerprintOf(t, o, p)
	if first == "" || fingerprintOf(t, o, p) != first {
		t.Fatal("unchanged file must keep a stable non-empty fingerprint")
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	// Same-size rewrite with restored mtime still changes ctime (and may change ino).
	time.Sleep(20 * time.Millisecond)
	if err := os.WriteFile(p, []byte("bbbb\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	second := fingerprintOf(t, o, p)
	if second == first {
		t.Fatal("same-size rewrite with restored mtime must change fingerprint")
	}
	if err := os.WriteFile(p, []byte("bbbb\nmore\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if fingerprintOf(t, o, p) == second {
		t.Fatal("append must change fingerprint")
	}
}

func TestDiscoveryTransformedSourcesHaveNoFingerprint(t *testing.T) {
	o := options(t)
	o.Derived = true
	root := filepath.Join(o.Home, ".codex")
	fixture(t, filepath.Join(root, "state.sqlite"), "SQLite format 3\x00")
	r := discover(t, o)
	var seen bool
	for _, q := range r.Requests {
		if q.Transform != "" {
			seen = true
			if q.Fingerprint != "" {
				t.Fatalf("transform %q must not carry a fingerprint", q.Transform)
			}
		}
	}
	if !seen {
		t.Skip("no transformed request emitted in this fixture")
	}
}
