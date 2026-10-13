package adapters

// Claude Code.
//
// Layout (checked against https://code.claude.com/docs/en/claude-directory and a real ~/.claude,
// Claude Code 2.1.x):
//
//	projects/<cwd-slug>/<session-id>.jsonl                          main transcript
//	projects/<cwd-slug>/<session-id>.orphaned-<ts>-<suffix>.jsonl   earlier transcript set aside
//	projects/<cwd-slug>/<session-id>.jsonl.superseded-<ts>          earlier transcript set aside
//	projects/<cwd-slug>/<session-id>/subagents/agent-<id>.jsonl     subagent transcript (+ .meta.json)
//	projects/<cwd-slug>/<session-id>/tool-results/<file>            large tool output spilled to disk
//	projects/<cwd-slug>/memory/                                     auto memory (Claude's own notes)
//	file-history/<session-id>/<hash>@v<n>                           file contents before each edit
//	todos/<session-id>-agent-<agent-id>.json, tasks/<list-id>/      todo and task lists
//	teams/<team>/                                                   agent-team config and mailboxes
//	session-env/<session-id>/, debug/<session-id>.txt               per-session env and debug log
//	plans/, paste-cache/, usage-data/, agent-memory/, history.jsonl
//	settings.json, CLAUDE.md, skills/, agents/, commands/, rules/, hooks/ ...   the harness
//
// ~/.claude.json (outside the dir unless CLAUDE_CONFIG_DIR is set) holds MCP servers,
// per-project settings and UI state next to the API key, so it is exported without
// primaryApiKey. CLAUDE_CONFIG_DIR moves the whole tree. Everything except auto memory is
// deleted after cleanupPeriodDays (default 30), which `agent-sessions install` raises.

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mich-murphy/memory/internal/fsutil"
	"github.com/mich-murphy/memory/internal/jsonx"
	"github.com/mich-murphy/memory/internal/scrub"
)

var (
	claudeMainRE     = regexp.MustCompile(`^([^./]+)\.jsonl$`)
	claudeSetAsideRE = regexp.MustCompile(`^([^./]+)\.(?:orphaned-.+\.jsonl|jsonl\.superseded-.+)$`)
	uuidPrefixRE     = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

	claudeSessionSubdirRoles = map[string]string{"subagents": "subagent", "tool-results": "tool_result"}
	// Top-level directories whose files belong to one session, found from the session id in
	// the path.
	claudePerSessionDirs = map[string]string{
		"file-history": "file_history", "session-env": "session_env", "debug": "debug", "todos": "todo", "tasks": "task",
	}
	claudeTopLevelRoles = map[string]string{
		"plans": "plan", "teams": "team", "paste-cache": "paste", "usage-data": "usage", "agent-memory": "memory",
		"skills": "config", "agents": "config", "commands": "config", "rules": "config", "output-styles": "config",
		"hooks": "config", "plugins": "config", "sessions": "liveness",
	}
	claudeConfigFiles = map[string]bool{
		"settings.json": true, "settings.local.json": true, "CLAUDE.md": true, "keybindings.json": true,
		"launcher-settings.json": true, "remote-settings.json": true, "policy-limits.json": true,
	}
)

// Claude is the Claude Code adapter.
type Claude struct {
	home  string
	found map[string]string // "<root>\x00<sid>" -> "<slug>/<sid>"
}

// NewClaude returns the Claude Code adapter for a home directory.
func NewClaude(home string) *Claude {
	return &Claude{home: home, found: map[string]string{}}
}

// Name is "claude".
func (c *Claude) Name() string { return "claude" }

// Home is the user's home directory.
func (c *Claude) Home() string { return c.home }

// SkipDirs: plugins/cache and plugins/marketplaces are git checkouts of plugin sources
// (snapshots record the installed plugins); local/ and downloads/ hold Claude Code's own
// install; backups/ holds copies of ~/.claude.json, API key included.
func (c *Claude) SkipDirs() []string {
	return []string{"plugins/cache", "plugins/marketplaces", "local", "downloads", "backups", "statsig", "ide", "telemetry"}
}

// ManagedPaths are the administrator settings files.
func (c *Claude) ManagedPaths() []string {
	return []string{
		"/etc/claude-code/managed-settings.json",
		"/etc/claude-code/managed-mcp.json",
		"/etc/claude-code/CLAUDE.md",
		"/Library/Application Support/ClaudeCode/managed-settings.json",
		"/Library/Application Support/ClaudeCode/managed-mcp.json",
		"/Library/Application Support/ClaudeCode/CLAUDE.md",
	}
}

// Rules are the harness files a snapshot records.
func (c *Claude) Rules() SnapshotRules {
	return SnapshotRules{
		User: []string{
			"CLAUDE.md", "settings.json", "settings.local.json", "launcher-settings.json", "keybindings.json",
			"rules/**", "skills/**", "agents/**", "commands/**", "output-styles/**", "hooks/**",
			"plugins/installed_plugins.json", "plugins/known_marketplaces.json",
		},
		Project: []string{
			"CLAUDE.md", "CLAUDE.local.md", "AGENTS.md", "**/CLAUDE.md", "**/CLAUDE.local.md", "**/AGENTS.md",
			".claude/settings.json", ".claude/settings.local.json", ".claude/rules/**", ".claude/skills/**",
			".claude/agents/**", ".claude/commands/**", ".claude/output-styles/**", ".claude/hooks/**", ".mcp.json",
		},
	}
}

// CanonicalRoot is ~/.claude.
func (c *Claude) CanonicalRoot() string { return filepath.Join(c.home, ".claude") }

// DefaultRoots is $CLAUDE_CONFIG_DIR or ~/.claude.
func (c *Claude) DefaultRoots() []string {
	if p := envPath("CLAUDE_CONFIG_DIR", c.home); p != "" {
		return []string{p}
	}
	return []string{c.CanonicalRoot()}
}

// InferRoot finds the directory holding projects/ above a transcript path.
func (c *Claude) InferRoot(path string) string {
	for dir := filepath.Dir(path); dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		if filepath.Base(dir) == "projects" {
			if rel, ok := Rel(path, dir); ok && strings.Count(rel, "/") >= 1 {
				return filepath.Dir(dir)
			}
		}
	}
	return ""
}

// Classify maps a path under root to its key, session and role.
func (c *Claude) Classify(path, root string) (SessionFile, bool) {
	rel, ok := Rel(path, root)
	if !ok {
		return SessionFile{}, false
	}
	parts := strings.Split(rel, "/")
	sf := func(sessionKey, role string) (SessionFile, bool) {
		return SessionFile{Agent: c.Name(), Path: path, Key: KeyPrefix(c, root) + rel, SessionKey: sessionKey, Role: role}, true
	}
	top := parts[0]
	if len(parts) == 1 {
		if top == "history.jsonl" {
			return sf("", "history")
		}
		if claudeConfigFiles[top] || hasAnySuffix(top, []string{".sh", ".py", ".js", ".ts"}) {
			return sf("", "config")
		}
		return sf("", "unknown")
	}
	if top == "projects" {
		return c.classifyProject(parts, sf)
	}
	if role, ok := claudePerSessionDirs[top]; ok {
		if sid := uuidPrefixRE.FindString(parts[1]); sid != "" {
			return sf(c.findSession(root, sid), role)
		}
		return sf("", role)
	}
	if role, ok := claudeTopLevelRoles[top]; ok {
		return sf("", role)
	}
	return sf("", "unknown")
}

func (c *Claude) classifyProject(parts []string, sf func(string, string) (SessionFile, bool)) (SessionFile, bool) {
	if len(parts) < 3 {
		return sf("", "unknown")
	}
	slug, name := parts[1], parts[2]
	if len(parts) == 3 {
		if m := claudeMainRE.FindStringSubmatch(name); m != nil {
			return sf(slug+"/"+m[1], "main")
		}
		if m := claudeSetAsideRE.FindStringSubmatch(name); m != nil {
			return sf(slug+"/"+m[1], "superseded")
		}
		return sf("", "unknown")
	}
	if name == "memory" {
		return sf("", "memory")
	}
	role := "aux"
	if len(parts) > 4 {
		if r, ok := claudeSessionSubdirRoles[parts[3]]; ok {
			role = r
		}
	}
	return sf(slug+"/"+name, role)
}

// findSession returns "<slug>/<sid>" for a session id, from where its transcript lives.
func (c *Claude) findSession(root, sid string) string {
	k := root + "\x00" + sid
	if v, ok := c.found[k]; ok {
		return v
	}
	matches, _ := filepath.Glob(filepath.Join(root, "projects", "*", sid+".jsonl"))
	if len(matches) == 0 {
		return ""
	}
	v := filepath.Base(filepath.Dir(matches[0])) + "/" + sid
	c.found[k] = v
	return v
}

// Related: a hook points at <session>.jsonl or at a subagent file inside <session>/, so sync the
// lot, plus the short-lived per-session files (file history, todos, tasks, team mailboxes)
// that Claude Code deletes once the session or team is done.
func (c *Claude) Related(sf SessionFile, root string) []string {
	slug, sid, ok := strings.Cut(sf.SessionKey, "/")
	if !ok {
		return nil
	}
	projectDir := filepath.Join(root, "projects", slug)
	main := filepath.Join(projectDir, sid+".jsonl")
	var out []string
	if isFile(main) {
		out = append(out, main)
	}
	out = append(out, FilesUnder(filepath.Join(projectDir, sid))...)
	if matches, err := filepath.Glob(filepath.Join(projectDir, globEscape(sid)+".*")); err == nil {
		for _, p := range matches {
			if p != main && isFile(p) {
				out = append(out, p)
			}
		}
	}
	out = append(out, FilesUnder(filepath.Join(root, "file-history", sid), filepath.Join(root, "session-env", sid),
		filepath.Join(root, "tasks"), filepath.Join(root, "teams"))...)
	if matches, err := filepath.Glob(filepath.Join(root, "todos", globEscape(sid)+"*")); err == nil {
		for _, p := range matches {
			if isFile(p) {
				out = append(out, p)
			}
		}
	}
	if debug := filepath.Join(root, "debug", sid+".txt"); isFile(debug) {
		out = append(out, debug)
	}
	return out
}

func globEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, "*", `\*`, "?", `\?`, "[", `\[`).Replace(s)
}

// UserBase is the root itself.
func (c *Claude) UserBase(root string) string { return root }

func (c *Claude) claudeJSON(root string) string {
	if root != c.CanonicalRoot() {
		return filepath.Join(root, ".claude.json")
	}
	return filepath.Join(c.home, ".claude.json")
}

// Exports adds database copies and ~/.claude.json without its API key.
func (c *Claude) Exports(root, workDir string, interval time.Duration) []SessionFile {
	out := SQLiteExports(c, root, workDir, interval)
	data, err := os.ReadFile(c.claudeJSON(root))
	if err != nil {
		return out
	}
	var doc any
	if jsonx.Decode(data, &doc) != nil {
		return out
	}
	if m, ok := doc.(map[string]any); ok {
		delete(m, "primaryApiKey")
	}
	body, err := jsonx.Indent(doc, 2)
	if err != nil {
		return out
	}
	dest := filepath.Join(workDir, c.Name(), RootTag(root), "claude.json")
	// Claude Code rewrites this file on every start (counters, tips, per-project state), so
	// like a database it is refreshed at most every interval to keep versions few.
	old, rerr := os.ReadFile(dest)
	st, serr := os.Stat(dest)
	if rerr != nil || serr != nil || (!bytes.Equal(old, body) && time.Since(st.ModTime()) >= interval) {
		if fsutil.WriteFileAtomic(dest, body, 0o600) != nil {
			return out
		}
	}
	return append(out, SessionFile{Agent: c.Name(), Path: dest, Key: KeyPrefix(c, root) + "claude.json", Role: "config"})
}

// ExtraSnapshotEntries: user- and project-scoped MCP servers live in ~/.claude.json next to UI
// state that changes every run, so the harness set records only the MCP parts.
func (c *Claude) ExtraSnapshotEntries(root, projectRoot string) []Entry {
	data, err := os.ReadFile(c.claudeJSON(root))
	if err != nil {
		return nil
	}
	m, ok := jsonx.Map(data)
	if !ok {
		return nil
	}
	servers := m["mcpServers"]
	if !truthy(servers) {
		servers = map[string]any{}
	}
	picked := map[string]any{"mcpServers": servers}
	if projects, ok := m["projects"].(map[string]any); ok && projectRoot != "" {
		if proj, ok := projects[projectRoot].(map[string]any); ok {
			sub := map[string]any{}
			for _, k := range []string{"mcpServers", "enabledMcpjsonServers", "disabledMcpjsonServers", "allowedTools"} {
				if v, ok := proj[k]; ok {
					sub[k] = v
				}
			}
			picked["project"] = sub
		}
	}
	body, err := jsonx.Indent(picked, 1)
	if err != nil {
		return nil
	}
	if body, err = scrub.File(c.Name(), ".claude.json", body); err != nil {
		return nil
	}
	return []Entry{{Scope: "user", Name: ".claude.json#mcp", Body: bytes.TrimSuffix(body, []byte("\n"))}}
}

// MemoryDirs: agent-memory/ (user and project) and the project's auto memory.
func (c *Claude) MemoryDirs(root, projectRoot, sessionKey string) []ScopedDir {
	out := []ScopedDir{{Name: "user", Dir: filepath.Join(root, "agent-memory")}}
	if slug, _, ok := strings.Cut(sessionKey, "/"); ok {
		out = append(out, ScopedDir{Name: "auto", Dir: filepath.Join(root, "projects", slug, "memory")})
	}
	if projectRoot != "" {
		out = append(out, ScopedDir{Name: "project", Dir: filepath.Join(projectRoot, ".claude", "agent-memory")})
	}
	return out
}

// PluginDirs reads installed_plugins.json.
func (c *Claude) PluginDirs(root string) []ScopedDir {
	data, err := os.ReadFile(filepath.Join(root, "plugins", "installed_plugins.json"))
	if err != nil {
		return nil
	}
	m, ok := jsonx.Map(data)
	if !ok {
		return nil
	}
	plugins, _ := m["plugins"].(map[string]any)
	var out []ScopedDir
	for _, name := range sortedKeys(plugins) {
		installs, ok := plugins[name].([]any)
		if !ok {
			installs = []any{plugins[name]}
		}
		for _, inst := range installs {
			im, _ := inst.(map[string]any)
			if p := str(im, "installPath"); p != "" && isDir(p) {
				out = append(out, ScopedDir{Name: name, Dir: p})
			}
		}
	}
	return out
}

// SessionInfo reads ids, cwd, branch and version from a transcript's first lines.
func (c *Claude) SessionInfo(head []map[string]any) map[string]any {
	info := map[string]any{}
	fields := [][2]string{
		{"sessionId", "session_id"},
		{"agentId", "agent_id"},
		{"cwd", "cwd"},
		{"gitBranch", "git_branch"},
		{"version", "agent_version"},
		{"entrypoint", "entrypoint"},
	}
	for _, d := range head {
		for _, f := range fields {
			if _, done := info[f[1]]; !done && truthy(d[f[0]]) {
				info[f[1]] = d[f[0]]
			}
		}
	}
	return info
}
