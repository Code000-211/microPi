# Iteration 2 Challenger 2 Handoff — `upi doctor`, `upi update` & `selfUpdatePlanRuns` Empirical Tests

## Verdict: PASS

All empirical CLI tests against `bin/upi` and all targeted Go unit tests in `coding/cli` and `internal/codingagent` passed. No project source files were modified, and all `/tmp/chal2-i2-*` temporary directories were cleaned up.

## 1. Binary Verification
Command:
```bash
ls -la bin/upi && file bin/upi
```
Observed output:
```text
-rwxr-xr-x 1 antigravity antigravity 63078560 Nov 26 15:19 bin/upi
bin/upi: ELF 64-bit LSB executable, x86-64, version 1 (SYSV), statically linked, Go BuildID=icGT7gPDL4t46rsVO_pM/LWIf_d77RakdnVQsdMVG/jPIr1lP_wgFui_UelgGx/ww7dPT890-RiGh-O2Aj9, BuildID[sha1]=c1b634072a8a5fc4f4e6bcdac16dc4f0db82f9b4, stripped
```
Result: `PASS` (statically linked `CGO_ENABLED=0` ELF x86-64 binary).

## 2. `upi doctor --offline` Empirical Tests

### 2a. Secret Redaction (`ANTHROPIC_API_KEY=sk-ant-secret123`)
Command:
```bash
env -i PATH="$PATH" HOME=/tmp/chal2-i2-home1 MICROPI_HOME=/tmp/chal2-i2-micropi1 ANTHROPIC_API_KEY=sk-ant-secret123 ./bin/upi doctor --offline
```
Observed output (relevant lines):
```text
auth (logged-in providers)
────────────────────────────────────────────────────────────
  (no auth.json: run `upi login`)
...
environment
────────────────────────────────────────────────────────────
  MICROPI_HOME:              /tmp/chal2-i2-micropi1
  ANTHROPIC_API_KEY:         sk-ant…(masked)
```
Exit code: `0`. `sk-ant-secret123` did not appear in stdout/stderr. `PASS`.

### 2b. Herdr Detection (`HERDR_ENV=1`)
Command:
```bash
env -i PATH="$PATH" HOME=/tmp/chal2-i2-home1 MICROPI_HOME=/tmp/chal2-i2-micropi1 HERDR_ENV=1 ./bin/upi doctor --offline
```
Observed output (relevant lines):
```text
herdr
────────────────────────────────────────────────────────────
  active:          true
  HERDR_ENV:       1
```
Exit code: `0`. `PASS`.

### 2c. Non-existent `MICROPI_HOME`
Command:
```bash
env -i PATH="$PATH" HOME=/tmp/chal2-i2-home1 MICROPI_HOME=/tmp/chal2-i2-nonexistent ./bin/upi doctor --offline
```
Observed output (relevant lines):
```text
config root:       /tmp/chal2-i2-nonexistent (not created yet)
agent dir:         /tmp/chal2-i2-nonexistent/agent (not created yet)
...
  effective:      none: no model has configured auth (run `upi login` or set a provider key)
```
Exit code: `0`. `PASS`.

### 2d. Read-only `MICROPI_HOME` (`chmod 555`)
Command:
```bash
env -i PATH="$PATH" HOME=/tmp/chal2-i2-home1 MICROPI_HOME=/tmp/chal2-i2-ro ./bin/upi doctor --offline
```
Observed output (relevant lines):
```text
config root:       /tmp/chal2-i2-ro (not writable: open /tmp/chal2-i2-ro/.upi-doctor-1538491619: permission denied)
agent dir:         /tmp/chal2-i2-ro/agent (not created yet)
...
project trust:     untrusted (the trust decision could not be read)
```
Exit code: `0`. `PASS`.

## 3. `upi update` Failure Mode & Targeted Unit Tests

### 3a. Unreachable `MICROPI_UPDATE_URL`
Command:
```bash
env -i PATH="$PATH" HOME=/tmp/chal2-i2-home1 MICROPI_HOME=/tmp/chal2-i2-micropi1 MICROPI_UPDATE_URL=https://127.0.0.1:59999/update.json ./bin/upi update
```
Observed output:
```text
Extensions are skipped. Run upi update --extensions to update extensions.
Update failed: could not reach the update source: Get "https://127.0.0.1:59999/update.json": dial tcp 127.0.0.1:59999: connect: connection refused
Automatic update from https://127.0.0.1:59999/update.json could not be applied. Download a new upi binary from your provider's releases, or pull the container image (log in to your registry first) and re-deploy.
```
Exit code: `1`. `PASS`.

### 3b. `coding/cli` Update Tests
Command:
```bash
GOTOOLCHAIN=auto go test -count=1 ./coding/cli -run 'TestMicroPi|TestSelfUpdate|TestApplyStandaloneUpdate|TestCheckSelfUpdateVersion'
```
Observed output:
```text
ok  	github.com/MichaelKinsy/PiG/coding/cli	3.129s
```
Exit code: `0`. `PASS`.

### 3c. `internal/codingagent` Update Tests
Command:
```bash
GOTOOLCHAIN=auto go test -count=1 ./internal/codingagent -run 'TestMicroPi|TestSelfUpdate|TestFetchMicroPi|TestParseMicroPi|TestSelfReplace'
```
Observed output:
```text
ok  	github.com/MichaelKinsy/PiG/internal/codingagent	2.299s
```
Exit code: `0`. `PASS`.

## 4. Cleanup
Command:
```bash
chmod 755 /tmp/chal2-i2-ro && rm -rf /tmp/chal2-i2-*
```
Exit code: `0`. All `/tmp/chal2-i2-*` artifacts removed.
