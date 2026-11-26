# Milestone M1 Scope: PiG Base Integration, Toolchain & `upi` CLI/Branding

## In-Scope
1. Integrate `https://github.com/MichaelKinsy/PiG` (`0.8.0+1.6.0`) Go codebase into `/home/antigravity/Code000-211/microPi` at the repo root (`go.mod`, `go.work`, `cmd/`, `agent/`, `ai/`, `coding/`, `tui/`, `env/`, `mcp/`, `codemode/`, `chord/`, `durable/`, `schema/`, `telemetry/`, `internal/`, `extensions/`, `piglets/`, `Makefile`, `.go-version`, etc.).
   - Preserve `packages/`, `scripts/`, `docs/` (M2–M5 reference them; M6 handles cleanup).
   - Preserve microPi's `README.md` (save PiG's as `README_PIG.md` if present).
   - Never run forbidden git commands (`git reset --hard`, `git checkout .`, `git clean -fd`, `git stash`).
2. Verify Go `1.27.4` toolchain (`GOTOOLCHAIN=auto`) works and `go build ./...` / `go test` on core packages pass.
3. Implement `cmd/upi/` and `upi` branding on top of PiG:
   - `cmd/upi/main.go` building the static `CGO_ENABLED=0` `upi` binary.
   - Default home/agent dirs: `~/.micropi` (`MICROPI_HOME`, `MICROPI_CODING_AGENT_DIR`, respecting `PIG_HOME` / `PIG_CODING_AGENT_DIR` if explicitly set).
   - `upi version` / `upi --version` reporting `µPi` (`VERSION` `0.1.0`) + PiG/upstream version.
   - `upi doctor` subcommand (Go-native diagnostics: binary/build info, Go/system tools like `rg`/`git`/`ssh`, config dir `~/.micropi`, auth/MCP/herdr status).
   - `upi update` subcommand wired to `Code000-211/microPi` (`upi-v*` releases).
4. Verification & Gate:
   - Full Iteration Loop: 3 Explorers -> 1 Worker -> parallel 2 Reviewers + 2 Challengers + 1 Forensic Auditor -> Gate.
   - Audit scope: `cmd/upi/`, `go.mod`, `go.work`, `coding/`, `internal/coding/pigversion/`, etc.

## Out-of-Scope
- Removing `packages/`, `patches/`, `profiles/`, `sources.json`, or TS/Rust code (M6).
- `docsearch`, cached `grep`/`find`, `fff` (M2).
- `filetracking`, `edit-diff`, `bash-file-writes`, `argument-aliases`, `checks` (M3).
- Background jobs, `wait`, `/jobs` TUI hub (M4).
- Compaction-ahead, project snapshot, `/remote`, `melious` (M5).
