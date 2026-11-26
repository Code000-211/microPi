# Scope — Milestone M4: Bash Execution, Background Jobs, `wait` Tool & Job Hub

## Exclusive File Ownership
- `internal/codingagent/jobs/**`
- `internal/codingagent/tools/bash.go`
- `internal/codingagent/tools/wait.go`
- `internal/codingagent/tools/bash_readonly.go`
- `internal/codingagent/tools/bash_executor.go`
- `internal/codingagent/` job-hub / session / keybinding / slash-command wiring

## Forbidden Paths (Owned by M2 in parallel)
- `internal/docsearch/**`
- `internal/codingagent/tools/docsearch.go`
- `internal/codingagent/tools/native_search.go`
- `internal/codingagent/tools/grep.go`
- `internal/codingagent/tools/find.go`

## Deliverables
1. **Background Jobs & `wait` Tool**:
   - `internal/codingagent/jobs/`: job manager, `bg1`/`bg2`... IDs, auto-background after 120s or explicit `timeout` (immediate if `background: true`), completion wakeup (`background-job` message with 1.5s settle window).
   - `internal/codingagent/tools/wait.go`: `wait` tool with backoff ladder `[120s, 300s, 600s]`, sleep-shortening when background jobs are running, long-sleep guard (>120s).
2. **Bash Execution & Output**:
   - `internal/codingagent/tools/{bash.go,bash_readonly.go,bash_executor.go}`:
   - Head+tail truncation (first 40 lines + end, 2000 lines / 32 KB).
   - ANSI/progress stripping + `NO_COLOR=1`.
   - Early execution of read-only commands during streaming.
   - Direct exec without shell when no shell syntax is present.
   - Wire M3's `BashAliases`, `FilesShownByCat`, `FileWritesInCommand` / `WatchFiles` / `CheckChangedFiles`.
   - Expose `OnCommandFinished func(cwd string)` hook (for M2 search cache invalidation wiring later without touching M2's files).
3. **Interactive Job Hub (`alt+j` / `/jobs`)**:
   - Port `job-hub.ts` live job hub view (`alt+j` keybinding and `/jobs` slash command) into `internal/codingagent/`.
