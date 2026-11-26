# Reviewer 2 (Target-Scoped Tools Reviewer) — M3 Handoff

## Verdict: **REJECT**

## 1. Assigned Test Commands & Exact Outputs

### Command 1: `CGO_ENABLED=0`
```
CGO_ENABLED=0 go test ./internal/codingagent/tools -run 'TestEdit|TestWrite|TestRead|TestSeek|TestFileWrites|TestFilesShown|TestFileWriteRefusal|TestRenameArgument|TestNormalizeTool'
```
*(Run with `-v`; exited with code `1` after `23.790s`.)*

Exact failure output:
```
--- FAIL: TestEditFuzzyMatchingUpstreamCases (0.07s)
    --- FAIL: TestEditFuzzyMatchingUpstreamCases/should_preserve_the_correct_occurrence_when_fuzzy_replacement_equals_a_nearby_line (0.00s)
        edit_fuzzy_test.go:107: lstat ../../../extensions/sdk-ts/node_modules: no such file or directory
    --- FAIL: TestEditFuzzyMatchingUpstreamCases/should_preserve_untouched_lines_and_produce_an_applicable_patch_for_fuzzy_multi-edits (0.00s)
        edit_fuzzy_test.go:107: lstat ../../../extensions/sdk-ts/node_modules: no such file or directory
--- FAIL: TestEditToolMatchesPi (0.57s)
    edit_tool_oracle_test.go:177: Pi oracle: exit status 1
        node:fs:2801
              const stats = binding.lstat(base, true, undefined, true /* throwIfNoEntry */);
                                    ^
        
        Error: ENOENT: no such file or directory, lstat '/home/antigravity/Code000-211/microPi/extensions/sdk-ts/node_modules'
...
--- FAIL: TestEditToolMatchesPiOverSeededFiles (0.46s)
    edit_tool_seeded_oracle_test.go:95: Pi oracle: exit status 1
        node:fs:2801
              const stats = binding.lstat(base, true, undefined, true /* throwIfNoEntry */);
                                    ^
        
        Error: ENOENT: no such file or directory, lstat '/home/antigravity/Code000-211/microPi/extensions/sdk-ts/node_modules'
...
FAIL
FAIL	github.com/MichaelKinsy/PiG/internal/codingagent/tools	23.790s
FAIL
```

### Command 2: `-race`
```
go test -race ./internal/codingagent/tools -run 'TestEdit|TestWrite|TestRead|TestSeek|TestFileWrites|TestFilesShown|TestFileWriteRefusal|TestRenameArgument|TestNormalizeTool'
```
*(Exited with code `1` after `173.629s`; no data races detected.)*

Exact output:
```
--- FAIL: TestEditFuzzyMatchingUpstreamCases (0.11s)
    --- FAIL: TestEditFuzzyMatchingUpstreamCases/should_preserve_the_correct_occurrence_when_fuzzy_replacement_equals_a_nearby_line (0.02s)
        edit_fuzzy_test.go:107: lstat ../../../extensions/sdk-ts/node_modules: no such file or directory
    --- FAIL: TestEditFuzzyMatchingUpstreamCases/should_preserve_untouched_lines_and_produce_an_applicable_patch_for_fuzzy_multi-edits (0.01s)
        edit_fuzzy_test.go:107: lstat ../../../extensions/sdk-ts/node_modules: no such file or directory
--- FAIL: TestEditToolMatchesPi (1.43s)
    edit_tool_oracle_test.go:177: Pi oracle: exit status 1
        node:fs:2801
              const stats = binding.lstat(base, true, undefined, true /* throwIfNoEntry */);
                                    ^
        
        Error: ENOENT: no such file or directory, lstat '/home/antigravity/Code000-211/microPi/extensions/sdk-ts/node_modules'
            at realpathSync (node:fs:2801:29)
            at file:///home/antigravity/Code000-211/microPi/internal/codingagent/tools/testdata/edit_tool.mjs:4:14
            at ModuleJob.run (node:internal/modules/esm/module_job:343:25)
            at async onImport.tracePromise.__proto__ (node:internal/modules/esm/loader:681:26)
            at async asyncRunEntryPointWithESMLoader (node:internal/modules/run_main:117:5) {
          errno: -2,
          code: 'ENOENT',
          syscall: 'lstat',
          path: '/home/antigravity/Code000-211/microPi/extensions/sdk-ts/node_modules'
        }
        
        Node.js v22.23.5
--- FAIL: TestEditToolMatchesPiOverSeededFiles (0.67s)
    edit_tool_seeded_oracle_test.go:95: Pi oracle: exit status 1
        node:fs:2801
              const stats = binding.lstat(base, true, undefined, true /* throwIfNoEntry */);
                                    ^
        
        Error: ENOENT: no such file or directory, lstat '/home/antigravity/Code000-211/microPi/extensions/sdk-ts/node_modules'
            at realpathSync (node:fs:2801:29)
            at file:///home/antigravity/Code000-211/microPi/internal/codingagent/tools/testdata/edit_tool_seeded.mjs:6:14
            at ModuleJob.run (node:internal/modules/esm/module_job:343:25)
            at async onImport.tracePromise.__proto__ (node:internal/modules/esm/loader:681:26)
            at async asyncRunEntryPointWithESMLoader (node:internal/modules/run_main:117:5) {
          errno: -2,
          code: 'ENOENT',
          syscall: 'lstat',
          path: '/home/antigravity/Code000-211/microPi/extensions/sdk-ts/node_modules'
        }
        
        Node.js v22.23.5
EDIT_ACCESS ["Operation aborted",false]
FAIL
FAIL	github.com/MichaelKinsy/PiG/internal/codingagent/tools	173.629s
FAIL
```

All M3 unit tests (`TestReadToolWithTrackerAndAliases`, `TestWriteToolWithTrackerAndAliases`, `TestEditToolWithTrackerAndAliases`, `TestEditToolClosestTextOnNotFound`, `TestRenameArgumentAliases`, `TestNormalizeToolArguments`, `TestFileWritesInCommand`, `TestFilesShownByCat`, `TestFileWriteRefusalAndWatchFiles`) passed in both runs.

---

## 2. Issues Found

### Blocking Issue 1: `read.go` marks all local files > 4100 bytes as `contentComplete = false`
- **Location**: `internal/codingagent/tools/read.go:328`, `internal/codingagent/tools/read.go:361`, `internal/codingagent/tools/read.go:450-458`
- **Problem**:
  - For any normal `read(path)` call (where `Offset` and `Limit` are nil, so `isStreamableRange(p)` is `true`), `readLocalFile(path, true)` returns a `ReadStream` (`src.stream != nil`, `src.data == nil`) whenever the file size is `>= imageprocessing.ImageTypeSniffBytes` (`4100` bytes).
  - In `ReadTool.Execute` (line 361):
    ```go
    contentComplete := src.stream == nil && len(data) < (1<<20)
    ```
  - Because `src.stream != nil`, `contentComplete` is `false` for **every local file between 4,100 bytes and 1 MiB** (e.g., a typical 5 KB–100 KB source file).
  - Consequently, `t.Tracker.OnFileRead(path, readContent, mtimeMs, cwd, data, false)` is called with `contentComplete = false` and `rawBytes = nil`. This skips `onSearchTouch`, skips `BackgroundChecksHook.OnRead`, and sets `TrackedFileState.ContentComplete = false` (preventing content comparison on mtime drift in `AssertCanModifyExistingFile`).
  - `read_test.go` only tested a 6-byte file and a 1.2 MB file, so this 4.1 KB–1 MiB gap was not caught.
- **Fix**: For local reads (`t.Operations == nil`) where `p.Offset == nil && p.Limit == nil` (or when the full file was read and size `< 1<<20` and not truncated), read/record the full file bytes for `Tracker.OnFileRead` with `contentComplete = true` when file size `< 1<<20`, and only set `contentComplete = false` when the file is `>= 1<<20` (1 MiB) or only a partial range/prefix was read. Add a test in `read_test.go` for a file between `4100` bytes and `1 MiB` (e.g. 10 KB).

### Non-Blocking / Minor Observations
1. **`write.go` custom `Operations.ReadExisting` path (`write.go:216-228`)**: When `t.Operations != nil && ops.ReadExisting != nil`, `WriteTool.Execute` calls `AssertCanModifyExistingFile` directly without the unread-write refusal (`unreadWriteRefusal` with 30k-char preview + `OnFileRead`), unlike the `t.Operations == nil` local path.
2. **Oracle tests (`edit_fuzzy_test.go`, `edit_tool_oracle_test.go`, `edit_tool_seeded_oracle_test.go`)**: These 3 tests fail because `extensions/sdk-ts/node_modules` is not present in the workspace.

---

## 3. Component Review Summary
- **`argument_aliases.go`**: Verified Claude Code & Qwen Code alias maps (`FilePathAlias`, `ReadPathAliases`, `EditReplacementAliases`, `EditTopLevelAliases`, `GrepAliases`, `BashAliases`). Canonical key precedence is enforced in both `RenameArgumentAliases` and `NormalizeToolArguments` / `renameOrderedAliases` without mutating input maps.
- **`edit_diff.go`**: Verified `findTrimmedLineSequence` (`seek_sequence`), `reindentNewTextForMatchedLines` (indentation normalization via GCD space unit / tab detection), `DescribeClosestText` (Dice bigram similarity + exact line anchors, line-numbered output), CRLF/BOM preservation (`SplitBom`, `normalizeToLF`, `applyReplacementsToOriginal`), and post-edit snippet 3-line context (`filetracking.FormatPostEditSnippet(..., 3)`).
- **`edit.go`**: Verified `Tracker` integration, nil-safety (`t.Tracker != nil`), unread single-match allowance + notice, stale-file single-match allowance + notice, post-edit snippet, and nested `AGENTS.md` instructions.
- **`write.go`**: Verified `Tracker` integration, nil-safety, 30k-char unread write refusal (`writeRefusalMaxChars = 30000`) marking the file as read, and stale-file refusal.
- **`read.go`**: Verified `Tracker` nil-safety, nested instructions, and `ReadPathAliases` integration; **blocked** on `contentComplete` being `false` for streamed files in the 4100 B – 1 MiB range.
- **`bash_file_writes.go`**: Verified `ParseShellSegments`, `FileWritesInCommand` (single-file `sed -i`, `perl -i`, authored literal redirections/`tee`, ignoring appends and scratch paths), `FilesShownByCat`, `FileWriteRefusal`, `WatchFiles`, and `CheckChangedFiles` helper API for M4 `bash.go`.
