# E2E Adversarial Challenger 2 Handoff

**Verdict: FAIL**

## Summary
- **Criterion 1 (Zero internal `microPi`/`PiG` imports — opaque-box discipline): PASS**
  - `tests/e2e/go.mod` defines a standalone module (`github.com/Code000-211/microPi/tests/e2e`, `go 1.24.3`) with zero external or internal `require` dependencies.
  - `GOWORK=off go list -test -f '{{.ImportPath}}: {{.Imports}}' .` confirmed only Go standard library packages are imported (`bufio`, `bytes`, `context`, `encoding/json`, `errors`, `fmt`, `io`, `net/http`, `net/http/httptest`, `os`, `os/exec`, `path/filepath`, `strings`, `sync`, `testing`, `time`, `reflect`).
- **Criterion 2 (Tier 1–4 test bodies — assertion quality, no empty bodies, no unconditional `t.Skip`): FAIL**
  - Zero empty test bodies and zero unconditional `t.Skip` calls were found.
  - However, **10 tests in Tier 1 and Tier 2 contain weak or tautological assertions** that pass even when the underlying feature is absent or broken (detailed below).
- **Criterion 3 (`t.Skip` restricted to `ResolveUPIBinary` when binary/source absent and `E2E_REQUIRE_BIN != 1`): PASS**
  - `grep -rn "Skip" tests/e2e/*.go` confirmed `t.Skip` is only called once, at `tests/e2e/harness.go:95` inside `ResolveUPIBinary`, and is skipped only when `resolveShouldSkip` is true and `os.Getenv("E2E_REQUIRE_BIN") != "1"`.

## Evidence: Weak / Tautological Assertions (Criterion 2)

1. **`tests/e2e/tier1_features_1_8_test.go:607-626` — `TestT1_F08_ShellSyntaxErrorReportedAfterEdit`**
   - Asserts `if !strings.Contains(msgText, "syntax") && !strings.Contains(msgText, "unexpected end of file") && !strings.Contains(msgText, "script.sh")`.
   - Because the user prompt is `"break script.sh"`, `allMessagesText(req)` always contains `"script.sh"`, making the check tautologically true even if no shell syntax diagnostic is emitted.

2. **`tests/e2e/tier1_features_1_8_test.go:628-645` — `TestT1_F08_JSONSyntaxErrorReportedAfterWrite`**
   - Asserts `assertContains(t, msgText, "config.json", "JSON diagnostic references file")`.
   - `"config.json"` is already present in `allMessagesText(req)` from the `read` and `write` tool results, so this passes without any JSON diagnostic.

3. **`tests/e2e/tier1_features_9_17_test.go:324-348` — `TestT1_F12_ToolResultPruningShortensOldLargeResults`**
   - Comment states `// Verify all tool results in the final request are bounded/pruned when over 40k tokens`, but only asserts `if len(results) < 12`. It never checks that `len(results[0])` was shortened or contains a pruning marker.

4. **`tests/e2e/tier1_features_9_17_test.go:685-695` — `TestT1_F16_MeliousCatalogMetadataFetchedWithKey`**
   - Configures `MockLLM.SetModels(...)` with `melious-custom-preview`, runs `--list-models melious`, and only asserts `assertContains(t, res.Stdout, "glm-5.3", ...)` (already a built-in model), never asserting `melious-custom-preview`.

5. **`tests/e2e/tier2_boundaries_1_8_test.go:546-568` — `TestT2_F08_DiagnosticCapAt15ErrorsWithAndNMore`**
   - Only asserts `assertContains(t, strings.ToLower(msgText), "undefsym", ...)`; never verifies the 15-error cap or `"more"` truncation message.

6. **`tests/e2e/tier2_boundaries_1_8_test.go:570-590` — `TestT2_F08_FixedAndReintroducedErrorReportedAgain`**
   - Only asserts `assertContains(t, strings.ToLower(allMessagesText(req)), "reintro.sh", ...)`, which is already in the `edit` tool results even if no diagnostic is emitted.

7. **`tests/e2e/tier2_boundaries_9_17_test.go:260-275` — `TestT2_F11_JobOutputFileExistsOnDisk`**
   - Never checks any log file on disk; only checks `toolResultMessages(req)` contains `"DISK_LOG_CHECK"`.

8. **`tests/e2e/tier2_boundaries_9_17_test.go:333-342` — `TestT2_F12_MaxContextTokensRespectedBelowWindowSize`**
   - Sets `maxContextTokens: 32000`, sends `"hi"`, and only asserts `"CAP_32K_OK"` in stdout without exercising context capping.

9. **`tests/e2e/tier2_boundaries_9_17_test.go:664-676` — `TestT2_F17_InstallScriptRejectsUnknownArgsCleanly`**
   - Never executes `install.sh` with an unknown arg; only reads `install.sh` and checks `len(data) == 0`.

10. **`tests/e2e/tier2_boundaries_9_17_test.go:647-652` — `TestT2_F17_OfflineFlagPreventsExternalNetworkCalls`**
    - Runs `--offline --list-models melious` and only checks `"glm-5.3"` (built-in), without verifying zero network requests were attempted.
