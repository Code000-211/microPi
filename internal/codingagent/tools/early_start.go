package tools

import (
	"encoding/json"

	"github.com/MichaelKinsy/PiG/agent"
)

// µPi: the built-in tools whose calls may start while the model's message still streams (agent.EarlyStarter): the read-only tools always, and bash
// when its command only reads (IsReadOnlyCommand), runs in the foreground, and nothing (a command prefix, a spawn hook) may change what it runs.
// Ports microPi's agent-session.ts _earlyToolExecution.
var (
	_ agent.EarlyStarter = (*ReadTool)(nil)
	_ agent.EarlyStarter = (*LsTool)(nil)
	_ agent.EarlyStarter = (*GrepTool)(nil)
	_ agent.EarlyStarter = (*FindTool)(nil)
	_ agent.EarlyStarter = (*ProjectSearchTool)(nil)
	_ agent.EarlyStarter = (*BashTool)(nil)
)

func (t *ReadTool) StartsEarly(json.RawMessage) bool          { return true }
func (t *LsTool) StartsEarly(json.RawMessage) bool            { return true }
func (t *GrepTool) StartsEarly(json.RawMessage) bool          { return true }
func (t *FindTool) StartsEarly(json.RawMessage) bool          { return true }
func (t *ProjectSearchTool) StartsEarly(json.RawMessage) bool { return true }

func (t *BashTool) StartsEarly(args json.RawMessage) bool {
	if t.CommandPrefix != "" || t.SpawnHook != nil {
		return false
	}
	var p shellParams
	// The aliases (run_in_background, is_background) are read too: the run applies them before it starts (PrepareArguments).
	if err := json.Unmarshal(NormalizeToolArguments(args, BashAliases), &p); err != nil {
		return false
	}
	if p.Background != nil && *p.Background {
		return false
	}
	return IsReadOnlyCommand(p.Command)
}
