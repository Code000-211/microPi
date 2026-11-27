package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

// Ports microPi packages/ai/test/melious-models.test.ts.

func meliousRefreshAgainst(t *testing.T, body string, status int) []AnyModel {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.URL.Query().Get("include_meta") != "true" || r.Header.Get("Authorization") != "Bearer sk-mel-test" {
			t.Errorf("request %s %v", r.URL, r.Header)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	models, err := fetchMeliousModels(RefreshModelsContext{Credential: &Credential{Type: CredentialAPIKey, Key: "sk-mel-test"}, Signal: context.Background(), BaseURL: server.URL + "/v1/"})
	if err != nil {
		t.Fatal(err)
	}
	return models
}

func meliousModelByID(models []AnyModel, id string) *Model {
	for _, model := range models {
		if m, ok := model.(*Model); ok && m.ID == id {
			return m
		}
	}
	return nil
}

func TestMeliousLiveCatalogAppliesPricingAndImageInput(t *testing.T) {
	before := meliousModelByID(GetAllBuiltinModels("melious"), "glm-5.3")
	models := meliousRefreshAgainst(t, `{"object":"list","data":[
		{"id":"glm-5.3","_meta":{"context_length":1048576,"max_completion_tokens":262144,"pricing":{"prompt":"0.0000011","completion":"0.0000035"}}},
		{"id":"deepseek-v4.2-preview","name":"DeepSeek V4.2 Preview","_meta":{"context_length":1000000,"max_completion_tokens":131072,"reasoning":true,"modalities":{"input":["text","image"]},"pricing":{"prompt":"0.0000005","completion":"0.0000015"}}}
	]}`, http.StatusOK)

	glm := meliousModelByID(models, "glm-5.3")
	if glm == nil {
		t.Fatal("glm-5.3 missing")
	}
	if c := glm.Capabilities; c.ContextWindow != 1_048_576 || c.MaxOutputTokens != 262_144 || c.InputCostPer1M != 1.1 || c.OutputCostPer1M != 3.5 || c.CacheReadCostPer1M != 0.12 || c.CacheWriteCostPer1M != 0 {
		t.Fatalf("glm-5.3 capabilities %+v", c)
	}
	// The shared built-in catalog is not changed.
	if after := meliousModelByID(GetAllBuiltinModels("melious"), "glm-5.3"); after.Capabilities.InputCostPer1M != before.Capabilities.InputCostPer1M || after.Capabilities.ContextWindow != before.Capabilities.ContextWindow {
		t.Fatalf("built-in model changed: %+v", after.Capabilities)
	}

	preview := meliousModelByID(models, "deepseek-v4.2-preview")
	if preview == nil {
		t.Fatal("new model not appended")
	}
	c := preview.Capabilities
	if preview.DisplayName != "DeepSeek V4.2 Preview" || preview.ProviderMeta.ProviderID != "melious" || preview.ProviderMeta.API != APIOpenAICompletions ||
		preview.ProviderMeta.BaseURL != meliousBaseURL || !preview.ProviderMeta.Reasoning || !slices.Equal(preview.Input, []ModelInputModality{"text", "image"}) ||
		c.ContextWindow != 1_000_000 || c.MaxOutputTokens != 131_072 || c.InputCostPer1M != 0.5 || c.OutputCostPer1M != 1.5 || c.CacheReadCostPer1M != 0 || c.CacheWriteCostPer1M != 0 {
		t.Fatalf("new model %+v %+v", preview, c)
	}
}

func TestMeliousLiveCatalogKeepsTheStaticCatalogOnABadList(t *testing.T) {
	if models := meliousRefreshAgainst(t, "<html>bad gateway</html>", http.StatusOK); models != nil {
		t.Fatalf("a list that is not JSON gave %d models", len(models))
	}
	if models := meliousRefreshAgainst(t, `{"data":[]}`, http.StatusOK); models != nil {
		t.Fatalf("an empty list gave %d models", len(models))
	}
	if models := meliousRefreshAgainst(t, `{"data":[{"id":"x"}]}`, http.StatusUnauthorized); models != nil {
		t.Fatalf("a 401 gave %d models", len(models))
	}
}

func TestParsePerMillionRate(t *testing.T) {
	for _, c := range []struct {
		raw  string
		want float64
	}{
		{`"0.0000011"`, 1.1}, {`0.25`, 0.25}, {`"2.5 USD"`, 2.5}, {`"abc"`, 9}, {`-1`, 9}, {`null`, 9}, {`0`, 0}, {`true`, 9},
	} {
		if got := parsePerMillionRate([]byte(c.raw), 9); got != c.want {
			t.Errorf("parsePerMillionRate(%s) = %v, want %v", c.raw, got, c.want)
		}
	}
}
