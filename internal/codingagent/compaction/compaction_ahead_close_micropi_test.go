package compaction

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// µPi: after Close no summary starts, so a run still in flight when the session closes makes no paid request after it.
func TestCompactionAheadStartsNothingAfterClose(t *testing.T) {
	session, _ := aheadSession(t, 4)
	settings := CompactionSettings{Enabled: true, ReserveTokens: 1000, KeepRecentTokens: 1}
	started := 0
	summarize := func(CompactionPreparation) func(context.Context) (CompactionResult, error) {
		started++
		return func(context.Context) (CompactionResult, error) { return CompactionResult{Summary: "ahead"}, nil }
	}
	var ahead CompactionAhead
	ahead.Close()
	if ahead.Update(AheadUpdate{ContextTokens: 90_000, ContextWindow: 100_000, Session: session, Branch: func() []codingagent.SessionEntry { return session.GetBranch() }, Settings: settings, Summarize: summarize}) || started != 0 {
		t.Fatalf("a closed CompactionAhead started %d summaries", started)
	}
}
