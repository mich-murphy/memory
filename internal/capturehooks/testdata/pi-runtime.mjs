import assert from "node:assert/strict";
import childProcess from "node:child_process";
import { readFileSync } from "node:fs";
import { stripTypeScriptTypes, syncBuiltinESMExports } from "node:module";

// All inputs and child results are synthetic. No SDK or collector is loaded.
const calls = [];
let spawnThrows = false;
childProcess.spawn = (command, argv, options) => {
  if (spawnThrows) throw new Error("synthetic spawn failure");
  const call = { command, argv, options, events: [], stdinEvents: [], payload: undefined, unrefs: 0 };
  calls.push(call);
  return {
    on(event, handler) { call.events.push(event); handler(new Error("synthetic child error")); },
    stdin: {
      on(event, handler) { call.stdinEvents.push(event); handler(new Error("synthetic stdin error")); },
      end(payload) { call.payload = payload; },
    },
    unref() { call.unrefs++; },
  };
};
syncBuiltinESMExports();
const source = readFileSync(process.argv[2], "utf8");
const stripped = stripTypeScriptTypes(source, { mode: "strip" });
const { default: register } = await import(`data:text/javascript,${encodeURIComponent(stripped)}`);
const handlers = new Map();
register({ on(name, handler) { assert(!handlers.has(name)); handlers.set(name, handler); } });
assert.deepEqual([...handlers.keys()], ["session_start", "agent_settled", "session_shutdown"]);
assert.equal(calls.length, 0, "registration must not spawn");

function context(file, { forbiddenMetadata = false, throwFile = false } = {}) {
  const reads = { file: 0, id: 0, cwd: 0, model: 0 };
  function metadata(key, value) {
    reads[key]++;
    assert(!forbiddenMetadata, `unexpected ${key} metadata read`);
    return value;
  }
  return {
    reads,
    ctx: {
      sessionManager: {
        getSessionFile() {
          reads.file++;
          if (throwFile) throw new Error("synthetic file getter failure");
          // A second call returns a different value, exposing unpinned reads.
          return reads.file === 1 ? file : "/synthetic/wrong-second-path.jsonl";
        },
        getSessionId() { return metadata("id", "synthetic-session"); },
      },
      get cwd() { return metadata("cwd", "/synthetic/work"); },
      get model() { return metadata("model", { id: "synthetic-model", provider: "synthetic-provider" }); },
    },
  };
}

// --no-session is independent of the collector's disabled marker/policy.
// Empty and undefined durable paths must stop every registered handler before
// spawn and before reading native metadata (especially session_start).
for (const file of [undefined, ""]) {
  for (const [name, handler] of handlers) {
    const { ctx, reads } = context(file);
    handler({ reason: "startup" }, ctx);
    assert.equal(calls.length, 0, `${name} spawned without a durable session`);
    assert.deepEqual(reads, { file: 1, id: 0, cwd: 0, model: 0 }, name);
  }
}
// The launch getter remains inside its preexisting error boundary.
{
  const { ctx, reads } = context(undefined, { forbiddenMetadata: true, throwFile: true });
  assert.doesNotThrow(() => handlers.get("session_start")({ reason: "startup" }, ctx));
  assert.deepEqual(reads, { file: 1, id: 0, cwd: 0, model: 0 });
  assert.equal(calls.length, 0);
}

const command = "/synthetic/collector";
const baseArgs = ["capture-v1", "hook", "--config", "/synthetic/config", "--agent", "pi"];
const file = "/synthetic/session with spaces.jsonl";
const oldArgv = process.argv;
try {
  process.argv = Array.from({ length: 40 }, (_, i) => `synthetic-${i}-` + "a".repeat(300));
  const { ctx, reads } = context(file);
  handlers.get("session_start")({ reason: "resume" }, ctx);
  assert.deepEqual(reads, { file: 1, id: 1, cwd: 1, model: 2 });
  assert.equal(calls.length, 1);
  const call = calls[0];
  assert.equal(call.command, command);
  assert.deepEqual(call.argv, baseArgs);
  assert.deepEqual(call.options, { stdio: ["pipe", "ignore", "ignore"], timeout: 2500, killSignal: "SIGKILL" });
  assert.deepEqual(JSON.parse(call.payload), {
    event: "session_start", source: "resume", session_file: file,
    session_id: "synthetic-session", cwd: "/synthetic/work",
    model: "synthetic-model", provider: "synthetic-provider",
    argv: process.argv.slice(0, 32).map(arg => arg.slice(0, 256)),
    context_origin: "extension",
  });
  assert.deepEqual(call.events, ["error"]);
  assert.deepEqual(call.stdinEvents, ["error"]);
  assert.equal(call.unrefs, 1);
} finally {
  process.argv = oldArgv;
}

for (const name of ["agent_settled", "session_shutdown"]) {
  const { ctx, reads } = context(file, { forbiddenMetadata: true });
  handlers.get(name)({}, ctx);
  assert.deepEqual(reads, { file: 1, id: 0, cwd: 0, model: 0 });
  const call = calls.at(-1);
  assert.equal(call.command, command);
  assert.deepEqual(call.argv, [...baseArgs, "--path", file]);
  assert.deepEqual(call.options, { stdio: "ignore", timeout: 2500, killSignal: "SIGKILL" });
  assert.equal(call.payload, undefined);
  assert.deepEqual(call.events, ["error"]);
  assert.equal(call.unrefs, 1);
}
assert.equal(calls.length, 3);
// Preserve the existing JSON byte bound and synchronous spawn error handling.
{
  const { ctx } = context(file);
  ctx.sessionManager.getSessionId = () => "x".repeat(65536);
  handlers.get("session_start")({ reason: "startup" }, ctx);
  assert.equal(calls.length, 3, "oversized metadata spawned");
}
spawnThrows = true;
for (const handler of handlers.values()) {
  const { ctx } = context(file);
  assert.doesNotThrow(() => handler({ reason: "startup" }, ctx));
}
assert.equal(calls.length, 3);
