# Briefing: E2E Testing Track (`sub_orch_e2e`)

## Current Phase
Iteration 1 — Implementation (Worker building `tests/e2e/` 4-tier suite + hermetic mock LLM + `TEST_INFRA.md`).

## Exploration Findings Summary
- **Repo State**: `go.mod`, `go.work`, `cmd/upi/` do not exist yet (M1 `IN_PROGRESS`). Therefore `tests/e2e/` will include its own self-contained `tests/e2e/go.mod` using Go stdlib only, plus harness self-tests that run immediately. Live-binary E2E tests resolve `UPI_BIN` -> `./upi` -> build `./cmd/upi`, skipping cleanly if absent unless `E2E_REQUIRE_BIN=1` is set.
- **CLI & Protocols**: `upi -p --mode json` (JSONL events), `upi --mode rpc` (JSONL stdin/stdout commands), `upi version` / `--version`, `upi doctor`, `upi update`, `--remote <host>[:<dir>]`, `--tools`, `--provider`, `--model`.
- **Hermetic Mock LLM**: `httptest.Server` serving OpenAI SSE (`POST /v1/chat/completions`) and Melious catalog (`GET /v1/models?include_meta=true`), wired via `models.json` inside isolated temp `HOME` / `MICROPI_HOME` / `PI_CODING_AGENT_DIR`.

## Key Paths
- Original request: `/home/antigravity/Code000-211/microPi/.agents/teamwork/ORIGINAL_REQUEST.md`
- Project scope: `/home/antigravity/Code000-211/microPi/PROJECT.md`
- Project rules: `/home/antigravity/Code000-211/microPi/AGENTS.md`
- Target test suite: `/home/antigravity/Code000-211/microPi/tests/e2e/`
- Target docs: `/home/antigravity/Code000-211/microPi/TEST_INFRA.md`, `/home/antigravity/Code000-211/microPi/TEST_READY.md`
