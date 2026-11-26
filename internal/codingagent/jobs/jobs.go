// Package jobs manages backgrounded bash commands, wakeup notifications, and reporting.
package jobs

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// JobStatus represents the lifecycle state of a background job.
type JobStatus string

const (
	StatusRunning  JobStatus = "running"
	StatusDone     JobStatus = "done"
	StatusFailed   JobStatus = "failed"
	StatusKilled   JobStatus = "killed"
	StatusTimedOut JobStatus = "timed out"
)

// ReportedBy indicates how an ended job was reported to the agent.
type ReportedBy string

const (
	ReportedWait    ReportedBy = "wait"
	ReportedBash    ReportedBy = "bash"
	ReportedMessage ReportedBy = "message"
)

const (
	// BackgroundJobMessageType is the customType of the message reporting ended jobs.
	BackgroundJobMessageType = "background-job"
	// JobSettleWindow is how long a waiter/reporter collects after one job ends so concurrent completions group together.
	JobSettleWindow = 1500 * time.Millisecond
	maxEndedJobs    = 50
)

// Job represents a command running or completed in the background.
type Job struct {
	ID             string
	Command        string
	CWD            string
	PID            int
	StartedAt      time.Time
	BackgroundedAt time.Time
	OutputFile     string
	Status         JobStatus
	ExitCode       *int
	EndedAt        *time.Time
	Tail           string
	ReportedBy     ReportedBy
}

// JobSpec describes a command being handed to the background job manager.
type JobSpec struct {
	Command    string
	CWD        string
	PID        int
	StartedAt  time.Time
	OutputFile string
	Output     func() string
	Kill       func()
}

// JobHandle allows the running command to signal completion.
type JobHandle struct {
	Job *Job
	mgr *Manager
}

// End marks the job as finished with the given status, exit code, and output tail.
func (h *JobHandle) End(status JobStatus, exitCode *int, tail string) {
	if h == nil || h.mgr == nil || h.Job == nil {
		return
	}
	h.mgr.endJob(h.Job.ID, status, exitCode, tail)
}

// Event is emitted on job start or end.
type Event struct {
	Type string // "start" or "end"
	Job  Job
}

// WaitOutcome explains why WaitForEnd returned.
type WaitOutcome string

const (
	OutcomeEnded   WaitOutcome = "ended"
	OutcomeTimeout WaitOutcome = "timeout"
	OutcomeWoken   WaitOutcome = "woken"
	OutcomeAborted WaitOutcome = "aborted"
)

type waiterEntry struct {
	ch chan WaitOutcome
}

// Manager tracks background jobs for a session.
type Manager struct {
	mu        sync.Mutex
	jobs      []*Job
	specs     map[string]JobSpec
	killing   map[string]bool
	listeners map[int]func(Event)
	nextLisID int
	waiters   map[*waiterEntry]struct{}
	count     int
}

// NewManager creates an empty background job Manager.
func NewManager() *Manager {
	return &Manager{
		specs:     make(map[string]JobSpec),
		killing:   make(map[string]bool),
		listeners: make(map[int]func(Event)),
		waiters:   make(map[*waiterEntry]struct{}),
	}
}

// Add registers a command that went to the background.
func (m *Manager) Add(spec JobSpec, backgroundedAt time.Time) *JobHandle {
	if backgroundedAt.IsZero() {
		backgroundedAt = time.Now()
	}
	if spec.StartedAt.IsZero() {
		spec.StartedAt = backgroundedAt
	}
	m.mu.Lock()
	m.count++
	id := fmt.Sprintf("bg%d", m.count)
	job := &Job{
		ID:             id,
		Command:        spec.Command,
		CWD:            spec.CWD,
		PID:            spec.PID,
		StartedAt:      spec.StartedAt,
		BackgroundedAt: backgroundedAt,
		OutputFile:     spec.OutputFile,
		Status:         StatusRunning,
	}
	m.jobs = append(m.jobs, job)
	m.specs[id] = spec
	lis := m.copyListenersLocked()
	snap := *job
	m.mu.Unlock()

	for _, fn := range lis {
		fn(Event{Type: "start", Job: snap})
	}
	return &JobHandle{Job: job, mgr: m}
}

func (m *Manager) endJob(id string, status JobStatus, exitCode *int, tail string) {
	m.mu.Lock()
	var target *Job
	for _, j := range m.jobs {
		if j.ID == id {
			target = j
			break
		}
	}
	if target == nil || target.Status != StatusRunning {
		m.mu.Unlock()
		return
	}
	if m.killing[id] {
		delete(m.killing, id)
		status = StatusKilled
	}
	now := time.Now()
	target.Status = status
	target.ExitCode = exitCode
	target.EndedAt = &now
	target.Tail = tail
	delete(m.specs, id)
	m.pruneLocked()
	lis := m.copyListenersLocked()
	snap := *target
	waiters := make([]*waiterEntry, 0, len(m.waiters))
	for w := range m.waiters {
		waiters = append(waiters, w)
	}
	m.mu.Unlock()

	for _, fn := range lis {
		fn(Event{Type: "end", Job: snap})
	}
	for _, w := range waiters {
		select {
		case w.ch <- OutcomeEnded:
		default:
		}
	}
}

func (m *Manager) copyListenersLocked() []func(Event) {
	out := make([]func(Event), 0, len(m.listeners))
	for _, fn := range m.listeners {
		out = append(out, fn)
	}
	return out
}

func (m *Manager) pruneLocked() {
	ended := 0
	for _, j := range m.jobs {
		if j.Status != StatusRunning {
			ended++
		}
	}
	for i := 0; i < len(m.jobs) && ended > maxEndedJobs; {
		j := m.jobs[i]
		if j.Status != StatusRunning && j.ReportedBy != "" {
			m.jobs = append(m.jobs[:i], m.jobs[i+1:]...)
			ended--
		} else {
			i++
		}
	}
}

// List returns snapshots of all tracked jobs, oldest first.
func (m *Manager) List() []Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Job, len(m.jobs))
	for i, j := range m.jobs {
		out[i] = *j
	}
	return out
}

// Get returns a snapshot of a job by ID.
func (m *Manager) Get(id string) (Job, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		if j.ID == id {
			return *j, true
		}
	}
	return Job{}, false
}

// Running returns snapshots of currently running jobs.
func (m *Manager) Running() []Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Job
	for _, j := range m.jobs {
		if j.Status == StatusRunning {
			out = append(out, *j)
		}
	}
	return out
}

// Output returns live output for a running job or the stored tail for an ended job.
func (m *Manager) Output(id string) string {
	m.mu.Lock()
	spec, ok := m.specs[id]
	var tail string
	if !ok {
		for _, j := range m.jobs {
			if j.ID == id {
				tail = j.Tail
				break
			}
		}
	}
	m.mu.Unlock()
	if ok && spec.Output != nil {
		return spec.Output()
	}
	return tail
}

// Kill stops a running job by ID.
func (m *Manager) Kill(id string) bool {
	m.mu.Lock()
	spec, ok := m.specs[id]
	if !ok {
		m.mu.Unlock()
		return false
	}
	m.killing[id] = true
	m.mu.Unlock()
	if spec.Kill != nil {
		spec.Kill()
	}
	return true
}

// KillAll stops every running job.
func (m *Manager) KillAll() {
	for _, j := range m.Running() {
		m.Kill(j.ID)
	}
}

// TakeUnreported returns ended jobs not yet reported and marks them as reported by `by`.
func (m *Manager) TakeUnreported(by ReportedBy) []Job {
	if by == "" {
		by = ReportedWait
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Job
	for _, j := range m.jobs {
		if j.Status != StatusRunning && j.ReportedBy == "" {
			j.ReportedBy = by
			out = append(out, *j)
		}
	}
	return out
}

// HasUnreported reports whether any ended job has not been reported yet.
func (m *Manager) HasUnreported() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		if j.Status != StatusRunning && j.ReportedBy == "" {
			return true
		}
	}
	return false
}

// HasWaiters reports whether a wait call is currently blocked on jobs.
func (m *Manager) HasWaiters() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.waiters) > 0
}

// WaitForEnd blocks until a job ends (plus JobSettleWindow if more are still running),
// or until d elapses, Wake is called, or ctx is canceled.
func (m *Manager) WaitForEnd(ctx context.Context, d time.Duration) WaitOutcome {
	if ctx.Err() != nil {
		return OutcomeAborted
	}
	if m.HasUnreported() {
		return OutcomeEnded
	}
	w := &waiterEntry{ch: make(chan WaitOutcome, 1)}
	m.mu.Lock()
	if m.hasUnreportedLocked() {
		m.mu.Unlock()
		return OutcomeEnded
	}
	m.waiters[w] = struct{}{}
	m.mu.Unlock()

	remove := func() {
		m.mu.Lock()
		delete(m.waiters, w)
		m.mu.Unlock()
	}

	timer := time.NewTimer(d)
	defer timer.Stop()

	var outcome WaitOutcome
	select {
	case outcome = <-w.ch:
	case <-timer.C:
		outcome = OutcomeTimeout
	case <-ctx.Done():
		outcome = OutcomeAborted
	}
	remove()

	if outcome != OutcomeEnded || len(m.Running()) == 0 {
		return outcome
	}

	// Settle window: collect concurrent job completions into one report.
	w2 := &waiterEntry{ch: make(chan WaitOutcome, 1)}
	m.mu.Lock()
	m.waiters[w2] = struct{}{}
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.waiters, w2)
		m.mu.Unlock()
	}()

	settleTimer := time.NewTimer(JobSettleWindow)
	defer settleTimer.Stop()
	for len(m.Running()) > 0 {
		select {
		case <-w2.ch:
			if len(m.Running()) == 0 {
				return outcome
			}
		case <-settleTimer.C:
			return outcome
		case <-ctx.Done():
			return outcome
		}
	}
	return outcome
}

func (m *Manager) hasUnreportedLocked() bool {
	for _, j := range m.jobs {
		if j.Status != StatusRunning && j.ReportedBy == "" {
			return true
		}
	}
	return false
}

// Wake ends every blocked WaitForEnd early (e.g., user sent a message).
func (m *Manager) Wake() {
	m.mu.Lock()
	waiters := make([]*waiterEntry, 0, len(m.waiters))
	for w := range m.waiters {
		waiters = append(waiters, w)
	}
	m.mu.Unlock()
	for _, w := range waiters {
		select {
		case w.ch <- OutcomeWoken:
		default:
		}
	}
}

// Subscribe registers a listener for job start/end events and returns an unsubscribe function.
func (m *Manager) Subscribe(fn func(Event)) func() {
	m.mu.Lock()
	m.nextLisID++
	id := m.nextLisID
	m.listeners[id] = fn
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		delete(m.listeners, id)
		m.mu.Unlock()
	}
}

func formatSeconds(d time.Duration) string {
	s := float64(d.Round(100*time.Millisecond)) / float64(time.Second)
	if s < 60 {
		return fmt.Sprintf("%.1f s", s)
	}
	m := int(s) / 60
	rem := int(s) - m*60
	return fmt.Sprintf("%d min %d s", m, rem)
}

func describeEnd(job Job) string {
	end := time.Now()
	if job.EndedAt != nil {
		end = *job.EndedAt
	}
	after := formatSeconds(end.Sub(job.StartedAt))
	switch job.Status {
	case StatusKilled:
		return "was stopped after " + after
	case StatusTimedOut:
		return "was stopped by its timeout after " + after
	default:
		if job.ExitCode == nil {
			return "ended without an exit code after " + after
		}
		return fmt.Sprintf("ended with exit code %d after %s", *job.ExitCode, after)
	}
}

// FormatJobReport formats one or more ended jobs into the agent report string.
func FormatJobReport(ended []Job) string {
	one := func(job Job) string {
		var b strings.Builder
		fmt.Fprintf(&b, "Background job %s (`%s`) %s.", job.ID, job.Command, describeEnd(job))
		if job.Tail != "" {
			b.WriteString("\n\nEnd of its output:\n")
			b.WriteString(job.Tail)
		}
		if job.OutputFile != "" {
			fmt.Fprintf(&b, "\n\n[Full output: %s]", job.OutputFile)
		}
		return b.String()
	}
	if len(ended) == 1 {
		return one(ended[0])
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d background jobs ended.\n\n", len(ended))
	for i, j := range ended {
		if i > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "── %s ──\n%s", j.ID, one(j))
	}
	return b.String()
}

// DescribeRunningJobs formats a summary of currently running jobs.
func DescribeRunningJobs(running []Job, now time.Time) string {
	if now.IsZero() {
		now = time.Now()
	}
	parts := make([]string, len(running))
	for i, j := range running {
		parts[i] = fmt.Sprintf("%s (`%s`, running %s)", j.ID, j.Command, formatSeconds(now.Sub(j.StartedAt)))
	}
	return strings.Join(parts, ", ")
}

// JobReporter batches ended jobs over JobSettleWindow and invokes send when no wait call is active.
type JobReporter struct {
	mgr         *Manager
	send        func([]Job)
	mu          sync.Mutex
	timer       *time.Timer
	disposed    bool
	unsubscribe func()
	wg          sync.WaitGroup
}

// NewJobReporter creates a JobReporter subscribed to mgr.
func NewJobReporter(mgr *Manager, send func([]Job)) *JobReporter {
	r := &JobReporter{mgr: mgr, send: send}
	r.unsubscribe = mgr.Subscribe(func(ev Event) {
		if ev.Type == "end" {
			r.schedule()
		}
	})
	return r
}

func (r *JobReporter) schedule() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.disposed || r.timer != nil {
		return
	}
	r.timer = time.AfterFunc(JobSettleWindow, r.report)
}

func (r *JobReporter) report() {
	r.mu.Lock()
	r.timer = nil
	if r.disposed {
		r.mu.Unlock()
		return
	}
	r.mu.Unlock()

	if r.mgr.HasWaiters() {
		r.schedule()
		return
	}
	ended := r.mgr.TakeUnreported(ReportedMessage)
	if len(ended) > 0 && r.send != nil {
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			r.send(ended)
		}()
	}
}

// Dispose stops pending timers and unsubscribes from the manager.
func (r *JobReporter) Dispose() {
	r.mu.Lock()
	r.disposed = true
	if r.timer != nil {
		r.timer.Stop()
		r.timer = nil
	}
	r.mu.Unlock()
	if r.unsubscribe != nil {
		r.unsubscribe()
	}
	r.wg.Wait()
}
