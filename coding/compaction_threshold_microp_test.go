// SPDX-License-Identifier: MIT

package coding

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// thresholdSession is a session whose agent directory holds settings, on a model of window tokens.
func thresholdSession(t *testing.T, settings string, window int) *Session {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("PIG_HOME", tmp)
	agentDir := filepath.Join(tmp, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	model := fakeModel()
	model.Capabilities.ContextWindow = window
	model.Capabilities.MaxOutputTokens = 8192
	s, err := NewSession(services, SessionOptions{Model: model, NoSession: true, SkipBuiltinTools: true, SystemPrompt: "Test"})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Events()
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}

func thresholdDecision(t *testing.T, s *Session, message *agent.AssistantMessage) []compactionDecisionCall {
	t.Helper()
	var calls []compactionDecisionCall
	if _, err := s.checkCompactionDecision(t.Context(), message, false, func(_ context.Context, reason string, retry bool) (bool, error) {
		calls = append(calls, compactionDecisionCall{reason, retry})
		return false, nil
	}); err != nil {
		t.Fatal(err)
	}
	return calls
}

// µPi (8f722e90): ports test/suite/compaction-after-pruning.test.ts. Threshold 50k. Pruning keeps the latest 4 tool results and clears
// in steps of 4 once there are 8: the request after the eighth result clears four, about 20k tokens, which the size of the request
// before it (52k) does not show. (PiG has no thresholdPercent: reserveTokens sets the 50k threshold of the 100k window.)
func TestCompactionThresholdIsCheckedAfterToolResultPruning(t *testing.T) {
	withoutCompactionAhead(t) // as compaction-after-pruning.test.ts does
	for _, pruning := range []bool{true, false} {
		settings := `{"compaction":{"enabled":true,"reserveTokens":50000},"toolResultPruning":{"enabled":` + map[bool]string{true: "true", false: "false"}[pruning] + `,"triggerTokens":10000,"keepRecent":4,"minChars":2000}}`
		s := thresholdSession(t, settings, 100_000)
		now := time.Now().UnixMilli()
		persistQueueMessage(t, s, queueUser("read them all", now))
		var last *agent.AssistantMessage
		for n := 1; n <= 8; n++ {
			total := n * 5_000
			if n == 8 {
				total = 52_000
			}
			id := "read-" + string(rune('0'+n))
			last = queueAssistant(s, "", total, 0, ai.StopReasonToolUse, now+int64(2*n))
			last.Content = []ai.AssistantContentBlock{ai.ToolCall{ID: id, Name: "read", Arguments: map[string]any{"path": "f"}}}
			persistQueueMessage(t, s, agent.AgentMessage{Assistant: last})
			persistQueueMessage(t, s, agent.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: "toolResult", ToolCallID: id, ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: strings.Repeat("y", 20_000)}}, Timestamp: now + int64(2*n+1)}})
		}
		s.RefreshContext()
		for _, message := range s.agent.Messages() {
			if message.Assistant != nil && message.Assistant.Timestamp == last.Timestamp {
				last = message.Assistant
			}
		}
		calls := thresholdDecision(t, s, last)
		if pruning && len(calls) != 0 {
			t.Fatalf("compacted although the next request's pruning takes the context below the threshold: %v", calls)
		}
		if !pruning && !slices.Equal(calls, []compactionDecisionCall{{"threshold", false}}) {
			t.Fatalf("without pruning: %v, want one threshold compaction", calls)
		}
	}
}

// µPi (710e4993): maxContextTokens (default 128000) caps the compaction threshold, not the window: a 150k context of a 200k window
// compacts at the threshold and is no overflow; an explicit reserveTokens gives the default way.
func TestMaxContextTokensCapsTheThresholdNotTheWindow(t *testing.T) {
	withoutCompactionAhead(t) // as compaction-window-scaling.test.ts does
	cases := []struct {
		name, settings string
		tokens         int
		want           []compactionDecisionCall
	}{
		{"default cap", `{}`, 150_000, []compactionDecisionCall{{"threshold", false}}},
		{"below the default cap", `{}`, 127_000, nil},
		{"explicit reserveTokens drops the default", `{"compaction":{"reserveTokens":16384}}`, 150_000, nil},
		{"explicit cap beside reserveTokens", `{"compaction":{"reserveTokens":16384,"maxContextTokens":100000}}`, 150_000, []compactionDecisionCall{{"threshold", false}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := thresholdSession(t, tc.settings, 200_000)
			now := time.Now().UnixMilli()
			persistQueueMessage(t, s, queueUser("go", now))
			message := queueAssistant(s, "done", tc.tokens, 0, ai.StopReasonStop, now+1)
			persistQueueMessage(t, s, agent.AgentMessage{Assistant: message})
			s.RefreshContext()
			for _, m := range s.agent.Messages() {
				if m.Assistant != nil && m.Assistant.Timestamp == message.Timestamp {
					message = m.Assistant
				}
			}
			if got := thresholdDecision(t, s, message); !slices.Equal(got, tc.want) {
				t.Fatalf("decisions = %v, want %v", got, tc.want)
			}
		})
	}
}
