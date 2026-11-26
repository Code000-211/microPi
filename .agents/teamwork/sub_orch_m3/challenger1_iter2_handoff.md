# Challenger 1 (Iteration 2) Handoff — Tools & File Tracking

**Verdict:** PASS

## Scope & Empirical Checks

Temporary adversarial test suite `TestChallenger1Iter2` was executed against `github.com/MichaelKinsy/PiG/internal/codingagent/tools` (`go test -v ./internal/codingagent/tools -run TestChallenger1Iter2`, 0.316s, all subtests PASS) and removed after verification.

1. **Streamed medium file (~20KB and ~60KB > 50KB truncation cap, < 1 MiB):**
   - `ReadTool.Execute` with `Tracker` recorded `ContentComplete == true` and retained the full on-disk content in `TrackedFileState.Content` (including for the ~60KB file whose tool output is truncated at 50KB).
   - `BackgroundChecksHook.OnRead(path)` was invoked exactly once.
   - A subsequent sub-millisecond external modification (1-byte change with `os.Chtimes` restoring the original mtime) was caught and rejected with `ErrStaleFile` by both `WriteTool` and `EditTool`.

2. **Large file (`>= 1 MiB`):**
   - Boundary `1<<20 - 1` (1,048,575 bytes): `ContentComplete == true`, `OnRead` invoked once.
   - Exact `1<<20` (1,048,576 bytes) and `> 1 MiB` (1,052,672 bytes): `ContentComplete == false`, `BackgroundChecksHook.OnRead` was NOT invoked.

3. **Partial `offset`/`limit` read on medium file:**
   - Tested `offset` only, `limit` only, and `offset` + `limit`.
   - In all three cases, `ContentComplete == false` and `BackgroundChecksHook.OnRead` was NOT invoked.

4. **CRLF/BOM multi-edit with `seek_sequence` and unread `write` 30k limit:**
   - Multi-edit on a UTF-8 BOM (`\uFEFF`) + CRLF (`\r\n`) + tab-indented Go file using LF + 2-space `oldText` inputs applied all edits accurately, preserved BOM and CRLF line endings, and allowed a follow-up `EditTool` call without false staleness.
   - Unread `WriteTool` against a 35,000-rune (`é`) file refused the first write without modifying disk, returned `[cut at 30000 of 35000 characters: read the rest if it matters]`, marked the file as read, and succeeded on the second `WriteTool` call.
