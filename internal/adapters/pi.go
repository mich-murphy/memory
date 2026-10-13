package adapters

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mich-murphy/memory/internal/jsonx"
)

var (
	piTopLevelRoles = map[string]Role{
		"extensions": RoleConfig, "prompts": RoleConfig, "skills": RoleConfig, "themes": RoleConfig,
		"tools": RoleConfig, "experimental": RoleExperimental,
	}
	piConfigFiles = map[string]bool{
		"settings.json": true, "models.json": true, "models-store.json": true, "mcp.json": true, "keybindings.json": true,
		"AGENTS.md": true, "AGENTS.override.md": true, "CLAUDE.md": true, "SYSTEM.md": true, "APPEND_SYSTEM.md": true,
	}
)

type Pi struct {
	home string
}

func NewPi(home string) *Pi { return &Pi{home: home} }

func (p *Pi) Name() string { return "pi" }

func (p *Pi) Home() string { return p.home }

func (p *Pi) SkipDirs() []string { return []string{"bin"} }

func (p *Pi) ManagedSettingsFiles() []string { return nil }

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

func (p *Pi) AgentDir() string {
	if d := envPath("PI_CODING_AGENT_DIR", p.home); d != "" {
		return d
	}
	return p.CanonicalRoot()
}

func (p *Pi) CanonicalRoot() string { return filepath.Join(p.home, ".pi", "agent") }

func (p *Pi) DefaultRoots() []string {
	agentDir := p.AgentDir()
	roots := []string{agentDir}
	if d := envPath("PI_CODING_AGENT_SESSION_DIR", p.home); d != "" {
		roots = append(roots, d)
	}
	if data, err := os.ReadFile(filepath.Join(agentDir, "settings.json")); err == nil {
		if m, ok := jsonx.Map(data); ok {
			// A relative sessionDir resolves per working directory; hooks report those.
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

// InferRoot maps <agent dir>/sessions/--cwd--/file.jsonl to the agent dir, so a custom
// PI_CODING_AGENT_DIR gets the same keys from hooks and sweeps.
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
			return SessionFile{Agent: p.Name(), Path: path, Key: prefix + rel, SessionKey: stem, Role: RoleMain}, true
		}
		role := RoleUnknown
		switch {
		case len(parts) == 1 && piConfigFiles[parts[0]]:
			role = RoleConfig
		case len(parts) == 1 && strings.HasSuffix(parts[0], ".log"):
			role = RoleLog
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
	return SessionFile{Agent: p.Name(), Path: path, Key: "dirs/" + RootTag(root) + "/" + rel, SessionKey: stem, Role: RoleMain}, true
}

func (p *Pi) Related(SessionFile, string) []string { return nil }

func (p *Pi) UserBase(root string) string {
	if p.isAgentDir(root) {
		return root
	}
	return p.AgentDir()
}

func (p *Pi) ExtraSnapshotEntries(string, string) []Entry { return nil }

func (p *Pi) MemoryDirs(string, string, string) []ScopedDir { return nil }

func (p *Pi) Plugins(string) []Plugin { return nil }

func (p *Pi) Exports(root, workDir string, interval time.Duration) []SessionFile {
	return SQLiteExports(p, root, workDir, interval)
}

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
