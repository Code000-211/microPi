package codingagent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func settingsManagerOf(t *testing.T, text string) *SettingsManager {
	t.Helper()
	var settings Settings
	if err := json.Unmarshal([]byte(text), &settings); err != nil {
		t.Fatal(err)
	}
	return NewInMemorySettingsManager(settings)
}

// µPi (ade455a6): ports "reads its settings with defaults" of tool-result-pruning.test.ts.
func TestToolResultPruningSettingsWithDefaults(t *testing.T) {
	if got := settingsManagerOf(t, `{}`).GetToolResultPruningSettings(); got != (ToolResultPruningConfig{Enabled: true, TriggerTokens: 40000, KeepRecent: 8, MinChars: 2000}) {
		t.Fatalf("defaults = %+v", got)
	}
	got := settingsManagerOf(t, `{"toolResultPruning":{"enabled":false,"keepRecent":0,"minChars":500}}`).GetToolResultPruningSettings()
	if got != (ToolResultPruningConfig{Enabled: false, TriggerTokens: 40000, KeepRecent: 8, MinChars: 500}) {
		t.Fatalf("settings = %+v", got)
	}
	got = settingsManagerOf(t, `{"toolResultPruning":{"enabled":null,"triggerTokens":1.5,"keepRecent":3,"minChars":"9"}}`).GetToolResultPruningSettings()
	if got != (ToolResultPruningConfig{Enabled: true, TriggerTokens: 40000, KeepRecent: 3, MinChars: 2000}) {
		t.Fatalf("settings = %+v", got)
	}
}

// µPi (710e4993): ports the maxContextTokens cases of compaction-window-scaling.test.ts that PiG's settings model has (it has no
// thresholdPercent, summaryMaxTokens or window-scaled keepRecentTokens).
func TestCompactionMaxContextTokensSetting(t *testing.T) {
	model := &ai.Model{ID: "model-1000000", Provider: cycleTestProvider{id: "provider"}}
	cases := []struct {
		settings string
		want     int
	}{
		{`{}`, 128000},
		{`{"compaction":{"maxContextTokens":300000}}`, 300000},
		// An explicit reserveTokens asks for a threshold of its own: the default gives way, an explicit cap stays.
		{`{"compaction":{"reserveTokens":16384}}`, 0},
		{`{"compaction":{"reserveTokens":16384,"maxContextTokens":64000}}`, 64000},
		{`{"compaction":{"modelOverrides":{"provider/model-1000000":{"maxContextTokens":80000}}}}`, 80000},
		{`{"compaction":{"modelOverrides":{"provider/model-1000000":{"reserveTokens":1000}}}}`, 0},
	}
	for _, tc := range cases {
		got, err := settingsManagerOf(t, tc.settings).GetCompactionMaxContextTokens(model)
		if err != nil {
			t.Fatalf("%s: %v", tc.settings, err)
		}
		if got != tc.want {
			t.Fatalf("%s: maxContextTokens = %d, want %d", tc.settings, got, tc.want)
		}
	}
	for _, invalid := range []string{
		`{"compaction":{"maxContextTokens":0}}`,
		`{"compaction":{"maxContextTokens":"many"}}`,
		`{"compaction":{"modelOverrides":{"provider/model-1000000":{"maxContextTokens":1.5}}}}`,
	} {
		_, err := settingsManagerOf(t, invalid).GetCompactionMaxContextTokens(model)
		if err == nil || !strings.Contains(err.Error(), "maxContextTokens setting:") || !strings.HasSuffix(err.Error(), "Expected an integer from 1 to 9007199254740991.") {
			t.Fatalf("%s: error %v", invalid, err)
		}
	}
	// The setting survives a write of the settings.
	var settings Settings
	if err := json.Unmarshal([]byte(`{"compaction":{"maxContextTokens":90000}}`), &settings); err != nil {
		t.Fatal(err)
	}
	if raw, err := json.Marshal(settings); err != nil || !strings.Contains(string(raw), `"maxContextTokens":90000`) {
		t.Fatalf("written settings %s (%v)", raw, err)
	}
}
