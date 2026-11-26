# Challenger 1 Handoff (File Tracking, Edit Ergonomics, Aliases & Bash File-Writes)

## Verdict: PASS

All 8 adversarial test areas were empirically probed using `go test -v -race` against `internal/codingagent/filetracking` and `internal/codingagent/tools` (temporary test files removed after execution so the repo remains clean).

## Empirical Test Results

1. **CRLF + UTF-8 BOM preservation on `edit` (including `seek_sequence` fuzzy indentation match + multi-edit)**: **PASS**
   - Multi-edit on a UTF-8 BOM (`\xef\xbb\xbf`) + `\r\n` file where Edit 1 used `seek_sequence` (2-space indented `oldText`/`newText` matched against 4-space indented file lines, inserting 3 lines) and Edit 2 used exact match on `footer`:
     - Preserved `\xef\xbb\xbf` BOM at byte 0.
     - Preserved `\r\n` across both untouched lines and newly inserted multi-line `newText`.
     - Reindented `newText` to 4 spaces.
     - Updated `Tracker` state with exact on-disk bytes so a subsequent `edit` succeeded without a false `ErrStaleFile`.

2. **Unread `edit` (unique vs non-unique/missing)**: **PASS**
   - Unread + non-unique `oldText`: failed with `ErrUnreadFile`; file remained unread.
   - Unread + missing `oldText`: failed with `ErrUnreadFile` (did not leak closest-text diff for unread files).
   - Unread + unique `oldText`: succeeded with `"The file had not been read in this session; every oldText matched it exactly once."` and marked the file read.
   - Already-read + missing similar `oldText`: failed with `Could not find...` and closest-text diff.

3. **Disk-changed `edit` (unique vs ambiguous/missing)**: **PASS**
   - Externally modified file with newer mtime and unique `oldText`: succeeded with `"The file had changed on disk since it was last read; the edit was applied to its current content."`.
   - Externally modified file with newer mtime and ambiguous/missing `oldText`: failed with `ErrStaleFile`.

4. **Sub-millisecond external edit on already-read file**: **PASS**
   - Modifying file content on disk while restoring the exact original `ModTime` (`os.Chtimes`):
     - `Tracker.AssertCanModifyExistingFile` returned `ErrStaleFile`.
     - `WriteTool.Execute` failed with `ErrStaleFile`.
   - Matching content with a newer mtime (>10ms) succeeded and refreshed `LastReadMtimeMs`.

5. **`write` over unread existing file > 30,000 chars**: **PASS**
   - Writing over an unread 30,500-rune UTF-8 file (`"é" * 30500`):
     - First `WriteTool.Execute` refused (`IsError: true`), truncated at 30,000 runes with `[cut at 30000 of 30500 characters: read the rest if it matters]`, and marked the file read.
     - Second `WriteTool.Execute` succeeded.

6. **Nested instructions (`nested_instructions.go`)**: **PASS**
   - Verified root-first ordering (`a/AGENTS.override.md` before `a/b/CLAUDE.md`).
   - Verified `AGENTS.override.md` shadows `AGENTS.md` and `CLAUDE.md` in the same directory, and `workspaceCwd/AGENTS.md` is ignored.
   - Verified UTF-8 BOM stripping on instruction files.
   - Verified session deduplication on subsequent reads in the same subtree.
   - Verified self-read skipping (reading `a/b/CLAUDE.md` directly does not emit itself, emits parent `a/AGENTS.override.md`, and marks `CLAUDE.md` seen).
   - Verified `WithLocal(false)` disables nested instruction discovery.

7. **`bash_file_writes.go`**: **PASS**
   - Detected: `sed -i 's/a/b/' foo.txt` (`sed -i`), `perl -pi -e 's/a/b/' bar.pl` (`perl -i`), `cat <<EOF > f.txt` (`a redirection`), `echo hi > out.txt` (`a redirection`), `cat <<EOF | tee t.txt` (`tee`), `echo hi | tee t2.txt` (`tee`).
   - Ignored: `echo hi >> out.txt` (append), `echo hi > /tmp/x` (scratch), `sed -i 's/a/b/' *.go` (glob), `sed -i 's/a/b/' a.go b.go` (multi-file), `cat <<EOF | tee -a t.txt` (`tee -a`), `echo hi | tee -a t2.txt` (`tee -a`).
   - `FileWriteRefusal` returned `"Use the edit or write tool instead of a redirection to modify out.txt."`.

8. **`argument_aliases.go`**: **PASS**
   - Verified canonical key wins when both alias and canonical are present across `NormalizeToolArguments` (`ReadPathAliases`), `EditTool.PrepareArguments` (`path` vs `file_path`, `oldText`/`newText` vs `old_string`/`new_string`), and `RenameArgumentAliases` (`BashAliases`, `GrepAliases`).
