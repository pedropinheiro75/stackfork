#!/bin/sh
# Install the latest stackfork release binary.
#
#   curl -fsSL https://raw.githubusercontent.com/pedropinheiro75/stackfork/main/install.sh | sh
#
# STACKFORK_VERSION=v0.2.0   install that tag instead of the latest release
# STACKFORK_INSTALL_DIR=dir  install there (default: /usr/local/bin when
#                            writable, else ~/.local/bin)
set -eu

REPO=pedropinheiro75/stackfork

say() { printf 'stackfork install: %s\n' "$*"; }
die() { printf 'stackfork install: error: %s\n' "$*" >&2; exit 1; }
hint() { die "$1
Install from source instead (Go 1.26.10 or later):
  go install github.com/$REPO/cmd/stackfork@latest"; }

# fetch URL FILE: download URL to FILE, fail on HTTP errors.
if command -v curl >/dev/null 2>&1; then
	fetch() { curl -fsSL -o "$2" "$1"; }
elif command -v wget >/dev/null 2>&1; then
	fetch() { wget -q -O "$2" "$1"; }
else
	die "curl or wget is required"
fi

if command -v sha256sum >/dev/null 2>&1; then
	sha256() { sha256sum "$1" | cut -d ' ' -f 1; }
elif command -v shasum >/dev/null 2>&1; then
	sha256() { shasum -a 256 "$1" | cut -d ' ' -f 1; }
else
	die "sha256sum or shasum is required"
fi

case "$(uname -s)" in
	Darwin) os=darwin ;;
	Linux) os=linux ;;
	*) die "unsupported OS: $(uname -s) (macOS and Linux only)" ;;
esac
case "$(uname -m)" in
	x86_64 | amd64) arch=amd64 ;;
	aarch64 | arm64) arch=arm64 ;;
	*) die "unsupported architecture: $(uname -m) (amd64 and arm64 only)" ;;
esac

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

tag=${STACKFORK_VERSION:-}
if [ -z "$tag" ]; then
	api="https://api.github.com/repos/$REPO/releases/latest"
	fetch "$api" "$tmp/latest.json" 2>/dev/null ||
		hint "no stackfork release found at $api (HTTP 404 means none is published yet)"
	tag=$(sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' "$tmp/latest.json" | head -n 1)
	[ -n "$tag" ] || hint "could not read the latest release tag from $api"
fi
case "$tag" in v*) ;; *) tag="v$tag" ;; esac

archive="stackfork_${tag#v}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$tag"
say "downloading $archive ($tag)"
fetch "$base/$archive" "$tmp/$archive" 2>/dev/null ||
	hint "could not download $base/$archive (is $tag a published release?)"
fetch "$base/checksums.txt" "$tmp/checksums.txt" 2>/dev/null ||
	die "could not download $base/checksums.txt"

want=$(awk -v f="$archive" '$2 == f { print $1 }' "$tmp/checksums.txt")
[ -n "$want" ] || die "$archive is not listed in checksums.txt"
got=$(sha256 "$tmp/$archive")
[ "$want" = "$got" ] || die "checksum mismatch for $archive (want $want, got $got)"

tar -xzf "$tmp/$archive" -C "$tmp" stackfork || die "stackfork binary not found in $archive"

dir=${STACKFORK_INSTALL_DIR:-}
if [ -z "$dir" ]; then
	if [ -d /usr/local/bin ] && [ -w /usr/local/bin ]; then
		dir=/usr/local/bin
	else
		dir="$HOME/.local/bin"
	fi
fi
mkdir -p "$dir"
cp "$tmp/stackfork" "$dir/stackfork.tmp"
chmod 755 "$dir/stackfork.tmp"
mv -f "$dir/stackfork.tmp" "$dir/stackfork"
if [ "$os" = darwin ]; then
	xattr -d com.apple.quarantine "$dir/stackfork" 2>/dev/null || true
fi

say "installed $dir/stackfork"
"$dir/stackfork" version
case ":$PATH:" in
	*":$dir:"*) ;;
	*) say "warning: $dir is not on your PATH; add it, for example: export PATH=\"$dir:\$PATH\"" ;;
esac
