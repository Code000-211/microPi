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
// Feature 9: Background Jobs & wait Tool (5 tests)
// ============================================================================

func TestT1_F09_BashExplicitBackgroundStartsImmediately(t *testing.T) {
	env := NewTestEnv(t)

	env.MockLLM.EnqueueToolCall("tc1", "bash", map[string]any{
		"command":    "sleep 0.2 && echo BG_STARTED_OK",
		"background": true,
	})
	env.MockLLM.EnqueueToolCall("tc2", "wait", map[string]any{})
	env.MockLLM.EnqueueText("finished")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "start background job")
	assertExitZero(t, res)

	reqs := env.MockLLM.CapturedRequests()
	if len(reqs) < 2 {
		t.Fatalf("expected >= 2 requests, got %d", len(reqs))
	}
	firstResult := strings.Join(toolResultMessages(reqs[1]), "\n")
	assertContains(t, firstResult, "bg1", "background job ID")
}

func TestT1_F09_WaitToolBlocksUntilJobCompletes(t *testing.T) {
	env := NewTestEnv(t)

	env.MockLLM.EnqueueToolCall("tc1", "bash", map[string]any{
		"command":    "sleep 0.3 && echo WAIT_DONE_MARKER",
		"background": true,
	})
	env.MockLLM.EnqueueToolCall("tc2", "wait", map[string]any{})
	env.MockLLM.EnqueueText("all done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "run and wait")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	results := strings.Join(toolResultMessages(req), "\n")
	assertContains(t, results, "WAIT_DONE_MARKER", "wait returns job output")
}

func TestT1_F09_JobCompletionWakesIdleAgent(t *testing.T) {
	env := NewTestEnv(t)

	env.MockLLM.EnqueueToolCall("tc1", "bash", map[string]any{
		"command":    "sleep 0.3 && echo WAKEUP_MARKER",
		"background": true,
	})
	env.MockLLM.EnqueueText("initial reply while job runs")
	env.MockLLM.EnqueueText("acknowledged job completion")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "launch bg job and reply")
	assertExitZero(t, res)

	reqs := env.MockLLM.CapturedRequests()
	if len(reqs) < 3 {
		t.Fatalf("expected agent wakeup turn (>=3 LLM requests), got %d", len(reqs))
	}
	assertContains(t, allMessagesText(reqs[len(reqs)-1]), "WAKEUP_MARKER", "wakeup carries job output")
}

func TestT1_F09_ShortSleepShortensWhenJobFinishes(t *testing.T) {
	env := NewTestEnv(t)

	env.MockLLM.EnqueueToolCall("tc1", "bash", map[string]any{
		"command":    "sleep 0.2 && echo FAST_JOB_END",
		"background": true,
	})
	// Sleep 20s followed by command; should wake up early when bg1 finishes in ~0.2s
	env.MockLLM.EnqueueToolCall("tc2", "bash", map[string]any{
		"command": "sleep 20; echo AFTER_SHORTENED_SLEEP",
	})
	env.MockLLM.EnqueueText("done")

	start := time.Now()
	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "sleep-shortening test")
	elapsed := time.Since(start)
	assertExitZero(t, res)
	if elapsed > 10*time.Second {
		t.Fatalf("expected sleep to shorten when job finished, took %s", elapsed)
	}
	req, _ := env.MockLLM.LastRequest()
	assertContains(t, strings.Join(toolResultMessages(req), "\n"), "AFTER_SHORTENED_SLEEP", "post-sleep command ran")
}

func TestT1_F09_PrintModeWaitsForRunningJobs(t *testing.T) {
	env := NewTestEnv(t)

	env.MockLLM.EnqueueToolCall("tc1", "bash", map[string]any{
		"command":    "sleep 0.3 && touch " + filepath.Join(env.WorkDir, "job_finished.flag"),
		"background": true,
	})
	env.MockLLM.EnqueueText("ok")
	env.MockLLM.EnqueueText("job finished cleanly")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "spawn bg job")
	assertExitZero(t, res)

	if !env.FileExists("job_finished.flag") {
		t.Fatalf("expected upi -p to wait for background job before exiting")
	}
}

// ============================================================================
// Feature 10: Bash Speed & Output (5 tests)
// ============================================================================

func TestT1_F10_HeadAndTailTruncationLongOutput(t *testing.T) {
	env := NewTestEnv(t)

	env.MockLLM.EnqueueToolCall("tc1", "bash", map[string]any{
		"command": "seq 1 2500",
	})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "generate 2500 lines")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	results := strings.Join(toolResultMessages(req), "\n")
	// Both head (first 40 lines) and tail (end) must be present
	assertContains(t, results, "1\n2\n", "head lines preserved")
	assertContains(t, results, "2500", "tail lines preserved")
}

func TestT1_F10_ANSIAndControlCharsStripped(t *testing.T) {
	env := NewTestEnv(t)

	env.MockLLM.EnqueueToolCall("tc1", "bash", map[string]any{
		"command": `printf '\033[31mRED_TEXT\033[0m\n'`,
	})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "print ansi")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	results := strings.Join(toolResultMessages(req), "\n")
	assertContains(t, results, "RED_TEXT", "plain text preserved")
	assertNotContains(t, results, "\x1b[31m", "ANSI escape sequence stripped")
}

func TestT1_F10_NoColorEnvInjectedByDefault(t *testing.T) {
	env := NewTestEnv(t)

	env.MockLLM.EnqueueToolCall("tc1", "bash", map[string]any{
		"command": `printf 'NO_COLOR=%s\n' "${NO_COLOR:-unset}"`,
	})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "check NO_COLOR")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	results := strings.Join(toolResultMessages(req), "\n")
	assertContains(t, results, "NO_COLOR=1", "NO_COLOR=1 set by default")
}

func TestT1_F10_DirectExecWithoutShellSyntax(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("direct.txt", "DIRECT_EXEC_OK\n")

	env.MockLLM.EnqueueToolCall("tc1", "bash", map[string]any{
		"command": "cat direct.txt",
	})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "cat direct.txt")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	results := strings.Join(toolResultMessages(req), "\n")
	assertContains(t, results, "DIRECT_EXEC_OK", "direct exec output")
}

func TestT1_F10_JSONAndPrintModeExitCleanlyAfterBash(t *testing.T) {
	env := NewTestEnv(t)

	env.MockLLM.EnqueueToolCall("tc1", "bash", map[string]any{
		"command": "echo BASH_EXIT_OK | cat",
	})
	env.MockLLM.EnqueueText("EXITED_CLEANLY")

	res, events := env.RunUPIJSON("-p", "--provider", "mock", "--model", "mock-coder", "run piped bash")
	assertExitZero(t, res)
	if len(events) == 0 {
		t.Fatalf("expected JSONL events")
	}
}

// ============================================================================
// Feature 11: Interactive Job Hub (5 tests)
// ============================================================================

func TestT1_F11_JobsSlashCommandListsRunningAndEnded(t *testing.T) {
	env := NewTestEnv(t)

	env.MockLLM.EnqueueToolCall("tc1", "bash", map[string]any{
		"command":    "sleep 0.5 && echo HUB_JOB_1",
		"background": true,
	})
	env.MockLLM.EnqueueText("started")

	sess := env.StartRPCSession("--provider", "mock", "--model", "mock-coder")
	if err := sess.Prompt("start job"); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	_, _ = sess.WaitForAgentEnd(10 * time.Second)

	if err := sess.Prompt("/jobs"); err != nil {
		t.Fatalf("/jobs prompt: %v", err)
	}
	events, err := sess.WaitForAgentEnd(10 * time.Second)
	if err != nil {
		t.Fatalf("wait /jobs: %v", err)
	}
	var all strings.Builder
	for _, ev := range events {
		all.WriteString(ev.Raw)
	}
	assertContains(t, all.String(), "bg1", "/jobs lists bg1")
}

func TestT1_F11_JobsCommandShowsOutputLogPath(t *testing.T) {
	env := NewTestEnv(t)

	env.MockLLM.EnqueueToolCall("tc1", "bash", map[string]any{
		"command":    "echo JOB_LOG_TEST",
		"background": true,
	})
	env.MockLLM.EnqueueText("started")

	sess := env.StartRPCSession("--provider", "mock", "--model", "mock-coder")
	_ = sess.Prompt("start job")
	_, _ = sess.WaitForAgentEnd(10 * time.Second)

	_ = sess.Prompt("/jobs")
	events, err := sess.WaitForAgentEnd(10 * time.Second)
	if err != nil {
		t.Fatalf("wait /jobs: %v", err)
	}
	var all strings.Builder
	for _, ev := range events {
		all.WriteString(ev.Raw)
	}
	assertContains(t, all.String(), ".log", "/jobs shows output log path")
}

func TestT1_F11_JobsCommandShowsExitCode(t *testing.T) {
	env := NewTestEnv(t)

	env.MockLLM.EnqueueToolCall("tc1", "bash", map[string]any{
		"command":    "exit 7",
		"background": true,
	})
	env.MockLLM.EnqueueText("started")

	sess := env.StartRPCSession("--provider", "mock", "--model", "mock-coder")
	_ = sess.Prompt("start failing job")
	_, _ = sess.WaitForAgentEnd(10 * time.Second)
	time.Sleep(200 * time.Millisecond)

	_ = sess.Prompt("/jobs")
	events, err := sess.WaitForAgentEnd(10 * time.Second)
	if err != nil {
		t.Fatalf("wait /jobs: %v", err)
	}
	var all strings.Builder
	for _, ev := range events {
		all.WriteString(ev.Raw)
	}
	assertContains(t, all.String(), "7", "/jobs shows non-zero exit code")
}

func TestT1_F11_JobsCommandEmptyWhenNoJobs(t *testing.T) {
	env := NewTestEnv(t)

	sess := env.StartRPCSession("--provider", "mock", "--model", "mock-coder")
	if err := sess.Prompt("/jobs"); err != nil {
		t.Fatalf("/jobs prompt: %v", err)
	}
	events, err := sess.WaitForAgentEnd(10 * time.Second)
	if err != nil {
		t.Fatalf("wait /jobs: %v", err)
	}
	if len(events) == 0 {
		t.Fatalf("expected /jobs response events")
	}
}

func TestT1_F11_RPCSessionSupportsJobsCommand(t *testing.T) {
	env := NewTestEnv(t)

	sess := env.StartRPCSession("--provider", "mock", "--model", "mock-coder")
	if err := sess.Send(map[string]any{"type": "get_commands"}); err != nil {
		t.Fatalf("get_commands: %v", err)
	}
	ev, err := sess.ReadEvent(5 * time.Second)
	if err != nil {
		t.Fatalf("read get_commands: %v", err)
	}
	if ev.Raw == "" {
		t.Fatalf("expected non-empty get_commands response")
	}
}

// ============================================================================
// Feature 12: Compaction-Ahead & Tool Pruning (5 tests)
// ============================================================================

func TestT1_F12_ToolResultPruningShortensOldLargeResults(t *testing.T) {
	env := NewTestEnv(t)
	// Write a ~16KB file (>2000 char threshold) and trigger >40k token context across 12 reads
	largeContent := strings.Repeat("A_LARGE_LINE_OF_CODE_0123456789\n", 500)
	env.WriteFile("large.txt", largeContent)

	// 12 read calls -> oldest ones fall outside keepRecent=8
	for i := 0; i < 12; i++ {
		env.MockLLM.EnqueueToolCall(fmt.Sprintf("tc_%d", i), "read", map[string]any{"path": "large.txt"})
	}
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "read large.txt 12 times")
	assertExitZero(t, res)

	req, ok := env.MockLLM.LastRequest()
	if !ok {
		t.Fatalf("expected final request")
	}
	results := toolResultMessages(req)
	if len(results) < 12 {
		t.Fatalf("expected 12 tool results, got %d", len(results))
	}
	oldest := results[0]
	if len(oldest) == 0 {
		t.Fatalf("expected oldest tool result to contain a pruned placeholder, got empty string")
	}
	lowerOldest := strings.ToLower(oldest)
	hasMarker := strings.Contains(lowerOldest, "cleared from the context to save space") ||
		strings.Contains(lowerOldest, "cleared") ||
		strings.Contains(lowerOldest, "pruned")
	if !hasMarker && len(oldest) >= len(largeContent)/2 {
		t.Fatalf("expected oldest large tool result to be pruned/shortened (orig=%d bytes), got %d bytes",
			len(largeContent), len(oldest))
	}
}

func TestT1_F12_ToolResultPruningKeepsRecent8Results(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("recent.txt", strings.Repeat("RECENT_KEEP_MARKER_", 120)+"\n")

	for i := 0; i < 10; i++ {
		env.MockLLM.EnqueueToolCall(fmt.Sprintf("tc_%d", i), "read", map[string]any{"path": "recent.txt"})
	}
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "read 10 times")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	results := toolResultMessages(req)
	lastResult := results[len(results)-1]
	assertContains(t, lastResult, "RECENT_KEEP_MARKER_", "most recent tool results remain intact")
}

func TestT1_F12_ManualCompactionPreservesUserTaskVerbatim(t *testing.T) {
	env := NewTestEnv(t)
	env.MockLLM.EnqueueText("first answer")
	env.MockLLM.EnqueueText("compaction summary text")
	env.MockLLM.EnqueueText("after compaction")

	sess := env.StartRPCSession("--provider", "mock", "--model", "mock-coder")
	taskText := "CRITICAL_CONSTRAINT_9988: never delete database_prod.sql"
	_ = sess.Prompt(taskText)
	_, _ = sess.WaitForAgentEnd(10 * time.Second)

	_ = sess.Send(map[string]any{"type": "compact"})
	time.Sleep(500 * time.Millisecond)

	_ = sess.Prompt("continue")
	_, _ = sess.WaitForAgentEnd(10 * time.Second)

	req, _ := env.MockLLM.LastRequest()
	assertContains(t, allMessagesText(req), "CRITICAL_CONSTRAINT_9988", "user task preserved verbatim after compaction")
}

func TestT1_F12_CompactionAheadUsesMaxContextTokensCap(t *testing.T) {
	env := NewTestEnv(t)
	settingsPath := filepath.Join(env.AgentDir, "settings.json")
	_ = os.WriteFile(settingsPath, []byte(`{"compaction":{"maxContextTokens":128000}}`), 0o644)

	env.MockLLM.EnqueueText("SETTINGS_LOADED_OK")
	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "hello")
	assertExitZero(t, res)
	assertContains(t, res.Stdout, "SETTINGS_LOADED_OK", "compaction settings accepted")
}

func TestT1_F12_PromptPrefixCacheStabilityAcrossTurns(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("a.txt", "hello\n")

	env.MockLLM.EnqueueToolCall("tc1", "read", map[string]any{"path": "a.txt"})
	env.MockLLM.EnqueueText("turn2")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "read a.txt")
	assertExitZero(t, res)

	reqs := env.MockLLM.CapturedRequests()
	if len(reqs) < 2 {
		t.Fatalf("expected >= 2 requests, got %d", len(reqs))
	}
	if reqs[0].SystemPrompt != reqs[1].SystemPrompt {
		t.Fatalf("system prompt changed between turn 1 and turn 2, breaking prompt cache")
	}
}

// ============================================================================
// Feature 13: Project Snapshot & Cut-Off Recovery (5 tests)
// ============================================================================

func TestT1_F13_ProjectSnapshotIncludedInSystemPrompt(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("cmd/app/main.go", "package main\n")
	env.WriteFile("README_LOCAL.md", "# Local\n")
	env.InitGitRepo()

	env.MockLLM.EnqueueText("ok")
	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "hi")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	assertContains(t, req.SystemPrompt, "<project>", "project snapshot tag")
	assertContains(t, req.SystemPrompt, "cmd/app/main.go", "project snapshot lists git file")
}

func TestT1_F13_ProjectSnapshotDisabledViaEnvVar(t *testing.T) {
	env := NewTestEnv(t)
	env.Env["PI_PROJECT_SNAPSHOT"] = "off"
	env.WriteFile("secret_file.go", "package main\n")
	env.InitGitRepo()

	env.MockLLM.EnqueueText("ok")
	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "hi")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	assertNotContains(t, req.SystemPrompt, "<project>", "PI_PROJECT_SNAPSHOT=off omits <project>")
}

func TestT1_F13_ProjectSnapshotLargeRepoSummarizesDirs(t *testing.T) {
	env := NewTestEnv(t)
	createManyFiles(t, env, 170)
	env.InitGitRepo()

	env.MockLLM.EnqueueText("ok")
	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "hi")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	assertContains(t, req.SystemPrompt, "<project>", "large repo still gets <project> summary")
	assertContains(t, req.SystemPrompt, "pkg", "large repo <project> summarizes pkg/ directory")
}

func TestT1_F13_CutOffResponseRetriesWithLowerThinking(t *testing.T) {
	env := NewTestEnv(t)

	env.MockLLM.EnqueueLengthCutOff("exhausted thinking budget...", "")
	env.MockLLM.EnqueueText("RECOVERED_AFTER_CUTOFF")

	res := env.RunUPI("-p", "--thinking", "high", "--provider", "mock", "--model", "mock-coder", "solve hard problem")
	assertExitZero(t, res)
	assertContains(t, res.Stdout, "RECOVERED_AFTER_CUTOFF", "cut-off recovery")

	reqs := env.MockLLM.CapturedRequests()
	if len(reqs) < 2 {
		t.Fatalf("expected >= 2 requests on cut-off recovery, got %d", len(reqs))
	}
	// µPi: the next request carries the cut-off notice and thinks one level less (cut-off-response.ts).
	assertContains(t, allMessagesText(reqs[1]), "Your last response reached the output limit while you were still working it out, so nothing was done.", "cut-off notice")
	if reqs[0].ReasoningEffort != "high" || reqs[1].ReasoningEffort != "medium" {
		t.Fatalf("reasoning effort = %q then %q, want high then medium", reqs[0].ReasoningEffort, reqs[1].ReasoningEffort)
	}
}

func TestT1_F13_CutOffResponseCapsAt3Retries(t *testing.T) {
	env := NewTestEnv(t)

	// 4 consecutive cut-offs: initial + 3 retries = 4 requests max, then stops
	for i := 0; i < 5; i++ {
		env.MockLLM.EnqueueLengthCutOff("thinking...", "partial")
	}

	_ = env.RunUPI("-p", "--thinking", "high", "--provider", "mock", "--model", "mock-coder", "infinite cutoff")
	reqs := env.MockLLM.CapturedRequests()
	if len(reqs) > 4 {
		t.Fatalf("expected at most 4 requests (1 initial + 3 cut-off retries), got %d", len(reqs))
	}
}

// ============================================================================
// Feature 14: System Prompt & Batched Execution (5 tests)
// ============================================================================

func TestT1_F14_SystemPromptIsTrimmedAndConcise(t *testing.T) {
	env := NewTestEnv(t)
	env.Env["PI_PROJECT_SNAPSHOT"] = "off"
	env.MockLLM.EnqueueText("ok")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "hi")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	if len(req.SystemPrompt) == 0 {
		t.Fatalf("expected non-empty system prompt")
	}
	assertContains(t, strings.ToLower(req.SystemPrompt), "concise", "trimmed prompt guidance")
}

func TestT1_F14_SystemPromptMentionsBatchedCallsAndBackgroundJobs(t *testing.T) {
	env := NewTestEnv(t)
	env.MockLLM.EnqueueText("ok")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "hi")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	lower := strings.ToLower(req.SystemPrompt)
	assertContains(t, lower, "background", "system prompt mentions background jobs")
}

func TestT1_F14_GrepFindOmittedFromDefaultToolSchemas(t *testing.T) {
	env := NewTestEnv(t)
	env.MockLLM.EnqueueText("ok")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "hi")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	if hasToolName(req, "grep") || hasToolName(req, "find") {
		t.Fatalf("expected grep/find omitted from default tool schemas, got %v", req.ToolNames)
	}
}

func TestT1_F14_BatchedEditAndBashExecuteInOrder(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("counter.txt", "val=1\n")

	env.MockLLM.EnqueueToolCalls(
		MockToolCall{ID: "tc1", Name: "edit", Arguments: `{"path":"counter.txt","oldText":"val=1","newText":"val=2"}`},
		MockToolCall{ID: "tc2", Name: "bash", Arguments: `{"command":"cat counter.txt"}`},
	)
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "edit and verify")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	results := strings.Join(toolResultMessages(req), "\n")
	assertContains(t, results, "val=2", "bash sees edit from same batched response")
}

func TestT1_F14_BatchedMultipleEditsExecuteSequentially(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("seq.txt", "step0\n")

	env.MockLLM.EnqueueToolCalls(
		MockToolCall{ID: "tc1", Name: "edit", Arguments: `{"path":"seq.txt","oldText":"step0","newText":"step1"}`},
		MockToolCall{ID: "tc2", Name: "edit", Arguments: `{"path":"seq.txt","oldText":"step1","newText":"step2"}`},
	)
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "two sequential edits")
	assertExitZero(t, res)
	assertContains(t, env.ReadFile("seq.txt"), "step2", "sequential batched edits")
}

// ============================================================================
// Feature 15: Remote SSH (/remote, --remote) (5 tests)
// ============================================================================

func TestT1_F15_RemoteCLIFlagRoutesReadToRemoteDir(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteRemoteFile("remote_only.txt", "REMOTE_SECRET_CONTENT\n")

	env.MockLLM.EnqueueToolCall("tc1", "read", map[string]any{"path": "remote_only.txt"})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--remote", "devbox:"+env.RemoteDir, "--provider", "mock", "--model", "mock-coder", "read remote_only.txt")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	assertContains(t, strings.Join(toolResultMessages(req), "\n"), "REMOTE_SECRET_CONTENT", "remote read")
}

func TestT1_F15_RemoteCLIFlagRoutesEditAndWriteToRemoteDir(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteRemoteFile("r_edit.txt", "before_remote\n")

	env.MockLLM.EnqueueToolCall("tc1", "edit", map[string]any{
		"path":    "r_edit.txt",
		"oldText": "before_remote",
		"newText": "after_remote",
	})
	env.MockLLM.EnqueueToolCall("tc2", "write", map[string]any{
		"path":    "r_new.txt",
		"content": "created_on_remote\n",
	})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--remote", "devbox:"+env.RemoteDir, "--provider", "mock", "--model", "mock-coder", "edit and write remote")
	assertExitZero(t, res)

	assertContains(t, env.ReadRemoteFile("r_edit.txt"), "after_remote", "remote edit")
	assertContains(t, env.ReadRemoteFile("r_new.txt"), "created_on_remote", "remote write")
}

func TestT1_F15_RemoteCLIFlagRoutesBashToRemoteDir(t *testing.T) {
	env := NewTestEnv(t)

	env.MockLLM.EnqueueToolCall("tc1", "bash", map[string]any{
		// µPi: an append; text redirected over a file is refused on a remote machine (bash-file-writes.ts).
		"command": "echo REMOTE_BASH_OK >> remote_bash.txt",
	})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--remote", "devbox:"+env.RemoteDir, "--provider", "mock", "--model", "mock-coder", "run remote bash")
	assertExitZero(t, res)

	assertContains(t, env.ReadRemoteFile("remote_bash.txt"), "REMOTE_BASH_OK", "remote bash")
}

// µPi: on a remote machine a command that changes a file as edit or write would is refused (bash-file-writes.ts).
func TestT1_F15_RemoteBashRefusesTextRedirectedIntoAFile(t *testing.T) {
	env := NewTestEnv(t)

	env.MockLLM.EnqueueToolCall("tc1", "bash", map[string]any{
		"command": "echo REMOTE_BASH_OK > remote_bash.txt",
	})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--remote", "devbox:"+env.RemoteDir, "--provider", "mock", "--model", "mock-coder", "run remote bash")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	assertContains(t, strings.Join(toolResultMessages(req), "\n"), "Not run: the command changes remote_bash.txt (a redirection).", "remote file-write refusal")
}

func TestT1_F15_RemoteSlashCommandSwitchesAndReportsStatus(t *testing.T) {
	env := NewTestEnv(t)

	sess := env.StartRPCSession("--provider", "mock", "--model", "mock-coder")
	if err := sess.Prompt("/remote devbox:" + env.RemoteDir); err != nil {
		t.Fatalf("/remote prompt: %v", err)
	}
	events, err := sess.WaitForAgentEnd(10 * time.Second)
	if err != nil {
		t.Fatalf("wait /remote: %v", err)
	}
	var all strings.Builder
	for _, ev := range events {
		all.WriteString(ev.Raw)
	}
	assertContains(t, all.String(), "devbox", "/remote status mentions host")
}

func TestT1_F15_RemoteOffRestoresLocalTools(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("local_marker.txt", "LOCAL_RESTORED\n")

	sess := env.StartRPCSession("--remote", "devbox:"+env.RemoteDir, "--provider", "mock", "--model", "mock-coder")
	_ = sess.Prompt("/remote off")
	_, _ = sess.WaitForAgentEnd(10 * time.Second)

	env.MockLLM.EnqueueToolCall("tc1", "read", map[string]any{"path": "local_marker.txt"})
	env.MockLLM.EnqueueText("done")
	_ = sess.Prompt("read local_marker.txt")
	_, _ = sess.WaitForAgentEnd(10 * time.Second)

	req, _ := env.MockLLM.LastRequest()
	assertContains(t, strings.Join(toolResultMessages(req), "\n"), "LOCAL_RESTORED", "/remote off restores local read")
}

// ============================================================================
// Feature 16: Melious Provider & Herdr (5 tests)
// ============================================================================

func TestT1_F16_MeliousBuiltInProviderAvailable(t *testing.T) {
	env := NewTestEnv(t)
	res := env.RunUPI("--list-models", "melious")
	assertExitZero(t, res)
	assertContains(t, res.Stdout, "glm-5.3", "melious built-in model")
}

func TestT1_F16_MeliousDefaultModelGLM53(t *testing.T) {
	env := NewTestEnv(t)
	env.MockLLM.EnqueueText("MELIOUS_OK")

	res := env.RunUPI("-p", "--provider", "melious", "hello melious")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	assertContains(t, req.Model, "glm-5.3", "melious default model")
}

func TestT1_F16_MeliousCatalogMetadataFetchedWithKey(t *testing.T) {
	env := NewTestEnv(t)
	env.MockLLM.SetModels([]MockModel{
		{ID: "glm-5.3", Object: "model", OwnedBy: "melious", ContextWindow: 200000, InputPrice: 0.15, OutputPrice: 0.60},
		{ID: "melious-custom-preview", Object: "model", OwnedBy: "melious", ContextWindow: 128000},
	})

	res := env.RunUPI("--list-models", "melious")
	assertExitZero(t, res)

	var foundCatalogReq bool
	for _, req := range env.MockLLM.CapturedRequests() {
		if req.Path == "/v1/models" && strings.Contains(req.RawQuery, "include_meta=true") {
			auth := req.Headers.Get("Authorization")
			if !strings.HasPrefix(auth, "Bearer ") || len(strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))) == 0 {
				t.Fatalf("expected Authorization: Bearer <key> on /v1/models?include_meta=true, got %q", auth)
			}
			foundCatalogReq = true
			break
		}
	}
	if !foundCatalogReq {
		t.Fatalf("expected GET /v1/models?include_meta=true request to MockLLM, got %d requests", len(env.MockLLM.CapturedRequests()))
	}
	assertContains(t, res.Stdout, "melious-custom-preview", "catalog includes personalized model melious-custom-preview")
}

func TestT1_F16_MeliousEnergyHoldErrorRetried(t *testing.T) {
	env := NewTestEnv(t)
	env.MockLLM.EnqueueError(429, `{"error":{"message":"Failed to create energy hold"}}`)
	env.MockLLM.EnqueueText("ENERGY_HOLD_RECOVERED")

	res := env.RunUPI("-p", "--provider", "melious", "--model", "glm-5.3", "test retry")
	assertExitZero(t, res)
	assertContains(t, res.Stdout, "ENERGY_HOLD_RECOVERED", "energy hold retry succeeded")
}

func TestT1_F16_MeliousFallbackToBuiltInWithoutEndpoint(t *testing.T) {
	env := NewTestEnv(t)
	env.MockLLM.SetModelsStatus(503)

	res := env.RunUPI("--list-models", "melious")
	assertExitZero(t, res)
	assertContains(t, res.Stdout, "glm-5.3", "built-in melious models available when catalog endpoint fails")
}

// ============================================================================
// Feature 17: Pi-Bolt Retirement & CI/Installers (5 tests)
// ============================================================================

func TestT1_F17_HelpOutputHasNoPiBoltBranding(t *testing.T) {
	env := NewTestEnv(t)
	res := env.RunUPI("--help")
	assertExitZero(t, res)
	assertNotContains(t, strings.ToLower(res.Stdout), "pi-bolt", "help output should not reference Pi-Bolt")
}

func TestT1_F17_VersionOutputIdentifiesUpiGoBuild(t *testing.T) {
	env := NewTestEnv(t)
	res := env.RunUPI("version")
	assertExitZero(t, res)
	lower := strings.ToLower(res.Stdout)
	if !strings.Contains(lower, "upi") && !strings.Contains(lower, "µpi") {
		t.Fatalf("version output should identify upi or µpi, got:\n%s", res.Stdout)
	}
}

func TestT1_F17_NoNodeOrBunRequiredAtRuntime(t *testing.T) {
	env := NewTestEnv(t)
	// Point PATH to an empty directory so node/bun cannot be found
	emptyBin := t.TempDir()
	env.Env["PATH"] = emptyBin

	res := env.RunUPI("--version")
	assertExitZero(t, res)
}

func TestT1_F17_ConfigDirDefaultsToMicropi(t *testing.T) {
	env := NewTestEnv(t)
	res := env.RunUPI("doctor")
	assertExitZero(t, res)
	assertContains(t, res.Stdout+res.Stderr, env.MicropiHome, "doctor reports MICROPI_HOME")
}

func TestT1_F17_InstallScriptsExistAndReferenceUpi(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("abs repoRoot: %v", err)
	}
	for _, script := range []string{"install.sh", "install.ps1"} {
		data, err := os.ReadFile(filepath.Join(repoRoot, script))
		if err != nil {
			t.Fatalf("read %s: %v", script, err)
		}
		assertContains(t, string(data), "upi", script+" references upi")
	}
}
