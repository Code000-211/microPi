package coding

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent/checks"
)

// µPi: ports of packages/coding-agent/test/suite/agent-session-checks.test.ts (a18b202f).

func fauxWrite(path, content string) scriptedResponse {
	return func([]ai.Message) *ai.AssistantMessage {
		return &ai.AssistantMessage{
			Content:  []ai.AssistantContentBlock{ai.ToolCall{ID: "call-write-" + path, Name: "write", Arguments: ai.JsonObject{"path": path, "content": content}}},
			Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonToolUse, Timestamp: time.Now().UnixMilli(),
		}
	}
}

// checkReports returns the contents of the session's check-report messages.
func checkReports(h *recoveryHarness) []string {
	var out []string
	for _, message := range h.session.Messages() {
		if message.Custom != nil && message.Custom["customType"] == checks.CheckReportMessageType {
			content, _ := message.Custom["content"].(string)
			out = append(out, content)
			if message.Custom["display"] != true {
				panic("check-report not displayed")
			}
		}
	}
	return out
}

func TestChecksWaitForTheCheckOfAnEditBeforeTheRunEndsAndGoOnWithItsReport(t *testing.T) {
	t.Setenv("PI_CHECKS", "json")
	h := newRecoveryHarness(t, harnessOptions{defaultTools: true},
		fauxWrite("config.json", `{"a": 1,}`),
		fauxReply("Done.", ai.StopReasonStop, 0),
		fauxWrite("config.json", `{"a": 1}`),
		fauxReply("Fixed.", ai.StopReasonStop, 0),
	)
	promptHarness(t, h, "write the config")
	if got := h.provider.callCount(); got != 4 {
		t.Fatalf("model calls = %d, want 4", got)
	}
	found := checkReports(h)
	if len(found) != 1 || !regexp.MustCompile(`config\.json:1:\d+`).MatchString(found[0]) || !regexp.MustCompile(`json check`).MatchString(found[0]) {
		t.Fatalf("reports = %q", found)
	}
	if last := lastAssistant(h); last == nil || last.StopReason != ai.StopReasonStop {
		t.Fatalf("last response = %+v, want a stop", last)
	}
	// The report is a message of the session, not text pasted into one request: the request after it carries it, as do later ones.
	if !projectionContains(h, "New errors after your changes (json check):") {
		t.Fatal("the check report is not in the session")
	}
}

func TestChecksReportLintFindingsOnlyWhenTheRunWouldEnd(t *testing.T) {
	if checks.OnPath("rumdl") == "" {
		t.Skip("rumdl is not on PATH")
	}
	t.Setenv("PI_CHECKS", "markdown")
	h := newRecoveryHarness(t, harnessOptions{defaultTools: true},
		fauxWrite("NOTES.md", "# Notes\n## Part\n"),
		fauxReply("Done.", ai.StopReasonStop, 0),
		fauxReply("Fixed the heading spacing.", ai.StopReasonStop, 0),
	)
	if err := os.WriteFile(filepath.Join(h.session.services.CWD(), ".markdownlint.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	promptHarness(t, h, "write notes")
	if got := h.provider.callCount(); got != 3 {
		t.Fatalf("model calls = %d, want 3", got)
	}
	found := checkReports(h)
	if len(found) != 1 || !regexp.MustCompile(`^Lint findings in what you changed \(markdown`).MatchString(found[0]) || !regexp.MustCompile(`NOTES\.md:1:1 MD022`).MatchString(found[0]) {
		t.Fatalf("reports = %q", found)
	}
}

func TestChecksEndTheRunAsBeforeWhenTheEditsCheckClean(t *testing.T) {
	t.Setenv("PI_CHECKS", "json")
	h := newRecoveryHarness(t, harnessOptions{defaultTools: true},
		fauxWrite("ok.json", `{"a": 1}`),
		fauxReply("Done.", ai.StopReasonStop, 0),
		fauxReply("never reached", ai.StopReasonStop, 0),
	)
	promptHarness(t, h, "write it")
	if found := checkReports(h); len(found) != 0 {
		t.Fatalf("reports = %q, want none", found)
	}
	if got := h.provider.callCount(); got != 2 {
		t.Fatalf("model calls = %d, want 2", got)
	}
}

// stubLinter reports a heading lint finding in each Markdown file whose second line is a heading.
type stubLinter struct{}

func (stubLinter) Name() string             { return "markdown" }
func (stubLinter) Matches(path string) bool { return filepath.Ext(path) == ".md" }
func (l stubLinter) Unit(path string) *checks.CheckUnit {
	return &checks.CheckUnit{Key: "markdown:" + path, Checker: l, CWD: filepath.Dir(path), File: path}
}
func (stubLinter) Run(_ context.Context, unit *checks.CheckUnit) ([]checks.Diagnostic, error) {
	data, err := os.ReadFile(unit.File)
	if err != nil {
		return nil, err
	}
	if lines := strings.Split(string(data), "\n"); len(lines) > 1 && strings.HasPrefix(lines[1], "#") {
		return []checks.Diagnostic{{File: unit.File, Line: 1, Column: 1, Severity: checks.SeverityWarning, Code: "MD022", Message: "Headings should be surrounded by blank lines"}}, nil
	}
	return nil, nil
}

// The lint case above with a stand-in for rumdl, so it runs where rumdl is not installed.
func TestChecksReportLintFindingsOnlyWhenTheRunWouldEndThenGoOn(t *testing.T) {
	cwd := suiteTempDir(t, "pi-checks-")
	bc := checks.NewBackgroundChecks(checks.BackgroundChecksOptions{CWD: cwd, Checkers: []checks.Checker{stubLinter{}}})
	h := newRecoveryHarness(t, harnessOptions{defaultTools: true, backgroundChecks: bc},
		fauxWrite(filepath.Join(cwd, "NOTES.md"), "# Notes\n## Part\n"),
		fauxReply("Done.", ai.StopReasonStop, 0),
		fauxReply("Fixed the heading spacing.", ai.StopReasonStop, 0),
		fauxReply("never reached", ai.StopReasonStop, 0),
	)
	promptHarness(t, h, "write notes")
	if got := h.provider.callCount(); got != 3 {
		t.Fatalf("model calls = %d, want 3", got)
	}
	found := checkReports(h)
	if len(found) != 1 || !strings.HasPrefix(found[0], "Lint findings in what you changed (markdown, as this project configures them); fix them, or say why they stay:") ||
		!strings.Contains(found[0], "NOTES.md:1:1 MD022") {
		t.Fatalf("reports = %q", found)
	}
}
