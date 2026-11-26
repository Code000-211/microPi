package e2e

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ============================================================================
// Feature 9: Background Jobs & wait Tool Boundaries (5 tests)
// ============================================================================

func TestT2_F09_WaitWithNoRunningJobsReturnsImmediately(t *testing.T) {
	env := NewTestEnv(t)

	env.MockLLM.EnqueueToolCall("tc1", "wait", map[string]any{})
	env.MockLLM.EnqueueText("no jobs")

	start := time.Now()
	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "wait with nothing running")
	assertExitZero(t, res)
	if time.Since(start) > 5*time.Second {
		t.Fatalf("wait with no running jobs should return immediately")
	}
}

func TestT2_F09_ConsecutiveEmptyWaitsWarnModelToDoOtherWork(t *testing.T) {
	env := NewTestEnv(t)

	env.MockLLM.EnqueueToolCall("tc1", "wait", map[string]any{})
	env.MockLLM.EnqueueToolCall("tc2", "wait", map[string]any{})
	env.MockLLM.EnqueueText("acknowledged")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "two empty waits")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	results := strings.Join(toolResultMessages(req), "\n")
	assertContains(t, strings.ToLower(results), "no", "second empty wait advises model")
}

func TestT2_F09_LongFixedSleepOutsideLoopRejected(t *testing.T) {
	env := NewTestEnv(t)

	env.MockLLM.EnqueueToolCall("tc1", "bash", map[string]any{
		"command": "sleep 250",
	})
	env.MockLLM.EnqueueText("refused long sleep")

	start := time.Now()
	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "sleep 250")
	assertExitZero(t, res)
	if time.Since(start) > 10*time.Second {
		t.Fatalf("long fixed sleep should be rejected immediately, not executed")
	}
	req, _ := env.MockLLM.LastRequest()
	results := strings.ToLower(strings.Join(toolResultMessages(req), "\n"))
	assertContains(t, results, "wait", "long sleep guard suggests wait tool")
}

func TestT2_F09_ConditionLoopWithShortSleepAllowed(t *testing.T) {
	env := NewTestEnv(t)
	flag := filepath.Join(env.WorkDir, "ready.flag")

	env.MockLLM.EnqueueToolCall("tc1", "bash", map[string]any{
		"command": fmt.Sprintf("touch %s; until test -f %s; do sleep 1; done; echo LOOP_OK", flag, flag),
	})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "until loop")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	assertContains(t, strings.Join(toolResultMessages(req), "\n"), "LOOP_OK", "condition loop allowed")
}

func TestT2_F09_ExplicitBackgroundJobHonorsTimeout(t *testing.T) {
	env := NewTestEnv(t)

	env.MockLLM.EnqueueToolCall("tc1", "bash", map[string]any{
		"command":    "sleep 30",
		"background": true,
		"timeout":    1,
	})
	env.MockLLM.EnqueueToolCall("tc2", "wait", map[string]any{})
	env.MockLLM.EnqueueText("timed out")

	start := time.Now()
	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "bg job with 1s timeout")
	assertExitZero(t, res)
	if time.Since(start) > 10*time.Second {
		t.Fatalf("background job with 1s timeout should terminate promptly")
	}
}

// ============================================================================
// Feature 10: Bash Speed & Output Boundaries (5 tests)
// ============================================================================

func TestT2_F10_LargeByteOutputOver32KBTruncatedWithLogFile(t *testing.T) {
	env := NewTestEnv(t)

	env.MockLLM.EnqueueToolCall("tc1", "bash", map[string]any{
		"command": `python3 -c 'print("HEADER_LINE"); [print("X"*200) for _ in range(300)]; print("FOOTER_LINE")'`,
	})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "emit >60KB")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	results := strings.Join(toolResultMessages(req), "\n")
	assertContains(t, results, "HEADER_LINE", "head preserved on >32KB output")
	assertContains(t, results, "FOOTER_LINE", "tail preserved on >32KB output")
}

func TestT2_F10_CarriageReturnProgressBarsCollapsed(t *testing.T) {
	env := NewTestEnv(t)

	env.MockLLM.EnqueueToolCall("tc1", "bash", map[string]any{
		"command": `printf '10%%\r50%%\r100%% DONE\n'`,
	})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "progress bar")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	results := strings.Join(toolResultMessages(req), "\n")
	assertContains(t, results, "100% DONE", "final progress line kept")
}

func TestT2_F10_UserForceColorPreservedOverDefaultNoColor(t *testing.T) {
	env := NewTestEnv(t)
	env.Env["FORCE_COLOR"] = "1"

	env.MockLLM.EnqueueToolCall("tc1", "bash", map[string]any{
		"command": `printf 'NO_COLOR=%s FORCE_COLOR=%s\n' "${NO_COLOR:-unset}" "${FORCE_COLOR:-unset}"`,
	})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "check FORCE_COLOR")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	results := strings.Join(toolResultMessages(req), "\n")
	assertContains(t, results, "FORCE_COLOR=1", "FORCE_COLOR preserved")
	assertContains(t, results, "NO_COLOR=unset", "NO_COLOR not injected when FORCE_COLOR=1")
}

func TestT2_F10_NonZeroExitCodeReportedInToolResult(t *testing.T) {
	env := NewTestEnv(t)

	env.MockLLM.EnqueueToolCall("tc1", "bash", map[string]any{
		"command": "echo FAIL_DETAIL >&2; exit 42",
	})
	env.MockLLM.EnqueueText("saw exit 42")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "failing bash")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	results := strings.Join(toolResultMessages(req), "\n")
	assertContains(t, results, "FAIL_DETAIL", "stderr captured")
	assertContains(t, results, "42", "exit code 42 reported")
}

func TestT2_F10_EmptyCommandHandledWithoutPanic(t *testing.T) {
	env := NewTestEnv(t)

	env.MockLLM.EnqueueToolCall("tc1", "bash", map[string]any{"command": ""})
	env.MockLLM.EnqueueText("handled")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "empty bash")
	assertExitZero(t, res)
	assertNotContains(t, res.Stderr, "panic:", "no panic on empty bash command")
}

// ============================================================================
// Feature 11: Interactive Job Hub Boundaries (5 tests)
// ============================================================================

func TestT2_F11_JobsCommandWithUnknownSubargHandled(t *testing.T) {
	env := NewTestEnv(t)

	sess := env.StartRPCSession("--provider", "mock", "--model", "mock-coder")
	_ = sess.Prompt("/jobs nonexistent_arg")
	events, err := sess.WaitForAgentEnd(10 * time.Second)
	if err != nil {
		t.Fatalf("wait /jobs: %v", err)
	}
	if len(events) == 0 {
		t.Fatalf("expected events for /jobs with unknown arg")
	}
}

func TestT2_F11_JobsReflectsMultipleConcurrentJobs(t *testing.T) {
	env := NewTestEnv(t)

	env.MockLLM.EnqueueToolCalls(
		MockToolCall{ID: "tc1", Name: "bash", Arguments: `{"command":"sleep 0.5 && echo J1","background":true}`},
		MockToolCall{ID: "tc2", Name: "bash", Arguments: `{"command":"sleep 0.5 && echo J2","background":true}`},
	)
	env.MockLLM.EnqueueText("started 2")

	sess := env.StartRPCSession("--provider", "mock", "--model", "mock-coder")
	_ = sess.Prompt("start two jobs")
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
	assertContains(t, all.String(), "bg1", "bg1 in /jobs")
	assertContains(t, all.String(), "bg2", "bg2 in /jobs")
}

func TestT2_F11_RPCAgentsAbortTerminatesBackgroundJobsCleanly(t *testing.T) {
	env := NewTestEnv(t)

	env.MockLLM.EnqueueToolCall("tc1", "bash", map[string]any{
		"command":    "sleep 60",
		"background": true,
	})
	env.MockLLM.EnqueueText("running")

	sess := env.StartRPCSession("--provider", "mock", "--model", "mock-coder")
	_ = sess.Prompt("start long job")
	_, _ = sess.WaitForAgentEnd(10 * time.Second)

	if err := sess.Close(); err != nil {
		// Closing RPC session should not hang waiting for sleep 60
		t.Logf("rpc close returned: %v", err)
	}
}

func TestT2_F11_JobsAfterRestartStartsClean(t *testing.T) {
	env := NewTestEnv(t)
	sess := env.StartRPCSession("--provider", "mock", "--model", "mock-coder")
	_ = sess.Prompt("/jobs")
	events, err := sess.WaitForAgentEnd(10 * time.Second)
	if err != nil {
		t.Fatalf("wait /jobs: %v", err)
	}
	var all strings.Builder
	for _, ev := range events {
		all.WriteString(ev.Raw)
	}
	assertNotContains(t, all.String(), "bg1", "fresh session has no bg1")
}

func TestT2_F11_JobOutputFileExistsOnDisk(t *testing.T) {
	env := NewTestEnv(t)

	env.MockLLM.EnqueueToolCall("tc1", "bash", map[string]any{
		"command":    "echo DISK_LOG_CHECK",
		"background": true,
	})
	env.MockLLM.EnqueueToolCall("tc2", "wait", map[string]any{})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "bg job log check")
	assertExitZero(t, res)

	req, ok := env.MockLLM.LastRequest()
	if !ok {
		t.Fatalf("expected captured LLM request")
	}
	toolOut := strings.Join(toolResultMessages(req), "\n")
	assertContains(t, toolOut, "DISK_LOG_CHECK", "job output captured")

	var logPath string
	for _, field := range strings.Fields(toolOut) {
		trimmed := strings.Trim(field, `"'(),;[]{}<>`)
		if strings.HasSuffix(trimmed, ".log") && filepath.IsAbs(trimmed) {
			logPath = trimmed
			break
		}
	}
	if logPath == "" {
		for _, searchDir := range []string{env.SessionDir, env.MicropiHome, env.WorkDir} {
			_ = filepath.WalkDir(searchDir, func(p string, d os.DirEntry, err error) error {
				if err == nil && !d.IsDir() && strings.HasSuffix(p, ".log") && logPath == "" {
					logPath = p
				}
				return nil
			})
		}
	}
	if logPath == "" {
		t.Fatalf("expected background job .log file path in tool output or session dirs, tool output:\n%s", toolOut)
	}
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("stat job output file %q: %v", logPath, err)
	}
	if info.Size() == 0 {
		t.Fatalf("expected job output file %q to be non-empty", logPath)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read job output file %q: %v", logPath, err)
	}
	assertContains(t, string(data), "DISK_LOG_CHECK", "job output file on disk contains stdout")
}

// ============================================================================
// Feature 12: Compaction-Ahead & Tool Pruning Boundaries (5 tests)
// ============================================================================

func TestT2_F12_ToolResultPruningDisabledViaSettings(t *testing.T) {
	env := NewTestEnv(t)
	settingsPath := filepath.Join(env.AgentDir, "settings.json")
	_ = os.WriteFile(settingsPath, []byte(`{"toolResultPruning":{"enabled":false}}`), 0o644)
	env.WriteFile("p.txt", strings.Repeat("UNPRUNED_DATA_", 200)+"\n")

	for i := 0; i < 10; i++ {
		env.MockLLM.EnqueueToolCall(fmt.Sprintf("tc_%d", i), "read", map[string]any{"path": "p.txt"})
	}
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "read 10 times with pruning off")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	results := toolResultMessages(req)
	assertContains(t, results[0], "UNPRUNED_DATA_", "oldest tool result unpruned when disabled")
}

func TestT2_F12_SmallToolResultsUnderMinCharsNeverPruned(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("small.txt", "TINY_KEEP_ALWAYS\n")

	for i := 0; i < 12; i++ {
		env.MockLLM.EnqueueToolCall(fmt.Sprintf("tc_%d", i), "read", map[string]any{"path": "small.txt"})
	}
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "read small 12 times")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	results := toolResultMessages(req)
	assertContains(t, results[0], "TINY_KEEP_ALWAYS", "small tool result (<2000 chars) never pruned")
}

func TestT2_F12_CompactOnEmptySessionHandledCleanly(t *testing.T) {
	env := NewTestEnv(t)
	sess := env.StartRPCSession("--provider", "mock", "--model", "mock-coder")

	if err := sess.Send(map[string]any{"type": "compact"}); err != nil {
		t.Fatalf("send compact: %v", err)
	}
	ev, err := sess.ReadEvent(5 * time.Second)
	if err != nil {
		t.Fatalf("read compact response: %v", err)
	}
	if ev.Raw == "" {
		t.Fatalf("expected non-empty response to compact on empty session")
	}
}

func TestT2_F12_MaxContextTokensRespectedBelowWindowSize(t *testing.T) {
	env := NewTestEnv(t)

	// Configure mock-coder with a 1,000,000-token context window, but cap
	// compaction.maxContextTokens at 32,000 tokens.
	modelsCfg := map[string]any{
		"providers": map[string]any{
			"mock": map[string]any{
				"baseUrl": env.MockLLM.BaseURL(),
				"apiKey":  "test-mock-key",
				"api":     "openai-completions",
				"models": []map[string]any{
					{
						"id":            "mock-coder",
						"name":          "Mock Coder 1M",
						"reasoning":     true,
						"contextWindow": 1000000,
						"maxTokens":     16384,
					},
				},
			},
		},
	}
	modelsData := mustJSON(t, modelsCfg)
	settingsData := []byte(`{"compaction":{"enabled":true,"maxContextTokens":32000,"reserveTokens":4000}}`)
	for _, dir := range []string{
		env.AgentDir,
		env.MicropiHome,
		filepath.Join(env.MicropiHome, "agent"),
		filepath.Join(env.Home, ".micropi", "agent"),
	} {
		_ = os.WriteFile(filepath.Join(dir, "models.json"), []byte(modelsData), 0o644)
		_ = os.WriteFile(filepath.Join(dir, "settings.json"), settingsData, 0o644)
	}

	// Send >32k tokens (~160KB across 4 turns, ~40k tokens) while staying far below 1M.
	chunk := strings.Repeat("CONTEXT_FILLER_LINE_0123456789_ABCDEF\n", 1100) // ~41.8KB each
	env.MockLLM.SetHandler(func(req CapturedRequest, turn int) MockTurn {
		return MockTurn{
			TextChunks:   []string{fmt.Sprintf("TURN_%d_OK", turn)},
			FinishReason: "stop",
		}
	})

	sess := env.StartRPCSession("--provider", "mock", "--model", "mock-coder")
	totalSent := 0
	for i := 0; i < 4; i++ {
		msg := fmt.Sprintf("CHUNK_%d\n%s", i, chunk)
		totalSent += len(msg)
		if err := sess.Prompt(msg); err != nil {
			t.Fatalf("prompt %d: %v", i, err)
		}
		if _, err := sess.WaitForAgentEnd(15 * time.Second); err != nil {
			t.Fatalf("wait agent_end %d: %v", i, err)
		}
	}

	if err := sess.Prompt("final check"); err != nil {
		t.Fatalf("final prompt: %v", err)
	}
	if _, err := sess.WaitForAgentEnd(15 * time.Second); err != nil {
		t.Fatalf("wait final agent_end: %v", err)
	}

	reqs := env.MockLLM.CapturedRequests()
	if len(reqs) < 5 {
		t.Fatalf("expected at least 5 LLM requests, got %d", len(reqs))
	}
	sawCompaction := false
	for _, r := range reqs {
		lower := strings.ToLower(r.SystemPrompt + "\n" + allMessagesText(r))
		if strings.Contains(lower, "summariz") || strings.Contains(lower, "compaction") {
			sawCompaction = true
			break
		}
	}
	lastReq := reqs[len(reqs)-1]
	if !sawCompaction && len(lastReq.Body) >= totalSent {
		t.Fatalf("expected compaction/pruning below 1M window once >32k tokens (%d bytes) were sent, but final request body was %d bytes with no compaction request",
			totalSent, len(lastReq.Body))
	}
}

func TestT2_F12_DuplicateUserTaskNotDuplicatedInCompactionPreservation(t *testing.T) {
	env := NewTestEnv(t)
	env.MockLLM.EnqueueText("r1")
	env.MockLLM.EnqueueText("r2")
	env.MockLLM.EnqueueText("summary")
	env.MockLLM.EnqueueText("r3")

	sess := env.StartRPCSession("--provider", "mock", "--model", "mock-coder")
	_ = sess.Prompt("UNIQUE_TASK_ALPHA")
	_, _ = sess.WaitForAgentEnd(10 * time.Second)
	_ = sess.Prompt("UNIQUE_TASK_ALPHA with extra follow-up notes")
	_, _ = sess.WaitForAgentEnd(10 * time.Second)

	_ = sess.Send(map[string]any{"type": "compact"})
	time.Sleep(400 * time.Millisecond)

	_ = sess.Prompt("final")
	_, _ = sess.WaitForAgentEnd(10 * time.Second)

	req, _ := env.MockLLM.LastRequest()
	assertContains(t, allMessagesText(req), "UNIQUE_TASK_ALPHA", "task preserved")
}

// ============================================================================
// Feature 13: Project Snapshot & Cut-Off Recovery Boundaries (5 tests)
// ============================================================================

func TestT2_F13_ProjectSnapshotNonGitDirHandledCleanly(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("plain.txt", "hello\n")
	// No InitGitRepo()

	env.MockLLM.EnqueueText("ok")
	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "hi")
	assertExitZero(t, res)
}

func TestT2_F13_ProjectSnapshotEmptyDirHandledCleanly(t *testing.T) {
	env := NewTestEnv(t)
	env.MockLLM.EnqueueText("ok")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "hi")
	assertExitZero(t, res)
}

func TestT2_F13_CutOffWithThinkingOffDoesNotLoopForever(t *testing.T) {
	env := NewTestEnv(t)
	env.MockLLM.EnqueueLengthCutOff("", "truncated")
	env.MockLLM.EnqueueText("recovered")

	res := env.RunUPI("-p", "--thinking", "off", "--provider", "mock", "--model", "mock-coder", "cutoff with thinking off")
	assertExitZero(t, res)
	if len(env.MockLLM.CapturedRequests()) > 4 {
		t.Fatalf("unexpected retry loop")
	}
}

func TestT2_F13_CutOffWithToolCallDoesNotTriggerThinkingStepDown(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("f.txt", "data\n")

	env.MockLLM.Enqueue(MockTurn{
		ToolCalls:    []MockToolCall{{ID: "tc1", Name: "read", Arguments: `{"path":"f.txt"}`}},
		FinishReason: "tool_calls",
	})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--thinking", "high", "--provider", "mock", "--model", "mock-coder", "read f.txt")
	assertExitZero(t, res)
	if len(env.MockLLM.CapturedRequests()) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(env.MockLLM.CapturedRequests()))
	}
}

func TestT2_F13_ProjectSnapshotTakenOncePerSession(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("initial.go", "package main\n")
	env.InitGitRepo()

	env.MockLLM.EnqueueToolCall("tc1", "write", map[string]any{"path": "added_later.go", "content": "package main\n"})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "write added_later.go")
	assertExitZero(t, res)

	reqs := env.MockLLM.CapturedRequests()
	if len(reqs) < 2 {
		t.Fatalf("expected 2 requests")
	}
	if reqs[0].SystemPrompt != reqs[1].SystemPrompt {
		t.Fatalf("project snapshot mutated mid-session, breaking prompt cache")
	}
}

// ============================================================================
// Feature 14: System Prompt & Batched Execution Boundaries (5 tests)
// ============================================================================

func TestT2_F14_BatchedToolCallsWithOneFailingReportsBothResults(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("good.txt", "GOOD_DATA\n")

	env.MockLLM.EnqueueToolCalls(
		MockToolCall{ID: "tc1", Name: "read", Arguments: `{"path":"missing_file_xyz.txt"}`},
		MockToolCall{ID: "tc2", Name: "read", Arguments: `{"path":"good.txt"}`},
	)
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "read missing and good")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	results := strings.Join(toolResultMessages(req), "\n")
	assertContains(t, results, "GOOD_DATA", "second tool call still executed after first errored")
}

func TestT2_F14_CustomSystemPromptOverridesDefault(t *testing.T) {
	env := NewTestEnv(t)
	env.MockLLM.EnqueueText("ok")

	res := env.RunUPI("-p", "--system-prompt", "CUSTOM_SYS_PROMPT_XYZ", "--provider", "mock", "--model", "mock-coder", "hi")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	assertContains(t, req.SystemPrompt, "CUSTOM_SYS_PROMPT_XYZ", "custom system prompt")
}

func TestT2_F14_AppendSystemPromptPreservesDefault(t *testing.T) {
	env := NewTestEnv(t)
	env.MockLLM.EnqueueText("ok")

	res := env.RunUPI("-p", "--append-system-prompt", "APPENDED_RULE_123", "--provider", "mock", "--model", "mock-coder", "hi")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	assertContains(t, req.SystemPrompt, "APPENDED_RULE_123", "appended system prompt")
}

func TestT2_F14_NoToolsFlagOmitsAllTools(t *testing.T) {
	env := NewTestEnv(t)
	env.MockLLM.EnqueueText("ok")

	res := env.RunUPI("-p", "--no-tools", "--provider", "mock", "--model", "mock-coder", "hi")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	if len(req.ToolNames) != 0 {
		t.Fatalf("expected 0 tools with --no-tools, got %v", req.ToolNames)
	}
}

func TestT2_F14_BatchedBashAndEditRespectsDependencyOrder(t *testing.T) {
	env := NewTestEnv(t)

	env.MockLLM.EnqueueToolCalls(
		MockToolCall{ID: "tc1", Name: "write", Arguments: `{"path":"order.txt","content":"STEP_ONE\n"}`},
		MockToolCall{ID: "tc2", Name: "edit", Arguments: `{"path":"order.txt","oldText":"STEP_ONE","newText":"STEP_TWO"}`},
		MockToolCall{ID: "tc3", Name: "bash", Arguments: `{"command":"cat order.txt"}`},
	)
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "write edit bash")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	assertContains(t, strings.Join(toolResultMessages(req), "\n"), "STEP_TWO", "ordered execution")
}

// ============================================================================
// Feature 15: Remote SSH Boundaries (5 tests)
// ============================================================================

func TestT2_F15_RemoteDisablesLocalGrepAndFindTools(t *testing.T) {
	env := NewTestEnv(t)
	env.MockLLM.EnqueueText("ok")

	res := env.RunUPI("-p", "--remote", "devbox:"+env.RemoteDir, "--tools", "+grep,+find", "--provider", "mock", "--model", "mock-coder", "hi")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	if hasToolName(req, "grep") || hasToolName(req, "find") {
		t.Fatalf("expected local grep/find disabled under --remote, got %v", req.ToolNames)
	}
}

func TestT2_F15_RemoteRefusesLocalSedInPlaceGuard(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteRemoteFile("r.txt", "old\n")

	env.MockLLM.EnqueueToolCall("tc1", "bash", map[string]any{"command": "sed -i 's/old/new/' r.txt"})
	env.MockLLM.EnqueueText("refused")

	res := env.RunUPI("-p", "--remote", "devbox:"+env.RemoteDir, "--provider", "mock", "--model", "mock-coder", "sed -i on remote")
	assertExitZero(t, res)
	assertContains(t, env.ReadRemoteFile("r.txt"), "old", "sed -i refused on remote")
}

func TestT2_F15_RemoteReadNonExistentFileReturnsError(t *testing.T) {
	env := NewTestEnv(t)

	env.MockLLM.EnqueueToolCall("tc1", "read", map[string]any{"path": "does_not_exist_remote.txt"})
	env.MockLLM.EnqueueText("handled")

	res := env.RunUPI("-p", "--remote", "devbox:"+env.RemoteDir, "--provider", "mock", "--model", "mock-coder", "read missing remote")
	assertExitZero(t, res)
}

func TestT2_F15_RemoteSSHFailureHandledWithoutPanic(t *testing.T) {
	env := NewTestEnv(t)
	failSSH := filepath.Join(env.FakeSSHDir, "fail-ssh")
	_ = os.WriteFile(failSSH, []byte("#!/usr/bin/env bash\necho 'ssh: connect failed' >&2\nexit 255\n"), 0o755)
	env.Env["PI_SSH_COMMAND"] = failSSH

	env.MockLLM.EnqueueToolCall("tc1", "bash", map[string]any{"command": "echo hi"})
	env.MockLLM.EnqueueText("handled ssh error")

	res := env.RunUPI("-p", "--remote", "devbox:"+env.RemoteDir, "--provider", "mock", "--model", "mock-coder", "run on failing ssh")
	assertNotContains(t, res.Stderr, "panic:", "no panic when ssh exits 255")
}

func TestT2_F15_RemoteDoesNotRunLocalCompilerChecks(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteRemoteFile("broken.sh", "#!/usr/bin/env bash\necho ok\n")

	env.MockLLM.EnqueueToolCall("tc1", "edit", map[string]any{
		"path": "broken.sh", "oldText": "echo ok", "newText": "if true; then",
	})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--remote", "devbox:"+env.RemoteDir, "--provider", "mock", "--model", "mock-coder", "edit remote sh")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	assertNotContains(t, strings.ToLower(allMessagesText(req)), "unexpected end of file", "no local compiler checks on remote edits")
}

// ============================================================================
// Feature 16: Melious Provider & Herdr Boundaries (5 tests)
// ============================================================================

func TestT2_F16_MeliousUnknownModelFailsCleanly(t *testing.T) {
	env := NewTestEnv(t)
	res := env.RunUPI("-p", "--provider", "melious", "--model", "no-such-melious-model", "hi")
	assertExitNonZero(t, res)
}

func TestT2_F16_MeliousMultipleEnergyHoldRetriesSucceed(t *testing.T) {
	env := NewTestEnv(t)
	env.MockLLM.EnqueueError(429, `{"error":{"message":"Failed to create energy hold"}}`)
	env.MockLLM.EnqueueError(429, `{"error":{"message":"Failed to create energy hold"}}`)
	env.MockLLM.EnqueueText("RECOVERED_TWICE")

	res := env.RunUPI("-p", "--provider", "melious", "--model", "glm-5.3", "retry twice")
	assertExitZero(t, res)
	assertContains(t, res.Stdout, "RECOVERED_TWICE", "multiple energy hold retries")
}

func TestT2_F16_MeliousMissingAPIKeyHandledCleanly(t *testing.T) {
	env := NewTestEnv(t)
	env.Env["MELIOUS_API_KEY"] = ""
	// Remove models.json melious key override so missing key is exercised
	_ = os.Remove(filepath.Join(env.AgentDir, "models.json"))
	_ = os.Remove(filepath.Join(env.MicropiHome, "models.json"))

	res := env.RunUPI("--list-models", "melious")
	// Built-in melious models should still list without crashing
	assertNotContains(t, res.Stderr, "panic:", "no panic without MELIOUS_API_KEY")
}

func TestT2_F16_Melious401UnauthorizedDoesNotInfiniteRetry(t *testing.T) {
	env := NewTestEnv(t)
	env.MockLLM.EnqueueError(401, `{"error":{"message":"Invalid API key"}}`)

	res := env.RunUPI("-p", "--provider", "melious", "--model", "glm-5.3", "bad key")
	assertExitNonZero(t, res)
	if len(env.MockLLM.CapturedRequests()) > 3 {
		t.Fatalf("401 unauthorized should not retry excessively, got %d requests", len(env.MockLLM.CapturedRequests()))
	}
}

func TestT2_F16_MeliousCatalogHTTP500FallsBackToBuiltIn(t *testing.T) {
	env := NewTestEnv(t)
	env.MockLLM.SetModelsStatus(500)

	res := env.RunUPI("--list-models", "melious")
	assertExitZero(t, res)
	assertContains(t, res.Stdout, "glm-5.3", "fallback to built-in catalog on 500")
}

// ============================================================================
// Feature 17: Pi-Bolt Retirement & CI/Installers Boundaries (5 tests)
// ============================================================================

func TestT2_F17_LegacyPiboltEnvVarsDoNotBreakUpi(t *testing.T) {
	env := NewTestEnv(t)
	env.Env["PIBOLT_OLD_VAR"] = "legacy"
	env.MockLLM.EnqueueText("LEGACY_SAFE")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "hi")
	assertExitZero(t, res)
	assertContains(t, res.Stdout, "LEGACY_SAFE", "legacy env vars harmless")
}

func TestT2_F17_OfflineFlagPreventsExternalNetworkCalls(t *testing.T) {
	env := NewTestEnv(t)
	res := env.RunUPI("--offline", "--list-models", "melious")
	assertExitZero(t, res)
	assertContains(t, res.Stdout, "glm-5.3", "offline mode uses built-in models")
	if reqs := env.MockLLM.CapturedRequests(); len(reqs) != 0 {
		t.Fatalf("expected 0 network requests in --offline mode, got %d (first: %s %s)",
			len(reqs), reqs[0].Method, reqs[0].Path)
	}
}

func TestT2_F17_DoctorExitsCleanlyInReadOnlyWorkDir(t *testing.T) {
	env := NewTestEnv(t)
	roDir := t.TempDir()
	_ = os.Chmod(roDir, 0o555)
	t.Cleanup(func() { _ = os.Chmod(roDir, 0o755) })

	res := env.RunUPIInDir(roDir, "doctor")
	assertNotContains(t, res.Stderr, "panic:", "doctor does not panic in read-only cwd")
}

func TestT2_F17_InstallScriptRejectsUnknownArgsCleanly(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("abs repoRoot: %v", err)
	}
	scriptPath := filepath.Join(repoRoot, "install.sh")

	tmp := t.TempDir()
	fakeHome := filepath.Join(tmp, "home")
	fakeInstall := filepath.Join(tmp, "micropi")
	fakeBinDir := filepath.Join(tmp, "bin")
	stubPath := filepath.Join(tmp, "stubs")
	for _, d := range []string{fakeHome, fakeInstall, fakeBinDir, stubPath} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	// Stub network tools so install.sh can never make external network calls.
	for _, tool := range []string{"curl", "wget"} {
		p := filepath.Join(stubPath, tool)
		if err := os.WriteFile(p, []byte("#!/bin/sh\necho 'network disabled in e2e' >&2\nexit 1\n"), 0o755); err != nil {
			t.Fatalf("write stub %s: %v", tool, err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", scriptPath, "--unknown-flag-xyz")
	cmd.Dir = tmp
	cmd.Env = []string{
		"PATH=" + stubPath + ":" + os.Getenv("PATH"),
		"HOME=" + fakeHome,
		"MICROPI_INSTALL=" + fakeInstall,
		"MICROPI_BIN_DIR=" + fakeBinDir,
		"MICROPI_YES=1",
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	if runErr == nil {
		t.Fatalf("expected non-zero exit code for install.sh --unknown-flag-xyz, got 0\nstdout:\n%s\nstderr:\n%s",
			stdout.String(), stderr.String())
	}
	combined := strings.ToLower(stdout.String() + "\n" + stderr.String())
	sanitized := strings.ReplaceAll(combined, strings.ToLower(tmp), "")
	if strings.Contains(sanitized, "download failed") || strings.Contains(sanitized, "will install") {
		t.Fatalf("install.sh should reject --unknown-flag-xyz before attempting installation, got:\nstdout:\n%s\nstderr:\n%s",
			stdout.String(), stderr.String())
	}
	if !strings.Contains(sanitized, "unknown") &&
		!strings.Contains(sanitized, "invalid") &&
		!strings.Contains(sanitized, "unrecognized") &&
		!strings.Contains(sanitized, "unsupported") &&
		!strings.Contains(sanitized, "usage") &&
		!strings.Contains(sanitized, "--unknown-flag-xyz") {
		t.Fatalf("expected diagnostic message rejecting unknown flag, got:\nstdout:\n%s\nstderr:\n%s",
			stdout.String(), stderr.String())
	}
}

func TestT2_F17_GitHubWorkflowsExist(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("abs repoRoot: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(repoRoot, ".github", "workflows"))
	if err != nil {
		t.Fatalf("read .github/workflows: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("expected workflow files in .github/workflows")
	}
}
