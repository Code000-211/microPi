package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
	"github.com/MichaelKinsy/PiG/internal/nodespawn"
)

// ─── Grep Tool ────────────────────────────────────────────────────────────────

type grepParams struct {
	Pattern    string   `json:"pattern"`
	Path       string   `json:"path,omitempty"`
	Glob       string   `json:"glob,omitempty"`       // file pattern filter (e.g. '*.ts')
	IgnoreCase bool     `json:"ignoreCase,omitempty"` // case-insensitive search
	Literal    bool     `json:"literal,omitempty"`    // treat pattern as literal string (--fixed-strings)
	Context    *float64 `json:"context,omitempty"`    // lines of context around each match
	Limit      *float64 `json:"limit,omitempty"`      // max matches (default: 100)
}

// GrepTool runs ripgrep to search files, mirroring upstream grep.ts.
//
// RgPath, when non-empty, pins the ripgrep binary. Otherwise rg is resolved
// on every call through Tools (upstream ensureTool: <agentDir>/bin, PATH,
// then a download); with no Tools, PATH only.
type GrepTool struct {
	CWD    string
	RgPath string
	Tools  *ToolsManager
	// Operations delegates the filesystem reads; nil selects the local filesystem (upstream options.operations).
	Operations *GrepOperations
}

// GrepOperations is upstream's GrepOperations: pluggable path checks. The search itself always runs the local ripgrep; to search a
// remote system, wrap the tool's Execute. Ports packages/coding-agent/src/core/tools/grep.ts.
type GrepOperations struct {
	// IsDirectory reports whether the path is a directory and fails when it does not exist.
	IsDirectory func(absolutePath string) (bool, error)
	// ReadFile is upstream's optional readFile.
	//
	// Deprecated: Ignored. Context lines come directly from ripgrep, so the tool never calls it. Wrap the tool's Execute to search a remote system.
	ReadFile func(absolutePath string) (string, error)
}

func (t *GrepTool) operations() GrepOperations {
	if t.Operations != nil {
		return *t.Operations
	}
	return GrepOperations{
		IsDirectory: func(path string) (bool, error) {
			info, err := os.Stat(path)
			if err != nil {
				return false, err
			}
			return info.IsDir(), nil
		},
	}
}

func (t *GrepTool) Name() string  { return "grep" }
func (t *GrepTool) Label() string { return "" }

func (t *GrepTool) Schema() ai.ToolSchema { return t.schema(false) }

// BorrowedSchema is Schema with the parameters shared, not copied: the caller reads them and never modifies them.
func (t *GrepTool) BorrowedSchema() ai.ToolSchema { return t.schema(true) }

func (t *GrepTool) schema(borrow bool) ai.ToolSchema {
	return toolSchemaWithParameters(ai.ToolSchema{
		Name:        "grep",
		Description: "Search file contents for a pattern. Returns matching lines with file paths and line numbers. Respects .gitignore. Output is truncated to 100 matches or 50KB (whichever is hit first). Long lines are truncated to 500 chars.",
	}, `{"type":"object","required":["pattern"],"properties":{
		"pattern":{"type":"string","description":"Search pattern (regex or literal string)"},
		"path":{"type":"string","description":"Directory or file to search (default: current directory)"},
		"glob":{"type":"string","description":"Filter files by glob pattern relative to path, e.g. 'src/**/*.ts'. Without '/', e.g. '*.ts', it matches file names at any depth"},
		"ignoreCase":{"type":"boolean","description":"Case-insensitive search (default: false)"},
		"literal":{"type":"boolean","description":"Treat pattern as literal string instead of regex (default: false)"},
		"context":{"type":"number","description":"Number of lines to show before and after each match (default: 0)"},
		"limit":{"type":"number","description":"Maximum number of matches to return (default: 100)"}
	}}`, borrow)
}

func (t *GrepTool) ExecutionMode() agent.ToolExecutionMode { return agent.ToolModeParallel }

// grepDefaultLimit mirrors upstream DEFAULT_LIMIT.
const grepDefaultLimit = 100

// maxSafeInteger is Number.MAX_SAFE_INTEGER.
const maxSafeInteger = 1<<53 - 1

// grepLeadingDotSlash is the "./" prefix of a glob (after an optional "!") that rg never matches.
var grepLeadingDotSlash = lazyregexp.New(`^(!?)(?:\./)+`)

// Execute mirrors upstream grep.ts execute: resolve the path, stream `rg --json` events with no line-length cap (upstream reads with
// readline), take the context lines from rg's own `context` events, stop rg once the match limit is proved to have cut results off,
// surface rg's stderr on failure, then apply byte truncation and upstream's notices.
func (t *GrepTool) Execute(ctx context.Context, _ string, rawParams json.RawMessage, _ agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	var p grepParams
	if err := json.Unmarshal(NormalizeToolArguments(rawParams, GrepAliases), &p); err != nil {
		return agent.AgentToolResult{}, fmt.Errorf("grep: invalid params: %w", err)
	}
	if ctx.Err() != nil {
		return grepError("Operation aborted"), nil
	}

	searchDir := p.Path
	if searchDir == "" {
		searchDir = "."
	}
	cwd, err := toolCWD(ctx, t.CWD)
	if err != nil {
		return agent.AgentToolResult{}, err
	}
	searchPath, err := resolvePath(cwd, searchDir)
	if err != nil {
		return agent.AgentToolResult{}, err
	}
	ops := t.operations()
	isDirectory, err := ops.IsDirectory(searchPath)
	if err != nil {
		return grepError("Path not found: " + searchPath), nil
	}

	contextValue := 0.0
	if p.Context != nil && *p.Context > 0 {
		contextValue = math.Min(math.Floor(*p.Context), maxSafeInteger)
	}
	effectiveLimit := float64(grepDefaultLimit)
	if p.Limit != nil {
		effectiveLimit = math.Floor(*p.Limit)
	}
	effectiveLimit = math.Max(1, effectiveLimit)

	f := grepFormatter{searchPath: searchPath, isDirectory: isDirectory}
	joined := newHeadJoin(truncationLimits(DefaultMaxBytes, 1<<53-1))
	outputBytes, outputLineCount := 0, 0
	var readFileFn func(string) (string, error)
	if t.Operations != nil && t.Operations.ReadFile != nil {
		readFileFn = t.Operations.ReadFile
	}

	matchCount, matchLimitReached, err := nativeGrep(ctx, nativeGrepOpts{
		Pattern:     p.Pattern,
		SearchPath:  searchPath,
		IsDirectory: isDirectory,
		Glob:        p.Glob,
		IgnoreCase:  p.IgnoreCase,
		Literal:     p.Literal,
		Context:     int(contextValue),
		Limit:       int(effectiveLimit),
		ReadFile:    readFileFn,
	}, func(ev nativeGrepEvent) bool {
		length := f.emit(grepEvent{
			path:       []byte(ev.absPath),
			lineNumber: ev.lineNumber,
			lineText:   ev.lineText,
			isMatch:    ev.isMatch,
		}, joined)
		outputBytes += length + 1
		outputLineCount++
		return false
	})
	if ctx.Err() != nil {
		return grepError("Operation aborted"), nil
	}
	if err != nil {
		return grepError(err.Error()), nil
	}
	if matchCount == 0 {
		return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "No matches found"}}}, nil
	}

	tr := joined.Result()
	tr.TotalLines = outputLineCount
	tr.TotalBytes = outputBytes - 1
	output := tr.Content
	var notices []string
	details := &GrepDetails{}
	if matchLimitReached {
		notices = append(notices, fmt.Sprintf("%s matches limit reached. Use limit=%s for more, or refine pattern",
			jsNumber(effectiveLimit), jsNumber(effectiveLimit*2)))
		details.MatchLimitReached = effectiveLimit
	}
	if tr.Truncated {
		notices = append(notices, FormatSize(DefaultMaxBytes)+" limit reached")
		trc := tr
		details.Truncation = &trc
	}
	if f.linesTruncated {
		notices = append(notices, fmt.Sprintf("Some lines truncated to %d chars. Use read tool to see full lines", GrepMaxLineLength))
		details.LinesTruncated = true
	}
	if len(notices) > 0 {
		output += "\n\n[" + strings.Join(notices, ". ") + "]"
	}
	output = sharedOutput(details.Truncation, output)
	result := agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: output}}}
	if len(notices) > 0 {
		result.Details = details
	}
	return result, nil
}

func grepError(message string) agent.AgentToolResult {
	return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: message}}, Details: map[string]any{}, IsError: true, Thrown: true}
}

// ripgrepRun is the outcome of one rg process.
type ripgrepRun struct {
	killedDueToLimit bool
	exitCode         int
	stderr           string
}

// runRipgrep streams rg's JSON lines to handle. Lines are read without a length cap and the pipe is always drained, so a huge match
// line can neither stall rg nor drop later events; when handle returns true rg is killed and the rest of its output discarded.
func runRipgrep(ctx context.Context, rgPath, dir string, args []string, handle func(line []byte) (finish bool)) (ripgrepRun, error) {
	//nolint:gosec // G204 (class b): program is rg resolved by PiG's tools manager; args are fixed flags, then -- and the pattern and search path from the grep tool call, so the model cannot reach an rg option.
	cmd := exec.CommandContext(ctx, rgPath, args...)
	// Upstream's spawn has no cwd option for a file search, so on Windows libuv finds rg from
	// PiG's working directory: the lookup precedes cmd.Dir.
	nodespawn.SetProgram(cmd)
	cmd.Dir = dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return ripgrepRun{}, err
	}
	if err := cmd.Start(); err != nil {
		return ripgrepRun{}, err
	}
	var run ripgrepRun
	reader := bufio.NewReader(stdout)
	var long []byte
	for {
		line, readErr := reader.ReadSlice('\n')
		// A line longer than the reader's buffer comes in pieces, joined in long.
		for errors.Is(readErr, bufio.ErrBufferFull) {
			long = append(long, line...)
			line, readErr = reader.ReadSlice('\n')
		}
		if len(long) > 0 {
			line = append(long, line...)
			long = line[:0]
		}
		if !run.killedDueToLimit && handle(line) {
			run.killedDueToLimit = true
			_ = cmd.Process.Kill()
		}
		if readErr != nil {
			break
		}
	}
	waitErr := cmd.Wait()
	run.stderr = stderr.String()
	if exitErr, ok := errors.AsType[*exec.ExitError](waitErr); ok {
		run.exitCode = exitErr.ExitCode()
	} else if waitErr != nil && !run.killedDueToLimit && ctx.Err() == nil {
		return run, waitErr
	}
	return run, nil
}

// isBlankLine is jsstring.Trim(line) == "": an event line starts with a brace, so only a line that does not is looked at.
func isBlankLine(line []byte) bool {
	return len(line) == 0 || line[0] != '{' && jsstring.Trim(string(line)) == ""
}

// grepFormatter formats the rows of a grep result (upstream formatPath and pushLine).
type grepFormatter struct {
	searchPath     string
	isDirectory    bool
	linesTruncated bool
	// relative is formatPath of relativeFor: a file's events run together, so it is formatted once per file.
	relativeFor []byte
	relative    string
	// row is the row being built.
	row []byte
}

func (f *grepFormatter) formatPath(filePath string) string {
	if f.isDirectory {
		abs := filePath
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(f.searchPath, abs)
		}
		if rel, err := filepath.Rel(f.searchPath, abs); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.Base(filePath)
}

// relativePath is formatPath(string(filePath)).
func (f *grepFormatter) relativePath(filePath []byte) string {
	if len(f.relativeFor) == 0 || !bytes.Equal(f.relativeFor, filePath) {
		f.relativeFor = append(f.relativeFor[:0], filePath...)
		f.relative = f.formatPath(string(filePath))
	}
	return f.relative
}

// appendTruncated appends the line to dst, cut to GrepMaxLineLength with the notice (upstream truncateLine), and records a cut.
func (f *grepFormatter) appendTruncated(dst, line []byte) []byte {
	if jsLengthBytes(line) <= GrepMaxLineLength {
		return append(dst, line...)
	}
	truncated := TruncateLine(string(line), GrepMaxLineLength)
	if truncated.WasTruncated {
		f.linesTruncated = true
	}
	return append(dst, truncated.Text...)
}

// stripLineTerminator is lineText.replace(/\r?\n?$/, ""): only the line terminator goes, a lone CR inside a line is content.
func stripLineTerminator(lineText []byte) []byte {
	switch {
	case bytes.HasSuffix(lineText, []byte("\r\n")):
		return lineText[:len(lineText)-2]
	case bytes.HasSuffix(lineText, []byte("\n")), bytes.HasSuffix(lineText, []byte("\r")):
		return lineText[:len(lineText)-1]
	}
	return lineText
}

// emit adds the row of one event to out and returns its length in bytes. Once out holds every byte its result can show, a row is only
// measured: its length and whether its text was cut are what the result and the notice still read. Neither path allocates per row.
func (f *grepFormatter) emit(e grepEvent, out *headJoin) int {
	relativePath := f.relativePath(e.path)
	sanitized := stripLineTerminator(e.lineText)
	separator := byte('-')
	if e.isMatch {
		separator = ':'
	}
	if out.full() && bytes.IndexByte(sanitized, '\n') == -1 {
		length := len(relativePath) + 1 + decimalBytes(e.lineNumber) + 2 + f.truncatedBytes(sanitized)
		out.AddLength(length)
		return length
	}
	f.row = append(f.row[:0], relativePath...)
	f.row = append(f.row, separator)
	f.row = strconv.AppendInt(f.row, int64(e.lineNumber), 10)
	f.row = append(f.row, separator, ' ')
	f.row = f.appendTruncated(f.row, sanitized)
	out.Add(f.row)
	return len(f.row)
}

// truncatedBytes is the length appendTruncated adds for line, recording a cut line as it does.
func (f *grepFormatter) truncatedBytes(line []byte) int {
	n, wasTruncated := truncatedLineBytes(line, GrepMaxLineLength)
	if wasTruncated {
		f.linesTruncated = true
	}
	return n
}

// GrepMaxLineLength matches upstream truncate.ts GREP_MAX_LINE_LENGTH.
const GrepMaxLineLength = GrepMaxLineLengthUpstream
