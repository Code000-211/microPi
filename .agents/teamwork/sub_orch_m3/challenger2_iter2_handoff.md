# Challenger 2 (Iteration 2) Handoff — Background Checks & `PI_CHECKS_WAIT=0`

**Verdict: PASS**

## Scope & Target
- Audited [`background_checks.go`](file:///home/antigravity/Code000-211/microPi/internal/codingagent/checks/background_checks.go) (`ChecksWaitDuration`, `Settle`, `WaitForIdle`, `OnRead`, `OnEdited`, `runUnit`, `TakeLintReport`).
- Ran existing unit tests and temporary adversarial tests (3 iterations under `go test -race`) in `./internal/codingagent/checks/...`, then removed the temporary test file.

## Empirical Results

1. **`PI_CHECKS_WAIT=0` with slow (1s) check in flight & pending**
   - Verified `ChecksWaitDuration()` returns `0` for `"0"`, `"0.0"`, and `" 0 "`.
   - Verified `Settle()` returns immediately (`< 50ms`) without blocking both while an edit is in the 150ms debounce window and while a 1s check is actively in-flight, leaving `Busy() == true` and returning `""`.

2. **Invalid / empty / negative `PI_CHECKS_WAIT` fallbacks**
   - Verified `ChecksWaitDuration()` falls back to `DefaultChecksWait` (`30s`) for `""`, `"   "`, `"invalid"`, `"-5"`, `"-0.1"`, `"NaN"`, `"Inf"`, `"-Inf"`, and `"+Inf"`.

3. **`PI_CHECKS_WAIT="0.05"` (50ms) timeout**
   - Verified `ChecksWaitDuration()` returns `50ms`.
   - With a 1s check in-flight, `Settle()` waited ~50ms (within `[40ms, 200ms]`), unblocked on timeout with `""` while `Busy() == true`, and after the check completed `WaitForIdle()` + `TakeLintReport()` returned the deferred lint finding.

4. **Overtake / dirty re-run under `-race`, baseline line-shift invariance, and `TakeLintReport()`**
   - Existing suite `go test -race -v ./internal/codingagent/checks/...` passed (6/6 tests, 2.625s).
   - Adversarial suite `go test -race -v -count=3 ./internal/codingagent/checks/... -run TestAdversarial` passed (3/3 iterations, 4.222s, 0 race detector warnings).
   - Verified stale in-flight results are discarded on overtake, rapid dirty re-edits trigger a clean re-run, pre-existing errors/lints shifted in line/column are not re-reported, `hasQueued() == true` skips `TakeLintReport()` consumption, and `TakeLintReport()` promotes reported lints into `lintBaseline` so subsequent edits and calls do not re-report them.
