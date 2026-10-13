package adapters

import (
	"bytes"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
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

	claudeSessionSubdirRoles = map[string]Role{"subagents": RoleSubagent, "tool-results": RoleToolResult}
	claudePerSessionDirs     = map[string]Role{
		"file-history": RoleFileHistory, "session-env": RoleSessionEnv, "debug": RoleDebug, "todos": RoleTodo,
		"tasks": RoleTask,
	}
	claudeTopLevelRoles = map[string]Role{
		"plans": RolePlan, "teams": RoleTeam, "paste-cache": RolePaste, "usage-data": RoleUsage,
		"agent-memory": RoleMemory, "skills": RoleConfig, "agents": RoleConfig, "commands": RoleConfig,
		"rules": RoleConfig, "output-styles": RoleConfig, "hooks": RoleConfig, "plugins": RoleConfig,
		"sessions": RoleLiveness,
	}
	claudeConfigFiles = map[string]bool{
		"settings.json": true, "settings.local.json": true, "CLAUDE.md": true, "keybindings.json": true,
		"launcher-settings.json": true, "remote-settings.json": true, "policy-limits.json": true,
	}
)

type Claude struct {
	home string
	// slugs maps each root's session ids to their project slugs, read once per root: a glob
	// per id lists every project, for each of the files a session leaves outside its project.
	slugs map[string]map[string]string
}

func NewClaude(home string) *Claude {
	return &Claude{home: home, slugs: map[string]map[string]string{}}
}

func (c *Claude) Name() string { return "claude" }

func (c *Claude) Home() string { return c.home }

// SkipDirs: plugins/cache and plugins/marketplaces are git checkouts of plugin sources
// (snapshots record the installed plugins); local/ and downloads/ hold Claude Code's own
// install; backups/ holds copies of ~/.claude.json, API key included.
func (c *Claude) SkipDirs() []string {
	return []string{"plugins/cache", "plugins/marketplaces", "local", "downloads", "backups", "statsig", "ide", "telemetry"}
}

func (c *Claude) ManagedSettingsFiles() []string {
	return []string{
		"/etc/claude-code/managed-settings.json",
		"/etc/claude-code/managed-mcp.json",
		"/etc/claude-code/CLAUDE.md",
		"/Library/Application Support/ClaudeCode/managed-settings.json",
		"/Library/Application Support/ClaudeCode/managed-mcp.json",
		"/Library/Application Support/ClaudeCode/CLAUDE.md",
	}
}

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

func (c *Claude) CanonicalRoot() string { return filepath.Join(c.home, ".claude") }

func (c *Claude) DefaultRoots() []string {
	if p := envPath("CLAUDE_CONFIG_DIR", c.home); p != "" {
		return []string{p}
	}
	return []string{c.CanonicalRoot()}
}

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

func (c *Claude) Classify(path, root string) (SessionFile, bool) {
	rel, ok := Rel(path, root)
	if !ok {
		return SessionFile{}, false
	}
	parts := strings.Split(rel, "/")
	sf := func(sessionKey string, role Role) (SessionFile, bool) {
		return SessionFile{Agent: c.Name(), Path: path, Key: KeyPrefix(c, root) + rel, SessionKey: sessionKey, Role: role}, true
	}
	top := parts[0]
	if len(parts) == 1 {
		if top == "history.jsonl" {
			return sf("", RoleHistory)
		}
		if claudeConfigFiles[top] || hasAnySuffix(top, []string{".sh", ".py", ".js", ".ts"}) {
			return sf("", RoleConfig)
		}
		return sf("", RoleUnknown)
	}
	if top == "projects" {
		return c.classifyProject(parts, sf)
	}
	if role, ok := claudePerSessionDirs[top]; ok {
		if sid := uuidPrefixRE.FindString(parts[1]); sid != "" {
			return sf(c.sessionKey(root, sid), role)
		}
		return sf("", role)
	}
	if role, ok := claudeTopLevelRoles[top]; ok {
		return sf("", role)
	}
	return sf("", RoleUnknown)
}

func (c *Claude) classifyProject(parts []string, sf func(string, Role) (SessionFile, bool)) (SessionFile, bool) {
	if len(parts) < 3 {
		return sf("", RoleUnknown)
	}
	slug, name := parts[1], parts[2]
	if len(parts) == 3 {
		if m := claudeMainRE.FindStringSubmatch(name); m != nil {
			return sf(slug+"/"+m[1], RoleMain)
		}
		if m := claudeSetAsideRE.FindStringSubmatch(name); m != nil {
			return sf(slug+"/"+m[1], RoleSuperseded)
		}
		return sf("", RoleUnknown)
	}
	if name == "memory" {
		return sf("", RoleMemory)
	}
	role := RoleAux
	if len(parts) > 4 {
		if r, ok := claudeSessionSubdirRoles[parts[3]]; ok {
			role = r
		}
	}
	return sf(slug+"/"+name, role)
}

func (c *Claude) sessionKey(root, sid string) string {
	slugs, ok := c.slugs[root]
	if !ok {
		slugs = sessionSlugs(filepath.Join(root, "projects"))
		c.slugs[root] = slugs
	}
	if slug, ok := slugs[sid]; ok {
		return slug + "/" + sid
	}
	// Started since the index was read.
	matches, _ := filepath.Glob(filepath.Join(root, "projects", "*", sid+".jsonl"))
	if len(matches) == 0 {
		return ""
	}
	return filepath.Base(filepath.Dir(matches[0])) + "/" + sid
}

// sessionSlugs keeps the first slug in name order for an id, as a glob would.
func sessionSlugs(projects string) map[string]string {
	out := map[string]string{}
	dirs, _ := os.ReadDir(projects) // no projects yet: nothing to index
	for _, d := range dirs {
		entries, _ := os.ReadDir(filepath.Join(projects, d.Name())) // not a directory: nothing to index
		for _, e := range entries {
			if sid, ok := strings.CutSuffix(e.Name(), ".jsonl"); ok {
				if _, seen := out[sid]; !seen {
					out[sid] = d.Name()
				}
			}
		}
	}
	return out
}

// Related includes the per-session files (file history, todos, tasks, team mailboxes) that
// Claude Code deletes once the session or team is done.
func (c *Claude) Related(sf SessionFile, root string) []string {
	slug, sid, ok := strings.Cut(sf.SessionKey, "/")
	if !ok {
		return nil
	}
	projectDir := filepath.Join(root, "projects", slug)
	main := filepath.Join(projectDir, sid+".jsonl")
	var out []string
	if fsutil.IsFile(main) {
		out = append(out, main)
	}
	out = append(out, FilesUnder(filepath.Join(projectDir, sid))...)
	if matches, err := filepath.Glob(filepath.Join(projectDir, globEscape(sid)+".*")); err == nil {
		for _, p := range matches {
			if p != main && fsutil.IsFile(p) {
				out = append(out, p)
			}
		}
	}
	out = append(out, FilesUnder(filepath.Join(root, "file-history", sid), filepath.Join(root, "session-env", sid),
		filepath.Join(root, "tasks"), filepath.Join(root, "teams"))...)
	if matches, err := filepath.Glob(filepath.Join(root, "todos", globEscape(sid)+"*")); err == nil {
		for _, p := range matches {
			if fsutil.IsFile(p) {
				out = append(out, p)
			}
		}
	}
	if debug := filepath.Join(root, "debug", sid+".txt"); fsutil.IsFile(debug) {
		out = append(out, debug)
	}
	return out
}

func globEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, "*", `\*`, "?", `\?`, "[", `\[`).Replace(s)
}

func (c *Claude) UserBase(root string) string { return root }

func (c *Claude) claudeJSON(root string) string {
	if root != c.CanonicalRoot() {
		return filepath.Join(root, ".claude.json")
	}
	return filepath.Join(c.home, ".claude.json")
}

func (c *Claude) Exports(root, workDir string, interval time.Duration) []SessionFile {
	var out []SessionFile
	data, err := os.ReadFile(c.claudeJSON(root))
	if err != nil {
		return out
	}
	// Scrubbed here as well as at upload, so the copy in the state dir holds no API key either.
	body, err := scrub.File(c.Name(), ".claude.json", data)
	if err != nil {
		return out
	}
	dest := filepath.Join(workDir, c.Name(), RootTag(root), "claude.json")
	// Claude Code rewrites this file on every start, so like a database it is refreshed at
	// most every interval to keep versions few.
	old, rerr := os.ReadFile(dest)
	st, serr := os.Stat(dest)
	if rerr != nil || serr != nil || (!bytes.Equal(old, body) && time.Since(st.ModTime()) >= interval) {
		if fsutil.WriteFileAtomic(dest, body, 0o600) != nil {
			return out
		}
	}
	return append(out, SessionFile{Agent: c.Name(), Path: dest, Key: KeyPrefix(c, root) + "claude.json", Role: RoleConfig})
}

// ExtraSnapshotEntries records only the MCP parts of ~/.claude.json, whose UI state changes
// every run.
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

func (c *Claude) MemoryDirs(root, projectRoot, sessionKey string) []ScopedDir {
	out := []ScopedDir{{Scope: "user", Dir: filepath.Join(root, "agent-memory")}}
	if slug, _, ok := strings.Cut(sessionKey, "/"); ok {
		out = append(out, ScopedDir{Scope: "auto", Dir: filepath.Join(root, "projects", slug, "memory")})
	}
	if projectRoot != "" {
		out = append(out, ScopedDir{Scope: "project", Dir: filepath.Join(projectRoot, ".claude", "agent-memory")})
	}
	return out
}

func (c *Claude) Plugins(root string) []Plugin {
	data, err := os.ReadFile(filepath.Join(root, "plugins", "installed_plugins.json"))
	if err != nil {
		return nil
	}
	m, ok := jsonx.Map(data)
	if !ok {
		return nil
	}
	plugins, _ := m["plugins"].(map[string]any)
	var out []Plugin
	for _, name := range slices.Sorted(maps.Keys(plugins)) {
		installs, ok := plugins[name].([]any)
		if !ok {
			installs = []any{plugins[name]}
		}
		for _, inst := range installs {
			im, _ := inst.(map[string]any)
			if p := str(im, "installPath"); p != "" && fsutil.IsDir(p) {
				out = append(out, Plugin{Name: name, Dir: p})
			}
		}
	}
	return out
}

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
