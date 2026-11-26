# Milestone M3 Handoff: File Tracking, Edit Ergonomics, Argument Aliases & Background Checks

**Status**: COMPLETE (Iteration 2 Gate PASS)

## Deliverables Implemented

1. **File Tracking (`internal/codingagent/filetracking/`)**:
   - `file_state.go`: `FileStateTracker`, `PathKey`, `TrackedFileState`, 10ms `changedSinceRead` tolerance, `NeedsContentCompare`, `AssertCanModifyExistingFile` (`ErrUnreadFile`, `ErrStaleFile` / `*StaleFileError`, sub-ms content comparison guard when `ContentComplete == true`), `RecordFileRead`, `RecordFileModifiedByAgent`, `HasBeenRead`, and C/C++/HIP `#include` indexing + unsaved header overlays.
   - `nested_instructions.go`: `NestedInstructions` with priority `["AGENTS.override.md", "AGENTS.md", "AGENTS.MD", "CLAUDE.md", "CLAUDE.MD"]`, BOM stripping, directory caching, root-first traversal strictly below `workspaceCwd`, deduplication, self-read skipping, `<system-reminder>` formatting, and remote disablement.
   - `file_tracker.go`: `Tracker` coordinating `FileStateTracker`, `NestedInstructions`, `BackgroundChecksHook` (`SetBackgroundChecks`), `WithLocal(false)`, `OnFileRead`, `OnFileModified`, and `FormatPostEditSnippet` (3 lines of context).
   - `filetracking_test.go`: unit tests covering all file-tracking & nested-instruction behaviors.

2. **Tool Argument Aliases (`internal/codingagent/tools/argument_aliases.go`)**:
   - `RenameArgumentAliases` and `NormalizeToolArguments` normalizing Claude Code (`file_path`, `old_string`, `new_string`, `run_in_background`, `-i`, `-C`, `head_limit`) and Qwen Code (`absolute_path`, `include`, `is_background`) parameter names with canonical-key precedence.
   - Wired into `ReadTool`, `WriteTool`, and `EditTool` `PrepareArguments`, with `GrepAliases` and `BashAliases` exported for `grep.go` and `bash.go`.
   - `argument_aliases_test.go`: unit tests.

3. **Edit Ergonomics & Tool Integration (`internal/codingagent/tools/{edit_diff.go,edit.go,write.go,read.go}`)**:
   - `edit_diff.go`: `SplitBOM`, `DetectLineEnding`, `NormalizeToLF`, `RestoreLineEndings`, `NormalizeForFuzzyMatch` (with ASCII fast path), `findTrimmedLineSequence` (`seek_sequence`) + `reindentNewTextForMatchedLines`, `EditNotFoundError`, and `DescribeClosestText` (Dice bigram + exact line anchors, max 20 lines, 200 chars/line, `>= 0.5` similarity, 1-indexed `→` line numbers).
   - `edit.go`: Nil-safe `Tracker *filetracking.Tracker` enforcing read-before-edit, allowing unread/disk-changed `edit` when every `oldText` matches uniquely once, preserving CRLF/BOM, recording `OnFileRead`/`OnFileModified`, and appending 3-line context snippet + nested instructions + closest-text hints on miss.
   - `write.go`: Nil-safe `Tracker *filetracking.Tracker` refusing overwrite of unread existing local files (returning up to 30,000 chars of existing content and marking read), verifying `AssertCanModifyExistingFile` (including sub-ms content checks), and calling `OnFileModified`.
   - `read.go`: Nil-safe `Tracker *filetracking.Tracker` recording `OnFileRead` (`ContentComplete = true` for full reads `< 1 MiB` via `limitedCaptureReader`, `ContentComplete = false` for `>= 1 MiB` or partial `offset`/`limit` reads) and appending nested instruction reminders.
   - Tests: `edit_diff_test.go`, `edit_test.go`, `write_test.go`, `read_test.go`.

4. **Bash File-Write Guard (`internal/codingagent/tools/bash_file_writes.go`)**:
   - `ParseShellSegments`, `CommandName`, `UnderDir`, `FileWritesInCommand` (single-file `sed -i`, `perl -i`, authored literal redirections/`tee`, ignoring scratch paths and globs), `FilesShownByCat`, `FileWriteRefusal` (for `/remote` refusal), and `WatchFiles` / `CheckChangedFiles` helper API for M4 `bash.go`.
   - `bash_file_writes_test.go`: unit tests.

5. **Background Compiler Diagnostics & Linters (`internal/codingagent/checks/`)**:
   - `background_checks.go`: `BackgroundChecks` with `PI_CHECKS` (`off`/`0`/`false`, `on`/empty, comma-separated allowlist) and `PI_CHECKS_WAIT` (30s default; `0` returns immediately in `Settle` without blocking), 0ms `OnRead` baseline (`Busy() == false`), 150ms `OnEdited` debounce, overtake/dirty re-run discarding stale results, `WaitForIdle`, `Settle`, `TakeLintReport`, `Dispose`, position-independent `Fingerprint`, `newAgainst`, and 15-diagnostic report cap.
   - `checkers.go` & `cpp_checker.go`: `go` (`go vet`), `rust` (`cargo check`/`clippy` with `CARGO_TARGET_DIR` isolation), `python` (`ty` + `ruff`, fallback `python3` AST), `typescript` (`tsc --noEmit --incremental`), `javascript` (`node --check`), `shell` (`bash -n` + `shuck`), `luau` (`luau-analyze`), `json` (`encoding/json` with line/col), `toml` (`BurntSushi/toml`), `yaml` (`go.yaml.in/yaml/v3`), `js-lint` (`biome`/`oxlint`), `markdown` (`rumdl`), `yaml-lint` (`ryl`), `luau-lint` (`selene`), `cpp-lint` (`clangd-tidy`), `rdna4-isa` (`radisa`), and CGO-free `cpp` (`clang++`/`clang -fsyntax-only` + `CompileCommandsResolver`).
   - `checks_test.go`: unit tests.

## Verification Summary (Iteration 2 — All 5 Verifiers Passed)
- **Reviewer 1 (Full-suite)**: **APPROVE** (`CGO_ENABLED=0 go build ./...` exit 0; `CGO_ENABLED=0 go vet ./internal/codingagent/...` exit 0; `filetracking` & `checks` tests passed under `CGO_ENABLED=0` and `-race`).
- **Reviewer 2 (Target-scoped)**: **APPROVE** (M3 tool tests in `internal/codingagent/tools` passed under `CGO_ENABLED=0` and `-race`).
- **Challenger 1**: **PASS** (empirical verification of streamed medium files, `>= 1 MiB` cutoff, partial `offset`/`limit`, CRLF/BOM `seek_sequence`, unread `write` 30k limit).
- **Challenger 2**: **PASS** (empirical verification of `PI_CHECKS_WAIT=0` non-blocking `Settle`, invalid/negative fallbacks, `0.05`s timeout, `-race` overtake/dirty re-run, `TakeLintReport()`).
- **Forensic Auditor**: **CLEAN** (file ownership verified — no touches to `grep.go`, `find.go`, `internal/docsearch/`, `bash.go`, `go.mod`, `tools_test.go`; 0 stubs/TODOs/skips/CGO; independent `go build`/`go vet`/`go test` passed).

## Integration Notes for Downstream Milestones (M4 / M5)
- `ReadTool`, `WriteTool`, and `EditTool` expose `Tracker *filetracking.Tracker` (nil-safe for standalone unit tests; wire a session-scoped `*filetracking.Tracker` with `checks.NewBackgroundChecks(...)` attached via `SetBackgroundChecks` in `coding/session_tool_registry.go` / `tools.go`).
- `bash.go` (M4) can use `tools.BashAliases`, `tools.FileWritesInCommand`, `tools.FilesShownByCat`, `tools.FileWriteRefusal` (on `/remote`), and `tools.WatchFiles` / `tools.CheckChangedFiles`.
