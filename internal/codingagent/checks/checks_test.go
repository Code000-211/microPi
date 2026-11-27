package checks

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type stubChecker struct {
	name    string
	matches func(string) bool
	runFn   func(ctx context.Context, unit *CheckUnit) ([]Diagnostic, error)
}

func (s *stubChecker) Name() string             { return s.name }
func (s *stubChecker) Matches(path string) bool { return s.matches(path) }
func (s *stubChecker) Unit(path string) *CheckUnit {
	return &CheckUnit{Key: s.name + ":" + filepath.Dir(path), Checker: s, CWD: filepath.Dir(path), File: path}
}
func (s *stubChecker) Run(ctx context.Context, unit *CheckUnit) ([]Diagnostic, error) {
	return s.runFn(ctx, unit)
}

func TestEnabledCheckers(t *testing.T) {
	all := DefaultCheckers()
	if len(all) < 10 {
		t.Fatalf("expected >= 10 default checkers, got %d", len(all))
	}
	for _, off := range []string{"off", "0", "false"} {
		if got := EnabledCheckers(off, all); len(got) != 0 {
			t.Errorf("EnabledCheckers(%q) = %d, want 0", off, len(got))
		}
	}
	for _, on := range []string{"", "on"} {
		if got := EnabledCheckers(on, all); len(got) != len(all) {
			t.Errorf("EnabledCheckers(%q) = %d, want %d", on, len(got), len(all))
		}
	}
	allow := EnabledCheckers("go, json ,toml", all)
	if len(allow) != 3 {
		t.Fatalf("allowlist got %d checkers, want 3", len(allow))
	}
}

func TestBackgroundChecksBaselineDebounceOvertakeAndLint(t *testing.T) {
	dir := t.TempDir()
	fileA := filepath.Join(dir, "a.go")
	fileB := filepath.Join(dir, "b.go")

	var phase atomic.Int32
	var runCount atomic.Int32
	gate := make(chan struct{})
	inFlight := make(chan struct{}, 1)

	checker := &stubChecker{
		name:    "stub",
		matches: func(p string) bool { return strings.HasSuffix(p, ".go") },
		runFn: func(ctx context.Context, _ *CheckUnit) ([]Diagnostic, error) {
			runCount.Add(1)
			switch phase.Load() {
			case 0:
				// Baseline: pre-existing error at line 5 in a.go, plus a warning.
				return []Diagnostic{
					{File: fileA, Line: 5, Column: 1, Severity: SeverityError, Code: "E1", Message: "preexisting"},
					{File: fileA, Line: 2, Column: 1, Severity: SeverityWarning, Code: "W1", Message: "old lint"},
				}, nil
			case 1:
				// Signal in-flight, wait for gate so an overtake edit arrives mid-run.
				select {
				case inFlight <- struct{}{}:
				default:
				}
				select {
				case <-gate:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				// Stale run result that must be discarded.
				return []Diagnostic{
					{File: fileA, Line: 1, Column: 1, Severity: SeverityError, Code: "ESTALE", Message: "stale"},
				}, nil
			default:
				// Final run: preexisting moved to line 10 (same fingerprint!), plus 16 new errors + 1 new warning.
				out := []Diagnostic{
					{File: fileA, Line: 10, Column: 1, Severity: SeverityError, Code: "E1", Message: "preexisting"},
					{File: fileA, Line: 2, Column: 1, Severity: SeverityWarning, Code: "W1", Message: "old lint"},
					{File: fileA, Line: 3, Column: 1, Severity: SeverityWarning, Code: "W2", Message: "new lint"},
				}
				for i := 1; i <= 16; i++ {
					out = append(out, Diagnostic{
						File:     fileA,
						Line:     20 + i,
						Column:   1,
						Severity: SeverityError,
						Code:     fmt.Sprintf("E%d", i+10),
						Message:  fmt.Sprintf("err %d", i),
					})
				}
				// Error in unedited fileB should still be reported because baseline exists.
				out = append(out, Diagnostic{File: fileB, Line: 1, Column: 1, Severity: SeverityError, Code: "EB", Message: "other file"})
				return out, nil
			}
		},
	}

	var mu sync.Mutex
	var reports []string
	bc := NewBackgroundChecks(BackgroundChecksOptions{
		CWD:      dir,
		Checkers: []Checker{checker},
		Report: func(text string) {
			mu.Lock()
			reports = append(reports, text)
			mu.Unlock()
		},
	})
	defer bc.Dispose()

	// 1. OnRead starts 0ms baseline check and does NOT set Busy() = true.
	bc.OnRead(fileA)
	if bc.Busy() {
		t.Fatal("OnRead should not set Busy()=true")
	}
	deadline := time.Now().Add(2 * time.Second)
	for runCount.Load() < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if runCount.Load() != 1 {
		t.Fatalf("expected baseline run to complete, runCount=%d", runCount.Load())
	}

	// 2. OnEdited sets Busy() = true, waits 150ms, and we overtake mid-run.
	phase.Store(1)
	bc.OnEdited(fileA)
	if !bc.Busy() {
		t.Fatal("OnEdited should set Busy()=true")
	}
	select {
	case <-inFlight:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for in-flight check")
	}

	// Trigger overtake while phase 1 is blocked on gate.
	phase.Store(2)
	bc.OnEdited(fileA)
	close(gate)

	lintReport := bc.Settle(context.Background(), func() bool { return false })
	if bc.Busy() {
		t.Fatal("expected not busy after Settle")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(reports) != 1 {
		t.Fatalf("expected 1 error report (stale discarded), got %d: %+v", len(reports), reports)
	}
	rep := reports[0]
	if strings.Contains(rep, "preexisting") || strings.Contains(rep, "ESTALE") {
		t.Errorf("report should not contain preexisting or stale errors:\n%s", rep)
	}
	if !strings.Contains(rep, "... and 2 more") {
		t.Errorf("expected max 15 cap with '... and 2 more' (17 new errors), got:\n%s", rep)
	}
	if !strings.Contains(lintReport, "new lint") || strings.Contains(lintReport, "old lint") {
		t.Errorf("unexpected lint report:\n%s", lintReport)
	}
	// Second TakeLintReport is empty.
	if again := bc.TakeLintReport(); again != "" {
		t.Errorf("expected empty second TakeLintReport, got %q", again)
	}
}

func TestNewAgainstWithoutBaselineFiltersToEditedFiles(t *testing.T) {
	diags := []Diagnostic{
		{File: "/a.go", Line: 1, Column: 1, Severity: SeverityError, Message: "err a"},
		{File: "/b.go", Line: 1, Column: 1, Severity: SeverityError, Message: "err b"},
	}
	edited := map[string]struct{}{"/a.go": {}}
	fresh := newAgainst(diags, nil, false, edited)
	if len(fresh) != 1 || fresh[0].File != "/a.go" {
		t.Fatalf("got %+v, want only /a.go", fresh)
	}
}

func TestJSONTOMLYAMLAndGoCheckers(t *testing.T) {
	dir := t.TempDir()

	// JSON
	badJSON := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(badJSON, []byte("{\n  \"a\": \n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	diags, err := jsonChecker.Run(context.Background(), jsonChecker.Unit(badJSON))
	if err != nil || len(diags) != 1 || diags[0].Line < 2 {
		t.Fatalf("jsonChecker = (%+v, %v)", diags, err)
	}
	if jsonChecker.Matches(filepath.Join(dir, "tsconfig.json")) {
		t.Fatal("jsonChecker should ignore tsconfig.json")
	}

	// TOML
	badTOML := filepath.Join(dir, "bad.toml")
	if err := os.WriteFile(badTOML, []byte("a = 1\nb = [unclosed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	diags, err = tomlChecker.Run(context.Background(), tomlChecker.Unit(badTOML))
	if err != nil || len(diags) != 1 {
		t.Fatalf("tomlChecker = (%+v, %v)", diags, err)
	}

	// YAML
	badYAML := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(badYAML, []byte("a:\n  - b\n - c\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	diags, err = yamlChecker.Run(context.Background(), yamlChecker.Unit(badYAML))
	if err != nil || len(diags) != 1 {
		t.Fatalf("yamlChecker = (%+v, %v)", diags, err)
	}

	// Go (if go is on PATH)
	if OnPath("go") != "" {
		goDir := filepath.Join(dir, "goproj")
		if err := os.MkdirAll(goDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(goDir, "go.mod"), []byte("module example.com/test\n\ngo 1.21\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		goFile := filepath.Join(goDir, "main.go")
		if err := os.WriteFile(goFile, []byte("package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Printf(\"%d\", \"not-an-int\")\n}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		u := goChecker.Unit(goFile)
		if u == nil {
			t.Fatal("goChecker.Unit returned nil")
		}
		diags, err = goChecker.Run(context.Background(), u)
		if err != nil || len(diags) == 0 {
			t.Fatalf("goChecker expected vet error, got (%+v, %v)", diags, err)
		}
		errFile := filepath.Join(goDir, "err.go")
		if err := os.WriteFile(errFile, []byte("package main\n\nfunc run() {\n\tmissingHelperFunc()\n}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		var reports reportLog
		bc := NewBackgroundChecks(BackgroundChecksOptions{CWD: goDir, Checkers: []Checker{goChecker}, Report: reports.add})
		bc.OnEdited(errFile)
		bc.Settle(context.Background(), nil)
		bc.Dispose()
		if rep := strings.Join(reports.all(), "\n"); !strings.Contains(rep, "missingHelperFunc") {
			t.Fatalf("BackgroundChecks Go compile error report = %q, want missingHelperFunc", rep)
		}
	}
}

func TestSanitizeCompileFlags(t *testing.T) {
	flags := SanitizeCompileFlags(
		[]string{"clang++", "-c", "-o", "foo.o", "-MD", "-fno-canonical-system-headers", "-DTEST=1", "foo.cpp"},
		"/proj/src/foo.cpp",
		"/proj/src",
		"",
	)
	joined := strings.Join(flags, " ")
	if strings.Contains(joined, "-c ") || strings.Contains(joined, "foo.o") || strings.Contains(joined, "-fno-canonical-system-headers") {
		t.Fatalf("unexpected unsanitized flags: %v", flags)
	}
	if !strings.Contains(joined, "-x c++") || !strings.Contains(joined, "-std=c++20") || !strings.Contains(joined, "-DTEST=1") {
		t.Fatalf("missing expected flags: %v", flags)
	}
}

// µPi: PI_CHECKS_WAIT is read as background-checks.ts reads it, `(Number(process.env.PI_CHECKS_WAIT) || 30) * 1000`: 0 and what is not a
// number mean the default; a negative value does not wait.
func TestChecksWaitNegativeDoesNotBlockSettle(t *testing.T) {
	t.Setenv("PI_CHECKS_WAIT", "")
	if got := ChecksWaitDuration(); got != DefaultChecksWait {
		t.Fatalf("unset PI_CHECKS_WAIT = %v, want %v", got, DefaultChecksWait)
	}
	for _, fallback := range []string{"0", "NaN", "Inf", "abc"} {
		t.Setenv("PI_CHECKS_WAIT", fallback)
		if got := ChecksWaitDuration(); got != DefaultChecksWait {
			t.Fatalf("PI_CHECKS_WAIT=%q = %v, want %v", fallback, got, DefaultChecksWait)
		}
	}
	t.Setenv("PI_CHECKS_WAIT", " 2.5 ")
	if got := ChecksWaitDuration(); got != 2500*time.Millisecond {
		t.Fatalf("PI_CHECKS_WAIT=2.5 = %v, want 2.5s", got)
	}
	t.Setenv("PI_CHECKS_WAIT", "-1")
	if got := ChecksWaitDuration(); got != 0 {
		t.Fatalf("PI_CHECKS_WAIT=-1 = %v, want 0", got)
	}

	dir := t.TempDir()
	fileA := filepath.Join(dir, "a.go")
	gate := make(chan struct{})
	inFlight := make(chan struct{}, 1)

	checker := &stubChecker{
		name:    "slow",
		matches: func(p string) bool { return strings.HasSuffix(p, ".go") },
		runFn: func(ctx context.Context, _ *CheckUnit) ([]Diagnostic, error) {
			select {
			case inFlight <- struct{}{}:
			default:
			}
			select {
			case <-gate:
			case <-time.After(600 * time.Millisecond):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return nil, nil
		},
	}

	bc := NewBackgroundChecks(BackgroundChecksOptions{
		CWD:      dir,
		Checkers: []Checker{checker},
	})
	defer func() {
		close(gate)
		bc.Dispose()
	}()

	bc.OnEdited(fileA)
	select {
	case <-inFlight:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for slow check to start")
	}

	start := time.Now()
	report := bc.Settle(context.Background(), func() bool { return false })
	elapsed := time.Since(start)

	if report != "" {
		t.Fatalf("expected empty lint report while check still running, got %q", report)
	}
	if elapsed >= 250*time.Millisecond {
		t.Fatalf("Settle blocked for %v with PI_CHECKS_WAIT=-1; expected immediate return", elapsed)
	}
	if !bc.Busy() {
		t.Fatal("expected BackgroundChecks to still be Busy while slow check is in-flight")
	}
}

// An edit that follows a read before the read's baseline check has finished must not report the file's pre-existing errors as new.
func TestAwaitBaselineKeepsSlowBaselineForImmediateEdit(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "broken.go")
	release := make(chan struct{})
	var runs atomic.Int32
	checker := &stubChecker{
		name:    "stub",
		matches: func(p string) bool { return strings.HasSuffix(p, ".go") },
		runFn: func(ctx context.Context, _ *CheckUnit) ([]Diagnostic, error) {
			if runs.Add(1) == 1 {
				// The baseline is slower than the edit that follows the read.
				select {
				case <-release:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			return []Diagnostic{{File: file, Line: 3, Column: 17, Severity: SeverityError, Message: "undefined: preExistingErr"}}, nil
		},
	}
	var reports reportLog
	bc := NewBackgroundChecks(BackgroundChecksOptions{CWD: dir, Checkers: []Checker{checker}, Report: reports.add})
	t.Cleanup(bc.Dispose)

	bc.OnRead(file)
	awaited := make(chan struct{})
	go func() {
		bc.AwaitBaseline(context.Background(), file)
		close(awaited)
	}()
	select {
	case <-awaited:
		t.Fatal("AwaitBaseline returned while the baseline check was still running")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case <-awaited:
	case <-time.After(5 * time.Second):
		t.Fatal("AwaitBaseline did not return after the baseline check ended")
	}

	bc.OnEdited(file)
	bc.Settle(context.Background(), nil)
	if all := reports.all(); len(all) != 0 {
		t.Fatalf("pre-existing error reported as new:\n%s", strings.Join(all, "\n"))
	}
	if got := runs.Load(); got != 2 {
		t.Fatalf("checker ran %d times, want 2 (baseline, then the edit's check)", got)
	}
}

func TestAwaitBaselineReturnsAtOnceWithNegativeChecksWait(t *testing.T) {
	t.Setenv("PI_CHECKS_WAIT", "-1")
	dir := t.TempDir()
	file := filepath.Join(dir, "a.go")
	block := make(chan struct{})
	checker := &stubChecker{
		name:    "stub",
		matches: func(p string) bool { return strings.HasSuffix(p, ".go") },
		runFn: func(ctx context.Context, _ *CheckUnit) ([]Diagnostic, error) {
			select {
			case <-block:
			case <-ctx.Done():
			}
			return nil, nil
		},
	}
	bc := NewBackgroundChecks(BackgroundChecksOptions{CWD: dir, Checkers: []Checker{checker}})
	t.Cleanup(func() { close(block); bc.Dispose() })
	bc.OnRead(file)
	done := make(chan struct{})
	go func() { bc.AwaitBaseline(context.Background(), file); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("AwaitBaseline blocked although PI_CHECKS_WAIT=-1")
	}
}

// reportLog collects the reports a BackgroundChecks sends.
type reportLog struct {
	mu      sync.Mutex
	reports []string
}

func (r *reportLog) add(text string) {
	r.mu.Lock()
	r.reports = append(r.reports, text)
	r.mu.Unlock()
}

func (r *reportLog) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.reports...)
}

// A run that ends right after an edit still gets that edit's errors: Settle returns only after the check's report was sent.
func TestSettleReturnsAfterTheReportOfTheEditsCheck(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.go")
	checker := &stubChecker{
		name:    "stub",
		matches: func(p string) bool { return strings.HasSuffix(p, ".go") },
		runFn: func(context.Context, *CheckUnit) ([]Diagnostic, error) {
			time.Sleep(100 * time.Millisecond)
			return []Diagnostic{{File: file, Line: 1, Column: 1, Severity: SeverityError, Message: "broken"}}, nil
		},
	}
	reported := make(chan struct{})
	var reports reportLog
	bc := NewBackgroundChecks(BackgroundChecksOptions{CWD: dir, Checkers: []Checker{checker}, Report: func(text string) {
		// The report takes a while to reach the queue; Settle must not look at the queue before it did.
		time.Sleep(50 * time.Millisecond)
		reports.add(text)
		close(reported)
	}})
	t.Cleanup(bc.Dispose)
	bc.OnEdited(file)
	queued := false
	if lint := bc.Settle(context.Background(), func() bool {
		select {
		case <-reported:
			queued = true
		default:
		}
		return queued
	}); lint != "" {
		t.Fatalf("lint = %q", lint)
	}
	if !queued {
		t.Fatal("Settle looked at the queue before the report of the edit's check was sent")
	}
	if got := reports.all(); len(got) != 1 || got[0] != "New errors after your changes (stub check):\n  a.go:1:1: broken" {
		t.Fatalf("reports = %q", got)
	}
}

// Lint findings are listed in the order their units were first seen, as the Map of background-checks.ts iterates them.
func TestTakeLintReportListsUnitsInTheOrderTheyWereSeen(t *testing.T) {
	dir := t.TempDir()
	lintOf := func(name string) *stubChecker {
		return &stubChecker{
			name:    name,
			matches: func(p string) bool { return strings.HasSuffix(p, "."+name) },
			runFn: func(_ context.Context, unit *CheckUnit) ([]Diagnostic, error) {
				return []Diagnostic{{File: unit.File, Line: 1, Column: 1, Severity: SeverityWarning, Code: "W", Message: "finding in " + filepath.Base(unit.File)}}, nil
			},
		}
	}
	names := []string{"zz", "aa", "mm", "bb", "yy", "cc", "xx", "dd"}
	var checkers []Checker
	for _, name := range names {
		checkers = append(checkers, lintOf(name))
	}
	for range 5 {
		bc := NewBackgroundChecks(BackgroundChecksOptions{CWD: dir, Checkers: checkers})
		for _, name := range names {
			// One folder per file: the stub's unit is the file's folder.
			bc.OnEdited(filepath.Join(dir, name, "f."+name))
		}
		report := bc.Settle(context.Background(), nil)
		bc.Dispose()
		lines := strings.Split(report, "\n")
		want := "Lint findings in what you changed (" + strings.Join(names, ", ") + ", as this project configures them); fix them, or say why they stay:"
		if lines[0] != want {
			t.Fatalf("title = %q, want %q", lines[0], want)
		}
		for i, name := range names {
			if !strings.HasSuffix(lines[i+1], "finding in f."+name) {
				t.Fatalf("line %d = %q, want the finding of %s", i+1, lines[i+1], name)
			}
		}
	}
}

func TestSessionCheckersRegisterCppLast(t *testing.T) {
	if clangBinary() == "" {
		t.Skip("no clang on PATH")
	}
	t.Setenv("PI_CHECKS", "")
	all := SessionCheckers(nil, t.TempDir())
	if len(all) != len(DefaultCheckers())+1 || all[len(all)-1].Name() != "cpp" {
		t.Fatalf("session checkers = %d, last %q; want DefaultCheckers then cpp", len(all), all[len(all)-1].Name())
	}
	t.Setenv("PI_CHECKS", "cpp")
	if only := SessionCheckers(nil, t.TempDir()); len(only) != 1 || only[0].Name() != "cpp" {
		t.Fatalf("PI_CHECKS=cpp gives %d checkers", len(only))
	}
}
