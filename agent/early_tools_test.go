package agent

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

type earlyMockReadTool struct {
	execCount atomic.Int32
	started   chan struct{}
}

func (t *earlyMockReadTool) Name() string        { return "read" }
func (t *earlyMockReadTool) Label() string       { return "Read" }
func (t *earlyMockReadTool) Description() string { return "mock read" }
func (t *earlyMockReadTool) ExecutionMode() ToolExecutionMode {
	return ToolModeParallel
}
func (t *earlyMockReadTool) Schema() ai.ToolSchema {
	return ai.ToolSchema{
		Name:       "read",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}},
	}
}
func (t *earlyMockReadTool) StartsEarly(json.RawMessage) bool { return true }
func (t *earlyMockReadTool) Execute(_ context.Context, _ string, _ json.RawMessage, _ ToolUpdateCallback) (AgentToolResult, error) {
	t.execCount.Add(1)
	if t.started != nil {
		select {
		case <-t.started:
		default:
			close(t.started)
		}
	}
	return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "ok"}}}, nil
}

func TestEarlyToolExecutionStartsDuringStreamAndReusesResult(t *testing.T) {
	tool := &earlyMockReadTool{started: make(chan struct{})}
	stream := ai.NewAssistantMessageEventStream()

	var calls int
	a := mustNewAgent(AgentOptions{
		Model: &ai.Model{ID: "m"},
		Tools: []AgentTool{tool},
		StreamFn: func(_ context.Context, _ *ai.Model, _ ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
			calls++
			if calls == 1 {
				return stream, nil
			}
			doneStream := ai.NewAssistantMessageEventStream()
			msg := agentTestAssistant([]ai.AssistantContentBlock{ai.TextContent{Text: "done"}}, ai.StopReasonStop)
			_ = doneStream.Push(ai.StartEvent{Partial: msg})
			_ = doneStream.Push(ai.DoneEvent{Reason: ai.StopReasonStop, Message: msg})
			doneStream.End(msg)
			return doneStream, nil
		},
	})

	go func() {
		tc := ai.ToolCall{ID: "tc1", Name: "read", Arguments: map[string]any{"path": "a.txt"}}
		partial := agentTestAssistant([]ai.AssistantContentBlock{tc}, "")
		_ = stream.Push(ai.StartEvent{Partial: partial})
		_ = stream.Push(ai.ToolCallEndEvent{ContentIndex: 0, ToolCall: tc, Partial: partial})

		// Wait until speculative read starts BEFORE DoneEvent is sent.
		select {
		case <-tool.started:
		case <-time.After(2 * time.Second):
			t.Error("expected speculative read to start before DoneEvent")
		}

		final := agentTestAssistant([]ai.AssistantContentBlock{tc}, ai.StopReasonToolUse)
		_ = stream.Push(ai.DoneEvent{Reason: ai.StopReasonToolUse, Message: final})
		stream.End(final)
	}()

	if _, err := a.Send(context.Background(), "go"); err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if got := tool.execCount.Load(); got != 1 {
		t.Fatalf("expected tool.Execute to run exactly once (reused from speculative run), got %d", got)
	}
}

// earlyMockWriteTool is a sequential tool with side effects: it never starts early.
type earlyMockWriteTool struct {
	execCount atomic.Int32
	// readCountAtStart is how often the read tool had run when this tool started.
	readCountAtStart atomic.Int32
	read             *earlyMockReadTool
}

func (t *earlyMockWriteTool) Name() string                     { return "write" }
func (t *earlyMockWriteTool) Label() string                    { return "Write" }
func (t *earlyMockWriteTool) Description() string              { return "mock write" }
func (t *earlyMockWriteTool) ExecutionMode() ToolExecutionMode { return ToolModeSequential }
func (t *earlyMockWriteTool) Schema() ai.ToolSchema {
	return ai.ToolSchema{Name: "write", Parameters: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}}}
}
func (t *earlyMockWriteTool) Execute(_ context.Context, _ string, _ json.RawMessage, _ ToolUpdateCallback) (AgentToolResult, error) {
	t.execCount.Add(1)
	t.readCountAtStart.Store(t.read.execCount.Load())
	return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "written"}}}, nil
}

// runEarlyMessage streams one assistant message with calls, waits wait before ending it, and answers the follow-up request with text.
func runEarlyMessage(t *testing.T, tools []AgentTool, calls []ai.ToolCall, wait time.Duration) {
	t.Helper()
	stream := ai.NewAssistantMessageEventStream()
	var requests int
	a := mustNewAgent(AgentOptions{
		Model: &ai.Model{ID: "m"},
		Tools: tools,
		StreamFn: func(_ context.Context, _ *ai.Model, _ ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
			requests++
			if requests == 1 {
				return stream, nil
			}
			doneStream := ai.NewAssistantMessageEventStream()
			msg := agentTestAssistant([]ai.AssistantContentBlock{ai.TextContent{Text: "done"}}, ai.StopReasonStop)
			_ = doneStream.Push(ai.StartEvent{Partial: msg})
			_ = doneStream.Push(ai.DoneEvent{Reason: ai.StopReasonStop, Message: msg})
			doneStream.End(msg)
			return doneStream, nil
		},
	})
	go func() {
		blocks := make([]ai.AssistantContentBlock, 0, len(calls))
		for _, c := range calls {
			blocks = append(blocks, c)
		}
		partial := agentTestAssistant(blocks, "")
		_ = stream.Push(ai.StartEvent{Partial: partial})
		for i, c := range calls {
			_ = stream.Push(ai.ToolCallEndEvent{ContentIndex: i, ToolCall: c, Partial: partial})
		}
		time.Sleep(wait)
		final := agentTestAssistant(blocks, ai.StopReasonToolUse)
		_ = stream.Push(ai.DoneEvent{Reason: ai.StopReasonToolUse, Message: final})
		stream.End(final)
	}()
	if _, err := a.Send(context.Background(), "go"); err != nil {
		t.Fatalf("Send failed: %v", err)
	}
}

// A read after a write in the same message must not run early: it would read what the write has not written yet.
func TestEarlyToolExecutionDoesNotRunAheadOfACallWithSideEffects(t *testing.T) {
	read := &earlyMockReadTool{}
	write := &earlyMockWriteTool{read: read}
	runEarlyMessage(t, []AgentTool{read, write}, []ai.ToolCall{
		{ID: "w1", Name: "write", Arguments: map[string]any{"path": "a.txt"}},
		{ID: "r1", Name: "read", Arguments: map[string]any{"path": "a.txt"}},
	}, 100*time.Millisecond)
	if got := write.readCountAtStart.Load(); got != 0 {
		t.Fatalf("read ran %d time(s) before the write that precedes it", got)
	}
	if read.execCount.Load() != 1 || write.execCount.Load() != 1 {
		t.Fatalf("read ran %d, write %d times, want 1 each", read.execCount.Load(), write.execCount.Load())
	}
}

// A read that leads a sequential batch starts early and its run is used: it runs once, before the write.
func TestEarlyToolExecutionLeadingReadOfASequentialBatchIsReused(t *testing.T) {
	read := &earlyMockReadTool{started: make(chan struct{})}
	write := &earlyMockWriteTool{read: read}
	runEarlyMessage(t, []AgentTool{read, write}, []ai.ToolCall{
		{ID: "r1", Name: "read", Arguments: map[string]any{"path": "a.txt"}},
		{ID: "w1", Name: "write", Arguments: map[string]any{"path": "a.txt"}},
	}, 100*time.Millisecond)
	select {
	case <-read.started:
	default:
		t.Fatal("the leading read did not run")
	}
	if got := read.execCount.Load(); got != 1 {
		t.Fatalf("read ran %d times, want 1 (the early run reused)", got)
	}
	if got := write.readCountAtStart.Load(); got != 1 {
		t.Fatalf("write started after %d reads, want 1", got)
	}
}
