# TEST_READY: microPi on PiG (`upi`) E2E Test Suite

**Status**: READY (Verified by Reviewer 1, Reviewer 2, Challenger 1, Challenger 2, and Forensic Auditor — Iteration 2)

## Summary
The opaque-box, requirement-driven E2E test suite for `microPi` on `PiG` (`upi`) is complete in `tests/e2e/` and documented in `TEST_INFRA.md`.

### Coverage (198 Tests Total)
- **Harness Self-Tests**: 6 tests (`TestHarness_*`) verifying `MockLLM` SSE streaming, tool-call scripting, `length` cut-off responses, Melious `/models` catalog + retry, isolated `HOME`/`MICROPI_HOME`/`models.json`, and hermetic `FakeSSH`.
- **Tier 1 (Feature Coverage)**: 85 tests (`>= 5` isolated happy-path tests for each of Features 1–17 in `PROJECT.md § Feature Inventory`).
- **Tier 2 (Boundary & Corner Cases)**: 85 tests (`>= 5` boundary/edge/error tests for each of Features 1–17).
- **Tier 3 (Cross-Feature Combinations)**: 16 pairwise combination tests.
- **Tier 4 (Real-World Application Scenarios)**: 6 realistic multi-turn end-user workloads.

## How M7 / Implementation Verification Should Run the Suite
```bash
cd tests/e2e
UPI_BIN=/path/to/upi E2E_REQUIRE_BIN=1 GOWORK=off go test -v ./...
```
Or let `ResolveUPIBinary` build `./cmd/upi` automatically:
```bash
cd tests/e2e
E2E_REQUIRE_BIN=1 GOWORK=off go test -v ./...
```

## Verification Gate Evidence (Iteration 2)
- `GOWORK=off go vet ./...`: PASS
- `GOWORK=off go test -c -o /dev/null ./...`: PASS
- `GOWORK=off go test -count=1 -v -race -run TestHarness ./...`: PASS (6/6, zero data races)
- Reviewer 1 (Build & Structure): **APPROVE**
- Reviewer 2 (4-Tier Coverage & Traceability): **APPROVE**
- Challenger 1 (Hermeticity & Race): **PASS**
- Challenger 2 (Opaque-Box & Assertions): **PASS**
- Forensic Auditor (`teamwork_preview_auditor`): **CLEAN**
