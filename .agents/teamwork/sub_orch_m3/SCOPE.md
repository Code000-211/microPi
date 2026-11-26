# M3 Scope: File Tracking, Edit Ergonomics, Argument Aliases & Background Checks

## Exclusive File Ownership
- `internal/codingagent/filetracking/**`
- `internal/codingagent/checks/**`
- `internal/codingagent/tools/edit.go`
- `internal/codingagent/tools/edit_diff.go`
- `internal/codingagent/tools/write.go`
- `internal/codingagent/tools/read.go`
- `internal/codingagent/tools/argument_aliases.go`
- `internal/codingagent/tools/bash_file_writes.go`

## Out of Scope / Do Not Touch
- `internal/codingagent/tools/grep.go`, `internal/codingagent/tools/find.go`, `internal/docsearch/**` (owned by M2 running in parallel)
- `internal/codingagent/tools/bash.go`, `internal/codingagent/tools/wait.go`, `internal/codingagent/jobs/**` (owned by M4)

## Deliverables
1. **File Tracking (`internal/codingagent/filetracking/`)**:
   - Read-before-edit tracking (`Tracker`, `FileState`)
   - Unread/disk-changed `edit` allowed when every `oldText` matches uniquely
   - `write` over unread existing file returns up to 30k chars and marks read
   - CRLF/BOM preservation
   - Nested `AGENTS.override.md`/`AGENTS.md`/`CLAUDE.md` injection on read
2. **Edit Ergonomics (`internal/codingagent/tools/{edit.go,edit_diff.go,write.go,read.go}`)**:
   - Whitespace-insensitive line matching (`seek_sequence`) with indentation normalization
   - Closest-text diff with line numbers on miss
   - 3 lines of context on success
3. **Bash File-Write Guard (`internal/codingagent/tools/bash_file_writes.go`)**:
   - Parse/track single-file `sed -i`, `perl -i`, or literal redirections (`echo`/`printf`/heredoc via `cat`/`tee`) locally (refused on `/remote`), exposing helper API for `bash.go` (M4)
4. **Tool Argument Aliases (`internal/codingagent/tools/argument_aliases.go`)**:
   - Normalize Claude Code (`file_path`, `old_string`, `new_string`, `run_in_background`, etc.) & Qwen Code (`absolute_path`, `include`, `is_background`, etc.) parameter names
5. **Background Compiler Diagnostics & Linters (`internal/codingagent/checks/`)**:
   - Post-`edit`/`write`/file-modifying-`bash` background diagnostics diffed against pre-read/pre-edit state
   - `PI_CHECKS_WAIT` (30s default) settle/wait
   - Checkers for Go (`go vet`), Rust (`cargo check`/`clippy`), Python (`ty`/`ruff`), TS (`tsc`), JS (`node --check`/biome/oxlint), Shell (`bash -n`/`shuck`), C/C++/HIP (`clang`/`gcc`), JSON/TOML/YAML, Markdown, Luau, `radisa` (`PI_CHECKS` configurable)
