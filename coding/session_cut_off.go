// Ports packages/coding-agent/src/core/cut-off-response.ts (µPi).

package coding

import (
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// µPi: a response cut off at the output limit with no tool call did nothing, and would end the run: the model is told and goes on, up to
// maxCutOffNotices times in a row, each time thinking one level less. In Terminal-Bench 2.1, glm-5.3-flash ended 2 of its first 14 runs this
// way, after 65,536 tokens of thinking, with most of the time left.

// CutOffMessageType is the customType of the notice after a response cut off at the output limit with nothing done.
const CutOffMessageType = "cut-off-response"

// cutOffNotice is the notice's text (cut-off-response.ts).
const cutOffNotice = "Your last response reached the output limit while you were still working it out, so nothing was done. Think in shorter steps: take the next concrete step now (a tool call, or write the result), and decide the rest from what it shows."

// maxCutOffNotices is how many such notices come in a row before the run ends as before.
const maxCutOffNotices = 3

// cutOffThinkingLevels are the thinking levels a cut-off response steps down through ("off" is never chosen for the model).
var cutOffThinkingLevels = []ai.ModelThinkingLevel{ai.ThinkingMinimal, ai.ThinkingLow, ai.ThinkingMedium, ai.ThinkingHigh, ai.ThinkingXHigh, ai.ThinkingMax}

// cutOffResponses counts the responses in a row cut off at the output limit with nothing done (cut-off-response.ts CutOffResponses).
type cutOffResponses struct {
	inARow atomic.Int32
}

// reset is a new prompt: a run that ended cut off must not leave it thinking less.
func (c *cutOffResponses) reset() { c.inARow.Store(0) }

// thinkingLevel is level, one level less for each response in a row cut off at the output limit (not below minimal). The session's own
// level stays as it is; only the requests use this one.
func (c *cutOffResponses) thinkingLevel(level ai.ModelThinkingLevel) ai.ModelThinkingLevel {
	at := slices.Index(cutOffThinkingLevels, level)
	inARow := int(c.inARow.Load())
	if inARow == 0 || at <= 0 {
		return level
	}
	return cutOffThinkingLevels[max(0, at-inARow)]
}

// answer returns the notice to put ahead of the next request when message was cut off at the output limit (maxTokens, the model's) with
// nothing done, or nil: the run goes on with it, thinking a level less.
func (c *cutOffResponses) answer(message *agent.AssistantMessage, maxTokens int) *agent.AgentMessage {
	if message == nil {
		return nil
	}
	if message.StopReason != ai.StopReasonLength {
		c.inARow.Store(0)
		return nil
	}
	// Only a response that did nothing but think: one cut off in its answer text ends as before (Pi's behaviour).
	for _, block := range message.Content {
		switch block := block.(type) {
		case ai.ToolCall:
			return nil
		case ai.TextContent:
			if strings.TrimSpace(block.Text) != "" {
				return nil
			}
		}
	}
	// Cut off below the output limit: the context ran out, which compact-and-retry handles.
	if maxTokens > 0 && icodingagent.IsRecoverableLength(message, maxTokens) {
		return nil
	}
	if c.inARow.Load() >= maxCutOffNotices {
		return nil
	}
	c.inARow.Add(1)
	notice := agent.AgentMessage{Custom: map[string]any{
		"role":       agent.RoleCustom,
		"customType": CutOffMessageType,
		"content":    cutOffNotice,
		"display":    true,
		"timestamp":  time.Now().UnixMilli(),
	}}.WithOrderRecord()
	return &notice
}

// requestThinkingLevel is the session's thinking level, one level less for each response in a row cut off at the output limit
// (agent-session.ts _requestThinkingLevel).
func (s *Session) requestThinkingLevel() ai.ModelThinkingLevel {
	return s.cutOffResponses.thinkingLevel(s.agent.ThinkingLevel())
}
