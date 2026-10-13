#!/bin/sh
tell_person() {
	echo "$1" >&2
	exit 1
}

tell_claude() {
	echo "$1" >&2
	exit 2
}

for tool in jq golangci-lint; do
	command -v "$tool" >/dev/null 2>&1 || tell_person "$tool is not installed, so Go edits are not being linted"
done
file=$(jq -r '.tool_input.file_path // empty') || exit 0
root=${CLAUDE_PROJECT_DIR:-$(pwd)}
case "$file" in
"$root"/*/testdata/*) exit 0 ;;
"$root"/*.go) ;;
*) exit 0 ;;
esac
[ -f "$file" ] || exit 0
cd "$root" || exit 0
golangci-lint fmt "$file" >/dev/null 2>&1
pkg=$(dirname "$file")
if ! out=$(golangci-lint run --allow-serial-runners "$pkg" 2>&1); then
	tell_claude "golangci-lint found problems in $pkg:
$out"
fi
