#!/bin/sh
# PostToolUse hook: format the Go file Claude just edited, lint its package, and hand any
# problems back to Claude (exit 2) before CI finds them. A missing tool is reported to the person
# (exit 1, which doesn't block Claude) rather than skipped in silence.
for tool in jq golangci-lint; do
	if ! command -v "$tool" >/dev/null 2>&1; then
		echo "$tool is not installed, so Go edits are not being linted" >&2
		exit 1
	fi
done
file=$(jq -r '.tool_input.file_path // empty') || exit 0
root=${CLAUDE_PROJECT_DIR:-$(pwd)}
case "$file" in
"$root"/*.go) ;;
*) exit 0 ;;
esac
case "$file" in
*/testdata/*) exit 0 ;;
esac
[ -f "$file" ] || exit 0
cd "$root" || exit 0
golangci-lint fmt "$file" >/dev/null 2>&1
# Parallel edits fire this hook concurrently; wait for the other run instead of failing.
if ! out=$(golangci-lint run --allow-serial-runners "$(dirname "$file")" 2>&1); then
	printf 'golangci-lint found problems in %s:\n%s\n' "$(dirname "$file")" "$out" >&2
	exit 2
fi
