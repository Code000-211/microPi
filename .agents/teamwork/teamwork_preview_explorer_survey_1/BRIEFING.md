# BRIEFING — 2026-11-26T14:15:00Z

## Mission
Step 0 Survey: Map microPi's architecture, structure, relationship to upstream Pi (`v1.4.0`) and Pi-Bolt, packages, build/release scripts, patches, profiles, plugins, and custom microPi additions.

## 🔒 My Identity
- Archetype: Teamwork explorer
- Roles: Read-only investigation, Synthesis
- Working directory: /home/antigravity/Code000-211/microPi
- State folder: /home/antigravity/Code000-211/microPi/.agents/teamwork/teamwork_preview_explorer_survey_1
- Original parent: 98032d689a421942cea73f1ad3409e2f
- Milestone: Step 0 Survey

## 🔒 Key Constraints
- Read-only investigation — do NOT implement or modify any source/project files
- Write all output files ONLY in `/home/antigravity/Code000-211/microPi/.agents/teamwork/teamwork_preview_explorer_survey_1`
- Report back via `send_message` to `98032d689a421942cea73f1ad3409e2f` with path and concise summary

## Current Parent
- Conversation ID: 98032d689a421942cea73f1ad3409e2f
- Updated: 2026-11-26T14:15:00Z

## Investigation State
- **Explored paths**:
  - Root files: `sources.json`, `package.json`, `tsconfig.json`, `VERSION`, `README.md`, `install.sh`, `install.ps1`, `test.sh`, `AGENTS.md`
  - Git metadata: `git log -n 60`, `git branch -a`, `git remote -v`, `git tag -l`
  - `packages/*`: `agent`, `ai`, `codemode`, `coding-agent`, `docsearch`, `mcp`, `telemetry`, `tui`
  - `patches/`: `bun.patch`, `webkit.patch`
  - `profiles/`: `pi-1.1.0`..`pi-1.4.0`, `runtime-*.hints`
  - `plugins/opensec/`, `keys/`, `.github/workflows/`, `scripts/`, `bench/`
- **Key findings**:
  - microPi (`µPi`, `upi`, v0.1.0) is a fork of Pi `v1.4.0` (`earendil-works/pi`) built on top of `Pi-Bolt` (AOT Bun+WebKit/JSC runtime), currently on branch `pi-bolt` with tag `upi-v0.1.0`.
  - `sources.json` pins patched `oven-sh/WebKit` (`patches/webkit.patch`), `oven-sh/bun` (`patches/bun.patch`), and upstream Pi `v1.4.0`.
  - Custom microPi features in `packages/coding-agent` and `packages/docsearch`: background checks (`core/checks/` + `native/libclang`), background jobs & `wait` tool (`core/background-jobs.ts`, `core/tools/wait.ts`, `job-hub.ts`), SIMD/cached `grep`/`find` (`core/tools/native-search.ts`, `fff-search.ts`, `native/fff`), `docsearch` Rust N-API addon (`packages/docsearch` compiled via `--builtins`), `compaction-ahead.ts`, `project-snapshot.ts`, `remote/` SSH workspace, `cut-off-response.ts`, `argument-aliases.ts`, `upi*.ts` self-update/compile/doctor.
- **Unexplored areas**:
  - Target `PiG` repository (`https://github.com/MichaelKinsy/PiG`) was not fetched/inspected in this Step 0 survey (scoped to microPi).

## Key Decisions Made
- Completed Step 0 survey and documented findings in `handoff.md`.

## Artifact Index
- `/home/antigravity/Code000-211/microPi/.agents/teamwork/teamwork_preview_explorer_survey_1/DISPATCH.md` — Dispatch log
- `/home/antigravity/Code000-211/microPi/.agents/teamwork/teamwork_preview_explorer_survey_1/BRIEFING.md` — Persistent working memory
- `/home/antigravity/Code000-211/microPi/.agents/teamwork/teamwork_preview_explorer_survey_1/handoff.md` — Step 0 Survey Handoff Report
