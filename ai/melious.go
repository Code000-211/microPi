package ai

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

const meliousBaseURL = "https://api.melious.ai/v1"

func init() {
	GeneratedProviders = append(GeneratedProviders, "melious")
	GeneratedModels = append(GeneratedModels, meliousGeneratedModels()...)
}

// MeliousProvider is the built-in "melious" provider.
func MeliousProvider() *ModelsProvider { return builtinProvider("melious") }

func meliousCompat(reasoning bool) *ModelCompat {
	f := false
	tr := true
	return &ModelCompat{
		SupportsStore:              &f,
		SupportsDeveloperRole:      &f,
		SupportsReasoningEffort:    &reasoning,
		SupportsUsageInStreaming:   &tr,
		MaxTokensField:             "max_tokens",
		ThinkingFormat:             "openai",
		SupportsLongCacheRetention: &f,
		SupportsStrictMode:         &tr,
		SendSessionAffinityHeaders: &tr,
	}
}

func meliousThinkingMap() map[ModelThinkingLevel]*string {
	off := "none"
	low := "low"
	med := "medium"
	high := "high"
	maxLvl := "max"
	return map[ModelThinkingLevel]*string{
		"off":     &off,
		"minimal": nil,
		"low":     &low,
		"medium":  &med,
		"high":    &high,
		"xhigh":   nil,
		"max":     &maxLvl,
	}
}

// meliousInputLimits is the image resize profile every vision model of the catalog carries (Pi's conservative default), or nil for a
// text-only model. Without it an image would go to a Melious vision model unresized.
func meliousInputLimits(images bool) *ModelInputLimits {
	if !images {
		return nil
	}
	return &ModelInputLimits{Images: &ModelImageInputLimits{Resize: &ModelImageResizeOptions{MaxWidth: 2000, MaxHeight: 2000, MaxBytes: 4718592, JPEGQuality: 80}}}
}

func meliousGeneratedModels() []GeneratedModel {
	type spec struct {
		id        string
		name      string
		reasoning bool
		images    bool
		ctxWin    int
		maxTokens int
		inCost    float64
		outCost   float64
		cacheRead float64
	}
	specs := []spec{
		{"deepseek-flash", "DeepSeek V4.1 Flash", true, true, 1000000, 262144, 0.25, 1, 0.025},
		{"deepseek-r1", "DeepSeek R1", true, false, 163840, 65536, 0.55, 2.19, 0.14},
		{"deepseek-v3.2", "DeepSeek V3.2", true, false, 163840, 65536, 0.27, 1.1, 0.07},
		{"deepseek-v4-pro", "DeepSeek V4 Pro", true, false, 1000000, 262144, 1.2, 3.6, 0.12},
		{"glm-5.2", "GLM 5.2", true, false, 1000000, 262144, 1.2, 3.8, 0.12},
		{"glm-5.3", "GLM 5.3", true, true, 1000000, 262144, 1.2, 3.8, 0.12},
		{"glm-5.3-flash", "GLM 5.3 Flash", true, true, 1000000, 131072, 0.12, 0.45, 0.025},
		{"gpt-oss-120b", "GPT OSS 120B", true, false, 131072, 131072, 0.1, 0.5, 0.05},
		{"kimi-k2.6", "Kimi K2.6", true, true, 262144, 131072, 0.85, 3.5, 0.15},
		{"kimi-k3", "Kimi K3", true, true, 1048576, 262144, 2.5, 12, 0.25},
		{"llama-3.3-70b-instruct", "Llama 3.3 70B Instruct", false, false, 131072, 32768, 0.35, 0.6, 0.05},
		{"minimax-m3", "MiniMax M3", true, true, 524288, 262144, 0.3, 1.2, 0.06},
		{"mistral-large-latest", "Mistral Large", false, true, 262144, 131072, 1.8, 5.4, 0.18},
		{"mistral-small-latest", "Mistral Small", true, true, 262144, 65536, 0.1, 0.3, 0.01},
		{"qwen3-coder", "Qwen 3 Coder", false, false, 262144, 65536, 0.3, 1.2, 0.05},
		{"qwen3.7-max", "Qwen 3.7 Max", true, false, 1000000, 131072, 2, 6, 0.25},
		{"qwen3.8-27b", "Qwen 3.8 27B", true, true, 262144, 65536, 0.5, 2, 0.08},
		{"qwen3.8-max", "Qwen 3.8 Max", true, true, 262144, 131072, 2, 6, 0.25},
	}

	out := make([]GeneratedModel, 0, len(specs))
	for _, s := range specs {
		caps := []string{"text"}
		if s.images {
			caps = []string{"text", "image"}
		}
		var tlm map[ModelThinkingLevel]*string
		if s.reasoning {
			tlm = meliousThinkingMap()
		}
		out = append(out, GeneratedModel{
			ID:                   s.id,
			Provider:             "melious",
			DisplayName:          s.name,
			API:                  APIOpenAICompletions,
			BaseURL:              meliousBaseURL,
			Compat:               meliousCompat(s.reasoning),
			ThinkingLevelMap:     tlm,
			ContextWindow:        s.ctxWin,
			MaxOutputTokens:      s.maxTokens,
			InputCostPerMTokens:  s.inCost,
			OutputCostPerMTokens: s.outCost,
			CacheReadCost:        s.cacheRead,
			Reasoning:            s.reasoning,
			Capabilities:         caps,
			InputLimits:          meliousInputLimits(s.images),
		})
	}
	return out
}

// meliousModelEntry is one entry of `GET /models?include_meta=true`. Fields that hold numbers, strings or anything
// else stay raw: the catalog takes what it can read, as melious.ts does.
type meliousModelEntry struct {
	ID                  string          `json:"id"`
	Name                json.RawMessage `json:"name"`
	ContextLength       json.RawMessage `json:"context_length"`
	MaxCompletionTokens json.RawMessage `json:"max_completion_tokens"`
	Pricing             json.RawMessage `json:"pricing"`
	Meta                *struct {
		ContextLength       json.RawMessage `json:"context_length"`
		MaxCompletionTokens json.RawMessage `json:"max_completion_tokens"`
		Pricing             json.RawMessage `json:"pricing"`
		Reasoning           json.RawMessage `json:"reasoning"`
		Modalities          *struct {
			Input json.RawMessage `json:"input"`
		} `json:"modalities"`
	} `json:"_meta"`
}

// meliousCost holds per-million-token rates.
type meliousCost struct{ input, output, cacheRead, cacheWrite float64 }

// rawPresent is JavaScript's `value ?? ...` test: a member that is there and not null.
func rawPresent(raw json.RawMessage) bool {
	return len(raw) > 0 && string(raw) != "null"
}

// rawTruthyNumber is a number member for JavaScript's `a || b`: 0, null and non-numbers fall through.
func rawTruthyNumber(raw json.RawMessage) (float64, bool) {
	var number float64
	if !rawPresent(raw) || json.Unmarshal(raw, &number) != nil || number == 0 {
		return 0, false
	}
	return number, true
}

var meliousLeadingFloat = regexp.MustCompile(`^[\t\n\v\f\r \x{a0}\x{feff}]*([+-]?(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?)`)

// parsePerMillionRate is melious.ts parsePerMillionRate: a string is read with parseFloat, and rates below 0.01 are
// per-token rates, converted to $/1M tokens.
func parsePerMillionRate(raw json.RawMessage, fallback float64) float64 {
	var rate float64
	var text string
	switch {
	case !rawPresent(raw):
		return fallback
	case json.Unmarshal(raw, &text) == nil:
		match := meliousLeadingFloat.FindStringSubmatch(text)
		if match == nil {
			return fallback
		}
		parsed, err := strconv.ParseFloat(match[1], 64)
		if err != nil {
			return fallback
		}
		rate = parsed
	case json.Unmarshal(raw, &rate) != nil:
		return fallback
	}
	if math.IsInf(rate, 0) || math.IsNaN(rate) || rate < 0 {
		return fallback
	}
	if rate > 0 && rate < 0.01 {
		rounded, _ := strconv.ParseFloat(strconv.FormatFloat(rate*1_000_000, 'f', 6, 64), 64)
		return rounded
	}
	return rate
}

// meliousCostOf is melious.ts meliousCost: the pricing's rates, each falling back to base.
func meliousCostOf(pricing json.RawMessage, base meliousCost) meliousCost {
	var fields struct {
		Prompt          json.RawMessage `json:"prompt"`
		Completion      json.RawMessage `json:"completion"`
		Input           json.RawMessage `json:"input"`
		Output          json.RawMessage `json:"output"`
		InputCacheRead  json.RawMessage `json:"input_cache_read"`
		InputCacheWrite json.RawMessage `json:"input_cache_write"`
	}
	if !rawPresent(pricing) {
		return base
	}
	if json.Unmarshal(pricing, &fields) != nil {
		// A pricing that is no object has none of the members: every rate falls back.
		return base
	}
	input, output := fields.Prompt, fields.Completion
	if !rawPresent(input) {
		input = fields.Input
	}
	if !rawPresent(output) {
		output = fields.Output
	}
	return meliousCost{
		input:      parsePerMillionRate(input, base.input),
		output:     parsePerMillionRate(output, base.output),
		cacheRead:  parsePerMillionRate(fields.InputCacheRead, base.cacheRead),
		cacheWrite: parsePerMillionRate(fields.InputCacheWrite, base.cacheWrite),
	}
}

// pricing is `meta?.pricing ?? entry.pricing`.
func (e *meliousModelEntry) pricing() json.RawMessage {
	if e.Meta != nil && rawPresent(e.Meta.Pricing) {
		return e.Meta.Pricing
	}
	return e.Pricing
}

// limit is `meta?.<field> || entry.<field>`.
func (e *meliousModelEntry) limit(meta, entry json.RawMessage) (int, bool) {
	if e.Meta != nil {
		if n, ok := rawTruthyNumber(meta); ok {
			return int(n), true
		}
	}
	if n, ok := rawTruthyNumber(entry); ok {
		return int(n), true
	}
	return 0, false
}

func (e *meliousModelEntry) contextLength() (int, bool) {
	var meta json.RawMessage
	if e.Meta != nil {
		meta = e.Meta.ContextLength
	}
	return e.limit(meta, e.ContextLength)
}

func (e *meliousModelEntry) maxCompletionTokens() (int, bool) {
	var meta json.RawMessage
	if e.Meta != nil {
		meta = e.Meta.MaxCompletionTokens
	}
	return e.limit(meta, e.MaxCompletionTokens)
}

// fetchMeliousModels is melious.ts personalizeMeliousCatalog: `GET /models?include_meta=true` with the key updates the
// static catalog's context window, output limit and rates and appends the models the catalog lacks. Any failure, a
// list that is not JSON included, keeps the static catalog (nil).
func fetchMeliousModels(refresh RefreshModelsContext) ([]AnyModel, error) {
	var key string
	if refresh.Credential != nil {
		key = refresh.Credential.Key
		if refresh.Credential.Type == CredentialOAuth {
			key = refresh.Credential.Access
		}
	}
	if key == "" {
		key = strings.TrimSpace(os.Getenv("MELIOUS_API_KEY"))
	}
	if key == "" {
		return nil, nil
	}

	baseURL := refresh.BaseURL
	if baseURL == "" {
		baseURL = meliousBaseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")
	modelsURL := baseURL + "/v1/models?include_meta=true"
	if strings.HasSuffix(baseURL, "/v1") {
		modelsURL = baseURL + "/models?include_meta=true"
	}

	ctx := refresh.Signal
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL, nil)
	if err != nil {
		return nil, nil
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, nil
	}

	var body struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || body.Data == nil {
		return nil, nil
	}
	// A Map keyed by id: the first entry of an id keeps its place, the last one its content.
	discovered := map[string]*meliousModelEntry{}
	var order []string
	for _, raw := range body.Data {
		var entry meliousModelEntry
		var probe struct {
			ID any `json:"id"`
		}
		if json.Unmarshal(raw, &probe) != nil {
			continue
		}
		if _, isString := probe.ID.(string); !isString {
			continue
		}
		if json.Unmarshal(raw, &entry) != nil {
			// A member of a type the entry does not take (a number for _meta, say) is read as missing.
			entry = meliousModelEntry{ID: probe.ID.(string)}
		}
		if _, seen := discovered[entry.ID]; !seen {
			order = append(order, entry.ID)
		}
		discovered[entry.ID] = &entry
	}
	if len(discovered) == 0 {
		return nil, nil
	}

	builtin := GetBuiltinModels("melious")
	seen := make(map[string]bool, len(builtin))
	out := make([]AnyModel, 0, len(builtin)+len(order))
	for _, model := range builtin {
		seen[model.ID] = true
		entry := discovered[model.ID]
		if entry == nil {
			out = append(out, model)
			continue
		}
		// A copy: the catalog's own model stays as it is.
		updated := *model
		if n, ok := entry.contextLength(); ok {
			updated.Capabilities.ContextWindow = n
		}
		if n, ok := entry.maxCompletionTokens(); ok {
			updated.Capabilities.MaxOutputTokens = n
		}
		if pricing := entry.pricing(); rawPresent(pricing) {
			caps := &updated.Capabilities
			cost := meliousCostOf(pricing, meliousCost{caps.InputCostPer1M, caps.OutputCostPer1M, caps.CacheReadCostPer1M, caps.CacheWriteCostPer1M})
			caps.InputCostPer1M, caps.OutputCostPer1M, caps.CacheReadCostPer1M, caps.CacheWriteCostPer1M = cost.input, cost.output, cost.cacheRead, cost.cacheWrite
		}
		out = append(out, &updated)
	}

	for _, id := range order {
		if seen[id] {
			continue
		}
		entry := discovered[id]
		ctxWin, ok := entry.contextLength()
		if !ok {
			ctxWin = 131_072
		}
		maxTok, ok := entry.maxCompletionTokens()
		if !ok {
			maxTok = min(ctxWin, 65_536)
		}
		hasImage := false
		var reasoning *bool
		if entry.Meta != nil {
			if entry.Meta.Modalities != nil {
				var inputs []any
				if json.Unmarshal(entry.Meta.Modalities.Input, &inputs) == nil {
					hasImage = slices.Contains(inputs, any("image"))
				}
			}
			var flag bool
			if rawPresent(entry.Meta.Reasoning) && json.Unmarshal(entry.Meta.Reasoning, &flag) == nil {
				reasoning = &flag
			}
		}
		isReasoning := false
		if reasoning != nil {
			isReasoning = *reasoning
		} else {
			lower := strings.ToLower(id)
			isReasoning = strings.Contains(lower, "deepseek") ||
				strings.Contains(lower, "glm-5") ||
				strings.Contains(lower, "qwen3") ||
				strings.Contains(lower, "kimi") ||
				strings.Contains(lower, "gpt-oss") ||
				strings.Contains(lower, "minimax") ||
				strings.Contains(lower, "r1")
		}
		name := id
		var entryName string
		if json.Unmarshal(entry.Name, &entryName) == nil && entryName != "" {
			name = entryName
		}
		capabilities := []string{"text"}
		if hasImage {
			capabilities = []string{"text", "image"}
		}
		cost := meliousCostOf(entry.pricing(), meliousCost{})
		generated := GeneratedModel{
			ID:                   id,
			Provider:             "melious",
			DisplayName:          name,
			API:                  APIOpenAICompletions,
			BaseURL:              meliousBaseURL,
			Compat:               meliousCompat(isReasoning),
			ContextWindow:        ctxWin,
			MaxOutputTokens:      maxTok,
			InputCostPerMTokens:  cost.input,
			OutputCostPerMTokens: cost.output,
			CacheReadCost:        cost.cacheRead,
			CacheWriteCost:       cost.cacheWrite,
			Reasoning:            isReasoning,
			Capabilities:         capabilities,
			InputLimits:          meliousInputLimits(hasImage),
		}
		out = append(out, generated.ToModel())
	}
	return out, nil
}
