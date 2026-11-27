// SPDX-License-Identifier: MIT

package coding

import (
	"context"
	"errors"
	"maps"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/compaction"
	"github.com/MichaelKinsy/PiG/internal/compactiontypes"
)

// Ports packages/coding-agent/src/core/agent-session.ts (_getSummarizationRequestAuth).
type summarizationRequest struct {
	model *ai.Model
	// apiKey, headers and env are the request auth of the summary model; headers omit the ones the auth deletes.
	apiKey    string
	headers   map[string]string
	env       map[string]string
	completer compaction.SimpleCompleter
	streamFn  compaction.StreamFn
	// thinkingLevel is the thinking level of the summary request: the session's, or the router's for a virtual selection.
	thinkingLevel ai.ModelThinkingLevel
}

// runDefaultCompaction generates Pi's built-in compaction summary for manual and automatic compaction (agent-session.ts
// _runDefaultCompaction).
func (s *Session) runDefaultCompaction(ctx context.Context, prep compaction.CompactionPreparation, model *ai.Model, customInstructions, reason string) (compaction.CompactionResult, error) {
	return s.defaultCompaction(ctx, prep, model, customInstructions, reason, false)(ctx)
}

// defaultCompaction is [Session.runDefaultCompaction] in two parts: the call reads the session's next request, which the summary on the
// cached prefix continues, and the function it returns makes the summary. ahead is a summary made while the agent works
// (compaction-ahead.ts): its retries are not shown, and its second part runs in the background.
func (s *Session) defaultCompaction(ctx context.Context, prep compaction.CompactionPreparation, model *ai.Model, customInstructions, reason string, ahead bool) func(context.Context) (compaction.CompactionResult, error) {
	var next *nextRequest
	var nextErr error
	if reason != "overflow" && compaction.CachedPrefixEnabled() {
		next, nextErr = s.nextRequest(ctx, prep)
	}
	return func(ctx context.Context) (compaction.CompactionResult, error) {
		// Resolve the request only when Pi summarizes itself: routing may call models or fail.
		request, err := s.prepareSummarizationRequest(ctx, model)
		if err != nil {
			return compaction.CompactionResult{}, err
		}
		// µPi: Pi's own summary sits beside the user's messages (session-manager.ts userMessagesBefore): it need not restate them
		// (agent-session.ts _runDefaultCompaction, keptUserMessagesInstruction).
		if kept := compaction.KeptUserMessagesInstruction(prep); kept != "" {
			if customInstructions != "" {
				customInstructions += "\n\n" + kept
			} else {
				customInstructions = kept
			}
		}
		retry, retryCallbacks := s.summarizationRetryOptions("compaction", reason)
		if ahead {
			retryCallbacks = ai.RetryCallbacks{}
		}
		if nextErr != nil {
			return compaction.CompactionResult{}, nextErr
		}
		if next != nil {
			cached, err := s.compactOnCachedPrefix(ctx, prep, request, next, customInstructions, retry, retryCallbacks)
			if err != nil {
				return compaction.CompactionResult{}, err
			}
			if cached != nil {
				return *cached, nil
			}
			if ctx.Err() != nil {
				return compaction.CompactionResult{}, errCompactionCancelled
			}
		}
		// upstream compact(preparation, model, apiKey, headers, customInstructions, signal, thinkingLevel, this.agent.streamFunction, env, retry, callbacks, sessionId): request.completer holds the Agent's stream function.
		streamFn := request.streamFn
		if streamFn == nil {
			streamFn = compaction.CompleterStreamFn(request.completer)
		}
		return compaction.Compact(ctx, prep, request.model, request.apiKey, request.headers, customInstructions, request.thinkingLevel, streamFn, request.env, retry, retryCallbacks, "")
	}
}

// nextRequest is the session's next request, as the summary on the cached prefix continues it.
type nextRequest struct {
	model    *ai.Model
	thinking ai.ModelThinkingLevel
	messages []ai.Message
	options  ai.StreamOptions
	// keptQuote is the first kept message as the instruction quotes it.
	keptQuote string
}

// nextRequest reads the messages, model and options the session's next request would send (agent-session.ts _compactOnCachedPrefix: the
// projection after the context hook and convertToLlm, so tool-result pruning too). nil when the summary cannot continue it: a virtual
// selection routes each request to a model of its choice, which has no cache of this conversation.
//
// pig divergence: a summary completer or stream function the tests inject stands in for the standalone summary request only, so a session
// with one sends that request.
func (s *Session) nextRequest(ctx context.Context, prep compaction.CompactionPreparation) (*nextRequest, error) {
	model := s.agent.Model()
	if model == nil || IsVirtualModel(model) || s.completer != nil || s.streamFn != nil {
		return nil, nil
	}
	// As the requests it continues think: less after responses cut off at the output limit (agent-session.ts _requestThinkingLevel, a37b2029).
	thinking := s.requestThinkingLevel()
	messages := s.contextFromProjection(s.inner.Load().BuildSessionProjectionView())
	llmMessages, options, err := s.agent.ProviderRequest(ctx, messages, model, thinking)
	if err != nil {
		return nil, err
	}
	next := &nextRequest{model: model, thinking: s.agent.ThinkingLevel(), messages: llmMessages, options: options}
	if kept, ok := s.inner.Load().GetEntry(prep.FirstKeptEntryID); ok && kept.Base().Type == "message" {
		if message, ok := icodingagent.AsMessage(kept); ok {
			next.keptQuote = compaction.DescribeKeptMessage(message.Message)
		}
	}
	return next, nil
}

// compactOnCachedPrefix asks the summary as the continuation of the session's own request, read from the provider's prompt cache (see
// compaction.CompactOnCachedPrefix). Only when the summary is asked of the session's own model and thinking level (another model has no
// cache of this conversation); nil to fall back to the standalone summary request (agent-session.ts _compactOnCachedPrefix).
func (s *Session) compactOnCachedPrefix(ctx context.Context, prep compaction.CompactionPreparation, request summarizationRequest, next *nextRequest, customInstructions string, retry *ai.RetryPolicy, callbacks ai.RetryCallbacks) (*compaction.CompactionResult, error) {
	if request.model == nil || providerID(request.model) != providerID(next.model) || request.model.ID != next.model.ID || request.thinkingLevel != next.thinking {
		return nil, nil
	}
	options := next.options
	options.APIKey = request.apiKey
	options.Headers = ai.ProviderHeadersFromStrings(request.headers)
	options.Env = maps.Clone(request.env)
	return compaction.CompactOnCachedPrefix(ctx, prep, compaction.CachedPrefixRequest{
		Messages:  next.messages,
		Model:     request.model,
		Options:   options,
		StreamFn:  s.agent.EffectiveStreamFunction(),
		Retry:     retry,
		Callbacks: callbacks,
	}, next.keptQuote, customInstructions)
}

// keepUserMessages records on prep the user's messages that go verbatim beside the summary, read from the whole branch: the context
// branch PrepareCompaction was given holds none of the messages before the latest compaction's kept range, where Pi passes getBranch()
// (µPi compaction.ts prepareCompaction, userMessagesBefore).
func (s *Session) keepUserMessages(prep *compaction.CompactionPreparation) {
	if !icodingagent.CompactionUserMessagesEnabled() {
		return
	}
	kept, complete := s.inner.Load().UserMessagesBeforeEntry(prep.FirstKeptEntryID)
	compactiontypes.SetKeptUserMessages(prep, kept, complete)
}

// withoutDeletedHeaders drops the headers an auth result deletes; summary requests take only header values.
// upstream: packages/coding-agent/src/core/agent-session.ts:withoutDeletedHeaders
func withoutDeletedHeaders(headers ai.ProviderHeaders) map[string]string {
	if headers == nil {
		return nil
	}
	values := make(map[string]string, len(headers))
	for name, value := range headers {
		if value != nil {
			values[name] = *value
		}
	}
	return values
}

// prepareSummarizationRequest resolves the summary model and its request auth (agent-session.ts:560 _getSummarizationRequestAuth).
func (s *Session) prepareSummarizationRequest(ctx context.Context, model *ai.Model) (summarizationRequest, error) {
	thinking := s.ThinkingLevel()
	// Pi's getAuth rejects with the signal's reason when the signal is already aborted (resolve.ts:33-44), before any request exists. A caller-owned Go provider has no getAuth: it is not called once the signal has aborted, with the same reason (pinned by TestCompactCancelsSynchronouslyFromStartEvent).
	if ctx.Err() != nil {
		return summarizationRequest{model: model, thinkingLevel: thinking}, abortSignalReason(ctx)
	}
	// Route a virtual model first: summaries size their input and output from the model they get. upstream: agent-session.ts:541-548
	if IsVirtualModel(model) {
		route, err := s.modelRuntime.ResolveModel(ctx, model, agent.ConvertToLLM(agent.NormalizeMessages(s.agent.Messages(), model)), ResolveModelOptions{Reason: ModelRouteReasonDirect, ThinkingLevel: thinking})
		if err != nil {
			return summarizationRequest{model: model, thinkingLevel: thinking}, err
		}
		model, thinking = route.Model, route.ThinkingLevel
	}
	request := summarizationRequest{model: model, completer: s.resolveCompleter(), streamFn: s.streamFn, thinkingLevel: thinking}
	// A session CreateAgentSession built sends summaries through its own stream (sdk.ts streamFn, marked with markRuntimeStreamFunction),
	// which resolves auth through the Model Runtime as it does for prompts. Credentials resolved here would reach the runtime as an
	// explicit API key instead of the stored OAuth login (#11520). The check runs after virtual-model routing. A caller's completer,
	// streamFn or replaced agent stream function keeps the pre-resolved credentials.
	if s.runtimeStream && s.completer == nil && s.streamFn == nil && s.agent.StreamFunction() == nil {
		return request, nil
	}
	// Registry-built providers use the stock request path. A directly supplied Go provider is a caller-owned stream, even when it carries provider identity metadata.
	_, registryBuilt := model.Provider.(*requestOptionsProvider)
	_, nativeAuth := model.Provider.(interface{ Auth() ai.ProviderAuth })
	prepared, provider, options := model, model.Provider, ai.StreamOptions{Headers: ai.ProviderHeadersFromStrings(model.ProviderMeta.Headers)}
	var err error
	if registryBuilt || nativeAuth {
		prepared, provider, options, err = s.modelRuntime.prepare(ctx, model, ai.StreamOptions{})
		if ctx.Err() != nil {
			// Pi's getAuth races its signal and rejects with the signal's reason (resolve.ts:33-44), which _getSummarizationRequestAuth rethrows.
			return request, abortSignalReason(ctx)
		}
	}
	if err != nil {
		if (s.streamFn != nil || s.agent.StreamFunction() != nil || !registryBuilt) && ctx.Err() == nil {
			return request, nil
		}
		if missing, ok := errors.AsType[*modelRuntimeAuthMissingError](err); ok {
			return request, errors.New(icodingagent.FormatNoAPIKeyFoundMessage(missing.provider))
		}
		return request, err
	}
	clone := *prepared
	clone.Provider = provider
	request.model = &clone
	request.apiKey, request.headers, request.env = options.APIKey, withoutDeletedHeaders(options.Headers), maps.Clone(options.Env)
	return request, nil
}

// errOperationAborted is DOMException AbortError's message: the reason a plainly aborted AbortSignal rejects with.
var errOperationAborted = errors.New("This operation was aborted")

// abortSignalReason is the reason an aborted signal rejects with: AbortError for a plain abort, TimeoutError for AbortSignal.timeout(), or the caller's cause.
func abortSignalReason(ctx context.Context) error {
	cause := context.Cause(ctx)
	switch {
	case cause == nil || errors.Is(cause, context.Canceled):
		return errOperationAborted
	case errors.Is(cause, context.DeadlineExceeded):
		return errors.New("The operation was aborted due to timeout")
	}
	return cause
}
