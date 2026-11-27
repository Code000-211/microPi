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
