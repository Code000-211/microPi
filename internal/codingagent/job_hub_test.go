package codingagent

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/codingagent/jobs"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// jobHubFixture is a job hub over a real manager with two jobs: bg1 ended with exit 2, bg2 still running.
type jobHubFixture struct {
	mgr     *jobs.Manager
	hub     *JobHubComponent
	running *jobs.JobHandle
	killed  atomic.Int32
	closed  atomic.Int32
	renders atomic.Int32
}

func newJobHubFixture(t *testing.T) *jobHubFixture {
	t.Helper()
	restoreStartupTheme(t)
	restoreTUIKeybindings(t)
	tui.SetThemeByName("dark")
	_ = DefaultKeybindingsManager()

	f := &jobHubFixture{mgr: jobs.NewManager()}
	start := time.Now().Add(-90 * time.Second)
	ended := f.mgr.Add(jobs.JobSpec{Command: "make   test", CWD: "/work", StartedAt: start, Output: func() string { return "" }}, start.Add(time.Second))
	code := 2
	ended.End(jobs.StatusFailed, &code, "compiling\nFAIL pkg\n")
	f.running = f.mgr.Add(jobs.JobSpec{
		Command:    "npm run dev",
		CWD:        "/work/web",
		PID:        4242,
		StartedAt:  start,
		OutputFile: "/tmp/bg2.log",
		Output:     func() string { return "line 1\nline 2\nserver ready\n" },
		Kill:       func() { f.killed.Add(1) },
	}, start.Add(2*time.Second))
	f.hub = NewJobHubComponent(f.mgr, JobHubOptions{
		Rows:          func() int { return 24 },
		RequestRender: func() { f.renders.Add(1) },
		OnClose:       func() { f.closed.Add(1) },
	})
	t.Cleanup(f.hub.Dispose)
	return f
}

func (f *jobHubFixture) plain(width int) []string {
	lines := f.hub.Render(width)
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = widthx.StripAnsi(line)
	}
	return out
}

func jobHubHasLine(lines []string, part string) bool {
	for _, line := range lines {
		if strings.Contains(line, part) {
			return true
		}
	}
	return false
}

func TestJobHubRendersJobsNewestFirstWithDetailsAndOutput(t *testing.T) {
	f := newJobHubFixture(t)
	lines := f.plain(80)
	if len(lines) > 24 {
		t.Fatalf("rendered %d lines, want at most the 24 rows", len(lines))
	}
	for i, line := range lines {
		if line != "" && widthx.VisibleWidth(line) != 80 {
			t.Errorf("line %d is %d columns wide: %q", i, widthx.VisibleWidth(line), line)
		}
	}
	if !strings.Contains(lines[1], "Jobs  1 running · 1 ended") {
		t.Errorf("header = %q", lines[1])
	}
	if !strings.HasPrefix(lines[3], "› ● bg2") || !strings.Contains(lines[3], "running") || !strings.Contains(lines[3], "npm run dev") {
		t.Errorf("first row = %q, want the running bg2 selected", lines[3])
	}
	if !strings.HasPrefix(lines[4], "  ✗ bg1") || !strings.Contains(lines[4], "exit 2") || !strings.Contains(lines[4], "1m 30s") || !strings.Contains(lines[4], "make test") {
		t.Errorf("second row = %q, want the ended bg1 with its exit code, duration and one-line command", lines[4])
	}
	for _, want := range []string{"command npm run dev", "cwd /work/web  pid 4242", "background since", "output /tmp/bg2.log", "server ready", "x stop", "escape/ctrl+c close"} {
		if !jobHubHasLine(lines, want) {
			t.Errorf("render has no %q:\n%s", want, strings.Join(lines, "\n"))
		}
	}
}

func TestJobHubRendersHintWithoutJobs(t *testing.T) {
	restoreStartupTheme(t)
	restoreTUIKeybindings(t)
	_ = DefaultKeybindingsManager()
	hub := NewJobHubComponent(jobs.NewManager(), JobHubOptions{Rows: func() int { return 12 }})
	defer hub.Dispose()
	lines := hub.Render(100)
	text := widthx.StripAnsi(strings.Join(lines, "\n"))
	for _, want := range []string{"0 running · 0 ended", "No background jobs in this session. A bash command still running after 2 minutes,", "background: true"} {
		if !strings.Contains(text, want) {
			t.Errorf("render has no %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "stop") {
		t.Errorf("stop hint shown without a running job:\n%s", text)
	}
}

func TestJobHubKeysSelectShowOutputStopAndClose(t *testing.T) {
	f := newJobHubFixture(t)

	// x stops the selected running job.
	f.hub.HandleInput("x")
	if got := f.killed.Load(); got != 1 {
		t.Fatalf("x killed %d times, want 1", got)
	}
	code := -1
	f.running.End(jobs.StatusFailed, &code, "stopped\n")
	if job, _ := f.mgr.Get("bg2"); job.Status != jobs.StatusKilled {
		t.Fatalf("bg2 status = %q, want killed", job.Status)
	}
	if jobHubHasLine(f.plain(80), "x stop") {
		t.Error("stop hint still shown for an ended job")
	}
	f.hub.HandleInput("x")
	if got := f.killed.Load(); got != 1 {
		t.Errorf("x on an ended job killed again (%d)", got)
	}

	// j and down move the selection, and stop at the last job; k moves it back.
	f.hub.HandleInput("j")
	if lines := f.plain(80); !strings.HasPrefix(lines[4], "› ✗ bg1") {
		t.Fatalf("after j the second row = %q, want bg1 selected", lines[4])
	}
	f.hub.HandleInput("\x1b[B")
	if lines := f.plain(80); !strings.HasPrefix(lines[4], "› ✗ bg1") {
		t.Fatalf("down past the end moved the selection: %q", lines[4])
	}
	f.hub.HandleInput("k")
	if lines := f.plain(80); !strings.HasPrefix(lines[3], "› ■ bg2") {
		t.Fatalf("after k the first row = %q, want bg2 selected", lines[3])
	}
	f.hub.HandleInput("\x1b[B")

	// Enter shows bg1's whole output; escape goes back to the list, not out of the hub.
	f.hub.HandleInput("\r")
	lines := f.plain(80)
	if len(lines) != 24 || !strings.HasPrefix(lines[1], "bg1  make test") || lines[2] != jobHubTestPad("compiling", 80) || lines[3] != jobHubTestPad("FAIL pkg", 80) {
		t.Fatalf("output view:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[len(lines)-1], "escape/ctrl+c back") {
		t.Errorf("output view hints = %q", lines[len(lines)-1])
	}
	f.hub.HandleInput("\x1b")
	if f.closed.Load() != 0 {
		t.Fatal("escape in the output view closed the hub")
	}
	if lines := f.plain(80); !strings.Contains(lines[1], "Jobs") {
		t.Fatalf("escape did not return to the list: %q", lines[1])
	}

	// An unbound key does nothing and asks for no frame; escape in the list closes.
	before := f.renders.Load()
	f.hub.HandleInput("q")
	if f.renders.Load() != before {
		t.Error("an unbound key requested a render")
	}
	f.hub.HandleInput("\x1b")
	if got := f.closed.Load(); got != 1 {
		t.Fatalf("escape closed %d times, want 1", got)
	}
}

func TestJobHubOutputViewScrolls(t *testing.T) {
	restoreStartupTheme(t)
	restoreTUIKeybindings(t)
	_ = DefaultKeybindingsManager()
	mgr := jobs.NewManager()
	var out strings.Builder
	for i := 1; i <= 30; i++ {
		out.WriteString("row " + strings.Repeat("x", i%3) + "\n")
	}
	output := out.String()
	mgr.Add(jobs.JobSpec{Command: "seq", Output: func() string { return output }}, time.Time{})
	hub := NewJobHubComponent(mgr, JobHubOptions{Rows: func() int { return 10 }})
	defer hub.Dispose()
	hub.HandleInput("\r")
	last := func() string { lines := hub.Render(40); return widthx.StripAnsi(lines[len(lines)-1]) }
	if strings.Contains(last(), "above the end") {
		t.Fatalf("output view starts scrolled: %q", last())
	}
	hub.HandleInput("\x1b[A")
	if !strings.Contains(last(), "1 lines above the end") {
		t.Fatalf("up did not scroll: %q", last())
	}
	for range 100 {
		hub.HandleInput("\x1b[5~")
	}
	if !strings.Contains(last(), "24 lines above the end") {
		t.Fatalf("page up is not clamped to the start: %q", last())
	}
}

// The refresh timer and manager events ask for frames; Dispose stops both.
func TestJobHubRequestsRendersUntilDisposed(t *testing.T) {
	mgr := jobs.NewManager()
	var renders atomic.Int32
	hub := NewJobHubComponent(mgr, JobHubOptions{RequestRender: func() { renders.Add(1) }})
	handle := mgr.Add(jobs.JobSpec{Command: "sleep 9"}, time.Time{})
	if renders.Load() == 0 {
		t.Fatal("a job start did not request a render")
	}
	deadline := time.Now().Add(5 * time.Second)
	for start := renders.Load(); renders.Load() == start; {
		if time.Now().After(deadline) {
			t.Fatal("the refresh timer did not request a render while a job runs")
		}
		time.Sleep(20 * time.Millisecond)
	}
	hub.Dispose()
	hub.Dispose()
	after := renders.Load()
	handle.End(jobs.StatusDone, nil, "")
	time.Sleep(2 * jobHubRefresh)
	if renders.Load() != after {
		t.Error("the hub requested renders after Dispose")
	}
}

func TestJobStatusTextCountsRunningJobs(t *testing.T) {
	restoreStartupTheme(t)
	mgr := jobs.NewManager()
	if got := jobStatusText(mgr, "alt+j"); got != "" {
		t.Fatalf("status without jobs = %q", got)
	}
	first := mgr.Add(jobs.JobSpec{Command: "a"}, time.Time{})
	if got := widthx.StripAnsi(jobStatusText(mgr, "alt+j")); got != "● 1 job running (alt+j)" {
		t.Errorf("one job = %q", got)
	}
	mgr.Add(jobs.JobSpec{Command: "b"}, time.Time{})
	if got := widthx.StripAnsi(jobStatusText(mgr, "")); got != "● 2 jobs running" {
		t.Errorf("two jobs without a hub key = %q", got)
	}
	first.End(jobs.StatusDone, nil, "")
	if got := widthx.StripAnsi(jobStatusText(mgr, "alt+j")); got != "● 1 job running (alt+j)" {
		t.Errorf("after one ended = %q", got)
	}
}

// /jobs without arguments opens the hub where one can be shown, and lists the jobs as text otherwise; its subcommands stay text.
func TestJobsCommandOpensHubWithoutArguments(t *testing.T) {
	mgr := jobs.NewManager()
	mgr.Add(jobs.JobSpec{Command: "sleep 9", Output: func() string { return "tick\n" }}, time.Time{})
	var opened int
	var out []string
	sc := &SlashContext{BackgroundJobs: mgr, OpenJobHub: func() { opened++ }, AppendText: func(s string) { out = append(out, s) }}
	if err := jobsHandler(sc); err != nil || opened != 1 || len(out) != 0 {
		t.Fatalf("/jobs: err %v, opened %d, output %q", err, opened, out)
	}
	sc.Args = "tail bg1"
	if err := jobsHandler(sc); err != nil || opened != 1 || len(out) != 1 || out[0] != "Output of bg1:\ntick\n" {
		t.Fatalf("/jobs tail: err %v, opened %d, output %q", err, opened, out)
	}
	sc.Args, sc.OpenJobHub, out = "", nil, nil
	if err := jobsHandler(sc); err != nil || len(out) != 1 || !strings.Contains(out[0], "bg1  [running]  `sleep 9`") {
		t.Fatalf("/jobs without a hub: err %v, output %q", err, out)
	}
}

func TestJobHubKeybindingDefaults(t *testing.T) {
	restoreTUIKeybindings(t)
	km := NewKeybindingsManagerFromBindings(nil, "")
	for action, want := range map[AppKeybinding]string{AppJobsHub: "alt+j", AppJobsUp: "k", AppJobsDown: "j", AppJobsStop: "x"} {
		if got := km.GetKeys(string(action)); len(got) != 1 || got[0] != want {
			t.Errorf("%s keys = %v, want [%s]", action, got, want)
		}
	}
	if got := classifyKeyWithBindings("\x1bj", km); got != actionJobHub {
		t.Errorf("alt+j classified as %v, want the job hub", got)
	}
	if got := resolveOutcome(actionJobHub, false, true); got != outcomeJobHub {
		t.Errorf("job hub outcome while working = %v", got)
	}
	// The hub's own keys stay text in the editor.
	for _, key := range []string{"j", "k", "x"} {
		if got := classifyKeyWithBindings(key, km); got != actionInsert {
			t.Errorf("%q classified as %v, want insert", key, got)
		}
	}
	remapped := NewKeybindingsManagerFromBindings(map[string][]KeyID{"app.jobs.hub": {"ctrl+u"}}, "")
	if got := classifyKeyWithBindings("\x15", remapped); got != actionJobHub {
		t.Errorf("remapped ctrl+u classified as %v, want the job hub", got)
	}
	if got := classifyKeyWithBindings("\x1bj", remapped); got == actionJobHub {
		t.Error("alt+j still opens the hub after it was remapped")
	}
}

func jobHubTestPad(text string, width int) string {
	return text + strings.Repeat(" ", width-widthx.VisibleWidth(text))
}

// µPi: the selection follows its job when a newer job starts: stop kills the job the user selected, not the one that moved into its row.
func TestJobHubSelectionFollowsTheJobWhenANewJobStarts(t *testing.T) {
	f := newJobHubFixture(t)
	f.plain(100) // bg2, the newest, is selected
	var newerKilled atomic.Int32
	f.mgr.Add(jobs.JobSpec{Command: "sleep 100", CWD: "/work", Output: func() string { return "" }, Kill: func() { newerKilled.Add(1) }}, time.Now())
	f.hub.HandleInput("x")
	if f.killed.Load() != 1 || newerKilled.Load() != 0 {
		t.Fatalf("stop killed the selected job %d times and the newer one %d times, want 1 and 0", f.killed.Load(), newerKilled.Load())
	}
}
