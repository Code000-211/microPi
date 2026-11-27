// SPDX-License-Identifier: MIT

package coding

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/compaction"
)

// µPi (8f722e90, a72998f3): ports packages/coding-agent/test/suite/agent-session-compaction-ahead.test.ts (a18b202f).
//
// Window 100k, threshold 99k: the summary starts ahead above 84,150 tokens, and the threshold is the limit. With maxContextTokens 50000
// (the test's thresholdPercent 50, which PiG has no setting for), the threshold is 50k, the summary starts above 42,500 and the agent
// goes on to 74,500.

// withoutCompactionAhead turns the summary made ahead off for a test of Pi's own compaction thresholds and requests (compactionFeatures.ahead
// = false), whose scripted responses it would take.
func withoutCompactionAhead(t *testing.T) { t.Cleanup(compaction.SetAhead(false)) }

// withoutCachedPrefix turns the summary on the cached prefix off for a test of Pi's own summary request (compactionFeatures.cachedPrefix =
// false).
func withoutCachedPrefix(t *testing.T) { t.Cleanup(compaction.SetCachedPrefix(false)) }

type aheadHarness struct {
	h *recoveryHarness
	s *Session
	t *testing.T

	mu        sync.Mutex
	summaries []string
	gate      chan struct{}
}

func newAheadHarness(t *testing.T, compactionSettings string) *aheadHarness {
	t.Helper()
	settings := `{"compaction":{"enabled":true,"reserveTokens":1000,"keepRecentTokens":1` + compactionSettings + `}}`
	h := newRecoveryHarness(t, harnessOptions{settings: settings, contextWindow: 100_000})
	a := &aheadHarness{h: h, s: h.session, t: t}
	open := make(chan struct{})
	close(open)
	a.gate = open
	h.session.Agent().SetStreamFunction(func(ctx context.Context, model *ai.Model, _ ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		a.mu.Lock()
		summary := fmt.Sprintf("summary %d", len(a.summaries)+1)
		a.summaries = append(a.summaries, summary)
		gate := a.gate
		a.mu.Unlock()
		stream := ai.NewAssistantMessageEventStream()
		go func() {
			select {
			case <-gate:
			case <-ctx.Done():
			}
			message := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: summary}}, Provider: model.ProviderID(), Model: model.ID, StopReason: ai.StopReasonStop, Usage: ai.Usage{Input: 10, TotalTokens: 10}, Timestamp: time.Now().UnixMilli()}
			_ = stream.Push(ai.DoneEvent{Reason: ai.StopReasonStop, Message: message})
		}()
		return stream, nil
	})
	return a
}

func (a *aheadHarness) hold() {
	a.mu.Lock()
	a.gate = make(chan struct{})
	a.mu.Unlock()
}

func (a *aheadHarness) release() {
	a.mu.Lock()
	close(a.gate)
	a.mu.Unlock()
}

func (a *aheadHarness) madeSummaries() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.summaries)
}

// agentAssistant is the agent's own copy of the assistant message of timestamp, which the compaction check finds by identity.
func (a *aheadHarness) agentAssistant(timestamp int64) *agent.AssistantMessage {
	a.s.RefreshContext()
	for _, message := range a.s.agent.Messages() {
		if message.Assistant != nil && message.Assistant.Timestamp == timestamp {
			return message.Assistant
		}
	}
	a.t.Fatalf("no assistant message at %d", timestamp)
	return nil
}

var aheadClock = time.Now().UnixMilli()

// nextAheadTime is a timestamp after every earlier one and the clock: a message older than the latest compaction carries stale usage.
func nextAheadTime() int64 {
	aheadClock = max(aheadClock+10, time.Now().UnixMilli())
	return aheadClock
}

// turn appends a user message and its answer, of totalTokens.
func (a *aheadHarness) turn(text string, totalTokens int) *agent.AssistantMessage {
	now := nextAheadTime()
	persistQueueMessage(a.t, a.s, queueUser(text, now))
	answer := text
	if len(answer) > 10 {
		answer = answer[:10]
	}
	persistQueueMessage(a.t, a.s, agent.AgentMessage{Assistant: queueAssistant(a.s, "answer to "+answer, totalTokens, 0, ai.StopReasonStop, now+1)})
	return a.agentAssistant(now + 1)
}

// step is one step of a single long turn: a read and its result.
func (a *aheadHarness) step(n, totalTokens int) *agent.AssistantMessage {
	now := nextAheadTime()
	id := fmt.Sprintf("read-%d", n)
	assistant := queueAssistant(a.s, "", totalTokens, 0, ai.StopReasonToolUse, now)
	assistant.Content = []ai.AssistantContentBlock{ai.ToolCall{ID: id, Name: "read", Arguments: map[string]any{"path": fmt.Sprintf("f%d", n)}}}
	persistQueueMessage(a.t, a.s, agent.AgentMessage{Assistant: assistant})
	persistQueueMessage(a.t, a.s, agent.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: "toolResult", ToolCallID: id, ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: strings.Repeat("y", 20_000)}}, Timestamp: now + 1}})
	return a.agentAssistant(now)
}

func (a *aheadHarness) check(message *agent.AssistantMessage) bool {
	a.t.Helper()
	goOn, err := a.s.checkCompaction(a.t.Context(), message, true)
	if err != nil {
		a.t.Fatal(err)
	}
	return goOn
}

func (a *aheadHarness) wait() { a.s.compactionAhead.Wait(a.t.Context()) }

func (a *aheadHarness) compactions() []icodingagent.CompactionEntry {
	var out []icodingagent.CompactionEntry
	for _, entry := range a.s.inner.Load().GetBranch() {
		if entry.Base().Type != "compaction" {
			continue
		}
		var value icodingagent.CompactionEntry
		if err := json.Unmarshal(entry.Raw(), &value); err != nil {
			a.t.Fatal(err)
		}
		out = append(out, value)
	}
	return out
}

var aheadLong = strings.Repeat("x", 40_000)

func TestCompactionAheadMakesTheSummaryNearTheThresholdAndCompactsWithIt(t *testing.T) {
	a := newAheadHarness(t, "")
	a.check(a.turn("one "+aheadLong, 50_000))
	if a.s.compactionAhead.Pending() {
		t.Fatal("a summary started at 50k")
	}
	a.check(a.turn("two "+aheadLong, 90_000))
	if !a.s.compactionAhead.Pending() {
		t.Fatal("no summary started at 90k")
	}
	a.wait()
	if got := a.madeSummaries(); !slices.Equal(got, []string{"summary 1"}) {
		t.Fatalf("summaries %v", got)
	}
	branch := a.s.inner.Load().GetBranch()
	keptFrom := branch[len(branch)-1].Base().ID

	// Work goes on after the summary started; it stays after the compaction.
	a.check(a.turn("three", 99_500))
	if got := a.madeSummaries(); !slices.Equal(got, []string{"summary 1"}) {
		t.Fatalf("summaries %v, want no second request", got)
	}
	entries := a.compactions()
	if len(entries) != 1 || !strings.Contains(entries[0].Summary, "summary 1") {
		t.Fatalf("compactions %+v", entries)
	}
	branch = a.s.inner.Load().GetBranch()
	index := func(id string) int {
		return slices.IndexFunc(branch, func(entry icodingagent.SessionEntry) bool { return entry.Base().ID == id })
	}
	if kept := index(entries[0].FirstKeptEntryID); kept <= 0 || kept > index(keptFrom) {
		t.Fatalf("first kept entry at %d, want in (0, %d]", kept, index(keptFrom))
	}
	if a.s.compactionAhead.Pending() {
		t.Fatal("the summary was not taken")
	}
}

func TestCompactionAheadWaitsForASummaryStillBeingMade(t *testing.T) {
	a := newAheadHarness(t, "")
	a.check(a.turn("one "+aheadLong, 50_000))
	a.hold()
	a.check(a.turn("two "+aheadLong, 90_000))
	three := a.turn("three", 99_500)
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.check(three)
	}()
	time.Sleep(20 * time.Millisecond)
	if n := len(a.compactions()); n != 0 {
		t.Fatalf("%d compactions before the summary is made", n)
	}
	a.release()
	<-done
	if got := a.madeSummaries(); !slices.Equal(got, []string{"summary 1"}) {
		t.Fatalf("summaries %v", got)
	}
	if entries := a.compactions(); len(entries) != 1 || !strings.Contains(entries[0].Summary, "summary 1") {
		t.Fatalf("compactions %+v", entries)
	}
}

func TestCompactionAheadGoesOnAboveTheThresholdWhileTheSummaryIsMade(t *testing.T) {
	a := newAheadHarness(t, `,"maxContextTokens":50000`)
	a.check(a.turn("one "+aheadLong, 30_000))
	a.hold()
	a.check(a.turn("two "+aheadLong, 45_000))
	if !a.s.compactionAhead.Pending() {
		t.Fatal("no summary started at 45k")
	}
	if a.check(a.turn("three", 55_000)) {
		t.Fatal("the check asked to continue")
	}
	if n := len(a.compactions()); n != 0 {
		t.Fatalf("%d compactions above the threshold while the summary is made", n)
	}
	a.release()
	a.wait()
	a.check(a.turn("four", 60_000))
	if got := a.madeSummaries(); !slices.Equal(got, []string{"summary 1"}) {
		t.Fatalf("summaries %v", got)
	}
	if entries := a.compactions(); len(entries) != 1 || !strings.Contains(entries[0].Summary, "summary 1") {
		t.Fatalf("compactions %+v", entries)
	}
}

func TestCompactionAheadWaitsWhenTheContextGetsTooFarAboveTheThreshold(t *testing.T) {
	a := newAheadHarness(t, `,"maxContextTokens":50000`)
	a.check(a.turn("one "+aheadLong, 30_000))
	a.hold()
	a.check(a.turn("two "+aheadLong, 45_000))
	three := a.turn("three", 80_000)
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.check(three)
	}()
	time.Sleep(20 * time.Millisecond)
	if n := len(a.compactions()); n != 0 {
		t.Fatalf("%d compactions before the summary is made", n)
	}
	a.release()
	<-done
	if got := a.madeSummaries(); !slices.Equal(got, []string{"summary 1"}) {
		t.Fatalf("summaries %v", got)
	}
	if entries := a.compactions(); len(entries) != 1 || !strings.Contains(entries[0].Summary, "summary 1") {
		t.Fatalf("compactions %+v", entries)
	}
}

func TestCompactionAheadMakesTheSummaryWithinOneLongTurn(t *testing.T) {
	a := newAheadHarness(t, `,"maxContextTokens":50000`)
	persistQueueMessage(t, a.s, queueUser("read them all", nextAheadTime()))
	for n := 1; n <= 8; n++ {
		a.check(a.step(n, n*5_500))
	}
	if !a.s.compactionAhead.Pending() {
		t.Fatal("no summary started within the turn")
	}
	a.wait()
	a.check(a.step(9, 60_000))
	if got := a.madeSummaries(); !slices.Equal(got, []string{"summary 1"}) {
		t.Fatalf("summaries %v", got)
	}
	if entries := a.compactions(); len(entries) != 1 || !strings.Contains(entries[0].Summary, "summary 1") {
		t.Fatalf("compactions %+v", entries)
	}
}

func TestCompactionAheadInARunTheAgentWorksOnWhileTheSummaryIsMade(t *testing.T) {
	var mu sync.Mutex
	var requests []string
	reads := 0
	release := make(chan struct{})
	// summaryAsked closes when the summary request arrives: it is sent from its own goroutine, so turn 11 waits for it before releasing it.
	summaryAsked := make(chan struct{})
	var askedOnce sync.Once
	// session is set once the harness exists; the last turn waits on its summary (see below).
	var session *Session
	respond := func(messages []ai.Message) *ai.AssistantMessage {
		last, _ := json.Marshal(messages[len(messages)-1])
		if strings.Contains(string(last), "being compacted") || strings.Contains(string(last), "summar") {
			mu.Lock()
			requests = append(requests, "summary")
			mu.Unlock()
			askedOnce.Do(func() { close(summaryAsked) })
			<-release
			return &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "the summary"}}, Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonStop, Timestamp: time.Now().UnixMilli()}
		}
		mu.Lock()
		reads++
		n := reads
		requests = append(requests, fmt.Sprintf("turn %d", n))
		mu.Unlock()
		// The summary is ready from the 11th turn on.
		if n == 11 {
			select {
			case <-summaryAsked:
			case <-time.After(5 * time.Second):
			}
			close(release)
		}
		// The summary request answers once turn 11 has released it, but finishes on its own goroutine: the last turn waits for it, so the
		// threshold check at the end of the run finds it made (as it would in a real run, where the next response takes seconds).
		if n == 12 && session != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			session.compactionAhead.Wait(ctx)
			cancel()
		}
		message := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "done"}}, Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonStop, Timestamp: time.Now().UnixMilli()}
		if n <= 11 {
			message.Content = []ai.AssistantContentBlock{ai.ToolCall{ID: fmt.Sprintf("call-%d", n), Name: "read", Arguments: map[string]any{"path": fmt.Sprintf("f%d.txt", n)}}}
			message.StopReason = ai.StopReasonToolUse
		}
		message.Usage = ai.Usage{Input: n * 5_500, TotalTokens: n * 5_500}
		return message
	}
	responses := make([]scriptedResponse, 20)
	for i := range responses {
		responses[i] = respond
	}
	h := newRecoveryHarness(t, harnessOptions{settings: `{"compaction":{"enabled":true,"reserveTokens":1000,"keepRecentTokens":1,"maxContextTokens":50000}}`, contextWindow: 100_000, defaultTools: true}, responses...)
	session = h.session
	for n := 1; n <= 12; n++ {
		if err := os.WriteFile(filepath.Join(h.session.CWD(), fmt.Sprintf("f%d.txt", n)), []byte(strings.Repeat("y", 20_000)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	promptHarness(t, h, "read f1.txt to f11.txt, one at a time")

	mu.Lock()
	got := slices.Clone(requests)
	mu.Unlock()
	summary := slices.Index(got, "summary")
	if summary <= 0 {
		t.Fatalf("requests %v: no summary after the first turn", got)
	}
	// Turn 11 lets the summary finish: had the agent stopped for it above the threshold of 50k (from turn 10 on), turn 11 would never have
	// come.
	if n := len(slices.DeleteFunc(slices.Clone(got), func(r string) bool { return r != "summary" })); n != 1 {
		t.Fatalf("requests %v: %d summaries", got, n)
	}
	// The summary is held until turn 11 releases it, so turn 11 coming at all shows the agent worked on while it was made; which of the two
	// requests arrives first is the scheduler's choice. It was asked for before the last turn.
	if turn12 := slices.Index(got, "turn 12"); turn12 < 0 || summary > turn12 || !slices.Contains(got, "turn 11") {
		t.Fatalf("requests %v: the summary was not made while the agent worked on", got)
	}
	entries := suiteCompactions(t, h.session)
	if len(entries) != 1 || !strings.Contains(entries[0].Summary, "the summary") {
		t.Fatalf("compactions %+v", entries)
	}
	if text := suiteLastText(h.session); text != "done" {
		t.Fatalf("last text %q", text)
	}
}

func TestCompactionAheadDropsTheSummaryWhenTheSessionWasCompactedSince(t *testing.T) {
	a := newAheadHarness(t, "")
	a.check(a.turn("one "+aheadLong, 50_000))
	a.check(a.turn("two "+aheadLong, 90_000))
	a.wait()
	if _, err := a.s.Compact(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	if got := a.madeSummaries(); !slices.Equal(got, []string{"summary 1", "summary 2"}) {
		t.Fatalf("summaries %v", got)
	}
	a.check(a.turn("three "+aheadLong, 99_500))
	if got := a.madeSummaries(); !slices.Equal(got, []string{"summary 1", "summary 2", "summary 3"}) {
		t.Fatalf("summaries %v", got)
	}
	if entries := a.compactions(); len(entries) != 2 || !strings.Contains(entries[1].Summary, "summary 3") {
		t.Fatalf("compactions %+v", entries)
	}
}

func TestCompactionAheadIsNotMadeWhenTurnedOff(t *testing.T) {
	t.Cleanup(compaction.SetAhead(false))
	a := newAheadHarness(t, "")
	a.check(a.turn("one "+aheadLong, 50_000))
	a.check(a.turn("two "+aheadLong, 90_000))
	if a.s.compactionAhead.Pending() || len(a.madeSummaries()) != 0 {
		t.Fatalf("pending %v, summaries %v", a.s.compactionAhead.Pending(), a.madeSummaries())
	}
}
