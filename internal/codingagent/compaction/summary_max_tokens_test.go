package compaction

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// µPi (a4146262): ports the summary caps of compaction-window-scaling.test.ts that PiG's settings model has (it has no thresholdPercent
// or window-scaled keepRecentTokens).

func windowModel(contextWindow, maxTokens int) *ai.Model {
	return &ai.Model{ID: "model", ProviderMeta: ai.ProviderMetadata{ProviderID: "provider"}, Capabilities: ai.ModelCapabilities{ContextWindow: contextWindow, MaxOutputTokens: maxTokens}}
}

// resolvedCompactionSettings resolves the settings text for model as the Session does (coding Session.compactionSettings).
func resolvedCompactionSettings(t *testing.T, text string, model *ai.Model) CompactionSettings {
	t.Helper()
	var settings codingagent.Settings
	if err := json.Unmarshal([]byte(text), &settings); err != nil {
		t.Fatal(err)
	}
	manager := codingagent.NewInMemorySettingsManager(settings)
	cfg, err := manager.GetCompactionSettings(model)
	if err != nil {
		t.Fatal(err)
	}
	maxContext, err := manager.GetCompactionMaxContextTokens(model)
	if err != nil {
		t.Fatal(err)
	}
	summaryMax, scale, err := manager.GetCompactionSummaryMaxTokens(model)
	if err != nil {
		t.Fatal(err)
	}
	return CompactionSettings{Enabled: cfg.Enabled, ReserveTokens: cfg.ReserveTokens, KeepRecentTokens: cfg.KeepRecentTokens, MaxContextTokens: maxContext, SummaryMaxTokens: summaryMax, ScaleSummaryWithContextWindow: scale}
}

func summaryBudgets(t *testing.T, text string, contextWindow, maxTokens int) (summary, turnPrefix int) {
	t.Helper()
	model := windowModel(contextWindow, maxTokens)
	settings := resolvedCompactionSettings(t, text, model)
	return GetSummaryMaxTokens(model, settings, false), GetSummaryMaxTokens(model, settings, true)
}

func TestSummaryMaxTokensWindowScaling(t *testing.T) {
	cases := []struct {
		name                string
		settings            string
		window, maxTokens   int
		summary, turnPrefix int
	}{
		// Defaults: the window's 3.2% share of a window capped at maxContextTokens (128,000) is below 80% of the reserve.
		{"defaults 128k", `{}`, 128_000, 128_000, 13_107, 8_192},
		{"defaults 1M", `{}`, 1_000_000, 128_000, 13_107, 8_192},
		{"explicit reserveTokens 1M", `{"compaction":{"reserveTokens":16384}}`, 1_000_000, 128_000, 13_107, 8_192},
		{"explicit reserveTokens 50000", `{"compaction":{"reserveTokens":50000}}`, 1_000_000, 128_000, 40_000, 25_000},
		{"explicit summaryMaxTokens", `{"compaction":{"summaryMaxTokens":20000}}`, 200_000, 128_000, 20_000, 10_000},
		{"explicit summaryMaxTokens beside reserveTokens", `{"compaction":{"summaryMaxTokens":2001,"reserveTokens":16384}}`, 1_000_000, 128_000, 2_001, 1_001},
		// Caps every summary by the model's output limit (maxContextTokens raised, so that the window's share passes it).
		{"capped by the output limit", `{"compaction":{"maxContextTokens":1000000}}`, 1_000_000, 16_384, 16_384, 16_000},
		{"summaryMaxTokens capped by the output limit", `{"compaction":{"summaryMaxTokens":50000}}`, 1_000_000, 16_384, 16_384, 16_384},
		{"no context window", `{}`, 0, 128_000, 13_107, 8_192},
		{"summaryMaxTokens model override", `{"compaction":{"modelOverrides":{"provider/model":{"summaryMaxTokens":24000}}}}`, 1_000_000, 128_000, 24_000, 12_000},
	}
	for _, tc := range cases {
		summary, turnPrefix := summaryBudgets(t, tc.settings, tc.window, tc.maxTokens)
		if summary != tc.summary || turnPrefix != tc.turnPrefix {
			t.Errorf("%s: summary %d, turn prefix %d; want %d, %d", tc.name, summary, turnPrefix, tc.summary, tc.turnPrefix)
		}
	}
	// A reserveTokens override is explicit: no scaling.
	model := &ai.Model{ID: "model", Provider: namedProvider("provider"), Capabilities: ai.ModelCapabilities{ContextWindow: 1_000_000, MaxOutputTokens: 128_000}}
	if settings := resolvedCompactionSettings(t, `{"compaction":{"modelOverrides":{"provider/model":{"reserveTokens":16384}}}}`, model); settings.ScaleSummaryWithContextWindow {
		t.Error("a reserveTokens model override scales the summary cap")
	}
	if settings := resolvedCompactionSettings(t, `{}`, model); !settings.ScaleSummaryWithContextWindow {
		t.Error("the defaults do not scale the summary cap")
	}
}

type namedProvider string

func (p namedProvider) ID() string   { return string(p) }
func (p namedProvider) Close() error { return nil }
func (p namedProvider) Stream(context.Context, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	return nil, errors.New("not streamed")
}

func TestSummaryMaxTokensSettingIsValidated(t *testing.T) {
	for _, tc := range []struct{ settings, want string }{
		{`{"compaction":{"summaryMaxTokens":0}}`, "Invalid compaction.summaryMaxTokens setting: 0. Expected an integer from 1 to 9007199254740991."},
		{`{"compaction":{"summaryMaxTokens":"4096"}}`, "Invalid compaction.summaryMaxTokens setting: 4096. Expected an integer from 1 to 9007199254740991."},
	} {
		var settings codingagent.Settings
		if err := json.Unmarshal([]byte(tc.settings), &settings); err != nil {
			t.Fatal(err)
		}
		_, _, err := codingagent.NewInMemorySettingsManager(settings).GetCompactionSummaryMaxTokens()
		if err == nil || err.Error() != tc.want {
			t.Errorf("%s: error %v, want %q", tc.settings, err, tc.want)
		}
	}
}

// compaction-summary-reasoning.test.ts "clamps compaction summary maxTokens to the model output cap" and "keeps a minimum summary output
// budget when reserveTokens is zero". µPi: Pi 6fcb4867 (#9512) sets no maxTokens on either summary of a split turn.
func TestCompactSummaryMaxTokens(t *testing.T) {
	for _, tc := range []struct {
		reserve, modelMax int
		want              int
	}{
		{500_000, 128_000, 128_000},
		{0, 8192, 4096},
	} {
		recorder := &summaryRecorder{}
		prep := CompactionPreparation{
			FirstKeptEntryID:    "entry-keep",
			MessagesToSummarize: summarizeThisMessages(),
			TurnPrefixMessages:  summarizeThisMessages(),
			IsSplitTurn:         true,
			TokensBefore:        600000,
			FileOps:             NewFileOps(),
			Settings:            CompactionSettings{Enabled: true, ReserveTokens: tc.reserve, KeepRecentTokens: 20000},
		}
		if _, err := Compact(t.Context(), prep, createSummaryModel(false, tc.modelMax, nil), "", nil, "", "", CompleterStreamFn(recorder), nil, nil, ai.RetryCallbacks{}, ""); err != nil {
			t.Fatal(err)
		}
		if len(recorder.calls) != 2 || recorder.calls[0].options.MaxTokens != tc.want || recorder.calls[1].options.MaxTokens != tc.want {
			t.Fatalf("reserve %d: maxTokens of %d calls, want %d on both", tc.reserve, len(recorder.calls), tc.want)
		}
	}
}

// compaction-summary-reasoning.test.ts "caps summary output by reserveTokens on 1M-context models".
func TestGenerateSummaryCapsOutputByReserveTokens(t *testing.T) {
	recorder := &summaryRecorder{}
	model := createSummaryModel(true, 128000, nil)
	model.Capabilities.ContextWindow = 1_000_000
	if _, err := GenerateSummary(t.Context(), summarizeThisMessages(), model, 16384, "test-key", nil, "", "", ai.ThinkingHigh, recorder.streamFn(), nil, nil, ai.RetryCallbacks{}, ""); err != nil {
		t.Fatal(err)
	}
	if len(recorder.calls) != 1 || recorder.calls[0].options.MaxTokens != 13107 || recorder.calls[0].options.Thinking != ai.ThinkingLevelHigh {
		t.Fatalf("calls %+v, want one with maxTokens 13107 and reasoning high", recorder.calls)
	}
}

// cutOffCompleter answers the first cutOff calls cut off at the output cap, then with a summary.
type cutOffCompleter struct {
	summaryRecorder
	cutOff int
}

func (c *cutOffCompleter) CompleteSimple(ctx context.Context, model *ai.Model, system string, messages []agent.AgentMessage, options ai.StreamOptions) (string, *ai.Usage, error) {
	if len(c.calls) < c.cutOff {
		c.calls = append(c.calls, summaryCall{model: model, messages: messages, options: options})
		usage := mockSummaryUsage()
		return "", &usage, ErrSummaryTokenCap
	}
	return c.summaryRecorder.CompleteSimple(ctx, model, system, messages, options)
}

// compaction-summary-reasoning.test.ts "summarizes again without reasoning when reasoning uses the whole output cap" and "rejects a
// length-limited summary made without reasoning" (µPi).
func TestSummaryCutOffWithReasoningIsAskedAgainWithout(t *testing.T) {
	completer := &cutOffCompleter{cutOff: 1}
	text, usage, err := GenerateSummaryWithUsage(t.Context(), summarizeThisMessages(), createSummaryModel(true, 8192, nil), 16384, "test-key", nil, "", "", ai.ThinkingHigh, CompleterStreamFn(completer), nil, nil, ai.RetryCallbacks{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if text != "## Goal\nTest summary" || usage == nil || usage.Input != 20 || usage.Output != 20 || usage.TotalTokens != 40 {
		t.Fatalf("text %q usage %+v", text, usage)
	}
	calls := completer.calls
	if len(calls) != 2 || calls[0].options.MaxTokens != 8192 || calls[0].options.Thinking != ai.ThinkingLevelHigh || calls[1].options.MaxTokens != 8192 || calls[1].options.Thinking != "" {
		t.Fatalf("calls %+v", calls)
	}

	completer = &cutOffCompleter{cutOff: 2}
	_, _, err = GenerateSummaryWithUsage(t.Context(), summarizeThisMessages(), createSummaryModel(true, 8192, nil), 16384, "test-key", nil, "", "", ai.ThinkingHigh, CompleterStreamFn(completer), nil, nil, ai.RetryCallbacks{}, "")
	if err == nil || !strings.Contains(err.Error(), "generation hit the token cap") || len(completer.calls) != 2 {
		t.Fatalf("error %v after %d calls, want the token cap after 2", err, len(completer.calls))
	}

	// Without reasoning, a cut-off summary fails at once.
	completer = &cutOffCompleter{cutOff: 1}
	if _, _, err = GenerateSummaryWithUsage(t.Context(), summarizeThisMessages(), createSummaryModel(false, 8192, nil), 16384, "test-key", nil, "", "", ai.ThinkingHigh, CompleterStreamFn(completer), nil, nil, ai.RetryCallbacks{}, ""); err == nil || len(completer.calls) != 1 {
		t.Fatalf("error %v after %d calls, want a failure after 1", err, len(completer.calls))
	}
}
