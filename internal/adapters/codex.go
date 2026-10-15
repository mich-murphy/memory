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

var (
	codexMetaInfo = map[string]string{
		"session_id": "id", "root_session_id": "session_id", "cwd": "cwd", "agent_version": "cli_version",
		"originator": "originator", "forked_from": "forked_from_id", "parent_session": "parent_thread_id",
		"agent_role": "agent_role",
	}
	codexGitInfo  = map[string]string{"git_branch": "branch", "git_commit": "commit_hash", "git_remote": "repository_url"}
	codexTurnInfo = map[string]string{"cwd": "cwd", "model": "model"}
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
	rel, ok := relative(path, root)
	if !ok || isSQLite(filepath.Base(path)) || skipped(filepath.Base(path)) {
		return SessionFile{}, false
	}
	prefix := keyPrefix(c, root)
	parts := strings.Split(rel, "/")
	name := filepath.Base(path)
	if codexRolloutRE.MatchString(name) && codexSessionDirs[parts[0]] {
		// Without the date folders and .zst, a rollout keeps its key when Codex recompresses,
		// archives or resumes it.
		uncompressed, zstd := strings.CutSuffix(name, ".zst")
		return SessionFile{
			Agent: c.Name(), Root: root, Path: path, Key: prefix + "rollouts/" + uncompressed,
			SessionKey: prefix + strings.TrimSuffix(uncompressed, ".jsonl"), Role: RoleMain, Zstd: zstd,
		}, true
	}
	return SessionFile{Agent: c.Name(), Root: root, Path: path, Key: prefix + rel, Role: topRole(codexRoles, rel)}, true
}

func (c *Codex) MemoryDirs(root, _, _ string) []ScopedDir {
	return []ScopedDir{{Scope: "user", Dir: filepath.Join(root, "memories")}}
}

func (c *Codex) SessionInfo(head []map[string]any) map[string]any {
	info := map[string]any{}
	// The newest header is the fork's own metadata after replayed parent headers.
	var own map[string]any
	var outer map[string]any
	ownIndex := 0
	for i, event := range head {
		if event["type"] == "session_meta" {
			if payload, ok := event["payload"].(map[string]any); ok {
				own, outer, ownIndex = payload, event, i
			}
		}
	}
	if own != nil {
		fill(info, own, codexMetaInfo)
		git, ok := own["git"].(map[string]any)
		if !ok || !truthy(own["git"]) {
			git, _ = outer["git"].(map[string]any)
		}
		fill(info, git, codexGitInfo)
		setStarted(info, own["timestamp"])
		if _, ok := info["started_at"]; !ok {
			setStarted(info, outer["timestamp"])
		}
		codexLineage(info, own)
	}
	for _, event := range head {
		if event["type"] == "turn_context" {
			payload, _ := event["payload"].(map[string]any)
			fill(info, payload, codexTurnInfo)
		}
	}
	firstEvent("codex", info, head[ownIndex:])
	observeEvents("codex", info, head)
	return info
}

func codexLineage(info, own map[string]any) {
	if parent := str(own, "forked_from_id"); parent != "" {
		setLineage(info, "fork", parent)
	}
	parent := str(own, "parent_thread_id")
	source, _ := own["source"].(map[string]any)
	subagent, _ := source["subagent"].(map[string]any)
	spawn, _ := subagent["thread_spawn"].(map[string]any)
	if id := str(spawn, "parent_thread_id"); id != "" {
		parent = id
	}
	if parent != "" {
		setLineage(info, "child_thread", parent)
	}
}
