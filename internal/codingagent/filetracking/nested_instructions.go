package filetracking

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode"

	"github.com/MichaelKinsy/PiG/internal/text"
)

var nestedInstructionCandidates = []string{
	"AGENTS.override.md",
	"AGENTS.md",
	"AGENTS.MD",
	"CLAUDE.md",
	"CLAUDE.MD",
}

// InstructionFile represents a discovered nested instruction file and its BOM-stripped content.
type InstructionFile struct {
	Path    string
	Content string
}

type cachedInstruction struct {
	file *InstructionFile
}

func loadInstructionFileFromDir(dir string) *InstructionFile {
	for _, filename := range nestedInstructionCandidates {
		filePath := filepath.Join(dir, filename)
		info, err := os.Stat(filePath)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		data, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}
		_, content := text.SplitBom(string(data))
		return &InstructionFile{
			Path:    filePath,
			Content: content,
		}
	}
	return nil
}

// FormatNestedInstructionsReminder formats nested AGENTS.md / CLAUDE.md files discovered on read
// into a <system-reminder> block.
func FormatNestedInstructionsReminder(files []InstructionFile) string {
	if len(files) == 0 {
		return ""
	}
	frames := make([]string, len(files))
	for i, f := range files {
		trimmed := strings.TrimRightFunc(f.Content, unicode.IsSpace)
		frames[i] = fmt.Sprintf("Contents of %s:\n\n%s", f.Path, trimmed)
	}
	return fmt.Sprintf("\n\n<system-reminder>\n%s\n</system-reminder>", strings.Join(frames, "\n\n"))
}

// NestedInstructions discovers CLAUDE.md / AGENTS.md files in folders strictly below workspaceCwd,
// returning each at most once per session.
type NestedInstructions struct {
	mu                     sync.Mutex
	dirInstructionCache    map[string]cachedInstruction
	sentNestedInstructions map[string]struct{}
}

// NewNestedInstructions creates a ready-to-use NestedInstructions tracker.
func NewNestedInstructions() *NestedInstructions {
	return &NestedInstructions{
		dirInstructionCache:    make(map[string]cachedInstruction),
		sentNestedInstructions: make(map[string]struct{}),
	}
}

// Discover finds any unseen nested instruction files in directories strictly below workspaceCwd
// up to filepath.Dir(readAbsolutePath), ordered root-first.
func (n *NestedInstructions) Discover(readAbsolutePath, workspaceCwd string) []InstructionFile {
	rootNorm := normalizeAbs(workspaceCwd)
	rootKey := PathKey(rootNorm)
	readNorm := normalizeAbs(readAbsolutePath)
	readKey := PathKey(readNorm)

	sep := string(os.PathSeparator)
	rootPrefix := rootKey
	if !strings.HasSuffix(rootPrefix, sep) {
		rootPrefix += sep
	}
	if !strings.HasPrefix(readKey, rootPrefix) {
		return nil
	}

	// Collect ancestor directories between rootNorm (exclusive) and dirname(readNorm) (inclusive), root-first.
	var dirs []string
	currentDir := filepath.Dir(readNorm)
	for {
		currentKey := PathKey(currentDir)
		if currentKey == rootKey || !strings.HasPrefix(currentKey, rootPrefix) {
			break
		}
		dirs = append(dirs, currentDir)
		parentDir := filepath.Dir(currentDir)
		if parentDir == currentDir {
			break
		}
		currentDir = parentDir
	}
	if len(dirs) == 0 {
		return nil
	}
	// Reverse to root-first.
	for i, j := 0, len(dirs)-1; i < j; i, j = i+1, j-1 {
		dirs[i], dirs[j] = dirs[j], dirs[i]
	}

	n.mu.Lock()
	defer n.mu.Unlock()

	var attached []InstructionFile
	for _, dir := range dirs {
		dirKey := PathKey(dir)
		cached, ok := n.dirInstructionCache[dirKey]
		if !ok {
			cached = cachedInstruction{file: loadInstructionFileFromDir(dir)}
			n.dirInstructionCache[dirKey] = cached
		}
		if cached.file == nil {
			continue
		}
		fileKey := PathKey(cached.file.Path)
		// If the user explicitly called Read on the instruction file itself, mark it as seen without duplicating it.
		if fileKey == readKey {
			n.sentNestedInstructions[fileKey] = struct{}{}
			continue
		}
		if _, sent := n.sentNestedInstructions[fileKey]; sent {
			continue
		}
		n.sentNestedInstructions[fileKey] = struct{}{}
		attached = append(attached, *cached.file)
	}
	return attached
}
