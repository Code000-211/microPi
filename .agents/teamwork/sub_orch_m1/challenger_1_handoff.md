# Challenger 1 Handoff — CLI, Env Precedence & Static Binary Empirical Tests

**Verdict: PASS**

All empirical checks on `./bin/upi` passed without modifying any project source files, and all temporary directories under `/tmp/chal1-*` were cleaned up.

## 1. Static Binary Verification
Commands:
```bash
ls -lh ./bin/upi && file ./bin/upi && (ldd ./bin/upi 2>&1 || true)
```
Observed output:
- `-rwxr-xr-x 1 antigravity antigravity 61M Nov 26 14:45 ./bin/upi`
- `./bin/upi: ELF 64-bit LSB executable, x86-64, version 1 (SYSV), statically linked, Go BuildID=JbLIQVl6Rgbu3oWZS39f/y25hBUT-B87GUggXgCmb/jPIr1lP_wgFui_UelgGx/RJdA8aRGxHJ867OqwtCk, BuildID[sha1]=6954080b69589baa43453a22dbf1613bf1cca172, stripped`
- `not a dynamic executable`

## 2. CLI Version & Help Flags
Commands & observed outputs (all exited `0`):
- `./bin/upi --version` -> `µPi 0.1.0 (PiG 0.8.0+1.6.0)`
- `./bin/upi -v` -> `µPi 0.1.0 (PiG 0.8.0+1.6.0)`
- `./bin/upi version`:
  ```
  µPi: 0.1.0
  pig: 0.8.0
  upstream pi: 1.6.0
  go: go1.27.4
  platform: linux/amd64
  build: a18b202
  ```
- `./bin/upi --help` -> exited `0`, lists `µPi / PiG Commands` (`upi login`, `upi doctor`, `upi diagnose`, `upi update`, `upi version`, etc.) and `µPi Options`.
  - Minor non-blocking observation: the top `Usage:`, `Commands:`, `Examples:`, and `PIG_*` env var sections in `--help` still show upstream `pig` strings from `coding/cli/args.go`.

## 3. Env Precedence in `./bin/upi doctor --offline`
All runs used `env -i PATH="$PATH"` with isolated `/tmp/chal1-*` directories:

1. **Default** (`HOME=/tmp/chal1-home`, no `MICROPI_HOME`/`PIG_HOME`):
   - `config root: /tmp/chal1-home/.micropi (not created yet)`
   - `agent dir: /tmp/chal1-home/.micropi/agent (not created yet)`
2. **`PIG_HOME=/tmp/chal1-pighome`**:
   - `config root: /tmp/chal1-pighome (writable)`
   - `agent dir: /tmp/chal1-pighome/agent (not created yet)`
3. **`MICROPI_HOME=/tmp/chal1-uhome` + `PIG_HOME=/tmp/chal1-pighome`**:
   - `config root: /tmp/chal1-uhome (writable)` (`MICROPI_HOME` won)
   - `agent dir: /tmp/chal1-uhome/agent (not created yet)`
4. **Agent Dir Precedence**:
   - `PIG_CODING_AGENT_DIR=/tmp/chal1-pigagent` -> `agent dir: /tmp/chal1-pigagent (writable)`
   - `MICROPI_CODING_AGENT_DIR=/tmp/chal1-uagent` + `PIG_CODING_AGENT_DIR=/tmp/chal1-pigagent` -> `agent dir: /tmp/chal1-uagent (writable)` (`MICROPI_CODING_AGENT_DIR` won)
   - `MICROPI_USE_PI_DIRS=1` -> `agent dir: /tmp/chal1-home/.pi/agent (not created yet)`
   - `MICROPI_CODING_AGENT_DIR=/tmp/chal1-uagent` + `MICROPI_USE_PI_DIRS=1` -> `agent dir: /tmp/chal1-home/.pi/agent (writable)` (consistent with `ResolveDefaultAgentDir()` in `internal/codingagent/settings.go:1962-1984`, where `UsePiDirs()` is evaluated first to opt into `~/.pi/agent`).

## 4. Binary Guard (`pi` rejection)
Command:
```bash
cp ./bin/upi /tmp/chal1-guard/pi
/tmp/chal1-guard/pi --version 2>&1
```
Observed output:
- Exit code: `2` (`GUARD_EXIT=2`)
- Message:
  ```
  upi: refusing to run as "pi".

  This binary is µPi (powered by PiG, the Go port of pi-coding-agent), not upstream pi.
  Running it as "pi" would silently shadow the real pi binary and corrupt
  shared expectations (PATH, sessions, ~/.pi/ tooling, shell aliases).

  Resolved path: /tmp/chal1-guard/pi

  If you want upi: rename or symlink it back to "upi".
  If you want pi:  install via npm/mise; do not point "pi" at this file.
  ```

## 5. Cleanup
- Verified `ls -d /tmp/chal1-*` returns `CLEAN`.
