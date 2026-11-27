package compaction

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// µPi (ade455a6, 8f722e90): ports tool-result-pruning.test.ts.

var pruningTestSettings = ToolResultPruningSettings{Enabled: true, TriggerTokens: 1000, KeepRecent: 2, MinChars: 100}

func pruningCall(id int) ai.Message {
	return ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.ToolCall{ID: "call-" + string(rune('0'+id)), Name: "read", Arguments: map[string]any{"path": "f"}}}, StopReason: ai.StopReasonToolUse}
}

func pruningResult(id, chars int) ai.Message {
	return ai.ToolResultMessage{ToolCallID: "call-" + string(rune('0'+id)), ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: strings.Repeat("x", chars)}}}
}

// pruningConversation is a conversation of n tool calls; result small is short, the others 1000 characters.
func pruningConversation(n, small int) []ai.Message {
	messages := []ai.Message{ai.UserMessage{Content: ai.UserText("go")}}
	for i := range n {
		chars := 1000
		if i == small {
			chars = 50
		}
		messages = append(messages, pruningCall(i), pruningResult(i, chars))
	}
	return messages
}

func prunedText(message ai.Message) string {
	result, ok := message.(ai.ToolResultMessage)
	if !ok || len(result.Content) == 0 {
		return ""
	}
	text, _ := result.Content[0].(ai.TextContent)
	return text.Text
}

func countCleared(messages []ai.Message) int {
	cleared := 0
	for _, message := range messages {
		if strings.HasPrefix(prunedText(message), "[Old") {
			cleared++
		}
	}
	return cleared
}

func TestPruneToolResultsLeavesTheContextAlone(t *testing.T) {
	few := pruningConversation(3, -1)
	if got := PruneToolResults(few, pruningTestSettings); &got[0] != &few[0] {
		t.Fatal("pruned with fewer than 2*keepRecent results")
	}
	small := pruningConversation(8, -1)
	high := pruningTestSettings
	high.TriggerTokens = 100_000
	if got := PruneToolResults(small, high); &got[0] != &small[0] {
		t.Fatal("pruned below the trigger")
	}
	off := pruningTestSettings
	off.Enabled = false
	if got := PruneToolResults(small, off); &got[0] != &small[0] {
		t.Fatal("pruned while disabled")
	}
}

func TestPruneToolResultsClearsOldLargeResults(t *testing.T) {
	messages := pruningConversation(6, 1)
	pruned := PruneToolResults(messages, pruningTestSettings)
	var results []string
	for _, message := range pruned {
		if _, ok := message.(ai.ToolResultMessage); ok {
			results = append(results, prunedText(message))
		}
	}
	// 6 results, keepRecent 2: the first 4 are old; result 1 is small.
	if results[0] != "[Old read result cleared from the context to save space (1000 characters). Run the tool again if you need it.]" {
		t.Fatalf("result 0 = %q", results[0])
	}
	if len(results[1]) != 50 || !strings.HasPrefix(results[2], "[Old read") || !strings.HasPrefix(results[3], "[Old read") || len(results[4]) != 1000 || len(results[5]) != 1000 {
		t.Fatalf("results = %q", results)
	}
	if id := pruned[2].(ai.ToolResultMessage).ToolCallID; id != "call-0" {
		t.Fatalf("toolCallId = %q", id)
	}
	if len(prunedText(messages[2])) != 1000 {
		t.Fatal("the session's messages changed")
	}
}

func TestPruneToolResultsMovesTheBoundaryInSteps(t *testing.T) {
	messages := pruningConversation(9, -1)
	first := PruneToolResults(messages[:1+2*6], pruningTestSettings)
	second := PruneToolResults(messages[:1+2*7], pruningTestSettings)
	third := PruneToolResults(messages, pruningTestSettings)
	// 6 and 7 results clear 4; 9 clear 6.
	if a, b, c := countCleared(first), countCleared(second), countCleared(third); a != 4 || b != 4 || c != 6 {
		t.Fatalf("cleared %d, %d, %d; want 4, 4, 6", a, b, c)
	}
	for i := range first {
		if prunedText(second[i]) != prunedText(first[i]) {
			t.Fatalf("message %d changed between steps", i)
		}
	}
	// The boundary is floor((n-k)/k)*k, active from 2k results.
	for n, want := range map[int]int{3: 0, 4: 2, 5: 2, 6: 4, 7: 4, 8: 6, 9: 6} {
		if got := countCleared(PruneToolResults(pruningConversation(n, -1), pruningTestSettings)); got != want {
			t.Fatalf("%d results clear %d, want %d", n, got, want)
		}
	}
}

func TestPruneToolResultsClearsImagesWhateverTheirSize(t *testing.T) {
	messages := pruningConversation(4, -1)
	messages[2] = ai.ToolResultMessage{ToolCallID: "call-0", ToolName: "read", Content: []ai.ToolResultMessageContent{ai.ImageContent{Data: "AAAA", MimeType: "image/png"}}}
	settings := pruningTestSettings
	settings.TriggerTokens = 0
	if got := prunedText(PruneToolResults(messages, settings)[2]); !strings.Contains(got, "(1 image(s))") {
		t.Fatalf("image result = %q", got)
	}
}

// Characters are counted in UTF-16 units, as String.length does.
func TestPruneToolResultsCountsUTF16Units(t *testing.T) {
	messages := pruningConversation(4, -1)
	messages[2] = ai.ToolResultMessage{ToolCallID: "call-0", ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: strings.Repeat("é", 1000)}}}
	if got := prunedText(PruneToolResults(messages, pruningTestSettings)[2]); !strings.Contains(got, "(1000 characters)") {
		t.Fatalf("result = %q", got)
	}
}

// The tokens of the next request: pruning that clears more of it than of the request the usage measured lowers them.
func TestTokensAfterPruning(t *testing.T) {
	settings := ToolResultPruningSettings{Enabled: true, TriggerTokens: 10_000, KeepRecent: 4, MinChars: 2000}
	messages := []agent.AgentMessage{{User: &agent.UserMessage{Role: "user", Content: ai.UserText("read them all")}}}
	for n := range 8 {
		id := "read-" + string(rune('0'+n))
		messages = append(messages,
			agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.ToolCall{ID: id, Name: "read", Arguments: map[string]any{"path": "f"}}}, StopReason: ai.StopReasonToolUse}},
			agent.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: "toolResult", ToolCallID: id, ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: strings.Repeat("y", 20_000)}}}},
		)
	}
	note := ceilQuarter(len("[Old read result cleared from the context to save space (20000 characters). Run the tool again if you need it.]"))
	// The request before the last response held 7 results, none cleared; the next one holds 8 and clears 4.
	if got, want := TokensAfterPruning(52_000, messages, len(messages)-2, settings), 52_000-4*(5_000-note); got != want {
		t.Fatalf("tokens after pruning = %d, want %d", got, want)
	}
	// Without usage the tokens are the estimate of all the messages.
	if got, want := TokensAfterPruning(60_000, messages, -1, settings), 60_000-4*(5_000-note); got != want {
		t.Fatalf("tokens after pruning = %d, want %d", got, want)
	}
	off := settings
	off.Enabled = false
	if got := TokensAfterPruning(52_000, messages, len(messages)-2, off); got != 52_000 {
		t.Fatalf("disabled: %d", got)
	}
}

// µPi: the results of a batch of parallel calls the model is waiting for are not cleared, however many there are: 7 earlier results and one
// assistant message with 10 calls, keepRecent 8, put the boundary at 8, inside the batch.
func TestPruneToolResultsKeepsTheResultsTheModelHasNotSeen(t *testing.T) {
	settings := ToolResultPruningSettings{Enabled: true, TriggerTokens: 1000, KeepRecent: 8, MinChars: 100}
	messages := []ai.Message{ai.UserMessage{Content: ai.UserText("go")}}
	for i := range 7 {
		messages = append(messages, pruningCall(i), pruningResult(i, 1000))
	}
	var calls []ai.AssistantContentBlock
	for i := range 10 {
		calls = append(calls, ai.ToolCall{ID: "batch-" + string(rune('a'+i)), Name: "read", Arguments: map[string]any{"path": "f"}})
	}
	messages = append(messages, ai.AssistantMessage{Content: calls, StopReason: ai.StopReasonToolUse})
	for i := range 10 {
		messages = append(messages, ai.ToolResultMessage{ToolCallID: "batch-" + string(rune('a'+i)), ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: strings.Repeat("y", 1000)}}})
	}
	pruned := PruneToolResults(messages, settings)
	for i, message := range pruned[len(pruned)-10:] {
		if strings.HasPrefix(prunedText(message), "[Old") {
			t.Fatalf("batch result %d was cleared before the model saw it", i)
		}
	}
	if got := countCleared(pruned); got != 7 {
		t.Fatalf("cleared %d results, want the 7 the model has seen", got)
	}
}
