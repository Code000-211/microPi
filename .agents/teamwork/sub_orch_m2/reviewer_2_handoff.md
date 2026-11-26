# Reviewer 2 Handoff: Target-Scoped Search & Registration Review

## Verdict: APPROVE

## 1. Files Reviewed
- `internal/codingagent/tools/docsearch.go`:
  - `ProjectSearchTool` implements `agent.AgentTool`, `agent.OutputSchemaProvider`, and `BorrowedSchema()`.
  - `Name()` returns `"project_search"`, `ExecutionMode()` returns `agent.ToolModeParallel`.
  - `Execute` handles `ctx.Err()`, resolves `cwd` via `toolCWD`, defaults `k` from `docsearch.LoadSettings().Results` (clamped to `[1, 20]`), queries `docsearch.SharedEngine(cwd).Search`, handles `!ready` gracefully, and returns `Content`, `StructuredContent` (`{"hits": ...}`), and `Details` (`count`, `micros`, `entities`).
- `internal/codingagent/tools/native_search.go`:
  - Pure-Go `treeCache` (`2s` TTL, `MAX_VERIFIED_DIRS=4096`, `partial` flag, `knownAbsKeys`, `ignoredDirKeys`) and `contentCache` (`64 MB` pool, `512 KB`/file cap, `1.5s` stat-free window, `container/list` LRU eviction, `nil` buffer for binary files, `1 MB` piece streaming for `> 2 MB` files).
  - Exports `ClearNativeSearchCache`, `NotifySearchExternalChange`, `InvalidateSearchCache`, and `NotifySearchFileTouched`.
  - `nativeGrep` supports literal & RE2 regex matching with `requiredLiteral` prefilter, context windows, glob filtering with `!` negation, brace expansion, and `--glob` override over `.gitignore`.
  - `nativeFindDetailed` supports smart-case, path/basename globs, brace expansion, and `fff` fuzzy fallback (`fuzzyFilterPaths`, `scoreFuzzyPathFallback` with 26-bit `asciiLetterMask` prefilter, `lastPossible` bound, and `fuzzyFallbackLimit=20`).
- `internal/codingagent/tools/grep.go`, `find.go`, `details.go`, `tools.go`:
  - `grep.go`: Replaced `rg` subprocess execution with `nativeGrep` while preserving `GrepOperations` (`IsDirectory`, `ReadFile`), `grepFormatter`, truncation, and `GrepDetails`.
  - `find.go`: Replaced `fd` subprocess execution with `nativeFindDetailed` while preserving `FindOperations` (`Exists`, `Glob`), and sets `FindDetails.FuzzyFallback = true` + prefix notice on fuzzy fallback.
  - `details.go`: Added `FuzzyFallback bool \`json:"fuzzyFallback,omitempty"\`` to `FindDetails`.
  - `tools.go`: Registered `project_search` across `builtinToolNames`, `builtinToolDescriptions`, `ToolsOptions`, `CreateProjectSearchTool`, `CreateTool`, `CreateCodingTools`, `CreateReadOnlyTools`, `builtinSchemaTools`, and compile-time interface assertions.
- `internal/codingagent/prompts/sections.go`:
  - Added `project_search` snippet to `DefaultToolSnippets()`.
- `internal/codingagent/slash_commands.go`:
  - Registered `/docs` in `defaultBuiltins()` (`TakesArguments: true`, `Handler: docsHandler`) delegating to `docsearch.HandleDocsCommand`.

## 2. Exclusive File Ownership Check
Verified via `stat` and `grep -rn "NotifySearchFileTouched\|InvalidateSearchCache\|NotifySearchExternalChange" internal/codingagent/`:
- `read.go`, `edit.go`, `write.go`, `internal/codingagent/filetracking/`, and `internal/codingagent/checks/` were NOT modified by M2. Cache invalidation hooks are exported in `native_search.go` and tested in `native_search_test.go` only.

## 3. Non-Blocking Observations
- In `internal/codingagent/tools/native_search.go`, `searchInPieces` (`> 2 MB` streaming path) does not emit context lines when `opts.Context > 0` (acceptable streaming trade-off).
- In `internal/codingagent/tools/docsearch.go`, if `res.Hits` is `nil`, `json.Marshal(map[string]any{"hits": res.Hits})` serializes as `{"hits":null}` rather than `{"hits":[]}`.

## 4. Independent Test Execution Results
1. `CGO_ENABLED=0 go test -count=1 -v ./internal/docsearch/...`
   - Exit 0: `PASS` (`ok github.com/MichaelKinsy/PiG/internal/docsearch 0.036s`)
   - All 5 tests passed: `TestTextTokenizationStemmingAndTranslation`, `TestChunkMarkdownAndAllLanguages`, `TestIndexSearchRankingStoreAndOverlay`, `TestCountFilesAndAutoDisable`, `TestEngineAndDocsCommand`.
2. `CGO_ENABLED=0 go test -count=1 -v ./internal/codingagent/tools -run "TestNativeSearch|TestContentCache|TestNativeGrep|TestNativeFind|TestProjectSearch|TestCreateAllToolsMirrorsUpstreamRegistry|TestCreateReadOnlyToolsReturnsReadGrepFindLs|TestToolsOptionsReachEachTool"`
   - Exit 0: `PASS` (`ok github.com/MichaelKinsy/PiG/internal/codingagent/tools 1.463s`)
   - All 8 targeted tests passed: `TestNativeSearchTreeAndContentCache`, `TestContentCacheEvictionAndBinary`, `TestNativeGrepModesContextGlobOverrideAndLargeFile`, `TestNativeFindAndFuzzyFallback`, `TestProjectSearchToolContract`, `TestCreateAllToolsMirrorsUpstreamRegistry`, `TestCreateReadOnlyToolsReturnsReadGrepFindLs`, `TestToolsOptionsReachEachTool`.
3. `CGO_ENABLED=0 go test -v ./internal/codingagent -run "TestSlash|TestBuiltinSlashCommandsFollowUpstreamOrder"`
   - Exit 0: `PASS` (`ok github.com/MichaelKinsy/PiG/internal/codingagent 0.868s`)
   - All `TestSlash*` and `TestBuiltinSlashCommandsFollowUpstreamOrder` tests passed.
