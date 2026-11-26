# M3 Implementation Worker Handoff

## Summary
Implemented all M3 deliverables in `/home/antigravity/Code000-211/microPi` using Go 1.27.4 (`CGO_ENABLED=0`), preserving compatibility with existing `internal/codingagent/tools` callers and tests:

1. **`internal/codingagent/filetracking/`**:
   - `file_state.go`: `FileStateTracker`, `PathKey`, `TrackedFileState`, `changedSinceRead` (10ms rounding tolerance), `NeedsContentCompare`, `AssertCanModifyExistingFile` (`ErrUnreadFile`, `ErrStaleFile` / `*StaleFileError`, sub-ms content comparison guard when `ContentComplete == true`), `RecordFileRead`, `RecordFileModifiedByAgent`, `HasBeenRead`, and C/C++/HIP local `#include` indexing + unsaved header overlays.
   - `nested_instructions.go`: `NestedInstructions` with candidate priority `["AGENTS.override.md", "AGENTS.md", "AGENTS.MD", "CLAUDE.md", "CLAUDE.MD"]`, BOM stripping, directory caching, root-first traversal strictly below `workspaceCwd`, deduplication via `sentNestedInstructions`, self-read skipping, and `<system-reminder>` formatting.
   - `file_tracker.go`: `Tracker` coordinating `FileStateTracker`, `NestedInstructions`, and `BackgroundChecksHook` (`SetBackgroundChecks`), with `OnFileRead`, `OnFileModified`, `StateTracker`, `WithLocal(false)` remote mode, and `FormatPostEditSnippet`.

2. **`internal/codingagent/tools/argument_aliases.go`**:
   - `RenameArgumentAliases` and `NormalizeToolArguments` (via `orderedjson` + `jsonstringify.Canonicalize`) normalizing Claude Code (`file_path`, `old_string`, `new_string`, `run_in_background`, `-i`, `-C`, `head_limit`) and Qwen Code (`absolute_path`, `include`, `is_background`) parameter names without overwriting an already-present canonical key.
   - Wired `PrepareArguments` and execution-time normalization on `ReadTool`, `WriteTool`, and `EditTool`, plus exported `GrepAliases` and `BashAliases` for `grep`/`bash`.

3. **`internal/codingagent/tools/{edit_diff.go,edit.go,write.go,read.go}`**:
   - `edit_diff.go`: `SplitBOM`, `DetectLineEnding`, `NormalizeToLF`, `RestoreLineEndings`, `NormalizeForFuzzyMatch` (with ASCII fast path), `findTrimmedLineSequence` (`seek_sequence`) + `reindentNewTextForMatchedLines`, `EditNotFoundError`, and `DescribeClosestText` (Dice bigram + exact line anchors, max 20 lines, 200 chars/line, `>=0.5` similarity, 1-indexed `→` line numbers).
   - `edit.go`: Nil-safe `Tracker *filetracking.Tracker` enforcing read-before-edit, allowing unread/disk-changed `edit` when every `oldText` matches uniquely once, preserving CRLF/BOM, recording `OnFileRead`/`OnFileModified`, and appending post-edit 3-line context snippet + nested instructions + closest-text hints on not-found errors.
   - `write.go`: Nil-safe `Tracker *filetracking.Tracker` refusing overwrite of unread existing local files (returning up to 30,000 chars of existing content and marking the file read), verifying `AssertCanModifyExistingFile` (including sub-ms content checks), and calling `OnFileModified`.
   - `read.go`: Nil-safe `Tracker *filetracking.Tracker` recording `OnFileRead` (`ContentComplete = false` for streamed/chunked reads `>= 1 MiB`) and appending nested instruction reminders.

4. **`internal/codingagent/tools/bash_file_writes.go`**:
   - `ParseShellSegments`, `CommandName`, `UnderDir`, `FileWritesInCommand` (single-file `sed -i`, `perl -i`, authored literal redirections/`tee`, ignoring scratch paths and globs), `FilesShownByCat`, `FileWriteRefusal`, and `WatchFiles` / `CheckChangedFiles` with `filetracking.Tracker` notification.

5. **`internal/codingagent/checks/`**:
   - `background_checks.go`: `BackgroundChecks` with `PI_CHECKS` / `PI_CHECKS_WAIT` (30s default), 0ms `OnRead` baseline (not setting `Busy()=true`), 150ms `OnEdited` debounce, overtake/dirty re-run discarding stale results, `WaitForIdle`, `Settle`, `TakeLintReport`, `Dispose`, position-independent `Fingerprint`, `newAgainst`, and 15-diagnostic report cap.
   - `checkers.go` & `cpp_checker.go`: `go` (`go vet`), `rust` (`cargo check`/`clippy` with `CARGO_TARGET_DIR` isolation), `python` (`ty` + `ruff`, fallback `python3` AST), `typescript` (`tsc --noEmit --incremental`), `javascript` (`node --check`), `shell` (`bash -n` + `shuck`), `luau` (`luau-analyze`), `json` (`encoding/json` with line/col), `toml` (`BurntSushi/toml`), `yaml` (`go.yaml.in/yaml/v3`), `js-lint` (`biome`/`oxlint`), `markdown` (`rumdl`), `yaml-lint` (`ryl`), `luau-lint` (`selene`), `cpp-lint` (`clangd-tidy`), `rdna4-isa` (`radisa`), and CGO-free `cpp` (`clang++`/`clang -fsyntax-only` + `CompileCommandsResolver`).

## File Inventory
- `internal/codingagent/filetracking/file_state.go`
- `internal/codingagent/filetracking/nested_instructions.go`
- `internal/codingagent/filetracking/file_tracker.go`
- `internal/codingagent/filetracking/filetracking_test.go`
- `internal/codingagent/checks/background_checks.go`
- `internal/codingagent/checks/checkers.go`
- `internal/codingagent/checks/cpp_checker.go`
- `internal/codingagent/checks/checks_test.go`
- `internal/codingagent/tools/argument_aliases.go`
- `internal/codingagent/tools/argument_aliases_test.go`
- `internal/codingagent/tools/bash_file_writes.go`
- `internal/codingagent/tools/bash_file_writes_test.go`
- `internal/codingagent/tools/edit_diff.go` (updated)
- `internal/codingagent/tools/edit_diff_test.go` (updated)
- `internal/codingagent/tools/edit.go` (updated)
- `internal/codingagent/tools/edit_test.go`
- `internal/codingagent/tools/write.go` (updated)
- `internal/codingagent/tools/write_test.go`
- `internal/codingagent/tools/read.go` (updated)
- `internal/codingagent/tools/read_test.go`

## Verification Commands & Outputs
Note: `go test -race` requires CGO (`go: -race requires cgo; enable cgo by setting CGO_ENABLED=1`), so both `CGO_ENABLED=0` and `-race` runs were executed after the final edit. In `internal/codingagent/tools`, pre-existing Node oracle tests (`extensions/sdk-ts/node_modules`) and `fd`-dependent tests fail in this environment because `sdk-ts/node_modules` and `fd` are not installed; all unit tests for M3 deliverables and existing pure-Go `edit`/`read` tests pass.

1. `CGO_ENABLED=0 go build ./internal/codingagent/... && CGO_ENABLED=0 go vet ./internal/codingagent/...`
   - Exit code: `0` (no output)

2. `CGO_ENABLED=0 go test ./internal/codingagent/filetracking/... ./internal/codingagent/checks/...`
   - Exit code: `0`
   - Output:
     ```
     ok  	github.com/MichaelKinsy/PiG/internal/codingagent/filetracking	0.022s
     ok  	github.com/MichaelKinsy/PiG/internal/codingagent/checks	0.529s
     ```

3. `go test -race ./internal/codingagent/filetracking/... ./internal/codingagent/checks/...`
   - Exit code: `0`
   - Output:
     ```
     ok  	github.com/MichaelKinsy/PiG/internal/codingagent/filetracking	1.051s
     ok  	github.com/MichaelKinsy/PiG/internal/codingagent/checks	1.509s
     ```

4. `go test -race ./internal/codingagent/tools -run 'TestEditToolWithTracker|TestEditToolClosestText|TestWriteToolWithTracker|TestReadToolWithTracker|TestSeekSequence|TestEditDiffHelpers|TestFileWritesInCommand|TestFilesShownByCat|TestFileWriteRefusal|TestRenameArgument|TestNormalizeTool|TestEditPrepareArgumentsKeepsMemberOrder|TestEditUniqueMatch|TestEditNotFound|TestEditMultiple|TestEditOverlap|TestEditCRLF|TestEditBOM|TestEditNoChange|TestReadTruncation|TestReadImage|TestReadGIF|TestReadBinary'`
   - Exit code: `0`
   - Output:
     ```
     ok  	github.com/MichaelKinsy/PiG/internal/codingagent/tools	1.753s
     ```

## Iteration 2 Fixes & Verification
1. **`internal/codingagent/tools/read.go` & `read_test.go`**:
   - Added `limitedCaptureReader` to capture streamed full-file reads (`p.Offset == nil && p.Limit == nil`) up to `1<<20` bytes (1 MiB) when `Tracker != nil`, setting `contentComplete = true` and passing the full decoded text and raw bytes to `t.Tracker.OnFileRead(...)` (triggering `BackgroundChecksHook.OnRead` and enabling sub-ms content comparison). Files `>= 1<<20` discard the capture buffer and keep `contentComplete = false`; partial `offset`/`limit` reads also keep `contentComplete = false`.
   - Added `TestReadToolWithTrackerStreamedMediumFile` in `internal/codingagent/tools/read_test.go` covering a ~10 KB streamed file (`HasBeenRead == true`, `ContentComplete == true`, `BackgroundChecksHook.OnRead` invoked) and a partial `offset`/`limit` read (`ContentComplete == false`).

2. **`internal/codingagent/checks/background_checks.go` & `checks_test.go`**:
   - Updated `ChecksWaitDuration()` so explicit `PI_CHECKS_WAIT=0` returns `0` (while unset, empty, negative, `NaN`, `Inf`, and non-numeric values fall back to `DefaultChecksWait = 30s`).
   - Updated `Settle(ctx, hasQueued)` to call `b.WaitForIdle(ctx, wait)` only when `wait > 0`, returning immediately (and still checking `TakeLintReport()` when `!hasQueued()`) when `wait == 0`.
   - Added `TestChecksWaitZeroDoesNotBlockSettle` in `internal/codingagent/checks/checks_test.go`.

### Iteration 2 Verification Outputs
1. `CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go vet ./internal/codingagent/... && CGO_ENABLED=0 go test -count=1 ./internal/codingagent/filetracking/... ./internal/codingagent/checks/...`
   - Exit code: `0`
   - Output:
     ```
     ok  	github.com/MichaelKinsy/PiG/internal/codingagent/filetracking	0.006s
     ok  	github.com/MichaelKinsy/PiG/internal/codingagent/checks	0.434s
     ```
2. `go test -race -count=1 ./internal/codingagent/filetracking/... ./internal/codingagent/checks/...`
   - Exit code: `0`
   - Output:
     ```
     ok  	github.com/MichaelKinsy/PiG/internal/codingagent/filetracking	1.067s
     ok  	github.com/MichaelKinsy/PiG/internal/codingagent/checks	1.685s
     ```
3. `go test -race -count=1 ./internal/codingagent/tools -run 'TestEditToolWithTracker|TestEditToolClosestText|TestWriteToolWithTracker|TestReadToolWithTracker|TestSeekSequence|TestEditDiffHelpers|TestFileWritesInCommand|TestFilesShownByCat|TestFileWriteRefusal|TestRenameArgument|TestNormalizeTool|TestEditPrepareArgumentsKeepsMemberOrder|TestEditUniqueMatch|TestEditNotFound|TestEditMultiple|TestEditOverlap|TestEditCRLF|TestEditBOM|TestEditNoChange|TestReadTruncation|TestReadImage|TestReadGIF|TestReadBinary'`
   - Exit code: `0`
   - Output:
     ```
     ok  	github.com/MichaelKinsy/PiG/internal/codingagent/tools	2.665s
     ```
