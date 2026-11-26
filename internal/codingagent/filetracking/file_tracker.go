package filetracking

import (
	"fmt"
	"strings"
	"sync"
)

// BackgroundChecksHook is notified when files are read or modified.
type BackgroundChecksHook interface {
	OnRead(path string)
	OnEdited(path string)
}

// FileModifiedParams describes a completed file modification for Tracker.OnFileModified.
type FileModifiedParams struct {
	AbsolutePath           string
	NewContent             string
	DisplayContent         string
	NewMtimeMs             float64
	FirstChangedLine       int
	IncludePostEditSnippet bool
}

// TrackerOption configures a Tracker.
type TrackerOption func(*Tracker)

// WithLocal configures whether the tracked files reside on the local machine.
// Remote trackers (local=false) skip local search cache updates and nested AGENTS.md discovery.
func WithLocal(local bool) TrackerOption {
	return func(t *Tracker) {
		t.local = local
	}
}

// WithSearchTouchHook sets an optional callback invoked on local complete reads and writes.
func WithSearchTouchHook(fn func(path string, content []byte, mtimeMs float64)) TrackerOption {
	return func(t *Tracker) {
		t.onSearchTouch = fn
	}
}

// Tracker coordinates FileStateTracker, NestedInstructions, and optional BackgroundChecksHook for a session.
type Tracker struct {
	state         *FileStateTracker
	nested        *NestedInstructions
	local         bool
	onSearchTouch func(path string, content []byte, mtimeMs float64)

	mu     sync.RWMutex
	checks BackgroundChecksHook
}

// NewTracker creates a new Tracker. By default, local is true.
func NewTracker(opts ...TrackerOption) *Tracker {
	t := &Tracker{
		state:  NewFileStateTracker(),
		nested: NewNestedInstructions(),
		local:  true,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(t)
		}
	}
	return t
}

// StateTracker returns the underlying FileStateTracker.
func (t *Tracker) StateTracker() *FileStateTracker {
	return t.state
}

// SetBackgroundChecks sets or clears the BackgroundChecksHook.
func (t *Tracker) SetBackgroundChecks(checks BackgroundChecksHook) {
	t.mu.Lock()
	t.checks = checks
	t.mu.Unlock()
}

func (t *Tracker) getChecks() BackgroundChecksHook {
	t.mu.RLock()
	c := t.checks
	t.mu.RUnlock()
	return c
}

// OnFileRead records a file read, notifies background checks (if contentComplete), and returns
// any newly discovered nested instruction reminder for local reads.
func (t *Tracker) OnFileRead(absolutePath, content string, mtimeMs float64, workspaceCwd string, rawBytes []byte, contentComplete bool) string {
	t.state.RecordFileRead(absolutePath, content, mtimeMs, contentComplete)
	if contentComplete {
		if t.local && t.onSearchTouch != nil {
			b := rawBytes
			if b == nil {
				b = []byte(content)
			}
			t.onSearchTouch(absolutePath, b, mtimeMs)
		}
		if checks := t.getChecks(); checks != nil {
			checks.OnRead(absolutePath)
		}
	}
	if !t.local {
		return ""
	}
	return FormatNestedInstructionsReminder(t.nested.Discover(absolutePath, workspaceCwd))
}

// AssertCanModifyExistingFile verifies read-before-edit and mtime/content freshness.
func (t *Tracker) AssertCanModifyExistingFile(absolutePath string, currentMtimeMs *float64, currentContent *string) error {
	return t.state.AssertCanModifyExistingFile(absolutePath, currentMtimeMs, currentContent)
}

// OnFileModified records an agent file modification, notifies background checks, and optionally
// returns a formatted post-edit context snippet.
func (t *Tracker) OnFileModified(params FileModifiedParams) string {
	t.state.RecordFileModifiedByAgent(params.AbsolutePath, params.NewContent, params.NewMtimeMs)
	if t.local && t.onSearchTouch != nil {
		t.onSearchTouch(params.AbsolutePath, []byte(params.NewContent), params.NewMtimeMs)
	}
	if checks := t.getChecks(); checks != nil {
		checks.OnEdited(params.AbsolutePath)
	}
	if params.IncludePostEditSnippet && params.FirstChangedLine > 0 {
		display := params.DisplayContent
		if display == "" {
			display = params.NewContent
		}
		return FormatPostEditSnippet(display, params.FirstChangedLine, 3)
	}
	return ""
}

// FormatPostEditSnippet formats a compact cat -n style snippet around firstChangedLine
// using a single IndexByte scan so large files never allocate a full split array.
// Context spans max(1, firstChangedLine - contextRadius) to firstChangedLine + contextRadius + 2.
func FormatPostEditSnippet(newContent string, firstChangedLine, contextRadius int) string {
	if firstChangedLine < 1 || newContent == "" {
		return ""
	}
	if contextRadius <= 0 {
		contextRadius = 3
	}

	startLine := max(1, firstChangedLine-contextRadius)
	endLine := firstChangedLine + contextRadius + 2

	var snippetLines []string
	currentLine := 1
	pos := 0
	length := len(newContent)

	for pos <= length && currentLine <= endLine {
		relNl := strings.IndexByte(newContent[pos:], '\n')
		nextNl := -1
		if relNl != -1 {
			nextNl = pos + relNl
		}
		if currentLine >= startLine {
			var lineText string
			if nextNl == -1 {
				lineText = newContent[pos:]
			} else {
				lineText = newContent[pos:nextNl]
			}
			snippetLines = append(snippetLines, fmt.Sprintf("%6d→%s", currentLine, lineText))
		}
		if nextNl == -1 {
			break
		}
		pos = nextNl + 1
		currentLine++
	}

	if len(snippetLines) == 0 {
		return ""
	}
	return fmt.Sprintf("\n\nHere is a snippet of the edited file around line %d:\n%s", firstChangedLine, strings.Join(snippetLines, "\n"))
}
