# BRIEFING — 2026-11-26T14:13:00Z

## Mission
Map all PiBolt/pibolt/pi-bolt references, PiBolt-specific features/patches/tooling, build/typecheck/lint/test infrastructure, and constraints for porting away from PiBolt in `/home/antigravity/Code000-211/microPi`.

## 🔒 My Identity
- Archetype: Explorer
- Roles: Investigator, Synthesizer
- Working directory: /home/antigravity/Code000-211/microPi
- State folder: /home/antigravity/Code000-211/microPi/.agents/teamwork/teamwork_preview_explorer_survey_2
- Original parent: 98032d689a421942cea73f1ad3409e2f
- Milestone: Step 0 Survey — PiBolt Integration & Build/Test Infra

## 🔒 Key Constraints
- Read-only investigation — do NOT implement or modify any source/project files
- Write output files ONLY in `/home/antigravity/Code000-211/microPi/.agents/teamwork/teamwork_preview_explorer_survey_2`
- Do NOT run `npm run build` or `npm test`, and do NOT run full vitest directly (per `AGENTS.md`)

## Current Parent
- Conversation ID: 98032d689a421942cea73f1ad3409e2f
- Updated: 2026-11-26T14:13:00Z

## Investigation State
- **Explored paths**: `sources.json`, `package.json`, `VERSION`, `test.sh`, `pi-test.sh`, `vitest.base.ts`, `biome.json`, `tsconfig.json`, `tsconfig.base.json`, `.github/workflows/{ci,runtime,release}.yml`, `npm/{package.json,install.cjs}`, `scripts/{build-pi.sh,build-runtime.sh,fetch-runtime.sh,prepare-pi.sh,make-kit.mjs,build-docsearch.mjs,check-patches.sh,runtime-stamp.sh,check-entry-graphs.mjs,check-runtime-deps.mjs}`, `scripts/lib/{common.sh,darwin-launcher.c}`, `packages/coding-agent/{package.json,src/upi.ts,src/upi-compile.ts,src/config.ts,src/bun/*,src/core/*,src/cli/*,src/extensions/*,native/*}`, `packages/tui/package.json`, `packages/docsearch`, `patches/{bun.patch,webkit.patch}`, `docs/{BUILDING.md,PLUGINS.md}`, `README.md`.
- **Key findings**: `microPi` (µPi) is a fork of Pi (`earendil-works/pi` v1.4.0) built on Pi-Bolt (`opensec-git/Pi-Bolt`) AOT-compiled Bun/WebKit (`patches/bun.patch`, `patches/webkit.patch`). `PIBOLT` compile-time define gates µPi-specific runtime behavior, `upi compile`, `upi doctor`, `piBolt.packages` settings overlay, native `fff`/`libclang`/`docsearch` wiring, and release/installer/CI pipelines.
- **Unexplored areas**: Complete; handoff ready.

## Key Decisions Made
- Categorized all PiBolt references into: (A) Engine/AOT Runtime & Patches, (B) Build/Packaging/Release/CI & Installers, (C) `packages/coding-agent` Runtime (`PIBOLT` define & `upi*` modules), (D) Native Addons & Built-in Extensions, (E) Build/Lint/Typecheck/Test Infrastructure.

## Artifact Index
- `/home/antigravity/Code000-211/microPi/.agents/teamwork/teamwork_preview_explorer_survey_2/DISPATCH.md` — Dispatch message log
- `/home/antigravity/Code000-211/microPi/.agents/teamwork/teamwork_preview_explorer_survey_2/BRIEFING.md` — Persistent working memory
- `/home/antigravity/Code000-211/microPi/.agents/teamwork/teamwork_preview_explorer_survey_2/handoff.md` — Comprehensive Step 0 Survey report
