# E2E Adversarial Challenger 1 Handoff

**Verdict:** `PASS`

## 1. Secrets, External URLs, Host `$HOME`, and Opaque-Box Isolation
- **Command:** `grep -rnE "https?://|sk-[a-zA-Z0-9]|ghp_|AKIA|/home/|/Users/|~/" tests/e2e/`
  - Only matches:
    - `tests/e2e/harness_test.go:184-185`: `t.Setenv("OPENAI_API_KEY", "sk-real-secret-should-be-stripped")` and `t.Setenv("ANTHROPIC_API_KEY", "sk-ant-secret-should-be-stripped")` (synthetic test values).
    - `tests/e2e/tier1_features_1_8_test.go:122`: comment `// Verify nothing was written to a real ~/.micropi outside temp Home/MicropiHome`.
- **Command:** `grep -rnE "os\.UserHomeDir|os\.Getenv\(\"HOME\"\)|\$HOME|\.micropi" tests/e2e/`
  - Only `tests/e2e/harness.go:818` (`filepath.Join(e.Home, ".micropi", "agent")`, using `e.Home` under `t.TempDir()`) and the comment above.
- **Command:** `grep -rnE "^\s*\"github\.com/|^\s*\"golang\.org/" tests/e2e/`
  - Returned `NO_EXTERNAL_OR_INTERNAL_IMPORTS`. `tests/e2e/go.mod` declares `go 1.24.3` with zero external or internal repository dependencies.

## 2. `NewTestEnv` Env Stripping & Directory Isolation
- `tests/e2e/harness.go` (`NewTestEnv`, `ChildEnv`) creates isolated `t.TempDir()` directories for `Home`, `MicropiHome`, `AgentDir`, `SessionDir`, `WorkDir`, `FakeSSHDir`, and `RemoteDir`.
- `ChildEnv()` strips 11 provider keys (`OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `MELIOUS_API_KEY`, `GEMINI_API_KEY`, `GROQ_API_KEY`, `XAI_API_KEY`, `OPENROUTER_API_KEY`, `MISTRAL_API_KEY`, `DEEPSEEK_API_KEY`, `CEREBRAS_API_KEY`, `AZURE_OPENAI_KEY`) and injects `HOME`, `MICROPI_HOME`, `PI_CODING_AGENT_DIR`, `PI_SESSION_DIR`, `OPENAI_BASE_URL` (pointing to local `MockLLM` `httptest.Server`), `PI_TELEMETRY=0`, and dummy `OPENAI_API_KEY`/`MELIOUS_API_KEY`.
- Verified empirically by `TestHarness_IsolatedEnvAndModelsJSON`.

## 3. Race Detection (`MockLLM` & `FakeSSH`)
- **Command:** `GOWORK=off go test -v -race -run TestHarness ./...` (in `tests/e2e/`)
- **Result:** `PASS` (exit 0, `1.196s`). All 6 tests (`TestHarness_MockLLMTextSSE`, `TestHarness_MockLLMToolCallSSE`, `TestHarness_MockLLMLengthCutOff`, `TestHarness_MockLLMMeliousCatalogAndRetry`, `TestHarness_IsolatedEnvAndModelsJSON`, `TestHarness_FakeSSHScript`) passed with `-race` enabled and zero data races.

## 4. `E2E_REQUIRE_BIN=1` & `UPI_BIN=/nonexistent` Loud Failure
- **Command:** `UPI_BIN=/nonexistent GOWORK=off go test -v -run TestT1_F01_BinaryExecutesAndPrintsHelp ./...`
  - **Result:** `FAIL` loudly with exit code `1`:
    `tier1_features_1_8_test.go:17: UPI_BIN "/nonexistent" does not point to an executable file: stat /nonexistent: no such file or directory`
- **Command:** `E2E_REQUIRE_BIN=1 GOWORK=off go test -v -run TestT1_F01_BinaryExecutesAndPrintsHelp ./...`
  - **Result:** `PASS` (exit `0`, `11.742s`). `cmd/upi/main.go` exists in the repo, so `ResolveUPIBinary` built `./cmd/upi` into a temp binary and executed the test. `harness.go:92-94` confirms `t.Fatalf` is called if no binary or `cmd/upi` exists when `E2E_REQUIRE_BIN=1`.
