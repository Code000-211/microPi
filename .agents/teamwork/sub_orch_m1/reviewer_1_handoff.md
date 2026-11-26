# Reviewer 1 Handoff — Full-Suite Build & Test

**Verdict: REJECT**

## 1. Summary
Go toolchain (`go1.27.4`), full-repo build (`go build ./...`), and static `CGO_ENABLED=0` binary build (`./cmd/upi`) all succeed. However, running the full test suites for `./coding/cli/...` and `./internal/codingagent/...` (rather than only the worker's targeted `-run` filters) reveals **non-oracle unit test failures** caused by the `pig` -> `upi` / `.micropi` rebranding where existing tests still assert `"pig"` CLI strings, `"pig"` user-agent/archive names, or `.pig` project paths.

## 2. Independent Verification Results

### Toolchain & Builds
- `GOTOOLCHAIN=auto go version` -> `go version go1.27.4 linux/amd64` (**PASS**)
- `GOTOOLCHAIN=auto go build ./...` -> exit `0` (**PASS**)
- `GOTOOLCHAIN=auto CGO_ENABLED=0 go build -buildvcs=false -trimpath -o /tmp/upi-rev1 ./cmd/upi` -> exit `0` (**PASS**)
  - `file /tmp/upi-rev1` -> `ELF 64-bit LSB executable, x86-64, version 1 (SYSV), statically linked, ...`
  - `ldd /tmp/upi-rev1` -> `not a dynamic executable`
  - `/tmp/upi-rev1` removed after inspection.

### Core Package Tests
- `./cmd/upi/...`: `? github.com/MichaelKinsy/PiG/cmd/upi [no test files]` (exit `0`)
- `./internal/configroot/...`: **PASS** (`0.036s`; 6 top-level tests / 32 subtests passed, 0 failed, 0 skipped)
- `./internal/coding/pigversion/...`: **PASS** (`0.013s`; 3 tests passed, 0 failed, 0 skipped)
- `./coding/cli/...` (`GOTOOLCHAIN=auto go test -v -timeout 60s ./coding/cli/...`): **FAIL** (exit `1`; **107 PASS, 14 FAIL, 1 SKIP**, plus 60s timeout during `TestAuthLoginRoutesToTheExtensionThatDeclaredTheProviderWhenNamesCollide`, which passed in `3.73s` when run individually).
  - **Non-oracle failures (branding / config-dir regressions)**:
    - `TestHelpTextMentionsPiglets`
    - `TestAuthInspectionSkipsUntrustedProjectExtensions` (`trusted=true: configs = []subprocess.ExtConfig{}, want 1`)
    - `TestAuthLoginRequiresExactlyOneProviderAndKnownOptions` (`errors ["Usage: upi auth login <provider> [--api-key] [--no-browser]"]`)
    - `TestAuthLoginRequiresAnInteractiveTerminalAfterCheckingTheArguments` (`"\"upi auth login ...\" requires an interactive terminal"`)
    - `TestAuthLoginSignsInWithAnAccountInTheBrowserWithoutAskingForAMethod` (got `"upi auth login login-test --no-browser"`, test expects `"pig auth login ..."`)
    - `TestAuthLoginListsSeveralOtherSignInMethods` (got `"upi auth login login-test --no-browser"`, test expects `"pig auth login ..."`)
    - `TestAuthLoginReportsProvidersConfiguredOutsidePig` (`"Login Test is configured outside upi (AWS profile)."`)
    - `TestAuthLoginCtrlCDuringTheCatalogRefreshSkipsItAndKeepsTheLogin` (`"... upi refreshes it on next start."`)
    - `TestAuthLogoutRequiresExactlyOneProvider` (`errors ["Usage: upi auth logout <provider>"]`)
  - **Other failures in `coding/cli`**:
    - `TestAuthCommandMatchesPi` (Pi oracle)
    - `TestEmbeddedPackedAuthDiscoveryAndLoginByLanguage` / `python`
- `./internal/codingagent/...` (`GOTOOLCHAIN=auto go test -v -timeout 60s ./internal/codingagent/...`): **FAIL** (exit `1`; **1086 PASS, 78 FAIL, 7 SKIP** across subpackages).
  - Passing subpackages: `frontmatter` (`0.690s`), `prompts` (`1.209s`), `sessiongen` (`0.048s`).
  - Failing subpackages: `internal/codingagent` (`60.070s` timeout), `compaction` (`33.337s`), `configschema` (`3.011s`), `export` (`2.410s`), `llama` (`20.337s`), `timings` (`0.111s`), `tools` (`60.027s` timeout).
  - **Non-oracle failures (branding / bug-report regressions in `internal/codingagent`)**:
    - `TestBugReportArchiveFileNameUsesPigIdentity` (got `"upi-bug-report-0190.zip"`)
    - `TestBugReportEnvironmentUsesPiGUserAgent` (got `"upi/test-0.8.0+1.6.0 ..."`, wants `"pig/test-0.8.0+1.6.0 ..."`)
    - `TestBugReportExportCarriesTheInteractiveModeState` (`archives = []`)
    - `TestBugExportWritesArchiveRecordsSessionAndLinksPigIssue` (`archives = []`)
    - `TestBugSummaryReplacesTranscript` (`archives = [] summaries = 1`)
    - `TestBugReportHintsUpstream/identifies_extensions_with_frames_in_a_crash_stack`
  - **Environment / Oracle failures in `internal/codingagent/...`**:
    - Missing `extensions/sdk-ts/node_modules` and `.upstream/current` (Pi oracle tests)
    - Missing `fd` / `fdfind` on `PATH` (several `tools` find tests)
    - `TestKillTrackedDetachedChildrenEndsRunningMCPStdioServers` in `internal/codingagent/tools` failed near the 60s package timeout (`condition not reached`).

## 3. Required Fixes Before Approval
1. Update the failing non-oracle tests in `coding/cli` (`help_test.go` / `args_test.go`, `auth_commands_test.go`, `auth_login_test.go`, `auth_logout_test.go`) and `internal/codingagent` (`bug_report_test.go`, `bug_report_prompt_upstream_test.go`, etc.) to match `upi` / `µPi` / `.micropi` branding and directory names (or fix any remaining `.pig` vs `.micropi` lookup mismatch in `auth_commands_test.go` / `bug_report` if the code itself is still looking at `.pig` or vice versa).
2. Re-run `go test ./coding/cli/...` and `go test ./internal/codingagent` (without `-run` filtering out those non-oracle tests) and confirm all non-oracle tests pass.
