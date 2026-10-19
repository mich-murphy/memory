package snapshot

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/testenv"
)

func build(t *testing.T, e *testenv.Env, cwd string) *Set {
	t.Helper()
	snap, err := Build(adapters.NewClaude(e.Home), e.Claude, cwd, "")
	if err != nil {
		t.Fatal(err)
	}
	return snap.Harness
}

func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

type listed struct{ Scope, Path, Target string }

// files leaves out the test machine's own managed settings and the ancestors above tmp.
func files(set *Set, tmp string) []listed {
	var out []listed
	for _, f := range set.Files {
		if f.Scope == "managed" || f.Scope == "ancestor" && !strings.HasPrefix(f.Path, tmp+"/") {
			continue
		}
		out = append(out, listed{f.Scope, f.Path, f.Target})
	}
	return out
}

func TestSnapshotHoldsTheHarness(t *testing.T) {
	e := testenv.New(t)
	home, c, p := e.Home, e.Claude, e.Project
	dotfiles := filepath.Join(home, "dotfiles")
	testenv.Write(t, filepath.Join(home, "CLAUDE.md"), "home rules\n")
	testenv.Write(t, filepath.Join(p, "docs", "conventions.md"), "use tabs\nSee @style.md and @naming.md.\n")
	testenv.Write(t, filepath.Join(p, "docs", "style.md"), "gofumpt\n")
	testenv.Write(t, filepath.Join(p, "docs", "naming.md"), "short names\n")
	testenv.Write(t, filepath.Join(p, "scripts", "check.sh"), "#!/bin/sh\n")
	testenv.Write(t, filepath.Join(home, "bin", "tool"), "\x7fELF")
	if err := os.Chmod(filepath.Join(home, "bin", "tool"), 0o755); err != nil {
		t.Fatal(err)
	}
	testenv.Write(t, filepath.Join(p, ".claude", "settings.local.json"), `{"hooks": {"Stop": [{"hooks": [
		{"type": "command", "command": "\"$CLAUDE_PROJECT_DIR\"/scripts/check.sh"},
		{"type": "command", "command": "~/.claude/hooks/missing.sh"},
		{"type": "command", "command": "`+home+`/bin/tool --fast"}]}]}}`)
	// curl's @file is not an import: only Markdown files have them.
	testenv.Write(t, filepath.Join(c, "hooks", "notify.sh"), "curl -sd @payload.json \"$URL\"\n")
	testenv.Write(t, filepath.Join(c, "hooks", "payload.json"), "{}")
	testenv.Write(t, filepath.Join(dotfiles, "reviewer.md"), "review\n")
	symlink(t, filepath.Join(dotfiles, "reviewer.md"), filepath.Join(c, "agents", "reviewer.md"))
	testenv.Write(t, filepath.Join(dotfiles, "skills", "shared", "SKILL.md"), "shared\n")
	symlink(t, filepath.Join(dotfiles, "skills", "shared"), filepath.Join(c, "skills", "shared"))
	testenv.Write(t, filepath.Join(c, "plugins", "marketplaces", "mkt", "CLAUDE.md"), "plugin dev notes\n")
	plugin := filepath.Join(c, "plugins", "cache", "mkt", "fmt", "1.0")
	testenv.Write(t, filepath.Join(plugin, "commands", "fmt.md"), "format\n")
	testenv.Write(t, filepath.Join(plugin, "server.key"), "secret")
	testenv.Write(t, filepath.Join(dotfiles, "plugin-shared", "a.md"), "a\n")
	symlink(t, filepath.Join(dotfiles, "plugin-shared"), filepath.Join(plugin, "shared"))
	testenv.Write(t, filepath.Join(c, "plugins", "installed_plugins.json"),
		`{"version": 2, "plugins": {"fmt@mkt": [{"installPath": "`+plugin+`"}]}}`)

	snap, err := Build(adapters.NewClaude(home), c, filepath.Join(p, "src"), "")
	if err != nil {
		t.Fatal(err)
	}
	want := []listed{
		{"ancestor", filepath.Join(home, "CLAUDE.md"), ""},
		{"ancestor", filepath.Join(home, "code", "CLAUDE.md"), ""},
		{"import", filepath.Join(p, "docs", "conventions.md"), ""},
		{"import", filepath.Join(p, "docs", "naming.md"), ""},
		{"import", filepath.Join(p, "docs", "style.md"), ""},
		{"plugin", "fmt@mkt/commands/fmt.md", ""},
		{"plugin", "fmt@mkt/shared/a.md", filepath.Join(dotfiles, "plugin-shared", "a.md")},
		{"project", ".claude/settings.json", ""},
		{"project", ".claude/settings.local.json", ""},
		{"project", "AGENTS.md", ""},
		{"project", "CLAUDE.md", ""},
		{"project", "src/CLAUDE.md", ""},
		{"script", filepath.Join(c, "hooks", "guard.sh"), ""},
		{"script", filepath.Join(home, "bin", "status.py"), ""},
		{"script", filepath.Join(p, "scripts", "check.sh"), ""},
		{"user", ".claude.json#mcp", ""},
		{"user", "CLAUDE.md", ""},
		{"user", "agents/reviewer.md", filepath.Join(dotfiles, "reviewer.md")},
		{"user", "hooks/guard.sh", ""},
		{"user", "hooks/notify.sh", ""},
		{"user", "hooks/payload.json", ""},
		{"user", "plugins/installed_plugins.json", ""},
		{"user", "settings.json", ""},
		{"user", "skills/deploy/SKILL.md", ""},
		{"user", "skills/deploy/scripts/go.sh", ""},
		{"user", "skills/shared/SKILL.md", filepath.Join(dotfiles, "skills", "shared", "SKILL.md")},
	}
	if diff := cmp.Diff(want, files(snap.Harness, e.Tmp)); diff != "" {
		t.Errorf("harness files (-want +got):\n%s", diff)
	}
	if got := snap.Harness.ProjectRoot; got == nil || *got != p {
		t.Errorf("ProjectRoot = %v, want %s", got, p)
	}

	st, err := os.Stat(filepath.Join(c, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := snap.Mtimes["harness:user:CLAUDE.md"], float64(st.ModTime().UnixNano())/1e9; got != want {
		t.Errorf("mtime of CLAUDE.md = %v, want %v", got, want)
	}
	if got, ok := snap.Mtimes["harness:user:.claude.json#mcp"]; ok {
		t.Errorf("mtime of an extract that is not a file = %v, want none", got)
	}
}

func TestNestedInstructionsComeFromGitAndSkipIgnoredTrees(t *testing.T) {
	e := testenv.New(t)
	p := e.Project
	testenv.Write(t, filepath.Join(p, ".gitignore"), ".tools/\nCLAUDE.local.md\n")
	testenv.Write(t, filepath.Join(p, ".tools", "git", "po", "AGENTS.md"), "vendored\n")
	testenv.Write(t, filepath.Join(p, "app", "CLAUDE.local.md"), "my notes\n")
	testenv.Write(t, filepath.Join(p, "app", "deep", "AGENTS.md"), "untracked rules\n")
	set := build(t, e, p)
	var nested []string
	for _, f := range set.Files {
		if f.Scope == "project" && strings.Contains(f.Path, "/") && !strings.HasPrefix(f.Path, ".claude/") {
			nested = append(nested, f.Path)
		}
	}
	want := []string{"app/CLAUDE.local.md", "app/deep/AGENTS.md", "src/CLAUDE.md"}
	if diff := cmp.Diff(want, nested); diff != "" {
		t.Errorf("nested instruction files (-want +got):\n%s", diff)
	}
}

func TestMemorySetHoldsOnlyWhatTheAgentRemembered(t *testing.T) {
	e := testenv.New(t)
	a := adapters.NewClaude(e.Home)
	snap, err := Build(a, e.Claude, e.Project, testenv.ClaudeKey)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Memory == nil {
		t.Fatal("Memory = nil, want the session's auto memory")
	}
	want := []listed{{"auto", filepath.Join(e.ClaudeSlug, "memory", "MEMORY.md"), ""}}
	if diff := cmp.Diff(want, files(snap.Memory, e.Tmp)); diff != "" {
		t.Errorf("memory files (-want +got):\n%s", diff)
	}

	// Auto memory is found through the session's project, and there is no agent-memory.
	if snap, err = Build(a, e.Claude, e.Project, ""); err != nil {
		t.Fatal(err)
	}
	if snap.Memory != nil {
		t.Errorf("Memory = %+v, want nil", snap.Memory)
	}
}

func TestCodexNotifyScriptIsKeptButNotAnMCPServerCommand(t *testing.T) {
	e := testenv.New(t)
	codex := filepath.Join(e.Home, ".codex")
	testenv.Write(t, filepath.Join(codex, "notify.py"), "print('done')\n")
	testenv.Write(t, filepath.Join(codex, "mcp", "server.py"), "serve()\n")
	testenv.Write(t, filepath.Join(codex, "config.toml"), `notify = ["python3", "`+codex+`/notify.py"]

[mcp_servers.local]
command = "`+codex+`/mcp/server.py"
`)
	snap, err := Build(adapters.NewCodex(e.Home), codex, e.Project, "")
	if err != nil {
		t.Fatal(err)
	}
	var scripts []string
	for _, f := range snap.Harness.Files {
		if f.Scope == "script" {
			scripts = append(scripts, f.Path)
		}
	}
	if want := []string{filepath.Join(codex, "notify.py")}; !slices.Equal(scripts, want) {
		t.Errorf("scripts = %v, want %v", scripts, want)
	}
}

func TestSnapshotDoesNotWalkHomeOrADirOutsideARepo(t *testing.T) {
	e := testenv.New(t)
	notes := filepath.Join(e.Home, "notes")
	testenv.Write(t, filepath.Join(e.Home, "deep", "x", "CLAUDE.md"), "should not be found")
	testenv.Write(t, filepath.Join(notes, "deep", "CLAUDE.md"), "should not be found")
	check := func(cwd string) {
		t.Helper()
		for _, f := range build(t, e, cwd).Files {
			if strings.Contains(f.Path, "deep") {
				t.Errorf("from %s, walked into %s", cwd, f.Path)
			}
		}
	}
	check(e.Home)
	check(notes)
	testenv.Git(t, e.Home, "init", "-q") // a dotfiles repo
	check(e.Home)
}

func TestSnapshotWithoutAUsableCwdHasNoProject(t *testing.T) {
	e := testenv.New(t)
	t.Chdir(e.Project) // where a hook runs
	for _, cwd := range []string{"", filepath.Join(e.Project, "deleted")} {
		set := build(t, e, cwd)
		if set.ProjectRoot != nil {
			t.Errorf("cwd %q: ProjectRoot = %s, want none", cwd, *set.ProjectRoot)
		}
		for _, f := range set.Files {
			if f.Scope == "project" || f.Scope == "ancestor" {
				t.Errorf("cwd %q: kept %+v", cwd, f)
			}
		}
	}
}

func TestFilesPastTheLimitsAreListedAsSkipped(t *testing.T) {
	dir := t.TempDir()
	c := newCollected("claude")
	for name, size := range map[string]int{"fits.md": maxFileSize, "too-large.md": maxFileSize + 1} {
		if err := os.WriteFile(filepath.Join(dir, name), make([]byte, size), 0o600); err != nil {
			t.Fatal(err)
		}
		c.add("user", filepath.Join(dir, name), name)
	}
	for i := range maxFilesPerScope + 1 {
		name := fmt.Sprintf("f%04d.md", i)
		testenv.Write(t, filepath.Join(dir, "many", name), "x")
		c.add("project", filepath.Join(dir, "many", name), name)
	}
	kept := map[string]int{}
	for _, e := range c.entries {
		kept[e.scope]++
	}
	if diff := cmp.Diff(map[string]int{"user": 1, "project": maxFilesPerScope}, kept); diff != "" {
		t.Errorf("kept per scope (-want +got):\n%s", diff)
	}
	want := []SkippedFile{
		{Scope: "user", Path: "too-large.md", Reason: "too_large", Size: maxFileSize + 1},
		{Scope: "project", Path: fmt.Sprintf("f%04d.md", maxFilesPerScope), Reason: "file_limit", Size: 1},
	}
	if diff := cmp.Diff(want, c.skipped); diff != "" {
		t.Errorf("skipped (-want +got):\n%s", diff)
	}
}

func TestSnapshotSkipsAConfigFileThatCannotBeCheckedForSecrets(t *testing.T) {
	e := testenv.New(t)
	testenv.Write(t, filepath.Join(e.Project, ".mcp.json"), `{"mcpServers": {"db": {"env": {"PW": "hunter2"`)
	set := build(t, e, e.Project)
	for _, f := range set.Files {
		if f.Path == ".mcp.json" {
			t.Errorf("kept %+v", f)
		}
	}
	want := SkippedFile{Scope: "project", Path: ".mcp.json", Reason: "unparseable", Size: 46}
	if !slices.Contains(set.Skipped, want) {
		t.Errorf("skipped = %v, want %v", set.Skipped, want)
	}
}
