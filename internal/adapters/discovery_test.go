package adapters

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeSource(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoveryReportsSourceFailuresAndKeepsLogicalSymlinkRoot(t *testing.T) {
	a := NewCodex(t.TempDir())
	missing := Discover(a, filepath.Join(t.TempDir(), "absent"))
	if !missing.Complete || len(missing.Issues) != 0 {
		t.Fatalf("absent = %+v", missing)
	}
	file := filepath.Join(t.TempDir(), "file")
	writeSource(t, file, "x")
	bad := Discover(a, file)
	if bad.Complete || len(bad.Issues) != 1 {
		t.Fatalf("file root = %+v", bad)
	}
	physical := t.TempDir()
	writeSource(t, filepath.Join(physical, "history.jsonl"), "x\n")
	logical := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(physical, logical); err != nil {
		t.Fatal(err)
	}
	got := Discover(a, logical)
	if !got.Complete || len(got.Files) != 1 || got.Files[0].Root != logical || got.Files[0].Path != filepath.Join(logical, "history.jsonl") {
		t.Fatalf("symlink = %+v", got)
	}
	if got.Files[0].Key != keyPrefix(a, logical)+"history.jsonl" {
		t.Fatalf("key = %s", got.Files[0].Key)
	}
	if err := os.Chmod(physical, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(physical, 0o700); err != nil {
			t.Error(err)
		}
	})
	got = Discover(a, logical)
	if got.Complete || len(got.Issues) == 0 {
		t.Fatalf("unreadable = %+v", got)
	}
}

func TestExportsReportMalformedSourcesWithoutPlainDatabaseFallback(t *testing.T) {
	a := NewCodex(t.TempDir())
	root := a.CanonicalRoot()
	writeSource(t, filepath.Join(root, "state.sqlite"), "not sqlite")
	writeSource(t, filepath.Join(root, "state.sqlite-wal"), "live WAL")
	found := Discover(a, root)
	if len(found.Files) != 0 || len(found.Databases) != 1 {
		t.Fatalf("Discover = %+v", found)
	}
	exported := SQLiteExports(t.Context(), a, root, found.Databases, t.TempDir(), time.Hour)
	if exported.Complete || len(exported.Files) != 0 || len(exported.Issues) != 1 {
		t.Fatalf("SQLiteExports = %+v", exported)
	}
	c := NewClaude(t.TempDir())
	writeSource(t, c.claudeJSON(c.CanonicalRoot()), "broken json")
	result := c.Exports(c.CanonicalRoot(), t.TempDir(), 0)
	if result.Complete || len(result.Issues) != 1 {
		t.Fatalf("Exports = %+v", result)
	}
	writeSource(t, c.claudeJSON(c.CanonicalRoot()), `{"primaryApiKey":"secret"}`)
	result = c.Exports(c.CanonicalRoot(), t.TempDir(), 0)
	if !result.Complete || len(result.Files) != 1 || result.Files[0].SourcePath != c.claudeJSON(c.CanonicalRoot()) {
		t.Fatalf("Exports = %+v", result)
	}
	blocked := filepath.Join(t.TempDir(), "file")
	writeSource(t, blocked, "x")
	result = c.Exports(c.CanonicalRoot(), blocked, 0)
	if result.Complete || len(result.Issues) != 1 {
		t.Fatalf("blocked export = %+v", result)
	}
}

func TestSessionNamespacesSeparateRootsAndPiSubdirectories(t *testing.T) {
	const id = "11111111-2222-3333-4444-555555555555"
	all := All(t.TempDir())
	for _, agent := range Names {
		a := all[agent]
		other := t.TempDir()
		if agent == "pi" {
			t.Setenv("PI_CODING_AGENT_DIR", other)
		}
		rel := map[string]string{"claude": "projects/slug/" + id + ".jsonl", "codex": "sessions/rollout-2026-01-02T03-04-05-" + id + ".jsonl", "pi": "sessions/--w--/same.jsonl"}[agent]
		one, ok := a.Classify(filepath.Join(a.CanonicalRoot(), rel), a.CanonicalRoot())
		if !ok {
			t.Fatal(agent)
		}
		two, ok := a.Classify(filepath.Join(other, rel), other)
		if !ok {
			t.Fatal(agent)
		}
		if two.SessionKey != keyPrefix(a, other)+one.SessionKey || !strings.HasPrefix(two.Key, keyPrefix(a, other)) {
			t.Fatalf("%s: %+v %+v", agent, one, two)
		}
	}
	p := all["pi"]
	root := p.CanonicalRoot()
	one, _ := p.Classify(filepath.Join(root, "sessions", "--one--", "same.jsonl"), root)
	two, _ := p.Classify(filepath.Join(root, "sessions", "--two--", "same.jsonl"), root)
	if one.SessionKey == two.SessionKey {
		t.Fatal("Pi directory collision")
	}
}

func TestRelatedWalksOnlyItsSessionAndReportsTargetedFailures(t *testing.T) {
	a := NewClaude(t.TempDir())
	root := a.CanonicalRoot()
	const sid = "11111111-2222-3333-4444-555555555555"
	main := filepath.Join(root, "projects", "slug", sid+".jsonl")
	writeSource(t, main, "{}\n")
	paths := []string{
		filepath.Join(root, "projects", "slug", sid, "subagents", "a.jsonl"),
		filepath.Join(root, "projects", "slug", sid+".jsonl.superseded-old"),
		filepath.Join(root, "todos", sid+"-agent.json"),
		filepath.Join(root, "debug", sid+".txt"),
		filepath.Join(root, "file-history", sid, "v1"),
		filepath.Join(root, "session-env", sid, "env"),
		filepath.Join(root, "tasks", "team", "1.json"),
		filepath.Join(root, "teams", "team", "mail.json"),
	}
	for _, path := range paths {
		writeSource(t, path, "{}\n")
	}
	unrelated := filepath.Join(root, "unrelated", "file")
	writeSource(t, unrelated, "secret")
	if err := os.Chmod(filepath.Dir(unrelated), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(filepath.Dir(unrelated), 0o700); err != nil {
			t.Error(err)
		}
	})
	sf, ok := a.Classify(main, root)
	if !ok {
		t.Fatal("main not classified")
	}
	got := a.Related(sf)
	if !got.Complete || len(got.Files) != len(paths)+1 {
		t.Fatalf("Related = %+v", got)
	}
	for _, file := range got.Files {
		if strings.Contains(file.Path, "unrelated") {
			t.Fatalf("unrelated captured: %s", file.Path)
		}
	}
	todo := filepath.Join(root, "todos")
	if err := os.Chmod(todo, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(todo, 0o700); err != nil {
			t.Error(err)
		}
	})
	got = a.Related(sf)
	if got.Complete || len(got.Issues) == 0 || len(got.Files) == 0 {
		t.Fatalf("partial Related = %+v", got)
	}
}

func TestSnapshotSourcesRejectOriginalDuplicateKeysAndReportSourceFailures(t *testing.T) {
	a := NewClaude(t.TempDir())
	root := a.CanonicalRoot()
	source := a.claudeJSON(root)
	absent := a.ExtraSnapshotEntries(root, "")
	if !absent.Complete || len(absent.Issues) != 0 {
		t.Fatalf("absent = %+v", absent)
	}
	for _, body := range []string{
		`{"mcpServers":{},"mcpServers":{"s":{"env":{"TOKEN":"secret"}}}}`,
		`{"projects":{"/p":{"mcpServers":{},"mcpServers":{"s":{"env":{"TOKEN":"secret"}}}}}}`,
		`{"projects":{"/p":{"mcpServers":{},"\u006dcpServers":{"s":{"env":{"TOKEN":"secret"}}}}}}`,
		`{"primaryApiKey":"one","primaryApiKey":"two","mcpServers":{}}`,
	} {
		writeSource(t, source, body)
		got := a.ExtraSnapshotEntries(root, "/p")
		if got.Complete || len(got.Issues) == 0 || len(got.Entries) != 0 {
			t.Fatalf("duplicate accepted: %s, %+v", body, got)
		}
	}
	writeSource(t, source, `{"mcpServers":{"s":{"env":{"TOKEN":"secret"}}},"projects":{"/p":{"allowedTools":["Read"]}}}`)
	got := a.ExtraSnapshotEntries(root, "/p")
	if !got.Complete || len(got.Entries) != 1 || strings.Contains(string(got.Entries[0].Body), "secret") {
		t.Fatalf("valid = %+v", got)
	}
	if err := os.Chmod(source, 0); err != nil {
		t.Fatal(err)
	}
	got = a.ExtraSnapshotEntries(root, "/p")
	if got.Complete || len(got.Issues) == 0 {
		t.Fatalf("unreadable = %+v", got)
	}
	if err := os.Chmod(source, 0o600); err != nil {
		t.Fatal(err)
	}
	writeSource(t, source, strings.Repeat("x", (5<<20)+1))
	if got = a.ExtraSnapshotEntries(root, ""); got.Complete || len(got.Issues) == 0 {
		t.Fatal("oversized source not reported")
	}
	pluginFile := filepath.Join(root, "plugins", "installed_plugins.json")
	writeSource(t, pluginFile, `{"plugins":{"bad":{"installPath":"/missing-plugin"}}}`)
	plugins := a.Plugins(root)
	if plugins.Complete || len(plugins.Issues) != 1 {
		t.Fatalf("missing plugin = %+v", plugins)
	}
	if err := os.Chmod(pluginFile, 0); err != nil {
		t.Fatal(err)
	}
	plugins = a.Plugins(root)
	if plugins.Complete || len(plugins.Issues) == 0 {
		t.Fatalf("unreadable plugin source = %+v", plugins)
	}
	if err := os.Chmod(pluginFile, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestExplicitClassificationNeverTreatsLiveDatabaseAsPlainFile(t *testing.T) {
	for _, a := range All(t.TempDir()) {
		for _, name := range []string{"state.sqlite", "logs.db", "state.sqlite-wal", "state.sqlite-shm"} {
			if _, ok := a.Classify(filepath.Join(a.CanonicalRoot(), name), a.CanonicalRoot()); ok {
				t.Errorf("%s classified live %s", a.Name(), name)
			}
		}
	}
}

func TestIntentionalClaudeConfigRejectsOtherProtectedSymlinkIdentities(t *testing.T) {
	for _, kind := range []string{"credentialHop", "backup", "config", "state"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			a := NewClaude(home)
			root := a.CanonicalRoot()
			source := a.claudeJSON(root)
			dest := filepath.Join(t.TempDir(), "innocent.json")
			writeSource(t, dest, `{"mcpServers":{"s":{"env":{"TOKEN":"secret"}}}}`)
			protected := filepath.Join(t.TempDir(), "auth.json")
			switch kind {
			case "backup":
				protected = filepath.Join(t.TempDir(), ".claude.json.bak")
			case "config":
				protected = filepath.Join(t.TempDir(), "private-config")
				t.Setenv("AGENT_SESSIONS_CONFIG", protected)
			case "state":
				protected = filepath.Join(t.TempDir(), "state", "file")
				t.Setenv("AGENT_SESSIONS_STATE_DIR", filepath.Dir(protected))
			}
			if err := os.MkdirAll(filepath.Dir(protected), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(dest, protected); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(protected, source); err != nil {
				t.Fatal(err)
			}
			exported := a.Exports(root, t.TempDir(), 0)
			if exported.Complete || len(exported.Files) != 0 || len(exported.Issues) == 0 {
				t.Fatalf("protected export = %+v", exported)
			}
			extra := a.ExtraSnapshotEntries(root, "")
			if extra.Complete || len(extra.Entries) != 0 || len(extra.Issues) == 0 {
				t.Fatalf("protected snapshot source = %+v", extra)
			}
		})
	}
}
