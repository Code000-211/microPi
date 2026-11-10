#!/usr/bin/env bash
# Downloads the released µPi runtime (instead of building it with build-runtime.sh) to $PIBOLT_WORK/runtime/bun.
#
# Usage: scripts/fetch-runtime.sh [--version upi-vX.Y.Z | bolt-vX.Y.Z]     (default: the latest release)
#        scripts/fetch-runtime.sh [--from micropi | pibolt]
# Environment: MICROPI_DOWNLOAD_BASE (a mirror of the µPi release files)
# µPi's releases are at Code000-211/microPi, its runtime is upi-runtime-<platform> and its key keys/release.pub. While µPi
# has no release yet, the runtime comes from Pi-Bolt's latest release instead: --from pibolt, or automatically (its runtime
# is pi-bolt-runtime-<platform> there, its key keys/pibolt-release.pub). Each source's checksums must carry that source
# key's signature, checked with OpenSSL 3 or else with Bun.
source "$(dirname "$0")/lib/common.sh"

TAG=latest
SOURCE=auto
while [ $# -gt 0 ]; do
	case "$1" in
	--version) TAG="$2"; shift ;;
	--from) SOURCE="$2"; shift ;;
	-h | --help) sed -n '2,8p' "$0"; exit 0 ;;
	*) die "unknown option $1" ;;
	esac
	shift
done
need curl

# A µPi tag is upi-v*, a Pi-Bolt tag bolt-v*: the tag names its source, unless --from says otherwise.
case "$TAG" in
latest) ;;
upi-v*) [ "$SOURCE" = auto ] && SOURCE=micropi ;;
bolt-v*) [ "$SOURCE" = auto ] && SOURCE=pibolt ;;
*) die "unknown tag $TAG (upi-v* or bolt-v*)" ;;
esac
case "$SOURCE" in
micropi | pibolt | auto) ;;
*) die "unknown source $SOURCE (micropi or pibolt)" ;;
esac

# The URLs and the file names, per source: which repository has the release, which key signed it, what the runtime archive
# is called there. Pi-Bolt's archive differs from µPi's in the pi-bolt- prefix only.
if [ "$SOURCE" != pibolt ]; then
	MICROPI_REPO="https://github.com/Code000-211/microPi"
	if [ "$TAG" = latest ]; then MICROPI_BASE="$MICROPI_REPO/releases/latest/download"; else MICROPI_BASE="$MICROPI_REPO/releases/download/$TAG"; fi
	MICROPI_BASE="${MICROPI_DOWNLOAD_BASE:-$MICROPI_BASE}"
fi
if [ "$SOURCE" != micropi ]; then
	PIBOLT_RELEASE_REPO="https://github.com/opensec-git/Pi-Bolt"
	if [ "$TAG" = latest ]; then PIBOLT_RELEASE_BASE="$PIBOLT_RELEASE_REPO/releases/latest/download"; else PIBOLT_RELEASE_BASE="$PIBOLT_RELEASE_REPO/releases/download/$TAG"; fi
fi
# pick_source: prints BASE, KEY and the archive's prefix for the resolved source (SOURCE=auto: µPi, but Pi-Bolt while µPi
# has no release's runtime).
pick_source() {
	local have
	if [ "$SOURCE" = auto ]; then
		have=$(curl -sIL -o /dev/null -w '%{http_code}' "$MICROPI_BASE/upi-runtime-$PIBOLT_PLATFORM.tar.gz" || true)
		if [ "$have" = 200 ]; then
			printf '%s\n' "$MICROPI_BASE keys/release.pub upi-runtime"
			return
		fi
		log "no µPi release yet: the runtime comes from Pi-Bolt's latest release"
		printf '%s\n' "$PIBOLT_RELEASE_BASE keys/pibolt-release.pub pi-bolt-runtime"
	elif [ "$SOURCE" = micropi ]; then
		printf '%s\n' "$MICROPI_BASE keys/release.pub upi-runtime"
	else
		printf '%s\n' "$PIBOLT_RELEASE_BASE keys/pibolt-release.pub pi-bolt-runtime"
	fi
}
read -r BASE KEY PREFIX < <(pick_source)
NAME=$PREFIX-$PIBOLT_PLATFORM.tar.gz

# verify_signature FILE SIG KEY: dies unless SIG is KEY's signature of FILE.
verify_signature() {
	local openssl candidate
	for candidate in openssl /opt/homebrew/opt/openssl@3/bin/openssl /usr/local/opt/openssl@3/bin/openssl; do
		command -v "$candidate" >/dev/null 2>&1 || continue
		case "$("$candidate" version 2>/dev/null)" in "OpenSSL "[3-9]* | "OpenSSL "[1-9][0-9]*) openssl=$candidate; break ;; esac
	done
	if [ -n "${openssl:-}" ]; then
		"$openssl" pkeyutl -verify -pubin -inkey "$3" -rawin -in "$1" -sigfile "$2" >/dev/null 2>&1 || die "the signature of $1 does not verify"
	elif command -v bun >/dev/null 2>&1; then
		bun -e 'const c = require("node:crypto"), f = require("node:fs"); const [file, sig, key] = process.argv.slice(-3);
process.exit(c.verify(null, f.readFileSync(file), f.readFileSync(key, "utf8"), f.readFileSync(sig)) ? 0 : 1);' "$1" "$2" "$3" ||
			die "the signature of $1 does not verify"
	else
		die "OpenSSL 3 or Bun is needed to check the release's signature"
	fi
	log "signature verified"
}

TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT
log "downloading $BASE/$NAME"
curl -fL --progress-bar -o "$TMP/$NAME" "$BASE/$NAME"
curl -fsSL -o "$TMP/SHA256SUMS" "$BASE/SHA256SUMS"
curl -fsSL -o "$TMP/SHA256SUMS.sig" "$BASE/SHA256SUMS.sig" || die "no signature at $BASE/SHA256SUMS.sig: the runtime is not used"
verify_signature "$TMP/SHA256SUMS" "$TMP/SHA256SUMS.sig" "$PIBOLT_ROOT/$KEY"
check_sum "$TMP" "$NAME" || die "checksum mismatch for $NAME"
tar -C "$TMP" -xzf "$TMP/$NAME"
BUNDLE="$TMP/${PREFIX}-$PIBOLT_PLATFORM"
[ -x "$BUNDLE/bun" ] || die "no bun in $NAME"
mkdir -p "$PIBOLT_WORK/runtime"
install -m 755 "$BUNDLE/bun" "$PIBOLT_WORK/runtime/bun"
# Which engine it is, as the release's signed checksums give it (scripts/build-pi.sh records it, for `upi doctor`).
if curl -fsSL -o "$TMP/RUNTIME_STAMP" "$BASE/RUNTIME_STAMP" 2>/dev/null && check_sum "$TMP" RUNTIME_STAMP; then
	cp "$TMP/RUNTIME_STAMP" "$PIBOLT_WORK/runtime/RUNTIME_STAMP"
else
	rm -f "$PIBOLT_WORK/runtime/RUNTIME_STAMP"
fi
log "runtime: $PIBOLT_WORK/runtime/bun (Bun $("$PIBOLT_WORK/runtime/bun" --version))"
