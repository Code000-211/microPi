# Briefing — Milestone M4

## Context
- Original Request: `/home/antigravity/Code000-211/microPi/.agents/teamwork/ORIGINAL_REQUEST.md`
- Project Scope: `/home/antigravity/Code000-211/microPi/PROJECT.md`
- Project Rules: `/home/antigravity/Code000-211/microPi/AGENTS.md`

## Reference Implementations
- `packages/coding-agent/src/core/background-jobs.ts`
- `packages/coding-agent/src/core/tools/wait.ts`
- `packages/coding-agent/src/core/tools/bash.ts`
- `packages/coding-agent/src/core/tools/bash-read-only.ts`
- `packages/coding-agent/src/core/tools/bash-executor.ts`
- `packages/coding-agent/src/modes/interactive/components/job-hub.ts`

## M3 Integration APIs
- `internal/codingagent/tools/argument_aliases.go`: `BashAliases`
- `internal/codingagent/tools/bash_file_writes.go`: `FileWritesInCommand`, `FilesShownByCat`, `FileWriteRefusal`, `WatchFiles`, `CheckChangedFiles`

## Current Iteration
- Iteration 1: Starting with 3 Explorers to inspect existing Go `internal/codingagent/`, `coding/`, `tui/`, M3 APIs, and TS reference implementations.
