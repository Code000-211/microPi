# Step 0 Survey Handoff: PiBolt Integration & Build/Test Infrastructure

**Summary**: `microPi` (µPi) is a fork of Pi (`earendil-works/pi` v1.4.0) built on top of Pi-Bolt (`opensec-git/Pi-Bolt`), which compiles Pi ahead-of-time (AOT) into a single native executable via a patched WebKit/JavaScriptCore and patched Bun (`patches/webkit.patch`, `patches/bun.patch`). PiBolt is wired across three layers: (1) the AOT engine, build scripts, CI/release workflows, and installers (`PIBOLT_*` env vars, `scripts/build-runtime.sh`, `scripts/build-pi.sh`, `npm/install.cjs`, `install.sh`, `install.ps1`), (2) `packages/coding-agent` runtime behavior gated on the compile-time `PIBOLT_BUILD` define (`PIBOLT` in `src/upi.ts`), `upi compile` (`src/upi-compile.ts`), `upi doctor` (`src/cli/doctor.ts`), and `piBolt.packages` settings overlay (`src/core/settings-manager.ts`), and (3) native addons (`packages/docsearch`, `packages/coding-agent/native/fff`, `packages/coding-agent/native/libclang`) and Darwin AOT launcher/spawn helpers (`scripts/lib/darwin-*`).

---

## 1. Observation

### Environment & Git State
- Branch: `pi-bolt...origin/pi-bolt` (`git status -sb` shows only untracked `.agents/`).
- Installed runtimes in devbox: Node `v22.23.5`, npm `12.2.2`, Bun `1.2.14`.
- Root `VERSION`: `0.1.0` (`npm/package.json` version: `0.1.0`, `packages/coding-agent/package.json` version: `1.4.0`).

### 1.1 Map of `PiBolt` / `pibolt` / `pi-bolt` References (Case-Insensitive)

A repository-wide `rg -il 'pibolt|pi-bolt'` (excluding `.git`, `node_modules`, `.agents`) matched **114 files**:

| Category | Files |
|---|---|
| **Engine & Patches** | `sources.json` (indirectly via `patches/*.patch`), `patches/bun.patch` (18,567 lines), `patches/webkit.patch` (17,578 lines), `keys/pibolt-release.pub` |
| **Build, Kit & Packaging Scripts** | `scripts/build-pi.sh`, `scripts/build-pi.ps1`, `scripts/build-runtime.sh`, `scripts/build-runtime.ps1`, `scripts/fetch-runtime.sh`, `scripts/fetch-sources.sh`, `scripts/fetch-sources.ps1`, `scripts/check-patches.sh`, `scripts/prepare-pi.sh`, `scripts/make-kit.mjs`, `scripts/package-release.sh`, `scripts/package-release.ps1`, `scripts/install-from-source.sh`, `scripts/bump-version.sh`, `scripts/publish-installer.sh`, `scripts/sign-release.sh`, `scripts/train-profile.sh`, `scripts/train-heap.ps1`, `scripts/train-runtime-hints.sh`, `scripts/train-runtime-hints.ps1`, `scripts/windows-selftest.ps1`, `scripts/lib/common.sh`, `scripts/lib/check-cfg-sysv-calls.py`, `scripts/lib/darwin-launcher.c`, `scripts/lib/darwin-spawn.h`, `scripts/lib/darwin-spawn-helper.c`, `scripts/lib/darwin-spawn-proxy.c`, `scripts/toolchain/linux-build.Dockerfile` |
| **CI/CD Workflows** | `.github/workflows/ci.yml` (pushes/PRs on `pi-bolt` branch, `PIBOLT_RUNTIME_OLDER=1`), `.github/workflows/runtime.yml` (attests runtimes from `refs/heads/pi-bolt`), `.github/workflows/release.yml` (`PIBOLT_RUNTIME_STAMP`, provenance checks against `refs/heads/pi-bolt`) |
| **Installers & Distribution** | `install.ps1` (`*-PiBolt*` functions), `npm/install.cjs` (`PiBoltInstaller.Cpu`, `.pibolt-old` cleanup), `.gitignore` |
| **Coding Agent Source (`packages/coding-agent/src/`)** | `upi.ts`, `upi-compile.ts`, `upi-installer.ts`, `upi-rollback.ts`, `upi-update-windows.ts`, `gen-plugin-manifest.mjs`, `config.ts`, `main.ts`, `package-manager-cli.ts`, `bun/version-fast-path.ts`, `cli/args.ts`, `cli/doctor.ts`, `core/agent-session.ts`, `core/package-manager.ts`, `core/resource-loader.ts`, `core/settings-manager.ts`, `core/settings-schema.ts`, `core/system-prompt.ts`, `core/extensions/jit-hint.ts`, `core/extensions/loader.ts`, `core/extensions/runner.ts`, `core/tools/find.ts`, `core/tools/grep.ts`, `extensions/index.ts`, `modes/interactive/components/config-selector.ts`, `modes/interactive/interactive-mode.ts`, `modes/interactive/session-share.ts`, `utils/version-check.ts`, plus `packages/coding-agent/schemas/settings.schema.json` |
| **Tests** | `packages/coding-agent/test/`: `settings-pibolt-packages.test.ts`, `herdr-extension.test.ts`, `native-search.test.ts`, `native-search-large-files.test.ts`, `package-manager-cli-preflight.test.ts`, `upi-*.test.ts` (14 files); `scripts/`: `gen-plugin-manifest.test.mjs`, `npm-postinstall.test.mjs`, `update-homebrew-formula.test.mjs`; `tests/`: `aot/`, `cfg/`, `compat/`, `installer/`, `pi/`, `runtime/`, `termux/` |
| **Docs, Bench & Plugins** | `README.md`, `CONTRIBUTING.md`, `SECURITY.md`, `THIRD_PARTY_NOTICES.md`, `LICENSE`, `docs/BUILDING.md`, `docs/PLUGINS.md`, `docs/RELEASING.md`, `docs/TROUBLESHOOTING.md`, `docs/releases/0.1.0.md`, `examples/plugins/README.md`, `bench/harness.py`, `bench/README.md`, `plugins/opensec/package-lock.json` |
| **Unreferenced** | `nix/` contains only `nix/model-catalog.json` (no PiBolt references). `install.sh` did not match `pibolt|pi-bolt` directly (uses `MICROPI_*` / `upi-*`). |

### 1.2 PiBolt-Specific Features, Patches & Wiring

1. **AOT Runtime & Patches (`sources.json`, `patches/`)**:
   - `sources.json` pins `oven-sh/WebKit.git` (branch `claude/sound-types-aot`, commit `d28f16e5...`, `patches/webkit.patch`) and `oven-sh/bun.git` (commit `37da174d...`, `patches/bun.patch`), plus `pi` upstream `v1.4.0` (`profiles/pi-1.4.0`).
   - `patches/webkit.patch` (17,578 lines): JavaScriptCore AOT compiler (`Pi-Bolt: JavaScriptCore's ahead-of-time compiler`), x86-64/ARM64/Darwin/Windows x64 static heap & AOT code image, loop splitting, immutable intrinsics, order-file layout.
   - `patches/bun.patch` (18,567 lines): `Pi-Bolt: ahead-of-time compiled executables`, static heap, `BUN_AOT=1`, `BUN_STATIC_HEAP=1`, `BUN_AOT_JIT=0`, `BUN_AOT_CPU=baseline`, `bun:ffi requires the JIT` error when JIT is off, GC/mimalloc/spawnSync/TLS/DNS fixes.
   - `scripts/fetch-runtime.sh`: Downloads prebuilt `upi-runtime-<platform>` (or falls back to `opensec-git/Pi-Bolt` `pi-bolt-runtime-<platform>` verified with `keys/pibolt-release.pub`) into `.work/runtime/bun`.
   - `scripts/build-runtime.sh`: Builds WebKit + Bun from source with LTO and linker order files (`profiles/runtime-*.hints`) into `.work/runtime/bun`, generating `RUNTIME_STAMP` (`scripts/runtime-stamp.sh`).

2. **AOT Compilation of Pi & Extensions (`scripts/build-pi.sh`, `scripts/make-kit.mjs`, `scripts/lib/common.sh`)**:
   - `scripts/build-pi.sh` compiles `packages/coding-agent/dist/bun/cli.js` (plus workers `src/utils/image-resize-worker.ts` and `src/extensions/codemode/worker.ts`) using `.work/runtime/bun build --compile ... --splitting --bytecode --format=esm` with `BUN_AOT=1`, `BUN_STATIC_HEAP=1`, `BUN_JSC_useJIT=0`, `BUN_JSC_omitBytecodeFromStaticHeap=1`, `BUN_JSC_useAOTLoopSplitting=1`, `BUN_JSC_useImmutableIntrinsics=1`, and training profiles (`profiles/pi-1.4.0/{bytecode.order,regexps.txt}`).
   - Injects compile-time defines: `--define "PIBOLT_BUILD=\"$PIBOLT_VERSION $CPU_VARIANT jit-$JIT${RUNTIME_STAMP:+ $RUNTIME_STAMP}\""` and `--define "MICROPI_PI_VERSION=\"$VERSION\""`.
   - On macOS (`darwin`), compiles `scripts/lib/darwin-launcher.c` + `darwin-spawn-helper.c` as `pi` and `darwin-spawn-proxy.c` as `pi-spawn`, renaming the Bun AOT binary to `pi-bin` (using `PIBOLT_SPAWN_HELPER_ENV="BUN_INTERNAL_SPAWN_HELPER"` and `BUN_INTERNAL_STATIC_HEAP_AT_LINKED_ADDRESS=1`).
   - `--builtins` invokes `scripts/build-docsearch.mjs` (builds Rust `docsearch-node` via `cargo` and stages `builtins.ts` into `.pibolt-plugins/builtins/`).
   - `--kit DIR` invokes `scripts/make-kit.mjs` to bundle `tree/` (minimal Pi `dist/` + `node_modules/`), `profile/`, `kit.json`, and `gen-plugin-manifest.mjs` for `upi compile`.

3. **Runtime Behavior Gated on `PIBOLT` (`packages/coding-agent/src/upi.ts`)**:
   - `PIBOLT` (`parsePiBoltBuild(PIBOLT_BUILD)`) is `undefined` in normal Node/Bun/source runs and populated only in `build-pi.sh` / `upi compile` builds.
   - `src/bun/version-fast-path.ts` & `src/main.ts:685`: Prints `µPi <version> (Pi <piVersion>, <platform>-<variant>, JIT on|off)`.
   - `src/cli/args.ts:324` & `src/main.ts:644` & `src/package-manager-cli.ts:1160`: Exposes `upi doctor` and `upi compile` commands.
   - `src/core/tools/grep.ts:168` & `src/core/tools/find.ts:199`: Enables native SIMD search (`fff-bridge.node` in `packages/coding-agent/native/fff/`) by default when `PIBOLT !== undefined`.
   - `src/core/agent-session.ts:227`: `EARLY_TOOL_NAMES` includes `"grep", "find"` when `PIBOLT` is defined.
   - `src/extensions/index.ts:21`: Conditionally registers built-in `herdr` extension when `PIBOLT && herdrFromEnvironment()`.
   - `src/core/extensions/loader.ts:698,782`: Virtual module injection (`PI_MODULE_NAMES` / `JIT_ONLY_PACKAGES`) and skipping external Herdr Pi integration when `PIBOLT` is set.
   - `src/core/extensions/jit-hint.ts` & `runner.ts:613`: Intercepts `bun:ffi requires the JIT` errors on `jit-off` builds and suggests `piBoltJitSwitchCommand()`.
   - `src/core/settings-manager.ts`, `src/core/package-manager.ts`, `src/modes/interactive/components/config-selector.ts`: Implements `piBolt.packages` / `piBolt.packagesOverlay` overlay on top of `packages` when `PIBOLT !== undefined` (and migrates `piBolt`-only packages).
   - `src/core/system-prompt.ts:270,280`: Changes preamble and docs hint when `PIBOLT` is set.
   - `src/utils/version-check.ts`, `src/upi-installer.ts`, `src/upi-rollback.ts`, `src/upi-update-windows.ts`: Self-update, rollback, and release verification against `Code000-211/microPi` releases (`upi-v*`).

4. **Native Addons (`packages/coding-agent/native/`, `packages/docsearch/`, `packages/tui/native/`)**:
   - `packages/tui/native/{darwin,linux,win32}`: TUI native `.node` prebuilds (part of upstream Pi).
   - `packages/coding-agent/native/fff/build.mjs`: Compiles `fff-bridge.c` into `fff-bridge.node` (AVX2 on x64) for cached `grep`/`find`.
   - `packages/coding-agent/native/libclang/build.mjs`: Compiles `libclang-bridge.c` into `libclang-bridge.node` for C/C++ diagnostics.
   - `packages/docsearch/`: Rust workspace (`docsearch-core`, `docsearch-node`) compiled via `cargo` into `docsearch.<os>-<arch>.node` for `project_search`.

### 1.3 Build, Typecheck, Lint & Test Infrastructure

- **Root `package.json` scripts**:
  - `npm run check`: Runs `generate:catalog-modules` -> `node scripts/biome-check.mjs --write` -> `check:pinned-deps` -> `check:runtime-deps` -> `check:ts-relative-imports.mjs` -> `check:entry-graphs.mjs` -> `generate-coding-agent-install-lock.mjs --check` -> `tsc --noEmit` -> `check:browser-smoke.mjs`.
  - `npm run build` / `npm run build:offline`: Sequentially builds `telemetry`, `tui`, `codemode`, `mcp`, `ai` (`build:offline` skips network model fetch), `agent`, `coding-agent`.
  - `npm test`: Runs `generate:catalog-modules` -> `test:scripts` (`node --test scripts/*.test.mjs scripts/*.test.ts`) -> `npm run test --workspaces --if-present`.
- **Isolated Test Runner (`./test.sh`)**:
  - Creates an isolated `$test_root` with clean `HOME`, `XDG_*`, `NPM_CONFIG_*`, `GIT_CONFIG_NOSYSTEM=1`, `PI_NO_LOCAL_LLM=1`, `AWS_EC2_METADATA_DISABLED=true`, and runs `env -i ... npm test`.
- **CLI Dev Runner (`./pi-test.sh`)**:
  - Runs `node --import packages/coding-agent/src/experimental/source-resolver.ts packages/coding-agent/src/cli.ts` (supports `--no-env` to unset all provider API keys).
- **TypeScript & Linting (`tsconfig.base.json`, `tsconfig.json`, `biome.json`)**:
  - TypeScript `7.0.2` (`tsc --noEmit`), `target: ES2024`, `module: Node16`/`NodeNext`, `strict: true`, `erasableSyntaxOnly: true` (Node strip-only compatible), `verbatimModuleSyntax: true`, `allowImportingTsExtensions: true`.
  - Biome `2.3.5` (`biome.json`) with custom grit plugin `./scripts/biome/model-type-comparison.grit`, tabs, `lineWidth: 120`.
- **Vitest (`vitest.base.ts`, `packages/coding-agent/package.json`)**:
  - `vitest.base.ts` aliases `@earendil-works/pi-*` workspace imports directly to `packages/*/src/*.ts` so tests run against TypeScript sources without rebuilding.
  - `packages/tui` uses `node --test` instead of Vitest.
- **CI (`.github/workflows/ci.yml`)**:
  - `lint` job: `shellcheck`, `actionlint`, `py_compile`, `VERSION`/`sources.json` consistency, `npm run build:offline`, `biome check`, `tsc --noEmit`.
  - `unit` job: installs `ripgrep` & `fd-find`, `npm run build:offline`, `packages/tui` (`npm test`), `packages/{ai,agent,coding-agent}` (`npx vitest run --retry 2`).
  - `upi` job: fetches runtime (`scripts/fetch-runtime.sh`), runs `scripts/prepare-pi.sh`, `scripts/build-pi.sh`, `tests/pi/run.sh`, `tests/termux/run.sh`, `tests/aot/run.sh`, `tests/runtime/run.sh`.

---

## 2. Logic Chain

1. **Distinguishing PiBolt (the AOT engine/wrapper) from µPi (the Pi fork features)**:
   - Observation 1.2.1 & 1.2.2 show PiBolt provides the AOT Bun/WebKit runtime, `build-pi.sh` / `make-kit.mjs` / `upi compile`, Darwin launcher/spawn proxy, and `PIBOLT` build-time flag.
   - Observation 1.2.3 & 1.2.4 show that `microPi`'s own agent improvements (cached SIMD `fff` grep/find, `libclang` diagnostics, Rust `docsearch`, background jobs, edit improvements, `herdr` integration, prompt tuning) live in `packages/coding-agent` and `packages/docsearch`, though several of them (e.g., native `grep`/`find`, `herdr`, `EARLY_TOOL_NAMES`, `system-prompt.ts` preamble) are currently gated on `PIBOLT !== undefined`.
2. **Porting away from PiBolt requires untangling two coupled surfaces**:
   - **Build/Packaging/CI surface**: Replacing `sources.json` (`webkit`/`bun`), `patches/*.patch`, `scripts/build-runtime.*`, `scripts/build-pi.*`, `scripts/make-kit.mjs`, `scripts/lib/darwin-*`, and `.github/workflows/{runtime,release,ci}.yml` with whatever PiG (`MichaelKinsy/PiG`) uses.
   - **Runtime/TS surface**: `PIBOLT` in `packages/coding-agent/src/upi.ts` is imported by 20+ source files and mocked in 15+ Vitest tests (`settings-pibolt-packages.test.ts`, `upi-*.test.ts`). Removing or replacing `PIBOLT` requires deciding which µPi features (`native` grep/find, `herdr`, `doctor`, `piBolt.packages` overlay, self-update) remain active under PiG vs. are retired (e.g. `upi compile` / `jit-hint.ts` / `bun:ffi` JIT-off handling).
3. **Constraints from `AGENTS.md` & `package.json`**:
   - All TS in `packages/*/src` and `packages/*/test` must obey `erasableSyntaxOnly: true` and `check-runtime-deps.mjs` (no undeclared external imports) and `check-entry-graphs.mjs` (import graph file budgets).
   - `npm run check` must pass after code changes; tests must be run via `./test.sh` or targeted `vitest` / `node --test` invocations (never `npm test` or full `vitest` directly).

---

## 3. Caveats

- `MichaelKinsy/PiG` was not fetched or inspected in this survey (scoped to mapping `microPi`'s PiBolt integration and build/test infra).
- `install.sh` did not match `pibolt|pi-bolt` literally in `rg`, but it shares the `MICROPI_*` / `upi-*` release/installer architecture with `install.ps1` and `npm/install.cjs` and will also need inspection when changing distribution/packaging.
- `packages/coding-agent/src/upi-compile.ts` (2,272 lines) and `packages/coding-agent/src/cli/doctor.ts` (1,000+ lines) were inspected via targeted views and `rg` matches rather than read in full.

---

## 4. Conclusion

`microPi` is deeply integrated with PiBolt at the **build/runtime/distribution layer** (`patches/{bun,webkit}.patch`, `sources.json`, `scripts/*`, `.github/workflows/*`, `install.ps1`, `npm/install.cjs`) and at the **application layer** via the compile-time `PIBOLT` constant in `packages/coding-agent/src/upi.ts` (which gates `upi compile`, `upi doctor`, `piBolt.packages`, `jit-hint`, native `grep`/`find`, `herdr`, and system prompt text). Porting `microPi` to PiG will require:
1. Decoupling µPi's feature gates (native `fff` search, `libclang`, `docsearch`, `herdr`, prompt/tool improvements) from `PIBOLT !== undefined` so they work in the PiG runtime.
2. Replacing or retiring PiBolt-specific AOT machinery (`patches/`, `sources.json`, `scripts/build-runtime.*`, `scripts/build-pi.*`, `make-kit.mjs`, `upi-compile.ts`, `jit-hint.ts`, `darwin-launcher.c`).
3. Updating CI/release/installer/test suites (`ci.yml`, `runtime.yml`, `release.yml`, `upi-*.test.ts`, `settings-pibolt-packages.test.ts`, `tests/{aot,runtime,pi}/`).

---

## 5. Verification Method

- **Reproduce PiBolt file inventory**:
  `rg -il 'pibolt|pi-bolt' --hidden --glob '!.git' --glob '!node_modules' --glob '!.agents' /home/antigravity/Code000-211/microPi | sort`
- **Inspect build/check/test definitions**:
  - `view_file` on `package.json`, `test.sh`, `pi-test.sh`, `vitest.base.ts`, `biome.json`, `tsconfig.base.json`, `sources.json`
  - `view_file` on `packages/coding-agent/src/upi.ts` and `scripts/build-pi.sh`
- **Invalidation conditions**: Any upstream merge or commit on `pi-bolt` that modifies `sources.json`, `scripts/`, `packages/coding-agent/src/upi*.ts`, or `.github/workflows/`.
