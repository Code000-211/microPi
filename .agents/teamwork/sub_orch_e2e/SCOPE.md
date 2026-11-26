# Scope: E2E Testing Track (`microPi` on `PiG` / `upi`)

## Objective
Design and create an opaque-box, requirement-driven E2E test suite in `/home/antigravity/Code000-211/microPi/tests/e2e/` covering every feature in `PROJECT.md § Feature Inventory` (Features 1–17, plus Feature 18 verification target), along with `/home/antigravity/Code000-211/microPi/TEST_INFRA.md` and `/home/antigravity/Code000-211/microPi/TEST_READY.md`.

## 4-Tier Test Case Design Methodology
- **Tier 1 (Feature Coverage)**: >= 5 isolated happy-path tests per feature.
- **Tier 2 (Boundary & Corner Cases)**: >= 5 edge/error/boundary tests per feature.
- **Tier 3 (Cross-Feature Combinations)**: Pairwise combinations across features (e.g., `filetracking` + `checks`, `jobs` + `compaction`, `/remote` + `bash-file-writes`, `argument-aliases` + `edit` + `checks`, `docsearch` + `filetracking`, etc.).
- **Tier 4 (Real-World Application Scenarios)**: >= 5 realistic end-user workloads.

## Constraints
- Opaque-box: invoke `upi` binary via `UPI_BIN` env var (defaulting to `./upi` or building from `./cmd/upi` when needed).
- Hermetic: use local mock/fake LLM HTTP server (`httptest.Server`), isolated temp `HOME` / `MICROPI_HOME` / workspace directories, no real provider API keys or paid tokens.
- Go 1.27.4 / `tests/e2e/` package (`package e2e_test`).
- Must compile cleanly (`go vet ./tests/e2e/...` and `go test -c ./tests/e2e/...`) even while M1–M6 are in progress (or skip/fail cleanly when `UPI_BIN` is absent if run before M1 finishes, while ensuring the harness itself is tested).
