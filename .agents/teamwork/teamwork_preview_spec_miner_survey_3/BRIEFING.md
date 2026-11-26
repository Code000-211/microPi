# BRIEFING — 2026-11-26T14:15:00Z

## Mission
Survey `https://github.com/MichaelKinsy/PiG` thoroughly to map its architecture, packages, APIs, build/test system, lineage, and interfaces relevant to porting `microPi` to `PiG`.

## 🔒 My Identity
- Archetype: Specification Miner
- Roles: specification_miner
- Working directory: /home/antigravity/Code000-211/microPi
- State folder: /home/antigravity/Code000-211/microPi/.agents/teamwork/teamwork_preview_spec_miner_survey_3
- Original parent: 98032d689a421942cea73f1ad3409e2f
- Milestone: Step 0 Survey — PiG Specification & Architecture

## 🔒 Key Constraints
- Write all output files ONLY in `/home/antigravity/Code000-211/microPi/.agents/teamwork/teamwork_preview_spec_miner_survey_3`.
- Do NOT modify any files in `/home/antigravity/Code000-211/microPi` outside the state folder.
- Clean up any temporary clone directory when done.
- Report back via `send_message` with the `handoff.md` path and concise summary.

## Current Parent
- Conversation ID: 98032d689a421942cea73f1ad3409e2f
- Updated: 2026-11-26T14:15:00Z

## Task Summary
- **What to build**: Comprehensive specification survey (`handoff.md`) of `MichaelKinsy/PiG`.
- **Success criteria**: Complete mapping of PiG language, runtime, architecture, packages, entry points, CLI, config, build/test system, lineage vs `pi`/`pi-mono`/`PiBolt`/`microPi`, and exact file paths, package names, versions, and APIs.
- **Interface contracts**: `ORIGINAL_REQUEST.md`
- **Code layout**: Read-only survey.

## Key Decisions Made
- Cloned `MichaelKinsy/PiG` (`--depth 1`) to scratch directory, inspected all core packages, docs, parity maps, and `piglets/pig-bolt`, then cleaned up scratch clone.
- Documented all findings in `handoff.md`.

## Artifact Index
- `/home/antigravity/Code000-211/microPi/.agents/teamwork/teamwork_preview_spec_miner_survey_3/DISPATCH.md` — Dispatch log
- `/home/antigravity/Code000-211/microPi/.agents/teamwork/teamwork_preview_spec_miner_survey_3/BRIEFING.md` — Persistent working memory
- `/home/antigravity/Code000-211/microPi/.agents/teamwork/teamwork_preview_spec_miner_survey_3/handoff.md` — Final Step 0 survey handoff report
