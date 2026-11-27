// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package codingagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"math"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/jsnumber"
)

// Ports packages/coding-agent/src/core/settings-manager.ts (getCompactionTokenSetting).
// Authored invalid values survive loading and merging so only the getter for the selected model rejects them.
func readCompactionNumber(fields map[string]json.RawMessage, key string) *float64 {
	raw, ok := fields[key]
	if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	var value float64
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	delete(fields, key)
	return &value
}

func (s *CompactionSettingsJSON) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	*s = CompactionSettingsJSON{extra: fields}
	s.ReserveTokens = readCompactionNumber(fields, "reserveTokens")
	s.KeepRecentTokens = readCompactionNumber(fields, "keepRecentTokens")
	s.MaxContextTokens = readCompactionNumber(fields, "maxContextTokens")
	if raw, ok := fields["enabled"]; ok {
		if err := json.Unmarshal(raw, &s.Enabled); err != nil {
			return err
		}
		delete(fields, "enabled")
	}
	if raw, ok := fields["modelOverrides"]; ok {
		if err := json.Unmarshal(raw, &s.ModelOverrides); err != nil {
			return err
		}
		delete(fields, "modelOverrides")
	}
	return nil
}
func compactionNumberFields(extra map[string]json.RawMessage, reserve, recent, maxContext *float64) (map[string]json.RawMessage, error) {
	fields := maps.Clone(extra)
	if fields == nil {
		fields = make(map[string]json.RawMessage)
	}
	for key, value := range map[string]*float64{"reserveTokens": reserve, "keepRecentTokens": recent, "maxContextTokens": maxContext} {
		if value != nil {
			raw, err := json.Marshal(*value)
			if err != nil {
				return nil, err
			}
			fields[key] = raw
		}
	}
	return fields, nil
}
func (s CompactionSettingsJSON) MarshalJSON() ([]byte, error) {
	fields, err := compactionNumberFields(s.extra, s.ReserveTokens, s.KeepRecentTokens, s.MaxContextTokens)
	if err != nil {
		return nil, err
	}
	if s.Enabled != nil {
		raw, e := json.Marshal(*s.Enabled)
		if e != nil {
			return nil, e
		}
		fields["enabled"] = raw
	}
	if s.ModelOverrides != nil {
		raw, e := json.Marshal(s.ModelOverrides)
		if e != nil {
			return nil, e
		}
		fields["modelOverrides"] = raw
	}
	return json.Marshal(fields)
}
func (s *CompactionModelOverride) UnmarshalJSON(data []byte) error {
	*s = CompactionModelOverride{}
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '{' {
		s.invalidEntry = bytes.Clone(data)
		return nil
	}
	if err := json.Unmarshal(data, &s.extra); err != nil {
		return err
	}
	s.ReserveTokens = readCompactionNumber(s.extra, "reserveTokens")
	s.KeepRecentTokens = readCompactionNumber(s.extra, "keepRecentTokens")
	return nil
}
func (s CompactionModelOverride) MarshalJSON() ([]byte, error) {
	if s.invalidEntry != nil {
		return s.invalidEntry, nil
	}
	fields, err := compactionNumberFields(s.extra, s.ReserveTokens, s.KeepRecentTokens, nil)
	if err != nil {
		return nil, err
	}
	return json.Marshal(fields)
}

func compactionJSONText(raw json.RawMessage) string {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "null"
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return string(raw)
	}
	return jsStringValue(value)
}

// compactionNumberText is String(value), as Pi interpolates a configured number into text.
func compactionNumberText(value float64) string { return jsnumber.String(value) }

func validateCompactionToken(field string, value *float64, invalid json.RawMessage) error {
	if invalid != nil {
		return fmt.Errorf("Invalid %s setting: %s. Expected a non-negative safe integer.", field, compactionJSONText(invalid))
	}
	// upstream: packages/coding-agent/src/core/settings-manager.ts:getCompactionTokenSetting
	if value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0) || math.Trunc(*value) != *value || *value < 0 || *value > (1<<53-1)) {
		return fmt.Errorf("Invalid %s setting: %s. Expected a non-negative safe integer.", field, compactionNumberText(*value))
	}
	return nil
}
func getCompactionTokenSetting(settings *CompactionSettingsJSON, field, provider, id string) (int, error) {
	result := defaultCompactionConfig.ReserveTokens
	if field == "keepRecentTokens" {
		result = defaultCompactionConfig.KeepRecentTokens
	}
	if settings == nil {
		return result, nil
	}
	ordinary := settings.ReserveTokens
	if field == "keepRecentTokens" {
		ordinary = settings.KeepRecentTokens
	}
	if err := validateCompactionToken("compaction."+field, ordinary, settings.extra[field]); err != nil {
		return 0, err
	}
	if ordinary != nil {
		result = int(*ordinary)
	}
	if provider == "" && id == "" {
		return result, nil
	}
	entry, ok := settings.ModelOverrides[provider+"/"+id]
	if !ok {
		return result, nil
	}
	path := fmt.Sprintf("compaction.modelOverrides[%q]", provider+"/"+id)
	if entry.invalidEntry != nil {
		return 0, fmt.Errorf("Invalid %s setting: %s. Expected an object.", path, compactionJSONText(entry.invalidEntry))
	}
	override := entry.ReserveTokens
	if field == "keepRecentTokens" {
		override = entry.KeepRecentTokens
	}
	if err := validateCompactionToken(path+"."+field, override, entry.extra[field]); err != nil {
		return 0, err
	}
	if override != nil {
		result = int(*override)
	}
	return result, nil
}

// defaultMaxContextTokens is the maxContextTokens that applies while neither reserveTokens nor maxContextTokens is set (µPi
// settings-defaults.ts SETTINGS_DEFAULTS.compaction.maxContextTokens).
const defaultMaxContextTokens = 128000

// getCompactionMaxContextTokens resolves compaction.maxContextTokens for the model through its override, the ordinary setting, then the
// default, which gives way to an explicit reserveTokens: that asks for a threshold of its own. 0 is no cap. µPi (710e4993,
// settings-manager.ts getCompactionSettings, getExplicitCompactionSetting with the range 1 to Number.MAX_SAFE_INTEGER).
func getCompactionMaxContextTokens(settings *CompactionSettingsJSON, provider, id string) (int, error) {
	if settings == nil {
		return defaultMaxContextTokens, nil
	}
	explicit, err := explicitCompactionRangeSetting(settings, "maxContextTokens", settings.MaxContextTokens, provider, id)
	if err != nil {
		return 0, err
	}
	switch {
	case explicit != nil:
		return int(*explicit), nil
	case explicitCompactionReserve(settings, provider, id):
		return 0, nil
	}
	return defaultMaxContextTokens, nil
}

// explicitCompactionReserve reports whether reserveTokens is set for the model, in its override or the ordinary setting, valid or not.
func explicitCompactionReserve(settings *CompactionSettingsJSON, provider, id string) bool {
	if settings.ReserveTokens != nil || settings.extra["reserveTokens"] != nil {
		return true
	}
	if provider == "" && id == "" {
		return false
	}
	entry, ok := settings.ModelOverrides[provider+"/"+id]
	return ok && entry.invalidEntry == nil && (entry.ReserveTokens != nil || entry.extra["reserveTokens"] != nil)
}

// explicitCompactionRangeSetting is the integer from 1 to Number.MAX_SAFE_INTEGER the user set for field, from the model's override or the
// ordinary setting (ordinary when the settings hold it as a field, else read from their other members); nil when neither sets it. µPi
// (settings-manager.ts getExplicitCompactionSetting with COMPACTION_SETTING_RANGES maxContextTokens and summaryMaxTokens).
func explicitCompactionRangeSetting(settings *CompactionSettingsJSON, field string, ordinary *float64, provider, id string) (*float64, error) {
	valid := func(value float64) bool {
		return !math.IsNaN(value) && !math.IsInf(value, 0) && math.Trunc(value) == value && value >= 1 && value <= (1<<53-1)
	}
	check := func(path string, value *float64, invalid json.RawMessage) error {
		const expected = "Expected an integer from 1 to 9007199254740991."
		if invalid != nil {
			return fmt.Errorf("Invalid %s setting: %s. %s", path, compactionJSONText(invalid), expected)
		}
		if value != nil && !valid(*value) {
			return fmt.Errorf("Invalid %s setting: %s. %s", path, compactionNumberText(*value), expected)
		}
		return nil
	}
	// readMember reads field from the members a settings object keeps as written.
	readMember := func(path string, members map[string]json.RawMessage) (*float64, error) {
		raw, present := members[field]
		if !present || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return nil, nil
		}
		var value float64
		if json.Unmarshal(raw, &value) != nil {
			return nil, check(path, nil, raw)
		}
		if err := check(path, &value, nil); err != nil {
			return nil, err
		}
		return &value, nil
	}
	explicit := ordinary
	if ordinary != nil || field == "maxContextTokens" {
		if err := check("compaction."+field, ordinary, settings.extra[field]); err != nil {
			return nil, err
		}
	} else {
		value, err := readMember("compaction."+field, settings.extra)
		if err != nil {
			return nil, err
		}
		explicit = value
	}
	if provider != "" || id != "" {
		if entry, ok := settings.ModelOverrides[provider+"/"+id]; ok {
			path := fmt.Sprintf("compaction.modelOverrides[%q]", provider+"/"+id)
			if entry.invalidEntry != nil {
				return nil, fmt.Errorf("Invalid %s setting: %s. Expected an object.", path, compactionJSONText(entry.invalidEntry))
			}
			// An override keeps these settings in its members as written (CompactionModelOverride.extra).
			value, err := readMember(path+"."+field, entry.extra)
			if err != nil {
				return nil, err
			}
			if value != nil {
				explicit = value
			}
		}
	}
	return explicit, nil
}

// GetCompactionSummaryMaxTokens resolves compaction.summaryMaxTokens for the optional model (0: unset, the cap is derived from
// reserveTokens), and whether that derived cap grows with the summarizer's context window: only while neither reserveTokens nor
// summaryMaxTokens is set. µPi (settings-manager.ts getCompactionSettings summaryMaxTokens, scaleSummaryWithContextWindow).
func (sm *SettingsManager) GetCompactionSummaryMaxTokens(model ...*ai.Model) (summaryMaxTokens int, scaleWithContextWindow bool, err error) {
	provider, id := compactionModelKey(model)
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	settings := sm.merged.Compaction
	if settings == nil {
		return 0, true, nil
	}
	explicit, err := explicitCompactionRangeSetting(settings, "summaryMaxTokens", nil, provider, id)
	if err != nil {
		return 0, false, err
	}
	if explicit != nil {
		return int(*explicit), false, nil
	}
	return 0, !explicitCompactionReserve(settings, provider, id), nil
}

func mergeCompactionNumber(current **float64, extra map[string]json.RawMessage, key string, incoming *float64, incomingExtra map[string]json.RawMessage) {
	if incoming != nil {
		*current = cloneFloatPtr(incoming)
		delete(extra, key)
	} else if raw, ok := incomingExtra[key]; ok {
		*current = nil
		extra[key] = bytes.Clone(raw)
	}
}
