# BRIEFING — 2026-11-26T14:11:00Z

## Mission
Port microPi (`/home/antigravity/Code000-211/microPi`) from PiBolt to PiG (`https://github.com/MichaelKinsy/PiG`), thoroughly plan, implement, and verify/test the port.

## 🔒 My Identity
- Archetype: teamwork_preview_orchestrator
- Roles: orchestrator, user_liaison, human_reporter, successor
- Level: Project Orchestrator (top-level)
- Working directory: /home/antigravity/Code000-211/microPi
- State folder: /home/antigravity/Code000-211/microPi/.agents/teamwork/teamwork_preview_orchestrator_1
- Original parent: Sentinel
- Original parent conversation ID: db96dee7acdb9a9a161d6904047d1fba

## 🔒 My Workflow
- **Pattern**: Project Pattern (Dual Track: Implementation Track + E2E Testing Track)
- **Scope document**: /home/antigravity/Code000-211/microPi/PROJECT.md
1. **Survey**: Spawn 3 Explorers/Spec Miners in parallel to map microPi, PiBolt usage, and PiG (`https://github.com/MichaelKinsy/PiG`). Merge into `PROJECT.md § Feature Inventory`.
2. **Assess & Decompose or Iterate**:
   - If scope fits single cycle → 2B (Explorer → Worker → Reviewer/Challenger/Auditor gate).
   - Otherwise → 2A: Decompose into 3–7 milestones in `PROJECT.md`, spawn sub-orchestrators per milestone + E2E Testing Orchestrator in parallel.
3. **On failure**: Retry → Replace → Skip (never Auditor) → Redistribute → Redesign.
4. **Succession**: Self-succeed at 20 spawns (when all non-sub-orchestrator subagents are complete).
- **Work items**:
  1. Step 0 Survey [done]
  2. M1: PiG Base Integration & `upi` CLI [done]
  3. M2: Search & Code Intelligence [in-progress]
  4. M3: File Tracking, Edit Ergonomics & Checks [done]
  5. M4: Bash Execution, Background Jobs & Job Hub [in-progress]
  6. M5: Context, Compaction, `/remote`, Melious & Prompts [pending]
  7. M6: Pi-Bolt Retirement, Cleanup & CI/Installers [pending]
  8. M7: Final Milestone (E2E Tiers 1-4 + Tier 5 Hardening) [pending]
  9. E2E Testing Track [done]
- **Current phase**: 2A (Decompose & Delegate)
- **Current focus**: Monitoring M2 and M4

## 🔒 Key Constraints
- Follow `/home/antigravity/Code000-211/microPi/AGENTS.md` strictly (erasable TS, no `any`, no inline imports, `npm run check`, `./test.sh` instead of full vitest, etc.).
- Read `/home/antigravity/Code000-211/microPi/.agents/teamwork/ORIGINAL_REQUEST.md` in every subagent.
- Never reuse a subagent after it has delivered its handoff — always spawn fresh.
- Forensic Auditor INTEGRITY VIOLATION is a binary veto.
- Report victory to `db96dee7acdb9a9a161d6904047d1fba` via `send_message` when all milestones are complete and verified.

## Current Parent
- Conversation ID: db96dee7acdb9a9a161d6904047d1fba
- Updated: 2026-11-26T16:07:00Z

## Key Decisions Made
- Starting with Step 0 Survey: 2 Explorers (microPi codebase + PiBolt integration/build/tests) + 1 Spec Miner (`https://github.com/MichaelKinsy/PiG` repo/architecture/APIs).
- PiG is a Go 1.27.4 (`CGO_ENABLED=0`) port of `pi` v1.6.0. Porting microPi to PiG instead of PiBolt means bringing PiG's Go codebase to repo root, porting microPi's product features (docsearch, fff search, file-tracking, checks, background-jobs/wait, compaction-ahead, /remote, melious, prompts) to Go, and retiring Pi-Bolt + TS packages in M6.

## Team Roster
| Agent | Type | Work Item | Status | Conv ID |
|-------|------|-----------|--------|---------|
| survey_1 | teamwork_preview_explorer | Step 0 Survey: microPi Architecture | completed | e68f72dfa251ce10f5f1d2dad9ff8734 |
| survey_2 | teamwork_preview_explorer | Step 0 Survey: PiBolt & Build Infra | completed | a63815ddd9edd7727616b2f68605e979 |
| survey_3 | teamwork_preview_spec_miner | Step 0 Survey: PiG Repository | completed | 4c853b31acc0aaa353c62dffa0440c54 |
| env_features | teamwork_preview_explorer | Env & Feature Parity Map | completed | 418e930c4dc726155ec02e0ecbeb5b15 |
| sub_orch_m1 | teamwork_preview_sub_orchestrator | M1: PiG Base & upi CLI | completed | cee29ff1cc5a615ffde753391bb86912 |
| sub_orch_e2e | teamwork_preview_sub_orchestrator | E2E Testing Track | completed | a68b282c10f468f08ee17e2ae910f9bf |
| sub_orch_m2 | teamwork_preview_sub_orchestrator | M2: Search & Intelligence | in-progress | 09076f45de48a69234b197b6979a121e |
| sub_orch_m3 | teamwork_preview_sub_orchestrator | M3: FileTrack & Checks | completed | 6c02fe6c0126ce5d9c49e450c149ff2f |
| sub_orch_m4 | teamwork_preview_sub_orchestrator | M4: Bash & Jobs Hub | in-progress | db0f2ac0eaf406ab3b36640d17038469 |

## Succession Status
- Succession required: no
- Spawn count: 9 / 20
- Pending subagents: 09076f45de48a69234b197b6979a121e, db0f2ac0eaf406ab3b36640d17038469
- Predecessor: none
- Successor spawned: not yet spawned
- Successor generation: none
- Successor state folder: none

## Active Timers
- Heartbeat cron: 98032d689a421942cea73f1ad3409e2f/task-11
- Safety timer: none
- On succession: kill all timers before spawning successor
- On context truncation: handle timers as step 4 of the State Recovery section of your prompt says

## Artifact Index
- `/home/antigravity/Code000-211/microPi/.agents/teamwork/ORIGINAL_REQUEST.md` — Immutable user request
- `/home/antigravity/Code000-211/microPi/.agents/teamwork/teamwork_preview_orchestrator_1/DISPATCH.md` — Dispatch log
- `/home/antigravity/Code000-211/microPi/.agents/teamwork/teamwork_preview_orchestrator_1/progress.md` — Liveness & progress checkpoint
