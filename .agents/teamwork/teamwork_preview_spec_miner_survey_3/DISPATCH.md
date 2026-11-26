## 2026-11-26T14:11:22Z

Read `/home/antigravity/Code000-211/microPi/.agents/teamwork/ORIGINAL_REQUEST.md` first.

Your state folder is `/home/antigravity/Code000-211/microPi/.agents/teamwork/teamwork_preview_spec_miner_survey_3`. Write all your output files (e.g., `handoff.md`, `spec.md`, `progress.md`) ONLY in that state folder. Do NOT modify any files in `/home/antigravity/Code000-211/microPi` (outside your state folder).

Objective (Step 0 Survey — PiG Specification & Architecture):
Investigate `https://github.com/MichaelKinsy/PiG` thoroughly using `gh` CLI (e.g., `gh repo view MichaelKinsy/PiG`, `gh api`, or cloning into a temporary `/tmp/pig_inspect` directory if needed — clean up `/tmp` when done) or `read_url_content`.
Map:
1. What PiG is (language, runtime, architecture, packages/modules, entry points, CLI, config, build/test system).
2. How PiG relates to `pi` / `pi-mono` / `PiBolt` / `microPi` (shared lineage, differences in APIs, package names, directory layout, dependencies, build tooling).
3. All key features, constraints, dependencies, and interfaces of PiG that matter for porting `microPi` to `PiG` instead of `PiBolt`.
4. Exact file paths, package names, versions, and APIs in `MichaelKinsy/PiG`.

Write a comprehensive `handoff.md` in `/home/antigravity/Code000-211/microPi/.agents/teamwork/teamwork_preview_spec_miner_survey_3/handoff.md` and report back via `send_message` with the path and a concise summary.
