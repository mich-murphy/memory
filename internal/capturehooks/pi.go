package capturehooks

import "encoding/json"

// No shell, transcript reads, queue/DB open, output capture, or detached sync.
// Session starts pass only native SDK metadata; other events enqueue path hints.
func piExtension(argv []string) ([]byte, error) {
	b, err := json.Marshal(argv)
	if err != nil {
		return nil, err
	}
	body := piPrefix + `import { spawn } from "node:child_process";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import type { ExtensionContext, SessionStartEvent } from "@earendil-works/pi-coding-agent";

const COMMAND: string[] = ` + string(b) + `;

function kick(file: string | undefined): void {
  if (!file) return; // --no-session has no durable source.
  try {
    const child = spawn(COMMAND[0], [...COMMAND.slice(1), "--path", file], {
      stdio: "ignore",
      timeout: 2500,
      killSignal: "SIGKILL",
    });
    child.on("error", () => {});
    child.unref();
  } catch {
    // Hint failure must never break a session. Discovery can find retained sources,
    // but cannot recover vanished bytes or missed native launch context.
  }
}

// Capture plain SDK facts inside the native event handler, never collector argv.
function launch(event: SessionStartEvent, ctx: ExtensionContext): void {
  try {
    const file = ctx.sessionManager.getSessionFile();
    if (!file) return; // --no-session has no durable source.
    const payload = JSON.stringify({
      event: "session_start",
      source: event.reason,
      session_file: file,
      session_id: ctx.sessionManager.getSessionId(),
      cwd: ctx.cwd,
      model: ctx.model?.id,
      provider: ctx.model?.provider,
      argv: process.argv.slice(0, 32).map((arg) => arg.slice(0, 256)),
      context_origin: "extension",
    });
    // The CLI enforces the same bound; never split/truncate JSON into ambiguity.
    if (Buffer.byteLength(payload, "utf8") > 65536) return;
    const child = spawn(COMMAND[0], COMMAND.slice(1), {
      stdio: ["pipe", "ignore", "ignore"],
      timeout: 2500,
      killSignal: "SIGKILL",
    });
    child.on("error", () => {});
    child.stdin.on("error", () => {});
    child.stdin.end(payload);
    child.unref();
  } catch {
    // Native metadata failure must never break the session.
  }
}

export default function (pi: ExtensionAPI) {
  pi.on("session_start", (event, ctx) => launch(event, ctx));
  pi.on("agent_settled", (_event, ctx) => kick(ctx.sessionManager.getSessionFile()));
  pi.on("session_shutdown", (_event, ctx) => kick(ctx.sessionManager.getSessionFile()));
}
`
	return []byte(body), nil
}
