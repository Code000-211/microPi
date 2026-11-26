package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/docsearch"
)

// ProjectSearchToolOptions configures ProjectSearchTool.
type ProjectSearchToolOptions struct{}

// ProjectSearchTool implements the `project_search` built-in tool backed by internal/docsearch.
type ProjectSearchTool struct {
	CWD string
}

type projectSearchParams struct {
	Query string `json:"query"`
	K     int    `json:"k,omitempty"`
	Path  string `json:"path,omitempty"`
}

const projectSearchParamsRaw = `{"type":"object","required":["query"],"properties":{
	"query":{"type":"string","description":"Search query (identifiers, function/type names, or keywords)"},
	"k":{"type":"integer","minimum":1,"maximum":20,"description":"Maximum number of sections to return (default: 6)"},
	"path":{"type":"string","description":"Optional path prefix filter relative to project root"}
}}`

var projectSearchOutputSchema = json.RawMessage(`{"type":"object","properties":{
	"hits":{"type":"array","items":{"type":"object","properties":{
		"path":{"type":"string"},
		"lineStart":{"type":"integer"},
		"lineEnd":{"type":"integer"},
		"heading":{"type":"string"},
		"text":{"type":"string"},
		"truncated":{"type":"boolean"}
	}}}
}}`)

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
		Name:        "project_search",
		Description: "Search this project's code and documentation and the configured doc folders. Returns the best-matching sections with path and line range: a doc section under its headings, or a whole function, class or struct under its name. A query naming a function or type returns its definition first. Ranks by terms and by the names the query mentions (identifiers, file names, flags, products). ~1 ms.",
		PromptGuidelines: []string{
			"Use project_search before grep/find/read when looking up function/type definitions or documentation sections.",
			"Include exact identifiers, file names, flags, or type names in the query.",
			"Use grep for exact-text or call-site searches.",
			"Batch independent project_search calls in parallel.",
		},
	}, projectSearchParamsRaw, borrow)
}

func (t *ProjectSearchTool) OutputSchema() json.RawMessage {
	return projectSearchOutputSchema
}

func (t *ProjectSearchTool) Execute(ctx context.Context, _ string, rawParams json.RawMessage, _ agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	var p projectSearchParams
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
	k := p.K
	if k <= 0 {
		k = docsearch.LoadSettings().Results
		if k <= 0 || k > 20 {
			k = 6
		}
	}
	if k > 20 {
		k = 20
	}
	eng := docsearch.SharedEngine(cwd)
	res, ready := eng.Search(ctx, p.Query, docsearch.SearchOpts{
		K:          k,
		PathPrefix: p.Path,
	}, 5*time.Second)
	if !ready {
		return agent.AgentToolResult{
			Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "project_search: index is still building; try again shortly."}},
		}, nil
	}
	text := docsearch.FormatToolResult(p.Query, res)
	structured, _ := json.Marshal(map[string]any{"hits": res.Hits})
	return agent.AgentToolResult{
		Content:           []ai.ToolResultMessageContent{ai.TextContent{Text: text}},
		StructuredContent: structured,
		Details: map[string]any{
			"count":    len(res.Hits),
			"micros":   res.Micros,
			"entities": res.Entities,
		},
	}, nil
}
