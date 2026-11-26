# Milestone M1 Handoff: PiG Base Integration, Toolchain & `upi` CLI/Branding

**Status**: PASSED (Iteration 2 Gate: 2/2 Reviewers `APPROVE`, 2/2 Challengers `PASS`, Forensic Auditor `CLEAN`)

## Deliverables Implemented
1. **PiG Base Integration (`0.8.0+1.6.0`)**:
   - Integrated `https://github.com/MichaelKinsy/PiG` (`main`, `0.8.0+1.6.0`) into `/home/antigravity/Code000-211/microPi` (`go.mod`, `go.work`, `.go-version`, `Makefile`, `cmd/`, `agent/`, `ai/`, `coding/`, `tui/`, `env/`, `mcp/`, `codemode/`, `chord/`, `durable/`, `schema/`, `telemetry/`, `internal/`, `extensions/`, `piglets/`, etc.).
   - Preserved `packages/`, `scripts/`, `docs/`, `README.md` (PiG's saved as `README_PIG.md`), `AGENTS.md`, `VERSION`, `.github/`, `.agents/`, and `LICENSE`; appended Go/PiG ignores to `.gitignore`.
2. **Go `1.27.4` Toolchain & Static Binary**:
   - Verified `GOTOOLCHAIN=auto go version` -> `go version go1.27.4 linux/amd64`.
   - `GOTOOLCHAIN=auto go build ./...` passes.
   - `cmd/upi/main.go` and `Makefile` `upi` target build a statically linked `CGO_ENABLED=0` `bin/upi` binary (`not a dynamic executable`).
3. **`upi` Branding, Home/Agent Directories & Subcommands**:
   - **Home & Agent Dirs**: `internal/configroot/configroot.go`, `internal/codingagent/paths.go`, and `internal/codingagent/settings.go` default to `~/.micropi` and `~/.micropi/agent` with precedence `MICROPI_HOME` > `PIG_HOME` > `XDG_CONFIG_HOME/micropi` > `~/.micropi` and `MICROPI_CODING_AGENT_DIR` > `PIG_CODING_AGENT_DIR` > (optional `MICROPI_USE_PI_DIRS`/`PIG_USE_PI_DIRS`) > `<configroot>/agent`.
   - **`upi version` / `upi --version`**: `internal/coding/pigversion/pigversion.go` (`MicroPiVersion = "0.1.0"`) and `coding/cli/main.go` report `µPi 0.1.0 (PiG 0.8.0+1.6.0)` and detailed version metadata.
   - **`upi doctor`**: `coding/cli/doctor.go` implements Go-native diagnostics (binary/build info, `git`/`rg`/`ssh`/`go` tool probes, `~/.micropi` & agent dir checks, auth/models without secrets, MCP config via `mcpext.LoadMcpConfig`, Herdr env detection, redacted env vars, `--offline` support).
   - **`upi update`**: `internal/codingagent/micropi_update.go`, `coding/cli/self_update.go`, and `coding/cli/package_commands.go` wire `upi update` to `Code000-211/microPi` (`upi-v*` releases) with `MICROPI_UPDATE_URL`/`PIG_UPDATE_URL` override, `--channel`, semver comparison, and `SHA256SUMS` verification.

## Verification Summary (Iteration 2)
- **Reviewer 1**: `APPROVE` — `go1.27.4`, `go build ./...`, static `CGO_ENABLED=0` `cmd/upi`, `./cmd/upi/...`, `./internal/configroot/...`, `./internal/coding/pigversion/...`, 45/45 `coding/cli` targeted/rebranded tests, and 11/11 `internal/codingagent` targeted/rebranded tests all passed.
- **Reviewer 2**: `APPROVE` — verified all M1 requirements, Iteration 2 fixes, and target-scoped test suites.
- **Challenger 1**: `PASS` — verified static ELF binary, `--version`/`version`, `upi doctor --offline` (`upi login` hint), `MICROPI_*`/`PIG_*` env precedence, and `pi` binary guard (exit 2).
- **Challenger 2**: `PASS` — verified secret redaction, Herdr detection, non-existent/read-only `MICROPI_HOME` handling, `upi update` unreachable-server error handling, and `MicroPi`/`SelfUpdate` unit tests.
- **Forensic Auditor**: `CLEAN` — verified `packages/`, `scripts/`, `docs/`, `README.md`, `AGENTS.md`, `VERSION`, `.github/`, and `LICENSE` are intact, no stubs/cheating, and independent builds/tests pass.
