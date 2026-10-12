package adapters

// Pi coding agent (earendil-works/pi; npm @earendil-works/pi-coding-agent).
//
// Layout (checked against packages/coding-agent/docs/session-format.md and session-manager.ts,
// 2026-10-10):
//
//	<agent-dir>/sessions/--<cwd encoded>--/<timestamp>_<session-id>.jsonl
//	<sessionDir>/<timestamp>_<session-id>.jsonl   when --session-dir, PI_CODING_AGENT_SESSION_DIR
//	                                              or the sessionDir setting picks another place
//
// Format v3: the first line is {"type":"session","version":3,"id",...,"cwd","parentSession"?}
// and entries form a tree via id/parentId. Append-only; /tree branches stay in the same file,
// /fork and /clone write new files. The file is created only once the session has a user or
// assistant message, and --no-session runs never write one. PI_CODING_AGENT_DIR moves the agent
// dir (default ~/.pi/agent).
//
// The whole agent dir is captured (settings, models, mcp.json, extensions, prompts, skills,
// themes, experimental sessions, the debug and MCP logs) except auth.json, oauth.json,
// mcp-auth.json and the fd/rg binaries in bin/. A session dir outside the agent dir only
// contributes .jsonl files.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mich-murphy/memory/internal/jsonx"
)

var (
	piTopLevelRoles = map[string]string{
		"extensions": "config", "prompts": "config", "skills": "config", "themes": "config", "tools": "config",
		"experimental": "experimental",
	}
	piConfigFiles = map[string]bool{
		"settings.json": true, "models.json": true, "models-store.json": true, "mcp.json": true, "keybindings.json": true,
		"AGENTS.md": true, "AGENTS.override.md": true, "CLAUDE.md": true, "SYSTEM.md": true, "APPEND_SYSTEM.md": true,
	}
)

// Pi is the Pi coding agent adapter.
type Pi struct {
	home string
}

// NewPi returns the Pi adapter for a home directory.
func NewPi(home string) *Pi { return &Pi{home: home} }

// Name is "pi".
func (p *Pi) Name() string { return "pi" }

// Home is the user's home directory.
func (p *Pi) Home() string { return p.home }

// SkipDirs: Pi's bundled fd and rg binaries.
func (p *Pi) SkipDirs() []string { return []string{"bin"} }

// ManagedPaths: none.
func (p *Pi) ManagedPaths() []string { return nil }

// Rules are the harness files a snapshot records.
func (p *Pi) Rules() SnapshotRules {
	return SnapshotRules{
		User: []string{
			"AGENTS.override.md", "AGENTS.md", "CLAUDE.md", "SYSTEM.md", "APPEND_SYSTEM.md", "settings.json", "mcp.json",
			"models.json", "skills/**", "prompts/**", "extensions/**",
		},
		Project: []string{
			"AGENTS.override.md", "AGENTS.md", "CLAUDE.md", "**/AGENTS.md", "**/CLAUDE.md", ".pi/settings.json",
			".pi/mcp.json", ".pi/SYSTEM.md", ".pi/APPEND_SYSTEM.md", ".pi/skills/**", ".pi/prompts/**",
			".pi/extensions/**", ".agents/skills/**",
		},
		Home: []string{".agents/skills/**"},
	}
}

// AgentDir is $PI_CODING_AGENT_DIR or ~/.pi/agent.
func (p *Pi) AgentDir() string {
	if d := envPath("PI_CODING_AGENT_DIR", p.home); d != "" {
		return d
	}
	return p.CanonicalRoot()
}

// CanonicalRoot is ~/.pi/agent.
func (p *Pi) CanonicalRoot() string { return filepath.Join(p.home, ".pi", "agent") }

// DefaultRoots is the agent dir plus any session dir set by environment or an absolute
// sessionDir setting. A session dir inside the agent dir is already covered by walking it.
func (p *Pi) DefaultRoots() []string {
	agentDir := p.AgentDir()
	roots := []string{agentDir}
	if d := envPath("PI_CODING_AGENT_SESSION_DIR", p.home); d != "" {
		roots = append(roots, d)
	}
	if data, err := os.ReadFile(filepath.Join(agentDir, "settings.json")); err == nil {
		if m, ok := jsonx.Map(data); ok {
			// Relative values resolve per working directory; hooks report those.
			if s := expandUser(str(m, "sessionDir"), p.home); s != "" && filepath.IsAbs(s) {
				roots = append(roots, filepath.Clean(s))
			}
		}
	}
	var out []string
	for _, r := range roots {
		if (r == agentDir || !withinDir(r, agentDir)) && !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	return out
}

func withinDir(path, root string) bool {
	_, ok := Rel(path, root)
	return ok || path == root
}

func (p *Pi) isAgentDir(root string) bool {
	return root == p.CanonicalRoot() || root == p.AgentDir() || isDir(filepath.Join(root, "sessions"))
}

// InferRoot: <agent dir>/sessions/--cwd--/file.jsonl gives the agent dir, so a custom
// PI_CODING_AGENT_DIR gets the same keys from hooks and sweeps; any other session dir is its
// own root.
func (p *Pi) InferRoot(path string) string {
	if filepath.Ext(path) != ".jsonl" {
		return ""
	}
	parent := filepath.Dir(path)
	name := filepath.Base(parent)
	if len(name) >= 4 && strings.HasPrefix(name, "--") && strings.HasSuffix(name, "--") {
		grand := filepath.Dir(parent)
		if filepath.Base(grand) == "sessions" {
			return filepath.Dir(grand)
		}
		return grand
	}
	return parent
}

// Classify maps a path under root to its key, session and role.
func (p *Pi) Classify(path, root string) (SessionFile, bool) {
	rel, ok := Rel(path, root)
	if !ok {
		return SessionFile{}, false
	}
	parts := strings.Split(rel, "/")
	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	isJSONL := filepath.Ext(path) == ".jsonl"
	if p.isAgentDir(root) {
		prefix := KeyPrefix(p, root)
		if parts[0] == "sessions" && isJSONL && len(parts) <= 3 {
			return SessionFile{Agent: p.Name(), Path: path, Key: prefix + rel, SessionKey: stem, Role: "main"}, true
		}
		role := "unknown"
		switch {
		case len(parts) == 1 && piConfigFiles[parts[0]]:
			role = "config"
		case len(parts) == 1 && strings.HasSuffix(parts[0], ".log"):
			role = "log"
		case len(parts) > 1:
			if r, ok := piTopLevelRoles[parts[0]]; ok {
				role = r
			}
		}
		return SessionFile{Agent: p.Name(), Path: path, Key: prefix + rel, Role: role}, true
	}
	if !isJSONL || len(parts) > 2 {
		return SessionFile{}, false
	}
	return SessionFile{Agent: p.Name(), Path: path, Key: "dirs/" + RootTag(root) + "/" + rel, SessionKey: stem, Role: "main"}, true
}

// Related: a Pi session is one file.
func (p *Pi) Related(SessionFile, string) []string { return nil }

// UserBase is the agent dir, even for a session dir root.
func (p *Pi) UserBase(root string) string {
	if p.isAgentDir(root) {
		return root
	}
	return p.AgentDir()
}

// ExtraSnapshotEntries: none for Pi.
func (p *Pi) ExtraSnapshotEntries(string, string) []Entry { return nil }

// MemoryDirs: none for Pi.
func (p *Pi) MemoryDirs(string, string, string) []ScopedDir { return nil }

// PluginDirs: none for Pi.
func (p *Pi) PluginDirs(string) []ScopedDir { return nil }

// Exports: database copies.
func (p *Pi) Exports(root, workDir string, interval time.Duration) []SessionFile {
	return SQLiteExports(p, root, workDir, interval)
}

// SessionInfo reads the session header line.
func (p *Pi) SessionInfo(head []map[string]any) map[string]any {
	for _, d := range head {
		if d["type"] != "session" {
			continue
		}
		info := map[string]any{}
		for _, f := range [][2]string{{"session_id", "id"}, {"cwd", "cwd"}, {"format_version", "version"}, {"parent_session", "parentSession"}} {
			if truthy(d[f[1]]) {
				info[f[0]] = d[f[1]]
			}
		}
		return info
	}
	return map[string]any{}
}
