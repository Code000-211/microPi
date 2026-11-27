package checks

import (
	"context"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// CheckReportMessageType is the message type used for background check reports.
	CheckReportMessageType = "check-report"
	// SettleDelay is how long edits settle before their check starts.
	SettleDelay = 150 * time.Millisecond
	// MaxReported is the maximum number of diagnostics listed in one report.
	MaxReported = 15
	// DefaultChecksWait is the default maximum wait in Settle (PI_CHECKS_WAIT).
	DefaultChecksWait = 30 * time.Second
)

// ChecksWaitDuration is how long a run that would end waits for the checks of its edits (PI_CHECKS_WAIT, seconds): long enough for a warm
// `cargo check` or `tsc`, short enough that a cold one on a large project does not hold the run.
//
// µPi: as background-checks.ts `(Number(process.env.PI_CHECKS_WAIT) || 30) * 1000`: unset, empty, 0 and a value that is not a number all
// mean the default 30 s; a negative value waits not at all (a timer with a negative delay fires at once).
func ChecksWaitDuration() time.Duration {
	raw := strings.TrimSpace(os.Getenv("PI_CHECKS_WAIT"))
	secs, err := strconv.ParseFloat(raw, 64)
	if err != nil || secs == 0 || math.IsNaN(secs) || math.IsInf(secs, 0) {
		return DefaultChecksWait
	}
	if secs < 0 {
		return 0
	}
	return time.Duration(secs * float64(time.Second))
}

type unitState struct {
	unit            *CheckUnit
	errors          map[string]int
	hasErrors       bool
	lintBaseline    map[string]int
	hasLintBaseline bool
	lint            []Diagnostic
	edited          map[string]struct{}
	editedEver      map[string]struct{}
	dirty           bool
	running         bool
	runningForEdits bool
	timer           *time.Timer
	// runDone is closed when the running check ends; nil while none runs.
	runDone chan struct{}
}

// BackgroundChecksOptions configures BackgroundChecks.
type BackgroundChecksOptions struct {
	CWD      string
	Report   func(text string)
	Checkers []Checker
}

// BackgroundChecks runs language checkers after file reads and edits, so that no tool call waits for them, and reports what is new against
// the state before (background-checks.ts).
//
// Errors are reported through Report as soon as a check finds them, against the unit's errors at its check before. Lint findings are
// collected against the unit's findings before the agent first changed it and reported once, when the model would end its run (Settle).
type BackgroundChecks struct {
	cwd      string
	checkers []Checker

	ctx    context.Context
	cancel context.CancelFunc

	mu     sync.Mutex
	report func(text string)
	units  map[string]*unitState
	// order holds the units in the order they were first seen, as the Map of background-checks.ts iterates them: lint reports list
	// their findings in that order.
	order       []*unitState
	idleWaiters []chan struct{}
	disposed    bool
}

// NewBackgroundChecks creates a BackgroundChecks coordinator.
func NewBackgroundChecks(opts BackgroundChecksOptions) *BackgroundChecks {
	checkers := opts.Checkers
	if checkers == nil {
		checkers = EnabledCheckersFromEnv()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &BackgroundChecks{
		cwd:      opts.CWD,
		report:   opts.Report,
		checkers: checkers,
		ctx:      ctx,
		cancel:   cancel,
		units:    make(map[string]*unitState),
	}
}

// SetReport sets the callback that receives each report of new errors: the session's check-report message (agent-session.ts
// _sendCheckReport). A Session sets it on the BackgroundChecks it is given, which are made before it.
func (b *BackgroundChecks) SetReport(report func(text string)) {
	b.mu.Lock()
	b.report = report
	b.mu.Unlock()
}

// Enabled reports whether any checker is enabled.
func (b *BackgroundChecks) Enabled() bool {
	return len(b.checkers) > 0
}

// Runs reports whether the named checker is enabled.
func (b *BackgroundChecks) Runs(name string) bool {
	for _, c := range b.checkers {
		if c.Name() == name {
			return true
		}
	}
	return false
}

// Fingerprint returns a position-independent key for a diagnostic (file, severity, code, message).
func Fingerprint(d Diagnostic) string {
	return fmt.Sprintf("%s\x00%s\x00%s\x00%s", d.File, d.Severity, d.Code, d.Message)
}

func countDiagnostics(diags []Diagnostic) map[string]int {
	m := make(map[string]int, len(diags))
	for _, d := range diags {
		m[Fingerprint(d)]++
	}
	return m
}

func newAgainst(diags []Diagnostic, before map[string]int, hasBefore bool, files map[string]struct{}) []Diagnostic {
	remaining := make(map[string]int, len(before))
	for k, v := range before {
		remaining[k] = v
	}
	var fresh []Diagnostic
	for _, d := range diags {
		key := Fingerprint(d)
		if remaining[key] > 0 {
			remaining[key]--
			continue
		}
		if !hasBefore {
			if _, ok := files[d.File]; !ok {
				continue
			}
		}
		fresh = append(fresh, d)
	}
	return fresh
}

func (b *BackgroundChecks) unitsForLocked(path string) []*unitState {
	var states []*unitState
	for _, checker := range b.checkers {
		if !checker.Matches(path) {
			continue
		}
		unit := checker.Unit(path)
		if unit == nil {
			continue
		}
		state, ok := b.units[unit.Key]
		if !ok {
			state = &unitState{
				unit:       unit,
				edited:     make(map[string]struct{}),
				editedEver: make(map[string]struct{}),
				dirty:      true,
			}
			b.units[unit.Key] = state
			b.order = append(b.order, state)
		}
		states = append(states, state)
	}
	return states
}

// OnRead starts a 0ms baseline check for any matching unit that has not yet been checked.
// Baseline checks do not mark BackgroundChecks as busy.
func (b *BackgroundChecks) OnRead(path string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.disposed {
		return
	}
	for _, state := range b.unitsForLocked(path) {
		if !state.hasErrors && !state.running && state.timer == nil {
			b.scheduleLocked(state, 0)
		}
	}
}

// AwaitBaseline is called before path is written. It waits, up to ChecksWaitDuration, for the baseline check of each of path's units that has
// none yet, and starts a baseline that is still scheduled. It is not capped lower: an edit that does not wait for a slow baseline gets the
// file's old errors reported as new, and the model then chases errors it did not make.
//
// Without it, an edit that lands while the baseline started by a read still runs makes that run's result stale, so it is dropped. The check after
// the edit then has no baseline, and every error in the edited file counts as new: an error the file had before the edit is reported as "New
// errors after your changes".
func (b *BackgroundChecks) AwaitBaseline(ctx context.Context, path string) {
	wait := ChecksWaitDuration()
	if wait <= 0 {
		return
	}
	b.mu.Lock()
	if b.disposed || b.ctx.Err() != nil {
		b.mu.Unlock()
		return
	}
	var pending []chan struct{}
	for _, state := range b.unitsForLocked(path) {
		if state.hasErrors || len(state.editedEver) > 0 {
			continue
		}
		if !state.running && state.timer != nil {
			state.timer.Stop()
			state.timer = nil
			b.startLocked(state)
		}
		if state.running && !state.runningForEdits {
			pending = append(pending, state.runDone)
		}
	}
	b.mu.Unlock()
	if len(pending) == 0 {
		return
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for _, done := range pending {
		select {
		case <-done:
		case <-timer.C:
			return
		case <-ctx.Done():
			return
		}
	}
}

// OnEdited marks path as edited and schedules a debounced check after SettleDelay (150ms).
func (b *BackgroundChecks) OnEdited(path string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.disposed {
		return
	}
	for _, state := range b.unitsForLocked(path) {
		state.edited[path] = struct{}{}
		state.editedEver[path] = struct{}{}
		state.dirty = true
		b.scheduleLocked(state, SettleDelay)
	}
}

func (b *BackgroundChecks) scheduleLocked(state *unitState, delay time.Duration) {
	if b.disposed || b.ctx.Err() != nil {
		return
	}
	if state.timer != nil {
		state.timer.Stop()
		state.timer = nil
	}
	state.timer = time.AfterFunc(delay, func() {
		b.mu.Lock()
		state.timer = nil
		if b.disposed || b.ctx.Err() != nil || state.running {
			b.mu.Unlock()
			return
		}
		b.startLocked(state)
		b.mu.Unlock()
	})
}

func (b *BackgroundChecks) startLocked(state *unitState) {
	state.dirty = false
	edited := make(map[string]struct{}, len(state.edited))
	for k := range state.edited {
		edited[k] = struct{}{}
	}
	state.running = true
	state.runningForEdits = len(edited) > 0
	state.runDone = make(chan struct{})

	go b.runUnit(state, edited)
}

func (b *BackgroundChecks) runUnit(state *unitState, edited map[string]struct{}) {
	diags, err := state.unit.Checker.Run(b.ctx, state.unit)

	var reportMsg string

	b.mu.Lock()
	if err != nil {
		for f := range edited {
			delete(state.edited, f)
		}
	} else if !state.dirty {
		for f := range edited {
			delete(state.edited, f)
		}
		var errs, lints []Diagnostic
		for _, d := range diags {
			if d.Severity == SeverityError {
				errs = append(errs, d)
			} else {
				lints = append(lints, d)
			}
		}
		errorsBefore := state.errors
		hadErrorsBefore := state.hasErrors
		state.errors = countDiagnostics(errs)
		state.hasErrors = true

		if len(state.editedEver) == 0 {
			state.lintBaseline = countDiagnostics(lints)
			state.hasLintBaseline = true
		} else {
			state.lint = newAgainst(lints, state.lintBaseline, state.hasLintBaseline, state.editedEver)
			if len(edited) > 0 {
				fresh := newAgainst(errs, errorsBefore, hadErrorsBefore, edited)
				if len(fresh) > 0 {
					reportMsg = b.format(fmt.Sprintf("New errors after your changes (%s check):", state.unit.Checker.Name()), fresh)
				}
			}
		}
	}
	if report := b.report; reportMsg != "" && report != nil && !b.disposed {
		// The report is sent before the check counts as done, so a run that waits for its checks (Settle) finds it queued, as the
		// setImmediate in background-checks.ts settle lets it reach the queue first. The unit still counts as running meanwhile, so no
		// check of it starts.
		b.mu.Unlock()
		report(reportMsg)
		b.mu.Lock()
	}

	state.running = false
	state.runningForEdits = false
	close(state.runDone)
	state.runDone = nil
	shouldRestart := state.dirty && !b.disposed && b.ctx.Err() == nil
	if shouldRestart {
		b.startLocked(state)
	} else {
		b.notifyIfIdleLocked()
	}
	b.mu.Unlock()
}

func (b *BackgroundChecks) format(title string, diags []Diagnostic) string {
	limit := min(len(diags), MaxReported)
	lines := make([]string, 0, limit+2)
	lines = append(lines, title)
	for i := 0; i < limit; i++ {
		d := diags[i]
		codePart := ""
		if d.Code != "" {
			codePart = " " + d.Code
		}
		lines = append(lines, fmt.Sprintf("  %s:%d:%d%s: %s", ShownPath(d.File, b.cwd), d.Line, d.Column, codePart, d.Message))
	}
	if len(diags) > MaxReported {
		lines = append(lines, fmt.Sprintf("  ... and %d more", len(diags)-MaxReported))
	}
	return strings.Join(lines, "\n")
}

// TakeLintReport returns formatted lint findings introduced since the baseline and promotes them
// into the baseline so they are not reported again. Returns "" when there are none.
func (b *BackgroundChecks) TakeLintReport() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	var findings []Diagnostic
	var linters []string
	seenLinter := make(map[string]struct{})
	for _, state := range b.order {
		if len(state.lint) == 0 {
			continue
		}
		findings = append(findings, state.lint...)
		name := state.unit.Checker.Name()
		if _, ok := seenLinter[name]; !ok {
			seenLinter[name] = struct{}{}
			linters = append(linters, name)
		}
		if state.lintBaseline == nil {
			state.lintBaseline = make(map[string]int)
		}
		for k, v := range countDiagnostics(state.lint) {
			state.lintBaseline[k] += v
		}
		state.hasLintBaseline = true
		state.lint = nil
	}
	if len(findings) == 0 {
		return ""
	}
	return b.format(
		fmt.Sprintf("Lint findings in what you changed (%s, as this project configures them); fix them, or say why they stay:", strings.Join(linters, ", ")),
		findings,
	)
}

// Busy reports whether any check for edits is running or pending.
func (b *BackgroundChecks) Busy() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.busyLocked()
}

func (b *BackgroundChecks) busyLocked() bool {
	for _, state := range b.units {
		if state.runningForEdits || len(state.edited) > 0 {
			return true
		}
	}
	return false
}

func (b *BackgroundChecks) notifyIfIdleLocked() {
	if b.busyLocked() {
		return
	}
	for _, ch := range b.idleWaiters {
		close(ch)
	}
	b.idleWaiters = nil
}

// WaitForIdle blocks until no check for edits is running or pending, timeout expires, or ctx is canceled.
func (b *BackgroundChecks) WaitForIdle(ctx context.Context, timeout time.Duration) {
	b.mu.Lock()
	if !b.busyLocked() || b.disposed {
		b.mu.Unlock()
		return
	}
	ch := make(chan struct{})
	b.idleWaiters = append(b.idleWaiters, ch)
	b.mu.Unlock()

	if timeout <= 0 {
		select {
		case <-ch:
		case <-ctx.Done():
		}
		return
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ch:
	case <-timer.C:
	case <-ctx.Done():
	}
}

// Settle runs before a run ends: it waits for the checks of the run's edits, up to ChecksWaitDuration, so their errors (sent through
// Report) still reach it. Then, when hasQueued says nothing is waiting for the agent, it returns the lint findings the agent's changes
// brought, which wait until the run would end so as not to interrupt it; "" when there are none (background-checks.ts settle).
func (b *BackgroundChecks) Settle(ctx context.Context, hasQueued func() bool) string {
	if wait := ChecksWaitDuration(); wait > 0 && b.Busy() {
		b.WaitForIdle(ctx, wait)
	}
	if hasQueued != nil && hasQueued() {
		return ""
	}
	return b.TakeLintReport()
}

// Dispose cancels all running/pending checks and wakes any WaitForIdle callers.
func (b *BackgroundChecks) Dispose() {
	b.cancel()
	b.mu.Lock()
	b.disposed = true
	for _, state := range b.units {
		if state.timer != nil {
			state.timer.Stop()
			state.timer = nil
		}
	}
	for _, ch := range b.idleWaiters {
		close(ch)
	}
	b.idleWaiters = nil
	b.mu.Unlock()
}
