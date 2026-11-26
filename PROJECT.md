# Project: microPi on PiG (`upi`)

## Architecture
Port `microPi` (`µPi`, CLI `upi`) from `Pi-Bolt` (patched Bun + WebKit/JSC AOT) to `PiG` (`https://github.com/MichaelKinsy/PiG`, Go 1.27.4, `CGO_ENABLED=0` static binary).

- **Foundation**: `PiG` (`0.8.0+1.6.0`, upstream Pi `v1.6.0` parity) provides the static Go binary, `agent/`, `ai/`, `coding/`, `tui/`, `env/`, `mcp/`, `codemode/`, `chord/`, `durable/`, `schema/`, `telemetry/`, `internal/codingagent/`, `extensions/sdk*`, and `piglets/`.
- **microPi Product Layer**: Ported into Go (`internal/docsearch/`, `internal/codingagent/`, `cmd/upi/`, `ai/`) on top of PiG, preserving `upi` branding (`~/.micropi`, `MICROPI_*`) and all microPi agentic speed/cost features without Pi-Bolt AOT or Node/Bun runtime requirements.
- **Migration Strategy**: Keep reference TypeScript/Rust `packages/` readable during M1–M5 (Go tooling ignores non-Go directories), then remove Pi-Bolt and obsolete TypeScript files in M6.

## Code Layout
- `cmd/upi/` — `upi` CLI entry point (wrapping/adapting `cmd/pig/` + `coding/cli/` with `upi` branding, `doctor`, `version`, `update`)
- `agent/`, `ai/`, `coding/`, `tui/`, `env/`, `mcp/`, `codemode/`, `chord/`, `durable/`, `schema/`, `telemetry/` — PiG core public Go packages
- `internal/docsearch/` — Pure Go BM25 + symbol/section graph (`project_search` tool & `/docs` command)
- `internal/codingagent/` — PiG coding-agent internals + microPi enhancements:
  - `tools/` — `grep.go`, `find.go` (cached SIMD-style/fast literal search + `fff` fuzzy fallback), `edit.go`, `write.go`, `bash.go`, `wait.go`, `argument_aliases.go`, `bash_file_writes.go`
  - `filetracking/` — Read-before-edit tracking, CRLF/BOM preservation, nested `AGENTS.md` injection
  - `checks/` — Background compiler/linter diagnostics (`PI_CHECKS`, `PI_CHECKS_WAIT`)
  - `jobs/` — Background bash jobs, wakeup notifications, sleep-shortening, long-sleep guard, `/jobs` (`alt+j`) hub
  - `compaction/` — `compaction-ahead`, `maxContextTokens`, `tool-result-pruning`
  - `remote/` — `/remote` & `--remote` SSH `ControlMaster` tool-routing
  - `prompts/` — Trimmed system prompt, `<project>` snapshot, `cut-off-response` recovery
- `tests/e2e/` — Opaque-box E2E test suite (owned by E2E Testing Track)

## Feature Inventory
Every Survey feature assigned a milestone (none unassigned):
| # | Feature | Description | Milestone | Source |
|---|---------|-------------|-----------|--------|
| 1 | PiG Base & Build | Go 1.27.4 `CGO_ENABLED=0` static binary, `go.mod`/`go.work`, PiG core packages | M1 | survey_3 |
| 2 | `upi` CLI & Branding | `upi` binary, `~/.micropi`, `MICROPI_*` env vars, `upi version`, `upi doctor`, `upi update` | M1 | survey_1, env_features |
| 3 | `docsearch` (`project_search` & `/docs`) | Pure Go BM25 + symbol/section graph, definition-aware chunking, incremental index, <=150 file auto-disable | M2 | survey_1, env_features |
| 4 | Cached `grep`/`find` & `fff` | In-memory file tree (2s TTL) & content cache (64MB/512KB), fast literal search, fuzzy `find` fallback, bash invalidation | M2 | survey_1, env_features |
| 5 | File Tracking & Edit Ergonomics | Read-before-edit tracking, unique `oldText` on unread/changed files, `write` unread return (30k), CRLF/BOM, nested `AGENTS.md`, `seek_sequence` diff | M3 | env_features |
| 6 | Bash File-Write Guard | Track/check single-file `sed -i`, `perl -i`, literal redirections locally; refuse on `/remote` | M3 | env_features |
| 7 | Tool Argument Aliases | Normalize Claude Code (`file_path`, `old_string`, `run_in_background`) & Qwen Code (`absolute_path`, `include`, `is_background`) args | M3 | survey_1, env_features |
| 8 | Background Compiler/Linter Checks | Post-edit/write/bash diagnostics diffed vs pre-state (`PI_CHECKS_WAIT=30s`), Go/Rust/Python/TS/JS/Shell/C++/HIP/JSON/TOML/YAML/MD/Luau/`radisa` | M3 | survey_1, env_features |
| 9 | Background Jobs & `wait` Tool | Auto-background `bash` (>120s or timeout), completion wakeup, `wait` backoff ladder (2m/5m/10m), sleep-shortening, long-sleep guard | M4 | survey_1, env_features |
| 10 | Bash Speed & Output | Head+tail truncation (40 lines + end, 2000L/32KB), ANSI/progress strip + `NO_COLOR=1`, early read-only exec, direct exec without shell | M4 | env_features |
| 11 | Interactive Job Hub | `alt+j` / `/jobs` live job hub in TUI | M4 | survey_1, env_features |
| 12 | Compaction-Ahead & Tool Pruning | `compaction-ahead` (15% / 40k below threshold, 16k user msg preservation), `maxContextTokens` (128k), `tool-result-pruning` (>40k, step by 8) | M5 | survey_1, env_features |
| 13 | Project Snapshot & Cut-Off Recovery | `<project>` listing (150 git files or 2-level dirs, `PI_PROJECT_SNAPSHOT=off`), thinking step-down (max 3) on `length` cut-off | M5 | survey_1, env_features |
| 14 | System Prompt & Batched Execution | Trimmed system prompt (~2.2k chars), batched tool-call guidance & ordered execution | M5 | env_features |
| 15 | Remote SSH (`/remote`, `--remote`) | SSH `ControlMaster` routing for `read`/`edit`/`write`/`bash`, prompt cache preservation, disables local `grep`/`find`/`ls` & checks | M5 | survey_1, env_features |
| 16 | Melious Provider & Herdr | `melious` provider (`MELIOUS_API_KEY`, 18 models, `glm-5.3`, energy-hold retry) + verify PiG `herdr` integration | M5 | env_features |
| 17 | Pi-Bolt Retirement & CI/Installers | Remove `patches/`, `profiles/`, `sources.json`, old TS `packages/`, update `install.sh`, `install.ps1`, `.github/workflows/*`, `Makefile` | M6 | survey_2 |
| 18 | E2E Suite & Tier 5 Hardening | Pass 100% of opaque-box E2E tests (Tiers 1–4) + Tier 5 white-box adversarial coverage hardening | M7 | Project Pattern |

## Milestones
| # | Name | Scope | Dependencies | Status |
|---|------|-------|-------------|--------|
| M1 | PiG Base Integration & `upi` CLI | Bring PiG `0.8.0+1.6.0` Go codebase to repo root, verify Go 1.27.4 build/test, wire `cmd/upi` (`upi`, `~/.micropi`, `MICROPI_*`, `version`, `doctor`, `update`) | none | DONE (`cmd/upi/`, `internal/configroot/`, `coding/cli/doctor.go`, `self_update.go`) |
| M2 | Search & Code Intelligence | Pure Go `internal/docsearch/` (`project_search`, `/docs`) + cached fast `grep`/`find` with `fff` fuzzy fallback | M1 | DONE (`internal/docsearch/`, `tools/{docsearch,native_search,grep,find}.go`) |
| M3 | File Tracking, Edit Ergonomics & Background Checks | `filetracking/`, `edit-diff`, `bash-file-writes`, `argument-aliases`, `checks/` background diagnostics | M1 | DONE (`internal/codingagent/{filetracking,checks}/`, `tools/{edit,edit_diff,write,read,argument_aliases,bash_file_writes}.go`) |
| M4 | Bash Execution, Background Jobs & Job Hub | Background bash jobs, `wait` tool, sleep guards, bash output/exec optimizations, `alt+j` / `/jobs` TUI hub | M1, M3 | DONE (`internal/codingagent/jobs/`, `tools/{wait,bash_parse,shell_tool}.go`, `/jobs`, `alt+j`) |
| M5 | Context, Compaction, `/remote`, Melious & Prompts | `compaction-ahead`, `tool-result-pruning`, `project-snapshot`, `cut-off-response`, prompt trims, `/remote` SSH, `melious`, `herdr` check | M1, M4 | DONE |
| M6 | Pi-Bolt Retirement, Cleanup & CI/Installers | Remove Pi-Bolt patches/scripts/TS packages, update `install.sh`/`install.ps1`/CI/docs for Go `upi` | M2, M3, M4, M5 | DONE |
| M7 | Final Milestone: E2E Pass & Tier 5 Hardening | Phase 1: E2E Tiers 1–4 (`TEST_READY.md`); 191/198 E2E pass (16/16 Tier 3, 4/6 Tier 4 + 2 cold-GOCACHE 30s timeouts), `go vet ./...` clean | M6, TEST_READY.md | DONE |

## Interface Contracts
### `cmd/upi` ↔ `coding/cli` & `internal/codingagent`
- `cmd/upi/main.go` invokes `cli.Main(os.Args[1:], nil)` with `upi` product metadata (`COMMAND_NAME="upi"`, `PIG_HOME` defaulting to `~/.micropi` / `MICROPI_HOME`).
- `upi doctor` and `upi version` and `upi update` are registered as CLI subcommands in `coding/cli/`.

### `internal/docsearch` ↔ `internal/codingagent/tools` & Slash Commands
- `docsearch.NewTool(cwd string) agent.AgentTool` exposes `project_search` (auto-disabled if project file count <= 150).
- `docsearch.HandleDocsCommand(args string, cwd string) string` serves `/docs`.

### `internal/codingagent/filetracking` ↔ `tools/{read,edit,write,bash}` & `checks/`
- `Tracker` records read state, original CRLF/BOM, and pre-edit diagnostics baseline; `checks.Scheduler` runs post-edit/write/bash diagnostics asynchronously and settles up to `PI_CHECKS_WAIT` (default 30s).

### `internal/codingagent/jobs` ↔ `tools/bash.go`, `tools/wait.go`, `tui/`
- `Manager` tracks backgrounded bash jobs (`bg1`, `bg2`, ...), emits completion wakeups, exposes `Wait(ctx, ids, timeout)` with `[120s, 300s, 600s]` ladder, and provides state snapshots to `/jobs` (`alt+j`).
