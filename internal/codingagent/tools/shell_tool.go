package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent/checks"
	"github.com/MichaelKinsy/PiG/internal/codingagent/filetracking"
	"github.com/MichaelKinsy/PiG/internal/codingagent/jobs"
	"github.com/MichaelKinsy/PiG/internal/codingagent/remote"
	"github.com/MichaelKinsy/PiG/internal/jsnumber"
	pitext "github.com/MichaelKinsy/PiG/internal/text"
)

// shellParams is the shell tool input (upstream bashSchema): a command and an
// optional timeout in seconds. Timeout is a pointer because upstream rejects
// an explicit zero or negative timeout while an omitted one means none.
type shellParams struct {
	Command    string   `json:"command"`
	Timeout    *float64 `json:"timeout"`
	Background *bool    `json:"background"`
}

// shellToolConfig mirrors upstream ShellToolConfig plus the options
// createShellToolDefinition receives.
type shellToolConfig struct {
	name           string
	shellName      string
	tempFilePrefix string
	// operations executes the command (upstream options.operations, default
	// createLocalShellOperations).
	operations BashOperations
	// commandPrefix is prepended with "\n" to every command.
	commandPrefix string
	// exposeSessionEnvironment mirrors upstream exposeSessionEnvironment.
	exposeSessionEnvironment bool
	// binDir is prepended to PATH (upstream getShellEnv's getBinDir).
	binDir string
	// spawnHook adjusts the command, working directory and environment before the shell runs (upstream options.spawnHook).
	spawnHook      BashSpawnHook
	backgroundJobs *jobs.Manager
	remote         *remote.Manager
	tracker        *filetracking.Tracker
	checks         *checks.BackgroundChecks
}

// BashSpawnContext is upstream's BashSpawnContext: what a shell tool runs. Env holds KEY=VALUE entries, as os/exec takes them, where upstream has a process environment object.
type BashSpawnContext struct {
	Command string
	CWD     string
	Env     []string
}

// BashSpawnHook is upstream's BashSpawnHook: it receives the context a shell tool is about to run and returns the context to run.
type BashSpawnHook func(context BashSpawnContext) BashSpawnContext

// PowerShellSpawnContext is upstream's PowerShellSpawnContext, an alias of BashSpawnContext (powershell.ts:24).
type PowerShellSpawnContext = BashSpawnContext

// PowerShellSpawnHook is upstream's PowerShellSpawnHook, an alias of BashSpawnHook (powershell.ts:25).
type PowerShellSpawnHook = BashSpawnHook

// shellToolSchema mirrors createShellToolDefinition's name, description,
// parameters, guidelines, and constrained sampling request.
func shellToolSchema(name, shellName string, exposeSessionEnvironment, borrow bool) ai.ToolSchema {
	var guidelines []string
	if exposeSessionEnvironment {
		guidelines = []string{sessionGuideline}
	}
	return toolSchemaWithParameters(ai.ToolSchema{
		Name:             name,
		PromptGuidelines: guidelines,
		Description: "Execute a " + shellName + " command in the current working directory. Returns stdout and stderr. " +
			"Output is truncated to last " + strconv.Itoa(DefaultMaxLinesUpstream) + " lines or " +
			strconv.Itoa(DefaultMaxBytesUpstream/1024) + "KB (whichever is hit first). " +
			"If truncated, full output is saved to a temp file. Optionally provide a timeout in seconds.",
		ConstrainedSampling: strictToolSampling(),
	}, `{"type":"object","required":["command"],"properties":{
		"command":{"type":"string","description":"Shell command to execute"},
		"timeout":{"type":"number","description":"Timeout in seconds (optional, no default timeout)"},
		"background":{"type":"boolean","description":"Run the command in the background and return its job ID immediately"}
	}}`, borrow)
}

// shellOutputSchema mirrors upstream bashOutputSchema (bash.ts:56-62): the result
// for programmatic callers such as codemode scripts. A non-zero exit code is
// an error result for the model, but scripts still resolve to this value.
// The key order is TypeBox's Type.Object serialization: type, required, properties.
const shellOutputSchema = `{"type":"object","required":["output","truncated","exit_code","wall_time_seconds"],"properties":{
	"output":{"type":"string","description":"Combined stdout and stderr, possibly truncated"},
	"truncated":{"type":"boolean"},
	"full_output_path":{"type":"string","description":"Full output, when truncated"},
	"exit_code":{"type":"number"},
	"wall_time_seconds":{"type":"number"}
}}`

// structuredOutputMaxBytes mirrors upstream STRUCTURED_OUTPUT_MAX_BYTES: the
// limit of structuredContent.output, which programmatic callers receive.
const structuredOutputMaxBytes = 1024 * 1024 // upstream: packages/coding-agent/src/core/tools/bash.ts:STRUCTURED_OUTPUT_MAX_BYTES

// roundedWallTimeSeconds mirrors upstream Math.round((performance.now() - startedAt) / 100) / 10:
// seconds to one decimal, halves rounding up.
func roundedWallTimeSeconds(elapsed time.Duration) float64 {
	return jsnumber.Round(float64(elapsed)/float64(time.Millisecond)/100) / 10
}

// shellStructuredContent mirrors upstream BashToolOutput; the field order is
// the order upstream builds the object in.
type shellStructuredContent struct {
	Output          string  `json:"output"`
	Truncated       bool    `json:"truncated"`
	FullOutputPath  string  `json:"full_output_path,omitempty"`
	ExitCode        int     `json:"exit_code"`
	WallTimeSeconds float64 `json:"wall_time_seconds"`
}

// strictToolSampling mirrors the constrainedSampling upstream's read, bash,
// powershell, edit and write definitions request: { type: "json_schema",
// strict: "prefer" }.
func strictToolSampling() *ai.ConstrainedSamplingConfig {
	return &ai.ConstrainedSamplingConfig{Type: ai.ConstrainedSamplingJSONSchema, Strict: ai.ConstrainedSamplingStrictPrefer}
}

// FormatJSNumber mirrors JavaScript String(number) for a finite number.
func FormatJSNumber(v float64) string { return jsNumber(v) }

// jsNumber mirrors JavaScript String(number) for the finite values a JSON
// timeout carries.
func jsNumber(v float64) string {
	if abs := math.Abs(v); abs != 0 && (abs < 1e-6 || abs >= 1e21) {
		mantissa, exp, _ := strings.Cut(strconv.FormatFloat(v, 'e', -1, 64), "e")
		return mantissa + "e" + exp[:1] + strings.TrimLeft(exp[1:], "0")
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// shellToolError is a failed shell tool call. Upstream throws (Thrown marks the result as that thrown error), and the agent
// loop turns the message into an error result with an empty details object.
func shellToolError(message string) agent.AgentToolResult {
	return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: message}}, Details: map[string]any{}, IsError: true, Thrown: true}
}

// appendShellStatus mirrors upstream appendStatus.
func appendShellStatus(text, status string) string {
	if text == "" {
		return status
	}
	return text + "\n\n" + status
}

// executeShellTool mirrors upstream createShellToolDefinition.execute: an
// initial empty update, then one operations.exec call whose raw output feeds
// an OutputAccumulator, with throttled streaming updates and upstream's
// abort, timeout, and exit-code statuses.
func executeShellTool(ctx context.Context, cwd string, cfg shellToolConfig, rawParams json.RawMessage, onUpdate agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	var p shellParams
	if err := json.Unmarshal(rawParams, &p); err != nil {
		return agent.AgentToolResult{}, fmt.Errorf("%s: invalid params: %w", cfg.name, err)
	}
	cwd, err := toolCWD(ctx, cwd)
	if err != nil {
		return agent.AgentToolResult{}, err
	}
	if cfg.name == "bash" && cfg.remote != nil && cfg.remote.Active() {
		lowerCmd := strings.ToLower(p.Command)
		if strings.Contains(lowerCmd, "sed -i") || strings.Contains(lowerCmd, "perl -i") {
			return shellToolError("Cannot run in-place file edit (`sed -i` / `perl -i`) over /remote; use the edit tool instead."), nil
		}
	}
	var watchedSnapshots []FileSnapshot
	if cfg.name == "bash" && (cfg.remote == nil || !cfg.remote.Active()) && (cfg.tracker != nil || cfg.checks != nil) {
		if writes := FileWritesInCommand(p.Command); len(writes) > 0 {
			watchedSnapshots = WatchFiles(cwd, writes)
		}
	}

	// Long-sleep guard and sleep-shortening for bash when BackgroundJobs is wired.
	sleepNote := ""
	effectiveRawCmd := p.Command
	if cfg.name == "bash" && cfg.backgroundJobs != nil && (p.Background == nil || !*p.Background) {
		if dur, sep, rest, ok := parseLeadingSleep(p.Command); ok {
			running := cfg.backgroundJobs.Running()
			if len(running) == 0 && sep == "" && dur >= 30*time.Second {
				return shellToolError("Long sleep blocked: no background jobs are running. Use background jobs with the wait tool, or run the next command directly."), nil
			}
			if len(running) > 0 {
				outcome := cfg.backgroundJobs.WaitForEnd(ctx, dur)
				if outcome == jobs.OutcomeAborted {
					return shellToolError("Command aborted"), nil
				}
				if outcome == jobs.OutcomeEnded {
					ended := cfg.backgroundJobs.TakeUnreported(jobs.ReportedBash)
					if len(ended) > 0 {
						sleepNote = jobs.FormatJobReport(ended)
					}
					if rest == "" {
						if sleepNote == "" {
							sleepNote = "Sleep shortened: background job finished."
						}
						return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: sleepNote}}}, nil
					}
					effectiveRawCmd = rest
				}
			}
		}
	}

	command := effectiveRawCmd
	if cfg.commandPrefix != "" {
		command = cfg.commandPrefix + "\n" + command
	}
	// upstream: bash.ts resolveSpawnContext runs the hook after the session variables are in the environment.
	spawn := BashSpawnContext{Command: command, CWD: cwd, Env: sessionEnvironment(ctx, cfg.exposeSessionEnvironment, cfg.binDir)}
	if cfg.spawnHook != nil {
		spawn = cfg.spawnHook(spawn)
	}
	command, cwd, env := spawn.Command, spawn.CWD, spawn.Env
	output := NewOutputAccumulator(cfg.tempFilePrefix)
	updates := startShellUpdates(ctx, output, onUpdate)
	var acceptingOutput atomic.Bool
	acceptingOutput.Store(true)
	finishOutput := func() OutputSnapshot {
		acceptingOutput.Store(false)
		output.Finish()
		if updates.started() {
			updates.finish()
		}
		snapshot := output.Snapshot(true)
		_ = output.CloseTempFile()
		return snapshot
	}

	startedAt := time.Now()

	// Backgroundable execution path when cfg.backgroundJobs != nil && cfg.name == "bash".
	if cfg.name == "bash" && cfg.backgroundJobs != nil {
		explicitBG := p.Background != nil && *p.Background
		bgCtx, bgCancel := context.WithCancel(context.Background())

		type execDone struct {
			result BashOperationsResult
			err    error
		}
		doneCh := make(chan execDone, 1)
		var detached atomic.Bool
		var handleMu sync.Mutex
		var handle *jobs.JobHandle

		registerBG := func() *jobs.JobHandle {
			handleMu.Lock()
			defer handleMu.Unlock()
			if handle != nil {
				return handle
			}
			logPath := output.EnsureBackgroundLog()
			handle = cfg.backgroundJobs.Add(jobs.JobSpec{
				Command:    effectiveRawCmd,
				CWD:        cwd,
				StartedAt:  startedAt,
				OutputFile: logPath,
				Output: func() string {
					return output.Snapshot(false).Content
				},
				Kill: func() {
					bgCancel()
				},
			}, time.Now())
			return handle
		}

		go func() {
			res, execErr := cfg.operations.Exec(bgCtx, command, cwd, BashOperationsExecOptions{
				OnData: func(data []byte) {
					if !acceptingOutput.Load() {
						return
					}
					if err := output.Append(data); err != nil {
						return
					}
					if !detached.Load() && updates.ready(ctx) {
						updates.schedule()
					}
				},
				Timeout: p.Timeout,
				Env:     env,
			})
			if detached.Load() {
				snap := finishOutput()
				h := registerBG()
				status := jobs.StatusDone
				var codePtr *int
				if res.ExitCode != nil {
					c := *res.ExitCode
					codePtr = &c
					if c != 0 {
						status = jobs.StatusFailed
					}
				}
				if execErr != nil {
					msg := execErr.Error()
					switch {
					case msg == "aborted":
						status = jobs.StatusKilled
					case strings.HasPrefix(msg, "timeout:"):
						status = jobs.StatusTimedOut
					default:
						status = jobs.StatusFailed
					}
				}
				h.End(status, codePtr, snap.Content)
				bgCancel()
				return
			}
			doneCh <- execDone{result: res, err: execErr}
		}()

		if explicitBG {
			detached.Store(true)
			h := registerBG()
			msg := fmt.Sprintf("Background job %s started (`%s`). Continue with other work or call wait; you will be notified when it finishes.\n\n[Full output: %s]", h.Job.ID, effectiveRawCmd, h.Job.OutputFile)
			return agent.AgentToolResult{
				Content: []ai.ToolResultMessageContent{ai.TextContent{Text: msg}},
				Details: map[string]any{"jobId": h.Job.ID, "outputFile": h.Job.OutputFile},
			}, nil
		}

		bgTimer := time.NewTimer(backgroundThreshold())
		defer bgTimer.Stop()

		select {
		case done := <-doneCh:
			bgCancel()
			res, fErr := finishForegroundShell(startedAt, done.result, done.err, output, finishOutput, sleepNote)
			return finalizeForegroundShell(command, res, fErr, cwd, watchedSnapshots, cfg.tracker, cfg.checks)
		case <-bgTimer.C:
			detached.Store(true)
			h := registerBG()
			msg := fmt.Sprintf("Command moved to background as job %s (`%s`). Continue with other work or call wait; you will be notified when it finishes.\n\n[Full output: %s]", h.Job.ID, effectiveRawCmd, h.Job.OutputFile)
			return agent.AgentToolResult{
				Content: []ai.ToolResultMessageContent{ai.TextContent{Text: msg}},
				Details: map[string]any{"jobId": h.Job.ID, "outputFile": h.Job.OutputFile},
			}, nil
		case <-ctx.Done():
			bgCancel()
			done := <-doneCh
			res, fErr := finishForegroundShell(startedAt, done.result, done.err, output, finishOutput, sleepNote)
			return finalizeForegroundShell(command, res, fErr, cwd, watchedSnapshots, cfg.tracker, cfg.checks)
		}
	}

	result, err := cfg.operations.Exec(ctx, command, cwd, BashOperationsExecOptions{
		OnData: func(data []byte) {
			if !acceptingOutput.Load() {
				return
			}
			if err := output.Append(data); err != nil {
				return
			}
			if updates.ready(ctx) {
				updates.schedule()
			}
		},
		Timeout: p.Timeout,
		Env:     env,
	})
	res, fErr := finishForegroundShell(startedAt, result, err, output, finishOutput, sleepNote)
	return finalizeForegroundShell(command, res, fErr, cwd, watchedSnapshots, cfg.tracker, cfg.checks)
}

func isReadOnlyShellCommand(command string) bool {
	cmd := strings.TrimSpace(command)
	if cmd == "" || strings.ContainsAny(cmd, ">;<&\n|`") || strings.Contains(cmd, "$(") {
		return false
	}
	for _, prefix := range []string{
		"ls", "cat ", "head ", "tail ", "wc ", "grep ", "rg ", "find ",
		"pwd", "which ", "echo ", "env", "printenv", "uname", "whoami",
		"git status", "git diff", "git log", "git show", "git branch", "git rev-parse",
		"go version", "go list", "go env",
	} {
		if cmd == strings.TrimSpace(prefix) || strings.HasPrefix(cmd, prefix) {
			return true
		}
	}
	return false
}

func finalizeForegroundShell(command string, res agent.AgentToolResult, err error, cwd string, snapshots []FileSnapshot, tracker *filetracking.Tracker, bgChecks *checks.BackgroundChecks) (agent.AgentToolResult, error) {
	if cwd != "" && !isReadOnlyShellCommand(command) {
		InvalidateSearchCache(cwd)
	}
	if len(snapshots) > 0 {
		changes := CheckChangedFiles(snapshots, tracker)
		if len(changes) > 0 {
			var sb strings.Builder
			for _, ch := range changes {
				if bgChecks != nil {
					bgChecks.OnEdited(ch.AbsolutePath)
				}
				fmt.Fprintf(&sb, "\n[Updated %s lines %d-%d]", ch.Target, ch.StartLine, ch.EndLine)
				if data, rErr := os.ReadFile(ch.AbsolutePath); rErr == nil {
					lines := strings.Split(normalizeToLF(string(data)), "\n")
					start := max(1, ch.StartLine)
					end := min(len(lines), ch.EndLine)
					if end-start > 30 {
						end = start + 30
					}
					for ln := start; ln <= end; ln++ {
						fmt.Fprintf(&sb, "\n%d: %s", ln, lines[ln-1])
					}
				}
			}
			if len(res.Content) > 0 {
				if tc, ok := res.Content[0].(ai.TextContent); ok {
					tc.Text += sb.String()
					res.Content[0] = tc
				}
			} else {
				res.Content = []ai.ToolResultMessageContent{ai.TextContent{Text: strings.TrimPrefix(sb.String(), "\n")}}
			}
		}
	}
	return res, err
}

func finishForegroundShell(startedAt time.Time, result BashOperationsResult, err error, output *OutputAccumulator, finishOutput func() OutputSnapshot, sleepNote string) (agent.AgentToolResult, error) {
	if err != nil {
		snapshot := finishOutput()
		text, _ := formatShellOutput(snapshot, output.LastLineBytes(), "")
		switch msg := err.Error(); {
		case msg == "aborted":
			return shellToolError(appendShellStatus(text, "Command aborted")), nil
		case strings.HasPrefix(msg, "timeout:"):
			status := "Command timed out after " + strings.TrimPrefix(msg, "timeout:") + " seconds"
			if timeoutErr, ok := errors.AsType[*ShellTimeoutError](err); ok && timeoutErr.ShellExit != nil {
				status += ". The shell exited with code " + strconv.Itoa(timeoutErr.ShellExit.ExitCode) + " after " + jsNumber(timeoutErr.ShellExit.AfterSeconds) + " seconds, but background processes it started kept writing output until the timeout."
			}
			return shellToolError(appendShellStatus(text, status)), nil
		default:
			return shellToolError(msg), nil
		}
	}
	snapshot := finishOutput()
	text, details := formatShellOutput(snapshot, output.LastLineBytes(), "(no output)")
	if result.ExitCode == nil {
		return shellToolError(appendShellStatus(text, "Command terminated without an exit code")), nil
	}
	wallTimeSeconds := roundedWallTimeSeconds(time.Since(startedAt))
	fullOutput, err := output.ReadFullOutput(structuredOutputMaxBytes)
	if err != nil {
		if pathError, ok := errors.AsType[*os.PathError](err); ok {
			return shellToolError(NodeFSError(pathError.Err, pathError.Op, pathError.Path)), nil
		}
		return shellToolError(err.Error()), nil
	}
	// Apply head+tail truncation so both the first 40 lines and the tail are preserved when output is long.
	if snapshot.Truncation.Truncated && fullOutput.Content != "" {
		if ht, ok, totalLines := truncateHeadAndTail(fullOutput.Content, microBashHeadLines, microBashMaxLines, microBashMaxBytes); ok {
			text = ht + fmt.Sprintf("\n\n[Showing head+tail of %d lines. Full output: %s]", totalLines, snapshot.FullOutputPath)
		}
	}
	if text != "" && text != "(no output)" {
		text = pitext.StripANSIString(text)
	}
	if sleepNote != "" {
		if text == "(no output)" {
			text = sleepNote
		} else {
			text = sleepNote + "\n\n" + text
		}
	}
	structured := shellStructuredContent{
		Output:          fullOutput.Content,
		Truncated:       fullOutput.Truncated,
		ExitCode:        *result.ExitCode,
		WallTimeSeconds: wallTimeSeconds,
	}
	if fullOutput.Truncated {
		structured.FullOutputPath = snapshot.FullOutputPath
	}
	structuredContent, err := json.Marshal(structured)
	if err != nil {
		return agent.AgentToolResult{}, err
	}
	var resultDetails any
	if details != nil {
		resultDetails = details
	}
	if *result.ExitCode != 0 {
		return agent.AgentToolResult{
			Content:           []ai.ToolResultMessageContent{ai.TextContent{Text: appendShellStatus(text, fmt.Sprintf("Command exited with code %d", *result.ExitCode))}},
			Details:           resultDetails,
			StructuredContent: structuredContent,
			IsError:           true,
		}, nil
	}
	return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: text}}, Details: resultDetails, StructuredContent: structuredContent}, nil
}

// formatShellOutput mirrors upstream formatOutput: the output (or emptyText),
// plus the truncation notice naming the full-output file when truncated.
func formatShellOutput(snapshot OutputSnapshot, lastLineBytes int, emptyText string) (string, *BashDetails) {
	text := snapshot.Content
	if text == "" {
		text = emptyText
	}
	tr := snapshot.Truncation
	if !tr.Truncated {
		return sharedOutput(nil, text), nil
	}
	details := &BashDetails{Truncation: &tr, FullOutputPath: snapshot.FullOutputPath}
	startLine := tr.TotalLines - tr.OutputLines + 1
	endLine := tr.TotalLines
	switch {
	case tr.LastLinePartial:
		text += fmt.Sprintf("\n\n[Showing last %s of line %d (line is %s). Full output: %s]",
			FormatSize(tr.OutputBytes), endLine, FormatSize(lastLineBytes), snapshot.FullOutputPath)
	case tr.TruncatedBy == "lines":
		text += fmt.Sprintf("\n\n[Showing lines %d-%d of %d. Full output: %s]",
			startLine, endLine, tr.TotalLines, snapshot.FullOutputPath)
	default:
		text += fmt.Sprintf("\n\n[Showing lines %d-%d of %d (%s limit). Full output: %s]",
			startLine, endLine, tr.TotalLines, FormatSize(DefaultMaxBytesUpstream), snapshot.FullOutputPath)
	}
	shareTruncatedContent(details.Truncation, text)
	return text, details
}

// shellUpdates starts the update scheduler once something can see the updates (agent.ToolUpdatesObserved): at the
// start of the command, with upstream's initial empty update, or when output arrives after an observer appeared.
type shellUpdates struct {
	output   *OutputAccumulator
	onUpdate agent.ToolUpdateCallback

	mu        sync.Mutex
	scheduler *shellUpdateScheduler
}

func startShellUpdates(ctx context.Context, output *OutputAccumulator, onUpdate agent.ToolUpdateCallback) *shellUpdates {
	u := &shellUpdates{output: output, onUpdate: onUpdate}
	if agent.ToolUpdatesObserved(ctx, onUpdate) {
		u.scheduler = newShellUpdateScheduler(output, onUpdate)
		// The initial empty update creates the streaming card before any
		// output arrives. upstream: bash.ts:321-323
		onUpdate(agent.AgentToolResult{Content: []ai.ToolResultMessageContent{}})
	}
	return u
}

// ready reports whether output should schedule an update, starting the scheduler when an observer appeared since the
// command started. An observer that was not there for the initial update does not get it, as a Pi listener added
// later does not.
func (u *shellUpdates) ready(ctx context.Context) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.scheduler == nil && u.onUpdate != nil && agent.ToolUpdatesObserved(ctx, u.onUpdate) {
		u.scheduler = newShellUpdateScheduler(u.output, u.onUpdate)
	}
	return u.scheduler != nil
}

func (u *shellUpdates) schedule() {
	u.mu.Lock()
	scheduler := u.scheduler
	u.mu.Unlock()
	scheduler.schedule()
}

func (u *shellUpdates) started() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.scheduler != nil
}

func (u *shellUpdates) finish() {
	u.mu.Lock()
	scheduler := u.scheduler
	u.mu.Unlock()
	scheduler.finish()
}
