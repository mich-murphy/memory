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
	codexSessionDirs   = map[string]bool{"sessions": true, "archived_sessions": true}
	codexHistoryFiles  = map[string]bool{"history.jsonl": true, "session_index.jsonl": true}
	codexTopLevelRoles = map[string]Role{
		"memories": RoleMemory, "log": RoleLog, "rules": RoleConfig, "skills": RoleConfig, "prompts": RoleConfig,
		"plugins": RoleConfig,
	}
	codexConfigFiles = map[string]bool{
		"config.toml": true, "hooks.json": true, "AGENTS.md": true, "AGENTS.override.md": true,
		"requirements.toml": true, "managed_config.toml": true,
	}
)

type Codex struct {
	noExtras
	home string
}

func NewCodex(home string) *Codex { return &Codex{home: home} }

func (c *Codex) Name() string { return "codex" }

func (c *Codex) Home() string { return c.home }

func (c *Codex) SkipDirs() []string { return []string{"plugins/cache", "bin", "packages"} }

func (c *Codex) ManagedSettingsFiles() []string {
	return []string{"/etc/codex/config.toml", "/etc/codex/requirements.toml", "/etc/codex/managed_config.toml"}
}

func (c *Codex) Rules() SnapshotRules {
	return SnapshotRules{
		User: []string{"AGENTS.md", "AGENTS.override.md", "config.toml", "hooks.json", "rules/**", "skills/**", "prompts/**"},
		Project: []string{
			"AGENTS.md", "AGENTS.override.md", "**/AGENTS.md", "**/AGENTS.override.md", ".codex/config.toml",
			".codex/hooks.json", ".codex/rules/**", ".codex/skills/**", ".agents/skills/**",
		},
		Home: []string{".agents/skills/**"},
	}
}

func (c *Codex) CanonicalRoot() string { return filepath.Join(c.home, ".codex") }

func (c *Codex) DefaultRoots() []string {
	if p := envPath("CODEX_HOME", c.home); p != "" {
		return []string{p}
	}
	return []string{c.CanonicalRoot()}
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
			Agent: c.Name(), Path: path, Key: prefix + "rollouts/" + uncompressed,
			SessionKey: strings.TrimSuffix(uncompressed, ".jsonl"), Role: RoleMain, Zstd: zstd,
		}, true
	}
	top := parts[0]
	role := RoleUnknown
	switch {
	case len(parts) == 1 && codexHistoryFiles[top]:
		role = RoleHistory
	case len(parts) == 1 && codexConfigFiles[top]:
		role = RoleConfig
	case len(parts) > 1 && !codexSessionDirs[top]:
		if r, ok := codexTopLevelRoles[top]; ok {
			role = r
		}
	}
	return SessionFile{Agent: c.Name(), Path: path, Key: prefix + rel, Role: role}, true
}

func (c *Codex) UserBase(root string) string { return root }

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
