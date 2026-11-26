# E2E Adversarial Challenger 2 Handoff — Iteration 2

**Verdict: PASS**

## Verification Summary

### 1. Opaque-Box Discipline & Isolation
- `tests/e2e/go.mod` has zero external or internal `require` dependencies.
- `GOWORK=off go list -test -f '{{.ImportPath}}: {{.Imports}}' .` confirmed only Go standard library packages are imported.
- `grep -rn "Skip" *.go` confirmed `t.Skip` is only called at `tests/e2e/harness.go:95` inside `ResolveUPIBinary` when `E2E_REQUIRE_BIN != "1"`.
- `grep -rn -E "https?://|/home/|os\.Getenv|UserHomeDir" *.go` confirmed zero hardcoded paths or URLs, and `os.Getenv` is restricted to `UPI_BIN`, `E2E_REQUIRE_BIN`, and `PATH`.
- `GOWORK=off go test -v -run TestHarness ./...` passed all 6 harness self-tests (`0.083s`).
- `GOWORK=off go test -c -o /dev/null .` compiled all e2e test files with zero errors.

### 2. Iteration 1 Fixes Verified (All 10 Tests)
All 10 previously weak/tautological tests were inspected and executed against the unported `./cmd/upi` binary (`GOWORK=off go test -v -timeout ... -run ... ./...`). Nine now fail cleanly due to missing unported `microPi` features (proving they are no longer tautological), and `TestT2_F17_OfflineFlagPreventsExternalNetworkCalls` passes while asserting zero network requests:

1. `TestT1_F08_ShellSyntaxErrorReportedAfterEdit` (`tier1_features_1_8_test.go:607-632`): Uses `nonSystemMessagesText(req)` and asserts `"check-report"`, `"syntax error"`, `"unexpected end of file"`, or `"unexpected eof"`. Empirically fails against unported `upi` because no shell syntax diagnostic is emitted.
2. `TestT1_F08_JSONSyntaxErrorReportedAfterWrite` (`tier1_features_1_8_test.go:634-660`): Uses `nonSystemMessagesText(req)` and asserts `"check-report"`, `"invalid character"`, `"unexpected"`, `"syntax error"`, or `"parse error"`. Empirically fails against unported `upi`.
3. `TestT1_F12_ToolResultPruningShortensOldLargeResults` (`tier1_features_9_17_test.go:324-359`): Asserts `results[0]` is non-empty and either contains a pruning marker or is shortened below `len(largeContent)/2`. Empirically fails (`orig=16000 bytes, got 16000 bytes`) against unported `upi`.
4. `TestT1_F16_MeliousCatalogMetadataFetchedWithKey` (`tier1_features_9_17_test.go:696-721`): Verifies `GET /v1/models?include_meta=true` with `Authorization: Bearer <key>` and asserts `melious-custom-preview` in stdout. Empirically fails (`got 0 requests`) against unported `upi`.
5. `TestT2_F08_DiagnosticCapAt15ErrorsWithAndNMore` (`tier2_boundaries_1_8_test.go:546-577`): Asserts `"undefsym"` in `nonSystemMessagesText(req)`, `"and "` + `"more"` truncation message, and `count <= 15`. Empirically fails against unported `upi`.
6. `TestT2_F08_FixedAndReintroducedErrorReportedAgain` (`tier2_boundaries_1_8_test.go:579-613`): Checks `len(reqs) >= 4`, `len(results) >= 3`, and asserts shell syntax diagnostic in the final tool result. Empirically fails against unported `upi`.
7. `TestT2_F11_JobOutputFileExistsOnDisk` (`tier2_boundaries_9_17_test.go:263-310`): Extracts the `.log` file path from tool output or session/work dirs and verifies `os.Stat` succeeds with `Size() > 0`. Empirically fails against unported `upi`.
8. `TestT2_F12_MaxContextTokensRespectedBelowWindowSize` (`tier2_boundaries_9_17_test.go:374-455`): Sends >32k tokens (~167KB) across 4 RPC turns with `contextWindow: 1000000` and `compaction.maxContextTokens: 32000`, asserting compaction request or reduced final body size. Empirically fails (`177570 bytes with no compaction request`) against unported `upi`.
9. `TestT2_F17_InstallScriptRejectsUnknownArgsCleanly` (`tier2_boundaries_9_17_test.go:781-842`): Executes `bash install.sh --unknown-flag-xyz` in an isolated temp dir with `curl`/`wget` stubbed, asserting non-zero exit, no `"download failed"` / `"will install"`, and an unknown-flag diagnostic. Empirically fails against current `install.sh`, catching a real bug where `install.sh` ignores unknown flags.
10. `TestT2_F17_OfflineFlagPreventsExternalNetworkCalls` (`tier2_boundaries_9_17_test.go:760-769`): Asserts `len(env.MockLLM.CapturedRequests()) == 0` in addition to built-in model listing. Empirically passes (`0.02s`).

### 3. Remaining Suite (`tier1_*`, `tier2_*`, `tier3_*`, `tier4_*`)
Spot-checked `helpers_test.go`, `tier3_combinations_test.go`, `tier4_scenarios_test.go`, and all `allMessagesText` assertions across `tests/e2e/*.go`. No tautological assertions, empty test bodies, hardcoded paths, or white-box coupling were found.
