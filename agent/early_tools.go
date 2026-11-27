package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/ai"
)

type earlyToolResult struct {
	done     chan struct{}
	executed *finalizedToolCall
	err      error
	// index is the call's content index, args its arguments in canonical form, cancel ends this run alone.
	index  int
	args   string
	cancel context.CancelFunc
}

// canonicalArgs is the arguments' JSON re-encoded (sorted keys, no spacing), so arguments streamed, ended and stored compare equal
// whatever their byte form; ok is false for anything but a JSON object.
func canonicalArgs(raw []byte) (string, bool) {
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil || value == nil {
		return "", false
	}
	out, err := json.Marshal(value)
	if err != nil {
		return "", false
	}
	return string(out), true
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
	// streamed holds the arguments streamed so far of calls not started yet, by content index (agent-loop.ts streamedArguments).
	streamed map[int]*strings.Builder
	wg       sync.WaitGroup
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
		streamed: make(map[int]*strings.Builder),
	}
}

// observeDelta follows a streamed piece of a tool call's arguments. Once they form a complete JSON object the call may start, before the
// provider ends the call: a provider such as openai-completions ends every call only when the whole message ends (agent-loop.ts
// EarlyToolCalls.observe). The end event then keeps the run if its final arguments are the same, else restarts it.
func (e *earlyToolExecutor) observeDelta(index int, partial *ai.AssistantMessage, delta string) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || e.ctx.Err() != nil {
		return
	}
	if _, started := e.started[index]; started {
		return
	}
	buf := e.streamed[index]
	if buf == nil {
		buf = &strings.Builder{}
		e.streamed[index] = buf
	}
	buf.WriteString(delta)
	// A complete JSON object cannot be continued: these are the call's arguments, unless the provider changes them.
	if trimmed := strings.TrimRight(delta, " \t\r\n"); trimmed == "" || !strings.HasSuffix(trimmed, "}") {
		return
	}
	if partial == nil || index < 0 || index >= len(partial.Content) {
		return
	}
	block, ok := partial.Content[index].(ai.ToolCall)
	if !ok || block.ID == "" || block.Name == "" {
		return
	}
	raw := []byte(buf.String())
	if !json.Valid(raw) || !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{")) {
		return
	}
	delete(e.streamed, index)
	e.startFromArgsLocked(index, partial, block.ID, block.Name, raw)
}

// maybeStartFromEvent follows the end of a streamed tool call: the call at content index of partial, with its complete arguments.
func (e *earlyToolExecutor) maybeStartFromEvent(index int, partial *ai.AssistantMessage, tc ai.ToolCall) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.streamed, index)
	if e.closed || e.ctx.Err() != nil {
		return
	}
	argsBytes, err := tc.ArgumentsJSON()
	if err != nil {
		e.closed = true
		return
	}
	if run, ok := e.inflight[tc.ID]; ok && run.index == index {
		// Started from its streamed arguments: kept if the final arguments are the same, else run again with them.
		if final, ok := canonicalArgs(argsBytes); ok && final == run.args {
			return
		}
		run.cancel()
		delete(e.inflight, tc.ID)
		delete(e.started, index)
	}
	e.startFromArgsLocked(index, partial, tc.ID, tc.Name, argsBytes)
}

// startFromArgsLocked starts the call at index with argsBytes when every call before it started early and its tool takes these
// arguments early; otherwise no later call of the message starts early.
func (e *earlyToolExecutor) startFromArgsLocked(index int, partial *ai.AssistantMessage, id, name string, argsBytes []byte) {
	if !e.earlierCallsStartedLocked(index, partial) || id == "" {
		e.closed = true
		return
	}
	canonical, ok := canonicalArgs(argsBytes)
	if !ok {
		e.closed = true
		return
	}
	starter, ok := findTool(e.tools, name).(EarlyStarter)
	if !ok || !starter.StartsEarly(argsBytes) {
		e.closed = true
		return
	}
	if _, exists := e.inflight[id]; exists {
		return
	}
	e.started[index] = struct{}{}
	e.startLocked(index, canonical, pendingToolCall{id: id, name: name, args: toolCallArguments(argsBytes)})
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

func (e *earlyToolExecutor) startLocked(index int, canonical string, call pendingToolCall) {
	ctx, cancel := context.WithCancel(e.ctx)
	res := &earlyToolResult{done: make(chan struct{}), index: index, args: canonical, cancel: cancel}
	e.inflight[call.id] = res
	e.wg.Add(1)

	go func() {
		defer e.wg.Done()
		defer close(res.done)
		defer cancel()
		outcome := prepareToolCall(ctx, e.tools, e.hooks, call)
		if outcome.prepared == nil {
			return
		}
		execRes, execErr := executePreparedToolCall(ctx, *outcome.prepared, nil, func() bool { return false })
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

// takeResult waits for the early run of call and returns it, or false when the call did not start early, ran with other arguments than
// args (the call's final arguments) or failed. A run not used is canceled.
func (e *earlyToolExecutor) takeResult(callID string, args []byte) (finalizedToolCall, bool) {
	spec, ok := e.take(callID)
	if !ok {
		return finalizedToolCall{}, false
	}
	if final, ok := canonicalArgs(args); !ok || final != spec.args {
		spec.cancel()
		return finalizedToolCall{}, false
	}
	<-spec.done
	if spec.err != nil || spec.executed == nil {
		return finalizedToolCall{}, false
	}
	return *spec.executed, true
}
