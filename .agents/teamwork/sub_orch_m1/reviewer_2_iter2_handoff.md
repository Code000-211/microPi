# Iteration 2 Reviewer 2 Handoff

## Verdict: APPROVE

## Scope & Inspection
Reviewed M1 requirements (`cmd/upi/`, `~/.micropi`, `MICROPI_HOME`, `MICROPI_CODING_AGENT_DIR`, `upi version`/`--version`, `upi doctor`, `upi update`) and Iteration 2 changes in `coding/cli/` and `internal/codingagent/`. No project source files were modified.

### Verified Implementation & Iteration 2 Changes
- `cmd/upi/main.go`: Entrypoint invokes `cli.Main(os.Args[1:], nil)` and builds cleanly with `CGO_ENABLED=0`.
- `internal/configroot/configroot.go`: `Resolve()` checks `MICROPI_HOME`, then `PIG_HOME`, then `XDG_CONFIG_HOME/micropi`, then `~/.micropi`.
- `internal/codingagent/paths.go` & `upi_paths_test.go`: Uses `CONFIG_DIR_NAME = ".micropi"` and `ENV_AGENT_DIR = "MICROPI_CODING_AGENT_DIR"`.
- `coding/cli/main.go`: `cliVersionString()` outputs `µPi 0.1.0 (PiG 0.8.0+1.6.0)` and `detailedVersionString()` outputs `µPi`, `pig`, `upstream pi`, `go`, `platform`, and `build` lines.
- `coding/cli/doctor.go`: `runDoctor` implements `upi doctor [--offline]` with redaction and `upi login` hint.
- `coding/cli/diagnose.go` & `coding/cli/diagnose_default_model.go`: Updated hints from `pig login` to `upi login`.
- `coding/cli/self_update.go`: `selfUpdatePlanRuns` accepts both `codingagent.PackageName` and `codingagent.AppName` (`"upi"`).
- `coding/cli/` tests (`args_test.go`, `help_gaps_test.go`, `auth_commands_test.go`, `auth_login_test.go`, `auth_logout_test.go`, `upi_commands_test.go`): Updated to `upi` / `.micropi` expectations and passing.
- `internal/codingagent/` tests (`bug_report_test.go`, `bug_report_prompt_upstream_test.go`, `bug_report_hint_upstream_test.go`, `crash_log_test.go`, `session_share_test.go`, `upi_paths_test.go`): Updated to `upi` / `upi-bug-report-*.zip` expectations and passing.

## Build & Test Results (Independently Executed)
1. `GOTOOLCHAIN=auto CGO_ENABLED=0 go build -buildvcs=false -trimpath -o /tmp/upi-review-bin ./cmd/upi && /tmp/upi-review-bin --version && /tmp/upi-review-bin version && rm -f /tmp/upi-review-bin`
   - Exit code: `0`
   - Output:
     ```
     µPi 0.1.0 (PiG 0.8.0+1.6.0)
     µPi: 0.1.0
     pig: 0.8.0
     upstream pi: 1.6.0
     go: go1.27.4
     platform: linux/amd64
     build: dev
     ```
2. `GOTOOLCHAIN=auto go test -count=1 ./cmd/upi/... ./internal/configroot/... ./internal/coding/pigversion/...`
   - Exit code: `0`
   - Output:
     ```
     ?   	github.com/MichaelKinsy/PiG/cmd/upi	[no test files]
     ok  	github.com/MichaelKinsy/PiG/internal/configroot	0.125s
     ok  	github.com/MichaelKinsy/PiG/internal/coding/pigversion	0.033s
     ```
3. `GOTOOLCHAIN=auto go test -count=1 -timeout 120s ./coding/cli -run 'TestHelpTextMentionsPiglets|TestHelpListsPigCommands|TestAuthInspectionSkipsUntrustedProjectExtensions|TestAuthLogin|TestAuthLogout|TestUpi|TestMicroPi|TestSelfUpdate|TestApplyStandaloneUpdate|TestCheckSelfUpdateVersion|TestIsSelfUpdateTarget'`
   - Exit code: `0`
   - Output: `ok  	github.com/MichaelKinsy/PiG/coding/cli	21.995s`
4. `GOTOOLCHAIN=auto go test -count=1 ./internal/codingagent -run 'TestResolveDefaultAgentDirPrecedence|TestBugReportArchiveFileNameUsesPigIdentity|TestBugReportEnvironmentUsesPiGUserAgent|TestBugReportExportCarriesTheInteractiveModeState|TestBugReportHintsUpstream|TestBugReportAttachesAndClearsTheCrashLog|TestBugExport|TestBugSummary|TestBugTranscriptEndsWithTheShareEntry'`
   - Exit code: `0`
   - Output: `ok  	github.com/MichaelKinsy/PiG/internal/codingagent	3.290s`
