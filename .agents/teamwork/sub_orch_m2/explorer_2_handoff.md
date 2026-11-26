# Explorer 2 Handoff: In-Process Search (`native-search`, `fff-search`, `grep`, `find`) & Pure-Go Design

## 1. Exact Caching & Search Design in `native-search.ts`

Source: `packages/coding-agent/src/core/tools/native-search.ts` (1590 lines).

### 1.1 File Tree Walk & Ignore Rules (`walk`, lines 366–624)
- **What is walked**:
  - `root` is resolved to an absolute path.
  - `repositoryOf(root)` walks upward looking for `.git` (file or directory). `top = repository ?? root`.
  - `requireGit`:
    - `grep` calls `getCachedTree(root, true)` (`requireGit = true`): `.gitignore` is only honored inside a git repo; `.ignore` is always honored.
    - `find` calls `getCachedTree(root, false)` (`requireGit = false`, matching `fd --no-require-git`): `.gitignore` and `.ignore` are honored even outside a git repo.
  - **Ignore sources** (when in a git repo):
    1. Global excludes: `core.excludesFile` from `~/.gitconfig` (handling `~` expansion and quotes), falling back to `$XDG_CONFIG_HOME/git/ignore` or `~/.config/git/ignore`.
    2. Repo excludes (`excludeFileOf(dir)`): `.git/info/exclude` if `.git` is a directory; if `.git` is a file (`gitdir: <path>`), resolves `gitdir` and checks `commondir` (for linked worktrees) before `<common>/info/exclude`.
    3. Parent directory `.gitignore` and `.ignore` files from `repository` down to `root`'s parent.
  - **Nested repositories**: During directory traversal (`visit`), if a subdirectory (`task.rel !== ""`) contains `.git`, all inherited `git: true` rules are stripped (`taskRules.filter(rule => !rule.git)`), the nested repo's `excludeFileOf(task.abs)` is loaded, and `taskGit = true` is set.
  - **`.git` directory**: Always skipped (`if (dirent.name === ".git") continue`).
  - **Hidden files**: Walked and included (unless matched by ignore rules), matching `rg --hidden` and `fd --hidden`.
  - **Symlinks**: Listed as `kind: "symlink"` by `walk` (so `find` returns them), **never followed** into directories, and **skipped** by `grep` (`entry.kind === "file"` only, plus `stat`/`O_NONBLOCK` regular-file checks).
  - **Unreadable dirs / non-regular ignore files**: `readSmallFile` opens with `O_RDONLY | O_NONBLOCK`, checks `stats.isFile()` and `stats.size <= 4 MiB`; unreadable dirs/files are silently skipped.
  - **Concurrency**: Directory reads are batched 64 folders at a time (`pending.splice(0, 64)`).
  - **Sorting**: Both `entries` and `ignoredFiles` are sorted lexicographically by `rel` (`byPath`).
  - **Ignored files & dirs tracking**:
    - `ignoredDirs`: absolute paths of directories excluded by ignore rules (used to avoid invalidating the tree cache when a file is touched inside an ignored directory like `build/` or `node_modules/`).
    - `ignoredFiles`: regular files excluded by ignore rules outside ignored directories (sorted by `rel`), kept so `grep --glob` can still include explicitly matched ignored files, matching ripgrep's `--glob` override semantics.

### 1.2 In-Memory File Tree Cache (`getCachedTree`, lines 628–820)
- **Cache key**: `normalizeCacheKey(resolvedRoot) + "|" + (requireGit ? "1" : "0")`. (`normalizeCacheKey` resolves path and lowercases on Windows.)
- **Stored entry (`CachedTreeEntry`)**:
  - `rootKey`: normalized root path.
  - `entries`: sorted `WalkEntry[]` (`rel`, `abs`, `kind`).
  - `ignoredFiles`: sorted `WalkEntry[]` (ignored files not under ignored dirs).
  - `displayPaths`: `string[]` parallel to `entries`, with trailing `/` appended for `kind === "dir"`.
  - `packedPathsBuffer`: lazily built `\0`-delimited `Buffer` for the C addon (`fff-bridge.node`), not needed in pure Go.
  - `knownAbsKeys`: `Set<string>` of normalized `abs` paths for all `entries` and `ignoredFiles`.
  - `ignoredDirKeys`: `Set<string>` of normalized `ignoredDirs`.
  - `dirMtimes`: `Map<string, number>` of up to `MAX_VERIFIED_DIRS = 4096` directory paths (root + walked subdirs) to their `mtimeMs`.
  - `partial`: `boolean`, `true` if `dirMtimes.size >= 4096`.
  - `timestamp`: monotonic timestamp (`performance.now()`) when walked or last revalidated.
- **Revalidation & TTL**:
  - `TREE_CACHE_VERIFY_MS = 2000` (2 seconds).
  - Fast path: if `now - cached.timestamp < 2000` AND `cached.timestamp > externalChangeAt`, return `cached` immediately with zero syscalls.
  - Revalidation path (after 2s TTL or after `externalChangeAt`):
    - If `changedSince && cached.partial`: immediately invalid (`valid = false`).
    - Otherwise, `stat` every directory in `cached.dirMtimes`; if any `mtimeMs` changed or `stat` fails, `valid = false`.
    - If all match, bump `cached.timestamp = now` and reuse `cached`.
    - Otherwise, re-walk and replace the cache entry.

### 1.3 Content Cache (lines 655–698, 1519–1572)
- **Constants**:
  - `MAX_CONTENT_CACHE_BYTES = 64 * 1024 * 1024` (64 MB hot buffer pool).
  - `MAX_CACHED_FILE_BYTES = 512 * 1024` (512 KB per-file cap; larger files are not cached in the pool).
  - `CONTENT_CACHE_STAT_FREE_MS = 1500` (1.5s stat-free window).
  - `WHOLE_FILE_BYTES = 2 * 1024 * 1024` (2 MB; files `<= 2 MB` are read whole, larger files are streamed in `PIECE_BYTES = 1 MB` chunks via `searchInPieces`).
- **Entry (`CachedFileBuffer`)**:
  - `mtimeMs`, `size`, `verifiedAt` (monotonic), `buffer: Buffer | null` (`null` represents a binary file, i.e., `buffer.indexOf(0) !== -1`).
- **Eviction policy**:
  - Insertion-ordered map (`Map` in JS): on `storeContent(key, mtimeMs, buffer)`, any previous entry for `key` is deleted (subtracting its byte length from `contentCacheBytes`) and re-inserted at the tail.
  - If `buffer.length > 512 KB`, it is not stored (and any prior entry for `key` was removed).
  - Binary files (`buffer.indexOf(0) !== -1`) are stored with `buffer: null` (0 bytes counted toward `contentCacheBytes`).
  - While `contentCacheBytes > 64 MB`, evict from the head (oldest stored/updated entry) until `<= 64 MB`.
- **Stat-free & mtime validation in `nativeGrep`**:
  - If `cachedBuf` exists AND `now - cachedBuf.verifiedAt < 1500` AND `cachedBuf.verifiedAt > externalChangeAt` AND `cachedBuf.size <= wholeFileBytes`: skip `stat()` and `readFile()`, use `cachedBuf.buffer` directly (or skip if `null` = binary).
  - Otherwise, `stat()` the file: if `stats.isFile()` and `stats.size <= wholeFileBytes`, if `cachedBuf` has matching `mtimeMs` and `size`, update `cachedBuf.verifiedAt = now` and reuse `cachedBuf.buffer` without `readFile()`. Otherwise `readFile()`, call `storeContent()`, and search.

### 1.4 Fast Literal Matching vs Regex Matching & Large Files
- **Literal mode (`literal: true`)**:
  - Case-sensitive: `Buffer.indexOf(needle)` (Boyer-Moore / memmem in native/Go `bytes.Index`).
  - Case-insensitive ASCII (`isAscii(pattern)`): `indexOfIgnoringCase` finds first/next occurrence of lower/upper first byte and compares ASCII-lowercased bytes.
  - Case-insensitive non-ASCII: falls back to `regexMatcher` with escaped pattern + `i` flag.
- **Regex mode (`literal: false`)**:
  - `translatePattern(pattern)` translates ripgrep/Rustisms:
    - Leading `(?i)` -> sets `ignoreCase = true` and strips prefix.
    - `(?P<name>...)` -> `(?<name>...)` (note: Go `regexp` already supports both `(?i)` and `(?P<name>...)` natively!).
    - `\x{...}` / `\u{...}` hex code points -> UTF-8/UTF-16 escapes.
  - `requiredLiteral(source)` extracts the longest guaranteed literal run (>= 3 chars) outside groups/alternations/quantifiers/char-classes.
  - **Prefilter & line-by-line decoding**:
    - If `prefilter` exists (case-sensitive literal or `requiredLiteral`), whole file/piece buffer is checked with `bytes.Index(buf, prefilter) == -1` first—skipping non-matching files with zero line splitting or regex calls.
    - `searchLines` scans for occurrences of the literal/prefilter, finds enclosing `\n` boundaries, counts lines only up to matches (`countLines`), strips trailing `\r`, truncates to `keepChars`, and runs the regex only on candidate lines.
  - **Regex without literal**:
    - Scanned in `WINDOW_BYTES = 16 KB` line-aligned windows (`lastBreak`), matching within a single line (never across `\n`), with cooperative deadline checks (`TURN_MS = 50ms`) so abort signals interrupt even catastrophic backtracking.
- **Large files (`> 2 MB`)**:
  - `searchInPieces` reads `1 MB` chunks with double-buffering (`buffer` + `spare`), carrying partial trailing lines across chunk boundaries, checking for NUL bytes (`0x00`) to abort early on binary files, and counting line numbers lazily only when a chunk passes `prefilter`.

### 1.5 Cache Invalidation Hooks
Note: In TypeScript, the functions are named:
- `notifySearchFileTouched(filePath, content?, mtimeMs?)` (called by `FileTracker.onFileRead` and `FileTracker.onFileModified` in `file-tracker.ts`, which are called by `read.ts`, `write.ts`, `edit.ts`, and `bash.ts` for `cat`/tracked edits):
  - If `content` and `mtimeMs` are provided, updates `contentCache` via `storeContent`.
  - For every cached tree in `treeCache`: if `key` is already in `cachedTree.knownAbsKeys`, or NOT below `cachedTree.rootKey`, or below `cachedTree.ignoredDirKeys`, the tree is kept; otherwise (a new non-ignored file created under `rootKey`), that tree entry is deleted.
- `notifySearchExternalChange()` (called in `bash.ts` and `bash-executor.ts` in `finally` blocks and when background jobs finish):
  - Sets `externalChangeAt = performance.now()`. Does **not** clear caches immediately; instead forces the next `getCachedTree` / `nativeGrep` to bypass the 2s / 1.5s stat-free windows and revalidate `dirMtimes` / `stat()` mtimes (and invalidates `partial` trees).
- `clearNativeSearchCache()`: clears `treeCache`, `contentCache`, and resets `contentCacheBytes = 0`.
- For Go (`InvalidateSearchCache(cwd string)` / `NotifySearchFileTouched` / `NotifySearchExternalChange`): we should provide both targeted invalidation (`InvalidateSearchCache(cwd)` or `NotifySearchExternalChange()`) and `NotifySearchFileTouched(path, content, mtime)` so `read.go`, `write.go`, `edit.go`, and `bash.go`/`powershell.go` can keep caches coherent without unnecessary re-walks.

---

## 2. `fff-search.ts` Fuzzy Matching & `find.ts` / `grep.ts` Semantics

### 2.1 `find` Tool & Fuzzy Fallback (`find.ts` + `native-search.ts` + `fff-search.ts`)
- **Parameters**: `pattern` (required), `path` (optional, default `.`), `limit` (optional, default `1000`).
- **Glob normalization**:
  - On Windows, `\` is converted to `/` (except escapes before `[]{}!,`) and leading `./` is stripped.
  - If `pattern` contains `/`: matched against relative path `rel`. Leading `/` is stripped; otherwise if it does not start with `**/` and is not `**`, `**/` is prepended (Note: in `native-search.ts` line 850, `src/*.ts` becomes `**/src/*.ts`, whereas in `find.ts` fd mode it anchors; `nativeFindDetailed` uses `expandBraces(pattern).map(compileGlob)`).
  - If `pattern` has no `/`: matched against basename `rel.slice(lastSlash + 1)`.
  - `expandBraces` expands `{a,b}` up to `MAX_BRACE_EXPANSIONS = 1024` (throws error if exceeded).
  - `compileGlob` / `globMatch` supports `*`, `?`, `[...]` (including `[!...]` / `[^...]`), `\` escapes, `**/`, and `**`, with linear-time memoized fallback (`globFromMarked`) to prevent exponential backtracking on many `*`s.
- **Results & formatting**:
  - Uses `getCachedTree(root, false, signal)` (so `.gitignore` is honored even outside git repos).
  - Returns `displayPaths` (dirs have trailing `/`), in sorted path order, up to `effectiveLimit`.
  - If `matched.length === 0` and `fuzzyFallback: true`:
    - Calls `fuzzyFilterPaths(displayPaths, options.pattern, min(maxResults, FUZZY_FALLBACK_LIMIT))` where `FUZZY_FALLBACK_LIMIT = 20`.
    - `fuzzyFilterPaths` strips `[*?[\]{}]` and trims whitespace from `query`; if empty, returns `[]`.
    - When `fuzzy` results are returned, `find.ts` prepends:
      `"No files match the pattern. The closest paths by name (not matches of the pattern):\n"`
      and sets `details.fuzzyFallback = true`.
  - If still empty, returns `"No files found matching pattern"`.
  - Truncation: `truncateHead(rawOutput, { maxLines: Number.MAX_SAFE_INTEGER })` (50 KB byte cap).
  - Notices:
    - If `relativized.length >= effectiveLimit`: `"<limit> results limit reached. Use limit=<limit*2> for more, or refine pattern"`, `details.resultLimitReached = effectiveLimit`.
    - If byte-truncated: `"50.0KB limit reached"`, `details.truncation = ...`.

### 2.2 `fff-search.ts` Fuzzy Scoring Algorithm (`scoreFuzzyPathFallback`)
- **Pre-filter**:
  - Lowercases `query` (`qLower`) and builds a 26-bit lowercase ASCII letter bitmask `qMask`.
  - Single pass over `pathStr`: computes `pMask`, `lastSlash`, and checks ordered subsequence match (`qi == qLen`). If `(pMask & qMask) !== qMask || qi < qLen`, returns `0` immediately.
- **Greedy boundary-aware scoring** (base `score = 100`):
  - For each query char `qc` at index `k` (searching from `searchFrom` to `pLen - (qLen - k)`):
    - Evaluates each matching position `i` and picks `bestPos` with highest `bonus` (early-exiting if `bonus >= 50`):
      - Consecutive match (`i == prevMatch + 1`): `+35`
      - Start of basename (`i == baseStart`, where `baseStart = lastSlash + 1`): `+50`
      - Else after separator (`/`, `_`, `-`, `.`): `+28`
      - Else camelCase boundary (lowercase `i-1`, uppercase `i`): `+24`
      - Inside basename (`i >= baseStart`): `+15`
    - Adds `20 + bestBonus` to `score`.
    - Gap penalty: if `prevMatch != -1 && bestPos > prevMatch + 1`, subtracts `(bestPos - prevMatch - 1) * 2`.
    - Tracks `matchedInBasename` and advances `searchFrom = bestPos + 1`.
  - Length penalty: `score -= pLen >> 2` (`pLen / 4`).
  - Exact basename match bonus: if `matchedInBasename == qLen && pLen - baseStart == qLen`, `score += 80`.
  - Clamps minimum positive score to `1`.
- **Sorting & limit**:
  - Filters `score > 0`, sorts descending by `score`, ties broken lexicographically ascending by `path`, slices top `limit` (capped at `20` by `FUZZY_FALLBACK_LIMIT`).

### 2.3 `grep` Tool (`grep.ts`)
- **Parameters**: `pattern`, `path` (default `.`), `glob`, `ignoreCase` (default `false`), `literal` (default `false`), `context` (default `0`), `limit` (default `100`).
- **Argument aliases**: `-i -> ignoreCase`, `-C -> context`, `head_limit -> limit`, `include -> glob`.
- **Glob normalization**: If `glob` contains `/` and does not start with `/`, `**/`, `!`, and is not `**`, prepends `**/`.
- **Output format**:
  - If `matches.length === 0`: `"No matches found"`.
  - Paths formatted relative to `searchPath` with `/` separators (or `basename(filePath)` if `searchPath` is a file).
  - Match lines: `<rel>:<lineNumber>: <truncatedLine>`
  - Context lines (when `context > 0`): `<rel>-<lineNumber>- <truncatedLine>` (for `max(1, lineNumber-context)` to `min(lines.length, lineNumber+context)`).
  - Each line has `\r` stripped and is truncated at `GREP_MAX_LINE_LENGTH = 500` chars (`... [truncated]`), setting `details.linesTruncated = true`.
  - Total output is truncated with `truncateHead(rawOutput, { maxLines: Number.MAX_SAFE_INTEGER })` (50 KB byte cap).
  - Notices appended as `\n\n[<notice1>. <notice2>]`:
    - Match limit reached: `"<limit> matches limit reached. Use limit=<limit*2> for more, or refine pattern"`, `details.matchLimitReached = effectiveLimit`.
    - Byte limit reached: `"50.0KB limit reached"`, `details.truncation = ...`.
    - Lines truncated: `"Some lines truncated to 500 chars. Use read tool to see full lines"`, `details.linesTruncated = true`.

---

## 3. Pure-Go Design Recommendation (`CGO_ENABLED=0`, Zero External Binaries)

In the current PiG codebase, `internal/codingagent/tools/grep.go` and `internal/codingagent/tools/find.go` shell out to `rg` and `fd` via `ToolsManager.EnsureTool` (downloading binaries if missing). Porting `native-search.ts` + `fff-search.ts` into pure Go eliminates external binary dependencies completely.

### 3.1 Proposed File Layout in `internal/codingagent/tools/`
1. **`native_search.go`**:
   - Glob compiler & linear-time matcher (`expandBraces`, `compileGlob`, `globMatch`) — or reuse/extend `internal/minimatch` if appropriate, though a dedicated ~200-line linear-time `globMatch` with `freeSteps`/memoization is self-contained and avoids `minimatch` allocation overhead during tree walks.
   - Ignore file parser & git repo discovery (`parseIgnoreFile`, `isIgnored`, `repositoryOf`, `excludeFileOf` supporting `.git/info/exclude` and worktree `gitdir:` + `commondir`, `globalExcludesFile`, `readSmallFile` with 4 MiB cap & `O_NONBLOCK` on Unix).
   - Concurrent directory walker (`walk` using a bounded worker pool, e.g. `runtime.NumCPU()` or 32–64 goroutines, `os.ReadDir`, skipping `.git`, not following symlinks, sorting `entries` and `ignoredFiles` by `rel`).
   - Thread-safe (`sync.RWMutex` / `sync.Mutex`) in-memory caches:
     - `treeCache`: `map[string]*cachedTreeEntry` with `2s` TTL (`time.Now()`), `MAX_VERIFIED_DIRS = 4096` `dirMtimes` revalidation, `partial` flag, `ignoredDirKeys`, `knownAbsKeys`.
     - `contentCache`: `64 MB` (`64 << 20`) pool, `512 KB` (`512 << 10`) per-file cap, `1500ms` stat-free window, `container/list` + `map[string]*list.Element` for $O(1)$ move-to-back & head eviction, storing `nil` slice for binary files (NUL byte detected via `bytes.IndexByte(buf, 0) != -1`).
     - Exported invalidation API:
       - `NotifySearchFileTouched(filePath string, content []byte, mtime time.Time)`
       - `NotifySearchExternalChange()`
       - `InvalidateSearchCache(cwd string)` (clears/invalidates trees under `cwd` or all if `cwd == ""`, plus bumps external change timestamp).
       - `ClearNativeSearchCache()` (for tests).
   - Fuzzy path scorer (`fuzzyFilterPaths`, `scoreFuzzyPath`) — direct port of `scoreFuzzyPathFallback` using byte/ASCII fast path and bitmask prefilter, `FUZZY_FALLBACK_LIMIT = 20`.
   - Grep engine (`nativeGrep`):
     - `literalMatcher` using `bytes.Index` (case-sensitive) and fast ASCII case-folding scan (case-insensitive ASCII).
     - `regexMatcher` using Go's `regexp` (which already uses RE2 — guaranteed linear time, no catastrophic backtracking! And natively supports `(?i)` and `(?P<name>...)`; we only need `\x{...}` / `\u{...}` translation if needed, or `lazyregexp` / `regexp.Compile`), plus `requiredLiteral` prefilter so non-matching files are skipped with `bytes.Index` before line splitting.
     - `searchInPieces` for files `> 2 MB` (`WHOLE_FILE_BYTES = 2 << 20`, `PIECE_BYTES = 1 << 20`) with `os.Open` + chunked reading, NUL-byte early exit, and lazy line counting.
     - `ctx.Err()` checks throughout walks, file reads, and line loops for instant cancellation.
2. **`grep.go`**:
   - Replace `rg` subprocess execution with `nativeGrep(...)` when `t.Operations == nil` (or keep `Operations.IsDirectory` check, and use `Operations.ReadFile` when `context > 0` or custom `Operations` is set).
   - Keep `GrepTool` struct, `Schema()`, `BorrowedSchema()`, `prepareArguments` / parameter aliases, `grepFormatter` / `headJoin` / `GrepDetails` (`MatchLimitReached`, `Truncation`, `LinesTruncated`), and exact notice strings untouched so all existing `grep_*_test.go` contracts remain intact.
   - Note on `context > 0`: In `nativeGrep` / `grep.go`, read context lines from the `contentCache` buffer (or `Operations.ReadFile` if provided) rather than spawning `rg`. Handle overlapping context windows cleanly so lines are formatted as `<rel>-<line>- <text>` and match lines as `<rel>:<line>: <text>`.
3. **`find.go`**:
   - Replace `fd` subprocess execution with `nativeFindDetailed(...)` when `t.Operations == nil || t.Operations.Glob == nil`.
   - Keep `FindTool.Operations` path (`findWithOperations`) for remote/custom glob backends.
   - Add `FuzzyFallback bool `json:"fuzzyFallback,omitempty"`` to `FindDetails` in `details.go`, and prepend the exact notice `"No files match the pattern. The closest paths by name (not matches of the pattern):\n"` when `fuzzy == true`.
   - Keep `effectiveLimit = math.Max(1, math.Floor(limit))`, `relativizeFindResultPath`, `formatFindResults`, and `TruncateHead` byte truncation.
4. **Cache Invalidation Hooks in PiG**:
   - `read.go`: after reading a complete regular file, call `NotifySearchFileTouched(absPath, rawBytes, info.ModTime())`.
   - `write.go` & `edit.go`: after writing/editing, call `NotifySearchFileTouched(absPath, newBytes, info.ModTime())`.
   - `bash.go` & `powershell.go`: in `defer` after command execution (and when background jobs complete), call `NotifySearchExternalChange()` (or `InvalidateSearchCache(cwd)`).
5. **Cleanup**:
   - `ensureSearchTool` / `ToolsManager` auto-download of `rg` and `fd` is no longer needed by `grep.go` and `find.go` (check if any tests in `tools_manager_*_test.go` test `ToolsManager` directly; `ToolsManager` itself can stay if tested, while `GrepTool` and `FindTool` become 100% pure-Go in-process).
