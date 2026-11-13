#!/bin/sh
# Builds the addon and installs the extension to ~/.pi/agent/extensions/docsearch. For development: a release has
# project_search compiled in already (build-pi.sh --builtins), and an installed copy would merely replace it here.
# Afterwards: upi compile   (compiles what is installed here into the executable)
set -e
cd "$(dirname "$0")"
case "$(uname -s)" in
  Linux) os=linux lib=libdocsearch_node.so ;;
  Darwin) os=darwin lib=libdocsearch_node.dylib ;;
  MINGW* | MSYS* | CYGWIN*) os=win32 lib=docsearch_node.dll ;;
  *) echo "docsearch: no build for $(uname -s)" >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=x64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) echo "docsearch: no build for $(uname -m)" >&2; exit 1 ;;
esac
PATH="$HOME/.cargo/bin:$PATH" cargo build --release -p docsearch-node
dest="${PI_CODING_AGENT_DIR:-$HOME/.pi/agent}/extensions/docsearch"
mkdir -p "$dest"
cp "target/release/$lib" "$dest/docsearch.$os-$arch.node"
# The extension loads the addon by a literal path (so that `upi compile` embeds it): this platform's.
sed "s/docsearch\.linux-x64\.node/docsearch.$os-$arch.node/" extension/index.ts > "$dest/index.ts"
echo "installed to $dest ($os-$arch)"
