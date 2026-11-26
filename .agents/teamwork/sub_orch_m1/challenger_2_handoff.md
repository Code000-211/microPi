# Milestone M1 Challenger 2 Handoff (`upi doctor` & `upi update` Failure-Mode Empirical Tests)

**Verdict: PASS**

All empirical failure-mode tests against `./bin/upi`, Go unit tests for self-update and `upi` commands, and code inspections of `coding/cli/doctor.go`, `internal/codingagent/micropi_update.go`, `coding/cli/self_update.go`, and `internal/codingagent/selfupdate.go` passed without panics, secret leaks, or unhandled errors. No project source files were modified.

---

## 1. `upi doctor` Empirical Tests (`./bin/upi`)

### 1.1 Secret Redaction
- **Command**:
  ```bash
  ANTHROPIC_API_KEY=sk-ant-supersecret123456 OPENAI_API_KEY=sk-proj-secret987654 ./bin/upi doctor --offline
  ```
- **Observed Output** (exit code `0`):
  - `OPENAI_API_KEY:            sk-pro…(masked)`
  - `ANTHROPIC_API_KEY:         sk-ant…(masked)`
  - Neither `sk-ant-supersecret123456` nor `sk-proj-secret987654` appeared in stdout/stderr.

### 1.2 Herdr Detection
- **Command**:
  ```bash
  TERM_PROGRAM=herdr HERDR_ENV=1 HERDR_KITTY_GRAPHICS=1 HERDR_AGENT=1 ./bin/upi doctor --offline
  ```
- **Observed Output** (exit code `0`):
  ```text
  herdr
  ────────────────────────────────────────────────────────────
    active:          true
    TERM_PROGRAM:    herdr
    HERDR_ENV:       1
    HERDR_KITTY_GRAPHICS: 1
    HERDR_AGENT:     1
  ```
- Without Herdr env vars, `active:          false` is reported cleanly.

### 1.3 Non-existent, Read-only `MICROPI_HOME`, and Invalid `mcp.json`
- **Non-existent `MICROPI_HOME`**:
  ```bash
  MICROPI_HOME=/tmp/chal2-nonexistent/micropi ./bin/upi doctor --offline
  ```
  - Exit code `0`, no panic.
  - Reports `config root:       /tmp/chal2-nonexistent/micropi (not created yet)` and `agent dir:         /tmp/chal2-nonexistent/micropi/agent (not created yet)`.
- **Read-only `MICROPI_HOME`**:
  ```bash
  mkdir -p /tmp/chal2-ro/agent && chmod 0555 /tmp/chal2-ro /tmp/chal2-ro/agent && MICROPI_HOME=/tmp/chal2-ro ./bin/upi doctor --offline
  ```
  - Exit code `0`, no panic.
  - Reports `not writable: open /tmp/chal2-ro/.upi-doctor-...: permission denied` for both config root and agent dir, and `project trust:     untrusted (the trust decision could not be read)`.
- **Invalid `mcp.json`**:
  ```bash
  mkdir -p /tmp/chal2-badmcp/agent && printf '{not valid json' > /tmp/chal2-badmcp/agent/mcp.json && MICROPI_HOME=/tmp/chal2-badmcp ./bin/upi doctor --offline
  ```
  - Exit code `0`, no panic.
  - Reports under `mcp`:
    ```text
      servers:         0
      error:           /tmp/chal2-badmcp/agent/mcp.json: Expected property name or '}' in JSON at position 1 (line 1 column 2)
    ```

---

## 2. `upi update` Empirical Tests & Code Inspection

### 2.1 Unreachable / Invalid `MICROPI_UPDATE_URL`
- **Unreachable HTTPS port**:
  ```bash
  MICROPI_UPDATE_URL=https://127.0.0.1:59999/update.json ./bin/upi update
  ```
  - Exit code `1`, no panic.
  - Output: `Update failed: could not reach the update source: Get "https://127.0.0.1:59999/update.json": dial tcp 127.0.0.1:59999: connect: connection refused` + fallback remediation message.
- **Unreachable loopback HTTP port (with `PIG_UPDATE_ALLOW_LOOPBACK_HTTP=1`)**:
  ```bash
  MICROPI_UPDATE_URL=http://127.0.0.1:59999/update.json PIG_UPDATE_ALLOW_LOOPBACK_HTTP=1 ./bin/upi update
  ```
  - Exit code `1`, no panic.
  - Output: `Update failed: could not reach the update source: Get "http://127.0.0.1:59999/update.json": dial tcp 127.0.0.1:59999: connect: connection refused`.
- **Non-HTTP(S) `file://` URL**:
  ```bash
  MICROPI_UPDATE_URL=file:///tmp/chal2-nonexistent.json ./bin/upi update
  ```
  - Exit code `1`, no panic.
  - Output: `Update failed: could not reach the update source: invalid update URL "file:///tmp/chal2-nonexistent.json"`.

### 2.2 Go Update Test Suites
- `GOTOOLCHAIN=auto go test -v ./coding/cli -run 'TestMicroPi|TestSelfUpdate'` -> **PASS** (5/5 tests passed in `1.457s`, including `TestMicroPiUpdateReleaseManifestAndSemver`).
- `GOTOOLCHAIN=auto go test -v ./internal/codingagent -run 'TestMicroPi|TestSelfUpdate|TestFetchMicroPi|TestParseMicroPi|TestSelfReplace'` -> **PASS** (8/8 tests passed in `1.083s`, including `TestSelfReplaceAtVerifiesChecksumAndReplaces` and `TestSelfReplaceAtInstallsPigFromReleaseArchive`).

### 2.3 Code Inspection (`micropi_update.go`, `self_update.go`, `selfupdate.go`)
- **`upi-v` tag parsing**: `ParseMicroPiReleaseTag` strictly requires the `upi-v` prefix and validates the remainder with `semver.StrictNewVersion`. Beta channel queries `/releases?per_page=30` and skips drafts; stable queries `/releases/latest` and skips drafts.
- **`Code000-211/microPi` URL & precedence**: `MicroPiReleasesAPIBase` defaults to `https://api.github.com/repos/Code000-211/microPi`. `UpdateSourceURL()` checks `MICROPI_UPDATE_URL` -> `PIG_UPDATE_URL` -> `<config-root>/update-url` -> `DefaultUpdateURL`; when empty, `fetchSelfUpdateManifest` uses `FetchMicroPiReleaseManifest`. All URLs (API, `SHA256SUMS`, binary download) pass through `validateUpdateURL`.
- **SHA256 mismatch & archive extraction**: `FetchMicroPiReleaseManifestFrom` requires `SHA256SUMS` and a matching entry for the platform asset. `selfReplaceAt` validates 64-hex-char SHA256, hashes while streaming to a temp file, and aborts with `checksum mismatch: refusing to install` before extraction or rename. `extractPigFromTarGz` accepts both `upi` and `pig` binaries.
- **Minor non-blocking note**: In `upi doctor`, `writeDiagnoseDefaultModel` (shared upstream helper) still prints `run \`pig login\`` when no auth is configured, whereas the `auth` section prints `run \`upi login\``.

---

## 3. Cleanup
- Verified all `/tmp/chal2-*` temporary directories were removed.
