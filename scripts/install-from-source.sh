#!/usr/bin/env bash
# Builds µPi from source in one go and installs the build as the `upi` command, where install.sh puts a release: the build's
# directory as MICROPI_INSTALL (default ~/.micropi)/upi-<platform>-<variant> and the `upi` command linked into MICROPI_BIN_DIR
# (default ~/.local/bin). The build replaced is kept as upi-<platform>-<variant>.previous, as install.sh keeps the release it
# replaces, for `upi update --rollback`. The compile kit is kept beside the build, for `upi compile --kit` with a runtime from
# the installed build.
#
#   curl -fsSL https://raw.githubusercontent.com/Code000-211/microPi/main/scripts/install-from-source.sh | bash
#
# Run inside a checkout, it builds that checkout. Piped from curl, it clones Code000-211/microPi first (to MICROPI_SOURCE,
# default ~/.micropi/source) and re-runs itself from the clone. Run it again to build from the newest sources and replace the
# installed build.
#
# Usage: scripts/install-from-source.sh [build-pi.sh options]
# Every option but --out and --kit is handed to build-pi.sh (--jit on|off, --cpu native|baseline, --builtins, ...);
# --out is out/upi and the kit is .work/kit, beside the runtime and the build. MICROPI_VARIANT names the installed
# folder's variant (x64, x64-baseline, arm64, and x64-jit and the rest with --jit on); by default the variant is what
# install.sh would pick: arm64 on macOS and ARM64, on x86-64 x64 with AVX2 and x64-baseline without.
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

# Piped runs re-run themselves from the clone, which has lib/common.sh: the checks of the sources here cover both paths.
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

ARGS=()
CPU=""
JIT=""
while [ $# -gt 0 ]; do
	case "$1" in
	-h | --help) sed -n '2,17p' "$0"; exit 0 ;;
	--out | --kit) die "install-from-source.sh picks $1 itself: out/upi and $PIBOLT_WORK/kit" ;;
	--pi) die "install-from-source.sh builds µPi itself: build another tree with prepare-pi.sh, build-pi.sh and package-release.sh" ;;
	--cpu) CPU="$2"; shift ;;
	--jit) JIT="$2"; shift ;;
	*) ARGS+=("$1") ;;
	esac
	shift
done

if [ ! -x "$PIBOLT_WORK/runtime/bun" ] && [ -z "${PIBOLT_BUN:-}" ]; then
	log "no runtime at $PIBOLT_WORK/runtime/bun: fetching the released one"
	"$PIBOLT_ROOT/scripts/fetch-runtime.sh"
fi

log "building Pi (scripts/prepare-pi.sh)"
"$PIBOLT_ROOT/scripts/prepare-pi.sh"

log "compiling µPi into $PIBOLT_ROOT/out/upi (scripts/build-pi.sh)"
"$PIBOLT_ROOT/scripts/build-pi.sh" --kit "$PIBOLT_WORK/kit" ${ARGS[@]+"${ARGS[@]}"}

# The variant's name in the installation, as install.sh names the folders it installs.
if [ -z "${MICROPI_VARIANT:-}" ]; then
	if [ "$PIBOLT_OS" = darwin ] || [ "$PIBOLT_ARCH" = arm64 ]; then VARIANT=arm64
	elif [ "$CPU" = baseline ] || ! grep -qw avx2 /proc/cpuinfo 2>/dev/null; then VARIANT=x64-baseline; else VARIANT=x64; fi
	case "$JIT" in
	on) VARIANT="$VARIANT-jit" ;;
	esac
else
	VARIANT="$MICROPI_VARIANT"
fi
case "$PIBOLT_OS-$VARIANT" in
darwin-arm64 | darwin-arm64-jit) ;;
darwin-*) die "MICROPI_VARIANT must be arm64 or arm64-jit on macOS" ;;
linux-arm64 | linux-arm64-jit) [ "$PIBOLT_ARCH" = arm64 ] || die "$VARIANT on x86-64: set x64, x64-baseline or x64-jit" ;;
linux-x64*) [ "$PIBOLT_ARCH" != arm64 ] || die "$VARIANT on ARM64: set arm64 or arm64-jit" ;;
*) die "MICROPI_VARIANT must be x64, x64-baseline or x64-jit on x86-64" ;;
esac
NAME="upi-$PIBOLT_OS-$VARIANT"

INSTALL="${MICROPI_INSTALL:-$HOME/.micropi}"
BIN_DIR="${MICROPI_BIN_DIR:-$HOME/.local/bin}"

if [ -e "$INSTALL/$NAME" ]; then
	log "moving the build installed at $INSTALL/$NAME to $NAME.previous"
	rm -rf "$INSTALL/$NAME.previous"
	mv "$INSTALL/$NAME" "$INSTALL/$NAME.previous"
fi
mkdir -p "$INSTALL" "$BIN_DIR"
cp -R "$PIBOLT_ROOT/out/upi" "$INSTALL/$NAME"
cp "$PIBOLT_ROOT/LICENSE" "$INSTALL/$NAME/LICENSE"
ln -sfn "$INSTALL/$NAME/pi" "$BIN_DIR/upi"

"$INSTALL/$NAME/pi" --version >/dev/null 2>&1 || die "the built executable does not run on this system"

if [ "$(command -v upi 2>/dev/null || true)" != "$BIN_DIR/upi" ]; then
	warn "$BIN_DIR is not on your PATH: add it, then run upi"
fi

printf '\nµPi %s installed: %s (%s), linked as %s/upi.\n' \
	"$("$INSTALL/$NAME/pi" --version)" "$INSTALL/$NAME" "$PIBOLT_PLATFORM" "$BIN_DIR"
if [ -f "$INSTALL/compile.json" ]; then
	printf '\nExtensions were compiled into the build this replaced: run upi compile to compile them into this one.\n'
else
	printf '\nExtensions to compile in: upi compile --kit %s --runtime %s/runtime/bun\n' "$PIBOLT_WORK/kit" "$INSTALL/$NAME/runtime/bun"
fi
