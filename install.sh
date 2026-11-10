#!/usr/bin/env sh
set -eu

REPO="https://github.com/Code000-211/microPi.git"
INSTALL_DIR="${PI_INSTALL_DIR:-$HOME/.local/bin}"
MIN_BUN_VERSION="1.4.4"

has_bun() { command -v bun >/dev/null 2>&1; }

require_bun_version() {
  version_raw=$(bun --version 2>/dev/null || true)
  version_clean=${version_raw%%-*}
  # simple >= check
  case "$version_clean" in
    1.[4-9].*|1.[5-9].*|2.*) ;;
    *) echo "Bun ${MIN_BUN_VERSION} or newer required"; exit 1 ;;
  esac
}

install_bun() {
  echo "Installing bun..."
  if command -v bash >/dev/null 2>&1; then
    curl -fsSL https://bun.sh/install | bash
  else
    curl -fsSL https://bun.sh/install | sh
  fi
  export BUN_INSTALL="$HOME/.bun"
  export PATH="$BUN_INSTALL/bin:$PATH"
  require_bun_version
}

# Create shim dir
SHIM_DIR="$(mktemp -d)"
export PATH="$SHIM_DIR:$PATH"

cat > "$SHIM_DIR/npm" <<'EOF'
#!/usr/bin/env sh
set -e
args="$@"
# map npm run check to bun check
if printf '%s\n' "$args" | grep -q '^run check'; then
  exec bun check --all --no-script
fi
# simple pass-through for ci/run
if printf '%s\n' "$args" | grep -q '^ci'; then
  exec bun ci
elif printf '%s\n' "$args" | grep -q '^run'; then
  # extract script name
  script=$(printf '%s\n' "$args" | awk '{print $2}')
  shift_args=$(printf '%s\n' "$args" | sed 's/[^ ]* run [^ ]* //')
  exec bun run "$script" $shift_args
fi
exec bun "$@"
EOF
chmod +x "$SHIM_DIR/npm"

cat > "$SHIM_DIR/node" <<'EOF'
#!/usr/bin/env sh
exec bun "$@"
EOF
chmod +x "$SHIM_DIR/node"

if ! has_bun; then
  install_bun
else
  require_bun_version
fi

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT
echo "Cloning microPi..."
git clone --depth 1 "$REPO" "$TMP_DIR/microPi"
cd "$TMP_DIR/microPi"

echo "Fetching runtime..."
./scripts/fetch-runtime.sh

echo "Preparing Pi..."
./scripts/prepare-pi.sh

echo "Building Pi..."
./scripts/build-pi.sh --kit .work/kit

mkdir -p "$INSTALL_DIR"
cp out/pi-bolt/pi "$INSTALL_DIR/upi"
chmod +x "$INSTALL_DIR/upi"

echo "✓ Installed upi to $INSTALL_DIR/upi"
"$INSTALL_DIR/upi" --version