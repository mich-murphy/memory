#!/bin/sh
# PostToolUse hook: format the Go file Claude just edited, lint its package, and hand any
# problems back to Claude (exit 2) before CI finds them.
command -v jq >/dev/null 2>&1 && command -v golangci-lint >/dev/null 2>&1 || exit 0
file=$(jq -r '.tool_input.file_path // empty')
case "$file" in
*.go) ;;
*) exit 0 ;;
esac
[ -f "$file" ] || exit 0
cd "${CLAUDE_PROJECT_DIR:-.}" || exit 0
golangci-lint fmt "$file" >/dev/null 2>&1
if ! out=$(golangci-lint run "$(dirname "$file")" 2>&1); then
	printf 'golangci-lint found problems in %s:\n%s\n' "$(dirname "$file")" "$out" >&2
	exit 2
fi
