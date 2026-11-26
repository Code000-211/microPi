package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/MichaelKinsy/PiG/internal/codingagent/filetracking"
)

// ShellSegment represents one simple command in a shell pipeline at the top level.
type ShellSegment struct {
	Words []string
	// Redirects holds targets of >, >|, &> (not appends >>, &>>, or fd duplications such as 2>&1).
	Redirects []string
	Heredoc   bool
}

var (
	digitsOnlyRE    = regexp.MustCompile(`^\d+$`)
	envAssignRE     = regexp.MustCompile(`^\w+=`)
	heredocDelimRE  = regexp.MustCompile(`^['"]?([\w.-]+)['"]?`)
	sedInPlaceRE    = regexp.MustCompile(`^-[a-zA-Z]*i`)
	sedExprFlagRE   = regexp.MustCompile(`^-[a-zA-Z]*e$`)
	tmpdirEnvRE     = regexp.MustCompile(`^\$\{?TMPDIR\}?/`)
	catUnsafeCharRE = regexp.MustCompile(`[|<>$` + "`" + `*?\[\n]|\(|\)`)
)

// ParseShellSegments splits a shell command into pipelines of segments at the top level,
// skipping heredoc bodies and comments, and stripping outer quotes from words.
func ParseShellSegments(command string) [][]ShellSegment {
	var pipelines [][]ShellSegment
	var pipeline []ShellSegment
	segment := ShellSegment{}
	var word strings.Builder
	inWord := false
	pendingRedirect := false
	appending := false
	var heredocs []string

	endWord := func() {
		if !inWord {
			return
		}
		w := word.String()
		if pendingRedirect {
			if !appending {
				segment.Redirects = append(segment.Redirects, w)
			}
			pendingRedirect = false
		} else {
			segment.Words = append(segment.Words, w)
		}
		word.Reset()
		inWord = false
	}

	endSegment := func() {
		endWord()
		pipeline = append(pipeline, segment)
		segment = ShellSegment{}
	}

	endPipeline := func() {
		endSegment()
		pipelines = append(pipelines, pipeline)
		pipeline = nil
	}

	n := len(command)
	for i := 0; i < n; i++ {
		c := command[i]
		switch {
		case c == '\'':
			end := strings.IndexByte(command[i+1:], '\'')
			if end < 0 {
				word.WriteString(command[i+1:])
				inWord = true
				i = n
			} else {
				word.WriteString(command[i+1 : i+1+end])
				inWord = true
				i = i + 1 + end
			}
		case c == '"':
			j := i + 1
			for j < n && command[j] != '"' {
				if command[j] == '\\' && j+1 < n {
					j++
				}
				j++
			}
			word.WriteString(command[i+1 : j])
			inWord = true
			i = j
		case c == '\\':
			if i+1 < n {
				word.WriteByte(command[i+1])
				i++
			}
			inWord = true
		case c == '\n':
			endPipeline()
			for len(heredocs) > 0 {
				delimiter := heredocs[0]
				heredocs = heredocs[1:]
				lineStart := i + 1
				for {
					relEnd := strings.IndexByte(command[lineStart:], '\n')
					var line string
					var lineEnd int
					if relEnd < 0 {
						line = command[lineStart:]
						lineEnd = -1
					} else {
						lineEnd = lineStart + relEnd
						line = command[lineStart:lineEnd]
					}
					if strings.TrimSpace(line) == delimiter || lineEnd < 0 {
						if lineEnd < 0 {
							i = n
						} else {
							i = lineEnd
						}
						break
					}
					lineStart = lineEnd + 1
				}
			}
		case c == '#' && !inWord:
			relEnd := strings.IndexByte(command[i:], '\n')
			if relEnd < 0 {
				i = n - 1
			} else {
				i = i + relEnd - 1
			}
		case c == ';' || (c == '&' && i+1 < n && command[i+1] == '&') || (c == '|' && i+1 < n && command[i+1] == '|'):
			endPipeline()
			if c != ';' {
				i++
			}
		case c == '|':
			endSegment()
		case c == '&' && i+1 < n && command[i+1] == '>':
			endWord()
			appending = i+2 < n && command[i+2] == '>'
			if appending {
				i += 2
			} else {
				i++
			}
			pendingRedirect = true
		case c == '<' && i+1 < n && command[i+1] == '<' && (i+2 >= n || command[i+2] != '<'):
			endWord()
			j := i + 2
			if j < n && command[j] == '-' {
				j++
			}
			for j < n && command[j] == ' ' {
				j++
			}
			if m := heredocDelimRE.FindStringSubmatchIndex(command[j:]); m != nil {
				heredocs = append(heredocs, command[j+m[2]:j+m[3]])
				segment.Heredoc = true
				i = j + m[1] - 1
			} else {
				i++
			}
		case c == '>':
			if inWord && digitsOnlyRE.MatchString(word.String()) {
				word.Reset()
				inWord = false
			} else {
				endWord()
			}
			j := i + 1
			appending = j < n && command[j] == '>'
			if j < n && (command[j] == '>' || command[j] == '|') {
				j++
			}
			if j < n && command[j] == '&' {
				i = j
				for i+1 < n && (unicode.IsDigit(rune(command[i+1])) || command[i+1] == '-') {
					i++
				}
				continue
			}
			i = j - 1
			pendingRedirect = true
		case c == ' ' || c == '\t' || c == '(' || c == ')' || c == '{' || c == '}':
			endWord()
		default:
			word.WriteByte(c)
			inWord = true
		}
	}
	endPipeline()

	var out [][]ShellSegment
	for _, p := range pipelines {
		nonEmpty := false
		for _, s := range p {
			if len(s.Words) > 0 || len(s.Redirects) > 0 {
				nonEmpty = true
				break
			}
		}
		if nonEmpty {
			out = append(out, p)
		}
	}
	return out
}

// CommandName returns the command name (past variable assignments and wrappers) and its remaining arguments.
func CommandName(words []string) (name string, args []string) {
	i := 0
	for i < len(words) {
		w := words[i]
		if envAssignRE.MatchString(w) || w == "sudo" || w == "command" || w == "env" || w == "nice" || w == "time" {
			i++
			continue
		}
		break
	}
	if i >= len(words) {
		return "", nil
	}
	cmd := words[i]
	if idx := strings.LastIndexByte(cmd, '/'); idx >= 0 {
		cmd = cmd[idx+1:]
	}
	return cmd, words[i+1:]
}

// UnderDir resolves target relative to dir unless target is absolute, ~-prefixed, or dir is empty.
func UnderDir(dir, target string) string {
	if strings.HasPrefix(target, "/") || strings.HasPrefix(target, "~") || dir == "" {
		return target
	}
	return dir + "/" + target
}

// FileWrite describes a direct file write detected in a bash command.
type FileWrite struct {
	How    string
	Target string
	Dir    string
}

func isScratchPath(target string) bool {
	tmp := strings.TrimRight(os.TempDir(), "/")
	return strings.HasPrefix(target, "/dev/") ||
		strings.HasPrefix(target, "/tmp/") ||
		(tmp != "" && strings.HasPrefix(target, tmp+"/")) ||
		tmpdirEnvRE.MatchString(target) ||
		strings.HasPrefix(target, "/proc/")
}

// FileWritesInCommand returns file writes detected at the top level of command
// (single-file sed -i, perl -i, or authored literal redirections/tee).
func FileWritesInCommand(command string) []FileWrite {
	var writes []FileWrite
	dir := ""
	scratch := func(target string) bool {
		return isScratchPath(UnderDir(dir, target))
	}

	for _, pipeline := range ParseShellSegments(command) {
		if len(pipeline) == 1 {
			name, args := CommandName(pipeline[0].Words)
			if name == "cd" && len(args) > 0 && args[0] != "" {
				dir = UnderDir(dir, args[0])
				continue
			}
		}

		for _, segment := range pipeline {
			name, args := CommandName(segment.Words)
			if name == "sed" || name == "perl" {
				inPlace := false
				for _, a := range args {
					if a == "--in-place" || strings.HasPrefix(a, "--in-place=") || sedInPlaceRE.MatchString(a) {
						inPlace = true
						break
					}
				}
				var positional []string
				script := false
				for k := 0; k < len(args); k++ {
					a := args[k]
					if a == "-e" || a == "-f" || a == "--expression" || a == "--file" || sedExprFlagRE.MatchString(a) {
						script = true
						k++
					} else if !strings.HasPrefix(a, "-") {
						positional = append(positional, a)
					}
				}
				files := positional
				if !script && len(positional) > 0 {
					files = positional[1:]
				} else if !script {
					files = nil
				}
				if inPlace && len(files) == 1 && files[0] != "" && !strings.ContainsAny(files[0], "*?[") && !scratch(files[0]) {
					writes = append(writes, FileWrite{How: name + " -i", Target: files[0], Dir: dir})
				}
			}
		}

		if len(pipeline) == 0 {
			continue
		}
		head := pipeline[0]
		firstName, firstArgs := CommandName(head.Words)
		authored := firstName == "echo" ||
			firstName == "printf" ||
			(firstName == "cat" && (head.Heredoc || len(firstArgs) == 0)) ||
			(firstName == "tee" && head.Heredoc)
		if !authored {
			continue
		}

		for _, segment := range pipeline {
			name, args := CommandName(segment.Words)
			for _, target := range segment.Redirects {
				if !scratch(target) {
					writes = append(writes, FileWrite{How: "a redirection", Target: target, Dir: dir})
				}
			}
			if name == "tee" {
				appendFlag := false
				for _, a := range args {
					if a == "-a" || a == "--append" {
						appendFlag = true
						break
					}
				}
				if !appendFlag {
					for _, target := range args {
						if !strings.HasPrefix(target, "-") && !scratch(target) {
							writes = append(writes, FileWrite{How: "tee", Target: target, Dir: dir})
						}
					}
				}
			}
		}
	}
	return writes
}

// FilesShownByCat returns the directory and file paths printed whole by simple cat commands
// (allowing -n and a leading cd). Returns ("", nil) for pipes, redirections, globs, or other flags.
func FilesShownByCat(command string) (string, []string) {
	if catUnsafeCharRE.MatchString(command) {
		return "", nil
	}
	dir := ""
	var files []string
	for _, pipeline := range ParseShellSegments(command) {
		if len(pipeline) != 1 {
			return "", nil
		}
		seg := pipeline[0]
		if len(seg.Redirects) > 0 || seg.Heredoc {
			return "", nil
		}
		name, args := CommandName(seg.Words)
		if name == "cd" && len(args) == 1 && len(files) == 0 {
			dir = UnderDir(dir, args[0])
			continue
		}
		if name != "cat" {
			return "", nil
		}
		for _, arg := range args {
			if arg == "-n" {
				continue
			}
			if strings.HasPrefix(arg, "-") {
				return "", nil
			}
			files = append(files, arg)
		}
	}
	if len(files) == 0 {
		return "", nil
	}
	return dir, files
}

// FileWriteRefusal formats a refusal message when bash file writes are disallowed (e.g. on /remote).
func FileWriteRefusal(writes []FileWrite) string {
	if len(writes) == 0 {
		return ""
	}
	w := writes[0]
	target := UnderDir(w.Dir, w.Target)
	return fmt.Sprintf("Use the edit or write tool instead of %s to modify %s.", w.How, target)
}

// FileSnapshot captures the pre-command state of a local file targeted by a bash command.
type FileSnapshot struct {
	AbsolutePath string
	Target       string
	Existed      bool
	Content      string
}

// WatchedFileChange describes a file modified by a bash command.
type WatchedFileChange struct {
	AbsolutePath string
	Target       string
	Created      bool
	StartLine    int
	EndLine      int
}

// WatchFiles snapshots the local targets of FileWritesInCommand before a bash command runs.
func WatchFiles(cwd string, writes []FileWrite) []FileSnapshot {
	if len(writes) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(writes))
	var snapshots []FileSnapshot
	for _, w := range writes {
		rel := UnderDir(w.Dir, w.Target)
		abs, err := resolveToCwd(rel, cwd)
		if err != nil {
			continue
		}
		if _, ok := seen[abs]; ok {
			continue
		}
		seen[abs] = struct{}{}

		snap := FileSnapshot{
			AbsolutePath: abs,
			Target:       w.Target,
		}
		if data, err := os.ReadFile(abs); err == nil {
			snap.Existed = true
			snap.Content = string(data)
		}
		snapshots = append(snapshots, snap)
	}
	return snapshots
}

// CheckChangedFiles compares post-command disk contents against snapshots, notifies tracker
// (if non-nil), and returns the list of changed or created files with 1-indexed changed line ranges.
func CheckChangedFiles(snapshots []FileSnapshot, tracker *filetracking.Tracker) []WatchedFileChange {
	var changes []WatchedFileChange
	for _, snap := range snapshots {
		data, err := os.ReadFile(snap.AbsolutePath)
		if err != nil {
			continue
		}
		newContent := string(data)
		if snap.Existed && newContent == snap.Content {
			continue
		}
		startLine, endLine := diffLineRange(snap.Content, newContent)
		changes = append(changes, WatchedFileChange{
			AbsolutePath: snap.AbsolutePath,
			Target:       snap.Target,
			Created:      !snap.Existed,
			StartLine:    startLine,
			EndLine:      endLine,
		})
		if tracker != nil {
			mtimeMs := float64(time.Now().UnixMilli())
			if info, statErr := os.Stat(snap.AbsolutePath); statErr == nil {
				mtimeMs = float64(info.ModTime().UnixNano()) / 1e6
			}
			tracker.OnFileModified(filetracking.FileModifiedParams{
				AbsolutePath:     snap.AbsolutePath,
				NewContent:       newContent,
				NewMtimeMs:       mtimeMs,
				FirstChangedLine: startLine,
			})
		}
	}
	return changes
}

func diffLineRange(oldText, newText string) (int, int) {
	oldLines := strings.Split(normalizeToLF(oldText), "\n")
	newLines := strings.Split(normalizeToLF(newText), "\n")
	prefix := 0
	for prefix < len(oldLines) && prefix < len(newLines) && oldLines[prefix] == newLines[prefix] {
		prefix++
	}
	if prefix == len(oldLines) && prefix == len(newLines) {
		return 1, 1
	}
	suffix := 0
	for suffix < len(oldLines)-prefix && suffix < len(newLines)-prefix &&
		oldLines[len(oldLines)-1-suffix] == newLines[len(newLines)-1-suffix] {
		suffix++
	}
	startLine := prefix + 1
	endLine := max(startLine, len(newLines)-suffix)
	return startLine, endLine
}

// Ensure filepath is referenced if needed by callers.
var _ = filepath.Clean
