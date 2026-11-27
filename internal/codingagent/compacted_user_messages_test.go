package codingagent

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// µPi (d8371227, 0abf563e, 4aab2a69): ports compaction-user-messages.test.ts, "the user's messages beside a compaction summary".

const compactedTicket = "Rename foo to bar in src/a.ts. Do not touch tests/. Keep the public API."

func plainUserMsg(text string) agent.AgentMessage {
	return agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserText(text), Timestamp: time.Now().UnixMilli()}}
}

func cumAppend(t *testing.T, s *Session, message agent.AgentMessage) string {
	t.Helper()
	id, err := s.AppendMessage(message)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func cumCompact(t *testing.T, s *Session, summary, keptID string, fromHook bool) {
	t.Helper()
	if _, err := s.AppendCompaction(summary, keptID, 5000, nil, fromHook, nil); err != nil {
		t.Fatal(err)
	}
}

// compactedTestSession is a ticket, its work, a follow-up from the user, a harness message, then a compaction that keeps only the
// last reply.
func compactedTestSession(t *testing.T) *Session {
	s := NewSession("compacted", t.TempDir())
	cumAppend(t, s, mkUserMsg(compactedTicket))
	cumAppend(t, s, mkAssistantMsg("working"))
	cumAppend(t, s, plainUserMsg("also update NOTES.md"))
	if _, err := s.AppendCustomMessageEntry("check-report", "src/a.ts:3 error", false, nil); err != nil {
		t.Fatal(err)
	}
	kept := cumAppend(t, s, mkAssistantMsg("done"))
	cumCompact(t, s, "The user asked for a rename.", kept, false)
	return s
}

// summaryRequests returns the userRequests of the compaction summary message of each projection the session builds; they must agree.
func summaryRequests(t *testing.T, s *Session) []string {
	t.Helper()
	find := func(messages []agent.AgentMessage) (agent.AgentMessage, bool) {
		for _, message := range messages {
			if message.Role() == agent.RoleCompactionSummary {
				return message, true
			}
		}
		return agent.AgentMessage{}, false
	}
	message, ok := find(s.BuildSessionProjection().Messages)
	if !ok {
		t.Fatal("no compaction summary in the projection")
	}
	requests := agent.CompactionSummaryUserRequests(message.Custom)
	view, _ := find(s.BuildSessionProjectionView().Messages)
	context, _ := find(s.BuildContext(nil))
	// The projection of the whole branch, as Pi's buildSessionProjection(getBranch()) computes it, agrees with the session's own.
	whole, _ := find(ProjectSessionPath(s.GetBranch()).Messages)
	for name, other := range map[string]agent.AgentMessage{"view": view, "context": context, "whole branch": whole} {
		if got := agent.CompactionSummaryUserRequests(other.Custom); !slices.Equal(got, requests) {
			t.Fatalf("%s projection carries %q, the projection %q", name, got, requests)
		}
	}
	return requests
}

func TestCompactionSummaryKeepsTheUsersOwnMessagesVerbatim(t *testing.T) {
	s := compactedTestSession(t)
	requests := summaryRequests(t, s)
	if want := []string{compactedTicket, "also update NOTES.md"}; !slices.Equal(requests, want) {
		t.Fatalf("userRequests = %q, want %q", requests, want)
	}
	var summary agent.AgentMessage
	for _, message := range s.BuildSessionProjection().Messages {
		if message.Role() == agent.RoleCompactionSummary {
			summary = message
		}
	}
	llm := agent.ConvertToLLM([]agent.AgentMessage{summary})
	user, ok := llm[0].(ai.UserMessage)
	if !ok {
		t.Fatalf("summary converts to %T", llm[0])
	}
	text := user.Content.(ai.UserContentBlocks)[0].(ai.TextContent).Text
	want := "The conversation history before this point was compacted into the following summary:\n\n<summary>\nThe user asked for a rename.\n</summary>\n\n" +
		"The user's messages from the compacted history, verbatim and oldest first:\n<user-message>\n" + compactedTicket + "\n</user-message>\n<user-message>\nalso update NOTES.md\n</user-message>"
	if text != want {
		t.Fatalf("summary text:\n%s\nwant:\n%s", text, want)
	}
	if strings.Contains(text, "src/a.ts:3 error") {
		t.Fatal("the harness's message is carried")
	}
}

func TestCompactionSummaryKeepsThemThroughASecondCompaction(t *testing.T) {
	s := compactedTestSession(t)
	cumAppend(t, s, plainUserMsg("and run the tests"))
	kept := cumAppend(t, s, mkAssistantMsg("tests pass"))
	cumCompact(t, s, "Rename done, tests run.", kept, false)
	if got, want := summaryRequests(t, s), []string{compactedTicket, "also update NOTES.md", "and run the tests"}; !slices.Equal(got, want) {
		t.Fatalf("userRequests = %q, want %q", got, want)
	}
}

// The second compaction's requests are read from a session file reopened, where the messages before the kept range are not resident.
func TestCompactionSummaryReadsThemFromAReopenedSession(t *testing.T) {
	sm := tempSessionMgr(t)
	s, err := sm.Create("reopened", "")
	if err != nil {
		t.Fatal(err)
	}
	cumAppend(t, s, mkUserMsg(compactedTicket))
	kept := cumAppend(t, s, mkAssistantMsg("done"))
	cumCompact(t, s, "first", kept, false)
	cumAppend(t, s, plainUserMsg("next"))
	kept = cumAppend(t, s, mkAssistantMsg("ok"))
	cumCompact(t, s, "second", kept, false)
	reopened, err := sm.Open(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := summaryRequests(t, reopened), []string{compactedTicket, "next"}; !slices.Equal(got, want) {
		t.Fatalf("userRequests = %q, want %q", got, want)
	}
}

func TestCompactionSummaryLeavesOutUserMessagesTheCompactionKept(t *testing.T) {
	s := NewSession("kept", t.TempDir())
	cumAppend(t, s, plainUserMsg(compactedTicket))
	cumAppend(t, s, mkAssistantMsg("working"))
	kept := cumAppend(t, s, plainUserMsg("kept request"))
	cumAppend(t, s, mkAssistantMsg("ok"))
	cumCompact(t, s, "summary", kept, false)
	if got := summaryRequests(t, s); !slices.Equal(got, []string{compactedTicket}) {
		t.Fatalf("userRequests = %q", got)
	}
}

func TestCompactionSummaryCutsWhatGoesBeyondTheLimit(t *testing.T) {
	s := NewSession("cut", t.TempDir())
	cumAppend(t, s, plainUserMsg(strings.Repeat("x", 20_000)))
	cumAppend(t, s, plainUserMsg("later"))
	kept := cumAppend(t, s, mkAssistantMsg("ok"))
	cumCompact(t, s, "summary", kept, false)
	got := summaryRequests(t, s)
	if len(got) != 1 || got[0] != strings.Repeat("x", 16_000)+"\n[cut here: the rest is in the summary]" {
		t.Fatalf("userRequests = %d messages, first %d bytes", len(got), len(got[0]))
	}
}

// The limit counts UTF-16 units, as String.length does: a message of 8,000 emoji (16,000 units) fits, one more cuts it.
func TestCompactedUserMessagesCountUTF16Units(t *testing.T) {
	fits := strings.Repeat("😀", 8_000)
	if kept, complete := userMessagesBeforeTexts([]string{fits}, 1); !complete || kept[0] != fits {
		t.Fatal("a message of 16,000 UTF-16 units was cut")
	}
	if _, complete := userMessagesBeforeTexts([]string{fits + "a"}, 1); complete {
		t.Fatal("a message of 16,001 UTF-16 units was not cut")
	}
	if kept, _ := userMessagesBeforeTexts([]string{fits, "b"}, 2); len(kept) != 1 {
		t.Fatalf("a second message fit beside 16,000 units: %d kept", len(kept))
	}
}

func TestCompactionSummaryKeepsTheTaskAndTheNewestThatFit(t *testing.T) {
	s := NewSession("newest", t.TempDir())
	cumAppend(t, s, plainUserMsg("task "+strings.Repeat("t", 6000)))
	for i := 1; i <= 5; i++ {
		cumAppend(t, s, plainUserMsg("m"+string(rune('0'+i))+" "+strings.Repeat("m", 4000)))
	}
	kept := cumAppend(t, s, mkAssistantMsg("ok"))
	cumCompact(t, s, "summary", kept, false)
	var starts []string
	for _, text := range summaryRequests(t, s) {
		starts = append(starts, text[:4])
	}
	if want := []string{"task", "m4 m", "m5 m"}; !slices.Equal(starts, want) {
		t.Fatalf("starts = %q, want %q", starts, want)
	}
}

func TestCompactionSummaryLeavesAnExtensionsCompactionAsWritten(t *testing.T) {
	s := NewSession("hook", t.TempDir())
	cumAppend(t, s, plainUserMsg(compactedTicket))
	kept := cumAppend(t, s, mkAssistantMsg("ok"))
	cumCompact(t, s, "exact handoff", kept, true)
	for _, message := range s.BuildSessionProjection().Messages {
		if message.Role() == agent.RoleCompactionSummary {
			if _, present := message.Custom["userRequests"]; present {
				t.Fatal("an extension's compaction carries the user's messages")
			}
		}
	}
}

func TestCompactionSummaryCarriesNothingWhenTheFeatureIsOff(t *testing.T) {
	t.Cleanup(SetCompactionUserMessages(false))
	for _, message := range compactedTestSession(t).BuildSessionProjection().Messages {
		if _, present := message.Custom["userRequests"]; present {
			t.Fatal("userRequests with the feature off")
		}
	}
}

func TestCompactionSummaryLeavesOutAMessageALaterOneContains(t *testing.T) {
	s := NewSession("continued", t.TempDir())
	cumAppend(t, s, plainUserMsg(compactedTicket))
	cumAppend(t, s, mkAssistantMsg("round 1"))
	cumAppend(t, s, plainUserMsg(compactedTicket+"\n\nNotes from the last round: x"))
	kept := cumAppend(t, s, mkAssistantMsg("round 2"))
	cumCompact(t, s, "summary", kept, false)
	if got, want := summaryRequests(t, s), []string{compactedTicket + "\n\nNotes from the last round: x"}; !slices.Equal(got, want) {
		t.Fatalf("userRequests = %q, want %q", got, want)
	}
}
