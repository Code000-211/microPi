# Reviewer 2 Handoff — M1 Target-Scoped Code & Requirement Review

**Verdict: APPROVE**

## 1. Requirements Verification
- **Static `upi` binary (`cmd/upi/main.go`, `Makefile`, `.gitignore`)**: Verified `cmd/upi/main.go` imports `ai/all`, `coding/cli`, and `coding/extension/host/lispcell` and invokes `cli.Main(os.Args[1:], nil)`. Verified `Makefile` `upi: node-runtime` target builds static `CGO_ENABLED=0` `bin/upi` and `.gitignore` ignores `/upi` and `bin/`. Built and verified `ELF 64-bit LSB executable, x86-64, version 1 (SYSV), statically linked, stripped`.
- **Home & agent directories (`internal/configroot/configroot.go`, `internal/codingagent/paths.go`, `settings.go`)**:
  - `configroot.Resolve()` checks `MICROPI_HOME` -> `PIG_HOME` -> `XDG_CONFIG_HOME/micropi` -> `~/.micropi`.
  - `codingagent.ResolveDefaultAgentDir()` checks `MICROPI_CODING_AGENT_DIR` -> `PIG_CODING_AGENT_DIR` -> `<configroot>/agent` (or `PI_CODING_AGENT_DIR` / `~/.pi/agent` when `MICROPI_USE_PI_DIRS=1` or `PIG_USE_PI_DIRS=1`).
  - `codingagent.AppName = "upi"`, `APP_TITLE = "µPi"`, `CONFIG_DIR_NAME = ".micropi"`, `ENV_AGENT_DIR = "MICROPI_CODING_AGENT_DIR"`.
- **Version output (`internal/coding/pigversion/pigversion.go`, `coding/upstream.go`, `coding/cli/main.go`)**:
  - `upi --version` outputs `µPi 0.1.0 (PiG 0.8.0+1.6.0)`.
  - `upi version` outputs `µPi: 0.1.0`, `pig: 0.8.0`, `upstream pi: 1.6.0`, `go: go1.27.4`, `platform: linux/amd64`, `build: a18b202`.
- **Diagnostics (`coding/cli/doctor.go`, `coding/cli/main.go`, `coding/cli/args.go`)**:
  - `upi doctor [--offline]` is registered in `coding/cli/main.go` and documented in `coding/cli/args.go`.
  - Reports µPi/PiG/build/Go/platform/binary/offline info, probes `git`, `rg`, `ssh`, `go`, checks `~/.micropi` & agent dir writability, auth providers, registered model providers & default model, MCP config (`mcpext.LoadMcpConfig`), Herdr env status, and redacted env vars.
- **Self-update (`internal/codingagent/micropi_update.go`, `selfupdate.go`, `coding/cli/self_update.go`, `package_commands.go`)**:
  - `UpdateSourceURL()` checks `MICROPI_UPDATE_URL` -> `PIG_UPDATE_URL` -> `update-url` sidecar -> `DefaultUpdateURL`.
  - When no override URL is set, `fetchSelfUpdateManifest` falls back to `FetchMicroPiReleaseManifest` targeting `https://api.github.com/repos/Code000-211/microPi` (`upi-v*` tags, `SHA256SUMS` verification, `--channel` / `MICROPI_CHANNEL` support).
  - `coding/cli/guard.go` rejects `pi`/`pi.exe` and identifies the binary as `upi` / `µPi`.

## 2. Target-Scoped Test & Build Results
- `GOTOOLCHAIN=auto CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags "-s -w -X github.com/MichaelKinsy/PiG/coding/cli.Build=$(git rev-parse --short HEAD 2>/dev/null || echo dev)" -o /tmp/upi-review ./cmd/upi` -> **PASS** (exit 0, statically linked)
- Smoke checks on built binary:
  - `--version` -> `µPi 0.1.0 (PiG 0.8.0+1.6.0)` (**PASS**)
  - `version` -> `µPi: 0.1.0`, `pig: 0.8.0`, `upstream pi: 1.6.0`, `go: go1.27.4`, `platform: linux/amd64`, `build: a18b202` (**PASS**)
  - `doctor --offline` -> exit 0, reports tools, `/home/antigravity/.micropi`, auth, models, MCP, Herdr, redacted env (**PASS**)
- `GOTOOLCHAIN=auto go test -v ./cmd/upi/... ./internal/configroot/... ./internal/coding/pigversion/...` -> **PASS** (exit 0):
  - `cmd/upi`: `[no test files]`
  - `internal/configroot`: 6/6 tests passed (including 32 `TestResolveMatchesTheSharedMatrix` subtests)
  - `internal/coding/pigversion`: 3/3 tests passed
- `GOTOOLCHAIN=auto go test -v ./coding/cli -run 'TestUpi|TestMicroPi|TestSelfUpdate|TestIsSelfUpdateTarget'` -> **PASS** (exit 0):
  - `coding/cli`: 8/8 tests passed (`TestSelfUpdateFallback_PointsAtStandaloneFallback`, `TestIsSelfUpdateTarget`, `TestSelfUpdateNeverRunsAProjectsSelfUpdateCommand`, `TestSelfUpdateChecksTheVersionBeforeRefusingAnInstallation`, `TestSelfUpdatePlansASameVersionPackageRename`, `TestUpiVersionOutputs`, `TestUpiDoctorOfflineAndRedaction`, `TestMicroPiUpdateReleaseManifestAndSemver`)
- `GOTOOLCHAIN=auto go test -v ./internal/codingagent -run TestResolveDefaultAgentDirPrecedence` -> **PASS** (exit 0):
  - `internal/codingagent`: 1/1 test passed (`TestResolveDefaultAgentDirPrecedence`)

## 3. Non-Blocking Observation
- In `coding/cli/self_update.go:141-144` (`selfUpdatePlanRuns`), `strings.TrimSpace(manifest.PackageName) != codingagent.PackageName` compares `"upi"` (returned by `FetchMicroPiReleaseManifest`) against `codingagent.PackageName` (`"@pi-in-go/pig"`). For standalone `upi`, `applyStandaloneUpdateWithChannel` still checks `IsNewerPackageVersion` and exits 0 (`up to date`), so it works correctly, though aligning `selfUpdatePlanRuns` to accept `codingagent.AppName` as well is a clean follow-up.
