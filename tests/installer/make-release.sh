#!/usr/bin/env bash
# Builds a fake µPi release into a folder, to serve tests/installer/run.sh locally (MICROPI_TEST_RELEASE) when gh
# cannot download a published release (offline, or none published yet). The release is signed with the private key in
# MICROPI_TEST_SIGNING_KEY (a PEM Ed25519 key whose public half is the RELEASE_KEY compiled into install.sh,
# keys/release.pub), so the installer's signature check passes.
#
# Usage: MICROPI_TEST_SIGNING_KEY=key.pem tests/installer/make-release.sh OUT VERSION
set -euo pipefail
OUT=$1
VERSION=$2
case "$(uname -s)-$(uname -m)" in Darwin-*) NAME=upi-darwin-arm64 ;; *-aarch64) NAME=upi-linux-arm64 ;; *) NAME=upi-linux-x64 ;; esac

rm -rf "$OUT"
mkdir -p "$OUT/$NAME"
# (A shell script stands in for the build: the installer runs it only for --version.)
cat >"$OUT/$NAME/pi" <<EOF
#!/bin/sh
case "\$1" in --version) printf 'upi v%s\\n' "$VERSION" ;; *) printf 'upi\\n' ;; esac
EOF
cat >"$OUT/$NAME/upi.txt" <<EOF
upi $VERSION
EOF
chmod +x "$OUT/$NAME/pi"
tar -C "$OUT" -czf "$OUT/$NAME.tar.gz" "$NAME"
tar -C "$OUT" -cf - "$NAME" | xz -T0 -z -c >"$OUT/$NAME.tar.xz"
(
	cd "$OUT"
	sum() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$@"; else shasum -a 256 "$@"; fi; }
	{
		printf '# upi %s\n' "$VERSION"
		sum "$NAME.tar.gz" "$NAME.tar.xz"
	} >SHA256SUMS
	[ -n "${MICROPI_TEST_SIGNING_KEY:-}" ] || { echo 'set MICROPI_TEST_SIGNING_KEY to a PEM private key (keys/release.pub is its public half)' >&2; exit 1; }
	openssl pkeyutl -sign -inkey "$MICROPI_TEST_SIGNING_KEY" -rawin -in SHA256SUMS -out SHA256SUMS.sig
)
