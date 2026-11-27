package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
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
	// local reports that the command runs on this machine (microPi's `options.operations === undefined`, or the remote
	// operations while no remote machine is connected).
	local bool
	// commandPrefix is prepended with "\n" to every command.
	commandPrefix string
	// exposeSessionEnvironment mirrors upstream exposeSessionEnvironment.
	exposeSessionEnvironment bool
	// binDir is prepended to PATH (upstream getShellEnv's getBinDir).
	binDir string
	// spawnHook adjusts the command, working directory and environment before the shell runs (upstream options.spawnHook).
	spawnHook      BashSpawnHook
	backgroundJobs *jobs.Manager
	// autoBackgroundSeconds is microPi's autoBackgroundSeconds: after how long a command goes on in the background
	// when backgroundJobs is set; 0 only when the call asks.
	autoBackgroundSeconds float64
	// editToolActive is microPi's editToolActive: while it reports true, a command that changes a file as edit or
	// write would is watched here and refused on a remote machine (bash-file-writes.ts). Nil is false.
	editToolActive func() bool
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

// bashBackgroundGuideline is microPi's second bash prompt guideline (bashToolSystemPromptContribution): from
// deepseek-harness and oh-my-pi. In Pi's sessions, 97 `sleep` calls waited 3.2 h for other commands.
const bashBackgroundGuideline = "Commands still running after two minutes continue in the background and report when they end: do other work meanwhile, never sleep or poll."

// shellToolSchema mirrors createShellToolDefinition's name, description, parameters, guidelines, and constrained
// sampling request. µPi: the description names the kept head (microPi's BASH_HEAD_LINES) and, where commands go on in
// the background (autoBackgroundSeconds > 0 with background jobs), after how long.
func shellToolSchema(name, shellName string, guidelines []string, autoBackgroundSeconds float64, borrow bool) ai.ToolSchema {
	description := "Execute a " + shellName + " command in the current working directory. Returns stdout and stderr; long output keeps its first " +
		strconv.Itoa(shellHeadLines) + " lines and its end, the full output is saved to a file. Optional timeout in seconds."
	if autoBackgroundSeconds > 0 {
		description += " Commands running over " + jsNumber(autoBackgroundSeconds) + " s continue in the background and report their end."
	}
	return toolSchemaWithParameters(ai.ToolSchema{
		Name:                name,
		PromptGuidelines:    guidelines,
		Description:         description,
		ConstrainedSampling: strictToolSampling(),
	}, `{"type":"object","required":["command"],"properties":{
		"command":{"type":"string","description":"Shell command to execute"},
		"timeout":{"type":"number","description":"Seconds to wait for the command (optional, no default). Where background jobs are, a command still running then goes on in the background and reports its end; otherwise it is ended"},
		"background":{"type":"boolean","description":"Start in the background at once; a message reports its end"}
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

// MovedToBackgroundNote is microPi's MOVED_TO_BACKGROUND_NOTE: words every result of the bash tool that moved its
// command to the background carries, whatever started the move.
const MovedToBackgroundNote = "moved to the background as job"

// backgroundDoneTailChars is microPi's BACKGROUND_DONE_TAIL_CHARS: how much of a background job's output its report
// carries.
const backgroundDoneTailChars = 2048

// backgroundAfter is microPi's backgroundAfterMs: after how long a call's command goes to the background: 0 when
// asked, ok=false when it never does (no jobs, or turned off). A timeout before the time to background it moves it
// then, rather than ending it: in Terminal-Bench 7 of 8 timeouts ended a loop waiting for a build (915 s of waiting
// lost), and in Pi's sessions 16 timeouts ended builds and runs after 7 minutes on average.
func backgroundAfter(backgroundJobs *jobs.Manager, background bool, timeout *float64, autoBackgroundSeconds float64) (seconds float64, ok bool) {
	if backgroundJobs == nil {
		return 0, false
	}
	if background {
		return 0, true
	}
	if autoBackgroundSeconds <= 0 {
		return 0, false
	}
	if timeout != nil && *timeout > 0 {
		return math.Min(*timeout, autoBackgroundSeconds), true
	}
	return autoBackgroundSeconds, true
}

// refuseOrWatch is microPi's refuseOrWatch: the checks before a command runs: a fixed sleep that would only go on in
// the background, a command that changes files as edit and write would on a remote machine, and a pkill that would end
// this process. It returns the refusal, or the files the command writes as edit and write would (watched when it runs
// here, see CheckChangedFiles).
func refuseOrWatch(command string, cfg shellToolConfig, background bool) ([]FileWrite, string) {
	if cfg.backgroundJobs != nil && !background {
		// From oh-my-pi's "NEVER poll": a sleep that would only go on in the background.
		if sleep := fixedSleepSeconds(command); sleep > math.Max(maxFixedSleepSeconds, cfg.autoBackgroundSeconds) {
			return nil, fixedSleepRefusal(sleep, cfg.autoBackgroundSeconds)
		}
	}
	// From oh-my-pi's bash interceptor. On this machine the command runs and gets the edit tool's checks afterwards
	// (see CheckChangedFiles); on a remote machine what changed is not known here: such a command is refused.
	var writes []FileWrite
	if cfg.editToolActive != nil && cfg.editToolActive() {
		writes = FileWritesInCommand(command)
	}
	if !cfg.local && len(writes) > 0 {
		return nil, FileWriteRefusal(writes)
	}
	// On this machine only: a remote pkill does not reach this process.
	if cfg.local {
		if pattern, ok := selfKillPattern(command); ok {
			return nil, selfKillRefusal(pattern)
		}
	}
	return writes, ""
}

// sleepUntilJobEnds is microPi's sleepUntilJobEnds: it sleeps up to seconds for a command, and less when a background
// job ends (as wait does): what a fixed sleep before a look at a job's progress waits for. It returns the note to put
// before the command's output: how long it slept, and the report of the jobs that ended, which the call gives rather
// than a message after it.
func sleepUntilJobEnds(ctx context.Context, backgroundJobs *jobs.Manager, seconds float64) (string, error) {
	start := time.Now()
	outcome := backgroundJobs.WaitForEnd(ctx, time.Duration(seconds*float64(time.Second)))
	if outcome == jobs.OutcomeAborted {
		return "", errors.New("Command aborted")
	}
	ended := backgroundJobs.TakeUnreported(jobs.ReportedBash)
	if len(ended) == 0 {
		return "", nil
	}
	slept := strconv.FormatFloat(time.Since(start).Seconds(), 'f', 1, 64)
	return "[The sleep of " + jsNumber(seconds) + " s ended after " + slept + " s: a background job ended.]\n" + jobs.FormatJobReport(ended) + "\n\n", nil
}

// expandHome is microPi's expandHome for the paths of a leading `cd` and the files cat printed.
func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return home + path[1:]
		}
	}
	return path
}

// markCatFilesRead is microPi's markCatFilesRead: files a command printed whole with `cat`: the model saw them, so
// editing or rewriting them needs no extra read.
func markCatFilesRead(command, cwd string, tracker *filetracking.Tracker) {
	dir, files := FilesShownByCat(command)
	base := expandHome(dir)
	if !filepath.IsAbs(base) {
		base = filepath.Join(cwd, base)
	}
	for _, file := range files {
		path := expandHome(file)
		if !filepath.IsAbs(path) {
			path = filepath.Join(base, path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		tracker.OnFileRead(path, string(data), float64(info.ModTime().UnixNano())/1e6, cwd, data, true)
	}
}

// lastChars is the end of text, at most n characters (JavaScript's text.slice(-n), by code points here).
func lastChars(text string, n int) string {
	if len(text) <= n {
		return text
	}
	runes := []rune(text)
	if len(runes) <= n {
		return text
	}
	return string(runes[len(runes)-n:])
}

// executeShellTool mirrors upstream createShellToolDefinition.execute: an
// initial empty update, then one operations.exec call whose raw output feeds
// an OutputAccumulator, with throttled streaming updates and upstream's
// abort, timeout, and exit-code statuses. µPi: with background jobs, a command
// still running after a while (or at once, when the call asks) goes on in the
// background as a job.
func executeShellTool(ctx context.Context, cwd string, cfg shellToolConfig, rawParams json.RawMessage, onUpdate agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	var p shellParams
	if err := json.Unmarshal(rawParams, &p); err != nil {
		return agent.AgentToolResult{}, fmt.Errorf("%s: invalid params: %w", cfg.name, err)
	}
	cwd, err := toolCWD(ctx, cwd)
	if err != nil {
		return agent.AgentToolResult{}, err
	}
	background := p.Background != nil && *p.Background
	command := p.Command
	writes, refusal := refuseOrWatch(command, cfg, background)
	if refusal != "" {
		return shellToolError(refusal), nil
	}
	// A command that sleeps a while before looking at a job's progress: the tool sleeps for it, and stops when a job
	// ends. Only after the refusals above: a command refused after its sleep lost the sleep.
	sleptNote := ""
	if cfg.backgroundJobs != nil && !background && len(cfg.backgroundJobs.Running()) > 0 {
		if seconds, rest, ok := leadingSleep(command); ok && seconds > maxFixedSleepSeconds && seconds <= cfg.autoBackgroundSeconds {
			note, err := sleepUntilJobEnds(ctx, cfg.backgroundJobs, seconds)
			if err != nil {
				return shellToolError(err.Error()), nil
			}
			sleptNote = note
			command = rest
			if command == "" {
				command = "true"
			}
		}
	}

	fullCommand := command
	if cfg.commandPrefix != "" {
		fullCommand = cfg.commandPrefix + "\n" + command
	}
	// upstream: bash.ts resolveSpawnContext runs the hook after the session variables are in the environment.
	spawn := BashSpawnContext{Command: fullCommand, CWD: cwd, Env: sessionEnvironment(ctx, cfg.exposeSessionEnvironment, cfg.binDir)}
	if cfg.spawnHook != nil {
		spawn = cfg.spawnHook(spawn)
	}
	fullCommand, cwd = spawn.Command, spawn.CWD
	output := newShellOutputAccumulator(cfg.tempFilePrefix)
	updates := startShellUpdates(ctx, output, onUpdate)
	var acceptingOutput atomic.Bool
	acceptingOutput.Store(true)
	onData := func(data []byte) {
		if !acceptingOutput.Load() {
			return
		}
		if err := output.Append(data); err != nil {
			return
		}
		if updates.ready(ctx) {
			updates.schedule()
		}
	}
	if sleptNote != "" {
		onData([]byte(sleptNote))
	}
	finishOutput := func() OutputSnapshot {
		acceptingOutput.Store(false)
		output.Finish()
		updates.finish()
		snapshot := output.Snapshot(true)
		_ = output.CloseTempFile()
		return snapshot
	}
	var watched []FileSnapshot
	if cfg.local {
		watched = WatchFiles(cwd, writes)
	}
	finish := func(result BashOperationsResult, err error, startedAt time.Time) (agent.AgentToolResult, error) {
		res, fErr := finishForegroundShell(startedAt, result, err, output, finishOutput)
		return finalizeForegroundShell(command, fullCommand, res, fErr, cwd, watched, cfg)
	}

	startedAt := time.Now()
	after, backgroundable := backgroundAfter(cfg.backgroundJobs, background, p.Timeout, cfg.autoBackgroundSeconds)
	if !backgroundable {
		result, err := cfg.operations.Exec(ctx, fullCommand, cwd, BashOperationsExecOptions{OnData: onData, Timeout: p.Timeout, Env: spawn.Env})
		return finish(result, err, startedAt)
	}

	// The command's own abort: the tool call's, until the command goes to the background.
	commandCtx, abortCommand := context.WithCancel(context.Background())
	// A command the call starts in the background keeps its timeout; another one goes there at it.
	execTimeout := p.Timeout
	if !background {
		execTimeout = nil
	}
	doneCh := make(chan shellExecDone, 1)
	// The command goes to the background only once its process exists: it has to be known to end it.
	spawned := make(chan struct{})
	var spawnOnce sync.Once
	var pid atomic.Int64
	go func() {
		result, err := cfg.operations.Exec(commandCtx, fullCommand, cwd, BashOperationsExecOptions{
			OnData:  onData,
			Timeout: execTimeout,
			Env:     spawn.Env,
			OnSpawn: func(processID int) {
				pid.Store(int64(processID))
				spawnOnce.Do(func() { close(spawned) })
			},
		})
		doneCh <- shellExecDone{result: result, err: err}
	}()

	var timer <-chan time.Time
	spawnedCh := (<-chan struct{})(spawned)
	for {
		select {
		case done := <-doneCh:
			abortCommand()
			return finish(done.result, done.err, startedAt)
		case <-ctx.Done():
			abortCommand()
			done := <-doneCh
			return finish(done.result, done.err, startedAt)
		case <-spawnedCh:
			spawnedCh = nil
			t := time.NewTimer(time.Duration(after * float64(time.Second)))
			defer t.Stop()
			timer = t.C
		case <-timer:
			return moveToBackground(moveToBackgroundParams{
				cfg: cfg, command: command, cwd: cwd, pid: int(pid.Load()), done: doneCh, output: output,
				updates: updates, acceptingOutput: &acceptingOutput, startedAt: startedAt, kill: abortCommand,
				background: background, timeout: p.Timeout, after: after,
			}), nil
		}
	}
}

// shellExecDone is how a command's operations.Exec call ended.
type shellExecDone struct {
	result BashOperationsResult
	err    error
}

type moveToBackgroundParams struct {
	cfg             shellToolConfig
	command         string
	cwd             string
	pid             int
	done            <-chan shellExecDone
	output          *OutputAccumulator
	updates         *shellUpdates
	acceptingOutput *atomic.Bool
	startedAt       time.Time
	kill            func()
	background      bool
	timeout         *float64
	after           float64
}

// moveToBackground is microPi's moveToBackground: a command still running at its time to go to the background is kept
// running as a job, whose end is recorded when it ends. The call's result: the output so far, and where the rest goes.
func moveToBackground(p moveToBackgroundParams) agent.AgentToolResult {
	p.updates.stop()
	file := p.output.EnsureBackgroundLog()
	text, details := formatShellOutput(p.output.Snapshot(true), p.output.LastLineBytes(), "")
	output := p.output
	handle := p.cfg.backgroundJobs.Add(jobs.JobSpec{
		Command:    p.command,
		CWD:        p.cwd,
		PID:        p.pid,
		StartedAt:  p.startedAt,
		OutputFile: file,
		Output:     func() string { return output.Snapshot(false).Content },
		Kill:       p.kill,
	}, time.Now())
	go func() {
		done := <-p.done
		status := jobs.StatusFailed
		var code *int
		if done.err != nil {
			if strings.HasPrefix(done.err.Error(), "timeout:") {
				status = jobs.StatusTimedOut
			}
		} else if done.result.ExitCode != nil {
			c := *done.result.ExitCode
			code = &c
			if c == 0 {
				status = jobs.StatusDone
			}
		}
		p.kill()
		InvalidateSearchCache(p.cwd)
		p.acceptingOutput.Store(false)
		output.Finish()
		snapshot := output.Snapshot(true)
		_ = output.CloseTempFile()
		handle.End(status, code, lastChars(snapshot.Content, backgroundDoneTailChars))
	}()
	id := handle.Job.ID
	stop := ""
	if p.pid > 0 {
		how := "kill -- -" + strconv.Itoa(p.pid)
		if runtime.GOOS == "windows" {
			how = "taskkill /T /F /PID " + strconv.Itoa(p.pid)
		}
		stop = " (pid " + strconv.Itoa(p.pid) + "; stop it with: " + how + ")"
	}
	atTimeout := !p.background && p.timeout != nil && p.after == *p.timeout
	var status string
	switch {
	case p.background:
		status = "Started in the background as job " + id + stop + "."
	case atTimeout:
		status = "Command still running at its timeout of " + jsNumber(*p.timeout) + " seconds: " + MovedToBackgroundNote + " " + id + stop + ", not stopped."
	default:
		status = "Command still running after " + jsNumber(p.after) + " seconds: " + MovedToBackgroundNote + " " + id + stop + "."
	}
	goesOn, into, stillEnds := "on ", file, ""
	if p.background {
		goesOn = ""
		if p.timeout != nil {
			stillEnds = "; its timeout of " + jsNumber(*p.timeout) + " seconds still ends it"
		}
	}
	if into == "" {
		into = "memory"
	}
	text = appendShellStatus(text, status+" Its output goes "+goesOn+"into "+into+stillEnds+". "+
		"A message reports when it ends, with its exit code and the end of its output: go on with other work, and call wait only when nothing else is left.")
	result := agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: text}}}
	if details != nil {
		result.Details = details
	}
	return result
}

// finalizeForegroundShell is what microPi's execute does after the command ended in the foreground: the search
// caches learn of the change, a watched file the command changed is reported and taken as read and edited, and files
// a successful command printed whole with `cat` count as read.
func finalizeForegroundShell(command, fullCommand string, res agent.AgentToolResult, err error, cwd string, snapshots []FileSnapshot, cfg shellToolConfig) (agent.AgentToolResult, error) {
	if cwd != "" && !IsReadOnlyCommand(fullCommand) {
		InvalidateSearchCache(cwd)
	}
	if err != nil || res.Thrown {
		return res, err
	}
	if changes := CheckChangedFiles(snapshots, cfg.tracker); len(changes) > 0 {
		if cfg.checks != nil {
			for _, ch := range changes {
				cfg.checks.OnEdited(ch.AbsolutePath)
			}
		}
		if len(res.Content) > 0 {
			if tc, ok := res.Content[0].(ai.TextContent); ok {
				tc.Text += FormatChangedFiles(changes)
				res.Content[0] = tc
			}
		}
	}
	if cfg.tracker != nil && !res.IsError {
		if details, ok := res.Details.(*BashDetails); !ok || details == nil || details.Truncation == nil {
			markCatFilesRead(command, cwd, cfg.tracker)
		}
	}
	return res, nil
}

func finishForegroundShell(startedAt time.Time, result BashOperationsResult, err error, output *OutputAccumulator, finishOutput func() OutputSnapshot) (agent.AgentToolResult, error) {
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
	// µPi: the kept head is named before the tail (microPi bash.ts CallOutput.format).
	startLine := tr.TotalLines - (tr.OutputLines - snapshot.HeadLines) + 1
	endLine := tr.TotalLines
	head, lines := "", "lines "
	if snapshot.HeadLines > 0 {
		head = fmt.Sprintf("lines 1-%d and ", snapshot.HeadLines)
		lines = head
	}
	switch {
	case tr.LastLinePartial:
		text += fmt.Sprintf("\n\n[Showing %sthe last %s of line %d (line is %s). Full output: %s]",
			head, FormatSize(tr.OutputBytes), endLine, FormatSize(lastLineBytes), snapshot.FullOutputPath)
	case tr.TruncatedBy == "lines":
		text += fmt.Sprintf("\n\n[Showing %s%d-%d of %d. Full output: %s]",
			lines, startLine, endLine, tr.TotalLines, snapshot.FullOutputPath)
	default:
		text += fmt.Sprintf("\n\n[Showing %s%d-%d of %d (%s limit). Full output: %s]",
			lines, startLine, endLine, tr.TotalLines, FormatSize(tr.MaxBytes), snapshot.FullOutputPath)
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
	// stopped: the call returned (its command went on in the background): no more updates for it.
	stopped bool
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
	if u.stopped {
		return false
	}
	if u.scheduler == nil && u.onUpdate != nil && agent.ToolUpdatesObserved(ctx, u.onUpdate) {
		u.scheduler = newShellUpdateScheduler(u.output, u.onUpdate)
	}
	return u.scheduler != nil
}

func (u *shellUpdates) schedule() {
	u.mu.Lock()
	scheduler := u.scheduler
	stopped := u.stopped
	u.mu.Unlock()
	if scheduler != nil && !stopped {
		scheduler.schedule()
	}
}

// finish sends the last update, if the updates were not stopped.
func (u *shellUpdates) finish() {
	u.mu.Lock()
	scheduler := u.scheduler
	stopped := u.stopped
	u.stopped = true
	u.mu.Unlock()
	if scheduler != nil && !stopped {
		scheduler.finish()
	}
}

// stop ends the updates without a last one (microPi's CallOutput.stopUpdates).
func (u *shellUpdates) stop() {
	u.mu.Lock()
	scheduler := u.scheduler
	stopped := u.stopped
	u.stopped = true
	u.mu.Unlock()
	if scheduler != nil && !stopped {
		close(scheduler.stop)
		<-scheduler.done
	}
}
