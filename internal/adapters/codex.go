package adapters

// OpenAI Codex CLI.
//
// Layout (checked against openai/codex codex-rs/rollout, 2026-10-10):
//
//	sessions/YYYY/MM/DD/rollout-<ts>-<thread-id>.jsonl            one file per thread
//	sessions/.../rollout-<ts>-<thread-id>_<rollout-id>.jsonl      after a thread revert
//	*.jsonl.zst                                                    rollouts untouched for 7 days are
//	                                                               recompressed; resuming one
//	                                                               decompresses it back to .jsonl
//	archived_sessions/...                                          archived threads, same names
//	history.jsonl, session_index.jsonl                             prompt history, thread names
//	memories/, log/                                                Codex's own memories, TUI log
//	state_<n>.sqlite, logs_<n>.sqlite, ...                         thread metadata, logs, goals...
//	config.toml, hooks.json, AGENTS.md, rules/, skills/, prompts/  the harness
//
// Subagents and forks get their own rollout files; session_meta records parent_thread_id and
// forked_from_id. CODEX_HOME moves the tree (default ~/.codex). auth.json and .credentials.json
// (MCP OAuth tokens when there's no keyring) are never copied.
//
// Codex keeps per-thread metadata (titles, token totals, sandbox and approval mode) in its
// SQLite databases, and its "paginated" history mode keeps some of it only there, so sweeps
// upload a backup-API copy of every database under db/. Rollouts stay the source of truth for
// the conversation.
//
// Keys drop the date folders and the .zst suffix, so a rollout keeps one key when Codex
// recompresses or archives it, and an unchanged body is not uploaded twice.

import (
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var (
	codexRolloutRE = regexp.MustCompile(
		`^rollout-\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}-([0-9a-fA-F-]{36})(?:_[0-9a-fA-F-]{36})?\.jsonl(?:\.zst)?$`,
	)
	codexSessionDirs   = map[string]bool{"sessions": true, "archived_sessions": true}
	codexHistoryFiles  = map[string]bool{"history.jsonl": true, "session_index.jsonl": true}
	codexTopLevelRoles = map[string]string{
		"memories": "memory", "log": "log", "rules": "config", "skills": "config", "prompts": "config", "plugins": "config",
	}
	codexConfigFiles = map[string]bool{
		"config.toml": true, "hooks.json": true, "AGENTS.md": true, "AGENTS.override.md": true,
		"requirements.toml": true, "managed_config.toml": true,
	}
)

// Codex is the Codex CLI adapter.
type Codex struct {
	home string
}

// NewCodex returns the Codex adapter for a home directory.
func NewCodex(home string) *Codex { return &Codex{home: home} }

// Name is "codex".
func (c *Codex) Name() string { return "codex" }

// Home is the user's home directory.
func (c *Codex) Home() string { return c.home }

// SkipDirs are plugin caches and Codex's own binaries.
func (c *Codex) SkipDirs() []string { return []string{"plugins/cache", "bin", "packages"} }

// ManagedPaths are the administrator settings files.
func (c *Codex) ManagedPaths() []string {
	return []string{"/etc/codex/config.toml", "/etc/codex/requirements.toml", "/etc/codex/managed_config.toml"}
}

// Rules are the harness files a snapshot records.
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

// CanonicalRoot is ~/.codex.
func (c *Codex) CanonicalRoot() string { return filepath.Join(c.home, ".codex") }

// DefaultRoots is $CODEX_HOME or ~/.codex.
func (c *Codex) DefaultRoots() []string {
	if p := envPath("CODEX_HOME", c.home); p != "" {
		return []string{p}
	}
	return []string{c.CanonicalRoot()}
}

// InferRoot finds the directory holding sessions/ or archived_sessions/ above a rollout.
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

// Classify maps a path under root to its key, session and role.
func (c *Codex) Classify(path, root string) (SessionFile, bool) {
	rel, ok := Rel(path, root)
	if !ok {
		return SessionFile{}, false
	}
	prefix := KeyPrefix(c, root)
	parts := strings.Split(rel, "/")
	name := filepath.Base(path)
	if codexRolloutRE.MatchString(name) && codexSessionDirs[parts[0]] {
		compression := ""
		if strings.HasSuffix(name, ".zst") {
			compression = "zst"
		}
		name = strings.TrimSuffix(name, ".zst")
		return SessionFile{
			Agent: c.Name(), Path: path, Key: prefix + "rollouts/" + name,
			SessionKey: strings.TrimSuffix(name, ".jsonl"), Role: "main", Compression: compression,
		}, true
	}
	top := parts[0]
	role := "unknown"
	switch {
	case len(parts) == 1 && codexHistoryFiles[top]:
		role = "history"
	case len(parts) == 1 && codexConfigFiles[top]:
		role = "config"
	case len(parts) > 1 && !codexSessionDirs[top]:
		if r, ok := codexTopLevelRoles[top]; ok {
			role = r
		}
	}
	return SessionFile{Agent: c.Name(), Path: path, Key: prefix + rel, Role: role}, true
}

// Related: a rollout is one file.
func (c *Codex) Related(SessionFile, string) []string { return nil }

// UserBase is the root itself.
func (c *Codex) UserBase(root string) string { return root }

// ExtraSnapshotEntries: none for Codex.
func (c *Codex) ExtraSnapshotEntries(string, string) []Entry { return nil }

// MemoryDirs: Codex's memories/.
func (c *Codex) MemoryDirs(root, _, _ string) []ScopedDir {
	return []ScopedDir{{Name: "user", Dir: filepath.Join(root, "memories")}}
}

// PluginDirs: none recorded for Codex.
func (c *Codex) PluginDirs(string) []ScopedDir { return nil }

// Exports: database copies.
func (c *Codex) Exports(root, workDir string, interval time.Duration) []SessionFile {
	return SQLiteExports(c, root, workDir, interval)
}

// SessionInfo reads session_meta and turn_context lines.
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
