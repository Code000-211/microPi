package tools

import (
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
)

// TOOL-18: upstream's tool definitions set no executionMode, so every built-in runs in parallel batches.
// µPi: the tools that change files or run a command (bash, powershell, edit, write) are sequential, so a response's calls run in order when one of
// them is; the read-only tools stay parallel.
func TestBuiltinToolsExecutionModes(t *testing.T) {
	sequential := map[string]bool{"bash": true, "powershell": true, "edit": true, "write": true}
	for _, tool := range CreateAllTools(t.TempDir(), nil) {
		want := agent.ToolModeParallel
		if sequential[tool.Name()] {
			want = agent.ToolModeSequential
		}
		if mode := tool.ExecutionMode(); mode != want {
			t.Errorf("%s execution mode = %q, want %q", tool.Name(), mode, want)
		}
	}
}
