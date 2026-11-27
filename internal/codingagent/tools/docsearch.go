package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/docsearch"
)

// ProjectSearchToolOptions configures ProjectSearchTool.
type ProjectSearchToolOptions struct{}

// ProjectSearchTool implements the `project_search` built-in tool backed by internal/docsearch.
//
// µPi: ports microPi packages/docsearch/extension/index.ts (registerTool "project_search").
type ProjectSearchTool struct {
	CWD string
}

func (t *ProjectSearchTool) Name() string  { return "project_search" }
func (t *ProjectSearchTool) Label() string { return "Project search" }
func (t *ProjectSearchTool) ExecutionMode() agent.ToolExecutionMode {
	return agent.ToolModeParallel
}

func (t *ProjectSearchTool) Schema() ai.ToolSchema { return t.schema(false) }

// BorrowedSchema is Schema with the parameters shared, not copied.
func (t *ProjectSearchTool) BorrowedSchema() ai.ToolSchema { return t.schema(true) }

func (t *ProjectSearchTool) schema(borrow bool) ai.ToolSchema {
	return toolSchemaWithParameters(ai.ToolSchema{
		Name:             "project_search",
		Description:      docsearch.ToolDescription,
		PromptGuidelines: slices.Clone(docsearch.ToolPromptGuidelines),
	}, docsearch.ToolParameters, borrow)
}

func (t *ProjectSearchTool) OutputSchema() json.RawMessage {
	return json.RawMessage(docsearch.ToolOutputSchema)
}

func (t *ProjectSearchTool) Execute(ctx context.Context, _ string, rawParams json.RawMessage, _ agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	var p docsearch.ToolParams
	if err := json.Unmarshal(rawParams, &p); err != nil {
		return agent.AgentToolResult{}, fmt.Errorf("project_search: invalid params: %w", err)
	}
	if ctx.Err() != nil {
		return agent.AgentToolResult{
			Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "Operation aborted"}},
			Details: map[string]any{},
			IsError: true,
			Thrown:  true,
		}, nil
	}
	cwd, err := toolCWD(ctx, t.CWD)
	if err != nil {
		return agent.AgentToolResult{}, err
	}
	return docsearch.ExecuteTool(ctx, cwd, p)
}
