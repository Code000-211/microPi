package compaction

import (
	"context"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// µPi (8f722e90, a72998f3): the bounds and the fit of compaction-ahead.ts.

func aheadSession(t *testing.T, turns int) (*codingagent.Session, []string) {
	t.Helper()
	session := codingagent.NewSession("ahead", t.TempDir())
	var ids []string
	for n := range turns {
		for _, message := range []agent.AgentMessage{
			{User: &agent.UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: strings.Repeat("q", 4000)}}, Timestamp: int64(2 * n)}},
			{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: "a"}}, StopReason: ai.StopReasonStop, Timestamp: int64(2*n + 1)}},
		} {
			id, err := session.AppendMessage(message)
			if err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
	}
	return session, ids
}

func TestCompactionAheadStartsNearTheThreshold(t *testing.T) {
	session, _ := aheadSession(t, 4)
	settings := CompactionSettings{Enabled: true, ReserveTokens: 1000, KeepRecentTokens: 1}
	started := 0
	summarize := func(CompactionPreparation) func(context.Context) (CompactionResult, error) {
		started++
		return func(context.Context) (CompactionResult, error) { return CompactionResult{Summary: "ahead"}, nil }
	}
	var ahead CompactionAhead
	update := func(tokens int, settings CompactionSettings) bool {
		return ahead.Update(AheadUpdate{ContextTokens: tokens, ContextWindow: 100_000, Session: session, Branch: func() []codingagent.SessionEntry { return session.GetBranch() }, Settings: settings, Summarize: summarize})
	}
	// Threshold 99,000: the summary starts above 84,150, and the threshold is the limit.
	if update(84_150, settings) || started != 0 {
		t.Fatalf("started %d at 84,150", started)
	}
	// A context so small that the kept part alone would be near the threshold again makes none.
	if update(90_000, CompactionSettings{Enabled: true, ReserveTokens: 1000, KeepRecentTokens: 45_000}) || started != 0 {
		t.Fatalf("started %d with keepRecentTokens 45,000", started)
	}
	if !update(90_000, settings) || started != 1 {
		t.Fatalf("started %d at 90,000, want 1 that the compaction waits for", started)
	}
	ahead.Wait(t.Context())
	// Made: the compaction no longer waits, and takes it.
	if update(95_000, settings) || started != 1 {
		t.Fatalf("started %d, or waits for a summary already made", started)
	}
	if result := ahead.Take(t.Context(), session, session.GetBranch(), settings); result == nil || result.Summary != "ahead" {
		t.Fatalf("taken %+v", result)
	}
	if ahead.Pending() {
		t.Fatal("taken twice")
	}
}

func TestCompactionAheadFitsUntilWhatItCoversChanges(t *testing.T) {
	settings := CompactionSettings{Enabled: true, ReserveTokens: 1000, KeepRecentTokens: 1}
	start := func(t *testing.T) (*CompactionAhead, *codingagent.Session, []string) {
		session, ids := aheadSession(t, 4)
		ahead := &CompactionAhead{}
		ahead.Update(AheadUpdate{ContextTokens: 90_000, ContextWindow: 100_000, Session: session, Branch: func() []codingagent.SessionEntry { return session.GetBranch() }, Settings: settings,
			Summarize: func(CompactionPreparation) func(context.Context) (CompactionResult, error) {
				return func(context.Context) (CompactionResult, error) { return CompactionResult{Summary: "ahead"}, nil }
			}})
		ahead.Wait(t.Context())
		return ahead, session, ids
	}
	t.Run("an edit of a kept entry", func(t *testing.T) {
		ahead, session, ids := start(t)
		if _, err := session.AppendContextEdit(ids[len(ids)-1], nil); err != nil {
			t.Fatal(err)
		}
		if ahead.Take(t.Context(), session, session.GetBranch(), settings) == nil {
			t.Fatal("an edit of the kept part dropped the summary")
		}
	})
	t.Run("an edit of a summarized entry", func(t *testing.T) {
		ahead, session, ids := start(t)
		if _, err := session.AppendContextEdit(ids[0], nil); err != nil {
			t.Fatal(err)
		}
		if ahead.Take(t.Context(), session, session.GetBranch(), settings) != nil {
			t.Fatal("an edit of a summarized entry kept the summary")
		}
	})
	t.Run("another keepRecentTokens", func(t *testing.T) {
		ahead, session, _ := start(t)
		if ahead.Take(t.Context(), session, session.GetBranch(), CompactionSettings{Enabled: true, ReserveTokens: 1000, KeepRecentTokens: 2}) != nil {
			t.Fatal("a summary for another kept part was taken")
		}
	})
	t.Run("another session", func(t *testing.T) {
		ahead, _, _ := start(t)
		other, _ := aheadSession(t, 4)
		if ahead.Take(t.Context(), other, other.GetBranch(), settings) != nil {
			t.Fatal("a summary of another session was taken")
		}
	})
}

func TestCachedPrefixInstructionAndKeptQuote(t *testing.T) {
	if got := cachedPrefixInstruction("", false, false, ""); !strings.HasPrefix(got, "The conversation above is being compacted to free context: your summary replaces all of it.\n\nDo not call any tool.") || strings.Contains(got, "<kept>") {
		t.Fatalf("instruction without a kept message: %q", got)
	}
	got := cachedPrefixInstruction("[User]: go on", true, true, "keep the plan")
	for _, part := range []string{"It begins with a summary of earlier history: carry that content forward into yours.", "That message is in the middle of a turn: include what that turn was doing up to it.", "First message kept:\n<kept>\n[User]: go on\n</kept>", SUMMARIZATION_PROMPT + "\n\nAdditional focus: keep the plan"} {
		if !strings.Contains(got, part) {
			t.Fatalf("instruction %q lacks %q", got, part)
		}
	}
	long := agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: strings.Repeat("x", 400)}}}}
	if quote := DescribeKeptMessage(long); quote != "[User]: "+strings.Repeat("x", 300-len("[User]: "))+"..." {
		t.Fatalf("quote %q", quote)
	}
}
