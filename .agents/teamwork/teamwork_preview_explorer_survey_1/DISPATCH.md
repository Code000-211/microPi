## 2026-11-26T14:11:21Z

Read `/home/antigravity/Code000-211/microPi/.agents/teamwork/ORIGINAL_REQUEST.md` first.
Also read `/home/antigravity/Code000-211/microPi/AGENTS.md` to understand project conventions.

Your state folder is `/home/antigravity/Code000-211/microPi/.agents/teamwork/teamwork_preview_explorer_survey_1`. Write all your output files (e.g., `handoff.md`, `analysis.md`, `progress.md`) ONLY in that state folder. Do NOT modify any source or project files.

Objective (Step 0 Survey — microPi Architecture & Structure):
Investigate `/home/antigravity/Code000-211/microPi` thoroughly to map:
1. What microPi is, its relationship to upstream pi / PiBolt, `sources.json`, `patches/`, `scripts/`, `packages/`, `profiles/`, `plugins/`, `install.sh`, `install.ps1`, `package.json`, `tsconfig.json`.
2. Recent git log (`git log -n 20 --oneline`) and branches/remotes to understand how microPi was built/ported onto PiBolt.
3. All packages and modules in `packages/` and any custom microPi additions/modifications.
4. Features, constraints, dependencies, and file/line locations.

Write a comprehensive `handoff.md` in `/home/antigravity/Code000-211/microPi/.agents/teamwork/teamwork_preview_explorer_survey_1/handoff.md` and report back via `send_message` with the path and a concise summary.
