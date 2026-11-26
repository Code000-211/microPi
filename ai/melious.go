package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
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
		})
	}
	return out
}

type meliousModelsResponse struct {
	Data []struct {
		ID                  string `json:"id"`
		Name                string `json:"name"`
		ContextLength       int    `json:"context_length"`
		MaxCompletionTokens int    `json:"max_completion_tokens"`
		Meta                *struct {
			ContextLength       int   `json:"context_length"`
			MaxCompletionTokens int   `json:"max_completion_tokens"`
			Reasoning           *bool `json:"reasoning"`
		} `json:"_meta"`
	} `json:"data"`
}

func fetchMeliousModels(refresh RefreshModelsContext) ([]AnyModel, error) {
	key := refresh.Credential.Key
	if key == "" && refresh.Credential.Type == CredentialOAuth {
		key = refresh.Credential.Access
	}
	if key == "" {
		key = strings.TrimSpace(os.Getenv("MELIOUS_API_KEY"))
	}
	if key == "" {
		return nil, nil
	}

	baseURL := strings.TrimRight(refresh.BaseURL, "/")
	if baseURL == "" {
		baseURL = strings.TrimRight(os.Getenv("MELIOUS_BASE_URL"), "/")
	}
	if baseURL == "" {
		baseURL = meliousBaseURL
	}
	modelsURL := baseURL + "/models?include_meta=true"
	if !strings.HasSuffix(baseURL, "/v1") {
		modelsURL = baseURL + "/v1/models?include_meta=true"
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
	if resp.StatusCode != http.StatusOK {
		return nil, nil
	}

	var parsed meliousModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil || len(parsed.Data) == 0 {
		return nil, nil
	}

	builtin := GetBuiltinModels("melious")
	byID := make(map[string]*Model, len(builtin))
	out := make([]AnyModel, 0, len(builtin)+len(parsed.Data))
	for _, m := range builtin {
		byID[m.ID] = m
		out = append(out, m)
	}

	for _, entry := range parsed.Data {
		if entry.ID == "" {
			continue
		}
		ctxWin := entry.ContextLength
		maxTok := entry.MaxCompletionTokens
		var reasoning *bool
		if entry.Meta != nil {
			if entry.Meta.ContextLength > 0 {
				ctxWin = entry.Meta.ContextLength
			}
			if entry.Meta.MaxCompletionTokens > 0 {
				maxTok = entry.Meta.MaxCompletionTokens
			}
			reasoning = entry.Meta.Reasoning
		}
		if existing, ok := byID[entry.ID]; ok {
			if ctxWin > 0 {
				existing.Capabilities.ContextWindow = ctxWin
			}
			if maxTok > 0 {
				existing.Capabilities.MaxOutputTokens = maxTok
			}
			continue
		}
		if ctxWin <= 0 {
			ctxWin = 131072
		}
		if maxTok <= 0 {
			maxTok = min(ctxWin, 65536)
		}
		isReasoning := false
		if reasoning != nil {
			isReasoning = *reasoning
		} else {
			lower := strings.ToLower(entry.ID)
			isReasoning = strings.Contains(lower, "deepseek") ||
				strings.Contains(lower, "glm-5") ||
				strings.Contains(lower, "qwen3") ||
				strings.Contains(lower, "kimi") ||
				strings.Contains(lower, "gpt-oss") ||
				strings.Contains(lower, "minimax") ||
				strings.Contains(lower, "r1")
		}
		name := entry.Name
		if name == "" {
			name = entry.ID
		}
		gm := GeneratedModel{
			ID:              entry.ID,
			Provider:        "melious",
			DisplayName:     name,
			API:             APIOpenAICompletions,
			BaseURL:         baseURL,
			Compat:          meliousCompat(isReasoning),
			ContextWindow:   ctxWin,
			MaxOutputTokens: maxTok,
			Reasoning:       isReasoning,
			Capabilities:    []string{"text"},
		}
		out = append(out, gm.ToModel())
	}
	return out, nil
}
