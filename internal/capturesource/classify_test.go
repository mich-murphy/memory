package capturesource

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestClassifyMatchesDiscovery(t *testing.T) {
	o := options(t)
	o.Now = func() time.Time { return time.Unix(123, 456) }
	for _, agent := range []string{"claude", "codex", "pi"} {
		root := filepath.Join(o.Home, "custom-"+agent)
		o.Roots = append(o.Roots, Root{agent, root})
		def := filepath.Join(o.Home, "."+agent)
		if agent == "pi" {
			def = filepath.Join(def, "agent")
		}
		for _, r := range []string{root, def} {
			paths := []string{"history.jsonl"}
			switch agent {
			case "claude":
				paths = []string{"projects/p/s.jsonl", "projects/p/s/subagents/c.jsonl", "projects/p/s/tool-results/r.txt"}
			case "codex":
				paths = []string{"sessions/2026/rollout-2026-01-01T00-00-00-11111111-1111-1111-1111-111111111111.jsonl", "history.jsonl"}
			case "pi":
				paths = []string{"sessions/p/s.jsonl"}
			}
			for _, p := range paths {
				fixture(t, filepath.Join(r, p), "{invalid")
			}
		}
	}
	r := discover(t, o)
	if len(r.Requests) != 12 {
		t.Fatalf("requests=%d issues=%v", len(r.Requests), r.Issues)
	}
	for _, want := range r.Requests {
		got, err := Classify(t.Context(), o, want.Identity.Agent, want.Path)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("got=%+v err=%v want=%+v", got, err, want)
		}
		root := ""
		for _, a := range got.Associations {
			if value, ok := strings.CutPrefix(a, "root:"); ok {
				root = value
			}
		}
		sum := sha256.Sum256([]byte(root))
		if !strings.Contains(got.Identity.SessionID, "/"+hex.EncodeToString(sum[:])+"/") {
			t.Fatal("missing full root identity", got.Identity)
		}
	}
}

func TestClassifyDenialsAndCoordinates(t *testing.T) {
	o := options(t)
	root := filepath.Join(o.Home, ".codex")
	for _, name := range []string{"auth.json", "config.toml", "state.sqlite", "other.zst", "history.jsonl"} {
		fixture(t, filepath.Join(root, name), "raw")
	}
	if err := os.Symlink(filepath.Join(root, "history.jsonl"), filepath.Join(root, "alias.jsonl")); err != nil {
		t.Fatal(err)
	}
	outside := fixture(t, filepath.Join(o.Home, "outside.jsonl"), "raw")
	cases := map[string]string{outside: "outside_inventory", filepath.Join(root, "auth.json"): "policy_denied", filepath.Join(root, "config.toml"): "protected_source_requires_scrub", filepath.Join(root, "state.sqlite"): "sqlite_backup_required", filepath.Join(root, "other.zst"): "decoded_source_required", filepath.Join(root, "alias.jsonl"): "alias_integration_required", root: "nonregular_source"}
	for path, code := range cases {
		q, err := Classify(t.Context(), o, "codex", path)
		var gap *ClassificationError
		if !errors.As(err, &gap) || gap.Issue.Code != code || gap.Issue.Path != path || q.Path != "" {
			t.Fatalf("%s: %+v %v", path, q, err)
		}
	}
	o.Policy = nil
	_, err := Classify(t.Context(), o, "codex", filepath.Join(root, "history.jsonl"))
	var gap *ClassificationError
	if !errors.As(err, &gap) || gap.Issue.Code != "policy_unavailable" {
		t.Fatal(err)
	}
}

func TestMostSpecificRootAndAliases(t *testing.T) {
	o := options(t)
	root := filepath.Join(o.Home, ".codex")
	nested := filepath.Join(root, "nested")
	o.Roots = []Root{{"codex", nested}}
	p := fixture(t, filepath.Join(nested, "history.jsonl"), "raw")
	q, err := Classify(t.Context(), o, "codex", p)
	if err != nil || q.Associations[2] != "root:"+nested {
		t.Fatal(q, err)
	}
	r := discover(t, o)
	if len(r.Requests) != 1 || !reflect.DeepEqual(r.Requests[0].Identity, q.Identity) {
		t.Fatal("overlapping inventory identity", r)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(nested, alias); err != nil {
		t.Fatal(err)
	}
	_, err = Classify(t.Context(), o, "codex", filepath.Join(alias, "history.jsonl"))
	var gap *ClassificationError
	if !errors.As(err, &gap) || gap.Issue.Code != "alias_integration_required" {
		t.Fatal(err)
	}
}

func TestGlobalDescriptorBudget(t *testing.T) {
	o := options(t)
	o.OnlyRoots = true
	o.MaxDepth = 64
	o.EntriesPerRoot = 4
	for i := range 128 {
		root := filepath.Join(o.Home, strconv.Itoa(i))
		o.Roots = append(o.Roots, Root{"codex", root})
		fixture(t, filepath.Join(root, "a", "b", "c", "history.jsonl"), "raw")
	}
	r := discover(t, o)
	if !hasCode(r, "fd_budget") {
		t.Fatal("missing fd gap")
	}
	open := 0
	for _, w := range r.Cursor.walks {
		open += len(w.stack)
	}
	if open > 256 {
		t.Fatal(open)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	o.Cursor = r.Cursor
	_, err := Discover(ctx, o)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := r.Cursor.Close(); err != nil {
		t.Fatal(err)
	}
	for _, w := range r.Cursor.walks {
		if len(w.stack) != 0 {
			t.Fatal("leaked descriptor")
		}
	}
}

func TestHookRespectsDeniedAncestor(t *testing.T) {
	o := options(t)
	root := filepath.Join(o.Home, ".claude")
	dir := filepath.Join(root, "projects", "private")
	p := fixture(t, filepath.Join(dir, "s.jsonl"), "raw")
	o.Policy = func(_ context.Context, s Scope) error {
		if s.Path == dir {
			return errors.New("private")
		}
		return nil
	}
	_, err := Classify(t.Context(), o, "claude", p)
	var gap *ClassificationError
	if !errors.As(err, &gap) || gap.Issue.Code != "policy_denied" {
		t.Fatal(err)
	}
	r := discover(t, o)
	if len(r.Requests) != 0 || !hasCode(r, "policy_denied") {
		t.Fatal(r)
	}
}

func TestUnreadableRootDoesNotBlockHealthyRoot(t *testing.T) {
	o := options(t)
	o.OnlyRoots = true
	bad := filepath.Join(o.Home, "bad")
	good := filepath.Join(o.Home, "good")
	fixture(t, filepath.Join(bad, "history.jsonl"), "raw")
	fixture(t, filepath.Join(good, "history.jsonl"), "raw")
	if err := os.Chmod(bad, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(bad, 0o700); err != nil {
			t.Error(err)
		}
	})
	if f, err := os.Open(bad); err == nil {
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		t.Skip("process can bypass directory permissions")
	}
	o.Roots = []Root{{"codex", bad}, {"codex", good}}
	r := discover(t, o)
	if len(r.Requests) != 1 || r.Requests[0].Path != filepath.Join(good, "history.jsonl") || !hasCode(r, "root_unreadable") {
		t.Fatal(r)
	}
	if r.Coverage[0].State != "unavailable" {
		t.Fatal(r.Coverage)
	}
}
