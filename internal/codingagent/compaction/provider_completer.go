package compaction

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/compactprogress"
)

// ProviderCompleter collects one summarization response from the caller's stream override or the model's provider. It preserves provider errors, usage and invalid-summary diagnostics.
type ProviderCompleter struct {
	// StreamFn is the caller's stream override; nil streams with the model's provider.
	StreamFn agent.StreamFn
}

// simpleStreamOptionsProvider is a provider whose simple stream prepares request options before it streams: the model runtime's registry-built providers (packages/ai/src/api/simple-options.ts buildBaseOptions and each API's streamSimple).
type simpleStreamOptionsProvider interface {
	SimpleStreamOptions(model *ai.Model, transcript ai.TranscriptContext, options ai.StreamOptions) ai.StreamOptions
}

func (c ProviderCompleter) CompleteSimple(
	ctx context.Context,
	model *ai.Model,
	systemPrompt string,
	messages []agent.AgentMessage,
	options ai.StreamOptions,
) (string, *ai.Usage, error) {
	llmMessages := make([]ai.Message, 0, len(messages))
	for _, message := range messages {
		switch {
		case message.User != nil:
			llmMessages = append(llmMessages, message.User.LLMMessage())
		case message.Assistant != nil:
			usage := ai.Usage{}
			if message.Assistant.Usage != nil {
				usage = *message.Assistant.Usage
			}
			llmMessages = append(llmMessages, ai.AssistantMessage{
				Content: message.Assistant.Content, API: message.Assistant.API,
				Provider: message.Assistant.Provider, Model: message.Assistant.ModelID,
				ResponseModel: message.Assistant.ResponseModel, ResponseID: message.Assistant.ResponseID,
				Diagnostics: message.Assistant.Diagnostics, Usage: usage,
				StopReason: message.Assistant.StopReason, Deferred: message.Assistant.Deferred,
				ErrorMessage: message.Assistant.ErrorMessage, RawStopReason: message.Assistant.RawStopReason,
				Timestamp: message.Assistant.Timestamp,
			})
		}
	}

	transcript := ai.NormalizeContext(ai.Context{
		SystemPrompt: systemPrompt,
		Messages:     llmMessages,
	})
	stream, err := streamSummary(ctx, c.StreamFn, model, transcript, options)
	if err != nil {
		return "", nil, fmt.Errorf("compaction completer: stream: %w", err)
	}
	message := collectSummary(ctx, stream)
	if ctx.Err() != nil {
		return "", nil, ctx.Err()
	}
	if message.StopReason == ai.StopReasonError {
		detail := message.ErrorMessage
		if detail == "" {
			detail = "Unknown error"
		}
		return "", nil, errors.New(detail)
	}
	if message.StopReason == ai.StopReasonLength {
		usage := message.Usage
		return "", &usage, ErrSummaryTokenCap
	}
	for _, block := range message.Content {
		if _, ok := block.(ai.ToolCall); ok {
			return "", nil, ErrSummarizationToolCall
		}
	}
	usage := message.Usage
	return summaryText(message.Content), &usage, nil
}

// summaryText is the text blocks of a summary response, joined (compaction.ts contentText).
func summaryText(content []ai.AssistantContentBlock) string {
	var text strings.Builder
	for _, block := range content {
		if block, ok := block.(ai.TextContent); ok {
			text.WriteString(block.Text)
		}
	}
	return text.String()
}

// streamSummary sends a summary request through streamFn, or, without one, the model's provider.
func streamSummary(ctx context.Context, streamFn agent.StreamFn, model *ai.Model, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	if streamFn != nil {
		return streamFn(ctx, model, transcript, options)
	}
	// completeSimple is a simple stream (compaction.ts completeSummarization): a provider the model runtime built prepares the options of one, as its StreamSimple does.
	if simple, ok := model.Provider.(simpleStreamOptionsProvider); ok {
		options = simple.SimpleStreamOptions(model, transcript, options)
	}
	return model.Provider.Stream(ctx, transcript, options)
}

// collectSummary reads a summary response to its end.
func collectSummary(ctx context.Context, stream *ai.AssistantMessageEventStream) *ai.AssistantMessage {
	if progress := compactprogress.From(ctx); progress != nil {
		// pig divergence (D652): the interactive compaction line shows the size of the summary as it streams. The events are only counted; the result below is what Result returned before, the caller's context cancels the iteration as it ends the request.
		progress.Reset()
		for _, event := range stream.ObservePublishedEvents(ctx) {
			switch event := event.(type) {
			case ai.TextDeltaEvent:
				progress.Add(len(event.Delta))
			case ai.ThinkingDeltaEvent:
				progress.Add(len(event.Delta))
			}
		}
	}
	return stream.Result()
}
