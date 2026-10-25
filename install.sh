#!/bin/sh
# Install piggery from a GitHub release: the binary for this OS and CPU, checked against the
# release's checksums.txt, into $PIGGERY_INSTALL_DIR (default ~/.local/bin), and `pig` beside it as a
# short name when nothing else is called that. No sudo.
#
#   curl -fsSL https://raw.githubusercontent.com/sting8k/piggery/main/install.sh | sh
#
# PIGGERY_VERSION=vX.Y.Z installs that release instead of the latest.
set -eu

repo="https://github.com/sting8k/piggery/releases"
dir="${PIGGERY_INSTALL_DIR:-$HOME/.local/bin}"

fail() {
	echo "piggery install: $*" >&2
	exit 1
}

case "$(uname -s)" in
Linux) os=linux ;;
Darwin) os=darwin ;;
*) fail "unsupported OS $(uname -s): builds exist for Linux and macOS" ;;
esac
case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
arm64 | aarch64) arch=arm64 ;;
*) fail "unsupported CPU $(uname -m): builds exist for amd64 and arm64" ;;
esac
name="piggery-$os-$arch"

if [ -n "${PIGGERY_VERSION:-}" ]; then
	url="$repo/download/$PIGGERY_VERSION"
else
	url="$repo/latest/download"
fi

if command -v curl >/dev/null 2>&1; then
	fetch() { curl -fsSL -o "$2" "$1"; }
elif command -v wget >/dev/null 2>&1; then
	fetch() { wget -q -O "$2" "$1"; }
else
	fail "needs curl or wget"
fi
if command -v sha256sum >/dev/null 2>&1; then
	sha256() { sha256sum "$1" | cut -d' ' -f1; }
elif command -v shasum >/dev/null 2>&1; then
	sha256() { shasum -a 256 "$1" | cut -d' ' -f1; }
else
	fail "needs sha256sum or shasum to check the download"
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

echo "piggery install: downloading $name from $url"
fetch "$url/$name" "$tmp/$name" || fail "could not download $url/$name"
fetch "$url/checksums.txt" "$tmp/checksums.txt" || fail "could not download $url/checksums.txt"

want=$(awk -v n="$name" '$2 == n || $2 == "*" n { print $1 }' "$tmp/checksums.txt")
[ -n "$want" ] || fail "checksums.txt has no line for $name"
got=$(sha256 "$tmp/$name")
[ "$got" = "$want" ] || fail "checksum mismatch for $name (got $got, want $want); nothing installed"

mkdir -p "$dir"
chmod +x "$tmp/$name"
# A new file moved over the old one: a running piggery keeps its own copy.
cp "$tmp/$name" "$dir/.piggery.new.$$"
mv -f "$dir/.piggery.new.$$" "$dir/piggery"

echo "piggery install: installed $("$dir/piggery" --version 2>/dev/null || echo "$name") in $dir"

# `pig`, a short name for piggery: a symlink next to it, only when the name is free. One that is
# already a link to piggery is ours and stays; any other `pig` (a file here, or one on PATH) is
# someone's, and is not touched.
if [ -L "$dir/pig" ] && { [ "$(readlink "$dir/pig")" = piggery ] || [ "$(readlink "$dir/pig")" = "$dir/piggery" ]; }; then
	:
elif [ -e "$dir/pig" ] || [ -L "$dir/pig" ]; then
	echo "piggery install: pig is taken by $dir/pig; use piggery"
elif other=$(command -v pig 2>/dev/null) && [ -n "$other" ]; then
	echo "piggery install: pig is taken by $other; use piggery"
else
	ln -s piggery "$dir/pig"
	echo "piggery install: pig is a short name for piggery ($dir/pig)"
fi
case ":$PATH:" in
*":$dir:"*) ;;
*) echo "piggery install: $dir is not on your PATH; add this to your shell profile:
  export PATH=\"$dir:\$PATH\"" ;;
esac
echo "Next: on a new machine, piggery setup <harness> (pi, claude, codex, …; piggery setup alone lists them); upgrading, piggery setup --outdated"
