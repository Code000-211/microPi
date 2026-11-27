// SPDX-License-Identifier: MIT

package codingagent

import (
	"math"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// ToolResultPruningConfig is the toolResultPruning setting with its defaults applied (µPi tool-result-pruning.ts
// ToolResultPruningSettings): once the context sent without clearing is at least TriggerTokens (estimated), every tool result older
// than the most recent KeepRecent whose content is at least MinChars long, or holds an image, is sent as a one-line note.
type ToolResultPruningConfig struct {
	Enabled       bool
	TriggerTokens int
	KeepRecent    int
	MinChars      int
}

// DefaultToolResultPruningConfig mirrors µPi's SETTINGS_DEFAULTS.toolResultPruning (settings-defaults.ts).
func DefaultToolResultPruningConfig() ToolResultPruningConfig {
	return ToolResultPruningConfig{Enabled: true, TriggerTokens: 40_000, KeepRecent: 8, MinChars: 2_000}
}

// GetToolResultPruningSettings returns the toolResultPruning setting with defaults applied: a count that is not a safe integer of at
// least its minimum (0, or 1 for keepRecent) is the default, and an enabled that is null or absent is the default (µPi
// settings-manager.ts getToolResultPruningSettings).
func (sm *SettingsManager) GetToolResultPruningSettings() ToolResultPruningConfig {
	result := DefaultToolResultPruningConfig()
	sm.mu.RLock()
	raw, ok := sm.merged.extra["toolResultPruning"]
	sm.mu.RUnlock()
	if !ok {
		return result
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return result
	}
	if value := fields["enabled"]; !isNullish(value) {
		result.Enabled = jsTruthy(value)
	}
	count := func(name string, fallback, least int) int {
		var value any
		if json.Unmarshal(fields[name], &value) != nil {
			return fallback
		}
		number, ok := value.(float64)
		if !ok || math.Trunc(number) != number || math.Abs(number) > 1<<53-1 || number < float64(least) {
			return fallback
		}
		return int(number)
	}
	result.TriggerTokens = count("triggerTokens", result.TriggerTokens, 0)
	result.KeepRecent = count("keepRecent", result.KeepRecent, 1)
	result.MinChars = count("minChars", result.MinChars, 0)
	return result
}
