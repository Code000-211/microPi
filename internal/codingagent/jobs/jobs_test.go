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
