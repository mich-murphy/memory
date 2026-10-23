package adapters

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/mich-murphy/memory/internal/capturepolicy"
	"github.com/mich-murphy/memory/internal/config"
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
	claudeRoles = map[string]Role{
		"history.jsonl": RoleHistory, "settings.json": RoleConfig, "settings.local.json": RoleConfig,
		"CLAUDE.md": RoleConfig, "keybindings.json": RoleConfig, "launcher-settings.json": RoleConfig,
		"remote-settings.json": RoleConfig, "policy-limits.json": RoleConfig, "plans/": RolePlan, "teams/": RoleTeam,
		"paste-cache/": RolePaste, "usage-data/": RoleUsage, "agent-memory/": RoleMemory, "skills/": RoleConfig,
		"agents/": RoleConfig, "commands/": RoleConfig, "rules/": RoleConfig, "output-styles/": RoleConfig,
		"hooks/": RoleConfig, "plugins/": RoleConfig, "sessions/": RoleLiveness,
	}
)

type Claude struct {
	layout
	// slugs maps each root's session ids to their project slugs, read once per root: a glob
	// per id lists every project, for each of the files a session leaves outside its project.
	slugs map[string]map[string]string
}

func NewClaude(home string) *Claude {
	return &Claude{
		name: "claude", home: home, dir: ".claude", dirEnv: "CLAUDE_CONFIG_DIR",
		// plugins/cache and plugins/marketplaces are git checkouts of plugin sources (snapshots
		// record the installed plugins); local/ and downloads/ hold Claude Code's own install;
		// backups/ holds copies of ~/.claude.json, API key included.
		skipDirs: []string{"plugins/cache", "plugins/marketplaces", "local", "downloads", "backups", "statsig", "ide", "telemetry"},
		managedSettings: []string{
			"/etc/claude-code/managed-settings.json",
			"/etc/claude-code/managed-mcp.json",
			"/etc/claude-code/CLAUDE.md",
			"/Library/Application Support/ClaudeCode/managed-settings.json",
			"/Library/Application Support/ClaudeCode/managed-mcp.json",
			"/Library/Application Support/ClaudeCode/CLAUDE.md",
		},
		rules: SnapshotRules{
			User: []string{
				"CLAUDE.md", "settings.json", "settings.local.json", "launcher-settings.json", "keybindings.json",
				"rules/**", "skills/**", "agents/**", "commands/**", "output-styles/**", "hooks/**",
				"plugins/installed_plugins.json",
			},
			Project: []string{
				"CLAUDE.md", "CLAUDE.local.md", "AGENTS.md", "**/CLAUDE.md", "**/CLAUDE.local.md", "**/AGENTS.md",
				".claude/settings.json", ".claude/settings.local.json", ".claude/rules/**", ".claude/skills/**",
				".claude/agents/**", ".claude/commands/**", ".claude/output-styles/**", ".claude/hooks/**", ".mcp.json",
			},
		},
		slugs: map[string]map[string]string{},
	}
}

func (c *Claude) InferRoot(path string) string {
	for dir := filepath.Dir(path); dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		if filepath.Base(dir) == "projects" {
			if rel, ok := relative(path, dir); ok && strings.Count(rel, "/") >= 1 {
				return filepath.Dir(dir)
			}
		}
	}
	return ""
}

func (c *Claude) Classify(path, root string) (SessionFile, bool) {
	rel, ok := relative(path, root)
	if !ok || isSQLite(filepath.Base(path)) || skipped(filepath.Base(path)) {
		return SessionFile{}, false
	}
	parts := strings.Split(rel, "/")
	sf := func(sessionKey string, role Role) (SessionFile, bool) {
		return SessionFile{Agent: c.Name(), Root: root, Path: path, Key: keyPrefix(c, root) + rel, SessionKey: prefixedSession(c, root, sessionKey), Role: role}, true
	}
	top := parts[0]
	if len(parts) == 1 && hasAnySuffix(top, []string{".sh", ".py", ".js", ".ts"}) {
		return sf("", RoleConfig)
	}
	if top == "projects" && len(parts) > 1 {
		return c.classifyProject(parts, sf)
	}
	if role, ok := claudePerSessionDirs[top]; ok && len(parts) > 1 {
		if sid := uuidPrefixRE.FindString(parts[1]); sid != "" {
			return sf(c.sessionKey(root, sid), role)
		}
		return sf("", role)
	}
	return sf("", topRole(claudeRoles, rel))
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
func (c *Claude) Related(sf SessionFile) Discovery {
	out := Discovery{Complete: true}
	localKey := strings.TrimPrefix(sf.SessionKey, keyPrefix(c, sf.Root))
	slug, sid, ok := strings.Cut(localKey, "/")
	if !ok || slug == "" || sid == "" {
		return out
	}
	project := filepath.Join(sf.Root, "projects", slug)
	sources := []string{
		filepath.Join(project, sid+".jsonl"), filepath.Join(project, sid),
		filepath.Join(sf.Root, "file-history", sid), filepath.Join(sf.Root, "session-env", sid),
		filepath.Join(sf.Root, "tasks"), filepath.Join(sf.Root, "teams"), filepath.Join(sf.Root, "debug", sid+".txt"),
	}
	for _, matched := range []struct{ dir, pattern string }{
		{project, globEscape(sid) + ".*"}, {filepath.Join(sf.Root, "todos"), globEscape(sid) + "*"},
	} {
		sources = append(sources, out.matches(matched.dir, matched.pattern)...)
	}
	seen := map[string]bool{}
	for _, source := range sources {
		if seen[source] {
			continue
		}
		seen[source] = true
		found := discoverUnder(c, sf.Root, source)
		out.Files = append(out.Files, found.Files...)
		out.Issues = append(out.Issues, found.Issues...)
		out.Skipped = append(out.Skipped, found.Skipped...)
		out.Complete = out.Complete && found.Complete
	}
	return out
}

// Unlike filepath.Glob, matching related source names must report directory read failures.
func (d *Discovery) matches(dir, pattern string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			d.issue(dir, "read", err)
		}
		return nil
	}
	var paths []string
	for _, entry := range entries {
		match, err := filepath.Match(pattern, entry.Name())
		if err != nil {
			d.issue(dir, "walk", err)
			return paths
		}
		if match {
			paths = append(paths, filepath.Join(dir, entry.Name()))
		}
	}
	return paths
}

func globEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, "*", `\*`, "?", `\?`, "[", `\[`).Replace(s)
}

func (c *Claude) claudeJSON(root string) string {
	if root != c.CanonicalRoot() {
		return filepath.Join(root, ".claude.json")
	}
	return filepath.Join(c.home, ".claude.json")
}

func (c *Claude) Exports(root, workDir string, interval time.Duration) Discovery {
	out := Discovery{Complete: true}
	source := c.claudeJSON(root)
	data, identities, err := readClaudeConfig(source)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			out.issue(source, "read", err)
		}
		return out
	}
	// Scrubbed here as well as at upload, so the copy in the state dir holds no API key either.
	body, err := scrub.Identities(c.Name(), identities, data)
	if err != nil {
		out.issue(source, "export", err)
		return out
	}
	dest := filepath.Join(workDir, c.Name(), rootTag(root), "claude.json")
	// Claude Code rewrites this file on every start, so like a database it is refreshed at
	// most every interval to keep versions few.
	old, rerr := os.ReadFile(dest)
	st, serr := os.Stat(dest)
	if rerr != nil || serr != nil || (!bytes.Equal(old, body) && time.Since(st.ModTime()) >= interval) {
		if err := fsutil.WriteFileAtomic(dest, body, 0o600); err != nil {
			out.issue(source, "export", err)
			return out
		}
	}
	out.Files = append(out.Files, SessionFile{Agent: c.Name(), Root: root, Path: dest, SourcePath: source, Key: keyPrefix(c, root) + "claude.json", Role: RoleConfig})
	return out
}

// ExtraSnapshotEntries records only the MCP parts of ~/.claude.json, whose UI state changes
// every run.
func (c *Claude) ExtraSnapshotEntries(root, projectRoot string) Sources {
	return c.SnapshotMCP(root, projectRoot, c.claudeJSON(root), readClaudeConfig)
}

// SnapshotMCP uses the caller-authorized original reader, never installed collector policy.
func (c *Claude) SnapshotMCP(_, projectRoot, source string, read func(string) ([]byte, []string, error)) Sources {
	out := Sources{Complete: true}
	data, identities, err := read(source)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			out.issue(source, "read", err)
		}
		return out
	}
	// Validate/redact the original document before selecting a subset. Reserialisation must
	// not conceal duplicate keys (including escaped or nested duplicates) in the source.
	data, err = scrub.Identities(c.Name(), identities, data)
	if err != nil {
		out.issue(source, "unparseable", err)
		return out
	}
	m, ok := jsonx.Map(data)
	if !ok {
		out.issue(source, "unparseable", errors.New("expected JSON object"))
		return out
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
		out.issue(source, "export", err)
		return out
	}
	out.Entries = []Entry{{Scope: "user", Name: ".claude.json#mcp", Body: bytes.TrimSuffix(body, []byte("\n"))}}
	return out
}

func (c *Claude) MemoryDirs(root, projectRoot, sessionKey string) []ScopedDir {
	out := []ScopedDir{{Scope: "user", Dir: filepath.Join(root, "agent-memory")}}
	if slug, _, ok := strings.Cut(strings.TrimPrefix(sessionKey, keyPrefix(c, root)), "/"); ok {
		out = append(out, ScopedDir{Scope: "auto", Dir: filepath.Join(root, "projects", slug, "memory")})
	}
	if projectRoot != "" {
		out = append(out, ScopedDir{Scope: "project", Dir: filepath.Join(projectRoot, ".claude", "agent-memory")})
	}
	return out
}

func (c *Claude) Plugins(root string) Sources {
	return c.SnapshotPlugins(root, snapshotSource)
}

// SnapshotPlugins reads its manifest through the caller-authorized reader.
func (c *Claude) SnapshotPlugins(root string, read func(string) ([]byte, error)) Sources {
	out := Sources{Complete: true}
	source := filepath.Join(root, "plugins", "installed_plugins.json")
	data, err := read(source)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			out.issue(source, "read", err)
		}
		return out
	}
	m, ok := jsonx.Map(data)
	if !ok {
		out.issue(source, "unparseable", errors.New("expected JSON object"))
		return out
	}
	plugins, ok := m["plugins"].(map[string]any)
	if !ok {
		out.issue(source, "unparseable", errors.New("expected plugins object"))
		return out
	}
	for _, name := range slices.Sorted(maps.Keys(plugins)) {
		installs, ok := plugins[name].([]any)
		if !ok {
			installs = []any{plugins[name]}
		}
		for _, inst := range installs {
			im, _ := inst.(map[string]any)
			p := str(im, "installPath")
			if p == "" {
				out.issue(source, "unparseable", fmt.Errorf("plugin %s has no install path", name))
				continue
			}
			st, err := os.Stat(p)
			if err != nil {
				out.issue(p, "stat", err)
				continue
			}
			if !st.IsDir() {
				out.issue(p, "stat", errors.New("plugin is not a directory"))
				continue
			}
			out.Plugins = append(out.Plugins, Plugin{Name: name, Dir: p})
		}
	}
	return out
}

// Native .claude.json is an intentional export, not an unrestricted credential exception.
// The shared policy checks every identity before reading its original bytes.
func readClaudeConfig(path string) ([]byte, []string, error) {
	policy, err := capturepolicy.New(config.FilePath(), config.StateDir())
	if err != nil {
		return nil, nil, err
	}
	resolution, err := capturepolicy.Resolve(path)
	if policy.ExcludesClaudeConfig(resolution.Identities) {
		return nil, nil, errors.New("claude configuration source excluded by capture policy")
	}
	if err != nil {
		return nil, nil, err
	}
	body, err := snapshotSource(resolution.Path)
	return body, resolution.Identities, err
}

// Snapshot configuration sources are small regular files. Bound the read before parsing,
// including sources replaced with a FIFO or grown between stat and read.
func snapshotSource(path string) ([]byte, error) {
	const limit = 5 << 20
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // read-only
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, errors.New("snapshot source is not a regular file")
	}
	if st.Size() > limit {
		return nil, errors.New("snapshot source exceeds size limit")
	}
	body, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if len(body) > limit {
		return nil, errors.New("snapshot source exceeds size limit")
	}
	return body, nil
}

var claudeInfo = map[string]string{
	"session_id": "sessionId", "agent_id": "agentId", "cwd": "cwd", "git_branch": "gitBranch",
	"agent_version": "version", "entrypoint": "entrypoint",
}

func (c *Claude) SessionInfo(head []map[string]any) map[string]any {
	info := map[string]any{}
	for _, d := range head {
		fill(info, d, claudeInfo)
	}
	if parent := str(info, "session_id"); str(info, "agent_id") != "" {
		setLineage(info, "subagent", parent)
	}
	firstEvent("claude", info, head)
	observeEvents("claude", info, head)
	return info
}
