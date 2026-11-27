package jobs

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestManagerLifecycleAndReports(t *testing.T) {
	mgr := NewManager()
	killed := false
	h1 := mgr.Add(JobSpec{
		Command:    "sleep 10",
		CWD:        "/tmp",
		PID:        101,
		OutputFile: "/tmp/bg1.log",
		Output:     func() string { return "live out" },
		Kill:       func() { killed = true },
	}, time.Now())
	if h1.Job.ID != "bg1" {
		t.Fatalf("got ID %q, want bg1", h1.Job.ID)
	}
	if out := mgr.Output("bg1"); out != "live out" {
		t.Fatalf("live output = %q, want live out", out)
	}
	if len(mgr.Running()) != 1 {
		t.Fatalf("running = %d, want 1", len(mgr.Running()))
	}

	code := 0
	h1.End(StatusDone, &code, "final tail")
	if len(mgr.Running()) != 0 {
		t.Fatalf("running after end = %d, want 0", len(mgr.Running()))
	}
	if !mgr.HasUnreported() {
		t.Fatal("expected HasUnreported = true")
	}
	ended := mgr.TakeUnreported(ReportedWait)
	if len(ended) != 1 || ended[0].ID != "bg1" {
		t.Fatalf("ended = %+v", ended)
	}
	report := FormatJobReport(ended)
	if !strings.Contains(report, "bg1") || !strings.Contains(report, "final tail") || !strings.Contains(report, "/tmp/bg1.log") {
		t.Fatalf("unexpected report: %s", report)
	}
	if mgr.HasUnreported() {
		t.Fatal("expected HasUnreported = false after take")
	}
	_ = killed
}

func TestManagerKillMarksKilled(t *testing.T) {
	mgr := NewManager()
	var h *JobHandle
	h = mgr.Add(JobSpec{
		Command: "long cmd",
		Kill: func() {
			code := 143
			h.End(StatusFailed, &code, "")
		},
	}, time.Now())
	if !mgr.Kill("bg1") {
		t.Fatal("Kill returned false")
	}
	j, ok := mgr.Get("bg1")
	if !ok || j.Status != StatusKilled {
		t.Fatalf("got %+v, want killed", j)
	}
}

func TestWaitForEndAndWake(t *testing.T) {
	mgr := NewManager()
	h := mgr.Add(JobSpec{Command: "sleep 1"}, time.Now())
	go func() {
		time.Sleep(30 * time.Millisecond)
		code := 0
		h.End(StatusDone, &code, "ok")
	}()
	outcome := mgr.WaitForEnd(context.Background(), 2*time.Second)
	if outcome != OutcomeEnded {
		t.Fatalf("outcome = %q, want ended", outcome)
	}
	mgr.TakeUnreported(ReportedWait)

	_ = mgr.Add(JobSpec{Command: "sleep 10"}, time.Now())
	go func() {
		time.Sleep(30 * time.Millisecond)
		mgr.Wake()
	}()
	outcome2 := mgr.WaitForEnd(context.Background(), 2*time.Second)
	if outcome2 != OutcomeWoken {
		t.Fatalf("outcome2 = %q, want woken", outcome2)
	}
}

func TestJobReporterBatchesAndSends(t *testing.T) {
	mgr := NewManager()
	var mu sync.Mutex
	var got [][]Job
	rep := NewJobReporter(mgr, func(ended []Job) {
		mu.Lock()
		got = append(got, ended)
		mu.Unlock()
	})
	defer rep.Dispose()

	h := mgr.Add(JobSpec{Command: "echo hi"}, time.Now())
	code := 0
	h.End(StatusDone, &code, "hi")

	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || len(got[0]) != 1 || got[0][0].ID != "bg1" {
		t.Fatalf("unexpected reporter batches: %+v", got)
	}
}

// µPi: background-jobs.ts seconds(): JavaScript number text, tenths rounded, minutes from 60 s on.
func TestFormatSecondsMatchesMicroPi(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{12 * time.Second, "12 s"},
		{1540 * time.Millisecond, "1.5 s"},
		{1550 * time.Millisecond, "1.6 s"},
		{0, "0 s"},
		{59960 * time.Millisecond, "1 min 0 s"},
		{125400 * time.Millisecond, "2 min 5 s"},
		{125600 * time.Millisecond, "2 min 6 s"},
	} {
		if got := formatSeconds(tc.d); got != tc.want {
			t.Errorf("formatSeconds(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
	end := time.Now()
	start := end.Add(-12 * time.Second)
	code := 0
	report := FormatJobReport([]Job{{ID: "bg1", Command: "make", StartedAt: start, EndedAt: &end, ExitCode: &code, Status: StatusDone}})
	if report != "Background job bg1 (`make`) ended with exit code 0 after 12 s." {
		t.Fatalf("report = %q", report)
	}
}
