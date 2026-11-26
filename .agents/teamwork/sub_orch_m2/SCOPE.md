# Milestone M2 Scope: Search & Code Intelligence (`docsearch` + cached `grep`/`find` & `fff`)

## Owned Files & Directories
- `internal/docsearch/**` (new package: pure Go BM25 + symbol/section graph, definition-aware chunking, incremental index updates, auto-disable <= 150 files)
- `internal/codingagent/tools/docsearch.go` (`project_search` tool)
- `internal/codingagent/tools/native_search.go` (in-memory file tree cache 2s TTL + mtime revalidation, content cache 64 MB cap / 512 KB per file, fast literal matching, `InvalidateSearchCache(cwd string)`)
- `internal/codingagent/tools/grep.go` (cached fast literal/regex grep)
- `internal/codingagent/tools/find.go` (cached find + `fff` fuzzy fallback on empty find)
- Minimal registration in `internal/codingagent/` for `project_search` tool and `/docs` slash command.

## Out of Scope (Do NOT Modify)
- `internal/codingagent/tools/edit.go`, `write.go`, `read.go`
- `internal/codingagent/filetracking/**`
- `internal/codingagent/checks/**`
- Any files owned by M3/M4/M5.

## Interface Contracts
- `docsearch.NewTool(cwd string) agent.AgentTool` (or matching `internal/codingagent/tools` signature) exposes `project_search` (auto-disabled if project file count <= 150).
- `docsearch.HandleDocsCommand(args string, cwd string) string` serves `/docs`.
- `tools.InvalidateSearchCache(cwd string)` invalidates file tree and content caches for `cwd`.
- `CGO_ENABLED=0 go build ./...` and `CGO_ENABLED=0 go build ./cmd/upi` must pass.
