#!/bin/sh
# Install/upgrade from a GitHub release, or uninstall without downloading anything.
# VERSION=v0.1.0 picks a release (default: latest); BIN_DIR defaults to ~/.local/bin.
set -eu

usage() {
	cat <<'EOF'
Usage: install.sh [install|uninstall|--help]
  install    Download and checksum-verify agent-sessions (default).
  uninstall  Run the installed binary's uninstall to remove managed hooks and
             the Pi extension, then remove that binary. No network is used.
Uninstall preserves config/credentials, machine-id, local state and remote data.
VERSION selects a release (default: latest); BIN_DIR selects the binary directory.
EOF
}

[ "$#" -le 1 ] || { usage >&2; exit 2; }
case "${1:-install}" in
install) ;;
uninstall) ;;
--help) usage; exit 0 ;;
*) usage >&2; exit 2 ;;
esac

bin_dir=${BIN_DIR:-$HOME/.local/bin}
# Make paths absolute, including relative paths beginning with a dash.
case "$bin_dir" in
/*) ;;
*) bin_dir=$(pwd)/$bin_dir ;;
esac
binary=$bin_dir/agent-sessions
if [ "${1:-install}" = uninstall ]; then
	if [ ! -e "$binary" ] && [ ! -L "$binary" ]; then
		echo "agent-sessions is not installed at $binary"
		exit 0
	fi
	# Don't run or delete a foreign symlink, directory or other special file.
	if [ -L "$binary" ] || [ ! -f "$binary" ] || [ ! -x "$binary" ]; then
		echo "refusing to uninstall: $binary is not a regular executable" >&2
		exit 1
	fi
	"$binary" uninstall
	# set -e ensures failed hook cleanup leaves the binary available for retry.
	rm -f "$binary"
	echo "uninstalled agent-sessions (config and collected data preserved)"
	exit 0
fi

repo=mich-murphy/memory
version=${VERSION:-latest}
validate_version() {
	case "$1" in
	v[0-9]*) ;;
	*) echo "invalid VERSION: expected a release tag like v0.1.0" >&2; exit 2 ;;
	esac
	case "$1" in
	*[!a-zA-Z0-9._-]*) echo "invalid VERSION: unsafe release tag" >&2; exit 2 ;;
	esac
}
[ "$version" = latest ] || validate_version "$version"
case "$(uname -s)" in
Linux) os=linux ;;
Darwin) os=darwin ;;
*) echo "unsupported OS" >&2; exit 1 ;;
esac
case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
arm64 | aarch64) arch=arm64 ;;
*) echo "unsupported CPU" >&2; exit 1 ;;
esac
asset=agent-sessions_${os}_${arch}

# Each invocation owns its download directory and exclusively created sibling
# file. Never use the predictable .agent-sessions.new (it could be a symlink).
tmp=
staged=
cleanup() {
	[ -z "$staged" ] || rm -f "$staged"
	[ -z "$tmp" ] || rm -rf "$tmp"
}
trap cleanup 0
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM
tmp=$(mktemp -d)

if command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then
	if [ "$version" = latest ]; then
		version=$(gh release view --repo "$repo" --json tagName --jq .tagName)
	fi
	validate_version "$version"
	gh release download "$version" --repo "$repo" --dir "$tmp" --pattern "$asset" --pattern checksums.txt
else
	if [ "$version" = latest ]; then
		# Resolve once: binary and checksum must come from the same release even
		# if a new release is published between the two downloads.
		url=$(curl -fsSL -o /dev/null -w '%{url_effective}' "https://github.com/$repo/releases/latest")
		prefix=https://github.com/$repo/releases/tag/
		case "$url" in
		"$prefix"*) version=${url#"$prefix"} ;;
		*) echo "could not resolve latest release" >&2; exit 1 ;;
		esac
	fi
	validate_version "$version"
	base=https://github.com/$repo/releases/download/$version
	curl -fsSL -o "$tmp/$asset" "$base/$asset"
	curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt"
fi

(
	cd "$tmp"
	grep " $asset\$" checksums.txt >expected
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum -c expected
	else
		shasum -a 256 -c expected
	fi
)

mkdir -p "$bin_dir"
[ ! -d "$binary" ] || { echo "refusing to replace directory: $binary" >&2; exit 1; }
staged=$(mktemp "$bin_dir/.agent-sessions.XXXXXXXXXX")
# Rename a complete sibling file: running syncs never see a half-written binary.
cp "$tmp/$asset" "$staged"
chmod 0755 "$staged"
mv -f "$staged" "$binary"
staged=
echo "installed $("$binary" version 2>/dev/null || echo agent-sessions) to $bin_dir"
case ":$PATH:" in
*":$bin_dir:"*) ;;
*) echo "add $bin_dir to PATH, then run: agent-sessions install" ;;
esac
