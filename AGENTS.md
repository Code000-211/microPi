# Development Rules

µPi is [PiG](https://github.com/MichaelKinsy/PiG) (Pi in Go) with µPi's features built into the harness. One static Go
binary, `upi` (`./cmd/upi`), module `github.com/MichaelKinsy/PiG`, default branch `PiG`. The `pi-bolt` branch holds µPi
0.1 (TypeScript on Pi-Bolt); it is not developed here.

## Conversational Style

- Keep answers short and concise
- No emojis in commits, issues, PR comments, or code
- No fluff or cheerful filler text (e.g., "Thanks @user" not "Thanks so much @user!")
- Technical prose only, be direct
- Use concise, clear, simple language. Define unavoidable jargon before using it.
- Explain non-trivial designs and problems as: problem, concrete example or short trace, then solution. State why the solution is necessary and distinguish it from optional complexity.
- Prefer concrete behavior and small illustrations over abstract summaries, dense terminology, or unexplained lists of changes.
- When the user asks a question, answer it first before making edits or running implementation commands.
- When responding to user feedback or an analysis, explicitly say whether you agree or disagree before saying what you changed.

## Where µPi lives

Most of the tree is PiG. µPi's own code is in these places, and a µPi change in PiG's code carries a `µPi:` comment:

| Feature | Code |
|---|---|
| Background checks after edits | `internal/codingagent/checks` |
| Background jobs, `wait`, job hub | `internal/codingagent/jobs`, `internal/codingagent/job_hub.go`, `internal/codingagent/tools/wait.go` |
| docsearch (`project_search`, auto-recall) | `internal/docsearch`, `internal/codingagent/docsearch_recall.go` |
| Native `grep`/`find`, search caches | `internal/codingagent/tools/native_search.go`, `grep.go`, `find.go` |
| Early tool starts, read-only bash | `agent/early_tools.go`, `internal/codingagent/tools/early_start.go`, `bash_readonly.go` |
| bash file writes, self-kill guard | `internal/codingagent/tools/bash_file_writes.go`, `bash_self_kill.go` |
| Compaction-ahead, tool-result pruning | `internal/codingagent/compaction` |
| Wiring into the session | `coding/session*.go`, `coding/session_tool_registry.go` |
| Release, installers, self-update | `.github/workflows/release.yml`, `install.sh`, `install.ps1`, `internal/codingagent/micropi_update.go` |

The µPi TypeScript sources these were ported from are on the `pi-bolt` branch; a ported function names its origin in a comment.

## Code Quality

- Read files in full before wide-ranging changes, before editing files you have not fully inspected, and when asked to investigate or audit. Do not rely on search snippets for broad changes.
- Go: `gofmt`, as the surrounding code. A comment says why, not what. Keep exported names documented.
- Inline single-line helpers that have only one call site.
- Check the module cache (`go doc`, `$(go env GOMODCACHE)`) for external API types; don't guess.
- No new dependency without need; `go.mod` pins exact versions. Run `go mod tidy` only for a change that needs it.
- Keep PiG's behavior where µPi does not change it: PiG matches Pi, and its parity tests check that. A deliberate µPi difference is marked `µPi:` and has a test.
- Always ask before removing functionality or code that appears intentional.
- Do not preserve backward compatibility unless the user asks for it.
- Never hardcode key checks (e.g. `matchesKey(keyData, "ctrl+x")`). Add defaults to the keybinding tables so they stay configurable.
- Paths, folders and variables shown to users are µPi's: `upi`, `~/.micropi`, `MICROPI_*` (`internal/codingagent/paths.go`).

## Lint policy

`golangci-lint` (`.golangci.yml`) is part of `make check`. Fix valid findings at source. Suppress only audited false-positive classes, with the narrowest rule, path or text selector, preferably an inline `//nolint:<linter> // rationale`. Do not add broad exclusions to make CI green.

## Commands

- After code changes (not docs): `gofmt -l <changed dirs>` must print nothing, `go vet` the changed packages, and `golangci-lint run <changed packages>/...`. Fix every finding before committing.
- Build: `CGO_ENABLED=0 go build -o upi ./cmd/upi`.
- Test the packages you changed: `go test -count=1 ./internal/docsearch/`, or one test with `-run TestName`. Add `-race` for concurrent code.
- Do not run `go test ./...` over the whole tree without being asked: it takes long, and some parity tests need Pi's npm package and Node (`make parity-deps`) and fail without them. Compare failures with the same tests on the previous commit before calling one new.
- End to end: `cd tests/e2e && GOWORK=off go test .` (builds and drives `upi`).
- `make check` is the full pre-commit gate (parity included); run it when asked or before a release.
- If you create or modify a test file, run it and iterate on test or implementation until it passes.
- A test for a µPi fix says what it guards in its comment; for a GitHub issue, add the issue number next to the test.
- For ad-hoc scripts, write them to a temp file (e.g. `/tmp`), run, edit if needed, remove when done. Don't embed multi-line scripts in `bash` commands.
- Never commit unless the user asks.

## Git

Multiple sessions may be running in this cwd at the same time, each modifying different files. Git operations that touch unstaged, staged, or untracked files outside your own changes will stomp on other sessions' work. Follow these rules:

Committing:

- Only commit files YOU changed in THIS session.
- Stage explicit paths (`git add <path1> <path2>`); never `git add -A` / `git add .`.
- Before committing, run `git status` and verify you are only staging your files.
- Message format: `{feat,fix,perf,docs,chore,test}[(coding-agent,agent,ai,tui)]: <commit message>` (optionally multiple lines). Message is informative and concise.

Never run (destroys other agents' work or bypasses checks):

- `git reset --hard`, `git checkout .`, `git clean -fd`, `git stash`, `git add -A`, `git add .`, `git commit --no-verify`.

If rebase conflicts occur:

- Resolve conflicts only in files you modified.
- If a conflict is in a file you did not modify, abort and ask the user.
- Never force push.

## Issues and PRs

Repository: `Code000-211/microPi`. See `CONTRIBUTING.md`.

When reviewing PRs:

- Do not move the worktree to the PR branch unless the user explicitly asks.
- Inspect PR metadata, commits and patches without changing branches; fetch PR files into temporary files or use `git show <ref>:<path>`.

When posting issue/PR comments:

- Write the comment to a temp file and post it from there (never multi-line markdown inline).
- Keep comments concise, technical, in the user's tone.
- End every AI-posted comment with the AI-generated disclaimer line specified by the originating prompt.

When closing issues via commit:

- Include `fixes #<number>` or `closes #<number>` in the message so merging auto-closes the issue. For multiple issues, repeat the keyword per issue (`closes #1, closes #2`); a shared keyword (`closes #1, #2`) only closes the first.

## Changelog

Do not edit `CHANGELOG.md` files; release notes are written at release time.

## Releasing

A release is the version in `VERSION` (and `MicroPiVersion` in `internal/coding/pigversion/pigversion.go`, which must match). Pushing a change of `VERSION` to `PiG` runs `.github/workflows/release.yml`: it builds `upi` for Linux x64/ARM64, macOS ARM64/x64 and Windows x64, runs the Linux x64 and macOS ARM64 builds, and creates a draft release `upi-vX.Y.Z` with the executables, `SHA256SUMS` and the installers. A maintainer publishes the draft; nothing is published automatically. Do not bump the version, tag or publish without the user's explicit request.

## Testing upi's interactive mode with tmux

For testing the interactive mode, load and follow [.pi/skills/interactive-testing.md](.pi/skills/interactive-testing.md), with `upi` (a build of `./cmd/upi`) in place of the command it names.

## User Override

If the user's instructions conflict with any rule in this document, ask for explicit confirmation before overriding. Only then execute their instructions.
