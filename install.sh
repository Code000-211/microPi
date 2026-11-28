#!/bin/sh
# SPDX-License-Identifier: MIT
# Installs µPi (the upi executable) from a GitHub release of Code000-211/microPi on Linux or macOS:
#
#   curl -fsSL https://github.com/Code000-211/microPi/releases/latest/download/install.sh | sh
#
# It downloads the executable for this machine and the release's SHA256SUMS, checks the executable against it, and installs it as
# $MICROPI_BIN_DIR/upi (default ~/.local/bin/upi). Run it again to update or reinstall; `upi update` updates too.
#
#   MICROPI_VERSION   a release tag such as upi-v0.2.1 (default: the latest release)
#   MICROPI_BIN_DIR   where upi goes (default: ~/.local/bin)
set -eu

REPO="https://github.com/Code000-211/microPi"
VERSION="${MICROPI_VERSION:-latest}"
BIN_DIR="${MICROPI_BIN_DIR:-$HOME/.local/bin}"

fail() {
	printf 'µPi install: %s\n' "$1" >&2
	exit 1
}

case "$VERSION" in
latest) BASE="$REPO/releases/latest/download" ;;
upi-v[0-9]*.[0-9]*.[0-9]*) BASE="$REPO/releases/download/$VERSION" ;;
*) fail "MICROPI_VERSION must be a release tag such as upi-v0.2.1" ;;
esac

case "$(uname -s)" in
Linux) os=linux ;;
Darwin) os=darwin ;;
*) fail "this installer is for Linux and macOS; on Windows use install.ps1" ;;
esac
case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) fail "no µPi build for $(uname -m)" ;;
esac
# A shell under Rosetta reports x86_64 on Apple silicon: the arm64 build is the native one.
if [ "$os" = darwin ] && [ "$arch" = amd64 ] && [ "$(sysctl -n sysctl.proc_translated 2>/dev/null || echo 0)" = 1 ]; then
	arch=arm64
fi
NAME="upi-$os-$arch"

if command -v curl >/dev/null 2>&1; then
	fetch() { curl -fsSL --retry 3 -o "$2" "$1"; }
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

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT INT TERM

printf 'Downloading %s (%s) ...\n' "$NAME" "$VERSION"
fetch "$BASE/SHA256SUMS" "$TMP/SHA256SUMS" || fail "could not download $BASE/SHA256SUMS"
fetch "$BASE/$NAME" "$TMP/upi" || fail "could not download $BASE/$NAME"

want=$(awk -v n="$NAME" '$2 == n || $2 == "*" n { print $1 }' "$TMP/SHA256SUMS")
[ -n "$want" ] || fail "SHA256SUMS has no line for $NAME"
got=$(sha256 "$TMP/upi")
[ "$got" = "$want" ] || fail "checksum mismatch for $NAME (expected $want, got $got): nothing was installed"

chmod 755 "$TMP/upi"
"$TMP/upi" --version >/dev/null 2>&1 || fail "the downloaded upi does not run on this machine"
mkdir -p "$BIN_DIR"
# Replaced by rename, so a running upi keeps its file.
mv -f "$TMP/upi" "$BIN_DIR/upi.new" && mv -f "$BIN_DIR/upi.new" "$BIN_DIR/upi"

printf 'Installed %s as %s\n' "$("$BIN_DIR/upi" --version 2>/dev/null)" "$BIN_DIR/upi"
case ":$PATH:" in
*":$BIN_DIR:"*) ;;
*) printf '%s is not on your PATH: add it, for example with\n  export PATH="%s:$PATH"\n' "$BIN_DIR" "$BIN_DIR" ;;
esac
