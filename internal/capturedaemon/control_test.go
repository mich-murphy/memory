package capturedaemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestControlLoaderRetainsTrustBoundaries(t *testing.T) {
	c := testConfig(t)
	path := filepath.Join(c.Home, "capture.json")
	if err := Save(path, c); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(c.PolicyDir, "exclusions")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadControl(path); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("activation ignored missing policy")
	}
	target := filepath.Join(c.Home, "elsewhere")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(c.PolicyDir, "exclusions")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadControl(path); err == nil {
		t.Fatal("control accepted symlinked policy path")
	}
	if err := os.Remove(filepath.Join(c.PolicyDir, "exclusions")); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(map[string]any){
		func(v map[string]any) { v["unknown_field"] = true },
		func(v map[string]any) { v["spool"] = "relative" },
		func(v map[string]any) { v["policy_dir"] = c.Spool },
		func(v map[string]any) { v["archive_id"] = "" },
	} {
		body, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		var v map[string]any
		if err := json.Unmarshal(body, &v); err != nil {
			t.Fatal(err)
		}
		mutate(v)
		body, err = json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadControl(path); err == nil {
			t.Fatal("unsafe control configuration accepted")
		}
	}
}

func TestHookRootsIgnoreLeafAliasesAndRetainMissingOrdinaryRoot(t *testing.T) {
	c := testConfig(t)
	logical := filepath.Join(c.Home, ".claude")
	c.DefaultRoots = false
	c.Roots = []Root{{Agent: "claude", Path: logical}}
	c.LeafAliases = []LeafAlias{{Agent: "claude", LogicalPath: filepath.Join(logical, "projects", "p", "a.jsonl"), Path: filepath.Join(c.Home, "external", "a.jsonl")}}
	roots, err := c.HookRoots()
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 1 || roots[0].Path != logical {
		t.Fatal("leaf alias changed hook installation authority", roots)
	}
	if _, err := os.Stat(logical); !os.IsNotExist(err) {
		t.Fatal("planning created ordinary root")
	}
}

func TestHookRootsProjectInheritedDeclaredRoute(t *testing.T) {
	c := testConfig(t)
	logical, physical := filepath.Join(c.Home, ".claude"), filepath.Join(c.Home, "physical")
	if err := os.MkdirAll(filepath.Join(physical, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	c.DefaultRoots = false
	c.Roots = []Root{{Agent: "claude", Path: logical}, {Agent: "claude", Path: filepath.Join(logical, "nested")}}
	c.RootAliases = []RootAlias{{Agent: "claude", Root: logical, Path: physical}}
	roots, err := c.HookRoots()
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 2 || roots[0].Path != physical || roots[1].Path != filepath.Join(physical, "nested") {
		t.Fatal("lost inherited physical route", roots)
	}
}

func TestHookRootRejectsUndeclaredAndAmbiguousResolution(t *testing.T) {
	for _, mode := range []string{"undeclared", "ambiguous", "child-link"} {
		t.Run(mode, func(t *testing.T) {
			c := testConfig(t)
			if err := os.MkdirAll(c.Home, 0o700); err != nil {
				t.Fatal(err)
			}
			logical := filepath.Join(c.Home, ".claude")
			physical := filepath.Join(c.Home, "physical")
			foreign := filepath.Join(c.Home, "foreign")
			for _, p := range []string{physical, foreign} {
				if err := os.MkdirAll(p, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			c.DefaultRoots = false
			c.Roots = []Root{{Agent: "claude", Path: logical}}
			c.RootAliases = []RootAlias{{Agent: "claude", Root: logical, Path: physical}}
			switch mode {
			case "undeclared":
				if err := os.Symlink(foreign, logical); err != nil {
					t.Fatal(err)
				}
			case "ambiguous":
				if err := os.Mkdir(logical, 0o700); err != nil {
					t.Fatal(err)
				}
			case "child-link":
				if err := os.Remove(physical); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(foreign, physical); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := c.HookRoots(); err == nil {
				t.Fatal("unsafe or ambiguous mapping accepted")
			}
		})
	}
}
