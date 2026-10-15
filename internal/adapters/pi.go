package adapters

import (
	"os"
	"path/filepath"
	"regexp"
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

// InferRoot recognises the same session layouts as Classify, including child hook paths.
func (p *Pi) InferRoot(path string) string {
	if filepath.Ext(path) != ".jsonl" {
		return ""
	}
	for dir := filepath.Dir(path); dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		if root, known := piHookRoot(path, dir); known {
			return root
		}
	}
	// With no enclosing marker, only Pi's own timestamp/UUID filename proves a
	// flat session. An arbitrary nested JSONL must not create a new main session.
	if !piSessionDirRE.MatchString(strings.TrimSuffix(filepath.Base(path), ".jsonl")) {
		return ""
	}
	for dir := filepath.Dir(path); dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		if filepath.Base(dir) == "subagent-artifacts" {
			return ""
		}
	}
	return piNamespaceRoot(filepath.Dir(path))
}

// piHookRoot validates a marked layout before inferring its namespace. A recognised
// marker with an arbitrary nested JSONL is rejected rather than treated as a flat root.
func piHookRoot(path, dir string) (string, bool) {
	base, root := dir, filepath.Dir(dir)
	switch {
	case piCWD(filepath.Base(dir)):
	case piSessionDirRE.MatchString(filepath.Base(dir)):
		base = root
		if piCWD(filepath.Base(root)) {
			root = filepath.Dir(root)
		}
	default:
		return "", false
	}
	rel, ok := relative(path, base)
	if !ok {
		return "", true
	}
	_, role := piSession(strings.Split(rel, "/"))
	if role != RoleMain && role != RoleSubagent {
		return "", true
	}
	return piNamespaceRoot(root), true
}

func piNamespaceRoot(root string) string {
	if filepath.Base(root) == "sessions" {
		return filepath.Dir(root)
	}
	return root
}

var (
	piSessionDirRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T[0-9:.Z+-]+_([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})$`)
	piRunRE        = regexp.MustCompile(`^run-(0|[1-9][0-9]*)$`)
)

func piCWD(name string) bool {
	return len(name) > 4 && strings.HasPrefix(name, "--") && strings.HasSuffix(name, "--")
}

// piSession accepts only the child layouts emitted by Pi's task/fork/run extensions.
// A validated parent directory is evidence for grouping, not a child's own identity.
func piSession(parts []string) (string, Role) {
	if len(parts) == 1 && strings.HasSuffix(parts[0], ".jsonl") {
		return strings.TrimSuffix(parts[0], ".jsonl"), RoleMain
	}
	if len(parts) < 2 || !piSessionDirRE.MatchString(parts[0]) {
		return "", RoleAux
	}
	parent := parts[0]
	if len(parts) == 3 && (parts[1] == "tasks" || parts[1] == "forks") && strings.HasSuffix(parts[2], ".jsonl") {
		return parent, RoleSubagent
	}
	if len(parts) == 4 && uuidPrefixRE.FindString(parts[1]) == parts[1] && piRunRE.MatchString(parts[2]) && parts[3] == "session.jsonl" {
		return parent, RoleSubagent
	}
	return parent, RoleAux
}

func (p *Pi) Classify(path, root string) (SessionFile, bool) {
	rel, ok := relative(path, root)
	if !ok || isSQLite(filepath.Base(path)) || skipped(filepath.Base(path)) {
		return SessionFile{}, false
	}
	parts := strings.Split(rel, "/")
	prefix := "dirs/" + rootTag(root) + "/"
	agentDir := p.isAgentDir(root)
	if agentDir {
		prefix = keyPrefix(p, root)
		if parts[0] != "sessions" {
			role := topRole(piRoles, rel)
			if len(parts) == 1 && strings.HasSuffix(rel, ".log") {
				role = RoleLog
			}
			return SessionFile{Agent: p.Name(), Root: root, Path: path, Key: prefix + rel, Role: role}, true
		}
		parts = parts[1:]
	}
	cwd := ""
	if len(parts) > 1 && piCWD(parts[0]) {
		cwd, parts = parts[0]+"/", parts[1:]
	}
	session, role := piSession(parts)
	if cwd == "" && len(parts) > 1 && !piSessionDirRE.MatchString(parts[0]) {
		session, role = "", RoleAux
	}
	// Non-transcript session-directory files are retained as auxiliary artifacts.
	if agentDir && role == RoleAux && session == "" && len(parts) == 1 {
		role = RoleUnknown
	}
	if session != "" {
		session = prefix + cwd + session
	}
	return SessionFile{Agent: p.Name(), Root: root, Path: path, Key: prefix + rel, SessionKey: session, Role: role}, true
}

func (p *Pi) Related(sf SessionFile) Discovery {
	prefix := "dirs/" + rootTag(sf.Root) + "/"
	base := sf.Root
	if p.isAgentDir(sf.Root) {
		prefix, base = keyPrefix(p, sf.Root), filepath.Join(sf.Root, "sessions")
	}
	local, ok := strings.CutPrefix(sf.SessionKey, prefix)
	if !ok || local == "" || !filepath.IsLocal(filepath.FromSlash(local)) {
		return Discovery{Complete: true}
	}
	out := discoverUnder(p, sf.Root, filepath.Join(base, filepath.FromSlash(local)+".jsonl"))
	children := discoverUnder(p, sf.Root, filepath.Join(base, filepath.FromSlash(local)))
	out.Files = append(out.Files, children.Files...)
	out.Issues = append(out.Issues, children.Issues...)
	out.Complete = out.Complete && children.Complete
	return out
}

func (p *Pi) UserBase(root string) string {
	if p.isAgentDir(root) {
		return root
	}
	return p.Dir()
}

func (p *Pi) MemoryDirs(string, string, string) []ScopedDir { return nil }

func (p *Pi) SessionInfo(head []map[string]any) map[string]any {
	info := map[string]any{}
	for _, d := range head {
		if d["type"] != "session" {
			continue
		}
		fill(info, d, piInfo)
		setStarted(info, d["timestamp"])
		if parent := piParentID(str(d, "parentSession")); parent != "" {
			setLineage(info, "fork", parent)
		}
		break
	}
	firstEvent("pi", info, head)
	observeEvents("pi", info, head)
	return info
}
