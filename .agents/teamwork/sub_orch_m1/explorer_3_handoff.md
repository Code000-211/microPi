# Explorer 3 Handoff: PiG CLI, Env & Subcommands

## 1. PiG Codebase Findings (Inspected from `MichaelKinsy/PiG` @ `0.8.0+1.6.0`)

### Entry Point & Build
- **`cmd/pig/main.go`** (L1–16): Imports `_ "<mod>/ai/all"`, `<mod>/coding/cli`, and `_ "<mod>/coding/extension/host/lispcell"`, then calls `cli.Main(os.Args[1:], nil)`.
- **`Makefile`** (L163, L187): Builds static binary with:
  ```sh
  CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags "-s -w -X <mod>/coding/cli.Build=$(git rev-parse --short HEAD 2>/dev/null || echo dev)" -o bin/upi ./cmd/upi
  ```
- **Environment note**: Current VM has `go1.24.3 linux/amd64` installed; verify `go.mod` / `.go-version` compatibility during M1 integration.

### CLI Dispatch & Binary Guard
- **`coding/cli/main.go`** (`Main`, L698–960):
  - Calls `guardBinaryIdentity()` (`coding/cli/guard.go`, L14–46), which only exits(2) if `filepath.Base(resolved)` is `"pi"` or `"pi.exe"`, and warns if installed inside npm `@mariozechner/pi-coding-agent` or `@earendil-works/pi-coding-agent`. Naming the binary `upi` passes without error.
  - Subcommand dispatch (L801–873): runs `runAuthCommand`, `runLoginCommand`, `runStatusCommand`, `runSetupCommand`, `runVerifyCommand`, `runPackageCommand` (handles `install`, `remove`/`uninstall`, `update`, `list`), `runBuildCommand`, `runACPCommand`, `runExtensionCommand`, `runExtensionsCommand`, `runRetiredCommand`, then `switch args[0]` (`"config"`, `"mcp"`, `"diagnose"`, `"provider-bridge"`, `"version"`), then `runPigPreSessionCommand`.
  - `--version` / `-v` (L883–886): handled after `parseArgs(args)` via `cliVersionString()`.
- **`coding/cli/args.go`** (L457–483): `pigCommands` constant defines help text for top-level commands and states config root (`~/.pig`).

### Version & Product Identity
- **`internal/coding/pigversion/pigversion.go`** (L9–26):
  - `PigVersion = "0.8.0"`, `UpstreamRef = "v1.6.0"`, `UpstreamVersion = "1.6.0"`, `UpstreamPackageVersion = "1.6.0"`, `Version = PigVersion + "+1.6.0"`.
- **`coding/cli/main.go`** (L83–144):
  - `cliVersionString()` returns `UpstreamPackageVersion` (`1.6.0`).
  - `detailedVersionString()` (used by `pig version`) prints `pig`, `upstream pi`, `go`, `platform`, `build`.
- **`internal/coding/pigidentity/pigidentity.go`** & **`identity.json`**: Single source of truth for `AttributionName` (`"PiG"`), `AttributionURL`, `CodexOriginator`, `AgentMarker`, `UserAgentProduct`, `HostedOrigin`. (Note: `TestIdentityJSONIsTheSingleSource` enforces parity between `identity.json` and `pigidentity.go`.)

### Config Root & Agent Directory Resolution
- **`internal/configroot/configroot.go`** (`Resolve`, L50–66):
  - Currently resolves: `PIG_HOME` -> `XDG_CONFIG_HOME/pig` -> `~/.pig`.
  - `Dir()` panics with `*UnresolvedError` if home cannot be resolved; `cli.Main` recovers it via `defer exitOnConfigRootFailure()`.
- **`internal/codingagent/paths.go`** (L18–75, L255):
  - `AppName = "pig"`, `CONFIG_DIR_NAME = "." + AppName` (`".pig"`), `ENV_AGENT_DIR = "PIG_CODING_AGENT_DIR"`, `ENV_SESSION_DIR = "PIG_CODING_AGENT_SESSION_DIR"`, `APP_TITLE = "PiG"`.
  - `UsePiDirs()` checks `PIG_USE_PI_DIRS == "1"`.
  - `ConfigRoot()` delegates to `configroot.Dir()`.
- **`internal/codingagent/settings.go`** (`ResolveDefaultAgentDir`, L1962–1978):
  - Checks `os.Getenv(agentDirEnvName())` (`PIG_CODING_AGENT_DIR`, or `PI_CODING_AGENT_DIR` when `UsePiDirs()`), then `UsePiDirs()` (`~/.pi/agent`), else `filepath.Join(configroot.Resolve(), "agent")`.

### Diagnostics, Self-Update, MCP & Herdr
- **`coding/cli/diagnose.go`** (`runDiagnose`, L23–189): Prints build info, extension SDK staging status, paths (`config root`, `agent dir`, `auth.json`, `cwd`, project trust, active piglet, crash/exit/debug logs), auth providers (`ai.NewAuthStorage`), built-in tools, registered model providers, default model, and masked env vars.
- **`coding/cli/self_update.go`** + **`coding/cli/package_commands.go`** (L483–518) + **`internal/codingagent/selfupdate.go`**:
  - `pig update` routes through `runUpdateCommand` -> `runSelfUpdate(opts.force)`.
  - `isSelfUpdateTarget(source)` (L54–61) matches `"self"` and `codingagent.AppName`.
  - `UpdateSourceURL()` (L116–124) checks `PIG_UPDATE_URL` -> `<ConfigRoot>/update-url` -> `DefaultUpdateURL` (build-time `-ldflags -X`).
  - `FetchUpdateManifest` expects a signed JSON `UpdateManifest` (`version`, `packageName`, `notes`, `binaries["<goos>/<goarch>"]`) verified with Ed25519 (`X-Pig-Release-Signature` header or `.sig` sidecar) against `updateTrustRoots()`.
- **`coding/mcpext/config.go`**: `LoadMcpConfig(LoadOptions{AgentDir, Cwd, ProjectTrusted})` loads `mcp.json` from `AgentDir` and trusted `<project>/<ConfigDirName()>/mcp.json`.
- **`herdr`**: No `herdr` Go package exists in PiG. PiG integrates with `herdr` via terminal env vars (`TERM_PROGRAM=herdr`, `HERDR_ENV=1`, `HERDR_KITTY_GRAPHICS=1`, `HERDR_AGENT`) documented in `docs/site/docs/terminal-setup.md`.

---

## 2. Recommended Go Implementation Strategy for `upi`

### A. `cmd/upi/main.go` & Static Build
1. Create `cmd/upi/main.go` mirroring `cmd/pig/main.go`:
   - Import `_ "<module>/ai/all"`, `<module>/coding/cli`, and `_ "<module>/coding/extension/host/lispcell"`.
   - Call `cli.Main(os.Args[1:], nil)` (or set `cli.AppDisplayName = "µPi"` / inject `upi` metadata if needed).
2. Build static binary with:
   ```sh
   CGO_ENABLED=0 go build -buildvcs=false -trimpath \
     -ldflags "-s -w -X <module>/coding/cli.Build=$(git rev-parse --short HEAD 2>/dev/null || echo dev)" \
     -o bin/upi ./cmd/upi
   ```
3. In `internal/codingagent/paths.go`, update:
   - `AppName = "upi"`, `APP_TITLE = "µPi"`, `CONFIG_DIR_NAME = ".micropi"` (so project-local config dir is `.micropi` unless `UsePiDirs()`/`MICROPI_USE_PI_DIRS=1` is set).
   - Keep `guardBinaryIdentity()` rejecting `"pi"` / `"pi.exe"`, and update its error/warning text from `pig` to `upi`.

### B. Home & Agent Directory Resolution (`~/.micropi`)
Update `internal/configroot/configroot.go` and `internal/codingagent/settings.go` / `paths.go`:
1. **`configroot.Resolve()`** precedence:
   1. `MICROPI_HOME` (if non-empty, `ExpandHome`)
   2. `PIG_HOME` (if non-empty, `ExpandHome` — explicit compatibility override)
   3. `XDG_CONFIG_HOME/micropi` (if `XDG_CONFIG_HOME` non-empty)
   4. `~/.micropi` (default)
2. **`codingagent.ResolveDefaultAgentDir()`** precedence:
   1. `MICROPI_CODING_AGENT_DIR` (if non-empty, `ExpandTildePath`)
   2. `PIG_CODING_AGENT_DIR` (if non-empty, `ExpandTildePath`)
   3. If `UsePiDirs()` (`MICROPI_USE_PI_DIRS == "1"` or `PIG_USE_PI_DIRS == "1"`): `PI_CODING_AGENT_DIR` or `~/.pi/agent`
   4. `filepath.Join(configroot.Resolve(), "agent")` (`~/.micropi/agent`)
3. Update `UnresolvedError.Error()` to mention `MICROPI_HOME`.

### C. `upi version` / `upi --version`
- Keep microPi's root `VERSION` file (`0.1.0`) embedded or generated into `internal/coding/pigversion/pigversion.go` (e.g., `MicroPiVersion = "0.1.0"` alongside `PigVersion = "0.8.0"` and `UpstreamVersion = "1.6.0"`).
- **`upi --version` / `-v`**: print `µPi 0.1.0 (PiG 0.8.0+1.6.0)` (concise single line).
- **`upi version`**: print detailed multi-line output:
  ```text
  µPi: 0.1.0
  pig: 0.8.0
  upstream pi: 1.6.0
  go: <runtime.Version()>
  platform: <GOOS>/<GOARCH>
  build: <buildIdentity()>
  ```

### D. `upi doctor` Subcommand
- Register `"doctor"` alongside `"diagnose"` in `coding/cli/main.go` (`switch args[0]`, L861):
  - `case "doctor", "diagnose": runDoctor(os.Stdout, binaryPath); exitProcess(0)`
- Implement `runDoctor(w io.Writer, binaryPath string)` in `coding/cli/doctor.go` (adapting `diagnose.go`):
  1. **Binary & Build**: `µPi` version (`0.1.0`), PiG/upstream pin (`0.8.0+1.6.0`), `buildIdentity()`, `runtime.Version()`, `GOOS/GOARCH`, `binaryPath`.
  2. **System / Go Tools** (via `exec.LookPath` + optional `--version` probe with short timeout): `git`, `rg` (ripgrep), `ssh`, `go` (needed for Go extensions). Report found path + version or `not found`.
  3. **Config & Paths**: `config root` (`~/.micropi`), writable check, `agent dir`, `auth.json`, `settings.json`, `cwd`, project trust, crash/exit/debug logs.
  4. **Auth & Models**: logged-in providers from `ai.NewAuthStorage(authPath)` (never printing secrets), registered model provider counts, default model resolution.
  5. **MCP Status**: call `mcpext.LoadMcpConfig` (with `AgentDir`, `Cwd`, `ProjectTrusted`) and report configured server count, names, scopes (`global`/`project`), and any config errors.
  6. **Herdr Status**: inspect `TERM_PROGRAM`, `HERDR_ENV`, `HERDR_KITTY_GRAPHICS`, `HERDR_AGENT` and report whether running inside `herdr` and whether `HERDR_AGENT` / Kitty graphics are active.
  7. **Environment**: masked `MICROPI_*`, `PIG_*`, `PI_*`, API keys, `PATH`, `SHELL`, `TERM`, `COLORTERM`, `NO_COLOR`, proxy vars.
- Add `upi doctor` to `pigCommands` help text in `coding/cli/args.go`.

### E. `upi update` Subcommand (`Code000-211/microPi`, `upi-v*` Releases)
Two clean options depending on release artifact format in `Code000-211/microPi`:
1. **Option 1 (Recommended — GitHub Releases `upi-v*` API + SHA256SUMS, matching `install.sh`)**:
   - In `coding/cli/package_commands.go`, keep `update` routing to `runUpdateCommand` -> `runSelfUpdate(opts.force)` (also support `--channel stable|beta` if desired).
   - Add a microPi GitHub Releases updater in `internal/codingagent/micropi_update.go` (invoked when `PIG_UPDATE_URL` / `MICROPI_UPDATE_URL` is unset):
     - Query `https://api.github.com/repos/Code000-211/microPi/releases/latest` (or `/releases` for `beta` channel), filtering tags matching `upi-v*`.
     - Strip `upi-v` prefix to compare semver against `MicroPiVersion` (`0.1.0`) using `IsNewerPackageVersion`.
     - Select asset for `<GOOS>/<GOARCH>` (e.g., `upi-linux-amd64`, `upi-linux-arm64`, `upi-darwin-arm64`, `upi-windows-amd64.exe` or `.tar.gz`), fetch `SHA256SUMS` (and `SHA256SUMS.sig` if signed), verify SHA-256, and atomically replace `os.Executable()` using PiG's `SelfReplaceAtWithCommit` / `fspublish` helpers.
2. **Option 2 (PiG `UpdateManifest` JSON)**:
   - If `Code000-211/microPi` releases publish `update.json` + `update.json.sig` (Ed25519 signed with `keys/release.pub`), set `DefaultUpdateURL = "https://github.com/Code000-211/microPi/releases/latest/download/update.json"` and `DefaultUpdateTrustRoot` to `keys/release.pub` via `-ldflags` or constants in `internal/codingagent/selfupdate.go`.
   - Note: `install.sh` (L36–38) already embeds an Ed25519 public key (`keys/release.pub`) and uses `upi-v*` tags, so `upi update` can share the exact same trust key and `MICROPI_UPDATE_URL` / `PIG_UPDATE_URL` override precedence.
