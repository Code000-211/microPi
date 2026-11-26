# Explorer 2 Handoff: Toolchain & microPi Conventions

## 1. Local Environment & Toolchain (`/home/antigravity/Code000-211/microPi`)
- **Go**: `go version go1.24.3 linux/amd64` (`GOROOT=/usr/local/go`, `GOMODCACHE=/go/pkg/mod`)
- **`GOTOOLCHAIN=auto`**: Enabled (`go env GOTOOLCHAIN` -> `auto`). `GOPROXY=https://proxy.golang.org,direct`, `GOSUMDB=sum.golang.org`. `https://proxy.golang.org` is reachable (HTTP/2 200), so `go` will auto-download `go1.27.4` if `go.mod` specifies `go 1.27.4` / `toolchain go1.27.4`.
- **Git**: `git version 2.56.1`
- **ripgrep**: `ripgrep 14.1.0` (PCRE2 10.42 with JIT; runtime SIMD SSE2/SSSE3/AVX2)
- **SSH**: `OpenSSH_9.6p1 Ubuntu-3ubuntu13.20, OpenSSL 3.0.13 30 Jan 2024`
- **Make**: `GNU Make 4.3`
- **GitHub CLI**: `gh version 2.103.3 (2026-10-26)`
- **Git status**: On branch `pi-bolt` tracking `origin/pi-bolt`, clean except untracked `.agents/` and `PROJECT.md`.

## 2. microPi Conventions & Expected Values

### Version & Branding
- **`VERSION` file** (`/home/antigravity/Code000-211/microPi/VERSION:1`): `0.1.0`
- **Upstream Pi parity version in current TS repo** (`packages/coding-agent/package.json:3`): `1.4.0` (PiG M1 target in `PROJECT.md` is `0.8.0+1.6.0` / Pi `v1.6.0` parity).
- **Branding constants** (`packages/coding-agent/src/config.ts:588-598`, `packages/coding-agent/src/main.ts:684-688`):
  - `COMMAND_NAME`: `"upi"`
  - `PRODUCT_NAME`: `"µPi"`
  - `APP_TITLE`: `"µPi"`
  - `APP_NAME`: `"pi"` (kept `"pi"` in TS so `ENV_AGENT_DIR` / `CONFIG_DIR_NAME` remain Pi-compatible)
  - `upi --version` format in `main.ts:686`: `µPi <version> (Pi <pi_version>, <os>-<arch/variant>, ...)`

### Home & Agent Directories
- **Install / Home directory (`~/.micropi`)**:
  - `install.sh:18,51`: `MICROPI_INSTALL` defaults to `$HOME/.micropi` (binary installed under `$INSTALL/upi-<platform>-<variant>/`, channel saved in `$INSTALL/channel`, symlinked to `$MICROPI_BIN_DIR/upi` defaulting to `~/.local/bin`).
  - `npm/bin/upi:8` & `packages/coding-agent/src/upi.ts:214,335`: `MICROPI_HOME` defaults to `~/.micropi` (used by npm launcher and `upi update`).
- **Agent config directory (`~/.pi/agent`)**:
  - **Important discrepancy**: `MICROPI_CODING_AGENT_DIR` does **not** exist in the current microPi TypeScript codebase.
  - `packages/coding-agent/src/config.ts:597,601,621-627` uses `CONFIG_DIR_NAME = ".pi"` and `ENV_AGENT_DIR = "PI_CODING_AGENT_DIR"` (`getAgentDir()` defaults to `~/.pi/agent`).
  - `README.md:106-107` explicitly documents that µPi installs into `~/.micropi` while reading `~/.pi` settings, skills, and extensions so existing Pi setups keep working.
  - Note: `PROJECT.md:60` proposes `PIG_HOME` defaulting to `~/.micropi` / `MICROPI_HOME` for `cmd/upi`; M1 should keep `~/.pi/agent` (`PI_CODING_AGENT_DIR`) compatible or explicitly bridge `MICROPI_CODING_AGENT_DIR` / `MICROPI_HOME` as needed.

### `upi doctor` Checks (`packages/coding-agent/src/cli/doctor.ts`)
- CLI: `upi doctor [--offline]` (`main.ts:644-647`, `doctor.ts:66-104`; also respects `PI_OFFLINE`).
- Output format (`formatDoctorReport`, `doctor.ts:1052-1072`): Plain text starting with `µPi doctor`, sections with 2-space indented aligned rows, followed by `Problems` (`none found` if empty) and optional `Suggestions`. Redacted via `redactReport` (`~` for `$HOME`, masks URL credentials/query strings and API keys/tokens).
- Sections & checks:
  1. **`µPi`** (`buildSection`, `doctor.ts:169-196`): version (`µPi <ver> (Pi <pi_ver>)`), build/platform/arch, runtime/engine info, executable path. (Bun AOT / `BUN_AOT` checks are Pi-Bolt specific and will be replaced/adapted for Go static binary).
  2. **`Installation`** (`installSection`, `doctor.ts:368-445`): install method (`installer`, `npm`, `homebrew`, `scoop`, `mise`, `manual`, `not installed`) and install dir (`~/.micropi` / `MICROPI_INSTALL`), other variants in install dir, `upi on PATH` analysis (`analyzePiBoltOnPath`: first `upi` on `PATH` and whether it resolves to this binary), online release signature check (`SHA256SUMS` + `SHA256SUMS.sig` against `MICROPI_RELEASE_KEY` Ed25519 key for `upi-v<version>`), and latest release check (`getLatestPiRelease`).
  3. **`System`** (`systemSection`, `doctor.ts:481-497`): OS/kernel (`systemName`), CPU model/threads/arch/memory, non-secret `REPORTED_VARIABLES` (`MICROPI_VARIANT`, `MICROPI_HOME`, `MICROPI_INSTALL`, `MICROPI_NPM`, `PI_CODING_AGENT_DIR`, `PI_OFFLINE`, `PI_SKIP_VERSION_CHECK`, `TERM`, `TERM_PROGRAM`, `TMUX`, etc.).
  4. **`Settings`**, **`Extensions`**, **`Packages`** (`extensionSections`, `doctor.ts:735-921`): agent folder, project trust & resources, settings parse errors, built-in/runtime extensions load status & timing, package resolution / missing packages, Pi vs µPi package list comparison.
  5. **`Herdr`** (`herdrSection`, `doctor.ts:925-955`): Herdr pane detection, `herdr --version` on PATH, `pi-herdr` / `pi` on PATH check.
  6. **`Editors (ACP)`** (`acpSection`, `doctor.ts:1009-1047`): `--acp` support, Zed (`~/.config/zed/settings.json`) and JetBrains (`~/.jetbrains/acp.json`) `agent_servers` config pointing to `upi` with `--acp`.

### `upi update` Repo & Tag Convention (`packages/coding-agent/src/upi.ts`, `utils/version-check.ts`, `package-manager-cli.ts`)
- **Repository**: `https://github.com/Code000-211/microPi` (`upi.ts:100-112`)
  - Releases URL: `https://github.com/Code000-211/microPi/releases` (`MICROPI_RELEASES_URL`)
  - Latest release redirect: `.../releases/latest` (`MICROPI_LATEST_RELEASE_URL`) — used first to avoid GitHub API 60 req/hr unauthenticated rate limit (`version-check.ts:152-156`)
  - Atom feed: `.../releases.atom` (`MICROPI_RELEASES_FEED`) — fallback for `beta` channel
  - GitHub API: `https://api.github.com/repos/Code000-211/microPi/releases/latest` & `/releases?per_page=30`
- **Tag convention**: `upi-v<X.Y.Z>` (regex `/^upi-v(\d+\.\d+\.\d+)$/` in `upi.ts:157`).
- **Channels**: `stable` (latest release, default, no file) or `beta` (newest release including pre-releases, stored in `<installDir>/channel` or `MICROPI_CHANNEL` env var, `upi.ts:119-146`).
- **Release verification**: Ed25519 signature (`SHA256SUMS` + `SHA256SUMS.sig`, header `# upi <version>`) verified with `MICROPI_RELEASE_KEY` (`upi.ts:102-105`, `doctor.ts:313-335`, `install.sh:36-38`).
- **Archive naming**: `upi-<platform>-<variant>.tar.xz` / `.tar.gz` / `.zip` (`doctor.ts:304-307`, `install.sh:123`).
- **Package-manager-owned installs**: Homebrew (`Code000-211/tap/upi` -> `brew upgrade upi`), Scoop (`Code000-211/upi` -> `scoop update upi`), mise (`upi` / `github:Code000-211/microPi` -> `mise upgrade --bump ...`) delegate updates to the package manager (`upi.ts:247-289`).
