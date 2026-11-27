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

// EarlyStarter is a tool whose calls may start while the assistant message is still streaming. StartsEarly is asked with a call's complete
// arguments (before PrepareArguments) and returns true only for a call without side effects, whose result may be dropped.
//
// µPi: ports microPi's EarlyToolExecution (agent-loop.ts EarlyToolCalls, agent-session.ts _earlyToolExecution).
type EarlyStarter interface {
	StartsEarly(args json.RawMessage) bool
}

// earlyToolExecutor runs the tool calls of one assistant message that may start while the message still streams. A call starts once its
// arguments are complete and every tool call before it in the message has started early; the first call that cannot start early ends early
// starts for the rest of the message, so an early run never runs ahead of a call that may have side effects. The loop uses a run in place of
// executing its call (take), in parallel and in sequential batches alike; runs it does not take are canceled and dropped.
type earlyToolExecutor struct {
	ctx    context.Context
	cancel context.CancelFunc
	tools  []AgentTool
	hooks  ToolCallHooks

	mu       sync.Mutex
	inflight map[string]*earlyToolResult
	// started holds the content indexes of the message's tool calls that started early; closed is set once a call could not.
	started map[int]struct{}
	closed  bool
	wg      sync.WaitGroup
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
		started:  make(map[int]struct{}),
	}
}

// maybeStartFromEvent follows the end of a streamed tool call: the call at content index of partial, with its complete arguments.
func (e *earlyToolExecutor) maybeStartFromEvent(index int, partial *ai.AssistantMessage, tc ai.ToolCall) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || e.ctx.Err() != nil {
		return
	}
	if !e.earlierCallsStartedLocked(index, partial) {
		e.closed = true
		return
	}
	argsBytes, err := json.Marshal(tc.Arguments)
	if err != nil || tc.ID == "" {
		e.closed = true
		return
	}
	starter, ok := findTool(e.tools, tc.Name).(EarlyStarter)
	if !ok || !starter.StartsEarly(argsBytes) {
		e.closed = true
		return
	}
	if _, exists := e.inflight[tc.ID]; exists {
		return
	}
	e.started[index] = struct{}{}
	e.startLocked(pendingToolCall{id: tc.ID, name: tc.Name, args: toolCallArguments(argsBytes)})
}

// earlierCallsStartedLocked reports whether every tool call before index in partial started early.
func (e *earlyToolExecutor) earlierCallsStartedLocked(index int, partial *ai.AssistantMessage) bool {
	if partial == nil {
		return len(e.started) == 0 && index == 0
	}
	for i, block := range partial.Content {
		if i >= index {
			break
		}
		if _, isCall := block.(ai.ToolCall); isCall {
			if _, ok := e.started[i]; !ok {
				return false
			}
		}
	}
	return true
}

func (e *earlyToolExecutor) startLocked(call pendingToolCall) {
	res := &earlyToolResult{done: make(chan struct{})}
	e.inflight[call.id] = res
	e.wg.Add(1)

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
	e.mu.Lock()
	e.closed = true
	e.mu.Unlock()
	e.cancel()
	e.wg.Wait()
}

// takeResult waits for the early run of call and returns it, or false when the call did not start early or its run failed.
func (e *earlyToolExecutor) takeResult(callID string) (finalizedToolCall, bool) {
	spec, ok := e.take(callID)
	if !ok {
		return finalizedToolCall{}, false
	}
	<-spec.done
	if spec.err != nil || spec.executed == nil {
		return finalizedToolCall{}, false
	}
	return *spec.executed, true
}
