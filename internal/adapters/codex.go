package adapters

import (
	"path/filepath"
	"regexp"
	"strings"
)

var (
	codexRolloutRE = regexp.MustCompile(
		`^rollout-\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}-([0-9a-fA-F-]{36})(?:_[0-9a-fA-F-]{36})?\.jsonl(?:\.zst)?$`,
	)
	codexSessionDirs = map[string]bool{"sessions": true, "archived_sessions": true}
	codexRoles       = map[string]Role{
		"history.jsonl": RoleHistory, "session_index.jsonl": RoleHistory, "config.toml": RoleConfig,
		"hooks.json": RoleConfig, "AGENTS.md": RoleConfig, "AGENTS.override.md": RoleConfig,
		"requirements.toml": RoleConfig, "managed_config.toml": RoleConfig, "memories/": RoleMemory, "log/": RoleLog,
		"rules/": RoleConfig, "skills/": RoleConfig, "prompts/": RoleConfig, "plugins/": RoleConfig,
	}
)

type Codex struct {
	layout
	noExtras
}

func NewCodex(home string) *Codex {
	return &Codex{
		name: "codex", home: home, dir: ".codex", dirEnv: "CODEX_HOME",
		skipDirs:        []string{"plugins/cache", "bin", "packages"},
		managedSettings: []string{"/etc/codex/config.toml", "/etc/codex/requirements.toml", "/etc/codex/managed_config.toml"},
		rules: SnapshotRules{
			User: []string{"AGENTS.md", "AGENTS.override.md", "config.toml", "hooks.json", "rules/**", "skills/**", "prompts/**"},
			Project: []string{
				"AGENTS.md", "AGENTS.override.md", "**/AGENTS.md", "**/AGENTS.override.md", ".codex/config.toml",
				".codex/hooks.json", ".codex/rules/**", ".codex/skills/**", ".agents/skills/**",
			},
			Home: []string{".agents/skills/**"},
		},
	}
}

func (c *Codex) InferRoot(path string) string {
	if !codexRolloutRE.MatchString(filepath.Base(path)) {
		return ""
	}
	for dir := filepath.Dir(path); dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		if codexSessionDirs[filepath.Base(dir)] {
			return filepath.Dir(dir)
		}
	}
	return ""
}

func (c *Codex) Classify(path, root string) (SessionFile, bool) {
	rel, ok := Rel(path, root)
	if !ok {
		return SessionFile{}, false
	}
	prefix := KeyPrefix(c, root)
	parts := strings.Split(rel, "/")
	name := filepath.Base(path)
	if codexRolloutRE.MatchString(name) && codexSessionDirs[parts[0]] {
		// Without the date folders and .zst, a rollout keeps its key when Codex recompresses,
		// archives or resumes it.
		uncompressed, zstd := strings.CutSuffix(name, ".zst")
		return SessionFile{
			Agent: c.Name(), Root: root, Path: path, Key: prefix + "rollouts/" + uncompressed,
			SessionKey: strings.TrimSuffix(uncompressed, ".jsonl"), Role: RoleMain, Zstd: zstd,
		}, true
	}
	return SessionFile{Agent: c.Name(), Root: root, Path: path, Key: prefix + rel, Role: topRole(codexRoles, rel)}, true
}

func (c *Codex) MemoryDirs(root, _, _ string) []ScopedDir {
	return []ScopedDir{{Scope: "user", Dir: filepath.Join(root, "memories")}}
}

func (c *Codex) SessionInfo(head []map[string]any) map[string]any {
	info := map[string]any{}
	setDefault := func(k string, v any) {
		if _, ok := info[k]; !ok {
			info[k] = v
		}
	}
	for _, d := range head {
		payload, ok := d["payload"].(map[string]any)
		if !ok {
			continue
		}
		switch d["type"] {
		case "session_meta":
			for _, f := range [][2]string{
				{"session_id", "id"},
				{"root_session_id", "session_id"},
				{"cwd", "cwd"},
				{"agent_version", "cli_version"},
				{"originator", "originator"},
				{"forked_from", "forked_from_id"},
				{"parent_session", "parent_thread_id"},
				{"agent_role", "agent_role"},
			} {
				setDefault(f[0], payload[f[1]])
			}
			git, ok := payload["git"].(map[string]any)
			if !ok || !truthy(payload["git"]) {
				git, _ = d["git"].(map[string]any)
			}
			if git != nil {
				setDefault("git_branch", git["branch"])
				setDefault("git_commit", git["commit_hash"])
				setDefault("git_remote", git["repository_url"])
			}
		case "turn_context":
			setDefault("cwd", payload["cwd"])
			setDefault("model", payload["model"])
		}
	}
	for k, v := range info {
		if !truthy(v) {
			delete(info, k)
		}
	}
	return info
}
