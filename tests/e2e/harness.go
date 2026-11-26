package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	resolveOnce     sync.Once
	resolvedBinPath string
	resolveErrMsg   string
	resolveShouldSkip bool
)

// ResolveUPIBinary locates the upi binary for opaque-box E2E execution.
// Resolution order:
//  1. UPI_BIN environment variable
//  2. ../../upi relative to tests/e2e/
//  3. `go build -o <temp>/upi ./cmd/upi` if ../../cmd/upi exists
//
// If none are available: calls t.Fatalf when E2E_REQUIRE_BIN=1, otherwise t.Skip.
func ResolveUPIBinary(t *testing.T) string {
	t.Helper()

	resolveOnce.Do(func() {
		if envBin := strings.TrimSpace(os.Getenv("UPI_BIN")); envBin != "" {
			abs, err := filepath.Abs(envBin)
			if err != nil {
				resolveErrMsg = fmt.Sprintf("invalid UPI_BIN %q: %v", envBin, err)
				return
			}
			info, err := os.Stat(abs)
			if err != nil || info.IsDir() {
				resolveErrMsg = fmt.Sprintf("UPI_BIN %q does not point to an executable file: %v", abs, err)
				return
			}
			resolvedBinPath = abs
			return
		}

		repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
		if err == nil {
			localBin := filepath.Join(repoRoot, "upi")
			if info, statErr := os.Stat(localBin); statErr == nil && !info.IsDir() {
				resolvedBinPath = localBin
				return
			}

			cmdDir := filepath.Join(repoRoot, "cmd", "upi")
			if info, statErr := os.Stat(cmdDir); statErr == nil && info.IsDir() {
				tmpDir, mkErr := os.MkdirTemp("", "upi-e2e-bin-*")
				if mkErr != nil {
					resolveErrMsg = fmt.Sprintf("mkdtemp for upi build: %v", mkErr)
					return
				}
				outBin := filepath.Join(tmpDir, "upi")
				cmd := exec.Command("go", "build", "-o", outBin, "./cmd/upi")
				cmd.Dir = repoRoot
				var out bytes.Buffer
				cmd.Stdout = &out
				cmd.Stderr = &out
				if buildErr := cmd.Run(); buildErr != nil {
					resolveErrMsg = fmt.Sprintf("failed to build ./cmd/upi: %v\n%s", buildErr, out.String())
					return
				}
				resolvedBinPath = outBin
				return
			}
		}

		resolveShouldSkip = true
	})

	if resolveErrMsg != "" {
		t.Fatal(resolveErrMsg)
	}
	if resolveShouldSkip {
		if os.Getenv("E2E_REQUIRE_BIN") == "1" {
			t.Fatalf("upi binary not found (checked UPI_BIN, ../../upi, ../../cmd/upi) and E2E_REQUIRE_BIN=1")
		}
		t.Skip("upi binary not found and ./cmd/upi not present yet")
	}
	return resolvedBinPath
}

// ChatMessage represents a captured OpenAI-compatible message in MockLLM.
type ChatMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	Name       string          `json:"name,omitempty"`
	ToolCalls  json.RawMessage `json:"tool_calls,omitempty"`
}

// ContentText returns the string representation of Content regardless of whether
// the client sent a plain string or an array of content parts.
func (m ChatMessage) ContentText() string {
	if len(m.Content) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(m.Content, &s); err == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(m.Content, &parts); err == nil {
		var sb strings.Builder
		for _, p := range parts {
			if p.Text != "" {
				sb.WriteString(p.Text)
			}
		}
		return sb.String()
	}
	return string(m.Content)
}

// CapturedRequest records an incoming HTTP request to MockLLM.
type CapturedRequest struct {
	Method          string
	Path            string
	RawQuery        string
	Headers         http.Header
	Body            string
	Model           string
	Messages        []ChatMessage
	Tools           []json.RawMessage
	ToolNames       []string
	SystemPrompt    string
	ReasoningEffort string
	MaxTokens       int
	Stream          bool
}

// MockToolCall describes a tool call emitted by MockLLM during SSE streaming.
type MockToolCall struct {
	ID        string
	Name      string
	Arguments string
}

// MockUsage describes optional token usage statistics in the final SSE chunk.
type MockUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// MockTurn defines a single scripted response from MockLLM.
type MockTurn struct {
	StatusCode      int
	ErrorBody       string
	RetryAfter      string
	TextChunks      []string
	ReasoningChunks []string
	ToolCalls       []MockToolCall
	FinishReason    string
	Delay           time.Duration
	Usage           *MockUsage
}

// MockModel defines an entry returned by GET /v1/models.
type MockModel struct {
	ID            string  `json:"id"`
	Object        string  `json:"object"`
	OwnedBy       string  `json:"owned_by,omitempty"`
	ContextWindow int     `json:"context_window,omitempty"`
	MaxTokens     int     `json:"max_tokens,omitempty"`
	InputPrice    float64 `json:"input_price,omitempty"`
	OutputPrice   float64 `json:"output_price,omitempty"`
}

// MockLLM is a hermetic OpenAI-compatible + Melious catalog httptest server.
type MockLLM struct {
	Server *httptest.Server

	mu           sync.Mutex
	requests     []CapturedRequest
	turns        []MockTurn
	handler      func(req CapturedRequest, turn int) MockTurn
	turnCount    int
	models       []MockModel
	modelsStatus int
}

// NewMockLLM starts a hermetic MockLLM server.
func NewMockLLM() *MockLLM {
	m := &MockLLM{
		modelsStatus: http.StatusOK,
		models: []MockModel{
			{
				ID:            "mock-coder",
				Object:        "model",
				OwnedBy:       "mock",
				ContextWindow: 128000,
				MaxTokens:     16384,
				InputPrice:    0.1,
				OutputPrice:   0.2,
			},
			{
				ID:            "glm-5.3",
				Object:        "model",
				OwnedBy:       "melious",
				ContextWindow: 200000,
				MaxTokens:     65536,
				InputPrice:    0.15,
				OutputPrice:   0.60,
			},
			{
				ID:            "glm-5.3-flash",
				Object:        "model",
				OwnedBy:       "melious",
				ContextWindow: 200000,
				MaxTokens:     65536,
				InputPrice:    0.05,
				OutputPrice:   0.20,
			},
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", m.handleChatCompletions)
	mux.HandleFunc("/v1/models", m.handleModels)
	m.Server = httptest.NewServer(mux)
	return m
}

// Close shuts down the MockLLM server.
func (m *MockLLM) Close() {
	if m.Server != nil {
		m.Server.Close()
	}
}

// BaseURL returns the OpenAI-compatible /v1 base URL.
func (m *MockLLM) BaseURL() string {
	return m.Server.URL + "/v1"
}

// Enqueue appends scripted turns to the response queue.
func (m *MockLLM) Enqueue(turns ...MockTurn) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.turns = append(m.turns, turns...)
}

// EnqueueText appends a simple text response turn.
func (m *MockLLM) EnqueueText(text string) {
	m.Enqueue(MockTurn{
		TextChunks:   []string{text},
		FinishReason: "stop",
	})
}

// EnqueueToolCall appends a single tool call turn.
func (m *MockLLM) EnqueueToolCall(id, name string, args any) {
	rawArgs := "{}"
	switch v := args.(type) {
	case string:
		rawArgs = v
	default:
		b, _ := json.Marshal(v)
		rawArgs = string(b)
	}
	m.Enqueue(MockTurn{
		ToolCalls: []MockToolCall{{
			ID:        id,
			Name:      name,
			Arguments: rawArgs,
		}},
		FinishReason: "tool_calls",
	})
}

// EnqueueToolCalls appends a turn emitting multiple tool calls in order.
func (m *MockLLM) EnqueueToolCalls(calls ...MockToolCall) {
	m.Enqueue(MockTurn{
		ToolCalls:    calls,
		FinishReason: "tool_calls",
	})
}

// EnqueueLengthCutOff appends a turn with reasoning_content and finish_reason: "length".
func (m *MockLLM) EnqueueLengthCutOff(reasoning, partialText string) {
	turn := MockTurn{
		FinishReason: "length",
	}
	if reasoning != "" {
		turn.ReasoningChunks = []string{reasoning}
	}
	if partialText != "" {
		turn.TextChunks = []string{partialText}
	}
	m.Enqueue(turn)
}

// EnqueueError appends an HTTP error response turn.
func (m *MockLLM) EnqueueError(statusCode int, body string) {
	m.Enqueue(MockTurn{
		StatusCode: statusCode,
		ErrorBody:  body,
	})
}

// SetHandler sets a dynamic response callback when the queue is empty.
func (m *MockLLM) SetHandler(fn func(req CapturedRequest, turn int) MockTurn) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.handler = fn
}

// SetModels overrides the models returned by GET /v1/models.
func (m *MockLLM) SetModels(models []MockModel) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.models = append([]MockModel(nil), models...)
}

// SetModelsStatus sets the HTTP status code returned by GET /v1/models.
func (m *MockLLM) SetModelsStatus(status int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.modelsStatus = status
}

// CapturedRequests returns a copy of all captured requests.
func (m *MockLLM) CapturedRequests() []CapturedRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]CapturedRequest, len(m.requests))
	copy(out, m.requests)
	return out
}

// LastRequest returns the most recently captured request, if any.
func (m *MockLLM) LastRequest() (CapturedRequest, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.requests) == 0 {
		return CapturedRequest{}, false
	}
	return m.requests[len(m.requests)-1], true
}

// Reset clears captured requests and queued turns.
func (m *MockLLM) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = nil
	m.turns = nil
	m.turnCount = 0
}

func (m *MockLLM) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	m.mu.Lock()
	m.requests = append(m.requests, CapturedRequest{
		Method:   r.Method,
		Path:     r.URL.Path,
		RawQuery: r.URL.RawQuery,
		Headers:  r.Header.Clone(),
	})
	status := m.modelsStatus
	models := append([]MockModel(nil), m.models...)
	m.mu.Unlock()

	if status != 0 && status != http.StatusOK {
		http.Error(w, `{"error":{"message":"models endpoint error"}}`, status)
		return
	}

	includeMeta := r.URL.Query().Get("include_meta") == "true"
	type modelPayload struct {
		ID            string  `json:"id"`
		Object        string  `json:"object"`
		OwnedBy       string  `json:"owned_by,omitempty"`
		ContextWindow int     `json:"context_window,omitempty"`
		MaxTokens     int     `json:"max_tokens,omitempty"`
		InputPrice    float64 `json:"input_price,omitempty"`
		OutputPrice   float64 `json:"output_price,omitempty"`
	}
	data := make([]modelPayload, 0, len(models))
	for _, mdl := range models {
		item := modelPayload{
			ID:      mdl.ID,
			Object:  "model",
			OwnedBy: mdl.OwnedBy,
		}
		if includeMeta {
			item.ContextWindow = mdl.ContextWindow
			item.MaxTokens = mdl.MaxTokens
			item.InputPrice = mdl.InputPrice
			item.OutputPrice = mdl.OutputPrice
		}
		data = append(data, item)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"object": "list",
		"data":   data,
	})
}

func (m *MockLLM) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	var parsed struct {
		Model           string            `json:"model"`
		Messages        []ChatMessage     `json:"messages"`
		Tools           []json.RawMessage `json:"tools"`
		ReasoningEffort string            `json:"reasoning_effort"`
		MaxTokens       int               `json:"max_tokens"`
		Stream          bool              `json:"stream"`
	}
	_ = json.Unmarshal(rawBody, &parsed)

	var sysBuilder strings.Builder
	for _, msg := range parsed.Messages {
		if msg.Role == "system" || msg.Role == "developer" {
			if sysBuilder.Len() > 0 {
				sysBuilder.WriteString("\n")
			}
			sysBuilder.WriteString(msg.ContentText())
		}
	}

	var toolNames []string
	for _, rawTool := range parsed.Tools {
		var toolDesc struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
			Name string `json:"name"`
		}
		if json.Unmarshal(rawTool, &toolDesc) == nil {
			if toolDesc.Function.Name != "" {
				toolNames = append(toolNames, toolDesc.Function.Name)
			} else if toolDesc.Name != "" {
				toolNames = append(toolNames, toolDesc.Name)
			}
		}
	}

	capReq := CapturedRequest{
		Method:          r.Method,
		Path:            r.URL.Path,
		RawQuery:        r.URL.RawQuery,
		Headers:         r.Header.Clone(),
		Body:            string(rawBody),
		Model:           parsed.Model,
		Messages:        parsed.Messages,
		Tools:           parsed.Tools,
		ToolNames:       toolNames,
		SystemPrompt:    sysBuilder.String(),
		ReasoningEffort: parsed.ReasoningEffort,
		MaxTokens:       parsed.MaxTokens,
		Stream:          parsed.Stream,
	}

	m.mu.Lock()
	m.requests = append(m.requests, capReq)
	turnIdx := m.turnCount
	m.turnCount++

	var turn MockTurn
	if len(m.turns) > 0 {
		turn = m.turns[0]
		m.turns = m.turns[1:]
	} else if m.handler != nil {
		turn = m.handler(capReq, turnIdx)
	} else {
		turn = MockTurn{
			TextChunks:   []string{"ok"},
			FinishReason: "stop",
		}
	}
	m.mu.Unlock()

	if turn.Delay > 0 {
		time.Sleep(turn.Delay)
	}

	if turn.StatusCode >= 400 {
		if turn.RetryAfter != "" {
			w.Header().Set("Retry-After", turn.RetryAfter)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(turn.StatusCode)
		body := turn.ErrorBody
		if body == "" {
			body = fmt.Sprintf(`{"error":{"message":"mock error %d"}}`, turn.StatusCode)
		}
		_, _ = io.WriteString(w, body)
		return
	}

	finishReason := turn.FinishReason
	if finishReason == "" {
		if len(turn.ToolCalls) > 0 {
			finishReason = "tool_calls"
		} else {
			finishReason = "stop"
		}
	}
	modelName := parsed.Model
	if modelName == "" {
		modelName = "mock-coder"
	}

	if !parsed.Stream {
		w.Header().Set("Content-Type", "application/json")
		var content strings.Builder
		for _, c := range turn.TextChunks {
			content.WriteString(c)
		}
		var reasoning strings.Builder
		for _, rc := range turn.ReasoningChunks {
			reasoning.WriteString(rc)
		}
		msg := map[string]any{
			"role":    "assistant",
			"content": content.String(),
		}
		if reasoning.Len() > 0 {
			msg["reasoning_content"] = reasoning.String()
		}
		if len(turn.ToolCalls) > 0 {
			tcs := make([]map[string]any, 0, len(turn.ToolCalls))
			for _, tc := range turn.ToolCalls {
				tcs = append(tcs, map[string]any{
					"id":   tc.ID,
					"type": "function",
					"function": map[string]any{
						"name":      tc.Name,
						"arguments": tc.Arguments,
					},
				})
			}
			msg["tool_calls"] = tcs
		}
		resp := map[string]any{
			"id":      fmt.Sprintf("chatcmpl-mock-%d", turnIdx),
			"object":  "chat.completion",
			"created": time.Now().Unix(),
			"model":   modelName,
			"choices": []map[string]any{
				{
					"index":         0,
					"message":       msg,
					"finish_reason": finishReason,
				},
			},
		}
		if turn.Usage != nil {
			resp["usage"] = turn.Usage
		}
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	// Stream OpenAI SSE response
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, _ := w.(http.Flusher)

	writeChunk := func(payload map[string]any) {
		b, _ := json.Marshal(payload)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
		if flusher != nil {
			flusher.Flush()
		}
	}

	chunkID := fmt.Sprintf("chatcmpl-mock-%d", turnIdx)
	created := time.Now().Unix()

	// Initial role chunk
	writeChunk(map[string]any{
		"id":      chunkID,
		"object":  "chat.completion.chunk",
		"created": created,
		"model":   modelName,
		"choices": []map[string]any{
			{
				"index":         0,
				"delta":         map[string]any{"role": "assistant"},
				"finish_reason": nil,
			},
		},
	})

	for _, rc := range turn.ReasoningChunks {
		writeChunk(map[string]any{
			"id":      chunkID,
			"object":  "chat.completion.chunk",
			"created": created,
			"model":   modelName,
			"choices": []map[string]any{
				{
					"index":         0,
					"delta":         map[string]any{"reasoning_content": rc},
					"finish_reason": nil,
				},
			},
		})
	}

	for _, tc := range turn.TextChunks {
		writeChunk(map[string]any{
			"id":      chunkID,
			"object":  "chat.completion.chunk",
			"created": created,
			"model":   modelName,
			"choices": []map[string]any{
				{
					"index":         0,
					"delta":         map[string]any{"content": tc},
					"finish_reason": nil,
				},
			},
		})
	}

	for idx, call := range turn.ToolCalls {
		writeChunk(map[string]any{
			"id":      chunkID,
			"object":  "chat.completion.chunk",
			"created": created,
			"model":   modelName,
			"choices": []map[string]any{
				{
					"index": 0,
					"delta": map[string]any{
						"tool_calls": []map[string]any{
							{
								"index": idx,
								"id":    call.ID,
								"type":  "function",
								"function": map[string]any{
									"name":      call.Name,
									"arguments": call.Arguments,
								},
							},
						},
					},
					"finish_reason": nil,
				},
			},
		})
	}

	finalChunk := map[string]any{
		"id":      chunkID,
		"object":  "chat.completion.chunk",
		"created": created,
		"model":   modelName,
		"choices": []map[string]any{
			{
				"index":         0,
				"delta":         map[string]any{},
				"finish_reason": finishReason,
			},
		},
	}
	if turn.Usage != nil {
		finalChunk["usage"] = turn.Usage
	}
	writeChunk(finalChunk)

	_, _ = io.WriteString(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

// TestEnv provides an isolated, hermetic environment for running upi E2E tests.
type TestEnv struct {
	T           *testing.T
	Home        string
	MicropiHome string
	AgentDir    string
	SessionDir  string
	WorkDir     string
	FakeSSHDir  string
	RemoteDir   string
	SSHLogPath  string
	MockLLM     *MockLLM
	Env         map[string]string
	Timeout     time.Duration
}

// NewTestEnv creates isolated temp directories, starts MockLLM, writes models.json,
// and configures a clean environment with real API keys stripped.
func NewTestEnv(t *testing.T) *TestEnv {
	t.Helper()

	root := t.TempDir()
	env := &TestEnv{
		T:           t,
		Home:        filepath.Join(root, "home"),
		MicropiHome: filepath.Join(root, "micropi-home"),
		AgentDir:    filepath.Join(root, "agent-dir"),
		SessionDir:  filepath.Join(root, "sessions"),
		WorkDir:     filepath.Join(root, "work"),
		FakeSSHDir:  filepath.Join(root, "fake-ssh"),
		RemoteDir:   filepath.Join(root, "remote"),
		SSHLogPath:  filepath.Join(root, "fake-ssh", "ssh.log"),
		MockLLM:     NewMockLLM(),
		Env:         make(map[string]string),
		Timeout:     30 * time.Second,
	}

	for _, dir := range []string{
		env.Home,
		env.MicropiHome,
		env.AgentDir,
		env.SessionDir,
		env.WorkDir,
		env.FakeSSHDir,
		env.RemoteDir,
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	t.Cleanup(func() {
		env.MockLLM.Close()
	})

	env.WriteModelsJSON()
	env.SetupFakeSSH()
	return env
}

// WriteModelsJSON writes models.json into AgentDir and MicropiHome pointing to MockLLM.
func (e *TestEnv) WriteModelsJSON() {
	e.T.Helper()

	cfg := map[string]any{
		"providers": map[string]any{
			"mock": map[string]any{
				"baseUrl": e.MockLLM.BaseURL(),
				"apiKey":  "test-mock-key",
				"api":     "openai-completions",
				"models": []map[string]any{
					{
						"id":            "mock-coder",
						"name":          "Mock Coder",
						"reasoning":     true,
						"contextWindow": 128000,
						"maxTokens":     16384,
					},
				},
			},
			"melious": map[string]any{
				"baseUrl": e.MockLLM.BaseURL(),
				"apiKey":  "test-melious-key",
				"api":     "openai-completions",
				"models": []map[string]any{
					{
						"id":            "glm-5.3",
						"name":          "GLM 5.3",
						"reasoning":     true,
						"contextWindow": 200000,
						"maxTokens":     65536,
					},
					{
						"id":            "glm-5.3-flash",
						"name":          "GLM 5.3 Flash",
						"reasoning":     true,
						"contextWindow": 200000,
						"maxTokens":     65536,
					},
				},
			},
		},
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		e.T.Fatalf("marshal models.json: %v", err)
	}

	for _, dir := range []string{
		e.AgentDir,
		e.MicropiHome,
		filepath.Join(e.MicropiHome, "agent"),
		filepath.Join(e.Home, ".micropi", "agent"),
	} {
		if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
			e.T.Fatalf("mkdir %s: %v", dir, mkErr)
		}
		p := filepath.Join(dir, "models.json")
		if err := os.WriteFile(p, data, 0o644); err != nil {
			e.T.Fatalf("write %s: %v", p, err)
		}
	}
}

// SetupFakeSSH writes an executable fake `ssh` script in FakeSSHDir and configures PI_SSH_COMMAND.
func (e *TestEnv) SetupFakeSSH() {
	e.T.Helper()

	sshPath := filepath.Join(e.FakeSSHDir, "ssh")
	script := fmt.Sprintf(`#!/usr/bin/env bash
set -euo pipefail
LOG_FILE=%q
REMOTE_DIR=%q

printf '%%s\n' "$*" >> "$LOG_FILE"

# Handle ControlMaster lifecycle queries hermetically
for arg in "$@"; do
  if [ "$arg" = "check" ] || [ "$arg" = "exit" ]; then
    exit 0
  fi
done

# Parse standard ssh flags to find host and optional remote command
CMD_ARGS=()
SKIP_NEXT=0
FOUND_HOST=0
for arg in "$@"; do
  if [ "$SKIP_NEXT" = "1" ]; then
    SKIP_NEXT=0
    continue
  fi
  if [ "$FOUND_HOST" = "1" ]; then
    CMD_ARGS+=("$arg")
    continue
  fi
  case "$arg" in
    -o|-S|-p|-i|-l|-F|-J|-W|-L|-R|-D|-b|-c|-e|-m|-w)
      SKIP_NEXT=1
      ;;
    -*)
      ;;
    *)
      FOUND_HOST=1
      ;;
  esac
done

if [ "${#CMD_ARGS[@]}" -eq 0 ]; then
  # Master background connection (-N -f etc.)
  exit 0
fi

cd "$REMOTE_DIR"
if [ "${#CMD_ARGS[@]}" -eq 1 ]; then
  exec bash -c "${CMD_ARGS[0]}"
else
  exec bash -c "${CMD_ARGS[*]}"
fi
`, e.SSHLogPath, e.RemoteDir)

	if err := os.WriteFile(sshPath, []byte(script), 0o755); err != nil {
		e.T.Fatalf("write fake ssh: %v", err)
	}
	e.Env["PI_SSH_COMMAND"] = sshPath
}

// WriteFile creates or overwrites a file relative to WorkDir.
func (e *TestEnv) WriteFile(relPath, content string) string {
	e.T.Helper()
	full := filepath.Join(e.WorkDir, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		e.T.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		e.T.Fatalf("write %s: %v", full, err)
	}
	return full
}

// WriteRemoteFile creates or overwrites a file relative to RemoteDir.
func (e *TestEnv) WriteRemoteFile(relPath, content string) string {
	e.T.Helper()
	full := filepath.Join(e.RemoteDir, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		e.T.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		e.T.Fatalf("write %s: %v", full, err)
	}
	return full
}

// ReadFile reads a file relative to WorkDir.
func (e *TestEnv) ReadFile(relPath string) string {
	e.T.Helper()
	b, err := os.ReadFile(filepath.Join(e.WorkDir, relPath))
	if err != nil {
		e.T.Fatalf("read %s: %v", relPath, err)
	}
	return string(b)
}

// ReadRemoteFile reads a file relative to RemoteDir.
func (e *TestEnv) ReadRemoteFile(relPath string) string {
	e.T.Helper()
	b, err := os.ReadFile(filepath.Join(e.RemoteDir, relPath))
	if err != nil {
		e.T.Fatalf("read remote %s: %v", relPath, err)
	}
	return string(b)
}

// FileExists reports whether a file exists relative to WorkDir.
func (e *TestEnv) FileExists(relPath string) bool {
	_, err := os.Stat(filepath.Join(e.WorkDir, relPath))
	return err == nil
}

// InitGitRepo initializes a git repository in WorkDir with an initial commit.
func (e *TestEnv) InitGitRepo() {
	e.T.Helper()
	cmds := [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "e2e@micropi.test"},
		{"git", "config", "user.name", "E2E Test"},
		{"git", "add", "."},
		{"git", "commit", "--allow-empty", "-m", "initial"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = e.WorkDir
		if out, err := cmd.CombinedOutput(); err != nil {
			e.T.Fatalf("git %v failed: %v\n%s", args, err, string(out))
		}
	}
}

// ChildEnv returns a sanitized process environment for child processes.
// Real provider API keys are stripped and replaced with dummy test values.
func (e *TestEnv) ChildEnv() []string {
	stripped := map[string]bool{
		"OPENAI_API_KEY":     true,
		"ANTHROPIC_API_KEY":  true,
		"MELIOUS_API_KEY":    true,
		"GEMINI_API_KEY":     true,
		"GROQ_API_KEY":       true,
		"XAI_API_KEY":        true,
		"OPENROUTER_API_KEY": true,
		"MISTRAL_API_KEY":    true,
		"DEEPSEEK_API_KEY":   true,
		"CEREBRAS_API_KEY":   true,
		"AZURE_OPENAI_KEY":   true,
	}

	base := make([]string, 0, len(os.Environ())+16)
	for _, kv := range os.Environ() {
		k, _, ok := strings.Cut(kv, "=")
		if !ok || stripped[k] {
			continue
		}
		base = append(base, kv)
	}

	defaults := map[string]string{
		"HOME":                e.Home,
		"MICROPI_HOME":        e.MicropiHome,
		"PI_CODING_AGENT_DIR": e.AgentDir,
		"PI_SESSION_DIR":      e.SessionDir,
		"OPENAI_API_KEY":      "test-openai-key",
		"MELIOUS_API_KEY":     "test-melious-key",
		"OPENAI_BASE_URL":     e.MockLLM.BaseURL(),
		"PI_TELEMETRY":        "0",
	}
	for k, v := range defaults {
		base = append(base, k+"="+v)
	}
	for k, v := range e.Env {
		base = append(base, k+"="+v)
	}
	return base
}

// RunResult captures the result of running the upi CLI.
type RunResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
	Err      error
}

// RunUPI executes the upi binary in WorkDir with the configured timeout.
func (e *TestEnv) RunUPI(args ...string) RunResult {
	e.T.Helper()
	return e.RunUPIInDir(e.WorkDir, args...)
}

// RunUPIInDir executes the upi binary in a specified directory.
func (e *TestEnv) RunUPIInDir(dir string, args ...string) RunResult {
	e.T.Helper()
	bin := ResolveUPIBinary(e.T)

	ctx, cancel := context.WithTimeout(context.Background(), e.Timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = e.ChildEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}
	return RunResult{
		ExitCode: exitCode,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Err:      err,
	}
}

// JSONEvent represents a parsed JSONL line from `--mode json` or `--mode rpc`.
type JSONEvent struct {
	Raw  string
	Type string
	Data map[string]any
}

// RunUPIJSON executes upi with `--mode json` prepended (if not already present)
// and parses all JSONL lines from stdout.
func (e *TestEnv) RunUPIJSON(args ...string) (RunResult, []JSONEvent) {
	e.T.Helper()
	hasMode := false
	for _, a := range args {
		if a == "--mode" {
			hasMode = true
			break
		}
	}
	fullArgs := args
	if !hasMode {
		fullArgs = append([]string{"--mode", "json"}, args...)
	}
	res := e.RunUPI(fullArgs...)
	events := parseJSONLines(res.Stdout)
	return res, events
}

func parseJSONLines(output string) []JSONEvent {
	var events []JSONEvent
	sc := bufio.NewScanner(strings.NewReader(output))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			continue
		}
		typ, _ := m["type"].(string)
		events = append(events, JSONEvent{
			Raw:  line,
			Type: typ,
			Data: m,
		})
	}
	return events
}

// RPCSession manages an interactive `upi --mode rpc` subprocess.
type RPCSession struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	events    chan JSONEvent
	cancel    context.CancelFunc
	done      chan error
	closeOnce sync.Once
	closeErr  error
}

// StartRPCSession launches `upi --mode rpc` with clean process cleanup on test end.
func (e *TestEnv) StartRPCSession(args ...string) *RPCSession {
	e.T.Helper()
	bin := ResolveUPIBinary(e.T)

	fullArgs := append([]string{"--mode", "rpc"}, args...)
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin, fullArgs...)
	cmd.Dir = e.WorkDir
	cmd.Env = e.ChildEnv()

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		e.T.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		e.T.Fatalf("stdout pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		e.T.Fatalf("start rpc session: %v", err)
	}

	sess := &RPCSession{
		cmd:    cmd,
		stdin:  stdin,
		events: make(chan JSONEvent, 128),
		cancel: cancel,
		done:   make(chan error, 1),
	}

	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			var m map[string]any
			if json.Unmarshal([]byte(line), &m) == nil {
				typ, _ := m["type"].(string)
				sess.events <- JSONEvent{Raw: line, Type: typ, Data: m}
			}
		}
		close(sess.events)
	}()

	go func() {
		sess.done <- cmd.Wait()
	}()

	e.T.Cleanup(func() {
		_ = sess.Close()
	})
	return sess
}

// Send writes a JSON command line to the RPC subprocess stdin.
func (s *RPCSession) Send(cmd map[string]any) error {
	b, err := json.Marshal(cmd)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(s.stdin, "%s\n", b)
	return err
}

// Prompt sends an RPC prompt command.
func (s *RPCSession) Prompt(message string) error {
	return s.Send(map[string]any{
		"type":    "prompt",
		"message": message,
	})
}

// ReadEvent waits for the next RPC event up to timeout.
func (s *RPCSession) ReadEvent(timeout time.Duration) (JSONEvent, error) {
	select {
	case ev, ok := <-s.events:
		if !ok {
			return JSONEvent{}, io.EOF
		}
		return ev, nil
	case <-time.After(timeout):
		return JSONEvent{}, fmt.Errorf("timed out waiting for RPC event after %s", timeout)
	}
}

// WaitForAgentEnd collects events until `agent_end` or `turn_end` completes or times out.
func (s *RPCSession) WaitForAgentEnd(timeout time.Duration) ([]JSONEvent, error) {
	deadline := time.After(timeout)
	var collected []JSONEvent
	for {
		select {
		case ev, ok := <-s.events:
			if !ok {
				return collected, io.EOF
			}
			collected = append(collected, ev)
			if ev.Type == "agent_settled" {
				return collected, nil
			}
		case <-deadline:
			return collected, fmt.Errorf("timed out waiting for agent_end after %s", timeout)
		}
	}
}

// Close terminates the RPC session cleanly.
func (s *RPCSession) Close() error {
	s.closeOnce.Do(func() {
		_ = s.stdin.Close()
		s.cancel()
		select {
		case err := <-s.done:
			s.closeErr = err
		case <-time.After(2 * time.Second):
			if s.cmd.Process != nil {
				_ = s.cmd.Process.Kill()
			}
			s.closeErr = <-s.done
		}
	})
	return s.closeErr
}
