#!/bin/sh
# Install or upgrade agent-sessions from a GitHub release, checking its sha256.
#   curl -fsSL https://raw.githubusercontent.com/mich-murphy/memory/main/install.sh | sh
# VERSION=v0.1.0 picks a release (default: the latest); BIN_DIR picks where (default ~/.local/bin).
# Uses gh when it is installed and logged in (needed while the repo is private), else curl.
set -eu

repo=mich-murphy/memory
version=${VERSION:-latest}
bin_dir=${BIN_DIR:-$HOME/.local/bin}

case "$(uname -s)" in
Linux) os=linux ;;
Darwin) os=darwin ;;
*) echo "unsupported OS: $(uname -s)" >&2; exit 1 ;;
esac
case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
arm64 | aarch64) arch=arm64 ;;
*) echo "unsupported CPU: $(uname -m)" >&2; exit 1 ;;
esac
asset=agent-sessions_${os}_${arch}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

if command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then
	tag=$version
	[ "$tag" = latest ] && tag=
	# shellcheck disable=SC2086 # an empty tag means the latest release
	gh release download $tag --repo "$repo" --dir "$tmp" --pattern "$asset" --pattern checksums.txt
else
	if [ "$version" = latest ]; then
		base=https://github.com/$repo/releases/latest/download
	else
		base=https://github.com/$repo/releases/download/$version
	fi
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
# Copy next to the target, then rename: a running sync never sees a half-written binary.
cp "$tmp/$asset" "$bin_dir/.agent-sessions.new"
chmod 0755 "$bin_dir/.agent-sessions.new"
mv -f "$bin_dir/.agent-sessions.new" "$bin_dir/agent-sessions"
echo "installed $("$bin_dir/agent-sessions" version 2>/dev/null || echo agent-sessions) to $bin_dir"
case ":$PATH:" in
*":$bin_dir:"*) ;;
*) echo "add $bin_dir to PATH, then run: agent-sessions install" ;;
esac
