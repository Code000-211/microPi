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

func TestWriteToolWithTrackerAndAliases(t *testing.T) {
	dir := t.TempDir()
	tr := filetracking.NewTracker()
	wt := &WriteTool{CWD: dir, Queue: NewFileMutationQueue(), Tracker: tr}

	// Writing a new file succeeds via file_path alias.
	rawNew := json.RawMessage(`{"file_path":"new.txt","content":"hello\n"}`)
	prepared, err := wt.PrepareArguments(rawNew)
	if err != nil {
		t.Fatal(err)
	}
	res, err := wt.Execute(context.Background(), "", prepared, nil)
	if err != nil || res.IsError {
		t.Fatalf("write new failed: err=%v res=%s", err, res.Text())
	}

	// Writing over an unread existing file refuses and returns existing content (up to 30k chars) and marks it read.
	existingPath := filepath.Join(dir, "existing.txt")
	bigExisting := strings.Repeat("a", 30050)
	if err := os.WriteFile(existingPath, []byte(bigExisting), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(writeParams{Path: "existing.txt", Content: "replaced\n"})
	res, err = wt.Execute(context.Background(), "", args, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected refusal when overwriting unread existing file")
	}
	txt := res.Text()
	if !strings.Contains(txt, "Not written:") || !strings.Contains(txt, "[cut at 30000 of 30050 characters") {
		t.Fatalf("unexpected refusal text: %q", txt[max(0, len(txt)-200):])
	}
	if !tr.StateTracker().HasBeenRead(existingPath) {
		t.Fatal("expected refused file to be marked as read")
	}

	// Second write now succeeds because the file counts as read.
	res, err = wt.Execute(context.Background(), "", args, nil)
	if err != nil || res.IsError {
		t.Fatalf("second write failed: err=%v res=%s", err, res.Text())
	}
	got, _ := os.ReadFile(existingPath)
	if string(got) != "replaced\n" {
		t.Fatalf("got %q, want %q", got, "replaced\n")
	}

	// External modification after read -> refused as stale.
	if err := os.WriteFile(existingPath, []byte("external edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = wt.Execute(context.Background(), "", args, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Text(), "modified since it was last read") {
		t.Fatalf("expected stale file error, got IsError=%v %q", res.IsError, res.Text())
	}
}
