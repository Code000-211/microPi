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

func TestEditToolWithTrackerAndAliases(t *testing.T) {
	ws := t.TempDir()
	sub := filepath.Join(ws, "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "AGENTS.md"), []byte("pkg rule\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(sub, "code.go")
	initial := "package pkg\n\nfunc a() int {\n\treturn 1\n}\n\nfunc b() int {\n\treturn 1\n}\n"
	if err := os.WriteFile(target, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	tr := filetracking.NewTracker()
	et := &EditTool{CWD: ws, Queue: NewFileMutationQueue(), Tracker: tr}

	// Unread file with non-unique oldText ("return 1" appears twice) -> refused as unread.
	args, _ := json.Marshal(editParams{
		Path:  "pkg/code.go",
		Edits: []editEntry{{OldText: "return 1", NewText: "return 2"}},
	})
	res, err := et.Execute(context.Background(), "", args, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Text(), "not been read") {
		t.Fatalf("expected unread refusal, got IsError=%v %q", res.IsError, res.Text())
	}

	// Unread file where oldText matches uniquely once -> allowed, appends snippet + nested AGENTS.md.
	rawAlias := json.RawMessage(`{"file_path":"pkg/code.go","edits":[{"old_string":"func a() int {\n\treturn 1\n}","new_string":"func a() int {\n\treturn 42\n}"}]}`)
	prepared, err := et.PrepareArguments(rawAlias)
	if err != nil {
		t.Fatal(err)
	}
	res, err = et.Execute(context.Background(), "", prepared, nil)
	if err != nil || res.IsError {
		t.Fatalf("unique unread edit failed: err=%v res=%s", err, res.Text())
	}
	txt := res.Text()
	if !strings.Contains(txt, "every oldText matched it exactly once") {
		t.Errorf("missing unread-unique notice: %q", txt)
	}
	if !strings.Contains(txt, "Here is a snippet of the edited file around line") {
		t.Errorf("missing post-edit snippet: %q", txt)
	}
	if !strings.Contains(txt, "pkg rule") {
		t.Errorf("missing nested AGENTS.md reminder: %q", txt)
	}

	// External disk change where oldText still matches uniquely once -> allowed.
	diskChanged := strings.Replace(initial, "return 42", "return 42", 1) + "\n// external comment\n"
	diskChanged = strings.Replace(diskChanged, "func a() int {\n\treturn 1\n}", "func a() int {\n\treturn 42\n}", 1)
	if err := os.WriteFile(target, []byte(diskChanged), 0o644); err != nil {
		t.Fatal(err)
	}
	args2, _ := json.Marshal(editParams{
		Path:  "pkg/code.go",
		Edits: []editEntry{{OldText: "// external comment", NewText: "// updated comment"}},
	})
	res, err = et.Execute(context.Background(), "", args2, nil)
	if err != nil || res.IsError {
		t.Fatalf("disk-changed unique edit failed: err=%v res=%s", err, res.Text())
	}
	if !strings.Contains(res.Text(), "changed on disk since it was last read") {
		t.Errorf("missing disk-changed notice: %q", res.Text())
	}
}

func TestEditToolClosestTextOnNotFound(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "sample.go")
	content := "package main\n\nfunc computeValue(x int) int {\n\treturn x * 2\n}\n"
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	et := &EditTool{CWD: dir, Queue: NewFileMutationQueue()}
	args, _ := json.Marshal(editParams{
		Path:  "sample.go",
		Edits: []editEntry{{OldText: "func computeValue(x int) int {\n\treturn x * 3\n}", NewText: "x"}},
	})
	res, err := et.Execute(context.Background(), "", args, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Text(), "The closest text in the file is at lines 3-5.") {
		t.Fatalf("expected closest text hint, got: %q", res.Text())
	}
}
