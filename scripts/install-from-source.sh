#!/usr/bin/env bash
# Builds µPi from source in one go and installs it.
#
#   curl -fsSL https://raw.githubusercontent.com/Code000-211/microPi/main/scripts/install-from-source.sh | bash
#
# Run inside a checkout, it builds that checkout. Piped from curl, it clones Code000-211/microPi first (to MICROPI_SOURCE,
# default ~/.micropi/source) and re-runs itself from the clone.
#
# It fetches the released µPi runtime, builds Pi and compiles µPi ahead of time, then installs the build to ~/.micropi/upi-
# <platform> — the layout install.sh uses, and where `upi` finds its assets and native modules beside itself — links `upi` into
# ~/.local/bin and keeps the compile kit, for `upi compile --kit`. Run it again to build from the newest sources and replace the
# installed build.
#
# Needs Linux x64 or ARM64 or macOS Apple silicon (as a run of install.sh needs), and to build: git, python3, Node.js 22.19 or
# later with npm, curl, and bash 4.4 or later (on macOS: brew install bash). No Bun, no shims: the released runtime is
# downloaded, `npm ci --ignore-scripts` runs npm.
# Windows is not supported.

# (Some environments do not set HOME, which $HOME/.micropi and npm need.)
if [ -z "${HOME:-}" ]; then HOME="$(cd ~ && pwd)"; export HOME; fi

# --- Where the sources are ---------------------------------------------------------------------------------------------

SCRIPTS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [ -f "$SCRIPTS_DIR/lib/common.sh" ]; then
	IN_CHECKOUT=1
else
	IN_CHECKOUT=""
fi

# In a checkout the shared helpers of the build scripts are here; piped, git and curl are checked before anything else, the
# clone is made, and the checks of everything the build needs run from the clone's copy of this script.
if [ -z "$IN_CHECKOUT" ]; then
	missing=""
	for tool in git curl; do
		command -v "$tool" >/dev/null 2>&1 || missing="$missing $tool"
	done
	if [ -n "$missing" ]; then
		printf 'error: not installed:%s — install them, then run this again\n' "$missing" >&2
		exit 1
	fi
	set -eu
	if [ "${BASH_VERSINFO[0]}" -lt 4 ] || { [ "${BASH_VERSINFO[0]}" -eq 4 ] && [ "${BASH_VERSINFO[1]}" -lt 4 ]; }; then
		printf 'error: bash 4.4 or later is required (this is %s); on macOS: brew install bash\n' "$BASH_VERSION" >&2
		exit 1
	fi
	SRC="${MICROPI_SOURCE:-$HOME/.micropi/source}"
	if [ -d "$SRC/.git" ]; then
		printf '==> updating the sources at %s\n' "$SRC" >&2
		git -C "$SRC" fetch --depth 1 origin "$(git -C "$SRC" rev-parse --abbrev-ref HEAD 2>/dev/null || echo HEAD)"
		git -C "$SRC" checkout -q FETCH_HEAD
	else
		printf '==> cloning Code000-211/microPi to %s\n' "$SRC" >&2
		mkdir -p "$SRC"
		git clone -q --depth 1 https://github.com/Code000-211/microPi "$SRC"
	fi
	exec "$SRC/scripts/install-from-source.sh" "$@"
fi

source "$SCRIPTS_DIR/lib/common.sh"

missing=""
for tool in git python3 curl node npm; do
	command -v "$tool" >/dev/null 2>&1 || missing="$missing $tool"
done
if [ -n "$missing" ]; then
	die "not installed:$missing — install them, then run this again"
fi
if ! node -e 'const v = process.versions.node.split("."); process.exit(v[0] > 22 || (v[0] == 22 && v[1] >= 19) ? 0 : 1)' 2>/dev/null; then
	die "$(node --version) is too old: µPi builds with Node.js 22.19 or later"
fi

INSTALL="${MICROPI_INSTALL:-$HOME/.micropi}"
BIN_DIR="${MICROPI_BIN_DIR:-$HOME/.local/bin}"

log "fetching the µPi runtime"
"$PIBOLT_ROOT/scripts/fetch-runtime.sh"
log "building Pi"
"$PIBOLT_ROOT/scripts/prepare-pi.sh"
KIT="$PIBOLT_ROOT/out/upi-kit-$PIBOLT_PLATFORM"
log "compiling µPi ahead of time"
"$PIBOLT_ROOT/scripts/build-pi.sh" --kit "$KIT"

BUILD="$PIBOLT_ROOT/out/upi"
[ -x "$BUILD/pi" ] || die "$BUILD/pi is not there: the build did not produce an executable"

# Installed as install.sh installs a release: the whole build, and the license files. The compile kit is kept for
# `upi compile --kit`.
log "installing to $INSTALL/upi-$PIBOLT_PLATFORM"
mkdir -p "$INSTALL"
rm -rf "$INSTALL/upi-$PIBOLT_PLATFORM"
cp -R "$BUILD" "$INSTALL/upi-$PIBOLT_PLATFORM"
cp "$PIBOLT_ROOT/LICENSE" "$INSTALL/upi-$PIBOLT_PLATFORM/LICENSE"
rm -rf "$INSTALL/upi-kit-$PIBOLT_PLATFORM"
cp -R "$KIT" "$INSTALL/upi-kit-$PIBOLT_PLATFORM"

"$INSTALL/upi-$PIBOLT_PLATFORM/pi" --version >/dev/null || die "the built executable does not run on this system"

mkdir -p "$BIN_DIR"
ln -sfn "$INSTALL/upi-$PIBOLT_PLATFORM/pi" "$BIN_DIR/upi"
if [ "$(command -v upi 2>/dev/null || true)" != "$BIN_DIR/upi" ]; then
	warn "$BIN_DIR is not on your PATH: add it, then run upi"
fi
version="$("$INSTALL/upi-$PIBOLT_PLATFORM/pi" --version)"
printf '\n%s was built from source and installed to %s (the kit: %s).\nRun `upi compile --kit %s` to add extensions.\n' \
	"$version" "$INSTALL/upi-$PIBOLT_PLATFORM" "$INSTALL/upi-kit-$PIBOLT_PLATFORM" "$INSTALL/upi-kit-$PIBOLT_PLATFORM"
