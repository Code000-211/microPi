package compaction

// µPi (ade455a6, 8f722e90): ports tool-result-pruning.ts.
//
// A file read or a test run fifty tool calls ago is resent with every request and rarely read again. Once the context is past
// TriggerTokens, every tool result older than the most recent KeepRecent whose content is at least MinChars long (or holds an image)
// is sent as a one-line note instead. The session keeps the full results: compaction summarizes them, and the agent can run the tool
// again.
//
// Prompt caches end at the first message that changed, so the boundary moves in steps of KeepRecent tool results rather than with
// every call: between steps the cleared prefix is the same bytes as before. Between KeepRecent and 2*KeepRecent-1 recent results stay
// complete.

import (
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// ToolResultPruningSettings configures step-wise clearing of old large tool results (tool-result-pruning.ts ToolResultPruningSettings).
type ToolResultPruningSettings = codingagent.ToolResultPruningConfig

// DefaultToolResultPruningSettings returns µPi's default pruning settings (settings-defaults.ts SETTINGS_DEFAULTS.toolResultPruning).
func DefaultToolResultPruningSettings() ToolResultPruningSettings {
	return codingagent.DefaultToolResultPruningConfig()
}

// toolResultOf returns msg as a tool result message.
func toolResultOf(msg ai.Message) (ai.ToolResultMessage, bool) {
	switch m := msg.(type) {
	case ai.ToolResultMessage:
		return m, true
	case *ai.ToolResultMessage:
		if m != nil {
			return *m, true
		}
	}
	return ai.ToolResultMessage{}, false
}

// toolResultSize counts the text (UTF-16 units, as String.length) and the images of a tool result's content.
func toolResultSize(content []ai.ToolResultMessageContent) (chars int, images int) {
	for _, block := range content {
		switch b := block.(type) {
		case ai.TextContent:
			chars += jsLength(b.Text)
		case *ai.TextContent:
			if b != nil {
				chars += jsLength(b.Text)
			}
		case ai.ImageContent, *ai.ImageContent:
			images++
		}
	}
	return chars, images
}

// estimateLLMTokens is compaction.ts estimateTokens of a provider message, as tool-result-pruning.ts estimates the converted messages.
func estimateLLMTokens(msg ai.Message) int {
	switch m := msg.(type) {
	case ai.SystemMessage:
		return estimateSystemTokens(m)
	case *ai.SystemMessage:
		if m != nil {
			return estimateSystemTokens(*m)
		}
	case ai.UserMessage:
		switch content := m.Content.(type) {
		case ai.UserText:
			return ceilQuarter(jsLength(string(content)))
		case ai.UserContentBlocks:
			return ceilQuarter(contentBlocksChars(content))
		}
	case *ai.UserMessage:
		if m != nil {
			return estimateLLMTokens(*m)
		}
	case ai.AssistantMessage:
		chars := 0
		for _, block := range m.Content {
			switch block := block.(type) {
			case ai.TextContent:
				chars += jsLength(block.Text)
			case ai.ThinkingContent:
				chars += jsLength(block.Thinking)
			case ai.ToolCall:
				chars += jsLength(block.Name) + ai.JSONStringifyLength(block.Arguments)
			}
		}
		return ceilQuarter(chars)
	case *ai.AssistantMessage:
		if m != nil {
			return estimateLLMTokens(*m)
		}
	case ai.ToolResultMessage, *ai.ToolResultMessage:
		result, _ := toolResultOf(m)
		chars, images := toolResultSize(result.Content)
		return ceilQuarter(chars + images*estimatedImageChars)
	}
	return 0
}

// seenToolResults counts the tool results before the last assistant message: the ones the model has seen. µPi: pruning never clears a
// result after it, the results of the calls the model is waiting for; a batch of parallel calls larger than KeepRecent would otherwise
// lose some before the model read them.
func seenToolResults(messages []ai.Message) int {
	last := -1
	for i, msg := range messages {
		switch msg.(type) {
		case ai.AssistantMessage, *ai.AssistantMessage:
			last = i
		}
	}
	seen := 0
	for _, msg := range messages[:max(last, 0)] {
		if _, ok := toolResultOf(msg); ok {
			seen++
		}
	}
	return seen
}

// seenAgentToolResults is seenToolResults on the session's messages.
func seenAgentToolResults(messages []agent.AgentMessage) int {
	last := -1
	for i, message := range messages {
		if message.Assistant != nil {
			last = i
		}
	}
	seen := 0
	for _, message := range messages[:max(last, 0)] {
		if message.ToolResult != nil {
			seen++
		}
	}
	return seen
}

// clearedToolResult is the one-line note an old, large tool result is sent as.
func clearedToolResult(result ai.ToolResultMessage) ai.ToolResultMessage {
	chars, images := toolResultSize(result.Content)
	var size []string
	if chars > 0 {
		size = append(size, fmt.Sprintf("%d characters", chars))
	}
	if images > 0 {
		size = append(size, fmt.Sprintf("%d image(s)", images))
	}
	result.Content = []ai.ToolResultMessageContent{ai.TextContent{Text: fmt.Sprintf("[Old %s result cleared from the context to save space (%s). Run the tool again if you need it.]", result.ToolName, strings.Join(size, ", "))}}
	return result
}

// PruneToolResults replaces old, large tool results with a one-line note once the estimated context is at least TriggerTokens and
// there are at least 2*KeepRecent tool results, clearing those before the boundary floor((n-k)/k)*k, which moves in steps of
// KeepRecent (tool-result-pruning.ts pruneToolResults). It returns messages itself when nothing is cleared.
func PruneToolResults(messages []ai.Message, settings ToolResultPruningSettings) []ai.Message {
	out, _ := pruneToolResults(messages, settings)
	return out
}

// pruneToolResults is PruneToolResults that also returns the estimated tokens it cleared (tool-result-pruning.ts prunedTokens).
func pruneToolResults(messages []ai.Message, settings ToolResultPruningSettings) ([]ai.Message, int) {
	keepRecent := max(1, settings.KeepRecent)
	if !settings.Enabled {
		return messages, 0
	}
	toolResults, tokens := 0, 0
	for _, msg := range messages {
		if _, ok := toolResultOf(msg); ok {
			toolResults++
		}
		tokens += estimateLLMTokens(msg)
	}
	if tokens < settings.TriggerTokens || toolResults < 2*keepRecent {
		return messages, 0
	}
	clearBefore := min((toolResults-keepRecent)/keepRecent*keepRecent, seenToolResults(messages))
	var out []ai.Message
	cleared, index := 0, 0
	for i, msg := range messages {
		result, ok := toolResultOf(msg)
		if !ok {
			continue
		}
		index++
		if index > clearBefore {
			break
		}
		if chars, images := toolResultSize(result.Content); images == 0 && chars < settings.MinChars {
			continue
		}
		if out == nil {
			out = make([]ai.Message, len(messages))
			copy(out, messages)
		}
		note := clearedToolResult(result)
		out[i] = note
		cleared += estimateLLMTokens(msg) - estimateLLMTokens(note)
	}
	if out == nil {
		return messages, 0
	}
	return out, cleared
}

// TokensAfterPruning is tokens of messages as the next request sends them: less what pruning clears in it beyond what it cleared in
// the request whose usage tokens counts (the one before the response at usageIndex; with usageIndex < 0, tokens is the estimate of all
// of messages). Pruning clears in steps, so a step can take a context back below the compaction threshold that the size of the last
// request was above (tool-result-pruning.ts tokensAfterPruning).
func TokensAfterPruning(tokens int, messages []agent.AgentMessage, usageIndex int, settings ToolResultPruningSettings) int {
	if !settings.Enabled {
		return tokens
	}
	cleared := prunedAgentTokens(messages, settings)
	if usageIndex >= 0 {
		cleared -= prunedAgentTokens(messages[:usageIndex], settings)
	}
	return max(0, tokens-max(0, cleared))
}

// prunedAgentTokens is tool-result-pruning.ts prunedTokens(convertToLlm(messages), settings), read from the messages without
// converting the history: the check runs before every request, and Pi's WeakMap caches of the conversion have no Go counterpart. A
// user, assistant, tool-result or system message converts to one of the same estimate; only a custom message is converted to be
// estimated.
func prunedAgentTokens(messages []agent.AgentMessage, settings ToolResultPruningSettings) int {
	keepRecent := max(1, settings.KeepRecent)
	toolResults := 0
	for _, message := range messages {
		if message.ToolResult != nil {
			toolResults++
		}
	}
	if !settings.Enabled || toolResults < 2*keepRecent {
		return 0
	}
	clearBefore := min((toolResults-keepRecent)/keepRecent*keepRecent, seenAgentToolResults(messages))
	// The large results before the boundary, the ones pruning would clear: with none, nothing is cleared whatever the size of the
	// context, and the history need not be estimated.
	var large []*agent.ToolResultMessage
	index := 0
	for _, message := range messages {
		result := message.ToolResult
		if result == nil {
			continue
		}
		index++
		if index > clearBefore {
			break
		}
		if chars, images := toolResultSize(result.Content); images > 0 || chars >= settings.MinChars {
			large = append(large, result)
		}
	}
	if len(large) == 0 {
		return 0
	}
	tokens := 0
	for _, message := range messages {
		if message.Custom != nil {
			for _, converted := range agent.ConvertToLLM([]agent.AgentMessage{message}) {
				tokens += estimateLLMTokens(converted)
			}
			continue
		}
		tokens += EstimateTokens(message)
	}
	if tokens < settings.TriggerTokens {
		return 0
	}
	cleared := 0
	for _, result := range large {
		llm := ai.ToolResultMessage{ToolName: result.ToolName, Content: result.Content}
		cleared += estimateLLMTokens(llm) - estimateLLMTokens(clearedToolResult(llm))
	}
	return cleared
}
