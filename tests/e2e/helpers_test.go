package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func assertExitZero(t *testing.T, res RunResult) {
	t.Helper()
	if res.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d (err=%v)\nstdout:\n%s\nstderr:\n%s",
			res.ExitCode, res.Err, res.Stdout, res.Stderr)
	}
}

func assertExitNonZero(t *testing.T, res RunResult) {
	t.Helper()
	if res.ExitCode == 0 {
		t.Fatalf("expected non-zero exit code, got 0\nstdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	}
}

func assertContains(t *testing.T, haystack, needle, label string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("%s: expected output to contain %q, got:\n%s", label, needle, haystack)
	}
}

func assertNotContains(t *testing.T, haystack, needle, label string) {
	t.Helper()
	if strings.Contains(haystack, needle) {
		t.Fatalf("%s: expected output NOT to contain %q, got:\n%s", label, needle, haystack)
	}
}

// createManyFiles creates count files in WorkDir under pkg/file_NNN.go so
// >150-file thresholds (docsearch, project snapshot) can be tested hermetically.
func createManyFiles(t *testing.T, env *TestEnv, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		rel := filepath.Join("pkg", fmt.Sprintf("mod_%03d.go", i))
		content := fmt.Sprintf("package pkg\n\nfunc Helper%03d() int { return %d }\n", i, i)
		env.WriteFile(rel, content)
	}
}

// toolResultMessages returns all tool-role messages from a captured LLM request.
func toolResultMessages(req CapturedRequest) []string {
	var out []string
	for _, m := range req.Messages {
		if m.Role == "tool" {
			out = append(out, m.ContentText())
		}
	}
	return out
}

// allMessagesText concatenates all message contents in a captured LLM request.
func allMessagesText(req CapturedRequest) string {
	var sb strings.Builder
	for _, m := range req.Messages {
		sb.WriteString(m.Role)
		sb.WriteString(": ")
		sb.WriteString(m.ContentText())
		sb.WriteString("\n")
	}
	return sb.String()
}

// nonSystemMessagesText concatenates all non-system/non-developer message contents.
func nonSystemMessagesText(req CapturedRequest) string {
	var sb strings.Builder
	for _, m := range req.Messages {
		if m.Role == "system" || m.Role == "developer" {
			continue
		}
		sb.WriteString(m.Role)
		sb.WriteString(": ")
		sb.WriteString(m.ContentText())
		sb.WriteString("\n")
	}
	return sb.String()
}

// hasToolName reports whether the captured request's tool declarations include name.
func hasToolName(req CapturedRequest, name string) bool {
	for _, tn := range req.ToolNames {
		if tn == name {
			return true
		}
	}
	return false
}

// mustJSON marshals v to a JSON string or fails the test.
func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return string(b)
}

// ensureFileNotEmpty verifies a file in WorkDir exists and is non-empty.
func ensureFileNotEmpty(t *testing.T, env *TestEnv, rel string) {
	t.Helper()
	info, err := os.Stat(filepath.Join(env.WorkDir, rel))
	if err != nil {
		t.Fatalf("stat %s: %v", rel, err)
	}
	if info.Size() == 0 {
		t.Fatalf("expected %s to be non-empty", rel)
	}
}
