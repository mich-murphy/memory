package testenv

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite" // database/sql driver "sqlite"
)

// Profile sizes a Workload. Main transcripts follow a log-normal spread around Median, the
// shape session sizes take in practice, plus one session per agent of Largest bytes.
type Profile struct {
	Sessions int
	Median   int
	Largest  int
	// RealProjects puts each session's cwd in a project directory under the home, so
	// snapshots find its instruction files. Without it, cwds are fixed paths that don't exist,
	// which keeps every captured byte independent of where the home is.
	RealProjects bool
}

var (
	SmallProfile   = Profile{Sessions: 3, Median: 24 << 10, Largest: 160 << 10}
	TypicalProfile = Profile{Sessions: 40, Median: 256 << 10, Largest: 12 << 20, RealProjects: true}
)

const workloadProjects = 4

// Workload is a home of Claude Code, Codex and Pi data whose lines follow each agent's own
// format, as seen in real sessions (Claude Code 2.1, Codex 0.162.1, Pi 1.1.0). The same Profile
// and Start always build the same bytes.
type Workload struct {
	Home  string
	Start time.Time
	// Mains are the main transcripts, in a fixed order: claude, codex, pi.
	Mains []string
	// Size is the total size of every file written.
	Size  int64
	Files int

	profile Profile
}

// NewWorkload writes the home under dir, with every file last modified at start.
func NewWorkload(tb testing.TB, dir string, p Profile, start time.Time) *Workload {
	tb.Helper()
	w := &Workload{Home: filepath.Join(dir, "home"), Start: start, profile: p}
	rng := rand.New(rand.NewPCG(1, 2))
	for agent := range 3 {
		for i := range p.Sessions {
			size := w.sessionSize(rng, i)
			proj := w.project(tb, i%workloadProjects)
			switch agent {
			case 0:
				w.claudeSession(tb, rng, proj, i, size)
			case 1:
				w.codexSession(tb, rng, proj, i, size)
			default:
				w.piSession(tb, rng, proj, i, size)
			}
		}
	}
	w.agentFiles(tb, rng)
	w.Touch(tb, start)
	return w
}

func (w *Workload) sessionSize(rng *rand.Rand, i int) int {
	if i == 0 {
		return w.profile.Largest
	}
	size := float64(w.profile.Median) * math.Exp(rng.NormFloat64())
	return int(min(max(size, 2<<10), float64(w.profile.Largest)))
}

func (w *Workload) project(tb testing.TB, n int) string {
	if !w.profile.RealProjects {
		return fmt.Sprintf("/home/dev/code/proj-%d", n)
	}
	dir := filepath.Join(w.Home, "code", fmt.Sprintf("proj-%d", n))
	if _, err := os.Stat(dir); err != nil {
		w.write(tb, filepath.Join(dir, "CLAUDE.md"), "# Project rules\n\n"+prose(rand.New(rand.NewPCG(uint64(n), 7)), 120)+"\n")
		w.write(tb, filepath.Join(dir, "AGENTS.md"), "# Agent rules\n\nRun make check before finishing.\n")
		w.write(tb, filepath.Join(dir, ".claude", "settings.json"), `{"permissions": {"allow": ["Bash(make *)"]}}`)
	}
	return dir
}

// Touch sets every file's mtime, so tests can age the home past the syncer's windows.
func (w *Workload) Touch(tb testing.TB, at time.Time) {
	tb.Helper()
	err := filepath.WalkDir(w.Home, func(p string, d os.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		return os.Chtimes(p, at, at)
	})
	if err != nil {
		tb.Fatal(err)
	}
}

func (w *Workload) write(tb testing.TB, path, text string) {
	tb.Helper()
	Write(tb, path, text)
	w.Size += int64(len(text))
	w.Files++
}

// AppendTurn adds about n bytes of complete lines in the transcript's own format, as one agent
// turn does, and sets its mtime.
func (w *Workload) AppendTurn(tb testing.TB, path string, n int, at time.Time) {
	tb.Helper()
	st, err := os.Stat(path)
	if err != nil {
		tb.Fatal(err)
	}
	rng := rand.New(rand.NewPCG(uint64(st.Size()), uint64(len(filepath.Base(path)))))
	g := newGen(rng, "/home/dev/code/proj-0", at)
	var lines string
	switch {
	case strings.Contains(path, "/.claude/"):
		lines = g.claudeLines(n)
	case strings.Contains(path, "/.codex/"):
		lines = g.codexLines(n)
	default:
		lines = g.piLines(n)
	}
	Append(tb, path, lines)
	if err := os.Chtimes(path, at, at); err != nil {
		tb.Fatal(err)
	}
}

type gen struct {
	rng   *rand.Rand
	cwd   string
	clock time.Time
	files []sourceFile
}

type sourceFile struct {
	path, text string
}

func newGen(rng *rand.Rand, cwd string, start time.Time) *gen {
	g := &gen{rng: rng, cwd: cwd, clock: start}
	for i := range 12 {
		g.files = append(g.files, sourceFile{
			path: fmt.Sprintf("%s/internal/%s/%s.go", cwd, pick(rng, words), pick(rng, words)),
			text: code(rng, 40+rng.IntN(200)) + strconv.Itoa(i),
		})
	}
	return g
}

func (g *gen) now() string {
	g.clock = g.clock.Add(time.Duration(200+g.rng.IntN(4000)) * time.Millisecond)
	return g.clock.UTC().Format("2006-01-02T15:04:05.000Z")
}

func (g *gen) id() string {
	r := g.rng
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", r.Uint32(), r.Uint32()&0xffff, r.Uint32()&0xffff,
		r.Uint32()&0xffff, r.Uint64()&0xffffffffffff)
}

// opaque is base64 of random bytes, like thinking signatures and Codex's encrypted reasoning,
// which don't compress.
func (g *gen) opaque(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(g.rng.Uint32())
	}
	return base64.StdEncoding.EncodeToString(b)
}

func (g *gen) toolOutput() (path, text string) {
	f := g.files[g.rng.IntN(len(g.files))]
	if g.rng.IntN(3) == 0 {
		return f.path, f.text
	}
	lines := strings.SplitAfter(f.text, "\n")
	from := g.rng.IntN(len(lines))
	return f.path, strings.Join(lines[from:min(len(lines), from+20+g.rng.IntN(60))], "")
}

func jsonLine(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // maps of strings and numbers always marshal
	}
	return string(b) + "\n"
}

func (g *gen) claudeLines(n int) string {
	var b strings.Builder
	sid, parent := g.id(), g.id()
	base := func(kind string) map[string]any {
		uuid := g.id()
		m := map[string]any{
			"parentUuid": parent, "isSidechain": false, "userType": "external", "cwd": g.cwd, "sessionId": sid,
			"version": "2.1.296", "gitBranch": "main", "type": kind, "uuid": uuid, "timestamp": g.now(),
		}
		parent = uuid
		return m
	}
	for b.Len() < n {
		u := base("user")
		u["message"] = map[string]any{"role": "user", "content": prose(g.rng, 10+g.rng.IntN(80))}
		b.WriteString(jsonLine(u))
		for range 1 + g.rng.IntN(6) {
			call := "toolu_" + g.opaque(18)[:24]
			path, out := g.toolOutput()
			a := base("assistant")
			content := []any{map[string]any{"type": "tool_use", "id": call, "name": "Read", "input": map[string]any{"file_path": path}}}
			if g.rng.IntN(3) == 0 {
				content = append([]any{map[string]any{"type": "thinking", "thinking": prose(g.rng, 30+g.rng.IntN(150)), "signature": g.opaque(300)}}, content...)
			}
			a["message"] = map[string]any{
				"id": "msg_" + g.opaque(18)[:24], "type": "message", "role": "assistant", "model": "claude-opus-4-8",
				"content": content, "stop_reason": "tool_use", "stop_sequence": nil,
				"usage": map[string]any{
					"input_tokens": g.rng.IntN(50), "cache_creation_input_tokens": g.rng.IntN(9000),
					"cache_read_input_tokens": g.rng.IntN(150000), "output_tokens": g.rng.IntN(2000),
					"service_tier": "standard",
				},
			}
			a["requestId"] = "req_" + g.opaque(18)[:24]
			b.WriteString(jsonLine(a))
			r := base("user")
			r["message"] = map[string]any{"role": "user", "content": []any{map[string]any{"tool_use_id": call, "type": "tool_result", "content": out}}}
			r["toolUseResult"] = map[string]any{"type": "text", "file": map[string]any{"filePath": path, "content": out, "numLines": strings.Count(out, "\n")}}
			b.WriteString(jsonLine(r))
		}
		a := base("assistant")
		a["message"] = map[string]any{
			"id": "msg_" + g.opaque(18)[:24], "type": "message", "role": "assistant", "model": "claude-opus-4-8",
			"content": []any{map[string]any{"type": "text", "text": prose(g.rng, 40+g.rng.IntN(300))}}, "stop_reason": "end_turn",
		}
		b.WriteString(jsonLine(a))
	}
	return b.String()
}

func (g *gen) codexLines(n int) string {
	var b strings.Builder
	turn := g.id()
	line := func(kind string, payload map[string]any) {
		b.WriteString(jsonLine(map[string]any{"timestamp": g.now(), "type": kind, "payload": payload}))
	}
	for b.Len() < n {
		line("turn_context", map[string]any{
			"turn_id": turn, "cwd": g.cwd, "approval_policy": "on-request", "model": "gpt-5.5-codex",
			"sandbox_policy": map[string]any{"type": "workspace-write", "writable_roots": []any{g.cwd}}, "effort": "high",
		})
		line("response_item", map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": prose(g.rng, 10+g.rng.IntN(80))}}})
		for range 1 + g.rng.IntN(6) {
			call := "call_" + g.opaque(18)[:24]
			path, out := g.toolOutput()
			line("response_item", map[string]any{"type": "reasoning", "summary": []any{}, "encrypted_content": g.opaque(600 + g.rng.IntN(1200))})
			line("response_item", map[string]any{"type": "function_call", "name": "exec_command", "arguments": `{"cmd":"sed -n 1,200p ` + path + `"}`, "call_id": call})
			line("response_item", map[string]any{"type": "function_call_output", "call_id": call, "output": "Process exited with code 0\nOutput:\n" + out})
			line("event_msg", map[string]any{"type": "token_count", "info": map[string]any{"total_token_usage": map[string]any{
				"input_tokens": g.rng.IntN(200000), "cached_input_tokens": g.rng.IntN(150000), "output_tokens": g.rng.IntN(9000),
				"reasoning_output_tokens": g.rng.IntN(4000), "total_tokens": g.rng.IntN(250000),
			}}})
		}
		text := prose(g.rng, 40+g.rng.IntN(300))
		line("response_item", map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": text}}})
		line("event_msg", map[string]any{"type": "task_complete", "turn_id": turn, "last_agent_message": text})
	}
	return b.String()
}

func (g *gen) piLines(n int) string {
	var b strings.Builder
	parent := any(nil)
	line := func(m map[string]any) {
		id := g.opaque(6)[:8]
		m["type"], m["id"], m["parentId"], m["timestamp"] = "message", id, parent, g.now()
		parent = id
		b.WriteString(jsonLine(m))
	}
	for b.Len() < n {
		line(map[string]any{"message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": prose(g.rng, 10+g.rng.IntN(80))}}, "timestamp": g.clock.UnixMilli()}})
		for range 1 + g.rng.IntN(6) {
			call := "toolu_" + g.opaque(18)[:24]
			path, out := g.toolOutput()
			line(map[string]any{"message": map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "thinking", "thinking": prose(g.rng, 20+g.rng.IntN(100))},
				map[string]any{"type": "toolCall", "id": call, "name": "read", "arguments": map[string]any{"path": path}},
			}, "provider": "anthropic", "model": "claude-opus-4-8", "usage": map[string]any{
				"input": g.rng.IntN(50), "output": g.rng.IntN(2000), "cacheRead": g.rng.IntN(150000), "cacheWrite": g.rng.IntN(9000),
			}, "stopReason": "toolUse", "timestamp": g.clock.UnixMilli()}})
			line(map[string]any{"message": map[string]any{
				"role": "toolResult", "toolCallId": call, "toolName": "read",
				"content": []any{map[string]any{"type": "text", "text": out}}, "isError": false, "timestamp": g.clock.UnixMilli(),
			}})
		}
		line(map[string]any{"message": map[string]any{
			"role": "assistant", "content": []any{map[string]any{"type": "text", "text": prose(g.rng, 40+g.rng.IntN(300))}},
			"stopReason": "stop", "timestamp": g.clock.UnixMilli(),
		}})
	}
	return b.String()
}

func slugOf(cwd string) string {
	return strings.NewReplacer("/", "-", ".", "-").Replace(cwd)
}

func (w *Workload) claudeSession(tb testing.TB, rng *rand.Rand, cwd string, i, size int) {
	g := newGen(rng, cwd, w.Start.Add(-time.Duration(i)*time.Hour))
	sid := g.id()
	root := filepath.Join(w.Home, ".claude")
	dir := filepath.Join(root, "projects", slugOf(cwd))
	main := filepath.Join(dir, sid+".jsonl")
	w.write(tb, main, g.claudeLines(size))
	w.Mains = append(w.Mains, main)
	if i%3 == 0 {
		agent := "agent-" + g.opaque(9)[:12]
		w.write(tb, filepath.Join(dir, sid, "subagents", agent+".jsonl"), g.claudeLines(size/4))
		w.write(tb, filepath.Join(dir, sid, "subagents", agent+".meta.json"), `{"agentType":"Explore"}`)
	}
	if i%4 == 0 {
		_, out := g.toolOutput()
		w.write(tb, filepath.Join(dir, sid, "tool-results", "toolu_"+g.opaque(9)[:12]+".txt"), strings.Repeat(out, 8))
	}
	for v := range 1 + i%5 {
		f := g.files[v%len(g.files)]
		w.write(tb, filepath.Join(root, "file-history", sid, fmt.Sprintf("%016x@v%d", rng.Uint64(), v+1)), f.text)
	}
	w.write(tb, filepath.Join(root, "todos", sid+"-agent-"+sid+".json"),
		jsonLine([]any{map[string]any{"content": prose(rng, 8), "status": "completed", "activeForm": prose(rng, 4)}}))
	w.write(tb, filepath.Join(root, "debug", sid+".txt"), debugLog(g, size/3))
}

func debugLog(g *gen, n int) string {
	var b strings.Builder
	for b.Len() < n {
		fmt.Fprintf(&b, "%s [DEBUG] %s\n", g.now(), prose(g.rng, 6+g.rng.IntN(20)))
	}
	return b.String()
}

func (w *Workload) codexSession(tb testing.TB, rng *rand.Rand, cwd string, i, size int) {
	start := w.Start.Add(-time.Duration(i) * time.Hour)
	g := newGen(rng, cwd, start)
	thread := g.id()
	ts := start.UTC().Format("2006-01-02T15-04-05")
	path := filepath.Join(w.Home, ".codex", "sessions", start.UTC().Format("2006/01/02"), "rollout-"+ts+"-"+thread+".jsonl")
	meta := jsonLine(map[string]any{"timestamp": g.now(), "type": "session_meta", "payload": map[string]any{
		"id": thread, "session_id": thread, "timestamp": g.now(), "cwd": cwd, "originator": "codex_cli_rs", "cli_version": "0.162.1",
		"source": "cli", "model_provider": "openai", "base_instructions": map[string]any{"text": codexInstructions},
		"git": map[string]any{"branch": "main", "commit_hash": fmt.Sprintf("%040x", rng.Uint64()), "repository_url": "git@github.com:dev/proj.git"},
	}})
	w.write(tb, path, meta+g.codexLines(size-len(meta)))
	w.Mains = append(w.Mains, path)
}

func (w *Workload) piSession(tb testing.TB, rng *rand.Rand, cwd string, i, size int) {
	start := w.Start.Add(-time.Duration(i) * time.Hour)
	g := newGen(rng, cwd, start)
	sid := g.id()
	ts := start.UTC().Format("2006-01-02T15-04-05-000Z")
	dir := filepath.Join(w.Home, ".pi", "agent", "sessions", "--"+strings.Trim(slugOf(cwd), "-")+"--")
	head := jsonLine(map[string]any{"type": "session", "version": 3, "id": sid, "timestamp": g.now(), "cwd": cwd}) +
		jsonLine(map[string]any{"type": "model_change", "id": g.opaque(6)[:8], "parentId": nil, "timestamp": g.now(), "provider": "anthropic", "modelId": "claude-opus-4-8"})
	path := filepath.Join(dir, ts+"_"+sid+".jsonl")
	w.write(tb, path, head+g.piLines(size-len(head)))
	w.Mains = append(w.Mains, path)
}

func (w *Workload) agentFiles(tb testing.TB, rng *rand.Rand) {
	claude, codex, pi := filepath.Join(w.Home, ".claude"), filepath.Join(w.Home, ".codex"), filepath.Join(w.Home, ".pi", "agent")
	var history strings.Builder
	projects := map[string]any{}
	for i := range w.profile.Sessions * 6 {
		cwd := fmt.Sprintf("/home/dev/code/proj-%d", i%workloadProjects)
		history.WriteString(jsonLine(map[string]any{
			"display": prose(rng, 5+rng.IntN(30)), "pastedContents": map[string]any{},
			"timestamp": w.Start.UnixMilli() - int64(i)*60000, "project": cwd,
		}))
		projects[cwd] = map[string]any{
			"allowedTools": []any{}, "history": []any{}, "mcpServers": map[string]any{},
			"hasTrustDialogAccepted": true, "lastCost": rng.Float64() * 10, "lastSessionId": strconv.Itoa(i),
		}
	}
	w.write(tb, filepath.Join(claude, "history.jsonl"), history.String())
	w.write(tb, filepath.Join(claude, "settings.json"), `{"env": {"ANTHROPIC_API_KEY": "sk-ant-not-a-real-key"}, "model": "opus"}`)
	w.write(tb, filepath.Join(claude, "CLAUDE.md"), "# User rules\n\n"+prose(rng, 200)+"\n")
	w.write(tb, filepath.Join(claude, "skills", "review", "SKILL.md"), "---\nname: review\n---\n"+prose(rng, 300)+"\n")
	w.write(tb, filepath.Join(claude, "plans", "quiet-plan.md"), "# Plan\n\n"+prose(rng, 400)+"\n")
	w.write(tb, filepath.Join(w.Home, ".claude.json"), jsonLine(map[string]any{
		"numStartups": 412, "primaryApiKey": "sk-ant-not-a-real-key", "projects": projects,
		"mcpServers": map[string]any{"gh": map[string]any{"command": "npx", "env": map[string]any{"GITHUB_TOKEN": "ghp_not_a_real_token"}}},
	}))
	w.write(tb, filepath.Join(codex, "history.jsonl"), history.String())
	w.write(tb, filepath.Join(codex, "config.toml"), "model = \"gpt-5.5-codex\"\n[mcp_servers.gh.env]\nGITHUB_TOKEN = \"ghp_not_a_real_token\"\n")
	w.write(tb, filepath.Join(codex, "AGENTS.md"), "# Codex rules\n\n"+prose(rng, 150)+"\n")
	w.write(tb, filepath.Join(codex, "log", "codex-tui.log"), debugLog(newGen(rng, "/", w.Start), w.profile.Median))
	w.codexDB(tb, filepath.Join(codex, "state_5.sqlite"))
	w.write(tb, filepath.Join(pi, "settings.json"), `{"defaultProvider": "anthropic", "defaultModel": "claude-opus-4-8"}`)
	w.write(tb, filepath.Join(pi, "AGENTS.md"), "# Pi rules\n\n"+prose(rng, 150)+"\n")
}

func (w *Workload) codexDB(tb testing.TB, path string) {
	tb.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		tb.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			tb.Fatal(err)
		}
	}()
	stmts := []string{"CREATE TABLE threads (id TEXT PRIMARY KEY, title TEXT, tokens INTEGER)"}
	for i := range w.profile.Sessions {
		stmts = append(stmts, fmt.Sprintf("INSERT INTO threads VALUES ('t%d', 'thread %d', %d)", i, i, i*1000))
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(tb.Context(), s); err != nil {
			tb.Fatal(err)
		}
	}
	w.Files++
}

var words = strings.Fields(`the a to of and in is for that it on with as this be are by from we use
	file test error value return func string context session state upload sync key bucket config
	path root agent hook read write check run build make change update fix add remove handle pass
	fails because when then before after each every only should would could need want see look
	type struct map slice byte int bool nil err ctx opts cfg data body size count index line lines
	manifest piece segment version blob snapshot transcript claude codex pi zstd sha256 hash r2
	refactor rename extract inline simplify cover branch case loop call result output input local`)

func pick(rng *rand.Rand, from []string) string {
	return from[rng.IntN(len(from))]
}

func prose(rng *rand.Rand, n int) string {
	var b strings.Builder
	for i := range n {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(pick(rng, words))
		if rng.IntN(14) == 0 {
			b.WriteByte('.')
		}
	}
	return b.String()
}

func code(rng *rand.Rand, lines int) string {
	var b strings.Builder
	indent := 0
	for range lines {
		switch r := rng.IntN(10); {
		case r == 0 && indent < 3:
			fmt.Fprintf(&b, "%sfunc %s%s(%s %s) error {\n", strings.Repeat("\t", indent), pick(rng, words), pick(rng, words), pick(rng, words), pick(rng, words))
			indent++
		case r == 1 && indent < 3:
			fmt.Fprintf(&b, "%sif err := %s.%s(%s); err != nil {\n", strings.Repeat("\t", indent), pick(rng, words), pick(rng, words), pick(rng, words))
			indent++
		case r == 2 && indent > 0:
			indent--
			fmt.Fprintf(&b, "%s}\n", strings.Repeat("\t", indent))
		case r == 3:
			fmt.Fprintf(&b, "%s// %s\n", strings.Repeat("\t", indent), prose(rng, 4+rng.IntN(10)))
		default:
			fmt.Fprintf(&b, "%s%s := %s.%s(%s, %d)\n", strings.Repeat("\t", indent), pick(rng, words), pick(rng, words), pick(rng, words), pick(rng, words), rng.IntN(1000))
		}
	}
	return b.String()
}

var codexInstructions = strings.Repeat("You are a coding agent running in the Codex CLI, a terminal-based coding assistant. "+
	"Be precise, safe, and helpful. Use the shell to read files, run tests and apply patches.\n", 120)
