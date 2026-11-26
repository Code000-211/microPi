package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent/jobs"
)

var (
	// WaitLadder defines the progressive wait durations for back-to-back empty waits.
	WaitLadder = [3]time.Duration{
		120 * time.Second,
		300 * time.Second,
		600 * time.Second,
	}
	waitLadderReset    = 60 * time.Second
	jobReportMsgWindow = 60 * time.Second
)

type waitIdleState struct {
	rung     int
	endedAt  time.Time
	waitedMs time.Duration
	jobIDs   []string
}

// WaitTool blocks until a background job finishes, wakes on user input, or times out along WaitLadder.
type WaitTool struct {
	BackgroundJobs *jobs.Manager

	mu   sync.Mutex
	idle *waitIdleState
}

func (t *WaitTool) Name() string  { return "wait" }
func (t *WaitTool) Label() string { return "wait" }

func (t *WaitTool) Schema() ai.ToolSchema {
	return toolSchemaWithParameters(ai.ToolSchema{
		Name:        "wait",
		Description: "Wait for one of your background jobs to end and get its report. Only when nothing else is left to do.",
	}, `{"type":"object","properties":{}}`, false)
}

func (t *WaitTool) ExecutionMode() agent.ToolExecutionMode { return agent.ToolModeParallel }

func (t *WaitTool) Execute(ctx context.Context, _ string, _ json.RawMessage, _ agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	mgr := t.BackgroundJobs
	runningIDs := func() []string {
		if mgr == nil {
			return nil
		}
		running := mgr.Running()
		ids := make([]string, len(running))
		for i, j := range running {
			ids[i] = j.ID
		}
		return ids
	}

	t.mu.Lock()
	var streak *waitIdleState
	if t.idle != nil {
		curRunning := runningIDs()
		held := time.Since(t.idle.endedAt) < waitLadderReset
		if !held {
			for _, id := range t.idle.jobIDs {
				if slices.Contains(curRunning, id) {
					held = true
					break
				}
			}
		}
		if held {
			streak = t.idle
		}
		t.idle = nil
	}
	t.mu.Unlock()

	report := func() (agent.AgentToolResult, bool) {
		if mgr == nil {
			return agent.AgentToolResult{}, false
		}
		ended := mgr.TakeUnreported(jobs.ReportedWait)
		if len(ended) == 0 {
			return agent.AgentToolResult{}, false
		}
		ids := make([]string, len(ended))
		for i, j := range ended {
			ids[i] = j.ID
		}
		return agent.AgentToolResult{
			Content: []ai.ToolResultMessageContent{ai.TextContent{Text: jobs.FormatJobReport(ended)}},
			Details: map[string]any{"jobs": ids},
		}, true
	}

	if res, ok := report(); ok {
		return res, nil
	}

	if mgr == nil || len(mgr.Running()) == 0 {
		if mgr != nil {
			var messaged []jobs.Job
			for _, j := range mgr.List() {
				if j.ReportedBy == jobs.ReportedMessage && j.EndedAt != nil && time.Since(*j.EndedAt) < jobReportMsgWindow {
					messaged = append(messaged, j)
				}
			}
			if len(messaged) > 0 {
				named := make([]string, len(messaged))
				ids := make([]string, len(messaged))
				for i, j := range messaged {
					named[i] = fmt.Sprintf("%s (`%s`)", j.ID, j.Command)
					ids[i] = j.ID
				}
				var msg string
				if len(messaged) == 1 {
					msg = fmt.Sprintf("Nothing to wait for: no background job is running. The report of %s came as its own message just now.", strings.Join(named, ", "))
				} else {
					msg = fmt.Sprintf("Nothing to wait for: no background job is running. The reports of %s came as their own messages just now.", strings.Join(named, ", "))
				}
				return agent.AgentToolResult{
					Content: []ai.ToolResultMessageContent{ai.TextContent{Text: msg}},
					Details: map[string]any{"jobs": ids},
				}, nil
			}
		}
		return agent.AgentToolResult{
			Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "Nothing to wait for: no background job is running."}},
			Details: map[string]any{"jobs": []string{}},
		}, nil
	}

	rung := 0
	if streak != nil {
		rung = min(streak.rung+1, len(WaitLadder)-1)
	}
	outcome := mgr.WaitForEnd(ctx, WaitLadder[rung])
	if res, ok := report(); ok {
		return res, nil
	}
	if outcome == jobs.OutcomeAborted {
		return agent.AgentToolResult{}, errors.New("Wait aborted")
	}
	if outcome == jobs.OutcomeWoken {
		return agent.AgentToolResult{
			Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "Wait ended for a message from the user."}},
			Details: map[string]any{"jobs": []string{}},
		}, nil
	}

	var prevWaited time.Duration
	if streak != nil {
		prevWaited = streak.waitedMs
	}
	waited := prevWaited + WaitLadder[rung]
	t.mu.Lock()
	t.idle = &waitIdleState{
		rung:     rung,
		endedAt:  time.Now(),
		waitedMs: waited,
		jobIDs:   runningIDs(),
	}
	t.mu.Unlock()

	msg := fmt.Sprintf("No background job ended within %d s. Running: %s.", int(WaitLadder[rung]/time.Second), jobs.DescribeRunningJobs(mgr.Running(), time.Now()))
	if rung >= 1 {
		msg += fmt.Sprintf("\n\nNothing ended after %d s of back-to-back waits. Waiting is not progress: go on with everything that does not need these jobs, and call wait again only when nothing else is left. Their reports come on their own.", int(waited.Round(time.Second)/time.Second))
	}
	return agent.AgentToolResult{
		Content: []ai.ToolResultMessageContent{ai.TextContent{Text: msg}},
		Details: map[string]any{"jobs": []string{}},
	}, nil
}
