// SPDX-License-Identifier: MIT

package coding

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent/compaction"
)

// µPi (a4146262, a37b2029): ports the cached-prefix cases of packages/coding-agent/test/suite/agent-session-compaction.test.ts and
// agent-session-cut-off.test.ts (a18b202f).

// recordRequests wraps the session's stream function and records each request's transcript and options.
func recordRequests(s *Session) func() ([]ai.TranscriptContext, []ai.StreamOptions) {
	var mu sync.Mutex
	var contexts []ai.TranscriptContext
	var options []ai.StreamOptions
	stream := s.Agent().EffectiveStreamFunction()
	s.Agent().SetStreamFunction(func(ctx context.Context, model *ai.Model, transcript ai.TranscriptContext, opts ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		mu.Lock()
		contexts = append(contexts, transcript)
		options = append(options, opts)
		mu.Unlock()
		return stream(ctx, model, transcript, opts)
	})
	return func() ([]ai.TranscriptContext, []ai.StreamOptions) {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(contexts), slices.Clone(options)
	}
}

func messagesJSON(t *testing.T, transcript ai.TranscriptContext) []string {
	t.Helper()
	var out []string
	for _, message := range transcript.Messages() {
		raw, err := json.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, string(raw))
	}
	return out
}

// As deepseek-harness compacts: the provider reads everything but the instruction from its prompt cache.
func TestCompactionRequestIsTheSessionsLastRequestPlusTheInstruction(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{settings: `{"compaction":{"keepRecentTokens":1}}`},
		fauxReply("first answer", ai.StopReasonStop, 0), fauxReply("second answer", ai.StopReasonStop, 0), fauxReply("## Goal\nThe summary.", ai.StopReasonStop, 0))
	requests := recordRequests(h.session)
	promptHarness(t, h, "first question")
	promptHarness(t, h, "second question")
	result, err := h.session.Compact(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	contexts, options := requests()
	if len(contexts) != 3 {
		t.Fatalf("%d requests, want 3", len(contexts))
	}
	previous, summary := messagesJSON(t, contexts[1]), messagesJSON(t, contexts[2])
	// The second request, then the second answer, then the instruction.
	if len(summary) != len(previous)+2 || !slices.Equal(summary[:len(previous)], previous) {
		t.Fatalf("summary request %v\ndoes not continue the previous one %v", summary, previous)
	}
	var instruction string
	if user, ok := contexts[2].Messages()[len(summary)-1].(ai.UserMessage); ok {
		blocks, _ := user.Content.(ai.UserContentBlocks)
		for _, block := range blocks {
			if block, ok := block.(ai.TextContent); ok {
				instruction += block.Text
			}
		}
	}
	if !strings.Contains(instruction, "Do not call any tool") {
		t.Fatalf("instruction %s", instruction)
	}
	// Where the summary ends: the first message kept, quoted.
	if !strings.Contains(instruction, "<kept>\n[Assistant]: second answer\n</kept>") {
		t.Fatalf("instruction does not quote the first kept message: %s", instruction)
	}
	if options[2].CacheRetention != "" {
		t.Fatalf("cache retention %q, want the session's", options[2].CacheRetention)
	}
	// The summary's output cap, as the standalone request has it.
	if want := compaction.GetSummaryMaxTokens(h.session.Model(), compaction.DefaultCompactionSettings, false); options[2].MaxTokens != want {
		t.Fatalf("maxTokens %d, want %d", options[2].MaxTokens, want)
	}
	if options[2].SessionID == "" || options[2].SessionID != options[1].SessionID {
		t.Fatalf("session ids %q and %q", options[2].SessionID, options[1].SessionID)
	}
	if !strings.Contains(result.Summary, "The summary.") {
		t.Fatalf("summary %q", result.Summary)
	}
}

func TestCompactionFallsBackToTheStandaloneRequestWhenTheCachedOneCallsATool(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{settings: `{"compaction":{"keepRecentTokens":1}}`}, fauxToolCall("read"), fauxReply("standalone summary", ai.StopReasonStop, 0))
	suiteCompactionSeed(t, h.session)
	requests := recordRequests(h.session)
	result, err := h.session.Compact(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	contexts, _ := requests()
	if len(contexts) != 2 {
		t.Fatalf("%d requests, want 2", len(contexts))
	}
	if got := ai.GetCurrentSystemPrompt(contexts[0].Messages()); got != h.session.agent.SystemPrompt() {
		t.Fatalf("cached request's system prompt %q, want the session's", got)
	}
	if got := ai.GetCurrentSystemPrompt(contexts[1].Messages()); !strings.Contains(got, "context summarization assistant") {
		t.Fatalf("standalone request's system prompt %q", got)
	}
	if !strings.Contains(result.Summary, "standalone summary") {
		t.Fatalf("summary %q", result.Summary)
	}
}

// A standalone request it can avoid falls back (an answer cut off with reasoning on asks again without); an error it would meet too fails
// the compaction without a second request (compaction.ts compactOnCachedPrefix).
func TestCompactionOnTheCachedPrefixFailsOrFallsBack(t *testing.T) {
	t.Run("an error fails the compaction", func(t *testing.T) {
		h := newRecoveryHarness(t, harnessOptions{settings: `{"compaction":{"keepRecentTokens":1}}`}, fauxError("quota exceeded"), fauxReply("standalone summary", ai.StopReasonStop, 0))
		suiteCompactionSeed(t, h.session)
		_, err := h.session.Compact(t.Context(), "")
		if err == nil || err.Error() != "Summarization failed: quota exceeded" || h.provider.callCount() != 1 {
			t.Fatalf("error %v after %d requests", err, h.provider.callCount())
		}
	})
	t.Run("an answer cut off without reasoning fails the compaction", func(t *testing.T) {
		h := newRecoveryHarness(t, harnessOptions{settings: `{"compaction":{"keepRecentTokens":1}}`}, fauxReply("partial", ai.StopReasonLength, 0), fauxReply("standalone summary", ai.StopReasonStop, 0))
		suiteCompactionSeed(t, h.session)
		_, err := h.session.Compact(t.Context(), "")
		if err == nil || !strings.Contains(err.Error(), "generation hit the token cap") || h.provider.callCount() != 1 {
			t.Fatalf("error %v after %d requests", err, h.provider.callCount())
		}
	})
	t.Run("an overflow falls back", func(t *testing.T) {
		h := newRecoveryHarness(t, harnessOptions{settings: `{"compaction":{"keepRecentTokens":1}}`}, fauxError("prompt is too long: 213462 tokens > 200000 maximum"), fauxReply("standalone summary", ai.StopReasonStop, 0))
		suiteCompactionSeed(t, h.session)
		result, err := h.session.Compact(t.Context(), "")
		if err != nil || !strings.Contains(result.Summary, "standalone summary") || h.provider.callCount() != 2 {
			t.Fatalf("result %+v error %v after %d requests", result, err, h.provider.callCount())
		}
	})
	t.Run("turned off, the standalone request is sent", func(t *testing.T) {
		t.Cleanup(compaction.SetCachedPrefix(false))
		h := newRecoveryHarness(t, harnessOptions{settings: `{"compaction":{"keepRecentTokens":1}}`}, fauxReply("standalone summary", ai.StopReasonStop, 0))
		suiteCompactionSeed(t, h.session)
		requests := recordRequests(h.session)
		if _, err := h.session.Compact(t.Context(), ""); err != nil {
			t.Fatal(err)
		}
		contexts, options := requests()
		if len(contexts) != 1 || !strings.Contains(ai.GetCurrentSystemPrompt(contexts[0].Messages()), "context summarization assistant") || options[0].CacheRetention != ai.CacheRetentionNone {
			t.Fatalf("requests %d, options %+v", len(contexts), options)
		}
	})
}

// agent-session-cut-off.test.ts "asks the summary on the cached prefix at the thinking level the requests before it used" (a37b2029).
func TestCachedPrefixSummaryThinksAsTheRequestsBeforeIt(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{maxTokens: cutOffMaxTokens, reasoning: true, settings: `{"compaction":{"keepRecentTokens":1}}`},
		fauxCutOff(false), fauxCutOff(false), fauxCutOff(false), fauxCutOff(false), fauxReply("## Goal\nSummary.", ai.StopReasonStop, 0))
	if err := h.session.SetThinkingLevel(ai.ThinkingMedium); err != nil {
		t.Fatal(err)
	}
	levels := recordThinkingLevels(h)
	promptHarness(t, h, "solve it")
	if _, err := h.session.Compact(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	if got := levels(); !slices.Equal(got, []string{"medium", "low", "minimal", "minimal", "minimal"}) {
		t.Fatalf("thinking levels %v", got)
	}
}
