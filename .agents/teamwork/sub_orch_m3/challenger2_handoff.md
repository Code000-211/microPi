# M3 Challenger 2 Handoff — Background Checks & Concurrency

## Verdict: **FAIL**

One spec/edge-case bug found in `PI_CHECKS_WAIT=0` handling (`ChecksWaitDuration` + `Settle` in `internal/codingagent/checks/background_checks.go`). All other 8 areas passed empirical testing under `go test -race`.

---

## Finding (Blocking)

### `PI_CHECKS_WAIT=0` falls back to 30s and blocks `Settle`
- **Location**: `internal/codingagent/checks/background_checks.go` (`ChecksWaitDuration` lines 25–32, `Settle` lines 393–401, `WaitForIdle` lines 364–389).
- **What happens**:
  1. `ChecksWaitDuration()` checks `if secs, err := strconv.ParseFloat(raw, 64); err == nil && secs > 0`, so `PI_CHECKS_WAIT=0` is rejected and falls back to `DefaultChecksWait` (`30s`).
  2. Furthermore, `WaitForIdle(ctx, timeout)` treats `timeout <= 0` as "wait indefinitely without a timer", and `Settle()` calls `b.WaitForIdle(ctx, ChecksWaitDuration())` whenever `b.Busy()` is true.
- **Empirical Evidence**:
  - `PI_CHECKS_WAIT=0` -> `ChecksWaitDuration()` returned `30s`.
  - `Settle(context.Background(), nil)` with `PI_CHECKS_WAIT=0` on a 1s slow stub checker blocked for `1.150687515s` instead of returning immediately.
- **Expected**:
  - Per `tests/e2e/tier2_boundaries_1_8_test.go:526` (`TestT2_F08_PIChecksWaitZeroDoesNotBlockOnSlowCheck`), `PI_CHECKS_WAIT=0` must not block on slow checks.
  - `ChecksWaitDuration()` should accept `secs >= 0` (returning `0` for `"0"`), and `Settle()` should skip `WaitForIdle` when `wait <= 0`.

---

## Empirical Test Matrix (`go test -v -race ./internal/codingagent/checks/...`)

Note: Temporary adversarial test file `internal/codingagent/checks/adversarial_m3_test.go` was executed with `go test -v -race` and removed afterward so the repository stays clean.

1. **`PI_CHECKS` parsing**: **PASS**
   - `"off"`, `"0"`, `"false"` (including surrounding whitespace) -> `0` checkers (`Enabled() == false`).
   - `"on"`, `""`, `"   "` -> all 16 default checkers.
   - `"json,yaml"`, `" , json , , yaml , "` -> exact allowlist (`Runs("json") == true`, `Runs("yaml") == true`, `Runs("go") == false`).
   - `"nonexistent"` -> `0` checkers.
2. **`PI_CHECKS_WAIT` & `WaitForIdle` / `Settle`**: **FAIL** (see Finding above)
   - Default (unset / `""` / `"invalid"`) -> `30s`.
   - Custom float `"0.25"` -> `250ms`.
   - `"0"` -> returned `30s` and blocked `Settle` for `1.15s` on a 1s check.
   - `WaitForIdle` timeout (`50ms`) and `ctx` cancellation (`40ms`) both unblocked promptly.
3. **Baseline diffing (line shift & no re-report)**: **PASS**
   - Pre-existing error from `OnRead` at `line 5:2` shifted to `line 15:8` on `OnEdited` -> NOT reported.
   - Newly introduced error `E2` at `line 20:1` -> reported once.
   - Subsequent `OnEdited` with identical `E2` -> NOT re-reported.
4. **No-baseline unit (untouched files ignored)**: **PASS**
   - Direct `OnEdited(fileA)` without prior `OnRead`: errors and warnings in untouched `fileB` were ignored (`reports == 0`, `TakeLintReport() == ""`).
5. **`OnRead` non-blocking / non-busy**: **PASS**
   - `OnRead` did not set `Busy() == true` (`false`), and `WaitForIdle` returned immediately (`<50ms`) even while `OnRead`'s check was blocked in-flight.
6. **Overtake / dirty re-run under `-race`**: **PASS**
   - `OnEdited` arriving while a check was in-flight discarded stale findings (`ESTALE`), re-ran cleanly, and passed 8-goroutine concurrent stress testing under `-race` with zero data races.
7. **Lint accumulation (`TakeLintReport`)**: **PASS**
   - Warnings were withheld from `Report` callback during `OnEdited`.
   - First `TakeLintReport()` returned only new warnings (excluding baseline warnings).
   - Second `TakeLintReport()` returned `""`.
8. **Real checkers (JSON/TOML/YAML/Go/Shell)**: **PASS**
   - `jsonChecker`: caught syntax error at `Line:4 Column:2`.
   - `tomlChecker`: caught syntax error at `Line:3`.
   - `yamlChecker`: caught syntax error at `Line:3`.
   - `shellChecker` (`bash -n`): caught unclosed `if` at `Line >= 3`.
   - `goChecker` (`go vet`): caught `fmt.Printf` format mismatch at `Line:6`.
9. **`Dispose()`**: **PASS**
   - Canceled in-flight checker context, unblocked concurrent `WaitForIdle`, and handled double `Dispose()` without panic.
