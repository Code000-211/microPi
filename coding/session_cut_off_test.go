package coding

import (
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent/jobs"
)

// µPi: ports of packages/coding-agent/test/suite/agent-session-cut-off.test.ts (a18b202f).

// cutOffMaxTokens is the faux model's output limit.
const cutOffMaxTokens = 100

// fauxCutOff is a response of thinking only, cut off at the output limit (or, short, below it: the context ran out).
func fauxCutOff(short bool) scriptedResponse {
	return func([]ai.Message) *ai.AssistantMessage {
		output, thinking := cutOffMaxTokens, strings.Repeat("Let me consider every case before acting. ", 12)
		if short {
			output, thinking = 4, "Let me think."
		}
		return &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.ThinkingContent{Thinking: thinking}}, Provider: "faux", Model: "faux-1",
			StopReason: ai.StopReasonLength, Usage: ai.Usage{Input: 10, Output: output, TotalTokens: 10 + output}, Timestamp: time.Now().UnixMilli()}
	}
}

func cutOffNotices(h *recoveryHarness) int {
	return len(customTypes(h, CutOffMessageType))
}

// customTypes lists the customType of every custom message of the session, or only those of type when it is not empty.
func customTypes(h *recoveryHarness, only string) []string {
	var out []string
	for _, message := range h.session.Messages() {
		if message.Custom == nil {
			continue
		}
		customType, _ := message.Custom["customType"].(string)
		if only == "" || customType == only {
			out = append(out, customType)
		}
	}
	return out
}

// recordThinkingLevels records the reasoning option of each request.
func recordThinkingLevels(h *recoveryHarness) func() []string {
	var mu sync.Mutex
	var levels []string
	h.provider.estimateUsage = func(_ ai.TranscriptContext, options ai.StreamOptions, _ *ai.AssistantMessage) {
		mu.Lock()
		levels = append(levels, string(options.Thinking))
		mu.Unlock()
	}
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(levels)
	}
}

func lastAssistant(h *recoveryHarness) *agent.AssistantMessage {
	messages := h.session.Messages()
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Assistant != nil {
			return messages[i].Assistant
		}
	}
	return nil
}

func TestCutOffResponseTellsTheModelAndGoesOn(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{maxTokens: cutOffMaxTokens}, fauxCutOff(false), fauxReply("Done.", ai.StopReasonStop, 0))
	promptHarness(t, h, "solve it")
	if got := h.provider.callCount(); got != 2 {
		t.Fatalf("model calls = %d, want 2", got)
	}
	if got := cutOffNotices(h); got != 1 {
		t.Fatalf("notices = %d, want 1", got)
	}
	if last := lastAssistant(h); last == nil || last.StopReason != ai.StopReasonStop {
		t.Fatalf("last response = %+v, want a stop", last)
	}
	// The notice is TS's text, displayed and saved.
	for _, message := range h.session.Messages() {
		if message.Custom != nil && message.Custom["customType"] == CutOffMessageType {
			if message.Custom["content"] != cutOffNotice || message.Custom["display"] != true {
				t.Fatalf("notice = %+v", message.Custom)
			}
		}
	}
	if !strings.HasPrefix(cutOffNotice, "Your last response reached the output limit while you were still working it out, so nothing was done. Think in shorter steps") {
		t.Fatalf("notice text = %q", cutOffNotice)
	}
}

func TestCutOffResponsesEndTheRunAfterThreeInARow(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{maxTokens: cutOffMaxTokens},
		fauxCutOff(false), fauxCutOff(false), fauxCutOff(false), fauxCutOff(false), fauxReply("never reached", ai.StopReasonStop, 0))
	promptHarness(t, h, "solve it")
	if got := cutOffNotices(h); got != 3 {
		t.Fatalf("notices = %d, want 3", got)
	}
	if got := h.provider.callCount(); got != 4 {
		t.Fatalf("model calls = %d, want 4 (one response left)", got)
	}
}

func TestCutOffResponsesThinkOneLevelLessAndAsBeforeAfterANormalResponse(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{maxTokens: cutOffMaxTokens, reasoning: true},
		fauxCutOff(false), fauxCutOff(false), fauxReply("Done.", ai.StopReasonStop, 0), fauxReply("Again.", ai.StopReasonStop, 0))
	if err := h.session.SetThinkingLevel(ai.ThinkingMedium); err != nil {
		t.Fatal(err)
	}
	levels := recordThinkingLevels(h)
	promptHarness(t, h, "solve it")
	promptHarness(t, h, "once more")
	if got, want := levels(), []string{"medium", "low", "minimal", "medium"}; !slices.Equal(got, want) {
		t.Fatalf("levels = %v, want %v", got, want)
	}
	if got := h.session.ThinkingLevel(); got != ai.ThinkingMedium {
		t.Fatalf("session thinking level = %q, want medium", got)
	}
}

func TestCutOffRunStartsTheNextPromptAtTheSessionsThinkingLevel(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{maxTokens: cutOffMaxTokens, reasoning: true},
		fauxCutOff(false), fauxCutOff(false), fauxCutOff(false), fauxCutOff(false), fauxCutOff(false), fauxReply("Done.", ai.StopReasonStop, 0))
	if err := h.session.SetThinkingLevel(ai.ThinkingHigh); err != nil {
		t.Fatal(err)
	}
	levels := recordThinkingLevels(h)
	promptHarness(t, h, "solve it")
	promptHarness(t, h, "once more")
	if got, want := levels(), []string{"high", "medium", "low", "minimal", "high", "medium"}; !slices.Equal(got, want) {
		t.Fatalf("levels = %v, want %v", got, want)
	}
	if got := cutOffNotices(h); got != 4 {
		t.Fatalf("notices = %d, want 4", got)
	}
}

func TestCutOffNoticeGoesAheadOfAQueuedJobReport(t *testing.T) {
	var h *recoveryHarness
	queueJobReport := func(response scriptedResponse) scriptedResponse {
		return func(request []ai.Message) *ai.AssistantMessage {
			_ = h.session.SendCustomMessage(t.Context(), extension.CustomMessageRef{CustomType: jobs.BackgroundJobMessageType, Content: "Background job bg1 ended.", Display: true},
				&extension.SendMessageOptions{DeliverAs: extension.DeliverAsSteer})
			return response(request)
		}
	}
	h = newRecoveryHarness(t, harnessOptions{maxTokens: cutOffMaxTokens, reasoning: true},
		queueJobReport(fauxCutOff(false)), fauxReply("x", ai.StopReasonStop, 0), fauxReply("Done.", ai.StopReasonStop, 0))
	if err := h.session.SetThinkingLevel(ai.ThinkingMedium); err != nil {
		t.Fatal(err)
	}
	levels := recordThinkingLevels(h)
	promptHarness(t, h, "solve it")
	if got, want := customTypes(h, ""), []string{CutOffMessageType, jobs.BackgroundJobMessageType}; !slices.Equal(got, want) {
		t.Fatalf("custom messages = %v, want %v", got, want)
	}
	if got, want := levels(), []string{"medium", "low", "medium"}; !slices.Equal(got, want) {
		t.Fatalf("levels = %v, want %v", got, want)
	}
}

func TestCutOffBelowTheOutputLimitIsLeftToCompactAndRetry(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{maxTokens: cutOffMaxTokens}, fauxCutOff(true), fauxReply("Done.", ai.StopReasonStop, 0), fauxReply("Done.", ai.StopReasonStop, 0))
	_ = h.session.Prompt(t.Context(), "solve it", nil)
	if got := cutOffNotices(h); got != 0 {
		t.Fatalf("notices = %d, want 0", got)
	}
}

func TestCutOffLeavesTextCutMidAnswerAndNormalResponsesAlone(t *testing.T) {
	cutText := func([]ai.Message) *ai.AssistantMessage {
		return &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "The answer is"}}, Provider: "faux", Model: "faux-1",
			StopReason: ai.StopReasonLength, Usage: ai.Usage{Input: 10, Output: cutOffMaxTokens}, Timestamp: time.Now().UnixMilli()}
	}
	h := newRecoveryHarness(t, harnessOptions{maxTokens: cutOffMaxTokens}, cutText, fauxReply("Done.", ai.StopReasonStop, 0), fauxReply("never reached", ai.StopReasonStop, 0))
	promptHarness(t, h, "solve it")
	promptHarness(t, h, "again")
	if got := cutOffNotices(h); got != 0 {
		t.Fatalf("notices = %d, want 0", got)
	}
	if got := h.provider.callCount(); got != 2 {
		t.Fatalf("model calls = %d, want 2", got)
	}
}

func TestCutOffThinkingLevelStopsAtMinimal(t *testing.T) {
	var c cutOffResponses
	c.inARow.Store(5)
	if got := c.thinkingLevel(ai.ThinkingHigh); got != ai.ThinkingMinimal {
		t.Fatalf("high after 5 = %q, want minimal", got)
	}
	if got := c.thinkingLevel(ai.ThinkingOff); got != ai.ThinkingOff {
		t.Fatalf("off = %q, want off", got)
	}
	if got := c.thinkingLevel(ai.ThinkingMinimal); got != ai.ThinkingMinimal {
		t.Fatalf("minimal = %q, want minimal", got)
	}
}
