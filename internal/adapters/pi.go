package adapters

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mich-murphy/memory/internal/fsutil"
	"github.com/mich-murphy/memory/internal/jsonx"
)

var piRoles = map[string]Role{
	"settings.json": RoleConfig, "models.json": RoleConfig, "models-store.json": RoleConfig, "mcp.json": RoleConfig,
	"keybindings.json": RoleConfig, "AGENTS.md": RoleConfig, "AGENTS.override.md": RoleConfig,
	"CLAUDE.md": RoleConfig, "SYSTEM.md": RoleConfig, "APPEND_SYSTEM.md": RoleConfig, "extensions/": RoleConfig,
	"prompts/": RoleConfig, "skills/": RoleConfig, "themes/": RoleConfig, "tools/": RoleConfig,
	"experimental/": RoleExperimental,
}

var piInfo = map[string]string{"session_id": "id", "cwd": "cwd", "format_version": "version", "parent_session": "parentSession"}

type Pi struct {
	layout
	noExtras
}

func NewPi(home string) *Pi {
	return &Pi{
		name: "pi", home: home, dir: filepath.Join(".pi", "agent"), dirEnv: "PI_CODING_AGENT_DIR",
		skipDirs: []string{"bin"},
		rules: SnapshotRules{
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
		},
	}
}

func (p *Pi) DefaultRoots() []string {
	agentDir := p.Dir()
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
		if (r == agentDir || !fsutil.Within(r, agentDir)) && !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	return out
}

func (p *Pi) isAgentDir(root string) bool {
	return root == p.CanonicalRoot() || root == p.Dir() || fsutil.IsDir(filepath.Join(root, "sessions"))
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
	rel, ok := relative(path, root)
	if !ok {
		return SessionFile{}, false
	}
	parts := strings.Split(rel, "/")
	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	isJSONL := filepath.Ext(path) == ".jsonl"
	if p.isAgentDir(root) {
		prefix := keyPrefix(p, root)
		if parts[0] == "sessions" && isJSONL && len(parts) <= 3 {
			return SessionFile{Agent: p.Name(), Root: root, Path: path, Key: prefix + rel, SessionKey: stem, Role: RoleMain}, true
		}
		role := topRole(piRoles, rel)
		if len(parts) == 1 && strings.HasSuffix(rel, ".log") {
			role = RoleLog
		}
		return SessionFile{Agent: p.Name(), Root: root, Path: path, Key: prefix + rel, Role: role}, true
	}
	if !isJSONL || len(parts) > 2 {
		return SessionFile{}, false
	}
	return SessionFile{Agent: p.Name(), Root: root, Path: path, Key: "dirs/" + rootTag(root) + "/" + rel, SessionKey: stem, Role: RoleMain}, true
}

func (p *Pi) UserBase(root string) string {
	if p.isAgentDir(root) {
		return root
	}
	return p.Dir()
}

func (p *Pi) MemoryDirs(string, string, string) []ScopedDir { return nil }

func (p *Pi) SessionInfo(head []map[string]any) map[string]any {
	for _, d := range head {
		if d["type"] != "session" {
			continue
		}
		info := map[string]any{}
		fill(info, d, piInfo)
		return info
	}
	return map[string]any{}
}
