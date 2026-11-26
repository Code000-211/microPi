package e2e

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHarness_MockLLMTextSSE(t *testing.T) {
	mock := NewMockLLM()
	defer mock.Close()

	mock.EnqueueText("hello from mock")

	payload := `{"model":"mock-coder","stream":true,"messages":[{"role":"system","content":"sys prompt"},{"role":"user","content":"hi"}]}`
	resp, err := http.Post(mock.BaseURL()+"/chat/completions", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("post chat/completions: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("expected SSE content-type, got %q", ct)
	}

	var gotText strings.Builder
	var sawDone bool
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			sawDone = true
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			t.Fatalf("unmarshal SSE chunk: %v", err)
		}
		if len(chunk.Choices) > 0 {
			gotText.WriteString(chunk.Choices[0].Delta.Content)
		}
	}
	if !sawDone {
		t.Fatalf("missing [DONE] sentinel in SSE stream")
	}
	if gotText.String() != "hello from mock" {
		t.Fatalf("expected 'hello from mock', got %q", gotText.String())
	}

	req, ok := mock.LastRequest()
	if !ok {
		t.Fatalf("expected captured request")
	}
	if req.SystemPrompt != "sys prompt" || req.Model != "mock-coder" {
		t.Fatalf("unexpected captured request: %+v", req)
	}
}

func TestHarness_MockLLMToolCallSSE(t *testing.T) {
	mock := NewMockLLM()
	defer mock.Close()

	mock.EnqueueToolCall("call_1", "read", map[string]any{"path": "main.go"})

	payload := `{"model":"mock-coder","stream":true,"messages":[{"role":"user","content":"read main.go"}]}`
	resp, err := http.Post(mock.BaseURL()+"/chat/completions", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	s := string(body)
	if !strings.Contains(s, `"name":"read"`) {
		t.Fatalf("expected tool name 'read' in SSE stream, got: %s", s)
	}
	if !strings.Contains(s, `"finish_reason":"tool_calls"`) {
		t.Fatalf("expected finish_reason tool_calls, got: %s", s)
	}
}

func TestHarness_MockLLMLengthCutOff(t *testing.T) {
	mock := NewMockLLM()
	defer mock.Close()

	mock.EnqueueLengthCutOff("long thinking trace...", "partial answer")

	payload := `{"model":"glm-5.3","stream":true,"reasoning_effort":"high","messages":[{"role":"user","content":"solve"}]}`
	resp, err := http.Post(mock.BaseURL()+"/chat/completions", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	s := string(body)
	if !strings.Contains(s, `"reasoning_content":"long thinking trace..."`) {
		t.Fatalf("expected reasoning_content in SSE stream, got: %s", s)
	}
	if !strings.Contains(s, `"finish_reason":"length"`) {
		t.Fatalf("expected finish_reason length, got: %s", s)
	}
	req, ok := mock.LastRequest()
	if !ok || req.ReasoningEffort != "high" {
		t.Fatalf("expected captured reasoning_effort=high, got %+v", req)
	}
}

func TestHarness_MockLLMMeliousCatalogAndRetry(t *testing.T) {
	mock := NewMockLLM()
	defer mock.Close()

	// Test GET /v1/models?include_meta=true
	resp, err := http.Get(mock.BaseURL() + "/models?include_meta=true")
	if err != nil {
		t.Fatalf("get models: %v", err)
	}
	defer resp.Body.Close()

	var catalog struct {
		Data []MockModel `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&catalog); err != nil {
		t.Fatalf("decode models: %v", err)
	}
	if len(catalog.Data) < 3 {
		t.Fatalf("expected >= 3 models, got %d", len(catalog.Data))
	}
	var foundGLM bool
	for _, mdl := range catalog.Data {
		if mdl.ID == "glm-5.3" && mdl.ContextWindow == 200000 && mdl.InputPrice > 0 {
			foundGLM = true
		}
	}
	if !foundGLM {
		t.Fatalf("expected glm-5.3 with metadata in catalog, got %+v", catalog.Data)
	}

	// Test 429 energy hold error injection followed by success
	mock.EnqueueError(http.StatusTooManyRequests, `{"error":{"message":"Failed to create energy hold"}}`)
	mock.EnqueueText("recovered")

	r1, err := http.Post(mock.BaseURL()+"/chat/completions", "application/json", strings.NewReader(`{"model":"glm-5.3","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatalf("post 1: %v", err)
	}
	b1, _ := io.ReadAll(r1.Body)
	r1.Body.Close()
	if r1.StatusCode != http.StatusTooManyRequests || !strings.Contains(string(b1), "Failed to create energy hold") {
		t.Fatalf("expected 429 energy hold error, got %d: %s", r1.StatusCode, string(b1))
	}

	r2, err := http.Post(mock.BaseURL()+"/chat/completions", "application/json", strings.NewReader(`{"model":"glm-5.3","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatalf("post 2: %v", err)
	}
	b2, _ := io.ReadAll(r2.Body)
	r2.Body.Close()
	if r2.StatusCode != http.StatusOK || !strings.Contains(string(b2), "recovered") {
		t.Fatalf("expected 200 recovered, got %d: %s", r2.StatusCode, string(b2))
	}
}

func TestHarness_IsolatedEnvAndModelsJSON(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-real-secret-should-be-stripped")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-secret-should-be-stripped")

	env := NewTestEnv(t)
	childEnv := env.ChildEnv()

	envMap := make(map[string]string, len(childEnv))
	for _, kv := range childEnv {
		k, v, _ := strings.Cut(kv, "=")
		envMap[k] = v
	}

	if envMap["OPENAI_API_KEY"] != "test-openai-key" {
		t.Fatalf("expected stripped/replaced OPENAI_API_KEY, got %q", envMap["OPENAI_API_KEY"])
	}
	if _, exists := envMap["ANTHROPIC_API_KEY"]; exists {
		t.Fatalf("expected ANTHROPIC_API_KEY to be stripped")
	}
	if envMap["HOME"] != env.Home || envMap["MICROPI_HOME"] != env.MicropiHome {
		t.Fatalf("expected isolated HOME and MICROPI_HOME, got %q / %q", envMap["HOME"], envMap["MICROPI_HOME"])
	}

	modelsPath := filepath.Join(env.AgentDir, "models.json")
	data, err := os.ReadFile(modelsPath)
	if err != nil {
		t.Fatalf("read models.json: %v", err)
	}
	if !strings.Contains(string(data), env.MockLLM.BaseURL()) {
		t.Fatalf("models.json does not reference MockLLM URL %s: %s", env.MockLLM.BaseURL(), string(data))
	}
}

func TestHarness_FakeSSHScript(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteRemoteFile("remote_marker.txt", "hello-remote\n")

	sshBin := env.Env["PI_SSH_COMMAND"]
	if sshBin == "" {
		t.Fatalf("expected PI_SSH_COMMAND to be set")
	}

	cmd := exec.Command(sshBin, "-o", "BatchMode=yes", "devbox", "cat remote_marker.txt")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fake ssh failed: %v\n%s", err, string(out))
	}
	if strings.TrimSpace(string(out)) != "hello-remote" {
		t.Fatalf("expected 'hello-remote', got %q", string(out))
	}

	logData, err := os.ReadFile(env.SSHLogPath)
	if err != nil {
		t.Fatalf("read ssh log: %v", err)
	}
	if !strings.Contains(string(logData), "devbox cat remote_marker.txt") {
		t.Fatalf("expected ssh invocation logged, got %q", string(logData))
	}
}
