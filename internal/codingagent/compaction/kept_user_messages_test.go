package compaction

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/compactiontypes"
)

// µPi (d8371227, 0abf563e, 4aab2a69): ports compaction-user-messages.test.ts, the preparation and the summary request's instruction.

const keptTicket = "Rename foo to bar in src/a.ts. Do not touch tests/. Keep the public API."

func keptSession(t *testing.T, messages ...agent.AgentMessage) *codingagent.Session {
	t.Helper()
	s := codingagent.NewSession("kept", t.TempDir())
	for _, message := range messages {
		if _, err := s.AppendMessage(message); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func keptUser(text string) agent.AgentMessage {
	return agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserText(text), Timestamp: 1}}
}

func keptAssistant(text string) agent.AgentMessage {
	return agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: text}}, StopReason: ai.StopReasonStop, Timestamp: 1}}
}

func TestKeptUserMessagesInstructionListsWhichStayWhenNotAllFit(t *testing.T) {
	messages := []agent.AgentMessage{keptUser("task " + strings.Repeat("t", 6000))}
	for i := 1; i <= 5; i++ {
		messages = append(messages, keptUser("m"+string(rune('0'+i))+" "+strings.Repeat("m", 4000)))
	}
	messages = append(messages, keptAssistant("ok"))
	s := keptSession(t, messages...)
	settings := DefaultCompactionSettings
	settings.KeepRecentTokens = 1
	prep := PrepareCompaction(s.GetBranch(), settings)
	if prep == nil {
		t.Fatal("nothing to compact")
	}
	if _, complete := compactiontypes.KeptUserMessagesOf(*prep); complete {
		t.Fatal("keptUserMessagesComplete = true, want false")
	}
	instruction := KeptUserMessagesInstruction(*prep)
	if !regexp.MustCompile(`Not all of them fit: only those that start as listed below stay[^\n]*:\n- "task t+"\n- "m4 m+"\n- "m5 m+"$`).MatchString(instruction) {
		t.Fatalf("instruction:\n%s", instruction)
	}
	if !strings.Contains(instruction, "(up to 16,000 characters;") || !strings.Contains(instruction, `one cut short ends with "[cut here: the rest is in the summary]"`) {
		t.Fatalf("instruction:\n%s", instruction)
	}
	// The session reads the same from its whole branch.
	kept, complete := s.UserMessagesBeforeEntry(prep.FirstKeptEntryID)
	if prepKept, _ := compactiontypes.KeptUserMessagesOf(*prep); complete || !slices.Equal(kept, prepKept) {
		t.Fatalf("session's kept messages %d (complete %v), the preparation's %d", len(kept), complete, len(prepKept))
	}
}

func TestKeptUserMessagesInstructionSaysNothingWhenTheFeatureIsOff(t *testing.T) {
	s := keptSession(t, keptUser(keptTicket), keptAssistant(strings.Repeat("x", 100_000)), keptUser("more"), keptAssistant(strings.Repeat("y", 100_000)))
	restore := codingagent.SetCompactionUserMessages(false)
	off := PrepareCompaction(s.GetBranch(), DefaultCompactionSettings)
	restore()
	if kept, _ := compactiontypes.KeptUserMessagesOf(*off); len(kept) != 0 {
		t.Fatalf("keptUserMessages = %q with the feature off", kept)
	}
	if got := KeptUserMessagesInstruction(*off); got != "" {
		t.Fatalf("instruction with the feature off: %q", got)
	}
	on := PrepareCompaction(s.GetBranch(), DefaultCompactionSettings)
	if kept, _ := compactiontypes.KeptUserMessagesOf(*on); !slices.Equal(kept, []string{keptTicket, "more"}) {
		t.Fatalf("keptUserMessages = %q", kept)
	}
	instruction := KeptUserMessagesInstruction(*on)
	if !strings.Contains(instruction, "Do not quote, copy or restate them") || strings.Contains(instruction, "Not all of them fit") {
		t.Fatalf("instruction:\n%s", instruction)
	}
}

// The summary text stays as the summarizer wrote it: µPi carries the user's messages beside it, not in it (the invented
// "## Original User Task" prefix is gone).
func TestCompactKeepsTheSummaryAsWritten(t *testing.T) {
	prep := CompactionPreparation{
		FirstKeptEntryID:    "keep-1",
		MessagesToSummarize: []agent.AgentMessage{keptUser(keptTicket)},
		Settings:            DefaultCompactionSettings,
		FileOps:             NewFileOps(),
	}
	result, err := Compact(t.Context(), prep, &ai.Model{}, "", nil, "", "", CompleterStreamFn(&fakeCompleter{response: "## Goal\nas in the user's messages"}), nil, nil, ai.RetryCallbacks{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary != "## Goal\nas in the user's messages" {
		t.Fatalf("summary = %q", result.Summary)
	}
}
