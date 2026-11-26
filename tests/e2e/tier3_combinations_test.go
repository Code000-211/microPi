package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestT3_DocsearchPlusFileTracking(t *testing.T) {
	env := NewTestEnv(t)
	createManyFiles(t, env, 160)
	env.WriteFile("pkg/billing.go", "package pkg\n\nfunc ComputeInvoiceTotal(a, b int) int {\n\treturn a + b\n}\n")

	env.MockLLM.EnqueueToolCall("tc1", "project_search", map[string]any{"query": "ComputeInvoiceTotal"})
	env.MockLLM.EnqueueToolCall("tc2", "edit", map[string]any{
		"path":    "pkg/billing.go",
		"oldText": "return a + b",
		"newText": "return a + b + 1",
	})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "find and edit ComputeInvoiceTotal")
	assertExitZero(t, res)
	assertContains(t, env.ReadFile("pkg/billing.go"), "return a + b + 1", "docsearch + exact unread edit")
}

func TestT3_ArgumentAliasesPlusEditPlusChecks(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("go.mod", "module example.com/t3\n\ngo 1.24\n")
	env.WriteFile("svc.go", "package main\n\nfunc run() {\n\tprintln(\"ok\")\n}\n")

	env.MockLLM.EnqueueToolCall("tc1", "edit", map[string]any{
		"file_path":  "svc.go",
		"old_string": `println("ok")`,
		"new_string": `missingHelperFunc()`,
	})
	env.MockLLM.EnqueueText("saw diagnostic")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "edit with aliases and trigger check")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	assertContains(t, strings.ToLower(allMessagesText(req)), "undefined", "aliases + edit + Go check")
}

func TestT3_BashFileWritesPlusFileTrackingPlusChecks(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("go.mod", "module example.com/t3\n\ngo 1.24\n")
	env.WriteFile("handler.go", "package main\n\nfunc handle() {\n\tprintln(\"v1\")\n}\n")

	// Modify via sed -i -> filetracking updates read state & background checks run
	env.MockLLM.EnqueueToolCall("tc1", "bash", map[string]any{
		"command": `sed -i 's/println("v1")/brokenCall()/' handler.go`,
	})
	env.MockLLM.EnqueueText("saw check after sed -i")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "sed -i handler.go")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	assertContains(t, strings.ToLower(allMessagesText(req)), "undefined", "sed -i triggered Go check")
}

func TestT3_BashFileWritesPlusRemote(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteRemoteFile("remote_cfg.txt", "key=old\n")

	env.MockLLM.EnqueueToolCall("tc1", "bash", map[string]any{
		"command": "sed -i 's/key=old/key=new/' remote_cfg.txt",
	})
	env.MockLLM.EnqueueToolCall("tc2", "edit", map[string]any{
		"path":    "remote_cfg.txt",
		"oldText": "key=old",
		"newText": "key=new",
	})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--remote", "devbox:"+env.RemoteDir, "--provider", "mock", "--model", "mock-coder", "update remote_cfg.txt")
	assertExitZero(t, res)
	assertContains(t, env.ReadRemoteFile("remote_cfg.txt"), "key=new", "remote sed refused, then edit succeeded")
}

func TestT3_JobsPlusWaitPlusCompaction(t *testing.T) {
	env := NewTestEnv(t)

	env.MockLLM.EnqueueToolCall("tc1", "bash", map[string]any{
		"command":    "sleep 0.2 && echo COMPACT_JOB_DONE",
		"background": true,
	})
	env.MockLLM.EnqueueToolCall("tc2", "wait", map[string]any{})
	env.MockLLM.EnqueueText("job done")
	env.MockLLM.EnqueueText("compaction summary")
	env.MockLLM.EnqueueText("after compact")

	sess := env.StartRPCSession("--provider", "mock", "--model", "mock-coder")
	_ = sess.Prompt("TASK_PINNED_777: run build and wait")
	_, _ = sess.WaitForAgentEnd(10 * time.Second)

	_ = sess.Send(map[string]any{"type": "compact"})
	time.Sleep(400 * time.Millisecond)

	_ = sess.Prompt("status?")
	_, _ = sess.WaitForAgentEnd(10 * time.Second)

	req, _ := env.MockLLM.LastRequest()
	assertContains(t, allMessagesText(req), "TASK_PINNED_777", "task preserved across job + wait + compaction")
}

func TestT3_CompactionAheadPlusToolResultPruning(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("big.txt", strings.Repeat("PRUNE_AND_COMPACT_LINE_", 150)+"\n")

	for i := 0; i < 12; i++ {
		env.MockLLM.EnqueueToolCall(fmt.Sprintf("tc_%d", i), "read", map[string]any{"path": "big.txt"})
	}
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "read big.txt 12 times")
	assertExitZero(t, res)

	reqs := env.MockLLM.CapturedRequests()
	if len(reqs) < 12 {
		t.Fatalf("expected >= 12 requests, got %d", len(reqs))
	}
	if reqs[0].SystemPrompt != reqs[len(reqs)-1].SystemPrompt {
		t.Fatalf("system prompt must remain stable across pruning turns")
	}
}

func TestT3_ProjectSnapshotPlusCutOffResponse(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("src/core.go", "package src\n")
	env.InitGitRepo()

	env.MockLLM.EnqueueLengthCutOff("thinking about src/core.go...", "")
	env.MockLLM.EnqueueText("RECOVERED_WITH_SNAPSHOT")

	res := env.RunUPI("-p", "--thinking", "high", "--provider", "mock", "--model", "mock-coder", "analyze core.go")
	assertExitZero(t, res)
	assertContains(t, res.Stdout, "RECOVERED_WITH_SNAPSHOT", "recovered after cutoff")

	req, _ := env.MockLLM.LastRequest()
	assertContains(t, req.SystemPrompt, "<project>", "snapshot retained across cutoff retry")
}

func TestT3_GrepFindPlusBashCacheInvalidation(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("a.go", "package a\n")

	env.MockLLM.EnqueueToolCall("tc1", "find", map[string]any{"pattern": "**/*.go"})
	env.MockLLM.EnqueueToolCall("tc2", "bash", map[string]any{"command": "echo 'package b' > b.go"})
	env.MockLLM.EnqueueToolCall("tc3", "find", map[string]any{"pattern": "**/*.go"})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--tools", "+grep,+find", "--provider", "mock", "--model", "mock-coder", "find bash find")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	assertContains(t, strings.Join(toolResultMessages(req), "\n"), "b.go", "find cache invalidated after bash")
}

func TestT3_MeliousPlusCutOffResponse(t *testing.T) {
	env := NewTestEnv(t)
	env.MockLLM.EnqueueLengthCutOff("glm-5.3 deep reasoning...", "")
	env.MockLLM.EnqueueText("MELIOUS_CUTOFF_OK")

	res := env.RunUPI("-p", "--thinking", "high", "--provider", "melious", "--model", "glm-5.3", "solve")
	assertExitZero(t, res)
	assertContains(t, res.Stdout, "MELIOUS_CUTOFF_OK", "melious cut-off recovery")
}

func TestT3_PrintModePlusBackgroundJobs(t *testing.T) {
	env := NewTestEnv(t)

	env.MockLLM.EnqueueToolCall("tc1", "bash", map[string]any{
		"command":    "sleep 0.25 && echo PRINT_BG_COMPLETE",
		"background": true,
	})
	env.MockLLM.EnqueueText("waiting for bg")
	env.MockLLM.EnqueueText("FINAL_PRINT_BG_OK")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "bg in print mode")
	assertExitZero(t, res)
	assertContains(t, res.Stdout, "FINAL_PRINT_BG_OK", "upi -p waits for background job wakeup")
}

func TestT3_CatInBashPlusUnreadEdit(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("cat_seen.txt", "lineA\nlineB\nlineC\n")

	env.MockLLM.EnqueueToolCall("tc1", "bash", map[string]any{"command": "cat cat_seen.txt"})
	env.MockLLM.EnqueueToolCall("tc2", "edit", map[string]any{
		"path":    "cat_seen.txt",
		"oldText": "lineB",
		"newText": "lineB_edited",
	})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "cat then edit")
	assertExitZero(t, res)
	assertContains(t, env.ReadFile("cat_seen.txt"), "lineB_edited", "cat counted as reading")
}

func TestT3_NestedAgentsMDPlusBatchedEditAndBash(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("module/AGENTS.md", "MODULE_RULE: keep tests green.\n")
	env.WriteFile("module/data.txt", "old_val\n")

	env.MockLLM.EnqueueToolCall("tc1", "read", map[string]any{"path": "module/data.txt"})
	env.MockLLM.EnqueueToolCalls(
		MockToolCall{ID: "tc2", Name: "edit", Arguments: `{"path":"module/data.txt","oldText":"old_val","newText":"new_val"}`},
		MockToolCall{ID: "tc3", Name: "bash", Arguments: `{"command":"cat module/data.txt"}`},
	)
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "read edit bash")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	all := allMessagesText(req)
	assertContains(t, all, "MODULE_RULE", "nested AGENTS.md injected")
	assertContains(t, all, "new_val", "batched edit+bash executed in order")
}

func TestT3_RemotePlusBackgroundJobsAndWait(t *testing.T) {
	env := NewTestEnv(t)

	env.MockLLM.EnqueueToolCall("tc1", "bash", map[string]any{
		"command":    "sleep 0.2 && echo REMOTE_BG_DONE",
		"background": true,
	})
	env.MockLLM.EnqueueToolCall("tc2", "wait", map[string]any{})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--remote", "devbox:"+env.RemoteDir, "--provider", "mock", "--model", "mock-coder", "remote bg + wait")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	assertContains(t, strings.Join(toolResultMessages(req), "\n"), "REMOTE_BG_DONE", "remote bg + wait")
}

func TestT3_RemotePlusArgumentAliases(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteRemoteFile("alias_remote.txt", "REMOTE_ALIAS_OK\n")

	env.MockLLM.EnqueueToolCall("tc1", "read", map[string]any{"file_path": "alias_remote.txt"})
	env.MockLLM.EnqueueText("done")

	res := env.RunUPI("-p", "--remote", "devbox:"+env.RemoteDir, "--provider", "mock", "--model", "mock-coder", "remote read with file_path")
	assertExitZero(t, res)

	req, _ := env.MockLLM.LastRequest()
	assertContains(t, strings.Join(toolResultMessages(req), "\n"), "REMOTE_ALIAS_OK", "remote + argument alias")
}

func TestT3_SeekSequencePlusBackgroundChecks(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteFile("go.mod", "module example.com/t3\n\ngo 1.24\n")
	env.WriteFile("indent.go", "package main\n\nfunc g() {\n\tprintln(\"ok\")\n}\n")

	env.MockLLM.EnqueueToolCall("tc1", "read", map[string]any{"path": "indent.go"})
	// Space-indented oldText triggers seek_sequence; newText introduces undefined symbol
	env.MockLLM.EnqueueToolCall("tc2", "edit", map[string]any{
		"path":    "indent.go",
		"oldText": `    println("ok")`,
		"newText": `    undefinedAfterSeek()`,
	})
	env.MockLLM.EnqueueText("saw check")

	res := env.RunUPI("-p", "--provider", "mock", "--model", "mock-coder", "seek_sequence + check")
	assertExitZero(t, res)

	assertContains(t, env.ReadFile("indent.go"), "\tundefinedAfterSeek()", "seek_sequence preserved tab")
	req, _ := env.MockLLM.LastRequest()
	assertContains(t, strings.ToLower(allMessagesText(req)), "undefined", "check ran after seek_sequence edit")
}

func TestT3_JobHubPlusSleepShortening(t *testing.T) {
	env := NewTestEnv(t)

	env.MockLLM.EnqueueToolCall("tc1", "bash", map[string]any{
		"command":    "sleep 0.2 && echo SHORT_JOB_END",
		"background": true,
	})
	env.MockLLM.EnqueueToolCall("tc2", "bash", map[string]any{
		"command": "sleep 15; echo AFTER_SHORT",
	})
	env.MockLLM.EnqueueText("done")

	sess := env.StartRPCSession("--provider", "mock", "--model", "mock-coder")
	_ = sess.Prompt("run job and sleep")
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
	assertContains(t, all.String(), "bg1", "/jobs shows job after sleep shortening")
}
