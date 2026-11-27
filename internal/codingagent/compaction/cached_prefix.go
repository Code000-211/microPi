// SPDX-License-Identifier: MIT

package compaction

// Ports the compaction on the session's cached prefix of packages/coding-agent/src/core/compaction/compaction.ts (µPi a4146262).

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// µPi (a4146262): the standalone summary request has its own system prompt and the conversation as text, with cacheRetention none, so the
// provider reads none of it from its cache: in Pi's sessions all 4 compactions read 0 cached tokens, 26k to 508k input tokens each. As
// deepseek-harness does, a threshold or manual compaction sends the session's next request with one more user message, the summary
// instruction, so the provider reads all but the instruction from its prompt cache.

// keptQuoteChars is how much of the first kept message the instruction quotes, so the model knows where its summary ends.
const keptQuoteChars = 300

// DescribeKeptMessage is the start of message as the summary instruction quotes it: how the model saw it, cut to a few lines
// (compaction.ts describeKeptMessage).
func DescribeKeptMessage(message agent.AgentMessage) string {
	text := strings.TrimSpace(SerializeConversation(convertToLlm([]agent.AgentMessage{message})))
	if jsstring.Length(text) > keptQuoteChars {
		return jsstring.Slice(text, 0, keptQuoteChars) + "..."
	}
	return text
}

// cachedPrefixInstruction is the summary instruction that ends the request (compaction.ts cachedPrefixInstruction).
func cachedPrefixInstruction(keptQuote string, isSplitTurn, hasPreviousSummary bool, customInstructions string) string {
	parts := []string{"The conversation above is being compacted to free context: your summary replaces all of it."}
	if keptQuote != "" {
		parts[0] = "The conversation above is being compacted to free context: your summary replaces everything above up to the message quoted below, and the conversation then continues from that message on, unchanged."
	}
	if hasPreviousSummary {
		parts = append(parts, "It begins with a summary of earlier history: carry that content forward into yours.")
	}
	if keptQuote != "" && isSplitTurn {
		parts = append(parts, "That message is in the middle of a turn: include what that turn was doing up to it.")
	}
	if keptQuote != "" {
		parts = append(parts, "First message kept:\n<kept>\n"+keptQuote+"\n</kept>")
	}
	parts = append(parts, "Do not call any tool. Answer with the summary only, in the format below.", SUMMARIZATION_PROMPT)
	if customInstructions != "" {
		parts = append(parts, "Additional focus: "+customInstructions)
	}
	return strings.Join(parts, "\n\n")
}

// CachedPrefixRequest is the session's own request, which the summary request continues: the same messages, model and options
// (compaction.ts CachedPrefixRequest).
type CachedPrefixRequest struct {
	// Messages are the provider-visible messages of the session's next request (system messages with the tools included).
	Messages []ai.Message
	Model    *ai.Model
	// Options are the session's request options (reasoning, routing session id, thinking budgets, transport), with its auth.
	Options ai.StreamOptions
	// StreamFn sends the request; nil streams with the model's provider.
	StreamFn  agent.StreamFn
	Retry     *ai.RetryPolicy
	Callbacks ai.RetryCallbacks
}

// CompactOnCachedPrefix is compaction as a continuation of the session's cached prefix: the session's next request with the summary
// instruction as its last message, so the provider reads all of it from its prompt cache and only the instruction and the summary are new
// (compaction.ts compactOnCachedPrefix). keptQuote is [DescribeKeptMessage] of the first kept message, "" when there is none.
//
// It returns nil and no error when the response is not a usable summary (an aborted request, an overflow, a tool call, an answer cut off
// with reasoning on, an empty answer), for the caller to fall back to [Compact]. An error the standalone request would meet too (no quota,
// a failed connection after the retries), and an answer cut off without reasoning, fail the compaction as it would, without a second
// request.
func CompactOnCachedPrefix(ctx context.Context, prep CompactionPreparation, request CachedPrefixRequest, keptQuote, customInstructions string) (*CompactionResult, error) {
	if prep.FirstKeptEntryID == "" {
		return nil, nil
	}
	instruction := ai.UserMessage{
		Content:   ai.UserContentBlocks{ai.TextContent{Text: cachedPrefixInstruction(keptQuote, prep.IsSplitTurn, prep.PreviousSummary != "", customInstructions)}},
		Timestamp: time.Now().UnixMilli(),
	}
	transcript := ai.NormalizeContext(ai.Context{Messages: append(slices.Clip(request.Messages), instruction)})
	// The output cap of a summary. (Anthropic's thinking budget stays the level's while the cap is above it, so the cached messages still
	// match.)
	options := request.Options
	options.MaxTokens = GetSummaryMaxTokens(request.Model, prep.Settings, false)
	if request.Model != nil {
		options.ModelCost = request.Model.CostRates()
	}
	response, err := ai.RetryAssistantCall(ctx, func() (ai.AssistantMessage, error) {
		stream, err := streamSummary(ctx, request.StreamFn, request.Model, transcript, options)
		if err != nil {
			stopReason := ai.StopReasonError
			if ctx.Err() != nil {
				stopReason = ai.StopReasonAborted
			}
			return ai.AssistantMessage{StopReason: stopReason, ErrorMessage: err.Error()}, nil
		}
		message := collectSummary(ctx, stream)
		if ctx.Err() != nil {
			return ai.AssistantMessage{StopReason: ai.StopReasonAborted}, nil
		}
		return *message, nil
	}, request.Retry, request.Callbacks)
	if err != nil {
		return nil, err
	}
	reasoning := options.Thinking != ""
	switch {
	case response.StopReason == ai.StopReasonError && !ai.IsContextOverflow(response, 0):
		detail := response.ErrorMessage
		if detail == "" {
			detail = "Unknown error"
		}
		return nil, errors.New("Summarization failed: " + detail)
	case response.StopReason == ai.StopReasonLength && !reasoning:
		return nil, errors.New("Summarization failed: " + ErrSummaryTokenCap.Error())
	case response.StopReason == ai.StopReasonAborted, response.StopReason == ai.StopReasonError, response.StopReason == ai.StopReasonLength:
		return nil, nil
	}
	for _, block := range response.Content {
		if _, ok := block.(ai.ToolCall); ok {
			return nil, nil
		}
	}
	summary := strings.TrimSpace(summaryText(response.Content))
	if summary == "" {
		return nil, nil
	}
	readFiles, modifiedFiles := ComputeFileLists(prep.FileOps)
	summary += FormatFileOperations(readFiles, modifiedFiles)
	usage := response.Usage
	return &CompactionResult{
		Summary:          summary,
		FirstKeptEntryID: prep.FirstKeptEntryID,
		TokensBefore:     prep.TokensBefore,
		Usage:            &usage,
		Details:          CompactionDetails{ReadFiles: readFiles, ModifiedFiles: modifiedFiles},
	}, nil
}
