package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestT4_Scenario_BugFixWithGoDiagnosticsAndTestRun(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("go.mod", "module example.com/calc\n\ngo 1.24\n")
	env.WriteFile("calc.go", "package calc\n\nfunc Add(a, b int) int {\n\treturn a - b // bug\n}\n")
	env.WriteFile("calc_test.go", "package calc\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(2, 3) != 5 {\n\t\tt.Fatalf(\"expected 5, got %d\", Add(2, 3))\n\t}\n}\n")
	env.InitGitRepo()

	// Turn 1: Read calc.go
	env.MockLLM.EnqueueToolCall("tc1", "read", map[string]any{"path": "calc.go"})
	// Turn 2: Edit with a typo (undefined variable) -> triggers background Go check
	env.MockLLM.EnqueueToolCall("tc2", "edit", map[string]any{
		"path":    "calc.go",
		"oldText": "return a - b // bug",
		"newText": "return a + bTypo",
	})
	// Turn 3: Fix typo and run `go test ./...` in one batched response
	env.MockLLM.EnqueueToolCalls(
		MockToolCall{ID: "tc3", Name: "edit", Arguments: `{"path":"calc.go","oldText":"return a + bTypo","newText":"return a + b"}`},
		MockToolCall{ID: "tc4", Name: "bash", Arguments: `{"command":"go test ./..."}`},
	)
	// Turn 4: Final answer
	env.MockLLM.EnqueueText("Fixed Add and verified go test ./... passes.")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "Fix the bug in Add and run tests")
	assertExitZero(t, res)
	assertContains(t, res.Stdout, "Fixed Add", "final summary")
	assertContains(t, env.ReadFile("calc.go"), "return a + b", "bug fixed on disk")

	reqs := env.MockLLM.CapturedRequests()
	if len(reqs) < 4 {
		t.Fatalf("expected >= 4 LLM turns, got %d", len(reqs))
	}
	assertContains(t, strings.ToLower(allMessagesText(reqs[2])), "btypo", "background check reported typo before turn 3")
	assertContains(t, strings.Join(toolResultMessages(reqs[3]), "\n"), "ok  \texample.com/calc", "go test passed in turn 3")
}

func TestT4_Scenario_LargeRepoSearchAndRefactor(t *testing.T) {
	env := NewTestEnv(t)
	createManyFiles(t, env, 165)
	env.WriteFile("go.mod", "module example.com/bigrepo\n\ngo 1.24\n")
	env.WriteFile("pkg/ratelimit.go", "package pkg\n\nfunc RateLimitBurst() int {\n\treturn 10\n}\n")
	env.WriteFile("pkg/ratelimit_test.go", "package pkg\n\nimport \"testing\"\n\nfunc TestRateLimitBurst(t *testing.T) {\n\tif RateLimitBurst() != 25 {\n\t\tt.Fatalf(\"want 25\")\n\t}\n}\n")
	env.InitGitRepo()

	env.MockLLM.EnqueueToolCall("tc1", "project_search", map[string]any{"query": "RateLimitBurst"})
	env.MockLLM.EnqueueToolCalls(
		MockToolCall{ID: "tc2", Name: "edit", Arguments: `{"path":"pkg/ratelimit.go","oldText":"return 10","newText":"return 25"}`},
		MockToolCall{ID: "tc3", Name: "bash", Arguments: `{"command":"go test ./pkg -run TestRateLimitBurst"}`},
	)
	env.MockLLM.EnqueueText("Updated RateLimitBurst to 25 and verified tests.")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "Increase RateLimitBurst to 25 and test")
	assertExitZero(t, res)
	assertContains(t, env.ReadFile("pkg/ratelimit.go"), "return 25", "refactored value")
}

func TestT4_Scenario_BackgroundBuildWaitAndJobHub(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("README.md", "# Service\nstatus: draft\n")

	// Start background build, edit README while it runs, wait for it, then check /jobs
	env.MockLLM.EnqueueToolCall("tc1", "bash", map[string]any{
		"command":    "sleep 0.3 && echo BUILD_ARTIFACT_READY > artifact.out && echo BUILD_OK",
		"background": true,
	})
	env.MockLLM.EnqueueToolCall("tc2", "edit", map[string]any{
		"path":    "README.md",
		"oldText": "status: draft",
		"newText": "status: built",
	})
	env.MockLLM.EnqueueToolCall("tc3", "wait", map[string]any{})
	env.MockLLM.EnqueueText("Build and README update complete.")

	sess := env.StartRPCSession("--provider", "mock", "--model", "mock-coder")
	if err := sess.Prompt("Build in background and update README"); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if _, err := sess.WaitForAgentEnd(15 * time.Second); err != nil {
		t.Fatalf("wait agent_end: %v", err)
	}

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
	assertContains(t, all.String(), "bg1", "/jobs lists completed build")
	assertContains(t, env.ReadFile("README.md"), "status: built", "README updated")
	assertContains(t, env.ReadFile("artifact.out"), "BUILD_ARTIFACT_READY", "background build produced artifact")
}

func TestT4_Scenario_RemoteWorkspaceEditTestAndReturnLocal(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteRemoteFile("service.sh", "#!/usr/bin/env bash\necho VERSION=1\n")
	env.WriteFile("local_report.txt", "pending\n")

	sess := env.StartRPCSession("--provider", "mock", "--model", "mock-coder")

	// Switch to remote
	_ = sess.Prompt("/remote devbox:" + env.RemoteDir)
	_, _ = sess.WaitForAgentEnd(10 * time.Second)

	// Remote read, edit, and bash
	env.MockLLM.EnqueueToolCall("tc1", "read", map[string]any{"path": "service.sh"})
	env.MockLLM.EnqueueToolCalls(
		MockToolCall{ID: "tc2", Name: "edit", Arguments: `{"path":"service.sh","oldText":"VERSION=1","newText":"VERSION=2"}`},
		MockToolCall{ID: "tc3", Name: "bash", Arguments: `{"command":"bash service.sh"}`},
	)
	env.MockLLM.EnqueueText("Remote service updated to VERSION=2.")

	_ = sess.Prompt("Update service.sh to VERSION=2 and run it")
	_, _ = sess.WaitForAgentEnd(15 * time.Second)

	assertContains(t, env.ReadRemoteFile("service.sh"), "VERSION=2", "remote file edited")

	// Switch back to local
	_ = sess.Prompt("/remote off")
	_, _ = sess.WaitForAgentEnd(10 * time.Second)

	env.MockLLM.EnqueueToolCall("tc4", "edit", map[string]any{
		"path":    "local_report.txt",
		"oldText": "pending",
		"newText": "remote-verified-v2",
	})
	env.MockLLM.EnqueueText("Local report updated.")

	_ = sess.Prompt("Update local_report.txt")
	_, _ = sess.WaitForAgentEnd(10 * time.Second)

	assertContains(t, env.ReadFile("local_report.txt"), "remote-verified-v2", "local file updated after /remote off")
}

func TestT4_Scenario_LongSessionCompactionAndPruningPreservesTask(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("large_log.txt", strings.Repeat("LOG_ENTRY_PAYLOAD_0123456789\n", 150))

	sess := env.StartRPCSession("--provider", "mock", "--model", "mock-coder")

	for i := 0; i < 10; i++ {
		env.MockLLM.EnqueueToolCall(fmt.Sprintf("tc_%d", i), "read", map[string]any{"path": "large_log.txt"})
	}
	env.MockLLM.EnqueueText("Inspected all log slices.")

	_ = sess.Prompt("INVARIANT_RULE_555: never drop audit_trail.db. Inspect large_log.txt.")
	if _, err := sess.WaitForAgentEnd(15 * time.Second); err != nil {
		t.Fatalf("wait initial inspection: %v", err)
	}

	// Trigger compaction
	env.MockLLM.EnqueueText("Summary: inspected large_log.txt multiple times.")
	_ = sess.Send(map[string]any{"type": "compact"})
	time.Sleep(500 * time.Millisecond)

	env.MockLLM.EnqueueText("Confirmed invariant still active.")
	_ = sess.Prompt("What is our invariant rule?")
	if _, err := sess.WaitForAgentEnd(10 * time.Second); err != nil {
		t.Fatalf("wait post-compaction: %v", err)
	}

	req, _ := env.MockLLM.LastRequest()
	assertContains(t, allMessagesText(req), "INVARIANT_RULE_555", "original task constraint preserved verbatim after compaction")
}

func TestT4_Scenario_MeliousEnergyHoldAndCutOffRecovery(t *testing.T) {
	env := NewTestEnv(t)

	// 1. Transient 429 energy hold error
	env.MockLLM.EnqueueError(429, `{"error":{"message":"Failed to create energy hold"}}`)
	// 2. Output cut off with reasoning_content
	env.MockLLM.EnqueueLengthCutOff("long chain of thought before cutoff...", "")
	// 3. Recovered batched write + bash verification
	env.MockLLM.EnqueueToolCalls(
		MockToolCall{ID: "tc1", Name: "write", Arguments: `{"path":"result.txt","content":"MELIOUS_SOLVED_42\n"}`},
		MockToolCall{ID: "tc2", Name: "bash", Arguments: `{"command":"cat result.txt"}`},
	)
	// 4. Final answer
	env.MockLLM.EnqueueText("Solved after energy-hold retry and cut-off recovery.")

	res := env.RunUPI("-p", "--provider", "melious", "--model", "glm-5.3", "--thinking", "high", "Write result.txt and verify")
	assertExitZero(t, res)
	assertContains(t, res.Stdout, "Solved after energy-hold retry", "final output")
	assertContains(t, env.ReadFile("result.txt"), "MELIOUS_SOLVED_42", "written file verified")
}
