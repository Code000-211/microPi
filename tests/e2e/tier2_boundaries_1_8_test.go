package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ============================================================================
// Feature 1: PiG Base & Build Boundaries (5 tests)
// ============================================================================

func TestT2_F01_InvalidModeFlagExitsNonZero(t *testing.T) {
	env := NewTestEnv(t)
	res := env.RunUPI("--mode", "invalid_mode_xyz")
	assertExitNonZero(t, res)
}

// Pi 1.6.0 (model-resolver.ts) keeps an unknown model id of a known provider as a custom id and only warns.
func TestT2_F01_UnknownModelOfKnownProviderWarns(t *testing.T) {
	env := NewTestEnv(t)
	env.MockLLM.EnqueueText("CUSTOM_ID_OK")
	res := env.RunUPI("-p", "--provider", "mock", "--model", "nonexistent-model-999", "hi")
	assertExitZero(t, res)
	assertContains(t, res.Stderr, `Model "nonexistent-model-999" not found for provider "mock". Using custom model id.`, "unknown model warning")
	assertContains(t, res.Stdout, "CUSTOM_ID_OK", "reply from the custom model id")
}

func TestT2_F01_EmptyPromptInPrintMode(t *testing.T) {
	env := NewTestEnv(t)
	env.MockLLM.EnqueueText("EMPTY_OK")
	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "")
	// Either exits cleanly or reports missing prompt without panicking
	assertNotContains(t, res.Stderr, "panic:", "no Go panic on empty prompt")
}

func TestT2_F01_MissingExtensionPathFailsEarly(t *testing.T) {
	env := NewTestEnv(t)
	res := env.RunUPI("-p", "-e", "./does_not_exist_ext.go", "--provider", "mock", "--model", "mock-coder", "hi")
	assertExitNonZero(t, res)
}

// Pi 1.6.0 (SessionManager.open) starts a new session at a --session path that does not exist yet.
func TestT2_F01_NonExistentSessionPathStartsNewSession(t *testing.T) {
	env := NewTestEnv(t)
	env.MockLLM.EnqueueText("NEW_SESSION_OK")
	path := filepath.Join(env.SessionDir, "missing.jsonl")
	res := env.RunUPI("-p", "--session", path, "--provider", "mock", "--model", "mock-coder", "hi")
	assertExitZero(t, res)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected a new session file at %s: %v", path, err)
	}
}

// ============================================================================
// Feature 2: upi CLI & Branding Boundaries (5 tests)
// ============================================================================

func TestT2_F02_UnknownFlagExitsNonZero(t *testing.T) {
	env := NewTestEnv(t)
	res := env.RunUPI("--definitely-unknown-flag-xyz")
	assertExitNonZero(t, res)
}

// upi runs when MICROPI_HOME does not exist yet. It creates the directory only when it writes there, so nothing here requires it to exist afterwards.
func TestT2_F02_MissingMicropiHomeIsNotRequired(t *testing.T) {
	env := NewTestEnv(t)
	env.Env["MICROPI_HOME"] = filepath.Join(t.TempDir(), "nested", "micropi")
	// The agent directory, and its models.json, otherwise moves with MICROPI_HOME.
	env.Env["MICROPI_CODING_AGENT_DIR"] = filepath.Join(env.MicropiHome, "agent")
	env.MockLLM.EnqueueText("MISSING_HOME_OK")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "hi")
	assertExitZero(t, res)
	assertContains(t, res.Stdout, "MISSING_HOME_OK", "reply with a missing MICROPI_HOME")
}

func TestT2_F02_DoctorHandlesMissingOptionalTools(t *testing.T) {
	env := NewTestEnv(t)
	env.Env["PATH"] = t.TempDir() // empty PATH
	res := env.RunUPI("doctor")
	assertNotContains(t, res.Stderr, "panic:", "doctor must not panic when optional tools are absent")
}

func TestT2_F02_EnvOverrideTakesPrecedenceOverDefaultHome(t *testing.T) {
	env := NewTestEnv(t)
	customHome := filepath.Join(t.TempDir(), "custom-micropi")
	env.Env["MICROPI_HOME"] = customHome

	res := env.RunUPI("doctor")
	assertExitZero(t, res)
	assertContains(t, res.Stdout+res.Stderr, customHome, "MICROPI_HOME override respected")
}

func TestT2_F02_NoSessionFlagSkipsSessionPersistence(t *testing.T) {
	env := NewTestEnv(t)
	env.MockLLM.EnqueueText("EPHEMERAL_OK")

	res := env.RunUPI("-p", "--no-session", "--provider", "mock", "--model", "mock-coder", "ephemeral run")
	assertExitZero(t, res)

	entries, err := os.ReadDir(env.SessionDir)
	if err != nil {
		t.Fatalf("readdir SessionDir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 session files with --no-session, got %d", len(entries))
	}
}

// ============================================================================
// Feature 3: docsearch Boundaries (5 tests)
// ============================================================================

func TestT2_F03_ProjectSearchAutoDisabledAt150OrFewerFiles(t *testing.T) {
	env := NewTestEnv(t)
	createManyFiles(t, env, 10)
	env.MockLLM.EnqueueText("ok")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "hi")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	if hasToolName(req, "project_search") {
		t.Fatalf("expected project_search to be auto-disabled for <=150 files, got tools: %v", req.ToolNames)
	}
}

func TestT2_F03_ProjectSearchEmptyQueryHandled(t *testing.T) {
	env := NewTestEnv(t)
	createManyFiles(t, env, 160)

	env.MockLLM.EnqueueToolCall("tc1", "project_search", map[string]any{"query": ""})
	env.MockLLM.EnqueueText("handled")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "empty query")
	assertExitZero(t, res)
	assertNotContains(t, res.Stderr, "panic:", "no panic on empty query")
}

func TestT2_F03_ProjectSearchNoMatchesReturnsCleanMessage(t *testing.T) {
	env := NewTestEnv(t)
	createManyFiles(t, env, 160)

	env.MockLLM.EnqueueToolCall("tc1", "project_search", map[string]any{"query": "ZZZ_NONEXISTENT_SYMBOL_987654"})
	env.MockLLM.EnqueueText("no matches")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "search nonexistent")
	assertExitZero(t, res)
}

func TestT2_F03_DocsSlashCommandEmptyArgsHandled(t *testing.T) {
	env := NewTestEnv(t)
	createManyFiles(t, env, 160)

	sess := env.StartRPCSession("--provider", "mock", "--model", "mock-coder")
	if err := sess.Prompt("/docs"); err != nil {
		t.Fatalf("prompt /docs: %v", err)
	}
	events, err := sess.WaitForAgentEnd(10 * time.Second)
	if err != nil {
		t.Fatalf("wait /docs: %v", err)
	}
	if len(events) == 0 {
		t.Fatalf("expected /docs usage or response events")
	}
}

func TestT2_F03_ProjectSearchIgnoresGitignoredDirs(t *testing.T) {
	env := NewTestEnv(t)
	createManyFiles(t, env, 160)
	env.WriteFile(".gitignore", "ignored_dir/\n")
	env.WriteFile("ignored_dir/secret.go", "package ignored\n\nfunc HiddenIgnoredSymbol() {}\n")
	env.InitGitRepo()

	env.MockLLM.EnqueueToolCall("tc1", "project_search", map[string]any{"query": "HiddenIgnoredSymbol"})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "search ignored")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	results := strings.Join(toolResultMessages(req), "\n")
	assertNotContains(t, results, "ignored_dir/secret.go", "docsearch ignores gitignored directory")
}

// ============================================================================
// Feature 4: Cached grep/find & fff Boundaries (5 tests)
// ============================================================================

func TestT2_F04_GrepNoMatchesReturnsCleanResult(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("a.txt", "nothing here\n")

	env.MockLLM.EnqueueToolCall("tc1", "grep", map[string]any{"pattern": "ABSENT_TOKEN_XYZ"})
	env.MockLLM.EnqueueText("none")

	res := env.RunUPI("-p", "--tools", "+grep,+find", "--provider", "mock", "--model", "mock-coder", "grep absent")
	assertExitZero(t, res)
}

func TestT2_F04_GrepLiteralSpecialChars(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("expr.txt", "arr[0] = (a + b) * c\n")

	env.MockLLM.EnqueueToolCall("tc1", "grep", map[string]any{"pattern": "arr[0]", "literal": true})
	env.MockLLM.EnqueueText("found")

	res := env.RunUPI("-p", "--tools", "+grep,+find", "--provider", "mock", "--model", "mock-coder", "grep literal brackets")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	assertContains(t, strings.Join(toolResultMessages(req), "\n"), "arr[0]", "literal bracket match")
}

func TestT2_F04_FindRespectsGitignore(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile(".gitignore", "build_out/\n")
	env.WriteFile("build_out/artifact.go", "package build\n")
	env.WriteFile("src/keep.go", "package src\n")
	env.InitGitRepo()

	env.MockLLM.EnqueueToolCall("tc1", "find", map[string]any{"pattern": "**/*.go"})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--tools", "+grep,+find", "--provider", "mock", "--model", "mock-coder", "find go")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	results := strings.Join(toolResultMessages(req), "\n")
	assertContains(t, results, "keep.go", "tracked file included")
	assertNotContains(t, results, "artifact.go", "gitignored file excluded")
}

func TestT2_F04_CacheInvalidatedAfterBashCreatesFile(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("existing.txt", "base\n")

	env.MockLLM.EnqueueToolCall("tc1", "grep", map[string]any{"pattern": "GENERATED_AFTER_BASH"})
	env.MockLLM.EnqueueToolCall("tc2", "bash", map[string]any{"command": "echo 'GENERATED_AFTER_BASH' > generated.txt"})
	env.MockLLM.EnqueueToolCall("tc3", "grep", map[string]any{"pattern": "GENERATED_AFTER_BASH"})
	env.MockLLM.EnqueueText("found")

	res := env.RunUPI("-p", "--tools", "+grep,+find", "--provider", "mock", "--model", "mock-coder", "grep bash grep")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	results := strings.Join(toolResultMessages(req), "\n")
	assertContains(t, results, "generated.txt", "cache invalidated after bash created file")
}

func TestT2_F04_CacheInvalidatedAfterBashModifiesFile(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("mutable.txt", "before_change\n")

	env.MockLLM.EnqueueToolCall("tc1", "grep", map[string]any{"pattern": "before_change"})
	env.MockLLM.EnqueueToolCall("tc2", "bash", map[string]any{"command": "sed -i 's/before_change/after_change/' mutable.txt"})
	env.MockLLM.EnqueueToolCall("tc3", "grep", map[string]any{"pattern": "after_change"})
	env.MockLLM.EnqueueText("found")

	res := env.RunUPI("-p", "--tools", "+grep,+find", "--provider", "mock", "--model", "mock-coder", "grep sed grep")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	results := strings.Join(toolResultMessages(req), "\n")
	assertContains(t, results, "after_change", "cache invalidated after bash modified file")
}

// ============================================================================
// Feature 5: File Tracking & Edit Ergonomics Boundaries (5 tests)
// ============================================================================

func TestT2_F05_EditUnreadFileWithNonUniqueOldTextRefused(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("dup.txt", "dup_line\nmiddle\ndup_line\n")

	env.MockLLM.EnqueueToolCall("tc1", "edit", map[string]any{
		"path":    "dup.txt",
		"oldText": "dup_line",
		"newText": "changed",
	})
	env.MockLLM.EnqueueText("refused as expected")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "edit ambiguous unread file")
	assertExitZero(t, res)
	if env.ReadFile("dup.txt") != "dup_line\nmiddle\ndup_line\n" {
		t.Fatalf("expected dup.txt to remain unmodified on ambiguous edit")
	}
}

func TestT2_F05_EditNoMatchShowsClosestLinesWithLineNumbers(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("close.txt", "alpha\nbeta_value_actual\ngamma\n")

	env.MockLLM.EnqueueToolCall("tc1", "read", map[string]any{"path": "close.txt"})
	env.MockLLM.EnqueueToolCall("tc2", "edit", map[string]any{
		"path":    "close.txt",
		"oldText": "beta_value_typo",
		"newText": "beta_value_new",
	})
	env.MockLLM.EnqueueText("saw closest match")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "edit with typo")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	results := strings.Join(toolResultMessages(req), "\n")
	assertContains(t, results, "beta_value_actual", "closest text hint shown on missed edit")
}

func TestT2_F05_WriteOverUnreadExistingFileRefusedWithContent(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("existing_unread.txt", "EXISTING_IMPORTANT_LINE\n")

	env.MockLLM.EnqueueToolCall("tc1", "write", map[string]any{
		"path":    "existing_unread.txt",
		"content": "overwritten\n",
	})
	env.MockLLM.EnqueueText("saw refusal")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "overwrite unread file")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	results := strings.Join(toolResultMessages(req), "\n")
	assertContains(t, results, "EXISTING_IMPORTANT_LINE", "refused write returns existing content")
	assertContains(t, env.ReadFile("existing_unread.txt"), "EXISTING_IMPORTANT_LINE", "file not overwritten")
}

func TestT2_F05_EditSeekSequenceReindentsTabsAndSpaces(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("tabs.go", "package main\n\nfunc f() {\n\toldCall()\n}\n")

	env.MockLLM.EnqueueToolCall("tc1", "read", map[string]any{"path": "tabs.go"})
	// Model sends spaces instead of tabs for indentation
	env.MockLLM.EnqueueToolCall("tc2", "edit", map[string]any{
		"path":    "tabs.go",
		"oldText": "    oldCall()",
		"newText": "    newCall()",
	})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "edit with space indent")
	assertExitZero(t, res)
	assertContains(t, env.ReadFile("tabs.go"), "\tnewCall()", "seek_sequence reindents to file tabs")
}

func TestT2_F05_TouchSameContentDoesNotTriggerStaleRefusal(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("touched.txt", "keep_me\n")

	env.MockLLM.EnqueueToolCall("tc1", "read", map[string]any{"path": "touched.txt"})
	env.MockLLM.EnqueueToolCall("tc2", "bash", map[string]any{"command": "sleep 0.05 && touch touched.txt"})
	env.MockLLM.EnqueueToolCall("tc3", "edit", map[string]any{
		"path":    "touched.txt",
		"oldText": "keep_me",
		"newText": "updated_after_touch",
	})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "read touch edit")
	assertExitZero(t, res)
	assertContains(t, env.ReadFile("touched.txt"), "updated_after_touch", "touch with same content not treated as stale")
}

// ============================================================================
// Feature 6: Bash File-Write Guard Boundaries (5 tests)
// ============================================================================

func TestT2_F06_SedMultiFileGlobAllowedWithoutEditGuard(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("d/a.txt", "old\n")
	env.WriteFile("d/b.txt", "old\n")

	env.MockLLM.EnqueueToolCall("tc1", "bash", map[string]any{"command": "sed -i 's/old/new/' d/*.txt"})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "multi-file sed")
	assertExitZero(t, res)
	assertContains(t, env.ReadFile("d/a.txt"), "new", "multi-file sed a.txt")
	assertContains(t, env.ReadFile("d/b.txt"), "new", "multi-file sed b.txt")
}

func TestT2_F06_AppendRedirectionAllowedWithoutGuard(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("log.txt", "line1\n")

	env.MockLLM.EnqueueToolCall("tc1", "bash", map[string]any{"command": "echo line2 >> log.txt"})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "append log.txt")
	assertExitZero(t, res)
	assertContains(t, env.ReadFile("log.txt"), "line1\nline2", "append preserved line1 and added line2")
}

func TestT2_F06_TmpFileWriteExemptFromGuard(t *testing.T) {
	env := NewTestEnv(t)
	tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("upi_e2e_tmp_%d.txt", time.Now().UnixNano()))
	t.Cleanup(func() { _ = os.Remove(tmpFile) })

	env.MockLLM.EnqueueToolCall("tc1", "bash", map[string]any{"command": fmt.Sprintf("echo tmp_ok > %s && cat %s", tmpFile, tmpFile)})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "write /tmp file")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	assertContains(t, strings.Join(toolResultMessages(req), "\n"), "tmp_ok", "/tmp write succeeded")
}

func TestT2_F06_ProgramStdoutRedirectExemptFromGuard(t *testing.T) {
	env := NewTestEnv(t)

	env.MockLLM.EnqueueToolCall("tc1", "bash", map[string]any{"command": "date > build.log && wc -l build.log"})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "redirect command output")
	assertExitZero(t, res)
	ensureFileNotEmpty(t, env, "build.log")
}

func TestT2_F06_QuotedCommandOverSSHNotCaughtLocally(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteRemoteFile("q.txt", "before\n")

	env.MockLLM.EnqueueToolCall("tc1", "bash", map[string]any{
		"command": env.Env["PI_SSH_COMMAND"] + ` devbox "sed -i 's/before/after/' q.txt"`,
	})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "quoted ssh sed")
	assertExitZero(t, res)
	assertContains(t, env.ReadRemoteFile("q.txt"), "after", "quoted ssh command ran on remote")
}

// ============================================================================
// Feature 7: Tool Argument Aliases Boundaries (5 tests)
// ============================================================================

func TestT2_F07_CanonicalArgTakesPrecedenceOverAlias(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("canonical.txt", "CANONICAL_WIN\n")
	env.WriteFile("alias.txt", "ALIAS_LOSE\n")

	env.MockLLM.EnqueueToolCall("tc1", "read", map[string]any{
		"path":      "canonical.txt",
		"file_path": "alias.txt",
	})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "canonical precedence")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	assertContains(t, strings.Join(toolResultMessages(req), "\n"), "CANONICAL_WIN", "canonical path wins over file_path")
}

func TestT2_F07_WriteClaudeFilePathAlias(t *testing.T) {
	env := NewTestEnv(t)

	env.MockLLM.EnqueueToolCall("tc1", "write", map[string]any{
		"file_path": "via_alias_write.txt",
		"content":   "WRITTEN_VIA_FILE_PATH\n",
	})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "write with file_path")
	assertExitZero(t, res)
	assertContains(t, env.ReadFile("via_alias_write.txt"), "WRITTEN_VIA_FILE_PATH", "write file_path alias")
}

func TestT2_F07_BashQwenIsBackgroundAlias(t *testing.T) {
	env := NewTestEnv(t)

	env.MockLLM.EnqueueToolCall("tc1", "bash", map[string]any{
		"command":       "sleep 0.2 && echo QWEN_BG_OK",
		"is_background": true,
	})
	env.MockLLM.EnqueueToolCall("tc2", "wait", map[string]any{})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "is_background alias")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	assertContains(t, strings.Join(toolResultMessages(req), "\n"), "QWEN_BG_OK", "is_background alias")
}

func TestT2_F07_EditMissingRequiredArgsReturnsError(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("x.txt", "hello\n")

	env.MockLLM.EnqueueToolCall("tc1", "edit", map[string]any{"file_path": "x.txt"})
	env.MockLLM.EnqueueText("handled error")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "missing oldText")
	assertExitZero(t, res)
	assertContains(t, env.ReadFile("x.txt"), "hello", "file untouched on invalid edit args")
}

func TestT2_F07_AliasWithEmptyValueHandledCleanly(t *testing.T) {
	env := NewTestEnv(t)

	env.MockLLM.EnqueueToolCall("tc1", "read", map[string]any{"file_path": ""})
	env.MockLLM.EnqueueText("handled empty path")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "empty file_path")
	assertExitZero(t, res)
}

// ============================================================================
// Feature 8: Background Compiler/Linter Checks Boundaries (5 tests)
// ============================================================================

func TestT2_F08_RapidSuccessiveEditsDebouncedToLatestCheck(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("go.mod", "module example.com/test\n\ngo 1.24\n")
	env.WriteFile("rapid.go", "package main\n\nfunc main() {\n\tprintln(\"v0\")\n}\n")

	env.MockLLM.EnqueueToolCalls(
		MockToolCall{ID: "tc1", Name: "edit", Arguments: `{"path":"rapid.go","oldText":"println(\"v0\")","newText":"brokenCall()"}`},
		MockToolCall{ID: "tc2", Name: "edit", Arguments: `{"path":"rapid.go","oldText":"brokenCall()","newText":"println(\"fixed\")"}`},
	)
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "break then fix rapidly")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	// The first edit's result quotes brokenCall(), so only a check report would show a stale error.
	assertNotContains(t, allMessagesText(req), "New errors after your changes", "overtaken broken edit should not report stale error")
}

func TestT2_F08_PIChecksWaitZeroDoesNotBlockOnSlowCheck(t *testing.T) {
	env := NewTestEnv(t)
	env.Env["PI_CHECKS_WAIT"] = "0"
	env.WriteFile("main.sh", "#!/usr/bin/env bash\necho ok\n")

	env.MockLLM.EnqueueToolCall("tc1", "edit", map[string]any{
		"path":    "main.sh",
		"oldText": "echo ok",
		"newText": "echo ok2",
	})
	env.MockLLM.EnqueueText("done")

	start := time.Now()
	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "edit with PI_CHECKS_WAIT=0")
	assertExitZero(t, res)
	if time.Since(start) > 10*time.Second {
		t.Fatalf("PI_CHECKS_WAIT=0 should return promptly")
	}
}

func TestT2_F08_DiagnosticCapAt15ErrorsWithAndNMore(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("go.mod", "module example.com/test\n\ngo 1.24\n")
	env.WriteFile("many.go", "package main\n\nfunc f() {}\n")

	var brokenBody strings.Builder
	brokenBody.WriteString("package main\n\nfunc f() {\n")
	for i := 0; i < 20; i++ {
		brokenBody.WriteString(fmt.Sprintf("\tundefSym%02d()\n", i))
	}
	brokenBody.WriteString("}\n")

	env.MockLLM.EnqueueToolCall("tc1", "read", map[string]any{"path": "many.go"})
	env.MockLLM.EnqueueToolCall("tc2", "write", map[string]any{"path": "many.go", "content": brokenBody.String()})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "write 20 errors")
	assertExitZero(t, res)

	req, ok := env.MockLLM.LastRequest()
	if !ok {
		t.Fatalf("expected captured LLM request")
	}
	diagText := strings.ToLower(nonSystemMessagesText(req))
	assertContains(t, diagText, "undefsym", "reports compiler diagnostics")
	if !strings.Contains(diagText, "and ") || !strings.Contains(diagText, "more") {
		t.Fatalf("expected diagnostic cap truncation message ('and N more'), got:\n%s", diagText)
	}
	if count := strings.Count(diagText, "undefsym"); count > 15 {
		t.Fatalf("expected at most 15 reported diagnostics, got %d:\n%s", count, diagText)
	}
}

func TestT2_F08_FixedAndReintroducedErrorReportedAgain(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("reintro.sh", "#!/usr/bin/env bash\necho ok\n")

	env.MockLLM.EnqueueToolCall("tc1", "edit", map[string]any{
		"path": "reintro.sh", "oldText": "echo ok", "newText": "if true; then",
	})
	env.MockLLM.EnqueueToolCall("tc2", "edit", map[string]any{
		"path": "reintro.sh", "oldText": "if true; then", "newText": "echo fixed",
	})
	env.MockLLM.EnqueueToolCall("tc3", "edit", map[string]any{
		"path": "reintro.sh", "oldText": "echo fixed", "newText": "if true; then",
	})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "break fix break")
	assertExitZero(t, res)

	reqs := env.MockLLM.CapturedRequests()
	if len(reqs) < 4 {
		t.Fatalf("expected at least 4 LLM requests, got %d", len(reqs))
	}
	lastReq := reqs[len(reqs)-1]
	if results := toolResultMessages(lastReq); len(results) < 3 {
		t.Fatalf("expected at least 3 tool results in final request, got %d", len(results))
	}
	// µPi: the report is a check-report message after the last tool result (agent-session.ts _sendCheckReport), not text pasted into
	// that result; the run that would end after the third edit waits for its check and goes on with the report.
	var afterTools strings.Builder
	for _, m := range lastReq.Messages {
		if m.Role == "tool" {
			afterTools.Reset()
			continue
		}
		afterTools.WriteString(m.ContentText())
		afterTools.WriteString("\n")
	}
	reported := strings.ToLower(afterTools.String())
	if !strings.Contains(reported, "new errors after your changes") ||
		(!strings.Contains(reported, "syntax error") &&
			!strings.Contains(reported, "unexpected end of file") &&
			!strings.Contains(reported, "unexpected eof")) {
		t.Fatalf("expected the reintroduced shell syntax error reported after the last tool result, got:\n%s", reported)
	}
}

func TestT2_F08_PIChecksSubsetFilterIgnoresOtherLanguages(t *testing.T) {
	env := NewTestEnv(t)
	env.Env["PI_CHECKS"] = "json"
	env.WriteFile("ignored.sh", "#!/usr/bin/env bash\necho ok\n")

	env.MockLLM.EnqueueToolCall("tc1", "edit", map[string]any{
		"path":    "ignored.sh",
		"oldText": "echo ok",
		"newText": "if true; then",
	})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "edit shell with PI_CHECKS=json")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	assertNotContains(t, strings.ToLower(allMessagesText(req)), "unexpected end of file", "PI_CHECKS=json skips shell checks")
}
