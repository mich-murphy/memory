package capturesource

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestPrimaryDefaultExactHint(t *testing.T) {
	o := options(t)
	o.Derived = true
	o.Now = func() time.Time { return time.Unix(123, 456) }
	p := fixture(t, primaryPath(o), "{not parsed until queue")
	unrelated := fixture(t, filepath.Join(o.Home, "unrelated", "private.json"), "must not observe")
	var calls []Scope
	o.Policy = func(_ context.Context, s Scope) error {
		calls = append(calls, s)
		if s.Path == unrelated || strings.Contains(s.Path, "/unrelated") {
			return errors.New("Home traversal")
		}
		return nil
	}
	q, err := Classify(t.Context(), o, "claude", p)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(filepath.Join(o.Home, ".claude")))
	if q.Identity.SessionID != "resource/"+hex.EncodeToString(sum[:])+"/config/claude.json" {
		t.Fatal("unstable resource namespace", q.Identity)
	}
	r := discover(t, o)
	if len(r.Requests) != 1 || !reflect.DeepEqual(q, r.Requests[0]) || hasCode(r, "protected_export_integration_required") {
		t.Fatal(q, r)
	}
	if q.SourceID != "config/claude.json" || q.Role != "artifact" || q.Transform != "scrubbed-config" || !containsAll(q.Associations, []string{"ownership:agent-resource", "adapter-role:config", "root:" + filepath.Join(o.Home, ".claude"), "transform:scrubbed-config"}) {
		t.Fatal(q)
	}
	for _, s := range calls {
		if s.Path == o.Home || s.Path == unrelated {
			t.Fatal("broadened inventory", s)
		}
	}
	o.Cursor = r.Cursor
	resumed := discover(t, o)
	if len(resumed.Requests) != 0 {
		t.Fatal("duplicate standalone observation", resumed)
	}
	o.Cursor = nil
	for _, flags := range []struct{ derived, only bool }{{false, false}, {true, true}} {
		o.Derived, o.OnlyRoots = flags.derived, flags.only
		_, err := Classify(t.Context(), o, "claude", p)
		assertPrimaryGap(t, err, "outside_inventory", p)
	}
}

func containsAll(got, want []string) bool {
	for _, w := range want {
		if !slices.Contains(got, w) {
			return false
		}
	}
	return true
}

func assertPrimaryGap(t *testing.T, err error, code, path string) {
	t.Helper()
	var gap *ClassificationError
	if !errors.As(err, &gap) || gap.Issue.Code != code || gap.Issue.Path != path {
		t.Fatalf("want %s at %s: %v", code, path, err)
	}
}

func TestPrimarySafetyAndCoordinates(t *testing.T) {
	for _, kind := range []string{"missing", "directory", "alias", "safe-alias", "collector-config", "state", "policy", "missing-policy"} {
		t.Run(kind, func(t *testing.T) {
			o := options(t)
			o.Derived = true
			p := primaryPath(o)
			code := "observed_absence"
			switch kind {
			case "directory":
				if err := os.Mkdir(p, 0o700); err != nil {
					t.Fatal(err)
				}
				code = "nonregular_source"
			case "alias":
				target := fixture(t, filepath.Join(o.Home, "auth.json"), "credentials")
				if err := os.Symlink(target, p); err != nil {
					t.Fatal(err)
				}
				code = "policy_denied"
			case "safe-alias":
				target := fixture(t, filepath.Join(o.Home, "safe.json"), "{}")
				if err := os.Symlink(target, p); err != nil {
					t.Fatal(err)
				}
				code = "alias_integration_required"
			case "collector-config":
				fixture(t, p, "{}")
				o.ConfigPath = p
				code = "policy_denied"
			case "state":
				fixture(t, p, "{}")
				o.StateDir = o.Home
				code = "policy_denied"
			case "policy":
				o.Policy = func(_ context.Context, s Scope) error {
					if s.Path == p {
						return errors.New("deny before metadata even if missing")
					}
					return nil
				}
				code = "policy_denied"
			case "missing-policy":
				o.Policy = nil
				code = "policy_unavailable"
			}
			_, err := Classify(t.Context(), o, "claude", p)
			assertPrimaryGap(t, err, code, p)
			r := discover(t, o)
			if len(r.Requests) != 0 || !hasCode(r, code) || hasCode(r, "protected_export_integration_required") {
				t.Fatal(r)
			}
			found := false
			for _, i := range r.Issues {
				found = found || (i.Path == p && i.Code == code)
			}
			if !found {
				t.Fatal("missing standalone coordinates", r.Issues)
			}
		})
	}
}

func TestPrimaryLearnedRootWins(t *testing.T) {
	o := options(t)
	o.Derived = true
	// A trusted explicit inventory can own Home; the standalone exception itself
	// never creates this root or expands inventory to it.
	o.Roots = []Root{{"claude", o.Home}}
	p := fixture(t, primaryPath(o), "{}")
	q, err := Classify(t.Context(), o, "claude", p)
	if err != nil {
		t.Fatal(err)
	}
	r := discover(t, o)
	if len(r.Requests) != 1 || !reflect.DeepEqual(q.Identity, r.Requests[0].Identity) || q.SourceID == "config/claude.json" {
		t.Fatal(q, r)
	}
}
