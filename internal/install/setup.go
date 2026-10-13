package install

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/BurntSushi/toml"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/config"
	"github.com/mich-murphy/memory/internal/fsutil"
	"github.com/mich-murphy/memory/internal/jsonx"
)

const PiMarker = "// agent-sessions: managed by `agent-sessions install`"

const piExtension = `%s; edits are overwritten.
// Docs: https://github.com/mich-murphy/memory
import { spawn } from "node:child_process";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

const AGENT_SESSIONS: string[] = %s;

function kick(file: string | undefined, extra: string[] = []): void {
	// No file for --no-session runs, or before the session's first message.
	if (!file || process.env.AGENT_SESSIONS_DISABLE) return;
	try {
		const child = spawn(AGENT_SESSIONS[0], [...AGENT_SESSIONS.slice(1), "hook", "pi", "--path", file, ...extra], {
			detached: true,
			stdio: "ignore",
		});
		child.on("error", () => {});
		child.unref();
	} catch {
		// Capture must never break the session.
	}
}

export default function (pi: ExtensionAPI) {
	// Pi renames its process, which hides its arguments from the process table, so pass them along.
	pi.on("session_start", (event, ctx) =>
		kick(ctx.sessionManager.getSessionFile(), [
			"--event", "session_start", "--source", event.reason, "--cwd", ctx.cwd, "--argv", JSON.stringify(process.argv),
		]),
	);
	pi.on("agent_settled", (_event, ctx) => kick(ctx.sessionManager.getSessionFile(), ["--event", "agent_settled"]));
	pi.on("session_shutdown", (_event, ctx) => kick(ctx.sessionManager.getSessionFile(), ["--event", "session_shutdown"]));
}
`

const configTemplate = `# agent-sessions settings. Hooks don't always see your shell's environment, so they read this file.
# Real environment variables override it.
R2_ENDPOINT=%s
R2_BUCKET=%s
R2_ACCESS_KEY_ID=%s
R2_SECRET_ACCESS_KEY=%s
# This machine's id in object keys is a UUID kept in machine-id next to this file.
# Minimum minutes between uploads of a changed SQLite database (Codex's state and logs).
# AGENT_SESSIONS_DB_INTERVAL_MINUTES=60
`

// PiExtension is the extension Pi loads, running command.
func PiExtension(command []string) ([]byte, error) {
	argv, err := jsonx.Compact(command)
	if err != nil {
		return nil, err
	}
	return fmt.Appendf(nil, piExtension, PiMarker, argv), nil
}

func PiExtensionPath(home string) string {
	return filepath.Join(adapters.NewPi(home).AgentDir(), "extensions", "agent-sessions.ts")
}

func Pi(o *Options) error {
	path := PiExtensionPath(o.Home)
	if data, err := os.ReadFile(path); err == nil && !bytes.Contains(data, []byte(PiMarker)) {
		o.logf("pi: %s exists and isn't agent-sessions'; leaving it alone", path)
		return nil
	}
	if o.Remove {
		if fsutil.Exists(path) && !o.DryRun {
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("install: %w", err)
			}
		}
		o.logf("removed: %s", path)
		return nil
	}
	body, err := PiExtension(o.Command)
	if err != nil {
		return err
	}
	if err := write(path, body, o); err != nil {
		return err
	}
	o.logf("pi: new Pi sessions load the extension; run /reload in open ones.")
	return nil
}

func have(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func WriteConfigTemplate(o *Options) (string, error) {
	path := config.FilePath()
	if fsutil.Exists(path) {
		o.logf("config: keeping %s", path)
		return path, nil
	}
	names := []string{"R2_ENDPOINT", "R2_BUCKET", "R2_ACCESS_KEY_ID", "R2_SECRET_ACCESS_KEY"}
	values := make([]any, len(names))
	var missing []string
	for i, k := range names {
		values[i] = os.Getenv(k)
		if values[i] == "" {
			missing = append(missing, k)
		}
	}
	if o.DryRun {
		o.logf("would write: %s", path)
		return path, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("config: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", fmt.Errorf("config: %w", err)
	}
	_, werr := fmt.Fprintf(f, configTemplate, values...)
	if err := errors.Join(werr, f.Close()); err != nil {
		return "", fmt.Errorf("config: %w", err)
	}
	if len(missing) > 0 {
		o.logf("config: wrote %s; fill in %s", path, strings.Join(missing, ", "))
	} else {
		o.logf("config: wrote %s from your environment", path)
	}
	return path, nil
}

// codexEventKey mirrors hook_event_key_label in codex-rs/hooks/src/lib.rs.
func codexEventKey(event string) string {
	var b strings.Builder
	for i, r := range event {
		if i > 0 && unicode.IsUpper(r) {
			b.WriteByte('_')
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

func tomlHooks(path string) map[string]any {
	var doc map[string]any
	if _, err := toml.DecodeFile(path, &doc); err != nil {
		return nil
	}
	hooks, _ := doc["hooks"].(map[string]any)
	return hooks
}

func tomlHasHookEvents(path string) bool {
	for k := range tomlHooks(path) {
		if k != "state" {
			return true
		}
	}
	return false
}

type HandlerRef struct {
	Event        string
	GroupIndex   int
	HandlerIndex int
}

func OurHandlers(settings *jsonx.Object) []HandlerRef {
	v, _ := settings.Get("hooks")
	hooks, isObj := v.(*jsonx.Object)
	if !isObj {
		return nil
	}
	var out []HandlerRef
	for _, event := range hooks.Keys() {
		gv, _ := hooks.Get(event)
		groups, _ := gv.([]any)
		for gi, group := range groups {
			for hi, h := range handlersOf(group) {
				if Ours.MatchString(commandOf(h)) {
					out = append(out, HandlerRef{event, gi, hi})
				}
			}
		}
	}
	return out
}

// CodexTrustState reads Codex's trust records from config.toml, kept under keys like
// <hooks.json path>:<event>:<group>:<handler>.
func CodexTrustState(root string) (configured, trusted int) {
	hooksPath := filepath.Join(root, "hooks.json")
	cfg, err := loadJSON(hooksPath)
	if err != nil {
		return 0, 0
	}
	refs := OurHandlers(cfg)
	state, _ := tomlHooks(filepath.Join(root, "config.toml"))["state"].(map[string]any)
	for _, r := range refs {
		key := fmt.Sprintf("%s:%s:%d:%d", hooksPath, codexEventKey(r.Event), r.GroupIndex, r.HandlerIndex)
		entry, _ := state[key].(map[string]any)
		if h, _ := entry["trusted_hash"].(string); h != "" {
			trusted++
		}
	}
	return len(refs), trusted
}

func AgentsPresent(home string) map[string]bool {
	dirs := map[string]string{
		"claude": adapters.NewClaude(home).DefaultRoots()[0],
		"codex":  adapters.NewCodex(home).DefaultRoots()[0],
		"pi":     adapters.NewPi(home).AgentDir(),
	}
	out := map[string]bool{}
	for name, d := range dirs {
		if fsutil.Exists(d) || have(name) {
			out[name] = true
		}
	}
	return out
}
