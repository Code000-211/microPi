# Handoff Report: Step 0 Survey — PiG Specification & Architecture

## 1. Observation

All observations below were directly collected from `https://github.com/MichaelKinsy/PiG` (default branch `main`, MIT License) and compared against `/home/antigravity/Code000-211/microPi`.

### 1.1 Identity, Versions, and Toolchain
- **Repository**: `https://github.com/MichaelKinsy/PiG`
- **Go Module**: `github.com/MichaelKinsy/PiG` (`go.mod:1`)
- **Go Version / Toolchain**: `go 1.27.0`, `toolchain go1.27.4` (`go.mod:3,9`, `.go-version:1`, `go.work:1,4`)
- **Godebug**: `godebug winsymlink=0` (`go.mod:12`, `go.work:7`) to match Node.js Windows junction/symlink behavior
- **Go Workspace (`go.work:9-14`)**:
  - `.`
  - `./extensions/sdk` (`github.com/MichaelKinsy/PiG/extensions/sdk v0.8.0`)
  - `./piglets/pig-bolt`
  - `./piglets/porter/extensions/pig-porter`
- **Version Pins (`.upstream/pin:7-10`, `internal/coding/pigversion/pigversion.go:9-26`, `coding/upstream.go:12-43`)**:
  - `PigVersion = "0.8.0"`
  - `UpstreamRef = "v1.6.0"`
  - `UpstreamCommit = "faac585884ae2c013a6b1400f3e9e77f10b34bac"`
  - `UpstreamVersion = "1.6.0"`
  - `UpstreamPackageVersion = "1.6.0"`
  - `Version = "0.8.0+1.6.0"`
  - `UpstreamReviewedVersion = "0.84.0"`
- **Build & Runtime**: Static Go binary (`CGO_ENABLED=0`, `Makefile:163,187`) built from `./cmd/pig` (`cmd/pig/main.go`). No Node.js is required for the core `pig` binary; Node.js >=22.13 (or Bun / `pig-bolt` AOT) is only needed to run TypeScript/JavaScript extensions out-of-process.
- **Configuration & Storage Paths (`README.md:131-132`, `coding/doc.go:56`, `coding/cli/main.go:3-5`)**:
  - Binary: `pig`
  - Config root: `~/.pig/` (override: `PIG_HOME`)
  - Agent dir: `~/.pig/agent` (override: `PIG_CODING_AGENT_DIR`)
  - Sessions: `<CWD>/.pig/sessions/<id>.jsonl` / `~/.pig/agent/sessions/`

### 1.2 Package & Directory Layout
- **Core Public Go Packages** (aligned with `pi-mono` packages, `README.md:142-156`, `docs/parity/PORT_MAP.md:27-32`):
  - `agent/` — Agent loop (`agent.go`, `agent_loop.go`), types (`types.go`: `AgentTurnContext`, `FinishTurn`, `PrepareNextTurn`, `PrepareRequest`), message handling, tool execution (`tool_execution.go`), proxy (`proxy.go`).
  - `ai/` — Providers (`anthropic.go`, `openai.go`, `openai_responses.go`, `google.go`, `google_vertex.go`, `mistral.go`, `githubcopilot.go`, `bedrock` via AWS SDK v2), `API` constants (`types.go:54-65`), model catalog (`models_generated.go`), OAuth flows (`oauth_*.go`), auth store (`auth_store.go`), streaming (`event_stream.go`, `sse.go`), cache warming (`cache_warming.go`).
  - `coding/` — Public coding-agent SDK (`doc.go`: `Services`, `Runtime`, `Session`, `CreateAgentSessionServices`, `NewRuntime`, `BuildModel`, `SessionStartOptions`), CLI (`coding/cli/`), extension contracts (`coding/extension/`), Piglet support (`coding/piglet/`, `coding/pigletbuild/`), plugin UI views (`coding/uiview/`).
  - `tui/` — Terminal UI engine (`tui.go`: `Component`, `InputHandler`, `KeyReleaseReceiver`, `Disposable`), differential renderer, alt-screen (`tui_alt_screen.go`), markdown/syntax highlighting (`markdown.go`, `highlight.go`), keybindings (`keybindings.go`), Kitty keyboard/image protocols, overlays, editors, selectors.
- **Additional Public Go Packages (`docs/parity/PORT_MAP.md:28-32`)**:
  - `chord/` — Application-composition runtime.
  - `codemode/` — Sandboxed code-execution runtime.
  - `durable/` — Detached durable Session runtime.
  - `env/` — Remote execution environment (`pi-env` daemon via `env/cmd/pi-env`, `Makefile:196-204`).
  - `mcp/` — Model Context Protocol client library.
  - `schema/` — Eval-free JSON Schema validator (drafts 2020-12 and 7).
  - `telemetry/` — Standalone telemetry library.
- **Private Implementation (`internal/`)**: 128 subpackages (`internal/codingagent/` with 1,893 files, `internal/codingagent/tools/` with `read.go`, `write.go`, `edit.go`, `bash.go`, `grep.go`, `find.go`, `ls.go`, `powershell.go`, `mutation_queue.go`, `tools_manager.go`, `internal/codingagent/prompts/coding.go`, `internal/codingagent/compaction/compaction.go`, `internal/codingagent/cache_warmer.go`, `internal/codingagent/interactive.go`, `internal/modelgen/`, etc.). External Go modules cannot import `internal/`.
- **Extension SDKs (`extensions/`)**:
  - `extensions/sdk/` — Go extension SDK (`github.com/MichaelKinsy/PiG/extensions/sdk v0.8.0`)
  - `extensions/sdk-rs/` — Rust extension SDK
  - `extensions/sdk-py/` — Python extension SDK
  - `extensions/sdk-lisp/` — Common Lisp (SBCL) extension SDK
  - `extensions/sdk-ts/` — Declaration-only TypeScript types (`index.d.ts`, `pi-pin.d.ts`)
- **Piglets (`piglets/`)**:
  - `piglets/standard/pig-standard.yaml` — PiG Standard composition (PiG login/sprite, PiG Runner, Angry Pigs, Bun runtime installer).
  - `piglets/pig-bolt/` — Experimental Piglet (`boltruntime` Go extension, `/bolt-runtime`) that compiles session TS extensions using **Pi-Bolt 1.1.2** Bun AOT compiler (`piglets/pig-bolt/README.md:5-10`).
  - `piglets/porter/` — Parity porting workbench.
- **CLI Entry Point & Modes (`cmd/pig/main.go`, `coding/cli/main.go`, `coding/cli/args.go`, `coding/cli/help_upstream.txt`)**:
  - Entry: `cmd/pig/main.go` -> `cli.Main(os.Args[1:], nil)`
  - Modes: Interactive TUI (fullscreen/regular), `--print` / `-p`, `--mode text|json|rpc`, `--mode rpc --listen <unix-socket-path>` (PiG-only multi-session RPC server, D232).
  - Subcommands: `install`, `remove`, `uninstall`, `update`, `list`, `config`, `auth`, `mcp`, `version`, `setup`, `extension init`, `piglet build`.
- **Parity Status (`test/parity/generated/coverage.md:7-33`)**:
  - Whole monorepo: 1046 / 1049 intended-portable files ported (99.7%).
  - Four core packages (`agent`, `ai`, `coding-agent`, `tui`): 536 / 537 (99.8%).

### 1.3 Key Dependencies (`go.mod:14-51`)
- `github.com/aws/aws-sdk-go-v2` (Bedrock), `github.com/evanw/esbuild v0.28.2`, `github.com/tetratelabs/wazero v1.12.0`, `modernc.org/sqlite v1.62.0`, `github.com/yuin/goldmark v1.8.5`, `github.com/alecthomas/chroma/v2 v2.27.0`, `github.com/gorilla/websocket`, `github.com/fsnotify/fsnotify`, `github.com/gofrs/flock`, `github.com/charmbracelet/x/ansi`, `golang.org/x/term`, `golang.org/x/sys`, `golang.org/x/net`, `golang.org/x/oauth2`.

---

## Features Discovered

| # | Category | Feature | Description | Inputs | Outputs | Error Behavior | Discovered Via |
|---|---|---|---|---|---|---|---|
| 1 | Runtime / Binary | Native Go CLI (`pig`) | Single static `CGO_ENABLED=0` Go binary porting `pi-coding-agent` v1.6.0; no Node.js required for core | CLI flags/args (`Args` in `coding/cli/args.go`), env vars (`PIG_HOME`, `PIG_CODING_AGENT_DIR`), stdin | Interactive TUI, `--print` text, `--mode json`, `--mode rpc` | Non-zero exit code + stderr / JSON error; terminal state restored via `defer tui.RestoreTerminalOnExit()` | `cmd/pig/main.go`, `coding/cli/main.go`, `Makefile:161-189` |
| 2 | Multi-Session RPC | `pig --mode rpc --listen <path>` | Serves one RPC session per Unix/AF_UNIX socket connection (mode 0600, no auth); optional `--listen-share-extensions` | `--mode rpc --listen <socket-path>` | JSON-RPC lines per connection | Rejects `--continue`, `--resume`, `--session`, `--session-id`, `--fork`, existing socket, or path >104/108 bytes | `README.md:78-88`, `coding/cli/args.go:140-143`, `coding/cli/rpc_listen.go` |
| 3 | Public Go SDK | `coding` package embedder API | Go library (`Services`, `Runtime`, `Session`) for embedding PiG with custom `ExtraTools`, `DispatchSlash`, `Events()`, `Fork`, `Clone` | `CreateAgentSessionServicesOptions`, `RuntimeOptions`, `SessionStartOptions`, `context.Context` | `*Session`, `[]agent.AgentMessage`, streaming `<-chan agent.AgentEvent` (requires `AcknowledgeEvent`) | Failed model calls return `StopReason: "error"` on last assistant message rather than `Send` error | `coding/doc.go`, `coding/create_agent_session.go`, `coding/runtime.go`, `coding/session.go` |
| 4 | Extension Host | Out-of-process multi-language extensions | Subprocess socket IPC (`coding/extension/host/subprocess/protocol.go`) supporting Go, Rust, Python, Common Lisp (SBCL), and Node.js/Bun TS extensions; isolated or packed cells (`cellpack`, `fusepack`) | Extension path / `-e` / `builtin:<name>` / package settings | Registered tools, commands, shortcuts, flags, event handlers, UI mounts (`uiview`) | `extension_unresponsive` on missed heartbeat `pong`; per-member failure isolation in packed cells | `docs/extension-api-parity.md:13-73`, `coding/extension/api.go`, `coding/extension/host/` |
| 5 | Built-in Tools | `read`, `write`, `edit`, `bash`, `grep`, `find`, `ls`, `powershell` | Ported 1:1 from `pi-coding-agent`: `edit` uses exact text replacement (`FileMutationQueue`), `bash` is one-shot spawn, `grep` shells out to `rg`, `find` shells out to `fd` | JSON tool parameters per tool schema | `agent.AgentToolResult` (text/image content, truncation at 50KB / 2000 lines, details) | `IsError: true` with message (e.g., `Command exited with code N`, `Command aborted`, exact match failure) | `internal/codingagent/tools/*.go` |
| 6 | Piglets | Explicit agent compositions | YAML-defined bundles (`piglets/standard`, `piglets/pig-bolt`, `piglets/porter`) selecting resources, tools, discovery, and defaults; can be built into standalone `Piglet Binary` (`pig piglet build`) | `--piglet <path.yaml>` | Configured `pig` session or standalone signed `Piglet Binary` | Fails verification (`binarypiglet.Verify()`) on signature/closure mismatch | `README.md:163-183`, `piglets/README.md`, `coding/cli/main.go:744-760` |
| 7 | TS Runtime Slot | `extensionsTypescriptRuntime` / `PIG_TS_RUNTIME` | Selects Node.js (default), Bun, or `aot` (`pig-bolt` using Pi-Bolt 1.1.2 Bun AOT compiler) for TS extensions | `"extensionsTypescriptRuntime": "aot"` or `PIG_TS_RUNTIME=aot` | Compiled extension executable or fallback to Bun/Node | Falls back to Bun/Node if source changes, UTF-8 invalid, or `import.meta` used as value | `piglets/pig-bolt/README.md` |
| 8 | Providers & Auth | `ai` package providers & OAuth | Anthropic, OpenAI (Completions/Responses/Codex), Azure, Google (GenerativeAI/Vertex), Mistral, Bedrock, GitHub Copilot, OpenRouter, etc.; file-locked `auth.json` | Model IDs, API keys / OAuth flows, `ai.StreamOptions` | `EventStream` of assistant deltas, tool calls, usage, cost | Retry classifier (`assistant_retry.go`, `provider_retry.go`), overflow detection (`overflow.go`) | `ai/*.go`, `ai/types.go` |
| 9 | Cache & Compaction | `cache_warmer.go` & `compaction/` | Upstream `cache-warmer.ts` and `compaction.ts` ported to Go with extension hooks (`session_before_compact`, `cache_warming_decision`) | Branch entries, token usage, settings | Background warm requests, compaction summary entries | `session_compact_failed` event, cancellation via `AbortSignal` / `context.Context` | `internal/codingagent/cache_warmer.go`, `internal/codingagent/compaction/compaction.go` |

## Edge Cases

| # | Feature | Input | Observed Behavior |
|---|---|---|---|
| 1 | `pig --version` vs `pig version` | `pig --version` / `pig -v` vs `pig version` | `pig --version` prints only `1.6.0` (pinned Pi package version) for launcher compatibility; `pig version` prints both `pig: 0.8.0` and `upstream pi: 1.6.0` (`README.md:138`, `coding/cli/main.go:128-144`). |
| 2 | Extension API in-process vs subprocess | TypeScript/Go/Rust/Python/Lisp extensions | PiG **forbids** in-process JS/WASM/Go-plugin extensions (`docs/extension-api-parity.md:15`); all extensions communicate over Unix socket IPC (`coding/extension/host/subprocess/protocol.go`), except fused Go extensions (`fusepack`) or internal `builtin` tools. |
| 3 | `internal/` package visibility | External Go module importing `internal/codingagent` | Disallowed by Go compiler (`internal/` boundary). Customizing built-in tools, prompts, or compaction requires either modifying PiG's own tree (forking PiG) or using `coding.SessionStartOptions.ExtraTools` / `--no-builtin-tools` / extension hooks. |
| 4 | Windows symlinks/junctions | Directory junctions on Windows | `godebug winsymlink=0` is required in both `go.mod` and `go.work` so Go classifies Windows directory junctions as symlinks like Node.js (`go.mod:11-12`). |
| 5 | `go install` module size & dual tags | `go install github.com/MichaelKinsy/PiG/cmd/pig@latest` | Releases must tag both root (`vX.Y.Z`) and `extensions/sdk/vX.Y.Z` on the same commit; `v0.5.0` was retracted for exceeding Go module zip size limits (`go.mod:5-6`, `README.md:230-232`). |

---

## 2. Logic Chain

1. **PiG vs `pi` / `pi-mono`**: `microPi` is a TypeScript/Bun fork of `earendil-works/pi` (`v1.4.0`, `microPi/sources.json:15-16`) compiled via **Pi-Bolt** (patched Bun + JavaScriptCore AOT). **PiG** (`0.8.0+1.6.0`) is a ground-up, parity-bound Go port of `earendil-works/pi` (`v1.6.0`) that compiles to a single static Go binary (`./cmd/pig`, `CGO_ENABLED=0`) without Bun or Node.js in the core runtime (`README.md:25-27`, `Makefile:163`).
2. **PiG vs Pi-Bolt**: PiG is *not* based on Pi-Bolt; in fact, PiG's `piglets/pig-bolt/` is an optional, experimental Piglet that invokes Pi-Bolt 1.1.2 *only* to AOT-compile third-party TypeScript extensions into a subprocess host (`piglets/pig-bolt/README.md:1-9`). Porting `microPi` to `PiG` instead of `PiBolt` means moving from a **TypeScript + patched Bun/JSC AOT + Rust N-API/FFI (`docsearch`) + C SIMD** stack to a **Go 1.27 (`CGO_ENABLED=0`)** stack.
3. **Implications for porting `microPi` features to PiG**:
   - **Built-in tools (`edit`, `bash`, `grep`, `find`)**: In `microPi`, these are TypeScript + native SIMD C (`grep`/`find`) + background job hub (`bash`) + background language checkers (`edit`). In PiG, built-in tools live in `internal/codingagent/tools/` (`edit.go`, `bash.go`, `grep.go`, `find.go`, `tools_manager.go`). Porting `microPi`'s tool enhancements into PiG's core means modifying `internal/codingagent/tools/` (Go) or overriding them via `coding.SessionStartOptions.ExtraTools` / `--no-builtin-tools` + Go extension.
   - **Rust `docsearch` (`project_search`)**: `microPi` embeds `packages/docsearch` (Rust). PiG builds with `CGO_ENABLED=0` (`Makefile:163,187`). To integrate `docsearch` into PiG without breaking static `CGO_ENABLED=0` builds, it must either be: (a) ported to pure Go, (b) invoked as a subprocess/sidecar binary (matching PiG's subprocess architecture, e.g., like `rg`/`fd` or a Rust extension via `extensions/sdk-rs`), or (c) compiled to WASM via `wazero` (already in `go.mod:38`).
   - **Prompt, Compaction & Cache-Warming**: PiG already has `internal/codingagent/prompts/coding.go`, `internal/codingagent/compaction/compaction.go`, and `internal/codingagent/cache_warmer.go` (porting upstream `cache-warmer.ts`). `microPi`'s smaller system prompt, project listing injection, and cache-warmed ahead-of-time compaction would target those Go files (or extension hooks `OnBeforeAgentStart`, `OnSessionBeforeCompact`, `OnCacheWarmingDecision`).
   - **TUI & Keybindings**: PiG's `tui/` and `internal/codingagent/interactive*.go` / `keybindings.go` replace `packages/tui` and `packages/coding-agent` interactive mode (e.g., for `microPi`'s job hub `alt+j` or `/remote`).
   - **Verification & Parity Gates**: PiG enforces strict CI/Makefile gates (`make check`, `make verify`, `make port-map-drift`, `test/parity/`) against `.upstream/pin` (`v1.6.0`). Any intentional divergence from upstream Pi must be recorded in `docs/parity/divergences/D<N>.md` (or `docs/additive-features.md`) if keeping PiG's parity gates green.

## 3. Caveats

- This survey inspected PiG's repository structure, build configuration, documentation, and public/internal Go source files at `main` (`0.8.0+1.6.0`); `make check` / `go test` were not executed on PiG because this is a read-only survey.
- Only top-level `microPi` files (`README.md`, `package.json`, `sources.json`, `packages/`) were inspected for lineage comparison; a full feature-by-feature diff of `microPi` vs upstream Pi `v1.4.0` was out of scope for this PiG survey.

## 4. Conclusion

`MichaelKinsy/PiG` (`0.8.0+1.6.0`) is a comprehensive, parity-bound Go 1.27 (`CGO_ENABLED=0`) port of `earendil-works/pi` `v1.6.0` with 99.8% file coverage across the four core packages (`agent/`, `ai/`, `coding/`, `tui/`). Porting `microPi` to `PiG` instead of `PiBolt` replaces the TypeScript + patched Bun/JSC AOT runtime with a native Go binary (`./cmd/pig`) and Go SDK (`coding`), while requiring `microPi`'s TypeScript/C/Rust enhancements (background bash job hub, post-edit compiler diagnostics, enhanced `edit`, in-memory SIMD `grep`/`find`, `docsearch` / `project_search`, prompt/compaction optimizations, `/remote`) to be implemented either in Go under `internal/codingagent/` (forking PiG) or as PiG Go/Rust extensions (`extensions/sdk`, `extensions/sdk-rs`) / `wazero` WASM / sidecar subprocesses.

## 5. Verification Method

To independently verify every claim in this report:
1. Clone `https://github.com/MichaelKinsy/PiG` (`git clone --depth 1 https://github.com/MichaelKinsy/PiG.git`) and inspect:
   - `.upstream/pin` and `internal/coding/pigversion/pigversion.go` for version pins (`0.8.0+1.6.0`).
   - `go.mod`, `go.work`, `.go-version`, and `Makefile` for Go `1.27.4`, `CGO_ENABLED=0`, `godebug winsymlink=0`, and `wazero` dependency.
   - `cmd/pig/main.go`, `coding/doc.go`, `coding/cli/args.go`, `coding/extension/api.go`, `agent/types.go`, `ai/types.go`, `tui/tui.go` for exact package names and public APIs.
   - `internal/codingagent/tools/{tools.go,edit.go,bash.go,grep.go,find.go}`, `internal/codingagent/prompts/coding.go`, and `internal/codingagent/cache_warmer.go` for built-in tool, prompt, and cache-warming implementations.
   - `piglets/pig-bolt/README.md` for PiG's relationship to Pi-Bolt.
   - `test/parity/generated/coverage.md` for parity coverage counts.
2. Any change to `MichaelKinsy/PiG`'s `main` branch after `2026-11-26` (such as a `PigVersion` or `.upstream/pin` bump) invalidates the exact version numbers and file counts recorded here.
