# Handoff Report: Environment Toolchains, microPi Features, and PiG Comparison

## 1. Observation

### 1.1 Installed Toolchains (Local Environment)
Direct command execution in `/home/antigravity/Code000-211/microPi`:
- `go version`: `go version go1.24.3 linux/amd64`
  - `go env GOTOOLCHAIN GOPROXY`: `auto`, `https://proxy.golang.org,direct`
  - `curl -sI --max-time 5 https://go.dev/dl/` returned `HTTP/2 405` (network reachable; `GOTOOLCHAIN=auto` can download newer Go toolchains from `proxy.golang.org` / `go.dev` if needed).
- `cargo --version`: `cargo 1.100.0 (3acc79c0c 2026-11-05)`
- `rustc --version`: `rustc 1.100.0 (0ef1bc386 2026-11-09)`
- `gcc --version`: `gcc (Ubuntu 13.3.0-6ubuntu2~24.04.1) 13.3.0`
- `clang --version`: `Ubuntu clang version 18.1.3 (1ubuntu1)` (`x86_64-pc-linux-gnu`)
- `rg --version`: `ripgrep 14.1.0` (`features:-simd-accel,+pcre2`, runtime AVX2/SSSE3/SSE2)
- `fd --version`: `NOT INSTALLED`
- `node --version`: `v22.23.5`
- `bun --version`: `1.2.14`
- `gh --version`: `gh version 2.103.3 (2026-10-26)`
- `uname -a`: `Linux devbox 6.18.57 #1-NixOS SMP PREEMPT_DYNAMIC @1792058300 x86_64 x86_64 x86_64 GNU/Linux`

### 1.2 User-Facing microPi Features (`README.md`, `docs/releases/0.1.0.md`, `packages/`)
1. **CLI & Distribution (`upi` identity)**:
   - Native AOT executable (`upi`, `~/.micropi`, `MICROPI_*` env/settings, `MICROPI_VERSION`, `MICROPI_VARIANT` for `x64`/`x64-baseline`/`x64-jit`/`arm64`/`arm64-jit`).
   - `upi doctor [--offline]` (`packages/coding-agent/src/cli/doctor.ts`): installation diagnostics, signature verification, compiled-in vs runtime extensions, Herdr/ACP setup, AOT heap check.
   - `upi version` / `--version`: version & build info.
   - `upi compile` (`packages/coding-agent/src/cli/args.ts:325`): compiles installed extensions ahead-of-time into the executable (`--kit`, `--runtime`, `--cwd`, `--exclude`, `--jit`).
   - `upi update [source|self|pi]` / `upi update --rollback`: self-update with signed `SHA256SUMS` (`keys/release.pub`, SSHSIG, `gh attestation verify`) and rollback.
   - `upi config`, `upi auth`, `upi mcp`, `upi install/remove/uninstall/list`: standard Pi/µPi subcommands.
2. **Finding Code**:
   - **`project_search` & `/docs` (`packages/docsearch/`)**: Rust BM25 + symbol/section graph search compiled into µPi; splits code at definitions (C/C++/HIP, Rust, Python, TS/JS, Go, shell); memory-mapped shared index (~32 MB/process + shared index); incremental updates; auto-disabled in projects <= 150 files (`README.md:524-526`).
   - **Cached `grep` / `find` + `fff` SIMD search (`packages/coding-agent/src/core/tools/{native-search.ts,fff-search.ts,grep.ts,find.ts}`)**: in-memory file tree (2 s TTL + mtime revalidation) & content cache (64 MB total, 512 KB/file); native SIMD literal matching (AVX2/NEON, TS fallback); fuzzy fallback on empty `find`; invalidated after `bash` commands; off by default (`--tools +grep,+find`).
3. **Editing & Background Checks**:
   - **Background compiler diagnostics & linters (`packages/coding-agent/src/core/checks/`)**: runs after `edit`, `write`, or file-modifying `bash`, diffed against pre-read/pre-edit state; waits up to `PI_CHECKS_WAIT` (30 s) before run ends. Supports C/C++/HIP (`libclang` + `compile_commands.json` + Makefile `-I`), Rust (`cargo check` / `clippy` in `target/pi-check` or `PI_CHECKS_CARGO_TARGET=shared`), Python (`ty` / `ruff`), TS (`tsc`), JS (`node --check` / Biome / oxlint), Shell (`bash -n` / `shuck`), Luau (`luau-analyze` / `selene`), JSON/TOML/YAML (Bun parsers / `rumdl` / `ryl`), AMD RDNA4 HIP (`radisa` for `gfx12*`). Configurable via `PI_CHECKS`, `LIBCLANG_PATH`, `HIP_OFFLOAD_ARCH`, etc.
   - **File tracking & fewer refused edits (`packages/coding-agent/src/core/file-tracking/`)**: `edit` applies to unread/disk-changed files when every `oldText` matches uniquely; `write` over unread existing file returns up to 30k chars and marks read; `cat` in bash marks read; CRLF/BOM preserved; nested `AGENTS.override.md`/`AGENTS.md`/`CLAUDE.md` injected on read.
   - **Edit matching (`edit-diff.ts`)**: whitespace-insensitive line matching (`seek_sequence`) with indentation normalization; closest-text diff with line numbers on miss; 3 lines of context on success.
   - **Bash file-write guard (`bash-file-writes.ts`)**: single-file `sed -i`, `perl -i`, or literal redirections (`echo`/`printf`/heredoc via `cat`/`tee`) are tracked/checked like `edit`/`write` locally (refused on `/remote`).
   - **Argument aliases (`packages/coding-agent/src/core/tools/argument-aliases.ts`)**: renames Claude Code / Qwen Code tool parameter names (`file_path` -> `path`, `old_string`, `run_in_background`, `absolute_path`, `include`, `is_background`) in `prepareArguments`.
4. **Running Commands**:
   - **Background jobs + `wait` tool + Job Hub (`background-jobs.ts`, `tools/wait.ts`, `alt+j` / `/jobs`)**: auto-backgrounds commands after 120 s or on `timeout` (unless `background: true`), wakes agent on completion; `wait` tool (2m/5m/10m); sleep-shortening when jobs run; long-sleep guard (>120 s); `alt+j` / `/jobs` live hub (`app.jobs.hub`).
   - **Bash output & speed (`bash.ts`, `bash-read-only.ts`, `bash-executor.ts`)**: head+tail truncation (first 40 lines + end, 2000 lines / 32 KB); ANSI/progress stripping + `NO_COLOR=1`; early execution of read-only commands during streaming; direct exec without shell when no shell syntax; pre-started bash pool.
5. **Remote Work (`/remote`, `--remote <host>[:<folder>]`, `PI_REMOTE`, `PI_SSH_COMMAND`)**:
   - Moves `read`, `edit`, `write`, `bash` over SSH (`ControlMaster` 4 h, interactive passphrase prompt, remote process-group cleanup watcher, preserves prompt cache & read/edit/job/wait/sleep-guard semantics; disables local `grep`/`find`/`ls` and local compiler diagnostics).
6. **Context, Compaction & Turns**:
   - **Compaction cap (`compaction.maxContextTokens`, default 128,000)**.
   - **Tool result pruning (`tool-result-pruning.ts`, `toolResultPruning`)**: shortens tool results >= 2000 chars (or images) older than latest 8 once context > 40,000 tokens, stepping by 8 to preserve prompt cache.
   - **Compaction ahead & from cache (`compaction/compaction-ahead.ts`)**: starts background summary at 15% (max 40k tokens) below threshold using the session's cached prefix; preserves user messages verbatim (up to 16k chars).
   - **Project snapshot (`project-snapshot.ts`, `PI_PROJECT_SNAPSHOT=off`)**: `<project>` listing (up to 150 git files or folder counts) in prompt.
   - **Cut-off response recovery (`cut-off-response.ts`)**: on output-limit truncation with no tool call, notifies model and continues with thinking reduced by 1 level (up to 3 times).
   - **System prompt (`system-prompt.ts`)**: trimmed to ~2.2k chars (from 2.9k), batched tool-call guidance, 2-min background rule, 2-line docs pointer.
   - **Batched tool execution**: ordered execution when `edit`/`write`/`bash` are present.
7. **Providers & Extensions**:
   - **Melious provider (`melious`, `MELIOUS_API_KEY`)**: 18 built-in models, default `glm-5.3`, live context/pricing fetch, retry on energy-hold errors.
   - **Herdr integration (`packages/coding-agent/src/extensions/herdr/`, `packages/tui/src/terminal-image.ts`)**: `HERDR_ENV`/`HERDR_SOCKET_PATH`/`HERDR_PANE_ID` socket reporter (`pane.report_agent`, `herdr:blocked`, etc.) + `TERM_PROGRAM=herdr` OSC 8 hyperlink detection.
   - **OpenSec bundled plugins (`plugins/opensec/plugins.ts`)**: `opensec-pi-subagents@0.20.9`, `opensec-pi-todo@2.13.1` (and `opensec-pi-fff` in `upi compile` tests).

### 1.3 `MichaelKinsy/PiG` Comparison (`gh api` inspection)
- **Go version**: `.go-version` is `1.27.4`; `go.mod` specifies `go 1.27.0` and `toolchain go1.27.4` (`make compliance` and `TestGoVersionPolicy` enforce `1.27.4`).
- **Build & Test**: `Makefile` + `automation/{dev,ci,gen,release,porter,images,make}/`. CLI has `pig build` (`coding/cli/build_command.go`) for Piglet packages/binaries, `pig verify`, `pig diagnose`, `pig docs`, `pig version`, `pig config`, `pig mcp`, `pig extension*`, `pig piglet`, `pig self-update`.
- **Docsearch / `project_search`**: **Not present** (`gh api search/code` for `docsearch` returned `0` results). `pig docs` (`coding/cli/docs_command.go`) is CLI documentation output, not an agent BM25/symbol search tool.
- **Remote (`env/` package)**: **Present** — `env/remote_env.go` ("Ports `packages/env/src/remote-env.ts`") with `ssh.go`, `connection.go`, `watch.go`, `list_files.go`, `daemon/`, `terminal.go`, `durable/env/`. This is Pi's upstream `packages/env` remote execution daemon architecture, distinct from microPi's `/remote` SSH ControlMaster tool-routing UX (`coding/cli` has no `/remote` slash command in `coding/cli/main.go`).
- **Herdr**: **Present** (`101` code search hits in PiG, e.g. terminal/OSC8/herdr support).
- **Plugins / Extensions**: PiG uses **Piglets & multi-language subprocess SDKs** (`extensions/sdk` Go, `sdk-rs` Rust, `sdk-py` Python, `sdk-lisp` Common Lisp, `sdk-ts` TypeScript) + `coding/extension/host/subprocess/protocol.go` + `coding/piglet/` + `coding/pigletbuild/` (replacing microPi's Bun AOT `upi compile` and `plugins/opensec` `.core.cjs` unwrapping).
- **Background jobs / `wait` / `compaction-ahead` / `checks`**: Not present as microPi's built-in harness features in `docs/parity/PORT_MAP.md` or `coding/` (PiG tracks upstream Pi parity in `docs/parity/PORT_MAP.md` and additive features in `docs/additive-features.md`).

## 2. Logic Chain
1. Local environment has Go `1.24.3`, while `PiG` pins Go `1.27.4` (`go.mod` + `.go-version`). Because `GOTOOLCHAIN=auto` and `GOPROXY=https://proxy.golang.org,direct` are active with network access, Go can auto-fetch `go1.27.4`, or `make setup` / manual Go 1.27.4 install can be used. Rust (`1.100.0`), GCC (`13.3.0`), Clang (`18.1.3`), `rg` (`14.1.0`), Node (`22.23.5`), Bun (`1.2.14`), and `gh` (`2.103.3`) are all ready for building/testing reference code (including `packages/docsearch` in Rust). Only `fd` is missing.
2. microPi's value-add over stock Pi 1.4.0 consists of: (a) distribution/AOT (`upi`, `upi compile`, `upi doctor`, `upi update`, `plugins/opensec`), (b) agent-loop/tool-execution optimizations (`checks`, `file-tracking`, `edit-diff`, `bash-file-writes`, `argument-aliases`, `background-jobs` + `wait` + job hub, `bash` head+tail/early-start/direct-exec), (c) search (`docsearch` Rust BM25 + `fff` SIMD cached `grep`/`find`), (d) context/turn optimizations (`compaction-ahead`, `tool-result-pruning`, `project-snapshot`, `cut-off-response`, `system-prompt.ts`), (e) `/remote` SSH UX, (f) `melious` provider, and (g) `herdr` extension.
3. Porting microPi to PiG means: (a) AOT Bun/`upi compile` is superseded by PiG's native Go binary + Piglets/subprocess SDKs (`coding/piglet*`), (b) `herdr` and `env/` remote execution already have foundations in PiG (though `/remote` UX in `coding/cli` and `coding` needs wiring/comparison), (c) `docsearch` can either be ported to Go or bridged via PiG's Rust subprocess SDK (`extensions/sdk-rs`) / CGo, and (d) the core microPi harness features (`checks`, `background-jobs`/`wait`, `file-tracking`, `compaction-ahead`, `tool-result-pruning`, `project-snapshot`, `cut-off-response`, `argument-aliases`, `fff` search, `melious`) have no PiG equivalents and are the primary porting surface.

## 3. Caveats
- `MichaelKinsy/PiG` was inspected via `gh api` (directory listings, `PORT_MAP.md` head, `additive-features.md` head, `go.mod`, `.go-version`, `Makefile`, `env/remote_env.go`, `coding/cli/main.go`, and `search/code` queries) rather than a full local clone; individual files in `internal/codingagent/` were not all read in full.
- `GOTOOLCHAIN=auto` download of Go 1.27.4 was inferred from `go env` + `curl -sI https://go.dev/dl/` rather than executing a `go1.27.4` build.

## 4. Conclusion
- **Environment**: Ready for Rust, C/C++, Node, Bun, and GitHub CLI work; Go is `1.24.3` locally and will need `1.27.4` (via `GOTOOLCHAIN=auto` or explicit install) to satisfy PiG's `go.mod` and compliance checks.
- **microPi vs PiG**: PiG already has native Go compilation, Piglet/multi-language extension hosting (including Rust SDK), `herdr` support, and `env/` remote execution (`remote_env.go`). It lacks `docsearch`, `fff` SIMD search, background compiler `checks`, `background-jobs`/`wait`/job-hub, `file-tracking`/`edit-diff` enhancements, `bash-file-writes` guard, `argument-aliases`, `compaction-ahead`, `tool-result-pruning`, `project-snapshot`, `cut-off-response`, and `melious`.

## 5. Verification Method
- Re-run toolchain checks: `for cmd in go cargo rustc gcc clang rg fd node bun gh; do command -v $cmd; done` and `go version`.
- Verify microPi features in `/home/antigravity/Code000-211/microPi/README.md`, `docs/releases/0.1.0.md`, and `packages/coding-agent/src/core/`.
- Verify PiG state via `gh api repos/MichaelKinsy/PiG/contents/.go-version` and `gh api 'search/code?q=repo:MichaelKinsy/PiG+docsearch'`.
