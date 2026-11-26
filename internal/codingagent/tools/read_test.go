package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent/filetracking"
)

func TestReadToolWithTrackerAndAliases(t *testing.T) {
	ws := t.TempDir()
	sub := filepath.Join(ws, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "AGENTS.md"), []byte("sub instruction\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	small := filepath.Join(sub, "small.txt")
	if err := os.WriteFile(small, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tr := filetracking.NewTracker()
	rt := &ReadTool{CWD: ws, Tracker: tr}

	raw := json.RawMessage(`{"absolute_path":"` + filepath.ToSlash(small) + `"}`)
	prepared, err := rt.PrepareArguments(raw)
	if err != nil {
		t.Fatal(err)
	}
	res, err := rt.Execute(context.Background(), "", prepared, nil)
	if err != nil || res.IsError {
		t.Fatalf("read failed: err=%v res=%s", err, res.Text())
	}
	if !strings.Contains(res.Text(), "sub instruction") {
		t.Errorf("expected nested instruction reminder in read output: %q", res.Text())
	}
	state, ok := tr.StateTracker().GetTrackedState(small)
	if !ok || !state.ContentComplete {
		t.Fatalf("expected ContentComplete=true for small read, got %+v (ok=%v)", state, ok)
	}

	// Large file (>= 1 MiB) records ContentComplete = false.
	large := filepath.Join(sub, "large.txt")
	largeContent := strings.Repeat("x\n", 600*1024) // > 1 MiB
	if err := os.WriteFile(large, []byte(largeContent), 0o644); err != nil {
		t.Fatal(err)
	}
	argsLarge, _ := json.Marshal(readParams{Path: "sub/large.txt"})
	res, err = rt.Execute(context.Background(), "", argsLarge, nil)
	if err != nil || res.IsError {
		t.Fatalf("large read failed: err=%v res=%s", err, res.Text())
	}
	largeState, ok := tr.StateTracker().GetTrackedState(large)
	if !ok || largeState.ContentComplete {
		t.Fatalf("expected ContentComplete=false for >=1MiB read, got %+v (ok=%v)", largeState, ok)
	}
}

type recordingChecksHook struct {
	onReadPaths []string
}

func (h *recordingChecksHook) OnRead(path string)   { h.onReadPaths = append(h.onReadPaths, path) }
func (h *recordingChecksHook) OnEdited(path string) {}

func TestReadToolWithTrackerStreamedMediumFile(t *testing.T) {
	ws := t.TempDir()
	// ~10 KB (> 4100-byte ImageTypeSniffBytes, < 1 MiB) triggers local streaming in readLocalFile.
	med := filepath.Join(ws, "medium.txt")
	medContent := strings.Repeat("line of text 0123456789\n", 450)
	if err := os.WriteFile(med, []byte(medContent), 0o644); err != nil {
		t.Fatal(err)
	}

	hook := &recordingChecksHook{}
	tr := filetracking.NewTracker()
	tr.SetBackgroundChecks(hook)
	rt := &ReadTool{CWD: ws, Tracker: tr}

	args, _ := json.Marshal(readParams{Path: "medium.txt"})
	res, err := rt.Execute(context.Background(), "", args, nil)
	if err != nil || res.IsError {
		t.Fatalf("medium read failed: err=%v res=%s", err, res.Text())
	}
	if !tr.StateTracker().HasBeenRead(med) {
		t.Fatal("expected HasBeenRead=true for ~10 KB file")
	}
	state, ok := tr.StateTracker().GetTrackedState(med)
	if !ok || !state.ContentComplete {
		t.Fatalf("expected ContentComplete=true for ~10 KB streamed file, got %+v (ok=%v)", state, ok)
	}
	if state.Content != medContent {
		t.Fatalf("expected tracked Content to match full ~10 KB file (len=%d vs %d)", len(state.Content), len(medContent))
	}
	if len(hook.onReadPaths) != 1 || hook.onReadPaths[0] != med {
		t.Fatalf("expected BackgroundChecksHook.OnRead(%q), got %v", med, hook.onReadPaths)
	}

	// Partial read with offset/limit must keep ContentComplete = false.
	partial := filepath.Join(ws, "partial.txt")
	if err := os.WriteFile(partial, []byte(medContent), 0o644); err != nil {
		t.Fatal(err)
	}
	offset, limit := 2.0, 5.0
	partialArgs, _ := json.Marshal(readParams{Path: "partial.txt", Offset: &offset, Limit: &limit})
	res, err = rt.Execute(context.Background(), "", partialArgs, nil)
	if err != nil || res.IsError {
		t.Fatalf("partial read failed: err=%v res=%s", err, res.Text())
	}
	partialState, ok := tr.StateTracker().GetTrackedState(partial)
	if !ok || partialState.ContentComplete {
		t.Fatalf("expected ContentComplete=false for partial offset/limit read, got %+v (ok=%v)", partialState, ok)
	}
}
