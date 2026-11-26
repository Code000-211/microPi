# Worker 1 Handoff: M2 (`internal/docsearch`, `project_search`, `/docs`, Cached `grep`/`find` + `fff` Fuzzy Fallback)

## 1. Files Created & Modified

### Created
- `internal/docsearch/lexicon.go` — Static `deEnLexicon` (~176 German→English technical term pairs).
- `internal/docsearch/text.go` — `RawWords`, `Terms`, `Stem`, `CamelParts`, `IsEntityWord`, `Entities`, `EntityAlias`, `WithTranslations`, `IsCodeExt`, `IsCodePath`, and 42 EN + 47 DE stopwords.
- `internal/docsearch/chunk.go` — Markdown heading-stack splitter (`MaxChars=1600`, `TargetChars=1100`, fence-aware) and definition-aware `SplitCode` for C/C++/HIP/CUDA, Rust, Python, JS/TS, Go, and Shell (with attached comment/attribute lines and nested `"Outer › Inner"` header merging).
- `internal/docsearch/walk.go` — Pure-Go file walker (`Scan`, `CountFiles`, `ResolveRoot`) using `internal/ignorerules` + `filepath.WalkDir` (respects `.gitignore`/`.ignore`, skips `.git`/`node_modules`/`target`, skips binary files with NUL in first 8192 bytes, `DefaultMaxFileBytes=4 MiB`, `DefaultMaxFiles=50_000`, zero external binaries).
- `internal/docsearch/store.go`, `mmap_unix.go`, `mmap_other.go` — Binary format (`DSINDEX\x02`, `FORMAT=3`, 8-byte aligned tables, `StrMap` with 64-bit Murmur3-finalized hash, `golang.org/x/sys/unix` mmap on Unix + fallback on non-Unix, `.ok` validation stamp).
- `internal/docsearch/index.go` — `Config`, `Index`, `Overlay`, `SearchOpts`, `Hit`, `Results`. BM25 (`K1=1.2`, `B=0.75`, `B_CODE=0.5`, `HEADING_BOOST=2`, non-negative IDF, coordination factor), bipartite section-entity + adjacent-section graph with hub pruning (`max(ns/40, 30)`), Andersen-Chung-Lang forward-push PPR (`ALPHA=0.5`, `EPS=1e-4`, `MAX_PUSHES=200_000`), E2GraphRAG co-occurrence (`+0.25*(c-1)`), go-to-definition boost (`+1.0`), heading term boost (`+0.3`), `sync.Pool` scratch buffers, parallel `Build`, `Diff`, `BuildOverlay`, `SearchCombined`.
- `internal/docsearch/engine.go` — `Engine` & `View` with background initial build/mmap load, `MaybeRefresh` (1s / `5*buildMs` throttle), `Overlay` incremental updates, quiet-time (`30s`) / threshold (`20` files or `10m`, immediate `>200`) background fold/rebuild under `github.com/gofrs/flock`, 256-entry generation-keyed result cache, 30-day cache pruning, `PI_DOCSEARCH=off`, `docsearch.json` settings, `NewTool(cwd)`, `NewToolForce(cwd)`, `HandleDocsCommand(args, cwd)`, `FormatToolResult`.
- `internal/docsearch/docsearch_test.go` — Unit tests for tokenization/stemming/DE-EN, chunking across all 6 code language families + markdown, BM25 + PPR + definition boost + co-occurrence, `Overlay` add/update/delete, store `Save`/mmap `Load`/`.ok`/corrupt rejection, `CountFiles` `<=150` auto-disable, `Engine`, and `/docs` commands.
- `internal/codingagent/tools/docsearch.go` — `ProjectSearchTool` (`agent.AgentTool` + `agent.OutputSchemaProvider` + `BorrowedSchema()`, `Name() == "project_search"`, `ExecutionMode() == agent.ToolModeParallel`, `structuredContent.hits`).
- `internal/codingagent/tools/native_search.go` — Pure-Go `treeCache` (2s TTL + `MAX_VERIFIED_DIRS=4096` mtime revalidation, `partial` flag, `knownAbsKeys`, `ignoredDirKeys`), `contentCache` (`64 MB` pool, `512 KB`/file cap, `1.5s` stat-free window, `container/list` eviction, `nil` for binary files, `1 MB` piece streaming for `>2 MB`), literal & RE2 `regexp` matching with `requiredLiteral` prefilter, `fff` fuzzy fallback (`fuzzyFilterPaths`, `scoreFuzzyPathFallback` with 26-bit mask prefilter & `lastPossible` bound, `FUZZY_FALLBACK_LIMIT=20`), and exported `InvalidateSearchCache`, `NotifySearchFileTouched`, `NotifySearchExternalChange`, `ClearNativeSearchCache`.
- `internal/codingagent/tools/native_search_test.go` — Unit tests for `treeCache`, `contentCache` eviction & binary detection, `InvalidateSearchCache`, `grep` (literal, regex, context, glob override over `.gitignore`, `>2 MB` streaming), `find` + `fff` fuzzy fallback, and `ProjectSearchTool`.

### Modified
- `internal/codingagent/tools/details.go` — Added `FuzzyFallback bool` to `FindDetails`.
- `internal/codingagent/tools/grep.go` — Replaced `rg` subprocess execution with pure-Go `nativeGrep` (preserving `Operations.IsDirectory` & `Operations.ReadFile`, exact output/truncation/notice formats).
- `internal/codingagent/tools/find.go` — Replaced `fd` subprocess execution with pure-Go `nativeFindDetailed` + `fff` fuzzy fallback (preserving `Operations.Glob` fallback).
- `internal/codingagent/tools/tools.go` — Registered `project_search` in `builtinToolNames`, `builtinToolDescriptions`, `ToolsOptions`, `CreateProjectSearchTool`, `CreateTool`, `CreateCodingTools`, `CreateReadOnlyTools`, `builtinSchemaTools`, and interface assertions.
- `internal/codingagent/prompts/sections.go` — Added `project_search` snippet to `DefaultToolSnippets()`.
- `internal/codingagent/slash_commands.go` — Registered `/docs` in `defaultBuiltins()` (`docsHandler` calling `docsearch.HandleDocsCommand`).
- `internal/pigstrip/ids_generated.go` & `coding/piglet/piglet.schema.json` — Regenerated via `go run ./coding/piglet/internal/genstripids`.
- `internal/codingagent/tools/powershell_test.go`, `read_only_tools_test.go`, `tool_options_test.go`, `internal/codingagent/slash_commands_test.go`, `internal/codingagent/strip_state_helpers_test.go` — Updated expected built-in tool/slash-command lists for `project_search` and `/docs`.

M3-owned files (`read.go`, `edit.go`, `write.go`, `filetracking/`, `checks/`) were NOT modified.

## 2. Verification Results (Run After Final Edit)
- `CGO_ENABLED=0 go build ./...` — **PASS** (exit 0)
- `CGO_ENABLED=0 go build ./cmd/upi` — **PASS** (exit 0)
- `CGO_ENABLED=0 go run ./coding/piglet/internal/genstripids -check` — **PASS** (exit 0)
- `CGO_ENABLED=0 go test ./internal/docsearch/...` — **PASS** (`ok github.com/MichaelKinsy/PiG/internal/docsearch`)
- `CGO_ENABLED=0 go test ./internal/codingagent/tools -run "TestNativeSearch|TestContentCache|TestNativeGrep|TestNativeFind|TestProjectSearch|TestCreateAllToolsMirrorsUpstreamRegistry|TestCreateReadOnlyToolsReturnsReadGrepFindLs|TestToolsOptionsReachEachTool"` — **PASS** (`ok github.com/MichaelKinsy/PiG/internal/codingagent/tools 1.154s`)
- `CGO_ENABLED=0 go test ./internal/codingagent -run "TestSlash|TestBuiltinSlashCommandsFollowUpstreamOrder"` — **PASS** (`ok github.com/MichaelKinsy/PiG/internal/codingagent 0.584s`)

Note: Pre-existing Node.js oracle tests in `internal/codingagent/tools` / `internal/codingagent` that require `extensions/sdk-ts/node_modules` or an external `fd` binary on `PATH` (plus subprocess-spawn-specific tests like `TestSearchAndShellToolsReportSpawnArgumentErrorsAsPiDoes`) fail in this pure-Go/offline environment for those external dependencies.
