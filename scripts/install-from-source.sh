#!/usr/bin/env bash
# Builds µPi from source and installs it as the `upi` command, where install.sh puts a release: the build's directory
# as MICROPI_INSTALL (default ~/.micropi)/upi-<platform>-<variant> and the `upi` command linked into MICROPI_BIN_DIR
# (default ~/.local/bin). The build replaced is kept as upi-<platform>-<variant>.previous, as install.sh keeps the
# release it replaces, for `upi update --rollback`.
#
# Usage: scripts/install-from-source.sh [build-pi.sh options]
# Every option but --out and --kit is handed to build-pi.sh (--jit on|off, --cpu native|baseline, --builtins, ...);
# --out is out/upi and the kit is .work/kit, beside the runtime and the build. MICROPI_VARIANT names the installed
# folder's variant (x64, x64-baseline, arm64, and x64-jit and the rest with --jit on); by default the variant is what
# install.sh would pick: arm64 on macOS and ARM64, on x86-64 x64 with AVX2 and x64-baseline without.
source "$(dirname "$0")/lib/common.sh"

ARGS=()
CPU=""
JIT=""
while [ $# -gt 0 ]; do
	case "$1" in
	-h | --help) sed -n '2,10p' "$0"; exit 0 ;;
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
	case "$JIT" in on) VARIANT="$VARIANT-jit" ;; esac
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
ln -sfn "$INSTALL/$NAME/pi" "$BIN_DIR/upi"

printf '\nµPi %s installed: %s (%s), linked as %s/upi.\n' \
	"$("$INSTALL/$NAME/pi" --version)" "$INSTALL/$NAME" "$PIBOLT_PLATFORM" "$BIN_DIR"
if [ -f "$INSTALL/compile.json" ]; then
	printf '\nExtensions were compiled into the build this replaced: run upi compile to compile them into this one.\n'
else
	printf '\nExtensions to compile in: upi compile --kit %s --runtime %s/runtime/bun\n' "$PIBOLT_WORK/kit" "$PIBOLT_WORK"
fi
