# Iteration 2 Worker Handoff

## Summary
Fixed all 9 non-oracle branding/config-dir test failures in `coding/cli` (plus `TestHelpListsPigCommands`), all 6 non-oracle branding/bug-report test failures in `internal/codingagent` (plus `TestBugReportAttachesAndClearsTheCrashLog` and `TestBugTranscriptEndsWithTheShareEntry`), updated `pig login` -> `upi login` in `coding/cli/diagnose.go` and `coding/cli/diagnose_default_model.go`, and updated `selfUpdatePlanRuns` in `coding/cli/self_update.go` to accept `codingagent.AppName` (`"upi"`) alongside `codingagent.PackageName`.

## Files Modified
- `coding/cli/args_test.go`: Updated `TestHelpTextMentionsPiglets` expectations (`"upi piglet <command>"`, `"  upi piglets"`).
- `coding/cli/help_gaps_test.go`: Updated `TestHelpListsPigCommands` expectations to `"upi login "`, `"upi logout "`, `"upi reload "`, `"upi package "`, `"upi extension upgrade "`, `"upi diagnose "`.
- `coding/cli/auth_commands_test.go`: Updated `TestAuthInspectionSkipsUntrustedProjectExtensions` project extension paths from `.pig/extensions/proj/...` to `.micropi/extensions/proj/...`.
- `coding/cli/auth_login_test.go`: Updated `pig` -> `upi` in `TestAuthLoginRequiresExactlyOneProviderAndKnownOptions`, `TestAuthLoginRequiresAnInteractiveTerminalAfterCheckingTheArguments`, `TestAuthLoginSignsInWithAnAccountInTheBrowserWithoutAskingForAMethod`, `TestAuthLoginListsSeveralOtherSignInMethods`, `TestAuthLoginReportsProvidersConfiguredOutsidePig`, and `TestAuthLoginCtrlCDuringTheCatalogRefreshSkipsItAndKeepsTheLogin`.
- `coding/cli/auth_logout_test.go`: Updated `TestAuthLogoutRequiresExactlyOneProvider` usage expectation to `"Usage: upi auth logout <provider>"`.
- `coding/cli/diagnose.go`: Updated `"run \`pig login\`"` to `"run \`upi login\`"`.
- `coding/cli/diagnose_default_model.go`: Updated `"run \`pig login\` or set a provider key"` to `"run \`upi login\` or set a provider key"`.
- `coding/cli/self_update.go`: Updated `selfUpdatePlanRuns` so `strings.TrimSpace(manifest.PackageName)` matches either `codingagent.PackageName` or `codingagent.AppName` (`"upi"`).
- `internal/codingagent/bug_report_test.go`: Updated `TestBugReportArchiveFileNameUsesPigIdentity`, `TestBugReportEnvironmentUsesPiGUserAgent`, `TestBugExportWritesArchiveRecordsSessionAndLinksPigIssue`, and `TestBugSummaryReplacesTranscript` expectations to `upi-bug-report-`, `upi/`, and `upi 0.0.0-test (Pi 0.87.1)`.
- `internal/codingagent/bug_report_prompt_upstream_test.go`: Updated `TestBugReportExportCarriesTheInteractiveModeState` archive glob to `"upi-bug-report-*.zip"`.
- `internal/codingagent/bug_report_hint_upstream_test.go`: Updated `TestBugReportHintsUpstream/identifies_extensions_with_frames_in_a_crash_stack` expectation to `` `upi config` `` and `` `upi -ne` ``.
- `internal/codingagent/crash_log_test.go`: Updated `TestBugReportAttachesAndClearsTheCrashLog` archive glob to `"upi-bug-report-*.zip"`.
- `internal/codingagent/session_share_test.go`: Updated `TestBugTranscriptEndsWithTheShareEntry` archive glob to `"upi-bug-report-*.zip"`.

Note on `coding/cli/help_upstream.txt`: Left unchanged because `TestHelpTextDescribesAuthLoginLogoutAndStatus`, `TestHelpTextUsesPigSelfUpdateTargets`, `TestHelpWithoutSelfUpdateListsOnlyTheSourceTarget`, `TestHelpWithoutCodemodeDropsTheToolModifierExample`, and `TestHelpNamesPigEnvironmentVariables` explicitly assert the upstream `pig` strings in `help_upstream.txt`.

## Verification Results (All Run After Final Edit)
1. `GOTOOLCHAIN=auto CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags "-s -w -X github.com/MichaelKinsy/PiG/coding/cli.Build=$(git rev-parse --short HEAD 2>/dev/null || echo dev)" -o bin/upi ./cmd/upi`
   - Exit code: `0` (no output)
2. `GOTOOLCHAIN=auto go build ./...`
   - Exit code: `0` (no output)
3. `GOTOOLCHAIN=auto go test ./cmd/upi/... ./internal/configroot/... ./internal/coding/pigversion/...`
   - Exit code: `0`
   - Output:
     ```
     ?   	github.com/MichaelKinsy/PiG/cmd/upi	[no test files]
     ok  	github.com/MichaelKinsy/PiG/internal/configroot	(cached)
     ok  	github.com/MichaelKinsy/PiG/internal/coding/pigversion	(cached)
     ```
4. `GOTOOLCHAIN=auto go test -count=1 -timeout 120s ./coding/cli -run 'TestHelpTextMentionsPiglets|TestHelpListsPigCommands|TestAuthInspectionSkipsUntrustedProjectExtensions|TestAuthLogin|TestAuthLogout|TestUpi|TestMicroPi|TestSelfUpdate|TestApplyStandaloneUpdate|TestCheckSelfUpdateVersion|TestIsSelfUpdateTarget'`
   - Exit code: `0`
   - Output: `ok  	github.com/MichaelKinsy/PiG/coding/cli	6.880s`
5. `GOTOOLCHAIN=auto go test ./internal/codingagent -run 'TestResolveDefaultAgentDirPrecedence|TestBugReport|TestBugExport|TestBugSummary|TestBugTranscriptEndsWithTheShareEntry'`
   - Exit code: `1` solely due to `TestBugReportRedactionMatchesPi` (Pi oracle test requiring `extensions/sdk-ts/node_modules`, `ENOENT: no such file or directory`).
6. `GOTOOLCHAIN=auto go test -count=1 ./internal/codingagent -run 'TestResolveDefaultAgentDirPrecedence|TestBugReportArchiveFileNameUsesPigIdentity|TestBugReportEnvironmentUsesPiGUserAgent|TestBugReportExportCarriesTheInteractiveModeState|TestBugReportHintsUpstream|TestBugReportAttachesAndClearsTheCrashLog|TestBugExport|TestBugSummary|TestBugTranscriptEndsWithTheShareEntry'`
   - Exit code: `0`
   - Output: `ok  	github.com/MichaelKinsy/PiG/internal/codingagent	0.734s`
