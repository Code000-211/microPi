# Step 0 Survey Handoff: microPi Architecture & Structure

**Summary**: `microPi` (`µPi`, CLI command `upi`, version `0.1.0`, tag `upi-v0.1.0`, branch `pi-bolt`) is a fork of upstream Pi `v1.4.0` (`https://github.com/earendil-works/pi.git`) built on top of `Pi-Bolt` (an ahead-of-time compiled Bun + patched WebKit/JavaScriptCore runtime). It combines Pi-Bolt's AOT single-binary compilation, `upi compile` extension-baking kit, and signed release/install infrastructure with microPi's own agentic-efficiency features (background language checkers, background jobs + `wait` tool, cached SIMD `grep`/`find`, Rust `docsearch` `project_search`, `compaction-ahead`, `project-snapshot`, `/remote` SSH workspace, `cut-off-response` recovery, and Claude/Qwen tool argument aliases).

---

## 1. Observation

### 1.1 Repository Identity, Upstream & Pi-Bolt Relationship
- **Working directory**: `/home/antigravity/Code000-211/microPi`
- **Remote**: `origin https://github.com/Code000-211/microPi.git` (`git remote -v`)
- **Branch / Tag**: `* pi-bolt` (`remotes/origin/pi-bolt`), tag `upi-v0.1.0` (`git branch -a`, `git tag -l`)
- **Version files**:
  - `VERSION`: `0.1.0`
  - `npm/package.json`: `"name": "upi"`, `"version": "0.1.0"`, `"bin": { "upi": "bin/upi.exe" }`
  - `package.json`: `"name": "pi-monorepo"`, `"version": "0.0.3"`, `"engines": { "node": ">=22.19.0" }`
- **`sources.json` (lines 1–20)**:
  - `webkit`: `https://github.com/oven-sh/WebKit.git`, branch `claude/sound-types-aot`, commit `d28f16e5cc234c19054d7edec2a2de5519500a1f`, patches `patches/webkit.patch` (947,189 bytes)
  - `bun`: `https://github.com/oven-sh/bun.git`, commit `37da174d500f2201793c8352f791afdd9102eab9`, patches `patches/bun.patch` (893,681 bytes)
  - `pi`: `"µPi is a fork of Pi: Pi is this repository's own tree, forked at the tag below"`, upstream `https://github.com/earendil-works/pi.git`, tag `v1.4.0`, profile `profiles/pi-1.4.0`
- **Recent `git log` (`git log -n 60 --oneline`)**:
  - PRs `#22`–`#33` rebranded Pi-Bolt to µPi (`upi`, `~/.micropi`, `MICROPI_*`, `keys/release.pub`, `upi-v*` tags, `Code000-211/microPi` releases, `docsearch` compiled via `--builtins`, `install.sh` / `install.ps1`, `install-from-source.sh`, `MICROPI_HAS_RELEASES = true`).
  - `149e891` / `c88552d`: `docs/releases/0.1.0.md` release notes for µPi `0.1.0`.
  - PRs `#17`, `#20`, `#51`, `#53`, `#55`, `#56`: microPi feature/fix commits on top of `pi-bolt` (`rdna4-isa` / `RADISA_COMPILE_COMMANDS`, `wait` race/ladder fix, Claude Code & Qwen Code tool argument aliases).

### 1.2 Root Configuration, Build & Release Files
| File / Dir | Purpose & Observed Details |
|---|---|
| `package.json` | Root npm workspaces (`packages/*`, `apps/*`, 5 `packages/coding-agent/examples/extensions/*`). Scripts: `build` / `build:offline` (telemetry -> tui -> codemode -> mcp -> ai -> agent -> coding-agent), `check` (catalog-modules, biome, pinned-deps, runtime-deps, ts-imports, entry-graphs, install-lock, `tsc --noEmit`, browser-smoke), `test`, `release:*`. |
| `tsconfig.json` | Maps `@earendil-works/pi-*` (and `typebox`) to `packages/*/src/*` with `NodeNext` resolution and `noEmit: true`. |
| `patches/` | `bun.patch` (893 KB), `webkit.patch` (947 KB) — Pi-Bolt's AOT JSC/Bun runtime patches. |
| `profiles/` | `pi-1.1.0`, `pi-1.2.0`, `pi-1.3.0`, `pi-1.4.0` (`bytecode.order`, `heap-functions.txt`, `regexps.txt`), plus `runtime-darwin-arm64.hints` and `runtime-win32-x64.hints`. |
| `plugins/opensec/` | `plugins.ts` & `package.json` bundling `opensec-pi-subagents@0.20.9` and `opensec-pi-todo@2.13.1` as `InlineExtension`s (`builtin: true, replaceable: true`) with `Symbol.for("<pkg>@<ver>:host-modules")` host module injection and locale caching. |
| `install.sh` / `install.ps1` | Signed release installers (`Ed25519` via `keys/release.pub`). Install to `~/.micropi` (`%USERPROFILE%\.micropi`), link `upi` into `~/.local/bin` (or `$PREFIX/bin` on Termux, user `PATH` on Windows). Support `MICROPI_VERSION`, `MICROPI_CHANNEL` (`stable`/`beta`), `MICROPI_VARIANT` (`x64`, `x64-baseline`, `x64-jit`, `arm64`, `arm64-jit`), `MICROPI_INSTALL`, `MICROPI_SOURCE` (`github`/`npm`). |
| `keys/` | `release.pub` (µPi Ed25519 public key) and `pibolt-release.pub` (fallback Pi-Bolt key). |
| `.github/workflows/` | `ci.yml`, `release.yml` (tags `upi-v*`), `runtime.yml`, `publish.yml`, `heap-profile.yml`, `upstream.yml`, `codeql.yml`. |
| `bench/` | `benchmark.py`, `harness.py`, `fake_model.py`, `harness/`, `fixtures/`, `orderfile_session.py`, `winproc.py` (560-run paired benchmark harness against stock Pi 1.4.0). |
| `test.sh` | Isolated-home test runner (`env -i` with temp `HOME`/`TMPDIR`/npm config, `PI_NO_LOCAL_LLM=1`, runs `npm test`). |

### 1.3 Key Scripts in `scripts/`
- `scripts/fetch-runtime.sh`: Downloads signed `upi-runtime-<platform>.tar.gz` from `Code000-211/microPi` (falling back to `opensec-git/Pi-Bolt` `pi-bolt-runtime-<platform>.tar.gz` if no µPi release runtime exists) to `.work/runtime/bun` + `RUNTIME_STAMP`.
- `scripts/build-runtime.sh`: Builds Bun on patched WebKit (`BUN_WEBKIT_PATH`, `LINUX_GLIBC_SYSROOT`, `--profile=release-local`, LTO, linker order file from `profiles/runtime-*.hints`).
- `scripts/prepare-pi.sh`: Runs `npm ci --ignore-scripts` and `npm run build:offline` (or `build` with `--refresh-models` / `--tag`) in-place.
- `scripts/build-docsearch.mjs`: Runs `cargo build --release -p docsearch-node` in `packages/docsearch`, stages `docsearch.<os>-<arch>.node` + `builtins.ts` manifest (`{ name: "docsearch", builtin: true, replaceable: true, factory: docsearch }`).
- `scripts/build-pi.sh`: Compiles `packages/coding-agent/dist/bun/cli.js` (+ workers `image-resize-worker.ts`, `extensions/codemode/worker.ts`) using the AOT Bun runtime with `BUN_JSC_useJIT=0`, `BUN_STATIC_HEAP=1`, `BUN_AOT=1`, `BUN_JSC_omitBytecodeFromStaticHeap=1`, `BUN_JSC_useAOTLoopSplitting=1` (policy 5), `BUN_JSC_useImmutableIntrinsics=1`, `--splitting`, `--compile-exec-argv=--smol`, `--define PIBOLT_BUILD=...`, `--define MICROPI_PI_VERSION=...`. Supports `--builtins`, `--plugins`, `--plugins-from-installed`, `--kit DIR` (via `scripts/make-kit.mjs`). On macOS builds `darwin-launcher.c`, `darwin-spawn-proxy.c`, `darwin_libsystem.py`, `darwin_hot_pages.py`.
- `scripts/make-kit.mjs`: Uses `bun build --metafile` to collect all files needed by Pi + provided packages (`@earendil-works/pi-coding-agent`, `pi-ai`, `pi-agent-core`, `pi-tui`, `typebox`) into a standalone compile kit for `upi compile`.

### 1.4 Packages in `packages/`
All 7 TypeScript packages carry version `1.4.0` (matching upstream Pi `v1.4.0`), plus 1 Rust/TS package (`docsearch`):
1. `packages/telemetry` (`@earendil-works/pi-telemetry`): Vendor-neutral telemetry contracts, memory/noop recorders, testing utilities.
2. `packages/tui` (`@earendil-works/pi-tui`): Terminal UI with differential rendering, alt-screen/main-screen (`tui-alt-screen.ts`, `tui-main-screen.ts`), native clipboard/terminal addons (`native/{linux,darwin,win32}`).
3. `packages/codemode` (`@earendil-works/pi-codemode`): Sandboxed JS execution (QuickJS/Cloudflare/worker) where the only capability is calling injected tools.
4. `packages/mcp` (`@earendil-works/pi-mcp`): Standalone MCP client (OAuth, transports, protocol, testing).
5. `packages/ai` (`@earendil-works/pi-ai`): Unified LLM API, providers, `models.generated.ts`, `providers/data/` (committed for offline builds), `compat.ts` (`isRecoverableLength`).
6. `packages/agent` (`@earendil-works/pi-agent-core`): Agent loop (`agent-loop.ts`), `Agent` (`agent.ts`), proxy, `stream-fn.ts`.
7. `packages/docsearch` (microPi addition): Rust workspace (`Cargo.toml` members `core`, `node`; `opt-level = 3`, `lto = "fat"`) + `extension/index.ts`. Provides `project_search` tool and `/docs` command using mmap'd BM25 + symbol/section graph.
8. `packages/coding-agent` (`@earendil-works/pi-coding-agent`): Main CLI, agent session, tools, modes, and the core of both Pi-Bolt and microPi customizations.

### 1.5 Custom microPi / Pi-Bolt Modules in `packages/coding-agent/src/`
- **µPi / Pi-Bolt runtime & CLI integration**:
  - `src/upi.ts`: Parses `PIBOLT_BUILD` & `MICROPI_PI_VERSION` defines, release URLs (`Code000-211/microPi`), `MICROPI_RELEASE_KEY`, `MICROPI_HAS_RELEASES = true`, installer spawning, channel (`stable`/`beta`).
  - `src/upi-compile.ts` (2,272 lines), `src/upi-installer.ts`, `src/upi-rollback.ts`, `src/upi-update-windows.ts`: `upi compile` (with `compile.json` persistence and `core/extensions/compiled.ts`), signed installer verification, rollback, Windows self-update.
  - `src/gen-plugin-manifest.mjs` & `src/gen-plugin-manifest.d.mts`: Generates plugin manifests from `~/.pi/agent` extensions/packages.
  - `src/bun/`: `cli.ts`, `main.lazy.ts`, `version-fast-path.ts`, `sandbox-env-setup.ts`, `restore-sandbox-env.ts`, `runtime-setup.ts` (fast `--version` path before loading `main.ts`).
  - `src/config.ts` (lines 346, 590–596), `src/cli/args.ts` (line 324), `src/main.ts` (lines 586–686): `COMMAND_NAME = "upi"`, `PRODUCT_NAME = "µPi"`, `upi doctor`, `upi compile`, `quietOtherHerdrReporters()`, `--remote`.
- **microPi Agentic & Tool Enhancements**:
  - **Background compiler/linter checks** (`src/core/checks/background-checks.ts`, `src/core/checks/checkers.ts`, `src/core/checks/cpp/libclang.ts`, `native/libclang/`): Runs after `edit`/`write` (150 ms settle, 30 s idle wait via `PI_CHECKS_WAIT`). 15 checkers in `CHECKERS` (`checkers.ts:920-936`): `rust` (`cargo check`), `python` (`ty`), `typescript` (TS 7 native `tsc`), `javascript`, `shell` (`shuck`), `luau` (`luau-analyze`), `json`, `toml`, `yaml`, `jsLint`, `markdown`, `yamlLint`, `luauLint`, `cppLint`, `rdna4` (`radisa`), plus in-process `libclang` (`LibclangEngine`) for C/C++/HIP.
  - **Background jobs & `wait` tool** (`src/core/background-jobs.ts`, `src/core/tools/wait.ts`, `src/modes/interactive/components/job-hub.ts`): Long/timed-out `bash` commands move to background (`bg1`, `bg2`, ...), wake the agent on completion (`BACKGROUND_JOB_MESSAGE_TYPE = "background-job"`, 1.5 s settle window), `wait` tool with backoff ladder (`WAIT_LADDER_MS = [120_000, 300_000, 600_000]`), interactive job hub (`alt+j`).
  - **Cached SIMD `grep` & `find`** (`src/core/tools/native-search.ts`, `src/core/tools/fff-search.ts`, `native/fff/`): In-memory file tree/content cache (64 MB pool, 512 KB/file) + native N-API SIMD search (`fff-bridge.node`) with TS fallback; invalidated after `bash` commands.
  - **File tracking & edit ergonomics** (`src/core/file-tracking/file-tracker.ts`, `file-state.ts`, `nested-instructions.ts`, `src/core/tools/bash-file-writes.ts`, `edit-diff.ts`): Read-before-edit tracking, post-edit snippets, closest-line suggestions on missed edits, `sed -i` / bash file write checks, nested `AGENTS.md` reminders.
  - **Compaction-ahead** (`src/core/compaction/compaction-ahead.ts`): Starts compaction summary in background at 15% (max 40k tokens) below threshold and allows overrun up to `min((limit - threshold)/2, threshold * 0.5)`.
  - **Project snapshot** (`src/core/project-snapshot.ts`): Bounded project file listing injected into system prompt (up to 150 files or 2-level dir counts).
  - **Remote workspace** (`src/core/remote/remote-workspace.ts`, `ssh.ts`): `/remote host:dir` and `--remote` routing `read`, `edit`, `write`, `bash` over SSH while keeping tool declarations intact for prompt cache stability.
  - **Cut-off response recovery** (`src/core/cut-off-response.ts`): Steps down thinking level (`MAX_CUT_OFF_NOTICES = 3`) and injects continuation notice when a response hits `stopReason === "length"` with only thinking.
  - **Cross-harness tool argument aliases** (`src/core/tools/argument-aliases.ts`): Normalizes Claude Code (`file_path`, `old_string`, `run_in_background`) and Qwen Code (`absolute_path`, `include`, `is_background`) argument names in `prepareArguments`.
  - **Built-in extensions** (`src/extensions/`): Includes `acp`, `codemode`, `herdr`, `llama`, `mcp`, `mcp-server`, `tool-search`, `usage`.

---

## 2. Logic Chain

1. **Identity & Fork Lineage**: `sources.json` explicitly states `µPi` is a fork of upstream Pi `v1.4.0`, and `git log` shows PRs `#22`–`#33` rebranding Pi-Bolt's `pi-bolt` branch to `upi` / `Code000-211/microPi` (`upi-v0.1.0`), with `packages/*` still at `1.4.0` and root `tsconfig.json` / `package.json` still using `@earendil-works/pi-*` workspace names.
2. **Dual-Layer Architecture**:
   - **Layer 1 (Pi-Bolt AOT Engine)**: `patches/bun.patch`, `patches/webkit.patch`, `profiles/`, `scripts/fetch-runtime.sh`, `scripts/build-runtime.sh`, `scripts/build-pi.sh`, `scripts/make-kit.mjs`, `packages/coding-agent/src/upi*.ts`, `src/bun/*`, `src/gen-plugin-manifest.mjs`. This layer handles AOT compilation into a single native binary, `upi compile` extension baking, `upi doctor`, and signed self-updates.
   - **Layer 2 (microPi Agentic & Native Features)**: `packages/docsearch` (Rust `project_search`), `core/checks/` + `native/libclang`, `core/tools/native-search.ts` + `native/fff`, `core/background-jobs.ts` + `tools/wait.ts`, `core/compaction/compaction-ahead.ts`, `core/project-snapshot.ts`, `core/remote/`, `core/cut-off-response.ts`, `core/tools/argument-aliases.ts`, `extensions/herdr`. These live in `packages/coding-agent` and `packages/docsearch` and are independent of *how* the binary is compiled, except where `build-pi.sh --builtins` / `make-kit.mjs` stages `docsearch` or where `PIBOLT` gates `upi`-specific CLI behavior.
3. **Porting Implication (Pi-Bolt → PiG)**: Porting microPi onto `PiG` (`https://github.com/MichaelKinsy/PiG`) means replacing or adapting Layer 1 (the Pi-Bolt AOT runtime, build/kit/compile/installer/update/release machinery) while preserving Layer 2 (the microPi packages, tools, checks, docsearch, remote, compaction, and harness features) on top of PiG's runtime/build model.

---

## 3. Caveats

- **PiG not yet inspected**: This Step 0 survey was scoped strictly to `/home/antigravity/Code000-211/microPi`. `https://github.com/MichaelKinsy/PiG` was not fetched or analyzed here.
- **Line-level diff vs. upstream `v1.4.0`**: Because upstream `earendil-works/pi` `v1.4.0` is not a git remote/tag in this clone (`git tag -l` only shows `upi-v0.1.0`), exact line-by-line diffs of every modified upstream file (e.g., inside `agent-session.ts` or `system-prompt.ts`) were not enumerated; instead, all new modules, entrypoints, and wiring points were mapped.
- **No build/test executed**: Per read-only survey scope and `AGENTS.md` ("Never run `npm run build` or `npm test` unless requested by the user"), no builds or tests were run.

---

## 4. Conclusion

`microPi` (`upi` v0.1.0) is a clean two-layer fork of Pi `v1.4.0`:
1. **Pi-Bolt infrastructure** (`patches/`, `profiles/`, `scripts/*`, `install.sh`, `install.ps1`, `packages/coding-agent/src/upi*.ts`, `src/bun/*`, `plugins/opensec/`) provides the patched Bun+WebKit AOT runtime, single-binary build, `upi compile` kit, and signed GitHub release/self-update pipeline.
2. **microPi product features** (`packages/docsearch`, `packages/coding-agent/src/core/{checks,compaction/compaction-ahead.ts,background-jobs.ts,project-snapshot.ts,remote,file-tracking,cut-off-response.ts}`, `src/core/tools/{wait.ts,native-search.ts,fff-search.ts,argument-aliases.ts}`, `native/{fff,libclang}`, `extensions/herdr`) implement the agentic speed/cost optimizations.

---

## 5. Verification Method

All observations above can be independently verified by inspecting:
- Metadata & config: `sources.json`, `VERSION`, `package.json`, `tsconfig.json`, `npm/package.json`
- Git state: `git -C /home/antigravity/Code000-211/microPi log -n 60 --oneline`, `git branch -a`, `git tag -l`, `git remote -v`
- Build & release scripts: `scripts/build-pi.sh`, `scripts/prepare-pi.sh`, `scripts/fetch-runtime.sh`, `scripts/build-runtime.sh`, `scripts/build-docsearch.mjs`, `scripts/make-kit.mjs`, `.github/workflows/release.yml`
- Core modules: `packages/coding-agent/src/upi.ts`, `packages/coding-agent/src/upi-compile.ts`, `packages/coding-agent/src/main.ts`, `packages/coding-agent/src/core/checks/checkers.ts`, `packages/coding-agent/src/core/background-jobs.ts`, `packages/coding-agent/src/core/tools/wait.ts`, `packages/coding-agent/src/core/remote/remote-workspace.ts`, `packages/docsearch/Cargo.toml`
- Project check command (when validating future changes): `npm run check` and `./test.sh` (per `AGENTS.md`).
