#!/bin/sh
# Install or upgrade agent-sessions from a pinned, immutable GitHub release, or uninstall
# it without using the network. Everything runs from main, called on the last line, so a
# truncated download of this script runs nothing.
set -eu

repo=mich-murphy/memory

usage() {
	cat <<'EOF'
Usage: VERSION=vX.Y.Z install.sh [install]
       install.sh uninstall | --help
  install    Download release VERSION with gh, verify it against GitHub's release
             attestation and its checksums.txt, then install it (the default).
  uninstall  Run the installed binary's uninstall to remove managed hooks and
             the Pi extension, then remove that binary. No network is used.
VERSION is required: a release tag such as v0.3.0. There is no "latest".
BIN_DIR selects the binary directory (default ~/.local/bin).
Uninstall preserves config/credentials, machine-id, local state and remote data.
EOF
}

validate_version() {
	case "$1" in
	v[0-9]*) ;;
	*) echo "invalid VERSION: expected a release tag like v0.3.0" >&2; exit 2 ;;
	esac
	case "$1" in
	*[!a-zA-Z0-9._-]*) echo "invalid VERSION: unsafe release tag" >&2; exit 2 ;;
	esac
}

uninstall_binary() {
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
}

cleanup() {
	[ -z "$staged" ] || rm -f "$staged"
	[ -z "$tmp" ] || rm -rf "$tmp"
}

install_release() {
	version=${VERSION:-}
	if [ -z "$version" ]; then
		echo "VERSION is required: choose a release tag such as v0.3.0" >&2
		exit 2
	fi
	validate_version "$version"
	if ! command -v gh >/dev/null 2>&1; then
		echo "gh is required: install the GitHub CLI, then run gh auth login" >&2
		exit 1
	fi
	if ! gh auth status >/dev/null 2>&1; then
		echo "gh is not logged in: run gh auth login" >&2
		exit 1
	fi
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
	trap cleanup 0
	trap 'exit 129' HUP
	trap 'exit 130' INT
	trap 'exit 143' TERM
	tmp=$(mktemp -d)

	# An immutable release's tag and assets can't change after publication, and GitHub
	# signs an attestation of them. The checksum alone only proves the two downloads
	# agree with each other.
	gh release verify "$version" --repo "$repo"
	gh release download "$version" --repo "$repo" --dir "$tmp" --pattern "$asset" --pattern checksums.txt
	gh release verify-asset "$version" "$tmp/$asset" --repo "$repo"
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
}

main() {
	[ "$#" -le 1 ] || { usage >&2; exit 2; }
	command=${1:-install}
	case "$command" in
	install | uninstall) ;;
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
	tmp=
	staged=
	if [ "$command" = uninstall ]; then
		uninstall_binary
	else
		install_release
	fi
}

main "$@"
