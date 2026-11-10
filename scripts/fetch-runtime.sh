#!/usr/bin/env bash
# Downloads the released µPi runtime (instead of building it with build-runtime.sh) to $PIBOLT_WORK/runtime/bun.
#
# Usage: scripts/fetch-runtime.sh [--version bolt-vX.Y.Z]     (default: the latest release)
# Environment: MICROPI_DOWNLOAD_BASE (a mirror of the release files)
# The checksums must carry the release key's signature (keys/release.pub), checked with OpenSSL 3 or else with Bun.
source "$(dirname "$0")/lib/common.sh"

TAG=latest
while [ $# -gt 0 ]; do
	case "$1" in
	--version) TAG="$2"; shift ;;
	-h | --help) sed -n '2,5p' "$0"; exit 0 ;;
	*) die "unknown option $1" ;;
	esac
	shift
done
need curl
REPO="https://github.com/opensec-git/Pi-Bolt"
if [ "$TAG" = latest ]; then BASE="$REPO/releases/latest/download"; else BASE="$REPO/releases/download/$TAG"; fi
BASE="${MICROPI_DOWNLOAD_BASE:-$BASE}"
NAME=upi-runtime-$PIBOLT_PLATFORM.tar.gz

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
verify_signature "$TMP/SHA256SUMS" "$TMP/SHA256SUMS.sig" "$PIBOLT_ROOT/keys/release.pub"
check_sum "$TMP" "$NAME" || die "checksum mismatch for $NAME"
tar -C "$TMP" -xzf "$TMP/$NAME"
mkdir -p "$PIBOLT_WORK/runtime"
install -m 755 "$TMP/upi-runtime-$PIBOLT_PLATFORM/bun" "$PIBOLT_WORK/runtime/bun"
# Which engine it is, as the release's signed checksums give it (scripts/build-pi.sh records it, for `upi doctor`).
if curl -fsSL -o "$TMP/RUNTIME_STAMP" "$BASE/RUNTIME_STAMP" 2>/dev/null && check_sum "$TMP" RUNTIME_STAMP; then
	cp "$TMP/RUNTIME_STAMP" "$PIBOLT_WORK/runtime/RUNTIME_STAMP"
else
	rm -f "$PIBOLT_WORK/runtime/RUNTIME_STAMP"
fi
log "runtime: $PIBOLT_WORK/runtime/bun (Bun $("$PIBOLT_WORK/runtime/bun" --version))"
