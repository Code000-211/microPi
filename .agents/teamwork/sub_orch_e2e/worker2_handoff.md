# E2E Test Suite Worker 2 Handoff

## Summary
Fixed all 10 weak/tautological assertions identified by Challenger 2 in `tests/e2e/`, using only Go standard library packages and preserving full opaque-box discipline.

## Changes Made

1. **`tests/e2e/helpers_test.go`**
   - Added `nonSystemMessagesText(req CapturedRequest) string` to inspect non-system/non-developer messages without matching instructions in the system prompt.

2. **`tests/e2e/tier1_features_1_8_test.go`**
   - `TestT1_F08_ShellSyntaxErrorReportedAfterEdit`: Removed tautological `script.sh` filename check; now asserts `nonSystemMessagesText(req)` contains `"check-report"`, `"syntax error"`, `"unexpected end of file"`, or `"unexpected eof"`.
   - `TestT1_F08_JSONSyntaxErrorReportedAfterWrite`: Removed tautological `config.json` filename check; now asserts `nonSystemMessagesText(req)` contains `"check-report"`, `"invalid character"`, `"unexpected"`, `"syntax error"`, or `"parse error"`.

3. **`tests/e2e/tier1_features_9_17_test.go`**
   - `TestT1_F12_ToolResultPruningShortensOldLargeResults`: Increased file size to ~16KB (12 reads = ~192KB > 40k tokens) and asserted that `results[0]` (outside `keepRecent=8`) is non-empty and either contains a pruning/cleared marker (`cleared from the context to save space`, `cleared`, `pruned`) or is shortened to `< len(largeContent)/2`.
   - `TestT1_F16_MeliousCatalogMetadataFetchedWithKey`: Asserts `MockLLM.CapturedRequests()` includes `GET /v1/models` with `include_meta=true` and a non-empty `Authorization: Bearer ...` header, AND asserts `melious-custom-preview` appears in `upi --list-models melious` stdout.

4. **`tests/e2e/tier2_boundaries_1_8_test.go`**
   - `TestT2_F08_DiagnosticCapAt15ErrorsWithAndNMore`: Asserts `nonSystemMessagesText(req)` contains `"undefsym"`, includes the `"and "` + `"more"` truncation message, and has `<= 15` `undefsym` findings.
   - `TestT2_F08_FixedAndReintroducedErrorReportedAgain`: Asserts `reqs` has `>= 4` requests and the final tool result (after `tc3` reintroduces the syntax error) contains `"check-report"`, `"syntax error"`, `"unexpected end of file"`, or `"unexpected eof"`.

5. **`tests/e2e/tier2_boundaries_9_17_test.go`**
   - `TestT2_F11_JobOutputFileExistsOnDisk`: Extracts the `.log` path from tool output (falling back to walking `SessionDir`/`MicropiHome`/`WorkDir`), calls `os.Stat` (non-empty) and `os.ReadFile`, and asserts the file on disk contains `DISK_LOG_CHECK`.
   - `TestT2_F12_MaxContextTokensRespectedBelowWindowSize`: Configures `mock-coder` with `contextWindow: 1000000` and `settings.json` with `compaction.maxContextTokens: 32000`, sends >32k tokens (~167KB across 4 RPC turns + final check), and asserts either a compaction/summarization LLM request was made or `len(lastReq.Body) < totalSent`.
   - `TestT2_F17_InstallScriptRejectsUnknownArgsCleanly`: Executes `bash install.sh --unknown-flag-xyz` in an isolated `t.TempDir()` with `HOME`, `MICROPI_INSTALL`, `MICROPI_BIN_DIR`, and stubbed `curl`/`wget` in `PATH` (zero network/host impact), strips `tmp` from output, asserts non-zero exit code, no download/install attempt, and an unknown/invalid flag diagnostic.
   - `TestT2_F17_OfflineFlagPreventsExternalNetworkCalls`: Asserts `len(env.MockLLM.CapturedRequests()) == 0` when running `upi --offline --list-models melious`.

## Verification
In `/home/antigravity/Code000-211/microPi/tests/e2e`:
- `GOWORK=off go vet ./...`: PASS
- `GOWORK=off go test -c ./...` (and `rm -f e2e.test`): PASS
- `GOWORK=off go test -count=1 -v -run TestHarness ./...`: PASS (6/6 harness self-tests)
