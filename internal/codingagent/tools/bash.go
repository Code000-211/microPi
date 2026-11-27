package tools

import (
	"context"
	"encoding/json"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent/checks"
	"github.com/MichaelKinsy/PiG/internal/codingagent/filetracking"
	"github.com/MichaelKinsy/PiG/internal/codingagent/jobs"
	"github.com/MichaelKinsy/PiG/internal/codingagent/remote"
)

// ─── Bash Tool ────────────────────────────────────────────────────────────────

// BashTool executes shell commands with local or caller-supplied operations.
// Ports packages/coding-agent/src/core/tools/bash.ts.
//
// Contract (mirrors upstream tools/bash.ts):
//   - One-shot spawn per call. cwd does NOT persist between calls.
//   - Resolved command = CommandPrefix + "\n" + command (when prefix set).
//   - Spawn via getShellConfig: settings shellPath, else /bin/bash, bash on
//     PATH, then sh (always with -c).
//   - Stream chunks: ANSI strip, binary sanitize, drop \r.
//   - Tempfile overflow at DEFAULT_MAX_BYTES (50 KB); rolling buffer 2x.
//   - On exit ≠ 0: append "Command exited with code N", return as IsError.
//     A signal-killed shell reports 128 + the signal number.
//   - On context cancel: append "Command aborted", return as IsError.
//   - Details: BashDetails{Truncation, FullOutputPath} on a truncated
//     success; error results carry none (upstream throws).
type BashTool struct {
	CWD string
	// Operations delegates command execution; nil selects local bash.
	Operations BashOperations
	// Settings is consulted via GetShellConfig to resolve shellPath. nil
	// falls through to the platform default.
	Settings SettingsView
	// CommandPrefix prepended (with \n) to every command. Empty = no prefix.
	CommandPrefix string
	// BinDir (<agentDir>/bin) is prepended to the command's PATH, mirroring
	// upstream getShellEnv. Empty leaves PATH unchanged.
	BinDir string
	// HideSessionEnvironment turns off upstream's exposeSessionEnvironment:
	// commands then see no PI_* session variables, and the prompt omits the
	// guideline that mentions them.
	HideSessionEnvironment bool
	// SpawnHook adjusts the command, working directory or environment before execution (upstream spawnHook). nil runs the command as built.
	SpawnHook BashSpawnHook
	// BackgroundJobs manages backgrounded bash commands and wakeup notifications.
	BackgroundJobs *jobs.Manager
	// Remote routes execution over SSH when active.
	Remote *remote.Manager
	// Tracker, given, takes the files a command printed whole with `cat` as read, and the files it changed as edit
	// or write would as read and edited (µPi: microPi's fileTracker option).
	Tracker *filetracking.Tracker
	Checks  *checks.BackgroundChecks
	// EditToolActive reports whether the edit tool is there: then a command that changes a file as edit or write
	// would (`sed -i`, text redirected into a file) is watched here and refused on a remote machine
	// (bash_file_writes.go). Nil is false. µPi: microPi's editToolActive option.
	EditToolActive func() bool
}

func (t *BashTool) Name() string  { return "bash" }
func (t *BashTool) Label() string { return "" }

// OutputSchema mirrors upstream bashOutputSchema: the structuredContent of a
// completed command.
func (t *BashTool) OutputSchema() json.RawMessage { return json.RawMessage(shellOutputSchema) }

func (t *BashTool) Schema() ai.ToolSchema { return t.schema(false) }

// BorrowedSchema is Schema with the parameters shared, not copied: the caller reads them and never modifies them.
func (t *BashTool) BorrowedSchema() ai.ToolSchema { return t.schema(true) }

func (t *BashTool) schema(borrow bool) ai.ToolSchema {
	var guidelines []string
	if !t.HideSessionEnvironment {
		// µPi: microPi's bashToolSystemPromptContribution.guidelines.
		guidelines = []string{sessionGuideline, bashBackgroundGuideline}
	}
	return shellToolSchema("bash", "bash", guidelines, t.autoBackgroundSeconds(), borrow)
}

// autoBackgroundSeconds is after how long a command goes on in the background: 0 without background jobs.
func (t *BashTool) autoBackgroundSeconds() float64 {
	if t.BackgroundJobs == nil {
		return 0
	}
	return autoBackgroundSeconds()
}

// PrepareArguments implements agent.ArgumentPreparer: Claude Code's run_in_background and Qwen Code's is_background
// are background (µPi: microPi's prepareArguments, argument-aliases.ts).
func (t *BashTool) PrepareArguments(raw json.RawMessage) (json.RawMessage, error) {
	return NormalizeToolArguments(raw, BashAliases), nil
}

// µPi: sequential, as microPi's: a response's calls run in order when one of them changes files or runs a command, so an edit and the
// command that tests it can come in one response (the system prompt's rule).
func (t *BashTool) ExecutionMode() agent.ToolExecutionMode { return agent.ToolModeSequential }

// Execute runs the command through the shared shell tool execution (upstream
// createShellToolDefinition with the bash config: getShellConfig-resolved
// bash, the settings command prefix, and pig-bash temp files).
func (t *BashTool) Execute(ctx context.Context, _ string, rawParams json.RawMessage, onUpdate agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	operations := t.Operations
	local := operations == nil
	if remoteOps, ok := operations.(*RemoteBashOperations); ok {
		local = remoteOps.Remote == nil || !remoteOps.Remote.Active()
	}
	if operations == nil {
		operations = &LocalShellOperations{ShellName: "bash", ResolveShell: func() (ShellConfig, error) { return resolveShellConfig(t.Settings) }}
	}
	autoBackground := t.autoBackgroundSeconds()
	if agent.StartedEarly(ctx) {
		// An early run may be dropped and must stay cancellable: it never moves to the background.
		autoBackground = 0
	}
	return executeShellTool(ctx, t.CWD, shellToolConfig{
		name:                     "bash",
		shellName:                "bash",
		tempFilePrefix:           "pi-bash",
		operations:               operations,
		local:                    local,
		autoBackgroundSeconds:    autoBackground,
		editToolActive:           t.EditToolActive,
		commandPrefix:            t.CommandPrefix,
		exposeSessionEnvironment: !t.HideSessionEnvironment,
		binDir:                   t.BinDir,
		spawnHook:                t.SpawnHook,
		backgroundJobs:           t.BackgroundJobs,
		remote:                   t.Remote,
		tracker:                  t.Tracker,
		checks:                   t.Checks,
	}, rawParams, onUpdate)
}
