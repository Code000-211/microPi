## 2026-11-26T14:11:22Z
Read `/home/antigravity/Code000-211/microPi/.agents/teamwork/ORIGINAL_REQUEST.md` first.
Also read `/home/antigravity/Code000-211/microPi/AGENTS.md` to understand project conventions.

Your state folder is `/home/antigravity/Code000-211/microPi/.agents/teamwork/teamwork_preview_explorer_survey_2`. Write all your output files (e.g., `handoff.md`, `analysis.md`, `progress.md`) ONLY in that state folder. Do NOT modify any source or project files.

Objective (Step 0 Survey — PiBolt Integration & Build/Test Infra):
Investigate `/home/antigravity/Code000-211/microPi` to map:
1. Every reference to `PiBolt` / `pibolt` / `pi-bolt` (case-insensitive) across the codebase, configs, scripts, docs, `sources.json`, `package.json`, `README.md`, `install.sh`, `install.ps1`, `nix/`, `npm/`, `.github/`, etc.
2. What PiBolt-specific features, patches, dependencies, runtime/build tools (e.g. Bolt, Bun, Node, native addons, etc.) exist in `microPi` and how they are wired.
3. The build, typecheck, lint, and test infrastructure (`npm run check`, `./test.sh`, `pi-test.sh`, `vitest.base.ts`, `biome.json`, `package.json` scripts). Do NOT run `npm run build` or `npm test`, and do NOT run full vitest directly (per `AGENTS.md`). You may inspect scripts and optionally check `git status` or `node -v`.
4. Dependencies and constraints for porting away from PiBolt.

Write a comprehensive `handoff.md` in `/home/antigravity/Code000-211/microPi/.agents/teamwork/teamwork_preview_explorer_survey_2/handoff.md` and report back via `send_message` with the path and a concise summary.
