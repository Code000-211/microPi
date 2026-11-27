//go:build !windows

package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/codingagent/filetracking"
	"github.com/MichaelKinsy/PiG/internal/codingagent/jobs"
)

// µPi: microPi's bash tool with background jobs (bash.ts moveToBackground, refuseOrWatch, sleepUntilJobEnds).

func runBash(t *testing.T, bt *BashTool, args map[string]any) (string, bool) {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	if prepared, err := bt.PrepareArguments(raw); err == nil {
		raw = prepared
	}
	res, err := bt.Execute(context.Background(), "", raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	return res.Text(), res.IsError
}

func waitForJobEnd(t *testing.T, mgr *jobs.Manager, id string) jobs.Job {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if job, ok := mgr.Get(id); ok && job.Status != jobs.StatusRunning {
			return job
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("job %s did not end", id)
	return jobs.Job{}
}

const backgroundTail = " A message reports when it ends, with its exit code and the end of its output: go on with other work, and call wait only when nothing else is left."

// A timeout before the time to background a command moves it to the background then, rather than ending it.
func TestBashTimeoutWithJobsMovesTheCommandToTheBackground(t *testing.T) {
	mgr := jobs.NewManager()
	bt := &BashTool{CWD: t.TempDir(), BackgroundJobs: mgr}
	text, isError := runBash(t, bt, map[string]any{"command": "echo early; sleep 1; echo late", "timeout": 0.3})
	if isError {
		t.Fatalf("result is an error: %s", text)
	}
	re := regexp.MustCompile(`^early\n\n\nCommand still running at its timeout of 0\.3 seconds: moved to the background as job bg1 \(pid (\d+); stop it with: kill -- -(\d+)\), not stopped\. Its output goes on into (\S+)\.` + regexp.QuoteMeta(backgroundTail) + `$`)
	m := re.FindStringSubmatch(text)
	if m == nil || m[1] != m[2] {
		t.Fatalf("result:\n%s", text)
	}
	job := waitForJobEnd(t, mgr, "bg1")
	if job.Status != jobs.StatusDone || job.ExitCode == nil || *job.ExitCode != 0 || job.Tail != "early\nlate\n" || job.PID == 0 {
		t.Fatalf("job = %+v", job)
	}
	if data, err := os.ReadFile(m[3]); err != nil || !strings.Contains(string(data), "late") {
		t.Fatalf("full output file %s: %q, %v", m[3], data, err)
	}
}

// A command still running after autoBackgroundSeconds goes on in the background.
func TestBashLongCommandGoesOnInTheBackground(t *testing.T) {
	t.Setenv("PI_BASH_BACKGROUND_MS", "300")
	mgr := jobs.NewManager()
	bt := &BashTool{CWD: t.TempDir(), BackgroundJobs: mgr}
	if desc := bt.Schema().Description; !strings.HasSuffix(desc, " Commands running over 0.3 s continue in the background and report their end.") {
		t.Fatalf("description = %q", desc)
	}
	text, _ := runBash(t, bt, map[string]any{"command": "sleep 1; exit 3"})
	if !regexp.MustCompile(`^Command still running after 0\.3 seconds: moved to the background as job bg1 \(pid \d+; stop it with: kill -- -\d+\)\. Its output goes on into \S+\.` + regexp.QuoteMeta(backgroundTail) + `$`).MatchString(text) {
		t.Fatalf("result:\n%s", text)
	}
	if job := waitForJobEnd(t, mgr, "bg1"); job.Status != jobs.StatusFailed || job.ExitCode == nil || *job.ExitCode != 3 {
		t.Fatalf("job = %+v", job)
	}
}

// A call started with background:true (or Claude Code's run_in_background) keeps its timeout.
func TestBashExplicitBackgroundKeepsItsTimeout(t *testing.T) {
	mgr := jobs.NewManager()
	bt := &BashTool{CWD: t.TempDir(), BackgroundJobs: mgr}
	text, _ := runBash(t, bt, map[string]any{"command": "sleep 5", "run_in_background": true, "timeout": 0.5})
	if !regexp.MustCompile(`^Started in the background as job bg1 \(pid \d+; stop it with: kill -- -\d+\)\. Its output goes into \S+; its timeout of 0\.5 seconds still ends it\.` + regexp.QuoteMeta(backgroundTail) + `$`).MatchString(text) {
		t.Fatalf("result:\n%s", text)
	}
	if job := waitForJobEnd(t, mgr, "bg1"); job.Status != jobs.StatusTimedOut {
		t.Fatalf("job = %+v", job)
	}
}

// Killing a job ends it as killed.
func TestBashBackgroundJobKill(t *testing.T) {
	mgr := jobs.NewManager()
	bt := &BashTool{CWD: t.TempDir(), BackgroundJobs: mgr}
	runBash(t, bt, map[string]any{"command": "sleep 30", "background": true})
	if !mgr.Kill("bg1") {
		t.Fatal("Kill returned false")
	}
	if job := waitForJobEnd(t, mgr, "bg1"); job.Status != jobs.StatusKilled {
		t.Fatalf("job = %+v", job)
	}
}

// A fixed sleep longer than a command runs before it goes to the background is refused, with or without running jobs;
// a loop's sleep and a short one run.
func TestBashFixedSleepRefusal(t *testing.T) {
	mgr := jobs.NewManager()
	bt := &BashTool{CWD: t.TempDir(), BackgroundJobs: mgr}
	text, isError := runBash(t, bt, map[string]any{"command": "make && sleep 3m; echo done"})
	if !isError || text != fixedSleepRefusal(180, 120) || !strings.HasPrefix(text, "Not run: the command sleeps 180 seconds, a fixed time longer than a command runs before it goes to the background (120 s). To wait for a background job, call wait") {
		t.Fatalf("result: %v %s", isError, text)
	}
	for _, command := range []string{"until false; do sleep 200; done & echo ok", "sleep 0.1; echo ok"} {
		if text, isError := runBash(t, bt, map[string]any{"command": command}); isError || !strings.Contains(text, "ok") {
			t.Fatalf("%s: %v %s", command, isError, text)
		}
	}
	// Without background jobs there is no refusal.
	if _, refusal := refuseOrWatch("sleep 300", shellToolConfig{local: true}, false); refusal != "" {
		t.Fatalf("refusal without jobs: %s", refusal)
	}
	// A call started in the background is not refused.
	if _, refusal := refuseOrWatch("sleep 300", shellToolConfig{local: true, backgroundJobs: mgr, autoBackgroundSeconds: 120}, true); refusal != "" {
		t.Fatalf("refusal for a background call: %s", refusal)
	}
}

func TestFixedSleepSecondsAndLeadingSleep(t *testing.T) {
	for command, want := range map[string]float64{
		"sleep 5":                                 5,
		"sleep 2m && x":                           120,
		"echo; sleep 1.5h":                        5400,
		"while true; do sleep 300; done":          0,
		"for i in 1 2; do sleep 9; done; sleep 4": 4,
		"x=\"$(sleep 12)\"":                       12,
		"sleep 12x":                               0,
		"nosleep 30":                              0,
	} {
		if got := fixedSleepSeconds(command); got != want {
			t.Errorf("fixedSleepSeconds(%q) = %v, want %v", command, got, want)
		}
	}
	if seconds, rest, ok := leadingSleep("  sleep 20s && tail log"); !ok || seconds != 20 || rest != "tail log" {
		t.Errorf("leadingSleep = %v %q %v", seconds, rest, ok)
	}
	if seconds, rest, ok := leadingSleep("sleep 30\n"); !ok || seconds != 30 || rest != "" {
		t.Errorf("leadingSleep = %v %q %v", seconds, rest, ok)
	}
	if _, _, ok := leadingSleep("sleep 30 | cat"); ok {
		t.Error("a piped sleep is not a leading sleep")
	}
}

// A leading sleep with a job running ends when the job ends; the call reports the job.
func TestBashLeadingSleepEndsWhenAJobEnds(t *testing.T) {
	mgr := jobs.NewManager()
	bt := &BashTool{CWD: t.TempDir(), BackgroundJobs: mgr}
	runBash(t, bt, map[string]any{"command": "sleep 0.3; echo job-out", "background": true})
	start := time.Now()
	text, isError := runBash(t, bt, map[string]any{"command": "sleep 20; echo after"})
	if elapsed := time.Since(start); elapsed > 8*time.Second {
		t.Fatalf("the sleep took %v", elapsed)
	}
	re := regexp.MustCompile("^\\[The sleep of 20 s ended after \\d+\\.\\d s: a background job ended\\.\\]\nBackground job bg1 \\(`sleep 0\\.3; echo job-out`\\) ended with exit code 0 after [0-9.]+ s\\.\n\nEnd of its output:\njob-out\n\n\n\\[Full output: \\S+\\]\n\nafter\n$")
	if isError || !re.MatchString(text) {
		t.Fatalf("result: %v\n%s", isError, text)
	}
	if job, _ := mgr.Get("bg1"); job.ReportedBy != jobs.ReportedBash {
		t.Fatalf("job reported by %q", job.ReportedBy)
	}
}

// When no job ends during a leading sleep, the rest of the command runs after it, without sleeping again.
func TestBashLeadingSleepDoesNotSleepTwice(t *testing.T) {
	if testing.Short() {
		t.Skip("sleeps 11 s")
	}
	mgr := jobs.NewManager()
	bt := &BashTool{CWD: t.TempDir(), BackgroundJobs: mgr}
	runBash(t, bt, map[string]any{"command": "sleep 60", "background": true})
	defer mgr.KillAll()
	start := time.Now()
	text, isError := runBash(t, bt, map[string]any{"command": "sleep 11; echo after"})
	elapsed := time.Since(start)
	if isError || text != "after\n" {
		t.Fatalf("result: %v %q", isError, text)
	}
	if elapsed < 11*time.Second || elapsed > 18*time.Second {
		t.Fatalf("took %v, want one sleep of 11 s", elapsed)
	}
}

func TestBashPrepareArgumentsRenamesBackgroundAliases(t *testing.T) {
	bt := &BashTool{}
	for _, alias := range []string{"run_in_background", "is_background"} {
		out, err := bt.PrepareArguments(json.RawMessage(`{"command":"x","` + alias + `":true}`))
		if err != nil {
			t.Fatal(err)
		}
		var p shellParams
		if err := json.Unmarshal(out, &p); err != nil || p.Background == nil || !*p.Background {
			t.Fatalf("%s: prepared %s", alias, out)
		}
	}
}

// Files a successful command printed whole with `cat` count as read; a changed watched file is reported.
func TestBashCatMarksFilesReadAndChangesAreReported(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tracker := filetracking.NewTracker()
	bt := &BashTool{CWD: dir, Tracker: tracker, EditToolActive: func() bool { return true }}
	if text, isError := runBash(t, bt, map[string]any{"command": "cat a.txt"}); isError || text != "one\ntwo\nthree\n" {
		t.Fatalf("cat: %v %q", isError, text)
	}
	if !tracker.StateTracker().HasBeenRead(path) {
		t.Fatal("cat did not mark a.txt read")
	}
	text, isError := runBash(t, bt, map[string]any{"command": "sed -i 's/two/TWO/' a.txt"})
	if isError || text != "(no output)\n\na.txt changed at line 2." {
		t.Fatalf("sed: %v %q", isError, text)
	}
	// Without the edit tool, nothing is watched.
	bt.EditToolActive = nil
	if text, _ := runBash(t, bt, map[string]any{"command": "sed -i 's/one/ONE/' a.txt"}); text != "(no output)" {
		t.Fatalf("sed without the edit tool: %q", text)
	}
}

// On a remote machine, a command that changes files as edit and write would is refused.
func TestBashRemoteFileWriteRefusal(t *testing.T) {
	cfg := shellToolConfig{local: false, editToolActive: func() bool { return true }}
	if _, refusal := refuseOrWatch("cd src && sed -i 's/a/b/' main.go", cfg, false); refusal != FileWriteRefusal([]FileWrite{{How: "sed -i", Target: "main.go", Dir: "src"}}) {
		t.Fatalf("refusal = %q", refusal)
	}
	if _, refusal := refuseOrWatch("echo x >> log.txt; make > build.log", cfg, false); refusal != "" {
		t.Fatalf("an append and a program's output must run: %q", refusal)
	}
	// A remote pkill does not reach this process: no self-kill check.
	if _, refusal := refuseOrWatch("pkill -f "+filepath.Base(os.Args[0]), shellToolConfig{}, false); refusal != "" {
		t.Fatalf("remote pkill refused: %q", refusal)
	}
}

// PI_REMOTE names the session's remote machine: a command's environment does not carry it.
func TestBashEnvironmentDropsPIRemote(t *testing.T) {
	t.Setenv("PI_REMOTE", "dev:~/x")
	for _, kv := range sessionEnvironment(context.Background(), true, "") {
		if strings.HasPrefix(kv, "PI_REMOTE=") {
			t.Fatalf("environment carries %s", kv)
		}
	}
}

// µPi: microPi's bash description and guidelines.
func TestBashToolTextsMatchMicroPi(t *testing.T) {
	schema := (&BashTool{}).Schema()
	if want := "Execute a bash command in the current working directory. Returns stdout and stderr; long output keeps its first 40 lines and its end, the full output is saved to a file. Optional timeout in seconds."; schema.Description != want {
		t.Fatalf("description = %q", schema.Description)
	}
	if len(schema.PromptGuidelines) != 2 || schema.PromptGuidelines[1] != "Commands still running after two minutes continue in the background and report when they end: do other work meanwhile, never sleep or poll." {
		t.Fatalf("guidelines = %q", schema.PromptGuidelines)
	}
	encoded, err := json.Marshal(schema.Parameters)
	if err != nil {
		t.Fatal(err)
	}
	params := string(encoded)
	for _, want := range []string{
		`"Seconds to wait for the command (optional, no default). Where background jobs are, a command still running then goes on in the background and reports its end; otherwise it is ended"`,
		`"Start in the background at once; a message reports its end"`,
	} {
		if !strings.Contains(params, want) {
			t.Errorf("parameters lack %s: %s", want, params)
		}
	}
	if desc := (&BashTool{BackgroundJobs: jobs.NewManager()}).Schema().Description; !strings.HasSuffix(desc, " Optional timeout in seconds. Commands running over 120 s continue in the background and report their end.") {
		t.Fatalf("description with jobs = %q", desc)
	}
}
