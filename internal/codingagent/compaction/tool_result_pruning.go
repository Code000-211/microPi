package compaction

import (
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
)

// ToolResultPruningSettings configures step-wise clearing of old large tool results.
type ToolResultPruningSettings struct {
	Enabled       bool
	TriggerTokens int
	KeepRecent    int
	MinChars      int
}

// DefaultToolResultPruningSettings returns microPi's default pruning settings.
func DefaultToolResultPruningSettings() ToolResultPruningSettings {
	return ToolResultPruningSettings{
		Enabled:       true,
		TriggerTokens: 40_000,
		KeepRecent:    8,
		MinChars:      2_000,
	}
}

func toolResultSize(content []ai.ToolResultMessageContent) (chars int, images int) {
	for _, block := range content {
		switch b := block.(type) {
		case ai.TextContent:
			chars += len(b.Text)
		case *ai.TextContent:
			if b != nil {
				chars += len(b.Text)
			}
		case ai.ImageContent:
			images++
		case *ai.ImageContent:
			if b != nil {
				images++
			}
		}
	}
	return chars, images
}

func estimateAIMessageTokens(msg ai.Message) int {
	chars := 0
	images := 0
	switch m := msg.(type) {
	case ai.UserMessage:
		switch c := m.Content.(type) {
		case ai.UserText:
			chars += len(c)
		case ai.UserContentBlocks:
			for _, b := range c {
				switch blk := b.(type) {
				case ai.TextContent:
					chars += len(blk.Text)
				case ai.ImageContent:
					images++
				}
			}
		}
	case *ai.UserMessage:
		if m != nil {
			return estimateAIMessageTokens(*m)
		}
	case ai.AssistantMessage:
		for _, b := range m.Content {
			switch blk := b.(type) {
			case ai.TextContent:
				chars += len(blk.Text)
			case ai.ThinkingContent:
				chars += len(blk.Thinking)
			case ai.ToolCall:
				chars += len(blk.Name) + 64
			}
		}
	case *ai.AssistantMessage:
		if m != nil {
			return estimateAIMessageTokens(*m)
		}
	case ai.ToolResultMessage:
		c, img := toolResultSize(m.Content)
		chars += c
		images += img
	case *ai.ToolResultMessage:
		if m != nil {
			return estimateAIMessageTokens(*m)
		}
	}
	return (chars+3)/4 + images*1200
}

// PruneToolResults replaces old, large tool results with a compact placeholder once
// the estimated context exceeds TriggerTokens, moving the boundary in steps of KeepRecent.
func PruneToolResults(messages []ai.Message, settings ToolResultPruningSettings) []ai.Message {
	if !settings.Enabled {
		return messages
	}
	keepRecent := max(1, settings.KeepRecent)
	toolResults := 0
	tokens := 0
	for _, msg := range messages {
		if _, ok := msg.(ai.ToolResultMessage); ok {
			toolResults++
		} else if _, ok := msg.(*ai.ToolResultMessage); ok {
			toolResults++
		}
		tokens += estimateAIMessageTokens(msg)
	}
	if tokens < settings.TriggerTokens || toolResults <= keepRecent {
		return messages
	}
	clearBefore := toolResults - keepRecent
	index := 0
	changed := false
	out := make([]ai.Message, len(messages))
	copy(out, messages)

	for i, msg := range messages {
		var tr ai.ToolResultMessage
		switch m := msg.(type) {
		case ai.ToolResultMessage:
			tr = m
		case *ai.ToolResultMessage:
			if m == nil {
				continue
			}
			tr = *m
		default:
			continue
		}
		if index >= clearBefore {
			index++
			continue
		}
		index++
		chars, images := toolResultSize(tr.Content)
		if images == 0 && chars < settings.MinChars {
			continue
		}
		var sizeParts []string
		if chars > 0 {
			sizeParts = append(sizeParts, fmt.Sprintf("%d characters", chars))
		}
		if images > 0 {
			sizeParts = append(sizeParts, fmt.Sprintf("%d image(s)", images))
		}
		note := fmt.Sprintf("[Old %s result cleared from the context to save space (%s). Run the tool again if you need it.]", tr.ToolName, strings.Join(sizeParts, ", "))
		tr.Content = []ai.ToolResultMessageContent{ai.TextContent{Text: note}}
		out[i] = tr
		changed = true
	}
	if !changed {
		return messages
	}
	return out
}
