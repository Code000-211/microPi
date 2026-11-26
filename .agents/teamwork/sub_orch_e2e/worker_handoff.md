# E2E Test Suite Worker Handoff

## Summary
Designed and implemented the complete opaque-box, requirement-driven Go E2E test suite in `tests/e2e/` and documented it in `TEST_INFRA.md`.

## Artifacts Created
- `tests/e2e/go.mod` — Standalone stdlib-only Go module (`module github.com/Code000-211/microPi/tests/e2e`, `go 1.24.3`, zero external dependencies).
- `tests/e2e/harness.go` — Hermetic E2E harness:
  - `ResolveUPIBinary(t)` with `sync.Once` caching (checks `UPI_BIN`, `../../upi`, `go build ./cmd/upi`, and respects `E2E_REQUIRE_BIN=1` vs `t.Skip`).
  - `NewTestEnv(t)` creating isolated `t.TempDir()` directories (`Home`, `MicropiHome`, `AgentDir`, `SessionDir`, `WorkDir`, `FakeSSHDir`, `RemoteDir`), stripping real provider API keys, and writing `models.json` to `AgentDir`, `MicropiHome`, `MicropiHome/agent`, and `Home/.micropi/agent`.
  - `MockLLM` (`httptest.Server`) supporting `POST /v1/chat/completions` (OpenAI SSE streaming for text, `tool_calls`, `reasoning_content` + `finish_reason: "length"`, HTTP 429 / `"Failed to create energy hold"` error injection, and full request capture) and `GET /v1/models?include_meta=true`.
  - `FakeSSH` executable script + `PI_SSH_COMMAND` + `SSHLogPath` for hermetic `/remote` and `--remote` tests.
  - `RunUPI`, `RunUPIInDir`, `RunUPIJSON`, and `StartRPCSession` helpers.
- `tests/e2e/harness_test.go` — 6 infrastructure self-tests (`TestHarness_*`).
- `tests/e2e/helpers_test.go` — Shared opaque-box assertion and setup helpers.
- `tests/e2e/tier1_features_1_8_test.go` — 40 Tier 1 happy-path tests (5 per feature for Features 1–8).
- `tests/e2e/tier1_features_9_17_test.go` — 45 Tier 1 happy-path tests (5 per feature for Features 9–17).
- `tests/e2e/tier2_boundaries_1_8_test.go` — 40 Tier 2 boundary/corner-case tests (5 per feature for Features 1–8).
- `tests/e2e/tier2_boundaries_9_17_test.go` — 45 Tier 2 boundary/corner-case tests (5 per feature for Features 9–17).
- `tests/e2e/tier3_combinations_test.go` — 16 Tier 3 pairwise cross-feature combination tests.
- `tests/e2e/tier4_scenarios_test.go` — 6 Tier 4 real-world multi-turn end-user scenario tests.
- `TEST_INFRA.md` — Architecture, run instructions, and complete Feature-to-Test traceability matrix.

**Total tests**: 198 (6 harness self-tests + 85 Tier 1 + 85 Tier 2 + 16 Tier 3 + 6 Tier 4).

## Verification Results (Post-Final Edit)
Ran in `/home/antigravity/Code000-211/microPi/tests/e2e`:
- `GOWORK=off go vet ./...` — PASS
- `GOWORK=off go test -c ./... && rm -f e2e.test` — PASS
- `GOWORK=off go test -v -run TestHarness ./...` — PASS (6/6 passed in 0.050s)
- `GOWORK=off go test -v -timeout 30s -run "TestT1_F01_BinaryExecutesAndPrintsHelp|TestT1_F02_VersionSubcommand|TestT1_F02_DoctorSubcommand" ./...` — PASS (3/3 passed in 6.922s against `./cmd/upi`)

## Notes for M1–M7
- `GOWORK=off` is needed when running inside `tests/e2e/` unless `./tests/e2e` is added to root `go.work`.
- During live-binary smoke testing against M1's `./cmd/upi`, `TestT1_F01_PrintModeBasicPrompt` reported `Error: Unknown provider "mock".` because M1's provider/models.json resolution path may differ or custom provider loading is still being wired; `WriteModelsJSON()` now populates `AgentDir`, `MicropiHome`, `MicropiHome/agent`, and `Home/.micropi/agent` to cover all standard locations.
