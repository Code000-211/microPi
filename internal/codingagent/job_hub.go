// Job hub: the session's background jobs, as in oh-my-pi's agent hub, without subagents.
//
// A list of the jobs, newest first, with the selected one's details and the end of its output below; Enter shows its
// whole output (what the bash tool keeps: the first lines and the end), x stops it. Running jobs update live.
//
// Ports microPi packages/coding-agent/src/modes/interactive/components/job-hub.ts.

package codingagent

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/internal/codingagent/jobs"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// jobHubRefresh is how often the hub draws again while it is open: running times and output change on their own.
const jobHubRefresh = 500 * time.Millisecond

// jobHubBackgroundAfter is the bash tool's default time before a command moves to the background (tools defaultBashBackgroundAfter,
// Pi DEFAULT_AUTO_BACKGROUND_SECONDS), named in the empty-list hint.
const jobHubBackgroundAfter = 120 * time.Second

// jobStatusKey is the footer status key of the running-jobs line.
const jobStatusKey = "pi-jobs"

type jobStatusStyle struct {
	mark  string
	color string
}

var jobStatusStyles = map[jobs.JobStatus]jobStatusStyle{
	jobs.StatusRunning:  {mark: "●", color: "accent"},
	jobs.StatusDone:     {mark: "✓", color: "success"},
	jobs.StatusFailed:   {mark: "✗", color: "error"},
	jobs.StatusTimedOut: {mark: "⏱", color: "warning"},
	jobs.StatusKilled:   {mark: "■", color: "muted"},
}

// formatJobDuration formats d as "12s", "3m 4s" or "1h 2m".
func formatJobDuration(d time.Duration) string {
	s := max(0, int(d/time.Second))
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	m := s / 60
	if m < 60 {
		return fmt.Sprintf("%dm %ds", m, s%60)
	}
	return fmt.Sprintf("%dh %dm", m/60, m%60)
}

func jobStatusLabel(job jobs.Job) string {
	if job.Status == jobs.StatusFailed && job.ExitCode != nil {
		return "exit " + strconv.Itoa(*job.ExitCode)
	}
	return string(job.Status)
}

func jobHubPadded(text string, width int) string {
	return widthx.TruncateToWidth(text, width, "...", true)
}

func oneLineCommand(command string) string {
	return strings.Join(strings.Fields(command), " ")
}

func jobOutputLines(output string) []string {
	return strings.Split(strings.TrimSuffix(output, "\n"), "\n")
}

func jobClockTime(t time.Time) string {
	return t.Format("3:04:05 PM")
}

// JobHubOptions configures a JobHubComponent.
type JobHubOptions struct {
	// Rows is the number of rows the hub may take.
	Rows func() int
	// RequestRender asks for a frame. It is called from the manager's goroutines and the refresh timer.
	RequestRender func()
	OnClose       func()
}

// JobHubComponent shows the session's background jobs over the whole screen until it is closed.
type JobHubComponent struct {
	focused  bool
	selected int
	// selectedID is the selected job: the list is rebuilt newest first on every key and frame, so a job started meanwhile moves the others
	// down, and the selection follows its job rather than its index.
	selectedID string
	// showOutput shows the selected job's whole output, scrolled outputScroll lines up from its end.
	showOutput   bool
	outputScroll int
	jobs         *jobs.Manager
	options      JobHubOptions
	now          func() time.Time
	stop         chan struct{}
	disposeOnce  sync.Once
	unsubscribe  func()
}

// NewJobHubComponent builds the hub over mgr. It redraws while jobs run until Dispose.
func NewJobHubComponent(mgr *jobs.Manager, options JobHubOptions) *JobHubComponent {
	h := &JobHubComponent{jobs: mgr, options: options, now: time.Now, stop: make(chan struct{})}
	h.unsubscribe = mgr.Subscribe(func(jobs.Event) { h.requestRender() })
	go func() {
		ticker := time.NewTicker(jobHubRefresh)
		defer ticker.Stop()
		for {
			select {
			case <-h.stop:
				return
			case <-ticker.C:
				if len(mgr.Running()) > 0 {
					h.requestRender()
				}
			}
		}
	}()
	return h
}

func (h *JobHubComponent) requestRender() {
	if h.options.RequestRender != nil {
		h.options.RequestRender()
	}
}

func (h *JobHubComponent) rows() int {
	if h.options.Rows == nil {
		return 24
	}
	return h.options.Rows()
}

// SetFocused implements tui.Focusable.
func (h *JobHubComponent) SetFocused(focused bool) { h.focused = focused }

// list returns the jobs newest first.
func (h *JobHubComponent) list() []jobs.Job {
	list := h.jobs.List()
	slices.Reverse(list)
	return list
}

func (h *JobHubComponent) current(list []jobs.Job) (jobs.Job, bool) {
	if i := slices.IndexFunc(list, func(j jobs.Job) bool { return j.ID == h.selectedID }); i >= 0 {
		h.selected = i
	}
	h.selected = min(h.selected, max(0, len(list)-1))
	if len(list) == 0 {
		return jobs.Job{}, false
	}
	h.selectedID = list[h.selected].ID
	return list[h.selected], true
}

// selectIndex selects the job at index i of list.
func (h *JobHubComponent) selectIndex(list []jobs.Job, i int) {
	h.selected = max(0, min(len(list)-1, i))
	if h.selected < len(list) {
		h.selectedID = list[h.selected].ID
	}
}

// HandleInput implements tui.InputHandler.
func (h *JobHubComponent) HandleInput(data string) {
	kb := tui.GetTUIKeybindings()
	list := h.list()
	job, ok := h.current(list)
	if h.showOutput {
		page := max(1, h.rows()-4)
		switch {
		case kb.Matches(data, tui.KBSelectCancel) || kb.Matches(data, tui.KBSelectConfirm):
			h.showOutput, h.outputScroll = false, 0
		case kb.Matches(data, tui.KBSelectUp):
			h.outputScroll++
		case kb.Matches(data, tui.KBSelectDown):
			h.outputScroll = max(0, h.outputScroll-1)
		case kb.Matches(data, tui.KBSelectPageUp):
			h.outputScroll += page
		case kb.Matches(data, tui.KBSelectPageDown):
			h.outputScroll = max(0, h.outputScroll-page)
		default:
			return
		}
		h.requestRender()
		return
	}
	if kb.Matches(data, tui.KBSelectCancel) {
		if h.options.OnClose != nil {
			h.options.OnClose()
		}
		return
	}
	switch {
	case kb.Matches(data, tui.KBSelectUp) || kb.Matches(data, string(AppJobsUp)):
		h.selectIndex(list, h.selected-1)
	case kb.Matches(data, tui.KBSelectDown) || kb.Matches(data, string(AppJobsDown)):
		h.selectIndex(list, h.selected+1)
	case kb.Matches(data, tui.KBSelectConfirm) && ok:
		h.showOutput, h.outputScroll = true, 0
	case kb.Matches(data, string(AppJobsStop)) && ok && job.Status == jobs.StatusRunning:
		h.jobs.Kill(job.ID)
	default:
		return
	}
	h.requestRender()
}

// Render implements tui.Component.
func (h *JobHubComponent) Render(width int) []string {
	t := tui.ActiveTheme()
	rows := max(8, h.rows())
	list := h.list()
	job, ok := h.current(list)
	if ok && h.showOutput {
		return h.renderOutput(t, job, width, rows)
	}

	running := 0
	for _, j := range list {
		if j.Status == jobs.StatusRunning {
			running++
		}
	}
	rule := t.Fg("border", strings.Repeat("─", max(0, width)))
	lines := []string{
		rule,
		jobHubPadded(t.Bold("Jobs")+t.Fg("muted", fmt.Sprintf("  %d running · %d ended", running, len(list)-running)), width),
		rule,
	}

	now := h.now()
	if len(list) == 0 {
		lines = append(lines,
			t.Fg("muted", fmt.Sprintf("No background jobs in this session. A bash command still running after %d minutes,", jobHubBackgroundAfter/time.Minute)),
			t.Fg("muted", "or one the agent starts with background: true, shows up here."),
		)
	}
	// Up to a third of the rows for the list, the rest for the selected job.
	listRows := min(len(list), max(3, rows/3))
	first := min(max(0, h.selected-listRows+1), max(0, len(list)-listRows))
	for i := first; i < first+listRows && i < len(list); i++ {
		j := list[i]
		style := jobStatusStyles[j.Status]
		if style.color == "" {
			style = jobStatusStyle{mark: "?", color: "muted"}
		}
		pointer := " "
		if i == h.selected {
			pointer = t.Fg("accent", "›")
		}
		end := now
		if j.EndedAt != nil {
			end = *j.EndedAt
		}
		elapsed := formatJobDuration(end.Sub(j.StartedAt))
		head := fmt.Sprintf("%s %s %-5s %s %s  ", pointer, t.Fg(style.color, style.mark), j.ID,
			t.Fg(style.color, fmt.Sprintf("%-9s", jobStatusLabel(j))), t.Fg("muted", fmt.Sprintf("%7s", elapsed)))
		command := oneLineCommand(j.Command)
		if i != h.selected {
			command = t.Fg("text", command)
		}
		lines = append(lines, jobHubPadded(head+command, width))
	}

	if ok {
		lines = append(lines, rule)
		pid := ""
		if job.PID != 0 {
			pid = t.Fg("muted", "  pid ") + strconv.Itoa(job.PID)
		}
		ended := ""
		if job.EndedAt != nil {
			ended = t.Fg("muted", "  ended ") + jobClockTime(*job.EndedAt)
		}
		outputFile := job.OutputFile
		if outputFile == "" {
			outputFile = "(in memory)"
		}
		reported := ""
		if job.ReportedBy != "" {
			reported = t.Fg("muted", "  · reported to the agent")
		}
		details := []string{
			t.Fg("muted", "command") + " " + oneLineCommand(job.Command),
			t.Fg("muted", "cwd") + " " + job.CWD + pid,
			t.Fg("muted", "started") + " " + jobClockTime(job.StartedAt) + t.Fg("muted", "  background since ") + jobClockTime(job.BackgroundedAt) + ended,
			t.Fg("muted", "output") + " " + outputFile + reported,
		}
		for _, line := range details {
			lines = append(lines, jobHubPadded(line, width))
		}
		lines = append(lines, rule)
		if room := rows - len(lines) - 2; room > 0 {
			output := jobOutputLines(h.jobs.Output(job.ID))
			shown := output[max(0, len(output)-room):]
			for _, line := range shown {
				lines = append(lines, jobHubPadded(t.Fg("toolOutput", line), width))
			}
			for i := len(shown); i < room; i++ {
				lines = append(lines, "")
			}
		}
	}

	lines = append(lines, rule)
	sep := t.Fg("muted", " · ")
	hints := []string{tui.RawKeyHint("↑↓", "select"), tui.KeyHint(tui.ActionKeyText(tui.KBSelectConfirm), "output")}
	if ok && job.Status == jobs.StatusRunning {
		hints = append(hints, tui.KeyHint(tui.ActionKeyText(string(AppJobsStop)), "stop"))
	}
	hints = append(hints, tui.KeyHint(tui.ActionKeyText(tui.KBSelectCancel), "close"))
	lines = append(lines, jobHubPadded(strings.Join(hints, sep), width))
	return lines[:min(len(lines), rows)]
}

func (h *JobHubComponent) renderOutput(t *tui.Theme, job jobs.Job, width, rows int) []string {
	output := jobOutputLines(h.jobs.Output(job.ID))
	room := max(1, rows-4)
	h.outputScroll = min(h.outputScroll, max(0, len(output)-room))
	end := len(output) - h.outputScroll
	shown := output[max(0, end-room):end]
	rule := t.Fg("border", strings.Repeat("─", max(0, width)))
	lines := []string{rule, jobHubPadded(t.Bold(job.ID)+"  "+t.Fg("muted", oneLineCommand(job.Command)), width)}
	for _, line := range shown {
		lines = append(lines, jobHubPadded(t.Fg("toolOutput", line), width))
	}
	for len(lines) < rows-2 {
		lines = append(lines, "")
	}
	lines = append(lines, rule)
	position := ""
	if h.outputScroll > 0 {
		position = t.Fg("muted", fmt.Sprintf("%d lines above the end · ", h.outputScroll))
	}
	hints := tui.RawKeyHint("↑↓ pgup pgdn", "scroll") + t.Fg("muted", " · ") + tui.KeyHint(tui.ActionKeyText(tui.KBSelectCancel), "back")
	return append(lines, jobHubPadded(position+hints, width))
}

// Invalidate implements tui.Component; the hub keeps no render cache.
func (h *JobHubComponent) Invalidate() {}

// Dispose stops the refresh timer and the manager subscription.
func (h *JobHubComponent) Dispose() {
	h.disposeOnce.Do(func() {
		close(h.stop)
		h.unsubscribe()
	})
}

// jobStatusText is the footer status text while jobs run, or "" when none runs.
func jobStatusText(mgr *jobs.Manager, hubKey string) string {
	running := len(mgr.Running())
	if running == 0 {
		return ""
	}
	t := tui.ActiveTheme()
	noun := "jobs"
	if running == 1 {
		noun = "job"
	}
	text := fmt.Sprintf("%s %d %s running", t.Fg("accent", "●"), running, noun)
	if hubKey != "" {
		text += t.Fg("dim", " ("+hubKey+")")
	}
	return text
}

// showJobHub shows the session's background jobs over the whole screen, until Escape.
func (m *InteractiveMode) showJobHub() {
	if m.jobHub != nil || m.opts.BackgroundJobs == nil || m.tuiInst == nil {
		return
	}
	var handle *tui.OverlayHandle
	var hub *JobHubComponent
	hub = NewJobHubComponent(m.opts.BackgroundJobs, JobHubOptions{
		Rows:          m.tuiInst.Height,
		RequestRender: m.requestRender,
		OnClose: func() {
			hub.Dispose()
			if handle != nil {
				handle.Hide()
			}
			m.jobHub = nil
			m.requestRender()
		},
	})
	m.jobHub = hub
	handle = m.tuiInst.ShowOverlay(hub, tui.OverlaySpec{
		Anchor:    "top-left",
		Width:     &tui.OverlayValue{Value: 100, Percent: true},
		MaxHeight: &tui.OverlayValue{Value: 100, Percent: true},
	}.Options())
	m.requestRender()
}

// subscribeJobStatus keeps the footer's running-jobs status current, until the mode stops.
func (m *InteractiveMode) subscribeJobStatus() {
	mgr := m.opts.BackgroundJobs
	if mgr == nil {
		return
	}
	show := func() {
		m.updateStatusOnOwner(func() {
			if m.statusLine == nil {
				return
			}
			hubKey := ""
			if m.keybindings != nil {
				hubKey = m.keybindings.KeyText(string(AppJobsHub))
			}
			m.statusLine.SetExtensionStatus(jobStatusKey, jobStatusText(mgr, hubKey))
			if m.tuiInst != nil {
				m.requestRender()
			}
		})
	}
	unsubscribe := mgr.Subscribe(func(jobs.Event) { show() })
	show()
	m.onStop(func() error {
		unsubscribe()
		return nil
	})
}
