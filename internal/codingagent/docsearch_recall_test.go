package codingagent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/docsearch"
	"github.com/MichaelKinsy/PiG/tui"
)

// µPi: the docsearch extension's before_agent_start adds the recall as a displayed custom message, and its renderer
// shows the places on one line (packages/docsearch/extension/index.ts).
func TestDocsearchRecallMessageForATypedPrompt(t *testing.T) {
	t.Setenv("MICROPI_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("DOCSEARCH_CACHE_DIR", t.TempDir())
	t.Setenv("PI_DOCSEARCH", "")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "guide.md"), []byte("# Deploy guide\n\nDeploy the staging cluster with blue green rollout and canary analysis.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := docsearch.OpenProject(dir)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p.Engine.WaitReady(ctx)

	prompt := "deploy the staging cluster with blue green rollout and canary analysis"
	existing := []extension.CustomMessageRef{{CustomType: "other", Content: "x"}}
	messages := DocsearchRecallMessages(context.Background(), dir, extension.UserPromptTrigger(prompt, nil), existing)
	if len(messages) != 2 || messages[0].CustomType != "other" {
		t.Fatalf("messages %+v", messages)
	}
	recall := messages[1]
	content, _ := recall.Content.(string)
	if recall.CustomType != "docsearch-recall" || !recall.Display || !strings.HasPrefix(content, "<doc_search_auto>\n") {
		t.Fatalf("recall %+v", recall)
	}
	encoded, _ := json.Marshal(recall.Details)
	if !strings.HasPrefix(string(encoded), `{"places":["guide.md:`) {
		t.Fatalf("details %s", encoded)
	}
	if got := DocsearchRecallMessages(context.Background(), dir, extension.CustomMessageTrigger(extension.CustomMessage{CustomType: "x"}), nil); len(got) != 0 {
		t.Fatalf("recall for a custom-message run: %+v", got)
	}

	var restored any
	_ = json.Unmarshal(encoded, &restored)
	for _, details := range []any{recall.Details, restored} {
		lines := docsearchRecallRenderer(details)(&tui.CustomMessage{}, tui.MessageRenderOptions{}).Render(80)
		if len(lines) != 1 || !strings.Contains(lines[0], "docs ") || !strings.Contains(lines[0], "guide.md:") {
			t.Fatalf("rendered %q", lines)
		}
	}
}

func TestDocsearchDefinitionHintOnlyForBash(t *testing.T) {
	if _, ok := DocsearchDefinitionHint(context.Background(), t.TempDir(), "read", json.RawMessage(`{"command":"rg Foo"}`), nil); ok {
		t.Fatal("hint for a tool that is not bash")
	}
}
