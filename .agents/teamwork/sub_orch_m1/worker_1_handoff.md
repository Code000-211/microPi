# Milestone M1 Worker Handoff

## 1. PiG Integration & Toolchain
- Shallow-cloned `https://github.com/MichaelKinsy/PiG` (`main` branch, `0.8.0+1.6.0`) to `/tmp/pig-src`, synced into `/home/antigravity/Code000-211/microPi/` via `rsync -a --ignore-existing --exclude='.git'`, saved PiG's `README.md` as `README_PIG.md`, appended Go/PiG ignores to `.gitignore`, and removed `/tmp/pig-src`.
- Verified `packages/`, `scripts/`, `docs/*.md`, `README.md`, `AGENTS.md`, `VERSION`, `.github/`, `.agents/`, `LICENSE` remained untouched.
- Verified `GOTOOLCHAIN=auto go version` -> `go version go1.27.4 linux/amd64`.

## 2. Files Created / Modified
- **Entrypoint & Build**:
  - Created `cmd/upi/main.go` (imports `ai/all`, `coding/cli`, `coding/extension/host/lispcell`, calls `cli.Main(os.Args[1:], nil)`).
  - Modified `Makefile`: added `upi: node-runtime` target building static `CGO_ENABLED=0` `bin/upi`.
- **Home, Agent Dirs & Branding**:
  - Modified `internal/configroot/configroot.go`: `Resolve()` checks `MICROPI_HOME` -> `PIG_HOME` -> `XDG_CONFIG_HOME/micropi` -> `~/.micropi`; updated `UnresolvedError.Error()`.
  - Modified `internal/codingagent/paths.go`: `AppName = "upi"`, `APP_TITLE = "µPi"`, `CONFIG_DIR_NAME = ".micropi"`, `ENV_AGENT_DIR = "MICROPI_CODING_AGENT_DIR"`, `UsePiDirs()` checks `MICROPI_USE_PI_DIRS == "1" || PIG_USE_PI_DIRS == "1"`, updated `AgentDirConfigured()`.
  - Modified `internal/codingagent/settings.go`: `ResolveDefaultAgentDir()` checks `MICROPI_CODING_AGENT_DIR` -> `PIG_CODING_AGENT_DIR` -> (if `UsePiDirs()`: `PI_CODING_AGENT_DIR` or `~/.pi/agent`) -> `filepath.Join(configroot.Resolve(), "agent")`.
  - Modified `coding/cli/guard.go`: keeps rejecting `"pi"`/`"pi.exe"`, updated `"pig"` text to `"upi"`/`"µPi"`.
- **Version, Doctor & Self-Update**:
  - Modified `internal/coding/pigversion/pigversion.go` & `coding/upstream.go`: added `MicroPiVersion = "0.1.0"`.
  - Modified `coding/cli/main.go`: updated `cliVersionString()` (`µPi 0.1.0 (PiG 0.8.0+1.6.0)`), `detailedVersionString()`, `versionString()`, `selfUpdateVersion()`, and registered `"doctor"` alongside `"diagnose"`.
  - Modified `coding/cli/args.go`: updated `pigCommands` help text for `upi` commands (`doctor`, `update`, `~/.micropi`).
  - Created `coding/cli/doctor.go`: implemented `runDoctor` (binary/build info, `git`/`rg`/`ssh`/`go` tool probes, config/agent dir writable checks, auth providers, registered model providers & default model, MCP config via `mcpext.LoadMcpConfig`, Herdr env status, redacted env vars, `--offline` support).
  - Created `internal/codingagent/micropi_update.go` & updated `internal/codingagent/selfupdate.go`, `coding/cli/self_update.go`, `coding/cli/package_commands.go`: added `MICROPI_UPDATE_URL` precedence, `--channel` / `MICROPI_CHANNEL`, and default `Code000-211/microPi` GitHub Releases (`upi-v*` tags + `SHA256SUMS` verification + `upi` tar.gz extraction).
- **Tests**:
  - Updated `internal/configroot/configroot_test.go` and `coding/cli/package_commands_test.go` for `~/.micropi` and `upi` branding + `MICROPI_HOME` precedence.
  - Created `internal/codingagent/upi_paths_test.go` (`TestResolveDefaultAgentDirPrecedence`).
  - Created `coding/cli/upi_commands_test.go` (`TestUpiVersionOutputs`, `TestUpiDoctorOfflineAndRedaction`, `TestMicroPiUpdateReleaseManifestAndSemver`).

## 3. Exact Build & Test Results (After Final Edit)
- `GOTOOLCHAIN=auto CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags "-s -w -X github.com/MichaelKinsy/PiG/coding/cli.Build=$(git rev-parse --short HEAD 2>/dev/null || echo dev)" -o bin/upi ./cmd/upi` -> **PASS** (exit 0)
- `GOTOOLCHAIN=auto go build ./...` -> **PASS** (exit 0)
- `GOTOOLCHAIN=auto go test ./cmd/upi/... ./internal/configroot/... ./internal/coding/pigversion/...` -> **PASS** (exit 0)
- `GOTOOLCHAIN=auto go test ./coding/cli -run 'TestUpi|TestMicroPi|TestSelfUpdate|TestApplyStandaloneUpdate|TestCheckSelfUpdateVersion|TestIsSelfUpdateTarget'` -> **PASS** (exit 0)
- `GOTOOLCHAIN=auto go test ./internal/codingagent -run TestResolveDefaultAgentDirPrecedence` -> **PASS** (exit 0)
- `./bin/upi --version` -> `µPi 0.1.0 (PiG 0.8.0+1.6.0)`
- `./bin/upi version` -> `µPi: 0.1.0`, `pig: 0.8.0`, `upstream pi: 1.6.0`, `go: go1.27.4`, `platform: linux/amd64`, `build: a18b202`
- `./bin/upi doctor --offline` -> **PASS** (exit 0; reports `µPi doctor`, tools, `/home/antigravity/.micropi`, models, MCP, Herdr, redacted env).
- Note on `./ai` & `./agent`: non-oracle unit tests pass, while Pi-oracle tests (`*AsPi` / `*MatchesPi` / `TestValidateToolArgumentsValidatesTheSchemaItIsGiven`) fail because PiG's `extensions/sdk-ts/node_modules` and `.upstream/current` parity fixtures are not materialized (`make parity-deps` / `make upstream-mirror`).
