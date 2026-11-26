# Iteration 2 Challenger 1 Handoff — Empirical CLI & Env Tests

## Verdict: PASS

All empirical CLI, static binary, environment variable precedence, and binary guard checks against `./bin/upi` passed. No project source files were modified, and all `/tmp/chal1-i2-*` temporary artifacts were cleaned up.

## Checks & Observed Outputs

### 1. Static Binary Check (`CGO_ENABLED=0`)
Commands:
```bash
ls -la ./bin/upi && file ./bin/upi && ldd ./bin/upi || true
```
Observed output:
```text
-rwxr-xr-x 1 antigravity antigravity 63078560 Nov 26 15:19 ./bin/upi
./bin/upi: ELF 64-bit LSB executable, x86-64, version 1 (SYSV), statically linked, Go BuildID=icGT7gPDL4t46rsVO_pM/LWIf_d77RakdnVQsdMVG/jPIr1lP_wgFui_UelgGx/ww7dPT890-RiGh-O2Aj9, BuildID[sha1]=c1b634072a8a5fc4f4e6bcdac16dc4f0db82f9b4, stripped
	not a dynamic executable
```
Result: **PASS** (statically linked, stripped ELF x86-64 binary, not dynamically linked).

### 2. Version Commands
Commands:
```bash
./bin/upi --version; echo "exit:$?"
./bin/upi version; echo "exit:$?"
```
Observed output:
```text
µPi 0.1.0 (PiG 0.8.0+1.6.0)
exit:0
µPi: 0.1.0
pig: 0.8.0
upstream pi: 1.6.0
go: go1.27.4
platform: linux/amd64
build: a18b202
exit:0
```
Result: **PASS**.

### 3. `doctor --offline` (Unauthenticated Guidance & Default Paths)
Command:
```bash
mkdir -p /tmp/chal1-i2-home && env -i PATH="$PATH" HOME=/tmp/chal1-i2-home ./bin/upi doctor --offline; echo "exit:$?"
```
Observed output (key sections):
```text
µPi doctor
────────────────────────────────────────────────────────────
upi-version:       µPi 0.1.0
pig-version:       0.8.0+1.6.0
build:             a18b202
go runtime:        go1.27.4
platform:          linux/amd64
binary:            /home/antigravity/Code000-211/microPi/bin/upi
offline:           true
...
config root:       /tmp/chal1-i2-home/.micropi (not created yet)
agent dir:         /tmp/chal1-i2-home/.micropi/agent (not created yet)
auth.json:         /tmp/chal1-i2-home/.micropi/agent/auth.json
settings.json:     /tmp/chal1-i2-home/.micropi/agent/settings.json
...
auth (logged-in providers)
────────────────────────────────────────────────────────────
  (no auth.json: run `upi login`)
...
default model
...
  effective:      none: no model has configured auth (run `upi login` or set a provider key)
...
exit:0
```
Result: **PASS** (both unauthenticated prompts say `upi login` instead of `pig login`, and default paths use `~/.micropi` and `~/.micropi/agent`).

### 4. Environment Variable Precedence
Commands:
```bash
# Case 2: PIG_HOME only
env -i PATH="$PATH" HOME=/tmp/chal1-i2-home PIG_HOME=/tmp/chal1-i2-pighome ./bin/upi doctor --offline | grep -E "^(config root|agent dir|auth\.json|settings\.json):"
# Case 3: MICROPI_HOME > PIG_HOME
env -i PATH="$PATH" HOME=/tmp/chal1-i2-home MICROPI_HOME=/tmp/chal1-i2-micropihome PIG_HOME=/tmp/chal1-i2-pighome ./bin/upi doctor --offline | grep -E "^(config root|agent dir|auth\.json|settings\.json):"
# Case 4: PIG_CODING_AGENT_DIR only
env -i PATH="$PATH" HOME=/tmp/chal1-i2-home MICROPI_HOME=/tmp/chal1-i2-micropihome PIG_CODING_AGENT_DIR=/tmp/chal1-i2-pigagent ./bin/upi doctor --offline | grep -E "^(config root|agent dir|auth\.json|settings\.json):"
# Case 5: MICROPI_CODING_AGENT_DIR > PIG_CODING_AGENT_DIR
env -i PATH="$PATH" HOME=/tmp/chal1-i2-home MICROPI_HOME=/tmp/chal1-i2-micropihome MICROPI_CODING_AGENT_DIR=/tmp/chal1-i2-micropiagent PIG_CODING_AGENT_DIR=/tmp/chal1-i2-pigagent ./bin/upi doctor --offline | grep -E "^(config root|agent dir|auth\.json|settings\.json):"
```
Observed output:
```text
=== Case 2: PIG_HOME only ===
config root:       /tmp/chal1-i2-pighome (not created yet)
agent dir:         /tmp/chal1-i2-pighome/agent (not created yet)
auth.json:         /tmp/chal1-i2-pighome/agent/auth.json
settings.json:     /tmp/chal1-i2-pighome/agent/settings.json
=== Case 3: MICROPI_HOME > PIG_HOME ===
config root:       /tmp/chal1-i2-micropihome (not created yet)
agent dir:         /tmp/chal1-i2-micropihome/agent (not created yet)
auth.json:         /tmp/chal1-i2-micropihome/agent/auth.json
settings.json:     /tmp/chal1-i2-micropihome/agent/settings.json
=== Case 4: PIG_CODING_AGENT_DIR only ===
config root:       /tmp/chal1-i2-micropihome (writable)
agent dir:         /tmp/chal1-i2-pigagent (not created yet)
auth.json:         /tmp/chal1-i2-pigagent/auth.json
settings.json:     /tmp/chal1-i2-pigagent/settings.json
=== Case 5: MICROPI_CODING_AGENT_DIR > PIG_CODING_AGENT_DIR ===
config root:       /tmp/chal1-i2-micropihome (writable)
agent dir:         /tmp/chal1-i2-micropiagent (not created yet)
auth.json:         /tmp/chal1-i2-micropiagent/auth.json
settings.json:     /tmp/chal1-i2-micropiagent/settings.json
```
Result: **PASS** (`MICROPI_HOME` > `PIG_HOME` > `~/.micropi`, and `MICROPI_CODING_AGENT_DIR` > `PIG_CODING_AGENT_DIR` > `~/.micropi/agent`).

### 5. Binary Name Guard
Command:
```bash
mkdir -p /tmp/chal1-i2-guard && cp ./bin/upi /tmp/chal1-i2-guard/pi && /tmp/chal1-i2-guard/pi --version 2>&1; echo "exit:$?"
```
Observed output:
```text
upi: refusing to run as "pi".

This binary is µPi (powered by PiG, the Go port of pi-coding-agent), not upstream pi.
Running it as "pi" would silently shadow the real pi binary and corrupt
shared expectations (PATH, sessions, ~/.pi/ tooling, shell aliases).

Resolved path: /tmp/chal1-i2-guard/pi

If you want upi: rename or symlink it back to "upi".
If you want pi:  install via npm/mise; do not point "pi" at this file.
exit:2
```
Result: **PASS** (exits with code 2).

### 6. Cleanup
Command:
```bash
rm -rf /tmp/chal1-i2-* && ls -d /tmp/chal1-i2-* 2>&1 || true
```
Observed output:
```text
ls: cannot access '/tmp/chal1-i2-*': No such file or directory
```
