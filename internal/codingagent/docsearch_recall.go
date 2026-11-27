package codingagent

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/docsearch"
	"github.com/MichaelKinsy/PiG/tui"
)

// µPi: the docsearch extension's hooks (packages/docsearch/extension/index.ts), which PiG runs natively beside the
// extensions' handlers: auto-recall before a run, the definition hint after a bash search, and the recall's renderer.

func init() {
	docsearch.SetAgentDir(ResolveAgentDir)
}

// docsearchRecallDetails are the recall message's details, in index.ts member order.
type docsearchRecallDetails struct {
	Places []string `json:"places"`
	Micros int64    `json:"micros"`
}

// DocsearchRecallMessages appends the auto-recall message for a typed prompt (index.ts before_agent_start) to the
// messages the before_agent_start handlers returned.
func DocsearchRecallMessages(ctx context.Context, cwd string, trigger extension.BeforeAgentStartTrigger, messages []extension.CustomMessageRef) []extension.CustomMessageRef {
	if trigger.MessageType != extension.BeforeAgentStartUser && trigger.MessageType != "" {
		return messages
	}
	recall, ok := docsearch.AutoRecall(ctx, cwd, trigger.Prompt)
	if !ok {
		return messages
	}
	return append(messages, extension.CustomMessageRef{
		CustomType: docsearch.RecallCustomType,
		Content:    recall.Content,
		Display:    true,
		Details:    docsearchRecallDetails{Places: recall.Places, Micros: recall.Micros},
	})
}

// DocsearchDefinitionHint is index.ts tool_result for bash: the content with the hint where the searched name is
// defined appended, or ok false.
func DocsearchDefinitionHint(ctx context.Context, cwd, toolName string, args json.RawMessage, content []string) (string, bool) {
	if toolName != "bash" {
		return "", false
	}
	var input struct {
		Command any `json:"command"`
	}
	_ = json.Unmarshal(args, &input)
	command, _ := input.Command.(string)
	return docsearch.DefinitionHint(ctx, cwd, command, strings.Join(content, "\n"))
}

// docsearchRecallRenderer draws a recall message as index.ts registerMessageRenderer("docsearch-recall") does: the
// places it added, on one line.
func docsearchRecallRenderer(details any) tui.MessageRenderer {
	var places []string
	switch d := details.(type) {
	case docsearchRecallDetails:
		places = d.Places
	case *docsearchRecallDetails:
		places = d.Places
	case map[string]any:
		if list, ok := d["places"].([]any); ok {
			for _, place := range list {
				if s, ok := place.(string); ok {
					places = append(places, s)
				}
			}
		}
	}
	return func(_ *tui.CustomMessage, _ tui.MessageRenderOptions) tui.Component {
		theme := tui.ActiveTheme()
		return tui.NewPaddedText(theme.Fg("accent", "📄 docs ")+theme.Fg("dim", strings.Join(places, " · ")), 0, 0, nil)
	}
}
