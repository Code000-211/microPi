package coding

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

// µPi: a file an extension's tool writes is found by the next find at once, not after the search cache's verify window; a built-in read-only
// tool leaves the cache trusted.
func TestExtensionToolEndMakesTheSearchCacheRecheck(t *testing.T) {
	session, err := sessionFromServices(t, nil, CreateAgentSessionFromServicesOptions{})
	if err != nil {
		t.Fatal(err)
	}
	tools.ClearNativeSearchCache()
	dir := t.TempDir()
	find := &tools.FindTool{CWD: dir}
	run := func() string {
		res, err := find.Execute(t.Context(), "id", []byte(`{"pattern":"*.txt"}`), nil)
		if err != nil {
			t.Fatal(err)
		}
		return res.Text()
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := run(); got != "a.txt" {
		t.Fatalf("first find = %q", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	session.publishAgentEvent(agent.ToolExecutionEndEvent{ToolName: "read"})
	if got := run(); got != "a.txt" {
		t.Fatalf("after read the cache is trusted within its window, find = %q", got)
	}
	session.publishAgentEvent(agent.ToolExecutionEndEvent{ToolName: "extension_writer"})
	if got := run(); got != "a.txt\nb.txt" {
		t.Fatalf("after an extension tool find = %q, want both files", got)
	}
}
