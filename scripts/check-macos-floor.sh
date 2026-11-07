#!/usr/bin/env bash
# Checks that every Mach-O file under each DIR runs on macOS 13, the oldest µPi supports (install.sh refuses older): its
# minimum macOS (LC_BUILD_VERSION's minos, or LC_VERSION_MIN_MACOSX's version) is 13.0 or lower. Building on a newer macOS or
# Xcode must not raise it.
# Usage: scripts/check-macos-floor.sh DIR...
set -euo pipefail
floor=13.0
status=0
checked=0
while IFS= read -r -d '' file; do
	file -b "$file" | grep -q '^Mach-O' || continue
	min=$(otool -l "$file" | awk '/cmd LC_BUILD_VERSION/ {b = 1} b && /minos/ {print $2; exit} /cmd LC_VERSION_MIN_MACOSX/ {v = 1} v && /version/ {print $2; exit}')
	checked=$((checked + 1))
	if [ -z "$min" ]; then
		echo "FAIL $file: no minimum macOS version"
		status=1
	elif [ "$(printf '%s\n%s\n' "$min" "$floor" | sort -V | tail -n 1)" != "$floor" ]; then
		echo "FAIL $file: needs macOS $min, later than $floor"
		status=1
	else
		echo "PASS $file: macOS $min"
	fi
done < <(find "$@" -type f -size +1k -print0)
[ "$checked" -gt 0 ] || { echo "FAIL no Mach-O file in $*"; exit 1; }
exit "$status"
