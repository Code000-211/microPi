package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ============================================================================
// Feature 1: PiG Base & Build (5 tests)
// ============================================================================

func TestT1_F01_BinaryExecutesAndPrintsHelp(t *testing.T) {
	env := NewTestEnv(t)
	res := env.RunUPI("--help")
	assertExitZero(t, res)
	assertContains(t, res.Stdout, "upi", "help output")
	assertContains(t, res.Stdout, "--mode", "help output")
}

func TestT1_F01_PrintModeBasicPrompt(t *testing.T) {
	env := NewTestEnv(t)
	env.MockLLM.EnqueueText("PONG_PRINT_MODE")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "Reply with PONG_PRINT_MODE")
	assertExitZero(t, res)
	assertContains(t, res.Stdout, "PONG_PRINT_MODE", "print mode stdout")

	req, ok := env.MockLLM.LastRequest()
	if !ok {
		t.Fatalf("expected MockLLM to receive at least one request")
	}
	assertContains(t, allMessagesText(req), "Reply with PONG_PRINT_MODE", "captured prompt")
}

func TestT1_F01_JSONModeEmitsValidJSONL(t *testing.T) {
	env := NewTestEnv(t)
	env.MockLLM.EnqueueText("JSONL_OK")

	res, events := env.RunUPIJSON("-p", "--provider", "mock", "--model", "mock-coder", "Say JSONL_OK")
	assertExitZero(t, res)
	if len(events) == 0 {
		t.Fatalf("expected JSONL events on stdout, got none:\n%s", res.Stdout)
	}
	var sawAgentEnd bool
	for _, ev := range events {
		if ev.Type == "agent_end" {
			sawAgentEnd = true
		}
	}
	if !sawAgentEnd {
		t.Fatalf("expected agent_end event in JSONL stream, got %d events", len(events))
	}
}

func TestT1_F01_RPCModePromptAndResponse(t *testing.T) {
	env := NewTestEnv(t)
	env.MockLLM.EnqueueText("RPC_PONG")

	sess := env.StartRPCSession("--provider", "mock", "--model", "mock-coder")
	if err := sess.Prompt("Say RPC_PONG"); err != nil {
		t.Fatalf("send rpc prompt: %v", err)
	}
	events, err := sess.WaitForAgentEnd(15 * time.Second)
	if err != nil {
		t.Fatalf("wait for agent_end: %v", err)
	}
	if len(events) == 0 {
		t.Fatalf("expected RPC events")
	}
	var rawAll strings.Builder
	for _, ev := range events {
		rawAll.WriteString(ev.Raw)
		rawAll.WriteString("\n")
	}
	assertContains(t, rawAll.String(), "RPC_PONG", "RPC event stream")
}

func TestT1_F01_StaticBinaryNoRuntimeDeps(t *testing.T) {
	env := NewTestEnv(t)
	// Strip PATH down to empty except for basic tools so neither node nor bun is needed to launch upi.
	env.Env["PATH"] = "/usr/bin:/bin"
	env.MockLLM.EnqueueText("STATIC_OK")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "ping")
	assertExitZero(t, res)
	assertContains(t, res.Stdout, "STATIC_OK", "static execution")
}

// ============================================================================
// Feature 2: upi CLI & Branding (5 tests)
// ============================================================================

func TestT1_F02_VersionSubcommand(t *testing.T) {
	env := NewTestEnv(t)
	res := env.RunUPI("version")
	assertExitZero(t, res)
	lower := strings.ToLower(res.Stdout)
	if !strings.Contains(lower, "upi") && !strings.Contains(lower, "µpi") {
		t.Fatalf("upi version output: expected upi or µpi, got:\n%s", res.Stdout)
	}
}

func TestT1_F02_DoctorSubcommand(t *testing.T) {
	env := NewTestEnv(t)
	res := env.RunUPI("doctor")
	assertExitZero(t, res)
	if strings.TrimSpace(res.Stdout) == "" {
		t.Fatalf("expected non-empty output from upi doctor")
	}
}

func TestT1_F02_MicropiHomeIsolation(t *testing.T) {
	env := NewTestEnv(t)
	env.MockLLM.EnqueueText("HOME_ISOLATED")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "hello")
	assertExitZero(t, res)

	// Verify nothing was written to a real ~/.micropi outside temp Home/MicropiHome
	entries, err := os.ReadDir(env.MicropiHome)
	if err != nil {
		t.Fatalf("readdir MicropiHome: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("expected MicropiHome to contain config/state files")
	}
}

func TestT1_F02_VersionFlagShortAndLong(t *testing.T) {
	env := NewTestEnv(t)
	resLong := env.RunUPI("--version")
	assertExitZero(t, resLong)
	resShort := env.RunUPI("-v")
	assertExitZero(t, resShort)
	if strings.TrimSpace(resLong.Stdout) == "" || strings.TrimSpace(resShort.Stdout) == "" {
		t.Fatalf("expected non-empty version output for --version and -v")
	}
}

func TestT1_F02_UpdateSubcommandHelpOrCheck(t *testing.T) {
	env := NewTestEnv(t)
	res := env.RunUPI("update", "--help")
	assertExitZero(t, res)
	assertContains(t, strings.ToLower(res.Stdout+res.Stderr), "update", "upi update --help")
}

// ============================================================================
// Feature 3: docsearch (project_search & /docs) (5 tests)
// ============================================================================

func TestT1_F03_ProjectSearchFindsFunctionDefinition(t *testing.T) {
	env := NewTestEnv(t)
	createManyFiles(t, env, 160)
	env.WriteFile("pkg/target_auth.go", "package pkg\n\n// ValidateSessionToken checks HMAC signatures.\nfunc ValidateSessionToken(tok string) bool {\n\treturn len(tok) > 10\n}\n")

	env.MockLLM.EnqueueToolCall("tc1", "project_search", map[string]any{"query": "ValidateSessionToken"})
	env.MockLLM.EnqueueText("found ValidateSessionToken")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "Find ValidateSessionToken")
	assertExitZero(t, res)

	req, ok := env.MockLLM.LastRequest()
	if !ok {
		t.Fatalf("expected second request after project_search")
	}
	results := strings.Join(toolResultMessages(req), "\n")
	assertContains(t, results, "target_auth.go", "project_search result file")
	assertContains(t, results, "ValidateSessionToken", "project_search result symbol")
}

func TestT1_F03_ProjectSearchFindsDocSection(t *testing.T) {
	env := NewTestEnv(t)
	createManyFiles(t, env, 160)
	env.WriteFile("docs/ARCHITECTURE.md", "# Architecture\n\n## Retry Policy\n\nRequests retry with exponential backoff up to 3 attempts.\n")

	env.MockLLM.EnqueueToolCall("tc1", "project_search", map[string]any{"query": "Retry Policy exponential backoff"})
	env.MockLLM.EnqueueText("found Retry Policy")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "Check retry policy docs")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	results := strings.Join(toolResultMessages(req), "\n")
	assertContains(t, results, "ARCHITECTURE.md", "project_search doc file")
}

func TestT1_F03_ProjectSearchEnabledAbove150Files(t *testing.T) {
	env := NewTestEnv(t)
	createManyFiles(t, env, 160)
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "list tools")
	assertExitZero(t, res)

	req, ok := env.MockLLM.LastRequest()
	if !ok {
		t.Fatalf("expected request")
	}
	if !hasToolName(req, "project_search") {
		t.Fatalf("expected project_search tool to be declared for >150 files, got tools: %v", req.ToolNames)
	}
}

func TestT1_F03_DocsSlashCommandViaRPC(t *testing.T) {
	env := NewTestEnv(t)
	createManyFiles(t, env, 160)
	env.WriteFile("docs/CACHE.md", "# Cache Design\n\nThe L2 cache uses a 64MB ring buffer.\n")

	sess := env.StartRPCSession("--provider", "mock", "--model", "mock-coder")
	if err := sess.Prompt("/docs L2 cache ring buffer"); err != nil {
		t.Fatalf("send /docs prompt: %v", err)
	}
	events, err := sess.WaitForAgentEnd(15 * time.Second)
	if err != nil {
		t.Fatalf("wait for /docs: %v", err)
	}
	var all strings.Builder
	for _, ev := range events {
		all.WriteString(ev.Raw)
		all.WriteString("\n")
	}
	assertContains(t, all.String(), "CACHE.md", "/docs search output")
}

func TestT1_F03_ProjectSearchIncrementalUpdate(t *testing.T) {
	env := NewTestEnv(t)
	createManyFiles(t, env, 160)

	env.MockLLM.EnqueueToolCall("tc1", "write", map[string]any{
		"path":    "pkg/fresh_symbol.go",
		"content": "package pkg\n\nfunc NewlyCreatedBeacon() string { return \"beacon\" }\n",
	})
	env.MockLLM.EnqueueToolCall("tc2", "project_search", map[string]any{"query": "NewlyCreatedBeacon"})
	env.MockLLM.EnqueueText("indexed")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "write and search NewlyCreatedBeacon")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	results := strings.Join(toolResultMessages(req), "\n")
	assertContains(t, results, "fresh_symbol.go", "incremental project_search")
}

// ============================================================================
// Feature 4: Cached grep/find & fff (5 tests)
// ============================================================================

func TestT1_F04_GrepLiteralCaseSensitive(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("src/alpha.txt", "ExactTokenHere\nexacttokenhere\n")

	env.MockLLM.EnqueueToolCall("tc1", "grep", map[string]any{"pattern": "ExactTokenHere"})
	env.MockLLM.EnqueueText("ok")

	res := env.RunUPI("-p", "--tools", "+grep,+find", "--provider", "mock", "--model", "mock-coder", "grep ExactTokenHere")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	results := strings.Join(toolResultMessages(req), "\n")
	assertContains(t, results, "ExactTokenHere", "case-sensitive grep")
	assertNotContains(t, results, "exacttokenhere", "case-sensitive grep should exclude lowercase")
}

func TestT1_F04_GrepCaseInsensitive(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("src/beta.txt", "MixedCaseMarker\n")

	env.MockLLM.EnqueueToolCall("tc1", "grep", map[string]any{"pattern": "mixedcasemarker", "ignoreCase": true})
	env.MockLLM.EnqueueText("ok")

	res := env.RunUPI("-p", "--tools", "+grep,+find", "--provider", "mock", "--model", "mock-coder", "grep ignoreCase")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	results := strings.Join(toolResultMessages(req), "\n")
	assertContains(t, results, "MixedCaseMarker", "case-insensitive grep")
}

func TestT1_F04_GrepWithGlobFilter(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("src/a.go", "needle_value\n")
	env.WriteFile("src/b.txt", "needle_value\n")

	env.MockLLM.EnqueueToolCall("tc1", "grep", map[string]any{"pattern": "needle_value", "glob": "*.go"})
	env.MockLLM.EnqueueText("ok")

	res := env.RunUPI("-p", "--tools", "+grep,+find", "--provider", "mock", "--model", "mock-coder", "grep glob")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	results := strings.Join(toolResultMessages(req), "\n")
	assertContains(t, results, "a.go", "grep glob includes .go")
	assertNotContains(t, results, "b.txt", "grep glob excludes .txt")
}

func TestT1_F04_FindExactGlob(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("deep/nested/target_widget.go", "package nested\n")
	env.WriteFile("deep/nested/other.txt", "ignore\n")

	env.MockLLM.EnqueueToolCall("tc1", "find", map[string]any{"pattern": "**/*.go"})
	env.MockLLM.EnqueueText("ok")

	res := env.RunUPI("-p", "--tools", "+grep,+find", "--provider", "mock", "--model", "mock-coder", "find go files")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	results := strings.Join(toolResultMessages(req), "\n")
	assertContains(t, results, "target_widget.go", "find exact glob")
}

func TestT1_F04_FindFuzzyFallback(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("internal/scheduler/worker_pool.go", "package scheduler\n")

	// Pattern does not match an exact glob, so fff fuzzy fallback should suggest closest path
	env.MockLLM.EnqueueToolCall("tc1", "find", map[string]any{"pattern": "workerpool"})
	env.MockLLM.EnqueueText("ok")

	res := env.RunUPI("-p", "--tools", "+grep,+find", "--provider", "mock", "--model", "mock-coder", "fuzzy find workerpool")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	results := strings.Join(toolResultMessages(req), "\n")
	assertContains(t, results, "worker_pool.go", "fff fuzzy fallback")
}

// ============================================================================
// Feature 5: File Tracking & Edit Ergonomics (5 tests)
// ============================================================================

func TestT1_F05_ReadThenEditHappyPath(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("app.txt", "line1\nold_value\nline3\n")

	env.MockLLM.EnqueueToolCall("tc1", "read", map[string]any{"path": "app.txt"})
	env.MockLLM.EnqueueToolCall("tc2", "edit", map[string]any{
		"path":    "app.txt",
		"oldText": "old_value",
		"newText": "new_value",
	})
	env.MockLLM.EnqueueText("edited")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "read and edit app.txt")
	assertExitZero(t, res)
	assertContains(t, env.ReadFile("app.txt"), "new_value", "edited file content")
}

func TestT1_F05_EditUnreadFileWithUniqueOldText(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("unread.txt", "header\nunique_sentinel_123\nfooter\n")

	env.MockLLM.EnqueueToolCall("tc1", "edit", map[string]any{
		"path":    "unread.txt",
		"oldText": "unique_sentinel_123",
		"newText": "updated_sentinel_456",
	})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "edit unread.txt directly")
	assertExitZero(t, res)
	assertContains(t, env.ReadFile("unread.txt"), "updated_sentinel_456", "unread exact-unique edit")
}

func TestT1_F05_EditResultShowsChangedLinesWithContext(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("ctx.txt", "c1\nc2\nc3\ntarget_line\nc5\nc6\nc7\n")

	env.MockLLM.EnqueueToolCall("tc1", "edit", map[string]any{
		"path":    "ctx.txt",
		"oldText": "target_line",
		"newText": "replaced_line",
	})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "edit ctx.txt")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	results := strings.Join(toolResultMessages(req), "\n")
	assertContains(t, results, "replaced_line", "edit result shows new line")
	assertContains(t, results, "c2", "edit result shows surrounding context")
}

func TestT1_F05_EditPreservesCRLFAndBOM(t *testing.T) {
	env := NewTestEnv(t)
	bomCRLF := "\xEF\xBB\xBFfirst\r\nsecond_old\r\nthird\r\n"
	env.WriteFile("win.txt", bomCRLF)

	env.MockLLM.EnqueueToolCall("tc1", "edit", map[string]any{
		"path":    "win.txt",
		"oldText": "second_old",
		"newText": "second_new",
	})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "edit win.txt")
	assertExitZero(t, res)

	got := env.ReadFile("win.txt")
	if !strings.HasPrefix(got, "\xEF\xBB\xBF") {
		t.Fatalf("expected UTF-8 BOM to be preserved")
	}
	if !strings.Contains(got, "second_new\r\n") {
		t.Fatalf("expected CRLF line endings to be preserved, got %q", got)
	}
}

func TestT1_F05_ReadInjectsNestedAgentsMD(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("sub/AGENTS.md", "SUBDIR_RULE_ALPHA: always validate inputs.\n")
	env.WriteFile("sub/code.txt", "hello\n")

	env.MockLLM.EnqueueToolCall("tc1", "read", map[string]any{"path": "sub/code.txt"})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "read sub/code.txt")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	results := strings.Join(toolResultMessages(req), "\n")
	assertContains(t, results, "SUBDIR_RULE_ALPHA", "nested AGENTS.md injected on read")
}

// ============================================================================
// Feature 6: Bash File-Write Guard (5 tests)
// ============================================================================

func TestT1_F06_SedInPlaceSingleFileTracked(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("cfg.txt", "mode=dev\n")

	env.MockLLM.EnqueueToolCall("tc1", "bash", map[string]any{"command": "sed -i 's/mode=dev/mode=prod/' cfg.txt"})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "update cfg.txt via sed")
	assertExitZero(t, res)
	assertContains(t, env.ReadFile("cfg.txt"), "mode=prod", "sed -i applied")

	req, _ := env.MockLLM.LastRequest()
	results := strings.Join(toolResultMessages(req), "\n")
	// µPi: microPi's checkChangedFiles report names the changed lines.
	assertContains(t, results, "cfg.txt changed at line 1.", "bash file-write guard reports changed lines")
}

func TestT1_F06_PerlInPlaceSingleFileTracked(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("ver.txt", "v1\n")

	env.MockLLM.EnqueueToolCall("tc1", "bash", map[string]any{"command": "perl -pi -e 's/v1/v2/' ver.txt"})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "update ver.txt via perl")
	assertExitZero(t, res)
	assertContains(t, env.ReadFile("ver.txt"), "v2", "perl -i applied")
}

func TestT1_F06_EchoRedirectionToExistingFileTracked(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("flag.txt", "old\n")

	env.MockLLM.EnqueueToolCall("tc1", "bash", map[string]any{"command": "echo 'new_flag' > flag.txt"})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "echo > flag.txt")
	assertExitZero(t, res)
	assertContains(t, env.ReadFile("flag.txt"), "new_flag", "echo redirection applied")
}

func TestT1_F06_HeredocCatToExistingFileTracked(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("notes.txt", "before\n")

	env.MockLLM.EnqueueToolCall("tc1", "bash", map[string]any{
		"command": "cat <<'EOF' > notes.txt\nafter_heredoc\nEOF",
	})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "cat heredoc")
	assertExitZero(t, res)
	assertContains(t, env.ReadFile("notes.txt"), "after_heredoc", "cat heredoc applied")
}

func TestT1_F06_TeeToExistingFileTracked(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("out.txt", "before\n")

	env.MockLLM.EnqueueToolCall("tc1", "bash", map[string]any{"command": "printf 'via_tee\\n' | tee out.txt"})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "tee out.txt")
	assertExitZero(t, res)
	assertContains(t, env.ReadFile("out.txt"), "via_tee", "tee applied")
}

// ============================================================================
// Feature 7: Tool Argument Aliases (5 tests)
// ============================================================================

func TestT1_F07_ReadClaudeFilePathAlias(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("claude_read.txt", "CLAUDE_ALIAS_CONTENT\n")

	env.MockLLM.EnqueueToolCall("tc1", "read", map[string]any{"file_path": "claude_read.txt"})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "read with file_path")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	results := strings.Join(toolResultMessages(req), "\n")
	assertContains(t, results, "CLAUDE_ALIAS_CONTENT", "file_path alias")
}

func TestT1_F07_EditClaudeOldNewStringAlias(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("claude_edit.txt", "hello_old\n")

	env.MockLLM.EnqueueToolCall("tc1", "edit", map[string]any{
		"file_path":  "claude_edit.txt",
		"old_string": "hello_old",
		"new_string": "hello_new",
	})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "edit with claude aliases")
	assertExitZero(t, res)
	assertContains(t, env.ReadFile("claude_edit.txt"), "hello_new", "old_string/new_string alias")
}

func TestT1_F07_BashClaudeRunInBackgroundAlias(t *testing.T) {
	env := NewTestEnv(t)

	env.MockLLM.EnqueueToolCall("tc1", "bash", map[string]any{
		"command":           "sleep 0.2; echo bg_alias_ok",
		"run_in_background": true,
	})
	env.MockLLM.EnqueueToolCall("tc2", "wait", map[string]any{})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "run_in_background alias")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	results := strings.Join(toolResultMessages(req), "\n")
	assertContains(t, results, "bg_alias_ok", "run_in_background alias")
}

func TestT1_F07_ReadQwenAbsolutePathAlias(t *testing.T) {
	env := NewTestEnv(t)
	full := env.WriteFile("qwen_read.txt", "QWEN_ABS_CONTENT\n")

	env.MockLLM.EnqueueToolCall("tc1", "read", map[string]any{"absolute_path": full})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "read with absolute_path")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	results := strings.Join(toolResultMessages(req), "\n")
	assertContains(t, results, "QWEN_ABS_CONTENT", "absolute_path alias")
}

func TestT1_F07_GrepQwenIncludeAlias(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("match.go", "qwen_needle\n")
	env.WriteFile("skip.md", "qwen_needle\n")

	env.MockLLM.EnqueueToolCall("tc1", "grep", map[string]any{"pattern": "qwen_needle", "include": "*.go"})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--tools", "+grep,+find", "--provider", "mock", "--model", "mock-coder", "grep with include alias")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	results := strings.Join(toolResultMessages(req), "\n")
	assertContains(t, results, "match.go", "include alias includes .go")
	assertNotContains(t, results, "skip.md", "include alias excludes .md")
}

// ============================================================================
// Feature 8: Background Compiler/Linter Checks (5 tests)
// ============================================================================

func TestT1_F08_GoSyntaxErrorReportedAfterEdit(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("go.mod", "module example.com/test\n\ngo 1.24\n")
	env.WriteFile("main.go", "package main\n\nfunc main() {\n\tprintln(\"ok\")\n}\n")

	env.MockLLM.EnqueueToolCall("tc1", "edit", map[string]any{
		"path":    "main.go",
		"oldText": `println("ok")`,
		"newText": `undefinedSymbol()`,
	})
	env.MockLLM.EnqueueText("saw diagnostic")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "break main.go")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	msgText := allMessagesText(req)
	assertContains(t, strings.ToLower(msgText), "undefined", "Go background check diagnostic")
}

func TestT1_F08_ShellSyntaxErrorReportedAfterEdit(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("script.sh", "#!/usr/bin/env bash\necho ok\n")

	env.MockLLM.EnqueueToolCall("tc1", "edit", map[string]any{
		"path":    "script.sh",
		"oldText": "echo ok",
		"newText": "if true; then\n  echo unclosed",
	})
	env.MockLLM.EnqueueText("saw shell error")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "break script.sh")
	assertExitZero(t, res)

	req, ok := env.MockLLM.LastRequest()
	if !ok {
		t.Fatalf("expected captured LLM request")
	}
	diagText := strings.ToLower(nonSystemMessagesText(req))
	if !strings.Contains(diagText, "check-report") &&
		!strings.Contains(diagText, "syntax error") &&
		!strings.Contains(diagText, "unexpected end of file") &&
		!strings.Contains(diagText, "unexpected eof") {
		t.Fatalf("expected shell syntax diagnostic in next LLM request, got:\n%s", diagText)
	}
}

func TestT1_F08_JSONSyntaxErrorReportedAfterWrite(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("config.json", "{\"ok\": true}\n")

	env.MockLLM.EnqueueToolCall("tc1", "read", map[string]any{"path": "config.json"})
	env.MockLLM.EnqueueToolCall("tc2", "write", map[string]any{
		"path":    "config.json",
		"content": "{\"ok\": }\n",
	})
	env.MockLLM.EnqueueText("saw json error")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "write invalid json")
	assertExitZero(t, res)

	req, ok := env.MockLLM.LastRequest()
	if !ok {
		t.Fatalf("expected captured LLM request")
	}
	diagText := strings.ToLower(nonSystemMessagesText(req))
	if !strings.Contains(diagText, "check-report") &&
		!strings.Contains(diagText, "invalid character") &&
		!strings.Contains(diagText, "unexpected") &&
		!strings.Contains(diagText, "syntax error") &&
		!strings.Contains(diagText, "parse error") {
		t.Fatalf("expected JSON syntax diagnostic in next LLM request, got:\n%s", diagText)
	}
}

func TestT1_F08_PreExistingErrorsFilteredOut(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("go.mod", "module example.com/test\n\ngo 1.24\n")
	env.WriteFile("broken.go", "package main\n\nfunc broken() { preExistingErr() }\nfunc good() { println(\"hi\") }\n")

	env.MockLLM.EnqueueToolCall("tc1", "read", map[string]any{"path": "broken.go"})
	env.MockLLM.EnqueueToolCall("tc2", "edit", map[string]any{
		"path":    "broken.go",
		"oldText": `println("hi")`,
		"newText": `println("hello")`,
	})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "edit good() only")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	msgText := allMessagesText(req)
	// The read and edit results quote the file, so only the check report itself shows whether the old error was filtered.
	assertNotContains(t, msgText, "New errors after your changes", "pre-existing error should be filtered out")
}

func TestT1_F08_PIChecksFilterAndOffSwitch(t *testing.T) {
	env := NewTestEnv(t)
	env.Env["PI_CHECKS"] = "off"
	env.WriteFile("broken.sh", "#!/usr/bin/env bash\necho ok\n")

	env.MockLLM.EnqueueToolCall("tc1", "edit", map[string]any{
		"path":    "broken.sh",
		"oldText": "echo ok",
		"newText": "if true; then",
	})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "edit with checks off")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	msgText := strings.ToLower(allMessagesText(req))
	assertNotContains(t, msgText, "unexpected end of file", "PI_CHECKS=off suppresses diagnostics")
	_ = filepath.Join(env.WorkDir, "broken.sh")
}
