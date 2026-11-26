# Explorer 1 Handoff — PiG Repo & Structure

## 1. PiG Version, Tags & Commit Info
- **Repository**: `https://github.com/MichaelKinsy/PiG`
- **`main` (`origin/main`)**: `a00da1e2` — `Merge batch/0.8.1-second-v3 (ddab36fa9f) (#573)`
  - `internal/coding/pigversion/pigversion.go`:
    - `PigVersion = "0.8.0"`
    - `UpstreamRef = "v1.6.0"`
    - `UpstreamVersion = "1.6.0"`
    - `UpstreamPackageVersion = "1.6.0"`
    - `UpstreamCommit = "faac585884ae2c013a6b1400f3e9e77f10b34bac"`
    - `Version = PigVersion + "+1.6.0"` (`"0.8.0+1.6.0"`)
  - `.upstream/pin`: `ref=v1.6.0`, `commit=faac585884ae2c013a6b1400f3e9e77f10b34bac`, `package_version=1.6.0`, `version=1.6.0`
- **Tag `v0.8.0`**: commit `73585394f40cde45fd40889db265179ac3c2af99` (tag object `063448d9374fbaf8a64dba83e79a21e20253b294`).
- Note: `main` (`a00da1e2`) already carries `0.8.0+1.6.0` plus post-0.8.0 fixes (`batch/0.8.1-second-v3`). Either `main` (`a00da1e2`) or `v0.8.0` (`73585394`) can be used as the base; `main` has the latest `0.8.0+1.6.0` fixes.

## 2. Go Toolchain, Modules & Build Configuration
- **Root `go.mod`**:
  - `module github.com/MichaelKinsy/PiG`
  - `go 1.27.0`
  - `toolchain go1.27.4`
  - `godebug winsymlink=0`
  - Depends on `github.com/MichaelKinsy/PiG/extensions/sdk v0.8.0`
- **`.go-version`**: `1.27.4`
- **`go.work`**:
  - `go 1.27.0`, `toolchain go1.27.4`, `godebug winsymlink=0`
  - Workspace modules: `.`, `./extensions/sdk`, `./piglets/pig-bolt`, `./piglets/porter/extensions/pig-porter`
- **Local Environment Caveat**:
  - Installed `go` at `/usr/local/go/bin/go` is `go1.24.3 linux/amd64` with `GOTOOLCHAIN=auto`. Running `go build`/`go test` will rely on `GOTOOLCHAIN=auto` to fetch `go1.27.4` (or require installing Go 1.27.4 if offline).
- **`Makefile` Build Targets & Flags**:
  - `pig`: `CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags "-X github.com/MichaelKinsy/PiG/coding/cli.Build=..." -o bin/pig ./cmd/pig`
  - `install`: same with `-ldflags "-s -w -X github.com/MichaelKinsy/PiG/coding/cli.Build=..."`
  - `build`: `go build ./...`
  - `Makefile` evaluates `automation/pin/pin.py make-vars` at parse time (requires `python3` and `.upstream/pin`).
- **Root `embed.go`**:
  - `package pig` at repo root embeds `CHANGELOG.md.zst` (`//go:embed CHANGELOG.md.zst`). Must be copied to repo root along with `CHANGELOG.md.zst` (and `CHANGELOG.md` for `TestBundledChangelogFrameMatchesSource`).

## 3. Top-Level File & Directory Comparison (Collisions)

### Non-Colliding PiG Top-Level Entries (Safe to Copy Directly)
- **Go Core & Entrypoints**: `agent/`, `ai/`, `chord/`, `cmd/`, `codemode/`, `coding/`, `durable/`, `embed.go`, `embed_test.go`, `env/`, `extensions/`, `internal/`, `mcp/`, `piglets/`, `schema/`, `telemetry/`, `tui/`
- **Go Module/Build Files**: `go.mod`, `go.sum`, `.go-version`, `go.work`, `go.work.sum`, `Makefile`, `automation/`
- **Other PiG Directories/Files**: `changelog.d/`, `CHANGELOG.md`, `CHANGELOG.md.zst`, `CITATION.cff`, `.coderabbit.yaml`, `.devcontainer/`, `.dockerignore`, `.editorconfig`, `flake.lock`, `flake.nix`, `.gitleaks.toml`, `.golangci.yml`, `handoffs/`, `ISSUE-147-PR.md`, `LICENSES/`, `.node-version`, `NOTICE`, `plans/`, `research/`, `REUSE.toml`, `schemas/`, `test/`, `third_party/`, `.upstream/`, `variables/`

### microPi-Only Top-Level Entries (Preserved Untouched)
- `packages/`, `scripts/`, `install.sh`, `install.ps1`, `npm/`, `patches/`, `plugins/`, `profiles/`, `sources.json`, `tests/`, `test.sh`, `pi-test.*`, `package.json`, `package-lock.json`, `tsconfig*.json`, `vitest.base.ts`, `biome.json`, `VERSION`, `PROJECT.md`, `CODE_OF_CONDUCT.md`, `CONTRIBUTING.md`, `SECURITY.md`, `keys/`, `.husky/`, `.npmrc`, `.pi/`
- Note: `packages/` and `scripts/` have **zero collision** with PiG.

### Colliding Top-Level Entries (13 Items) — Resolution Strategy
| Path | microPi Content | PiG Content | Safe Integration Action |
|------|----------------|-------------|-------------------------|
| `.git/` | microPi git repo | PiG git repo | **Keep microPi `.git/`** (never overwrite) |
| `.agents/` | `.agents/teamwork/` | `.agents/skills/` | **Keep microPi `.agents/`**; optionally copy `.agents/skills/` if needed |
| `AGENTS.md` | microPi dev rules | PiG agent rules | **Keep microPi `AGENTS.md`** |
| `README.md` | microPi README | PiG README | **Keep microPi `README.md`** |
| `docs/` | `ACP.md`, `BUILDING.md`, `PLUGINS.md`, `RELEASING.md`, `TROUBLESHOOTING.md`, `releases/` | PiG design/parity/SDK docs + `docs/go.mod` | **Preserve microPi `docs/` files**; either skip PiG `docs/` in M1 or merge non-colliding subdirs (`docs/parity/`, `docs/testing/`, etc.) without overwriting microPi's `.md` files |
| `.github/` | microPi workflows (`ci.yml`, `release.yml`, etc.) | PiG workflows | **Keep microPi `.github/`** in M1 (updated in M6) |
| `.gitignore` | microPi/TS/Pi-Bolt ignores | Go/PiG ignores (`bin/`, `tmp/`, etc.) | **Merge**: keep microPi `.gitignore` and append PiG Go ignores (`bin/`, `tmp/`, `*.exe`, etc.) |
| `LICENSE` | MIT (Mario Zechner, OpenSec Pi-Bolt) | MIT (Michael Kinsy) | **Keep microPi `LICENSE`** (or add Michael Kinsy copyright line if desired) |
| `THIRD_PARTY_NOTICES.md` | microPi notices | PiG Go notices | **Keep microPi `THIRD_PARTY_NOTICES.md`** for now (or merge in M6) |
| `bench/` | Pi-Bolt Python bench | PiG Go bench (`evals/`, `durable/`, etc.) | **Keep microPi `bench/`** in M1 (or merge non-colliding subdirs) |
| `examples/` | `plugins/` | `chord-todo/`, `extensions/`, `headless/`, `sdk/`, etc. | **Merge non-colliding subdirs** (preserve `examples/plugins/`) |
| `nix/` | `model-catalog.json` | `package.nix`, `vendor-hash.sh` | **Keep microPi `nix/model-catalog.json`** (or copy `package.nix` alongside) |
| `.gitattributes` | microPi gitattributes | PiG gitattributes | **Keep microPi `.gitattributes`** |

## 4. Recommended Safe Copy / Integration Plan (M1)
1. Clone PiG (`main` at `a00da1e2` or tag `v0.8.0` at `73585394`) to a temporary directory.
2. Copy all **non-colliding** PiG directories and files into `/home/antigravity/Code000-211/microPi` (`rsync -a --ignore-existing` or explicit list of Go directories + `go.mod`, `go.sum`, `go.work`, `go.work.sum`, `.go-version`, `embed.go`, `embed_test.go`, `CHANGELOG.md`, `CHANGELOG.md.zst`, `Makefile`, `automation/`, `.upstream/`, `variables/`, `third_party/`, `test/`).
   - Using `rsync -a --ignore-existing` (excluding `.git/`) guarantees `README.md`, `AGENTS.md`, `docs/*.md`, `scripts/`, `packages/`, `.github/`, `.agents/`, `LICENSE`, `bench/`, `nix/model-catalog.json`, and `.gitignore` are preserved.
3. Manually append PiG's Go-specific entries (`bin/`, `tmp/`, etc.) to microPi's `.gitignore`.
4. Add `cmd/upi/main.go` alongside `cmd/pig/main.go` (or wrapping `coding/cli.Main`) and add a `upi` target in `Makefile` (`CGO_ENABLED=0 go build -buildvcs=false -trimpath ... -o bin/upi ./cmd/upi`).
5. Verify `CGO_ENABLED=0 go build ./cmd/pig ./cmd/upi` and `go test ./ai/... ./agent/...` (note: check `GOTOOLCHAIN=auto` / Go 1.27.4 availability).
