# Milestone M1 Briefing

## Objective
Port the foundation of `microPi` (`µPi`, CLI `upi`) to `PiG` (`https://github.com/MichaelKinsy/PiG`, `0.8.0+1.6.0`, Go 1.27.4, `CGO_ENABLED=0` static binary) and implement `cmd/upi/` with `upi` branding, `~/.micropi` / `MICROPI_*` env defaults, `upi version`, `upi doctor`, and `upi update`.

## Key Constraints
- Read `/home/antigravity/Code000-211/microPi/.agents/teamwork/ORIGINAL_REQUEST.md` and `/home/antigravity/Code000-211/microPi/AGENTS.md` first.
- Do NOT delete `packages/`, `scripts/`, `docs/`, or overwrite microPi's `README.md`.
- Never run `git reset --hard`, `git checkout .`, `git clean -fd`, `git stash`, `git add -A`, `git add .`.
- Follow `AGENTS.md` Go/repo rules (erasable TS if any, no emojis in code/commits, technical prose).

## Current Status
- Iteration 1: Starting Exploration phase (3 Explorers).
