package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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

// FileWriteRefusal is microPi's fileWriteRefusal (bash.ts): why a command that changes files as the edit and write
// tools would was not run (on a remote machine, where what changed is not known here), and what to do instead.
func FileWriteRefusal(writes []FileWrite) string {
	what := make([]string, len(writes))
	for i, w := range writes {
		what[i] = fmt.Sprintf("%s (%s)", w.Target, w.How)
	}
	return "Not run: the command changes " + strings.Join(what, ", ") + ". Change files with edit (exact replacements: it checks that the file was read and did not change since, and reports the compiler's new errors) or write (whole files). " +
		"Appending (`>>`), files under /tmp, and a program's output redirected into a file run."
}

// FileSnapshot is an existing file a bash command changes as edit and write would, with its content before the
// command ran (microPi's WatchedFile).
type FileSnapshot struct {
	AbsolutePath string
	// Target is the file as the command names it.
	Target string
	// Content is "" when the file could not be read.
	Content string
}

// WatchedFileChange is a watched file the command changed, with the first and last line (1-based) that changed.
type WatchedFileChange struct {
	AbsolutePath string
	Target       string
	StartLine    int
	EndLine      int
}

// WatchFiles is microPi's watchFiles: the existing files of writes, with their content before the command ran. Only
// files that exist: a new file has no compiler errors to compare.
func WatchFiles(cwd string, writes []FileWrite) []FileSnapshot {
	seen := make(map[string]struct{}, len(writes))
	var snapshots []FileSnapshot
	for _, w := range writes {
		abs, err := resolveToCwd(UnderDir(w.Dir, w.Target), cwd)
		if err != nil {
			continue
		}
		if _, ok := seen[abs]; ok {
			continue
		}
		if _, err := os.Stat(abs); err != nil {
			continue
		}
		seen[abs] = struct{}{}
		snap := FileSnapshot{AbsolutePath: abs, Target: w.Target}
		if data, err := os.ReadFile(abs); err == nil {
			snap.Content = string(data)
		}
		snapshots = append(snapshots, snap)
	}
	return snapshots
}

// CheckChangedFiles is microPi's checkChangedFiles: for each watched file the command changed, the lines that changed.
// The file tracker takes the new content as read (an edit after it needs no read). Empty when nothing changed.
func CheckChangedFiles(snapshots []FileSnapshot, tracker *filetracking.Tracker) []WatchedFileChange {
	var changes []WatchedFileChange
	for _, snap := range snapshots {
		data, err := os.ReadFile(snap.AbsolutePath)
		if err != nil {
			continue
		}
		newContent := string(data)
		if newContent == snap.Content {
			continue
		}
		startLine, endLine := changedLineRange(snap.Content, newContent)
		changes = append(changes, WatchedFileChange{AbsolutePath: snap.AbsolutePath, Target: snap.Target, StartLine: startLine, EndLine: endLine})
		if tracker == nil {
			continue
		}
		info, statErr := os.Stat(snap.AbsolutePath)
		if statErr != nil {
			continue
		}
		tracker.OnFileModified(filetracking.FileModifiedParams{
			AbsolutePath:     snap.AbsolutePath,
			NewContent:       newContent,
			NewMtimeMs:       float64(info.ModTime().UnixNano()) / 1e6,
			FirstChangedLine: startLine,
		})
	}
	return changes
}

// FormatChangedFiles is the report microPi's checkChangedFiles returns, appended to the command's result:
// "\n\nX changed at line N." or "\n\nX changed in lines A-B." per file.
func FormatChangedFiles(changes []WatchedFileChange) string {
	var b strings.Builder
	for _, ch := range changes {
		if ch.StartLine == ch.EndLine {
			fmt.Fprintf(&b, "\n\n%s changed at line %d.", ch.Target, ch.StartLine)
		} else {
			fmt.Fprintf(&b, "\n\n%s changed in lines %d-%d.", ch.Target, ch.StartLine, ch.EndLine)
		}
	}
	return b.String()
}

// changedLineRange is microPi's changedLineRange: the first and last line (1-based) where after differs from before.
func changedLineRange(before, after string) (int, int) {
	a := strings.Split(before, "\n")
	b := strings.Split(after, "\n")
	start := 0
	for start < len(a) && start < len(b) && a[start] == b[start] {
		start++
	}
	endA, endB := len(a)-1, len(b)-1
	for endA > start && endB > start && a[endA] == b[endB] {
		endA--
		endB--
	}
	return start + 1, max(start+1, endB+1)
}

// Ensure filepath is referenced if needed by callers.
var _ = filepath.Clean
