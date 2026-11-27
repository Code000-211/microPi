// SPDX-License-Identifier: MIT

package compaction

// Ports packages/coding-agent/src/core/compaction/compaction-ahead.ts (µPi 8f722e90, a72998f3).

import (
	"context"
	"encoding/json"
	"math"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// µPi: near the compaction threshold, the summary of what the compaction would cut is made in the background, and the threshold compaction
// takes it ([CompactionAhead.Take]) instead of stopping the agent for the summary request. Before, the agent stood still for each summary:
// 14, 56, 60 and 95 s in four real sessions.
//
// The threshold is not the context's limit: while the summary is still being made, the agent goes on above it, and stops to wait for it
// only some way above (see compactionAheadOverrun). The entries the summary covers stay as they are: later ones are appended, and kept by
// the compaction.

// How far below the compaction threshold the summary starts in the background: a share of the threshold, up to a number of tokens. Started
// closer, the agent works on above the threshold for longer (a summary takes 15 to 90 seconds); started earlier, more of the context stays
// after the compaction, and a session that ends below the threshold paid for a summary it never used.
const (
	compactionAheadShare     = 0.15
	compactionAheadMaxTokens = 40_000
	// compactionAheadOverrun is how far above the threshold the agent goes on while the summary made ahead is not ready yet, as a share of
	// the threshold: at most halfway to the context's limit (the window less reserveTokens).
	compactionAheadOverrun = 0.5
)

// pendingSummary is a compaction summary made in the background before the threshold was reached.
type pendingSummary struct {
	session *codingagent.Session
	// leafID is the branch's last entry when it started: the summary covers the entries before the cut point up to it.
	leafID           string
	firstKeptEntryID string
	keepRecentTokens int
	ctx              context.Context
	cancel           context.CancelFunc
	// done is closed once the summary is made, failed or cancelled; result is then nil when it failed or was cancelled.
	done   chan struct{}
	result *CompactionResult
}

func (p *pendingSummary) isDone() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

// CompactionAhead holds the summary being made or made, until a compaction takes it or it no longer fits (compaction-ahead.ts
// CompactionAhead). The zero value is ready to use.
type CompactionAhead struct {
	mu      sync.Mutex
	pending *pendingSummary
}

// AheadUpdate is what [CompactionAhead.Update] is told after a response.
type AheadUpdate struct {
	// ContextTokens is the size of the session's current branch, as the next request sends it.
	ContextTokens int
	ContextWindow int
	// Session is the session manager whose branch Branch is.
	Session *codingagent.Session
	// Branch reads the session's current branch, once the context is near the threshold.
	Branch   func() []codingagent.SessionEntry
	Settings CompactionSettings
	// Summarize is Pi's own summary request for prep. It is called on the caller's goroutine, and the function it returns runs in the
	// background, until its context ends.
	Summarize func(prep CompactionPreparation) func(ctx context.Context) (CompactionResult, error)
}

// Update starts the summary when the context is near the threshold, or keeps the one being made while it still fits. Not for a context so
// small that the kept part alone would be near the threshold again.
//
// It returns whether the compaction is to wait: a summary is being made, and the context is below where it stops.
func (c *CompactionAhead) Update(update AheadUpdate) bool {
	settings := update.Settings
	threshold := GetCompactionThreshold(update.ContextWindow, settings)
	start := float64(threshold) - math.Min(float64(threshold)*compactionAheadShare, compactionAheadMaxTokens)
	if float64(update.ContextTokens) <= start || start < float64(2*settings.KeepRecentTokens) {
		return false
	}
	limit := update.ContextWindow - settings.ReserveTokens
	goesOn := float64(update.ContextTokens) <= float64(threshold)+math.Min(float64(limit-threshold)/2, float64(threshold)*compactionAheadOverrun)
	branch := update.Branch()
	c.mu.Lock()
	defer c.mu.Unlock()
	if current := c.pending; current != nil {
		if current.fits(update.Session, branch, settings) {
			return goesOn && !current.isDone()
		}
		current.cancel()
		c.pending = nil
	}
	if len(branch) == 0 {
		return false
	}
	leafID := branch[len(branch)-1].Base().ID
	prep := PrepareCompaction(branch, settings)
	if leafID == "" || prep == nil || (len(prep.MessagesToSummarize) == 0 && len(prep.TurnPrefixMessages) == 0) {
		return false
	}
	job := update.Summarize(*prep)
	ctx, cancel := context.WithCancel(context.Background())
	pending := &pendingSummary{
		session:          update.Session,
		leafID:           leafID,
		firstKeptEntryID: prep.FirstKeptEntryID,
		keepRecentTokens: settings.KeepRecentTokens,
		ctx:              ctx,
		cancel:           cancel,
		done:             make(chan struct{}),
	}
	go func() {
		defer close(pending.done)
		if result, err := job(ctx); err == nil {
			pending.result = &result
		}
	}()
	c.pending = pending
	return goesOn
}

// Take returns the summary made ahead for a threshold compaction of branch, waited for when it is still being made (the wait ends with
// ctx, which cancels it); nil when there is none, it does not fit, or it failed. Taken once.
func (c *CompactionAhead) Take(ctx context.Context, session *codingagent.Session, branch []codingagent.SessionEntry, settings CompactionSettings) *CompactionResult {
	c.mu.Lock()
	pending := c.pending
	c.pending = nil
	c.mu.Unlock()
	if pending == nil {
		return nil
	}
	if !pending.fits(session, branch, settings) {
		pending.cancel()
		return nil
	}
	select {
	case <-pending.done:
	case <-ctx.Done():
		pending.cancel()
		<-pending.done
	}
	return pending.result
}

// Cancel stops the summary being made.
func (c *CompactionAhead) Cancel() {
	c.mu.Lock()
	pending := c.pending
	c.mu.Unlock()
	if pending != nil {
		pending.cancel()
	}
}

// Wait waits until the summary being made, if any, is made, failed or cancelled; ctx ends the wait.
func (c *CompactionAhead) Wait(ctx context.Context) {
	c.mu.Lock()
	pending := c.pending
	c.mu.Unlock()
	if pending == nil {
		return
	}
	select {
	case <-pending.done:
	case <-ctx.Done():
	}
}

// Pending reports whether a summary is being made or was made and not yet taken.
func (c *CompactionAhead) Pending() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pending != nil
}

// fits reports whether a summary made ahead still fits branch: nothing it covers was compacted, edited or left since.
func (p *pendingSummary) fits(session *codingagent.Session, branch []codingagent.SessionEntry, settings CompactionSettings) bool {
	if p.session != session || p.keepRecentTokens != settings.KeepRecentTokens || p.ctx.Err() != nil {
		return false
	}
	index := func(id string) int {
		return slices.IndexFunc(branch, func(entry codingagent.SessionEntry) bool { return entry.Base().ID == id })
	}
	leafIndex, keptIndex := index(p.leafID), index(p.firstKeptEntryID)
	if leafIndex < 0 || keptIndex < 0 {
		return false
	}
	for _, entry := range branch[leafIndex+1:] {
		switch entry.Base().Type {
		case "compaction":
			return false
		case "context_edit":
			var edit codingagent.ContextEditEntry
			if json.Unmarshal(entry.Raw(), &edit) != nil || index(edit.TargetID) < keptIndex {
				return false
			}
		}
	}
	return true
}
