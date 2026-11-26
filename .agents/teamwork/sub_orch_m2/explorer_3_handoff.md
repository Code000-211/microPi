# Explorer 3 Handoff — Codebase Investigation for M2 (`project_search` & `/docs`)

## 1. Go Module, Workspace, and Build Status
- **Module**: `github.com/MichaelKinsy/PiG` in `/home/antigravity/Code000-211/microPi/go.mod`
  - `go 1.27.0`, `toolchain go1.27.4`, `godebug winsymlink=0`
- **Workspace**: `/home/antigravity/Code000-211/microPi/go.work`
  - `go 1.27.0`, `toolchain go1.27.4`, `godebug winsymlink=0`
  - `use` modules: `.`, `./extensions/sdk`, `./piglets/pig-bolt`, `./piglets/porter/extensions/pig-porter`
- **Installed Go**: `go version go1.27.4 linux/amd64`
- **Key Dependencies already in `go.mod`**: `modernc.org/sqlite v1.62.0`, `github.com/alecthomas/chroma/v2`, `github.com/yuin/goldmark`, `github.com/stretchr/testify`
- **Build (`CGO_ENABLED=0 go build ./...`)**: **PASS** (exited with code 0 on `go1.27.4 linux/amd64`).

## 2. Existing Search / Docs State (What Exists vs. What Does Not)
- `internal/docsearch`: **Does NOT exist.**
- `internal/codingagent/tools/native_search.go`: **Does NOT exist.**
- `internal/codingagent/tools/docsearch.go`: **Does NOT exist.**
- `internal/pigdocs/`: **Already exists** (embedded documentation bundle `pages.zst` with `List() []string`, `Read(name string) ([]byte, error)`, `DocsDir(configRoot string) string`, `Sync`, `Prepare`, `EnsureSynced`, `RunCommand` for `pig docs [sync|path|list|show <name>]`).
- `coding/cli/docs_command.go` (+ `docs_command_stripped.go` with `//go:build pig_strip_docs`): implements `runDocsCommand` for the `pig docs` CLI subcommand, plus `prepareDocsBundle()`, `writeDocsBundle()`, `holdDocsBundle()`, `releaseDocsLeases()`.
- Built-in `/docs` slash command: **Does NOT exist** in `internal/codingagent/slash_commands.go` (`defaultBuiltins()`).

## 3. `agent.AgentTool` Interface & Tool Registration

### `agent.AgentTool` (`agent/agent.go:429-442`)
```go
type AgentTool interface {
	Name() string
	Label() string
	Schema() ai.ToolSchema
	Execute(ctx context.Context, toolCallID string, params json.RawMessage,
		onUpdate ToolUpdateCallback) (AgentToolResult, error)
	ExecutionMode() ToolExecutionMode
}
```
Optional interfaces in `agent/agent.go`:
- `ArgumentPreparer`: `PrepareArguments(raw json.RawMessage) (json.RawMessage, error)`
- `OutputSchemaProvider`: `OutputSchema() ...`
- `BorrowedSchema() ai.ToolSchema` (used by `GrepTool`, `FindTool`, etc. in `internal/codingagent/tools/`)

### Existing `tools/` Patterns (`internal/codingagent/tools/`)
- `grep.go`: `GrepTool` (`Name() == "grep"`, `ExecutionMode() == agent.ToolModeParallel`), uses `toolSchemaWithParameters(...)` in `schema(borrow bool)`, `toolCWD(ctx, t.CWD)`, `resolvePath(cwd, dir)`.
- `find.go`: `FindTool` (`Name() == "find"`, `ExecutionMode() == agent.ToolModeParallel`), same schema/CWD/path conventions.
- Compile-time interface assertions at bottom of `internal/codingagent/tools/tools.go:508-515`:
  `var _ agent.AgentTool = (*GrepTool)(nil)` etc.

### Minimal Registration Points for `project_search` (and `docsearch` if exposed as a tool)
1. **`internal/codingagent/tools/tools.go`**:
   - `builtinToolNames` (`tools.go:102`): add `"project_search"` (Note: `optInBuiltinToolNames` at `tools.go:108` holds `{"powershell"}` — decide whether `project_search` is opt-in or always in `CreateCodingTools`/`CreateAllTools`; note `internal/codingagent/settings_default_tools.go:10` has `defaultToolNames = []string{"read", "bash", "edit", "write"}` for startup defaults).
   - `builtinToolDescriptions` (`tools.go:110-119`): add description entry.
   - `ToolsOptions` (`tools.go:219-230`): add option field if needed (e.g., `ProjectSearch *ProjectSearchToolOptions`).
   - `CreateProjectSearchTool(cwd string, options *ProjectSearchToolOptions) *ProjectSearchTool`
   - `CreateTool(name, cwd string, options *ToolsOptions)` (`tools.go:324-345`): add `case "project_search":`.
   - `CreateCodingTools` (`tools.go:351-363`) / `CreateAllTools` (`tools.go:369-376`) / `CreateReadOnlyTools` (`tools.go:458-466`) if applicable.
   - `builtinSchemaTools()` (`tools.go:394-399`): add `&ProjectSearchTool{}` so `BuiltinToolSchemas()` and `DefaultToolGuidelines()` include it.
   - Bottom interface check: `var _ agent.AgentTool = (*ProjectSearchTool)(nil)`.
2. **`internal/codingagent/prompts/sections.go`**:
   - `DefaultToolSnippets()` (`sections.go:34-45`): add `"project_search"` summary (used by `tools.BuiltinToolDefinition` in `internal/codingagent/tools/tool_definition.go:76` and `cli_runtime_build.go:671`).
3. **`coding/tool_definitions.go`** (optional convenience wrapper, mirrors `CreateGrepToolDefinition`/`CreateFindToolDefinition`):
   - `CreateProjectSearchToolDefinition(...)` calling `builtinToolDefinition(tools.CreateProjectSearchTool(...))`.
4. **`internal/codingagent/tool_builtin_renderers.go`**:
   - `builtInToolRenderers(name string)` (`tool_builtin_renderers.go:143-162`): add `case "project_search":` if a custom TUI call/result renderer is desired (otherwise falls back to generic tool renderer).
5. **Note on `coding/piglet/internal/genstripids/main.go`**:
   - `genstripids` reads `agenttools.BuiltinToolNames()` and `codingagent.BuiltinSlashCommands()` to generate `internal/pigstrip/ids_generated.go`. If any strip-id freshness test runs in CI, `go run ./coding/piglet/internal/genstripids` may need to be checked/updated if new built-in tool/command names are added.

## 4. Slash Command Registration (`/docs`)
1. **`internal/codingagent/slash_commands.go`**:
   - `defaultBuiltins()` (`slash_commands.go:664-716`): add `BuiltinSlashCommand{Name: "docs", Description: "...", ArgumentHint: "<query>", TakesArguments: true, Handler: docsHandler}`.
   - `SlashRegistry` (`slash_commands.go:425`): `NewSlashRegistry()` seeds from `BuiltinSlashCommands()`, which filters `defaultBuiltins()` via `pigstrip`.
   - Wired automatically into:
     - **Interactive mode**: `internal/codingagent/interactive.go:1988` (`m.slashRegistry = NewSlashRegistry()`) and `internal/codingagent/interactive_commands.go:106` (`buildAutocompleteProvider()` uses `BuiltinSlashCommands()`).
     - **Headless / SDK mode**: `coding/session_ops.go:334` (`Session.DispatchSlash` creates `icodingagent.NewSlashRegistry()` and calls `registry.DispatchHeadless(internalSC, line, nil)`).
   - Handler signature: `func docsHandler(sc *SlashContext) error`, writing output via `sc.Append(markdown)` / `sc.AppendText(text)`.
2. **CLI subcommand `pig docs`**:
   - Already wired in `coding/cli/main.go:270` (`runDocsCommand` in `coding/cli/docs_command.go` -> `pigdocs.RunCommand`).

## 5. Files Owned by M3 (Strictly Avoid Modifying)
Verified present in `internal/codingagent/tools/`:
- `internal/codingagent/tools/read.go`
- `internal/codingagent/tools/edit.go`
- `internal/codingagent/tools/write.go`

Verified **not** present in the repo (no `filetracking/` or `checks/` directories exist yet; M3 will create/own them if needed):
- `filetracking/`
- `checks/`
