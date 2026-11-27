package agent

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/MichaelKinsy/PiG/ai"
)

type earlyToolResult struct {
	done     chan struct{}
	executed *finalizedToolCall
	err      error
}

type earlyToolExecutor struct {
	ctx    context.Context
	cancel context.CancelFunc
	tools  []AgentTool
	hooks  ToolCallHooks

	mu       sync.Mutex
	inflight map[string]*earlyToolResult
	wg       sync.WaitGroup
}

func isSpeculativeReadOnlyTool(name string) bool {
	switch name {
	case "read", "ls", "grep", "find", "project_search":
		return true
	default:
		return false
	}
}

func newEarlyToolExecutor(parent context.Context, h *loopHost) *earlyToolExecutor {
	if h == nil || h.cfg.ToolExecution == ToolModeSequential {
		return nil
	}
	hooks := h.toolCallHooks()
	if len(hooks.before()) > 0 {
		return nil
	}
	ctx, cancel := context.WithCancel(parent)
	return &earlyToolExecutor{
		ctx:      ctx,
		cancel:   cancel,
		tools:    h.tools(),
		hooks:    hooks,
		inflight: make(map[string]*earlyToolResult),
	}
}

func (e *earlyToolExecutor) maybeStartFromEvent(tc ai.ToolCall) {
	if e == nil || tc.ID == "" || !isSpeculativeReadOnlyTool(tc.Name) {
		return
	}
	tool := findTool(e.tools, tc.Name)
	if tool == nil || tool.ExecutionMode() != ToolModeParallel {
		return
	}
	argsBytes, err := json.Marshal(tc.Arguments)
	if err != nil {
		return
	}
	call := pendingToolCall{
		id:   tc.ID,
		name: tc.Name,
		args: toolCallArguments(argsBytes),
	}

	e.mu.Lock()
	if e.ctx.Err() != nil {
		e.mu.Unlock()
		return
	}
	if _, exists := e.inflight[tc.ID]; exists {
		e.mu.Unlock()
		return
	}
	res := &earlyToolResult{done: make(chan struct{})}
	e.inflight[tc.ID] = res
	e.wg.Add(1)
	e.mu.Unlock()

	go func() {
		defer e.wg.Done()
		defer close(res.done)
		outcome := prepareToolCall(e.ctx, e.tools, e.hooks, call)
		if outcome.prepared == nil {
			return
		}
		execRes, execErr := executePreparedToolCall(e.ctx, *outcome.prepared, nil, func() bool { return false })
		if execErr != nil {
			res.err = execErr
			return
		}
		res.executed = &execRes
	}()
}

func (e *earlyToolExecutor) take(callID string) (*earlyToolResult, bool) {
	if e == nil {
		return nil, false
	}
	e.mu.Lock()
	res, ok := e.inflight[callID]
	if ok {
		delete(e.inflight, callID)
	}
	e.mu.Unlock()
	return res, ok
}

func (e *earlyToolExecutor) cancelAll() {
	if e == nil {
		return
	}
	e.cancel()
	e.wg.Wait()
}
