package filetracking

import (
	"errors"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
)

// TrackedFileState records what the agent knows about a file it read or wrote.
type TrackedFileState struct {
	AbsolutePath    string
	LastReadMtimeMs float64
	// Content is the exact on-disk text (BOM and line endings included), or a prefix when ContentComplete is false.
	Content string
	// ContentComplete is false when only a prefix of a large file was read, so Content cannot be compared or reparsed.
	ContentComplete bool
	// ModifiedByAgent is true once the agent has written this file through edit/write.
	ModifiedByAgent bool
}

// IncludeSourceMatch pairs a source file that includes a header with the #include text it used.
type IncludeSourceMatch struct {
	Source  string
	Include string
}

// UnsavedOverlay represents an in-memory file overlay for C/C++/HIP header checks.
type UnsavedOverlay struct {
	Filename string
	Content  string
}

// ErrUnreadFile is returned when modifying an existing file that has not been read yet.
var ErrUnreadFile = errors.New("File has not been read yet. Read it first before writing to it.")

// ErrStaleFile is returned when an existing file changed on disk since the agent last read or wrote it.
var ErrStaleFile = errors.New("File has been modified since it was last read (either by the user or an external tool). Read the file again before editing it.")

// StaleFileError is the typed error for ErrStaleFile.
type StaleFileError struct{}

func (e *StaleFileError) Error() string { return ErrStaleFile.Error() }
func (e *StaleFileError) Is(target error) bool {
	return target == ErrStaleFile
}

// PathKey returns an absolute, cleaned path key, lowercased on case-insensitive Windows filesystems.
func PathKey(filePath string) string {
	norm := normalizeAbs(filePath)
	if runtime.GOOS == "windows" {
		return strings.ToLower(norm)
	}
	return norm
}

func normalizeAbs(filePath string) string {
	abs, err := filepath.Abs(filePath)
	if err != nil {
		return filepath.Clean(filePath)
	}
	return filepath.Clean(abs)
}

// changedSinceRead reports whether mtimeMs is newer than the file's when the agent last read or wrote it,
// with 10 ms tolerance for timestamp rounding.
func changedSinceRead(state *TrackedFileState, mtimeMs float64) bool {
	return mtimeMs > state.LastReadMtimeMs+10
}

var localIncludeRE = regexp.MustCompile(`(?m)^\s*#\s*include\s*"([^"]+)"`)

var cppSourceExts = map[string]bool{
	".cpp": true, ".cc": true, ".cxx": true, ".c": true, ".cu": true, ".hip": true,
}

var cppHeaderExts = map[string]bool{
	".h": true, ".hh": true, ".hpp": true, ".hxx": true, ".cuh": true, ".hiph": true,
}

func isCppOrHipSource(filePath string) bool {
	return cppSourceExts[strings.ToLower(filepath.Ext(filePath))]
}

func isCppOrHipHeader(filePath string) bool {
	return cppHeaderExts[strings.ToLower(filepath.Ext(filePath))]
}

// FileStateTracker tracks read/modified state and local C/C++/HIP include relationships for files in a session.
type FileStateTracker struct {
	mu                   sync.RWMutex
	files                map[string]*TrackedFileState
	headerToHostSource   map[string]string
	includeTextToSources map[string]map[string]struct{}
}

// NewFileStateTracker creates a ready-to-use FileStateTracker.
func NewFileStateTracker() *FileStateTracker {
	return &FileStateTracker{
		files:                make(map[string]*TrackedFileState),
		headerToHostSource:   make(map[string]string),
		includeTextToSources: make(map[string]map[string]struct{}),
	}
}

// AssertCanModifyExistingFile verifies that an existing file was previously read in this session
// and has not been modified externally since the last read/edit.
// A changed mtime alone is not a failure when currentContent matches the tracked complete content.
func (t *FileStateTracker) AssertCanModifyExistingFile(absolutePath string, currentMtimeMs *float64, currentContent *string) error {
	key := PathKey(absolutePath)

	t.mu.Lock()
	defer t.mu.Unlock()

	state, ok := t.files[key]
	if !ok {
		return ErrUnreadFile
	}

	sameContent := currentContent != nil && state.ContentComplete && *currentContent == state.Content
	if currentMtimeMs != nil && changedSinceRead(state, *currentMtimeMs) {
		if !sameContent {
			return &StaleFileError{}
		}
		state.LastReadMtimeMs = *currentMtimeMs
	}
	// Guard against sub-millisecond external modifications where mtime did not advance.
	if currentContent != nil && state.ContentComplete && !sameContent {
		return &StaleFileError{}
	}
	return nil
}

// RecordFileRead records that a file was read. Pass contentComplete=false when content is only a prefix.
func (t *FileStateTracker) RecordFileRead(absolutePath, content string, mtimeMs float64, contentComplete bool) TrackedFileState {
	key := PathKey(absolutePath)
	norm := normalizeAbs(absolutePath)

	t.mu.Lock()
	defer t.mu.Unlock()

	modifiedByAgent := false
	if existing, ok := t.files[key]; ok {
		modifiedByAgent = existing.ModifiedByAgent
	}
	state := &TrackedFileState{
		AbsolutePath:    norm,
		LastReadMtimeMs: mtimeMs,
		Content:         content,
		ContentComplete: contentComplete,
		ModifiedByAgent: modifiedByAgent,
	}
	t.files[key] = state

	if contentComplete && isCppOrHipSource(norm) {
		t.indexLocalIncludesLocked(norm, content)
	}
	return *state
}

// RecordFileModifiedByAgent updates the file's content and mtime after a successful edit/write by the agent.
// newContent must be exactly what was written to disk (BOM and line endings included).
func (t *FileStateTracker) RecordFileModifiedByAgent(absolutePath, newContent string, newMtimeMs float64) {
	key := PathKey(absolutePath)
	norm := normalizeAbs(absolutePath)

	t.mu.Lock()
	defer t.mu.Unlock()

	t.files[key] = &TrackedFileState{
		AbsolutePath:    norm,
		LastReadMtimeMs: newMtimeMs,
		Content:         newContent,
		ContentComplete: true,
		ModifiedByAgent: true,
	}
	if isCppOrHipSource(norm) {
		t.indexLocalIncludesLocked(norm, newContent)
	}
}

// NeedsContentCompare reports whether mtimeMs is newer than when the agent last read or wrote the file,
// whose whole content it knows.
func (t *FileStateTracker) NeedsContentCompare(absolutePath string, mtimeMs float64) bool {
	key := PathKey(absolutePath)
	t.mu.RLock()
	defer t.mu.RUnlock()

	state, ok := t.files[key]
	return ok && state.ContentComplete && changedSinceRead(state, mtimeMs)
}

// HasBeenRead reports whether the file has been read or written in this session.
func (t *FileStateTracker) HasBeenRead(absolutePath string) bool {
	key := PathKey(absolutePath)
	t.mu.RLock()
	defer t.mu.RUnlock()

	_, ok := t.files[key]
	return ok
}

// GetTrackedState returns a copy of the tracked state for absolutePath, if present.
func (t *FileStateTracker) GetTrackedState(absolutePath string) (TrackedFileState, bool) {
	key := PathKey(absolutePath)
	t.mu.RLock()
	defer t.mu.RUnlock()

	state, ok := t.files[key]
	if !ok {
		return TrackedFileState{}, false
	}
	return *state, true
}

// FindHostSourceForHeader returns a source file read in this session that includes the header relative to its own folder.
func (t *FileStateTracker) FindHostSourceForHeader(headerAbsolutePath string) (string, bool) {
	key := PathKey(headerAbsolutePath)
	t.mu.RLock()
	defer t.mu.RUnlock()

	src, ok := t.headerToHostSource[key]
	return src, ok
}

// FindSourcesIncludingByPath returns sources read in this session with an #include "..." matching a trailing
// part of headerAbsolutePath, longest first.
func (t *FileStateTracker) FindSourcesIncludingByPath(headerAbsolutePath string) []IncludeSourceMatch {
	norm := normalizeAbs(headerAbsolutePath)
	parts := strings.FieldsFunc(norm, func(r rune) bool { return r == '/' || r == '\\' })

	t.mu.RLock()
	defer t.mu.RUnlock()

	var found []IncludeSourceMatch
	maxK := min(len(parts)-1, 6)
	for k := maxK; k >= 1; k-- {
		include := strings.Join(parts[len(parts)-k:], "/")
		key := include
		if runtime.GOOS == "windows" {
			key = strings.ToLower(include)
		}
		for source := range t.includeTextToSources[key] {
			found = append(found, IncludeSourceMatch{Source: source, Include: include})
		}
	}
	return found
}

// GetUnsavedHeaderOverlays returns headers modified by the agent in this session (excluding excludePath).
func (t *FileStateTracker) GetUnsavedHeaderOverlays(excludePath string) []UnsavedOverlay {
	excludeKey := ""
	if excludePath != "" {
		excludeKey = PathKey(excludePath)
	}

	t.mu.RLock()
	defer t.mu.RUnlock()

	var overlays []UnsavedOverlay
	for key, state := range t.files {
		if excludeKey != "" && key == excludeKey {
			continue
		}
		if state.ModifiedByAgent && state.ContentComplete && isCppOrHipHeader(state.AbsolutePath) {
			overlays = append(overlays, UnsavedOverlay{
				Filename: state.AbsolutePath,
				Content:  state.Content,
			})
		}
	}
	return overlays
}

func (t *FileStateTracker) indexLocalIncludesLocked(sourceAbsolutePath, content string) {
	sourceDir := filepath.Dir(sourceAbsolutePath)
	matches := localIncludeRE.FindAllStringSubmatch(content, -1)
	for _, m := range matches {
		if len(m) < 2 || m[1] == "" {
			continue
		}
		relInclude := m[1]
		resolvedHeader := filepath.Clean(filepath.Join(sourceDir, relInclude))
		t.headerToHostSource[PathKey(resolvedHeader)] = sourceAbsolutePath

		incText := strings.ReplaceAll(relInclude, "\\", "/")
		for strings.HasPrefix(incText, "./") {
			incText = incText[2:]
		}
		textKey := incText
		if runtime.GOOS == "windows" {
			textKey = strings.ToLower(textKey)
		}
		sources, ok := t.includeTextToSources[textKey]
		if !ok {
			sources = make(map[string]struct{})
			t.includeTextToSources[textKey] = sources
		}
		sources[sourceAbsolutePath] = struct{}{}
	}
}
